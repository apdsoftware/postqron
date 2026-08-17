// Package paddle riceve e verifica gli eventi del webhook di Paddle (R16).
//
// Fa tre cose, e deliberatamente solo quelle: **verifica la firma** del corpo
// grezzo, **registra l'evento** perché una ripetizione non produca un secondo
// effetto, e passa ciò che ha capito a un [EntitlementSink]. Cosa significhi un
// evento per il piano dell'utente — quale piano, quali job restano accesi — è
// di internal/billing: qui non si sa nemmeno che esistano dei job.
//
// Non dipende da pgx, per la stessa ragione di internal/githubhook: il rifiuto
// di una firma sbagliata e l'assenza di doppio effetto si devono poter provare
// senza un database in piedi. L'implementazione PostgreSQL di [Store] sta in
// internal/paddlepg.
//
// # Paddle è Merchant of Record
//
// Non siamo noi a vendere: il contratto di vendita è fra l'utente e Paddle
// (SPEC §2, R60, R61-bis, e i Termini §1). La conseguenza per questo package è
// concreta e va rispettata riga per riga: **qui non si calcolano imposte, non si
// emettono documenti fiscali e non si conservano importi**. Di una transazione
// ci interessa che sia avvenuta e a quale prezzo di catalogo si riferisca — il
// `price_id`, che è un riferimento — non quanto è stato incassato né con quale
// aliquota. Ogni campo di valuta che finisse in questo codice sarebbe una
// seconda copia di un dato di cui il titolare è un altro.
//
// # Perché il corpo grezzo
//
// Paddle firma i byte esatti che manda, insieme al momento in cui li ha mandati.
// Qualunque passaggio che li normalizzi prima del confronto — decodificare il
// JSON e riserializzarlo, riordinare i campi, togliere spazi — produce un HMAC
// diverso da quello firmato, e la verifica smette di dire qualcosa di vero. Per
// questo [Request.Body] è `[]byte` e non una struttura già decodificata: la
// decodifica avviene *dopo* la verifica, su byte di cui sappiamo già la
// provenienza.
package paddle

import (
	"context"
	"errors"
	"strings"
	"time"
)

// HeaderSignature porta la firma della consegna, nella forma `ts=<unix>;h1=<hex>`.
const HeaderSignature = "Paddle-Signature"

// SecretEnvVar è la variabile che porta il segreto di firma delle notifiche
// (CREDENTIALS §1). Sta qui e non in internal/config perché quel package è
// condiviso fra le issue in corso: il valore lo legge chi lo usa.
const SecretEnvVar = "PADDLE_WEBHOOK_SECRET"

// EnvironmentEnvVar dichiara se le credenziali sono quelle della sandbox o
// quelle di produzione. Non entra in nessuna decisione di questo package — la
// firma si verifica allo stesso modo — ma il livello HTTP lo restituisce al
// checkout, perché è il valore con cui Paddle.js decide a quale ambiente
// collegarsi, e sbagliarlo è il modo più semplice di aprire un checkout vero
// credendo di provare.
const EnvironmentEnvVar = "PADDLE_ENVIRONMENT"

// minSecretLen è la lunghezza sotto la quale un segreto non è un segreto.
//
// I segreti di notifica di Paddle sono `pdl_ntfset_` seguito da una stringa
// lunga; il controllo serve a intercettare il caso vero — una variabile riempita
// con un valore di comodo durante una prova e mai sostituita.
const minSecretLen = 16

// SecretFromEnv legge il segreto del webhook dall'ambiente.
func SecretFromEnv(getenv func(string) string) string {
	return strings.TrimSpace(getenv(SecretEnvVar))
}

// Tipi di evento trattati.
//
// Sono tutti e soli quelli che descrivono lo **stato della sottoscrizione**, che
// è la fonte di verità del piano (R16). Gli eventi di transazione arrivano,
// vengono registrati e ignorati, e non è una lacuna: vedi [EventPrefixSubscription].
const (
	EventSubscriptionCreated   = "subscription.created"
	EventSubscriptionActivated = "subscription.activated"
	EventSubscriptionUpdated   = "subscription.updated"
	EventSubscriptionCanceled  = "subscription.canceled"
	EventSubscriptionPaused    = "subscription.paused"
	EventSubscriptionResumed   = "subscription.resumed"
	EventSubscriptionPastDue   = "subscription.past_due"
	EventSubscriptionTrialing  = "subscription.trialing"
)

// EventPrefixSubscription riconosce gli eventi che portano una sottoscrizione.
//
// Il riconoscimento è per prefisso e non per elenco chiuso, ed è una scelta:
// tutti gli eventi `subscription.*` di Paddle portano lo **stesso oggetto
// sottoscrizione**, con il suo stato corrente. Trattarli in modo uniforme
// significa che un tipo nuovo introdotto da Paddle domani viene applicato
// correttamente invece di essere scartato — perché ciò che applichiamo non è
// «cosa è successo» ma «com'è adesso», e quello è nel payload di ognuno.
//
// **Gli eventi di transazione non entrano di proposito.** Un pagamento fallito
// non cambia il piano: i Termini §4.2 dicono che durante i tentativi di Paddle
// il servizio continua, e il passaggio al piano Free avviene solo quando la
// sottoscrizione finisce davvero — cioè con un `subscription.canceled`, che è un
// evento di sottoscrizione. Reagire al singolo pagamento fallito degraderebbe
// l'account durante una finestra in cui abbiamo promesso per iscritto di non
// farlo.
const EventPrefixSubscription = "subscription."

// Errori riconoscibili dal chiamante. Il livello HTTP li traduce in status: la
// corrispondenza vive lì, non qui.
var (
	// ErrInvalidSignature indica che la richiesta non è firmata, o non lo è con
	// il segreto giusto, o è stata alterata dopo la firma, o porta un timestamp
	// fuori tolleranza. **È lo stesso errore per tutti i casi**: distinguerli
	// nella risposta racconterebbe a chi prova come sta andando il tentativo.
	ErrInvalidSignature = errors.New("paddle: firma non valida")

	// ErrInvalidRequest indica una consegna firmata correttamente ma malformata —
	// payload non decodificabile, campi obbligatori assenti. Si può dire al
	// chiamante cosa manca: la firma prova che il chiamante è Paddle.
	ErrInvalidRequest = errors.New("paddle: richiesta non valida")
)

// Limiti di forma dei valori che finiscono nella migrazione 0013. Non sono
// controlli di sicurezza — la firma li ha già coperti — ma i vincoli delle
// colonne: superarli significherebbe un INSERT rifiutato e un 500 che Paddle
// ripete all'infinito, quindi vengono fermati prima.
const (
	maxEventIDLen   = 100
	maxEventTypeLen = 100
)

// Status è lo stato di lavorazione di un evento, uguale all'enumerato
// `paddle_event_status` della migrazione 0013.
type Status string

const (
	// StatusReceived è la presa in carico: la lavorazione è iniziata.
	StatusReceived Status = "received"
	// StatusProcessed è l'evento applicato agli entitlement.
	StatusProcessed Status = "processed"
	// StatusIgnored è l'evento verificato ma senza niente da fare.
	StatusIgnored Status = "ignored"
	// StatusFailed è la lavorazione fallita. È l'unico stato da cui una
	// ripetizione viene rilavorata: vedi [Store.Claim].
	StatusFailed Status = "failed"
)

// Outcome è l'esito che il chiamante comunica a Paddle.
type Outcome string

const (
	// OutcomeApplied: sottoscrizione verificata e applicata agli entitlement.
	OutcomeApplied Outcome = "applied"
	// OutcomeDuplicate: evento già ricevuto. Nessun secondo effetto.
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeStale: evento più vecchio di quanto già applicato. Verificato,
	// registrato e non applicato — vedi la filigrana della 0013.
	OutcomeStale Outcome = "stale"
	// OutcomeIgnored: evento verificato ma senza niente da fare.
	OutcomeIgnored Outcome = "ignored"
)

// SubscriptionStatus è lo stato di una sottoscrizione secondo Paddle. I valori
// coincidono con l'enumerato `subscription_status` della migrazione 0001, e non
// per caso: sono lo stesso dominio, e tradurli sarebbe una tabella di conversione
// in più da tenere allineata.
type SubscriptionStatus string

const (
	SubscriptionActive   SubscriptionStatus = "active"
	SubscriptionTrialing SubscriptionStatus = "trialing"
	SubscriptionPastDue  SubscriptionStatus = "past_due"
	SubscriptionPaused   SubscriptionStatus = "paused"
	SubscriptionCanceled SubscriptionStatus = "canceled"
)

// Entitles indica se lo stato dà diritto al piano acquistato.
//
// `past_due` **entitles**, e non è una svista: i Termini §4.2 dicono che mentre
// Paddle ritenta il pagamento il servizio continua. Togliere il piano al primo
// addebito fallito contraddirebbe un documento che l'utente ha accettato, e lo
// farebbe nel momento in cui è più probabile che si tratti di una carta scaduta
// e non di un cliente perso.
//
// `paused` non entitles: una sottoscrizione in pausa non sta pagando, e non
// vendiamo la pausa come funzionalità. `canceled` nemmeno, ed è il caso che porta
// al piano Free — con tutto ciò che R58 prescrive, che è di internal/billing.
func (s SubscriptionStatus) Entitles() bool {
	switch s {
	case SubscriptionActive, SubscriptionTrialing, SubscriptionPastDue:
		return true
	default:
		return false
	}
}

// Request è una consegna così come arriva dal livello HTTP.
type Request struct {
	// Signature è il valore grezzo di [HeaderSignature].
	Signature string
	// Body sono i byte esatti ricevuti, non decodificati e non normalizzati:
	// vedi la nota sul corpo grezzo nella documentazione del package.
	Body []byte
}

// Result è l'esito della ricezione.
type Result struct {
	EventID string
	Type    string
	Outcome Outcome
}

// Event è l'involucro comune di ogni notifica di Paddle.
type Event struct {
	// ID è `event_id`: identico fra la consegna originale e ogni sua
	// ripetizione, ed è la chiave dell'idempotenza.
	ID string
	// Type è `event_type`: `subscription.updated`, `transaction.completed`, ...
	Type string
	// OccurredAt è il momento del fatto **secondo Paddle**. È l'ordine di cui
	// fidarsi: quello di arrivo lo decide la rete.
	OccurredAt time.Time
}

// Subscription è una sottoscrizione verificata, ridotta a ciò che serve per
// decidere un entitlement.
//
// **È il confine verso internal/billing.** Non contiene importi, valute né
// imposte, e non è una semplificazione: sono dati di cui il titolare è Paddle
// (R61, R61-bis), e conservarne una copia significherebbe avere due risposte
// alla stessa domanda e nessun modo di sapere quale vale.
type Subscription struct {
	// Event è la notifica che ha portato questa sottoscrizione. **Non è
	// incorporato**, e la ripetizione di `sub.Event.ID` accanto a `sub.ID` è il
	// prezzo che si paga volentieri: incorporandolo, `sub.ID` sarebbe
	// l'identificativo della sottoscrizione e `sub.Event.ID` quello dell'evento,
	// due valori diversi dietro nomi che si somigliano troppo — e scambiarli
	// significa deduplicare sulla chiave sbagliata.
	Event Event

	// ID è l'identificativo Paddle della sottoscrizione (`sub_...`): è la chiave
	// su cui la riga di `subscriptions` viene ritrovata.
	ID string
	// CustomerID è il cliente Paddle (`ctm_...`).
	CustomerID string

	Status SubscriptionStatus

	// PriceIDs sono i prezzi delle voci **attive** della sottoscrizione. È da
	// qui che si risale al piano, attraverso il catalogo: il prezzo è un
	// riferimento, non un importo.
	PriceIDs []string

	// UserID è il nostro identificativo utente, letto da `custom_data`. È il
	// legame fra una sottoscrizione Paddle e un account nostro, e ce lo mettiamo
	// noi al momento del checkout: senza, un pagamento riuscito resterebbe senza
	// destinatario. Può essere vuoto — una sottoscrizione creata a mano dal
	// pannello di Paddle non lo ha — e chi lo riceve deve saper cercare per
	// `ID` prima di arrendersi.
	UserID string

	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time

	// CanceledAt è valorizzato quando la sottoscrizione è finita davvero, non
	// quando la cancellazione è stata *programmata*.
	CanceledAt *time.Time

	// ScheduledCancelAt è la cancellazione programmata: i Termini §4.1 dicono che
	// una disdetta ha effetto **alla fine del periodo pagato**, e fino ad allora
	// il piano resta quello. Serve a poterlo dire nell'interfaccia, non a
	// cambiare l'entitlement adesso.
	ScheduledCancelAt *time.Time
}

// EntitlementSink riceve le sottoscrizioni già verificate e deduplicate.
//
// **È il confine dichiarato verso internal/billing.** Questo package non lo
// implementa: qui finisce la ricezione, lì comincia il piano dell'utente.
// L'implementazione deve tenere conto di due vincoli che nascono da questo lato
// del confine:
//
//   - Paddle chiude la connessione dopo pochi secondi, quindi il lavoro lungo va
//     accodato, non svolto dentro la chiamata;
//   - un errore restituito qui marca l'evento come fallito e fa rispondere 500,
//     così che una ripetizione — automatica o lanciata a mano dal cruscotto di
//     Paddle — venga rilavorata invece che scartata come duplicato.
//
// Il secondo valore di ritorno dice se l'evento è stato **applicato** o scartato
// perché più vecchio di quanto già in forza. Non è un errore: è l'esito normale
// di una consegna fuori ordine, e va distinto perché è ciò che si guarda quando
// un piano non cambia e nessuno capisce perché.
type EntitlementSink interface {
	ApplySubscription(ctx context.Context, sub Subscription) (applied bool, err error)
}

// Record è la riga che registra un evento ricevuto.
type Record struct {
	ID   string
	Type string

	OccurredAt time.Time

	SubscriptionID string
	CustomerID     string

	ReceivedAt time.Time
}

// Store conserva gli eventi già visti. L'implementazione PostgreSQL è
// internal/paddlepg.
type Store interface {
	// Claim registra l'evento e dice se è questa richiesta a doverlo lavorare.
	//
	// Restituisce false — senza errore — quando l'evento è già stato ricevuto: è
	// il punto in cui l'idempotenza di R16 diventa vera, e dev'essere **una sola
	// operazione atomica**, non una lettura seguita da una scrittura. Due
	// ripetizioni concorrenti dello stesso evento arrivano insieme, e con due
	// istruzioni separate passerebbero entrambe.
	//
	// L'unica eccezione è un evento in stato [StatusFailed]: quello viene
	// riassegnato, perché è esattamente il caso per cui Paddle ripete.
	Claim(ctx context.Context, record Record) (bool, error)

	// Complete registra l'esito della lavorazione. `failure` è il motivo del
	// fallimento e va valorizzato se e solo se lo stato è [StatusFailed].
	Complete(ctx context.Context, eventID string, status Status, failure string, at time.Time) error
}
