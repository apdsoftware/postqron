package ratelimit

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
)

// Budget applica **regole diverse a famiglie diverse di chiamanti**.
//
// Serve quando la regola non è una costante del codice ma un dato. I limiti di
// SPEC §8 cambiano con il piano dell'utente, e un [Limiter] ne applica una sola:
// un Budget tiene un limitatore per famiglia — «il piano Free», «il piano Pro» —
// e dentro ciascuno la chiave resta il singolo chiamante.
//
// È la generalizzazione di ciò che #395 aveva scritto dentro internal/jobs per i
// trigger manuali: la forma era già quella giusta — una regola derivata dal
// piano, applicata per chiave utente — e le quote generali di R10 (#398) hanno
// bisogno esattamente della stessa cosa su altre operazioni. Sta qui e non lì
// perché due copie della stessa struttura divergono al primo limite aggiunto.
//
// È sicuro da usare da più goroutine.
type Budget struct {
	opts []Option

	mu       sync.Mutex
	families map[string]*family
}

// family è un limitatore insieme alla regola con cui è stato costruito.
//
// La regola viaggia accanto al limitatore perché la matrice dei piani è **dati**
// e non codice: la migrazione 0003 dice che correggere un limite non deve
// richiedere un deploy. Un limitatore costruito una volta e riusato per sempre
// continuerebbe ad applicare i numeri letti al primo passaggio, e la correzione
// avrebbe effetto solo al riavvio successivo — cioè non sarebbe una correzione,
// sarebbe un deploy con un altro nome.
type family struct {
	rule    Rule
	limiter *Limiter
}

// NewBudget costruisce un budget vuoto. Le opzioni valgono per ogni limitatore
// che nascerà al suo interno.
func NewBudget(opts ...Option) *Budget {
	return &Budget{opts: opts, families: map[string]*family{}}
}

// Allow consuma un gettone della chiave dentro la famiglia indicata.
//
// `family` identifica la **regola** (il codice del piano, l'operazione), `key`
// identifica il **chiamante**. Tenerli distinti è ciò che permette a due utenti
// dello stesso piano di avere contatori separati e a uno stesso utente di avere
// un contatore per operazione.
//
// Una regola non valida non limita niente, e restituisce `true`. È la stessa
// scelta che #395 aveva fatto per il tetto dei trigger: i numeri arrivano da una
// tabella, e se un giorno ne arrivasse uno assurdo il comportamento giusto è non
// limitare, non rifiutare tutto. Un limitatore che nega ogni richiesta perché il
// listino ha una riga sbagliata è un guasto totale del prodotto prodotto da un
// errore di battitura.
func (b *Budget) Allow(family, key string, rule Rule) (bool, time.Duration) {
	limiter := b.limiterFor(family, rule)
	if limiter == nil {
		return true, 0
	}
	return limiter.Allow(key)
}

// Reset restituisce alla chiave tutto il suo credito dentro una famiglia.
func (b *Budget) Reset(family, key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if entry, ok := b.families[family]; ok {
		entry.limiter.Reset(key)
	}
}

// Families è il numero di famiglie vive. Esiste per i test.
func (b *Budget) Families() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.families)
}

// limiterFor restituisce il limitatore della famiglia, costruendolo se manca e
// **sostituendolo se la regola è cambiata**.
//
// La mappa delle famiglie non ha un tetto come quella delle chiavi, e non è una
// dimenticanza: le chiavi arrivano dall'esterno, le famiglie no. Una famiglia è
// un codice di piano moltiplicato per un'operazione, cioè un valore che nasce
// nel codice o in una riga di `plans` — non qualcosa che un chiamante possa
// inventare a ogni richiesta.
func (b *Budget) limiterFor(name string, rule Rule) *Limiter {
	if rule.valid() != nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if entry, ok := b.families[name]; ok {
		if entry.rule == rule {
			return entry.limiter
		}
		// La regola è cambiata sotto i piedi: i secchi vecchi erano tarati su
		// numeri che non valgono più e ripartire da capo è l'unica lettura
		// difendibile di «il limite adesso è un altro».
		delete(b.families, name)
	}

	limiter, err := New(rule, b.opts...)
	if err != nil {
		return nil
	}
	b.families[name] = &family{rule: rule, limiter: limiter}
	return limiter
}

// Fingerprint riduce una chiave a un'impronta di lunghezza fissa.
//
// Le chiavi di rate limiting si conservano in memoria per il tempo della
// finestra, e ciò che vi finisce dentro arriva dal chiamante: indirizzi email
// tentati, token presentati, identificativi di utente. L'impronta esiste per due
// ragioni, e nessuna delle due è la crittografia.
//
//   - **Non conservare ciò che non serve.** Un elenco di indirizzi tentati o di
//     token presentati, in memoria, è materiale che non serve a contare e che in
//     un dump di memoria o in un log di debug è un danno.
//   - **Contare allo stesso modo identità vere e inventate.** È la regola che
//     #396 ha stabilito per l'autenticazione (vedi il commento del package): un
//     contatore che esiste solo per gli account veri risponde, con la sola sua
//     esistenza, alla domanda che l'enumerazione si pone. L'impronta ha la stessa
//     forma e lo stesso costo per un'identità che esiste e per una fabbricata sul
//     momento, quindi da fuori i due casi sono indistinguibili.
//
// Le parti sono normalizzate (spazi ai lati e maiuscole) e unite con un
// separatore che non può comparire dentro una parte: senza, `("ab", "c")` e
// `("a", "bc")` sarebbero la stessa impronta, e due chiamanti diversi
// finirebbero nello stesso secchio.
func Fingerprint(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized = append(normalized, strings.ToLower(strings.TrimSpace(part)))
	}
	sum := sha256.Sum256([]byte(strings.Join(normalized, "\x00")))
	// Sedici byte: la metà di SHA-256 basta e avanza a distinguere le chiavi di
	// una mappa da centomila voci, e occupa la metà.
	return hex.EncodeToString(sum[:16])
}
