package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
	"github.com/apdsoftware/postqron/services/api/internal/notify"
)

func newAuthMailer(t *testing.T) (*notify.AuthMailer, *notify.MemoryQueue) {
	t.Helper()
	queue := notify.NewMemoryQueue()
	service, err := notify.NewService(notify.Options{Queue: queue})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	mailer, err := notify.NewAuthMailer(service)
	if err != nil {
		t.Fatalf("NewAuthMailer: %v", err)
	}
	return mailer, queue
}

// I due messaggi dell'autenticazione che hanno un template finiscono in coda,
// ciascuno sull'evento giusto.
func TestLAutenticazioneAccodaBenvenutoESicurezza(t *testing.T) {
	mailer, queue := newAuthMailer(t)
	ctx := context.Background()

	if err := mailer.Send(ctx, auth.Message{Kind: auth.KindWelcome, UserID: "u-1"}); err != nil {
		t.Fatalf("benvenuto: %v", err)
	}
	if err := mailer.Send(ctx, auth.Message{
		Kind: auth.KindPasswordChanged, UserID: "u-1", To: "sam@example.test",
	}); err != nil {
		t.Fatalf("cambio password: %v", err)
	}

	if got := len(queue.OfEvent(notify.EventWelcome)); got != 1 {
		t.Errorf("benvenuti accodati: %d", got)
	}
	security := queue.OfEvent(notify.EventSecurity)
	if len(security) != 1 {
		t.Fatalf("eventi di sicurezza accodati: %d", len(security))
	}
	if security[0].Payload.SecurityKind != notify.SecurityPasswordChanged {
		t.Errorf("tipo dell'evento di sicurezza: %q", security[0].Payload.SecurityKind)
	}
}

// I tre messaggi che portano un token restano fuori, con l'errore che dice
// perché. Non è una regressione: oggi non partono comunque, e un errore
// riconoscibile è meglio di un silenzio.
func TestIMessaggiConTokenRestanoFuoriDallaCoda(t *testing.T) {
	mailer, queue := newAuthMailer(t)
	ctx := context.Background()

	kinds := []auth.MessageKind{
		auth.KindEmailVerification,
		auth.KindRegistrationAttempt,
		auth.KindPasswordReset,
	}
	for _, kind := range kinds {
		err := mailer.Send(ctx, auth.Message{
			Kind: kind, UserID: "u-1", To: "sam@example.test", Token: "segreto-monouso",
		})
		if !errors.Is(err, mailronix.ErrNoTemplate) {
			t.Errorf("%s: errore %v, atteso ErrNoTemplate", kind, err)
		}
	}
	if queue.Len() != 0 {
		t.Errorf("notifiche accodate: %d, attese zero — nessun token deve finire in una colonna", queue.Len())
	}
}

// Un messaggio senza account non ha un destinatario in coda: la coda è per
// utente, non per indirizzo.
func TestUnMessaggioSenzaAccountNonEntraInCoda(t *testing.T) {
	mailer, queue := newAuthMailer(t)

	err := mailer.Send(context.Background(), auth.Message{
		Kind: auth.KindRegistrationAttempt, To: "sconosciuto@example.test",
	})
	if !errors.Is(err, mailronix.ErrNoTemplate) {
		t.Errorf("errore: %v", err)
	}
	if queue.Len() != 0 {
		t.Errorf("notifiche accodate: %d", queue.Len())
	}
}
