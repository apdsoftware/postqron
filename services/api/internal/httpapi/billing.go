package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// Il contratto delle rotte di fatturazione (R16), in breve.
//
//	GET  /billing/subscription   piano in forza e job fermi   200
//	POST /billing/checkout       apertura del checkout        200
//
// # Cosa queste rotte non fanno
//
// **Non restituiscono importi.** Paddle è Merchant of Record: i prezzi sono in
// euro, al netto delle imposte, e la loro fonte di verità è il catalogo Paddle
// (R61, R61-bis, Termini §1). Una cifra restituita da qui sarebbe una seconda
// copia libera di divergere da quella che il cliente paga davvero — e la nostra
// sarebbe quella sbagliata, perché l'imposta la calcola Paddle sul paese del
// cliente e noi quel paese non lo conosciamo nemmeno.
//
// **Non emettono documenti fiscali e non ne servono.** Fatture e ricevute sono
// di Paddle (R60): l'accesso ai documenti passa dal suo portale, non da noi.
//
// **Non chiamano Paddle.** Il checkout di Paddle Billing si apre nel browser con
// Paddle.js a partire da un `price_id` e da un token pubblico: qui non c'è una
// chiamata di rete, quindi questa rotta non può fallire perché un servizio
// esterno è lento. Il collaudo di un pagamento vero in sandbox è un'altra cosa,
// e sta nella issue #478.
//
// Codici di errore, stabili e pensati per il branching applicativo (R53):
//
//	400 invalid_request          corpo illeggibile o campo sconosciuto
//	413 body_too_large           corpo oltre il tetto
//	401 unauthenticated          sessione assente o scaduta
//	409 business_use_required    manca la conferma di uso professionale (R63)
//	409 plan_not_purchasable     il piano non è in vendita (Free, o ritirato)
//	409 period_not_available     quel piano non ha quella periodicità (R62)
//	503 billing_unavailable      nessun catalogo Paddle su questa macchina

// billingAPI raccoglie le rotte di fatturazione.
type billingAPI struct {
	*guard
	svc *billing.Service
	log *slog.Logger
}

func newBillingAPI(guard *guard, logger *slog.Logger, svc *billing.Service) *billingAPI {
	return &billingAPI{guard: guard, svc: svc, log: logger}
}

// routes registra le rotte di fatturazione.
//
// **Entrambe esigono una sessione** (`authenticated`), non una Identity, ed è la
// stessa scelta delle rotte `/keys` e `/secrets`. Qui il motivo è il più diretto
// dei tre: `POST /billing/checkout` è il primo passo di un impegno di spesa, e
// raccoglie la dichiarazione con cui l'utente afferma di agire nell'esercizio di
// un'attività (R63). Una dichiarazione resa da una chiave API non è una
// dichiarazione dell'utente — è una dichiarazione di chi ha la chiave, che può
// essere un'integrazione scritta mesi prima da qualcun altro.
func (a *billingAPI) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /billing/subscription", a.authenticated(a.subscription))
	mux.HandleFunc("POST /billing/checkout", a.authenticated(a.checkout))
}

// ------------------------------------------------------------------- corpi

// checkoutRequest è la richiesta di aprire un checkout.
type checkoutRequest struct {
	Plan   string `json:"plan"`
	Period string `json:"period"`

	// BusinessUse è la conferma di R63. Il campo è **obbligatoriamente esplicito**
	// e non ha un default utile: lo zero di un booleano JSON assente è `false`,
	// che è il valore che rifiuta. È deliberato — R63 chiede «conferma esplicita
	// di agire nell'esercizio di un'attività, non una casella preselezionata
	// sepolta nel modulo», e un campo il cui default accetta sarebbe proprio
	// quella casella.
	BusinessUse bool `json:"business_use"`

	// VATNumber è facoltativo: si conferma sempre lo status, si raccoglie il
	// numero quando c'è (R63).
	VATNumber string `json:"vat_number"`
}

// CheckoutResponse è ciò che serve al browser per aprire il checkout.
//
// Non contiene importi: vedi la nota in testa al file.
type CheckoutResponse struct {
	Plan     string `json:"plan"`
	PlanName string `json:"plan_name"`
	Period   string `json:"period"`

	// PriceID è la riga del catalogo Paddle da aprire. È un riferimento, non un
	// prezzo.
	PriceID string `json:"price_id"`

	// Environment e ClientToken sono i due valori con cui Paddle.js si collega.
	// Il token è **pubblico per costruzione** — vive nel codice della pagina — e
	// non va confuso con la chiave API server-side, che non esce mai da qui.
	Environment string `json:"environment"`
	ClientToken string `json:"client_token"`

	// CustomData è ciò che Paddle ci rimanderà indietro nei webhook: è il legame
	// fra il pagamento e l'account, e il client deve passarlo a Paddle.js così
	// com'è.
	CustomData billing.CustomData `json:"custom_data"`
}

// SubscriptionResponse è il piano in forza, con i job che un cambio di piano ha
// spento.
//
// I due conteggi dei sospesi stanno qui e non in una rotta a sé perché è insieme
// che servono: R58 dice che l'interfaccia deve *dire* cosa è successo, e uno
// stato del piano che non nomina i job fermi è esattamente il modo in cui
// l'utente scopre il problema quando il job che gli serviva non è partito.
type SubscriptionResponse struct {
	Plan     string `json:"plan"`
	PlanName string `json:"plan_name"`
	Status   string `json:"status"`
	Period   string `json:"period,omitempty"`

	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
	// CancelAt è la disdetta programmata. I Termini §4.1 dicono che ha effetto
	// alla fine del periodo pagato: fino ad allora il piano resta questo, e il
	// client deve poterlo dire invece di far credere che sia già finito.
	CancelAt *time.Time `json:"cancel_at,omitempty"`

	// MaxJobs è il tetto del piano; assente significa nessun tetto rigido.
	MaxJobs    *int `json:"max_jobs,omitempty"`
	ActiveJobs int  `json:"active_jobs"`

	Suspended SuspendedJobsResponse `json:"suspended_jobs"`
}

// SuspendedJobsResponse conta i job spenti da un cambio di piano, per motivo.
//
// Due numeri e non uno, perché i rimedi sono due: a chi ne ha per il tetto si
// chiede di sceglierne alcuni da riaccendere, a chi ne ha per la risoluzione si
// chiede di cambiare la schedulazione. Un totale unico costringerebbe il client
// a dire una delle due cose a tutti.
type SuspendedJobsResponse struct {
	ByJobLimit   int `json:"by_job_limit"`
	ByResolution int `json:"by_resolution"`
	Total        int `json:"total"`
}

// ------------------------------------------------------------------ gestori

// subscription restituisce il piano in forza.
func (a *billingAPI) subscription(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	ent, err := a.svc.Entitlement(r.Context(), user.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	writeJSON(w, r, a.log, http.StatusOK, SubscriptionResponse{
		Plan:             ent.PlanCode,
		PlanName:         ent.PlanName,
		Status:           ent.Status,
		Period:           string(ent.Period),
		CurrentPeriodEnd: ent.CurrentPeriodEnd,
		CancelAt:         ent.CancelAt,
		MaxJobs:          ent.MaxJobs,
		ActiveJobs:       ent.ActiveJobs,
		Suspended: SuspendedJobsResponse{
			ByJobLimit:   ent.Suspended.ByJobLimit,
			ByResolution: ent.Suspended.ByResolution,
			Total:        ent.Suspended.Total(),
		},
	})
}

// checkout valida la richiesta e restituisce ciò che serve ad aprire il
// checkout.
func (a *billingAPI) checkout(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	var body checkoutRequest
	if err := decodeJSON(w, r, &body); err != nil {
		writeDecodeError(w, r, a.log, err)
		return
	}

	esito, err := a.svc.Checkout(r.Context(), user.ID, billing.CheckoutRequest{
		PlanCode:    body.Plan,
		Period:      paddle.Period(body.Period),
		BusinessUse: body.BusinessUse,
		VATNumber:   body.VATNumber,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	writeJSON(w, r, a.log, http.StatusOK, CheckoutResponse{
		Plan:        esito.PlanCode,
		PlanName:    esito.PlanName,
		Period:      string(esito.Period),
		PriceID:     esito.PriceID,
		Environment: esito.Environment,
		ClientToken: esito.ClientToken,
		CustomData:  esito.CustomData,
	})
}

// fail traduce gli errori del servizio in status.
//
// I messaggi sono espliciti perché il rimedio è diverso in ogni caso, ed è
// quello che l'utente ha bisogno di sapere: mancare la conferma di R63 si
// risolve spuntando una casella, un annuale su Team non si risolve affatto —
// quel piano l'annuale non ce l'ha, ed è una scelta dichiarata (R62), non una
// lacuna da aggirare riprovando.
func (a *billingAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, billing.ErrBusinessUseRequired):
		writeError(w, r, a.log, http.StatusConflict, "business_use_required",
			"I piani a pagamento sono offerti per uso professionale: conferma di agire nell'esercizio di un'attività per procedere.")

	case errors.Is(err, billing.ErrPeriodNotAvailable):
		writeError(w, r, a.log, http.StatusConflict, "period_not_available",
			"Questo piano non ha quella periodicità: la fatturazione annuale esiste solo sul piano Pro.")

	case errors.Is(err, billing.ErrPlanNotPurchasable):
		writeError(w, r, a.log, http.StatusConflict, "plan_not_purchasable",
			"Questo piano non è acquistabile. Il piano Free è l'ingresso e non si compra.")

	case errors.Is(err, billing.ErrInvalidVATNumber):
		writeError(w, r, a.log, http.StatusBadRequest, "validation_failed",
			"Partita IVA non valida.")

	case errors.Is(err, billing.ErrNotConfigured):
		// 503 e non 500: non è un guasto, è una macchina su cui la vendita non è
		// configurata. La distinzione conta per chi legge i log di uno sviluppo.
		a.log.WarnContext(r.Context(), "checkout richiesto senza catalogo Paddle configurato")
		writeError(w, r, a.log, http.StatusServiceUnavailable, "billing_unavailable",
			"La fatturazione non è disponibile su questa istanza.")

	default:
		if handled := writeAuthError(w, r, a.log, err); handled {
			return
		}
		a.log.ErrorContext(r.Context(), "errore nelle rotte di fatturazione", slog.Any("error", err))
		writeError(w, r, a.log, http.StatusInternalServerError, "internal_error",
			"Errore interno. Riprova più tardi.")
	}
}
