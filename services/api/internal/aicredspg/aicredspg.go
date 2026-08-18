// Package aicredspg è l'implementazione PostgreSQL di aicreds.Store.
//
// Sta in un package a parte per la stessa ragione di internal/secretspg e
// internal/apikeypg: internal/aicreds non deve dipendere da pgx, ed è ciò che
// permette di provare che la chiave non esce e che la revoca cancella senza un
// database in piedi. Qui non c'è logica: solo le query, sui vincoli che le
// migrazioni 0007 e 0016 già garantiscono.
package aicredspg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/aicreds"
)

// Store implementa [aicreds.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("aicredspg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ aicreds.Store = (*Store)(nil)

// credentialColumns è l'elenco delle colonne lette da ogni SELECT che
// restituisce un [aicreds.Credential], nello stesso ordine in cui scanCredential
// le legge.
//
// **Non contiene `ciphertext` né `nonce`**, e non è un'omissione da correggere:
// tutto ciò che viene elencato, mostrato o loggato passa da qui, e il materiale
// cifrato lo legge la sola query che deve chiamare il fornitore. Una colonna
// aggiunta qui finirebbe in ogni risposta dell'API senza che nessuno l'abbia
// deciso.
const credentialColumns = `id::text, user_id::text, provider::text, coalesce(label, ''),
	key_version, last_used_at, revoked_at, created_at, updated_at`

// sealedColumns aggiunge il materiale cifrato. La usa **una query sola**:
// [Store.LiveByProvider], che precede una chiamata al fornitore.
const sealedColumns = credentialColumns + `, ciphertext, nonce`

// UpsertCredential registra la chiave di un provider, sostituendo quella viva.
//
// È un `INSERT ... ON CONFLICT` sull'indice parziale
// `ai_credentials_live_provider_key`, e non una lettura seguita da una scrittura:
// due richieste simultanee che incollano la stessa chiave devono produrre una
// riga sola, e a deciderlo dev'essere l'indice — non l'ordine in cui due
// goroutine hanno letto.
//
// La clausola `WHERE revoked_at IS NULL` sull'`ON CONFLICT` ripete il predicato
// dell'indice: senza, PostgreSQL non saprebbe quale indice parziale usare per
// risolvere il conflitto e rifiuterebbe la query.
func (s *Store) UpsertCredential(ctx context.Context, in aicreds.Sealed) (aicreds.Credential, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO ai_credentials
		     (user_id, provider, label, ciphertext, nonce, key_version, created_at)
		 VALUES ($1, $2::ai_provider, nullif($3, ''), $4, $5, $6, $7)
		 ON CONFLICT (user_id, provider) WHERE revoked_at IS NULL
		 DO UPDATE SET ciphertext  = excluded.ciphertext,
		               nonce       = excluded.nonce,
		               key_version = excluded.key_version,
		               label       = excluded.label
		 RETURNING `+credentialColumns,
		in.UserID, string(in.Provider), in.Label, in.Ciphertext, in.Nonce,
		int16(in.KeyVersion), in.CreatedAt)

	credential, err := scanCredential(row)
	if errors.Is(err, aicreds.ErrNotFound) {
		return aicreds.Credential{}, errors.New("aicredspg: INSERT su ai_credentials non ha restituito la riga")
	}
	return credential, err
}

// ListCredentials elenca le chiavi di un utente, dalla più recente.
//
// Le due varianti sono scritte per esteso invece di passare `includeRevoked`
// come parametro: la forma con `($2 OR revoked_at IS NULL)` impedisce a
// PostgreSQL di usare l'indice, che è la stessa ragione per cui lo fanno
// internal/secretspg e internal/apikeypg.
func (s *Store) ListCredentials(
	ctx context.Context, userID string, includeRevoked bool,
) ([]aicreds.Credential, error) {
	query := `SELECT ` + credentialColumns + `
	            FROM ai_credentials
	           WHERE user_id = $1
	           ORDER BY created_at DESC`
	if !includeRevoked {
		query = `SELECT ` + credentialColumns + `
		           FROM ai_credentials
		          WHERE user_id = $1 AND revoked_at IS NULL
		          ORDER BY created_at DESC`
	}

	rows, err := s.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("aicredspg: elenco delle chiavi AI: %w", err)
	}
	defer rows.Close()

	credentials := []aicreds.Credential{}
	for rows.Next() {
		credential, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		credentials = append(credentials, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("aicredspg: lettura delle chiavi AI: %w", err)
	}
	return credentials, nil
}

// LiveByProvider legge la chiave viva di un utente per un provider.
//
// `user_id = $1` e `revoked_at IS NULL` sono nella WHERE e non in un controllo
// applicativo: è la differenza fra «solo il proprietario legge la propria
// chiave, e solo finché è viva» come proprietà della query e come promessa del
// chiamante.
func (s *Store) LiveByProvider(
	ctx context.Context, userID string, provider aicreds.Provider,
) (aicreds.Sealed, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+sealedColumns+`
		   FROM ai_credentials
		  WHERE user_id = $1 AND provider = $2::ai_provider AND revoked_at IS NULL`,
		userID, string(provider))

	return scanSealed(row)
}

// RevokeCredential revoca una chiave **svuotandone il materiale cifrato**.
//
// Le due cose stanno nella stessa istruzione perché il vincolo
// `ai_credentials_revoked_is_empty_check` non ammette che si separino: una
// `UPDATE` che datasse soltanto la revoca verrebbe rifiutata dal database, ed è
// la garanzia che «revocata» non possa mai significare «con il materiale ancora
// lì».
func (s *Store) RevokeCredential(ctx context.Context, userID, credentialID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE ai_credentials
		    SET revoked_at = $3,
		        ciphertext = '\x'::bytea,
		        nonce      = '\x'::bytea
		  WHERE id = $2 AND user_id = $1 AND revoked_at IS NULL`,
		userID, credentialID, at)
	if err != nil {
		return fmt.Errorf("aicredspg: revoca della chiave AI: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return aicreds.ErrNotFound
	}
	return nil
}

// TouchCredential aggiorna `last_used_at`.
//
// Una riga che non c'è più non è un errore: la lettura e la registrazione
// dell'uso non sono nella stessa transazione, e una revoca arrivata fra le due
// è il comportamento giusto — non un guasto da segnalare.
func (s *Store) TouchCredential(ctx context.Context, credentialID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE ai_credentials SET last_used_at = $2 WHERE id = $1`, credentialID, at)
	if err != nil {
		return fmt.Errorf("aicredspg: aggiornamento di last_used_at: %w", err)
	}
	return nil
}

// ------------------------------------------------------------------ scansione

// scanner è ciò che pgx.Row e pgx.Rows hanno in comune.
type scanner interface {
	Scan(dest ...any) error
}

// Le due funzioni sono separate e non una sola parametrica, come in
// internal/secretspg: la forma lunga la usa una query sola, e tenerle distinte
// è ciò che rende visibile in un `grep` **quali** query leggono il materiale
// cifrato.

func scanCredential(row scanner) (aicreds.Credential, error) {
	var (
		credential aicreds.Credential
		provider   string
		version    int16
	)
	err := row.Scan(&credential.ID, &credential.UserID, &provider, &credential.Label,
		&version, &credential.LastUsedAt, &credential.RevokedAt,
		&credential.CreatedAt, &credential.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return aicreds.Credential{}, aicreds.ErrNotFound
		}
		return aicreds.Credential{}, fmt.Errorf("aicredspg: lettura di ai_credentials: %w", err)
	}
	credential.Provider = aicreds.Provider(provider)
	credential.KeyVersion = uint16(version)
	return credential, nil
}

func scanSealed(row scanner) (aicreds.Sealed, error) {
	var (
		sealed   aicreds.Sealed
		provider string
		version  int16
	)
	err := row.Scan(&sealed.ID, &sealed.UserID, &provider, &sealed.Label,
		&version, &sealed.LastUsedAt, &sealed.RevokedAt,
		&sealed.CreatedAt, &sealed.UpdatedAt, &sealed.Ciphertext, &sealed.Nonce)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return aicreds.Sealed{}, aicreds.ErrNotFound
		}
		return aicreds.Sealed{}, fmt.Errorf("aicredspg: lettura di ai_credentials: %w", err)
	}
	sealed.Provider = aicreds.Provider(provider)
	sealed.KeyVersion = uint16(version)
	return sealed, nil
}
