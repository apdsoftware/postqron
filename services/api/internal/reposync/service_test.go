package reposync_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/reposync"
)

func TestUnPushCreaIJobDelFile(t *testing.T) {
	store := nuovoArchivio()
	repo := store.collega(collegamento())
	contenuto := nuovoRepository(fileConJob("digest", "healthcheck"))

	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	if store.quantiJob() != 2 {
		t.Fatalf("job creati = %d, attesi 2", store.quantiJob())
	}
	creato, ok := store.perNome("digest")
	if !ok {
		t.Fatal("il job `digest` non è stato creato")
	}
	if creato.RepositoryID != repo.ID {
		t.Errorf("RepositoryID = %q, atteso %q: un job che nasce dal file è gestito", creato.RepositoryID, repo.ID)
	}
	if !creato.Managed() {
		t.Error("il job creato dal sync deve risultare gestito (jobs.Job.Managed)")
	}
}

// ---------------------------------------------------------------------------
// I tre casi che fanno danno.
// ---------------------------------------------------------------------------

// Uno. Il job sparito dal file perde la schedulazione e tiene il proprio
// storico: viene archiviato, non cancellato.
func TestIlJobSparitoDalFileVieneDisattivatoENonRimosso(t *testing.T) {
	store := nuovoArchivio()
	repo := store.collega(collegamento())
	esistente := store.inserisci(jobEsistenteIn(repo.ID, "vecchio"))

	contenuto := nuovoRepository(fileConJob("nuovo"))
	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	dopo, ancoraCE := store.perNome("vecchio")
	if !ancoraCE {
		t.Fatal("il job è stato rimosso: lo storico delle sue esecuzioni sarebbe sparito con lui (cascata della 0006)")
	}
	if dopo.ID != esistente.ID {
		t.Errorf("ID = %q, atteso %q: deve essere la stessa riga", dopo.ID, esistente.ID)
	}
	if !dopo.Archived() {
		t.Error("il job sparito dal file è ancora attivo")
	}
	if dopo.Runnable() {
		t.Error("un job archiviato non deve più produrre occorrenze")
	}
	if _, creato := store.perNome("nuovo"); !creato {
		t.Error("il job nuovo del file non è stato creato")
	}
}

// Due. La pausa manuale sopravvive al sync.
func TestUnPushCheNonToccaUnJobInPausaNonLoRiattiva(t *testing.T) {
	store := nuovoArchivio()
	repo := store.collega(collegamento())

	inPausa := jobEsistenteIn(repo.ID, "digest")
	inPausa.Enabled = false
	store.inserisci(inPausa)

	// Il file contiene lo stesso job, più uno nuovo: il push cambia qualcosa,
	// ma non quel job.
	contenuto := nuovoRepository(fileConJob("digest", "healthcheck"))
	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	dopo, _ := store.perNome("digest")
	if dopo.Enabled {
		t.Error("il sync ha riattivato un job che l'utente aveva messo in pausa dalla dashboard")
	}
	if dopo.Archived() {
		t.Error("il job è ancora nel file: non va archiviato")
	}
	if _, creato := store.perNome("healthcheck"); !creato {
		t.Error("il resto del sync deve essere avvenuto lo stesso")
	}
}

// Tre. Un file non valido non tocca lo stato (R13).
func TestUnFileNonValidoNonModificaLoStatoEsistente(t *testing.T) {
	store := nuovoArchivio()
	repo := store.collega(collegamento())
	prima := store.inserisci(jobEsistenteIn(repo.ID, "digest"))

	// `schedule` ed `every` insieme sono mutuamente esclusivi (SPEC §9), e il
	// secondo job non esiste proprio: due errori, un solo rifiuto.
	contenuto := nuovoRepository(`version: 1
jobs:
  - name: digest
    schedule: "0 9 * * *"
    every: 10s
    request: { url: https://esempio.it/digest, method: GET }
  - name: rotto
    request: { url: non-un-url }
`)

	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}

	// La consegna **non** fallisce: ripeterla non renderebbe valido il file.
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush = %v, atteso nil: un file non valido è un problema dell'utente, non una consegna da ripetere", err)
	}

	if len(store.applicazioni) != 0 {
		t.Fatalf("applicazioni = %d, attese 0: un file non valido non deve produrre nessuna scrittura sui job",
			len(store.applicazioni))
	}
	dopo, ok := store.perNome("digest")
	if !ok {
		t.Fatal("il job esistente è sparito")
	}
	if !reflect.DeepEqual(dopo, prima) {
		t.Errorf("il job è cambiato:\n prima = %+v\n dopo  = %+v", prima, dopo)
	}
	if store.quantiJob() != 1 {
		t.Errorf("job = %d, atteso 1: nessun job del file rotto deve essere stato creato", store.quantiJob())
	}

	if len(store.fallimenti) != 1 {
		t.Fatalf("fallimenti registrati = %d, atteso 1", len(store.fallimenti))
	}
	registrato := store.fallimenti[0]
	if registrato.commit != commitUno {
		t.Errorf("commit registrato = %q, atteso %q", registrato.commit, commitUno)
	}
	// Il messaggio è ciò che l'utente legge: senza riga e colonna non ha modo
	// di correggere il file.
	if !strings.Contains(registrato.reason, "cron.yaml:") {
		t.Errorf("il motivo registrato non porta la posizione degli errori:\n%s", registrato.reason)
	}
}

// ---------------------------------------------------------------------------

func TestDuePushIdenticheProduconoUnSoloEffetto(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())
	contenuto := nuovoRepository(fileConJob("digest"))

	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}

	// Due consegne **diverse** che portano lo stesso commit: la
	// deduplicazione per identificativo di #421 non le ferma.
	primo := push(commitUno)
	secondo := push(commitUno)
	secondo.Delivery = "un-altra-consegna"

	for _, evento := range []githubhook.PushEvent{primo, secondo} {
		if err := svc.HandlePush(t.Context(), evento); err != nil {
			t.Fatalf("HandlePush: %v", err)
		}
	}

	if len(store.applicazioni) != 1 {
		t.Errorf("applicazioni = %d, attesa 1: il secondo push sullo stesso commit non ha niente da fare",
			len(store.applicazioni))
	}
	if contenuto.letture != 1 {
		t.Errorf("letture del file = %d, attesa 1: la seconda consegna si ferma prima di scaricare", contenuto.letture)
	}
	if store.quantiJob() != 1 {
		t.Errorf("job = %d, atteso 1: nessun duplicato", store.quantiJob())
	}
}

func TestUnPushSuUnCommitNuovoConUnFileIdenticoNonRiscriveNiente(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())
	contenuto := nuovoRepository(fileConJob("digest"))

	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("primo HandlePush: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitDue)); err != nil {
		t.Fatalf("secondo HandlePush: %v", err)
	}

	if len(store.applicazioni) != 2 {
		t.Fatalf("applicazioni = %d, attese 2", len(store.applicazioni))
	}
	// Il secondo commit tocca altri file del repository: `cron.yaml` è lo
	// stesso, e il piano risultante non deve contenere nessuna scrittura.
	if !store.applicazioni[1].Empty() {
		t.Errorf("il secondo piano contiene %d modifiche, atteso vuoto", len(store.applicazioni[1].Changes))
	}
	if store.applicazioni[1].Unchanged != 1 {
		t.Errorf("Unchanged = %d, atteso 1", store.applicazioni[1].Unchanged)
	}
}

// Il limite di piano vale anche qui: un file con più job di quanti il piano ne
// consenta viene rifiutato **per intero**, dicendo quale piano lo consente.
func TestUnFileOltreIlTettoDiPianoVieneRifiutatoPerIntero(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())

	nomi := make([]string, 0, 30)
	for i := range 30 {
		nomi = append(nomi, "job-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	contenuto := nuovoRepository(fileConJob(nomi...))

	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush = %v, atteso nil", err)
	}

	if store.quantiJob() != 0 {
		t.Fatalf("job creati = %d, attesi 0: venti applicati e dieci ignorati sarebbe la risposta sbagliata",
			store.quantiJob())
	}
	if len(store.fallimenti) != 1 {
		t.Fatalf("fallimenti = %d, atteso 1", len(store.fallimenti))
	}
	motivo := store.fallimenti[0].reason
	if !strings.Contains(motivo, "Free") {
		t.Errorf("il motivo non dice quale piano ha rifiutato:\n%s", motivo)
	}
	if !strings.Contains(motivo, "20") {
		t.Errorf("il motivo non dice quanti job il piano consente:\n%s", motivo)
	}
}

// Il tetto è sull'utente, non sul file: i job che l'utente ha fuori da questo
// repository contano.
func TestIJobFuoriDalRepositoryContanoNelTettoDiPiano(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())
	store.fuori = 19

	contenuto := nuovoRepository(fileConJob("uno", "due"))
	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	if store.quantiJob() != 0 {
		t.Errorf("job creati = %d, attesi 0: 19 fuori più 2 nel file sfondano il tetto di 20", store.quantiJob())
	}
	if len(store.fallimenti) != 1 {
		t.Fatalf("fallimenti = %d, atteso 1", len(store.fallimenti))
	}
	if motivo := store.fallimenti[0].reason; !strings.Contains(motivo, "fuori da questo file") {
		t.Errorf("il motivo non spiega che il tetto conta anche i job fuori dal file:\n%s", motivo)
	}
}

func TestUnFileAssenteNonArchiviaNiente(t *testing.T) {
	store := nuovoArchivio()
	repo := store.collega(collegamento())
	store.inserisci(jobEsistenteIn(repo.ID, "digest"))

	contenuto := nuovoRepository("")
	contenuto.assente = true

	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush = %v, atteso nil", err)
	}

	dopo, _ := store.perNome("digest")
	if dopo.Archived() {
		t.Error("un `cron.yaml` cancellato non è una richiesta di disattivare i job: `jobs: []` lo è")
	}
	if len(store.applicazioni) != 0 {
		t.Errorf("applicazioni = %d, attese 0", len(store.applicazioni))
	}
	if len(store.fallimenti) != 1 {
		t.Fatalf("fallimenti = %d, atteso 1: l'utente deve poter vedere perché il sync non è avvenuto",
			len(store.fallimenti))
	}
	if motivo := store.fallimenti[0].reason; !strings.Contains(motivo, "jobs: []") {
		t.Errorf("il motivo non dice come ottenere davvero la disattivazione:\n%s", motivo)
	}
}

// Un elenco vuoto invece sì: è una richiesta scritta nel file, visibile nella
// pull request che la introduce.
func TestUnElencoDiJobVuotoArchiviaTutto(t *testing.T) {
	store := nuovoArchivio()
	repo := store.collega(collegamento())
	store.inserisci(jobEsistenteIn(repo.ID, "digest"))

	contenuto := nuovoRepository("version: 1\njobs: []\n")
	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	dopo, ancoraCE := store.perNome("digest")
	if !ancoraCE {
		t.Fatal("il job è stato cancellato invece che archiviato")
	}
	if !dopo.Archived() {
		t.Error("`jobs: []` è una richiesta esplicita di disattivare tutto")
	}
}

func TestUnaPushSuUnRamoDiversoNonSincronizza(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())
	contenuto := nuovoRepository(fileConJob("digest"))

	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}

	evento := push(commitUno)
	evento.Ref = "refs/heads/feature/riscrittura"
	if err := svc.HandlePush(t.Context(), evento); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	if store.quantiJob() != 0 {
		t.Error("una push su un ramo di lavoro non deve schedulare niente")
	}
	if len(store.fallimenti) != 0 {
		t.Error("non è un fallimento da mostrare in dashboard: è una push che non ci riguarda")
	}
}

func TestLaCancellazioneDiUnRamoNonArchiviaNiente(t *testing.T) {
	store := nuovoArchivio()
	repo := store.collega(collegamento())
	store.inserisci(jobEsistenteIn(repo.ID, "digest"))

	svc, err := servizio(store, nuovoRepository(fileConJob("digest")), jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}

	evento := push(commitUno)
	evento.Deleted = true
	evento.After = "0000000000000000000000000000000000000000"
	if err := svc.HandlePush(t.Context(), evento); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	dopo, _ := store.perNome("digest")
	if dopo.Archived() {
		t.Error("cancellare il ramo collegato è di solito pulizia, non una richiesta di spegnere i job")
	}
}

func TestUnCollegamentoDisattivatoNonSincronizza(t *testing.T) {
	store := nuovoArchivio()
	spento := collegamento()
	spento.Enabled = false
	store.collega(spento)

	svc, err := servizio(store, nuovoRepository(fileConJob("digest")), jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}
	if store.quantiJob() != 0 {
		t.Error("un collegamento disattivato non sincronizza")
	}
}

// Due clienti che collegano lo stesso repository pubblico sono due righe con
// due piani: il fallimento di uno non ferma l'altro.
func TestDueCollegamentiDelloStessoRepositorySonoRiconciliatiEntrambi(t *testing.T) {
	store := nuovoArchivio()
	primo := collegamento()
	secondo := collegamento()
	secondo.UserID = "utente-2"
	store.collega(primo)
	store.collega(secondo)

	svc, err := servizio(store, nuovoRepository(fileConJob("digest")), jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	if store.quantiJob() != 2 {
		t.Errorf("job = %d, attesi 2: un job per collegamento", store.quantiJob())
	}
}

// Un guasto **sì** deve far fallire la consegna: è l'unico caso in cui la
// ripetizione di GitHub serve a qualcosa.
func TestUnGuastoDiGitHubFaFallireLaConsegna(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())

	contenuto := nuovoRepository("")
	contenuto.err = errFinto

	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err == nil {
		t.Fatal("HandlePush = nil: un guasto di GitHub deve far ripetere la consegna")
	}
	if len(store.fallimenti) != 0 {
		t.Error("un guasto nostro non va registrato come file rifiutato: non è colpa dell'utente")
	}
}

func TestUnGuastoDelDatabaseFaFallireLaConsegna(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())
	store.falliSu["Apply"] = errFinto

	svc, err := servizio(store, nuovoRepository(fileConJob("digest")), jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err == nil {
		t.Fatal("HandlePush = nil: un guasto della persistenza deve risalire")
	}
}

// Se non riusciamo nemmeno a dire che il file è stato rifiutato, quello che
// resta è un push apparentemente riuscito con dei job invariati.
func TestSeIlRifiutoNonSiRiesceARegistrareLaConsegnaFallisce(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())
	store.falliSu["RecordFailure"] = errFinto

	svc, err := servizio(store, nuovoRepository("questo non è yaml valido: ["), jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err == nil {
		t.Fatal("HandlePush = nil: un rifiuto che l'utente non può leggere è un sync silenziosamente perso")
	}
}

func TestUnRepositoryNonCollegatoVieneIgnorato(t *testing.T) {
	store := nuovoArchivio()
	svc, err := servizio(store, nuovoRepository(fileConJob("digest")), jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush = %v, atteso nil: è il caso normale fra installazione e collegamento", err)
	}
}

// I `${VAR}` del file sono verificati contro i segreti del workspace al sync,
// non all'esecuzione: è ciò che evita di scoprire alle tre di notte che il
// riferimento non esisteva.
func TestUnSegretoInesistenteFaRifiutareIlFile(t *testing.T) {
	store := nuovoArchivio()
	store.collega(collegamento())

	contenuto := nuovoRepository(`version: 1
jobs:
  - name: digest
    every: 5m
    request:
      url: https://esempio.it/digest
      method: POST
      headers: { Authorization: "Bearer ${TOKEN_CHE_NON_ESISTE}" }
`)

	svc, err := servizio(store, contenuto, jobs.FreePlan, []string{"ALTRO_TOKEN"})
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}

	if store.quantiJob() != 0 {
		t.Error("un file con un segreto inesistente non deve creare niente")
	}
	if len(store.fallimenti) != 1 {
		t.Fatalf("fallimenti = %d, atteso 1", len(store.fallimenti))
	}
}

func TestIlSinkUsaLInstallazioneDelCollegamentoQuandoCE(t *testing.T) {
	store := nuovoArchivio()
	repo := collegamento()
	repo.InstallationID = 99
	store.collega(repo)

	contenuto := nuovoRepository(fileConJob("digest"))
	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}

	evento := push(commitUno)
	evento.InstallationID = 4242
	if err := svc.HandlePush(t.Context(), evento); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}
	if contenuto.ultimoID != 99 {
		t.Errorf("installazione usata = %d, attesa 99 (quella del collegamento)", contenuto.ultimoID)
	}
}

func TestSenzaInstallazioneSulCollegamentoSiUsaQuellaDellaPush(t *testing.T) {
	store := nuovoArchivio()
	repo := collegamento()
	repo.InstallationID = 0
	store.collega(repo)

	contenuto := nuovoRepository(fileConJob("digest"))
	svc, err := servizio(store, contenuto, jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	if err := svc.HandlePush(t.Context(), push(commitUno)); err != nil {
		t.Fatalf("HandlePush: %v", err)
	}
	if contenuto.ultimoID != 4242 {
		t.Errorf("installazione usata = %d, attesa 4242 (quella della push)", contenuto.ultimoID)
	}
}

func TestIlServizioNonSiCostruisceSenzaLeDipendenzeObbligatorie(t *testing.T) {
	completo := reposync.Options{
		Store:    nuovoArchivio(),
		Plans:    listino{piano: jobs.FreePlan},
		Secrets:  segreti{},
		Contents: nuovoRepository(""),
	}

	casi := map[string]func(*reposync.Options){
		"senza store":    func(o *reposync.Options) { o.Store = nil },
		"senza piani":    func(o *reposync.Options) { o.Plans = nil },
		"senza segreti":  func(o *reposync.Options) { o.Secrets = nil },
		"senza contenut": func(o *reposync.Options) { o.Contents = nil },
	}
	for nome, togli := range casi {
		t.Run(nome, func(t *testing.T) {
			opts := completo
			togli(&opts)
			if _, err := reposync.NewService(opts); err == nil {
				t.Error("NewService = nil, atteso un errore")
			}
		})
	}
}

func TestUnServizioValidoEUnPushSink(t *testing.T) {
	svc, err := servizio(nuovoArchivio(), nuovoRepository(""), jobs.FreePlan, nil)
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	var sink githubhook.PushSink = svc
	if sink == nil {
		t.Fatal("il servizio deve implementare githubhook.PushSink: è il confine dichiarato da #421")
	}
}

func TestValidCommit(t *testing.T) {
	casi := map[string]bool{
		commitUno: true,
		"0000000000000000000000000000000000000000": true,
		"abc1234": true,
		"abc123":  false,
		"":        false,
		"ABC1234ABC1234ABC1234ABC1234ABC1234ABC12": false,
		"non-un-commit": false,
	}
	for valore, atteso := range casi {
		if got := reposync.ValidCommit(valore); got != atteso {
			t.Errorf("ValidCommit(%q) = %v, atteso %v", valore, got, atteso)
		}
	}
}

// jobEsistenteIn è un job già presente nel repository indicato.
func jobEsistenteIn(repositoryID, nome string) jobs.Job {
	job := jobs.NewJob()
	job.Name = nome
	job.Every = 5 * time.Minute
	job.URL = "https://esempio.it/" + nome
	job.Method = jobs.MethodGET
	job.UserID = "utente-1"
	job.RepositoryID = repositoryID
	return job
}
