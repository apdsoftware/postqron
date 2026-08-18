// Ciò che solo PostgreSQL può garantire.
//
// La politica del consenso è provata senza database in internal/marketing, con
// uno store in memoria. Qui si verifica il resto: che le query dicano quello che
// il package promette, e soprattutto che i **vincoli** reggano anche a chi li
// aggira — un `INSERT` scritto a mano non passa da [marketing.Record.Validate],
// e se la difesa fosse solo lì basterebbe una query in più per superarla.
package marketingpg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/marketing"
	"github.com/apdsoftware/postqron/services/api/internal/marketingpg"
)

func newStore(t *testing.T, pool *pgxpool.Pool) *marketingpg.Store {
	t.Helper()
	store, err := marketingpg.New(pool)
	if err != nil {
		t.Fatalf("marketingpg.New: %v", err)
	}
	return store
}

// newUser crea un utente e ne restituisce l'identificativo.
func newUser(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email, full_name, language) VALUES ($1, $2, $3) RETURNING id::text`,
		email, "Mario Rossi", "it").Scan(&id)
	if err != nil {
		t.Fatalf("creazione dell'utente: %v", err)
	}
	return id
}

func record(t *testing.T, store *marketingpg.Store, userID string, d marketing.Decision, s marketing.Source) marketing.Applied {
	t.Helper()
	applied, err := store.Record(t.Context(), marketing.Record{
		UserID: userID, Decision: d, Source: s, IP: "203.0.113.7",
	})
	if err != nil {
		t.Fatalf("Record(%s): %v", d, err)
	}
	return applied
}

// ---------------------------------------------------------------- la traccia

// La traccia conserva ogni cambio di idea, non solo l'ultimo.
//
// È la frase di §2.8 — «we keep a record of when you consented and when you
// withdrew» — nella forma in cui è più facile sbagliarla: due colonne su `users`
// avrebbero risposto bene finché l'utente cambiava idea una volta sola, e al
// secondo giro avrebbero perso il primo consenso, cioè quello che copre il
// periodo su cui un reclamo verterebbe.
func TestLaTracciaConservaOgniCambioDiIdea(t *testing.T) {
	pool := newTestDatabase(t)
	store := newStore(t, pool)
	userID := newUser(t, pool, "traccia@example.test")

	for _, passo := range []struct {
		decision marketing.Decision
		source   marketing.Source
	}{
		{marketing.DecisionGranted, marketing.SourceProfile},
		{marketing.DecisionWithdrawn, marketing.SourceUnsubscribeLink},
		{marketing.DecisionGranted, marketing.SourceProfile},
	} {
		if applied := record(t, store, userID, passo.decision, passo.source); applied != marketing.Recorded {
			t.Fatalf("%s: esito %q, atteso %q", passo.decision, applied, marketing.Recorded)
		}
	}

	history, err := store.History(t.Context(), userID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("la traccia ha %d righe, attese 3: %+v", len(history), history)
	}

	attese := []marketing.Decision{
		marketing.DecisionGranted, // la più recente per prima
		marketing.DecisionWithdrawn,
		marketing.DecisionGranted,
	}
	for i, attesa := range attese {
		if history[i].Decision != attesa {
			t.Errorf("riga %d: decisione %q, attesa %q", i, history[i].Decision, attesa)
		}
		if history[i].OccurredAt.IsZero() {
			t.Errorf("riga %d: senza data", i)
		}
	}
	// La provenienza distingue la revoca dal link da quella delle impostazioni.
	if history[1].Source != marketing.SourceUnsubscribeLink {
		t.Errorf("la revoca risulta arrivata da %q", history[1].Source)
	}

	// Le date sono in ordine: l'orologio è **quello del database**, e il
	// confronto è fra righe scritte dallo stesso orologio. Confrontarle con
	// un istante preso in Go sarebbe confrontare due orologi diversi.
	for i := range len(history) - 1 {
		if history[i].OccurredAt.Before(history[i+1].OccurredAt) {
			t.Errorf("la traccia non è in ordine: riga %d è precedente alla %d", i, i+1)
		}
	}

	state, err := store.State(t.Context(), userID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if !state.Decided || !state.Consented {
		t.Errorf("lo stato attuale è %+v, atteso un consenso in vigore", state)
	}
}

// Una decisione ripetuta non allunga la traccia.
//
// La condizione sta **nella stessa istruzione** che inserisce, e non in un
// «leggi e poi decidi»: due connessioni concorrenti che rileggessero lo stato
// prima di scrivere produrrebbero due revoche identiche, che è quello che
// succede quando un browser rinvia un form.
func TestUnaDecisioneRipetutaNonAllungaLaTraccia(t *testing.T) {
	pool := newTestDatabase(t)
	store := newStore(t, pool)
	userID := newUser(t, pool, "ripetuta@example.test")

	if applied := record(t, store, userID, marketing.DecisionGranted, marketing.SourceProfile); applied != marketing.Recorded {
		t.Fatalf("primo consenso: %q", applied)
	}
	for range 3 {
		if applied := record(t, store, userID, marketing.DecisionGranted, marketing.SourceProfile); applied != marketing.Unchanged {
			t.Errorf("consenso ripetuto: esito %q, atteso %q", applied, marketing.Unchanged)
		}
	}

	history, err := store.History(t.Context(), userID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("la traccia ha %d righe dopo quattro consensi identici, attesa 1", len(history))
	}
}

// Chi non ha mai deciso non ha acconsentito.
//
// L'assenza di una riga è un rifiuto e non un valore predefinito: è la regola
// dell'Art. 6(1)(a), e sta in due posti che devono dire la stessa cosa — la
// vista `marketing_consent_state`, che non elenca chi non compare, e il
// `coalesce` di `recordSQL`.
func TestChiNonHaMaiDecisoNonHaConsenso(t *testing.T) {
	pool := newTestDatabase(t)
	store := newStore(t, pool)
	userID := newUser(t, pool, "indeciso@example.test")

	state, err := store.State(t.Context(), userID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if state.Decided || state.Consented {
		t.Errorf("chi non ha mai deciso risulta %+v", state)
	}

	recipient, err := store.Recipient(t.Context(), userID)
	if err != nil {
		t.Fatalf("Recipient: %v", err)
	}
	if recipient.Consented {
		t.Error("chi non ha mai deciso risulta consenziente: sarebbe un'email di marketing senza consenso")
	}

	// E una revoca su chi non ha mai deciso non scrive niente: non c'è niente da
	// ritirare, e una riga direbbe che ha ritirato qualcosa che non aveva.
	if applied := record(t, store, userID, marketing.DecisionWithdrawn, marketing.SourceUnsubscribeLink); applied != marketing.Unchanged {
		t.Errorf("revoca senza consenso: esito %q, atteso %q", applied, marketing.Unchanged)
	}
}

// -------------------------------------------------------------- il vincolo

// Il database rifiuta un consenso arrivato dal link di disiscrizione.
//
// L'`INSERT` è scritto a mano di proposito: passa **sopra** a
// [marketing.Record.Validate], che è la difesa in Go, e verifica quella che
// nessun `if` dimenticato può aggirare. Se la difesa fosse solo nel codice,
// bastherebbe una query in più — o un percorso nuovo che qualcuno aggiunge fra
// un anno — perché chiunque abbia un link possa iscrivere l'intestatario di un
// indirizzo che non è il suo.
func TestIlDatabaseRifiutaUnConsensoDalLinkDiDisiscrizione(t *testing.T) {
	pool := newTestDatabase(t)
	userID := newUser(t, pool, "vincolo@example.test")

	_, err := pool.Exec(t.Context(),
		`INSERT INTO marketing_consents (user_id, decision, source)
		 VALUES ($1::uuid, 'granted', 'unsubscribe_link')`,
		userID)
	if err == nil {
		t.Fatal("il database ha accettato un consenso proveniente dal link di disiscrizione")
	}
	if !strings.Contains(err.Error(), "marketing_consents_grant_needs_session_check") {
		t.Errorf("il rifiuto non viene dal vincolo atteso: %v", err)
	}

	// La revoca dalla stessa provenienza, invece, è esattamente ciò che il link
	// deve poter fare.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO marketing_consents (user_id, decision, source)
		 VALUES ($1::uuid, 'withdrawn', 'unsubscribe_link')`,
		userID); err != nil {
		t.Fatalf("il database ha rifiutato una revoca dal link: %v", err)
	}
}

// ----------------------------------------------------------- il destinatario

// Destinatario e consenso arrivano dalla stessa lettura.
//
// È la proprietà su cui poggia «senza consenso non parte niente»: se fossero due
// query, fra l'una e l'altra ci sarebbe una finestra in cui l'utente si
// disiscrive e l'email parte lo stesso.
func TestIlDestinatarioEIlConsensoArrivanoInsieme(t *testing.T) {
	pool := newTestDatabase(t)
	store := newStore(t, pool)
	userID := newUser(t, pool, "destinatario@example.test")

	record(t, store, userID, marketing.DecisionGranted, marketing.SourceProfile)

	recipient, err := store.Recipient(t.Context(), userID)
	if err != nil {
		t.Fatalf("Recipient: %v", err)
	}
	switch {
	case !recipient.Consented:
		t.Error("il consenso non arriva insieme al destinatario")
	case recipient.Email != "destinatario@example.test":
		t.Errorf("indirizzo %q", recipient.Email)
	case recipient.Language != "it":
		t.Errorf("lingua %q, attesa quella del profilo (R33)", recipient.Language)
	}

	record(t, store, userID, marketing.DecisionWithdrawn, marketing.SourceUnsubscribeLink)

	recipient, err = store.Recipient(t.Context(), userID)
	if err != nil {
		t.Fatalf("Recipient: %v", err)
	}
	if recipient.Consented {
		t.Error("dopo la revoca il destinatario risulta ancora consenziente")
	}
}

// Un account che ha chiesto di essere cancellato non riceve marketing.
//
// Il suo consenso è formalmente in vigore — nessuno lo ha ritirato — ma il gesto
// più forte che poteva fare dice il contrario. Le transazionali continuano, ed è
// giusto: raccontano proprio la cancellazione in corso.
func TestUnAccountInCancellazioneNonRiceveMarketing(t *testing.T) {
	pool := newTestDatabase(t)
	store := newStore(t, pool)
	userID := newUser(t, pool, "uscita@example.test")

	record(t, store, userID, marketing.DecisionGranted, marketing.SourceProfile)

	if _, err := pool.Exec(t.Context(),
		`UPDATE users
		    SET deletion_requested_at = now(), purge_after = now() + interval '30 days'
		  WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("richiesta di cancellazione: %v", err)
	}

	if _, err := store.Recipient(t.Context(), userID); !errors.Is(err, marketing.ErrNoRecipient) {
		t.Errorf("un account in cancellazione è ancora un destinatario di marketing: %v", err)
	}

	// **Ma può ancora disiscriversi.** Le due domande sono diverse, e questo è il
	// caso in cui si vede: dire a qualcuno che il link di disiscrizione non
	// funziona proprio mentre sta chiudendo l'account sarebbe il momento peggiore
	// per farlo, e §2.8 non pone condizioni alla revoca.
	language, err := store.Language(t.Context(), userID)
	if err != nil {
		t.Fatalf("un account in cancellazione non può più disiscriversi: %v", err)
	}
	if language != "it" {
		t.Errorf("lingua %q, attesa quella del profilo", language)
	}
	if applied := record(t, store, userID, marketing.DecisionWithdrawn, marketing.SourceUnsubscribeLink); applied != marketing.Recorded {
		t.Errorf("la revoca dal link è stata rifiutata: %q", applied)
	}

	// Il consenso però resta leggibile: la finestra di ripensamento non cancella
	// la prova, e chi torna indietro ritrova la propria storia.
	history, err := store.History(t.Context(), userID)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("la traccia ha %d righe, attese 2: la richiesta di cancellazione non la tocca", len(history))
	}
}

// Un account cancellato non decide più niente.
func TestUnAccountCancellatoNonDecidePiu(t *testing.T) {
	pool := newTestDatabase(t)
	store := newStore(t, pool)
	userID := newUser(t, pool, "cancellato@example.test")

	if _, err := pool.Exec(t.Context(),
		`UPDATE users SET deleted_at = now() WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("cancellazione: %v", err)
	}

	applied, err := store.Record(t.Context(), marketing.Record{
		UserID: userID, Decision: marketing.DecisionGranted, Source: marketing.SourceProfile,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if applied != marketing.NoUser {
		t.Errorf("esito %q, atteso %q", applied, marketing.NoUser)
	}
}

// La traccia sparisce con l'account.
//
// `ON DELETE CASCADE`, al contrario di `audit_log`: questa traccia riguarda
// l'utente, non l'operato di un terzo su di lui. Quando l'account non c'è più
// non c'è nessun indirizzo a cui scrivere, quindi non c'è più niente da
// provare — e tenere la prova di un diritto che non eserciteremo sarebbe tenere
// un dato personale senza scopo.
func TestLaTracciaSparisceConLAccount(t *testing.T) {
	pool := newTestDatabase(t)
	store := newStore(t, pool)
	userID := newUser(t, pool, "purga@example.test")

	record(t, store, userID, marketing.DecisionGranted, marketing.SourceProfile)

	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("purga dell'account: %v", err)
	}

	var rimaste int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM marketing_consents WHERE user_id = $1::uuid`, userID).Scan(&rimaste); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if rimaste != 0 {
		t.Errorf("dopo la purga restano %d decisioni", rimaste)
	}
}

// Un indirizzo IP illeggibile non impedisce la revoca.
//
// L'indirizzo è un contorno della prova, la decisione è la prova. Rifiutare una
// revoca perché la testata che porta l'indirizzo era malformata significherebbe
// continuare a scrivere a chi ha chiesto di smettere, per un dettaglio che non
// lo riguarda.
func TestUnIndirizzoIllegibileNonImpedisceLaRevoca(t *testing.T) {
	pool := newTestDatabase(t)
	store := newStore(t, pool)
	userID := newUser(t, pool, "ip@example.test")

	record(t, store, userID, marketing.DecisionGranted, marketing.SourceProfile)

	applied, err := store.Record(context.Background(), marketing.Record{
		UserID:   userID,
		Decision: marketing.DecisionWithdrawn,
		Source:   marketing.SourceUnsubscribeLink,
		IP:       "non-un-indirizzo",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if applied != marketing.Recorded {
		t.Errorf("esito %q, atteso %q", applied, marketing.Recorded)
	}
}
