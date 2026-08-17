package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// Dispatcher avvisa il motore che c'è un'esecuzione da servire subito.
//
// Il motore (issue #388) è l'unico che esegue: l'API non fa richieste HTTP verso
// il target, mai. Un trigger manuale è una **riga in `job_executions`** con
// `triggered_by = 'manual'` e stato `pending`, che è la stessa forma con cui il
// motore prende in carico un'occorrenza schedulata (R4, migrazione 0006).
//
// Con Dispatcher nil il motore la trova al giro successivo del suo ciclo: la
// riga c'è, l'esecuzione avverrà. L'interfaccia esiste perché quando #388 avrà
// una coda in memoria possa svegliarla subito invece di far aspettare l'utente
// che ha appena premuto «esegui adesso», e perché il confine sia dichiarato
// adesso invece che scoperto al merge.
type Dispatcher interface {
	// Notify è un suggerimento, non una consegna: non deve bloccare e il suo
	// errore non è un errore della richiesta HTTP.
	Notify(ctx context.Context, exec Execution)
}

// Options sono le dipendenze del [Service].
type Options struct {
	// Store è obbligatorio.
	Store Store
	// Logger è obbligatorio.
	Logger *slog.Logger
	// Guard è il controllo sul target (R38). Può essere nil finché la issue
	// #455 non lo fornisce: vedi [TargetGuard].
	Guard TargetGuard
	// Dispatcher può essere nil: vedi [Dispatcher].
	Dispatcher Dispatcher
	// Now sostituisce l'orologio nei test.
	Now func() time.Time
}

// Service è la logica di R8: CRUD dei job, registro delle esecuzioni, trigger
// manuale.
type Service struct {
	store      Store
	log        *slog.Logger
	guard      TargetGuard
	dispatcher Dispatcher
	now        func() time.Time
	budget     *triggerBudget
}

// NewService costruisce il servizio.
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("jobs: lo Store è obbligatorio")
	}
	if opts.Logger == nil {
		return nil, errors.New("jobs: il Logger è obbligatorio")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		store:      opts.Store,
		log:        opts.Logger,
		guard:      opts.Guard,
		dispatcher: opts.Dispatcher,
		now:        now,
		budget:     newTriggerBudget(now),
	}, nil
}

// ------------------------------------------------------------------- lettura

// Get restituisce un job dell'utente.
func (s *Service) Get(ctx context.Context, userID, jobID string) (Job, error) {
	return s.store.JobByID(ctx, userID, jobID)
}

// ListOptions sono i parametri dell'elenco dei job.
type ListOptions struct {
	Enabled         *bool
	Environment     Environment
	IncludeArchived bool
	Limit           int
	Cursor          string
}

// List restituisce una pagina dell'elenco dei job.
func (s *Service) List(ctx context.Context, userID string, opts ListOptions) (Page[Job], error) {
	if opts.Environment != "" && !slices.Contains(Environments, opts.Environment) {
		return Page[Job]{}, &ValidationError{Fields: []FieldError{{
			Field: "environment", Code: "unknown_value",
			Message: fmt.Sprintf("ambiente %q sconosciuto: ammessi %s.", opts.Environment, joinEnvironments(Environments)),
		}}}
	}

	limit := ClampLimit(opts.Limit)
	filter := JobFilter{
		UserID:          userID,
		Enabled:         opts.Enabled,
		Environment:     opts.Environment,
		IncludeArchived: opts.IncludeArchived,
		Limit:           limit,
	}
	if opts.Cursor != "" {
		cursor, err := ParseJobCursor(opts.Cursor)
		if err != nil {
			return Page[Job]{}, err
		}
		filter.Cursor = cursor
	}

	rows, err := s.store.ListJobs(ctx, filter)
	if err != nil {
		return Page[Job]{}, err
	}

	page := Page[Job]{Items: rows, Limit: limit}
	// La riga in più chiesta allo Store è il modo per sapere che esiste una
	// pagina successiva senza un `count(*)` su una tabella che cresce.
	if len(rows) > limit {
		last := rows[limit-1]
		page.Items = rows[:limit]
		page.NextCursor = JobCursor{CreatedAt: last.CreatedAt, ID: last.ID}.Encode()
	}
	return page, nil
}

// ExecutionOptions sono i parametri del registro delle esecuzioni.
type ExecutionOptions struct {
	Status      []ExecutionStatus
	Environment Environment
	TriggeredBy []ExecutionTrigger
	Since       time.Time
	Until       time.Time
	Limit       int
	Cursor      string
}

// Executions restituisce una pagina del registro di un job.
//
// Il job viene letto prima: senza, l'identificativo di un job altrui darebbe
// accesso al suo registro, che contiene URL, stati e estratti di risposta.
func (s *Service) Executions(ctx context.Context, userID, jobID string, opts ExecutionOptions) (Page[Execution], error) {
	job, err := s.store.JobByID(ctx, userID, jobID)
	if err != nil {
		return Page[Execution]{}, err
	}

	invalid := &ValidationError{}
	for _, status := range opts.Status {
		if !slices.Contains(ExecutionStatuses, status) {
			invalid.add("status", "unknown_value", "stato %q sconosciuto.", status)
		}
	}
	for _, trigger := range opts.TriggeredBy {
		if !slices.Contains(ExecutionTriggers, trigger) {
			invalid.add("trigger", "unknown_value", "origine %q sconosciuta.", trigger)
		}
	}
	if opts.Environment != "" && !slices.Contains(Environments, opts.Environment) {
		invalid.add("environment", "unknown_value", "ambiente %q sconosciuto.", opts.Environment)
	}
	if !opts.Since.IsZero() && !opts.Until.IsZero() && !opts.Until.After(opts.Since) {
		invalid.add("until", "out_of_range", "`until` dev'essere successivo a `since`.")
	}
	if err := invalid.orNil(); err != nil {
		return Page[Execution]{}, err
	}

	limit := ClampLimit(opts.Limit)
	filter := ExecutionFilter{
		JobID:       job.ID,
		Status:      opts.Status,
		Environment: opts.Environment,
		TriggeredBy: opts.TriggeredBy,
		Since:       opts.Since,
		Until:       opts.Until,
		Limit:       limit,
	}
	if opts.Cursor != "" {
		cursor, err := ParseExecutionCursor(opts.Cursor)
		if err != nil {
			return Page[Execution]{}, err
		}
		filter.Cursor = cursor
	}

	rows, err := s.store.ListExecutions(ctx, filter)
	if err != nil {
		return Page[Execution]{}, err
	}

	page := Page[Execution]{Items: rows, Limit: limit}
	if len(rows) > limit {
		last := rows[limit-1]
		page.Items = rows[:limit]
		page.NextCursor = ExecutionCursor{
			ScheduledFor: last.ScheduledFor,
			Environment:  last.Environment,
			Attempt:      last.Attempt,
		}.Encode()
	}
	return page, nil
}

// ----------------------------------------------------------------- scrittura

// Create crea un job.
//
// L'ordine dei controlli è deliberato: prima il piano viene letto, poi la forma
// del job viene validata (compresa la risoluzione, che dipende dal piano), poi
// si verifica il tetto al numero di job. Dire «hai finito i job» a chi ha
// scritto un'espressione cron sbagliata sarebbe una diagnosi giusta e inutile:
// l'utente correggerebbe il piano invece del campo.
func (s *Service) Create(ctx context.Context, userID string, job Job) (Job, error) {
	plan, err := s.store.PlanForUser(ctx, userID)
	if err != nil {
		return Job{}, err
	}

	job.ID = ""
	job.UserID = userID
	// L'API non crea job gestiti da un repository: quelli nascono dalla
	// riconciliazione di un `cron.yaml` (R13, issue #421).
	job.RepositoryID = ""
	job.ArchivedAt = nil

	if err := job.Validate(ctx, plan, s.guard); err != nil {
		return Job{}, err
	}

	count, err := s.store.CountJobs(ctx, userID)
	if err != nil {
		return Job{}, err
	}
	if err := plan.CheckJobCount(count); err != nil {
		return Job{}, err
	}

	job.NextRunAt = job.NextRun(s.now())

	created, err := s.store.CreateJob(ctx, job, plan.MaxJobs)
	if errors.Is(err, ErrJobLimitReached) {
		// Il conteggio di poco fa diceva che c'era posto: fra i due è arrivata
		// un'altra creazione. Il messaggio è lo stesso, perché per l'utente la
		// situazione è la stessa.
		if limit := plan.CheckJobCount(valueOr(plan.MaxJobs, count+1)); limit != nil {
			return Job{}, limit
		}
	}
	if err != nil {
		return Job{}, err
	}
	return created, nil
}

// Patch è una modifica parziale di un job.
//
// Un puntatore nil significa «campo non toccato»; un puntatore valorizzato
// significa «sostituisci con questo». La distinzione esiste perché un PATCH che
// non la facesse azzererebbe in silenzio ogni campo che il client non ha
// rimandato.
//
// Schedule ed Every si escludono a vicenda anche qui, e in un modo che merita
// di essere detto: **valorizzarne uno azzera l'altro**. Un job cron a cui si
// manda `every` diventa un job a intervallo; senza questa regola l'unico modo di
// cambiare modalità sarebbe mandare i due campi insieme, cioè mandare
// esattamente la coppia che il vincolo XOR rifiuta.
type Patch struct {
	Name           *string
	Description    *string
	Schedule       *string
	Every          *time.Duration
	Timezone       *string
	Environments   *[]Environment
	URL            *string
	Method         *Method
	Headers        *map[string]string
	Body           *string
	Timeout        *time.Duration
	MaxRetries     *int
	RetryBackoff   *Backoff
	AlertOnFailure *[]AlertChannel
	Enabled        *bool
}

// Empty indica una patch che non chiede nessuna modifica.
func (p Patch) Empty() bool {
	return p.Name == nil && p.Description == nil && p.Schedule == nil && p.Every == nil &&
		p.Timezone == nil && p.Environments == nil && p.URL == nil && p.Method == nil &&
		p.Headers == nil && p.Body == nil && p.Timeout == nil && p.MaxRetries == nil &&
		p.RetryBackoff == nil && p.AlertOnFailure == nil && p.Enabled == nil
}

// onlyEnabled indica una patch che tocca solo la pausa.
func (p Patch) onlyEnabled() bool {
	withoutEnabled := p
	withoutEnabled.Enabled = nil
	return withoutEnabled.Empty()
}

// ApplyTo sovrappone la patch a un job.
func (p Patch) ApplyTo(job *Job) {
	assign(&job.Name, p.Name)
	assign(&job.Description, p.Description)
	assign(&job.Timezone, p.Timezone)
	assign(&job.URL, p.URL)
	assign(&job.Method, p.Method)
	assign(&job.Body, p.Body)
	assign(&job.Timeout, p.Timeout)
	assign(&job.MaxRetries, p.MaxRetries)
	assign(&job.RetryBackoff, p.RetryBackoff)
	assign(&job.Enabled, p.Enabled)
	assign(&job.Environments, p.Environments)
	assign(&job.AlertOnFailure, p.AlertOnFailure)
	assign(&job.Headers, p.Headers)

	if p.Schedule != nil {
		job.Schedule = *p.Schedule
		if *p.Schedule != "" && p.Every == nil {
			job.Every = 0
		}
	}
	if p.Every != nil {
		job.Every = *p.Every
		if *p.Every != 0 && p.Schedule == nil {
			job.Schedule = ""
		}
	}
}

// Update applica una modifica parziale.
func (s *Service) Update(ctx context.Context, userID, jobID string, patch Patch) (Job, error) {
	current, err := s.store.JobByID(ctx, userID, jobID)
	if err != nil {
		return Job{}, err
	}
	if current.Archived() {
		return Job{}, ErrArchived
	}
	// Un job gestito da un `cron.yaml` si modifica nel file: qualunque altra
	// modifica sarebbe sovrascritta dalla riconciliazione al push successivo, e
	// l'utente non avrebbe modo di capire perché. La pausa fa eccezione perché
	// la 0005 la tiene distinta da `archived_at` proprio per sopravvivere al
	// sync.
	if current.Managed() && !patch.onlyEnabled() {
		return Job{}, ErrManaged
	}

	plan, err := s.store.PlanForUser(ctx, userID)
	if err != nil {
		return Job{}, err
	}

	updated := current
	updated.Headers = cloneHeaders(current.Headers)
	patch.ApplyTo(&updated)

	if err := updated.Validate(ctx, plan, s.guard); err != nil {
		return Job{}, err
	}
	updated.NextRunAt = updated.NextRun(s.now())

	return s.store.UpdateJob(ctx, updated)
}

// Delete elimina un job.
//
// Le esecuzioni seguono per cascata (0006): è una perdita di storico, ed è ciò
// che «elimina questo job» significa. Un job ancora presente in un `cron.yaml`
// non si elimina da qui — tornerebbe al push successivo — mentre uno già
// archiviato sì: quello dal file è già sparito.
func (s *Service) Delete(ctx context.Context, userID, jobID string) error {
	current, err := s.store.JobByID(ctx, userID, jobID)
	if err != nil {
		return err
	}
	if current.Managed() && !current.Archived() {
		return ErrManaged
	}
	return s.store.DeleteJob(ctx, userID, jobID)
}

// ------------------------------------------------------------ trigger manuale

// Trigger registra un'esecuzione manuale (R8).
//
// # Perché non è una scorciatoia per aggirare i limiti
//
// Un «esegui adesso» senza tetto è il modo più semplice di trasformare un piano
// Free in esecuzioni illimitate: basta un ciclo. Il tetto qui è doppio, e
// nessuno dei due numeri è inventato.
//
//  1. **Per job, lo impone la chiave primaria di `job_executions`.**
//     L'occorrenza manuale viene ancorata alla griglia della risoluzione del
//     piano — al minuto su Free, ai dieci secondi su Pro, al secondo su Team —
//     esattamente come fa la modalità a intervallo di [schedule], che è ancorata
//     all'epoch. Due trigger nella stessa casella hanno lo stesso
//     `scheduled_for` e collidono sulla chiave: il rifiuto è del database, è
//     atomico e sopravvive a un riavvio dell'API, che è più di quanto un
//     contatore in memoria possa promettere. Il costo del trigger diventa quello
//     di un'occorrenza schedulata, che è precisamente ciò per cui l'utente paga.
//
//  2. **Per utente, lo impone il budget aggregato del piano** — vedi
//     [Plan.ManualBudget], che lo deriva dalla stessa matrice di SPEC §8 invece
//     di aggiungerci una riga.
//
// L'esecuzione resta **tracciata**: la riga porta `triggered_by = 'manual'` e si
// rilegge da `GET /jobs/{id}/executions?trigger=manual`. Limitare senza
// registrare renderebbe impossibile accorgersi di un abuso che sta sotto la
// soglia.
func (s *Service) Trigger(ctx context.Context, userID, jobID string, env Environment) (Execution, error) {
	job, err := s.store.JobByID(ctx, userID, jobID)
	if err != nil {
		return Execution{}, err
	}
	if job.Archived() {
		return Execution{}, ErrArchived
	}
	if !job.Enabled {
		// Il motore marcherebbe `skipped` l'occorrenza di un job in pausa al
		// momento del dispatch (0001): accettarla qui significherebbe
		// rispondere 202 a una richiesta che non verrà eseguita.
		return Execution{}, ErrDisabled
	}

	target, err := resolveEnvironment(job, env)
	if err != nil {
		return Execution{}, err
	}

	plan, err := s.store.PlanForUser(ctx, userID)
	if err != nil {
		return Execution{}, err
	}
	if err := s.budget.check(plan, userID); err != nil {
		return Execution{}, err
	}

	now := s.now()
	slot := alignToSlot(now, plan.MinInterval)

	exec := Execution{
		JobID:        job.ID,
		ScheduledFor: slot,
		Environment:  target,
		Attempt:      1,
		Status:       StatusPending,
		TriggeredBy:  TriggerManual,
	}

	created, err := s.store.CreateExecution(ctx, exec)
	switch {
	case err == nil:
	case errors.Is(err, ErrExecutionExists):
		return Execution{}, s.explainConflict(ctx, job, plan, slot, target, now)
	default:
		return Execution{}, err
	}

	if s.dispatcher != nil {
		s.dispatcher.Notify(ctx, created)
	}
	s.log.InfoContext(ctx, "esecuzione manuale registrata",
		slog.String("job_id", job.ID),
		slog.String("user_id", userID),
		slog.String("environment", string(target)),
		slog.Time("scheduled_for", slot))
	return created, nil
}

// explainConflict distingue le due ragioni per cui la casella era occupata.
//
// Se dentro c'era un altro trigger manuale, l'utente sta andando più veloce di
// quanto il piano consenta e la risposta giusta è «riprova fra tanto». Se invece
// c'era un'occorrenza schedulata, il job in quell'istante è già stato eseguito:
// non è un limite, è un conflitto, e riprovare più tardi lo risolve da sé.
// Dirlo con lo stesso codice porterebbe il client a mostrare un invito
// all'upgrade a chi non ha superato niente.
func (s *Service) explainConflict(ctx context.Context, job Job, plan Plan, slot time.Time, env Environment, now time.Time) error {
	interval := plan.MinInterval
	if interval < time.Second {
		interval = time.Second
	}
	retryAfter := slot.Add(interval).Sub(now)
	if retryAfter < time.Second {
		retryAfter = time.Second
	}

	existing, err := s.store.ExecutionAt(ctx, job.ID, slot, env, 1)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil && existing.TriggeredBy != TriggerManual {
		return fmt.Errorf("%w: il job ha già un'esecuzione schedulata alle %s in %s",
			ErrExecutionExists, slot.Format(time.RFC3339), env)
	}

	return &PlanLimitError{
		Limit:      LimitManualTrigger,
		Plan:       plan.Code,
		RetryAfter: retryAfter,
		message: fmt.Sprintf(
			"il piano %s consente un'esecuzione manuale ogni %s per job: riprova fra %s.",
			plan.label(), FormatDuration(interval), FormatDuration(retryAfter.Round(time.Second))),
	}
}

// resolveEnvironment sceglie l'ambiente del trigger.
//
// Vuoto è ammesso solo se il job ne ha uno solo: con due ambienti la scelta è
// dell'utente, e indovinarla significherebbe eseguire in produzione una prova
// destinata a staging.
func resolveEnvironment(job Job, env Environment) (Environment, error) {
	if env == "" {
		if len(job.Environments) == 1 {
			return job.Environments[0], nil
		}
		return "", &ValidationError{Fields: []FieldError{{
			Field: "environment", Code: "required",
			Message: fmt.Sprintf("il job vive in più ambienti (%s): indica quale eseguire.",
				joinEnvironments(job.Environments)),
		}}}
	}
	if !job.HasEnvironment(env) {
		return "", &ValidationError{Fields: []FieldError{{
			Field: "environment", Code: "unknown_value",
			Message: fmt.Sprintf("il job non vive nell'ambiente %q: ambienti del job %s.",
				env, joinEnvironments(job.Environments)),
		}}}
	}
	return env, nil
}

// alignToSlot porta un istante sulla griglia della risoluzione del piano.
//
// L'ancoraggio è l'epoch Unix, lo stesso di [schedule] per la modalità a
// intervallo: due processi, due fusi e due riavvii producono la stessa griglia.
func alignToSlot(t time.Time, interval time.Duration) time.Time {
	seconds := int64(interval / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	unix := t.Unix()
	k := unix / seconds
	// Divisione con arrotondamento verso il basso: prima dell'epoch i secondi
	// Unix sono negativi e il troncamento di Go darebbe la casella successiva.
	if unix < 0 && k*seconds != unix {
		k--
	}
	return time.Unix(k*seconds, 0).UTC()
}

// ------------------------------------------------------- budget dei trigger

// triggerBudget applica il tetto aggregato ai trigger manuali di un utente.
//
// Un limitatore per codice di piano, perché la regola cambia con il piano e
// [ratelimit.Limiter] ne applica una sola. La chiave dentro ciascuno è
// l'utente.
//
// È in memoria, con lo stesso ragionamento del limitatore dell'autenticazione:
// l'API è un processo solo su una VPS (SPEC §2), e un contatore condiviso
// costerebbe una scrittura per tentativo. Il tetto **per job**, che è quello che
// conta davvero, è invece nel database e sopravvive ai riavvii.
//
// È anche il punto in cui la issue #398 innesta le quote generali di R10: la
// forma — una regola derivata dal piano, applicata per chiave utente — è già
// quella giusta, e resta da estendere alle altre operazioni.
type triggerBudget struct {
	now func() time.Time

	mu       sync.Mutex
	limiters map[string]*ratelimit.Limiter
}

func newTriggerBudget(now func() time.Time) *triggerBudget {
	return &triggerBudget{now: now, limiters: map[string]*ratelimit.Limiter{}}
}

func (b *triggerBudget) check(plan Plan, userID string) error {
	burst, window, ok := plan.ManualBudget()
	if !ok {
		// Il piano non dichiara né tetto né soglia di fair use: è venduto come
		// illimitato e non c'è un numero da cui derivare. Inventarlo sarebbe una
		// decisione commerciale presa dal codice.
		return nil
	}

	limiter := b.limiterFor(plan.Code, burst, window)
	if limiter == nil {
		return nil
	}
	if allowed, retryAfter := limiter.Allow(userID); !allowed {
		return &PlanLimitError{
			Limit:      LimitManualTrigger,
			Plan:       plan.Code,
			RetryAfter: retryAfter,
			message: fmt.Sprintf(
				"il piano %s consente %d esecuzioni manuali ogni %s in totale: riprova fra %s.",
				plan.label(), burst, FormatDuration(window), FormatDuration(retryAfter)),
		}
	}
	return nil
}

func (b *triggerBudget) limiterFor(planCode string, burst int, window time.Duration) *ratelimit.Limiter {
	b.mu.Lock()
	defer b.mu.Unlock()

	if limiter, ok := b.limiters[planCode]; ok {
		return limiter
	}
	limiter, err := ratelimit.New(
		ratelimit.Rule{Burst: burst, Window: window},
		ratelimit.WithClock(b.now),
	)
	if err != nil {
		// Una riga di `plans` con `max_jobs = 0` non è costruibile (il CHECK lo
		// vieta), ma la matrice è dati e non codice: se un giorno lo diventasse,
		// il comportamento giusto è non limitare, non rifiutare tutto.
		return nil
	}
	b.limiters[planCode] = limiter
	return limiter
}

// ------------------------------------------------------------------ supporto

func assign[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func valueOr(p *int, fallback int) int {
	if p != nil {
		return *p
	}
	return fallback
}

func cloneHeaders(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
