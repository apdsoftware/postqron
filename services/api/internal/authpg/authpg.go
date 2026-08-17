// Package authpg è l'implementazione PostgreSQL di auth.Store.
//
// Sta in un package a parte perché internal/auth non deve dipendere da pgx: è
// ciò che permette di provare l'enumerazione degli account, i tempi di risposta
// e il rate limiting senza un database in piedi. Qui, viceversa, non c'è logica:
// solo le query, e i vincoli che la migrazione 0009 già garantisce.
package authpg

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
)

// uniqueViolation è lo SQLSTATE di una violazione di vincolo unico.
//
// Il *nome* del vincolo non si confronta: la lezione della 0006 (vedi il README
// delle migrazioni) è che il nome che arriva può essere quello di una partizione
// o di un indice, e un confronto con una costante funziona nei test e sbaglia in
// produzione. Qui l'unico vincolo unico raggiungibile dall'INSERT su `users` è
// quello sull'indirizzo, quindi il codice basta.
const uniqueViolation = "23505"

// Store implementa [auth.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("authpg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ auth.Store = (*Store)(nil)

// ---------------------------------------------------------------------- users

// userColumns è l'elenco delle colonne lette in ogni SELECT su `users`, nello
// stesso ordine in cui scanUser le legge. Sta in una costante perché tre query
// diverse devono restare allineate: una colonna aggiunta in una sola delle tre è
// un errore che il compilatore non vede.
const userColumns = `id::text, email, coalesce(full_name, ''), role::text, timezone,
	coalesce(password_hash, ''), email_verified_at, suspended_at, last_login_at, created_at`

// UserByEmail cerca un account vivo per indirizzo.
//
// Il confronto è su `lower(email)`, che è esattamente la forma dell'indice unico
// `users_email_key` della 0002: scritto così, la query usa l'indice invece di
// scandire la tabella.
func (s *Store) UserByEmail(ctx context.Context, email string) (auth.User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+`
		   FROM users
		  WHERE lower(email) = lower($1) AND deleted_at IS NULL`, email)
	return scanUser(row)
}

// UserByID cerca un account vivo per identificativo.
func (s *Store) UserByID(ctx context.Context, id string) (auth.User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+`
		   FROM users
		  WHERE id = $1 AND deleted_at IS NULL`, id)
	return scanUser(row)
}

// CreateUser crea un account.
//
// L'inserimento è tentato e non preceduto da una SELECT di controllo, per due
// ragioni: la seconda è più veloce, la prima è che una SELECT seguita da un
// INSERT è una corsa — due registrazioni simultanee sullo stesso indirizzo
// passerebbero entrambe il controllo. L'unico arbitro è l'indice unico.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, fullName string) (auth.User, error) {
	var name *string
	if fullName != "" {
		name = &fullName
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, full_name)
		 VALUES ($1, $2, $3)
		 RETURNING `+userColumns, email, passwordHash, name)

	user, err := scanUser(row)
	if err == nil {
		return user, nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return auth.User{}, auth.ErrEmailTaken
	}
	// scanUser traduce «nessuna riga» in ErrNotFound, che qui non ha senso: un
	// INSERT ... RETURNING che non restituisce nulla è un errore diverso.
	if errors.Is(err, auth.ErrNotFound) {
		return auth.User{}, errors.New("authpg: INSERT su users non ha restituito la riga")
	}
	return auth.User{}, err
}

// UpdatePasswordHash sostituisce l'hash della password.
func (s *Store) UpdatePasswordHash(ctx context.Context, userID, passwordHash string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET password_hash = $2 WHERE id = $1 AND deleted_at IS NULL`,
		userID, passwordHash)
	if err != nil {
		return fmt.Errorf("authpg: aggiornamento dell'hash: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	return nil
}

// TouchLastLogin registra il momento dell'ultimo accesso riuscito.
func (s *Store) TouchLastLogin(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET last_login_at = $2 WHERE id = $1 AND deleted_at IS NULL`,
		userID, at)
	if err != nil {
		return fmt.Errorf("authpg: aggiornamento di last_login_at: %w", err)
	}
	return nil
}

// MarkEmailVerified segna l'indirizzo come confermato.
//
// La condizione `email_verified_at IS NULL` rende l'operazione idempotente e
// conserva la data della prima conferma: un secondo passaggio dal link non deve
// spostarla in avanti.
func (s *Store) MarkEmailVerified(ctx context.Context, userID string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET email_verified_at = $2
		  WHERE id = $1 AND deleted_at IS NULL AND email_verified_at IS NULL`,
		userID, at)
	if err != nil {
		return fmt.Errorf("authpg: conferma dell'indirizzo: %w", err)
	}
	return nil
}

// ------------------------------------------------------------------- sessions

const sessionColumns = `id::text, user_id::text, token_hash, created_at, last_used_at,
	expires_at, revoked_at, ip_address, coalesce(user_agent, '')`

// CreateSession registra una sessione nuova.
func (s *Store) CreateSession(ctx context.Context, in auth.Session) (auth.Session, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id, token_hash, created_at, last_used_at, expires_at, ip_address, user_agent)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING `+sessionColumns,
		in.UserID, in.TokenHash, in.CreatedAt, in.LastUsedAt, in.ExpiresAt,
		addrParam(in.IPAddress), textParam(in.UserAgent))

	session, err := scanSession(row)
	if errors.Is(err, auth.ErrNotFound) {
		return auth.Session{}, errors.New("authpg: INSERT su sessions non ha restituito la riga")
	}
	return session, err
}

// SessionByTokenHash cerca una sessione per impronta del token.
func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string) (auth.Session, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE token_hash = $1`, tokenHash)
	return scanSession(row)
}

// TouchSession aggiorna `last_used_at`.
func (s *Store) TouchSession(ctx context.Context, id string, at time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET last_used_at = $2 WHERE id = $1 AND revoked_at IS NULL`, id, at)
	if err != nil {
		return fmt.Errorf("authpg: aggiornamento di last_used_at: %w", err)
	}
	return nil
}

// ListSessions elenca le sessioni vive di un utente.
//
// La scadenza per inattività non si applica qui: è una durata che conosce solo
// il Service, e replicarla in SQL significherebbe averla in due posti.
func (s *Store) ListSessions(ctx context.Context, userID string, now time.Time) ([]auth.Session, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+sessionColumns+`
		   FROM sessions
		  WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2
		  ORDER BY last_used_at DESC`, userID, now)
	if err != nil {
		return nil, fmt.Errorf("authpg: elenco delle sessioni: %w", err)
	}
	defer rows.Close()

	var sessions []auth.Session
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("authpg: elenco delle sessioni: %w", err)
	}
	return sessions, nil
}

// RevokeSession revoca una sessione dell'utente indicato.
//
// `user_id = $1` è nella clausola WHERE e non in un controllo applicativo: è la
// differenza fra «solo il proprietario può chiudere la sessione» come proprietà
// della query e come promessa del chiamante.
func (s *Store) RevokeSession(ctx context.Context, userID, sessionID string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = $3
		  WHERE id = $2 AND user_id = $1 AND revoked_at IS NULL`,
		userID, sessionID, at)
	if err != nil {
		return fmt.Errorf("authpg: revoca della sessione: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	return nil
}

// RevokeSessionByTokenHash revoca la sessione corrente.
func (s *Store) RevokeSessionByTokenHash(ctx context.Context, tokenHash string, at time.Time) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE token_hash = $1 AND revoked_at IS NULL`,
		tokenHash, at)
	if err != nil {
		return fmt.Errorf("authpg: revoca della sessione: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	return nil
}

// RevokeUserSessions revoca tutte le sessioni vive di un utente, tranne quella
// eventualmente indicata.
//
// Le due varianti della query sono scritte per esteso invece di passare
// l'esclusione come parametro tri-stato (`$3 IS NULL OR id <> $3`): quella forma
// costringe a un cast esplicito verso uuid che, con un parametro nullo, dipende
// da come il driver ha dedotto il tipo. Due istruzioni leggibili valgono più di
// una condizione che funziona per come pgx serializza un puntatore.
func (s *Store) RevokeUserSessions(ctx context.Context, userID, exceptID string, at time.Time) (int, error) {
	const base = `UPDATE sessions SET revoked_at = $2 WHERE user_id = $1 AND revoked_at IS NULL`

	var (
		tag pgconn.CommandTag
		err error
	)
	if exceptID == "" {
		tag, err = s.pool.Exec(ctx, base, userID, at)
	} else {
		tag, err = s.pool.Exec(ctx, base+` AND id <> $3`, userID, at, exceptID)
	}
	if err != nil {
		return 0, fmt.Errorf("authpg: revoca delle sessioni: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ---------------------------------------------------------------- user_tokens

const userTokenColumns = `id::text, user_id::text, purpose::text, token_hash,
	created_at, expires_at, consumed_at, requested_ip`

// CreateUserToken registra un token monouso.
func (s *Store) CreateUserToken(ctx context.Context, in auth.UserToken) (auth.UserToken, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO user_tokens (user_id, purpose, token_hash, created_at, expires_at, requested_ip)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+userTokenColumns,
		in.UserID, string(in.Purpose), in.TokenHash, in.CreatedAt, in.ExpiresAt, addrParam(in.RequestedIP))

	token, err := scanUserToken(row)
	if errors.Is(err, auth.ErrNotFound) {
		return auth.UserToken{}, errors.New("authpg: INSERT su user_tokens non ha restituito la riga")
	}
	return token, err
}

// ConsumeUserToken segna il token come consumato e ne restituisce il contenuto.
//
// È un solo UPDATE condizionato con RETURNING, non una SELECT seguita da un
// UPDATE: due richieste concorrenti con lo stesso token devono produrre un
// vincitore e un [auth.ErrNotFound]. Con due istruzioni separate passerebbero
// entrambe, e un link di recupero servirebbe due volte.
func (s *Store) ConsumeUserToken(
	ctx context.Context, tokenHash string, purpose auth.TokenPurpose, now time.Time,
) (auth.UserToken, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE user_tokens SET consumed_at = $3
		  WHERE token_hash = $1
		    AND purpose = $2
		    AND consumed_at IS NULL
		    AND expires_at > $3
		 RETURNING `+userTokenColumns,
		tokenHash, string(purpose), now)
	return scanUserToken(row)
}

// RevokeUserTokens consuma senza usarli i token pendenti di un utente.
func (s *Store) RevokeUserTokens(
	ctx context.Context, userID string, purpose auth.TokenPurpose, at time.Time,
) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE user_tokens SET consumed_at = $3
		  WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL`,
		userID, string(purpose), at)
	if err != nil {
		return 0, fmt.Errorf("authpg: invalidazione dei token: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ------------------------------------------------------------------ scansione

// scanner è ciò che pgx.Row e pgx.Rows hanno in comune: permette a ogni scanXxx
// di servire sia una query a riga singola sia l'iterazione di un elenco.
type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (auth.User, error) {
	var (
		user  auth.User
		email string
	)
	err := row.Scan(&user.ID, &email, &user.FullName, &user.Role, &user.Timezone,
		&user.PasswordHash, &user.EmailVerifiedAt, &user.SuspendedAt, &user.LastLoginAt, &user.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.User{}, auth.ErrNotFound
		}
		return auth.User{}, fmt.Errorf("authpg: lettura di users: %w", err)
	}
	user.Email = email
	return user, nil
}

func scanSession(row scanner) (auth.Session, error) {
	var (
		session auth.Session
		ip      *netip.Prefix
	)
	err := row.Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CreatedAt,
		&session.LastUsedAt, &session.ExpiresAt, &session.RevokedAt, &ip, &session.UserAgent)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Session{}, auth.ErrNotFound
		}
		return auth.Session{}, fmt.Errorf("authpg: lettura di sessions: %w", err)
	}
	session.IPAddress = addrFromPrefix(ip)
	return session, nil
}

func scanUserToken(row scanner) (auth.UserToken, error) {
	var (
		token   auth.UserToken
		purpose string
		ip      *netip.Prefix
	)
	err := row.Scan(&token.ID, &token.UserID, &purpose, &token.TokenHash,
		&token.CreatedAt, &token.ExpiresAt, &token.ConsumedAt, &ip)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.UserToken{}, auth.ErrNotFound
		}
		return auth.UserToken{}, fmt.Errorf("authpg: lettura di user_tokens: %w", err)
	}
	token.Purpose = auth.TokenPurpose(purpose)
	token.RequestedIP = addrFromPrefix(ip)
	return token, nil
}

// ------------------------------------------------------------------ parametri

// addrParam converte un indirizzo in un parametro per una colonna `inet`.
//
// pgx mappa `inet` su netip.Prefix, non su netip.Addr: un indirizzo singolo è il
// prefisso con la maschera piena.
func addrParam(addr *netip.Addr) *netip.Prefix {
	if addr == nil || !addr.IsValid() {
		return nil
	}
	unmapped := addr.Unmap()
	prefix := netip.PrefixFrom(unmapped, unmapped.BitLen())
	return &prefix
}

func addrFromPrefix(prefix *netip.Prefix) *netip.Addr {
	if prefix == nil || !prefix.IsValid() {
		return nil
	}
	addr := prefix.Addr()
	return &addr
}

// textParam trasforma la stringa vuota in NULL: le colonne facoltative dello
// schema hanno un CHECK contro il valore vuoto, e una stringa vuota è
// «l'informazione non c'è», non «l'informazione è la stringa vuota».
func textParam(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
