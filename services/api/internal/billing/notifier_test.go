package billing_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// postino registra le comunicazioni invece di mandarle.
type postino struct {
	avvisi []billing.PlanChangeNotice
	errore error
}

func (p *postino) PlanChanged(_ context.Context, notice billing.PlanChangeNotice) error {
	if p.errore != nil {
		return p.errore
	}
	p.avvisi = append(p.avvisi, notice)
	return nil
}

func servizioConPostino(t *testing.T, store billing.Store, cat paddle.Catalog, n billing.Notifier) *billing.Service {
	t.Helper()
	svc, err := billing.NewService(billing.Options{
		Store:       store,
		Catalog:     cat,
		ClientToken: "live_token_di_prova",
		Environment: "sandbox",
		Notifier:    n,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:         func() time.Time { return time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	return svc
}

// Un downgrade che sospende dei job comunica **i due conteggi separati**: sono
// due rimedi diversi, e un totale unico direbbe a metà degli utenti la cosa
// sbagliata (R58).
func TestUnDowngradeComunicaIJobSospesiPerMotivo(t *testing.T) {
	store := nuovoRegistro()
	store.sospensione = billing.Suspension{ByJobLimit: 7, ByResolution: 1}
	postino := &postino{}
	svc := servizioConPostino(t, store, catalogo(t), postino)

	quando := time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC)
	// Prima si compra Pro, poi si smette di pagare: il secondo evento è il
	// downgrade che ferma i job.
	sub := sottoscrizione("sub_1", paddle.SubscriptionActive, prezzoProMensile, utente, quando)
	if _, err := svc.ApplySubscription(context.Background(), sub); err != nil {
		t.Fatalf("acquisto: %v", err)
	}

	scaduta := sottoscrizione("sub_1", paddle.SubscriptionCanceled, prezzoProMensile, utente,
		quando.Add(30*24*time.Hour))
	if _, err := svc.ApplySubscription(context.Background(), scaduta); err != nil {
		t.Fatalf("disdetta: %v", err)
	}

	if len(postino.avvisi) != 2 {
		t.Fatalf("comunicazioni: %d, attese due", len(postino.avvisi))
	}

	acquisto := postino.avvisi[0]
	if acquisto.PreviousPlan != "Free" || acquisto.NewPlan != "Pro" {
		t.Errorf("acquisto comunicato come %q → %q", acquisto.PreviousPlan, acquisto.NewPlan)
	}

	downgrade := postino.avvisi[1]
	switch {
	case downgrade.PreviousPlan != "Pro" || downgrade.NewPlan != "Free":
		t.Errorf("downgrade comunicato come %q → %q", downgrade.PreviousPlan, downgrade.NewPlan)
	case downgrade.SuspendedByJobLimit != 7:
		t.Errorf("job sospesi per il tetto: %d", downgrade.SuspendedByJobLimit)
	case downgrade.SuspendedByResolution != 1:
		t.Errorf("job sospesi per la risoluzione: %d", downgrade.SuspendedByResolution)
	case downgrade.UserID != utente:
		t.Errorf("destinatario: %q", downgrade.UserID)
	}
}

// Un rinnovo non è una variazione: stesso piano prima e dopo, nessuna email.
//
// È l'evento Paddle più frequente che esista, e senza questo controllo l'utente
// riceverebbe ogni mese l'annuncio di un cambiamento mai avvenuto.
func TestUnRinnovoNonComunicaNiente(t *testing.T) {
	store := nuovoRegistro()
	postino := &postino{}
	svc := servizioConPostino(t, store, catalogo(t), postino)
	ctx := context.Background()

	quando := time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC)
	for mese := range 3 {
		sub := sottoscrizione("sub_1", paddle.SubscriptionActive, prezzoProMensile, utente,
			quando.Add(time.Duration(mese)*30*24*time.Hour))
		if _, err := svc.ApplySubscription(ctx, sub); err != nil {
			t.Fatalf("mese %d: %v", mese, err)
		}
	}

	if len(postino.avvisi) != 1 {
		t.Errorf("comunicazioni: %d, attesa solo quella dell'acquisto", len(postino.avvisi))
	}
}

// Un'email che non parte non fa fallire il pagamento.
//
// È la clausola che rende sicuro agganciare l'invio a un webhook di
// fatturazione: se l'errore risalisse, il livello HTTP risponderebbe 500 e
// Paddle ripeterebbe la consegna di un evento **già applicato per intero** —
// piano scritto, job sospesi — solo perché non si è riusciti a raccontarlo.
func TestUnAvvisoCheNonParteNonFaFallireIlPagamento(t *testing.T) {
	store := nuovoRegistro()
	postino := &postino{errore: errors.New("coda irraggiungibile")}
	svc := servizioConPostino(t, store, catalogo(t), postino)

	sub := sottoscrizione("sub_1", paddle.SubscriptionActive, prezzoProMensile, utente,
		time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC))

	applicato, err := svc.ApplySubscription(context.Background(), sub)
	if err != nil {
		t.Fatalf("ApplySubscription: %v", err)
	}
	if !applicato {
		t.Error("l'evento non è stato applicato")
	}
	if len(store.scritture) != 1 {
		t.Errorf("scritture: %d, il piano dev'essere stato scritto comunque", len(store.scritture))
	}
	if len(store.limiti) != 1 {
		t.Errorf("applicazioni di R58: %d, i job vanno sospesi comunque", len(store.limiti))
	}
}

// Senza destinatario configurato il piano si applica lo stesso: una macchina
// senza email non è una macchina senza fatturazione.
func TestSenzaNotificatoreIlPianoSiApplicaComunque(t *testing.T) {
	store := nuovoRegistro()
	svc := servizio(t, store, catalogo(t))

	sub := sottoscrizione("sub_1", paddle.SubscriptionActive, prezzoProMensile, utente,
		time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC))
	applicato, err := svc.ApplySubscription(context.Background(), sub)
	if err != nil || !applicato {
		t.Fatalf("ApplySubscription: applicato=%v err=%v", applicato, err)
	}
}
