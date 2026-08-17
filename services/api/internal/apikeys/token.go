package apikeys

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

// TokenPrefix è il marchio in chiaro con cui comincia ogni chiave API.
//
// Serve a tre cose, e nessuna delle tre è decorativa:
//
//   - **disambigua la testata `Authorization`.** Una sessione e una chiave API
//     arrivano entrambe come `Bearer <valore>`; il prefisso è ciò che permette al
//     guard di sapere quale delle due verificare senza provarle entrambe — e
//     provarle entrambe significherebbe che una chiave fallita diventa un
//     tentativo di sessione, cioè due contatori di rate limiting invece di uno.
//   - **rende una chiave riconoscibile quando sfugge.** È la forma che gli
//     scanner di segreti cercano: un valore che comincia per `pq_live_` in un
//     repository pubblico è identificabile come chiave Postqron, e un giorno
//     questa è la differenza fra accorgersene e non accorgersene.
//   - **fa da etichetta in elenco.** Il prefisso conservato in `api_keys.prefix`
//     è questo più i primi caratteri del segreto, ed è la forma che la 0002 dà
//     come esempio.
//
// `live` e non `test` perché non esistono chiavi di prova: un ambiente di prova,
// in Postqron, è un'installazione a parte con il suo database.
const TokenPrefix = "pq_live_"

// tokenBytes è l'entropia del segreto di una chiave, in byte.
//
// 32 byte da CSPRNG sono 256 bit, gli stessi dei token di sessione di
// internal/auth, e per la stessa ragione: è il motivo per cui la chiave a riposo
// è un HMAC e non un Argon2id. Non c'è nessuna debolezza di entropia da
// compensare con un KDF lento, e un KDF lento andrebbe pagato a **ogni**
// richiesta autenticata — che per un'API è il caso normale, non l'eccezione.
const tokenBytes = 32

// tokenBits è l'entropia in bit, per la documentazione.
const tokenBits = tokenBytes * 8

// prefixLength è quanti caratteri della chiave finiscono in `api_keys.prefix`.
//
// Il valore comprende [TokenPrefix]: con 8 caratteri di marchio e 4 di segreto
// si ottiene la forma `pq_live_a1b2` che la 0002 porta come esempio, e resta nei
// 4..32 caratteri che la colonna ammette.
//
// I 4 caratteri di segreto esposti sono 24 bit dei 256: ne restano 232, cioè la
// chiave non si avvicina a diventare indovinabile. In cambio, due chiavi dello
// stesso utente si distinguono a occhio in un elenco, che è l'unico modo di
// sapere quale revocare.
const prefixLength = len(TokenPrefix) + 4

// newToken genera una chiave API in chiaro e il prefisso da mostrare.
//
// Il segreto è in base64url senza padding: 43 caratteri invece dei 64
// dell'esadecimale, senza caratteri che vadano protetti in una testata HTTP o in
// una variabile d'ambiente — che sono i due posti in cui una chiave API finisce.
func newToken() (token, prefix string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generazione della chiave API: %w", err)
	}
	token = TokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	return token, token[:prefixLength], nil
}

// LooksLikeToken indica se il valore ha la forma di una chiave API.
//
// È deliberatamente un controllo di forma e non di validità: serve al guard di
// internal/httpapi per scegliere *quale* credenziale sta guardando, prima di
// sapere se è buona. Un token di sessione, che è base64url puro, potrebbe in
// teoria cominciare per `pq_live_` — la probabilità è 2⁻⁴⁸ per token, cioè non
// accadrà, e se accadesse l'unica conseguenza è che quella sessione riceverebbe
// un 401 e ne aprirebbe un'altra.
func LooksLikeToken(value string) bool { return strings.HasPrefix(value, TokenPrefix) }
