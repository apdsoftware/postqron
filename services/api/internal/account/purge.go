package account

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// I valori predefiniti della passata di purga. Sono tarati su SPEC §2: API,
// motore e database sulla stessa macchina, un VPS, non un cluster.
const (
	// DefaultPurgeInterval è la distanza fra due passate.
	//
	// Un'ora, come internal/retention, e per la stessa ragione: la scadenza si
	// misura in giorni, quindi la frequenza non serve a essere puntuali — serve
	// a rendere ogni passata piccola. Una passata al giorno concentrerebbe tutto
	// il lavoro in un momento solo, che è esattamente la forma con cui la
	// manutenzione si accorge di esistere.
	//
	// La conseguenza va conosciuta: la purga avviene **fino a un'ora dopo** la
	// scadenza, mai prima. Sbagliare in ritardo è l'unica direzione accettabile
	// — cancellare in anticipo accorcerebbe una grazia promessa per iscritto.
	DefaultPurgeInterval = time.Hour

	// DefaultMaxAccounts è quanti account si purgano per passata.
	//
	// Cinque è basso di proposito. Un account può portarsi dietro milioni di
	// righe di `job_executions` (vedi [Store.Purge]), e la passata gira sulla
	// stessa macchina del motore: il tetto è ciò che impedisce a una giornata di
	// molte chiusure di trasformarsi in un'ora di database occupato. Quello che
	// avanza lo prende la passata dopo, un'ora più tardi.
	DefaultMaxAccounts = 5
)

// PurgeObserver riceve il resoconto delle passate. Serve alle metriche (R7): la
// purga è invisibile finché funziona, e il momento in cui smette di funzionare è
// quello in cui nessuno la sta guardando — con la differenza, rispetto alla
// retention, che qui a non funzionare è una promessa scritta in un documento
// legale.
//
// Il metodo è chiamato dal ciclo di [Purger.Run] e deve tornare subito.
type PurgeObserver interface {
	// Purged è chiamato alla fine di ogni passata, riuscita o no.
	Purged(st SweepStats, err error)
}

type nopPurgeObserver struct{}

func (nopPurgeObserver) Purged(SweepStats, error) {}

// SweepStats è il resoconto di una passata.
type SweepStats struct {
	// Due sono l'insieme di account trovati scaduti, Accounts quelli purgati per
	// intero. La differenza non è un errore: un account lasciato a metà è
	// [Purged.Truncated], e la passata successiva lo riprende.
	Due      int
	Accounts int

	Executions   int64
	Jobs         int64
	AuditDeleted int64
	AuditKept    int64

	// Truncated dice che almeno un account è rimasto a metà, o che il tetto di
	// [PurgeOptions.MaxAccounts] è stato raggiunto.
	Truncated bool
}

// PurgeOptions configura il [Purger]. Store è obbligatorio.
type PurgeOptions struct {
	Store  Store
	Logger *slog.Logger

	// Interval è la distanza fra due passate. Zero significa
	// [DefaultPurgeInterval].
	Interval time.Duration
	// MaxAccounts è il tetto di account per passata. Zero significa
	// [DefaultMaxAccounts].
	MaxAccounts int

	Observer PurgeObserver

	// Now è l'orologio, iniettabile per i test — che devono poter far scadere
	// una grazia senza aspettarla.
	Now func() time.Time
}

// Purger rimuove gli account la cui grazia è scaduta. Va costruito con
// [NewPurger] e acceso con [Purger.Run].
type Purger struct {
	store       Store
	log         *slog.Logger
	obs         PurgeObserver
	interval    time.Duration
	maxAccounts int
	now         func() time.Time
}

// NewPurger costruisce la passata di purga.
func NewPurger(opts PurgeOptions) (*Purger, error) {
	if opts.Store == nil {
		return nil, errors.New("account: lo store è obbligatorio")
	}
	if opts.Interval < 0 || opts.MaxAccounts < 0 {
		return nil, errors.New("account: nessuna delle manopole della purga ammette valori negativi")
	}

	p := &Purger{
		store:       opts.Store,
		log:         opts.Logger,
		obs:         opts.Observer,
		interval:    opts.Interval,
		maxAccounts: opts.MaxAccounts,
		now:         opts.Now,
	}
	if p.log == nil {
		p.log = slog.Default()
	}
	if p.obs == nil {
		p.obs = nopPurgeObserver{}
	}
	if p.interval == 0 {
		p.interval = DefaultPurgeInterval
	}
	if p.maxAccounts == 0 {
		p.maxAccounts = DefaultMaxAccounts
	}
	if p.now == nil {
		p.now = time.Now
	}
	return p, nil
}

// Sweep purga gli account scaduti e restituisce quel che ha fatto.
//
// Un account che fallisce **non impedisce agli altri di essere purgati**: sono
// lavori indipendenti, e fermare la passata al primo errore significherebbe che
// un solo account in stato strano tiene in vita a tempo indeterminato i dati di
// tutti quelli in coda dietro di lui — cioè trasforma un guasto in una promessa
// non mantenuta. Gli errori si accumulano e tornano insieme.
func (p *Purger) Sweep(ctx context.Context) (SweepStats, error) {
	var (
		stats SweepStats
		errs  []error
	)

	due, err := p.store.DueForPurge(ctx, p.now(), p.maxAccounts)
	if err != nil {
		return stats, err
	}
	stats.Due = len(due)
	if len(due) == p.maxAccounts {
		// Il tetto è stato raggiunto: potrebbero essercene altri. Sovrastimare è
		// deliberato, come in internal/retention — «forse ne restano» va detto,
		// «ho finito» dev'essere vero.
		stats.Truncated = true
	}

	for _, userID := range due {
		if err := ctx.Err(); err != nil {
			stats.Truncated = true
			return stats, errors.Join(errs...)
		}

		purged, err := p.store.Purge(ctx, userID)
		stats.Executions += purged.Executions
		stats.Jobs += purged.Jobs
		stats.AuditDeleted += purged.AuditDeleted
		stats.AuditKept += purged.AuditKept
		if purged.Truncated {
			stats.Truncated = true
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if purged.Truncated {
			// L'account c'è ancora e la passata successiva lo riprende: non è
			// finito, e non va contato come tale.
			p.log.InfoContext(ctx, "purga dell'account interrotta al tetto dei lotti, riprende alla passata successiva",
				slog.String("user_id", userID),
				slog.Int64("esecuzioni_cancellate", purged.Executions),
				slog.Int("lotti", purged.Batches))
			continue
		}

		stats.Accounts++
		// La riga che dimostra che la promessa è stata mantenuta. `user_id` è
		// l'ultimo momento in cui esiste: dopo questa istruzione non c'è più
		// niente nel database che lo contenga, e senza questa riga non ci sarebbe
		// modo di sapere che l'account è stato rimosso invece che perso.
		p.log.InfoContext(ctx, "account purgato (R45)",
			slog.String("user_id", userID),
			slog.Int64("esecuzioni", purged.Executions),
			slog.Int64("job", purged.Jobs),
			slog.Int64("audit_cancellate", purged.AuditDeleted),
			slog.Int64("audit_conservate", purged.AuditKept),
			slog.Int64("eventi_paddle", purged.PaddleEvents),
			slog.Int64("consegne_github", purged.GitHubDeliveries),
			slog.Int("lotti", purged.Batches))
	}

	return stats, errors.Join(errs...)
}

// Run esegue una passata subito e poi una a ogni intervallo, finché il contesto
// non si chiude.
//
// Una passata fallita **non** ferma il ciclo, per la stessa ragione di
// internal/retention: un errore transitorio del database si riassorbe alla
// passata dopo, e un processo che muore perché non è riuscito a purgare un
// account sarebbe un rimedio peggiore del male. Ciò che l'errore deve fare è
// comparire nei log — che è anche l'unico modo in cui un errore *persistente* si
// distingue da uno transitorio.
func (p *Purger) Run(ctx context.Context) error {
	p.log.Info("purga degli account avviata (R45)",
		slog.Duration("passata", p.interval),
		slog.Int("account_per_passata", p.maxAccounts))

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		p.sweepAndLog(ctx)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *Purger) sweepAndLog(ctx context.Context) {
	stats, err := p.Sweep(ctx)
	p.obs.Purged(stats, err)
	if err != nil {
		if ctx.Err() != nil {
			// L'arresto del processo ha interrotto la passata a metà. Non è un
			// guasto: ciò che non è stato purgato lo prende il riavvio.
			return
		}
		p.log.Error("purga degli account: passata incompleta", slog.Any("error", err))
	}
	if stats.Due == 0 {
		// Il caso normale è che non ci sia niente da fare: dirlo a ogni ora
		// riempirebbe il log di righe che non informano nessuno.
		return
	}
	p.log.LogAttrs(ctx, slog.LevelInfo, "purga degli account: passata conclusa",
		slog.Int("scaduti", stats.Due),
		slog.Int("purgati", stats.Accounts),
		slog.Int64("esecuzioni", stats.Executions),
		slog.Bool("resta_lavoro", stats.Truncated))
}
