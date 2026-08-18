package notify

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
)

// MemoryQueue è una coda in memoria con la stessa politica di deduplicazione di
// quella vera.
//
// Serve ai test di questo package e a quelli dei package di dominio, che devono
// poter verificare *che cosa* sarebbe stato mandato senza un database e senza
// una riga di rete. Non va usata in produzione: le notifiche non sopravvivono
// al processo, che è l'unica cosa per cui la coda vera esiste.
//
// La deduplicazione riproduce il comportamento dell'indice unico della 0008 —
// una sola riga viva per `dedupe_key` — perché è la proprietà su cui poggia la
// politica anti-spam, e un doppio che non la riproducesse renderebbe verdi dei
// test che il database farebbe fallire.
type MemoryQueue struct {
	mu sync.Mutex
	// EnqueueErr, se valorizzato, è l'errore che Enqueue restituisce. Serve a
	// verificare che un'email che non parte non faccia fallire l'operazione che
	// l'ha causata.
	EnqueueErr error
	// Recipients associa un utente al suo destinatario. Un utente assente
	// produce [NoRecipient], come nel database.
	Recipients map[string]Recipient

	rows    []*memoryRow
	byKey   map[string]*memoryRow
	counter int
}

type memoryRow struct {
	Request
	id       string
	status   string
	attempts int
	// scheduledAt è mutabile: la presa in carico la sposta avanti, il ritentare
	// pure.
	scheduledAt time.Time
	emailLogID  string
	reason      string
}

// NewMemoryQueue costruisce una coda vuota.
func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{
		Recipients: make(map[string]Recipient),
		byKey:      make(map[string]*memoryRow),
	}
}

// WithRecipient registra un destinatario. Restituisce la coda per poterla
// costruire in un'espressione sola.
func (q *MemoryQueue) WithRecipient(r Recipient) *MemoryQueue {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.Recipients == nil {
		q.Recipients = make(map[string]Recipient)
	}
	q.Recipients[r.UserID] = r
	return q
}

// Enqueue implementa [Queue].
func (q *MemoryQueue) Enqueue(_ context.Context, req Request) (Result, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.EnqueueErr != nil {
		return "", q.EnqueueErr
	}
	if _, ok := q.Recipients[req.UserID]; !ok && len(q.Recipients) > 0 {
		return NoRecipient, nil
	}

	if req.DedupeKey != "" {
		if existing, ok := q.byKey[req.DedupeKey]; ok {
			if existing.status == "pending" {
				// Lo stesso incremento dell'ON CONFLICT della coda vera: la
				// raffica alza il conteggio dell'avviso già accodato.
				existing.Payload.Failures += req.Payload.Failures
			}
			return Grouped, nil
		}
	}

	q.counter++
	row := &memoryRow{
		Request:     req,
		id:          "mem-" + itoa(q.counter),
		status:      "pending",
		scheduledAt: req.ScheduledAt,
	}
	q.rows = append(q.rows, row)
	if req.DedupeKey != "" {
		q.byKey[req.DedupeKey] = row
	}
	return Queued, nil
}

// Due implementa [Queue].
func (q *MemoryQueue) Due(_ context.Context, now time.Time, limit int, lease time.Duration) ([]Pending, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var due []Pending
	for _, row := range q.rows {
		if len(due) >= limit {
			break
		}
		if row.status != "pending" || row.scheduledAt.After(now) {
			continue
		}
		row.attempts++
		row.scheduledAt = now.Add(lease)

		recipient, ok := q.Recipients[row.UserID]
		if !ok {
			recipient = Recipient{UserID: row.UserID, Email: row.UserID + "@example.test", Language: "en"}
		}
		due = append(due, Pending{
			ID:          row.id,
			Event:       row.Event,
			Attempts:    row.attempts,
			JobID:       row.JobID,
			Environment: row.Environment,
			Recipient:   recipient,
			Payload:     row.Payload,
		})
	}
	return due, nil
}

// MarkSent implementa [Queue].
func (q *MemoryQueue) MarkSent(_ context.Context, id, emailLogID string, _ time.Time) error {
	return q.update(id, func(row *memoryRow) {
		row.status = "sent"
		row.emailLogID = emailLogID
	})
}

// Retry implementa [Queue].
func (q *MemoryQueue) Retry(_ context.Context, id string, at time.Time, reason string) error {
	return q.update(id, func(row *memoryRow) {
		row.status = "pending"
		row.scheduledAt = at
		row.reason = reason
	})
}

// MarkFailed implementa [Queue].
func (q *MemoryQueue) MarkFailed(_ context.Context, id, reason string, _ time.Time) error {
	return q.update(id, func(row *memoryRow) {
		row.status = "failed"
		row.reason = reason
	})
}

// MarkSkipped implementa [Queue].
func (q *MemoryQueue) MarkSkipped(_ context.Context, id, reason string, _ time.Time) error {
	return q.update(id, func(row *memoryRow) {
		row.status = "skipped"
		row.reason = reason
	})
}

func (q *MemoryQueue) update(id string, fn func(*memoryRow)) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, row := range q.rows {
		if row.id == id {
			fn(row)
			return nil
		}
	}
	return errors.New("notify: notifica inesistente: " + id)
}

// Enqueued è una notifica accodata, nella forma in cui un test la interroga.
type Enqueued struct {
	Request
	Status     string
	Attempts   int
	EmailLogID string
	Reason     string
}

// All restituisce le notifiche in ordine di accodamento.
func (q *MemoryQueue) All() []Enqueued {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Enqueued, 0, len(q.rows))
	for _, row := range q.rows {
		out = append(out, Enqueued{
			Request:    row.Request,
			Status:     row.status,
			Attempts:   row.attempts,
			EmailLogID: row.emailLogID,
			Reason:     row.reason,
		})
	}
	return out
}

// OfEvent restituisce le notifiche di un solo evento.
func (q *MemoryQueue) OfEvent(event Event) []Enqueued {
	var out []Enqueued
	for _, row := range q.All() {
		if row.Event == event {
			out = append(out, row)
		}
	}
	return out
}

// Len è il numero di notifiche accodate, in qualunque stato.
func (q *MemoryQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.rows)
}

// ---------------------------------------------------------------- il mittente

// RecordingSender registra ciò che *sarebbe* stato spedito.
//
// È il doppio che sostituisce il client Mailronix nei test: nessuna chiamata di
// rete, e in cambio l'accesso al corpo esatto del messaggio — che è ciò che
// permette di verificare che dentro non ci sia finito un segreto.
type RecordingSender struct {
	mu sync.Mutex
	// Err, se valorizzato, è l'errore restituito da Send. Un
	// [mailronix.APIError] ritentabile fa ritentare, gli altri no.
	Err  error
	sent []mailronix.Email
	// LogID compone l'identificativo restituito. Vuoto vale "log-<n>".
	LogID func(n int) string
}

// Send implementa [Sender].
func (s *RecordingSender) Send(_ context.Context, email mailronix.Email) (mailronix.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return mailronix.Receipt{}, s.Err
	}
	s.sent = append(s.sent, email)

	id := "log-" + itoa(len(s.sent))
	if s.LogID != nil {
		id = s.LogID(len(s.sent))
	}
	return mailronix.Receipt{Status: mailronix.StatusQueued, EmailLogID: id}, nil
}

// Sent restituisce una copia dei messaggi spediti.
func (s *RecordingSender) Sent() []mailronix.Email {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mailronix.Email(nil), s.sent...)
}

// Len è il numero di messaggi spediti.
func (s *RecordingSender) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

// itoa evita di importare strconv per un solo numero in un file di doppi.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
