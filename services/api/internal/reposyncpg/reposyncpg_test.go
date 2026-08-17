package reposyncpg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/reposync"
	"github.com/apdsoftware/postqron/services/api/internal/reposyncpg"
)

func nuovoStore(t *testing.T) (*reposyncpg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := newTestDatabase(t)
	store, err := reposyncpg.New(pool)
	if err != nil {
		t.Fatalf("costruzione dello store: %v", err)
	}
	return store, pool
}

func TestUnPianoVieneApplicatoEIlCollegamentoRegistraLEsito(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "uno@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	applied, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID,
		UserID:       utente,
		Commit:       commitUno,
		Changes: []reposync.Change{
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "digest")},
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "healthcheck")},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applied.Created != 2 {
		t.Errorf("Created = %d, attesi 2", applied.Created)
	}

	creato, ok := leggiJob(t, pool, repo.ID, "digest")
	if !ok {
		t.Fatal("il job non è stato creato")
	}
	if creato.NextRunAt != nil {
		t.Error("un job appena creato non ha una prossima occorrenza: la calcola lo scheduler (0010)")
	}
	if !creato.Enabled {
		t.Error("un job nato dal file nasce attivo")
	}

	repositories, err := store.RepositoriesByExternalID(t.Context(), 777)
	if err != nil {
		t.Fatalf("RepositoriesByExternalID: %v", err)
	}
	if len(repositories) != 1 {
		t.Fatalf("collegamenti = %d, atteso 1", len(repositories))
	}
	if got := repositories[0].LastSyncedCommit; got != commitUno {
		t.Errorf("LastSyncedCommit = %q, atteso %q", got, commitUno)
	}
	if got := repositories[0].LastSyncStatus; got != reposync.SyncSucceeded {
		t.Errorf("LastSyncStatus = %q, atteso succeeded", got)
	}
}

// Il caso che fa danno numero 1, contro il database vero: archiviare conserva
// la riga e le sue esecuzioni.
func TestArchiviareConservaLaRigaELeSueEsecuzioni(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "due@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	if _, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "digest")}},
	}); err != nil {
		t.Fatalf("Apply (creazione): %v", err)
	}

	creato, _ := leggiJob(t, pool, repo.ID, "digest")
	inserisciEsecuzione(t, pool, creato.ID)

	correnti, err := store.ManagedJobs(t.Context(), repo.ID)
	if err != nil {
		t.Fatalf("ManagedJobs: %v", err)
	}
	if len(correnti) != 1 {
		t.Fatalf("job del repository = %d, atteso 1", len(correnti))
	}

	// Il file non dichiara più niente: tutto archiviato.
	piano := reposync.Reconcile(repo, nil, correnti)
	piano.Commit = commitDue
	if _, err := store.Apply(t.Context(), piano); err != nil {
		t.Fatalf("Apply (archiviazione): %v", err)
	}

	dopo, ancoraCE := leggiJob(t, pool, repo.ID, "digest")
	if !ancoraCE {
		t.Fatal("il job è stato cancellato invece che archiviato")
	}
	if dopo.ArchivedAt == nil {
		t.Error("archived_at non è stato valorizzato")
	}
	if dopo.NextRunAt != nil {
		t.Error("un job archiviato non deve avere una prossima occorrenza")
	}
	if n := contaEsecuzioni(t, pool, creato.ID); n != 1 {
		t.Errorf("esecuzioni = %d, attesa 1: lo storico è l'unico modo per capire cosa faceva il job", n)
	}
}

// Il caso che fa danno numero 2, contro il database vero: nessuna scrittura del
// sync nomina la colonna `enabled`.
func TestIlSyncNonRiattivaUnJobInPausa(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "tre@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	if _, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "digest")}},
	}); err != nil {
		t.Fatalf("Apply (creazione): %v", err)
	}

	// L'utente mette in pausa dalla dashboard.
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET enabled = false WHERE repository_id = $1::uuid`, repo.ID); err != nil {
		t.Fatalf("pausa manuale: %v", err)
	}

	correnti, err := store.ManagedJobs(t.Context(), repo.ID)
	if err != nil {
		t.Fatalf("ManagedJobs: %v", err)
	}
	if correnti[0].Enabled {
		t.Fatal("la pausa non è stata letta: il resto del test non proverebbe niente")
	}

	// Il push successivo cambia l'URL di quel job.
	desiderato := jobDelFile("digest")
	desiderato.URL = "https://esempio.it/nuovo"
	piano := reposync.Reconcile(repo, []jobs.Job{desiderato}, correnti)
	piano.Commit = commitDue
	if len(piano.Changes) != 1 {
		t.Fatalf("piano = %+v, atteso un solo aggiornamento", piano.Changes)
	}
	if _, err := store.Apply(t.Context(), piano); err != nil {
		t.Fatalf("Apply (aggiornamento): %v", err)
	}

	dopo, _ := leggiJob(t, pool, repo.ID, "digest")
	if dopo.Enabled {
		t.Error("il sync ha riattivato un job messo in pausa dalla dashboard")
	}
	if dopo.URL != "https://esempio.it/nuovo" {
		t.Errorf("URL = %q: il resto dell'aggiornamento deve essere avvenuto", dopo.URL)
	}
}

// Un job tolto dal file e poi rimesso è la stessa riga: se fosse un INSERT,
// `jobs_repository_name_key` lo rifiuterebbe — e prima ancora, l'utente
// perderebbe lo storico.
func TestUnJobRipristinatoELaStessaRiga(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "quattro@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	if _, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "digest")}},
	}); err != nil {
		t.Fatalf("Apply (creazione): %v", err)
	}
	primo, _ := leggiJob(t, pool, repo.ID, "digest")

	correnti, _ := store.ManagedJobs(t.Context(), repo.ID)
	piano := reposync.Reconcile(repo, nil, correnti)
	piano.Commit = commitDue
	if _, err := store.Apply(t.Context(), piano); err != nil {
		t.Fatalf("Apply (archiviazione): %v", err)
	}

	// Il file lo rimette.
	correnti, _ = store.ManagedJobs(t.Context(), repo.ID)
	piano = reposync.Reconcile(repo, []jobs.Job{jobDelFile("digest")}, correnti)
	piano.Commit = "3333333333333333333333333333333333333333"
	applied, err := store.Apply(t.Context(), piano)
	if err != nil {
		t.Fatalf("Apply (ripristino): %v", err)
	}
	if applied.Restored != 1 {
		t.Errorf("Restored = %d, atteso 1", applied.Restored)
	}

	dopo, _ := leggiJob(t, pool, repo.ID, "digest")
	if dopo.ID != primo.ID {
		t.Errorf("ID = %q, atteso %q: un job che torna nel file è lo stesso job", dopo.ID, primo.ID)
	}
	if dopo.ArchivedAt != nil {
		t.Error("il job ripristinato è ancora archiviato")
	}
	if n := contaJob(t, pool, repo.ID); n != 1 {
		t.Errorf("job nel repository = %d, atteso 1: nessun duplicato", n)
	}
}

// L'atomicità di R13: se una scrittura fallisce, non ne resta nessuna.
func TestUnPianoCheFallisceAMetaNonLasciaTracce(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "cinque@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	// Il secondo job ha un URL che `jobs_url_scheme_check` rifiuta: è un job
	// che il parser non produrrebbe mai, e qui serve proprio a rompere la
	// scrittura a metà per vedere cosa resta.
	rotto := jobCreato(repo, "rotto")
	rotto.URL = "ftp://esempio.it/no"

	_, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "buono")},
			{Kind: reposync.ChangeCreate, Job: rotto},
		},
	})
	if err == nil {
		t.Fatal("Apply = nil: il vincolo del database avrebbe dovuto rifiutare il secondo job")
	}

	if n := contaJob(t, pool, repo.ID); n != 0 {
		t.Errorf("job creati = %d, attesi 0: un sync a metà è peggio di un sync mancato", n)
	}

	repositories, _ := store.RepositoriesByExternalID(t.Context(), 777)
	if got := repositories[0].LastSyncStatus; got == reposync.SyncSucceeded {
		t.Error("il collegamento risulta sincronizzato con successo dopo una transazione annullata")
	}
}

func TestApplicareDueVolteLoStessoCommitNonProduceUnSecondoEffetto(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "sei@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	piano := reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "digest")}},
	}
	if _, err := store.Apply(t.Context(), piano); err != nil {
		t.Fatalf("primo Apply: %v", err)
	}
	if _, err := store.Apply(t.Context(), piano); !errors.Is(err, reposync.ErrAlreadySynced) {
		t.Fatalf("secondo Apply = %v, atteso ErrAlreadySynced", err)
	}
	if n := contaJob(t, pool, repo.ID); n != 1 {
		t.Errorf("job = %d, atteso 1: un secondo INSERT sarebbe finito su jobs_repository_name_key", n)
	}
}

// Il caso che fa danno numero 3, dal lato della persistenza: il percorso di
// fallimento non sa scrivere sui job.
func TestRecordFailureNonToccaIJob(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "sette@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	if _, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "digest")}},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	prima, _ := leggiJob(t, pool, repo.ID, "digest")

	if err := store.RecordFailure(t.Context(), repo.ID, commitDue, "cron.yaml:3:5: jobs[0].every: durata illeggibile"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	dopo, ok := leggiJob(t, pool, repo.ID, "digest")
	if !ok {
		t.Fatal("il job è sparito")
	}
	if dopo.ArchivedAt != nil || dopo.URL != prima.URL || dopo.Enabled != prima.Enabled {
		t.Errorf("il job è cambiato:\n prima = %+v\n dopo  = %+v", prima, dopo)
	}

	repositories, _ := store.RepositoriesByExternalID(t.Context(), 777)
	if got := repositories[0].LastSyncStatus; got != reposync.SyncFailed {
		t.Errorf("LastSyncStatus = %q, atteso failed", got)
	}
	if got := repositories[0].LastSyncedCommit; got != commitDue {
		t.Errorf("LastSyncedCommit = %q, atteso %q", got, commitDue)
	}
}

// Un commit in una forma che `repositories_last_synced_commit_check`
// rifiuterebbe non deve far cadere l'unica riga che spiega l'errore.
func TestRecordFailureConUnCommitNonScrivibileRegistraComunqueIlMotivo(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "otto@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	if err := store.RecordFailure(t.Context(), repo.ID, "NON-UN-COMMIT", "il file non è leggibile"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}

	repositories, _ := store.RepositoriesByExternalID(t.Context(), 777)
	if got := repositories[0].LastSyncStatus; got != reposync.SyncFailed {
		t.Errorf("LastSyncStatus = %q, atteso failed", got)
	}
	if got := repositories[0].LastSyncedCommit; got != "" {
		t.Errorf("LastSyncedCommit = %q, atteso vuoto", got)
	}
}

func TestIlTettoDiPianoVieneRiverificatoDentroLaTransazione(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "nove@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	// Due job creati dalla dashboard: fuori dal repository, dentro il tetto.
	for _, nome := range []string{"a-mano-uno", "a-mano-due"} {
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO jobs (user_id, name, every_seconds, url) VALUES ($1::uuid, $2, 300, 'https://esempio.it/x')`,
			utente, nome); err != nil {
			t.Fatalf("creazione del job a mano: %v", err)
		}
	}

	tetto := 3
	_, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno, MaxJobs: &tetto,
		Changes: []reposync.Change{
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "uno")},
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "due")},
		},
	})
	if !errors.Is(err, jobs.ErrJobLimitReached) {
		t.Fatalf("Apply = %v, atteso ErrJobLimitReached", err)
	}
	if n := contaJob(t, pool, repo.ID); n != 0 {
		t.Errorf("job creati = %d, attesi 0: il rifiuto è per intero, non parziale", n)
	}
}

// Un file che toglie dei job e ne aggiunge altrettanti deve passare anche su un
// piano al limite: è per questo che le archiviazioni precedono le creazioni.
func TestUnFileCheSostituisceIJobPassaAncheAlLimiteDelPiano(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "dieci@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	tetto := 2
	if _, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno, MaxJobs: &tetto,
		Changes: []reposync.Change{
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "vecchio-uno")},
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "vecchio-due")},
		},
	}); err != nil {
		t.Fatalf("Apply (creazione): %v", err)
	}

	correnti, _ := store.ManagedJobs(t.Context(), repo.ID)
	piano := reposync.Reconcile(repo, []jobs.Job{jobDelFile("nuovo-uno"), jobDelFile("nuovo-due")}, correnti)
	piano.Commit = commitDue
	piano.MaxJobs = &tetto

	if _, err := store.Apply(t.Context(), piano); err != nil {
		t.Fatalf("Apply (sostituzione) = %v: due tolti e due aggiunti stanno nel tetto di due", err)
	}
	if _, ok := leggiJob(t, pool, repo.ID, "nuovo-uno"); !ok {
		t.Error("il job nuovo non è stato creato")
	}
}

func TestCountJobsOutsideEscludeIJobDelRepositoryEGliArchiviati(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "undici@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)
	altro := collegaAltroRepository(t, pool, utente, 888)

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO jobs (user_id, name, every_seconds, url) VALUES ($1::uuid, 'a-mano', 300, 'https://esempio.it/x')`,
		utente); err != nil {
		t.Fatalf("job a mano: %v", err)
	}
	if _, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "nostro")}},
	}); err != nil {
		t.Fatalf("Apply (nostro): %v", err)
	}
	if _, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: altro.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{{Kind: reposync.ChangeCreate, Job: jobCreato(altro, "altrui")}},
	}); err != nil {
		t.Fatalf("Apply (altrui): %v", err)
	}
	// Un archiviato non occupa un posto nel catalogo.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO jobs (user_id, name, every_seconds, url, archived_at)
		 VALUES ($1::uuid, 'sepolto', 300, 'https://esempio.it/x', now())`, utente); err != nil {
		t.Fatalf("job archiviato: %v", err)
	}

	fuori, err := store.CountJobsOutside(t.Context(), utente, repo.ID)
	if err != nil {
		t.Fatalf("CountJobsOutside: %v", err)
	}
	if fuori != 2 {
		t.Errorf("CountJobsOutside = %d, attesi 2 (uno a mano + uno dell'altro repository)", fuori)
	}
}

// I job di un repository si leggono tutti, e solo quelli: `ManagedJobs` filtra
// per repository perché la riconciliazione non deve vedere i job creati a mano.
func TestManagedJobsVedeSoloIJobDelRepositoryArchiviatiCompresi(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "dodici@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO jobs (user_id, name, every_seconds, url) VALUES ($1::uuid, 'a-mano', 300, 'https://esempio.it/x')`,
		utente); err != nil {
		t.Fatalf("job a mano: %v", err)
	}
	if _, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
		Changes: []reposync.Change{
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "uno")},
			{Kind: reposync.ChangeCreate, Job: jobCreato(repo, "due")},
		},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET archived_at = now() WHERE repository_id = $1::uuid AND name = 'due'`, repo.ID); err != nil {
		t.Fatalf("archiviazione: %v", err)
	}

	correnti, err := store.ManagedJobs(t.Context(), repo.ID)
	if err != nil {
		t.Fatalf("ManagedJobs: %v", err)
	}
	if len(correnti) != 2 {
		t.Fatalf("job letti = %d, attesi 2 (l'archiviato compreso)", len(correnti))
	}
	for _, job := range correnti {
		if job.RepositoryID != repo.ID {
			t.Errorf("job %q non appartiene al repository", job.Name)
		}
		if job.Method != jobs.MethodGET {
			t.Errorf("job %q: Method = %q, atteso GET", job.Name, job.Method)
		}
		if job.Every != 5*time.Minute {
			t.Errorf("job %q: Every = %v, atteso 5m", job.Name, job.Every)
		}
	}
}

// Il round-trip dei campi: ciò che il file dichiara deve tornare identico dalla
// lettura, altrimenti la riconciliazione successiva vedrebbe una differenza che
// non c'è e riscriverebbe il job a ogni push.
func TestUnJobScrittoERilettoENonProduceModificheAlSyncSuccessivo(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "tredici@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	desiderato := jobDelFile("digest")
	desiderato.Description = "il digest quotidiano"
	desiderato.Every = 0
	desiderato.Schedule = "0 9 * * *"
	desiderato.Timezone = "Europe/Rome"
	// Due ambienti nell'ordine del file: `slices.Equal` in sameSpec è sensibile
	// all'ordine, e un array che tornasse riordinato dal database renderebbe
	// ogni push un aggiornamento.
	desiderato.Environments = []jobs.Environment{jobs.EnvironmentStaging, jobs.EnvironmentProduction}
	desiderato.Method = jobs.MethodPOST
	desiderato.Headers = map[string]string{"Authorization": "Bearer ${TOKEN}", "X-Origine": "postqron"}
	desiderato.Body = `{"kind":"daily"}`
	desiderato.Timeout = 60 * time.Second
	desiderato.MaxRetries = 5
	desiderato.RetryBackoff = jobs.BackoffLinear
	desiderato.AlertOnFailure = []jobs.AlertChannel{jobs.AlertEmail, jobs.AlertSlack}

	primo := reposync.Reconcile(repo, []jobs.Job{desiderato}, nil)
	primo.Commit = commitUno
	if _, err := store.Apply(t.Context(), primo); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	correnti, err := store.ManagedJobs(t.Context(), repo.ID)
	if err != nil {
		t.Fatalf("ManagedJobs: %v", err)
	}
	secondo := reposync.Reconcile(repo, []jobs.Job{desiderato}, correnti)
	if !secondo.Empty() {
		t.Fatalf("il secondo sync produce %d modifiche, atteso nessuna: un campo non sopravvive al round-trip.\n%+v",
			len(secondo.Changes), secondo.Changes)
	}
	if secondo.Unchanged != 1 {
		t.Errorf("Unchanged = %d, atteso 1", secondo.Unchanged)
	}
}

func TestUnCollegamentoSparitoNonEUnGuasto(t *testing.T) {
	store, pool := nuovoStore(t)
	utente := creaUtente(t, pool, "quattordici@esempio.it")
	repo := collegaRepository(t, pool, utente, 777)

	if _, err := pool.Exec(t.Context(), `DELETE FROM repositories WHERE id = $1::uuid`, repo.ID); err != nil {
		t.Fatalf("rimozione del collegamento: %v", err)
	}

	_, err := store.Apply(t.Context(), reposync.Plan{
		RepositoryID: repo.ID, UserID: utente, Commit: commitUno,
	})
	if !errors.Is(err, reposync.ErrAlreadySynced) {
		t.Fatalf("Apply = %v, atteso ErrAlreadySynced: non c'è più niente da riconciliare", err)
	}
}

func TestUnRepositoryNonCollegatoNonRestituisceRighe(t *testing.T) {
	store, _ := nuovoStore(t)
	repositories, err := store.RepositoriesByExternalID(t.Context(), 999)
	if err != nil {
		t.Fatalf("RepositoriesByExternalID: %v", err)
	}
	if len(repositories) != 0 {
		t.Errorf("collegamenti = %d, atteso 0", len(repositories))
	}
}

func TestLoStoreNonSiCostruisceSenzaPool(t *testing.T) {
	if _, err := reposyncpg.New(nil); err == nil {
		t.Error("New(nil) = nil, atteso un errore")
	}
}

// jobCreato è un job del file già ancorato al repository, come lo produce
// [reposync.Reconcile] per una creazione.
func jobCreato(repo reposync.Repository, nome string) jobs.Job {
	job := jobDelFile(nome)
	job.UserID = repo.UserID
	job.RepositoryID = repo.ID
	return job
}

// collegaAltroRepository collega un secondo repository allo stesso utente:
// `repositories_identity_key` è su (utente, provider, owner, nome), quindi il
// nome deve cambiare.
func collegaAltroRepository(t *testing.T, pool *pgxpool.Pool, userID string, externalID int64) reposync.Repository {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO repositories (user_id, installation_id, external_id, owner, name)
		 VALUES ($1::uuid, 4242, $2, 'acme', 'altro') RETURNING id::text`,
		userID, externalID).Scan(&id)
	if err != nil {
		t.Fatalf("collegamento del secondo repository: %v", err)
	}
	return reposync.Repository{ID: id, UserID: userID, Owner: "acme", Name: "altro",
		DefaultBranch: "main", ConfigPath: "cron.yaml", Enabled: true}
}

// inserisciEsecuzione registra un tentativo passato del job: è ciò che
// l'archiviazione deve conservare e che una DELETE porterebbe via per cascata.
func inserisciEsecuzione(t *testing.T, pool *pgxpool.Pool, jobID string) {
	t.Helper()
	quando := time.Now().UTC().Truncate(time.Second)
	if _, err := pool.Exec(t.Context(),
		`SELECT job_executions_ensure_partition($1::date)`, quando); err != nil {
		t.Fatalf("preparazione della partizione: %v", err)
	}
	_, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions
		     (job_id, scheduled_for, environment, attempt, status, triggered_by,
		      started_at, finished_at, response_status)
		 VALUES ($1::uuid, $2, 'production', 1, 'succeeded', 'schedule', $2, $2, 200)`,
		jobID, quando)
	if err != nil {
		t.Fatalf("inserimento dell'esecuzione: %v", err)
	}
}

func contaEsecuzioni(t *testing.T, pool *pgxpool.Pool, jobID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM job_executions WHERE job_id = $1::uuid`, jobID).Scan(&n); err != nil {
		t.Fatalf("conteggio delle esecuzioni: %v", err)
	}
	return n
}
