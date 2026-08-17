package dispatch

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// maxTextLength è il tetto che lo schema impone a `response_excerpt` e a `error`
// (migrazione 0006), in caratteri.
//
// Il troncamento sta qui, nel punto che scrive, e non in chi esegue: è un
// vincolo della tabella, e un CHECK violato farebbe perdere l'intero esito di
// un'esecuzione — cioè il fatto che è avvenuta e com'è andata — per colpa di
// una risposta troppo lunga.
const maxTextLength = 8192

// occurrenceWhere indirizza l'occorrenza per chiave naturale: è la stessa
// quaterna che su `job_executions` è chiave primaria, quindi ogni statement di
// questo file è un accesso puntuale sull'indice che esiste già.
const occurrenceWhere = `
	 WHERE job_id = $1::uuid
	   AND scheduled_for = $2
	   AND environment = $3::environment
	   AND attempt = $4`

// claimSQL è il secondo cancello dell'idempotenza (R4).
//
// `AND status = 'pending'` non è una precauzione: è **la** condizione. Lo
// scheduler può consegnare la stessa occorrenza due volte — lo dichiara la terza
// clausola di [scheduler.Dispatcher], ed è così che un riavvio non perde lavoro
// — e senza questo `WHERE` due processi chiamerebbero due volte il bersaglio
// dell'utente. Si esegue solo se questo UPDATE ha toccato una riga.
//
// `started_at` lo scrive PostgreSQL: l'istante di inizio deve venire dallo
// stesso orologio di `finished_at`, altrimenti `duration_ms` — che è una colonna
// generata dalla differenza — misurerebbe anche lo scarto fra due macchine.
const claimSQL = `
	UPDATE job_executions
	   SET status = 'running', started_at = now()` + occurrenceWhere + `
	   AND status = 'pending'`

// finishSQL scrive l'esito. La condizione su `running` fa il paio con quella di
// [claimSQL]: si chiude solo ciò che si è preso. Se la riga è stata rilasciata
// dall'arresto nel frattempo, questo UPDATE non tocca niente e il pool lo dice.
const finishSQL = `
	UPDATE job_executions
	   SET status = $5::execution_status,
	       finished_at = now(),
	       response_status = $6,
	       response_excerpt = $7,
	       error = $8` + occurrenceWhere + `
	   AND status = 'running'`

// skipSQL chiude un'occorrenza che non è mai partita. `started_at` e
// `finished_at` restano NULL: è la forma che il vincolo
// `job_executions_terminal_check` riserva a `skipped`, e una durata su
// un'esecuzione mai avvenuta sarebbe un numero inventato.
const skipSQL = `
	UPDATE job_executions
	   SET status = 'skipped', error = $5` + occurrenceWhere + `
	   AND status = 'pending'`

// releaseSQL riporta a `pending` un'occorrenza presa e non portata a termine.
//
// `started_at` torna NULL perché il tentativo che quell'istante descriveva non
// esiste più: la riga è di nuovo in attesa, e chi la riprenderà scriverà il
// proprio. Da qui la ritrova il recupero dello scheduler, che legge le righe
// `pending` per `created_at` — colonna che questo UPDATE non tocca — dentro
// `job_executions_in_flight_idx`.
const releaseSQL = `
	UPDATE job_executions
	   SET status = 'pending', started_at = NULL` + occurrenceWhere + `
	   AND status = 'running'`

// PostgresStore è [Store] su PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore costruisce lo store sul pool di connessioni del servizio.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

var _ Store = (*PostgresStore)(nil)

// key sono i quattro argomenti della chiave naturale, nell'ordine di
// [occurrenceWhere].
func key(occ scheduler.Occurrence) []any {
	return []any{occ.Job.ID, occ.ScheduledFor, occ.Environment, occ.Attempt}
}

// Claim porta l'occorrenza da `pending` a `running`.
func (s *PostgresStore) Claim(ctx context.Context, occ scheduler.Occurrence) (bool, error) {
	tag, err := s.pool.Exec(ctx, claimSQL, key(occ)...)
	if err != nil {
		return false, fmt.Errorf("dispatch: presa di %s: %w", occ, err)
	}
	return updated(tag), nil
}

// Finish scrive l'esito di un'occorrenza presa da questo pool.
func (s *PostgresStore) Finish(ctx context.Context, occ scheduler.Occurrence, rec Record) (bool, error) {
	args := append(key(occ),
		string(rec.Outcome),
		responseStatus(rec.ResponseStatus),
		// I due testi escono dal loro tipo solo qui, sul bordo del database: fino a
		// questa riga sono [secrets.Excerpt], cioè testo che è passato dalla
		// redazione (R43, issue #496).
		text(rec.ResponseExcerpt.String()),
		text(rec.Error.String()),
	)
	tag, err := s.pool.Exec(ctx, finishSQL, args...)
	if err != nil {
		return false, fmt.Errorf("dispatch: esito di %s: %w", occ, err)
	}
	return updated(tag), nil
}

// Skip chiude come `skipped` un'occorrenza mai partita.
func (s *PostgresStore) Skip(ctx context.Context, occ scheduler.Occurrence, reason string) (bool, error) {
	tag, err := s.pool.Exec(ctx, skipSQL, append(key(occ), text(reason))...)
	if err != nil {
		return false, fmt.Errorf("dispatch: chiusura di %s come saltata: %w", occ, err)
	}
	return updated(tag), nil
}

// Release riporta l'occorrenza a `pending`.
func (s *PostgresStore) Release(ctx context.Context, occ scheduler.Occurrence) (bool, error) {
	tag, err := s.pool.Exec(ctx, releaseSQL, key(occ)...)
	if err != nil {
		return false, fmt.Errorf("dispatch: rilascio di %s: %w", occ, err)
	}
	return updated(tag), nil
}

func updated(tag pgconn.CommandTag) bool { return tag.RowsAffected() > 0 }

// responseStatus tiene lo status dentro l'intervallo che la colonna accetta.
// Fuori da lì vale «nessuna risposta»: perdere lo status è meno grave che
// perdere l'esecuzione per un CHECK violato.
func responseStatus(status int) *int16 {
	if status < 100 || status > 599 {
		return nil
	}
	v := int16(status)
	return &v
}

// text prepara un testo per una colonna che ammette NULL e ha un tetto di
// lunghezza: vuoto diventa NULL, troppo lungo viene troncato con un segno
// visibile che dice che manca qualcosa.
func text(s string) *string {
	if s == "" {
		return nil
	}
	if utf8.RuneCountInString(s) > maxTextLength {
		runes := []rune(s)
		s = string(runes[:maxTextLength-1]) + "…"
	}
	return &s
}
