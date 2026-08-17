package githubhookpg_test

import (
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
	"github.com/apdsoftware/postqron/services/api/internal/githubhookpg"
)

func newStore(t *testing.T) (*githubhookpg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := newTestDatabase(t)
	store, err := githubhookpg.New(pool)
	if err != nil {
		t.Fatalf("costruzione dello store: %v", err)
	}
	return store, pool
}

func consegnaPush(id string) githubhook.Delivery {
	return githubhook.Delivery{
		ID:                   id,
		Event:                githubhook.EventPush,
		InstallationID:       55512345,
		RepositoryExternalID: 987654321,
		RepositoryFullName:   "acme/infra",
		Ref:                  "refs/heads/main",
		HeadCommit:           "2222222222222222222222222222222222222222",
		ReceivedAt:           time.Now().UTC(),
	}
}

// riga legge lo stato registrato per una consegna.
func riga(t *testing.T, pool *pgxpool.Pool, id string) (stato string, tentativi int, motivo *string, concluso *time.Time) {
	t.Helper()
	err := pool.QueryRow(t.Context(),
		`SELECT status::text, attempts, error_message, processed_at
		   FROM github_webhook_deliveries WHERE delivery_id = $1`, id).
		Scan(&stato, &tentativi, &motivo, &concluso)
	if err != nil {
		t.Fatalf("lettura della consegna %q: %v", id, err)
	}
	return stato, tentativi, motivo, concluso
}

func TestClaimRegistraLaConsegnaNuova(t *testing.T) {
	store, pool := newStore(t)

	claimed, err := store.Claim(t.Context(), consegnaPush("consegna-1"))
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !claimed {
		t.Fatal("Claim = false su una consegna nuova")
	}

	stato, tentativi, motivo, concluso := riga(t, pool, "consegna-1")
	if stato != string(githubhook.StatusReceived) {
		t.Errorf("stato = %q, atteso received", stato)
	}
	if tentativi != 1 {
		t.Errorf("attempts = %d, atteso 1", tentativi)
	}
	if motivo != nil {
		t.Errorf("error_message = %q, atteso NULL", *motivo)
	}
	if concluso != nil {
		t.Errorf("processed_at = %v, atteso NULL", *concluso)
	}
}

// TestClaimRifiutaLaConsegnaRipetuta è l'idempotenza di R11 provata sul
// database: è lì che vive, non nel codice applicativo.
func TestClaimRifiutaLaConsegnaRipetuta(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()

	if claimed, err := store.Claim(ctx, consegnaPush("consegna-ripetuta")); err != nil || !claimed {
		t.Fatalf("prima Claim: claimed = %v, err = %v", claimed, err)
	}
	if err := store.Complete(ctx, "consegna-ripetuta", githubhook.StatusProcessed, "", time.Now()); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	claimed, err := store.Claim(ctx, consegnaPush("consegna-ripetuta"))
	if err != nil {
		t.Fatalf("seconda Claim: %v", err)
	}
	if claimed {
		t.Fatal("Claim = true su una consegna già lavorata")
	}

	stato, tentativi, _, concluso := riga(t, pool, "consegna-ripetuta")
	if stato != string(githubhook.StatusProcessed) {
		t.Errorf("stato = %q, atteso processed: la ripetizione ha riaperto la riga", stato)
	}
	if tentativi != 1 {
		t.Errorf("attempts = %d, atteso 1", tentativi)
	}
	if concluso == nil {
		t.Error("processed_at = NULL dopo una lavorazione conclusa")
	}
}

// TestClaimRifiutaLaConsegnaAncoraInLavorazione: due copie della stessa consegna
// arrivate a distanza di un istante non devono lavorare entrambe.
func TestClaimRifiutaLaConsegnaAncoraInLavorazione(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()

	if claimed, err := store.Claim(ctx, consegnaPush("consegna-in-corso")); err != nil || !claimed {
		t.Fatalf("prima Claim: claimed = %v, err = %v", claimed, err)
	}
	claimed, err := store.Claim(ctx, consegnaPush("consegna-in-corso"))
	if err != nil {
		t.Fatalf("seconda Claim: %v", err)
	}
	if claimed {
		t.Fatal("Claim = true su una consegna ancora in lavorazione")
	}
}

// TestClaimRiassegnaLaConsegnaFallita: è il caso per cui GitHub ripete, e il
// modo in cui collauderemo l'endpoint ripetendo a mano dal registro dell'App.
func TestClaimRiassegnaLaConsegnaFallita(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()

	if _, err := store.Claim(ctx, consegnaPush("consegna-fallita")); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.Complete(ctx, "consegna-fallita", githubhook.StatusFailed, "database non raggiungibile", time.Now()); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	stato, _, motivo, _ := riga(t, pool, "consegna-fallita")
	if stato != string(githubhook.StatusFailed) || motivo == nil || *motivo != "database non raggiungibile" {
		t.Fatalf("stato = %q, motivo = %v", stato, motivo)
	}

	claimed, err := store.Claim(ctx, consegnaPush("consegna-fallita"))
	if err != nil {
		t.Fatalf("Claim sulla ripetizione: %v", err)
	}
	if !claimed {
		t.Fatal("Claim = false su una consegna fallita: la ripetizione non la rilavorerebbe mai")
	}

	stato, tentativi, motivo, concluso := riga(t, pool, "consegna-fallita")
	if stato != string(githubhook.StatusReceived) {
		t.Errorf("stato = %q, atteso received", stato)
	}
	if tentativi != 2 {
		t.Errorf("attempts = %d, atteso 2", tentativi)
	}
	if motivo != nil {
		t.Errorf("error_message = %q: il motivo del tentativo precedente è rimasto", *motivo)
	}
	if concluso != nil {
		t.Errorf("processed_at = %v, atteso NULL su una lavorazione riaperta", *concluso)
	}
}

// TestClaimConcorrentiHannoUnSoloVincitore: è la ragione per cui Claim è una
// sola istruzione. Con una SELECT seguita da un INSERT questo test passerebbe a
// volte, che è il modo peggiore di fallire.
func TestClaimConcorrentiHannoUnSoloVincitore(t *testing.T) {
	store, _ := newStore(t)
	ctx := t.Context()

	const copie = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		vincitori int
	)
	pronti := make(chan struct{})

	for range copie {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-pronti
			claimed, err := store.Claim(ctx, consegnaPush("consegna-concorrente"))
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			if claimed {
				mu.Lock()
				vincitori++
				mu.Unlock()
			}
		}()
	}
	close(pronti)
	wg.Wait()

	if vincitori != 1 {
		t.Fatalf("vincitori = %d, atteso 1", vincitori)
	}
}

// TestConsegnaSenzaRepository: `ping` non ha né repository né installazione. Le
// colonne facoltative vanno a NULL, non a zero — i vincoli della 0011 lo
// esigono, e uno zero significherebbe «repository numero zero».
func TestConsegnaSenzaRepository(t *testing.T) {
	store, pool := newStore(t)

	claimed, err := store.Claim(t.Context(), githubhook.Delivery{
		ID:         "consegna-ping",
		Event:      githubhook.EventPing,
		ReceivedAt: time.Now(),
	})
	if err != nil || !claimed {
		t.Fatalf("Claim: claimed = %v, err = %v", claimed, err)
	}

	var installazione, repository *int64
	var nome, ref, commit *string
	err = pool.QueryRow(t.Context(),
		`SELECT installation_id, repository_external_id, repository_full_name, ref, head_commit
		   FROM github_webhook_deliveries WHERE delivery_id = 'consegna-ping'`).
		Scan(&installazione, &repository, &nome, &ref, &commit)
	if err != nil {
		t.Fatalf("lettura della consegna: %v", err)
	}
	if installazione != nil || repository != nil || nome != nil || ref != nil || commit != nil {
		t.Errorf("colonne facoltative valorizzate: %v %v %v %v %v",
			installazione, repository, nome, ref, commit)
	}
}

// TestCommitDiCancellazione: la cancellazione di un ramo arriva con `after` a
// quaranta zeri, che è un valore legale e va conservato.
func TestCommitDiCancellazione(t *testing.T) {
	store, pool := newStore(t)

	consegna := consegnaPush("consegna-cancellazione")
	consegna.HeadCommit = "0000000000000000000000000000000000000000"
	if _, err := store.Claim(t.Context(), consegna); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var commit string
	if err := pool.QueryRow(t.Context(),
		`SELECT head_commit FROM github_webhook_deliveries WHERE delivery_id = $1`,
		consegna.ID).Scan(&commit); err != nil {
		t.Fatalf("lettura del commit: %v", err)
	}
	if commit != consegna.HeadCommit {
		t.Errorf("head_commit = %q", commit)
	}
}

// TestCompleteFallitaSenzaMotivo: il vincolo della 0011 esige un motivo su
// `failed`. Lo store ne mette uno invece di far fallire l'INSERT, perché un
// fallimento senza motivo è comunque un fallimento da registrare.
func TestCompleteFallitaSenzaMotivo(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()

	if _, err := store.Claim(ctx, consegnaPush("consegna-senza-motivo")); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := store.Complete(ctx, "consegna-senza-motivo", githubhook.StatusFailed, "", time.Now()); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	stato, _, motivo, _ := riga(t, pool, "consegna-senza-motivo")
	if stato != string(githubhook.StatusFailed) {
		t.Errorf("stato = %q", stato)
	}
	if motivo == nil || *motivo == "" {
		t.Error("error_message vuoto su uno stato failed")
	}
}

func TestCompleteSuUnaConsegnaInesistente(t *testing.T) {
	store, _ := newStore(t)

	err := store.Complete(t.Context(), "consegna-mai-vista", githubhook.StatusProcessed, "", time.Now())
	if err == nil {
		t.Fatal("Complete su una consegna inesistente non ha prodotto errore")
	}
}

// TestPurgeEliminaLeConsegneVecchie. La retention *è* la finestra in cui la
// deduplicazione vale: dopo la pulizia la stessa consegna risulta nuova, e
// questo test lo dice esplicitamente perché è una proprietà da conoscere, non
// un difetto da scoprire.
func TestPurgeEliminaLeConsegneVecchie(t *testing.T) {
	store, pool := newStore(t)
	ctx := t.Context()

	vecchia := consegnaPush("consegna-vecchia")
	vecchia.ReceivedAt = time.Now().Add(-90 * 24 * time.Hour)
	if _, err := store.Claim(ctx, vecchia); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := store.Claim(ctx, consegnaPush("consegna-recente")); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	rimosse, err := store.Purge(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if rimosse != 1 {
		t.Fatalf("rimosse = %d, attesa 1", rimosse)
	}

	var restanti int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM github_webhook_deliveries`).Scan(&restanti); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if restanti != 1 {
		t.Errorf("restanti = %d, attesa 1", restanti)
	}

	// Dopo la pulizia la consegna vecchia non è più riconosciuta come già vista.
	claimed, err := store.Claim(ctx, vecchia)
	if err != nil {
		t.Fatalf("Claim dopo la pulizia: %v", err)
	}
	if !claimed {
		t.Error("Claim = false su una consegna che la pulizia ha rimosso")
	}
}

func TestNewRifiutaUnPoolNullo(t *testing.T) {
	if _, err := githubhookpg.New(nil); err == nil {
		t.Fatal("lo store si è costruito senza pool")
	}
}

// TestNewServiceSenzaSegreto: nessun segreto configurato, nessun servizio e
// nessun errore. Chi lo chiama non registra la rotta.
func TestNewServiceSenzaSegreto(t *testing.T) {
	svc, err := githubhookpg.NewService(nil, func(string) string { return "" }, nil, nil)
	if err != nil {
		t.Fatalf("err = %v, atteso nessun errore", err)
	}
	if svc != nil {
		t.Fatal("servizio costruito senza segreto del webhook")
	}
}

func TestNewServiceComponeIlServizio(t *testing.T) {
	_, pool := newStore(t)

	ambiente := map[string]string{githubhook.SecretEnvVar: "segreto-del-webhook-di-prova"}
	svc, err := githubhookpg.NewService(pool, func(k string) string { return ambiente[k] }, nil, nil)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc == nil {
		t.Fatal("servizio nullo con il segreto configurato")
	}
	if svc.HasSink() {
		t.Error("HasSink = true senza consumatore")
	}

	// Il servizio composto qui è quello che risponderà in esercizio: una push
	// firmata arriva fino alla riga registrata.
	corpo := []byte(`{"ref":"refs/heads/main","after":"2222222222222222222222222222222222222222",
	                  "repository":{"id":42,"full_name":"acme/infra","default_branch":"main"},
	                  "installation":{"id":7}}`)
	res, err := svc.Receive(t.Context(), githubhook.Request{
		Signature: githubhook.Sign([]byte(ambiente[githubhook.SecretEnvVar]), corpo),
		Event:     githubhook.EventPush,
		Delivery:  "consegna-composta",
		Body:      corpo,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if res.Outcome != githubhook.OutcomeIgnored {
		t.Errorf("outcome = %q, atteso ignored senza consumatore", res.Outcome)
	}

	stato, _, _, _ := riga(t, pool, "consegna-composta")
	if stato != string(githubhook.StatusIgnored) {
		t.Errorf("stato = %q, atteso ignored", stato)
	}
}
