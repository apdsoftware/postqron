package jobs

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Limiti di paginazione.
//
// Non sono cosmetici. Un job del piano Team con `every: 1s` produce 86.400 righe
// al giorno **per ambiente**: una lista senza tetto è una richiesta che nessuno
// riesce a servire e nessun client riesce a ricevere. Il tetto è basso di
// proposito, e il cursore è l'unico modo di andare oltre.
const (
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// Store è la persistenza di cui R8 ha bisogno.
//
// È un'interfaccia e non il pool di pgx per la stessa ragione di [auth.Store]:
// i comportamenti che questa issue deve garantire — rifiuto anticipato,
// applicazione dei limiti di piano, tetto al trigger manuale — si verificano sul
// Service, e devono restare provabili su una macchina senza `make db-up`.
//
// Gli identificativi sono stringhe (uuid in forma testuale): il tipo uuid di pgx
// non attraversa questo confine.
type Store interface {
	// CreateJob inserisce un job.
	//
	// `maxJobs`, se non nil, è il tetto di piano: l'inserimento deve avvenire
	// **solo se** il numero di job non archiviati dell'utente è inferiore, e
	// nella stessa istruzione. Il conteggio fatto prima dal Service produce il
	// messaggio utile; questo impedisce che due creazioni simultanee lo
	// sfondino entrambe. Restituisce [ErrJobLimitReached] se il tetto è pieno e
	// [ErrNameTaken] se il nome è già usato.
	CreateJob(ctx context.Context, job Job, maxJobs *int) (Job, error)

	// JobByID legge un job dell'utente. [ErrNotFound] se non esiste o è di un
	// altro: l'ambito sull'utente è parte del contratto, non un filtro che il
	// chiamante può dimenticare.
	JobByID(ctx context.Context, userID, jobID string) (Job, error)

	// ListJobs elenca i job dell'utente secondo il filtro, ordinati dal più
	// recente. Restituisce fino a `Limit + 1` righe: la riga in più è come il
	// Service sa che esiste una pagina successiva senza un secondo conteggio.
	ListJobs(ctx context.Context, filter JobFilter) ([]Job, error)

	// CountJobs conta i job non archiviati dell'utente (tetto di piano).
	CountJobs(ctx context.Context, userID string) (int, error)

	// CountActiveJobs conta i job **accesi** e non archiviati dell'utente.
	//
	// È un conteggio distinto da CountJobs e non un suo filtro perché risponde a
	// una domanda diversa: quanta capacità l'utente sta usando, non quanto è
	// grande il suo catalogo. Serve alla riaccensione dopo un downgrade (R58) —
	// vedi [Plan.CheckActiveJobCount], dove la differenza è spiegata per esteso.
	CountActiveJobs(ctx context.Context, userID string) (int, error)

	// UpdateJob riscrive le colonne modificabili di un job. Il job porta già
	// l'esito dell'applicazione della patch e della validazione: qui non si
	// decide più niente.
	//
	// `resetNextRun` azzera `jobs.next_run_at`; con `false` la colonna resta
	// **intatta** e `job.NextRunAt` viene ignorato. La distinzione esiste perché
	// quella colonna è dello scheduler (0005, 0010): riscriverla con il valore
	// letto poco prima sarebbe un aggiornamento perso ogni volta che il motore la
	// fa avanzare fra la lettura e la scrittura.
	UpdateJob(ctx context.Context, job Job, resetNextRun bool) (Job, error)

	// DeleteJob elimina un job dell'utente. Le esecuzioni seguono per
	// `ON DELETE CASCADE` (0006).
	DeleteJob(ctx context.Context, userID, jobID string) error

	// PlanForUser restituisce il piano dell'utente (R15). Senza sottoscrizione
	// viva restituisce il piano `free`.
	PlanForUser(ctx context.Context, userID string) (Plan, error)

	// PlanByCode legge una riga del listino per codice, indipendentemente da chi
	// la sottoscrive. [ErrNotFound] se quel codice non esiste.
	//
	// Serve a un solo caso, e non è generale per caso: R25-bis dice che la
	// portata di Agency è quella di Team applicata per workspace, e derivarla
	// richiede di leggere i numeri di un piano che non è quello dell'utente. Il
	// codice del piano di riferimento sta nel codice — lo nomina R25-bis — ma i
	// **numeri** restano nel database, che è la fonte di verità della matrice.
	PlanByCode(ctx context.Context, code string) (Plan, error)

	// ListExecutions elenca le esecuzioni di un job, dalla più recente. Come
	// ListJobs, restituisce fino a `Limit + 1` righe.
	ListExecutions(ctx context.Context, filter ExecutionFilter) ([]Execution, error)

	// ListExecutionsForward elenca le esecuzioni di un job **in avanti**: dalla
	// più antica alla più recente, a partire da `Since` incluso e da `Cursor`
	// escluso. Restituisce al massimo `Limit` righe, senza la riga in più di
	// ListExecutions: un flusso non ha una pagina successiva da annunciare, torna
	// a chiedere al giro dopo.
	//
	// È la lettura dello streaming (SPEC §4.2, R6) e cammina sullo stesso indice
	// di ListExecutions letto nell'altro verso — vedi internal/jobs/stream.go per
	// il motivo per cui i due versi sono due metodi e non un parametro: un
	// cursore che significasse «prima» o «dopo» a seconda di un booleano è
	// esattamente il tipo di ambiguità che produce righe saltate.
	ListExecutionsForward(ctx context.Context, filter ExecutionFilter) ([]Execution, error)

	// CreateExecution registra un tentativo. Restituisce [ErrExecutionExists] se
	// la chiave naturale è già occupata — che è il lock di idempotenza di R4 — e
	// [ErrPartitionMissing] se manca la partizione giornaliera.
	CreateExecution(ctx context.Context, exec Execution) (Execution, error)

	// ExecutionAt legge il tentativo su una chiave naturale. Serve a
	// distinguere, dopo un conflitto, un trigger manuale troppo ravvicinato da
	// un'occorrenza schedulata che occupava già l'istante.
	ExecutionAt(ctx context.Context, jobID string, scheduledFor time.Time, env Environment, attempt int) (Execution, error)
}

// JobFilter descrive una pagina dell'elenco dei job.
type JobFilter struct {
	UserID string

	// Enabled filtra sui job in pausa o attivi; nil non filtra.
	Enabled *bool
	// Environment filtra sui job che vivono in un ambiente; vuoto non filtra.
	Environment Environment
	// IncludeArchived include i job disattivati dalla riconciliazione (R13).
	// Sono esclusi per default: un job archiviato non è più nel `cron.yaml`
	// dell'utente, e mostrarlo nell'elenco normale confonderebbe.
	IncludeArchived bool

	Limit  int
	Cursor *JobCursor
}

// ExecutionFilter descrive una pagina del registro delle esecuzioni.
type ExecutionFilter struct {
	JobID string

	Status      []ExecutionStatus
	Environment Environment
	// TriggeredBy filtra per origine. È ciò che rende consultabile lo storico
	// dei trigger manuali (R8): non basta limitarli, devono essere contabili.
	TriggeredBy []ExecutionTrigger

	Since time.Time
	Until time.Time

	Limit  int
	Cursor *ExecutionCursor
}

// ClampLimit riporta un limite richiesto dentro i confini ammessi.
func ClampLimit(requested int) int {
	switch {
	case requested <= 0:
		return DefaultPageSize
	case requested > MaxPageSize:
		return MaxPageSize
	default:
		return requested
	}
}

// ------------------------------------------------------------------- cursori
//
// La paginazione è a chiave (keyset), non a offset. Con OFFSET, la pagina 50 di
// un registro che riceve 86.400 inserimenti al giorno richiede al database di
// scartare 2.500 righe per servirne 50, e — peggio — le righe scorrono fra una
// pagina e l'altra: chi legge all'indietro un registro che cresce salta ciò che
// è stato inserito nel frattempo. Un cursore ancorato alla chiave di
// ordinamento non ha nessuno dei due problemi.
//
// Il cursore è opaco per contratto (base64url di una tupla) ma non è un
// segreto: le query sono comunque limitate all'utente, quindi un cursore
// fabbricato a mano non dà accesso a niente che non fosse già accessibile.
// Opaco serve a poter cambiare la chiave di ordinamento senza rompere i client.

// JobCursor ancora la pagina successiva dell'elenco dei job.
//
// La chiave è (created_at, id) perché `jobs_user_id_idx` è esattamente
// (user_id, created_at DESC): l'ordinamento non costa un sort. L'id rompe la
// parità fra due job creati nello stesso microsecondo, che senza sarebbe un
// ordine non deterministico — e quindi righe saltate o ripetute fra due pagine.
type JobCursor struct {
	CreatedAt time.Time
	ID        string
}

// ExecutionCursor ancora la pagina successiva del registro.
//
// La chiave è la stessa quaterna che è chiave primaria in `job_executions`,
// meno il job che è già nel filtro: il cursore cammina sull'indice che esiste
// già.
type ExecutionCursor struct {
	ScheduledFor time.Time
	Environment  Environment
	Attempt      int
}

const (
	cursorKindJob       = "j1"
	cursorKindExecution = "e1"
)

// Encode serializza il cursore.
func (c JobCursor) Encode() string {
	return encodeCursor(cursorKindJob, strconv.FormatInt(c.CreatedAt.UTC().UnixNano(), 10), c.ID)
}

// Encode serializza il cursore.
func (c ExecutionCursor) Encode() string {
	return encodeCursor(cursorKindExecution,
		strconv.FormatInt(c.ScheduledFor.UTC().UnixNano(), 10),
		string(c.Environment),
		strconv.Itoa(c.Attempt))
}

// ParseJobCursor legge un cursore dell'elenco dei job.
func ParseJobCursor(raw string) (*JobCursor, error) {
	parts, err := decodeCursor(raw, cursorKindJob, 2)
	if err != nil {
		return nil, err
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	return &JobCursor{CreatedAt: time.Unix(0, nanos).UTC(), ID: parts[1]}, nil
}

// ParseExecutionCursor legge un cursore del registro delle esecuzioni.
func ParseExecutionCursor(raw string) (*ExecutionCursor, error) {
	parts, err := decodeCursor(raw, cursorKindExecution, 3)
	if err != nil {
		return nil, err
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	attempt, err := strconv.Atoi(parts[2])
	if err != nil || attempt < 1 {
		return nil, ErrInvalidCursor
	}
	return &ExecutionCursor{
		ScheduledFor: time.Unix(0, nanos).UTC(),
		Environment:  Environment(parts[1]),
		Attempt:      attempt,
	}, nil
}

func encodeCursor(kind string, fields ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(kind + "|" + strings.Join(fields, "|")))
}

func decodeCursor(raw, kind string, fields int) ([]string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != fields+1 || parts[0] != kind {
		return nil, ErrInvalidCursor
	}
	return parts[1:], nil
}
