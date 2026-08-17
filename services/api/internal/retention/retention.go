// Package retention cancella davvero i log di esecuzione scaduti (R6, SPEC §8:
// 3, 15, 30, 90 giorni per piano).
//
// # Perché esiste
//
// La privacy policy (legal/en/privacy-policy.md §2.2 e §5) dichiara che i log
// delle esecuzioni sono conservati per il periodo del piano **e poi cancellati**.
// R10-bis applica la stessa finestra in lettura e lo dice esplicitamente di sé:
// è metà del lavoro, copre l'intervallo fra due passate di questo pacchetto e
// non lo sostituisce. Nascondere righe che continuano a esistere renderebbe
// inesatto un documento legale pubblicato, che è un problema peggiore di un
// limite non applicato. Questa è l'altra metà.
//
// # Due meccanismi, non uno
//
// La 0006 partiziona `job_executions` per giorno su `scheduled_for` proprio per
// questo, e la divisione è dichiarata nel commento di quella migrazione:
//
//  1. **La retention lunga si chiude con un DROP di partizione.** È istantaneo e
//     non lascia bloat, ed è l'unica cosa praticabile sui piani a risoluzione di
//     un secondo, dove un job da solo produce 86.400 righe al giorno.
//  2. **Le retention corte cadono dentro partizioni ancora vive** — un utente
//     Free a tre giorni convive nella stessa giornata con un utente Agency a
//     novanta — e vanno tolte a righe, a lotti.
//
// La divisione regge perché i piani a retention lunga sono quelli a risoluzione
// alta, cioè quelli il cui volume rende impraticabile la cancellazione riga per
// riga; i piani a retention corta sono fermi a un minuto, dove un DELETE
// periodico è del tutto sostenibile.
//
// # La retention è del piano, non globale
//
// Una partizione giornaliera contiene le esecuzioni di **tutti** gli utenti. Il
// taglio del DROP è quindi `oggi − max(log_retention_days)` fra i piani
// rappresentati: eliminarla prima cancellerebbe i log di un cliente Agency
// perché un cliente Free stava nella stessa giornata. Il taglio della
// cancellazione a righe, invece, è per utente, e coincide con quello che
// [jobs.Plan.RetentionFloor] usa in lettura — le due metà di R10-bis devono
// nascondere e cancellare esattamente le stesse righe, altrimenti l'utente vede
// sparire uno storico che l'API gli stava ancora mostrando.
//
// # Non deve fermare il dispatch
//
// Due cose lo garantiscono, ed entrambe sono state misurate contro PostgreSQL
// vero invece che assunte (vedi i test):
//
//   - **`lock_timeout` sul DROP.** Eliminare una partizione richiede un lock
//     ACCESS EXCLUSIVE sulla tabella padre. Se una transazione di scrittura è
//     aperta, il DROP si mette in coda — e la coda dei lock di PostgreSQL è
//     ordinata, quindi **ogni INSERT successivo si accoda dietro di lui**, anche
//     su partizioni che non c'entrano nulla. Misurato: un inserimento su oggi ha
//     atteso sei secondi dietro un DROP in attesa. Con un `lock_timeout` corto il
//     DROP rinuncia e riprova alla passata dopo; la retention è misurata in
//     giorni, rimandarla di un'ora non costa niente, fermare il motore sì.
//   - **Lotti e pause sulla cancellazione a righe**, con `SKIP LOCKED`: un DELETE
//     unico su milioni di righe tiene una transazione lunga, gonfia il WAL e
//     tiene fermo l'orizzonte di vacuum. Una riga che il motore sta toccando in
//     questo istante non si aspetta: si salta, e la prende la passata dopo.
//
// # Le partizioni future
//
// Lo stesso problema al contrario, ed è la ragione per cui questo pacchetto non
// si occupa solo di cancellare. **Senza una partizione disponibile
// l'inserimento fallisce** (`23514`, «no partition of relation "job_executions"
// found for row»): è deliberato che fallisca invece di finire in una partizione
// di default, ma è deliberato solo finché qualcuno crea le partizioni. La 0006
// ne crea quattordici in avanti al momento della migrazione e poi non le crea
// più nessuno: senza questa passata il motore smette di scrivere due settimane
// dopo il deploy. Per questo [Service.Sweep] crea **prima** e cancella dopo — se
// una passata deve fallire a metà, deve fallire dalla parte in cui il servizio
// resta scrivibile.
//
// Anche la creazione prende ACCESS EXCLUSIVE sul padre, per lo stesso motivo del
// DROP: attaccare una partizione cambia il descrittore della tabella. Vale
// quindi lo stesso `lock_timeout`, e il caso normale non ne ha comunque bisogno
// — `CREATE TABLE IF NOT EXISTS` su una partizione che esiste già è un no-op che
// non tocca il padre. Il lock serve una volta al giorno, per la giornata nuova
// in fondo alla finestra.
package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// I valori predefiniti. Sono tarati su SPEC §2: API, motore e database sulla
// stessa macchina, un VPS, non un cluster.
const (
	// DefaultInterval è la distanza fra due passate.
	//
	// Un'ora è molto più spesso del necessario — la retention si misura in
	// giorni — ed è deliberato: passate frequenti sono passate piccole, e una
	// passata piccola è una che non si nota. L'alternativa, una passata al
	// giorno, concentrerebbe tutto il lavoro in un momento solo, che è
	// esattamente la forma con cui la manutenzione si accorge di esistere.
	DefaultInterval = time.Hour

	// DefaultBatch è quante righe si cancellano per lotto.
	//
	// Cinquemila righe sono una transazione che dura millisecondi e produce un
	// pezzo di WAL modesto. Il numero conta meno del fatto che ce ne sia uno:
	// ciò che va evitato è il DELETE unico da milioni di righe.
	DefaultBatch = 5000

	// DefaultPause è la pausa fra due lotti. Serve a lasciare respiro ad
	// autovacuum e al flush del WAL fra un lotto e l'altro: senza, i lotti
	// tornano a essere un DELETE unico scritto in modo più complicato.
	DefaultPause = 200 * time.Millisecond

	// DefaultLockTimeout è quanto si aspetta un lock prima di rinunciare.
	//
	// Vale soprattutto per il DROP di partizione, per la ragione scritta nella
	// documentazione del package: un DROP in attesa non aspetta da solo, mette
	// in coda dietro di sé ogni scrittura successiva. Due secondi sono
	// abbondanti per prenderlo su un database in quiete e abbastanza pochi da
	// non essere percepiti da chi sta scrivendo.
	DefaultLockTimeout = 2 * time.Second

	// DefaultMaxBatches è il tetto di lotti per passata: un fermo del cancellatore
	// lungo abbastanza da accumulare cinquanta milioni di righe non deve
	// trasformare la prima passata utile in una che non finisce mai. Ciò che
	// avanza lo prende la passata successiva, e il fatto che sia avanzato è
	// scritto in [Stats.Truncated] invece di essere taciuto.
	DefaultMaxBatches = 10_000

	// DefaultDaysAhead è il margine di partizioni future preparate a ogni
	// passata, ed è quello della 0006. Due settimane servono a rendere visibile
	// con largo anticipo un problema che altrimenti si manifesterebbe come un
	// motore che smette di scrivere.
	DefaultDaysAhead = 14

	// daysBehind è quanti giorni indietro si assicura la partizione. Uno basta:
	// serve a coprire il confine di mezzanotte in UTC, non a ricostruire il
	// passato.
	daysBehind = 1
)

// Options sono le dipendenze e le manopole del servizio.
type Options struct {
	// Pool è la connessione a PostgreSQL, la stessa dell'API e del motore
	// (SPEC §2). Obbligatorio.
	Pool *pgxpool.Pool
	// Logger, se assente, è quello di default.
	Logger *slog.Logger

	// Interval è la distanza fra due passate. Zero significa [DefaultInterval].
	Interval time.Duration
	// Batch è quante righe per lotto. Zero significa [DefaultBatch].
	Batch int
	// Pause è la pausa fra due lotti. Zero significa [DefaultPause]. Non c'è modo
	// di chiedere «nessuna pausa», ed è voluto: la pausa è ciò che rende i lotti
	// davvero separati, e un lotto senza pausa è un DELETE unico scritto in modo
	// più complicato.
	Pause time.Duration
	// LockTimeout è l'attesa massima su un lock. Zero significa
	// [DefaultLockTimeout].
	LockTimeout time.Duration
	// MaxBatches è il tetto di lotti per passata. Zero significa
	// [DefaultMaxBatches].
	MaxBatches int
	// DaysAhead è il margine di partizioni future. Zero significa
	// [DefaultDaysAhead].
	DaysAhead int

	// Observer riceve il resoconto di ogni passata (R7). Nil significa «nessuno
	// guarda».
	Observer Observer

	// Now è l'orologio, iniettabile per i test. Nil significa [time.Now].
	//
	// È l'orologio **della retention**: decide il taglio delle partizioni e il
	// confine per utente. Non decide la finestra di partizioni future, che segue
	// il `now()` del database — quelle vanno preparate attorno al momento in cui
	// il motore scrive davvero, non attorno a un istante finto.
	Now func() time.Time
}

// Observer riceve il resoconto delle passate. Serve alle metriche (R7): la
// manutenzione è invisibile finché funziona, e il momento in cui smette di
// funzionare è quello in cui nessuno la sta guardando.
//
// Il metodo è chiamato dal ciclo di [Service.Run] e deve tornare subito.
type Observer interface {
	// Swept è chiamato alla fine di ogni passata, riuscita o no. `err` è
	// l'errore complessivo della passata, nil se è arrivata in fondo.
	Swept(st Stats, err error)
}

// nopObserver è l'osservatore di default: non guarda niente.
type nopObserver struct{}

func (nopObserver) Swept(Stats, error) {}

// Service applica la retention. Va costruito con [New] e acceso con
// [Service.Run].
type Service struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	obs  Observer

	interval    time.Duration
	batch       int
	pause       time.Duration
	lockTimeout time.Duration
	maxBatches  int
	daysAhead   int
	now         func() time.Time
}

// New costruisce il servizio.
func New(opts Options) (*Service, error) {
	if opts.Pool == nil {
		return nil, errors.New("retention: il pool di connessioni è obbligatorio")
	}
	if opts.Pause < 0 {
		return nil, errors.New("retention: la pausa fra i lotti non può essere negativa")
	}
	if opts.Interval < 0 || opts.Batch < 0 || opts.LockTimeout < 0 || opts.MaxBatches < 0 || opts.DaysAhead < 0 {
		return nil, errors.New("retention: nessuna delle manopole ammette valori negativi")
	}

	s := &Service{
		pool:        opts.Pool,
		log:         opts.Logger,
		obs:         opts.Observer,
		interval:    opts.Interval,
		batch:       opts.Batch,
		pause:       opts.Pause,
		lockTimeout: opts.LockTimeout,
		maxBatches:  opts.MaxBatches,
		daysAhead:   opts.DaysAhead,
		now:         opts.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.obs == nil {
		s.obs = nopObserver{}
	}
	if s.interval == 0 {
		s.interval = DefaultInterval
	}
	if s.batch == 0 {
		s.batch = DefaultBatch
	}
	if opts.Pause == 0 {
		s.pause = DefaultPause
	}
	if s.lockTimeout == 0 {
		s.lockTimeout = DefaultLockTimeout
	}
	if s.maxBatches == 0 {
		s.maxBatches = DefaultMaxBatches
	}
	if s.daysAhead == 0 {
		s.daysAhead = DefaultDaysAhead
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Stats è il resoconto di una passata. Serve alle metriche (R7) e a distinguere
// una passata che non ha trovato niente da fare da una che non ha potuto farlo.
type Stats struct {
	// PartitionsEnsured sono le partizioni giornaliere create o confermate.
	PartitionsEnsured int
	// PartitionsDropped sono le partizioni eliminate perché interamente oltre la
	// retention più lunga in circolazione.
	PartitionsDropped int
	// RowsDeleted sono le righe cancellate una a una, quelle dei piani a
	// retention corta dentro partizioni ancora vive.
	RowsDeleted int64
	// Batches sono i lotti eseguiti per cancellarle.
	Batches int

	// DropDeferred dice che il DROP ha rinunciato al lock e riproverà: non è un
	// errore, è la scelta di non fermare il dispatch. Va contato perché una
	// serie ininterrotta di rinunce significa che qualcuno tiene aperta una
	// transazione lunga, e quello sì è un problema.
	DropDeferred bool
	// EnsureDeferred dice la stessa cosa della creazione delle partizioni
	// future, che prende lo stesso lock. È il più serio dei due: rimandare una
	// cancellazione costa spazio, rimandare una creazione abbastanza a lungo
	// costa le scritture del motore.
	EnsureDeferred bool
	// Truncated dice che la passata si è fermata al tetto di lotti, o su un lock
	// occupato, invece che perché non c'era più niente da cancellare: potrebbe
	// aver lasciato righe scadute sul tavolo, e le prende la passata successiva.
	// Sovrastimare è deliberato — «forse ne restano» va detto, «ho finito»
	// dev'essere vero.
	Truncated bool

	// LongestRetention e ShortestRetention sono i due estremi trovati fra i piani
	// rappresentati, in giorni. Stanno qui perché sono ciò che spiega il numero
	// di partizioni eliminate: un solo cliente Agency vivo alza il taglio da tre
	// giorni a novanta per tutti.
	LongestRetention  int
	ShortestRetention int
}

// Sweep esegue una passata completa e restituisce quel che ha fatto.
//
// L'ordine dei tre passi non è di comodo:
//
//  1. **Prepara le partizioni future.** Se qualcosa deve rompersi, deve
//     rompersi lasciando il servizio scrivibile.
//  2. **Elimina le partizioni interamente scadute**, che è il grosso del lavoro
//     al costo minore.
//  3. **Cancella a righe quel che resta**, cioè i piani a retention corta dentro
//     le partizioni ancora vive. È il passo più caro, e viene per ultimo perché
//     il DROP gli ha già tolto di mezzo tutto ciò che poteva.
//
// Un errore in un passo non impedisce gli altri: sono tre lavori indipendenti, e
// far saltare la creazione delle partizioni future perché un DELETE è andato
// storto sarebbe il modo più rapido di trasformare un problema di manutenzione
// in un servizio che non scrive più. Gli errori si accumulano e tornano insieme.
func (s *Service) Sweep(ctx context.Context) (Stats, error) {
	var (
		stats Stats
		errs  []error
	)

	ensured, ensureDeferred, err := s.ensurePartitions(ctx)
	stats.PartitionsEnsured, stats.EnsureDeferred = ensured, ensureDeferred
	if err != nil {
		errs = append(errs, err)
	}

	window, err := s.retentionWindow(ctx)
	if err != nil {
		// Senza i due estremi non c'è nessun taglio da applicare, e inventarne
		// uno cancellerebbe lo storico di qualcuno. Qui si esce.
		return stats, errors.Join(append(errs, err)...)
	}
	stats.LongestRetention, stats.ShortestRetention = window.longest, window.shortest

	dropped, deferred, err := s.dropExpiredPartitions(ctx, window.longest)
	stats.PartitionsDropped, stats.DropDeferred = dropped, deferred
	if err != nil {
		errs = append(errs, err)
	}

	rows, batches, truncated, err := s.deleteExpiredRows(ctx, window.shortest)
	stats.RowsDeleted, stats.Batches, stats.Truncated = rows, batches, truncated
	if err != nil {
		errs = append(errs, err)
	}

	return stats, errors.Join(errs...)
}

// Run esegue una passata subito e poi una a ogni intervallo, finché il contesto
// non si chiude.
//
// Una passata fallita **non** ferma il ciclo. La retention è manutenzione: un
// errore transitorio del database, o un lock che non si è preso, si riassorbe
// alla passata dopo, e un processo che muore perché non è riuscito a cancellare
// dei log sarebbe un rimedio molto peggiore del male. Ciò che l'errore deve
// fare è comparire nei log — che è anche l'unico modo in cui un errore
// *persistente* si distingue da uno transitorio.
func (s *Service) Run(ctx context.Context) error {
	s.log.Info("retention avviata",
		slog.Duration("passata", s.interval),
		slog.Int("lotto", s.batch),
		slog.Int("giorni_di_margine", s.daysAhead))

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		s.sweepAndLog(ctx)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// sweepAndLog esegue una passata e la racconta.
func (s *Service) sweepAndLog(ctx context.Context) {
	started := s.now()
	stats, err := s.Sweep(ctx)
	s.obs.Swept(stats, err)
	if err != nil {
		if ctx.Err() != nil {
			// L'arresto del processo ha interrotto la passata a metà. Non è un
			// guasto: ciò che non è stato cancellato lo prende il riavvio.
			return
		}
		s.log.Error("retention: passata incompleta", slog.Any("error", err))
	}

	attrs := []slog.Attr{
		slog.Int("partizioni_create", stats.PartitionsEnsured),
		slog.Int("partizioni_eliminate", stats.PartitionsDropped),
		slog.Int64("righe_cancellate", stats.RowsDeleted),
		slog.Int("lotti", stats.Batches),
		slog.Int("retention_massima_giorni", stats.LongestRetention),
		slog.Duration("durata", s.now().Sub(started)),
	}
	if stats.DropDeferred {
		// Rimandare è la scelta giusta, ma va detto: una serie di rinunce di
		// fila significa che qualcuno tiene aperta una transazione lunga.
		attrs = append(attrs, slog.Bool("drop_rimandato", true))
	}
	if stats.EnsureDeferred {
		attrs = append(attrs, slog.Bool("creazione_partizioni_rimandata", true))
	}
	if stats.Truncated {
		attrs = append(attrs, slog.Bool("tetto_lotti_raggiunto", true))
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "retention: passata conclusa", attrs...)
}

// ------------------------------------------------------------------ supporto

// isLockTimeout riconosce la rinuncia a un lock (SQLSTATE 55P03,
// `lock_not_available`), che è il modo in cui `lock_timeout` si manifesta.
//
// Va distinta da un errore vero perché è **l'esito voluto**: significa che
// qualcuno stava scrivendo e che la retention gli ha lasciato la precedenza.
func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}

// sleep aspetta rispettando il contesto.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// annotate uniforma il prefisso degli errori del package.
func annotate(what string, err error) error {
	return fmt.Errorf("retention: %s: %w", what, err)
}
