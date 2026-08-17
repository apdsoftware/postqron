package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	"golang.org/x/text/unicode/norm"
)

// ----------------------------------------------------------------- algoritmo

// Argon2id è l'algoritmo di hashing delle password di Postqron.
//
// # Perché Argon2id
//
// Il requisito è resistere a un attaccante che, ottenuto un dump del database,
// prova password in parallelo su hardware dedicato. Le tre famiglie candidate
// si comportano in modo diverso proprio su quel punto:
//
//   - **bcrypt** lavora su un working set di ~4 KiB. Quattro kibibyte stanno
//     nella memoria on-chip di una GPU o di un FPGA, quindi l'attaccante ne
//     istanzia migliaia di copie in parallelo a costo quasi nullo: il fattore di
//     costo di bcrypt aumenta il tempo *per tentativo*, non il costo *per
//     tentativo parallelo*, che è quello che conta. In più bcrypt tronca
//     silenziosamente l'input a 72 byte, e una troncatura silenziosa su un
//     segreto è un difetto a prescindere.
//   - **scrypt** è memory-hard e sarebbe accettabile, ma è vulnerabile ad
//     attacchi cache-timing sul suo accesso alla memoria dipendente dai dati, e
//     non ha una variante che li mitighi.
//   - **Argon2id** è memory-hard come scrypt — raddoppiare la memoria raddoppia
//     l'area di silicio per ogni tentativo parallelo, che è l'unico costo che un
//     ASIC non aggira — ed è ibrido: la prima passata usa accessi indipendenti
//     dai dati (2i, resistente al cache-timing), le successive accessi
//     dipendenti dai dati (2d, resistente al time-memory trade-off). È il
//     vincitore della Password Hashing Competition e la raccomandazione OWASP
//     corrente.
//
// La scelta è Argon2id.
//
// # Perché questi parametri
//
// I parametri sono la parte che invecchia, non l'algoritmo. Da dove vengono:
//
//   - `m = 19456` KiB (19 MiB), `t = 2`, `p = 1` è il *minimo* raccomandato da
//     OWASP per Argon2id. Non si è scelto un valore più alto (per esempio i
//     64 MiB che si vedono spesso) per un motivo misurabile e non estetico: il
//     costo per l'attaccante cresce con la memoria, ma per il difensore cresce
//     con memoria × login concorrenti, e l'API di Postqron condivide la VPS con
//     PostgreSQL (SPEC §2, e vedi il commento sul pool in internal/database).
//     A 19 MiB, [maxConcurrentHashes] login simultanei costano ~150 MiB di
//     picco; a 64 MiB costerebbero mezzo gigabyte, e la difesa si trasformerebbe
//     in un vettore di esaurimento della memoria.
//   - Il numero di hash concorrenti è limitato esplicitamente
//     ([maxConcurrentHashes]) invece di essere lasciato al numero di richieste
//     in arrivo. È la contromisura al problema di cui sopra: senza, mille login
//     simultanei sono mille allocazioni da 19 MiB.
//   - `p = 1` perché il parallelismo interno non aumenta il costo per
//     l'attaccante (che parallelizza fra tentativi, non dentro un tentativo) e
//     su una macchina piccola sottrae solo core al resto del servizio.
//   - Salt di 16 byte e chiave di 32 byte sono le lunghezze raccomandate: 128
//     bit di salt rendono inutile qualunque tabella precalcolata, 256 bit di
//     output sono oltre il necessario e non costano nulla.
//
// # Costo misurato e come rivederlo
//
// `BenchmarkHash` misura il costo effettivo. Con questi parametri:
//
//	BenchmarkHash-12    20794825 ns/op    // ~21 ms, Apple M3 Pro, 2026-08-17
//
// Il riferimento comune è che un hash costi almeno un centinaio di millisecondi
// sull'hardware di produzione; 21 ms su un portatile recente sono verosimilmente
// tre o quattro volte tanto sulla VPS, quindi nell'ordine giusto. **Il numero da
// rimisurare è quello sulla VPS, non questo**, e non è ancora disponibile
// (CREDENTIALS.md §3: l'infrastruttura non è provisionata).
//
// Se il costo risulterà troppo basso, la leva da alzare è **`Time`, non
// `Memory`**. È il punto che si sbaglia più spesso: raddoppiare `Memory`
// raddoppia sia il costo per l'attaccante sia il picco di memoria del difensore
// — che è la risorsa scarsa qui, per il motivo scritto sopra — mentre `Time`
// aumenta il lavoro per tentativo a memoria costante. `Memory` si alza solo se
// la VPS cresce e il vincolo di memoria cade.
//
// I valori vanno riesaminati **entro il 2028**. L'aggiornamento non richiede
// migrazioni: ogni hash porta con sé i parametri con cui è stato prodotto, e
// [Hasher.Verify] segnala quelli obsoleti perché [Service] li rigeneri al login
// successivo, che è l'unico momento in cui la password in chiaro esiste.
type Argon2idParams struct {
	// Memory è la memoria di lavoro in KiB.
	Memory uint32
	// Time è il numero di passate sulla memoria.
	Time uint32
	// Parallelism è il numero di lane interne.
	Parallelism uint8
	// SaltLength è la lunghezza del salt in byte.
	SaltLength uint32
	// KeyLength è la lunghezza dell'hash in byte.
	KeyLength uint32
}

// DefaultParams sono i parametri correnti. Le motivazioni stanno su
// [Argon2idParams].
var DefaultParams = Argon2idParams{
	Memory:      19 * 1024,
	Time:        2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// maxConcurrentHashes limita gli hash Argon2id calcolati insieme.
//
// Ogni hash alloca `Memory` KiB per tutta la sua durata: senza un tetto, un
// picco di login (o un attacco che li provoca) si traduce direttamente in
// pressione sulla memoria della VPS. Le richieste in eccesso aspettano il loro
// turno — l'attesa allunga la risposta di tutti allo stesso modo, quindi non
// apre la differenza di tempo che [Service] lavora per chiudere.
const maxConcurrentHashes = 8

// maxPasswordLength è il limite di lunghezza della password, in byte.
//
// Argon2 non ha il troncamento di bcrypt, quindi il limite non serve alla
// correttezza: serve a evitare che una richiesta con un megabyte di «password»
// faccia lavorare la funzione di hash su un input arbitrariamente grande.
const maxPasswordLength = 128

// MinPasswordLength è la lunghezza minima ammessa, in caratteri.
//
// Dodici e non otto: NIST SP 800-63B ha eliminato le regole di composizione
// (una maiuscola, un numero, un simbolo) perché producono password prevedibili,
// e ha spostato la difesa sulla lunghezza. Senza regole di composizione, otto
// caratteri sono pochi.
const MinPasswordLength = 12

// -------------------------------------------------------------------- hasher

// Hasher calcola e verifica gli hash delle password.
type Hasher struct {
	params Argon2idParams
	// slots è il semaforo che limita gli hash concorrenti.
	slots chan struct{}
	// decoy è un hash prodotto con `params` su una password casuale, contro cui
	// si verifica quando l'utente non esiste. Vedi [Hasher.VerifyDecoy].
	decoy string
}

var _ PasswordHasher = (*Hasher)(nil)

// NewHasher costruisce un Hasher con i parametri indicati.
func NewHasher(params Argon2idParams) (*Hasher, error) {
	if params.Memory < 8*1024 {
		return nil, fmt.Errorf("argon2id: Memory = %d KiB, minimo 8192", params.Memory)
	}
	if params.Time < 1 {
		return nil, errors.New("argon2id: Time deve essere almeno 1")
	}
	if params.Parallelism < 1 {
		return nil, errors.New("argon2id: Parallelism deve essere almeno 1")
	}
	if params.SaltLength < 16 {
		return nil, fmt.Errorf("argon2id: SaltLength = %d, minimo 16", params.SaltLength)
	}
	if params.KeyLength < 32 {
		return nil, fmt.Errorf("argon2id: KeyLength = %d, minimo 32", params.KeyLength)
	}

	h := &Hasher{
		params: params,
		slots:  make(chan struct{}, maxConcurrentHashes),
	}

	// L'hash civetta si calcola una volta all'avvio, non a ogni login mancato:
	// altrimenti il percorso «utente inesistente» pagherebbe un hash in più di
	// quello «password sbagliata», che è esattamente la differenza da evitare.
	// La password è casuale e non viene conservata: nessuno deve poterla
	// indovinare, perché nessun account la usa.
	filler := make([]byte, 32)
	if _, err := rand.Read(filler); err != nil {
		return nil, fmt.Errorf("argon2id: sorgente casuale non disponibile: %w", err)
	}
	decoy, err := h.hash(base64.RawStdEncoding.EncodeToString(filler))
	if err != nil {
		return nil, err
	}
	h.decoy = decoy
	return h, nil
}

// Params restituisce i parametri con cui il Hasher produce hash nuovi.
func (h *Hasher) Params() Argon2idParams { return h.params }

// Hash produce l'hash di una password nel formato PHC.
//
// Il contesto serve solo all'attesa di uno slot: Argon2id, una volta partito,
// non è interrompibile.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	return h.hash(password)
}

// hash calcola l'hash senza passare dal semaforo. Usata da [NewHasher], che
// gira prima che il semaforo abbia contendenti.
func (h *Hasher) hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id: sorgente casuale non disponibile: %w", err)
	}

	key := argon2.IDKey([]byte(NormalizePassword(password)), salt,
		h.params.Time, h.params.Memory, h.params.Parallelism, h.params.KeyLength)

	// Formato PHC: autodescrittivo, standard, e con i parametri dentro. È ciò
	// che rende possibile alzare il costo in futuro senza migrare la tabella —
	// un hash vecchio resta verificabile con i suoi parametri.
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.Memory, h.params.Time, h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify confronta una password con un hash memorizzato.
//
// `ok` dice se la password è quella giusta; `stale` dice se l'hash è stato
// prodotto con parametri diversi da quelli correnti e va rigenerato — cosa che
// si può fare solo qui, perché è l'unico momento in cui la password in chiaro
// esiste.
//
// Un hash illeggibile non è un errore da propagare al chiamante come «errore
// del server»: restituisce `ok = false` **dopo** aver comunque pagato il costo
// di un Argon2id, perché anche quel caso deve durare come tutti gli altri.
func (h *Hasher) Verify(ctx context.Context, encoded, password string) (ok bool, stale bool, err error) {
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()

	params, salt, want, decodeErr := decodePHC(encoded)
	if decodeErr != nil {
		// Costo equivalente su un hash che non c'è: senza questo, una riga con
		// `password_hash` corrotto o vuoto risponderebbe più in fretta di una
		// integra.
		h.compareDecoy(password)
		return false, false, nil
	}

	got := argon2.IDKey([]byte(NormalizePassword(password)), salt,
		params.Time, params.Memory, params.Parallelism, uint32(len(want)))

	ok = subtle.ConstantTimeCompare(got, want) == 1
	return ok, ok && params != h.params, nil
}

// VerifyDecoy paga il costo di una verifica senza avere un hash da verificare.
//
// È la contromisura all'enumerazione per via temporale: un login con email
// inesistente non ha nulla da confrontare, e se salta il confronto risponde
// misurabilmente più in fretta di uno con email esistente e password sbagliata.
// Il rimedio non è un `time.Sleep` — che è un valore inventato, e sbagliato non
// appena l'hardware cambia — ma fare *lo stesso lavoro*: un Argon2id con gli
// stessi parametri, sullo stesso semaforo, su un hash civetta calcolato
// all'avvio.
func (h *Hasher) VerifyDecoy(ctx context.Context, password string) error {
	if err := h.acquire(ctx); err != nil {
		return err
	}
	defer h.release()
	h.compareDecoy(password)
	return nil
}

func (h *Hasher) compareDecoy(password string) {
	params, salt, want, err := decodePHC(h.decoy)
	if err != nil {
		// L'hash civetta lo produce NewHasher: se non si rilegge è un bug del
		// package, non una condizione d'esercizio. Resta il costo dell'Argon2id,
		// che è la ragione per cui questa funzione esiste.
		salt, want = make([]byte, h.params.SaltLength), make([]byte, h.params.KeyLength)
		params = h.params
	}
	got := argon2.IDKey([]byte(NormalizePassword(password)), salt,
		params.Time, params.Memory, params.Parallelism, uint32(len(want)))
	// Il confronto non serve a nessuno, ma senza di esso il compilatore
	// potrebbe eliminare il calcolo.
	if subtle.ConstantTimeCompare(got, want) == 1 {
		_ = got
	}
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() { <-h.slots }

// -------------------------------------------------------------------- policy

// ErrPasswordTooShort e le sue sorelle sono i rifiuti della policy sulle
// password. Sono errori distinti perché il messaggio all'utente cambia; nessuno
// di loro dipende dall'esistenza dell'account.
var (
	ErrPasswordTooShort = fmt.Errorf("la password deve avere almeno %d caratteri", MinPasswordLength)
	ErrPasswordTooLong  = fmt.Errorf("la password non può superare %d byte", maxPasswordLength)
	ErrPasswordBlank    = errors.New("la password non può essere composta di soli spazi")
)

// NormalizePassword porta la password in forma normale NFKC.
//
// Serve al caso concreto in cui la stessa password viene digitata da una
// tastiera diversa: alcune combinazioni Unicode (una lettera accentata composta
// da due code point invece che da uno) sono lo stesso carattere per l'utente ma
// byte diversi per Argon2, e senza normalizzazione il login fallirebbe dal
// telefono e funzionerebbe dal portatile. La normalizzazione è applicata sia in
// scrittura sia in verifica, quindi le due strade coincidono sempre.
func NormalizePassword(password string) string { return norm.NFKC.String(password) }

// ValidatePassword applica la policy sulle password.
//
// Non ci sono regole di composizione, per la ragione spiegata su
// [MinPasswordLength]. Non c'è nemmeno un controllo contro le password violate
// (Pwned Passwords): richiederebbe una chiamata di rete dentro il percorso di
// registrazione, cioè una dipendenza esterna su un'operazione che deve
// funzionare comunque.
func ValidatePassword(password string) error {
	normalized := NormalizePassword(password)
	if len(normalized) > maxPasswordLength {
		return ErrPasswordTooLong
	}
	if utf8.RuneCountInString(normalized) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if strings.TrimSpace(normalized) == "" {
		return ErrPasswordBlank
	}
	return nil
}

// ---------------------------------------------------------------- decodifica

// decodePHC legge un hash in formato PHC.
func decodePHC(encoded string) (params Argon2idParams, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	// "" | argon2id | v=19 | m=...,t=...,p=... | salt | key
	if len(parts) != 6 || parts[0] != "" {
		return params, nil, nil, errors.New("hash non nel formato PHC")
	}
	if parts[1] != "argon2id" {
		return params, nil, nil, fmt.Errorf("algoritmo non supportato: %q", parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return params, nil, nil, errors.New("versione di Argon2 illeggibile")
	}
	if version != argon2.Version {
		return params, nil, nil, fmt.Errorf("versione di Argon2 non supportata: %d", version)
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&params.Memory, &params.Time, &params.Parallelism); err != nil {
		return params, nil, nil, errors.New("parametri di Argon2 illeggibili")
	}

	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return params, nil, nil, errors.New("salt illeggibile")
	}
	if key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil {
		return params, nil, nil, errors.New("hash illeggibile")
	}
	if len(salt) == 0 || len(key) == 0 {
		return params, nil, nil, errors.New("salt o hash vuoto")
	}

	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(key))
	return params, salt, key, nil
}
