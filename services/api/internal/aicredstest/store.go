// Package aicredstest contiene i doppi di prova delle chiavi AI.
//
// Esiste come package a sé, e non come file `_test.go` di internal/aicreds,
// perché serve a due suite: quella del Service e quella delle rotte in
// internal/httpapi. Duplicarlo significherebbe due archivi finti che divergono,
// e la seconda copia smetterebbe di rispettare il contratto di [aicreds.Store]
// senza che nessuno se ne accorga. È la stessa scelta, e la stessa forma, di
// internal/secretstest.
//
// Non è importabile dal codice d'esercizio per costruzione: `internal/aicredstest`
// non è mai referenziato fuori dai test.
package aicredstest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/aicreds"
)

// Store è un'implementazione in memoria di [aicreds.Store] per i test.
//
// Riproduce i vincoli della 0016 che contano per il Service: **una chiave viva
// per provider**, e lo svuotamento del materiale cifrato alla revoca. Le
// proprietà che dipendono da PostgreSQL — che quei vincoli reggano anche sotto
// concorrenza, che l'indice parziale sia quello usato — sono provate in
// internal/aicredspg contro il database vero.
type Store struct {
	mu          sync.Mutex
	seq         atomic.Int64
	credentials map[string]aicreds.Sealed

	// failOn, se valorizzato, fa restituire un errore all'operazione con quel
	// nome. Serve a provare che un guasto della persistenza non produce una
	// chiave vuota trattata come buona.
	failOn map[string]error

	// calls conta le operazioni. È come si verifica che una lettura recente non
	// riscriva `last_used_at`.
	calls map[string]int
}

// NewStore costruisce un archivio vuoto.
func NewStore() *Store {
	return &Store{
		credentials: map[string]aicreds.Sealed{},
		failOn:      map[string]error{},
		calls:       map[string]int{},
	}
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

// --------------------------------------------------- contratto aicreds.Store

func (m *Store) UpsertCredential(_ context.Context, in aicreds.Sealed) (aicreds.Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("UpsertCredential"); err != nil {
		return aicreds.Credential{}, err
	}

	now := in.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	// L'unicità fra i vivi: se c'è già una chiave viva per quel provider, la si
	// sostituisce invece di aggiungerne una seconda. È ciò che l'indice parziale
	// `ai_credentials_live_provider_key` impone al database.
	for id, existing := range m.credentials {
		if existing.UserID != in.UserID || existing.Provider != in.Provider || existing.Revoked() {
			continue
		}
		updated := existing
		updated.Ciphertext = in.Ciphertext
		updated.Nonce = in.Nonce
		updated.KeyVersion = in.KeyVersion
		updated.Label = in.Label
		updated.UpdatedAt = now
		m.credentials[id] = updated
		return updated.Credential, nil
	}

	id := fmt.Sprintf("aicred-%d", m.seq.Add(1))
	stored := in
	stored.ID = id
	stored.CreatedAt = now
	stored.UpdatedAt = now
	m.credentials[id] = stored
	return stored.Credential, nil
}

func (m *Store) ListCredentials(
	_ context.Context, userID string, includeRevoked bool,
) ([]aicreds.Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListCredentials"); err != nil {
		return nil, err
	}

	var out []aicreds.Credential
	for _, sealed := range m.credentials {
		if sealed.UserID != userID {
			continue
		}
		if sealed.Revoked() && !includeRevoked {
			continue
		}
		out = append(out, sealed.Credential)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Store) LiveByProvider(
	_ context.Context, userID string, provider aicreds.Provider,
) (aicreds.Sealed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("LiveByProvider"); err != nil {
		return aicreds.Sealed{}, err
	}

	for _, sealed := range m.credentials {
		if sealed.UserID == userID && sealed.Provider == provider && sealed.Live() {
			return sealed, nil
		}
	}
	return aicreds.Sealed{}, aicreds.ErrNotFound
}

func (m *Store) RevokeCredential(
	_ context.Context, userID, credentialID string, at time.Time,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("RevokeCredential"); err != nil {
		return err
	}

	sealed, ok := m.credentials[credentialID]
	if !ok || sealed.UserID != userID || sealed.Revoked() {
		return aicreds.ErrNotFound
	}

	// La revoca svuota il materiale: è il vincolo
	// `ai_credentials_revoked_is_empty_check`, e riprodurlo qui è ciò che rende
	// il doppio utile — un Service che revocasse senza svuotare passerebbe i test
	// con un archivio che si limita a datare la riga.
	sealed.RevokedAt = &at
	sealed.UpdatedAt = at
	sealed.Ciphertext = nil
	sealed.Nonce = nil
	m.credentials[credentialID] = sealed
	return nil
}

func (m *Store) TouchCredential(_ context.Context, credentialID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("TouchCredential"); err != nil {
		return err
	}

	sealed, ok := m.credentials[credentialID]
	if !ok {
		return aicreds.ErrNotFound
	}
	sealed.LastUsedAt = &at
	m.credentials[credentialID] = sealed
	return nil
}

// ------------------------------------------------------------ ispezione

// Sealed restituisce la riga come è conservata, materiale cifrato compreso. È
// come i test verificano che ciò che sta «a riposo» non sia il testo in chiaro.
func (m *Store) Sealed(id string) aicreds.Sealed {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.credentials[id]
}

// All restituisce tutte le righe conservate.
func (m *Store) All() []aicreds.Sealed {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]aicreds.Sealed, 0, len(m.credentials))
	for _, sealed := range m.credentials {
		out = append(out, sealed)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Count è il numero di righe conservate, revocate comprese.
func (m *Store) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.credentials)
}

// Corrupt sostituisce il materiale cifrato di una riga. Serve a provare che una
// riga manomessa non si apre e non finisce nei log.
func (m *Store) Corrupt(id string, ciphertext []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sealed := m.credentials[id]
	sealed.Ciphertext = ciphertext
	m.credentials[id] = sealed
}

var _ aicreds.Store = (*Store)(nil)
