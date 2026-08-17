package auth

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// MessageKind è il tipo di messaggio che l'autenticazione ha bisogno di far
// recapitare. Il valore corrisponde uno a uno a un template di
// `emails/templates/` (R19), che però non è responsabilità di questo package.
type MessageKind string

const (
	// KindEmailVerification accompagna una registrazione riuscita: contiene il
	// token con cui confermare l'indirizzo.
	KindEmailVerification MessageKind = "email_verification"

	// KindRegistrationAttempt va all'indirizzo di un account che esiste già,
	// quando qualcuno prova a registrarsi con quell'indirizzo.
	//
	// Esiste per una ragione precisa: se la registrazione risponde allo stesso
	// modo per un indirizzo libero e per uno preso — ed è quello che fa, per non
	// permettere l'enumerazione — allora l'unico canale che può dire al
	// proprietario «qualcuno ha provato a registrarsi con il tuo indirizzo, e se
	// eri tu la password l'hai già» è l'email. Senza questo messaggio la
	// protezione contro l'enumerazione diventa un buco di usabilità.
	KindRegistrationAttempt MessageKind = "registration_attempt"

	// KindPasswordReset contiene il token di recupero password.
	KindPasswordReset MessageKind = "password_reset"

	// KindPasswordChanged è la notifica di sicurezza dopo un cambio o una
	// reimpostazione della password (SPEC R21, evento `security`). Non contiene
	// token: è un avviso, e serve perché un cambio password non richiesto è il
	// primo segno di un account compromesso.
	KindPasswordChanged MessageKind = "password_changed"

	// KindWelcome è il benvenuto di R21, e parte alla **conferma
	// dell'indirizzo**, non alla registrazione.
	//
	// I due momenti non sono intercambiabili. Alla registrazione l'indirizzo è
	// una stringa che qualcuno ha scritto in un modulo: mandarci subito due
	// email — la conferma e il benvenuto — significa raddoppiare il volume verso
	// indirizzi che possono non esistere, che è il modo più semplice di
	// accumulare bounce e finire in suppression list. Alla conferma, invece,
	// l'indirizzo ha dimostrato di funzionare e l'account è reale: è il primo
	// momento in cui il benvenuto ha un destinatario vero e qualcosa da dire.
	//
	// Non contiene token: il token è quello che l'utente ha appena consumato.
	KindWelcome MessageKind = "welcome"
)

// Message è quanto l'autenticazione sa dire sull'email da recapitare.
//
// Deliberatamente non contiene né HTML, né oggetto, né URL: i template sono
// della issue #418 e il client Mailronix della #419. In particolare **non c'è il
// link**, solo il token: comporre l'URL richiede di sapere dov'è il frontend, e
// questo package non lo sa né deve saperlo.
type Message struct {
	// Kind seleziona il template.
	Kind MessageKind
	// To è l'indirizzo del destinatario, nella forma in cui l'utente lo ha
	// scritto.
	To string
	// UserID è l'account coinvolto, quando esiste. Vuoto per i messaggi che
	// riguardano un indirizzo senza account.
	UserID string
	// Token è il segreto monouso che il template deve trasformare in un link.
	// Vuoto per i messaggi che non ne hanno uno.
	Token string
	// ExpiresAt è la scadenza del token, da mostrare nel testo. Zero se non
	// pertinente.
	ExpiresAt time.Time
}

// Mailer è l'unica cosa che l'autenticazione sa dell'invio di email.
//
// L'implementazione reale — client Mailronix (#419) o inserimento nella coda
// `notifications` della 0008 — si aggancia qui senza toccare questo package.
//
// Il contratto ha due clausole che l'implementazione deve rispettare:
//
//   - **Send non deve bloccare a lungo.** Il Service la chiama comunque fuori
//     dal percorso della risposta HTTP (vedi [Service.dispatch]), ma un invio
//     sincrono verso un servizio esterno lento farebbe accumulare goroutine.
//     Inserire in coda e uscire è il comportamento previsto.
//   - **L'errore non è osservabile dal client.** SPEC R20.1 dice che il recapito
//     non si deduce dalla risposta di Mailronix; qui vale di più: se Send
//     fallisse e il Service lo riportasse all'utente, la differenza fra un
//     indirizzo con account e uno senza tornerebbe visibile. L'errore viene
//     registrato e nient'altro.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// LogMailer è l'implementazione usata finché #419 non è pronta.
//
// Registra che un messaggio *sarebbe* partito, con lo scopo e l'account, e
// **mai** né il token né l'indirizzo: il token è un segreto e l'indirizzo è un
// dato personale che nel log non serve (SPEC §5). Chi sviluppa in locale e ha
// bisogno del link lo ottiene dal database, non dal log.
type LogMailer struct {
	Logger *slog.Logger
}

// Send registra il messaggio senza recapitarlo.
func (m LogMailer) Send(ctx context.Context, msg Message) error {
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.InfoContext(ctx, "email non inviata: nessun mailer configurato (#419)",
		slog.String("kind", string(msg.Kind)),
		slog.String("user_id", msg.UserID))
	return nil
}

// MemoryMailer trattiene i messaggi in memoria.
//
// È il doppio per i test — quelli di questo package e quelli di internal/httpapi
// — e serve anche in sviluppo a chi vuole leggere un token di reset senza
// aprire psql. Non va usata in produzione: i token restano in memoria in chiaro.
type MemoryMailer struct {
	mu   sync.Mutex
	sent []Message
	// Err, se valorizzato, è l'errore che Send restituisce. Serve a verificare
	// che un invio fallito non cambi la risposta al client.
	Err error
}

// Send memorizza il messaggio.
func (m *MemoryMailer) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return m.Err
}

// Sent restituisce una copia dei messaggi accumulati.
func (m *MemoryMailer) Sent() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.sent...)
}

// Last restituisce l'ultimo messaggio del tipo indicato, e se ne è stato
// trovato uno.
func (m *MemoryMailer) Last(kind MessageKind) (Message, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if m.sent[i].Kind == kind {
			return m.sent[i], true
		}
	}
	return Message{}, false
}

// Reset svuota l'elenco.
func (m *MemoryMailer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = nil
}
