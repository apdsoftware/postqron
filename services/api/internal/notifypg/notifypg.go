// Package notifypg è l'implementazione PostgreSQL di [notify.Queue].
//
// Sta in un package a parte per la stessa ragione di internal/billingpg e
// internal/paddlepg: internal/notify non deve dipendere da pgx, ed è ciò che
// permette di provare la politica anti-spam, la scelta della lingua e l'assenza
// di segreti nel corpo senza un database in piedi.
//
// Qui non c'è politica: ci sono le query, e le due proprietà che **solo il
// database può garantire**.
//
// # La deduplicazione è dell'indice, non di un `if`
//
// L'accodamento è **una sola istruzione**. Non è una preferenza di stile: un job
// che fallisce ogni secondo produce fallimenti concorrenti per costruzione, e un
// «esiste già un avviso? no? allora inseriscilo» lascerebbe passare due
// connessioni insieme — cioè fallirebbe esattamente nel caso denso, che è
// l'unico da cui la politica difende. L'unicità la fa
// `notifications_dedupe_key_idx` della 0008, e l'`ON CONFLICT` decide cosa fare
// del secondo arrivo: se l'avviso è ancora in coda ne alza il conteggio, se è
// già partito non fa niente.
//
// # La presa in carico è un contratto a scadenza
//
// [Store.Due] non segna le righe «in lavorazione»: le prende con
// `FOR UPDATE SKIP LOCKED` e ne sposta avanti la ripresa. Un corriere che muore
// a metà non lascia una notifica bloccata per sempre — alla scadenza torna in
// coda da sola — e due corrieri sulla stessa macchina non si prendono la stessa
// riga. Il prezzo, dichiarato, è che una notifica possa partire due volte se il
// recapito dura più della scadenza.
//
// # Il destinatario si legge adesso
//
// Indirizzo, nome e lingua arrivano dalla stessa query che prende la riga, e non
// vengono conservati nella coda. Un avviso accodato stanotte e recapitato
// stamattina va all'indirizzo di adesso, nella lingua di adesso (R33): copiarli
// all'accodamento significherebbe scrivere a un indirizzo che l'utente ha
// cambiato, e in una lingua che non usa più.
package notifypg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/notify"
)

// maxErrorLen limita quanto si scrive nella colonna `error`.
//
// Il testo arriva dall'errore del client Mailronix, che può incorporare
// l'estratto di una risposta non prevista — la pagina di blocco di Cloudflare,
// per esempio, che è lunga a piacere. Una colonna di diagnostica non è il posto
// in cui conservare una pagina HTML.
const maxErrorLen = 1000

// Store implementa [notify.Queue] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("notifypg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ notify.Queue = (*Store)(nil)

// NewService compone il servizio di R21 sul pool dato.
func NewService(pool *pgxpool.Pool, logger *slog.Logger) (*notify.Service, error) {
	store, err := New(pool)
	if err != nil {
		return nil, err
	}
	return notify.NewService(notify.Options{Queue: store, Logger: logger})
}

// ---------------------------------------------------------------- accodamento

// enqueueSQL accoda una notifica, deduplicata, con il destinatario verificato.
//
// Le tre parti, in ordine:
//
//   - `target` è la verifica del destinatario. Un account cancellato o
//     logicamente chiuso non riceve niente, e un avviso di job fallito si
//     accoda solo se **quel job ha chiesto gli avvisi via email**
//     (`jobs.alert_on_failure`, R21): un array vuoto è una scelta legittima per
//     un job rumoroso, e va rispettata qui, prima di scrivere la riga.
//   - l'`INSERT ... ON CONFLICT` è il raggruppamento. Sul conflitto si somma il
//     conteggio dei fallimenti **solo se l'avviso è ancora in coda**: se è già
//     partito non c'è niente da aggiornare e non deve nascerne un secondo.
//   - il `SELECT` finale distingue i tre esiti. `xmax = 0` è vero solo per una
//     riga appena inserita; un `NULL` significa che l'`ON CONFLICT` non ha
//     toccato niente, cioè che l'avviso di quella finestra è già partito.
const enqueueSQL = `
WITH target AS (
    SELECT u.id AS user_id
      FROM users u
     WHERE u.id = $1::uuid
       AND u.deleted_at IS NULL
       AND ($4::uuid IS NULL OR EXISTS (
             SELECT 1 FROM jobs j
              WHERE j.id = $4::uuid
                AND j.user_id = u.id
                AND 'email' = ANY (j.alert_on_failure)))
),
upserted AS (
    INSERT INTO notifications
        (user_id, event, channel, job_id, environment,
         execution_scheduled_for, execution_attempt, dedupe_key, payload, scheduled_at)
    SELECT t.user_id, $2::text::notification_event, $3::text::alert_channel,
           $4::uuid, $5::text::environment,
           $6::timestamptz, $7::smallint, $8::text, $9::jsonb, $10::timestamptz
      FROM target t
    ON CONFLICT (dedupe_key) WHERE dedupe_key IS NOT NULL
    DO UPDATE SET payload = jsonb_set(
             notifications.payload, '{failures}',
             to_jsonb(coalesce((notifications.payload ->> 'failures')::int, 0)
                    + coalesce((excluded.payload ->> 'failures')::int, 0)))
     WHERE notifications.status = 'pending'
    RETURNING (xmax = 0) AS inserted
)
SELECT EXISTS (SELECT 1 FROM target) AS has_target,
       (SELECT inserted FROM upserted) AS inserted`

// Enqueue implementa [notify.Queue].
func (s *Store) Enqueue(ctx context.Context, req notify.Request) (notify.Result, error) {
	payload, err := json.Marshal(req.Payload)
	if err != nil {
		return "", fmt.Errorf("notifypg: serializzazione del payload: %w", err)
	}

	channel := req.Channel
	if channel == "" {
		channel = notify.ChannelEmail
	}

	// Il vincolo `notifications_execution_ref_check` della 0008 esige che
	// l'istante e il tentativo dell'esecuzione ci siano entrambi o nessuno dei
	// due: metà riferimento non individua niente.
	var scheduledFor *time.Time
	var attempt *int16
	if !req.ExecutionScheduledFor.IsZero() && req.ExecutionAttempt >= 1 {
		when := req.ExecutionScheduledFor.UTC()
		number := int16(req.ExecutionAttempt)
		scheduledFor, attempt = &when, &number
	}

	scheduledAt := req.ScheduledAt
	if scheduledAt.IsZero() {
		scheduledAt = time.Now()
	}

	var hasTarget bool
	var inserted *bool
	err = s.pool.QueryRow(ctx, enqueueSQL,
		req.UserID,
		string(req.Event),
		string(channel),
		nullUUID(req.JobID),
		nullText(req.Environment),
		scheduledFor,
		attempt,
		nullText(req.DedupeKey),
		payload,
		scheduledAt.UTC(),
	).Scan(&hasTarget, &inserted)
	if err != nil {
		return "", fmt.Errorf("notifypg: accodamento della notifica: %w", err)
	}

	switch {
	case !hasTarget:
		return notify.NoRecipient, nil
	case inserted != nil && *inserted:
		return notify.Queued, nil
	default:
		return notify.Grouped, nil
	}
}

// ------------------------------------------------------------- presa in carico

// dueSQL prende in carico le notifiche pronte e ne restituisce il destinatario.
//
// `FOR UPDATE SKIP LOCKED` è ciò che permette a più corrieri di lavorare la
// stessa coda senza prendersi le stesse righe; l'aggiornamento di
// `scheduled_at` è il contratto a scadenza descritto nella doc del package.
//
// La riga resta `pending` per tutta la lavorazione, ed è deliberato: non esiste
// uno stato «in corso» da cui un processo morto non uscirebbe più.
const dueSQL = `
WITH due AS (
    SELECT n.id
      FROM notifications n
     WHERE n.status = 'pending'
       AND n.channel = 'email'
       AND n.scheduled_at <= $1::timestamptz
     ORDER BY n.scheduled_at
     LIMIT $2
     FOR UPDATE SKIP LOCKED
)
UPDATE notifications n
   SET attempts = n.attempts + 1,
       scheduled_at = $1::timestamptz + $3::interval
  FROM due d, users u
 WHERE n.id = d.id
   AND u.id = n.user_id
RETURNING n.id::text, n.event::text, n.attempts,
          coalesce(n.job_id::text, ''), coalesce(n.environment::text, ''), n.payload,
          u.id::text, u.email, coalesce(u.full_name, ''), u.language,
          (u.deleted_at IS NOT NULL)`

// Due implementa [notify.Queue].
func (s *Store) Due(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]notify.Pending, error) {
	if limit <= 0 {
		return nil, nil
	}
	if lease <= 0 {
		lease = time.Minute
	}

	rows, err := s.pool.Query(ctx, dueSQL, now.UTC(), limit, lease)
	if err != nil {
		return nil, fmt.Errorf("notifypg: lettura delle notifiche pronte: %w", err)
	}
	defer rows.Close()

	var pending []notify.Pending
	for rows.Next() {
		var (
			p       notify.Pending
			event   string
			payload []byte
		)
		if err := rows.Scan(
			&p.ID, &event, &p.Attempts,
			&p.JobID, &p.Environment, &payload,
			&p.Recipient.UserID, &p.Recipient.Email, &p.Recipient.Name,
			&p.Recipient.Language, &p.Recipient.Closed,
		); err != nil {
			return nil, fmt.Errorf("notifypg: lettura di una notifica: %w", err)
		}
		p.Event = notify.Event(event)
		// La colonna ha un CHECK sulle cinque lingue, ma la normalizzazione è
		// gratuita e toglie di mezzo il caso di una riga scritta prima del
		// vincolo o da una migrazione futura.
		p.Recipient.Language = emailrender.NormalizeLanguage(p.Recipient.Language)

		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &p.Payload); err != nil {
				return nil, fmt.Errorf("notifypg: payload della notifica %s: %w", p.ID, err)
			}
		}
		pending = append(pending, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notifypg: lettura delle notifiche pronte: %w", err)
	}
	return pending, nil
}

// ------------------------------------------------------------------ chiusure

// MarkSent implementa [notify.Queue].
//
// Registra l'identificativo Mailronix **e nulla di più**: `sent` significa
// «consegnata a Mailronix», non «arrivata» (R20.1). Non c'è, e non va aggiunta,
// una colonna che dica il contrario.
func (s *Store) MarkSent(ctx context.Context, id, emailLogID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE notifications
		    SET status = 'sent', sent_at = $2, email_log_id = $3, error = NULL
		  WHERE id = $1::uuid`,
		id, at.UTC(), nullText(emailLogID))
	if err != nil {
		return fmt.Errorf("notifypg: chiusura della notifica %s: %w", id, err)
	}
	return nil
}

// Retry implementa [notify.Queue].
func (s *Store) Retry(ctx context.Context, id string, at time.Time, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE notifications
		    SET scheduled_at = $2, error = $3
		  WHERE id = $1::uuid AND status = 'pending'`,
		id, at.UTC(), truncate(reason))
	if err != nil {
		return fmt.Errorf("notifypg: rinvio della notifica %s: %w", id, err)
	}
	return nil
}

// MarkFailed implementa [notify.Queue].
func (s *Store) MarkFailed(ctx context.Context, id, reason string, at time.Time) error {
	return s.close(ctx, id, "failed", reason, at)
}

// MarkSkipped implementa [notify.Queue].
func (s *Store) MarkSkipped(ctx context.Context, id, reason string, at time.Time) error {
	return s.close(ctx, id, "skipped", reason, at)
}

func (s *Store) close(ctx context.Context, id, status, reason string, _ time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE notifications
		    SET status = $2::text::notification_status, error = $3
		  WHERE id = $1::uuid AND status = 'pending'`,
		id, status, truncate(reason))
	if err != nil {
		return fmt.Errorf("notifypg: chiusura della notifica %s come %s: %w", id, status, err)
	}
	return nil
}

// ---------------------------------------------------------------- utilità

// nullText traduce la stringa vuota in NULL. Le colonne interessate hanno un
// CHECK `<> ”`: la stringa vuota le farebbe fallire, e comunque significa
// «assente», che in SQL si scrive NULL.
func nullText(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

// nullUUID è nullText per le colonne uuid.
func nullUUID(v string) *string { return nullText(v) }

// truncate accorcia il testo di diagnostica. Vedi [maxErrorLen].
//
// Il taglio è per rune e non per byte: un errore che nomina un carattere
// accentato finirebbe altrimenti con mezza sequenza UTF-8, e PostgreSQL
// rifiuterebbe l'intero aggiornamento — cioè perderebbe la diagnosi proprio nel
// caso in cui serve.
func truncate(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if runes := []rune(v); len(runes) > maxErrorLen {
		v = string(runes[:maxErrorLen]) + "…"
	}
	return &v
}
