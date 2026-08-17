// Package ratelimit limita la frequenza di un'operazione per chiave.
//
// Nasce per l'autenticazione (R14, issue #396): senza un tetto ai tentativi, un
// login protetto da Argon2id resta comunque forzabile — l'hashing lento alza il
// costo per tentativo, non il numero di tentativi ammessi — e il recupero
// password diventa un modo per far arrivare mille email a un indirizzo che non è
// il proprio.
//
// Il rate limiting *generale* delle API, con le quote per piano di SPEC §8
// (R10), è la issue #398 e non sta qui. Quando arriverà, questo package è il
// posto in cui aggiungerlo: l'algoritmo è lo stesso, cambiano le regole.
//
// # Perché in memoria
//
// Il contatore vive nel processo, non nel database. Tre ragioni:
//
//   - **L'API è un processo solo su una VPS** (SPEC §2). Un contatore condiviso
//     risolverebbe un problema che non c'è, al prezzo di una scrittura per
//     tentativo.
//   - **Una scrittura per tentativo fallito è essa stessa un vettore.** Chi
//     martella il login farebbe scrivere il database a ogni colpo.
//   - **Non deve rivelare quali account esistono.** La chiave per account è
//     l'impronta dell'indirizzo, contata allo stesso modo per un indirizzo
//     registrato e per uno inventato. Un contatore in tabella, ancorato a un
//     `user_id`, esisterebbe solo per gli account veri: la presenza della riga
//     sarebbe la risposta alla domanda che l'enumerazione si pone.
//
// Il prezzo è che un riavvio azzera i contatori. È accettabile perché
// l'attaccante non decide i riavvii, e perché la finestra che conta —
// impedire migliaia di tentativi in pochi minuti — è molto più corta
// dell'intervallo fra due riavvii dell'API.
package ratelimit

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// Rule descrive quante operazioni sono ammesse e in quanto tempo.
//
// L'algoritmo è un token bucket: il secchio parte pieno con `Burst` gettoni e si
// riempie di nuovo in modo continuo, `Burst` gettoni per `Window`. È preferito
// alla finestra fissa perché non ha il bordo: con una finestra fissa di 15
// minuti e 5 tentativi si possono fare 10 tentativi a cavallo dello scoccare
// dell'ora, e quel raddoppio è precisamente il momento in cui non lo si vuole.
type Rule struct {
	// Burst è il numero di operazioni ammesse a secchio pieno.
	Burst int
	// Window è il tempo in cui il secchio si riempie da vuoto a pieno.
	Window time.Duration
}

func (r Rule) valid() error {
	if r.Burst <= 0 {
		return fmt.Errorf("Burst deve essere positivo, non %d", r.Burst)
	}
	if r.Window <= 0 {
		return fmt.Errorf("Window deve essere positiva, non %s", r.Window)
	}
	return nil
}

// defaultMaxKeys è il numero massimo di chiavi tracciate.
//
// Il limite esiste perché le chiavi arrivano dall'esterno: indirizzi IP e
// impronte di email inventate. Senza tetto, chi genera chiavi diverse a ogni
// richiesta fa crescere la mappa fino a esaurire la memoria — cioè usa il
// limitatore come vettore. Con il tetto, il caso peggiore è noto:
// ~100.000 voci da poche decine di byte.
const defaultMaxKeys = 100_000

// Limiter applica una [Rule] a ogni chiave, indipendentemente.
//
// È sicuro da usare da più goroutine.
type Limiter struct {
	rule    Rule
	maxKeys int
	now     func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	// tokens è il numero di gettoni residui, frazionario perché il riempimento
	// è continuo.
	tokens float64
	// last è il momento a cui `tokens` si riferisce.
	last time.Time
}

// Option configura un [Limiter].
type Option func(*Limiter)

// WithClock sostituisce l'orologio. Serve ai test, che devono poter far passare
// quindici minuti senza aspettarli.
func WithClock(now func() time.Time) Option {
	return func(l *Limiter) { l.now = now }
}

// WithMaxKeys cambia il numero massimo di chiavi tracciate.
func WithMaxKeys(n int) Option {
	return func(l *Limiter) {
		if n > 0 {
			l.maxKeys = n
		}
	}
}

// New costruisce un limitatore.
func New(rule Rule, opts ...Option) (*Limiter, error) {
	if err := rule.valid(); err != nil {
		return nil, err
	}
	l := &Limiter{
		rule:    rule,
		maxKeys: defaultMaxKeys,
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l, nil
}

// MustNew è New per le regole costanti dichiarate nel codice: una regola scritta
// a mano che non è valida è un bug, non una condizione d'esercizio.
func MustNew(rule Rule, opts ...Option) *Limiter {
	l, err := New(rule, opts...)
	if err != nil {
		panic("ratelimit: regola non valida: " + err.Error())
	}
	return l
}

// Allow consuma un gettone della chiave.
//
// Restituisce `false` quando la chiave ha esaurito il suo credito, insieme al
// tempo dopo il quale un tentativo tornerà possibile — il valore che va nella
// testata `Retry-After`.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[key]
	if b == nil {
		l.evictIfNeeded(now)
		b = &bucket{tokens: float64(l.rule.Burst), last: now}
		l.buckets[key] = b
	} else {
		b.refill(l.rule, now)
	}

	if b.tokens < 1 {
		// Tempo che manca al prossimo gettone intero.
		missing := 1 - b.tokens
		perToken := l.rule.Window.Seconds() / float64(l.rule.Burst)
		retry := time.Duration(math.Ceil(missing*perToken)) * time.Second
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry
	}

	b.tokens--
	b.last = now
	return true, 0
}

// Reset restituisce alla chiave tutto il suo credito.
//
// Serve dopo un'operazione riuscita: i tentativi falliti di chi poi indovina la
// propria password non devono restare a suo carico, altrimenti un utente
// distratto si autoescluderebbe.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

// Tracked è il numero di chiavi in memoria. Esiste per i test sull'eviction.
func (l *Limiter) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (b *bucket) refill(rule Rule, now time.Time) {
	elapsed := now.Sub(b.last)
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed.Seconds() * float64(rule.Burst) / rule.Window.Seconds()
	if b.tokens > float64(rule.Burst) {
		b.tokens = float64(rule.Burst)
	}
	b.last = now
}

// evictIfNeeded fa spazio prima di inserire una chiave nuova.
//
// Va chiamata con il lock preso. Prima elimina le chiavi tornate a credito
// pieno: sono quelle che non portano informazione — reinserirle darebbe lo
// stesso risultato — e in esercizio normale bastano da sole, perché una chiave
// si riempie in `Window`. Solo se non ne libera nessuna passa a scartare la
// chiave vista meno di recente, che è la meno probabile parte di un attacco in
// corso.
func (l *Limiter) evictIfNeeded(now time.Time) {
	if len(l.buckets) < l.maxKeys {
		return
	}

	full := float64(l.rule.Burst)
	freed := 0
	for key, b := range l.buckets {
		b.refill(l.rule, now)
		if b.tokens >= full {
			delete(l.buckets, key)
			freed++
		}
	}
	if freed > 0 {
		return
	}

	// Nessuna chiave recuperata: la mappa è piena di secchi in uso, cioè è
	// esattamente lo scenario di riempimento deliberato. Si scarta il più
	// vecchio. Non si rifiuta la richiesta: negare tutto quando la mappa è
	// piena trasformerebbe il riempimento in un blocco totale del login.
	var oldestKey string
	var oldest time.Time
	for key, b := range l.buckets {
		if oldestKey == "" || b.last.Before(oldest) {
			oldestKey, oldest = key, b.last
		}
	}
	if oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}
