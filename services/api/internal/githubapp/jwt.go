package githubapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Durata del JWT con cui l'App si presenta.
//
// GitHub rifiuta un JWT con `exp` oltre dieci minuti dall'emissione e uno con
// `iat` nel futuro. Nove minuti stanno dentro il primo limite; il minuto tolto
// a `iat` copre il secondo, perché l'orologio della nostra macchina e quello di
// GitHub non sono lo stesso orologio e una deriva di pochi secondi basterebbe a
// far rifiutare ogni richiesta con un errore che non nomina l'ora.
const (
	jwtLifetime = 9 * time.Minute
	jwtBackdate = time.Minute
)

// appJWT firma il JWT con cui l'App si autentica verso `/app/...`.
//
// È scritto a mano invece che con una libreria per un motivo solo: un JWT RS256
// sono tre valori concatenati e una firma, cioè venti righe, e una dipendenza in
// più su un binario che tocca chiavi private è una superficie che va aggiornata
// e sorvegliata per sempre. Non c'è niente da *verificare* qui — la verifica la
// fa GitHub — ed è la verifica la parte in cui un JWT scritto a mano diventa un
// difetto di sicurezza.
func (c *Client) appJWT() (string, error) {
	now := c.now()

	header := map[string]any{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-jwtBackdate).Unix(),
		"exp": now.Add(jwtLifetime).Unix(),
		"iss": c.appID,
	}

	encodedHeader, err := encodeSegment(header)
	if err != nil {
		return "", err
	}
	encodedClaims, err := encodeSegment(claims)
	if err != nil {
		return "", err
	}

	signingInput := encodedHeader + "." + encodedClaims
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("githubapp: firma del JWT dell'App: %w", err)
	}

	var out strings.Builder
	out.WriteString(signingInput)
	out.WriteByte('.')
	out.WriteString(base64.RawURLEncoding.EncodeToString(signature))
	return out.String(), nil
}

func encodeSegment(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("githubapp: serializzazione del JWT dell'App: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}
