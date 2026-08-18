// Package notify decide che un'email va mandata, a chi, quando e in che lingua
// (R21).
//
// È la cucitura fra tre parti che non si conoscono: i package di dominio, che
// sanno **che cosa è successo**; internal/emailrender, che sa **come si scrive**
// (#418); internal/mailronix, che sa **come si recapita** (#419). Nessuno dei
// tre sa dell'esistenza degli altri due, ed è questo package a metterli in fila.
//
// # Un evento non è un invio
//
// Fra il fatto e l'email c'è una coda — la tabella `notifications` della 0008 —
// e non è un dettaglio di realizzazione. Sono tre proprietà che si ottengono
// solo separandoli:
//
//   - **L'invio non può far fallire il fatto.** Un webhook Paddle che assegna un
//     piano non deve rispondere 500 perché Mailronix è lento: il pagamento è
//     avvenuto, il piano va scritto, l'email è un'altra cosa. Chi accoda scrive
//     una riga e prosegue.
//   - **Il raggruppamento diventa possibile.** Un avviso che parte subito non si
//     può più unire a quello che arriva un minuto dopo. Uno accodato con
//     qualche minuto di grazia sì — vedi [Policy].
//   - **Il ritentare ha un posto.** Un errore di recapito lascia la riga in coda
//     con la sua data di ripresa, invece di svanire in una goroutine.
//
// # 202 non è un recapito (R20.1)
//
// Mailronix risponde `202` in modo identico sia che l'email parta sia che il
// destinatario sia in suppression list. Di conseguenza:
//
//   - si registra [mailronix.Receipt.EmailLogID] in `notifications.email_log_id`
//     e nulla di più, e la riga passa a `sent` — che vuol dire «consegnata a
//     Mailronix», non «arrivata»;
//   - **nessuna decisione di prodotto legge quello stato come recapito**. Non
//     esiste, e non va introdotto, un percorso che dica «l'utente è stato
//     avvisato»;
//   - l'alert di job fallito si ripete finché il job fallisce, invece di partire
//     una volta sola alla prima rottura. Vedi [Policy] per il perché.
//
// # Solo transazionale
//
// Qui passano le quattro email di R21 e nient'altro. Sono **transazionali**: si
// mandano perché l'utente ha un account e riguardano il servizio che ha
// chiesto, non hanno un link di disiscrizione e non consultano nessun consenso —
// il piè di pagina dei template lo dice a chi le riceve.
//
// Le comunicazioni di marketing hanno consenso separato e disiscrizione
// obbligatoria, e **non passano da questo package**: mescolarle significherebbe
// o mettere un link di disiscrizione su un avviso di sicurezza — che l'utente
// userebbe, smettendo di ricevere gli avvisi — o mandare una promozione senza,
// che è un problema legale. Il confine è qui e va tenuto qui.
package notify

import (
	"context"
	"errors"
	"time"
)

// Event è l'evento che genera la notifica. I valori coincidono con quelli del
// tipo `notification_event` della migrazione 0001 e ci vengono scritti così
// come sono.
//
// Attenzione al nome dell'ultimo: l'enumerato del database lo chiama `security`,
// il template di emails/templates/ lo chiama `security_alert`. La traduzione
// avviene in un punto solo, [Courier.render], e non va sparpagliata.
type Event string

const (
	EventWelcome     Event = "welcome"
	EventJobFailed   Event = "job_failed"
	EventPlanChanged Event = "plan_changed"
	EventSecurity    Event = "security"
)

// Events elenca gli eventi coperti da R21, in ordine stabile.
func Events() []Event {
	return []Event{EventWelcome, EventJobFailed, EventPlanChanged, EventSecurity}
}

// Channel è il canale di recapito, uguale al tipo `alert_channel` della 0001.
// Oggi questo package ne serve uno solo; gli altri tre sono la R29.
type Channel string

// ChannelEmail è l'unico canale che questo package sa percorrere.
const ChannelEmail Channel = "email"

// FailureKind classifica il motivo di un fallimento, e non è un messaggio
// d'errore.
//
// La ragione è la stessa per cui non lo è in [emailrender.JobFailedData], e
// vale la pena ripeterla dove il valore nasce: il testo grezzo di un errore di
// rete o il corpo di una risposta possono contenere un URL con un token nella
// query — dalla #458 in poi gli URL dei job contengono i segreti del workspace
// risolti — e questa è un'email. Ciò che non entra in questo tipo non può
// uscire da un template.
type FailureKind string

const (
	FailureTimeout    FailureKind = "timeout"
	FailureConnection FailureKind = "connection"
	FailureDNS        FailureKind = "dns"
	FailureTLS        FailureKind = "tls"
	FailureHTTPStatus FailureKind = "http_status"
	FailureUnknown    FailureKind = "unknown"
)

// SecurityKind è il tipo di evento di sicurezza notificato (R21).
type SecurityKind string

const (
	SecurityPasswordChanged     SecurityKind = "password_reset"
	SecurityAPIKeyCreated       SecurityKind = "api_key_created"
	SecurityAPIKeyRevoked       SecurityKind = "api_key_revoked"
	SecurityAccountImpersonated SecurityKind = "account_impersonated"
)

// Payload è il contesto dell'evento, che finisce in `notifications.payload`.
//
// È **una struttura sola e piatta** invece di quattro: la colonna è un jsonb, e
// una gerarchia di tipi con un campo `any` costringerebbe chi legge a
// indovinare il tipo dall'evento. Piatta, il vincolo che conta si verifica
// guardando un elenco di campi — ed è il vincolo di questo package:
//
// **nessun campo di questa struttura è un segreto, e nessuno lo diventerà.**
// Non c'è un token, non c'è una chiave, non c'è un corpo di risposta, non c'è il
// testo di un errore. Un template non può interpolare ciò che non gli viene mai
// passato, e un jsonb non può contenere ciò che non ci si scrive: il valore
// resta fuori dal database, fuori dai log e fuori dall'email nello stesso gesto.
// Il test TestPayloadCarriesNoSecrets tiene il conto.
type Payload struct {
	// RecipientName è il nome con cui salutare, per il benvenuto. Facoltativo.
	RecipientName string `json:"recipient_name,omitempty"`

	// ---------------------------------------------------------- job fallito

	// JobName è il nome del job scelto dall'utente.
	JobName string `json:"job_name,omitempty"`
	// Failures è il numero di esecuzioni fallite che questo avviso racconta.
	// Parte da uno e cresce a ogni fallimento raggruppato nella finestra di
	// grazia: vedi [Policy].
	Failures int `json:"failures,omitempty"`
	// LastAttemptAt è l'istante dell'ultimo tentativo fallito.
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
	// FailureKind classifica l'esito, senza portarne il testo.
	FailureKind FailureKind `json:"failure_kind,omitempty"`
	// HTTPStatus ha senso solo con FailureKind uguale a FailureHTTPStatus.
	HTTPStatus int `json:"http_status,omitempty"`

	// -------------------------------------------------------- cambio piano

	PreviousPlan string    `json:"previous_plan,omitempty"`
	NewPlan      string    `json:"new_plan,omitempty"`
	EffectiveAt  time.Time `json:"effective_at,omitempty"`
	// SuspendedByJobLimit e SuspendedByResolution sono i due conteggi di R58,
	// separati perché i rimedi che l'email chiede sono diversi.
	SuspendedByJobLimit   int `json:"suspended_by_job_limit,omitempty"`
	SuspendedByResolution int `json:"suspended_by_resolution,omitempty"`

	// ------------------------------------------------------------ sicurezza

	SecurityKind SecurityKind `json:"security_kind,omitempty"`
	OccurredAt   time.Time    `json:"occurred_at,omitempty"`
	// ResourceName è il **nome** della risorsa coinvolta — l'etichetta di una
	// chiave API, per esempio — mai il suo valore.
	ResourceName string `json:"resource_name,omitempty"`
	// SourceIP è l'indirizzo da cui è partita l'azione. Facoltativo.
	SourceIP string `json:"source_ip,omitempty"`
}

// Request è la riga che si vuole scrivere in coda.
//
// La compone [Service] a partire da un evento di dominio: chi chiama non
// costruisce chiavi di deduplicazione a mano, o la politica anti-spam
// diventerebbe una convenzione da ricordare invece che una regola.
type Request struct {
	Event   Event
	Channel Channel
	// UserID è il destinatario. **Non c'è un indirizzo**: l'email, il nome e la
	// lingua si leggono da `users` al momento dell'invio, non a quello
	// dell'accodamento. Un alert accodato stanotte e recapitato stamattina va
	// all'indirizzo di adesso e nella lingua di adesso, che è ciò che l'utente
	// si aspetta se nel frattempo li ha cambiati.
	UserID string

	// DedupeKey è la chiave del raggruppamento. Vuota significa «questa notifica
	// non si raggruppa con nessuna».
	DedupeKey string

	// ScheduledAt è il momento da cui la notifica può partire. Più avanti di
	// adesso è la finestra di grazia in cui i fallimenti si raggruppano.
	ScheduledAt time.Time

	// JobID, Environment ed Execution* valorizzano le colonne di riferimento
	// della 0008. Servono a ritrovare l'avviso partendo dal job, non a comporre
	// il testo — quello sta tutto in Payload.
	JobID                 string
	Environment           string
	ExecutionScheduledFor time.Time
	ExecutionAttempt      int

	Payload Payload
}

// Result è l'esito di un accodamento.
type Result string

const (
	// Queued: la riga è nuova, l'email partirà.
	Queued Result = "queued"
	// Grouped: esisteva già un avviso per la stessa chiave. Se era ancora in
	// attesa il suo conteggio è cresciuto, se era già partito non si manda
	// niente. In nessuno dei due casi nasce una seconda email.
	Grouped Result = "grouped"
	// NoRecipient: non c'è nessuno a cui mandarla — l'account non esiste più, o
	// il job non ha chiesto avvisi via email. Non è un errore.
	NoRecipient Result = "no_recipient"
)

// Recipient è chi riceve, letto al momento dell'invio.
type Recipient struct {
	UserID string
	// Email è l'indirizzo. Non finisce **mai** in un log: è un dato personale, e
	// nel log non serve a niente che l'identificativo dell'utente non copra già.
	Email string
	// Name è il nome, eventualmente vuoto.
	Name string
	// Language è la lingua del profilo (R33), già normalizzata a una delle
	// cinque supportate.
	Language string
	// Closed è vero se l'account è stato chiuso dopo l'accodamento. A un account
	// chiuso non si scrive.
	Closed bool
}

// Pending è una notifica presa in carico dal corriere.
type Pending struct {
	ID    string
	Event Event
	// Attempts è il numero di tentativi di **recapito** già fatti, questo
	// compreso. Non ha niente a che vedere con i tentativi di un job.
	Attempts int
	// JobID ed Environment vengono dalle colonne della 0008 e non dal payload:
	// sono un riferimento, e duplicarli nel jsonb significherebbe tenerne due
	// copie libere di divergere.
	JobID       string
	Environment string
	Recipient   Recipient
	Payload     Payload
}

// Queue è la coda delle notifiche. L'implementazione PostgreSQL è
// internal/notifypg.
//
// Le operazioni sono tutte idempotenti rispetto a una consegna ripetuta, ed è
// una necessità e non una comodità: un webhook Paddle ripetuto e un'occorrenza
// recuperata dallo scheduler rieseguono lo stesso codice.
type Queue interface {
	// Enqueue scrive la richiesta in coda.
	//
	// La deduplicazione dev'essere **una sola istruzione**: contare e poi
	// inserire lascerebbe passare due avvisi a due connessioni concorrenti,
	// che è esattamente il caso da cui la politica difende — un job che
	// fallisce ogni secondo produce fallimenti concorrenti per costruzione.
	Enqueue(ctx context.Context, req Request) (Result, error)

	// Due prende in carico le notifiche pronte e ne restituisce il contenuto
	// già unito al destinatario.
	//
	// La presa in carico è un **contratto a scadenza**: la riga resta `pending`
	// ma la sua ripresa slitta di `lease`, così che un corriere morto a metà
	// non lasci una notifica bloccata per sempre. Il prezzo dichiarato è che una
	// notifica possa partire due volte se il recapito dura più della scadenza.
	Due(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]Pending, error)

	// MarkSent chiude la notifica registrando l'identificativo Mailronix.
	// **Non significa recapitata** (R20.1).
	MarkSent(ctx context.Context, id, emailLogID string, at time.Time) error

	// Retry rimette in coda una notifica il cui recapito è fallito in modo
	// transitorio.
	Retry(ctx context.Context, id string, at time.Time, reason string) error

	// MarkFailed chiude una notifica che non partirà.
	MarkFailed(ctx context.Context, id, reason string, at time.Time) error

	// MarkSkipped chiude una notifica che non ha più senso mandare, per esempio
	// perché l'account è stato chiuso nel frattempo.
	MarkSkipped(ctx context.Context, id, reason string, at time.Time) error
}

// ErrNoRecipient segnala un accodamento senza destinatario. Vedi [NoRecipient]:
// non è un errore per chi chiama, ed è qui perché uno store può volerlo
// distinguere.
var ErrNoRecipient = errors.New("notify: nessun destinatario per questa notifica")
