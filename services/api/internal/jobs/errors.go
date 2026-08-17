package jobs

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errori che lo Store restituisce e che il Service distingue.
var (
	// ErrNotFound indica che il job non esiste o non è dell'utente. I due casi
	// sono deliberatamente indistinguibili: la differenza direbbe a chiunque se
	// un identificativo altrui è vivo.
	ErrNotFound = errors.New("job non trovato")

	// ErrNameTaken indica che l'utente ha già un job con quel nome. È l'indice
	// unico `jobs_user_name_key` a deciderlo, non una SELECT preventiva: fra la
	// verifica e l'inserimento c'è una corsa che due richieste simultanee
	// vincono entrambe.
	ErrNameTaken = errors.New("nome già usato da un altro job")

	// ErrJobLimitReached indica che l'inserimento è stato rifiutato dal tetto di
	// piano applicato dentro la stessa istruzione dell'INSERT. È la rete di
	// sicurezza del conteggio fatto prima: quello produce il messaggio utile,
	// questo garantisce che due creazioni simultanee non sfondino il limite.
	ErrJobLimitReached = errors.New("numero massimo di job raggiunto")

	// ErrManaged indica un tentativo di modificare un job che appartiene a un
	// `cron.yaml` (R13). Vedi [Job.Managed].
	ErrManaged = errors.New("job gestito da un repository: modificalo nel cron.yaml")

	// ErrArchived indica un job disattivato dalla riconciliazione.
	ErrArchived = errors.New("job archiviato")

	// ErrDisabled indica un job in pausa. Un trigger manuale su un job in pausa
	// verrebbe marcato `skipped` dal motore al momento del dispatch: rifiutarlo
	// qui, con la ragione, è meglio che accettarlo e non eseguirlo.
	ErrDisabled = errors.New("job in pausa")

	// ErrExecutionExists indica un conflitto sulla chiave naturale di
	// `job_executions`: un'esecuzione per quella occorrenza c'è già.
	ErrExecutionExists = errors.New("esecuzione già registrata per questa occorrenza")

	// ErrPartitionMissing indica che manca la partizione giornaliera di
	// `job_executions` (0006). Non è un errore del client: è la manutenzione
	// periodica che non ha girato, e va detto come indisponibilità temporanea
	// invece che come guasto generico.
	ErrPartitionMissing = errors.New("partizione delle esecuzioni non disponibile")

	// ErrInvalidCursor indica un cursore di paginazione illeggibile.
	ErrInvalidCursor = errors.New("cursore di paginazione non valido")
)

// FieldError è un singolo motivo di rifiuto, ancorato al campo che lo causa.
//
// `Field` usa la notazione a punti del corpo JSON (`request.url`,
// `alerts.on_failure`): è ciò che permette a un form di evidenziare il campo
// giusto senza interpretare il messaggio, che è testo per una persona.
type FieldError struct {
	Field   string
	Code    string
	Message string
}

func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// ValidationError raccoglie tutti i motivi di rifiuto di una richiesta.
//
// Sono tutti, non il primo: un client che compila un form vuole sapere in un
// colpo solo cosa correggere, e restituirne uno per volta trasforma la
// creazione di un job in un dialogo a turni.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Error())
	}
	return "richiesta non valida: " + strings.Join(parts, "; ")
}

// add accoda un motivo di rifiuto.
func (e *ValidationError) add(field, code, format string, args ...any) {
	e.Fields = append(e.Fields, FieldError{
		Field:   field,
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	})
}

// orNil restituisce nil quando non c'è nessun motivo di rifiuto, così che il
// chiamante possa fare `return v.orNil()` senza un `if` che si dimentica.
func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// LimitKind è il limite di piano che ha rifiutato l'operazione (R15, SPEC §8).
//
// È un valore distinto e non un messaggio perché il client ci fa branching: un
// tetto ai job porta alla pagina di upgrade, una risoluzione troppo fine porta a
// correggere il campo, un trigger manuale troppo frequente porta ad aspettare.
type LimitKind string

// I limiti applicati da questo package.
const (
	// LimitJobs è il tetto al numero di job (SPEC §8: 20 su Free, 200 su Pro).
	LimitJobs LimitKind = "jobs"
	// LimitResolution è la risoluzione minima (1 minuto su Free, 10 secondi su
	// Pro, 1 secondo su Team e Agency).
	LimitResolution LimitKind = "resolution"
	// LimitEnvironments è la disponibilità di più ambienti: il piano Free ne ha
	// uno solo (SPEC §8, R23).
	LimitEnvironments LimitKind = "environments"
	// LimitManualTrigger è il tetto alla frequenza dei trigger manuali. Non è
	// una riga del listino: è derivato dagli altri due, vedi [Plan.ManualBudget].
	LimitManualTrigger LimitKind = "manual_trigger"
)

// PlanLimitError è il rifiuto dovuto a un limite di piano.
//
// Non degrada mai in silenzio: un `every: 1s` su piano Free viene rifiutato con
// il motivo, non arrotondato a un minuto (SPEC §9). Un limite applicato
// silenziosamente è indistinguibile, per l'utente, da un guasto del prodotto.
type PlanLimitError struct {
	// Limit è il limite violato.
	Limit LimitKind
	// Plan è il codice del piano dell'utente, così che il messaggio possa dire
	// «su Free» senza che il client debba chiederlo a un'altra rotta.
	Plan string
	// Field è il campo della richiesta responsabile, quando ce n'è uno.
	Field string
	// RetryAfter è il tempo dopo il quale l'operazione tornerà possibile.
	// Valorizzato solo per i limiti di frequenza.
	RetryAfter time.Duration

	message string
}

func (e *PlanLimitError) Error() string { return e.message }

// AsPlanLimit estrae un [PlanLimitError] dalla catena, se c'è.
func AsPlanLimit(err error) (*PlanLimitError, bool) {
	var limit *PlanLimitError
	if errors.As(err, &limit) {
		return limit, true
	}
	return nil, false
}

// AsValidation estrae un [ValidationError] dalla catena, se c'è.
func AsValidation(err error) (*ValidationError, bool) {
	var invalid *ValidationError
	if errors.As(err, &invalid) {
		return invalid, true
	}
	return nil, false
}
