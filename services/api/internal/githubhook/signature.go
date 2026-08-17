package githubhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// signaturePrefix è la forma in cui GitHub dichiara l'algoritmo.
//
// Non è decorazione: la testata `X-Hub-Signature` (senza `-256`) esiste ancora e
// porta un HMAC-SHA1. Accettare un valore senza prefisso, o con un prefisso
// diverso, significherebbe lasciare che sia il chiamante a scegliere
// l'algoritmo con cui lo verifichiamo.
const signaturePrefix = "sha256="

// Sign calcola la firma di un corpo, nella forma che GitHub manda.
//
// È esportata perché è ciò che serve ai test per costruire una richiesta
// legittima: senza, ogni test riscriverebbe l'HMAC per conto suo e proverebbe
// il proprio calcolo invece del nostro. In esercizio non la chiama nessuno —
// noi verifichiamo firme, non le produciamo.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature confronta la firma dichiarata con l'HMAC-SHA256 del corpo.
//
// Restituisce [ErrInvalidSignature] — sempre lo stesso errore — per firma
// assente, prefisso sbagliato, esadecimale illeggibile, lunghezza diversa e
// valore diverso. Non c'è nessun caso in cui il chiamante debba sapere *quale*
// dei cinque: saperlo aiuterebbe soltanto chi sta provando.
//
// Il confronto è [hmac.Equal], cioè a tempo costante. Un `==` fra due stringhe
// esce al primo byte diverso, e il tempo di uscita è una misura di quanti byte
// erano giusti: ripetuta, quella misura ricostruisce la firma un byte alla
// volta senza conoscere il segreto. È un attacco pratico su un endpoint di
// rete, e la difesa costa una chiamata di funzione.
//
// `body` sono i byte esatti ricevuti. Vedi la nota sul corpo grezzo nella
// documentazione del package: qualunque normalizzazione applicata prima di qui
// rende questa verifica una cerimonia.
func VerifySignature(secret, body []byte, signature string) error {
	if len(secret) == 0 {
		// Senza segreto non esiste una firma valida. Non è un rifiuto della
		// richiesta ma un difetto di configurazione, e va trattato come tale dal
		// costruttore del servizio: qui si limita a non poter accettare niente.
		return ErrInvalidSignature
	}

	declared, ok := strings.CutPrefix(signature, signaturePrefix)
	if !ok {
		return ErrInvalidSignature
	}

	// La decodifica esadecimale non è a tempo costante, e va bene: opera sul
	// valore dichiarato dal chiamante, che il chiamante conosce già. Il segreto
	// non entra in questo passaggio.
	provided, err := hex.DecodeString(declared)
	if err != nil {
		return ErrInvalidSignature
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return ErrInvalidSignature
	}
	return nil
}
