// Package secretstest contiene i doppi di prova dei segreti del workspace.
//
// Esiste come package a sé, e non come file `_test.go` di internal/secrets,
// perché serve a due suite: quella del Service e quella delle rotte in
// internal/httpapi. Duplicarlo significherebbe due archivi finti che divergono,
// e la seconda copia smetterebbe di rispettare il contratto di [secrets.Store]
// senza che nessuno se ne accorga.
//
// Non è importabile dal codice d'esercizio per costruzione: `internal/secretstest`
// non è mai referenziato fuori dai test.
package secretstest

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// Store è un'implementazione in memoria di [secrets.Store] per i test.
//
// Riproduce i vincoli della 0012 che contano per il Service: l'unicità del nome
// **fra i soli vivi**, e lo svuotamento del testo cifrato alla revoca. Le
// proprietà che dipendono da PostgreSQL — che quei vincoli reggano anche sotto
// concorrenza, che l'indice parziale sia quello usato — sono provate in
// internal/secretspg contro il database vero.
type Store struct {
	mu      sync.Mutex
	seq     atomic.Int64
	secrets map[string]secrets.Sealed

	// failOn, se valorizzato, fa restituire un errore all'operazione con quel
	// nome. Serve a provare che un guasto della persistenza non fa partire
	// un'esecuzione con un riferimento non risolto.
	failOn map[string]error

	// calls conta le operazioni. È come si verifica che una richiesta senza
	// riferimenti non legga niente, e che la risoluzione chieda i nomi che le
	// servono invece di tutti.
	calls map[string]int
}

// NewStore costruisce un archivio vuoto.
func NewStore() *Store {
	return &Store{
		secrets: map[string]secrets.Sealed{},
		failOn:  map[string]error{},
		calls:   map[string]int{},
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

// ---------------------------------------------------- contratto secrets.Store

func (m *Store) CreateSecret(_ context.Context, in secrets.Sealed) (secrets.Secret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateSecret"); err != nil {
		return secrets.Secret{}, err
	}
	for _, existing := range m.secrets {
		// Lo stesso vincolo dell'indice parziale `workspace_secrets_live_name_key`:
		// il nome di un segreto revocato torna disponibile.
		if existing.UserID == in.UserID && existing.Name == in.Name && existing.Live() {
			return secrets.Secret{}, secrets.ErrDuplicateName
		}
	}
	in.ID = fmt.Sprintf("secret-%04d", m.seq.Add(1))
	in.UpdatedAt = in.CreatedAt
	m.secrets[in.ID] = in
	return in.Secret, nil
}

func (m *Store) UpdateSecret(
	_ context.Context, userID, secretID string, in secrets.Sealed, description *string,
) (secrets.Secret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("UpdateSecret"); err != nil {
		return secrets.Secret{}, err
	}
	current, ok := m.secrets[secretID]
	if !ok || current.UserID != userID || current.Revoked() {
		return secrets.Secret{}, secrets.ErrNotFound
	}
	current.Ciphertext = in.Ciphertext
	current.Nonce = in.Nonce
	current.KeyVersion = in.KeyVersion
	if description != nil {
		current.Description = *description
	}
	m.secrets[secretID] = current
	return current.Secret, nil
}

func (m *Store) ListSecrets(_ context.Context, userID string, includeRevoked bool) ([]secrets.Secret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListSecrets"); err != nil {
		return nil, err
	}
	var out []secrets.Secret
	for _, secret := range m.secrets {
		if secret.UserID != userID {
			continue
		}
		if secret.Revoked() && !includeRevoked {
			continue
		}
		out = append(out, secret.Secret)
	}
	// L'ordine reale è per created_at discendente; a parità di istante — che nei
	// test con orologio finto è la norma — l'identificativo lo rende stabile.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func (m *Store) SecretByID(_ context.Context, userID, secretID string) (secrets.Secret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("SecretByID"); err != nil {
		return secrets.Secret{}, err
	}
	secret, ok := m.secrets[secretID]
	if !ok || secret.UserID != userID || secret.Revoked() {
		return secrets.Secret{}, secrets.ErrNotFound
	}
	return secret.Secret, nil
}

func (m *Store) LiveByNames(_ context.Context, userID string, names []string) ([]secrets.Sealed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("LiveByNames"); err != nil {
		return nil, err
	}
	var out []secrets.Sealed
	for _, secret := range m.secrets {
		if secret.UserID == userID && secret.Live() && slices.Contains(names, secret.Name) {
			out = append(out, secret)
		}
	}
	return out, nil
}

func (m *Store) LiveNames(_ context.Context, userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("LiveNames"); err != nil {
		return nil, err
	}
	var names []string
	for _, secret := range m.secrets {
		if secret.UserID == userID && secret.Live() {
			names = append(names, secret.Name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// RevokeSecret revoca **e svuota**, come impone
// `workspace_secrets_revoked_is_empty_check`. Un doppio che si limitasse a
// scrivere la data lascerebbe passare un Service che si aspetta di poter ancora
// decifrare un segreto revocato.
func (m *Store) RevokeSecret(_ context.Context, userID, secretID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("RevokeSecret"); err != nil {
		return err
	}
	secret, ok := m.secrets[secretID]
	if !ok || secret.UserID != userID || secret.Revoked() {
		return secrets.ErrNotFound
	}
	secret.RevokedAt = &at
	secret.Ciphertext = nil
	secret.Nonce = nil
	m.secrets[secretID] = secret
	return nil
}

func (m *Store) TouchSecrets(_ context.Context, secretIDs []string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("TouchSecrets"); err != nil {
		return err
	}
	for _, id := range secretIDs {
		secret, ok := m.secrets[id]
		if !ok || secret.Revoked() {
			continue
		}
		secret.LastUsedAt = &at
		m.secrets[id] = secret
	}
	return nil
}

func (m *Store) CountLiveSecrets(_ context.Context, userID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CountLiveSecrets"); err != nil {
		return 0, err
	}
	count := 0
	for _, secret := range m.secrets {
		if secret.UserID == userID && secret.Live() {
			count++
		}
	}
	return count, nil
}

// ---------------------------------------------------------------- ispezione

// Sealed restituisce la riga conservata, con il suo materiale cifrato. Serve al
// test che verifica che a riposo non ci sia nulla che assomigli al valore.
func (m *Store) Sealed(id string) secrets.Sealed {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.secrets[id]
}

// All restituisce tutte le righe conservate, comprese le revocate.
func (m *Store) All() []secrets.Sealed {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]secrets.Sealed, 0, len(m.secrets))
	for _, secret := range m.secrets {
		out = append(out, secret)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Count è il numero di righe in archivio, comprese le revocate.
func (m *Store) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.secrets)
}

// Seed inserisce una riga già formata, senza passare dal Service. Serve ai test
// che devono partire da un segreto revocato, di un altro workspace, o cifrato
// con una chiave che il processo non ha più.
func (m *Store) Seed(in secrets.Sealed) secrets.Sealed {
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.ID == "" {
		in.ID = fmt.Sprintf("secret-%04d", m.seq.Add(1))
	}
	m.secrets[in.ID] = in
	return in
}

var _ secrets.Store = (*Store)(nil)
