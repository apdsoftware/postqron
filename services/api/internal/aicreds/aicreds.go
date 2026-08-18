// Package aicreds implementa le chiavi AI degli utenti di Postqron (R18, BYOK):
// inserimento, elenco, revoca, e la lettura in chiaro che serve al backend per
// chiamare il fornitore.
//
// # Perché non è internal/secrets
//
// La domanda va risposta prima di leggere il resto, perché la risposta sbagliata
// sarebbe una seconda cifratura. Le chiavi AI e i segreti del workspace (R42)
// condividono **il modo di proteggere il materiale** e non condividono niente
// altro:
//
//   - la cifratura è la stessa, ed è la stessa *implementazione*:
//     internal/secretbox, con lo stesso keyring del processo, lo stesso
//     `key_version` e la stessa rotazione. Non ce n'è una seconda;
//   - il tipo che non si stampa è lo stesso: [secretbox.Plaintext], di cui
//     `secrets.Value` è un alias. Non ce n'è un secondo;
//   - il ciclo di vita è diverso, e non un po'. Un segreto del workspace ha un
//     **nome** che l'utente sceglie e scrive nel suo `cron.yaml`, e viene
//     risolto dentro la richiesta HTTP di un job — dove il valore rischia di
//     ricomparire nell'estratto della risposta che l'utente legge, ed è per
//     questo che internal/secrets ha cinquecento righe di parser `${VAR}` e un
//     redattore. Una chiave AI non ha nome, non compare in nessuna richiesta
//     dell'utente, e la legge il **nostro** backend per chiamare il fornitore
//     (R30, analisi dei log). Non c'è niente da espandere e niente da redigere
//     in un testo che l'utente vedrà.
//
// Da qui l'identità della riga: una chiave per **provider**, non per nome. Non
// esiste `LiveByNames`, non esiste `Validate`, non esiste `Redactor`. Riusare
// [secrets.Store] avrebbe voluto dire piegarlo per portare un provider dove
// porta un nome, e mettere il parser di `cron.yaml` a un import di distanza
// dalle chiavi AI.
//
// # Le quattro proprietà che il package deve garantire
//
// **1. La chiave è cifrata a riposo.** Nel database entra solo ciò che
// [secretbox.Keyring.SealPlaintext] produce, legato alla riga che lo contiene —
// utente e provider entrano nell'autenticazione — quindi il materiale spostato
// su un'altra riga non si apre.
//
// **2. La chiave non esce dall'API, perché il tipo non ce l'ha.** [Credential]
// è la forma in cui una chiave viene elencata, restituita e loggata, e **non ha
// un campo per la chiave né per un suo frammento**. Non c'è un `last_four` e
// dalla migrazione 0016 non c'è nemmeno la colonna: quattro caratteri della
// chiave in chiaro in ogni backup contraddicono «cifrate a riposo», e a
// distinguere la riga bastano il provider e le date.
//
// **3. La chiave non compare nei log.** Il testo in chiaro esiste in un tipo
// solo, [secretbox.Plaintext], che non si stampa con nessun verbo. E l'unico
// punto in cui torna in chiaro — [Service.Reveal] — non lo logga né lo mette in
// un errore: vedi service.go.
//
// **4. Revocare cancella il materiale.** [Service.Revoke] svuota il testo
// cifrato nella stessa istruzione che scrive la data, perché il vincolo
// `ai_credentials_revoked_is_empty_check` non ammette che si separino. Il
// provider torna libero: l'unicità della 0016 vale fra le sole chiavi vive.
package aicreds

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// ------------------------------------------------------------------ provider

// Provider è il fornitore AI a cui una chiave appartiene.
//
// I valori sono quelli dell'enumerato `ai_provider` della migrazione 0001, e
// l'insieme è chiuso da entrambi i lati: un valore che il database non
// accetterebbe viene rifiutato qui con un messaggio, invece di diventare un 500
// alla scrittura.
type Provider string

const (
	Anthropic Provider = "anthropic"
	OpenAI    Provider = "openai"
	Google    Provider = "google"
)

// Providers elenca i fornitori ammessi, nell'ordine in cui l'API li presenta.
func Providers() []Provider { return []Provider{Anthropic, OpenAI, Google} }

// Valid indica se il provider è uno di quelli ammessi.
func (p Provider) Valid() bool { return slices.Contains(Providers(), p) }

// String implementa [fmt.Stringer].
func (p Provider) String() string { return string(p) }

// ParseProvider normalizza e verifica il nome di un fornitore.
//
// La normalizzazione è minima — spazi ai bordi e minuscole — perché `Anthropic`
// scritto con la maiuscola in un corpo JSON è la stessa intenzione di
// `anthropic`, e rifiutarlo sarebbe pignoleria su un valore che non è un
// identificatore dell'utente ma un termine del nostro vocabolario.
func ParseProvider(raw string) (Provider, bool) {
	provider := Provider(strings.ToLower(strings.TrimSpace(raw)))
	return provider, provider.Valid()
}

// ------------------------------------------------------------- vincoli di forma

const (
	// MaxLabelLength è la lunghezza massima dell'etichetta, allineata al CHECK
	// della 0007.
	MaxLabelLength = 100

	// MinKeyLength è la lunghezza minima di una chiave.
	//
	// Non è una misura di entropia — il formato lo decide il fornitore, non noi —
	// ma la soglia sotto la quale ciò che è stato incollato quasi certamente non
	// è una chiave: le chiavi dei tre fornitori ammessi stanno tutte fra i
	// trentanove e i cento e passa caratteri. Venti è largo apposta, per non
	// rifiutare un formato che cambia.
	MinKeyLength = 20

	// MaxKeyLength è la lunghezza massima. Una chiave non è un file, e il limite
	// serve a non far arrivare al cifrario un corpo di richiesta intero.
	MaxKeyLength = 512
)

// --------------------------------------------------------------- la credenziale

// Credential è una chiave AI **senza la chiave**.
//
// Non contiene il valore in chiaro, non contiene il testo cifrato e non contiene
// nessun frammento del valore: è la forma che l'API restituisce, che la
// dashboard elenca e che finisce nei log. Il valore in chiaro esiste solo dentro
// [Service.Reveal], per il tempo di comporre la chiamata al fornitore.
//
// La struttura non ha, e non deve avere, un campo «ultime quattro cifre» o
// «anteprima». La 0007 ne prevedeva uno e la 0016 lo ha tolto: la ragione per
// esteso sta nel commento di quella migrazione, e in breve è che
// `ai_credentials` ha una chiave sola per provider — «quale chiave è questa» lo
// dice il provider, «è quella che ho appena ruotato» lo dice `UpdatedAt`.
type Credential struct {
	ID     string
	UserID string

	// Provider è il fornitore, ed è l'identità della riga: ce n'è una viva sola
	// per ciascuno.
	Provider Provider

	// Label è la nota dell'utente («chiave del piano team»). Non è la chiave e
	// non ne è un pezzo: è l'unico testo di questa riga che l'utente scrive e
	// l'API restituisce.
	Label string

	// KeyVersion è la versione di ENCRYPTION_KEY con cui la riga è cifrata.
	// Serve alla rotazione e non dice niente sulla chiave.
	KeyVersion uint16

	LastUsedAt *time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Revoked indica se la chiave è stata revocata.
func (c Credential) Revoked() bool { return c.RevokedAt != nil }

// Live indica se la chiave è ancora utilizzabile.
func (c Credential) Live() bool { return c.RevokedAt == nil }

// LogValue è la forma con cui la credenziale compare nei log.
//
// Qui non c'è niente di segreto da nascondere — il campo non esiste — ma il
// metodo fissa comunque *che cosa* si scrive di una chiave, così che un campo
// aggiunto domani non ci finisca per inerzia. È la stessa ragione per cui ce
// l'hanno [secrets.Secret] e le chiavi API.
func (c Credential) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", c.ID),
		slog.String("user_id", c.UserID),
		slog.String("provider", string(c.Provider)),
		slog.Bool("revoked", c.Revoked()),
	)
}

// -------------------------------------------------------------------- errori

var (
	// ErrNotFound indica che la riga cercata non esiste. Lo restituisce lo
	// [Store]; il [Service] non lo fa uscire dal package così com'è.
	ErrNotFound = errors.New("non trovato")

	// ErrCredentialNotFound è restituito quando la chiave da revocare non
	// esiste, non è dell'utente, o era già revocata. I tre casi non si
	// distinguono: farlo direbbe a chiunque se un identificativo altrui è vivo.
	ErrCredentialNotFound = errors.New("chiave AI non trovata")

	// ErrNoLiveKey è restituito da [Service.Reveal] quando l'utente non ha una
	// chiave viva per quel provider. È l'errore che il debugging AI (R30) deve
	// saper distinguere da un guasto, per dire «configura una chiave» invece di
	// «riprova più tardi».
	ErrNoLiveKey = errors.New("nessuna chiave AI configurata per questo provider")
)

// ValidationError raccoglie i motivi per cui una richiesta è stata rifiutata,
// ancorati al campo che li causa: è la stessa forma di internal/secrets e
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

// --------------------------------------------------------------------- store

// Sealed è una riga di `ai_credentials` con il suo materiale cifrato.
//
// È il solo tipo che porta il materiale fuori dallo [Store], e lo usano due
// punti: la scrittura e [Service.Reveal]. Non esce mai dal package — nessuna
// rotta lo serializza — e il testo in chiaro non c'è nemmeno qui: per ottenerlo
// serve il keyring del processo.
type Sealed struct {
	Credential

	Ciphertext []byte
	Nonce      []byte
}

// LogValue nasconde il materiale cifrato, che nei log non serve.
func (s Sealed) LogValue() slog.Value { return s.Credential.LogValue() }

// Store è la persistenza di cui le chiavi AI hanno bisogno.
//
// È un'interfaccia e non il pool di pgx per la stessa ragione di [secrets.Store]
// e [apikeys.Store]: le proprietà che R18 chiede — la chiave che non esce, il
// materiale che sparisce alla revoca — si verificano sul [Service], e devono
// restare verificabili su una macchina senza `make db-up`. Le proprietà che
// dipendono da PostgreSQL — l'unicità fra le sole chiavi vive, il vincolo che
// lega la revoca allo svuotamento — sono provate in internal/aicredspg contro il
// database vero.
//
// Nell'interfaccia non c'è, e non va aggiunta, nessuna operazione che
// restituisca una chiave in chiaro: lo [Store] non ha il keyring e non saprebbe
// come produrla.
type Store interface {
	// UpsertCredential registra la chiave di un provider, sostituendo quella
	// viva se c'è.
	//
	// È un'operazione sola e non una coppia crea/aggiorna perché la 0016 ammette
	// **una chiave viva per provider**: due rotte produrrebbero due modi di
	// arrivare allo stesso stato, e la seconda volta che un utente incolla la sua
	// chiave Anthropic non è un conflitto da segnalare — è la stessa intenzione
	// della prima. L'ambito sull'utente è parte del contratto e va imposto nella
	// query.
	UpsertCredential(ctx context.Context, in Sealed) (Credential, error)

	// ListCredentials elenca le chiavi di un utente, dalla più recente. Con
	// `includeRevoked` falso restituisce solo quelle vive.
	ListCredentials(ctx context.Context, userID string, includeRevoked bool) ([]Credential, error)

	// LiveByProvider legge la chiave **viva** di un utente per un provider, con
	// il suo materiale cifrato. È la lettura che precede una chiamata al
	// fornitore (R30). Restituisce [ErrNotFound] se non c'è o è revocata.
	LiveByProvider(ctx context.Context, userID string, provider Provider) (Sealed, error)

	// RevokeCredential revoca una chiave dell'utente **svuotandone il materiale
	// cifrato**: le due cose avvengono nella stessa istruzione perché il vincolo
	// `ai_credentials_revoked_is_empty_check` non ammette che si separino.
	// Restituisce [ErrNotFound] se la chiave non esiste, non è dell'utente o era
	// già revocata.
	RevokeCredential(ctx context.Context, userID, credentialID string, at time.Time) error

	// TouchCredential aggiorna `last_used_at` di una chiave.
	TouchCredential(ctx context.Context, credentialID string, at time.Time) error
}

// binding è il dato che lega un testo cifrato alla riga che lo contiene.
//
// Entra nell'autenticazione di AES-GCM (vedi internal/secretbox): un materiale
// cifrato spostato su un'altra riga — altro utente, altro provider — non si apre,
// e quindi non diventa una chiave utilizzabile per conto di qualcun altro.
//
// Il dominio in testa è diverso da quello dei segreti del workspace, e
// deliberatamente: con lo stesso keyring, un dominio condiviso renderebbe
// apribile come chiave AI un segreto del workspace che avesse per caso lo stesso
// utente e lo stesso nome del provider.
//
// Il separatore è un byte che nei due campi non può comparire, così che
// `("ab", "c")` e `("a", "bc")` non producano lo stesso legame.
func binding(userID string, provider Provider) []byte {
	return []byte("postqron/v1/ai-credential\x00" + userID + "\x00" + string(provider))
}

// validateWrite normalizza e verifica provider, chiave ed etichetta.
//
// I motivi di rifiuto si raccolgono tutti invece di fermarsi al primo: chi
// compila un form vuole sapere in un giro tutto quello che c'è da correggere.
func validateWrite(rawProvider string, key secretbox.Plaintext, label string) (Provider, string, error) {
	invalid := &ValidationError{}

	provider, ok := ParseProvider(rawProvider)
	if !ok {
		invalid.add("provider", "invalid_provider", fmt.Sprintf(
			"Provider non riconosciuto. Quelli ammessi sono: %s.", providerList()))
	}

	// La chiave **non** viene ripulita dagli spazi ai bordi: si rifiuta. Toglierli
	// significherebbe salvare qualcosa di diverso da ciò che l'utente ha
	// incollato, e se un giorno un fornitore emettesse una chiave con uno spazio
	// significativo il fallimento sarebbe fra i più difficili da capire che
	// esistano. Rifiutare invece lo dice subito, ed è il caso comune: una riga
	// copiata da un `.env` porta con sé l'a capo.
	switch {
	case key.Empty():
		invalid.add("key", "required", "Incolla la chiave API del fornitore.")
	case strings.TrimSpace(key.Reveal()) != key.Reveal():
		invalid.add("key", "surrounding_whitespace",
			"La chiave comincia o finisce con uno spazio o un a capo. Copiala senza: "+
				"non la togliamo noi, perché salveremmo qualcosa di diverso da quello che hai incollato.")
	case strings.ContainsFunc(key.Reveal(), isControl):
		invalid.add("key", "control_characters",
			"La chiave contiene caratteri di controllo: probabilmente è stata incollata "+
				"insieme a un pezzo di riga (`ANTHROPIC_API_KEY=...`) invece che da sola.")
	case key.Len() < MinKeyLength:
		invalid.add("key", "too_short", fmt.Sprintf(
			"La chiave è di %d caratteri: le chiavi dei fornitori ammessi sono molto più lunghe, "+
				"quindi questa è quasi certamente incompleta.", key.Len()))
	case key.Len() > MaxKeyLength:
		invalid.add("key", "too_long",
			fmt.Sprintf("La chiave non può superare %d caratteri.", MaxKeyLength))
	}

	label = strings.TrimSpace(label)
	if len(label) > MaxLabelLength {
		invalid.add("label", "too_long",
			fmt.Sprintf("L'etichetta non può superare %d caratteri.", MaxLabelLength))
	}

	return provider, label, invalid.orNil()
}

// isControl riconosce i caratteri che in una testata HTTP non possono comparire.
// Sono anche quelli che un incollaggio sbagliato porta con sé.
func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func providerList() string {
	names := make([]string, 0, len(Providers()))
	for _, provider := range Providers() {
		names = append(names, string(provider))
	}
	return strings.Join(names, ", ")
}
