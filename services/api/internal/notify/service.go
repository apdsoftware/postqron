package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Options configura il [Service]. Queue è l'unico campo obbligatorio.
type Options struct {
	Queue  Queue
	Policy Policy
	Logger *slog.Logger
	// Now sostituisce l'orologio. Serve ai test, e a nient'altro.
	Now func() time.Time
}

// Service è ciò che i package di dominio chiamano quando succede qualcosa.
//
// Espone un metodo per evento e nient'altro: chi lo usa non compone chiavi di
// deduplicazione, non sceglie un momento di partenza e non conosce la tabella.
// È deliberato — la politica anti-spam di [Policy] è una regola del prodotto, e
// una regola che ogni chiamante può reimplementare a modo suo non è una regola.
type Service struct {
	queue  Queue
	policy Policy
	log    *slog.Logger
	now    func() time.Time
}

// NewService costruisce il servizio.
func NewService(opts Options) (*Service, error) {
	if opts.Queue == nil {
		return nil, errors.New("notify: Queue è obbligatoria")
	}
	s := &Service{
		queue:  opts.Queue,
		policy: opts.Policy.withDefaults(),
		log:    opts.Logger,
		now:    opts.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// Policy restituisce la politica in vigore. Serve ai test e alla diagnostica.
func (s *Service) Policy() Policy { return s.policy }

// ---------------------------------------------------------------- benvenuto

// Welcome accoda l'email di benvenuto.
//
// Non serve l'indirizzo: lo legge il corriere al momento dell'invio, dalla
// stessa riga di `users` da cui legge la lingua. Vedi [Request.UserID].
func (s *Service) Welcome(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return errors.New("notify: benvenuto senza utente")
	}
	return s.enqueue(ctx, Request{
		Event:       EventWelcome,
		Channel:     ChannelEmail,
		UserID:      userID,
		DedupeKey:   welcomeKey(userID),
		ScheduledAt: s.now(),
	})
}

// ------------------------------------------------------------- job fallito

// JobFailure è un'esecuzione che si è chiusa male e per cui non ci saranno
// altri tentativi.
//
// **Non contiene testo dell'errore né corpo della risposta**, e non è una
// dimenticanza: vedi [FailureKind].
type JobFailure struct {
	UserID      string
	JobID       string
	JobName     string
	Environment string

	// OccurredAt è l'istante dell'ultimo tentativo.
	OccurredAt time.Time
	// ScheduledFor e Attempt individuano la riga di `job_executions` da cui
	// l'avviso nasce. Servono a ritrovarla, non a comporre il testo.
	ScheduledFor time.Time
	Attempt      int

	Kind FailureKind
	// HTTPStatus vale solo con Kind uguale a [FailureHTTPStatus].
	HTTPStatus int
}

// JobFailed accoda un avviso di job fallito, applicando la politica di [Policy].
//
// Chiamarla a ogni fallimento è **il modo previsto di usarla**: il
// raggruppamento e il tetto stanno qui e nell'indice unico della 0008, non nella
// disciplina di chi chiama. Un motore che dovesse ricordarsi di non chiamarla
// troppo spesso sarebbe un motore con dentro una seconda politica anti-spam,
// scritta peggio.
func (s *Service) JobFailed(ctx context.Context, f JobFailure) error {
	if strings.TrimSpace(f.UserID) == "" || strings.TrimSpace(f.JobID) == "" {
		return fmt.Errorf("notify: avviso di job fallito senza utente o job (utente %q, job %q)", f.UserID, f.JobID)
	}
	if strings.TrimSpace(f.Environment) == "" {
		return errors.New("notify: avviso di job fallito senza ambiente")
	}

	now := s.now()
	occurred := f.OccurredAt
	if occurred.IsZero() {
		occurred = now
	}
	kind := f.Kind
	if kind == "" {
		kind = FailureUnknown
	}

	return s.enqueue(ctx, Request{
		Event:   EventJobFailed,
		Channel: ChannelEmail,
		UserID:  f.UserID,
		// Il bucket si calcola su `now` e non su `occurred`: è il tetto sugli
		// invii, e gli invii avvengono adesso. Un'occorrenza recuperata con ore
		// di ritardo non deve poter riaprire un bucket già chiuso.
		DedupeKey: s.policy.failureKey(f.JobID, f.Environment, now),
		// La grazia è ciò che trasforma una raffica in un messaggio solo.
		ScheduledAt:           now.Add(s.policy.FailureGrace),
		JobID:                 f.JobID,
		Environment:           f.Environment,
		ExecutionScheduledFor: f.ScheduledFor,
		ExecutionAttempt:      f.Attempt,
		Payload: Payload{
			JobName:       f.JobName,
			Failures:      1,
			LastAttemptAt: occurred.UTC(),
			FailureKind:   kind,
			HTTPStatus:    f.HTTPStatus,
		},
	})
}

// ----------------------------------------------------------- cambio di piano

// PlanChange è una variazione di piano già scritta, con l'esito di R58.
type PlanChange struct {
	UserID string
	// PreviousPlan e NewPlan sono i **nomi** dei piani, quelli che l'utente
	// legge in listino. Se coincidono non c'è niente da comunicare.
	PreviousPlan string
	NewPlan      string
	EffectiveAt  time.Time

	// SuspendedByJobLimit e SuspendedByResolution sono i job che il cambio ha
	// fermato, per motivo (R58). Restano due numeri perché sono due rimedi.
	SuspendedByJobLimit   int
	SuspendedByResolution int
}

// PlanChanged accoda la notifica di variazione piano.
//
// Una variazione in cui il piano non cambia **non produce niente**, e il
// controllo sta qui perché è qui che si conoscono entrambi i nomi. Senza, il
// rinnovo mensile di un abbonamento — che è un evento Paddle a tutti gli
// effetti, con lo stesso piano prima e dopo — manderebbe all'utente un'email che
// annuncia un cambiamento che non è avvenuto.
func (s *Service) PlanChanged(ctx context.Context, c PlanChange) error {
	if strings.TrimSpace(c.UserID) == "" {
		return errors.New("notify: variazione di piano senza utente")
	}
	previous := strings.TrimSpace(c.PreviousPlan)
	next := strings.TrimSpace(c.NewPlan)
	if previous == "" || next == "" {
		return fmt.Errorf("notify: variazione di piano incompleta (da %q a %q)", c.PreviousPlan, c.NewPlan)
	}
	if previous == next {
		s.log.DebugContext(ctx, "nessuna email: il piano non è cambiato",
			slog.String("user_id", c.UserID), slog.String("plan", next))
		return nil
	}

	effective := c.EffectiveAt
	if effective.IsZero() {
		effective = s.now()
	}
	effective = effective.UTC()

	return s.enqueue(ctx, Request{
		Event:       EventPlanChanged,
		Channel:     ChannelEmail,
		UserID:      c.UserID,
		DedupeKey:   planKey(c.UserID, next, effective),
		ScheduledAt: s.now(),
		Payload: Payload{
			PreviousPlan:          previous,
			NewPlan:               next,
			EffectiveAt:           effective,
			SuspendedByJobLimit:   max(c.SuspendedByJobLimit, 0),
			SuspendedByResolution: max(c.SuspendedByResolution, 0),
		},
	})
}

// -------------------------------------------------------------- sicurezza

// SecurityEvent è un fatto già avvenuto che riguarda la sicurezza dell'account.
//
// **Non porta token**, e non deve poterlo fare: i flussi che consegnano un
// valore monouso — conferma dell'indirizzo, recupero password — appartengono
// all'autenticazione e non passano di qui. Della risorsa coinvolta si porta il
// nome, mai il valore.
type SecurityEvent struct {
	UserID string
	Kind   SecurityKind
	// OccurredAt è quando il fatto è accaduto. Zero vale adesso.
	OccurredAt time.Time
	// ResourceName è l'etichetta della risorsa coinvolta. Facoltativo.
	ResourceName string
	// SourceIP è l'indirizzo da cui è partita l'azione. Facoltativo.
	SourceIP string
}

// Security accoda un avviso di sicurezza.
func (s *Service) Security(ctx context.Context, e SecurityEvent) error {
	if strings.TrimSpace(e.UserID) == "" {
		return errors.New("notify: evento di sicurezza senza utente")
	}
	if e.Kind == "" {
		return errors.New("notify: evento di sicurezza senza tipo")
	}

	now := s.now()
	occurred := e.OccurredAt
	if occurred.IsZero() {
		occurred = now
	}

	return s.enqueue(ctx, Request{
		Event:     EventSecurity,
		Channel:   ChannelEmail,
		UserID:    e.UserID,
		DedupeKey: s.policy.securityKey(e.UserID, e.Kind, now),
		// Nessuna grazia: un avviso di sicurezza in ritardo vale meno di uno
		// puntuale, e non c'è niente da raggruppare che valga quel ritardo.
		ScheduledAt: now,
		Payload: Payload{
			SecurityKind: e.Kind,
			OccurredAt:   occurred.UTC(),
			ResourceName: e.ResourceName,
			SourceIP:     e.SourceIP,
		},
	})
}

// ---------------------------------------------------------------- interno

// enqueue scrive in coda e registra l'esito.
//
// Il log non nomina mai il destinatario: l'identificativo dell'utente basta a
// ritrovarlo e non è un dato personale sparso in un file di testo.
func (s *Service) enqueue(ctx context.Context, req Request) error {
	result, err := s.queue.Enqueue(ctx, req)
	if err != nil {
		return fmt.Errorf("notify: accodamento di %s: %w", req.Event, err)
	}

	switch result {
	case Grouped:
		s.log.DebugContext(ctx, "notifica raggruppata in un avviso già in coda",
			slog.String("event", string(req.Event)),
			slog.String("user_id", req.UserID),
			slog.String("dedupe_key", req.DedupeKey))
	case NoRecipient:
		s.log.DebugContext(ctx, "notifica senza destinatario, non accodata",
			slog.String("event", string(req.Event)),
			slog.String("user_id", req.UserID))
	default:
		s.log.InfoContext(ctx, "notifica accodata",
			slog.String("event", string(req.Event)),
			slog.String("user_id", req.UserID),
			slog.Time("scheduled_at", req.ScheduledAt))
	}
	return nil
}
