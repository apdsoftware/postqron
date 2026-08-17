package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// La testata `Paddle-Signature` ha la forma `ts=<unix>;h1=<hex>`.
//
// I due campi non sono decorazione. `h1` dichiara **quale** schema di firma è
// stato usato — è il nome della versione, come `sha256=` lo è per GitHub — e
// accettare una chiave diversa significherebbe lasciar scegliere al chiamante
// con cosa lo verifichiamo. `ts` entra nel calcolo dell'HMAC, ed è ciò che
// impedisce di riusare una firma valida su un corpo vecchio.
const (
	signatureFieldTimestamp = "ts"
	signatureFieldHash      = "h1"
)

// DefaultTolerance è la distanza massima fra il momento dichiarato dalla firma e
// il nostro orologio.
//
// Cinque minuti sono abbondanti per una consegna e stretti per un riuso: Paddle
// **rifirma a ogni tentativo**, quindi una ripetizione arrivata tre ore dopo
// porta un `ts` di tre ore dopo, non quello originale, e non viene rifiutata da
// questo controllo.
//
// Non è però la difesa principale dal riuso, e conviene saperlo prima di
// allargarla: quella è il registro degli eventi (migrazione 0013), che riconosce
// un `event_id` già visto qualunque sia il suo `ts`. Questo controllo copre la
// finestra in cui il registro non ha ancora niente da riconoscere — e copre il
// caso, più concreto, di un orologio nostro andato alla deriva, che qui si vede
// come un rifiuto anziché come una sottoscrizione applicata al momento
// sbagliato.
const DefaultTolerance = 5 * time.Minute

// Sign calcola la firma di un corpo, nella forma in cui Paddle la manda.
//
// È esportata perché è ciò che serve ai test per costruire una consegna
// legittima: senza, ogni test riscriverebbe l'HMAC per conto suo e proverebbe il
// proprio calcolo invece del nostro. In esercizio non la chiama nessuno — noi
// verifichiamo firme, non le produciamo.
func Sign(secret, body []byte, ts time.Time) string {
	unix := strconv.FormatInt(ts.Unix(), 10)
	return signatureFieldTimestamp + "=" + unix + ";" +
		signatureFieldHash + "=" + hex.EncodeToString(digest(secret, unix, body))
}

// digest è l'HMAC-SHA256 di `<ts>:<corpo>`.
//
// Il timestamp sta **dentro** il messaggio firmato, non accanto: se fosse
// soltanto una testata a fianco, chiunque potrebbe riscriverlo e presentare una
// firma vecchia come appena emessa. Firmandolo, cambiarlo invalida l'HMAC.
func digest(secret []byte, unix string, body []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(unix))
	mac.Write([]byte(":"))
	mac.Write(body)
	return mac.Sum(nil)
}

// VerifySignature confronta la firma dichiarata con l'HMAC-SHA256 del corpo.
//
// Restituisce [ErrInvalidSignature] — sempre lo stesso errore — per firma
// assente, campi mancanti, timestamp illeggibile, timestamp fuori tolleranza,
// esadecimale illeggibile, lunghezza diversa e valore diverso. Non c'è nessun
// caso in cui il chiamante debba sapere *quale* dei sette: saperlo aiuterebbe
// soltanto chi sta provando.
//
// Il confronto è [hmac.Equal], cioè a tempo costante. Un `==` fra due stringhe
// esce al primo byte diverso, e il tempo di uscita è una misura di quanti byte
// erano giusti: ripetuta, quella misura ricostruisce la firma un byte alla volta
// senza conoscere il segreto. È un attacco pratico su un endpoint di rete, e la
// difesa costa una chiamata di funzione.
//
// `body` sono i byte esatti ricevuti. Vedi la nota sul corpo grezzo nella
// documentazione del package: qualunque normalizzazione applicata prima di qui
// rende questa verifica una cerimonia.
func VerifySignature(secret, body []byte, signature string, now time.Time, tolerance time.Duration) error {
	if len(secret) == 0 {
		// Senza segreto non esiste una firma valida. Non è un rifiuto della
		// richiesta ma un difetto di configurazione, e va trattato come tale dal
		// costruttore del servizio: qui si limita a non poter accettare niente.
		return ErrInvalidSignature
	}

	unix, hashes := parseSignature(signature)
	if unix == "" || len(hashes) == 0 {
		return ErrInvalidSignature
	}

	seconds, err := strconv.ParseInt(unix, 10, 64)
	if err != nil {
		return ErrInvalidSignature
	}
	if tolerance > 0 {
		// In entrambe le direzioni: un `ts` nel futuro è anomalo quanto uno nel
		// passato, e accettarlo significherebbe accettare una firma che diventerà
		// valida più tardi.
		if delta := now.Sub(time.Unix(seconds, 0)); delta > tolerance || delta < -tolerance {
			return ErrInvalidSignature
		}
	}

	// Il valore atteso si calcola una volta sola, fuori dal ciclo: dipende dal
	// corpo e dal `ts`, non da quale delle firme dichiarate stiamo provando.
	expected := digest(secret, unix, body)
	for _, declared := range hashes {
		// La decodifica esadecimale non è a tempo costante, e va bene: opera sul
		// valore dichiarato dal chiamante, che il chiamante conosce già. Il
		// segreto non entra in questo passaggio.
		provided, err := hex.DecodeString(declared)
		if err != nil {
			continue
		}
		if hmac.Equal(provided, expected) {
			return nil
		}
	}
	return ErrInvalidSignature
}

// parseSignature estrae `ts` e le firme `h1` dalla testata.
//
// Le firme sono più d'una perché la testata può portarne più di una durante la
// rotazione del segreto di notifica: accettarne una qualsiasi non allarga
// niente, perché ciascuna dev'essere comunque un HMAC valido sotto la nostra
// chiave. Restare fermi alla prima renderebbe invece la rotazione una finestra
// di consegne rifiutate.
//
// I campi che non riconosciamo vengono ignorati e non fanno fallire la lettura:
// una versione futura della testata che ne aggiunge uno non deve spegnere il
// webhook. Ciò che non si può ignorare è un `ts` mancante o ripetuto — il
// secondo caso è un tentativo di far firmare un valore e verificarne un altro, e
// si tratta come firma non valida.
func parseSignature(signature string) (string, []string) {
	var unix string
	var hashes []string
	for field := range strings.SplitSeq(signature, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(field), "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(name) {
		case signatureFieldTimestamp:
			if unix != "" {
				return "", nil
			}
			unix = strings.TrimSpace(value)
		case signatureFieldHash:
			if hash := strings.TrimSpace(value); hash != "" {
				hashes = append(hashes, hash)
			}
		}
	}
	return unix, hashes
}
