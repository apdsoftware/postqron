package apikeys

import (
	"context"
	"log/slog"
	"time"
)

// SecurityKind è il tipo di evento di sicurezza che questo package produce
// (R21).
//
// Sono due, e sono entrambi eventi che il proprietario dell'account deve poter
// vedere arrivare: una chiave API creata è una nuova credenziale sul suo
// account, una revocata è una credenziale che smette di funzionare. Se non è
// stato lui, è la prima cosa che glielo dice.
type SecurityKind string

const (
	SecurityAPIKeyCreated SecurityKind = "api_key_created"
	SecurityAPIKeyRevoked SecurityKind = "api_key_revoked"
)

// SecurityNotice è ciò che si comunica di un evento di sicurezza.
//
// **Non contiene la chiave, né il suo prefisso, né la sua impronta.** Della
// risorsa si porta il nome — l'etichetta che l'utente le ha dato — e nient'altro:
// è la stessa regola per cui [Key.LogValue] tiene il segreto fuori dai log, qui
// applicata a un messaggio che esce dal servizio.
type SecurityNotice struct {
	UserID string
	Kind   SecurityKind
	// ResourceName è il nome leggibile della chiave. Può essere vuoto.
	ResourceName string
	OccurredAt   time.Time
}

// SecurityNotifier riceve gli eventi di sicurezza di questo package.
//
// L'implementazione è internal/notify. Un errore non deve far fallire
// l'operazione: una chiave creata resta creata anche se l'avviso non parte, e
// restituire un errore al client significherebbe fargli credere che la chiave
// non esista quando invece esiste — cioè il peggior esito possibile per una
// credenziale.
type SecurityNotifier interface {
	SecurityEvent(ctx context.Context, notice SecurityNotice) error
}

// announce comunica l'evento, se c'è qualcuno che lo raccoglie.
func (s *Service) announce(ctx context.Context, kind SecurityKind, userID, resourceName string) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.SecurityEvent(ctx, SecurityNotice{
		UserID:       userID,
		Kind:         kind,
		ResourceName: resourceName,
		OccurredAt:   s.now(),
	}); err != nil {
		s.log.ErrorContext(ctx, "avviso di sicurezza non accodato",
			slog.String("user_id", userID),
			slog.String("kind", string(kind)),
			slog.Any("error", err))
	}
}
