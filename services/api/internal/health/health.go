// Package health risponde a una domanda sola: **il motore sta facendo il suo
// mestiere adesso?** (R7)
//
// # Perché non basta /healthz
//
// Un processo che risponde `200` perché il suo server HTTP funziona sta
// dichiarando di essere in piedi, che è una cosa vera e quasi inutile. Postqron
// promette di eseguire cose all'orario dovuto, e per farlo deve poter **scrivere
// su PostgreSQL** e avere uno scheduler che gira: un processo vivo che non
// riesce a scrivere è malato, e un health check che lo chiama sano è peggio di
// nessun health check — fa credere che vada tutto bene mentre non parte più
// niente.
//
// Le due domande restano due, con due risposte:
//
//   - **«sono in piedi?»** è la liveness, e la risposta è il fatto stesso che una
//     richiesta HTTP sia stata servita. Non tocca il database, non può fallire
//     per colpa di qualcun altro, e serve a chi riavvia il processo quando si
//     inchioda. È `/healthz`, e resta dov'era.
//   - **«sto funzionando?»** è la prontezza, ed è questo pacchetto. Serve a chi
//     toglie traffico a un'istanza malata e, soprattutto, a noi: è il numero da
//     guardare per sapere che il motore ha smesso di dispacciare **prima** che lo
//     dica un cliente.
//
// Confonderle costerebbe in entrambi i versi: un processo che si dichiara morto
// perché il database è in riavvio verrebbe ammazzato e riavviato in un ciclo che
// non risolve niente, e un processo che si dichiara sano mentre non scrive
// resterebbe in produzione a non fare nulla.
//
// # Le sonde, e perché sono queste
//
// Tre, e ognuna è un modo diverso in cui il motore smette di eseguire:
//
//  1. **Il database accetta scritture.** Non `SELECT 1`: un database in recovery
//     risponde benissimo alle letture e rifiuta ogni scrittura, e l'API
//     continuerebbe a servire la dashboard mentre nessuna esecuzione viene
//     registrata.
//  2. **Le partizioni future esistono.** `job_executions` è partizionata per
//     giorno e senza la partizione del giorno l'inserimento fallisce
//     (migrazione 0006, SQLSTATE 23514). È **il** guasto da rendere visibile
//     prima che accada, non dopo: la finestra si consuma silenziosamente al ritmo
//     di un giorno al giorno, e il momento in cui se ne accorge qualcuno è quello
//     in cui il motore ha già smesso di scrivere. Qui è un numero — quanti giorni
//     di margine restano — che scende visibilmente per due settimane prima di
//     arrivare a zero.
//  3. **Lo scheduler batte.** L'ultima passata **riuscita** ha meno di
//     [DefaultStaleAfter]. È la sonda che distingue «il motore non dispaccia da
//     tre minuti», che è un problema nostro, da «un job del cliente fallisce da
//     tre giorni», che è un problema suo: la seconda non ha niente a che fare con
//     la prontezza del servizio.
//
// # Cosa **non** è una sonda, e perché
//
// La coda del dispatch piena non degrada la prontezza. Un'occorrenza che la coda
// rifiuta lascia la propria riga `pending`, e il recupero dello scheduler la
// riprende: è un rallentamento, non una perdita, e il servizio è pronto a
// riceverne altre. Metterla qui significherebbe togliere traffico all'API — che
// non c'entra niente — perché il motore è occupato. Si guarda nelle metriche,
// dove il numero c'è.
//
// # Il costo, che su una macchina sola è il vincolo
//
// API, motore e database stanno sulla stessa VPS (SPEC §2): ogni query di
// osservabilità è tolta alle query dei clienti. Per questo **le sonde girano su
// un ticker, non sulla richiesta**: [Service.Run] le esegue una volta ogni
// [DefaultInterval] e chi chiede la prontezza legge l'ultimo esito con
// [Service.Snapshot], senza toccare il database. Il costo è quindi fisso e
// indipendente dal traffico — una query ogni cinque secondi, che è anche la
// ragione per cui le tre sonde sono **una sola query**: il round trip costa più
// del lavoro che fa.
//
// La contropartita è dichiarata: la risposta può essere vecchia fino a un
// intervallo. Va benissimo per una grandezza che si misura in minuti, e sarebbe
// sbagliato il contrario — un `/readyz` che interroga il database a ogni
// chiamata è un modo di far cadere il servizio spingendo su un endpoint che non
// chiede credenziali.
package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// I valori di partenza.
const (
	// DefaultInterval è ogni quanto le sonde girano.
	//
	// Cinque secondi sono la granularità con cui ha senso rispondere «il motore
	// sta funzionando»: più fitto non aggiunge informazione — le grandezze
	// osservate cambiano in minuti — e moltiplica una query su una macchina che
	// ne ha di meglio da fare.
	DefaultInterval = 5 * time.Second

	// DefaultTimeout è il tetto di una passata di sonde. È una query sul catalogo
	// e una funzione di sistema: se non tornano in due secondi, il fatto che non
	// tornino *è* la risposta.
	DefaultTimeout = 2 * time.Second

	// DefaultStaleAfter è da quanto l'ultima passata riuscita dello scheduler
	// può essere vecchia prima che il motore si dichiari fermo.
	//
	// Lo scheduler passa ogni 250 ms ([scheduler.DefaultInterval]): trenta
	// secondi sono centoventi passate, cioè un margine che nessun intoppo
	// ordinario consuma — una passata lunga, un lock, un riavvio del database —
	// e che un motore davvero fermo supera comunque in mezzo minuto. Più corto
	// trasformerebbe ogni singhiozzo in un allarme, più lungo lascerebbe passare
	// un minuto di silenzio senza dire niente.
	DefaultStaleAfter = 30 * time.Second

	// DefaultPartitionWarning è sotto quanti giorni di margine di partizioni la
	// prontezza si degrada.
	//
	// La retention ne prepara quattordici in avanti a ogni passata oraria
	// ([retention.DefaultDaysAhead]). Scendere sotto tre significa che quella
	// passata non completa da più di undici giorni: c'è ancora tempo per
	// rimediare, e non ce n'è tanto. Zero non è un avviso, è il motore che ha
	// smesso di poter scrivere.
	DefaultPartitionWarning = 3
)

// Status è l'esito di una sonda, o dell'insieme.
type Status string

// I tre esiti. Sono ordinati per gravità: vedi [worst].
const (
	// StatusOK: la sonda ha guardato e non ha trovato niente da dire.
	StatusOK Status = "ok"
	// StatusDegraded: funziona adesso e smetterà di funzionare se nessuno
	// interviene. È lo stato che esiste perché R7 chieda di vedere un guasto
	// *prima* che accada, e **non** toglie il servizio dalla rotazione: un
	// margine di partizioni che si accorcia non è un motivo per smettere di
	// servire.
	StatusDegraded Status = "degraded"
	// StatusDown: il motore non può fare il proprio mestiere adesso.
	StatusDown Status = "down"
)

// Engine è il battito del motore in memoria.
//
// È un'interfaccia dichiarata qui, e non un riferimento a `*scheduler.Engine`,
// per due ragioni. Lo scheduler non tiene la propria storia — la espone a chi
// osserva e va avanti — quindi l'istante dell'ultima passata riuscita ce l'ha
// chi lo sta osservando, non lui. E dichiararla qui è ciò che tiene questo
// pacchetto sotto a internal/metrics invece che accanto: le metriche implementano
// questa, non il contrario.
type Engine interface {
	// LastTick è l'istante dell'ultima passata **riuscita** dello scheduler, e
	// se ce n'è stata una. Una passata fallita non è un battito: vedi
	// [scheduler.Stats.Failed].
	LastTick() (time.Time, bool)
}

// Check è l'esito di una singola sonda.
type Check struct {
	// Name è il nome della sonda, stabile e non tradotto: ci si costruiscono
	// sopra le etichette delle metriche.
	Name string
	// Status è il suo esito.
	Status Status
	// Detail è la frase che spiega l'esito a una persona. È in italiano come il
	// resto della diagnostica del backend, e **non** esce da un endpoint
	// pubblico: vedi la nota sulla riservatezza in [Report].
	Detail string
}

// Report è la risposta alla domanda, con il conto di come ci si è arrivati.
//
// # Cosa se ne può mostrare, e a chi
//
// Lo `Status` complessivo è un fatto pubblico: è ciò che un bilanciatore deve
// poter leggere senza credenziali per togliere traffico a un'istanza malata.
// Il dettaglio no. «Margine di partizioni: 2 giorni», «lo scheduler è fermo da
// 4 minuti» sono informazioni operative che raccontano a chiunque com'è fatto
// il servizio e quando è debole. Chi le espone è [httpapi], e le espone solo a
// chi si autentica.
type Report struct {
	// Status è il peggiore fra quelli delle sonde.
	Status Status
	// At è quando le sonde hanno guardato.
	At time.Time
	// Checks sono le sonde, in ordine stabile.
	Checks []Check

	// PartitionHorizonDays è quanti giorni di margine restano prima che
	// `job_executions` non abbia più una partizione in cui scrivere. Negativo
	// significa che manca già quella di oggi.
	PartitionHorizonDays int
	// SchedulerAge è da quanto lo scheduler non porta a termine una passata.
	SchedulerAge time.Duration
}

// Ready dice se il servizio è pronto a lavorare.
//
// Degradato è pronto, e la scelta è deliberata: un margine di partizioni che si
// accorcia è un problema da risolvere questa settimana, non un motivo per
// smettere di eseguire i job di tutti adesso.
func (r Report) Ready() bool { return r.Status != StatusDown }

// Options sono le dipendenze del servizio.
type Options struct {
	// Pool è la connessione a PostgreSQL, la stessa dell'API e del motore
	// (SPEC §2). Obbligatorio.
	Pool *pgxpool.Pool
	// Engine è il battito dello scheduler. Nil significa che la sonda non viene
	// eseguita: è la configurazione di un processo che serve solo l'API, non
	// quella normale, e [New] lo segnala.
	Engine Engine
	// Logger, se assente, è quello di default.
	Logger *slog.Logger

	// Interval, Timeout, StaleAfter e PartitionWarning valgono i rispettivi
	// default quando sono zero.
	Interval         time.Duration
	Timeout          time.Duration
	StaleAfter       time.Duration
	PartitionWarning int

	// Now è l'orologio, iniettabile per i test. Nil significa [time.Now].
	Now func() time.Time
}

// Service esegue le sonde e conserva l'ultimo esito. Va costruito con [New] e
// acceso con [Service.Run].
type Service struct {
	pool   *pgxpool.Pool
	engine Engine
	log    *slog.Logger

	interval         time.Duration
	timeout          time.Duration
	staleAfter       time.Duration
	partitionWarning int
	now              func() time.Time

	mu   sync.RWMutex
	last Report
	// seen distingue «non ho ancora guardato» da «ho guardato e va tutto bene».
	// Un servizio appena avviato non è pronto finché non l'ha verificato, e il
	// valore zero di [Report] direbbe il contrario.
	seen bool
}

// New costruisce il servizio.
func New(opts Options) (*Service, error) {
	if opts.Pool == nil {
		return nil, errors.New("health: il pool di connessioni è obbligatorio")
	}
	if opts.Interval < 0 || opts.Timeout < 0 || opts.StaleAfter < 0 || opts.PartitionWarning < 0 {
		return nil, errors.New("health: nessuna delle manopole ammette valori negativi")
	}

	s := &Service{
		pool:             opts.Pool,
		engine:           opts.Engine,
		log:              opts.Logger,
		interval:         opts.Interval,
		timeout:          opts.Timeout,
		staleAfter:       opts.StaleAfter,
		partitionWarning: opts.PartitionWarning,
		now:              opts.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.interval == 0 {
		s.interval = DefaultInterval
	}
	if s.timeout == 0 {
		s.timeout = DefaultTimeout
	}
	if s.staleAfter == 0 {
		s.staleAfter = DefaultStaleAfter
	}
	if s.partitionWarning == 0 {
		s.partitionWarning = DefaultPartitionWarning
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.engine == nil {
		s.log.Warn("prontezza: nessun battito del motore da osservare, la sonda dello scheduler non verrà eseguita")
	}
	return s, nil
}

// Snapshot è l'ultimo esito osservato. **Non tocca il database**: è il metodo
// che serve gli endpoint, e il motivo per cui interrogarli non costa niente.
//
// Prima della prima passata risponde «non pronto», che è la verità: un processo
// appena avviato non ha ancora verificato di poter lavorare, e dichiararsi
// pronto per difetto è il modo di far arrivare traffico a un'istanza che non sa
// ancora se il database c'è.
func (s *Service) Snapshot() Report {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.seen {
		return Report{
			Status: StatusDown,
			At:     s.now(),
			Checks: []Check{{
				Name:   "avvio",
				Status: StatusDown,
				Detail: "le sonde non hanno ancora guardato",
			}},
		}
	}
	return s.last
}

// Check esegue le sonde adesso e aggiorna l'ultimo esito.
//
// È esportata perché è il metodo su cui si scrivono i test — le sonde si
// verificano chiamandole, non aspettando un ticker — e perché [Service.Run] la
// chiama subito, prima del primo intervallo: un processo appena avviato deve
// sapere entro un istante se può lavorare.
func (s *Service) Check(ctx context.Context) Report {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	now := s.now()
	report := Report{At: now}

	state, err := s.probe(ctx)
	switch {
	case err != nil:
		report.Checks = append(report.Checks, Check{
			Name: "database", Status: StatusDown,
			Detail: fmt.Sprintf("il database non risponde: %v", err),
		})
		// Senza una risposta non si sa niente delle partizioni: dire «zero
		// giorni di margine» sarebbe inventarsi un secondo guasto a partire dal
		// primo.
		report.PartitionHorizonDays = 0
	case state.inRecovery:
		report.Checks = append(report.Checks, Check{
			Name: "database", Status: StatusDown,
			Detail: "il database è in sola lettura: nessuna esecuzione può essere registrata",
		})
	default:
		report.Checks = append(report.Checks, Check{
			Name: "database", Status: StatusOK, Detail: "raggiungibile e in scrittura",
		})
		report.PartitionHorizonDays = state.horizonDays
		report.Checks = append(report.Checks, s.partitionCheck(state))
	}

	if s.engine != nil {
		check, age := s.schedulerCheck(now)
		report.SchedulerAge = age
		report.Checks = append(report.Checks, check)
	}

	report.Status = StatusOK
	for _, check := range report.Checks {
		report.Status = worst(report.Status, check.Status)
	}

	s.mu.Lock()
	s.last = report
	s.seen = true
	s.mu.Unlock()
	return report
}

// Run esegue una passata subito e poi una a ogni intervallo, finché il contesto
// non si chiude.
//
// Una sonda che va male **non** ferma il ciclo, ed è il punto: il momento in cui
// il database non risponde è precisamente quello in cui serve continuare a
// guardare, per accorgersi di quando torna.
func (s *Service) Run(ctx context.Context) error {
	s.log.Info("prontezza: sonde avviate",
		slog.Duration("passata", s.interval),
		slog.Duration("battito_massimo", s.staleAfter),
		slog.Int("margine_partizioni_giorni", s.partitionWarning))

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	previous := StatusOK
	for {
		report := s.Check(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Si scrive nel log solo quando lo stato **cambia**: una passata ogni
		// cinque secondi che dice «ok» riempirebbe il log di niente, e una che
		// dice «down» dodici volte al minuto seppellirebbe la prima, che è
		// l'unica con un'informazione dentro.
		if report.Status != previous {
			s.logChange(ctx, previous, report)
			previous = report.Status
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) logChange(ctx context.Context, from Status, report Report) {
	attrs := []slog.Attr{
		slog.String("da", string(from)),
		slog.String("a", string(report.Status)),
	}
	for _, check := range report.Checks {
		if check.Status != StatusOK {
			attrs = append(attrs, slog.String(check.Name, check.Detail))
		}
	}
	level := slog.LevelInfo
	switch report.Status {
	case StatusDown:
		level = slog.LevelError
	case StatusDegraded:
		level = slog.LevelWarn
	}
	s.log.LogAttrs(ctx, level, "prontezza: lo stato del motore è cambiato", attrs...)
}

// ------------------------------------------------------------------ le sonde

// partitionCheck traduce il margine di partizioni in un esito.
func (s *Service) partitionCheck(state probeState) Check {
	switch {
	case !state.hasPartitions:
		return Check{
			Name: "partizioni", Status: StatusDown,
			Detail: "job_executions non ha nessuna partizione: il motore non può scrivere",
		}
	case state.horizonDays < 0:
		return Check{
			Name: "partizioni", Status: StatusDown,
			Detail: fmt.Sprintf("manca la partizione di oggi: l'ultima copre %s",
				state.lastDay.Format(time.DateOnly)),
		}
	case state.horizonDays < s.partitionWarning:
		return Check{
			Name: "partizioni", Status: StatusDegraded,
			Detail: fmt.Sprintf("restano %d giorni di margine: la manutenzione non sta più preparando le partizioni future",
				state.horizonDays),
		}
	default:
		return Check{
			Name: "partizioni", Status: StatusOK,
			Detail: fmt.Sprintf("%d giorni di margine", state.horizonDays),
		}
	}
}

// schedulerCheck guarda il battito del motore.
func (s *Service) schedulerCheck(now time.Time) (Check, time.Duration) {
	last, ok := s.engine.LastTick()
	if !ok {
		// Nessuna passata riuscita da quando il processo è vivo. Non è un
		// dettaglio d'avvio: lo scheduler passa quattro volte al secondo, quindi
		// «nessuna» significa che non ne è mai andata a buon fine una.
		return Check{
			Name: "scheduler", Status: StatusDown,
			Detail: "nessuna passata portata a termine da quando il processo è avviato",
		}, 0
	}
	age := now.Sub(last)
	if age > s.staleAfter {
		return Check{
			Name: "scheduler", Status: StatusDown,
			Detail: fmt.Sprintf("l'ultima passata riuscita è di %s fa: il motore non sta dispacciando",
				age.Round(time.Second)),
		}, age
	}
	return Check{
		Name: "scheduler", Status: StatusOK,
		Detail: fmt.Sprintf("ultima passata riuscita %s fa", age.Round(time.Millisecond)),
	}, age
}

// worst tiene il peggiore fra due esiti.
func worst(a, b Status) Status {
	if severity(b) > severity(a) {
		return b
	}
	return a
}

func severity(s Status) int {
	switch s {
	case StatusDown:
		return 2
	case StatusDegraded:
		return 1
	default:
		return 0
	}
}
