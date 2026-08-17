// Package secrets implementa i segreti del workspace di Postqron (R42, R43):
// creazione, aggiornamento, elenco e revoca, e la risoluzione dei riferimenti
// `${VAR}` al momento dell'esecuzione.
//
// Come internal/auth e internal/apikeys, il package contiene la logica e non
// conosce né HTTP né PostgreSQL: le rotte stanno in internal/httpapi/secrets.go,
// la persistenza dietro l'interfaccia [Store] con l'implementazione in
// internal/secretspg, la cifratura in internal/secretbox.
//
// # Le cinque proprietà che il package deve garantire
//
// **1. Il valore è cifrato a riposo.** Nel database entra solo ciò che
// [secretbox.Keyring.Seal] produce. Il testo cifrato è legato alla riga che lo
// contiene — proprietario e nome entrano nell'autenticazione — quindi spostarlo
// su un'altra riga non lo rende leggibile.
//
// **2. Il valore non esce dall'API, perché il tipo non ce l'ha.** [Secret] è la
// forma in cui un segreto viene elencato, restituito e loggato, e **non ha un
// campo per il valore**. Non c'è un filtro da ricordarsi di applicare, non c'è
// una variante «di dettaglio» che ne mostri di più, e non esiste nessun metodo
// dello [Store] che restituisca un valore in chiaro a chi non stia risolvendo
// un'esecuzione. È la lezione delle chiavi API (R9): un filtro si dimentica, un
// campo assente no.
//
// **3. La risoluzione avviene all'esecuzione.** [Service.Resolve] espande i
// `${VAR}` in URL, header e corpo nel momento in cui la richiesta parte. A
// riposo, nella colonna `jobs.headers` e in `cron.yaml`, restano i riferimenti.
//
// **4. Un riferimento non risolvibile fallisce al sync, non alle tre di
// notte.** [Service.Validate] è la stessa analisi di [Service.Resolve] senza la
// decifratura, ed è ciò che il parser di `cron.yaml` chiama prima di scrivere
// qualunque cosa. Vedi resolve.go per il motivo per cui sono due funzioni sulla
// stessa struttura e non due implementazioni che possono divergere.
//
// **5. Il valore non compare nei log.** Il testo in chiaro esiste in due tipi
// soli — [Value] e [Resolved] — e nessuno dei due si stampa: entrambi
// implementano [fmt.Formatter] e [slog.LogValuer] restituendo una forma
// mascherata, per qualunque verbo. Il valore si ottiene chiamando un metodo che
// si chiama `Reveal`, che è una cosa che si scrive apposta.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Vincoli di forma. Sono nel codice e non in configurazione perché sono lo
// stesso contratto che il `CHECK` della 0012 impone al database e che il parser
// di `cron.yaml` deve applicare al file: un limite d'esercizio configurabile
// renderebbe valido in un ambiente ciò che è invalido in un altro.
const (
	// MaxNameLength è la lunghezza massima del nome, allineata al CHECK della
	// 0012.
	MaxNameLength = 64

	// MaxDescriptionLength è la lunghezza massima della nota, allineata al CHECK
	// della 0012.
	MaxDescriptionLength = 200

	// MinValueLength è la lunghezza minima del valore.
	//
	// Non è una misura di entropia — il valore lo sceglie il servizio di
	// destinazione, non noi — ma il prezzo della redazione. Il valore di ogni
	// segreto risolto viene cercato e mascherato dentro l'estratto della risposta
	// (vedi [Redactor]), e un segreto di due caratteri comparirebbe per caso in
	// mezzo a qualunque testo: la redazione smetterebbe di proteggere qualcosa e
	// comincerebbe a corrompere le risposte che l'utente legge.
	MinValueLength = 8

	// MaxValueLength è la lunghezza massima del valore. Un JWT lungo ci sta
	// comodo; un file no, e un segreto non è un file.
	MaxValueLength = 4096

	// MaxSecretsPerWorkspace è il numero massimo di segreti vivi per workspace.
	//
	// **Non è un limite di piano.** È un tetto tecnico contro la creazione senza
	// fine di righe da parte di un account autenticato, nello stesso spirito di
	// apikeys.MaxActiveKeys. Il limite commerciale, se un giorno ci sarà, è R15 e
	// va applicato prima di questo, non al suo posto.
	MaxSecretsPerWorkspace = 200
)

// ---------------------------------------------------------------- il valore

// Value è il valore in chiaro di un segreto.
//
// È una stringa avvolta in un tipo che **non si stampa**. Il punto non è
// l'incapsulamento: è che `slog.String("secret", value)`, `fmt.Sprintf("%s",
// value)` e `log.Printf("%v", input)` sono tre righe che qualcuno scrive prima o
// poi, e con una `string` nuda funzionerebbero tutte e tre — cioè scriverebbero
// il segreto in un log che la privacy policy §2.2 dice conservato e visibile
// all'utente.
//
// Con questo tipo scrivono `«segreto»`. Per ottenere il valore vero bisogna
// chiamare [Value.Reveal], che si legge in una revisione e si cerca con un
// grep.
type Value string

// redacted è ciò che compare al posto del valore ovunque lo si stampi.
const redacted = "«segreto»"

// Reveal restituisce il valore in chiaro.
//
// **È l'unico modo di ottenerlo**, e il nome è scelto per essere impossibile da
// scrivere per distrazione: se compare in una riga che costruisce un log o una
// risposta HTTP, quella riga è un difetto e si vede.
func (v Value) Reveal() string { return string(v) }

// Len è la lunghezza del valore. Serve alla validazione senza rivelare niente.
func (v Value) Len() int { return len(v) }

// Empty indica un valore assente.
func (v Value) Empty() bool { return v == "" }

// String implementa [fmt.Stringer] mascherando il valore.
func (v Value) String() string { return redacted }

// Format implementa [fmt.Formatter].
//
// [fmt.Stringer] da solo non basta: `%q` citerebbe la stringa sottostante e
// `%#v` ne stamperebbe la forma Go, che è il valore fra virgolette con il nome
// del tipo davanti. Implementando Formatter la maschera vale per **ogni** verbo,
// compresi quelli che qualcuno userà fra un anno.
func (v Value) Format(f fmt.State, verb rune) {
	switch verb {
	case 'q':
		fmt.Fprintf(f, "%q", redacted)
	default:
		fmt.Fprint(f, redacted)
	}
}

// LogValue implementa [slog.LogValuer]: nei log strutturati compare la maschera.
func (v Value) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON impedisce che il valore finisca in una risposta o in un file di
// stato serializzando la struttura che lo contiene.
//
// Non è la difesa principale — la difesa principale è che [Secret] il campo non
// ce l'ha — ma è quella che copre i tipi che un giorno lo conterranno per
// forza, come il corpo di una richiesta di creazione.
func (v Value) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// --------------------------------------------------------------- il segreto

// Secret è un segreto del workspace **senza il suo valore**.
//
// Non contiene il valore in chiaro e nemmeno il testo cifrato: è la forma che
// l'API restituisce, che la dashboard elenca e che finisce nei log. Il valore
// esiste solo dentro [Service.Resolve], per il tempo di comporre una richiesta
// HTTP.
//
// La struttura non ha un campo «anteprima», «ultime quattro cifre» o simili, a
// differenza di `ai_credentials.last_four`. La ragione è che lì il valore è una
// chiave di un fornitore noto, che l'utente riconosce dalla coda; qui è un
// valore arbitrario del cliente — potrebbe *essere* quattro caratteri
// significativi — e a riconoscerlo basta il nome che gli ha dato lui.
type Secret struct {
	ID     string
	UserID string

	// Name è il nome con cui `cron.yaml` lo riferisce come `${NOME}`.
	Name string

	// Description è la nota dell'utente. Non è il valore e non ne è un pezzo: è
	// testo che l'utente scrive per sé, e l'API lo restituisce.
	Description string

	// KeyVersion è la versione di ENCRYPTION_KEY con cui la riga è cifrata.
	// Serve alla rotazione e non dice niente sul valore.
	KeyVersion uint16

	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Revoked indica se il segreto è stato revocato.
func (s Secret) Revoked() bool { return s.RevokedAt != nil }

// Live indica se il segreto è ancora risolvibile.
func (s Secret) Live() bool { return s.RevokedAt == nil }

// LogValue è la forma con cui il segreto compare nei log.
//
// Esiste per la stessa ragione dell'omonimo delle chiavi API: `slog.Any("secret",
// s)` è la riga che qualcuno scrive, e senza questo metodo stamperebbe la
// struttura per intero. Qui non c'è niente di segreto da nascondere — il campo
// non esiste — ma il metodo fissa comunque *che cosa* si scrive di un segreto,
// così che aggiungere un campo domani non lo faccia comparire nei log per
// inerzia.
func (s Secret) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", s.ID),
		slog.String("user_id", s.UserID),
		slog.String("name", s.Name),
		slog.Bool("revoked", s.Revoked()),
	)
}

// -------------------------------------------------------------------- errori

var (
	// ErrNotFound indica che la riga cercata non esiste. Lo restituisce lo
	// [Store]; il [Service] non lo fa uscire dal package così com'è.
	ErrNotFound = errors.New("non trovato")

	// ErrSecretNotFound è restituito quando il segreto da aggiornare o revocare
	// non esiste, non è del workspace, o era già revocato. I tre casi non si
	// distinguono: farlo direbbe a chiunque se un identificativo altrui è vivo.
	ErrSecretNotFound = errors.New("segreto non trovato")

	// ErrDuplicateName è restituito quando esiste già un segreto vivo con quel
	// nome. Aggiornarlo è un'altra rotta, e sovrascriverlo in silenzio
	// significherebbe che una `POST` ripetuta cambia il valore di un segreto che
	// altri job stanno usando.
	ErrDuplicateName = errors.New("esiste già un segreto con questo nome")

	// ErrTooManySecrets è restituito quando il workspace ha raggiunto
	// [MaxSecretsPerWorkspace].
	ErrTooManySecrets = errors.New("troppi segreti nel workspace")
)

// ValidationError raccoglie i motivi per cui una richiesta è stata rifiutata,
// ancorati al campo che li causa: è la stessa forma di internal/jobs e
// internal/apikeys, perché è quella che permette a un form di evidenziare i
// campi senza interpretare un messaggio.
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

// --------------------------------------------------------------------- store

// Sealed è una riga di `workspace_secrets` con il suo testo cifrato.
//
// È il solo tipo che porta il materiale cifrato fuori dallo [Store], ed è usato
// da un punto solo: la risoluzione all'esecuzione. Non esce mai dal package —
// nessuna rotta lo serializza, nessun log lo stampa — e il testo in chiaro non
// c'è nemmeno qui: per ottenerlo serve il keyring del processo.
type Sealed struct {
	Secret

	Ciphertext []byte
	Nonce      []byte
}

// LogValue nasconde il materiale cifrato, che nei log non serve.
func (s Sealed) LogValue() slog.Value { return s.Secret.LogValue() }

// Store è la persistenza di cui i segreti hanno bisogno.
//
// È un'interfaccia e non il pool di pgx per la stessa ragione di [apikeys.Store]
// e [auth.Store]: le proprietà che questa issue deve garantire — il valore che
// non esce, il riferimento che fallisce al sync, la revoca che rende il segreto
// non risolvibile — si verificano sul [Service], e devono restare verificabili
// su una macchina senza `make db-up`. Le proprietà che dipendono da PostgreSQL
// (unicità del nome fra i vivi, svuotamento del testo cifrato alla revoca)
// sono provate in internal/secretspg contro il database vero.
//
// Nell'interfaccia non c'è, e non va aggiunta, nessuna operazione che
// restituisca un valore in chiaro: lo [Store] non ha il keyring e non saprebbe
// come produrlo.
type Store interface {
	// CreateSecret registra un segreto nuovo. Restituisce [ErrDuplicateName] se
	// esiste già un segreto vivo con lo stesso nome nello stesso workspace:
	// l'unicità è dell'indice `workspace_secrets_live_name_key`, non di una
	// lettura precedente del chiamante.
	CreateSecret(ctx context.Context, in Sealed) (Secret, error)

	// UpdateSecret sostituisce il valore, e la nota se `description` non è nil.
	// L'ambito sul workspace è parte del contratto e va imposto nella query.
	// Restituisce [ErrNotFound] se il segreto non esiste, non è del workspace o
	// è revocato.
	UpdateSecret(ctx context.Context, userID, secretID string, in Sealed, description *string) (Secret, error)

	// ListSecrets elenca i segreti di un workspace, dal più recente. Con
	// `includeRevoked` falso restituisce solo quelli vivi.
	ListSecrets(ctx context.Context, userID string, includeRevoked bool) ([]Secret, error)

	// SecretByID legge un segreto **vivo** del workspace. L'ambito sul workspace
	// è parte del contratto e va imposto nella query: senza, l'identificativo di
	// un segreto altrui basterebbe a leggerne il nome. Restituisce [ErrNotFound]
	// se non esiste, non è del workspace o è revocato.
	SecretByID(ctx context.Context, userID, secretID string) (Secret, error)

	// LiveByNames restituisce i segreti **vivi** del workspace fra quelli
	// indicati, con il loro testo cifrato. È la lettura della risoluzione (R43):
	// prende i nomi che servono e non tutti, perché un'occorrenza ne riferisce
	// quasi sempre uno solo e questa query gira a ogni esecuzione.
	//
	// I nomi mancanti non sono un errore: chi chiama deve distinguere «non
	// esiste» da «errore di lettura», ed è esattamente la differenza fra un
	// riferimento non risolvibile e un guasto.
	LiveByNames(ctx context.Context, userID string, names []string) ([]Sealed, error)

	// LiveNames elenca i nomi dei segreti vivi del workspace. È ciò che serve
	// alla validazione al sync, che deve dire quali riferimenti non esistono
	// senza decifrare niente.
	LiveNames(ctx context.Context, userID string) ([]string, error)

	// RevokeSecret revoca un segreto del workspace **svuotandone il testo
	// cifrato**: le due cose avvengono nella stessa istruzione perché il vincolo
	// `workspace_secrets_revoked_is_empty_check` non ammette che si separino.
	// Restituisce [ErrNotFound] se il segreto non esiste, non è del workspace o
	// era già revocato.
	RevokeSecret(ctx context.Context, userID, secretID string, at time.Time) error

	// TouchSecrets aggiorna `last_used_at` dei segreti indicati.
	TouchSecrets(ctx context.Context, secretIDs []string, at time.Time) error

	// CountLiveSecrets conta i segreti vivi di un workspace.
	CountLiveSecrets(ctx context.Context, userID string) (int, error)
}
