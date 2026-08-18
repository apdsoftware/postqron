// Package account cancella account e workspace (R45).
//
// # Che cosa promettiamo per iscritto
//
// Non è una funzionalità che si progetta dal codice: due documenti legali
// dicono già cosa succede, e il codice deve corrispondere a loro.
//
// La privacy policy (legal/en/privacy-policy.md §5):
//
//	«When you delete your account we stop execution and revoke keys
//	 immediately, then remove the data after a grace period of 30 days,
//	 during which you can change your mind. […] Records we must keep for tax
//	 or legal reasons survive deletion, and only those.»
//
// I Termini (legal/en/terms-of-service.md §7):
//
//	«You may close your account at any time. On closure we stop execution,
//	 revoke keys and delete your data after the grace period stated in the
//	 Privacy Policy.»
//
// Ne discendono quattro obblighi, e ognuno ha un pezzo di codice che lo
// mantiene:
//
//  1. **subito**: le esecuzioni si fermano e le chiavi si revocano nella stessa
//     transazione della richiesta. Non alla purga, non a una passata successiva:
//     «immediately» è una parola che un'autorità legge alla lettera;
//  2. **trenta giorni**: [DefaultGrace] è quel numero, e
//     TestLaGraziaPredefinitaÈQuellaDellaPrivacyPolicy lo rilegge dal documento
//     invece di fidarsi di questo commento;
//  3. **si può tornare indietro**: durante la grazia l'account esiste, l'utente
//     può entrare e annullare, e i job che avevamo fermato ripartono — solo
//     quelli;
//  4. **poi non resta niente**: la purga rimuove, e ciò che sopravvive è
//     elencato e motivato in [Purged].
//
// # Le tre cose che una cancellazione sbaglia
//
// **Cancellare troppo poco.** Dati che restano dopo aver dichiarato per iscritto
// di averli cancellati: è una dichiarazione falsa in un documento legale. Il
// rimedio non è una lista di tabelle scritta a mano — quella non conosce la
// tabella che arriverà con la prossima migrazione — ma un test che interroga lo
// schema e non permette a nessuna tabella di sfuggire in silenzio: vedi
// internal/accountpg.
//
// **Cancellare troppo.** Le esecuzioni sono partizionate per giorno e
// attraversarle tutte è un'operazione pesante su una VPS che ospita anche il
// motore e il database (SPEC §2); una cascata scritta male porta via righe di
// altri, e non c'è un annulla. Ogni istruzione della purga è quindi ancorata a
// `user_id`, e le due tabelle che un `user_id` non ce l'hanno —
// `github_webhook_deliveries` e `paddle_webhook_events` — sono trattate una per
// una, con la condizione che dimostra che quella riga è di questo utente e di
// nessun altro.
//
// **Cancellare al momento sbagliato.** Un account con job in esecuzione in
// questo istante non si cancella come uno fermo, e uno con una sottoscrizione
// viva nemmeno: vedi [ErrSubscriptionActive].
//
// # Account e workspace sono la stessa cosa, oggi
//
// R45 nomina entrambi. La 0012 spiega perché nel database ce n'è uno solo: «il
// workspace di SPEC §9 oggi coincide con l'account; i workspace multipli sono
// R25, appartengono al piano Agency e non hanno ancora una tabella».
// Cancellare l'account cancella quindi il workspace, ed è l'unica lettura
// possibile finché R25 non arriva.
package account

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultGrace è il periodo di ripensamento, e **non è un numero scelto qui**:
// è quello che la privacy policy §5 promette all'utente.
//
// Cambiarlo senza cambiare il documento renderebbe il documento inesatto, che è
// un problema peggiore di un periodo tarato male.
// TestLaGraziaPredefinitaÈQuellaDellaPrivacyPolicy legge il file e confronta.
const DefaultGrace = 30 * 24 * time.Hour

// GraceEnvVar sostituisce [DefaultGrace]. È il «periodo di sicurezza
// configurabile» di R45.
//
// Serve agli ambienti di prova, dove aspettare trenta giorni per vedere una
// purga non è praticabile. **In esercizio va lasciata vuota**: il valore giusto
// è quello che l'utente ha letto.
const GraceEnvVar = "POSTQRON_ACCOUNT_DELETION_GRACE"

// Errori che il servizio distingue e che il livello HTTP traduce in codici.
var (
	// ErrNotFound indica un account che non esiste.
	ErrNotFound = errors.New("account: non trovato")

	// ErrAlreadyRequested indica una cancellazione già in corso. La richiesta
	// **non** è idempotente di proposito: ripeterla sposterebbe in avanti la
	// scadenza della grazia, cioè rimanderebbe una cancellazione che l'utente
	// aveva già chiesto, senza che nessuno l'abbia deciso.
	ErrAlreadyRequested = errors.New("account: cancellazione già richiesta")

	// ErrNotRequested indica un annullamento senza niente da annullare.
	ErrNotRequested = errors.New("account: nessuna cancellazione in corso")

	// ErrSubscriptionActive indica una richiesta senza la presa d'atto, su un
	// account con una sottoscrizione a pagamento viva. Vedi
	// [Service.RequestDeletion].
	ErrSubscriptionActive = errors.New("account: sottoscrizione a pagamento attiva")
)

// Status è ciò che l'utente vede della propria cancellazione.
type Status struct {
	// Requested dice se una cancellazione è in corso.
	Requested bool
	// RequestedAt e PurgeAfter delimitano la finestra di ripensamento. Zero se
	// non c'è nessuna richiesta.
	RequestedAt time.Time
	PurgeAfter  time.Time

	// Subscription è la sottoscrizione a pagamento viva, se c'è. Sta qui perché
	// è ciò che decide se la richiesta ha bisogno della presa d'atto, e la
	// dashboard deve poterlo sapere **prima** di ricevere un rifiuto.
	Subscription Subscription
}

// Subscription è quel tanto di sottoscrizione che serve a decidere.
//
// Non è il modello di internal/billing e non deve diventarlo: qui interessa una
// domanda sola — «l'utente sta pagando qualcosa che questa cancellazione non
// ferma?».
type Subscription struct {
	// Paid è vero se c'è una sottoscrizione viva su un piano a pagamento, cioè
	// una che Paddle continuerà a fatturare dopo che l'account non ci sarà più.
	Paid bool
	// PlanCode è il piano, per il messaggio all'utente.
	PlanCode string
	// PaddleSubscriptionID è l'identificativo da mostrare a chi va ad annullare
	// presso Paddle.
	PaddleSubscriptionID string
}

// Receipt è ciò che la richiesta ha fermato e revocato. Serve alla risposta
// dell'API e al log: «abbiamo interrotto le esecuzioni e revocato le chiavi» è
// una frase che va accompagnata dai numeri, altrimenti non è verificabile.
type Receipt struct {
	RequestedAt time.Time
	PurgeAfter  time.Time

	JobsStopped     int
	KeysRevoked     int
	SecretsRevoked  int
	AIKeysRevoked   int
	SessionsRevoked int
	TokensRevoked   int
}

// Restored è ciò che l'annullamento ha rimesso in piedi.
//
// **Le chiavi non ci sono, e non è una dimenticanza.** La revoca ha svuotato il
// materiale cifrato (0012 e 0016 lo impongono con un vincolo), quindi non esiste
// niente da restituire: la privacy policy dice «revoke keys immediately» e la
// promessa è irreversibile per costruzione. Chi torna indietro ritrova i propri
// job e i propri dati, e si riemette le chiavi.
type Restored struct {
	JobsResumed int
}

// Purged è il resoconto di una purga, tabella per tabella.
//
// L'elenco è dettagliato perché è la prova di ciò che il documento promette: un
// conteggio unico direbbe «ho cancellato qualcosa», e la domanda a cui bisogna
// saper rispondere è «che cosa».
type Purged struct {
	UserID string

	Executions   int64
	Jobs         int64
	AuditDeleted int64
	// AuditKept sono le righe di audit sopravvissute: quelle in cui ad agire è
	// stato **un altro** — un admin che ha impersonato questo utente (SPEC §4.3).
	// Restano perché documentano l'azione di quell'altro, e sparirebbero se
	// bastasse convincere l'utente a chiudere l'account. I riferimenti al
	// cancellato li azzera la foreign key della 0008 (ON DELETE SET NULL); il
	// `metadata` lo svuota la purga.
	AuditKept        int64
	PaddleEvents     int64
	GitHubDeliveries int64

	// Batches sono i lotti in cui le esecuzioni sono state cancellate, e
	// Truncated dice che ne restavano: la purga si è fermata al tetto e la
	// riprende la passata successiva. Sovrastimare è deliberato — «forse ne
	// restano» va detto, «ho finito» dev'essere vero.
	Batches   int
	Truncated bool
}

// Store è la persistenza di cui la cancellazione ha bisogno.
//
// È un'interfaccia e non il pool di pgx per la stessa ragione di [auth.Store] e
// di [secrets.Store]: la logica di questo package — la conferma, la presa
// d'atto, il calcolo della scadenza — si prova senza database, e le query
// vivono in internal/accountpg dove si leggono tutte insieme. Qui non c'è
// nessun SQL, ed è il punto: **una cancellazione sbagliata non si scopre
// leggendo il codice che la ordina, si scopre leggendo le istruzioni che la
// eseguono.**
type Store interface {
	// Status legge lo stato della cancellazione e la sottoscrizione viva.
	Status(ctx context.Context, userID string) (Status, error)

	// RequestDeletion apre la finestra e, **nella stessa transazione**, ferma i
	// job e revoca chiavi, segreti, credenziali AI, sessioni e token pendenti.
	// Non è divisibile in due chiamate: fra l'una e l'altra ci sarebbe un
	// istante in cui l'account è dichiarato in cancellazione e il motore lo sta
	// ancora eseguendo.
	//
	// Restituisce [ErrAlreadyRequested] se una richiesta è già in corso.
	RequestDeletion(ctx context.Context, userID string, at, purgeAfter time.Time) (Receipt, error)

	// CancelDeletion chiude la finestra e riaccende **solo** i job che la
	// richiesta aveva fermato. Restituisce [ErrNotRequested] se non c'era nulla
	// da annullare.
	CancelDeletion(ctx context.Context, userID string) (Restored, error)

	// DueForPurge elenca gli account la cui grazia è scaduta, al più `limit`.
	DueForPurge(ctx context.Context, now time.Time, limit int) ([]string, error)

	// Purge rimuove un account e tutto ciò che gli appartiene. È idempotente su
	// un account già rimosso e riprendibile su uno lasciato a metà: vedi
	// [Purged.Truncated].
	Purge(ctx context.Context, userID string) (Purged, error)
}

// annotate uniforma il prefisso degli errori del package.
func annotate(what string, err error) error {
	return fmt.Errorf("account: %s: %w", what, err)
}
