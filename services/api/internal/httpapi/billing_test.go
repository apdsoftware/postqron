package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// ---------------------------------------------------------------- impalcatura

// registroFinto è un [billing.Store] in memoria: qui si prova il livello HTTP —
// codici, corpi, chi può chiamare — non la persistenza, che ha i suoi test
// contro un database vero in internal/billingpg.
type registroFinto struct {
	intenti []billing.CheckoutIntent
	ent     billing.Entitlement
}

func (r *registroFinto) SaveSubscription(context.Context, billing.SubscriptionChange) (billing.SaveResult, error) {
	return billing.SaveResult{}, nil
}

func (r *registroFinto) EnforcePlanLimits(context.Context, string, string, time.Time) (billing.Suspension, error) {
	return billing.Suspension{}, nil
}

func (r *registroFinto) Plan(_ context.Context, code string) (billing.PlanSummary, error) {
	nomi := map[string]string{"free": "Free", "pro": "Pro", "team": "Team", "agency": "Agency"}
	return billing.PlanSummary{Code: code, Name: nomi[code], IsPublic: true}, nil
}

func (r *registroFinto) RecordCheckoutIntent(_ context.Context, intent billing.CheckoutIntent) error {
	r.intenti = append(r.intenti, intent)
	return nil
}

func (r *registroFinto) Entitlement(context.Context, string) (billing.Entitlement, error) {
	return r.ent, nil
}

type billingFixture struct {
	*api
	store *registroFinto
	user  auth.User
	token string
}

// newBillingFixture costruisce un'API con la fatturazione configurata. `vendibile`
// falso riproduce la macchina senza catalogo Paddle.
func newBillingFixture(t *testing.T, vendibile bool) *billingFixture {
	t.Helper()

	store := &registroFinto{
		ent: billing.Entitlement{
			PlanCode: "free", PlanName: "Free", Status: "active",
			MaxJobs: intPtr(20), ActiveJobs: 0,
		},
	}
	a := newAPI(t, func(_ *config.Config, _ *auth.Options, deps *httpapi.Deps) {
		valori := map[string]string{}
		token := ""
		if vendibile {
			valori = map[string]string{
				"PADDLE_PRICE_PRO_MONTHLY":    "pri_pro_m",
				"PADDLE_PRICE_PRO_YEARLY":     "pri_pro_y",
				"PADDLE_PRICE_TEAM_MONTHLY":   "pri_team_m",
				"PADDLE_PRICE_AGENCY_MONTHLY": "pri_agency_m",
			}
			token = "test_client_token"
		}
		catalogo, err := paddle.CatalogFromEnv(func(k string) string { return valori[k] })
		if err != nil {
			t.Fatalf("catalogo: %v", err)
		}
		svc, err := billing.NewService(billing.Options{
			Store:       store,
			Catalog:     catalogo,
			ClientToken: token,
			Environment: "sandbox",
			Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("billing.NewService: %v", err)
		}
		deps.Billing = svc
	})

	user, sessione := a.registerAndLogin()
	return &billingFixture{api: a, store: store, user: user, token: sessione}
}

func intPtr(v int) *int { return &v }

func (f *billingFixture) checkout(corpo map[string]any, prepare ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do(http.MethodPost, "/billing/checkout", corpo, prepare...)
}

// checkout2 è checkout per i corpi che una mappa non sa esprimere: JSON
// troncato, o più grande del tetto. `do` codifica ciò che riceve, e una
// json.RawMessage codifica se stessa.
func (f *billingFixture) checkout2(corpo any, prepare ...func(*http.Request)) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.do(http.MethodPost, "/billing/checkout", corpo, prepare...)
}

// -------------------------------------------------------------------- prove

// Il checkout restituisce ciò che serve a Paddle.js e **nessun importo**: i
// prezzi sono di Paddle, in euro e al netto delle imposte (R61, R61-bis), e una
// cifra restituita da qui sarebbe una seconda copia libera di divergere da
// quella che il cliente paga.
func TestCheckoutNonRestituisceImporti(t *testing.T) {
	f := newBillingFixture(t, true)

	rec := f.checkout(map[string]any{
		"plan": "pro", "period": "monthly", "business_use": true,
	}, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}

	var body httpapi.CheckoutResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	switch {
	case body.PriceID != "pri_pro_m":
		t.Errorf("price_id = %q", body.PriceID)
	case body.ClientToken != "test_client_token":
		t.Errorf("client_token = %q", body.ClientToken)
	case body.Environment != "sandbox":
		t.Errorf("environment = %q", body.Environment)
	case body.CustomData.UserID != f.user.ID:
		t.Errorf("custom_data = %+v: senza, un pagamento riuscito resta senza destinatario", body.CustomData)
	}

	// Nessun campo di valuta nella risposta grezza: un importo qui sarebbe un
	// difetto anche se corretto oggi.
	var grezzo map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &grezzo); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	for _, vietato := range []string{"price", "amount", "currency", "total", "vat", "tax"} {
		if _, presente := grezzo[vietato]; presente {
			t.Errorf("la risposta contiene %q: gli importi sono di Paddle (R61)", vietato)
		}
	}
}

// R63: senza la conferma di uso professionale non si compra. Il campo non ha un
// default che accetta — un booleano assente vale `false`, che rifiuta — perché
// una casella preselezionata non è una conferma esplicita.
func TestCheckoutSenzaConfermaProfessionale(t *testing.T) {
	f := newBillingFixture(t, true)

	casi := map[string]map[string]any{
		"conferma negata":   {"plan": "pro", "period": "monthly", "business_use": false},
		"campo non mandato": {"plan": "pro", "period": "monthly"},
	}

	for nome, corpo := range casi {
		t.Run(nome, func(t *testing.T) {
			rec := f.checkout(corpo, withCookie(f.token))
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, atteso 409: %s", rec.Code, rec.Body.String())
			}
			var errore httpapi.ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &errore); err != nil {
				t.Fatalf("decodifica: %v", err)
			}
			if errore.Error.Code != "business_use_required" {
				t.Errorf("codice = %q", errore.Error.Code)
			}
		})
	}

	if len(f.store.intenti) != 0 {
		t.Error("un checkout rifiutato non registra un intento")
	}
}

// R62: l'annuale esiste solo su Pro, e il messaggio lo dice. Team e Agency sono
// esclusivamente mensili per scelta dichiarata, non per una lacuna da aggirare
// riprovando.
func TestCheckoutAnnualeSoloSuPro(t *testing.T) {
	f := newBillingFixture(t, true)

	for _, piano := range []string{"team", "agency"} {
		rec := f.checkout(map[string]any{
			"plan": piano, "period": "yearly", "business_use": true,
		}, withCookie(f.token))
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s annuale: status = %d, atteso 409: %s", piano, rec.Code, rec.Body.String())
		}
		var errore httpapi.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &errore); err != nil {
			t.Fatalf("decodifica: %v", err)
		}
		if errore.Error.Code != "period_not_available" {
			t.Errorf("%s: codice = %q", piano, errore.Error.Code)
		}
	}

	rec := f.checkout(map[string]any{
		"plan": "pro", "period": "yearly", "business_use": true,
	}, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("pro annuale: status = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}
}

// Il piano Free non è un acquisto: nessun prezzo pagato, nessuna cifra da
// esporre (R63), e R59 dice che non esistono prove gratuite da vendere.
func TestCheckoutDelPianoFree(t *testing.T) {
	f := newBillingFixture(t, true)

	rec := f.checkout(map[string]any{
		"plan": "free", "period": "monthly", "business_use": true,
	}, withCookie(f.token))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, atteso 409: %s", rec.Code, rec.Body.String())
	}
	var errore httpapi.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errore); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	if errore.Error.Code != "plan_not_purchasable" {
		t.Errorf("codice = %q", errore.Error.Code)
	}
}

// La dichiarazione di R63 va registrata, con la partita IVA se c'è: una conferma
// che non lascia traccia non è opponibile a nessuno.
func TestCheckoutRegistraLaDichiarazione(t *testing.T) {
	f := newBillingFixture(t, true)

	rec := f.checkout(map[string]any{
		"plan": "team", "period": "monthly", "business_use": true,
		"vat_number": "IT03835250162",
	}, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	if len(f.store.intenti) != 1 {
		t.Fatalf("intenti registrati = %d, atteso 1", len(f.store.intenti))
	}
	intento := f.store.intenti[0]
	switch {
	case intento.UserID != f.user.ID:
		t.Errorf("utente = %q", intento.UserID)
	case !intento.BusinessUse:
		t.Error("conferma non registrata")
	case intento.VATNumber != "IT03835250162":
		t.Errorf("partita IVA = %q", intento.VATNumber)
	}
}

// Senza sessione non si compra: la dichiarazione di R63 è dell'utente, e una
// resa da una chiave API sarebbe la dichiarazione di chi ha la chiave.
func TestCheckoutRichiedeLaSessione(t *testing.T) {
	f := newBillingFixture(t, true)

	rec := f.checkout(map[string]any{
		"plan": "pro", "period": "monthly", "business_use": true,
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, atteso 401: %s", rec.Code, rec.Body.String())
	}
}

// Su una macchina senza catalogo non si vende, e la risposta lo dice con un 503:
// non è un guasto, è una configurazione assente.
func TestCheckoutSenzaCatalogo(t *testing.T) {
	f := newBillingFixture(t, false)

	rec := f.checkout(map[string]any{
		"plan": "pro", "period": "monthly", "business_use": true,
	}, withCookie(f.token))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, atteso 503: %s", rec.Code, rec.Body.String())
	}

	// Il piano in forza si legge comunque: senza catalogo non si compra, ma
	// l'utente ha comunque un piano.
	stato := f.do(http.MethodGet, "/billing/subscription", nil, withCookie(f.token))
	if stato.Code != http.StatusOK {
		t.Fatalf("GET /billing/subscription = %d: %s", stato.Code, stato.Body.String())
	}
}

// R58: lo stato del piano nomina i job fermi, e li distingue per motivo — perché
// i rimedi sono due. Un totale unico costringerebbe il client a dire una delle
// due cose a tutti.
func TestStatoDelPianoNominaIJobFermi(t *testing.T) {
	f := newBillingFixture(t, true)
	fine := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f.store.ent = billing.Entitlement{
		PlanCode: "free", PlanName: "Free", Status: "active",
		MaxJobs: intPtr(20), ActiveJobs: 3,
		CurrentPeriodEnd: &fine,
		Suspended:        billing.Suspension{ByJobLimit: 20, ByResolution: 5},
	}

	rec := f.do(http.MethodGet, "/billing/subscription", nil, withCookie(f.token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var body httpapi.SubscriptionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodifica: %v", err)
	}
	switch {
	case body.Plan != "free" || body.PlanName != "Free":
		t.Errorf("piano = %s/%s", body.Plan, body.PlanName)
	case body.Suspended.ByJobLimit != 20:
		t.Errorf("sospesi per tetto = %d", body.Suspended.ByJobLimit)
	case body.Suspended.ByResolution != 5:
		t.Errorf("sospesi per risoluzione = %d", body.Suspended.ByResolution)
	case body.Suspended.Total != 25:
		t.Errorf("totale = %d", body.Suspended.Total)
	case body.MaxJobs == nil || *body.MaxJobs != 20:
		t.Errorf("tetto = %v", body.MaxJobs)
	case body.ActiveJobs != 3:
		t.Errorf("job attivi = %d", body.ActiveJobs)
	}
}

// R15: i tre tetti che il piano impone — numero di job, risoluzione minima,
// retention dei log — si leggono **prima** di sbatterci.
//
// La prova gira sui quattro piani insieme e non su uno solo, perché il difetto
// che questo campo esiste per togliere è precisamente una tabella di listino
// scritta a mano da qualche parte: un client che ricevesse `min_interval`
// costante lo noterebbe solo confrontando due piani.
func TestStatoDelPianoDichiaraITettiDiR15(t *testing.T) {
	f := newBillingFixture(t, true)

	// I valori sono quelli della migrazione 0003, che è la fonte di verità del
	// listino: qui si prova che risalgano fino al corpo, non quali siano.
	casi := []struct {
		piano       string
		minInterval time.Duration
		retention   time.Duration
		atteso      string
		giorni      int
	}{
		{"free", time.Minute, 3 * 24 * time.Hour, "1m", 3},
		{"pro", 10 * time.Second, 15 * 24 * time.Hour, "10s", 15},
		{"team", time.Second, 30 * 24 * time.Hour, "1s", 30},
		{"agency", time.Second, 90 * 24 * time.Hour, "1s", 90},
	}

	for _, caso := range casi {
		t.Run(caso.piano, func(t *testing.T) {
			f.store.ent = billing.Entitlement{
				PlanCode: caso.piano, PlanName: caso.piano, Status: "active",
				MinInterval: caso.minInterval, LogRetention: caso.retention,
			}

			rec := f.do(http.MethodGet, "/billing/subscription", nil, withCookie(f.token))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}

			var body httpapi.SubscriptionResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decodifica: %v", err)
			}
			// La forma di `min_interval` è quella di `every` e di `timeout`: ciò
			// che il client legge lo può rimandare indietro senza convertirlo.
			if body.MinInterval != caso.atteso {
				t.Errorf("risoluzione minima = %q, attesa %q", body.MinInterval, caso.atteso)
			}
			if body.LogRetentionDays != caso.giorni {
				t.Errorf("retention = %d giorni, attesi %d", body.LogRetentionDays, caso.giorni)
			}
		})
	}
}

func TestStatoDelPianoRichiedeLaSessione(t *testing.T) {
	f := newBillingFixture(t, true)

	rec := f.do(http.MethodGet, "/billing/subscription", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, atteso 401", rec.Code)
	}
}

// Senza servizio le rotte non esistono: la dashboard non mostra il piano, e i
// job continuano a girare con gli entitlement che hanno.
func TestSenzaServizioLeRotteDiFatturazioneNonEsistono(t *testing.T) {
	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione: %v", err)
	}
	router := httpapi.NewRouter(cfg, "test",
		slog.New(slog.NewTextHandler(io.Discard, nil)), httpapi.Deps{})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/billing/subscription", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, atteso 404", rec.Code)
	}
}

// TestIlCheckoutDistingueUnCorpoTroppoGrandeDaUnCampoSbagliato è la prova del
// difetto trovato descrivendo l'API per il contratto OpenAPI (#465).
//
// `POST /billing/checkout` era l'unica delle sei rotte che leggono un corpo a
// non riconoscere `errBodyTooLarge`: schiacciava ogni errore di decodifica in un
// solo `400 validation_failed`. Le due conseguenze erano concrete — un client
// con un corpo troppo grande leggeva «i tuoi campi sono sbagliati» e avrebbe
// riprovato all'infinito con lo stesso corpo, e `validation_failed` arrivava
// senza il `details` per campo che quel codice promette altrove.
func TestIlCheckoutDistingueUnCorpoTroppoGrandeDaUnCampoSbagliato(t *testing.T) {
	f := newBillingFixture(t, true)

	casi := map[string]struct {
		corpo  any
		status int
		codice string
	}{
		"corpo oltre il tetto": {
			// 9 KiB: il tetto condiviso è 8 KiB (`maxRequestBody`, non esportato).
			// È lo stesso numero e la stessa ragione del caso in auth_test.go.
			json.RawMessage(`{"plan":"` + strings.Repeat("a", 9000) + `"}`),
			http.StatusRequestEntityTooLarge, "body_too_large",
		},
		"campo sconosciuto": {
			map[string]any{"plan": "pro", "period": "monthly", "business_use": true, "sconosciuto": 1},
			http.StatusBadRequest, "invalid_request",
		},
	}

	for nome, caso := range casi {
		t.Run(nome, func(t *testing.T) {
			rec := f.checkout2(caso.corpo, withCookie(f.token))
			if rec.Code != caso.status {
				t.Fatalf("status = %d, atteso %d: %s", rec.Code, caso.status, rec.Body.String())
			}
			if got := errorCode(t, rec); got != caso.codice {
				t.Errorf("codice = %q, atteso %q", got, caso.codice)
			}
		})
	}
}
