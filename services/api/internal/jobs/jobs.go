// Package jobs è il modello di dominio dei cronjob e delle loro esecuzioni:
// la parte di R8 che non è né HTTP né SQL.
//
// # Perché esiste come package separato
//
// La validazione di un job vive in tre posti — il vincolo `CHECK` nel database,
// il parser di [schedule], e il rifiuto anticipato dell'API — e i tre devono
// dire la stessa cosa. Il posto in cui «la stessa cosa» viene decisa è questo:
// un 500 perché PostgreSQL ha respinto ciò che l'API aveva accettato è un
// difetto, non un caso limite, e si evita solo se esiste un unico punto che
// conosce entrambi i contratti.
//
// Il pacchetto non importa pgx e non importa net/http. È la stessa scelta di
// internal/auth rispetto a internal/authpg: i test sui limiti di piano, sul
// rifiuto di `every: 1s` in Free e sul tetto al trigger manuale girano senza un
// database in piedi, mentre le proprietà che dipendono da PostgreSQL —
// atomicità del conteggio dei job, unicità del nome, chiave naturale delle
// esecuzioni — sono provate in internal/jobspg contro il database vero.
package jobs

import (
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/schedule"
)

// Environment è un ambiente di esecuzione: il tipo `environment` della
// migrazione 0001 (R23).
type Environment string

// Gli ambienti previsti. Il piano Free ne ha uno solo e usa `production`.
const (
	EnvironmentStaging    Environment = "staging"
	EnvironmentProduction Environment = "production"
)

// Environments elenca gli ambienti ammessi, **nell'ordine di dichiarazione del
// tipo `environment`** della migrazione 0001. L'ordine non è decorativo: vedi
// [EnvironmentRank].
var Environments = []Environment{EnvironmentStaging, EnvironmentProduction}

// EnvironmentRank è la posizione dell'ambiente nel tipo enumerato.
//
// PostgreSQL ordina un enum per ordine di dichiarazione, non alfabeticamente:
// `staging` viene prima di `production`. Conta perché l'ordinamento del registro
// delle esecuzioni segue la chiave primaria di `job_executions`, e ordinarlo in
// modo diverso — per esempio confrontando le stringhe — costringerebbe il
// database a ordinare 86.400 righe al giorno invece di leggerle già ordinate
// dall'indice. Il cursore di paginazione deve quindi usare questo stesso
// criterio, altrimenti salta righe.
func EnvironmentRank(e Environment) int {
	for i, env := range Environments {
		if env == e {
			return i
		}
	}
	return len(Environments)
}

// Method è un metodo HTTP ammesso come target (tipo `http_method`).
// PostQron esegue esclusivamente chiamate HTTP: nessun comando, nessun
// container (SPEC §10).
type Method string

// I metodi ammessi.
const (
	MethodGET     Method = "GET"
	MethodPOST    Method = "POST"
	MethodPUT     Method = "PUT"
	MethodPATCH   Method = "PATCH"
	MethodDELETE  Method = "DELETE"
	MethodHEAD    Method = "HEAD"
	MethodOPTIONS Method = "OPTIONS"
)

// Methods elenca i metodi ammessi.
var Methods = []Method{MethodGET, MethodPOST, MethodPUT, MethodPATCH, MethodDELETE, MethodHEAD, MethodOPTIONS}

// Backoff è la politica di attesa fra due tentativi (tipo `retry_backoff`, R5).
type Backoff string

// Le politiche di retry ammesse.
const (
	BackoffExponential Backoff = "exponential"
	BackoffLinear      Backoff = "linear"
	BackoffFixed       Backoff = "fixed"
)

// Backoffs elenca le politiche ammesse.
var Backoffs = []Backoff{BackoffExponential, BackoffLinear, BackoffFixed}

// AlertChannel è un canale di avviso su fallimento (tipo `alert_channel`, R21, R29).
type AlertChannel string

// I canali di avviso ammessi.
const (
	AlertEmail   AlertChannel = "email"
	AlertSlack   AlertChannel = "slack"
	AlertDiscord AlertChannel = "discord"
	AlertWebhook AlertChannel = "webhook"
)

// AlertChannels elenca i canali ammessi.
var AlertChannels = []AlertChannel{AlertEmail, AlertSlack, AlertDiscord, AlertWebhook}

// ExecutionStatus è lo stato di un tentativo (tipo `execution_status`, R6).
type ExecutionStatus string

// Gli stati di un tentativo.
const (
	StatusPending   ExecutionStatus = "pending"
	StatusRunning   ExecutionStatus = "running"
	StatusSucceeded ExecutionStatus = "succeeded"
	StatusFailed    ExecutionStatus = "failed"
	StatusTimedOut  ExecutionStatus = "timed_out"
	StatusSkipped   ExecutionStatus = "skipped"
)

// ExecutionStatuses elenca gli stati ammessi.
var ExecutionStatuses = []ExecutionStatus{
	StatusPending, StatusRunning, StatusSucceeded, StatusFailed, StatusTimedOut, StatusSkipped,
}

// ExecutionTrigger è l'origine di un tentativo (tipo `execution_trigger`).
type ExecutionTrigger string

// Le origini di un tentativo.
const (
	TriggerSchedule ExecutionTrigger = "schedule"
	TriggerManual   ExecutionTrigger = "manual"
	TriggerRetry    ExecutionTrigger = "retry"
)

// ExecutionTriggers elenca le origini ammesse.
var ExecutionTriggers = []ExecutionTrigger{TriggerSchedule, TriggerManual, TriggerRetry}

// Job è un cronjob come lo vede il dominio (R1, R22, R23).
//
// Le due modalità di schedulazione sono rappresentate come lo sono nel database
// e in [schedule.Spec]: `Schedule` valorizzato e `Every` a zero, oppure il
// contrario. Non c'è un campo «modalità»: sarebbe una terza verità libera di
// contraddire le altre due.
type Job struct {
	ID     string
	UserID string

	// RepositoryID è vuoto per i job nati da API o dashboard, valorizzato per
	// quelli che vengono da un `cron.yaml` (R13). La differenza non è
	// decorativa: vedi [Job.Managed].
	RepositoryID string

	// Name è l'identità stabile del job (SPEC §9), unica per utente fra i job
	// creati a mano e unica per repository fra quelli sincronizzati.
	Name        string
	Description string

	// Schedule è l'espressione cron a cinque campi; vuota se il job usa
	// l'intervallo.
	Schedule string
	// Every è l'intervallo; zero se il job usa il cron. È la modalità che copre
	// la risoluzione sub-minuto dei piani Pro e Team (R22).
	Every time.Duration
	// Timezone è un nome IANA. Vuoto vale `UTC`.
	Timezone string

	Environments []Environment

	URL     string
	Method  Method
	Headers map[string]string
	Body    string

	Timeout      time.Duration
	MaxRetries   int
	RetryBackoff Backoff

	AlertOnFailure []AlertChannel

	Enabled    bool
	ArchivedAt *time.Time
	NextRunAt  *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewJob restituisce un job con i valori predefiniti della migrazione 0005.
//
// I default stanno qui e non nella colonna di un INSERT parziale perché l'API
// deve poterli mostrare al client nella risposta alla creazione: un default
// applicato dal database si scopre solo rileggendo la riga, e un default
// applicato in due posti diversi diverge.
func NewJob() Job {
	return Job{
		Timezone:       "UTC",
		Environments:   []Environment{EnvironmentProduction},
		Method:         MethodPOST,
		Headers:        map[string]string{},
		Timeout:        30 * time.Second,
		MaxRetries:     3,
		RetryBackoff:   BackoffExponential,
		AlertOnFailure: []AlertChannel{AlertEmail},
		Enabled:        true,
	}
}

// Managed indica che il job è la proiezione di una voce di `cron.yaml`.
//
// Un job gestito non si modifica né si cancella dall'API: la riconciliazione
// (R13) riporterebbe lo stato del file al primo push successivo, e la modifica
// dell'utente sparirebbe senza un errore. L'unica eccezione è la pausa
// (`enabled`), che la 0005 tiene deliberatamente distinta da `archived_at`
// proprio perché deve sopravvivere al sync.
func (j Job) Managed() bool { return j.RepositoryID != "" }

// Archived indica un job disattivato dalla riconciliazione.
func (j Job) Archived() bool { return j.ArchivedAt != nil }

// Spec è la schedulazione del job nella forma che [schedule.Parse] accetta.
func (j Job) Spec() schedule.Spec {
	return schedule.Spec{Expression: j.Schedule, Every: j.Every, Timezone: j.Timezone}
}

// Runnable indica se il job può produrre occorrenze: né in pausa né archiviato.
func (j Job) Runnable() bool { return j.Enabled && !j.Archived() }

// HasEnvironment indica se il job vive nell'ambiente indicato.
func (j Job) HasEnvironment(env Environment) bool {
	for _, e := range j.Environments {
		if e == env {
			return true
		}
	}
	return false
}

// Execution è un tentativo di esecuzione (R6).
//
// La chiave è la quaterna naturale (JobID, ScheduledFor, Environment, Attempt),
// come nella 0006: è anche il lock di idempotenza (R4), ed è ciò su cui si
// appoggia il tetto al trigger manuale — vedi [Service.Trigger].
type Execution struct {
	JobID        string
	ScheduledFor time.Time
	Environment  Environment
	Attempt      int

	Status      ExecutionStatus
	TriggeredBy ExecutionTrigger

	StartedAt  *time.Time
	FinishedAt *time.Time
	DurationMS *int

	ResponseStatus  *int
	ResponseExcerpt string
	Error           string

	CreatedAt time.Time
}

// Page è una pagina di risultati con il cursore per la successiva.
//
// NextCursor vuoto significa «non c'è altro». La forma è la stessa per i job e
// per le esecuzioni: su un job a 1 secondo sono 86.400 righe al giorno, e una
// paginazione che cambia forma a seconda della risorsa costringe il client a
// scrivere due volte lo stesso ciclo.
type Page[T any] struct {
	Items      []T
	NextCursor string
	Limit      int
}
