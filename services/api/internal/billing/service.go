package billing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// maxVATNumberLen è il limite della colonna `paddle_checkout_intents.vat_number`
// (0013). Fermare qui un valore troppo lungo costa un `if` e risparmia un INSERT
// rifiutato dal database su una rotta che l'utente vede.
const maxVATNumberLen = 64

// Options configura il [Service]. Store è obbligatorio.
type Options struct {
	Store Store

	// Catalog collega i prezzi Paddle ai piani. Un catalogo vuoto rende il
	// checkout non disponibile — [ErrNotConfigured] — ma **non** impedisce di
	// applicare gli eventi: un webhook che arriva su una macchina senza catalogo
	// deve fallire rumorosamente, non essere ignorato.
	Catalog paddle.Catalog

	// ClientToken è `PADDLE_CLIENT_TOKEN`: il token con cui Paddle.js apre il
	// checkout nel browser. È **pubblico per costruzione** — vive nel codice
	// della pagina — e restituirlo a un utente autenticato non espone niente che
	// non fosse già destinato al suo browser. Da non confondere con
	// `PADDLE_API_KEY`, che è server-side e non deve uscire da qui in nessun caso.
	ClientToken string

	// Environment è `PADDLE_ENVIRONMENT`: `sandbox` oppure `production`. Non
	// cambia nessuna decisione di questo package, ed è il valore con cui Paddle.js
	// sceglie a quale ambiente collegarsi — sbagliarlo è il modo più semplice di
	// aprire un checkout vero credendo di provare.
	Environment string

	Logger *slog.Logger

	// Now sostituisce l'orologio. Serve ai test.
	Now func() time.Time
}

// Service applica gli eventi Paddle agli entitlement e valida i checkout.
type Service struct {
	store       Store
	catalog     paddle.Catalog
	clientToken string
	environment string
	log         *slog.Logger
	now         func() time.Time
}

// NewService costruisce il servizio.
func NewService(opts Options) (*Service, error) {
	if opts.Store == nil {
		return nil, errors.New("billing: Store è obbligatorio")
	}
	s := &Service{
		store:       opts.Store,
		catalog:     opts.Catalog,
		clientToken: strings.TrimSpace(opts.ClientToken),
		environment: strings.TrimSpace(opts.Environment),
		log:         opts.Logger,
		now:         opts.Now,
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

// CanSell indica se su questa macchina c'è qualcosa da vendere: un catalogo con
// almeno un prezzo e il token del checkout. Serve a chi registra le rotte.
func (s *Service) CanSell() bool { return !s.catalog.Empty() && s.clientToken != "" }

var _ paddle.EntitlementSink = (*Service)(nil)

// ApplySubscription applica una sottoscrizione verificata (R16, R58).
//
// I passi sono due e vanno in quest'ordine: prima si scrive il piano, poi si
// applica R58. Invertirli significherebbe sospendere job contro un piano che
// potrebbe non essere quello che finisce in forza — per esempio perché l'evento
// era fuori ordine e la scrittura lo rifiuta.
//
// Il secondo valore di ritorno di [Store.SaveSubscription] è la ragione per cui
// R58 non viene applicato a ogni consegna: su un evento vecchio non c'è niente
// da far rispettare, e sospendere i job dell'utente sulla base di un piano
// superato sarebbe il danno peggiore che questo package può fare.
func (s *Service) ApplySubscription(ctx context.Context, sub paddle.Subscription) (bool, error) {
	change, err := s.translate(sub)
	if err != nil {
		// Fallire è deliberato, e la conseguenza va conosciuta: l'evento resta
		// registrato come `failed`, il livello HTTP risponde 500 e Paddle ripete.
		// L'alternativa — ignorare — lascerebbe un utente che ha pagato senza il
		// suo piano e nessun errore a dirlo. Un prezzo fuori catalogo è quasi
		// sempre una variabile PADDLE_PRICE_* mancante: si corregge e la
		// ripetizione lo applica.
		s.log.ErrorContext(ctx, "sottoscrizione Paddle non traducibile in un piano",
			slog.String("subscription_id", sub.ID),
			slog.String("event_id", sub.Event.ID),
			slog.Any("error", err))
		return false, err
	}

	result, err := s.store.SaveSubscription(ctx, change)
	if err != nil {
		return false, fmt.Errorf("billing: scrittura della sottoscrizione: %w", err)
	}
	if !result.Applied {
		return false, nil
	}

	suspension, err := s.store.EnforcePlanLimits(ctx, result.UserID, result.PlanCode, s.now())
	if err != nil {
		return false, fmt.Errorf("billing: applicazione dei limiti di piano: %w", err)
	}

	if suspension.Total() > 0 {
		// Va lasciato scritto: è un cambiamento che l'utente vede e su cui aprirà
		// un ticket se non capisce cosa è successo.
		s.log.InfoContext(ctx, "job sospesi da un cambio di piano (R58)",
			slog.String("user_id", result.UserID),
			slog.String("plan", result.PlanCode),
			slog.Int("per_tetto", suspension.ByJobLimit),
			slog.Int("per_risoluzione", suspension.ByResolution))
	}
	s.log.InfoContext(ctx, "piano aggiornato da Paddle",
		slog.String("user_id", result.UserID),
		slog.String("plan", result.PlanCode),
		slog.String("status", change.Status),
		slog.String("subscription_id", change.PaddleSubscriptionID))
	return true, nil
}

// translate converte una sottoscrizione Paddle in una scrittura.
//
// È l'unico punto in cui il catalogo viene consultato in ingresso, ed è anche
// dove si decide cosa significa uno stato che non dà diritto al piano.
func (s *Service) translate(sub paddle.Subscription) (SubscriptionChange, error) {
	change := SubscriptionChange{
		UserID:               sub.UserID,
		PaddleSubscriptionID: sub.ID,
		PaddleCustomerID:     sub.CustomerID,
		Status:               string(sub.Status),
		CurrentPeriodStart:   sub.CurrentPeriodStart,
		CurrentPeriodEnd:     sub.CurrentPeriodEnd,
		CanceledAt:           sub.CanceledAt,
		CancelAt:             sub.ScheduledCancelAt,
		OccurredAt:           sub.Event.OccurredAt,
	}

	if !sub.Status.Entitles() {
		// Cancellata, scaduta o in pausa: si torna al piano Free, con tutto ciò
		// che R58 prescrive. Lo stato scritto è quello vero di Paddle — `paused`
		// resta `paused` — perché è ciò che permette di distinguere, guardando la
		// riga, una disdetta da una sospensione del pagamento.
		change.PlanCode = paddle.PlanFree
		return change, nil
	}

	// Fra le voci attive si prende la prima che il catalogo riconosce. Una
	// sottoscrizione con più voci è possibile — componenti aggiuntivi, quantità —
	// ma il piano lo dà una sola di esse, ed è quella che compare in listino.
	for _, priceID := range sub.PriceIDs {
		if ref, ok := s.catalog.Plan(priceID); ok {
			change.PlanCode = ref.PlanCode
			change.Period = ref.Period
			change.PaddlePriceID = ref.PriceID
			return change, nil
		}
	}

	return SubscriptionChange{}, fmt.Errorf("%w: sottoscrizione %s, prezzi %v",
		ErrUnknownPrice, sub.ID, sub.PriceIDs)
}

// ---------------------------------------------------------------- checkout

// CheckoutRequest è ciò che l'utente chiede di comprare.
type CheckoutRequest struct {
	PlanCode string
	Period   paddle.Period

	// BusinessUse è la conferma di R63. **Deve essere vera**, e deve arrivare da
	// una scelta esplicita: R63 chiede «conferma esplicita di agire nell'esercizio
	// di un'attività, non una casella preselezionata sepolta nel modulo».
	BusinessUse bool

	// VATNumber è facoltativo: si conferma sempre lo status, si raccoglie il
	// numero quando c'è.
	VATNumber string
}

// Checkout è ciò che serve al browser per aprire il checkout di Paddle.
//
// **Non contiene un importo.** Il prezzo lo mostra Paddle, nella valuta e con le
// imposte che il paese del cliente richiede (R61, R61-bis): una cifra
// restituita da qui sarebbe una seconda copia libera di divergere da quella che
// il cliente paga davvero, e la nostra sarebbe quella sbagliata.
type Checkout struct {
	PlanCode string
	PlanName string
	Period   paddle.Period
	PriceID  string

	Environment string
	ClientToken string

	// CustomData è ciò che Paddle ci rimanderà indietro nei webhook. È il legame
	// fra il pagamento e l'account: senza, una sottoscrizione riuscita resterebbe
	// senza destinatario.
	CustomData CustomData
}

// CustomData è il payload che viaggia con l'acquisto e torna negli eventi.
type CustomData struct {
	UserID string `json:"user_id"`
}

// Checkout valida la richiesta, registra la dichiarazione di R63 e restituisce
// ciò che serve ad aprire il checkout.
//
// **Non chiama Paddle.** Il checkout di Paddle Billing si apre nel browser con
// Paddle.js a partire da un `price_id` e da un token pubblico: qui non c'è una
// chiamata di rete da fare, e non averla significa che questa rotta non può
// fallire perché un servizio esterno è lento. La conseguenza è che il collaudo
// vero — un pagamento in sandbox — è un'altra cosa da questo, e sta nella issue
// #478.
//
// L'ordine dei controlli non è indifferente: prima la conferma di R63, poi il
// listino. Chi non ha confermato non deve scoprire quali piani esistono
// mandando richieste, e soprattutto la conferma è la condizione dell'acquisto —
// non un dettaglio da verificare dopo aver preparato tutto il resto.
func (s *Service) Checkout(ctx context.Context, userID string, req CheckoutRequest) (Checkout, error) {
	if !req.BusinessUse {
		return Checkout{}, ErrBusinessUseRequired
	}

	vat := strings.TrimSpace(req.VATNumber)
	if len(vat) > maxVATNumberLen {
		return Checkout{}, fmt.Errorf("%w: oltre %d caratteri", ErrInvalidVATNumber, maxVATNumberLen)
	}

	if s.catalog.Empty() || s.clientToken == "" {
		return Checkout{}, ErrNotConfigured
	}

	planCode := strings.TrimSpace(req.PlanCode)
	if planCode == "" || planCode == paddle.PlanFree {
		// Il piano Free non è un acquisto: nessun prezzo pagato, nessuna cifra da
		// esporre (R63). Ci si arriva non comprando, o smettendo di pagare.
		return Checkout{}, fmt.Errorf("%w: %q", ErrPlanNotPurchasable, req.PlanCode)
	}

	period := req.Period
	if period == "" {
		period = paddle.PeriodMonthly
	}
	if period != paddle.PeriodMonthly && period != paddle.PeriodYearly {
		return Checkout{}, fmt.Errorf("%w: %q", ErrPeriodNotAvailable, req.Period)
	}

	ref, ok := s.catalog.Price(planCode, period)
	if !ok {
		// I due casi si distinguono qui e non nel catalogo, perché è qui che
		// servono due messaggi diversi: un piano che ha *altre* periodicità esiste
		// e non si vende così (R62); un piano che non ne ha nessuna non è in
		// vendita affatto.
		if len(s.catalog.Periods(planCode)) > 0 {
			return Checkout{}, fmt.Errorf("%w: %s in %s", ErrPeriodNotAvailable, planCode, period)
		}
		return Checkout{}, fmt.Errorf("%w: %q", ErrPlanNotPurchasable, planCode)
	}

	plan, err := s.store.Plan(ctx, planCode)
	if err != nil {
		return Checkout{}, err
	}
	if !plan.IsPublic {
		// Un piano non pubblico resta assegnabile a mano dall'area admin ma non si
		// compra: è il modo in cui la 0003 permette di ritirare un piano senza
		// rompere le sottoscrizioni che lo usano ancora.
		return Checkout{}, fmt.Errorf("%w: %q non è in listino", ErrPlanNotPurchasable, planCode)
	}

	if err := s.store.RecordCheckoutIntent(ctx, CheckoutIntent{
		UserID:      userID,
		PlanCode:    planCode,
		Period:      period,
		PriceID:     ref.PriceID,
		BusinessUse: true,
		VATNumber:   vat,
		CreatedAt:   s.now(),
	}); err != nil {
		// La dichiarazione non registrata è una dichiarazione che non c'è stata:
		// proseguire lascerebbe comprare senza la traccia che R63 pretende.
		return Checkout{}, fmt.Errorf("billing: registrazione dell'intento di checkout: %w", err)
	}

	return Checkout{
		PlanCode:    planCode,
		PlanName:    plan.Name,
		Period:      period,
		PriceID:     ref.PriceID,
		Environment: s.environment,
		ClientToken: s.clientToken,
		CustomData:  CustomData{UserID: userID},
	}, nil
}

// Entitlement legge il piano in forza e i job che un cambio di piano ha spento.
//
// I job sospesi fanno parte della risposta e non di una rotta a sé: R58 dice che
// l'interfaccia deve *dire* cosa è successo, e una schermata di stato del piano
// che non nomina i job fermi è esattamente il modo in cui un utente scopre il
// problema quando il job che gli serviva non è partito.
func (s *Service) Entitlement(ctx context.Context, userID string) (Entitlement, error) {
	return s.store.Entitlement(ctx, userID)
}
