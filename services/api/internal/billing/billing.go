// Package billing traduce ciò che Paddle dice in ciò che l'utente ottiene
// (R16), e applica il downgrade di R58.
//
// Il confine con internal/paddle è netto e vale la pena dirlo: lì si verifica
// *che* un evento venga da Paddle, qui si decide *cosa significa*. Sono due
// preoccupazioni con due modi diversi di sbagliare — una firma accettata a torto
// e un piano assegnato a torto — e tenerle separate è ciò che permette di
// provare la seconda senza costruire la prima.
//
// # Paddle è Merchant of Record
//
// Il contratto di vendita è fra l'utente e Paddle (Termini §1): noi vendiamo un
// servizio, non emettiamo documenti fiscali e non calcoliamo imposte. Qui dentro
// **non esiste un importo**, e non è un'omissione da colmare: i prezzi sono in
// euro, al netto delle imposte, e la loro fonte di verità è il catalogo Paddle
// (R61, R61-bis). Ciò che attraversa questo package sono `price_id`, che sono
// riferimenti a quel catalogo.
//
// # R58, che è la parte che conta
//
// Quando il piano si restringe, i job dell'utente possono non starci più. La
// regola è scritta in SPEC §14 e ripetuta nei Termini §4.1, ed è **una scelta di
// prodotto prima che una regola tecnica**: si sospende, non si sceglie e non si
// cancella.
//
//   - Se i job attivi superano il tetto del nuovo piano, **si sospendono tutti** e
//     l'utente ne riaccende quanti il piano ne consente. Non scegliamo noi quali
//     salvare perché non possiamo saperlo: due job identici per schedulazione e
//     destinazione possono valere uno la fatturazione mensile e l'altro un
//     promemoria.
//   - Se i job attivi ci stanno già, **non si tocca niente**. Fermare tutto quando
//     non serve sarebbe un danno gratuito.
//   - **Nulla viene cancellato**, in nessun caso. I job sospesi restano visibili,
//     modificabili ed esportabili, con il loro storico.
//   - **La risoluzione è un secondo vincolo, indipendente dal numero.** Un job
//     `every: 1s` non può stare acceso su un piano che si ferma al minuto, e non
//     lo può nemmeno se c'è posto: va prima cambiata la schedulazione.
//
// Sull'ultimo punto la lettura merita di essere esplicita, perché è l'unica di
// questo package che non si legge alla lettera in un solo documento. SPEC §14
// descrive la risoluzione parlando di **riaccensione**, cioè del caso in cui il
// job è già spento perché il tetto era stato superato. Qui la si applica anche
// quando il tetto **non** è stato superato, sospendendo i soli job troppo fitti,
// e la ragione è che i Termini §4 promettono l'opposto della lettura alternativa:
// «*a plan's job count, minimum interval and log retention are real ceilings*».
// Un `every: 1s` che continua a girare su un piano fermo al minuto renderebbe
// quella frase falsa — e renderebbe falso anche R15. Per gli stessi job la
// sospensione è **selettiva** e non totale: dove il numero lascia una scelta
// all'utente, la risoluzione non ne lascia nessuna, perché riaccenderne un altro
// non libera posto e l'unico rimedio è cambiare la schedulazione.
//
// Lo stesso comportamento vale al mancato pagamento e alla scadenza, che portano
// al piano Free (Termini §4.2).
//
// Non dipende da pgx: l'implementazione PostgreSQL di [Store] sta in
// internal/billingpg. La regola di R58 è però **una sola istruzione SQL** e vive
// lì, non qui, e il motivo è lo stesso del tetto di piano in jobspg: contare e
// poi sospendere è una corsa.
package billing

import (
	"context"
	"errors"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// Errori riconoscibili dal chiamante. Il livello HTTP li traduce in status.
var (
	// ErrPlanNotPurchasable indica un piano che non si compra: il piano Free è
	// l'ingresso e non ha un prezzo (R59: non esistono prove gratuite, il Free
	// *è* il piano gratuito), e un piano ritirato dal listino non si vende più.
	ErrPlanNotPurchasable = errors.New("billing: piano non acquistabile")

	// ErrPeriodNotAvailable indica una periodicità che quel piano non ha. È R62:
	// l'annuale esiste solo su Pro, e Team e Agency sono esclusivamente mensili.
	// È deliberato, non una lacuna da colmare.
	ErrPeriodNotAvailable = errors.New("billing: periodicità non disponibile per questo piano")

	// ErrBusinessUseRequired indica un checkout senza la conferma di uso
	// professionale. È R63, ed è il presupposto che rende legittima l'esposizione
	// dei prezzi al netto delle imposte: verso i consumatori l'Unione Europea
	// pretende il prezzo finale. Dichiarare l'uso professionale e poi lasciar
	// comprare chiunque senza chiederlo è la posizione peggiore delle due.
	ErrBusinessUseRequired = errors.New("billing: serve la conferma di uso professionale")

	// ErrNotConfigured indica che su questa macchina non c'è un catalogo Paddle:
	// nessun piano è acquistabile. Non è un errore della richiesta.
	ErrNotConfigured = errors.New("billing: catalogo Paddle non configurato")

	// ErrUnknownPrice indica una sottoscrizione su un prezzo che il catalogo non
	// conosce. **Non ha un ripiego**: assegnare un piano a caso sarebbe peggio
	// che non assegnarne nessuno, e la causa vera è quasi sempre una variabile
	// PADDLE_PRICE_* mancante o un catalogo di produzione letto con la
	// configurazione di sandbox.
	ErrUnknownPrice = errors.New("billing: prezzo Paddle non in catalogo")

	// ErrUnknownSubscriber indica una sottoscrizione che non si riesce ad
	// attribuire a nessun account: né `custom_data.user_id` né una riga già
	// legata a quell'identificativo Paddle.
	ErrUnknownSubscriber = errors.New("billing: sottoscrizione senza account a cui attribuirla")

	// ErrInvalidVATNumber indica una partita IVA di forma inaccettabile. Non è
	// una validazione fiscale — quella è di Paddle, che è il Merchant of Record —
	// ma il rifiuto di un valore che non starebbe nella colonna.
	ErrInvalidVATNumber = errors.New("billing: partita IVA non valida")
)

// SuspensionReason è il motivo per cui un job è stato spento da noi, uguale
// all'enumerato `job_suspension_reason` della migrazione 0013.
type SuspensionReason string

const (
	// ReasonJobLimit: i job attivi superavano il tetto del piano di destinazione.
	// Rimedio dell'utente: riaccenderne quanti il piano ne consente.
	ReasonJobLimit SuspensionReason = "plan_job_limit"
	// ReasonResolution: la schedulazione è più fitta di quanto il piano consenta.
	// Rimedio dell'utente: cambiare la schedulazione. Non è una questione di
	// posto.
	ReasonResolution SuspensionReason = "plan_resolution"
)

// SubscriptionChange è ciò che un evento Paddle chiede di scrivere.
//
// È già tradotto: il piano è un codice di `plans`, non un `price_id`, e lo stato
// è quello dell'enumerato `subscription_status`. La traduzione la fa
// [Service.ApplySubscription], che è l'unico posto in cui il catalogo viene
// consultato.
type SubscriptionChange struct {
	// UserID è il nostro utente, quando l'evento lo porta in `custom_data`. Vuoto
	// significa «cercalo dall'identificativo Paddle»: vedi [ErrUnknownSubscriber].
	UserID string

	PaddleSubscriptionID string
	PaddleCustomerID     string
	PaddlePriceID        string

	PlanCode string
	Period   paddle.Period
	Status   string

	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	CanceledAt         *time.Time
	CancelAt           *time.Time

	// OccurredAt è l'istante del fatto secondo Paddle. È **la filigrana**: la
	// scrittura avviene solo se questo istante non precede quello dell'ultimo
	// evento già applicato alla stessa sottoscrizione.
	OccurredAt time.Time
}

// SaveResult è l'esito della scrittura di una sottoscrizione.
type SaveResult struct {
	// Applied è falso quando l'evento è più vecchio di quanto già in forza. Non è
	// un errore: è l'esito normale di una consegna fuori ordine.
	Applied bool

	// UserID è l'account a cui la sottoscrizione è stata attribuita. Serve al
	// passo successivo — l'applicazione di R58 — che ha bisogno di sapere di chi
	// sono i job da guardare.
	UserID string

	// PlanCode è il piano in forza dopo la scrittura.
	PlanCode string

	// PreviousPlanCode è il piano in forza **prima**. Vale `free` quando
	// l'utente non aveva una sottoscrizione viva, che è la condizione normale di
	// chi non ha mai comprato (R59).
	//
	// Serve alla notifica di R21 e a una decisione che senza di esso non si
	// potrebbe prendere: se i due piani coincidono non c'è niente da comunicare,
	// e un rinnovo mensile — che è un evento Paddle a tutti gli effetti —
	// manderebbe un'email che annuncia un cambiamento mai avvenuto.
	PreviousPlanCode string
}

// PlanChangeNotice è ciò che l'utente deve sapere di una variazione di piano
// (R21), compreso quello che deve **fare** se R58 ha fermato dei job.
//
// I nomi sono quelli di listino e non i codici: l'email è per una persona, e
// «Pro» è come il piano si chiama nella pagina dei prezzi che ha visto.
type PlanChangeNotice struct {
	UserID       string
	PreviousPlan string
	NewPlan      string
	EffectiveAt  time.Time

	// SuspendedByJobLimit e SuspendedByResolution restano due numeri distinti
	// perché sono due rimedi distinti: vedi [Suspension].
	SuspendedByJobLimit   int
	SuspendedByResolution int
}

// Notifier riceve le variazioni di piano da comunicare.
//
// L'implementazione è internal/notify, che decide quando la comunicazione
// diventa un'email e in che lingua. La direzione della dipendenza è quella di
// [paddle.EntitlementSink]: chi produce il fatto dichiara l'interfaccia, chi lo
// consuma la implementa, e questo package resta provabile senza template né
// client SMTP.
//
// Un errore **non deve** far fallire l'applicazione della sottoscrizione: il
// pagamento è avvenuto e il piano va scritto comunque. Vedi
// [Service.ApplySubscription].
type Notifier interface {
	PlanChanged(ctx context.Context, notice PlanChangeNotice) error
}

// Suspension è l'esito dell'applicazione di R58.
//
// I due conteggi sono separati perché i rimedi sono diversi, ed è la stessa
// ragione per cui l'enumerato della 0013 distingue i due motivi: a chi ha job
// sospesi per il numero si chiede di sceglierne alcuni, a chi ne ha per la
// risoluzione si chiede di cambiare la schedulazione. Un totale unico
// costringerebbe l'interfaccia a dire una delle due cose a tutti.
type Suspension struct {
	ByJobLimit   int
	ByResolution int
}

// Total è il numero di job spenti dall'ultima applicazione di R58.
func (s Suspension) Total() int { return s.ByJobLimit + s.ByResolution }

// CheckoutIntent è la dichiarazione raccolta prima di aprire il checkout (R63).
type CheckoutIntent struct {
	UserID   string
	PlanCode string
	Period   paddle.Period
	PriceID  string

	// BusinessUse è la conferma di agire nell'esercizio di un'attività. Un
	// intento senza conferma non si registra: viene rifiutato prima.
	BusinessUse bool

	// VATNumber è la partita IVA **dove esiste**. R63 è esplicito sul fatto che
	// non vada resa obbligatoria: diversi regimi minimi europei ne sono privi — i
	// Kleinunternehmer tedeschi, per esempio — e pretenderla escluderebbe
	// acquirenti legittimi.
	VATNumber string

	CreatedAt time.Time
}

// PlanSummary è la riga di listino vista da qui: il minimo per sapere se un
// piano si può vendere e come chiamarlo in un messaggio.
type PlanSummary struct {
	Code     string
	Name     string
	IsPublic bool
}

// Entitlement è ciò che l'utente ha adesso, nella forma che serve a mostrarglielo.
type Entitlement struct {
	PlanCode string
	PlanName string
	Status   string
	Period   paddle.Period

	CurrentPeriodEnd *time.Time
	// CancelAt è la disdetta programmata: i Termini §4.1 dicono che ha effetto
	// alla fine del periodo pagato, e fino ad allora il piano resta questo.
	CancelAt *time.Time

	// MaxJobs è il tetto del piano; nil significa nessun tetto rigido.
	MaxJobs *int
	// ActiveJobs sono i job accesi: è il numero che l'utente confronta col tetto
	// quando decide quali riaccendere.
	ActiveJobs int

	// MinInterval e LogRetention sono gli altri due tetti che R15 nomina —
	// frequenza minima e conservazione dei log — accanto al numero di job.
	//
	// Stanno qui perché **R15 chiede due cose, non una**: che i limiti siano
	// applicati lato backend, e che l'interfaccia li dica. La seconda metà non si
	// può fare senza i numeri: un client che non li riceve o inventa una tabella
	// di listino — una seconda copia di `plans`, libera di divergere in silenzio
	// — oppure lascia compilare un modulo che verrà rifiutato, che è il difetto
	// che R15 esiste per evitare. Sono gli stessi valori che [jobs.Plan] usa per
	// *decidere* (`min_interval_seconds`, `log_retention_days` della 0003), letti
	// dalla stessa riga: il server resta l'unico giudice, questi servono a dirlo
	// prima.
	MinInterval  time.Duration
	LogRetention time.Duration

	// Suspended conta i job che un cambio di piano ha spento, per motivo. È ciò
	// che rende visibile la promessa di R58: i job ci sono ancora, e vanno
	// riaccesi.
	Suspended Suspension
}

// Store è la persistenza degli entitlement. L'implementazione PostgreSQL è
// internal/billingpg.
type Store interface {
	// SaveSubscription scrive la sottoscrizione, **rispettando la filigrana**.
	//
	// Il confronto fra [SubscriptionChange.OccurredAt] e l'ultimo evento
	// applicato dev'essere nella stessa transazione della scrittura: letto prima
	// e confrontato in Go, due consegne concorrenti passerebbero entrambe. Un
	// evento che non supera il confronto restituisce `Applied: false` **senza
	// errore** — non c'è niente da rilavorare.
	SaveSubscription(ctx context.Context, change SubscriptionChange) (SaveResult, error)

	// EnforcePlanLimits applica R58: sospende i job che il piano non regge più.
	//
	// Dev'essere **una sola istruzione**, e non un conteggio seguito da un
	// aggiornamento: fra i due, una creazione concorrente cambierebbe il numero
	// su cui la decisione è stata presa. È anche il motivo per cui la regola sta
	// in SQL e non qui.
	//
	// È idempotente: applicata due volte di seguito, la seconda non tocca niente.
	// Serve, perché una consegna fallita e ripetuta la riesegue.
	EnforcePlanLimits(ctx context.Context, userID, planCode string, at time.Time) (Suspension, error)

	// Plan legge una riga di listino. Errore se quel codice non esiste.
	Plan(ctx context.Context, code string) (PlanSummary, error)

	// RecordCheckoutIntent registra la dichiarazione di R63.
	RecordCheckoutIntent(ctx context.Context, intent CheckoutIntent) error

	// Entitlement legge lo stato corrente dell'utente.
	Entitlement(ctx context.Context, userID string) (Entitlement, error)
}
