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

	// Notifier comunica all'utente le variazioni di piano (R21). Può essere nil:
	// su una macchina senza email configurate il piano si applica lo stesso, e
	// deve.
	Notifier Notifier

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
	notifier    Notifier
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
		notifier:    opts.Notifier,
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
	if errors.Is(err, ErrUnknownSubscriber) {
		// La sottoscrizione non appartiene a nessun account, e il caso che la
		// produce è previsto: un account cancellato e purgato (R45) si porta via
		// la propria riga di `subscriptions`, mentre dalla parte di Paddle il
		// contratto resta vivo e continua a generare eventi.
		//
		// L'errore viene rietichettato come [paddle.ErrUnattributable] perché è
		// quella l'informazione che serve a chi lo riceve: non «è andata male» ma
		// «ripetere non serve». Il 500 che otterrebbe altrimenti farebbe ripetere
		// Paddle per tre giorni su un fatto che nessuna ripetizione può sistemare.
		s.log.ErrorContext(ctx, "sottoscrizione Paddle senza account: probabile account cancellato (R45)",
			slog.String("subscription_id", sub.ID),
			slog.String("event_id", sub.Event.ID),
			slog.String("event_type", sub.Event.Type))
		return false, fmt.Errorf("%w: %w", paddle.ErrUnattributable, err)
	}
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

	s.announce(ctx, result, suspension, change.OccurredAt)
	return true, nil
}

// announce comunica all'utente la variazione, se c'è stata (R21).
//
// **Non restituisce un errore, e non è una svista.** Il pagamento è avvenuto, il
// piano è scritto e i job sono stati sospesi: se questa funzione risalisse un
// errore, il livello HTTP risponderebbe 500 e Paddle ripeterebbe la consegna di
// un evento che è già stato applicato per intero. Un'email che non parte è un
// guasto da registrare, non un motivo per rimettere in discussione una
// fatturazione andata a buon fine.
//
// L'ordine conta: si comunica **dopo** aver applicato R58, perché l'email deve
// dire quanti job sono stati fermati, e prima di conoscere quel numero
// racconterebbe metà della storia proprio nel caso in cui l'altra metà è
// l'unica azione richiesta all'utente.
func (s *Service) announce(ctx context.Context, result SaveResult, suspension Suspension, occurredAt time.Time) {
	if s.notifier == nil {
		return
	}
	if result.PreviousPlanCode == result.PlanCode {
		// Un rinnovo non è una variazione. Il controllo è anche a valle, ma
		// farlo qui risparmia due letture di listino per ogni rinnovo di ogni
		// abbonamento, che è l'evento Paddle più frequente che esista.
		return
	}

	previous, err := s.planName(ctx, result.PreviousPlanCode)
	if err != nil {
		s.log.ErrorContext(ctx, "variazione di piano non comunicata: listino illeggibile",
			slog.String("user_id", result.UserID), slog.Any("error", err))
		return
	}
	next, err := s.planName(ctx, result.PlanCode)
	if err != nil {
		s.log.ErrorContext(ctx, "variazione di piano non comunicata: listino illeggibile",
			slog.String("user_id", result.UserID), slog.Any("error", err))
		return
	}

	effective := occurredAt
	if effective.IsZero() {
		effective = s.now()
	}

	if err := s.notifier.PlanChanged(ctx, PlanChangeNotice{
		UserID:                result.UserID,
		PreviousPlan:          previous,
		NewPlan:               next,
		EffectiveAt:           effective,
		SuspendedByJobLimit:   suspension.ByJobLimit,
		SuspendedByResolution: suspension.ByResolution,
	}); err != nil {
		s.log.ErrorContext(ctx, "variazione di piano non comunicata",
			slog.String("user_id", result.UserID),
			slog.String("plan", result.PlanCode),
			slog.Any("error", err))
	}
}

// planName traduce un codice di piano nel nome che l'utente legge in listino.
//
// Un piano ritirato dal listino resta leggibile — la 0003 lo rende non pubblico,
// non lo cancella — quindi questa lettura riesce anche per chi sta uscendo da un
// piano che non si vende più.
func (s *Service) planName(ctx context.Context, code string) (string, error) {
	plan, err := s.store.Plan(ctx, code)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(plan.Name) == "" {
		return code, nil
	}
	return plan.Name, nil
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
