package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Job è un job come serve al dispatch: l'identità, la schedulazione e il
// bersaglio HTTP, letti in un colpo solo dalla stessa riga che lo scheduler ha
// già dovuto leggere per sapere che era dovuto.
//
// I campi sono quelli di `jobs` (migrazione 0005) e ne conservano i nomi. Non
// c'è un `next_run_at`: è lo stato interno dello scheduler, non qualcosa che
// serva a chi esegue la chiamata.
type Job struct {
	// ID e UserID sono uuid in forma testuale, come altrove nel backend.
	ID     string
	UserID string
	// Name è l'identità stabile del job (SPEC §9), utile nei log.
	Name string

	// Expression è `jobs.schedule`, vuota se il job usa un intervallo.
	Expression string
	// Every è `jobs.every_seconds`, zero se il job usa un'espressione cron.
	Every time.Duration
	// Timezone è il fuso del job. Conta solo per la modalità cron.
	Timezone string

	// Environments sono gli ambienti in cui il job vive (R23). Ogni ambiente
	// produce la propria occorrenza, con il proprio esito.
	Environments []string

	URL    string
	Method string
	// Headers è l'oggetto JSON di `jobs.headers`, non decodificato: i
	// riferimenti `${VAR}` ai segreti sono ancora dentro e vanno risolti da chi
	// esegue, non da chi schedula. Vedi [Job.HeaderMap].
	Headers json.RawMessage
	// Body è il corpo della richiesta; nil se il job non ne ha uno.
	Body *string

	Timeout      time.Duration
	MaxRetries   int
	RetryBackoff string

	// Overlap è `jobs.overlap_policy` (migrazione 0014, R41): cosa fare quando
	// un'occorrenza scatta mentre la precedente è ancora in corso.
	//
	// Lo scheduler non la applica — non sa cosa sia in volo, lo sa il dispatch —
	// ma la legge nella stessa passata in cui legge tutto il resto del job, e la
	// porta a valle dentro l'occorrenza. Vuoto vale [DefaultOverlap], che è ciò
	// che una riga scritta prima della 0014 non può avere.
	Overlap OverlapPolicy

	// Enabled è lo stato al momento della lettura. È sempre vero per le
	// occorrenze appena accodate — la query calda filtra i job in pausa — ma
	// può essere falso per un'occorrenza recuperata (vedi
	// [Occurrence.Recovered]): il job è stato messo in pausa fra il momento in
	// cui l'occorrenza è stata accodata e quello in cui viene ripresa. Chi
	// esegue decide se onorarla o segnarla `skipped`.
	Enabled bool
}

// HeaderMap decodifica gli header nella forma che serve a una richiesta HTTP.
//
// La decodifica sta qui e non nella lettura dal database perché un `headers`
// malformato non deve poter far fallire l'intero lotto di job dovuti: lo schema
// garantisce che sia un oggetto JSON, non che i valori siano stringhe.
func (j Job) HeaderMap() (map[string]string, error) {
	if len(j.Headers) == 0 {
		return map[string]string{}, nil
	}
	headers := map[string]string{}
	if err := json.Unmarshal(j.Headers, &headers); err != nil {
		return nil, fmt.Errorf("job %s: header non decodificabili: %w", j.Name, err)
	}
	return headers, nil
}

// OverlapPolicy è cosa fare quando un'occorrenza scatta mentre la precedente è
// ancora in corso: il tipo `overlap_policy` della migrazione 0014 (R41).
//
// È dichiarato qui e non importato da internal/jobs per la stessa ragione per
// cui [Job] non è `jobs.Job`: il motore legge le proprie colonne e non dipende
// dal package dell'API. I valori sono quelli dell'enum del database, che è la
// sola definizione che conta per entrambi.
type OverlapPolicy string

// Le politiche, e il predefinito per una riga che non ne porta una.
const (
	// OverlapSkip non esegue l'occorrenza in eccesso.
	OverlapSkip OverlapPolicy = "skip"
	// OverlapQueue la fa aspettare: le esecuzioni restano serializzate.
	OverlapQueue OverlapPolicy = "queue"
	// OverlapAllow la lascia partire in parallelo, entro i tetti del servizio.
	OverlapAllow OverlapPolicy = "allow"

	// DefaultOverlap è la politica di chi non ne dichiara una. Coincide con il
	// default della colonna (0014) e con `jobs.DefaultOverlapPolicy`: è il valore
	// che non fa danni a un job di cui non si sa niente.
	DefaultOverlap = OverlapSkip
)

// Or restituisce la politica, o il predefinito quando non ce n'è una.
//
// Serve perché un valore vuoto non arriva mai dal database — la colonna è NOT
// NULL — ma arriva da chiunque costruisca un'occorrenza a mano: l'adattatore del
// trigger manuale, un test. Trattarlo come «nessuna politica» invece che come il
// predefinito dichiarato produrrebbe due comportamenti diversi per la stessa
// riga a seconda di chi la legge.
func (p OverlapPolicy) Or() OverlapPolicy {
	switch p {
	case OverlapSkip, OverlapQueue, OverlapAllow:
		return p
	default:
		return DefaultOverlap
	}
}

// Occurrence è una singola esecuzione dovuta, già scritta su `job_executions` e
// pronta per essere eseguita.
//
// L'esistenza della riga è ciò che rende l'occorrenza *presa*: la chiave
// primaria naturale `(job_id, scheduled_for, environment, attempt)` è il lock di
// idempotenza di R4, e lo scheduler consegna al dispatch solo le occorrenze di
// cui ha vinto l'inserimento.
type Occurrence struct {
	Job Job

	// ScheduledFor è l'istante **teorico** dell'occorrenza, non quello in cui è
	// partita: è la colonna che la identifica e su cui l'idempotenza si regge.
	ScheduledFor time.Time
	// Environment è l'ambiente di questa occorrenza (R23).
	Environment string
	// Attempt è il numero di tentativo; 1 per tutto ciò che nasce dalla
	// schedulazione. I valori successivi sono dei retry (R5, issue #392).
	Attempt int16

	// EnqueuedAt è l'istante in cui lo scheduler ha consegnato l'occorrenza al
	// dispatch. Insieme a ScheduledFor è la misura di R47: vedi [Occurrence.Lag].
	EnqueuedAt time.Time

	// Recovered distingue un'occorrenza ripresa da una riga rimasta `pending`
	// (tipicamente dopo un riavvio) da una appena calcolata. Per chi esegue non
	// cambia nulla; per chi misura sì, perché il ritardo di una ripresa non
	// dice niente sulla precisione dello scheduler.
	Recovered bool
}

// Lag è lo scarto fra l'orario dovuto e il momento in cui l'occorrenza è stata
// consegnata al dispatch.
//
// È **la** grandezza di R47: la tolleranza dichiarata da [DefaultTolerance] è un
// impegno su questo numero, e senza un punto in cui misurarlo «risoluzione 1
// secondo» resterebbe una frase di marketing. La misura in esercizio è la issue
// #462; qui nasce il punto in cui osservarla.
func (o Occurrence) Lag() time.Duration { return o.EnqueuedAt.Sub(o.ScheduledFor) }

// String è la forma leggibile di un'occorrenza, per log e messaggi d'errore.
func (o Occurrence) String() string {
	return fmt.Sprintf("%s@%s[%s]", o.Job.Name, o.ScheduledFor.UTC().Format(time.RFC3339), o.Environment)
}

// Dispatcher riceve le occorrenze dovute. È l'unica cosa che lo scheduler sa
// del mondo a valle: il worker pool è la issue #389, l'esecutore HTTP la #390,
// il retry la #392.
//
// Il contratto ha tre clausole, e tutte e tre contano.
//
// **1. La riga esiste già.** Quando Dispatch viene chiamata l'occorrenza è su
// `job_executions` con stato `pending`. Non va inserita di nuovo.
//
// **2. Dispatch non deve bloccare.** Lo scheduler la chiama nel proprio ciclo:
// un'implementazione che aspetta la fine della chiamata HTTP ritarderebbe tutte
// le occorrenze successive, ed è esattamente ciò che R3 vieta. L'implementazione
// attesa accoda e torna. Se non può accodare — coda piena, arresto in corso —
// **restituisce un errore**: lo scheduler lo conta e lascia la riga `pending`,
// dove il recupero la ritrova.
//
// **3. La stessa occorrenza può arrivare due volte.** Una riga rimasta `pending`
// oltre [Options.ReclaimAfter] viene riofferta: è così che un riavvio non perde
// il lavoro già preso. Chi esegue chiude R4 prendendosela con un aggiornamento
// condizionato —
//
//	UPDATE job_executions SET status = 'running', started_at = now()
//	 WHERE job_id = $1 AND scheduled_for = $2 AND environment = $3
//	   AND attempt = $4 AND status = 'pending'
//
// — e procede solo se ha aggiornato una riga. Quel `WHERE status = 'pending'` è
// il secondo cancello dell'idempotenza: il primo è la chiave primaria, che
// impedisce a due occorrenze di nascere; questo impedisce a una stessa
// occorrenza di partire due volte.
type Dispatcher interface {
	Dispatch(ctx context.Context, occ Occurrence) error
}

// DispatcherFunc adatta una funzione a [Dispatcher].
type DispatcherFunc func(ctx context.Context, occ Occurrence) error

// Dispatch implementa [Dispatcher].
func (f DispatcherFunc) Dispatch(ctx context.Context, occ Occurrence) error { return f(ctx, occ) }

// Dropped descrive le occorrenze che lo scheduler ha deciso di **non**
// eseguire perché troppo vecchie: quelle antecedenti a `now - CatchUp`.
//
// Non diventano righe su `job_executions`. Un job a un secondo fermo per tre ore
// produrrebbe 10.800 righe `skipped` per dire che non è successo niente, cioè la
// stessa ondata di scritture che la finestra di recupero serve a evitare. Il
// fatto resta comunque osservabile — è questo tipo — e contato: un limite
// dichiarato e misurato, non una perdita silenziosa.
type Dropped struct {
	Job Job
	// From e To sono la prima e l'ultima occorrenza scartata.
	From, To time.Time
	// Count è quante ne sono state scartate.
	Count int
	// Truncated è vero quando il conteggio si è fermato al proprio tetto: le
	// occorrenze scartate sono almeno Count, non esattamente Count. Succede solo
	// a fermi lunghissimi su job a risoluzione di un secondo.
	Truncated bool
}

// Observer riceve ciò che lo scheduler osserva mentre lavora. Serve alle
// metriche (R7) e alla misura della tolleranza di dispatch (R47, issue #462):
// il motore non tiene serie storiche, le espone.
//
// I metodi sono chiamati dal ciclo dello scheduler e devono tornare subito.
type Observer interface {
	// Enqueued è chiamato per ogni occorrenza passata al dispatch, con il suo
	// ritardo già leggibile in [Occurrence.Lag]. Anche quando il dispatch la
	// rifiuta: la riga è comunque accodata, e il ritardo con cui lo scheduler ci
	// è arrivato è comunque una misura valida di R47.
	Enqueued(occ Occurrence)
	// Dropped è chiamato quando un job si lascia indietro delle occorrenze
	// perché fuori dalla finestra di recupero.
	Dropped(d Dropped)
	// Tick è chiamato alla fine di ogni passata, riuscita o no.
	Tick(st Stats)
}

// nopObserver è l'osservatore di default: non guarda niente.
type nopObserver struct{}

func (nopObserver) Enqueued(Occurrence) {}
func (nopObserver) Dropped(Dropped)     {}
func (nopObserver) Tick(Stats)          {}
