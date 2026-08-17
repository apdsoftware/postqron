// Package apikeypg è l'implementazione PostgreSQL di apikeys.Store.
//
// Sta in un package a parte per la stessa ragione di internal/authpg:
// internal/apikeys non deve dipendere da pgx, ed è ciò che permette di provare la
// revoca immediata, lo scope negato e il fatto che la chiave non sia
// ricostruibile senza un database in piedi. Qui non c'è logica: solo le query,
// sui vincoli che la migrazione 0002 già garantisce.
package apikeypg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
)

// uniqueViolation è lo SQLSTATE di una violazione di vincolo unico. Sull'INSERT
// in `api_keys` l'unico raggiungibile è `api_keys_key_hash_key`, quindi il
// codice basta e il nome del vincolo non va confrontato — vedi il commento
// omonimo in internal/authpg per il perché.
const uniqueViolation = "23505"

// Store implementa [apikeys.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("apikeypg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ apikeys.Store = (*Store)(nil)

// keyColumns è l'elenco delle colonne lette in ogni SELECT su `api_keys`, nello
// stesso ordine in cui scanKey le legge. Sta in una costante perché più query
// devono restare allineate: una colonna aggiunta in una sola è un errore che il
// compilatore non vede.
const keyColumns = `id::text, user_id::text, name, prefix, key_hash, scopes,
	last_used_at, expires_at, revoked_at, created_at`

// CreateKey registra una chiave nuova.
func (s *Store) CreateKey(ctx context.Context, in apikeys.Key) (apikeys.Key, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO api_keys (user_id, name, prefix, key_hash, scopes, expires_at, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+keyColumns,
		in.UserID, in.Name, in.Prefix, in.Hash, scopeParam(in.Scopes), in.ExpiresAt, in.CreatedAt)

	key, err := scanKey(row)
	if err == nil {
		return key, nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		// L'impronta è già in tabella. Con 256 bit di entropia questo non è una
		// collisione: è la stessa chiave inserita due volte, cioè un difetto del
		// chiamante, e va detto invece di essere inghiottito.
		return apikeys.Key{}, errors.New("apikeypg: impronta della chiave già presente")
	}
	if errors.Is(err, apikeys.ErrNotFound) {
		return apikeys.Key{}, errors.New("apikeypg: INSERT su api_keys non ha restituito la riga")
	}
	return apikeys.Key{}, err
}

// KeyByHash cerca una chiave per impronta.
//
// Una sola riga, trovata con un'uguaglianza sull'indice unico
// `api_keys_key_hash_key`: nessun filtro su `user_id`, nessuna scansione, e
// nessuna condizione su revoca e scadenza — quelle le valuta il Service, che è
// il posto in cui si possono provare senza un database.
func (s *Store) KeyByHash(ctx context.Context, hash string) (apikeys.Key, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+keyColumns+` FROM api_keys WHERE key_hash = $1`, hash)
	return scanKey(row)
}

// ListKeys elenca le chiavi di un utente, dalla più recente.
//
// Le due varianti sono scritte per esteso invece di passare `includeRevoked`
// come parametro alla query: la forma con `($2 OR revoked_at IS NULL)` impedisce
// a PostgreSQL di usare `api_keys_active_by_user_idx`, che è l'indice parziale
// che la 0002 ha creato esattamente per questa lettura.
func (s *Store) ListKeys(ctx context.Context, userID string, includeRevoked bool) ([]apikeys.Key, error) {
	query := `SELECT ` + keyColumns + `
	            FROM api_keys
	           WHERE user_id = $1 AND revoked_at IS NULL
	           ORDER BY created_at DESC`
	if includeRevoked {
		query = `SELECT ` + keyColumns + `
		           FROM api_keys
		          WHERE user_id = $1
		          ORDER BY created_at DESC`
	}

	rows, err := s.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("apikeypg: elenco delle chiavi: %w", err)
	}
	defer rows.Close()

	var keys []apikeys.Key
	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("apikeypg: elenco delle chiavi: %w", err)
	}
	return keys, nil
}

// RevokeKey revoca una chiave dell'utente indicato.
//
// `user_id = $1` è nella clausola WHERE e non in un controllo applicativo: è la
// differenza fra «solo il proprietario può revocare la chiave» come proprietà
// della query e come promessa del chiamante.
//
// `revoked_at IS NULL` rende l'operazione non idempotente di proposito: una
// seconda revoca restituisce [apikeys.ErrNotFound], e il Service la traduce in
// «chiave non trovata». Sovrascrivere la data sposterebbe in avanti il momento
// della revoca, che è l'unica informazione che questa riga porta.
func (s *Store) RevokeKey(ctx context.Context, userID, keyID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET revoked_at = $3
		  WHERE id = $2 AND user_id = $1 AND revoked_at IS NULL`,
		userID, keyID, at)
	if err != nil {
		return fmt.Errorf("apikeypg: revoca della chiave: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return apikeys.ErrNotFound
	}
	return nil
}

// TouchKey aggiorna `last_used_at`.
//
// La condizione sulla revoca evita che una richiesta autenticata appena prima di
// una revoca concorrente riscriva la riga di una chiave già spenta.
func (s *Store) TouchKey(ctx context.Context, keyID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET last_used_at = $2 WHERE id = $1 AND revoked_at IS NULL`, keyID, at)
	if err != nil {
		return fmt.Errorf("apikeypg: aggiornamento di last_used_at: %w", err)
	}
	return nil
}

// CountActiveKeys conta le chiavi vive di un utente.
func (s *Store) CountActiveKeys(ctx context.Context, userID string, now time.Time) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM api_keys
		  WHERE user_id = $1
		    AND revoked_at IS NULL
		    AND (expires_at IS NULL OR expires_at > $2)`, userID, now).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("apikeypg: conteggio delle chiavi: %w", err)
	}
	return count, nil
}

// ------------------------------------------------------------------ scansione

// scanner è ciò che pgx.Row e pgx.Rows hanno in comune.
type scanner interface {
	Scan(dest ...any) error
}

func scanKey(row scanner) (apikeys.Key, error) {
	var (
		key    apikeys.Key
		scopes []string
	)
	err := row.Scan(&key.ID, &key.UserID, &key.Name, &key.Prefix, &key.Hash, &scopes,
		&key.LastUsedAt, &key.ExpiresAt, &key.RevokedAt, &key.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apikeys.Key{}, apikeys.ErrNotFound
		}
		return apikeys.Key{}, fmt.Errorf("apikeypg: lettura di api_keys: %w", err)
	}

	// Gli scope si rileggono senza scartare quelli sconosciuti: se un giorno una
	// riga ne contiene uno che il codice non riconosce più — una colonna scritta
	// da una versione precedente — filtrarlo qui lo farebbe sparire in silenzio
	// dall'elenco mostrato al proprietario. Non autorizza niente comunque, perché
	// nessuna rotta lo richiede.
	key.Scopes = make([]apikeys.Scope, 0, len(scopes))
	for _, scope := range scopes {
		key.Scopes = append(key.Scopes, apikeys.Scope(scope))
	}
	return key, nil
}

// scopeParam converte gli scope nella forma che pgx passa a `text[]`.
func scopeParam(scopes []apikeys.Scope) []string {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		out = append(out, string(scope))
	}
	return out
}
