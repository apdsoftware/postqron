// Package marketing manda le email di prodotto a chi le ha chieste, e smette
// quando chiede di smettere (Privacy Policy §2.8).
//
// # Perché non è internal/notify
//
// La coda delle notifiche di #420 sa già accodare, scegliere la lingua,
// raggruppare, recapitare e ritentare, e riusarla sembrava la scelta ovvia. Non
// lo è, e il motivo non è architetturale: è che le due famiglie di email hanno
// **regole opposte in ogni punto in cui una regola c'è**.
//
//   - La coda ha una politica anti-spam pensata perché un avviso arrivi
//     comunque: raggruppa e limita, ma **manda sempre**. Qui manca il permesso
//     stesso di mandare, e va verificato prima di ogni singolo invio.
//   - La coda ha una finestra di grazia in cui i fallimenti si accumulano.
//     Qui non c'è niente da raggruppare: una comunicazione di prodotto non
//     arriva a raffica, e ritardarla di cinque minuti non serve a nessuno.
//   - La coda non consulta nessun consenso, e non deve: un avviso di sicurezza
//     si manda perché è successo qualcosa, non perché l'utente ha detto di sì.
//   - Il layout scrive il link di disiscrizione solo per gli eventi che
//     [emailrender.KindOf] dichiara di marketing, e la coda si rifiuta di
//     recapitarli.
//
// Ciò che è comune è stato riusato davvero: i template (internal/emailrender),
// il recapito (internal/mailronix) e la regola della lingua (R33, il profilo
// decide). Ciò che non lo era non è stato piegato.
//
// # Il consenso è un atto positivo
//
// §2.8 dice che la base giuridica è il consenso dell'Art. 6(1)(a), «asked for on
// its own and never bundled with accepting the terms or creating an account», e
// che «refusing costs you nothing». Nel codice si traduce in tre proprietà:
//
//  1. **L'assenza di una decisione è un rifiuto.** Non esiste un valore
//     predefinito, né una colonna che nasca a `true`: [Store.Recipient]
//     restituisce `Consented` falso per chi non ha mai deciso, e la vista
//     `marketing_consent_state` della 0019 non elenca chi non compare.
//  2. **Il consenso si presta da solo.** L'unica rotta che lo concede non fa
//     nient'altro, e la 0019 impedisce al database di registrare un consenso
//     arrivato dal link di disiscrizione.
//  3. **Rifiutare non tocca niente.** Non c'è una riga di questo package che
//     legga il consenso per decidere qualcosa che non sia un'email di
//     marketing.
//
// # Il consenso si verifica dove si legge l'indirizzo
//
// [Store.Recipient] restituisce indirizzo, lingua e consenso **con una sola
// query**. È deliberato: leggere il destinatario e poi chiedere il consenso
// lascerebbe in mezzo una finestra in cui l'utente si disiscrive e l'email parte
// lo stesso. Con una lettura sola quella finestra non esiste, e «senza consenso
// non parte niente» diventa una proprietà della query invece che dell'ordine in
// cui sono scritte due chiamate.
//
// # 202 non è un recapito (R20.1)
//
// Vale qui come nella coda transazionale, e qui pesa di più: la disiscrizione
// che promettiamo è che **smettiamo di mandare**, non che l'utente smetta di
// ricevere. Non possiamo promettere la seconda, perché la risposta di Mailronix
// è identica per un destinatario recapitabile e per uno in suppression list, e
// non sapremmo dire quali messaggi siano davvero arrivati. Ciò che questo
// package garantisce è verificabile da noi: dopo una revoca, nessun invio parte.
package marketing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Decision è la decisione presa dall'utente, con gli stessi due valori del tipo
// `marketing_consent_decision` della migrazione 0019.
type Decision string

const (
	// DecisionGranted è il consenso prestato (Art. 6(1)(a)).
	DecisionGranted Decision = "granted"
	// DecisionWithdrawn è la revoca. Ferma il marketing e **nient'altro**:
	// §2.8 dice «unsubscribing stops marketing email only».
	DecisionWithdrawn Decision = "withdrawn"
)

// Source è da dove è arrivata la decisione, come il tipo
// `marketing_consent_source` della 0019.
//
// Non è telemetria: è la parte della prova che dice **come** il consenso è stato
// raccolto, ed è ciò che permette di mostrare che non è stato chiesto in blocco
// con l'accettazione dei termini.
type Source string

const (
	// SourceProfile è la dashboard, con una sessione, da un comando che non fa
	// nient'altro.
	SourceProfile Source = "profile"
	// SourceUnsubscribeLink è il link nel piè di pagina di un'email di
	// marketing, che per §2.8 funziona senza accedere. Può produrre **solo**
	// una revoca: il database lo impone, non il codice.
	SourceUnsubscribeLink Source = "unsubscribe_link"
)

// Record è una decisione da scrivere nella traccia.
type Record struct {
	UserID   string
	Decision Decision
	Source   Source
	// IP è l'indirizzo da cui è arrivata la decisione. Facoltativo, e usato con
	// parsimonia: serve a distinguere una revoca chiesta dall'utente da una
	// arrivata da chissà dove — che, con un link che funziona senza accedere, è
	// una domanda che ci verrà posta.
	IP string
}

// Validate rifiuta una decisione che il database scarterebbe, e una che non
// dovrebbe esistere.
func (r Record) Validate() error {
	if strings.TrimSpace(r.UserID) == "" {
		return errors.New("marketing: decisione senza utente")
	}
	if r.Decision != DecisionGranted && r.Decision != DecisionWithdrawn {
		return fmt.Errorf("marketing: decisione sconosciuta: %q", r.Decision)
	}
	if r.Source != SourceProfile && r.Source != SourceUnsubscribeLink {
		return fmt.Errorf("marketing: provenienza sconosciuta: %q", r.Source)
	}
	// Lo stesso vincolo della 0019, ripetuto qui perché l'errore che produce è
	// leggibile e arriva prima del viaggio fino al database. Il vincolo che
	// conta resta quello, che nessun `if` dimenticato può aggirare.
	if r.Decision == DecisionGranted && r.Source == SourceUnsubscribeLink {
		return errors.New(
			"marketing: un consenso non si presta dal link di disiscrizione, che funziona senza accedere: " +
				"servirebbe a chiunque abbia il link per iscrivere l'intestatario dell'indirizzo")
	}
	return nil
}

// Applied è l'esito della scrittura di una decisione.
type Applied string

const (
	// Recorded: la decisione è nuova e la traccia si è allungata.
	Recorded Applied = "recorded"
	// Unchanged: lo stato era già quello. Non nasce una seconda riga, e non è
	// un errore — è il secondo clic sullo stesso link, o il rinvio di un form.
	Unchanged Applied = "unchanged"
	// NoUser: l'account non esiste o è stato chiuso. Non è un errore per chi
	// chiama: a un account che non c'è non si scrive comunque.
	NoUser Applied = "no_user"
)

// State è l'ultima decisione di un utente.
type State struct {
	UserID string
	// Decided è falso per chi non ha mai deciso. È distinto da `Consented`
	// falso, e la distinzione conta per la dashboard: «non hai ancora scelto» e
	// «hai detto di no» vanno mostrati in modo diverso, ed entrambi significano
	// che non si manda niente.
	Decided bool
	// Consented è vero solo se l'ultima decisione è [DecisionGranted].
	Consented bool
	// OccurredAt è quando quella decisione è stata presa. Zero se non ce n'è.
	OccurredAt time.Time
	Source     Source
}

// Entry è una riga della traccia: la prova che §2.8 promette di conservare.
type Entry struct {
	Decision   Decision
	Source     Source
	OccurredAt time.Time
}

// Recipient è chi riceve, letto **insieme al suo consenso**.
//
// I due valori arrivano dalla stessa query e non da due chiamate: vedi la
// sezione «Il consenso si verifica dove si legge l'indirizzo» nella doc del
// package.
type Recipient struct {
	UserID string
	// Email è l'indirizzo. Non finisce **mai** in un log: è un dato personale, e
	// nel log non serve a niente che l'identificativo dell'utente non copra già.
	Email string
	Name  string
	// Language è la lingua del profilo (R33), già normalizzata.
	Language string
	// Consented è vero solo se l'ultima decisione registrata è un consenso.
	Consented bool
}

// Store è la traccia del consenso e la lettura dei destinatari.
// L'implementazione PostgreSQL è internal/marketingpg.
type Store interface {
	// Record scrive una decisione, **se cambia lo stato**.
	//
	// Una decisione uguale alla precedente non allunga la traccia: un secondo
	// clic sullo stesso link di disiscrizione, o un form rinviato, non sono due
	// revoche. La traccia deve raccontare le volte in cui l'utente ha cambiato
	// idea, non le volte in cui un browser ha ripetuto una richiesta.
	//
	// La garanzia è sul caso sequenziale. Due richieste davvero simultanee
	// possono lasciare due righe identiche, ed è accettato: vedi la doc di
	// internal/marketingpg per perché quel duplicato non fa danno e perché
	// chiuderlo costerebbe più di quanto valga.
	Record(ctx context.Context, record Record) (Applied, error)

	// State legge l'ultima decisione di un utente.
	State(ctx context.Context, userID string) (State, error)

	// History restituisce la traccia completa, dalla più recente. È la prova di
	// §2.8: «we keep a record of when you consented and when you withdrew».
	History(ctx context.Context, userID string) ([]Entry, error)

	// Recipient legge destinatario e consenso in una sola istruzione.
	//
	// È la lettura del **percorso d'invio**, e risponde a una domanda sola: «a
	// costui possiamo mandare marketing adesso?». Restituisce [ErrNoRecipient]
	// per un account che non esiste, che è chiuso, o che ha chiesto di essere
	// cancellato — l'ultimo caso perché il suo consenso è formalmente in vigore
	// ma il gesto più forte che poteva fare dice il contrario.
	Recipient(ctx context.Context, userID string) (Recipient, error)

	// Language è la lingua del profilo di un account vivo (R33).
	//
	// Serve al percorso della **disiscrizione**, ed è separata da
	// [Store.Recipient] per due motivi che vanno nella stessa direzione.
	//
	// Il primo è che le due domande sono diverse. «Possiamo scrivergli?» esclude
	// chi ha chiesto di essere cancellato; «può disiscriversi?» no — anzi, dirgli
	// che il link non funziona proprio mentre sta chiudendo l'account sarebbe il
	// momento peggiore per farlo, e §2.8 non pone condizioni alla revoca.
	//
	// Il secondo è che qui **l'indirizzo email non serve**, e non leggerlo è il
	// modo più solido di tenerlo fuori da una pagina che apre chiunque abbia un
	// link. Ciò che non viene letto non può essere mostrato per distrazione.
	//
	// Restituisce [ErrNoRecipient] solo se l'account non esiste più.
	Language(ctx context.Context, userID string) (string, error)
}

// ErrNoRecipient segnala che non c'è nessuno a cui scrivere: l'account non
// esiste, o è stato chiuso.
var ErrNoRecipient = errors.New("marketing: nessun destinatario")

// ErrInvalidToken segnala un token di disiscrizione che non si verifica.
//
// **Non distingue i motivi**, e non è pigrizia: un errore che dicesse «firma
// sbagliata» invece di «formato sbagliato» direbbe a chi prova a indovinare
// quanto si è avvicinato. Vale la stessa regola dell'autenticazione.
var ErrInvalidToken = errors.New("marketing: token di disiscrizione non valido")
