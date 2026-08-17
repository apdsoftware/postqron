// Package githubhookpg è l'implementazione PostgreSQL di githubhook.Store.
//
// Sta in un package a parte per la stessa ragione di internal/apikeypg:
// internal/githubhook non deve dipendere da pgx, ed è ciò che permette di
// provare il rifiuto di una firma sbagliata senza un database in piedi. Qui non
// c'è logica: solo le due query su cui poggia l'idempotenza di R11, sui vincoli
// che la migrazione 0011 già garantisce.
package githubhookpg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
)

// Store implementa [githubhook.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("githubhookpg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ githubhook.Store = (*Store)(nil)

// NewService compone il servizio di R11 sul pool dato.
//
// Esiste perché mettere insieme lo store, il segreto dell'ambiente e il
// consumatore è l'unica cosa che il chiamante dovrebbe fare, e farla in tre
// righe sparse nel `main` è il modo in cui una di quelle righe finisce per
// mancare. `sink` è il consumatore delle push (#422): oggi è nil, e le push
// vengono verificate e registrate senza essere sincronizzate.
//
// **Restituisce (nil, nil) se il segreto del webhook non è configurato.** Non è
// un errore: è la macchina di sviluppo di chi non ha la GitHub App, e la
// risposta giusta è non registrare la rotta — non registrarne una che accetta
// tutto. Chi la riceve nil lo dice nel log e prosegue; è ciò che fa
// [httpapi.NewRouter].
func NewService(pool *pgxpool.Pool, getenv func(string) string, logger *slog.Logger, sink githubhook.PushSink) (*githubhook.Service, error) {
	secret := githubhook.SecretFromEnv(getenv)
	if secret == "" {
		return nil, nil
	}

	store, err := New(pool)
	if err != nil {
		return nil, err
	}
	return githubhook.NewService(githubhook.Options{
		Secret: secret,
		Store:  store,
		Sink:   sink,
		Logger: logger,
	})
}

// Claim registra la consegna e dice se è questa richiesta a doverla lavorare.
//
// È **una sola istruzione**, e non una SELECT seguita da un INSERT, perché è
// esattamente il punto in cui l'idempotenza di R11 può rompersi: GitHub ripete
// le consegne, e due copie della stessa possono arrivare insieme. Con due
// istruzioni separate entrambe leggerebbero «non c'è» e passerebbero entrambe;
// con un `INSERT ... ON CONFLICT` il vincitore lo decide il vincolo di chiave
// primaria, che è l'unico arbitro che le due richieste hanno in comune.
//
// La clausola `WHERE ... status = 'failed'` è il secondo pezzo del
// comportamento: una consegna la cui lavorazione è fallita **dev'essere**
// rilavorabile, perché è il caso per cui GitHub ripete e quello che useremo
// ripetendo a mano dal registro dell'App. Ogni altro stato — in lavorazione,
// conclusa, ignorata — non aggiorna niente, l'UPDATE non tocca righe, il
// `RETURNING` non restituisce niente, e il chiamante vede «già vista».
func (s *Store) Claim(ctx context.Context, delivery githubhook.Delivery) (bool, error) {
	receivedAt := delivery.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}

	var claimed bool
	err := s.pool.QueryRow(ctx,
		`INSERT INTO github_webhook_deliveries
		     (delivery_id, event, status, installation_id, repository_external_id,
		      repository_full_name, ref, head_commit, received_at)
		 VALUES ($1, $2, 'received', $3, $4, $5, $6, $7, $8)
		 ON CONFLICT (delivery_id) DO UPDATE
		    SET status = 'received',
		        attempts = github_webhook_deliveries.attempts + 1,
		        received_at = excluded.received_at,
		        processed_at = NULL,
		        error_message = NULL
		  WHERE github_webhook_deliveries.status = 'failed'
		 RETURNING true`,
		delivery.ID,
		delivery.Event,
		nullInt64(delivery.InstallationID),
		nullInt64(delivery.RepositoryExternalID),
		nullText(delivery.RepositoryFullName),
		nullText(delivery.Ref),
		nullText(delivery.HeadCommit),
		receivedAt,
	).Scan(&claimed)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("githubhookpg: registrazione della consegna: %w", err)
	}
	return claimed, nil
}

// Complete registra l'esito della lavorazione.
func (s *Store) Complete(ctx context.Context, deliveryID string, status githubhook.Status, failure string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}

	// Il vincolo `github_webhook_deliveries_error_check` esige un motivo quando
	// lo stato è `failed`. Vale la pena non farlo scoprire al database: un
	// fallimento senza motivo è comunque un fallimento da registrare.
	if status == githubhook.StatusFailed && failure == "" {
		failure = "motivo non riportato"
	}

	// `processed_at` si valorizza solo su uno stato terminale: la colonna dice
	// quando la lavorazione è finita, e `received` significa che non lo è.
	var processedAt *time.Time
	if status != githubhook.StatusReceived {
		processedAt = &at
	}

	tag, err := s.pool.Exec(ctx,
		`UPDATE github_webhook_deliveries
		    SET status = $2::github_delivery_status,
		        error_message = $3,
		        processed_at = $4
		  WHERE delivery_id = $1`,
		deliveryID, string(status), nullText(failure), processedAt)
	if err != nil {
		return fmt.Errorf("githubhookpg: aggiornamento della consegna: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// La riga è stata registrata da Claim un istante prima: se non c'è più,
		// qualcuno l'ha cancellata sotto i piedi. Non è un caso da inghiottire.
		return fmt.Errorf("githubhookpg: consegna %q non trovata", deliveryID)
	}
	return nil
}

// Purge elimina le consegne più vecchie di `grace` e restituisce quante.
//
// Delega alla funzione della 0011 invece di riscrivere la condizione: la
// retention di questa tabella *è* la finestra in cui la deduplicazione vale, e
// due posti che la esprimono divergono.
func (s *Store) Purge(ctx context.Context, grace time.Duration) (int64, error) {
	if grace < 0 {
		return 0, errors.New("githubhookpg: grace non può essere negativo")
	}
	var removed int64
	err := s.pool.QueryRow(ctx,
		`SELECT github_webhook_deliveries_purge(make_interval(secs => $1))`, grace.Seconds()).Scan(&removed)
	if err != nil {
		return 0, fmt.Errorf("githubhookpg: pulizia delle consegne: %w", err)
	}
	return removed, nil
}

// ------------------------------------------------------------------ supporto

// Le colonne facoltative della 0011 hanno vincoli che il valore zero viola
// (`installation_id > 0`, `ref <> ''`): il valore assente si scrive NULL, non
// zero.

func nullInt64(value int64) *int64 {
	if value <= 0 {
		return nil
	}
	return &value
}

func nullText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
