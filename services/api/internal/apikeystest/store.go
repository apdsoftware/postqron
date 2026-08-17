// Package apikeystest contiene i doppi di prova delle chiavi API.
//
// Esiste come package a sé, e non come file `_test.go` di internal/apikeys,
// perché serve a due suite: quella del Service e quella delle rotte in
// internal/httpapi. Duplicarlo significherebbe due archivi finti che divergono, e
// la seconda copia smetterebbe di rispettare il contratto di [apikeys.Store]
// senza che nessuno se ne accorga.
//
// Non è importabile dal codice d'esercizio per costruzione:
// `internal/apikeystest` non è mai referenziato fuori dai test.
package apikeystest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
)

// Store è un'implementazione in memoria di [apikeys.Store] per i test.
//
// Non è una scorciatoia: i comportamenti che la issue #397 deve garantire — la
// revoca che ha effetto al tentativo successivo, lo scope negato, il fatto che la
// chiave in chiaro non sia recuperabile — si verificano sul Service, e con un
// database di mezzo diventerebbero test del database. Le proprietà che invece
// dipendono da PostgreSQL (unicità dell'impronta, ambito della revoca sulla
// riga) sono provate in internal/apikeypg contro il database vero.
type Store struct {
	mu   sync.Mutex
	seq  atomic.Int64
	keys map[string]apikeys.Key

	// failOn, se valorizzato, fa restituire un errore all'operazione con quel
	// nome. Serve a provare che un guasto della persistenza non autentica nessuno.
	failOn map[string]error

	// calls conta le operazioni, per i test che devono distinguere «non ha
	// trovato» da «non ha cercato» — ed è come si verifica che la ricerca sia una
	// lettura per impronta e non una scansione dell'elenco.
	calls map[string]int

	// alwaysReturn, se valorizzato, fa restituire a KeyByHash quella chiave
	// qualunque impronta le si chieda. Serve a provare che il Service non si fida
	// della SELECT: vedi [Store.AlwaysReturn].
	alwaysReturn string
}

// NewStore costruisce un archivio vuoto.
func NewStore() *Store {
	return &Store{
		keys:   map[string]apikeys.Key{},
		failOn: map[string]error{},
		calls:  map[string]int{},
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

// AlwaysReturn fa restituire a KeyByHash la chiave indicata, ignorando
// l'impronta richiesta.
//
// Modella un archivio rotto — una query senza la clausola WHERE giusta, un
// adattatore che confonde i parametri — ed esiste perché la proprietà «solo
// l'impronta esatta autentica» va provata contro un archivio che *non* la
// rispetta. Contro uno che la rispetta, il confronto a valle è sempre vero e non
// dimostrerebbe niente.
func (m *Store) AlwaysReturn(keyID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alwaysReturn = keyID
}

// ---------------------------------------------------- contratto apikeys.Store

func (m *Store) CreateKey(_ context.Context, in apikeys.Key) (apikeys.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CreateKey"); err != nil {
		return apikeys.Key{}, err
	}
	for _, existing := range m.keys {
		if existing.Hash == in.Hash {
			// Lo stesso vincolo dell'indice unico `api_keys_key_hash_key`.
			return apikeys.Key{}, fmt.Errorf("impronta già presente")
		}
	}
	in.ID = fmt.Sprintf("key-%04d", m.seq.Add(1))
	m.keys[in.ID] = in
	return in, nil
}

func (m *Store) KeyByHash(_ context.Context, hash string) (apikeys.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("KeyByHash"); err != nil {
		return apikeys.Key{}, err
	}
	if m.alwaysReturn != "" {
		if key, ok := m.keys[m.alwaysReturn]; ok {
			return key, nil
		}
	}
	for _, key := range m.keys {
		if key.Hash == hash {
			return key, nil
		}
	}
	return apikeys.Key{}, apikeys.ErrNotFound
}

func (m *Store) ListKeys(_ context.Context, userID string, includeRevoked bool) ([]apikeys.Key, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("ListKeys"); err != nil {
		return nil, err
	}
	var out []apikeys.Key
	for _, key := range m.keys {
		if key.UserID != userID {
			continue
		}
		if key.Revoked() && !includeRevoked {
			continue
		}
		out = append(out, key)
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

func (m *Store) RevokeKey(_ context.Context, userID, keyID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("RevokeKey"); err != nil {
		return err
	}
	key, ok := m.keys[keyID]
	if !ok || key.UserID != userID || key.Revoked() {
		return apikeys.ErrNotFound
	}
	key.RevokedAt = &at
	m.keys[keyID] = key
	return nil
}

func (m *Store) TouchKey(_ context.Context, keyID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("TouchKey"); err != nil {
		return err
	}
	key, ok := m.keys[keyID]
	if !ok || key.Revoked() {
		return apikeys.ErrNotFound
	}
	key.LastUsedAt = &at
	m.keys[keyID] = key
	return nil
}

func (m *Store) CountActiveKeys(_ context.Context, userID string, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.enter("CountActiveKeys"); err != nil {
		return 0, err
	}
	count := 0
	for _, key := range m.keys {
		if key.UserID == userID && key.Active(now) {
			count++
		}
	}
	return count, nil
}

// ---------------------------------------------------------------- ispezione

// Key restituisce la chiave conservata.
func (m *Store) Key(id string) apikeys.Key {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.keys[id]
}

// Count è il numero di chiavi in archivio, comprese le revocate.
func (m *Store) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.keys)
}

// Hashes elenca le impronte conservate. Serve al test che verifica che a riposo
// non ci sia nulla che assomigli alla chiave in chiaro.
func (m *Store) Hashes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.keys))
	for _, key := range m.keys {
		out = append(out, key.Hash)
	}
	return out
}

// Seed inserisce una chiave già formata, senza passare dal Service. Serve ai test
// che devono partire da una chiave scaduta, revocata o di un altro utente.
func (m *Store) Seed(key apikeys.Key) apikeys.Key {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key.ID == "" {
		key.ID = fmt.Sprintf("key-%04d", m.seq.Add(1))
	}
	m.keys[key.ID] = key
	return key
}

var _ apikeys.Store = (*Store)(nil)
