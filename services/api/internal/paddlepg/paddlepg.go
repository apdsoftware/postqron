// Package paddlepg è l'implementazione PostgreSQL di paddle.Store.
//
// Sta in un package a parte per la stessa ragione di internal/githubhookpg:
// internal/paddle non deve dipendere da pgx, ed è ciò che permette di provare il
// rifiuto di una firma sbagliata e l'assenza di doppio effetto senza un database
// in piedi. Qui non c'è logica: solo le due query su cui poggia l'idempotenza di
// R16, sui vincoli che la migrazione 0013 già garantisce.
package paddlepg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// Store implementa [paddle.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("paddlepg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ paddle.Store = (*Store)(nil)

// NewService compone il servizio di R16 sul pool dato.
//
// **Restituisce (nil, nil) se il segreto del webhook non è configurato.** Non è
// un errore: è la macchina di sviluppo di chi non ha Paddle, e la risposta
// giusta è non registrare la rotta — non registrarne una che accetta tutto. Su
// un endpoint di fatturazione la differenza fra le due è un piano a pagamento
// regalato a chiunque conosca l'indirizzo.
//
// `sink` è il consumatore degli entitlement (internal/billing). Può essere nil,
// e allora gli eventi vengono verificati e registrati senza essere applicati.
func NewService(pool *pgxpool.Pool, getenv func(string) string, logger *slog.Logger, sink paddle.EntitlementSink) (*paddle.Service, error) {
	secret := paddle.SecretFromEnv(getenv)
	if secret == "" {
		return nil, nil
	}

	store, err := New(pool)
	if err != nil {
		return nil, err
	}
	return paddle.NewService(paddle.Options{
		Secret: secret,
		Store:  store,
		Sink:   sink,
		Logger: logger,
	})
}

// Claim registra l'evento e dice se è questa richiesta a doverlo lavorare.
//
// È **una sola istruzione**, e non una SELECT seguita da un INSERT, perché è
// esattamente il punto in cui l'idempotenza di R16 può rompersi: Paddle ripete
// le consegne, e due copie dello stesso evento possono arrivare insieme. Con due
// istruzioni separate entrambe leggerebbero «non c'è» e passerebbero entrambe;
// con un `INSERT ... ON CONFLICT` il vincitore lo decide il vincolo di chiave
// primaria, che è l'unico arbitro che le due richieste hanno in comune.
//
// La clausola `WHERE ... status = 'failed'` è il secondo pezzo del
// comportamento: un evento la cui lavorazione è fallita **dev'essere**
// rilavorabile, perché è il caso per cui Paddle ripete e quello che useremo
// ripetendo a mano dal cruscotto. Ogni altro stato — in lavorazione, applicato,
// ignorato — non aggiorna niente, l'UPDATE non tocca righe, il `RETURNING` non
// restituisce niente, e il chiamante vede «già visto».
func (s *Store) Claim(ctx context.Context, record paddle.Record) (bool, error) {
	receivedAt := record.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}

	var claimed bool
	err := s.pool.QueryRow(ctx,
		`INSERT INTO paddle_webhook_events
		     (event_id, event_type, status, occurred_at,
		      paddle_subscription_id, paddle_customer_id, received_at)
		 VALUES ($1, $2, 'received', $3, $4, $5, $6)
		 ON CONFLICT (event_id) DO UPDATE
		    SET status = 'received',
		        attempts = paddle_webhook_events.attempts + 1,
		        received_at = excluded.received_at,
		        processed_at = NULL,
		        error_message = NULL
		  WHERE paddle_webhook_events.status = 'failed'
		 RETURNING true`,
		record.ID,
		record.Type,
		record.OccurredAt,
		nullText(record.SubscriptionID),
		nullText(record.CustomerID),
		receivedAt,
	).Scan(&claimed)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("paddlepg: registrazione dell'evento: %w", err)
	}
	return claimed, nil
}

// Complete registra l'esito della lavorazione.
func (s *Store) Complete(ctx context.Context, eventID string, status paddle.Status, failure string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}

	// Il vincolo `paddle_webhook_events_error_check` esige un motivo quando lo
	// stato è `failed`. Vale la pena non farlo scoprire al database: un
	// fallimento senza motivo è comunque un fallimento da registrare.
	if status == paddle.StatusFailed && failure == "" {
		failure = "motivo non riportato"
	}

	// `processed_at` si valorizza solo su uno stato terminale: la colonna dice
	// quando la lavorazione è finita, e `received` significa che non lo è.
	var processedAt *time.Time
	if status != paddle.StatusReceived {
		processedAt = &at
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE paddle_webhook_events
		    SET status = $2::paddle_event_status,
		        error_message = $3,
		        processed_at = $4
		  WHERE event_id = $1`,
		eventID, string(status), nullText(failure), processedAt)
	if err != nil {
		return fmt.Errorf("paddlepg: aggiornamento dell'evento: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// La riga è stata registrata da Claim un istante prima: se non c'è più,
		// qualcuno l'ha cancellata sotto i piedi. Non è un caso da inghiottire.
		return fmt.Errorf("paddlepg: evento %q non trovato", eventID)
	}
	return nil
}

// Purge elimina gli eventi più vecchi di `grace` e restituisce quanti.
//
// Delega alla funzione della 0013 invece di riscrivere la condizione: la
// retention di questa tabella *è* la finestra in cui la deduplicazione vale, e
// due posti che la esprimono divergono.
func (s *Store) Purge(ctx context.Context, grace time.Duration) (int64, error) {
	if grace < 0 {
		return 0, errors.New("paddlepg: grace non può essere negativo")
	}
	var removed int64
	err := s.pool.QueryRow(ctx,
		`SELECT paddle_webhook_events_purge(make_interval(secs => $1))`, grace.Seconds()).Scan(&removed)
	if err != nil {
		return 0, fmt.Errorf("paddlepg: pulizia degli eventi: %w", err)
	}
	return removed, nil
}

// Le colonne facoltative della 0013 hanno vincoli che la stringa vuota viola
// (`paddle_subscription_id <> ”`): il valore assente si scrive NULL.
func nullText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
