package accountpg_test

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/billingpg"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// TestUnEventoPaddleDopoLaPurgaNonFaRipetereLaConsegna è il caso che esiste e va
// gestito: **il contratto, dalla parte di Paddle, sopravvive all'account.**
//
// Paddle è Merchant of Record (SPEC §2, Termini §1): la sottoscrizione è un
// rapporto fra l'utente e Paddle, e da qui non parte nessuna chiamata per
// annullarla — `PADDLE_API_KEY` esiste in configurazione e non la usa nessuno.
// Un `subscription.updated` può quindi arrivare il giorno dopo la purga, e
// arriverà.
//
// Cosa succederebbe senza questo pezzo: `resolveUser` non trova né l'utente né
// la riga di `subscriptions` — se ne sono andati insieme — e restituisce
// `ErrUnknownSubscriber`; l'evento viene registrato come `failed`, il livello
// HTTP risponde 500, e **Paddle ripete per tre giorni**. Un allarme continuo su
// un fatto normale, che finirebbe per coprire quelli veri.
//
// Il test parte dal database vero perché è lì che il caso nasce: si popola un
// account con la sua sottoscrizione, lo si purga, e si consegna un evento di
// quella sottoscrizione.
func TestUnEventoPaddleDopoLaPurgaNonFaRipetereLaConsegna(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "omega", 24)

	// Il catalogo dei prezzi: senza, l'evento non sarebbe traducibile in un piano
	// e fallirebbe per un motivo diverso da quello che si vuole provare.
	const prezzoPro = "pri_pro_mensile"
	svc, err := billingpg.NewService(pool, func(name string) string {
		if name == "PADDLE_PRICE_PRO_MONTHLY" {
			return prezzoPro
		}
		return ""
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("costruzione del servizio di fatturazione: %v", err)
	}

	evento := paddle.Subscription{
		Event: paddle.Event{
			ID:         "evt_dopo_la_purga",
			Type:       "subscription.updated",
			OccurredAt: time.Now(),
		},
		ID:         te.PaddleSubscriptionID,
		CustomerID: te.PaddleCustomerID,
		UserID:     te.UserID,
		Status:     paddle.SubscriptionActive,
		PriceIDs:   []string{prezzoPro},
	}

	// Prima della purga l'evento si applica normalmente: è la controprova che
	// l'esito diverso di dopo dipende dalla cancellazione e non da un evento
	// costruito male.
	if _, err := svc.ApplySubscription(t.Context(), evento); err != nil {
		t.Fatalf("applicazione prima della purga: %v", err)
	}

	requestNow(t, store, te.UserID, 0)
	if _, err := store.Purge(t.Context(), te.UserID); err != nil {
		t.Fatalf("purga: %v", err)
	}

	evento.Event.ID = "evt_ancora_dopo_la_purga"
	evento.Event.OccurredAt = time.Now().Add(time.Minute)
	_, err = svc.ApplySubscription(t.Context(), evento)

	switch {
	case err == nil:
		t.Fatal("l'evento è stato applicato a un account che non esiste più")
	case !errors.Is(err, paddle.ErrUnattributable):
		t.Fatalf("errore = %v, atteso paddle.ErrUnattributable: senza, il livello HTTP "+
			"risponde 500 e Paddle ripete per tre giorni una consegna che nessuna ripetizione può sistemare", err)
	case !errors.Is(err, billing.ErrUnknownSubscriber):
		t.Error("l'errore ha perso la causa originale: chi indaga non saprebbe perché l'evento non è stato attribuito")
	}

	// E l'evento non ha resuscitato niente: nessuna riga di `subscriptions`, e
	// nessun utente.
	if n := count(t, pool, `SELECT count(*) FROM users WHERE id = $1::uuid`, te.UserID); n != 0 {
		t.Error("l'evento ha ricreato l'account cancellato")
	}
	if n := count(t, pool,
		`SELECT count(*) FROM subscriptions WHERE paddle_subscription_id = $1`,
		te.PaddleSubscriptionID); n != 0 {
		t.Error("l'evento ha ricreato la sottoscrizione di un account cancellato")
	}
}

// TestDuranteLaGraziaGliEventiPaddleSiApplicanoAncora fissa il rovescio, che è
// altrettanto importante: durante la finestra di ripensamento **l'account
// esiste**, e Paddle lo sta ancora fatturando.
//
// Un evento ignorato in quel periodo lascerebbe l'entitlement fermo a un piano
// che l'utente non ha più — e se poi cambiasse idea, tornerebbe a un account con
// il piano sbagliato.
func TestDuranteLaGraziaGliEventiPaddleSiApplicanoAncora(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "omegadue", 25)

	const prezzoPro = "pri_pro_mensile"
	svc, err := billingpg.NewService(pool, func(name string) string {
		if name == "PADDLE_PRICE_PRO_MONTHLY" {
			return prezzoPro
		}
		return ""
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("costruzione del servizio di fatturazione: %v", err)
	}

	requestNow(t, store, te.UserID, time.Hour)

	applicato, err := svc.ApplySubscription(t.Context(), paddle.Subscription{
		Event: paddle.Event{
			ID:         "evt_durante_la_grazia",
			Type:       "subscription.updated",
			OccurredAt: time.Now(),
		},
		ID:         te.PaddleSubscriptionID,
		CustomerID: te.PaddleCustomerID,
		UserID:     te.UserID,
		Status:     paddle.SubscriptionActive,
		PriceIDs:   []string{prezzoPro},
	})
	if err != nil {
		t.Fatalf("applicazione durante la grazia: %v", err)
	}
	if !applicato {
		t.Error("l'evento non è stato applicato: durante la grazia l'account esiste e viene ancora fatturato")
	}
}
