package reposync_test

import (
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/reposync"
)

// jobDesiderato è una voce come la restituisce internal/cronyaml:
// `RepositoryID` vuoto ed `Enabled` a true.
func jobDesiderato(nome string) jobs.Job {
	job := jobs.NewJob()
	job.Name = nome
	job.Every = 5 * time.Minute
	job.URL = "https://esempio.it/" + nome
	job.Method = jobs.MethodGET
	return job
}

// jobEsistente è lo stesso job come sta nel database dopo un sync.
func jobEsistente(id, nome string) jobs.Job {
	job := jobDesiderato(nome)
	job.ID = id
	job.UserID = "utente-1"
	job.RepositoryID = "repo-1"
	return job
}

func repoDiProva() reposync.Repository {
	return reposync.Repository{ID: "repo-1", UserID: "utente-1"}
}

func TestUnJobNuovoVieneCreato(t *testing.T) {
	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{jobDesiderato("digest")}, nil)

	if len(plan.Changes) != 1 || plan.Changes[0].Kind != reposync.ChangeCreate {
		t.Fatalf("piano = %+v, atteso una sola creazione", plan.Changes)
	}
	creato := plan.Changes[0].Job
	if creato.RepositoryID != "repo-1" {
		t.Errorf("RepositoryID = %q: il job creato deve appartenere al repository", creato.RepositoryID)
	}
	if creato.UserID != "utente-1" {
		t.Errorf("UserID = %q", creato.UserID)
	}
	if creato.NextRunAt != nil {
		t.Error("un job appena creato non ha una prossima occorrenza: la calcola lo scheduler (0010)")
	}
}

// Il caso che fa danno numero 1: un job sparito dal file si **archivia**, e
// archiviare non è cancellare.
func TestUnJobSparitoDalFileVieneArchiviatoENonCancellato(t *testing.T) {
	esistente := jobEsistente("job-1", "vecchio")

	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{jobDesiderato("nuovo")}, []jobs.Job{esistente})

	var archiviato, creato bool
	for _, change := range plan.Changes {
		switch change.Kind {
		case reposync.ChangeArchive:
			archiviato = true
			if change.Job.ID != "job-1" {
				t.Errorf("archiviato il job sbagliato: %q", change.Job.ID)
			}
		case reposync.ChangeCreate:
			creato = true
		}
	}
	if !archiviato {
		t.Error("il job sparito dal file non è stato archiviato")
	}
	if !creato {
		t.Error("il job nuovo non è stato creato")
	}

	// Non esiste un tipo di modifica che cancelli: è la proprietà, non un
	// dettaglio dell'implementazione. Lo storico delle esecuzioni di un job
	// archiviato resta consultabile, ed è l'unico modo per capire cosa faceva.
	for _, change := range plan.Changes {
		if change.Kind != reposync.ChangeCreate &&
			change.Kind != reposync.ChangeUpdate &&
			change.Kind != reposync.ChangeArchive {
			t.Fatalf("tipo di modifica inatteso %q: la riconciliazione non cancella", change.Kind)
		}
	}
}

// Rinominare un job equivale a cancellarlo e crearne un altro (SPEC §9):
// l'identità è `name`, non l'URL né la schedulazione.
func TestRinominareUnJobLoArchiviaENeCreaUnAltro(t *testing.T) {
	esistente := jobEsistente("job-1", "digest")
	rinominato := jobDesiderato("digest-quotidiano")
	rinominato.URL = esistente.URL

	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{rinominato}, []jobs.Job{esistente})

	created, updated, archived := plan.Counts()
	if created != 1 || archived != 1 || updated != 0 {
		t.Fatalf("creati=%d aggiornati=%d archiviati=%d, atteso 1/0/1", created, updated, archived)
	}
}

func TestUnFileIdenticoNonProduceNessunaModifica(t *testing.T) {
	esistente := jobEsistente("job-1", "digest")

	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{jobDesiderato("digest")}, []jobs.Job{esistente})

	if !plan.Empty() {
		t.Fatalf("piano = %+v, atteso vuoto: due push identiche non devono riscrivere niente", plan.Changes)
	}
	if plan.Unchanged != 1 {
		t.Errorf("Unchanged = %d, atteso 1", plan.Unchanged)
	}
}

func TestUnJobCambiatoVieneAggiornato(t *testing.T) {
	esistente := jobEsistente("job-1", "digest")
	desiderato := jobDesiderato("digest")
	desiderato.URL = "https://esempio.it/nuovo-indirizzo"

	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{desiderato}, []jobs.Job{esistente})

	if len(plan.Changes) != 1 || plan.Changes[0].Kind != reposync.ChangeUpdate {
		t.Fatalf("piano = %+v, atteso un solo aggiornamento", plan.Changes)
	}
	aggiornato := plan.Changes[0].Job
	if aggiornato.ID != "job-1" {
		t.Errorf("ID = %q: un job aggiornato è lo stesso job", aggiornato.ID)
	}
	if aggiornato.URL != "https://esempio.it/nuovo-indirizzo" {
		t.Errorf("URL = %q", aggiornato.URL)
	}
	if plan.Changes[0].ResetNextRun {
		t.Error("cambiare l'URL non invalida la prossima occorrenza: quella colonna è dello scheduler")
	}
}

func TestCambiareLaSchedulazioneAzzeraLaProssimaOccorrenza(t *testing.T) {
	esistente := jobEsistente("job-1", "digest")
	quando := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	esistente.NextRunAt = &quando

	casi := map[string]func(*jobs.Job){
		"intervallo diverso": func(j *jobs.Job) { j.Every = 10 * time.Minute },
		"da intervallo a cron": func(j *jobs.Job) {
			j.Every = 0
			j.Schedule = "0 9 * * *"
		},
		"fuso diverso": func(j *jobs.Job) { j.Timezone = "Europe/Rome" },
	}

	for nome, modifica := range casi {
		t.Run(nome, func(t *testing.T) {
			desiderato := jobDesiderato("digest")
			modifica(&desiderato)

			plan := reposync.Reconcile(repoDiProva(), []jobs.Job{desiderato}, []jobs.Job{esistente})
			if len(plan.Changes) != 1 {
				t.Fatalf("piano = %+v, atteso un solo aggiornamento", plan.Changes)
			}
			if !plan.Changes[0].ResetNextRun {
				t.Error("la prossima occorrenza calcolata non vale più: va azzerata")
			}
		})
	}
}

// Il caso che fa danno numero 2: la pausa manuale sopravvive al sync.
func TestUnJobInPausaNonVieneRiattivatoDaUnPushCheNonLoTocca(t *testing.T) {
	inPausa := jobEsistente("job-1", "digest")
	inPausa.Enabled = false

	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{jobDesiderato("digest")}, []jobs.Job{inPausa})

	if !plan.Empty() {
		t.Fatalf("piano = %+v: un push che non cambia il job non deve toccarlo, e riattivarlo è toccarlo",
			plan.Changes)
	}
}

func TestUnJobInPausaRestaInPausaAncheQuandoIlFileLoCambia(t *testing.T) {
	inPausa := jobEsistente("job-1", "digest")
	inPausa.Enabled = false

	desiderato := jobDesiderato("digest")
	desiderato.URL = "https://esempio.it/altro"

	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{desiderato}, []jobs.Job{inPausa})

	if len(plan.Changes) != 1 {
		t.Fatalf("piano = %+v, atteso un solo aggiornamento", plan.Changes)
	}
	if plan.Changes[0].Job.Enabled {
		t.Error("il file non esprime la pausa e non deve poterla sovrascrivere (SPEC §9, migrazione 0005)")
	}
}

func TestUnJobTornatoNelFileVieneRipristinatoENonDuplicato(t *testing.T) {
	quando := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	archiviato := jobEsistente("job-1", "digest")
	archiviato.ArchivedAt = &quando

	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{jobDesiderato("digest")}, []jobs.Job{archiviato})

	if len(plan.Changes) != 1 {
		t.Fatalf("piano = %+v, attesa una sola modifica", plan.Changes)
	}
	change := plan.Changes[0]
	if change.Kind != reposync.ChangeUpdate {
		t.Fatalf("Kind = %q: un job che torna nel file è lo stesso job, non uno nuovo", change.Kind)
	}
	if !change.Restored {
		t.Error("Restored = false: il ripristino di un job archiviato è un fatto a sé nel registro")
	}
	if change.Job.ArchivedAt != nil {
		t.Error("il job ripristinato è ancora archiviato")
	}
}

// La pausa non è una risposta all'archiviazione, e il ripristino non è una
// risposta alla pausa: le due dimensioni restano indipendenti in tutte e
// quattro le combinazioni.
func TestUnJobInPausaEArchiviatoTornaInPausa(t *testing.T) {
	quando := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	job := jobEsistente("job-1", "digest")
	job.Enabled = false
	job.ArchivedAt = &quando

	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{jobDesiderato("digest")}, []jobs.Job{job})

	if len(plan.Changes) != 1 {
		t.Fatalf("piano = %+v, attesa una sola modifica", plan.Changes)
	}
	if plan.Changes[0].Job.Enabled {
		t.Error("un job messo in pausa, tolto dal file e rimesso deve tornare in pausa")
	}
	if plan.Changes[0].Job.ArchivedAt != nil {
		t.Error("il job è tornato nel file: non è più archiviato")
	}
}

func TestUnJobGiaArchiviatoENonPiuNelFileNonVieneRiarchiviato(t *testing.T) {
	quando := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	job := jobEsistente("job-1", "sparito")
	job.ArchivedAt = &quando

	plan := reposync.Reconcile(repoDiProva(), nil, []jobs.Job{job})

	if !plan.Empty() {
		t.Fatalf("piano = %+v: riarchiviare sposterebbe in avanti la data da cui il job non gira più",
			plan.Changes)
	}
}

func TestUnElencoVuotoArchiviaTutto(t *testing.T) {
	plan := reposync.Reconcile(repoDiProva(), nil, []jobs.Job{
		jobEsistente("job-1", "uno"),
		jobEsistente("job-2", "due"),
	})

	created, updated, archived := plan.Counts()
	if created != 0 || updated != 0 || archived != 2 {
		t.Fatalf("creati=%d aggiornati=%d archiviati=%d, atteso 0/0/2", created, updated, archived)
	}
}

// L'ordine conta per il tetto di piano: un file che toglie dei job e ne
// aggiunge altrettanti deve passare anche su un piano al limite.
func TestLeArchiviazioniPrecedonoLeCreazioni(t *testing.T) {
	plan := reposync.Reconcile(repoDiProva(),
		[]jobs.Job{jobDesiderato("nuovo")},
		[]jobs.Job{jobEsistente("job-1", "vecchio")})

	if len(plan.Changes) != 2 {
		t.Fatalf("piano = %+v, attese due modifiche", plan.Changes)
	}
	if plan.Changes[0].Kind != reposync.ChangeArchive {
		t.Errorf("la prima modifica è %q, attesa un'archiviazione", plan.Changes[0].Kind)
	}
	if plan.Changes[1].Kind != reposync.ChangeCreate {
		t.Errorf("la seconda modifica è %q, attesa una creazione", plan.Changes[1].Kind)
	}
}

// I job creati a mano dall'utente non appartengono a nessun repository e la
// riconciliazione non li vede: [Store.ManagedJobs] filtra per repository, e
// questo test fissa il fatto che Reconcile non ha nessun modo di raggiungerli.
func TestLaRiconciliazioneLavoraSoloSuiJobCheRiceve(t *testing.T) {
	plan := reposync.Reconcile(repoDiProva(), []jobs.Job{jobDesiderato("digest")}, nil)

	if archiviati := len(plan.Changes) - 1; archiviati != 0 {
		t.Fatalf("piano = %+v: senza job correnti non c'è niente da archiviare", plan.Changes)
	}
}
