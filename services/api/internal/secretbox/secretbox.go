// Package secretbox è la cifratura a riposo dei segreti di Postqron (R42).
//
// Espone un mazzo di chiavi ([Keyring]) e due operazioni, [Keyring.Seal] e
// [Keyring.Open]. Non sa che cosa sta cifrando: i segreti del workspace
// (internal/secrets) sono il primo utente, le chiavi AI di R18 saranno il
// secondo, e il formato è lo stesso perché la 0007 e la 0011 hanno le stesse tre
// colonne — `ciphertext`, `nonce`, `key_version`.
//
// # AES-256-GCM
//
// Cifrario autenticato: il testo cifrato non è solo illeggibile, è anche
// **rilevabile se modificato**. Serve perché il testo in chiaro finisce in una
// testata `Authorization` di una richiesta uscente: un byte cambiato da chi ha
// accesso in scrittura al database, senza autenticazione, diventerebbe un valore
// diverso spedito a un bersaglio esterno senza che nessuno se ne accorga.
//
// Il nonce è di 12 byte da CSPRNG e cambia a ogni scrittura. Con GCM il riuso di
// un nonce sotto la stessa chiave è il modo classico di perdere tutto, e per
// questo non esiste, in questo package, nessuna funzione che accetti un nonce
// dal chiamante.
//
// # Il dato associato
//
// [Seal] e [Open] prendono un `binding`, che entra nell'autenticazione ma non
// nel testo cifrato (AAD, in gergo). Il chiamante ci mette ciò che identifica la
// riga — proprietario e nome del segreto — e l'effetto è che un testo cifrato
// spostato su un'altra riga **non si apre**: copiare la colonna del segreto di
// un altro utente dentro la propria riga non lo rende leggibile, e nemmeno
// utilizzabile in esecuzione.
//
// # Rotazione
//
// `ENCRYPTION_KEY` non è rigenerabile (docs/CREDENTIALS.md §6): perderla rende
// illeggibile tutto ciò che è stato cifrato con lei. Questo package è scritto
// sapendolo, e la rotazione è prevista dal primo giorno anche se il comando che
// la esegue non esiste ancora:
//
//   - la variabile accetta **più chiavi**, nella forma `versione:chiave`
//     separata da virgole. La prima è quella attiva, con cui si cifra; le altre
//     restano per aprire ciò che era già scritto;
//   - ogni testo cifrato porta con sé la versione della chiave che l'ha prodotto
//     ([Box.KeyVersion], colonna `key_version`), quindi righe cifrate con chiavi
//     diverse convivono senza ambiguità;
//   - [Keyring.NeedsRotation] dice se una riga è ferma a una chiave vecchia. È
//     ciò su cui si scriverà il comando di rotazione: leggere, aprire con la
//     vecchia, risigillare con l'attiva.
//
// Ciò che manca — e va detto, perché non lo si scopra al momento del bisogno —
// è il comando che scorre le tabelle e risigilla. Non c'è nulla in questo
// package che lo impedisca; è lavoro, non un cambio di forma dei dati.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// EnvVar è la variabile che porta le chiavi di cifratura.
//
// La lettura non passa da internal/config per la stessa ragione di
// SESSION_SECRET in internal/auth: quel package appartiene a un'altra issue, e a
// regime il posto giusto è lì accanto alle POSTGRES_*.
const EnvVar = "ENCRYPTION_KEY"

// keyLength è la lunghezza di una chiave AES-256, in byte.
const keyLength = 32

// nonceLength è la lunghezza del nonce di GCM, in byte. È la dimensione
// standard, l'unica per cui GCM non richiede un passaggio di derivazione in più.
const nonceLength = 12

// MaxVersion è la versione più alta rappresentabile.
//
// La colonna `key_version` è uno `smallint`: un numero più grande passerebbe la
// validazione qui e verrebbe rifiutato dal database alla scrittura, cioè
// diventerebbe un 500 al posto di un errore d'avvio.
const MaxVersion = 32767

var (
	// ErrUnknownVersion segnala che la riga è stata cifrata con una chiave che
	// il processo non ha. È l'errore che si vede se si rimuove dalla variabile
	// una chiave con cui esistono ancora righe: la riga non è persa, manca la
	// chiave.
	ErrUnknownVersion = errors.New("secretbox: chiave di cifratura non disponibile per questa versione")

	// ErrNotAuthenticated segnala che il testo cifrato non è integro, oppure che
	// è stato aperto con un binding diverso da quello con cui era stato chiuso.
	// I due casi non si distinguono e non devono: GCM non lo dice, e la risposta
	// è la stessa.
	ErrNotAuthenticated = errors.New("secretbox: testo cifrato non integro o non di questa riga")
)

// Box è un valore cifrato, nella forma in cui sta nelle colonne della 0011.
//
// Non contiene il testo in chiaro e non ha nessun metodo che lo restituisca:
// aprirlo richiede il [Keyring], che sta nel processo e non nella struttura.
type Box struct {
	Ciphertext []byte
	Nonce      []byte
	// KeyVersion è la versione della chiave che ha prodotto il testo cifrato.
	KeyVersion uint16
}

// Empty indica un Box senza contenuto: è la forma che assume una riga revocata,
// dove il vincolo `workspace_secrets_revoked_is_empty_check` pretende le due
// colonne vuote.
func (b Box) Empty() bool { return len(b.Ciphertext) == 0 && len(b.Nonce) == 0 }

// ------------------------------------------------------------------ keyring

// Keyring è il mazzo di chiavi di cifratura del processo.
//
// Il valore zero non è utilizzabile e non cifra niente: [Keyring.Valid] lo
// riconosce, ed è ciò che permette a chi lo riceve di rifiutarlo all'avvio
// invece di scoprirlo alla prima scrittura.
type Keyring struct {
	keys   map[uint16][]byte
	active uint16
}

// NewKeyring costruisce il mazzo a partire dal contenuto della variabile.
//
// Sono ammesse due forme:
//
//	<chiave>                         una chiave sola, versione 1
//	<versione>:<chiave>,<versione>:<chiave>,...   più chiave, la prima è attiva
//
// La prima forma è quella di `.env.example` e resta valida per sempre: chi non
// ha mai ruotato non deve riscrivere niente. La seconda è la forma che una
// rotazione produce, ed è deliberatamente la stessa variabile — un secondo nome
// (`ENCRYPTION_KEY_OLD`) sarebbe una variabile che qualcuno dimentica di
// rimuovere, e una chiave dismessa che resta in giro è una chiave viva.
//
// Le chiavi sono in base64 (standard o url, con o senza padding), come le
// produce `openssl rand -base64 32`.
func NewKeyring(raw string) (Keyring, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Keyring{}, fmt.Errorf("%s è vuota: generane una con `openssl rand -base64 32`", EnvVar)
	}

	keys := make(map[uint16][]byte)
	var active uint16
	for index, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return Keyring{}, fmt.Errorf("%s: voce vuota in posizione %d", EnvVar, index+1)
		}

		version, encoded, err := splitVersion(item, index)
		if err != nil {
			return Keyring{}, err
		}
		key, err := decodeKey(encoded, version)
		if err != nil {
			return Keyring{}, err
		}
		if _, duplicate := keys[version]; duplicate {
			return Keyring{}, fmt.Errorf("%s: versione %d dichiarata due volte", EnvVar, version)
		}
		keys[version] = key
		if index == 0 {
			active = version
		}
	}
	return Keyring{keys: keys, active: active}, nil
}

// KeyringFromEnv legge [EnvVar] e ne costruisce il mazzo.
//
// Non esiste un valore di default, e non può esistere: una chiave generata
// all'avvio renderebbe illeggibile a ogni riavvio tutto ciò che il riavvio
// precedente aveva scritto, e una chiave costante nel codice sarebbe un segreto
// nel codice (SPEC §5).
func KeyringFromEnv(getenv func(string) string) (Keyring, error) {
	value := strings.TrimSpace(getenv(EnvVar))
	if value == "" {
		return Keyring{}, fmt.Errorf("%s non è impostata: vedi .env.example", EnvVar)
	}
	return NewKeyring(value)
}

// Valid indica se il mazzo è utilizzabile.
func (k Keyring) Valid() bool { return len(k.keys) > 0 && k.keys[k.active] != nil }

// ActiveVersion è la versione con cui [Keyring.Seal] cifra.
func (k Keyring) ActiveVersion() uint16 { return k.active }

// Versions elenca le versioni disponibili, in ordine crescente. Serve alla
// diagnostica d'avvio: «apro le versioni 1 e 2, cifro con la 2» è una riga di
// log che rende visibile una rotazione a metà.
func (k Keyring) Versions() []uint16 { return slices.Sorted(maps.Keys(k.keys)) }

// NeedsRotation indica se il Box è cifrato con una chiave che non è più quella
// attiva, cioè se una rotazione lo lascerebbe indietro.
//
// Un Box vuoto — una riga revocata — non ha niente da risigillare.
func (k Keyring) NeedsRotation(b Box) bool { return !b.Empty() && b.KeyVersion != k.active }

// Seal cifra un valore con la chiave attiva.
//
// `binding` entra nell'autenticazione e non nel testo cifrato: vedi la
// documentazione del package per che cosa comporta, e internal/secrets per che
// cosa ci mette dentro.
func (k Keyring) Seal(plaintext []byte, binding []byte) (Box, error) {
	if !k.Valid() {
		return Box{}, errors.New("secretbox: keyring non inizializzato")
	}
	gcm, err := k.aead(k.active)
	if err != nil {
		return Box{}, err
	}

	nonce := make([]byte, nonceLength)
	if _, err := rand.Read(nonce); err != nil {
		return Box{}, fmt.Errorf("secretbox: generazione del nonce: %w", err)
	}

	// Il primo argomento è nil e non `nonce`: `Seal` accoderebbe il testo cifrato
	// al buffer che riceve, e le due colonne della 0011 sono separate proprio
	// perché il nonce vada conservato per conto suo.
	return Box{
		Ciphertext: gcm.Seal(nil, nonce, plaintext, binding),
		Nonce:      nonce,
		KeyVersion: k.active,
	}, nil
}

// Open decifra un Box.
//
// Il `binding` dev'essere identico a quello passato a [Keyring.Seal]: se non lo
// è, l'apertura fallisce con [ErrNotAuthenticated] esattamente come se il testo
// cifrato fosse stato manomesso.
func (k Keyring) Open(b Box, binding []byte) ([]byte, error) {
	if !k.Valid() {
		return nil, errors.New("secretbox: keyring non inizializzato")
	}
	if b.Empty() {
		// Una riga revocata. Chiamare Open su di lei è un difetto del chiamante —
		// il valore non c'è più per costruzione — e va detto, non inghiottito
		// restituendo una stringa vuota che poi finisce in una testata.
		return nil, errors.New("secretbox: valore cifrato assente (riga revocata?)")
	}
	if len(b.Nonce) != nonceLength {
		return nil, fmt.Errorf("secretbox: nonce di %d byte, attesi %d", len(b.Nonce), nonceLength)
	}

	gcm, err := k.aead(b.KeyVersion)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, b.Nonce, b.Ciphertext, binding)
	if err != nil {
		// L'errore di GCM non si propaga così com'è: non dice niente di utile e
		// invita a distinguere casi che non sono distinguibili.
		return nil, ErrNotAuthenticated
	}
	return plaintext, nil
}

// aead costruisce il cifrario per una versione di chiave.
func (k Keyring) aead(version uint16) (cipher.AEAD, error) {
	key, ok := k.keys[version]
	if !ok {
		return nil, fmt.Errorf("%w: versione %d (disponibili: %v)", ErrUnknownVersion, version, k.Versions())
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: cifrario AES: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: modalità GCM: %w", err)
	}
	return gcm, nil
}

// ------------------------------------------------------------------- interni

// splitVersion separa `versione:chiave`. Senza il prefisso la voce è ammessa
// solo se è l'unica, e vale versione 1: è la forma di `.env.example`.
func splitVersion(item string, index int) (uint16, string, error) {
	prefix, rest, found := strings.Cut(item, ":")
	if !found {
		if index != 0 {
			return 0, "", fmt.Errorf(
				"%s: la voce %d non dichiara la versione; con più chiavi la forma è `versione:chiave`",
				EnvVar, index+1)
		}
		return 1, item, nil
	}

	version, err := strconv.ParseUint(strings.TrimSpace(prefix), 10, 16)
	if err != nil || version == 0 || version > MaxVersion {
		return 0, "", fmt.Errorf(
			"%s: versione non valida %q; attesa un intero fra 1 e %d",
			EnvVar, prefix, MaxVersion)
	}
	return uint16(version), strings.TrimSpace(rest), nil
}

// decodeKey legge una chiave in base64 e ne verifica la lunghezza.
func decodeKey(encoded string, version uint16) ([]byte, error) {
	key, err := decodeBase64(encoded)
	if err != nil {
		return nil, fmt.Errorf(
			"%s: la chiave di versione %d non è base64 valido; generane una con `openssl rand -base64 32`",
			EnvVar, version)
	}
	if len(key) != keyLength {
		return nil, fmt.Errorf(
			"%s: la chiave di versione %d è di %d byte, attesi %d (`openssl rand -base64 32`)",
			EnvVar, version, len(key), keyLength)
	}
	return key, nil
}

// decodeBase64 accetta le quattro varianti dell'alfabeto base64.
//
// Non è pignoleria: `openssl rand -base64 32` produce padding e alfabeto
// standard, ma una chiave copiata da un gestore di segreti arriva spesso in
// forma url-safe, e rifiutarla costringerebbe a convertirla a mano — cioè a
// farla passare per la cronologia di una shell.
func decodeBase64(s string) ([]byte, error) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		if key, err := encoding.DecodeString(s); err == nil {
			return key, nil
		}
	}
	return nil, errors.New("base64 non valido")
}
