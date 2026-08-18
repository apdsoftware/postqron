package marketing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

// UnsubscribeDomain è il dominio HKDF della chiave di firma.
//
// Distinto da quelli di internal/auth — sessioni, token monouso, chiavi API — e
// la distinzione è la proprietà che conta: a parità di `SESSION_SECRET`, la
// firma di una disiscrizione **non è** un token di sessione, non è un token di
// recupero password e non è una chiave API. Un difetto che confondesse le
// tabelle non produrrebbe comunque una credenziale valida per un altro uso.
const UnsubscribeDomain = "postqron/v1/marketing-unsubscribe"

// minSecretLength è la lunghezza minima del segreto, la stessa di
// internal/auth: sotto i 32 byte non c'è più entropia della chiave che deve
// produrre.
const minSecretLength = 32

// Signer firma e verifica i link di disiscrizione di §2.8.
//
// # Perché una firma e non una riga nel database
//
// Il link deve funzionare «with one click and without signing in», e deve
// funzionare **da qualunque email di marketing mai spedita**: chi si disiscrive
// lo fa dal messaggio che ha sottomano, che può essere di mesi fa. Un token
// conservato nel database dovrebbe quindi esistere in chiaro — altrimenti non si
// potrebbe riscriverlo nell'email al prossimo invio — e sarebbe l'unica
// credenziale del sistema conservata così, contro la regola che vale per
// sessioni, token monouso e chiavi API.
//
// Con una firma non c'è niente a riposo: l'indirizzo si ricompone a ogni invio
// dallo stesso identificativo e dalla stessa chiave, e la verifica è un
// confronto, non una lettura.
//
// # Che cosa autorizza, e che cosa no
//
// **Solo la disiscrizione, e per un solo utente.** Non identifica, non
// autentica, non apre una sessione: chi ha il link può togliersi le email di
// marketing e nient'altro. È il livello di potere che §2.8 richiede — meno
// non basterebbe a mantenere la promessa, di più la trasformerebbe in un
// problema.
//
// Chiunque abbia il link può usarlo, ed è **inevitabile e dichiarato**: un link
// che funziona senza accedere è, per costruzione, un indirizzo che chiunque lo
// possieda può visitare. La difesa non è impedirlo, che significherebbe chiedere
// di accedere; è che il danno sia limitato a una cosa sola, reversibile
// dall'utente dalle impostazioni, e che non tocchi le email transazionali.
//
// # Che cosa succede se `SESSION_SECRET` ruota
//
// I link già spediti smettono di verificarsi. È il rovescio dichiarato di non
// conservare niente, ed è la stessa proprietà per cui una rotazione invalida
// tutte le sessioni: si rinuncia alla durata per avere la revoca in un colpo.
//
// La conseguenza va misurata per quello che è: chi apre un link vecchio dopo una
// rotazione trova la pagina che glielo dice e due strade che funzionano — le
// impostazioni dell'account e l'indirizzo di supporto — e le comunicazioni
// successive portano un link nuovo, valido. Non si perde il diritto, si perde
// una scorciatoia, per il tempo di un invio.
type Signer struct {
	key []byte
}

// NewSigner deriva la chiave di firma dal segreto.
//
// La derivazione è la stessa di [auth.NewKeyring] — HKDF-SHA256 senza salt, con
// il dominio a separare gli usi — ed è ripetuta qui invece di essere presa da
// lì per non far dipendere questo package dall'autenticazione: il consenso al
// marketing non ha niente a che vedere con chi sei, e l'unico legame sarebbe
// stato il segreto.
func NewSigner(secret string) (Signer, error) {
	if len(secret) < minSecretLength {
		return Signer{}, fmt.Errorf(
			"marketing: il segreto deve essere lungo almeno %d byte (attuali: %d); "+
				"è lo stesso SESSION_SECRET del resto del servizio",
			minSecretLength, len(secret))
	}
	key := make([]byte, sha256.Size)
	if _, err := io.ReadFull(hkdf.New(sha256.New, []byte(secret), nil, []byte(UnsubscribeDomain)), key); err != nil {
		return Signer{}, fmt.Errorf("marketing: derivazione della chiave di firma: %w", err)
	}
	return Signer{key: key}, nil
}

// Valid indica se il firmatario è utilizzabile.
func (s Signer) Valid() bool { return len(s.key) > 0 }

// Token compone il valore che finisce nel link di disiscrizione.
//
// La forma è `<utente>.<firma>`: l'identificativo in chiaro e il suo HMAC. In
// chiaro perché la verifica non deve costare una scansione della tabella degli
// utenti — si ricalcola la firma sull'identificativo dichiarato e la si
// confronta — e perché quell'identificativo non dice niente che l'email stessa
// non dica già a chi la sta leggendo.
func (s Signer) Token(userID string) string {
	return userID + "." + s.mac(userID)
}

// Verify restituisce l'utente di un token, se la firma torna.
//
// Il confronto è a tempo costante. Non è teatro crittografico: la firma è
// l'unica cosa che separa un link legittimo da uno costruito a tavolino, e un
// confronto che esce al primo byte diverso è misurabile.
func (s Signer) Verify(token string) (string, error) {
	if !s.Valid() {
		return "", fmt.Errorf("%w: firmatario non inizializzato", ErrInvalidToken)
	}

	userID, signature, found := strings.Cut(strings.TrimSpace(token), ".")
	if !found || userID == "" || signature == "" {
		return "", ErrInvalidToken
	}
	if !hmac.Equal([]byte(signature), []byte(s.mac(userID))) {
		return "", ErrInvalidToken
	}
	return userID, nil
}

func (s Signer) mac(value string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}
