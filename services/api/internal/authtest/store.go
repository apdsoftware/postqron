// Package authtest contiene i doppi di prova dell'autenticazione.
//
// Esiste come package a sé, e non come file `_test.go` di internal/auth, perché
// serve a due suite: quella del Service e quella delle rotte in
// internal/httpapi. Duplicarlo significherebbe due archivi finti che divergono,
// e la seconda copia smetterebbe di rispettare il contratto di [auth.Store]
// senza che nessuno se ne accorga.
//
// Non è importabile dal codice d'esercizio per costruzione: `internal/authtest`
// non è mai referenziato fuori dai test.
package authtest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
)

// Store è un'implementazione in memoria di [auth.Store] per i test.
//
// Non è una scorciatoia: i comportamenti che questa issue deve garantire —
// nessuna enumerazione degli account, tempi di risposta indistinguibili, rate
// limiting — si verificano sul Service, e con un database di mezzo le misure di
// tempo diventerebbero misure del database. Le proprietà che invece dipendono
// da PostgreSQL (atomicità del consumo di un token, ambito della revoca sulla
// riga) sono provate in internal/authpg contro il database vero.
type Store struct {
	mu     sync.Mutex
	seq    atomic.Int64
	users  map[string]auth.User
	sess   map[string]auth.Session
	tokens map[string]auth.UserToken

	// failOn, se valorizzato, fa restituire un errore all'operazione con quel
	// nome. Serve a provare che un guasto della persistenza non cambia ciò che
	// il client osserva.
	failOn map[string]error

	// calls conta le operazioni, per i test che devono distinguere «non ha
	// trovato» da «non ha cercato».
	calls map[string]int
}

// NewStore costruisce un archivio vuoto.
func NewStore() *Store {
	return &Store{
		users:  map[string]auth.User{},
		sess:   map[string]auth.Session{},
		tokens: map[string]auth.UserToken{},
		failOn: map[string]error{},
		calls:  map[string]int{},
	}
}

func (m *Store) nextID(prefix string) string {
	return fmt.Sprintf("%s-%04d", prefix, m.seq.Add(1))
}

// enter registra la chiamata e restituisce l'errore iniettato, se c'è.
// Va chiamata con il lock già preso.
func (m *Store) enter(op string) error {
	m.calls[op]++
	return m.failOn[op]
}

// CallCount è il numero di volte che l'operazione è stata invocata.
func (m *Store) CallCount(op string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[op]
}

// Fail fa fallire l'operazione indicata.
func (m *Store) Fail(op string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failOn[op] = err
}

// ---------------------------------------------------------------------- users

func (m *Store) UserByEmail(_ context.Context, email string) (auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("UserByEmail"); err != nil {
		return auth.User{}, err
	}
	wanted := strings.ToLower(strings.TrimSpace(email))
	for _, user := range m.users {
		if strings.ToLower(user.Email) == wanted {
			return user, nil
		}
	}
	return auth.User{}, auth.ErrNotFound
}

func (m *Store) UserByID(_ context.Context, id string) (auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("UserByID"); err != nil {
		return auth.User{}, err
	}
	user, ok := m.users[id]
	if !ok {
		return auth.User{}, auth.ErrNotFound
	}
	return user, nil
}

func (m *Store) CreateUser(_ context.Context, email, passwordHash, fullName string) (auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateUser"); err != nil {
		return auth.User{}, err
	}
	wanted := strings.ToLower(email)
	for _, user := range m.users {
		if strings.ToLower(user.Email) == wanted {
			return auth.User{}, auth.ErrEmailTaken
		}
	}
	user := auth.User{
		ID:           m.nextID("user"),
		Email:        email,
		FullName:     fullName,
		Role:         "user",
		Timezone:     "UTC",
		PasswordHash: passwordHash,
		CreatedAt:    time.Now(),
	}
	m.users[user.ID] = user
	return user, nil
}

func (m *Store) UpdatePasswordHash(_ context.Context, userID, passwordHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("UpdatePasswordHash"); err != nil {
		return err
	}
	user, ok := m.users[userID]
	if !ok {
		return auth.ErrNotFound
	}
	user.PasswordHash = passwordHash
	m.users[userID] = user
	return nil
}

func (m *Store) TouchLastLogin(_ context.Context, userID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("TouchLastLogin"); err != nil {
		return err
	}
	user, ok := m.users[userID]
	if !ok {
		return auth.ErrNotFound
	}
	user.LastLoginAt = &at
	m.users[userID] = user
	return nil
}

func (m *Store) MarkEmailVerified(_ context.Context, userID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("MarkEmailVerified"); err != nil {
		return err
	}
	user, ok := m.users[userID]
	if !ok {
		return auth.ErrNotFound
	}
	if user.EmailVerifiedAt == nil {
		user.EmailVerifiedAt = &at
		m.users[userID] = user
	}
	return nil
}

// ------------------------------------------------------------------- sessioni

func (m *Store) CreateSession(_ context.Context, in auth.Session) (auth.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateSession"); err != nil {
		return auth.Session{}, err
	}
	in.ID = m.nextID("sess")
	m.sess[in.ID] = in
	return in, nil
}

func (m *Store) SessionByTokenHash(_ context.Context, tokenHash string) (auth.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("SessionByTokenHash"); err != nil {
		return auth.Session{}, err
	}
	for _, session := range m.sess {
		if session.TokenHash == tokenHash {
			return session, nil
		}
	}
	return auth.Session{}, auth.ErrNotFound
}

func (m *Store) TouchSession(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("TouchSession"); err != nil {
		return err
	}
	session, ok := m.sess[id]
	if !ok || session.RevokedAt != nil {
		return auth.ErrNotFound
	}
	session.LastUsedAt = at
	m.sess[id] = session
	return nil
}

func (m *Store) ListSessions(_ context.Context, userID string, now time.Time) ([]auth.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListSessions"); err != nil {
		return nil, err
	}
	var out []auth.Session
	for _, session := range m.sess {
		if session.UserID == userID && session.RevokedAt == nil && session.ExpiresAt.After(now) {
			out = append(out, session)
		}
	}
	// L'ordine reale è per last_used_at discendente; qui basta che sia stabile.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].LastUsedAt.After(out[i].LastUsedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}

func (m *Store) RevokeSession(_ context.Context, userID, sessionID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("RevokeSession"); err != nil {
		return err
	}
	session, ok := m.sess[sessionID]
	if !ok || session.UserID != userID || session.RevokedAt != nil {
		return auth.ErrNotFound
	}
	session.RevokedAt = &at
	m.sess[sessionID] = session
	return nil
}

func (m *Store) RevokeSessionByTokenHash(_ context.Context, tokenHash string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("RevokeSessionByTokenHash"); err != nil {
		return err
	}
	for id, session := range m.sess {
		if session.TokenHash == tokenHash && session.RevokedAt == nil {
			session.RevokedAt = &at
			m.sess[id] = session
			return nil
		}
	}
	return auth.ErrNotFound
}

func (m *Store) RevokeUserSessions(_ context.Context, userID, exceptID string, at time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("RevokeUserSessions"); err != nil {
		return 0, err
	}
	revoked := 0
	for id, session := range m.sess {
		if session.UserID != userID || session.RevokedAt != nil || id == exceptID {
			continue
		}
		session.RevokedAt = &at
		m.sess[id] = session
		revoked++
	}
	return revoked, nil
}

// --------------------------------------------------------------------- token

func (m *Store) CreateUserToken(_ context.Context, in auth.UserToken) (auth.UserToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateUserToken"); err != nil {
		return auth.UserToken{}, err
	}
	in.ID = m.nextID("tok")
	m.tokens[in.ID] = in
	return in, nil
}

func (m *Store) ConsumeUserToken(
	_ context.Context, tokenHash string, purpose auth.TokenPurpose, now time.Time,
) (auth.UserToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ConsumeUserToken"); err != nil {
		return auth.UserToken{}, err
	}
	for id, token := range m.tokens {
		if token.TokenHash != tokenHash || token.Purpose != purpose {
			continue
		}
		if token.ConsumedAt != nil || !token.ExpiresAt.After(now) {
			return auth.UserToken{}, auth.ErrNotFound
		}
		token.ConsumedAt = &now
		m.tokens[id] = token
		return token, nil
	}
	return auth.UserToken{}, auth.ErrNotFound
}

func (m *Store) RevokeUserTokens(
	_ context.Context, userID string, purpose auth.TokenPurpose, at time.Time,
) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("RevokeUserTokens"); err != nil {
		return 0, err
	}
	revoked := 0
	for id, token := range m.tokens {
		if token.UserID != userID || token.Purpose != purpose || token.ConsumedAt != nil {
			continue
		}
		token.ConsumedAt = &at
		m.tokens[id] = token
		revoked++
	}
	return revoked, nil
}

// ---------------------------------------------------------------- ispezione

// Suspend segna l'account come sospeso.
func (m *Store) Suspend(userID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user := m.users[userID]
	user.SuspendedAt = &at
	m.users[userID] = user
}

// SetPasswordHash sostituisce l'hash senza passare dal Service.
func (m *Store) SetPasswordHash(userID, hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user := m.users[userID]
	user.PasswordHash = hash
	m.users[userID] = user
}

// PasswordHash legge l'hash memorizzato.
func (m *Store) PasswordHash(userID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.users[userID].PasswordHash
}

// User restituisce l'utente.
func (m *Store) User(userID string) auth.User {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.users[userID]
}

// Session restituisce la sessione.
func (m *Store) Session(id string) auth.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sess[id]
}

// SetSessionLastUsed sposta indietro l'ultimo utilizzo di una sessione.
func (m *Store) SetSessionLastUsed(id string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sess[id]
	session.LastUsedAt = at
	m.sess[id] = session
}

// UserCount è il numero di account.
func (m *Store) UserCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.users)
}

// PendingTokens è il numero di token pendenti per uno scopo.
func (m *Store) PendingTokens(userID string, purpose auth.TokenPurpose) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, token := range m.tokens {
		if token.UserID == userID && token.Purpose == purpose && token.ConsumedAt == nil {
			n++
		}
	}
	return n
}

var _ auth.Store = (*Store)(nil)
