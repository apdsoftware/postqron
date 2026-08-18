package marketing

import (
	"context"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
)

// I doppi di prova di questo package.
//
// Stanno in un file normale e non in un `_test.go` per la stessa ragione dei
// gemelli di internal/notify: li usano anche i test di internal/httpapi, e un
// tipo di prova che vive in un file di test non è importabile da fuori.
//
// [MemoryStore] non è una seconda implementazione della politica: la politica
// qui non c'è. È una tabella in memoria che si comporta come quella vera sui due
// punti che contano — l'ultima decisione vince, e una decisione uguale alla
// precedente non allunga la traccia — così che i test sul consenso girino senza
// database. Ciò che solo PostgreSQL può garantire lo verifica
// internal/marketingpg.

// MemoryStore è un [Store] in memoria.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string][]Entry
	users   map[string]Recipient
	now     func() time.Time
}

// NewMemoryStore costruisce lo store di prova.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		entries: make(map[string][]Entry),
		users:   make(map[string]Recipient),
		now:     time.Now,
	}
}

var _ Store = (*MemoryStore)(nil)

// WithClock sostituisce l'orologio delle decisioni.
func (s *MemoryStore) WithClock(now func() time.Time) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
	return s
}

// WithUser dichiara un destinatario esistente. Il consenso **non** si imposta da
// qui: si presta con [MemoryStore.Record], come nella vita vera.
func (s *MemoryStore) WithUser(r Recipient) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[r.UserID] = r
	return s
}

// Record implementa [Store].
func (s *MemoryStore) Record(_ context.Context, record Record) (Applied, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.users[record.UserID]; !ok {
		return NoUser, nil
	}
	if history := s.entries[record.UserID]; len(history) > 0 && history[0].Decision == record.Decision {
		return Unchanged, nil
	}

	// In testa: la traccia si legge dalla più recente, come quella vera.
	s.entries[record.UserID] = append([]Entry{{
		Decision:   record.Decision,
		Source:     record.Source,
		OccurredAt: s.now().UTC(),
	}}, s.entries[record.UserID]...)
	return Recorded, nil
}

// State implementa [Store].
func (s *MemoryStore) State(_ context.Context, userID string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := State{UserID: userID}
	if history := s.entries[userID]; len(history) > 0 {
		state.Decided = true
		state.Consented = history[0].Decision == DecisionGranted
		state.OccurredAt = history[0].OccurredAt
		state.Source = history[0].Source
	}
	return state, nil
}

// History implementa [Store].
func (s *MemoryStore) History(_ context.Context, userID string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.entries[userID]), nil
}

// Recipient implementa [Store], leggendo destinatario e consenso insieme.
func (s *MemoryStore) Recipient(_ context.Context, userID string) (Recipient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return Recipient{}, ErrNoRecipient
	}
	if history := s.entries[userID]; len(history) > 0 {
		user.Consented = history[0].Decision == DecisionGranted
	} else {
		user.Consented = false
	}
	return user, nil
}

// Language implementa [Store].
//
// Non guarda il consenso e non restituisce l'indirizzo: alla disiscrizione
// serve sapere solo in che lingua parlare.
func (s *MemoryStore) Language(_ context.Context, userID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[userID]
	if !ok {
		return "", ErrNoRecipient
	}
	return user.Language, nil
}

// ------------------------------------------------------------------ mittente

// RecordingSender è un [Sender] che annota invece di spedire.
//
// È il doppio che sostituisce la rete: nessun test di questo package parla con
// Mailronix, e il `202` che restituisce è quello vero — identico per un
// destinatario recapitabile e per uno in suppression list (R20.1), perché è
// esattamente ciò che il servizio risponde.
type RecordingSender struct {
	mu    sync.Mutex
	sent  []mailronix.Email
	fail  error
	count int
}

var _ Sender = (*RecordingSender)(nil)

// Send implementa [Sender].
func (s *RecordingSender) Send(_ context.Context, email mailronix.Email) (mailronix.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fail != nil {
		return mailronix.Receipt{}, s.fail
	}
	s.count++
	s.sent = append(s.sent, email)
	return mailronix.Receipt{EmailLogID: "log-" + strconv.Itoa(s.count)}, nil
}

// WithFailure fa fallire ogni invio successivo.
func (s *RecordingSender) WithFailure(err error) *RecordingSender {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
	return s
}

// Sent restituisce i messaggi consegnati finora.
func (s *RecordingSender) Sent() []mailronix.Email {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}
