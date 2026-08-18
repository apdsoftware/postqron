// Package metrics raccoglie ciò che il motore osserva mentre lavora e lo espone
// in una pagina di testo (R7).
//
// # Quali metriche, e perché queste
//
// La domanda a cui questo pacchetto deve rispondere è una sola — «il motore sta
// facendo il suo mestiere?» — e la grandezza che la risponde meglio non è il
// numero di esecuzioni né il tasso di errore dei job dei clienti: è **quanto
// un'occorrenza aspetta fra l'orario dovuto e la partenza effettiva**.
//
// È la misura del prodotto. Postqron vende risoluzioni di un minuto, dieci
// secondi e un secondo (SPEC §8) e dichiara una tolleranza
// ([scheduler.DefaultTolerance], R47): se quel ritardo cresce, il motore sta
// perdendo, e lo sta facendo prima che un job fallisca e molto prima che se ne
// accorga un cliente. Sta qui come istogramma perché la media di un ritardo non
// dice niente — quello che conta è la coda della distribuzione — e con l'aggiunta
// del massimo, che un istogramma non può ricostruire.
//
// Attorno a quella ci sono le grandezze che spiegano *perché* il ritardo cresce,
// e sono tutte di coda: quante occorrenze aspettano, quante ne sono in volo,
// quante sono state rifiutate, e quante volte i tetti del servizio hanno tolto
// di mezzo del lavoro pronto — la politica di sovrapposizione (R41) e il tetto
// per workspace (R10), entrambi introdotti da #457. Un'occorrenza saltata è un
// fatto, e un fatto va contato: scritto solo in un log è un fatto che nessuno
// somma.
//
// Infine ciò che rende il motore capace di scrivere: le passate della retention
// e il margine di partizioni di `job_executions`. Non è manutenzione da tenere
// d'occhio per diligenza — senza una partizione l'inserimento fallisce, e il
// motore smette di funzionare quattordici giorni dopo che qualcosa si è rotto.
//
// # Il costo, dichiarato con i numeri
//
// Su una macchina sola (SPEC §2) qualunque cosa si aggiunga è tolta alle query
// dei clienti, quindi vale la pena dire quanto costa questa.
//
//   - **Nessuna dipendenza nuova.** Il formato di esposizione di Prometheus è
//     testo con una riga per serie; scriverlo a mano costa il file accanto a
//     questo. La libreria ufficiale porterebbe un registro globale, la
//     reflection sulle etichette e un albero di dipendenze transitive per
//     produrre le stesse quaranta righe — su un binario che oggi dipende da
//     `pgx`, `crypto` e `yaml` e da nient'altro.
//   - **A regime la registrazione è un'atomica.** Ogni occorrenza aggiunge una
//     `atomic.Int64.Add` per contatore e una ricerca lineare su undici confini
//     per l'istogramma: nanosecondi, senza allocazioni, dentro un percorso che
//     fa comunque una chiamata HTTP e due UPDATE.
//   - **La memoria è fissa.** Nessuna serie storica, nessuna etichetta per job:
//     una struttura di qualche centinaio di byte, che non cresce né con il
//     numero di clienti né con quello dei job. Le serie storiche le tiene chi
//     raccoglie, che è il suo mestiere.
//   - **La lettura non tocca il database.** Le grandezze istantanee arrivano dal
//     worker pool in memoria, e lo stato di prontezza dall'ultima fotografia di
//     internal/health, che gira sul proprio ticker. Una raccolta costa la
//     composizione di una pagina di testo di qualche kilobyte.
//
// # Perché non ci sono etichette per job
//
// Perché sarebbe la scelta che fa esplodere il costo, e in un modo che si scopre
// tardi. Una serie per job significa che un cliente con mille job moltiplica per
// mille la memoria di chi raccoglie e la dimensione di ogni pagina — e le
// metriche per job esistono già, sono R28, e vivono su `job_executions`, che è
// la sorgente giusta per una grandezza che si guarda per job e all'indietro nel
// tempo. Qui ci sono gli aggregati del **servizio**, che è ciò che serve per
// sapere se il motore sta funzionando adesso.
package metrics

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/health"
	"github.com/apdsoftware/postqron/services/api/internal/retention"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// PoolStats è la fotografia istantanea del worker pool, letta al momento della
// raccolta. In esercizio è `*dispatch.Pool`.
//
// È un'interfaccia e non il tipo concreto perché il verso della dipendenza
// conta: l'osservabilità conosce il motore, il motore non conosce
// l'osservabilità.
type PoolStats interface {
	Stats() dispatch.Stats
}

// HealthSnapshot è l'ultimo esito delle sonde di prontezza, letto al momento
// della raccolta. In esercizio è `*health.Service`.
//
// Si legge la fotografia e **non** si provoca una passata: una raccolta di
// metriche non deve poter mettere carico sul database, altrimenti l'osservatorio
// diventa parte del problema che osserva.
type HealthSnapshot interface {
	Snapshot() health.Report
}

// Options configura il registro.
type Options struct {
	// Version è la versione del binario, esposta come etichetta di
	// `postqron_build_info`.
	Version string
	// Env è l'ambiente di esecuzione, sulla stessa serie.
	Env string

	// Tolerance è la tolleranza di dispatch dichiarata (R47). Zero vale
	// [scheduler.DefaultTolerance]. È esposta come metrica perché una misura
	// senza l'impegno accanto non dice se va bene o male.
	Tolerance time.Duration

	// Now è l'orologio, iniettabile per i test. Nil significa [time.Now].
	Now func() time.Time
}

// Registry raccoglie le grandezze del motore. Va costruito con [New].
//
// Implementa [scheduler.Observer], [dispatch.Observer] e [retention.Observer] —
// i tre agganci che il motore offre — e [health.Engine], perché il battito dello
// scheduler è una cosa che solo chi osserva le passate conosce.
//
// Tutti i metodi sono chiamati dai cicli del motore e sono privi di attese: la
// scrittura è un'atomica, e non c'è nessun lock che un worker possa trovare
// occupato.
type Registry struct {
	version string
	env     string
	now     func() time.Time
	started time.Time

	tolerance time.Duration
	pool      PoolStats
	health    HealthSnapshot

	sched    schedulerCounters
	lag      histogram
	reten    retentionCounters
	failures map[dispatch.FailureKind]*atomic.Int64
}

// schedulerCounters sono i cumulativi delle passate.
type schedulerCounters struct {
	ticks, failures                     atomic.Int64
	jobs, seeded                        atomic.Int64
	enqueued, recovered                 atomic.Int64
	conflicts, rejected, overlapped     atomic.Int64
	dropped, expired, invalid, late     atomic.Int64
	tickNanos                           atomic.Int64
	backlog                             atomic.Bool
	lastTickUnixNano, lastAttemptUnixNs atomic.Int64
}

// retentionCounters sono i cumulativi della manutenzione.
type retentionCounters struct {
	sweeps, failures            atomic.Int64
	ensured, dropped            atomic.Int64
	rows, batches               atomic.Int64
	dropDeferred, ensDeferred   atomic.Int64
	truncated, longestRetention atomic.Int64
}

// kinds è l'ordine stabile in cui le classi di fallimento vengono esposte.
// Stabile perché è l'insieme dei valori dell'etichetta `kind`, e un insieme che
// cambia ordine fra due raccolte è un grafico che salta.
//
// L'elenco è quello di [dispatch.FailureKind] e non una copia scelta qui: le
// stesse sei classi finiscono in un'email (R21) e in un grafico, e due elenchi
// divergerebbero al primo valore aggiunto — con la metrica che smette
// silenziosamente di contare proprio la causa nuova.
var kinds = dispatch.FailureKinds()

// New costruisce il registro.
func New(opts Options) *Registry {
	r := &Registry{
		version:   opts.Version,
		env:       opts.Env,
		now:       opts.Now,
		tolerance: opts.Tolerance,
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.tolerance == 0 {
		r.tolerance = scheduler.DefaultTolerance
	}
	r.failures = make(map[dispatch.FailureKind]*atomic.Int64, len(kinds))
	for _, kind := range kinds {
		r.failures[kind] = new(atomic.Int64)
	}
	r.started = r.now()
	return r
}

// ---------------------------------------------------------------- scheduler

// Le due sorgenti istantanee si innestano dopo la costruzione invece di stare in
// [Options], e non è una scelta di stile: è l'ordine in cui i pezzi possono
// nascere.
//
// Il registro deve esistere **per primo** — è lui a raccogliere fin dal primo
// istante di vita del processo, e uno innestato dopo perderebbe le passate e i
// fallimenti dei primi secondi, che sono quelli in cui un deploy andato male si
// vede. Ma il worker pool e le sonde nascono dopo di lui, e le sonde in
// particolare **chiedono al registro** il battito dello scheduler
// ([Registry.LastTick]): senza questa separazione le due parti non potrebbero
// tenersi per mano.
//
// Nil resta un valore legittimo per entrambe: le rispettive serie non vengono
// scritte, che è la configurazione di un processo senza motore e quella dei
// test. Meglio una serie assente che una serie a zero, che si leggerebbe come
// «la coda è vuota» quando la verità è «non c'è nessuna coda».
func (r *Registry) UsePool(p PoolStats) { r.pool = p }

// UseHealth collega le sonde di prontezza.
func (r *Registry) UseHealth(h HealthSnapshot) { r.health = h }

var _ scheduler.Observer = (*Registry)(nil)

// Enqueued registra il ritardo di un'occorrenza consegnata al dispatch (R47).
//
// Le occorrenze **riprese** non entrano nella misura, per la stessa ragione per
// cui non entrano in [scheduler.Stats.MaxLag]: il loro ritardo dice per quanto
// il processo è stato fermo, non con che precisione lo scheduler dispaccia, e
// mescolarle sposterebbe la coda della distribuzione di ore per un fatto che non
// c'entra.
func (r *Registry) Enqueued(occ scheduler.Occurrence) {
	if occ.Recovered {
		return
	}
	r.lag.observe(occ.Lag())
}

// Dropped registra le occorrenze che un job si è lasciato indietro perché fuori
// dalla finestra di recupero.
func (r *Registry) Dropped(d scheduler.Dropped) {
	r.sched.dropped.Add(int64(d.Count))
}

// Tick registra il resoconto di una passata.
//
// Il battito — l'istante che [Registry.LastTick] restituisce — avanza **solo se
// la passata è arrivata in fondo**. Una passata fallita è un tentativo, non un
// battito: contarla renderebbe indistinguibile un motore che gira da uno che ci
// prova quattro volte al secondo contro un database che non risponde.
func (r *Registry) Tick(st scheduler.Stats) {
	now := r.now()
	r.sched.ticks.Add(1)
	r.sched.tickNanos.Add(int64(st.Duration))
	r.sched.lastAttemptUnixNs.Store(now.UnixNano())
	if st.Failed {
		r.sched.failures.Add(1)
	} else {
		r.sched.lastTickUnixNano.Store(now.UnixNano())
	}

	r.sched.jobs.Add(int64(st.Jobs))
	r.sched.seeded.Add(int64(st.Seeded))
	r.sched.enqueued.Add(int64(st.Enqueued))
	r.sched.recovered.Add(int64(st.Recovered))
	r.sched.conflicts.Add(int64(st.Conflicts))
	r.sched.rejected.Add(int64(st.Rejected))
	r.sched.overlapped.Add(int64(st.Overlapped))
	r.sched.expired.Add(int64(st.Expired))
	r.sched.invalid.Add(int64(st.Invalid))
	r.sched.late.Add(int64(st.Late))
	r.sched.backlog.Store(st.Backlog)
}

var _ health.Engine = (*Registry)(nil)

// LastTick implementa [health.Engine]: l'istante dell'ultima passata riuscita.
//
// Sta qui e non nello scheduler perché lo scheduler non tiene la propria storia
// — la espone a chi osserva e va avanti — e chi la tiene è questo registro.
func (r *Registry) LastTick() (time.Time, bool) {
	nanos := r.sched.lastTickUnixNano.Load()
	if nanos == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}

// ----------------------------------------------------------------- dispatch

var _ dispatch.Observer = (*Registry)(nil)

// Failed registra un fallimento definitivo, per classe.
//
// Il contesto non serve a questo osservatore: non va da nessuna parte, scrive in
// memoria. È nella firma perché ce l'ha l'interfaccia, che esiste anche per chi
// deve andare sul database (gli avvisi di R21).
func (r *Registry) Failed(_ context.Context, f dispatch.Failure) {
	if counter, ok := r.failures[f.Kind]; ok {
		counter.Add(1)
		return
	}
	// Una classe che non conosciamo finisce fra le sconosciute: perdere il
	// conteggio sarebbe peggio che approssimarne la categoria.
	r.failures[dispatch.FailureUnknown].Add(1)
}

// ---------------------------------------------------------------- retention

var _ retention.Observer = (*Registry)(nil)

// Swept registra il resoconto di una passata di manutenzione.
func (r *Registry) Swept(st retention.Stats, err error) {
	r.reten.sweeps.Add(1)
	if err != nil {
		r.reten.failures.Add(1)
	}
	r.reten.ensured.Add(int64(st.PartitionsEnsured))
	r.reten.dropped.Add(int64(st.PartitionsDropped))
	r.reten.rows.Add(st.RowsDeleted)
	r.reten.batches.Add(int64(st.Batches))
	if st.DropDeferred {
		r.reten.dropDeferred.Add(1)
	}
	if st.EnsureDeferred {
		r.reten.ensDeferred.Add(1)
	}
	if st.Truncated {
		r.reten.truncated.Add(1)
	}
	r.reten.longestRetention.Store(int64(st.LongestRetention))
}
