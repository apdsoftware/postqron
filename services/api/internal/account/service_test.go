package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/account"
)

// ------------------------------------------------------------------ doppioni

// storeDiProva registra cosa gli è stato chiesto. Non simula il database: le
// query hanno i loro test contro PostgreSQL vero in internal/accountpg, e quello
// che si verifica qui è **l'ordine delle decisioni** — che la password venga
// chiesta prima di toccare qualunque cosa, e che la presa d'atto venga chiesta
// prima di aprire la finestra.
type storeDiProva struct {
	status account.Status

	requested   int
	requestedAt time.Time
	purgeAfter  time.Time

	canceled int
	err      error
}

func (s *storeDiProva) Status(context.Context, string) (account.Status, error) {
	return s.status, s.err
}

func (s *storeDiProva) RequestDeletion(_ context.Context, _ string, at, purgeAfter time.Time) (account.Receipt, error) {
	s.requested++
	s.requestedAt, s.purgeAfter = at, purgeAfter
	if s.err != nil {
		return account.Receipt{}, s.err
	}
	return account.Receipt{RequestedAt: at, PurgeAfter: purgeAfter, JobsStopped: 3}, nil
}

func (s *storeDiProva) CancelDeletion(context.Context, string) (account.Restored, error) {
	s.canceled++
	return account.Restored{JobsResumed: 3}, s.err
}

func (s *storeDiProva) DueForPurge(context.Context, time.Time, int) ([]string, error) {
	return nil, nil
}

func (s *storeDiProva) Purge(context.Context, string) (account.Purged, error) {
	return account.Purged{}, nil
}

// confermaDiProva accetta una password sola.
type confermaDiProva struct {
	giusta  string
	chieste int
}

func (c *confermaDiProva) ConfirmPassword(_ context.Context, _, password string) error {
	c.chieste++
	if password != c.giusta {
		return errors.New("password non corretta")
	}
	return nil
}

func nuovoServizio(t *testing.T, store *storeDiProva, grace time.Duration) (*account.Service, *confermaDiProva) {
	t.Helper()
	conferma := &confermaDiProva{giusta: "quella-giusta"}
	svc, err := account.New(account.Options{
		Store:   store,
		Confirm: conferma,
		Grace:   grace,
		Now:     func() time.Time { return time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	return svc, conferma
}

// ------------------------------------------------------------------ conferma

// TestSenzaLaPasswordNonSiCancellaNiente: la cancellazione è irreversibile, e una
// sessione rubata da un portatile lasciato aperto non deve bastare.
//
// Ciò che il test verifica non è il rifiuto — quello è ovvio — ma che lo store
// **non venga toccato**: una conferma controllata dopo aver già fermato i job
// sarebbe una conferma che non serve a niente.
func TestSenzaLaPasswordNonSiCancellaNiente(t *testing.T) {
	store := &storeDiProva{}
	svc, _ := nuovoServizio(t, store, time.Hour)

	if _, err := svc.RequestDeletion(t.Context(), "u-1", account.RequestInput{Password: "sbagliata"}); err == nil {
		t.Fatal("la richiesta è stata accettata con la password sbagliata")
	}
	if store.requested != 0 {
		t.Error("lo store è stato toccato prima che la password fosse confermata")
	}
}

// TestLaConfermaPrecedeOgniAltroControllo fissa l'ordine: la password si verifica
// **prima** di leggere lo stato dell'account.
//
// Non è pedanteria: senza, un rifiuto `subscription_active` direbbe a chi ha
// rubato una sessione che quell'account ha un piano a pagamento — cioè
// risponderebbe a una domanda senza aver chiesto la password.
func TestLaConfermaPrecedeOgniAltroControllo(t *testing.T) {
	store := &storeDiProva{status: account.Status{
		Subscription: account.Subscription{Paid: true, PlanCode: "pro"},
	}}
	svc, conferma := nuovoServizio(t, store, time.Hour)

	_, err := svc.RequestDeletion(t.Context(), "u-1", account.RequestInput{Password: "sbagliata"})
	var active *account.SubscriptionActiveError
	if errors.As(err, &active) {
		t.Fatal("una password sbagliata ha ottenuto in risposta il piano dell'account")
	}
	if conferma.chieste != 1 {
		t.Errorf("conferme chieste = %d, attesa 1", conferma.chieste)
	}
}

// ------------------------------------------------------------------- Paddle

// TestConUnAbbonamentoVivoServeLaPresaDAtto verifica il rifiuto della prima
// richiesta e il passaggio della seconda.
//
// La ragione sta nei due documenti insieme: Paddle è Merchant of Record e
// l'abbonamento non è nostro da annullare (Termini §1), e i Termini §4.3 dicono
// che non c'è rimborso pro rata. Un utente che chiude l'account e continua a
// essere fatturato per mesi ha un problema vero, e dopo non c'è nulla che
// possiamo fare per lui.
func TestConUnAbbonamentoVivoServeLaPresaDAtto(t *testing.T) {
	store := &storeDiProva{status: account.Status{
		Subscription: account.Subscription{Paid: true, PlanCode: "pro", PaddleSubscriptionID: "sub_01"},
	}}
	svc, _ := nuovoServizio(t, store, time.Hour)

	_, err := svc.RequestDeletion(t.Context(), "u-1", account.RequestInput{Password: "quella-giusta"})
	var active *account.SubscriptionActiveError
	switch {
	case !errors.As(err, &active):
		t.Fatalf("errore = %v, atteso SubscriptionActiveError", err)
	case !errors.Is(err, account.ErrSubscriptionActive):
		t.Error("l'errore non si riconosce con errors.Is: il livello HTTP non saprebbe tradurlo")
	case active.Subscription.PlanCode != "pro" || active.Subscription.PaddleSubscriptionID != "sub_01":
		t.Errorf("l'errore non porta l'abbonamento: %+v", active.Subscription)
	}
	if store.requested != 0 {
		t.Error("la finestra è stata aperta nonostante il rifiuto")
	}

	// Presa d'atto: i Termini §7 dicono «You may close your account at any time»,
	// e la seconda richiesta deve passare. Un rifiuto definitivo sarebbe un
	// divieto che il documento non prevede.
	if _, err := svc.RequestDeletion(t.Context(), "u-1", account.RequestInput{
		Password:                 "quella-giusta",
		SubscriptionAcknowledged: true,
	}); err != nil {
		t.Fatalf("richiesta con presa d'atto: %v", err)
	}
	if store.requested != 1 {
		t.Errorf("richieste allo store = %d, attesa 1", store.requested)
	}
}

// TestSenzaAbbonamentoNonSiChiedeNiente: chi è sul piano d'ingresso non deve
// incontrare un ostacolo inventato. Il piano Free non passa da Paddle (0003) e
// non c'è niente di cui prendere atto.
func TestSenzaAbbonamentoNonSiChiedeNiente(t *testing.T) {
	store := &storeDiProva{}
	svc, _ := nuovoServizio(t, store, time.Hour)

	if _, err := svc.RequestDeletion(t.Context(), "u-1", account.RequestInput{Password: "quella-giusta"}); err != nil {
		t.Fatalf("richiesta su un account senza abbonamento: %v", err)
	}
}

// ------------------------------------------------------------------- grazia

// TestLaScadenzaÈLIstanteDellaRichiestaPiùLaGrazia verifica che il calcolo
// avvenga **una volta**, al momento della richiesta.
//
// È il motivo per cui la 0017 mette `purge_after` sulla riga invece di lasciar
// calcolare alla purga: il periodo è configurabile (R45), quindi cambia, e una
// scadenza ricalcolata a ogni passata cancellerebbe domani account a cui erano
// stati promessi trenta giorni.
func TestLaScadenzaÈLIstanteDellaRichiestaPiùLaGrazia(t *testing.T) {
	store := &storeDiProva{}
	svc, _ := nuovoServizio(t, store, 48*time.Hour)

	if _, err := svc.RequestDeletion(t.Context(), "u-1", account.RequestInput{Password: "quella-giusta"}); err != nil {
		t.Fatalf("richiesta: %v", err)
	}
	atteso := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	if !store.purgeAfter.Equal(atteso) {
		t.Errorf("scadenza = %v, attesa %v", store.purgeAfter, atteso)
	}
}

func TestLaGraziaSiLeggeDallAmbiente(t *testing.T) {
	casi := map[string]struct {
		valore  string
		attesa  time.Duration
		errante bool
	}{
		"vuota è quella del documento": {valore: "", attesa: account.DefaultGrace},
		"una durata valida":            {valore: "48h", attesa: 48 * time.Hour},
		"zero purga subito":            {valore: "0s", attesa: 0},
		"non è una durata":             {valore: "trenta giorni", errante: true},
		"negativa":                     {valore: "-1h", errante: true},
	}
	for nome, caso := range casi {
		t.Run(nome, func(t *testing.T) {
			grace, err := account.GraceFromEnv(func(string) string { return caso.valore })
			if caso.errante {
				if err == nil {
					t.Fatalf("valore %q accettato, atteso un rifiuto", caso.valore)
				}
				return
			}
			if err != nil {
				t.Fatalf("valore %q rifiutato: %v", caso.valore, err)
			}
			if grace != caso.attesa {
				t.Errorf("grazia = %v, attesa %v", grace, caso.attesa)
			}
		})
	}
}

// ------------------------------------------------------------ annullamento

// TestLAnnullamentoNonChiedeLaPassword: la conferma protegge dall'azione
// distruttiva, e questa è l'azione opposta — chi annulla sta togliendo un
// pericolo, non creandone uno.
func TestLAnnullamentoNonChiedeLaPassword(t *testing.T) {
	store := &storeDiProva{}
	svc, conferma := nuovoServizio(t, store, time.Hour)

	if _, err := svc.CancelDeletion(t.Context(), "u-1"); err != nil {
		t.Fatalf("annullamento: %v", err)
	}
	if conferma.chieste != 0 {
		t.Error("l'annullamento ha chiesto la password")
	}
	if store.canceled != 1 {
		t.Errorf("annullamenti allo store = %d, atteso 1", store.canceled)
	}
}

// TestUnaCancellazioneGiàInCorsoNonSiRipete: ripetere la richiesta sposterebbe
// in avanti una scadenza che l'utente aveva già fissato, e quella è una
// decisione che non deve prendere un doppio clic.
func TestUnaCancellazioneGiàInCorsoNonSiRipete(t *testing.T) {
	store := &storeDiProva{status: account.Status{Requested: true}}
	svc, _ := nuovoServizio(t, store, time.Hour)

	_, err := svc.RequestDeletion(t.Context(), "u-1", account.RequestInput{Password: "quella-giusta"})
	if !errors.Is(err, account.ErrAlreadyRequested) {
		t.Fatalf("errore = %v, atteso ErrAlreadyRequested", err)
	}
	if store.requested != 0 {
		t.Error("la finestra è stata riaperta su una cancellazione già in corso")
	}
}

// ------------------------------------------------------------ costruzione

func TestIlServizioRifiutaUnaCostruzioneIncompleta(t *testing.T) {
	casi := map[string]account.Options{
		"senza store":    {Confirm: &confermaDiProva{}},
		"senza conferma": {Store: &storeDiProva{}},
		"grazia negativa": {
			Store: &storeDiProva{}, Confirm: &confermaDiProva{}, Grace: -time.Hour,
		},
	}
	for nome, opts := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, err := account.New(opts); err == nil {
				t.Fatal("costruzione accettata, atteso un rifiuto")
			}
		})
	}
}
