package reposync_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/reposync"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// archivio è un doppio di [reposync.Store] che riproduce ciò che conta del
// database: i job restano dove sono finché qualcuno non li scrive, e Apply è
// atomico — o passa tutto, o non passa niente.
//
// Un doppio più permissivo del database renderebbe verdi proprio i test che
// devono restare rossi: è la stessa nota di internal/jobstest.
type archivio struct {
	mu sync.Mutex

	repositories []reposync.Repository
	// job è indicizzato per id, come la tabella.
	job map[string]jobs.Job
	seq int

	// fuori è il numero di job dell'utente fuori dal repository.
	fuori int

	// applicazioni e fallimenti registrano cosa è stato chiesto allo store: è
	// così che un test prova che uno stato **non** è stato toccato.
	applicazioni []reposync.Plan
	fallimenti   []fallimento

	falliSu map[string]error
}

type fallimento struct {
	repositoryID string
	commit       string
	reason       string
}

func nuovoArchivio() *archivio {
	return &archivio{job: map[string]jobs.Job{}, falliSu: map[string]error{}}
}

func (a *archivio) collega(repo reposync.Repository) reposync.Repository {
	a.mu.Lock()
	defer a.mu.Unlock()
	if repo.ID == "" {
		a.seq++
		repo.ID = "repo-" + strconv.Itoa(a.seq)
	}
	a.repositories = append(a.repositories, repo)
	return repo
}

// inserisci mette un job già esistente, come se lo avesse creato un sync
// precedente.
func (a *archivio) inserisci(job jobs.Job) jobs.Job {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.seq++
	job.ID = "job-" + strconv.Itoa(a.seq)
	a.job[job.ID] = job
	return job
}

func (a *archivio) perNome(nome string) (jobs.Job, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, job := range a.job {
		if job.Name == nome {
			return job, true
		}
	}
	return jobs.Job{}, false
}

func (a *archivio) quantiJob() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.job)
}

func (a *archivio) RepositoriesByExternalID(_ context.Context, _ int64) ([]reposync.Repository, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.falliSu["RepositoriesByExternalID"]; err != nil {
		return nil, err
	}
	return append([]reposync.Repository(nil), a.repositories...), nil
}

func (a *archivio) ManagedJobs(_ context.Context, repositoryID string) ([]jobs.Job, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.falliSu["ManagedJobs"]; err != nil {
		return nil, err
	}
	var out []jobs.Job
	for _, job := range a.job {
		if job.RepositoryID == repositoryID {
			out = append(out, job)
		}
	}
	// L'ordine della mappa non è deterministico e quello del database sì: senza
	// questo, un test che confronta l'ordine delle modifiche sarebbe instabile.
	sortByName(out)
	return out, nil
}

func (a *archivio) CountJobsOutside(_ context.Context, _, _ string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.falliSu["CountJobsOutside"]; err != nil {
		return 0, err
	}
	return a.fuori, nil
}

func (a *archivio) Apply(_ context.Context, plan reposync.Plan) (reposync.Applied, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.falliSu["Apply"]; err != nil {
		return reposync.Applied{}, err
	}

	for i := range a.repositories {
		if a.repositories[i].ID != plan.RepositoryID {
			continue
		}
		if a.repositories[i].LastSyncedCommit == plan.Commit &&
			a.repositories[i].LastSyncStatus == reposync.SyncSucceeded {
			return reposync.Applied{}, reposync.ErrAlreadySynced
		}
	}

	// Il tetto di piano riverificato dentro la scrittura, come fa la
	// transazione vera.
	if plan.MaxJobs != nil {
		attivi := a.fuori
		for _, job := range a.job {
			if !job.Archived() {
				attivi++
			}
		}
		var creazioni int
		for _, change := range plan.Changes {
			if change.Kind == reposync.ChangeCreate {
				creazioni++
			}
			if change.Kind == reposync.ChangeArchive {
				attivi--
			}
		}
		if attivi+creazioni > *plan.MaxJobs {
			return reposync.Applied{}, jobs.ErrJobLimitReached
		}
	}

	var applied reposync.Applied
	adesso := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for _, change := range plan.Changes {
		switch change.Kind {
		case reposync.ChangeArchive:
			job := a.job[change.Job.ID]
			job.ArchivedAt = &adesso
			job.NextRunAt = nil
			a.job[job.ID] = job
			applied.Archived++

		case reposync.ChangeUpdate:
			corrente := a.job[change.Job.ID]
			scritto := change.Job
			// `enabled` è dell'utente: lo store non lo riceve nemmeno, e qui si
			// riprende da ciò che c'era. È la riga che rende questo doppio
			// fedele alla UPDATE vera, che quella colonna non la nomina.
			scritto.Enabled = corrente.Enabled
			if !change.ResetNextRun {
				scritto.NextRunAt = corrente.NextRunAt
			} else {
				scritto.NextRunAt = nil
			}
			a.job[scritto.ID] = scritto
			applied.Updated++
			if change.Restored {
				applied.Restored++
			}

		case reposync.ChangeCreate:
			a.seq++
			creato := change.Job
			creato.ID = "job-" + strconv.Itoa(a.seq)
			creato.Enabled = true
			a.job[creato.ID] = creato
			applied.Created++
		}
	}

	for i := range a.repositories {
		if a.repositories[i].ID == plan.RepositoryID {
			a.repositories[i].LastSyncedCommit = plan.Commit
			a.repositories[i].LastSyncStatus = reposync.SyncSucceeded
		}
	}

	a.applicazioni = append(a.applicazioni, plan)
	return applied, nil
}

func (a *archivio) RecordFailure(_ context.Context, repositoryID, commit, reason string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.falliSu["RecordFailure"]; err != nil {
		return err
	}
	for i := range a.repositories {
		if a.repositories[i].ID == repositoryID {
			a.repositories[i].LastSyncedCommit = commit
			a.repositories[i].LastSyncStatus = reposync.SyncFailed
		}
	}
	a.fallimenti = append(a.fallimenti, fallimento{repositoryID, commit, reason})
	return nil
}

func sortByName(items []jobs.Job) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Name < items[j-1].Name; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// ------------------------------------------------------------- altri doppi

// listino restituisce sempre lo stesso piano.
type listino struct {
	piano jobs.Plan
	err   error
}

func (l listino) PlanForUser(context.Context, string) (jobs.Plan, error) {
	return l.piano, l.err
}

// segreti restituisce i nomi dei segreti del workspace.
type segreti struct {
	nomi []string
	err  error
}

func (s segreti) Available(context.Context, string) (secrets.NameSet, error) {
	if s.err != nil {
		return secrets.NameSet{}, s.err
	}
	return secrets.NewNameSet(s.nomi), nil
}

// repository è il contenuto finto di un repository GitHub, indicizzato per
// commit: è così che un test può far tornare due file diversi a due push
// diverse.
type repository struct {
	mu       sync.Mutex
	perRef   map[string]string
	assente  bool
	err      error
	letture  int
	ultimoID int64
}

func nuovoRepository(contenuto string) *repository {
	return &repository{perRef: map[string]string{"": contenuto}}
}

func (r *repository) scrivi(ref, contenuto string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.perRef[ref] = contenuto
}

func (r *repository) FileAtRef(_ context.Context, installationID int64, _, _, _, ref string) ([]byte, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.letture++
	r.ultimoID = installationID
	if r.err != nil {
		return nil, false, r.err
	}
	if r.assente {
		return nil, false, nil
	}
	if contenuto, ok := r.perRef[ref]; ok {
		return []byte(contenuto), true, nil
	}
	return []byte(r.perRef[""]), true, nil
}

// ------------------------------------------------------------------ eventi

const (
	commitUno = "1111111111111111111111111111111111111111"
	commitDue = "2222222222222222222222222222222222222222"
)

func push(commit string) githubhook.PushEvent {
	return githubhook.PushEvent{
		Delivery:       "consegna-" + commit[:7],
		InstallationID: 4242,
		Repository: githubhook.Repository{
			ExternalID:    777,
			Owner:         "acme",
			Name:          "api",
			FullName:      "acme/api",
			DefaultBranch: "main",
		},
		Ref:   "refs/heads/main",
		After: commit,
	}
}

func collegamento() reposync.Repository {
	return reposync.Repository{
		UserID:         "utente-1",
		InstallationID: 4242,
		Owner:          "acme",
		Name:           "api",
		DefaultBranch:  "main",
		ConfigPath:     "cron.yaml",
		Enabled:        true,
	}
}

// ------------------------------------------------------------------- utilità

func servizio(store reposync.Store, contents reposync.Contents, piano jobs.Plan, nomiSegreti []string) (*reposync.Service, error) {
	return reposync.NewService(reposync.Options{
		Store:    store,
		Plans:    listino{piano: piano},
		Secrets:  segreti{nomi: nomiSegreti},
		Contents: contents,
	})
}

func fileConJob(nomi ...string) string {
	out := "version: 1\njobs:\n"
	for _, nome := range nomi {
		out += fmt.Sprintf("  - name: %s\n    every: 5m\n    request: { url: https://esempio.it/%s, method: GET }\n", nome, nome)
	}
	return out
}

var errFinto = errors.New("guasto simulato")
