// Package secretspg è l'implementazione PostgreSQL di secrets.Store.
//
// Sta in un package a parte per la stessa ragione di internal/authpg e
// internal/apikeypg: internal/secrets non deve dipendere da pgx, ed è ciò che
// permette di provare la risoluzione, la validazione al sync e il fatto che il
// valore non esca senza un database in piedi. Qui non c'è logica: solo le query,
// sui vincoli che la migrazione 0012 già garantisce.
package secretspg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// uniqueViolation è lo SQLSTATE di una violazione di vincolo unico. Sull'INSERT
// in `workspace_secrets` l'unico raggiungibile è
// `workspace_secrets_live_name_key`, quindi il codice basta e il nome del
// vincolo non va confrontato.
const uniqueViolation = "23505"

// Store implementa [secrets.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("secretspg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ secrets.Store = (*Store)(nil)

// secretColumns è l'elenco delle colonne lette da ogni SELECT che restituisce un
// [secrets.Secret], nello stesso ordine in cui scanSecret le legge.
//
// **Non contiene `ciphertext` né `nonce`**, e non è un'omissione da correggere:
// tutto ciò che viene elencato, mostrato o loggato passa da qui, e il materiale
// cifrato lo legge la sola query che deve risolvere un'esecuzione. Una colonna
// aggiunta qui finirebbe in ogni risposta dell'API senza che nessuno l'abbia
// deciso.
const secretColumns = `id::text, user_id::text, name, coalesce(description, ''),
	key_version, last_used_at, revoked_at, created_at, updated_at`

// sealedColumns aggiunge il materiale cifrato. La usa **una query sola**:
// [Store.LiveByNames], che serve la risoluzione all'esecuzione.
const sealedColumns = secretColumns + `, ciphertext, nonce`

// CreateSecret registra un segreto nuovo.
func (s *Store) CreateSecret(ctx context.Context, in secrets.Sealed) (secrets.Secret, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO workspace_secrets
		     (user_id, name, description, ciphertext, nonce, key_version, created_at)
		 VALUES ($1, $2, nullif($3, ''), $4, $5, $6, $7)
		 RETURNING `+secretColumns,
		in.UserID, in.Name, in.Description, in.Ciphertext, in.Nonce, int16(in.KeyVersion), in.CreatedAt)

	secret, err := scanSecret(row)
	if err == nil {
		return secret, nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return secrets.Secret{}, secrets.ErrDuplicateName
	}
	if errors.Is(err, secrets.ErrNotFound) {
		return secrets.Secret{}, errors.New("secretspg: INSERT su workspace_secrets non ha restituito la riga")
	}
	return secrets.Secret{}, err
}

// UpdateSecret sostituisce il valore cifrato, e la nota se `description` non è
// nil.
//
// `user_id = $1` e `revoked_at IS NULL` sono nella WHERE e non in un controllo
// applicativo: è la differenza fra «solo il proprietario può cambiare il valore,
// e solo finché il segreto è vivo» come proprietà della query e come promessa del
// chiamante. Riscrivere un segreto revocato lo farebbe tornare in vita violando
// il vincolo `workspace_secrets_revoked_is_empty_check`, cioè con un 500.
func (s *Store) UpdateSecret(
	ctx context.Context, userID, secretID string, in secrets.Sealed, description *string,
) (secrets.Secret, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE workspace_secrets
		    SET ciphertext = $3,
		        nonce = $4,
		        key_version = $5,
		        description = CASE WHEN $6::boolean THEN nullif($7, '') ELSE description END
		  WHERE id = $2 AND user_id = $1 AND revoked_at IS NULL
		 RETURNING `+secretColumns,
		userID, secretID, in.Ciphertext, in.Nonce, int16(in.KeyVersion),
		description != nil, stringOrEmpty(description))

	secret, err := scanSecret(row)
	if err != nil {
		return secrets.Secret{}, err
	}
	return secret, nil
}

// ListSecrets elenca i segreti di un workspace, dal più recente.
//
// Le due varianti sono scritte per esteso invece di passare `includeRevoked`
// come parametro: la forma con `($2 OR revoked_at IS NULL)` impedisce a
// PostgreSQL di usare l'indice, che è la stessa ragione per cui lo fa
// internal/apikeypg.
func (s *Store) ListSecrets(ctx context.Context, userID string, includeRevoked bool) ([]secrets.Secret, error) {
	query := `SELECT ` + secretColumns + `
	            FROM workspace_secrets
	           WHERE user_id = $1 AND revoked_at IS NULL
	           ORDER BY created_at DESC`
	if includeRevoked {
		query = `SELECT ` + secretColumns + `
		           FROM workspace_secrets
		          WHERE user_id = $1
		          ORDER BY created_at DESC`
	}

	rows, err := s.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("secretspg: elenco dei segreti: %w", err)
	}
	defer rows.Close()

	var out []secrets.Secret
	for rows.Next() {
		secret, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("secretspg: elenco dei segreti: %w", err)
	}
	return out, nil
}

// SecretByID legge un segreto vivo del workspace.
func (s *Store) SecretByID(ctx context.Context, userID, secretID string) (secrets.Secret, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+secretColumns+`
		   FROM workspace_secrets
		  WHERE id = $2 AND user_id = $1 AND revoked_at IS NULL`, userID, secretID)
	return scanSecret(row)
}

// LiveByNames restituisce i segreti vivi indicati, con il loro testo cifrato.
//
// È la query calda di R43: gira a ogni occorrenza e legge quasi sempre una riga.
// `name = ANY($2)` con `user_id = $1` è servita dall'indice unico
// `workspace_secrets_live_name_key`, che ha `user_id` come prefisso e non
// contiene le righe revocate — quindi la clausola sulla revoca non costa una
// riga letta e scartata: quelle righe nell'indice non ci sono.
//
// I nomi mancanti non sono un errore: distinguere «revocato» da «mai esistito»
// non serve a chi esegue, e a chi valida serve solo sapere che manca.
func (s *Store) LiveByNames(ctx context.Context, userID string, names []string) ([]secrets.Sealed, error) {
	if len(names) == 0 {
		return nil, nil
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+sealedColumns+`
		   FROM workspace_secrets
		  WHERE user_id = $1 AND revoked_at IS NULL AND name = ANY($2)`, userID, names)
	if err != nil {
		return nil, fmt.Errorf("secretspg: lettura dei segreti: %w", err)
	}
	defer rows.Close()

	var out []secrets.Sealed
	for rows.Next() {
		sealed, err := scanSealed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sealed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("secretspg: lettura dei segreti: %w", err)
	}
	return out, nil
}

// LiveNames elenca i nomi dei segreti vivi. È la lettura della validazione al
// sync: nessuna colonna cifrata, perché per dire che un nome non c'è non serve
// decifrare niente.
func (s *Store) LiveNames(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name FROM workspace_secrets
		  WHERE user_id = $1 AND revoked_at IS NULL
		  ORDER BY name`, userID)
	if err != nil {
		return nil, fmt.Errorf("secretspg: elenco dei nomi: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("secretspg: lettura di workspace_secrets: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("secretspg: elenco dei nomi: %w", err)
	}
	return names, nil
}

// RevokeSecret revoca un segreto **e ne cancella il valore**.
//
// La data e lo svuotamento sono nello stesso UPDATE perché il vincolo
// `workspace_secrets_revoked_is_empty_check` non ammette che si separino: non
// esiste un istante in cui la riga è revocata e il testo cifrato è ancora lì,
// nemmeno dentro una transazione più lunga.
//
// `revoked_at IS NULL` rende l'operazione non idempotente di proposito: una
// seconda revoca restituisce [secrets.ErrNotFound]. Sovrascrivere la data
// sposterebbe in avanti il momento della revoca, che è l'unica informazione che
// la riga porta ancora.
func (s *Store) RevokeSecret(ctx context.Context, userID, secretID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE workspace_secrets
		    SET revoked_at = $3, ciphertext = '\x'::bytea, nonce = '\x'::bytea
		  WHERE id = $2 AND user_id = $1 AND revoked_at IS NULL`,
		userID, secretID, at)
	if err != nil {
		return fmt.Errorf("secretspg: revoca del segreto: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

// TouchSecrets aggiorna `last_used_at`.
//
// La condizione sulla revoca evita che un'esecuzione partita appena prima di una
// revoca concorrente riscriva la riga di un segreto già spento.
func (s *Store) TouchSecrets(ctx context.Context, secretIDs []string, at time.Time) error {
	if len(secretIDs) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE workspace_secrets SET last_used_at = $2
		  WHERE id = ANY($1) AND revoked_at IS NULL`, secretIDs, at)
	if err != nil {
		return fmt.Errorf("secretspg: aggiornamento di last_used_at: %w", err)
	}
	return nil
}

// CountLiveSecrets conta i segreti vivi di un workspace.
func (s *Store) CountLiveSecrets(ctx context.Context, userID string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM workspace_secrets
		  WHERE user_id = $1 AND revoked_at IS NULL`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("secretspg: conteggio dei segreti: %w", err)
	}
	return count, nil
}

// ------------------------------------------------------------------ scansione

// scanner è ciò che pgx.Row e pgx.Rows hanno in comune.
type scanner interface {
	Scan(dest ...any) error
}

func scanSecret(row scanner) (secrets.Secret, error) {
	var (
		secret  secrets.Secret
		version int16
	)
	err := row.Scan(&secret.ID, &secret.UserID, &secret.Name, &secret.Description,
		&version, &secret.LastUsedAt, &secret.RevokedAt, &secret.CreatedAt, &secret.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return secrets.Secret{}, secrets.ErrNotFound
		}
		return secrets.Secret{}, fmt.Errorf("secretspg: lettura di workspace_secrets: %w", err)
	}
	secret.KeyVersion = uint16(version)
	return secret, nil
}

func scanSealed(row scanner) (secrets.Sealed, error) {
	var (
		sealed  secrets.Sealed
		version int16
	)
	err := row.Scan(&sealed.ID, &sealed.UserID, &sealed.Name, &sealed.Description,
		&version, &sealed.LastUsedAt, &sealed.RevokedAt, &sealed.CreatedAt, &sealed.UpdatedAt,
		&sealed.Ciphertext, &sealed.Nonce)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return secrets.Sealed{}, secrets.ErrNotFound
		}
		return secrets.Sealed{}, fmt.Errorf("secretspg: lettura di workspace_secrets: %w", err)
	}
	sealed.KeyVersion = uint16(version)
	return sealed, nil
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
