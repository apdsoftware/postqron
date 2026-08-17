// Package apikeys implementa le chiavi API di Postqron (R9): generazione,
// conservazione come hash, scope e revoca immediata.
//
// Come internal/auth, il package contiene la logica e non conosce né HTTP né
// PostgreSQL: le rotte stanno in internal/httpapi, la persistenza dietro
// l'interfaccia [Store] con l'implementazione in internal/apikeypg.
//
// # Le quattro proprietà che il package deve garantire
//
// **1. La chiave in chiaro esiste una volta sola.** [Service.Create] è l'unico
// punto in cui il valore in chiaro compare, e lo restituisce al chiamante che
// l'ha richiesta. A riposo resta soltanto la sua impronta, e non esiste nessuna
// funzione — né qui, né nello [Store], né nelle rotte — che ricostruisca o
// restituisca una chiave dopo la creazione. Nemmeno per un amministratore: la
// domanda «qual era quella chiave» non ha una risposta da nessuna parte del
// sistema, ed è voluto.
//
// **2. La ricerca è indicizzata e il confronto è a tempo costante.** L'impronta
// è un HMAC-SHA256 deterministico ([auth.Keyring.APIKeyHash]): deterministico
// significa che la si cerca con un'uguaglianza sull'indice unico della 0002,
// senza scandire le chiavi dell'utente né quelle di tutti. Il confronto finale
// passa comunque da [hmac.Equal]; vedi [Service.Authenticate] per il motivo per
// cui vale la pena farlo anche quando la ricerca è già per uguaglianza.
//
// **3. Gli scope si applicano dove le rotte decidono.** Qui vivono l'elenco
// degli scope legittimi e [Key.Allows]; l'applicazione è nel guard di
// internal/httpapi, che è l'unico punto da cui passano tutte le rotte. Uno scope
// verificato dentro ai singoli handler sarebbe uno scope dimenticato al primo
// handler nuovo.
//
// **4. La revoca ha effetto al tentativo successivo.** Non c'è cache: ogni
// richiesta autenticata con chiave fa una lettura indicizzata e valuta
// [Key.Active] sulla riga appena letta. Il prezzo è una SELECT per richiesta; la
// contropartita è che «revocata» significa revocata adesso e non fra cinque
// minuti.
package apikeys

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
)

// -------------------------------------------------------------------- scope

// Scope è un permesso in forma `risorsa:azione`, come lo conserva la colonna
// `scopes` della migrazione 0002.
type Scope string

// Gli scope riconosciuti. Corrispondono uno a uno alle decisioni che le rotte di
// R8 prendono, e non a una tassonomia astratta: un permesso che non separa due
// rotte diverse non separa niente.
//
// I permessi di scrittura **non** implicano quelli di lettura. È una scelta, e
// la ragione è che l'implicazione è una regola che vive solo nel codice: chi
// guarda l'elenco degli scope di una chiave vedrebbe `jobs:write` e dovrebbe
// sapere a memoria che comprende anche la lettura. Meglio due voci esplicite.
const (
	// ScopeJobsRead consulta i job: `GET /jobs`, `GET /jobs/{id}`.
	ScopeJobsRead Scope = "jobs:read"
	// ScopeJobsWrite crea, modifica ed elimina i job.
	ScopeJobsWrite Scope = "jobs:write"
	// ScopeExecutionsRead consulta il registro delle esecuzioni.
	ScopeExecutionsRead Scope = "executions:read"
	// ScopeExecutionsTrigger avvia un'esecuzione manuale.
	//
	// È separato da ScopeJobsWrite perché sono due poteri diversi: modificare la
	// definizione di un job e far partire adesso una chiamata verso l'esterno. Una
	// chiave da cruscotto vuole il secondo senza il primo.
	ScopeExecutionsTrigger Scope = "executions:trigger"
)

// allScopes è l'elenco completo, nell'ordine in cui va mostrato.
var allScopes = []Scope{ScopeJobsRead, ScopeJobsWrite, ScopeExecutionsRead, ScopeExecutionsTrigger}

// Scopes restituisce gli scope assegnabili a una chiave.
func Scopes() []Scope { return slices.Clone(allScopes) }

// Valid indica se lo scope è fra quelli riconosciuti.
func (s Scope) Valid() bool { return slices.Contains(allScopes, s) }

func (s Scope) String() string { return string(s) }

// La gestione delle chiavi API **non** ha uno scope, e non è una dimenticanza:
// nessuna chiave può creare, elencare o revocare chiavi. Se potesse, una chiave
// di sola lettura sarebbe a un passo dall'emetterne una di scrittura, e l'intero
// sistema di scope diventerebbe una formalità aggirabile in una richiesta. Le
// rotte `/keys` esigono una sessione: vedi authAPI in internal/httpapi.

// ---------------------------------------------------------------------- chiave

// Key è una chiave API già ridotta alla sua impronta.
//
// Non contiene il valore in chiaro perché in questa struttura non c'è mai
// stato: [Service.Create] lo restituisce a parte, in [Created], e la Key che
// finisce nel database e nei log è questa.
type Key struct {
	ID     string
	UserID string
	Name   string

	// Prefix è la parte iniziale della chiave, in chiaro, che serve a
	// riconoscerla in elenco («quella che comincia per pq_live_a1b2»). Non è un
	// segreto: sono [prefixLength] caratteri di una chiave da [tokenBits] bit, e
	// ciò che resta è comunque fuori portata. Non è nemmeno la chiave di ricerca —
	// quella è [Key.Hash].
	Prefix string

	// Hash è l'impronta HMAC della chiave. È l'unica forma in cui la chiave esiste
	// a riposo. Non va serializzata verso nessun client: vedi [Key.LogValue] per
	// il motivo per cui non finisce nemmeno nei log.
	Hash string

	Scopes []Scope

	LastUsedAt *time.Time
	ExpiresAt  *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// Active indica se la chiave è utilizzabile a un dato istante.
//
// Sono due condizioni e vanno lette in quest'ordine: la revoca è definitiva, la
// scadenza è facoltativa (una chiave senza `expires_at` non scade).
func (k Key) Active(now time.Time) bool {
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !now.Before(*k.ExpiresAt) {
		return false
	}
	return true
}

// Revoked indica se la chiave è stata revocata.
func (k Key) Revoked() bool { return k.RevokedAt != nil }

// Allows indica se la chiave porta lo scope richiesto.
//
// Un elenco vuoto non autorizza niente: una chiave senza scope è legittima —
// la 0002 la ammette — ma è una chiave che non può fare nulla finché non le si
// assegna qualcosa. È anche il comportamento che rende sicuro il valore zero di
// [Key]: una struttura non inizializzata non apre nessuna porta.
func (k Key) Allows(scope Scope) bool { return slices.Contains(k.Scopes, scope) }

// LogValue è la forma con cui la chiave compare nei log.
//
// Esiste perché `slog.Any("key", k)` è la riga che qualcuno prima o poi scrive,
// e senza questo metodo stamperebbe anche [Key.Hash]. L'impronta non è la chiave
// e non è invertibile, ma è materiale con cui si autentica chi ha anche il
// segreto di firma: nei log non serve, e ciò che non serve nei log non ci va
// (SPEC §5). Restano identificativo, prefisso e scope, che sono esattamente ciò
// che serve a capire quale chiave ha fatto cosa.
func (k Key) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", k.ID),
		slog.String("user_id", k.UserID),
		slog.String("prefix", k.Prefix),
		slog.Int("scopes", len(k.Scopes)),
	)
}

// Created è l'esito di una creazione.
type Created struct {
	Key Key
	// Secret è la chiave in chiaro. È l'unico momento in cui esiste: va
	// consegnata al chiamante e dimenticata. Non si scrive in un log, non si
	// serializza in nessuna risposta che non sia quella della creazione, e non
	// esiste nessun modo di rileggerla dopo.
	Secret string
}

// -------------------------------------------------------------------- errori

var (
	// ErrNotFound indica che la riga cercata non esiste. Lo restituisce lo
	// [Store]; il [Service] non lo fa uscire dal package così com'è.
	ErrNotFound = errors.New("non trovato")

	// ErrKeyNotFound è restituito quando la chiave da revocare non esiste, non è
	// dell'utente o era già revocata. I tre casi non si distinguono: farlo
	// direbbe a chiunque se un identificativo altrui è vivo.
	ErrKeyNotFound = errors.New("chiave non trovata")

	// ErrInvalidKey è l'unico esito di un'autenticazione con chiave non riuscita,
	// qualunque sia la causa: chiave inesistente, malformata, scaduta, revocata,
	// o appartenente a un account cancellato. Distinguere permetterebbe di usare
	// l'API come oracolo sulla validità di una chiave trovata in giro.
	ErrInvalidKey = errors.New("chiave API assente o non più valida")

	// ErrTooManyKeys è restituito quando l'utente ha raggiunto il tetto di
	// [MaxActiveKeys].
	ErrTooManyKeys = errors.New("troppe chiavi attive")
)

// ValidationError raccoglie i motivi per cui una richiesta di creazione è stata
// rifiutata, ancorati al campo che li causa: è la stessa forma che usa
// internal/jobs, perché è quella che permette a un form di evidenziare i campi
// senza interpretare un messaggio.
type ValidationError struct {
	Fields []FieldError
}

// FieldError è un motivo di rifiuto ancorato a un campo.
type FieldError struct {
	Field   string
	Code    string
	Message string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, field := range e.Fields {
		parts = append(parts, field.Field+": "+field.Message)
	}
	return "richiesta non valida — " + strings.Join(parts, "; ")
}

// AsValidation estrae l'errore di validazione da una catena di errori.
func AsValidation(err error) (*ValidationError, bool) {
	var invalid *ValidationError
	if errors.As(err, &invalid) {
		return invalid, true
	}
	return nil, false
}

func (e *ValidationError) add(field, code, message string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Code: code, Message: message})
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// -------------------------------------------------------------------- store

// Store è la persistenza di cui le chiavi API hanno bisogno.
//
// È un'interfaccia e non il pool di pgx per la stessa ragione di [auth.Store]: i
// comportamenti che questa issue deve garantire — la revoca che ha effetto al
// tentativo successivo, lo scope negato, il fatto che la chiave in chiaro non
// sia ricostruibile — si verificano sul [Service], e devono restare verificabili
// su una macchina senza `make db-up`. Le proprietà che dipendono da PostgreSQL
// (unicità dell'impronta, ambito della revoca sulla riga) sono provate in
// internal/apikeypg contro il database vero.
//
// Nell'interfaccia non c'è, e non va aggiunta, nessuna operazione che restituisca
// una chiave in chiaro: non esiste il dato da restituire.
type Store interface {
	// CreateKey registra una chiave nuova. L'impronta è unica per contratto
	// dell'indice `api_keys_key_hash_key`.
	CreateKey(ctx context.Context, key Key) (Key, error)

	// KeyByHash cerca una chiave per impronta, **compresa** quella revocata o
	// scaduta: è il [Service] a decidere che non vale più, e restituire
	// [ErrNotFound] già qui renderebbe impossibile distinguere nei log una chiave
	// mai esistita da una ritirata.
	KeyByHash(ctx context.Context, hash string) (Key, error)

	// ListKeys elenca le chiavi di un utente, dalla più recente. Con
	// `includeRevoked` falso restituisce solo quelle non revocate.
	ListKeys(ctx context.Context, userID string, includeRevoked bool) ([]Key, error)

	// RevokeKey revoca una chiave dell'utente indicato. L'ambito sull'utente è
	// parte del contratto e va imposto nella query, non nel chiamante: senza,
	// l'identificativo di una chiave altrui basterebbe a spegnerla. Restituisce
	// [ErrNotFound] se la chiave non esiste, non è dell'utente o era già revocata.
	RevokeKey(ctx context.Context, userID, keyID string, at time.Time) error

	// TouchKey aggiorna `last_used_at`.
	TouchKey(ctx context.Context, keyID string, at time.Time) error

	// CountActiveKeys conta le chiavi vive di un utente a un dato istante.
	CountActiveKeys(ctx context.Context, userID string, now time.Time) (int, error)
}

// Users è la parte di [auth.Store] che serve a risolvere il proprietario di una
// chiave.
//
// È un'interfaccia ristretta e non l'intero [auth.Store] perché è tutto ciò che
// questo package usa, e perché così la soddisfano sia l'implementazione
// PostgreSQL sia il doppio di prova dell'autenticazione senza scrivere un
// adattatore.
type Users interface {
	UserByID(ctx context.Context, id string) (auth.User, error)
}
