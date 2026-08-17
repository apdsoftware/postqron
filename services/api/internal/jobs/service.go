package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/netguard"
	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// Dispatcher consegna a chi esegue l'occorrenza nata da un trigger manuale.
//
// L'API non fa richieste HTTP verso il target, mai. Un trigger manuale è una
// **riga in `job_executions`** con `triggered_by = 'manual'` e stato `pending`,
// che è la stessa forma con cui il motore prende in carico un'occorrenza
// schedulata (R4, migrazione 0006).
//
// # Perché non è opzionale come sembra
//
// La forma della riga è la stessa, ma il padrone no. Lo scheduler (#388)
// filtra `triggered_by = 'schedule'` sia nel recupero delle occorrenze in
// sospeso sia nella loro scadenza, e lo dichiara: «lo scheduler riprende ciò che
// lo scheduler ha creato. Un trigger manuale (R8) e un retry (R5) nascono
// altrove, hanno un padrone che sa quando riproporli». È una scelta giusta —
// riproporre un «esegui adesso» dopo un riavvio, mezz'ora dopo, sarebbe la cosa
// sbagliata — e la conseguenza è che **il padrone di questa riga è chi la
// crea**, cioè qui.
//
// Quindi: con Dispatcher nil la riga viene registrata, contata e resta
// consultabile, ma nessuno la esegue. Non è una degradazione temporanea
// accettabile in silenzio — [Service.Trigger] lo scrive nel log a ogni
// occorrenza — ed è ciò che il worker pool (#389) deve collegare qui quando
// arriverà. L'adattatore va scritto in cmd/api, dove i due mondi si incontrano
// senza conoscersi: questo package non importa internal/scheduler, perché l'API
// non deve dipendere dal motore.
type Dispatcher interface {
	// Dispatch consegna l'occorrenza. La riga su `job_executions` esiste già:
	// non va inserita di nuovo, ed è la stessa clausola del contratto di
	// scheduler.Dispatcher.
	//
	// Non deve bloccare: se non può accodare restituisce un errore, e
	// l'occorrenza resta `pending`.
	Dispatch(ctx context.Context, exec Execution) error
}

// Options sono le dipendenze del [Service].
type Options struct {
	// Store è obbligatorio.
	Store Store
	// Logger è obbligatorio.
	Logger *slog.Logger
	// Guard è il controllo sul target (R38).
	//
	// **Nil significa il blocco predefinito, non "nessun blocco"**: il servizio
	// costruisce un [netguard.Guard] con la policy predefinita. È il contrario
	// di ciò che questo campo faceva finché #455 non esisteva, ed è un
	// cambiamento voluto: un servizio che esegue richieste HTTP verso URL scelti
	// dall'utente, dalla stessa macchina del database, non deve poter nascere
	// senza difesa perché qualcuno ha dimenticato una riga di composizione. Chi
	// vuole un guard diverso — con [netguard.Policy.Deny] valorizzato con
	// l'indirizzo pubblico della VPS, per esempio — lo passa qui.
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
	guard := opts.Guard
	if guard == nil {
		// Vedi [Options.Guard]: il predefinito è il blocco, non la sua assenza.
		guard = netguard.New(netguard.Options{Logger: opts.Logger})
	}
	return &Service{
		store:      opts.Store,
		log:        opts.Logger,
		guard:      guard,
		dispatcher: opts.Dispatcher,
		now:        now,
		budget:     newTriggerBudget(now),
	}, nil
}

// ------------------------------------------------------------------- portata

// budgetBasisPlan è il piano da cui i piani «moltiplicati» prendono la propria
// portata.
//
// Non è un numero scelto: R25-bis dice testualmente che il budget di Agency è
// quello di Team applicato per workspace, perché «Agency non è Team con più
// potenza, è Team moltiplicato» — un workspace Agency serve un cliente finale
// con gli stessi job di un cliente Team, quindi non ha ragione di eseguire di
// più. Qui c'è il **nome** del piano di riferimento, che è ciò che la spec
// dichiara; i numeri restano nella tabella `plans`.
const budgetBasisPlan = "team"

// PlanBudget è la portata del piano di un utente, già risolta.
//
// «Risolta» significa che R25-bis è stato applicato: un piano che non dichiara
// una portata propria ma include workspace isolati non è illimitato, prende
// quella del piano che moltiplica.
type PlanBudget struct {
	// Plan è il piano dell'utente.
	Plan Plan

	// Rule è la portata concessa. Ha senso solo se Limited è vero.
	Rule ratelimit.Rule

	// Limited è falso quando dal listino non si ricava nessuna portata. In quel
	// caso non si limita: inventare un numero sarebbe una decisione commerciale
	// presa dal codice.
	Limited bool

	// PerWorkspace dice che Rule si applica **per workspace** e non per account
	// (R25-bis). Oggi i due coincidono — il workspace di SPEC §9 è l'account,
	// vedi la migrazione 0012 — quindi la chiave resta l'utente; quando R25
	// introdurrà i workspace come entità, è questo campo a dire che la chiave
	// deve diventare il workspace. La capacità totale dell'account scala allora
	// col numero di workspace, cioè con ciò per cui l'agenzia paga.
	PerWorkspace bool

	// BasisPlan è il piano da cui la portata è stata presa, quando non è quella
	// del piano dell'utente. Serve al messaggio di rifiuto: dire «il piano Agency
	// consente 1.000 operazioni al secondo» senza dire da dove viene quel numero
	// lo farebbe sembrare una riga di listino che non esiste.
	BasisPlan Plan
}

// Reject costruisce il rifiuto dovuto all'esaurimento della portata.
//
// Il messaggio dice **quale piano concede cosa**, ed è l'unico punto del
// prodotto in cui un limite tecnico è anche un'informazione commerciale: un 429
// muto costringerebbe l'utente a indovinare se ha sbagliato lui o se deve
// pagare. Su un piano che applica la portata per workspace non c'è nessun invito
// all'upgrade, perché non ci sarebbe niente da comprare: la capacità di quel
// piano cresce aggiungendo workspace, non cambiando riga di listino.
func (b PlanBudget) Reject(limit LimitKind, operations string, retryAfter time.Duration) *PlanLimitError {
	if retryAfter < time.Second {
		retryAfter = time.Second
	}

	var message string
	switch {
	case b.PerWorkspace:
		message = fmt.Sprintf(
			"il piano %s applica per workspace la portata del piano %s, cioè %d %s ogni %s: riprova fra %s.",
			b.Plan.label(), b.BasisPlan.label(), b.Rule.Burst, operations,
			FormatDuration(b.Rule.Window), FormatDuration(retryAfter))
	default:
		message = fmt.Sprintf(
			"il piano %s consente %d %s ogni %s: riprova fra %s, oppure passa a un piano superiore.",
			b.Plan.label(), b.Rule.Burst, operations,
			FormatDuration(b.Rule.Window), FormatDuration(retryAfter))
	}

	return &PlanLimitError{
		Limit:      limit,
		Plan:       b.Plan.Code,
		RetryAfter: retryAfter,
		message:    message,
	}
}

// Budget restituisce la portata del piano dell'utente (R10, R15, R25-bis).
//
// È il punto da cui le quote di piano si applicano **fuori** da questo package:
// le rotte dell'API pubblica ne hanno bisogno per rifiutare una scrittura prima
// di eseguirla, e non devono per questo conoscere la matrice di SPEC §8.
//
// Costa una lettura del piano, e una seconda solo per i piani che moltiplicano
// un altro piano. Chi la chiama a ogni richiesta deve tenersi il risultato per
// un po': un limitatore che interroga il database per decidere se rifiutare ha
// già speso ciò che doveva proteggere.
func (s *Service) Budget(ctx context.Context, userID string) (PlanBudget, error) {
	plan, err := s.store.PlanForUser(ctx, userID)
	if err != nil {
		return PlanBudget{}, err
	}
	return s.budgetFor(ctx, plan)
}

func (s *Service) budgetFor(ctx context.Context, plan Plan) (PlanBudget, error) {
	if rule, ok := plan.Throughput(); ok {
		return PlanBudget{Plan: plan, Rule: rule, Limited: true}, nil
	}
	if !plan.MultiWorkspace {
		// Un piano senza portata propria e senza workspace da moltiplicare non
		// dà appigli: non c'è niente da cui derivare, e non si inventa.
		return PlanBudget{Plan: plan}, nil
	}

	basis, err := s.store.PlanByCode(ctx, budgetBasisPlan)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return PlanBudget{}, err
		}
		// Il listino non ha più il piano di riferimento. Non limitare è la
		// scelta giusta fra le due: rifiutare tutto il traffico di un cliente
		// Agency perché una riga di `plans` è stata rinominata sarebbe un guasto
		// molto più grave del limite mancato. Va detto, però, e non una volta
		// sola: senza questo limite R25-bis non è applicato.
		s.log.ErrorContext(ctx,
			"portata non derivabile: il piano di riferimento di R25-bis non esiste nel listino",
			slog.String("plan", plan.Code), slog.String("basis_plan", budgetBasisPlan))
		return PlanBudget{Plan: plan}, nil
	}

	rule, ok := basis.Throughput()
	if !ok {
		s.log.ErrorContext(ctx,
			"portata non derivabile: il piano di riferimento di R25-bis non dichiara né tetto né soglia",
			slog.String("plan", plan.Code), slog.String("basis_plan", basis.Code))
		return PlanBudget{Plan: plan}, nil
	}
	return PlanBudget{
		Plan:         plan,
		Rule:         rule,
		Limited:      true,
		PerWorkspace: true,
		BasisPlan:    basis,
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

	// La retention del piano si verifica **prima** della query, non filtrando le
	// righe che tornano: una lettura che scandisce novanta giorni di partizioni
	// per poi scartarne ottantasette ha già speso ciò che il limite doveva
	// risparmiare (R10-bis, SPEC §8).
	plan, err := s.store.PlanForUser(ctx, userID)
	if err != nil {
		return Page[Execution]{}, err
	}
	now := s.now()
	if err := plan.CheckRetention(opts.Since, opts.Until, now); err != nil {
		return Page[Execution]{}, err
	}
	since := opts.Since
	if since.IsZero() {
		// Nessuna finestra richiesta: quella predefinita è la retention del
		// piano. Non è un rifiuto mascherato — non c'è niente che l'utente abbia
		// chiesto e non stia ottenendo — ed è ciò che rende la risposta coerente
		// con quella che darà la cancellazione periodica (#393) quando avrà
		// girato.
		since = plan.RetentionFloor(now)
	}

	limit := ClampLimit(opts.Limit)
	filter := ExecutionFilter{
		JobID:       job.ID,
		Status:      opts.Status,
		Environment: opts.Environment,
		TriggeredBy: opts.TriggeredBy,
		Since:       since,
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
	// `next_run_at` non si calcola qui. La colonna è dello scheduler —
	// «calcolato dallo scheduler» dice la 0005, e la migrazione 0010 lo scrive
	// per esteso nominando proprio questo caso: «un job appena creato da API ...
	// nasce con la colonna a NULL». Da lì cade in `jobs_unscheduled_idx`, dove
	// il motore lo raccoglie alla passata successiva. Calcolarla anche qui
	// sarebbe una seconda copia della stessa verità, libera di divergere dalla
	// prima il giorno in cui lo scheduler applica una finestra di recupero o una
	// tolleranza.
	job.NextRunAt = nil

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

	// La riaccensione è l'altra metà di R58, e senza questo controllo la regola
	// resterebbe a metà: il downgrade sospende, e poi l'utente riaccenderebbe
	// tutto ciò che aveva. Il tetto da applicare è quello dei job **accesi** —
	// non quello del catalogo, che dopo un downgrade è per costruzione già oltre
	// il limite: vedi [Plan.CheckActiveJobCount].
	//
	// Il vincolo di risoluzione non compare qui perché [Job.Validate] lo ha già
	// applicato poche righe sopra: un `every: 1s` su un piano fermo al minuto è
	// rifiutato con il messaggio che dice cosa fare — allargare l'intervallo o
	// cambiare piano — che è ciò che R58 pretende dall'interfaccia. Le due metà
	// stanno in due posti diversi perché sono due domande diverse: «c'è posto» e
	// «questo job è ammissibile».
	if !current.Enabled && updated.Enabled && !current.Archived() {
		active, err := s.store.CountActiveJobs(ctx, userID)
		if err != nil {
			return Job{}, err
		}
		if err := plan.CheckActiveJobCount(active); err != nil {
			return Job{}, err
		}
	}

	return s.store.UpdateJob(ctx, updated, mustResetNextRun(current, updated))
}

// mustResetNextRun decide se la modifica invalida la prossima occorrenza.
//
// L'API non calcola mai `jobs.next_run_at` — è dello scheduler (0005, e la 0010
// lo scrive per esteso) — ma deve poterla **azzerare**, che è il modo di dire
// «ricalcolala». Sono due i casi, e sono diversi fra loro:
//
//   - il job non è più eseguibile: è la stessa condizione di `jobs_due_idx`, e
//     lasciarci un valore vorrebbe dire tenere una riga di indice che promette
//     un'esecuzione che non avverrà;
//   - la schedulazione è cambiata: senza azzerarla il job continuerebbe a
//     partire all'orario di quella vecchia fino alla prima occorrenza dopo il
//     cambio. NULL lo fa ricadere in `jobs_unscheduled_idx`, da cui il motore lo
//     riprende alla passata successiva.
//
// Fuori da questi due casi la colonna **non si tocca**: riscriverla con il
// valore letto poco prima sarebbe un aggiornamento perso ai danni dello
// scheduler, che nel frattempo può averla fatta avanzare.
func mustResetNextRun(current, updated Job) bool {
	if !updated.Runnable() {
		return true
	}
	return current.Schedule != updated.Schedule ||
		current.Every != updated.Every ||
		current.Timezone != updated.Timezone
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
//     [Plan.Throughput], che lo deriva dalla stessa matrice di SPEC §8 invece
//     di aggiungerci una riga, e [Service.budgetFor], che per i piani venduti
//     come illimitati applica R25-bis invece di lasciarli senza tetto.
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
	budget, err := s.budgetFor(ctx, plan)
	if err != nil {
		return Execution{}, err
	}
	if err := s.budget.check(budget, userID); err != nil {
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

	attributes := []any{
		slog.String("job_id", job.ID),
		slog.String("user_id", userID),
		slog.String("environment", string(target)),
		slog.Time("scheduled_for", slot),
	}

	if s.dispatcher == nil {
		// La riga c'è, ma non la eseguirà nessuno: lo scheduler non raccoglie i
		// trigger manuali, per scelta dichiarata (vedi [Dispatcher]). Va detto a
		// ogni occorrenza e non una volta all'avvio, perché è l'unico posto in
		// cui il fatto è collegabile alla richiesta che l'ha prodotto.
		s.log.WarnContext(ctx,
			"esecuzione manuale registrata ma non consegnata: nessun dispatcher collegato (issue #389)",
			attributes...)
		return created, nil
	}

	if err := s.dispatcher.Dispatch(ctx, created); err != nil {
		// La riga resta `pending`, e chi ha rifiutato la consegna — coda piena,
		// arresto in corso — sa che è lì. La richiesta non fallisce: ciò che
		// l'utente ha chiesto è stato registrato, ed è la registrazione che il
		// 202 promette.
		s.log.WarnContext(ctx, "esecuzione manuale registrata ma non accodata",
			append(attributes, slog.Any("error", err))...)
		return created, nil
	}

	s.log.InfoContext(ctx, "esecuzione manuale registrata", attributes...)
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
// Una regola per codice di piano, perché la regola cambia con il piano; la
// chiave dentro ciascuna è l'utente. È esattamente la forma che
// [ratelimit.Budget] mette a disposizione — questo tipo la scriveva a mano prima
// che #398 la generalizzasse, e adesso resta solo la parte che riguarda i job:
// come si deriva la regola e cosa si risponde quando scatta.
//
// È in memoria, con lo stesso ragionamento del limitatore dell'autenticazione:
// l'API è un processo solo su una VPS (SPEC §2), e un contatore condiviso
// costerebbe una scrittura per tentativo. Il tetto **per job**, che è quello che
// conta davvero, è invece nel database e sopravvive ai riavvii.
type triggerBudget struct {
	limiter *ratelimit.Budget
}

func newTriggerBudget(now func() time.Time) *triggerBudget {
	return &triggerBudget{limiter: ratelimit.NewBudget(ratelimit.WithClock(now))}
}

func (b *triggerBudget) check(budget PlanBudget, userID string) error {
	if !budget.Limited {
		// Dal listino non si ricava nessuna portata: vedi [Service.budgetFor],
		// che ha già provato a derivarla e lo ha registrato nel log.
		return nil
	}

	allowed, retryAfter := b.limiter.Allow(
		"manual_trigger:"+budget.Plan.Code,
		ratelimit.Fingerprint(userID),
		budget.Rule,
	)
	if allowed {
		return nil
	}
	return budget.Reject(LimitManualTrigger, "esecuzioni manuali", retryAfter)
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
