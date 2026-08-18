package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
)

// Renderer è la parte di [emailrender.Renderer] che serve al corriere.
type Renderer interface {
	Render(event emailrender.Event, language string, data any) (emailrender.Message, error)
}

// Sender è la parte del client Mailronix che serve al corriere. Averla come
// interfaccia è ciò che permette di provare tutto questo package senza una sola
// chiamata di rete.
type Sender interface {
	Send(ctx context.Context, email mailronix.Email) (mailronix.Receipt, error)
}

// CourierOptions configura il [Courier]. Queue, Renderer e Sender sono
// obbligatori.
type CourierOptions struct {
	Queue    Queue
	Renderer Renderer
	Sender   Sender

	Logger *slog.Logger
	Now    func() time.Time

	// Batch è quante notifiche si prendono in carico per passata.
	Batch int
	// Lease è per quanto una notifica presa in carico resta invisibile agli
	// altri corrieri. Vedi [Queue.Due].
	Lease time.Duration
	// MaxAttempts è il numero di tentativi di recapito, primo compreso, oltre il
	// quale la notifica si chiude `failed`.
	MaxAttempts int
	// Backoff è l'attesa prima del tentativo successivo. `attempt` è il numero
	// del tentativo appena fallito, a partire da 1.
	Backoff func(attempt int) time.Duration
}

// Valori predefiniti del corriere.
const (
	DefaultBatch       = 50
	DefaultLease       = 2 * time.Minute
	DefaultMaxAttempts = 5
	// DefaultInterval è ogni quanto [Courier.Run] guarda la coda. Trenta secondi
	// sono molto meno della finestra di grazia di [Policy]: il ritardo che
	// aggiunge è invisibile accanto ai cinque minuti che il raggruppamento
	// impone di proposito.
	DefaultInterval = 30 * time.Second
)

// Courier svuota la coda: compila, recapita, annota.
//
// È l'unico punto in cui i tre package si incontrano davvero, e l'unico che
// parla con la rete. Tutto ciò che decide *se* mandare sta a monte, in
// [Service] e [Policy]: qui non c'è nessuna politica, solo un tentativo di
// recapito e la registrazione onesta di com'è andato.
type Courier struct {
	queue    Queue
	renderer Renderer
	sender   Sender

	log         *slog.Logger
	now         func() time.Time
	batch       int
	lease       time.Duration
	maxAttempts int
	backoff     func(attempt int) time.Duration
}

// NewCourier costruisce il corriere.
func NewCourier(opts CourierOptions) (*Courier, error) {
	switch {
	case opts.Queue == nil:
		return nil, errors.New("notify: NewCourier richiede una Queue")
	case opts.Renderer == nil:
		return nil, errors.New("notify: NewCourier richiede un Renderer")
	case opts.Sender == nil:
		return nil, errors.New("notify: NewCourier richiede un Sender")
	}

	c := &Courier{
		queue:       opts.Queue,
		renderer:    opts.Renderer,
		sender:      opts.Sender,
		log:         opts.Logger,
		now:         opts.Now,
		batch:       opts.Batch,
		lease:       opts.Lease,
		maxAttempts: opts.MaxAttempts,
		backoff:     opts.Backoff,
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.batch <= 0 {
		c.batch = DefaultBatch
	}
	if c.lease <= 0 {
		c.lease = DefaultLease
	}
	if c.maxAttempts <= 0 {
		c.maxAttempts = DefaultMaxAttempts
	}
	if c.backoff == nil {
		c.backoff = defaultBackoff
	}
	return c, nil
}

// Stats è il resoconto di una passata.
//
// Non esiste un contatore «recapitate», e la mancanza è il punto: R20.1 dice che
// quel numero non è conoscibile. `Sent` conta i messaggi che Mailronix ha preso
// in carico, che è un'altra cosa e va chiamata con un altro nome.
type Stats struct {
	// Sent sono le notifiche consegnate a Mailronix.
	Sent int
	// Retried sono quelle il cui recapito è fallito in modo transitorio e che
	// torneranno in coda.
	Retried int
	// Failed sono quelle che non partiranno più.
	Failed int
	// Skipped sono quelle che non ha più senso mandare, per esempio a un
	// account chiuso dopo l'accodamento.
	Skipped int
}

// Total è il numero di notifiche prese in carico nella passata.
func (s Stats) Total() int { return s.Sent + s.Retried + s.Failed + s.Skipped }

// Deliver esegue una passata sulla coda.
//
// L'errore restituito riguarda la **coda**, non un recapito: un'email che non
// parte chiude la propria riga e non ferma le altre. È la stessa ragione per cui
// il fallimento di un invio non deve far fallire l'operazione che lo ha
// causato, applicata un livello più in basso.
func (c *Courier) Deliver(ctx context.Context) (Stats, error) {
	now := c.now()
	pending, err := c.queue.Due(ctx, now, c.batch, c.lease)
	if err != nil {
		return Stats{}, fmt.Errorf("notify: lettura della coda: %w", err)
	}

	var stats Stats
	for _, p := range pending {
		if ctx.Err() != nil {
			// L'arresto è in corso: le notifiche non ancora trattate hanno un
			// contratto a scadenza e torneranno da sole.
			return stats, ctx.Err()
		}
		c.deliverOne(ctx, p, &stats)
	}
	return stats, nil
}

// Run svuota la coda a intervalli regolari, fino all'annullamento del contesto.
func (c *Courier) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			stats, err := c.Deliver(ctx)
			switch {
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				return err
			case err != nil:
				// La coda non è raggiungibile: si riprova al giro dopo. Fermare
				// il corriere lascerebbe le notifiche in attesa fino al riavvio
				// del processo.
				c.log.ErrorContext(ctx, "passata sulla coda delle notifiche fallita", slog.Any("error", err))
			case stats.Total() > 0:
				c.log.InfoContext(ctx, "coda delle notifiche svuotata",
					slog.Int("sent", stats.Sent),
					slog.Int("retried", stats.Retried),
					slog.Int("failed", stats.Failed),
					slog.Int("skipped", stats.Skipped))
			}
		}
	}
}

// deliverOne porta una notifica a uno stato terminale, o la rimette in coda.
func (c *Courier) deliverOne(ctx context.Context, p Pending, stats *Stats) {
	now := c.now()

	if p.Recipient.Closed {
		c.close(ctx, p, "account chiuso dopo l'accodamento", now, c.queue.MarkSkipped)
		stats.Skipped++
		return
	}

	message, err := c.render(p)
	if err != nil {
		// Un template che non compila non compilerà nemmeno fra dieci minuti:
		// ritentare consumerebbe la coda senza cambiare esito. La riga si chiude
		// e l'errore resta scritto, che è il modo di accorgersene.
		c.log.ErrorContext(ctx, "notifica non compilabile",
			slog.String("id", p.ID),
			slog.String("event", string(p.Event)),
			slog.Any("error", err))
		c.close(ctx, p, err.Error(), now, c.queue.MarkFailed)
		stats.Failed++
		return
	}

	receipt, err := c.sender.Send(ctx, mailronix.Email{
		To:      p.Recipient.Email,
		Subject: message.Subject,
		HTML:    message.HTML,
		Text:    message.Text,
	})
	if err != nil {
		c.handleSendError(ctx, p, err, now, stats)
		return
	}

	if err := c.queue.MarkSent(ctx, p.ID, receipt.EmailLogID, now); err != nil {
		// Il messaggio è partito e la riga è rimasta in `pending`: alla scadenza
		// del contratto tornerà in coda e l'utente riceverà un doppione. È il
		// male minore rispetto all'alternativa — dare per chiusa una riga che
		// non lo è — e va lasciato scritto perché chi legge i log lo riconosca.
		c.log.ErrorContext(ctx, "notifica consegnata a Mailronix ma non chiusa in coda: possibile doppione",
			slog.String("id", p.ID),
			slog.String("email_log_id", receipt.EmailLogID),
			slog.Any("error", err))
		stats.Failed++
		return
	}

	// Si registra l'identificativo e nulla di più: né l'indirizzo, che è un dato
	// personale, né una parola sul recapito, che questa risposta non dice
	// (R20.1).
	c.log.InfoContext(ctx, "email accodata presso Mailronix",
		slog.String("id", p.ID),
		slog.String("event", string(p.Event)),
		slog.String("user_id", p.Recipient.UserID),
		slog.String("language", message.Language),
		slog.String("email_log_id", receipt.EmailLogID))
	stats.Sent++
}

// handleSendError decide se il recapito fallito merita un altro tentativo.
func (c *Courier) handleSendError(ctx context.Context, p Pending, sendErr error, now time.Time, stats *Stats) {
	permanent := !mailronix.Retryable(sendErr)
	exhausted := p.Attempts >= c.maxAttempts

	if permanent || exhausted {
		reason := sendErr.Error()
		if exhausted && !permanent {
			reason = fmt.Sprintf("%d tentativi di recapito esauriti: %s", p.Attempts, sendErr)
		}
		c.log.ErrorContext(ctx, "notifica non recapitabile",
			slog.String("id", p.ID),
			slog.String("event", string(p.Event)),
			slog.String("user_id", p.Recipient.UserID),
			slog.Int("attempts", p.Attempts),
			slog.Bool("permanent", permanent),
			slog.Any("error", sendErr))
		c.close(ctx, p, reason, now, c.queue.MarkFailed)
		stats.Failed++
		return
	}

	wait := c.backoff(p.Attempts)
	// Un Retry-After esplicito ha la precedenza: è il servizio a dire quando
	// tornare, e ignorarlo è il modo più rapido di prendersi un altro 429.
	if hinted, ok := mailronix.RetryAfter(sendErr); ok && hinted > wait {
		wait = hinted
	}
	if err := c.queue.Retry(ctx, p.ID, now.Add(wait), sendErr.Error()); err != nil {
		c.log.ErrorContext(ctx, "notifica non rimessa in coda",
			slog.String("id", p.ID), slog.Any("error", err))
		stats.Failed++
		return
	}
	c.log.WarnContext(ctx, "recapito fallito, la notifica torna in coda",
		slog.String("id", p.ID),
		slog.String("event", string(p.Event)),
		slog.Int("attempts", p.Attempts),
		slog.Duration("wait", wait),
		slog.Any("error", sendErr))
	stats.Retried++
}

// close chiude una riga con lo stato indicato, registrando l'eventuale guasto
// della chiusura stessa.
func (c *Courier) close(
	ctx context.Context,
	p Pending,
	reason string,
	at time.Time,
	mark func(context.Context, string, string, time.Time) error,
) {
	if err := mark(ctx, p.ID, reason, at); err != nil {
		c.log.ErrorContext(ctx, "notifica non chiusa in coda",
			slog.String("id", p.ID), slog.Any("error", err))
	}
}

// render traduce una notifica in un messaggio compilato.
//
// Fra la scelta del template e la compilazione c'è un controllo, ed è il
// guardiano del confine dichiarato nella doc di questo package: **da qui non
// passa marketing**. Vedi [errMarketingInCodaTransazionale].
func (c *Courier) render(p Pending) (emailrender.Message, error) {
	event, data, err := transactionalTemplate(p)
	if err != nil {
		return emailrender.Message{}, err
	}

	if err := assertTransactional(event); err != nil {
		return emailrender.Message{}, err
	}

	return c.renderer.Render(event, p.Recipient.Language, data)
}

// assertTransactional rifiuta un template che non sia transazionale.
//
// Un evento senza natura dichiarata viene rifiutato come il marketing, e non è
// prudenza eccessiva: le due direzioni dell'errore non si equivalgono. Mandare
// una comunicazione di marketing senza consenso e senza disiscrizione è un
// illecito; non mandare un'email finché qualcuno non ha dichiarato che cos'è
// costa una riga `failed` con scritto il perché.
func assertTransactional(event emailrender.Event) error {
	kind, declared := emailrender.KindOf(event)
	switch {
	case !declared:
		return fmt.Errorf("%w: %q non dichiara la propria natura", errMarketingInCodaTransazionale, event)
	case kind != emailrender.KindTransactional:
		return fmt.Errorf("%w: %q è %q", errMarketingInCodaTransazionale, event, kind)
	}
	return nil
}

// errMarketingInCodaTransazionale è il rifiuto di recapitare una comunicazione
// di marketing dalla coda delle notifiche.
//
// La coda non ha, e non deve avere, un modo di verificare il consenso
// dell'Art. 6(1)(a): la sua politica è quella anti-spam degli avvisi, che
// raggruppa e limita ma **manda sempre**, perché un avviso di sicurezza va
// mandato. Una comunicazione di marketing che finisse qui partirebbe senza che
// nessuno abbia guardato se l'utente l'ha chiesta, e senza il link di
// disiscrizione che il layout scrive solo per gli eventi di marketing.
//
// Il controllo è a valle della scelta del template e non della scrittura in
// coda, perché è lì che si conosce la natura dell'evento: chi accoda vede un
// [Event] di questo package, che è già un dominio chiuso di eventi
// transazionali, e il modo in cui il confine si romperebbe è che qualcuno
// aggiunga un caso a [transactionalTemplate] puntando a un template di
// marketing. Il controllo sta dove quel gesto si vede.
var errMarketingInCodaTransazionale = errors.New(
	"notify: la coda delle notifiche non recapita comunicazioni di marketing " +
		"(privacy policy §2.8: consenso separato e disiscrizione obbligatoria, che qui non si verificano)")

// transactionalTemplate sceglie template e contesto per una notifica.
//
// È l'unico punto in cui i nomi dei quattro eventi del database diventano quelli
// dei quattro template — `security` di qua, `security_alert` di là — e l'unico
// in cui il [Payload] diventa il contesto tipato che emailrender pretende. Un
// campo del payload che non compare qui non compare in nessuna email.
func transactionalTemplate(p Pending) (emailrender.Event, any, error) {
	switch p.Event {
	case EventWelcome:
		name := p.Recipient.Name
		if name == "" {
			name = p.Payload.RecipientName
		}
		return emailrender.EventWelcome, emailrender.WelcomeData{RecipientName: name}, nil

	case EventJobFailed:
		failures := p.Payload.Failures
		if failures < 1 {
			// Un avviso esiste perché almeno un fallimento c'è stato: una riga
			// con zero è una riga scritta male, non un avviso senza fallimenti.
			failures = 1
		}
		return emailrender.EventJobFailed, emailrender.JobFailedData{
			JobID:               p.JobID,
			JobName:             p.Payload.JobName,
			Environment:         emailrender.Environment(p.Environment),
			ConsecutiveFailures: failures,
			LastAttemptAt:       p.Payload.LastAttemptAt,
			FailureKind:         emailrender.FailureKind(p.Payload.FailureKind),
			HTTPStatus:          p.Payload.HTTPStatus,
		}, nil

	case EventPlanChanged:
		return emailrender.EventPlanChanged, emailrender.PlanChangedData{
			PreviousPlan:          p.Payload.PreviousPlan,
			NewPlan:               p.Payload.NewPlan,
			EffectiveAt:           p.Payload.EffectiveAt,
			SuspendedByJobLimit:   p.Payload.SuspendedByJobLimit,
			SuspendedByResolution: p.Payload.SuspendedByResolution,
		}, nil

	case EventSecurity:
		return emailrender.EventSecurityAlert, emailrender.SecurityAlertData{
			Kind:         emailrender.SecurityEventKind(p.Payload.SecurityKind),
			OccurredAt:   p.Payload.OccurredAt,
			ResourceName: p.Payload.ResourceName,
			SourceIP:     p.Payload.SourceIP,
		}, nil

	default:
		// `job_recovered` esiste nell'enumerato della 0001 ma non fra gli eventi
		// di R21, e non ha un template. Arrivare qui significa che qualcuno lo ha
		// accodato: meglio una riga `failed` con scritto perché che un'email a
		// caso.
		return "", nil, fmt.Errorf("evento senza template: %q", p.Event)
	}
}

// defaultBackoff attende un minuto, poi due, quattro, otto, fino a mezz'ora.
//
// È molto più lento del backoff interno del client Mailronix, e di proposito:
// quello copre l'inciampo di una richiesta, questo copre un servizio giù. Un
// avviso che arriva con dieci minuti di ritardo è ancora utile; una coda che
// ritenta ogni secondo contro un servizio in avaria non lo è per nessuno.
func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	wait := time.Minute << (attempt - 1)
	if wait > 30*time.Minute || wait <= 0 {
		return 30 * time.Minute
	}
	return wait
}
