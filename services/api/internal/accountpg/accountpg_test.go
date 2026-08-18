package accountpg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/account"
)

// TestLaRichiestaFermaTuttoEInUnaVoltaSola verifica la frase «we stop execution
// and revoke keys immediately» della privacy policy §5, voce per voce.
//
// I conteggi sono sul database e non sulla ricevuta: la ricevuta dice quello che
// lo store crede di aver fatto, e quello che conta è cosa c'è in tabella.
func TestLaRichiestaFermaTuttoEInUnaVoltaSola(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "eta", 7)

	receipt, err := store.RequestDeletion(t.Context(), te.UserID, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("richiesta: %v", err)
	}

	if n := count(t, pool,
		`SELECT count(*) FROM jobs WHERE user_id = $1::uuid AND (enabled OR next_run_at IS NOT NULL)`,
		te.UserID); n != 0 {
		t.Errorf("%d job dell'account sono ancora accesi o con una prossima occorrenza", n)
	}
	if n := count(t, pool,
		`SELECT count(*) FROM jobs WHERE user_id = $1::uuid AND suspended_reason = 'account_deletion'`,
		te.UserID); n != receipt.JobsStopped {
		t.Errorf("job marcati come fermati dalla cancellazione = %d, ricevuta = %d", n, receipt.JobsStopped)
	}

	if n := count(t, pool,
		`SELECT count(*) FROM api_keys WHERE user_id = $1::uuid AND revoked_at IS NULL`, te.UserID); n != 0 {
		t.Errorf("%d chiavi API non revocate", n)
	}
	if n := count(t, pool,
		`SELECT count(*) FROM sessions WHERE user_id = $1::uuid AND revoked_at IS NULL`, te.UserID); n != 0 {
		t.Errorf("%d sessioni ancora aperte", n)
	}
	if n := count(t, pool,
		`SELECT count(*) FROM user_tokens WHERE user_id = $1::uuid AND consumed_at IS NULL`, te.UserID); n != 0 {
		t.Errorf("%d token monouso ancora validi", n)
	}

	// La revoca dei segreti **svuota il materiale cifrato**: è la promessa più
	// forte delle due, e il vincolo della 0012 non permette di separarla dalla
	// data. Un account cancellato che lasciasse in giro testo cifrato sarebbe un
	// account cancellato male.
	if n := count(t, pool,
		`SELECT count(*) FROM workspace_secrets
		  WHERE user_id = $1::uuid AND (revoked_at IS NULL OR octet_length(ciphertext) > 0)`,
		te.UserID); n != 0 {
		t.Errorf("%d segreti del workspace non revocati o con il valore ancora dentro", n)
	}
	if n := count(t, pool,
		`SELECT count(*) FROM ai_credentials
		  WHERE user_id = $1::uuid AND (revoked_at IS NULL OR octet_length(ciphertext) > 0)`,
		te.UserID); n != 0 {
		t.Errorf("%d chiavi AI non revocate o con il materiale ancora dentro", n)
	}
}

// TestUnJobInVoloNonContinua è il caso «cancellare al momento sbagliato»: un job
// la cui prossima occorrenza è già dovuta, cioè uno che il motore prenderebbe
// alla passata successiva.
//
// La verifica è fatta con **la query calda del dispatch**, riscritta qui uguale
// a quella di internal/scheduler: verificare le colonne una per una direbbe che
// le abbiamo scritte, non che il motore smette di vedere il job. Sono due cose
// diverse, e la seconda è quella che conta — la privacy policy promette che le
// esecuzioni si fermano, non che una colonna cambia valore.
func TestUnJobInVoloNonContinua(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "theta", 8)

	const dovuti = `
		SELECT count(*) FROM jobs
		 WHERE enabled
		   AND archived_at IS NULL
		   AND next_run_at IS NOT NULL
		   AND next_run_at <= now()
		   AND user_id = $1::uuid`

	if n := count(t, pool, dovuti, te.UserID); n == 0 {
		t.Fatal("il seme non ha prodotto nessun job dovuto: il test non proverebbe niente")
	}

	requestNow(t, store, te.UserID, time.Hour)

	if n := count(t, pool, dovuti, te.UserID); n != 0 {
		t.Errorf("%d job dell'account sono ancora dovuti dopo la richiesta di cancellazione", n)
	}

	// E non ricompaiono dall'altra parte: `jobs_unscheduled_idx` (0010) serve la
	// query con cui lo scheduler cerca i job accesi senza prossima occorrenza, e
	// un job spento non deve finirci dentro.
	if n := count(t, pool,
		`SELECT count(*) FROM jobs
		  WHERE enabled AND archived_at IS NULL AND next_run_at IS NULL AND user_id = $1::uuid`,
		te.UserID); n != 0 {
		t.Errorf("%d job dell'account aspettano ancora una prima occorrenza", n)
	}
}

// TestLAnnullamentoRiaccendeSoloIJobCheAvevamoFermatoNoi è la ragione per cui la
// 0017 aggiunge un valore all'enum invece di limitarsi a spegnere i job.
//
// Tre job: uno acceso, uno che l'utente aveva messo in pausa da sé, uno sospeso
// da un cambio di piano (R58). Dopo un giro completo di richiesta e
// annullamento, solo il primo deve essere tornato acceso — riaccendere gli altri
// due significherebbe far ripartire chiamate verso bersagli che nessuno voleva
// far ripartire.
func TestLAnnullamentoRiaccendeSoloIJobCheAvevamoFermatoNoi(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "iota", 9)

	exec(t, pool,
		`INSERT INTO jobs (user_id, name, every_seconds, url, enabled)
		 VALUES ($1::uuid, 'in-pausa', 300, 'https://pausa.example/hook', false)`, te.UserID)
	exec(t, pool,
		`INSERT INTO jobs (user_id, name, every_seconds, url, enabled, suspended_at, suspended_reason)
		 VALUES ($1::uuid, 'sospeso-dal-piano', 300, 'https://piano.example/hook', false,
		         now(), 'plan_job_limit')`, te.UserID)

	requestNow(t, store, te.UserID, time.Hour)

	restored, err := store.CancelDeletion(t.Context(), te.UserID)
	if err != nil {
		t.Fatalf("annullamento: %v", err)
	}
	if restored.JobsResumed != 1 {
		t.Errorf("job riaccesi = %d, atteso 1", restored.JobsResumed)
	}

	for _, tc := range []struct {
		name    string
		enabled bool
		reason  string
	}{
		{"job-iota", true, ""},
		{"in-pausa", false, ""},
		{"sospeso-dal-piano", false, "plan_job_limit"},
	} {
		var enabled bool
		var reason *string
		if err := pool.QueryRow(t.Context(),
			`SELECT enabled, suspended_reason::text FROM jobs WHERE user_id = $1::uuid AND name = $2`,
			te.UserID, tc.name).Scan(&enabled, &reason); err != nil {
			t.Fatalf("lettura del job %q: %v", tc.name, err)
		}
		got := ""
		if reason != nil {
			got = *reason
		}
		if enabled != tc.enabled || got != tc.reason {
			t.Errorf("job %q: acceso=%v motivo=%q, atteso acceso=%v motivo=%q",
				tc.name, enabled, got, tc.enabled, tc.reason)
		}
	}

	// La finestra è chiusa: l'account non è più in coda per la purga.
	due, err := store.DueForPurge(t.Context(), time.Now().AddDate(1, 0, 0), 10)
	if err != nil {
		t.Fatalf("ricerca degli account scaduti: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("dopo l'annullamento l'account è ancora in coda per la purga: %v", due)
	}
}

// TestLeChiaviRevocateNonTornanoConLAnnullamento fissa una promessa
// irreversibile: la privacy policy dice «revoke keys **immediately**», non «le
// sospendiamo per trenta giorni».
//
// Il vincolo della 0012 la rende irreversibile per costruzione — il testo
// cifrato non c'è più — e questo test lo verifica dall'esterno, perché è ciò che
// l'utente vede: torna indietro e ritrova i suoi job, non le sue chiavi.
func TestLeChiaviRevocateNonTornanoConLAnnullamento(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "kappa", 10)

	requestNow(t, store, te.UserID, time.Hour)
	if _, err := store.CancelDeletion(t.Context(), te.UserID); err != nil {
		t.Fatalf("annullamento: %v", err)
	}

	if n := count(t, pool,
		`SELECT count(*) FROM api_keys WHERE user_id = $1::uuid AND revoked_at IS NULL`, te.UserID); n != 0 {
		t.Errorf("%d chiavi API sono tornate valide dopo l'annullamento", n)
	}
	if n := count(t, pool,
		`SELECT count(*) FROM workspace_secrets
		  WHERE user_id = $1::uuid AND (revoked_at IS NULL OR octet_length(ciphertext) > 0)`,
		te.UserID); n != 0 {
		t.Errorf("%d segreti sono tornati utilizzabili dopo l'annullamento", n)
	}
}

// TestLaRichiestaRipetutaNonSpostaLaScadenza: ripetere la richiesta rimanderebbe
// una cancellazione che l'utente aveva già chiesto, e quella è una decisione che
// non deve prendere un doppio clic.
func TestLaRichiestaRipetutaNonSpostaLaScadenza(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "lambda", 11)

	primo := time.Now()
	if _, err := store.RequestDeletion(t.Context(), te.UserID, primo, primo.Add(time.Hour)); err != nil {
		t.Fatalf("prima richiesta: %v", err)
	}

	secondo := primo.Add(24 * time.Hour)
	_, err := store.RequestDeletion(t.Context(), te.UserID, secondo, secondo.Add(time.Hour))
	if !errors.Is(err, account.ErrAlreadyRequested) {
		t.Fatalf("seconda richiesta: errore = %v, atteso ErrAlreadyRequested", err)
	}

	status, err := store.Status(t.Context(), te.UserID)
	if err != nil {
		t.Fatalf("stato: %v", err)
	}
	// Il confronto ha una tolleranza perché `timestamptz` arrotonda al
	// microsecondo, non perché la scadenza possa spostarsi: ventiquattro ore di
	// scarto — quelle della seconda richiesta — non passerebbero.
	if scarto := status.PurgeAfter.Sub(primo.Add(time.Hour)); scarto > time.Millisecond || scarto < -time.Millisecond {
		t.Errorf("scadenza = %v, attesa quella della prima richiesta %v (scarto %v)",
			status.PurgeAfter, primo.Add(time.Hour), scarto)
	}
}

// TestLAnnullamentoSenzaRichiestaNonFaNiente distingue i due errori: l'account
// che non c'è e quello che non aveva chiesto niente.
func TestLAnnullamentoSenzaRichiestaNonFaNiente(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "mu", 12)

	if _, err := store.CancelDeletion(t.Context(), te.UserID); !errors.Is(err, account.ErrNotRequested) {
		t.Errorf("errore = %v, atteso ErrNotRequested", err)
	}
	if _, err := store.CancelDeletion(t.Context(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("errore su un account inesistente = %v, atteso ErrNotFound", err)
	}
	if _, err := store.Status(t.Context(), "non-un-uuid"); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("errore su un identificativo malformato = %v, atteso ErrNotFound", err)
	}
}

// TestLoStatoRiconosceLAbbonamentoAPagamento verifica la condizione da cui
// dipende la presa d'atto: «c'è qualcosa che Paddle continuerà a fatturargli
// dopo che questo account non esisterà più».
//
// Il piano Free non passa da Paddle (0003) e non deve far comparire nessun
// avviso: un rifiuto che chiedesse di annullare un abbonamento inesistente
// sarebbe un ostacolo inventato.
func TestLoStatoRiconosceLAbbonamentoAPagamento(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "nu", 13)

	status, err := store.Status(t.Context(), te.UserID)
	if err != nil {
		t.Fatalf("stato: %v", err)
	}
	if !status.Subscription.Paid {
		t.Fatal("abbonamento a pagamento non riconosciuto")
	}
	if status.Subscription.PlanCode != "pro" || status.Subscription.PaddleSubscriptionID != te.PaddleSubscriptionID {
		t.Errorf("abbonamento = %+v, atteso pro/%s", status.Subscription, te.PaddleSubscriptionID)
	}

	// Annullata la sottoscrizione, non c'è più niente da fatturare.
	exec(t, pool,
		`UPDATE subscriptions SET status = 'canceled', canceled_at = now() WHERE user_id = $1::uuid`,
		te.UserID)
	status, err = store.Status(t.Context(), te.UserID)
	if err != nil {
		t.Fatalf("stato: %v", err)
	}
	if status.Subscription.Paid {
		t.Errorf("abbonamento annullato ancora considerato a pagamento: %+v", status.Subscription)
	}

	// E il piano d'ingresso, che una riga in `subscriptions` non ce l'ha affatto
	// (R59), nemmeno.
	altro := seedTenant(t, pool, "xi", 14)
	exec(t, pool, `DELETE FROM subscriptions WHERE user_id = $1::uuid`, altro.UserID)
	status, err = store.Status(t.Context(), altro.UserID)
	if err != nil {
		t.Fatalf("stato: %v", err)
	}
	if status.Subscription.Paid {
		t.Errorf("account senza sottoscrizione considerato a pagamento: %+v", status.Subscription)
	}
}
