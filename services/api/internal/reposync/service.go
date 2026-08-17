package reposync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/apdsoftware/postqron/services/api/internal/cronyaml"
	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Options configura il [Service]. Store, Plans, Secrets e Contents sono
// obbligatori.
type Options struct {
	// Store è la persistenza della riconciliazione.
	Store Store

	// Plans legge il piano dell'utente: i limiti di SPEC §8 valgono anche qui,
	// e un file che li sfonda va rifiutato per intero.
	Plans Plans

	// Secrets elenca i nomi dei segreti del workspace, contro cui i `${VAR}`
	// del file vengono verificati senza decifrare niente.
	Secrets SecretNames

	// Contents legge `cron.yaml` dal repository.
	Contents Contents

	// Guard è il controllo sui target ammessi (R38). nil salta il controllo:
	// resta quello vero, che è l'apertura della connessione al momento
	// dell'esecuzione.
	Guard jobs.TargetGuard

	Logger *slog.Logger
}

// Service riconcilia i repository a ogni push (R13).
//
// Implementa [githubhook.PushSink]: è il consumatore che #421 aveva dichiarato
// e lasciato vuoto.
type Service struct {
	store    Store
	plans    Plans
	secrets  SecretNames
	contents Contents
	guard    jobs.TargetGuard
	log      *slog.Logger
}

var _ githubhook.PushSink = (*Service)(nil)

// NewService costruisce il servizio.
func NewService(opts Options) (*Service, error) {
	switch {
	case opts.Store == nil:
		return nil, errors.New("reposync: lo store è obbligatorio")
	case opts.Plans == nil:
		return nil, errors.New("reposync: la lettura dei piani è obbligatoria")
	case opts.Secrets == nil:
		// Non è opzionale, e passare nil non equivale a «nessun segreto»:
		// significherebbe rifiutare ogni `${VAR}` di ogni file, cioè un sync
		// che smette di funzionare per chiunque usi un token.
		return nil, errors.New("reposync: l'elenco dei segreti è obbligatorio")
	case opts.Contents == nil:
		return nil, errors.New("reposync: la lettura dei file è obbligatoria")
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		store:    opts.Store,
		plans:    opts.Plans,
		secrets:  opts.Secrets,
		contents: opts.Contents,
		guard:    opts.Guard,
		log:      logger,
	}, nil
}

// HandlePush riconcilia tutti i collegamenti che seguono il repository spinto.
//
// # Cosa fa risalire un errore, e cosa no
//
// Un errore restituito da qui marca la consegna come fallita e fa rispondere
// 500, cioè fa **ripetere GitHub** (vedi [githubhook.PushSink]). È la risposta
// giusta solo per ciò che una ripetizione può risolvere: il database non
// raggiungibile, GitHub che risponde 502, un token rifiutato.
//
// Un `cron.yaml` non valido, o assente, **non è di quel tipo**. Ripetere la
// consegna non renderà valido il file, e insistere produrrebbe una consegna in
// errore permanente su un problema che è dell'utente. Quei casi vengono
// registrati su `repositories` — è la colonna che la dashboard mostra — e la
// consegna si chiude con successo.
//
// # Perché un ciclo e non un repository
//
// `repositories_identity_key` è unico **per utente**: due clienti che collegano
// lo stesso repository pubblico hanno due righe, con due piani e due insiemi di
// segreti. Il fallimento di uno non deve fermare gli altri, e la push è una
// sola: gli errori si accumulano e risalgono insieme.
func (s *Service) HandlePush(ctx context.Context, event githubhook.PushEvent) error {
	if event.Repository.ExternalID <= 0 {
		// Senza identità del repository non c'è niente a cui risalire. Il
		// livello di sopra non lascia passare un payload così, ma questo
		// package non può dipendere da quel fatto.
		return nil
	}

	repositories, err := s.store.RepositoriesByExternalID(ctx, event.Repository.ExternalID)
	if err != nil {
		return fmt.Errorf("reposync: ricerca dei collegamenti: %w", err)
	}
	if len(repositories) == 0 {
		// La App è installata sul repository ma nessuno l'ha collegato da noi.
		// Non è un errore: è il caso normale fra l'installazione e il
		// collegamento.
		s.log.InfoContext(ctx, "push GitHub senza collegamenti, ignorata",
			slog.String("delivery", event.Delivery),
			slog.String("repository", event.Repository.FullName))
		return nil
	}

	branch := event.Branch()
	commit := event.After

	var failures []error
	for _, repo := range repositories {
		if reason, skip := skipReason(repo, event, branch, commit); skip {
			s.log.InfoContext(ctx, "push GitHub non sincronizzata",
				slog.String("delivery", event.Delivery),
				slog.String("repository", repo.FullName()),
				slog.String("reason", reason))
			continue
		}
		if err := s.sync(ctx, repo, event, commit); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", repo.FullName(), err))
		}
	}
	return errors.Join(failures...)
}

// skipReason dice se e perché la push non riguarda questo collegamento.
//
// Sono tutti casi in cui non c'è niente da sincronizzare e niente da segnalare
// all'utente: registrarli come fallimenti riempirebbe la dashboard di errori
// per ogni push su un branch di lavoro.
func skipReason(repo Repository, event githubhook.PushEvent, branch, commit string) (string, bool) {
	switch {
	case !repo.Enabled:
		return "collegamento disattivato", true
	case branch == "":
		// Un tag, o una `refs/` che non è un ramo. `cron.yaml` si sincronizza
		// da un ramo.
		return "il riferimento non è un ramo", true
	case branch != repo.DefaultBranch:
		return "ramo diverso da quello collegato (" + repo.DefaultBranch + ")", true
	case !event.HasContent():
		// Il ramo è stato cancellato. **Non è «il file non c'è più»**:
		// cancellare il ramo collegato non è una richiesta di smettere di
		// eseguire i job, e trattarlo come tale archivierebbe tutto sulla base
		// di un'operazione che di solito è di pulizia.
		return "ramo cancellato: nessun commit da leggere", true
	case !ValidCommit(commit):
		return "commit in una forma non riconosciuta", true
	}
	return "", false
}

// sync riconcilia un singolo collegamento.
func (s *Service) sync(ctx context.Context, repo Repository, event githubhook.PushEvent, commit string) error {
	log := s.log.With(
		slog.String("delivery", event.Delivery),
		slog.String("repository", repo.FullName()),
		slog.String("commit", commit))

	// **Prima porta dell'idempotenza.** GitHub ripete le consegne e #421 le
	// deduplica per identificativo, ma due consegne *diverse* possono portare
	// lo stesso commit — una ripetizione lanciata a mano dal registro dell'App,
	// due push ravvicinate che GitHub raggruppa. Questo confronto le ferma
	// prima ancora di scaricare il file. La seconda porta è dentro
	// [Store.Apply], sotto il lock della riga, per le due che arrivano insieme.
	if repo.LastSyncStatus == SyncSucceeded && repo.LastSyncedCommit == commit {
		log.InfoContext(ctx, "commit già sincronizzato, nessun effetto")
		return nil
	}

	// Il piano e i segreti sono dell'utente, non del repository: due
	// collegamenti dello stesso repository da parte di due clienti diversi
	// leggono lo stesso file e possono avere esiti diversi.
	plan, err := s.plans.PlanForUser(ctx, repo.UserID)
	if err != nil {
		return fmt.Errorf("lettura del piano: %w", err)
	}
	secretNames, err := s.secrets.Available(ctx, repo.UserID)
	if err != nil {
		return fmt.Errorf("elenco dei segreti: %w", err)
	}
	otherJobs, err := s.store.CountJobsOutside(ctx, repo.UserID, repo.ID)
	if err != nil {
		return fmt.Errorf("conteggio dei job fuori dal repository: %w", err)
	}

	installationID := repo.InstallationID
	if installationID <= 0 {
		installationID = event.InstallationID
	}

	source, found, err := s.contents.FileAtRef(ctx, installationID, repo.Owner, repo.Name, repo.ConfigPath, commit)
	if err != nil {
		// Un guasto di GitHub o un'installazione revocata: risale, la consegna
		// fallisce, GitHub ripete. È l'unico caso in cui ripetere serve.
		return fmt.Errorf("lettura di %s: %w", repo.ConfigPath, err)
	}
	if !found {
		// **Il file non c'è, e questo non archivia niente.** Un `cron.yaml`
		// assente è indistinguibile da un repository collegato per sbaglio, da
		// un percorso configurato male e da una cancellazione accidentale: tre
		// letture, di cui una sola vorrebbe che i job smettessero di girare. Chi
		// vuole quel risultato lo scrive nel file — `jobs: []` è valido, e
		// internal/cronyaml lo dichiara esplicitamente — dove resta visibile
		// nella pull request che lo introduce.
		reason := fmt.Sprintf(
			"%s non esiste al commit %s. Se volevi togliere i job, scrivi `jobs: []` nel file: cancellarlo non li disattiva.",
			repo.ConfigPath, shortCommit(commit))
		log.WarnContext(ctx, "cron.yaml non trovato, stato invariato")
		return s.recordFailure(ctx, repo, commit, reason)
	}

	file, err := cronyaml.Parse(ctx, source, cronyaml.Options{
		Source:    repo.ConfigPath,
		Plan:      plan,
		Secrets:   secretNames,
		Guard:     s.guard,
		OtherJobs: otherJobs,
	})
	if err != nil {
		// **R13: un file non valido non modifica lo stato esistente.** Nessuna
		// scrittura sui job è nemmeno stata calcolata: [Reconcile] non viene
		// chiamata. L'unica traccia è su `repositories`, con il testo completo
		// degli errori — riga, colonna e correzione — che è ciò che l'utente
		// legge in dashboard.
		limited := false
		if parse, ok := cronyaml.AsParseError(err); ok {
			limited = parse.PlanLimited()
		}
		log.WarnContext(ctx, "cron.yaml rifiutato, stato invariato",
			slog.Bool("plan_limited", limited),
			slog.Any("error", err))
		return s.recordFailure(ctx, repo, commit, err.Error())
	}

	current, err := s.store.ManagedJobs(ctx, repo.ID)
	if err != nil {
		return fmt.Errorf("lettura dei job del repository: %w", err)
	}

	reconciled := Reconcile(repo, file.Domain(), current)
	reconciled.Commit = commit
	reconciled.MaxJobs = plan.MaxJobs

	applied, err := s.store.Apply(ctx, reconciled)
	switch {
	case errors.Is(err, ErrAlreadySynced):
		// Una consegna gemella è arrivata al traguardo per prima. L'effetto c'è
		// già ed è lo stesso: non c'è niente da fare e niente da segnalare.
		log.InfoContext(ctx, "commit già sincronizzato da una consegna concorrente")
		return nil

	case errors.Is(err, jobs.ErrJobLimitReached):
		// Il tetto di piano è stato sfondato **fra** il conteggio e la
		// scrittura, da un job creato nel frattempo dalla dashboard o
		// dall'API. La transazione è tornata indietro per intero: nessun job è
		// stato creato, e nessuno di quelli esistenti è cambiato.
		reason := planLimitReason(plan, len(file.Jobs))
		log.WarnContext(ctx, "sync rifiutato dal tetto di piano, stato invariato",
			slog.String("plan", plan.Code))
		return s.recordFailure(ctx, repo, commit, reason)

	case err != nil:
		return fmt.Errorf("applicazione della riconciliazione: %w", err)
	}

	log.InfoContext(ctx, "cron.yaml sincronizzato",
		slog.Int("created", applied.Created),
		slog.Int("updated", applied.Updated),
		slog.Int("restored", applied.Restored),
		slog.Int("archived", applied.Archived),
		slog.Int("unchanged", reconciled.Unchanged))
	return nil
}

// recordFailure registra un rifiuto sul collegamento.
//
// L'errore della registrazione **risale**, e non è un dettaglio: se non
// riusciamo a dire all'utente che il file è stato rifiutato, quello che resta è
// un push che sembra riuscito e dei job che non sono cambiati. Meglio una
// consegna fallita, che GitHub ripete.
func (s *Service) recordFailure(ctx context.Context, repo Repository, commit, reason string) error {
	if err := s.store.RecordFailure(ctx, repo.ID, commit, truncate(reason, MaxSyncErrorLength)); err != nil {
		return fmt.Errorf("registrazione del sync fallito: %w", err)
	}
	return nil
}

// planLimitReason spiega il rifiuto del tetto di piano con le stesse parole con
// cui lo spiega [jobs.Plan.CheckJobCount], perché è lo stesso rifiuto: qui
// arriva da una corsa fra due scritture invece che dal conteggio del file, e
// non è una distinzione che riguardi chi legge.
func planLimitReason(plan jobs.Plan, declared int) string {
	limit := "il tuo piano"
	if plan.MaxJobs != nil {
		name := plan.Name
		if name == "" {
			name = plan.Code
		}
		limit = fmt.Sprintf("il piano %s consente %d job", name, *plan.MaxJobs)
	}
	return fmt.Sprintf(
		"%s, e applicare questo file ne richiederebbe %d in tutto contando quelli che hai fuori dal repository. "+
			"Il file non è stato applicato: togli dei job, oppure passa a un piano superiore.",
		limit, declared)
}

// shortCommit accorcia un commit alla forma con cui GitHub lo mostra.
func shortCommit(commit string) string {
	if len(commit) <= 7 {
		return commit
	}
	return commit[:7]
}

// truncate accorcia un testo dicendo che è stato accorciato: un messaggio
// tagliato a metà senza avviso sembra un messaggio finito male.
func truncate(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	const suffix = "\n  […] elenco troncato."
	if maxLen <= len(suffix) {
		return text[:maxLen]
	}
	return text[:maxLen-len(suffix)] + suffix
}
