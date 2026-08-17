package notify

import (
	"context"
	"fmt"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
)

// AuthMailer aggancia l'autenticazione alla coda.
//
// È l'implementazione che [auth.Mailer] annunciava fin dalla #396:
// «l'implementazione reale — client Mailronix (#419) o inserimento nella coda
// `notifications` della 0008 — si aggancia qui». Le due clausole del contratto
// di quel tipo sono soddisfatte per costruzione:
//
//   - **non blocca a lungo**: accodare è un INSERT, il recapito avviene altrove;
//   - **l'errore non è osservabile dal client**: [auth.Service] lo registra e
//     non lo mostra, e questo tipo non gli dà niente di più preciso da mostrare.
//
// # I tre messaggi che non passano di qui
//
// [auth.MessageKind] ne elenca cinque; questo aggancio ne copre due:
//
//	auth.KindWelcome          →  benvenuto (R21)
//	auth.KindPasswordChanged  →  evento di sicurezza (R21)
//
// Gli altri tre — verifica dell'indirizzo, tentativo di registrazione su un
// indirizzo già preso, recupero password — **portano un token monouso**, e né
// [Payload] né [emailrender.SecurityAlertData] hanno un campo che possa
// ospitarlo: è una proprietà voluta e verificata da un test in entrambi i
// package. Servono tre template nuovi e un tipo di dati che il token lo accetti,
// e sono lavoro di un'altra issue. Fino ad allora restituiscono
// [mailronix.ErrNoTemplate] — lo stesso errore dell'altra implementazione,
// perché è lo stesso fatto, e `errors.Is` deve riconoscerlo da entrambe.
type AuthMailer struct {
	service *Service
}

// NewAuthMailer costruisce l'aggancio.
func NewAuthMailer(service *Service) (*AuthMailer, error) {
	if service == nil {
		return nil, fmt.Errorf("notify: NewAuthMailer richiede un Service")
	}
	return &AuthMailer{service: service}, nil
}

// AuthMailer soddisfa il contratto della #396.
var _ auth.Mailer = (*AuthMailer)(nil)

// Send accoda il messaggio dell'autenticazione.
func (m *AuthMailer) Send(ctx context.Context, msg auth.Message) error {
	if msg.UserID == "" {
		// Senza account non c'è a chi mandare: la coda è per utente, non per
		// indirizzo. È il caso del tentativo di registrazione su un indirizzo
		// libero, che comunque non ha un template.
		return fmt.Errorf("%w: %s non è legato a un account", mailronix.ErrNoTemplate, msg.Kind)
	}

	switch msg.Kind {
	case auth.KindWelcome:
		return m.service.Welcome(ctx, msg.UserID)

	case auth.KindPasswordChanged:
		// Un cambio o una reimpostazione della password è esattamente l'evento
		// di sicurezza `password_reset` di R21: racconta un fatto avvenuto e non
		// porta token.
		return m.service.Security(ctx, SecurityEvent{
			UserID: msg.UserID,
			Kind:   SecurityPasswordChanged,
		})

	case auth.KindEmailVerification, auth.KindRegistrationAttempt, auth.KindPasswordReset:
		return fmt.Errorf("%w: %s porta un token monouso e richiede un template che emails/templates/ non ha ancora",
			mailronix.ErrNoTemplate, msg.Kind)

	default:
		return fmt.Errorf("%w: tipo sconosciuto %q", mailronix.ErrNoTemplate, msg.Kind)
	}
}
