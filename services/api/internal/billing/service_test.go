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

// ------------------------------------------------------------------ finzioni

// registro è un [billing.Store] in memoria. Riproduce la filigrana e il piano in
// forza; **non** riproduce R58, che è una istruzione SQL e si prova contro un
// database vero in internal/billingpg. Qui si prova ciò che decide questo
// package: quale piano, per quale utente, e se l'evento va applicato.
type registro struct {
	filigrane map[string]time.Time
	utenti    map[string]string // sottoscrizione Paddle -> utente
	// pianoPerUtente è il piano in forza: serve a restituire
	// `PreviousPlanCode`, che è ciò che permette di distinguere una variazione
	// da un rinnovo.
	pianoPerUtente map[string]string
	piani          map[string]billing.PlanSummary

	scritture []billing.SubscriptionChange
	limiti    []string // piani contro cui R58 è stato applicato
	intenti   []billing.CheckoutIntent

	sospensione  billing.Suspension
	erroreIntent error
}

func nuovoRegistro() *registro {
	return &registro{
		filigrane:      map[string]time.Time{},
		utenti:         map[string]string{},
		pianoPerUtente: map[string]string{},
		piani: map[string]billing.PlanSummary{
			"free":   {Code: "free", Name: "Free", IsPublic: true},
			"pro":    {Code: "pro", Name: "Pro", IsPublic: true},
			"team":   {Code: "team", Name: "Team", IsPublic: true},
			"agency": {Code: "agency", Name: "Agency", IsPublic: true},
		},
	}
}

func (r *registro) SaveSubscription(_ context.Context, change billing.SubscriptionChange) (billing.SaveResult, error) {
	if ultima, visto := r.filigrane[change.PaddleSubscriptionID]; visto && change.OccurredAt.Before(ultima) {
		return billing.SaveResult{Applied: false}, nil
	}

	userID := change.UserID
	if userID == "" {
		userID = r.utenti[change.PaddleSubscriptionID]
	}
	if userID == "" {
		return billing.SaveResult{}, billing.ErrUnknownSubscriber
	}

	precedente, aveva := r.pianoPerUtente[userID]
	if !aveva {
		precedente = paddle.PlanFree
	}

	r.filigrane[change.PaddleSubscriptionID] = change.OccurredAt
	r.utenti[change.PaddleSubscriptionID] = userID
	r.pianoPerUtente[userID] = change.PlanCode
	r.scritture = append(r.scritture, change)
	return billing.SaveResult{
		Applied:          true,
		UserID:           userID,
		PlanCode:         change.PlanCode,
		PreviousPlanCode: precedente,
	}, nil
}

func (r *registro) EnforcePlanLimits(_ context.Context, _, planCode string, _ time.Time) (billing.Suspension, error) {
	r.limiti = append(r.limiti, planCode)
	return r.sospensione, nil
}

func (r *registro) Plan(_ context.Context, code string) (billing.PlanSummary, error) {
	piano, esiste := r.piani[code]
	if !esiste {
		return billing.PlanSummary{}, errors.New("piano inesistente")
	}
	return piano, nil
}

func (r *registro) RecordCheckoutIntent(_ context.Context, intent billing.CheckoutIntent) error {
	if r.erroreIntent != nil {
		return r.erroreIntent
	}
	r.intenti = append(r.intenti, intent)
	return nil
}

func (r *registro) Entitlement(_ context.Context, _ string) (billing.Entitlement, error) {
	return billing.Entitlement{PlanCode: "free", PlanName: "Free"}, nil
}

// ------------------------------------------------------------------ supporto

const (
	utente = "11111111-1111-4111-8111-111111111111"

	prezzoProMensile    = "pri_pro_m"
	prezzoProAnnuale    = "pri_pro_y"
	prezzoTeamMensile   = "pri_team_m"
	prezzoAgencyMensile = "pri_agency_m"
)

func catalogo(t *testing.T) paddle.Catalog {
	t.Helper()
	valori := map[string]string{
		"PADDLE_PRICE_PRO_MONTHLY":    prezzoProMensile,
		"PADDLE_PRICE_PRO_YEARLY":     prezzoProAnnuale,
		"PADDLE_PRICE_TEAM_MONTHLY":   prezzoTeamMensile,
		"PADDLE_PRICE_AGENCY_MONTHLY": prezzoAgencyMensile,
	}
	cat, err := paddle.CatalogFromEnv(func(k string) string { return valori[k] })
	if err != nil {
		t.Fatalf("catalogo: %v", err)
	}
	return cat
}

func servizio(t *testing.T, store billing.Store, cat paddle.Catalog) *billing.Service {
	t.Helper()
	svc, err := billing.NewService(billing.Options{
		Store:       store,
		Catalog:     cat,
		ClientToken: "live_token_di_prova",
		Environment: "sandbox",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:         func() time.Time { return time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("costruzione del servizio: %v", err)
	}
	return svc
}

func sottoscrizione(subID string, stato paddle.SubscriptionStatus, prezzo, userID string, quando time.Time) paddle.Subscription {
	sub := paddle.Subscription{
		Event:      paddle.Event{ID: "evt_" + subID, Type: paddle.EventSubscriptionUpdated, OccurredAt: quando},
		ID:         subID,
		CustomerID: "ctm_01",
		Status:     stato,
		UserID:     userID,
	}
	if prezzo != "" {
		sub.PriceIDs = []string{prezzo}
	}
	return sub
}

var (
	prima = time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	dopo  = time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC)
)

// -------------------------------------------------------------------- prove

func TestIlPrezzoDecideIlPiano(t *testing.T) {
	casi := map[string]struct {
		prezzo  string
		piano   string
		periodo paddle.Period
	}{
		"pro mensile":    {prezzoProMensile, paddle.PlanPro, paddle.PeriodMonthly},
		"pro annuale":    {prezzoProAnnuale, paddle.PlanPro, paddle.PeriodYearly},
		"team mensile":   {prezzoTeamMensile, paddle.PlanTeam, paddle.PeriodMonthly},
		"agency mensile": {prezzoAgencyMensile, paddle.PlanAgency, paddle.PeriodMonthly},
	}

	for nome, caso := range casi {
		t.Run(nome, func(t *testing.T) {
			store := nuovoRegistro()
			svc := servizio(t, store, catalogo(t))

			applicato, err := svc.ApplySubscription(context.Background(),
				sottoscrizione("sub_01", paddle.SubscriptionActive, caso.prezzo, utente, prima))
			if err != nil {
				t.Fatalf("applicazione: %v", err)
			}
			if !applicato {
				t.Fatal("la sottoscrizione doveva essere applicata")
			}
			scritta := store.scritture[0]
			if scritta.PlanCode != caso.piano || scritta.Period != caso.periodo {
				t.Fatalf("piano = %s/%s, atteso %s/%s",
					scritta.PlanCode, scritta.Period, caso.piano, caso.periodo)
			}
		})
	}
}

// `past_due` mantiene il piano: i Termini §4.2 promettono che durante i
// tentativi di Paddle il servizio continua. `paused` e `canceled` portano al
// Free, con tutto ciò che R58 prescrive.
func TestStatoNonPaganteRiportaAlFree(t *testing.T) {
	casi := map[paddle.SubscriptionStatus]string{
		paddle.SubscriptionActive:   paddle.PlanPro,
		paddle.SubscriptionPastDue:  paddle.PlanPro,
		paddle.SubscriptionTrialing: paddle.PlanPro,
		paddle.SubscriptionPaused:   paddle.PlanFree,
		paddle.SubscriptionCanceled: paddle.PlanFree,
	}

	for stato, atteso := range casi {
		t.Run(string(stato), func(t *testing.T) {
			store := nuovoRegistro()
			svc := servizio(t, store, catalogo(t))

			if _, err := svc.ApplySubscription(context.Background(),
				sottoscrizione("sub_01", stato, prezzoProMensile, utente, prima)); err != nil {
				t.Fatalf("applicazione: %v", err)
			}

			scritta := store.scritture[0]
			if scritta.PlanCode != atteso {
				t.Fatalf("piano = %q, atteso %q", scritta.PlanCode, atteso)
			}
			// Lo stato scritto resta quello vero di Paddle: guardando la riga si
			// deve poter distinguere una disdetta da una sospensione del pagamento.
			if scritta.Status != string(stato) {
				t.Errorf("stato scritto = %q, atteso %q", scritta.Status, stato)
			}
			// R58 si applica contro il piano di destinazione, sempre: se i job ci
			// stanno già, l'istruzione non tocca niente.
			if len(store.limiti) != 1 || store.limiti[0] != atteso {
				t.Errorf("R58 applicato contro %v, atteso [%s]", store.limiti, atteso)
			}
		})
	}
}

// Una cancellazione non ha bisogno di un prezzo in catalogo per essere applicata:
// la destinazione è il Free, e il Free non si compra.
func TestCancellazioneSenzaPrezzoRiconosciuto(t *testing.T) {
	store := nuovoRegistro()
	store.utenti["sub_01"] = utente
	svc := servizio(t, store, catalogo(t))

	applicato, err := svc.ApplySubscription(context.Background(),
		sottoscrizione("sub_01", paddle.SubscriptionCanceled, "pri_mai_visto", "", prima))
	if err != nil {
		t.Fatalf("applicazione: %v", err)
	}
	if !applicato {
		t.Fatal("una cancellazione va sempre applicata")
	}
	if store.scritture[0].PlanCode != paddle.PlanFree {
		t.Fatalf("piano = %q", store.scritture[0].PlanCode)
	}
}

// Un prezzo fuori catalogo non ha un ripiego: assegnare un piano a caso sarebbe
// peggio che non assegnarne nessuno. Fallire fa ripetere Paddle, e la
// ripetizione applica correttamente una volta corretta la configurazione.
func TestPrezzoSconosciutoFallisceRumorosamente(t *testing.T) {
	store := nuovoRegistro()
	svc := servizio(t, store, catalogo(t))

	_, err := svc.ApplySubscription(context.Background(),
		sottoscrizione("sub_01", paddle.SubscriptionActive, "pri_di_un_altro_ambiente", utente, prima))
	if !errors.Is(err, billing.ErrUnknownPrice) {
		t.Fatalf("atteso ErrUnknownPrice, ottenuto %v", err)
	}
	if len(store.scritture) != 0 {
		t.Error("nessun piano doveva essere scritto")
	}
}

// L'evento fuori ordine non applica niente — e soprattutto **non applica R58**:
// sospendere i job sulla base di un piano superato è il danno peggiore che
// questo package possa fare.
func TestEventoFuoriOrdineNonApplicaNemmenoR58(t *testing.T) {
	store := nuovoRegistro()
	svc := servizio(t, store, catalogo(t))

	if _, err := svc.ApplySubscription(context.Background(),
		sottoscrizione("sub_01", paddle.SubscriptionCanceled, prezzoProMensile, utente, dopo)); err != nil {
		t.Fatalf("evento recente: %v", err)
	}

	applicato, err := svc.ApplySubscription(context.Background(),
		sottoscrizione("sub_01", paddle.SubscriptionActive, prezzoTeamMensile, utente, prima))
	if err != nil {
		t.Fatalf("evento vecchio: %v", err)
	}
	if applicato {
		t.Fatal("un evento più vecchio non va applicato")
	}
	if len(store.scritture) != 1 {
		t.Errorf("scritture = %d, attesa 1", len(store.scritture))
	}
	if len(store.limiti) != 1 {
		t.Errorf("R58 applicato %d volte, attesa 1", len(store.limiti))
	}
}

// Una sottoscrizione senza `custom_data.user_id` si attribuisce dalla riga già
// legata a quell'identificativo Paddle. Senza nemmeno quella, non si indovina.
func TestSottoscrizioneSenzaUtente(t *testing.T) {
	store := nuovoRegistro()
	svc := servizio(t, store, catalogo(t))

	_, err := svc.ApplySubscription(context.Background(),
		sottoscrizione("sub_ignota", paddle.SubscriptionActive, prezzoProMensile, "", prima))
	if !errors.Is(err, billing.ErrUnknownSubscriber) {
		t.Fatalf("atteso ErrUnknownSubscriber, ottenuto %v", err)
	}

	store.utenti["sub_nota"] = utente
	applicato, err := svc.ApplySubscription(context.Background(),
		sottoscrizione("sub_nota", paddle.SubscriptionActive, prezzoProMensile, "", prima))
	if err != nil || !applicato {
		t.Fatalf("applicazione con utente ricavato dalla sottoscrizione: %v", err)
	}
}

// ---------------------------------------------------------------- checkout

// R63: la conferma di uso professionale è la condizione dell'acquisto. Senza,
// non si arriva nemmeno a sapere quali piani esistono.
func TestCheckoutSenzaConfermaProfessionale(t *testing.T) {
	store := nuovoRegistro()
	svc := servizio(t, store, catalogo(t))

	_, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
		PlanCode: paddle.PlanPro, Period: paddle.PeriodMonthly, BusinessUse: false,
	})
	if !errors.Is(err, billing.ErrBusinessUseRequired) {
		t.Fatalf("atteso ErrBusinessUseRequired, ottenuto %v", err)
	}
	if len(store.intenti) != 0 {
		t.Error("un checkout rifiutato non registra un intento")
	}
}

// R62: l'annuale esiste solo su Pro. Il messaggio distingue il caso da un piano
// che non è in vendita affatto, perché i rimedi sono diversi.
func TestAnnualeSoloSuPro(t *testing.T) {
	store := nuovoRegistro()
	svc := servizio(t, store, catalogo(t))

	for _, piano := range []string{paddle.PlanTeam, paddle.PlanAgency} {
		_, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
			PlanCode: piano, Period: paddle.PeriodYearly, BusinessUse: true,
		})
		if !errors.Is(err, billing.ErrPeriodNotAvailable) {
			t.Errorf("%s annuale: atteso ErrPeriodNotAvailable, ottenuto %v", piano, err)
		}
	}

	esito, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
		PlanCode: paddle.PlanPro, Period: paddle.PeriodYearly, BusinessUse: true,
	})
	if err != nil {
		t.Fatalf("pro annuale rifiutato: %v", err)
	}
	if esito.PriceID != prezzoProAnnuale {
		t.Fatalf("prezzo = %q", esito.PriceID)
	}
}

// Il piano Free non è un acquisto: nessun prezzo pagato, nessuna cifra da
// esporre (R63). E R59 dice che non esistono prove gratuite da vendere.
func TestIlFreeNonSiCompra(t *testing.T) {
	svc := servizio(t, nuovoRegistro(), catalogo(t))

	for _, piano := range []string{paddle.PlanFree, "", "piano_inventato"} {
		_, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
			PlanCode: piano, Period: paddle.PeriodMonthly, BusinessUse: true,
		})
		if !errors.Is(err, billing.ErrPlanNotPurchasable) {
			t.Errorf("piano %q: atteso ErrPlanNotPurchasable, ottenuto %v", piano, err)
		}
	}
}

// Un piano ritirato dal listino resta assegnabile a mano dall'area admin ma non
// si compra più (0003).
func TestPianoNonPubblicoNonSiCompra(t *testing.T) {
	store := nuovoRegistro()
	store.piani["team"] = billing.PlanSummary{Code: "team", Name: "Team", IsPublic: false}
	svc := servizio(t, store, catalogo(t))

	_, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
		PlanCode: paddle.PlanTeam, Period: paddle.PeriodMonthly, BusinessUse: true,
	})
	if !errors.Is(err, billing.ErrPlanNotPurchasable) {
		t.Fatalf("atteso ErrPlanNotPurchasable, ottenuto %v", err)
	}
}

// La dichiarazione di R63 va registrata, con la partita IVA se c'è. Il checkout
// non restituisce importi: quelli sono di Paddle.
func TestCheckoutRegistraLaDichiarazione(t *testing.T) {
	store := nuovoRegistro()
	svc := servizio(t, store, catalogo(t))

	esito, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
		PlanCode:    paddle.PlanTeam,
		Period:      paddle.PeriodMonthly,
		BusinessUse: true,
		VATNumber:   "  IT03835250162  ",
	})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}

	if len(store.intenti) != 1 {
		t.Fatalf("intenti registrati = %d, atteso 1", len(store.intenti))
	}
	intento := store.intenti[0]
	switch {
	case intento.UserID != utente:
		t.Errorf("utente = %q", intento.UserID)
	case !intento.BusinessUse:
		t.Error("la conferma non è stata registrata")
	case intento.VATNumber != "IT03835250162":
		t.Errorf("partita IVA = %q: gli spazi ai bordi vanno tolti", intento.VATNumber)
	case intento.PriceID != prezzoTeamMensile:
		t.Errorf("prezzo = %q", intento.PriceID)
	}

	switch {
	case esito.CustomData.UserID != utente:
		t.Errorf("custom_data = %+v: senza, un pagamento riuscito resta senza destinatario", esito.CustomData)
	case esito.ClientToken != "live_token_di_prova":
		t.Errorf("token del checkout = %q", esito.ClientToken)
	case esito.Environment != "sandbox":
		t.Errorf("ambiente = %q", esito.Environment)
	case esito.PlanName != "Team":
		t.Errorf("nome del piano = %q", esito.PlanName)
	}
}

// La partita IVA non è obbligatoria: diversi regimi minimi europei ne sono
// privi, e pretenderla escluderebbe acquirenti legittimi (R63).
func TestPartitaIVAFacoltativa(t *testing.T) {
	store := nuovoRegistro()
	svc := servizio(t, store, catalogo(t))

	if _, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
		PlanCode: paddle.PlanPro, Period: paddle.PeriodMonthly, BusinessUse: true,
	}); err != nil {
		t.Fatalf("checkout senza partita IVA rifiutato: %v", err)
	}
	if store.intenti[0].VATNumber != "" {
		t.Errorf("partita IVA = %q, attesa vuota", store.intenti[0].VATNumber)
	}

	_, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
		PlanCode: paddle.PlanPro, Period: paddle.PeriodMonthly, BusinessUse: true,
		VATNumber: string(make([]byte, 0, 100)) + "IT" + longString(80),
	})
	if !errors.Is(err, billing.ErrInvalidVATNumber) {
		t.Fatalf("atteso ErrInvalidVATNumber, ottenuto %v", err)
	}
}

// Una dichiarazione non registrata è una dichiarazione che non c'è stata:
// proseguire lascerebbe comprare senza la traccia che R63 pretende.
func TestIntentoNonRegistratoFermaIlCheckout(t *testing.T) {
	store := nuovoRegistro()
	store.erroreIntent = errors.New("database irraggiungibile")
	svc := servizio(t, store, catalogo(t))

	if _, err := svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
		PlanCode: paddle.PlanPro, Period: paddle.PeriodMonthly, BusinessUse: true,
	}); err == nil {
		t.Fatal("atteso un errore")
	}
}

// Senza catalogo non c'è niente da vendere, e la rotta non va nemmeno
// registrata: [billing.Service.CanSell] è ciò che lo dice a chi la registra.
func TestSenzaCatalogoNonSiVende(t *testing.T) {
	vuoto, err := paddle.CatalogFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("catalogo vuoto: %v", err)
	}
	svc := servizio(t, nuovoRegistro(), vuoto)

	if svc.CanSell() {
		t.Error("CanSell() vero senza catalogo")
	}
	_, err = svc.Checkout(context.Background(), utente, billing.CheckoutRequest{
		PlanCode: paddle.PlanPro, Period: paddle.PeriodMonthly, BusinessUse: true,
	})
	if !errors.Is(err, billing.ErrNotConfigured) {
		t.Fatalf("atteso ErrNotConfigured, ottenuto %v", err)
	}

	// Il catalogo assente non impedisce però di **applicare** gli eventi: una
	// macchina che riceve webhook e non sa tradurli deve fallire rumorosamente.
	if _, err := svc.ApplySubscription(context.Background(),
		sottoscrizione("sub_01", paddle.SubscriptionActive, prezzoProMensile, utente, prima)); !errors.Is(err, billing.ErrUnknownPrice) {
		t.Fatalf("atteso ErrUnknownPrice, ottenuto %v", err)
	}
}

func longString(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 'X'
	}
	return string(buf)
}
