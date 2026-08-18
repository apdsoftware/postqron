package secretbox

import (
	"fmt"
	"log/slog"
)

// Plaintext è un valore segreto in chiaro: ciò che entra in [Keyring.Seal] e
// ciò che esce da [Keyring.Open].
//
// È una stringa avvolta in un tipo che **non si stampa**. Il punto non è
// l'incapsulamento: è che `slog.String("secret", value)`, `fmt.Sprintf("%s",
// value)` e `log.Printf("%v", input)` sono tre righe che qualcuno scrive prima o
// poi, e con una `string` nuda funzionerebbero tutte e tre — cioè scriverebbero
// il segreto in un log che la privacy policy §2.2 dice conservato e visibile
// all'utente.
//
// Con questo tipo scrivono `«segreto»`. Per ottenere il valore vero bisogna
// chiamare [Plaintext.Reveal], che si legge in una revisione e si cerca con un
// grep.
//
// # Perché sta qui e non in chi lo usa
//
// Perché gli utenti sono due e diventeranno di più: i segreti del workspace
// (internal/secrets, R42) e le chiavi AI degli utenti (internal/aicreds, R18).
// Le due funzionalità hanno cicli di vita diversi — chi le legge, quando e
// perché non coincide — ma «una stringa che non si può loggare» è la stessa cosa
// per entrambe, e una seconda copia di questo tipo sarebbe una copia che un
// giorno dimentica [Plaintext.Format] e se ne accorge dal contenuto di un log.
//
// Sta in questo package e non in uno suo perché è il package che i due
// importano già entrambi, ed è quello a cui il valore in chiaro serve: è
// l'argomento di `Seal` e il risultato di `Open`.
type Plaintext string

// Redacted è ciò che compare al posto del valore ovunque lo si stampi.
const Redacted = "«segreto»"

// Reveal restituisce il valore in chiaro.
//
// **È l'unico modo di ottenerlo**, e il nome è scelto per essere impossibile da
// scrivere per distrazione: se compare in una riga che costruisce un log o una
// risposta HTTP, quella riga è un difetto e si vede.
func (p Plaintext) Reveal() string { return string(p) }

// Len è la lunghezza del valore. Serve alla validazione senza rivelare niente.
func (p Plaintext) Len() int { return len(p) }

// Empty indica un valore assente.
func (p Plaintext) Empty() bool { return p == "" }

// String implementa [fmt.Stringer] mascherando il valore.
func (p Plaintext) String() string { return Redacted }

// Format implementa [fmt.Formatter].
//
// [fmt.Stringer] da solo non basta: `%q` citerebbe la stringa sottostante e
// `%#v` ne stamperebbe la forma Go, che è il valore fra virgolette con il nome
// del tipo davanti. Implementando Formatter la maschera vale per **ogni** verbo,
// compresi quelli che qualcuno userà fra un anno.
func (p Plaintext) Format(f fmt.State, verb rune) {
	switch verb {
	case 'q':
		fmt.Fprintf(f, "%q", Redacted)
	default:
		fmt.Fprint(f, Redacted)
	}
}

// LogValue implementa [slog.LogValuer]: nei log strutturati compare la maschera.
func (p Plaintext) LogValue() slog.Value { return slog.StringValue(Redacted) }

// MarshalJSON impedisce che il valore finisca in una risposta o in un file di
// stato serializzando la struttura che lo contiene.
//
// Non è la difesa principale — la difesa principale è che i tipi restituiti
// dall'API il campo non ce l'hanno — ma è quella che copre i tipi che un giorno
// lo conterranno per forza, come il corpo di una richiesta di creazione.
func (p Plaintext) MarshalJSON() ([]byte, error) { return []byte(`"` + Redacted + `"`), nil }

// SealPlaintext cifra un [Plaintext] con la chiave attiva.
//
// È [Keyring.Seal] con l'argomento giusto: senza, ogni chiamante scriverebbe
// `k.Seal([]byte(value.Reveal()), ...)`, e `Reveal` in mezzo a una riga di
// cifratura è indistinguibile a colpo d'occhio da `Reveal` in mezzo a una riga
// di log. Con questo metodo `Reveal` resta una parola che compare solo dove non
// dovrebbe.
func (k Keyring) SealPlaintext(value Plaintext, binding []byte) (Box, error) {
	return k.Seal([]byte(value), binding)
}

// OpenPlaintext decifra un Box restituendo il valore già avvolto nel tipo che
// non si stampa. Vedi [Keyring.Open] per quando fallisce.
func (k Keyring) OpenPlaintext(b Box, binding []byte) (Plaintext, error) {
	value, err := k.Open(b, binding)
	if err != nil {
		return "", err
	}
	return Plaintext(value), nil
}
