package accountpg_test

import (
	"testing"
)

// Questo file copre le due tabelle che un `user_id` non ce l'hanno, e che sono
// quindi le uniche in cui una purga può portare via righe di **altri**.
//
// Le altre tabelle sono difese dalla forma della condizione: `user_id = $1`
// non può prendere la riga di qualcun altro. Qui la strada per arrivare
// all'utente passa da un identificativo che può essere condiviso, ed è dove una
// cancellazione a cascata scritta male fa il danno che nessuno vede — finché
// l'altro utente non chiede perché un sync non è mai partito.

// TestLeConsegneDiUnRepositorySeguitoDaDueUtentiRestano è il caso che la 0004
// dichiara esplicitamente possibile: «utenti diversi possono collegare lo stesso
// repository pubblico».
//
// Due account seguono lo stesso repository, ciascuno con la propria
// installazione della GitHub App. Purgato il primo, la consegna che riguarda
// quel repository **non** si tocca: appartiene anche al secondo, e il registro
// di deduplicazione su cui poggia l'idempotenza di R11 è suo quanto del
// cancellato.
//
// La direzione dell'errore è deliberata e va detta: si conserva per altri trenta
// giorni una riga che contiene il nome di un repository — la porta via la
// retention della 0011 — invece di rompere la deduplicazione di un utente che
// non c'entra.
func TestLeConsegneDiUnRepositorySeguitoDaDueUtentiRestano(t *testing.T) {
	store, pool := newStore(t)
	vittima := seedTenant(t, pool, "omicron", 15)
	altro := seedTenant(t, pool, "pi", 16)

	// I due collegano lo stesso repository **attraverso la stessa
	// installazione**: è il caso di una GitHub App installata su
	// un'organizzazione a cui appartengono due nostri clienti. È la forma che
	// l'indice unico della 0004 permette, perché l'unicità è per utente — ed è la
	// sola in cui l'installazione non basta a distinguere di chi sia una
	// consegna.
	const (
		repoCondiviso         = int64(777777)
		installazioneComune   = int64(555555)
		consegnaDellOrganismo = "delivery-organizzazione-01"
	)
	exec(t, pool,
		`UPDATE repositories SET external_id = $2, installation_id = $3 WHERE user_id = $1::uuid`,
		vittima.UserID, repoCondiviso, installazioneComune)
	exec(t, pool,
		`UPDATE repositories SET external_id = $2, installation_id = $3 WHERE user_id = $1::uuid`,
		altro.UserID, repoCondiviso, installazioneComune)

	// Una consegna sola, come la manda GitHub: un'installazione, un evento. È di
	// tutti e due, e non c'è modo di dire che sia solo del cancellato.
	exec(t, pool,
		`DELETE FROM github_webhook_deliveries WHERE delivery_id IN ($1, $2)`,
		vittima.GitHubDeliveryID, altro.GitHubDeliveryID)
	exec(t, pool,
		`INSERT INTO github_webhook_deliveries (delivery_id, event, status, installation_id,
		                                        repository_external_id, repository_full_name)
		 VALUES ($1, 'push', 'processed', $2, $3, 'org-condivisa/repo')`,
		consegnaDellOrganismo, installazioneComune, repoCondiviso)

	requestNow(t, store, vittima.UserID, 0)
	if _, err := store.Purge(t.Context(), vittima.UserID); err != nil {
		t.Fatalf("purga: %v", err)
	}

	if n := count(t, pool,
		`SELECT count(*) FROM github_webhook_deliveries WHERE delivery_id = $1`,
		consegnaDellOrganismo); n != 1 {
		t.Error("la consegna di un repository seguito anche da un altro utente è stata cancellata: " +
			"la sua deduplicazione (R11) è rotta, e nessuno se ne accorgerà finché un sync non partirà due volte")
	}
	if n := count(t, pool,
		`SELECT count(*) FROM repositories WHERE user_id = $1::uuid`, altro.UserID); n != 1 {
		t.Error("il collegamento dell'altro utente allo stesso repository è sparito")
	}
}

// TestLeConsegneDiUnRepositorySoloSuoSpariscono è il caso normale, e verifica
// che la prudenza del test precedente non si sia mangiata la regola: quando il
// repository è di un utente solo, le sue consegne se ne vanno con lui.
//
// Senza questo test, una condizione troppo timida — `NOT EXISTS` scritto male,
// per esempio — passerebbe l'altro test conservando tutto per sempre.
func TestLeConsegneDiUnRepositorySoloSuoSpariscono(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "rho", 17)

	requestNow(t, store, te.UserID, 0)
	if _, err := store.Purge(t.Context(), te.UserID); err != nil {
		t.Fatalf("purga: %v", err)
	}

	if n := count(t, pool,
		`SELECT count(*) FROM github_webhook_deliveries WHERE delivery_id = $1`,
		te.GitHubDeliveryID); n != 0 {
		t.Error("la consegna di un repository seguito da lui solo è sopravvissuta alla purga")
	}
}

// TestGliEventiPaddleDiUnAltroAbbonamentoRestano verifica l'altra tabella senza
// `user_id`. La strada è `paddle_subscription_id`, che la 0003 tiene unico:
// nessun evento di un altro abbonamento deve sparire.
func TestGliEventiPaddleDiUnAltroAbbonamentoRestano(t *testing.T) {
	store, pool := newStore(t)
	vittima := seedTenant(t, pool, "sigma", 18)
	altro := seedTenant(t, pool, "tau", 19)

	// Un evento senza abbonamento — un `transaction.completed` di chi non ha
	// ancora una sottoscrizione — non è di nessuno e non deve essere preso da una
	// condizione che tratta NULL con leggerezza.
	exec(t, pool,
		`INSERT INTO paddle_webhook_events (event_id, event_type, status, occurred_at)
		 VALUES ('evt_senza_abbonamento', 'transaction.completed', 'ignored', now())`)

	requestNow(t, store, vittima.UserID, 0)
	if _, err := store.Purge(t.Context(), vittima.UserID); err != nil {
		t.Fatalf("purga: %v", err)
	}

	if n := count(t, pool,
		`SELECT count(*) FROM paddle_webhook_events WHERE event_id = $1`, altro.PaddleEventID); n != 1 {
		t.Error("l'evento Paddle dell'altro account è stato cancellato")
	}
	if n := count(t, pool,
		`SELECT count(*) FROM paddle_webhook_events WHERE event_id = 'evt_senza_abbonamento'`); n != 1 {
		t.Error("un evento senza abbonamento è stato cancellato: non era di nessuno")
	}
	if n := count(t, pool,
		`SELECT count(*) FROM paddle_webhook_events WHERE event_id = $1`, vittima.PaddleEventID); n != 0 {
		t.Error("l'evento Paddle del cancellato è sopravvissuto alla purga")
	}
}

// ---------------------------------------------------------------- audit log

// TestLAuditDelleSueAzioniSparisce verifica la prima metà della regola di
// `audit_log`: se l'unica persona coinvolta è il cancellato, la riga va via.
//
// È l'applicazione letterale della privacy policy §5 — «remove the data» — al
// caso in cui quei dati non servono a nessun altro.
func TestLAuditDelleSueAzioniSparisce(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "upsilon", 20)

	exec(t, pool,
		`INSERT INTO audit_log (actor_user_id, action, entity_type, entity_id, ip_address, user_agent, metadata)
		 VALUES ($1::uuid, 'api_key.revoked', 'api_key', 'chiave-1', '203.0.113.9', 'browser-suo',
		         '{"nota": "azione-sua"}'::jsonb)`, te.UserID)
	exec(t, pool,
		`INSERT INTO audit_log (actor_user_id, target_user_id, action)
		 VALUES ($1::uuid, $1::uuid, 'plan.changed')`, te.UserID)

	requestNow(t, store, te.UserID, 0)
	purged, err := store.Purge(t.Context(), te.UserID)
	if err != nil {
		t.Fatalf("purga: %v", err)
	}

	if purged.AuditDeleted != 2 {
		t.Errorf("righe di audit cancellate = %d, attese 2", purged.AuditDeleted)
	}
	if purged.AuditKept != 0 {
		t.Errorf("righe di audit conservate = %d, attese 0", purged.AuditKept)
	}
	if n := count(t, pool, `SELECT count(*) FROM audit_log`); n != 0 {
		t.Errorf("%d righe di audit sopravvissute: nessuna riguardava altri", n)
	}
}

// TestLAuditDiUnAdminSopravviveSenzaIRiferimentiAlCancellato è la seconda metà
// della regola, ed è l'invariante che la 0008 voleva proteggere quando ha scelto
// `ON DELETE SET NULL`:
//
//	**un admin non deve poter far sparire la traccia della propria
//	impersonificazione convincendo l'utente a chiudere l'account.**
//
// Quindi la riga resta, e con lei l'identità dell'admin, il suo indirizzo IP e
// il suo user agent — che sono dati **suoi**, non del cancellato. Sparisce il
// riferimento all'impersonato, che lo azzera la foreign key, e si svuota
// `metadata`, l'unico campo in cui potrebbe finire qualcosa che lo riguarda.
//
// Ciò che sopravvive non è più un dato personale del cancellato: dice che a un
// certo istante un certo admin ha impersonato qualcuno, e chi fosse non è più
// ricostruibile da nessuna colonna.
func TestLAuditDiUnAdminSopravviveSenzaIRiferimentiAlCancellato(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "phi", 21)

	var adminID string
	must(t, pool.QueryRow(t.Context(),
		`INSERT INTO users (email, password_hash, role) VALUES ($1, 'hash', 'admin') RETURNING id::text`,
		"admin@esempio-cancellazione.test").Scan(&adminID))

	exec(t, pool,
		`INSERT INTO audit_log (actor_user_id, impersonated_user_id, action, ip_address, user_agent, metadata)
		 VALUES ($1::uuid, $2::uuid, 'user.impersonated', '198.51.100.4', 'browser-admin',
		         '{"motivo": "riguarda-il-cancellato"}'::jsonb)`, adminID, te.UserID)

	requestNow(t, store, te.UserID, 0)
	purged, err := store.Purge(t.Context(), te.UserID)
	if err != nil {
		t.Fatalf("purga: %v", err)
	}

	if purged.AuditDeleted != 0 {
		t.Errorf("righe di audit cancellate = %d, attese 0: quella riga è di un altro", purged.AuditDeleted)
	}

	var (
		actor            *string
		impersonated     *string
		ip, userAgent    string
		metadata, action string
	)
	if err := pool.QueryRow(t.Context(),
		`SELECT actor_user_id::text, impersonated_user_id::text, host(ip_address), user_agent,
		        metadata::text, action
		   FROM audit_log`).
		Scan(&actor, &impersonated, &ip, &userAgent, &metadata, &action); err != nil {
		t.Fatalf("lettura della riga di audit: %v", err)
	}

	switch {
	case action != "user.impersonated":
		t.Errorf("azione = %q: la traccia dell'impersonificazione è stata alterata", action)
	case actor == nil || *actor != adminID:
		t.Error("l'admin non è più l'attore della riga: chiudere un account ne ha cancellato la storia")
	case impersonated != nil:
		t.Errorf("il riferimento all'impersonato è ancora %q: doveva azzerarlo la foreign key", *impersonated)
	case ip != "198.51.100.4" || userAgent != "browser-admin":
		t.Errorf("IP=%q user agent=%q: sono dell'admin e non dovevano essere toccati", ip, userAgent)
	case metadata != "{}":
		t.Errorf("metadata = %s: poteva contenere dati del cancellato e doveva essere svuotato", metadata)
	}
}

// TestLaPurgaÈRiprendibileDopoUnInterruzione: la fase a lotti è ripetibile, e
// l'unico stato che non deve esistere è «mezzo cancellato e non più
// riconoscibile».
//
// Con un lotto da una riga sola e il tetto a uno, la prima chiamata si ferma
// lasciando l'account in piedi; la seconda finisce il lavoro. È anche la prova
// che [account.Purged.Truncated] dice la verità.
func TestLaPurgaÈRiprendibileDopoUnInterruzione(t *testing.T) {
	pool := newTestDatabase(t)
	te := seedTenant(t, pool, "chi", 22)

	// Una seconda esecuzione, così che un lotto da una riga non basti.
	exec(t, pool,
		`INSERT INTO job_executions
		     (job_id, scheduled_for, environment, attempt, status, started_at, finished_at, response_status)
		 VALUES ($1::uuid, now() - interval '3 minutes', 'production', 1, 'succeeded',
		         now() - interval '3 minutes', now() - interval '3 minutes', 200)`, te.JobID)

	strozzato := newThrottledStore(t, pool, 1, 1)
	requestNow(t, strozzato, te.UserID, 0)

	primo, err := strozzato.Purge(t.Context(), te.UserID)
	if err != nil {
		t.Fatalf("prima passata: %v", err)
	}
	if !primo.Truncated {
		t.Fatal("la prima passata dichiara di aver finito con il tetto dei lotti a uno")
	}
	if n := count(t, pool, `SELECT count(*) FROM users WHERE id = $1::uuid`, te.UserID); n != 1 {
		t.Fatal("l'account è stato rimosso da una passata che non aveva finito di cancellare le esecuzioni")
	}

	// Le passate successive finiscono il lavoro. Sono più di due perché il tetto
	// è di un lotto per passata.
	for i := 0; i < 5; i++ {
		purged, err := strozzato.Purge(t.Context(), te.UserID)
		if err != nil {
			t.Fatalf("passata %d: %v", i+2, err)
		}
		if !purged.Truncated {
			break
		}
	}

	if n := count(t, pool, `SELECT count(*) FROM users WHERE id = $1::uuid`, te.UserID); n != 0 {
		t.Error("l'account non è stato rimosso nemmeno dopo che le esecuzioni erano finite")
	}
}

// TestLaPurgaDiUnAccountGiàRimossoNonÈUnErrore: due passate concorrenti sono la
// stessa promessa mantenuta due volte, non un guasto.
func TestLaPurgaDiUnAccountGiàRimossoNonÈUnErrore(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "psi", 23)

	requestNow(t, store, te.UserID, 0)
	if _, err := store.Purge(t.Context(), te.UserID); err != nil {
		t.Fatalf("prima purga: %v", err)
	}
	if _, err := store.Purge(t.Context(), te.UserID); err != nil {
		t.Fatalf("seconda purga su un account già rimosso: %v", err)
	}
}
