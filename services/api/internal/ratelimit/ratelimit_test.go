package ratelimit_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// clock è un orologio pilotato dal test: le finestre di rate limiting si misurano
// in minuti, e aspettarle davvero renderebbe la suite inutilizzabile.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newLimiter(t testing.TB, rule ratelimit.Rule, opts ...ratelimit.Option) (*ratelimit.Limiter, *clock) {
	t.Helper()
	c := newClock()
	limiter, err := ratelimit.New(rule, append([]ratelimit.Option{ratelimit.WithClock(c.Now)}, opts...)...)
	if err != nil {
		t.Fatalf("costruzione del limitatore: %v", err)
	}
	return limiter, c
}

func TestNewRifiutaRegoleNonValide(t *testing.T) {
	tests := map[string]ratelimit.Rule{
		"burst nullo":       {Burst: 0, Window: time.Minute},
		"burst negativo":    {Burst: -1, Window: time.Minute},
		"finestra nulla":    {Burst: 5, Window: 0},
		"finestra negativa": {Burst: 5, Window: -time.Minute},
	}
	for name, rule := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ratelimit.New(rule); err == nil {
				t.Fatal("atteso un errore")
			}
		})
	}
}

func TestConsumaIlBurstPoiBlocca(t *testing.T) {
	limiter, _ := newLimiter(t, ratelimit.Rule{Burst: 3, Window: 15 * time.Minute})

	for i := 1; i <= 3; i++ {
		if ok, _ := limiter.Allow("chiave"); !ok {
			t.Fatalf("tentativo %d rifiutato, atteso ammesso", i)
		}
	}

	ok, retryAfter := limiter.Allow("chiave")
	if ok {
		t.Fatal("il quarto tentativo è stato ammesso")
	}
	if retryAfter <= 0 {
		t.Error("Retry-After deve essere positivo: il client non saprebbe quando riprovare")
	}
	if retryAfter > 15*time.Minute {
		t.Errorf("Retry-After = %s, non può superare la finestra", retryAfter)
	}
}

// Il credito torna in modo continuo, non a scatti allo scoccare della finestra.
// È la differenza fra un token bucket e una finestra fissa, e conta: con una
// finestra fissa si possono fare 2×Burst tentativi a cavallo del bordo.
func TestIlCreditoTornaInModoGraduale(t *testing.T) {
	limiter, clk := newLimiter(t, ratelimit.Rule{Burst: 4, Window: 4 * time.Minute})

	for range 4 {
		if ok, _ := limiter.Allow("chiave"); !ok {
			t.Fatal("il burst iniziale è stato rifiutato")
		}
	}
	if ok, _ := limiter.Allow("chiave"); ok {
		t.Fatal("tentativo ammesso a secchio vuoto")
	}

	// Un minuto su quattro: un gettone, non quattro.
	clk.advance(time.Minute)
	if ok, _ := limiter.Allow("chiave"); !ok {
		t.Fatal("dopo un minuto il gettone recuperato non è stato concesso")
	}
	if ok, _ := limiter.Allow("chiave"); ok {
		t.Fatal("dopo un minuto sono stati concessi due gettoni invece di uno")
	}

	// Passata l'intera finestra il secchio è pieno, e non oltre.
	clk.advance(10 * time.Minute)
	for i := range 4 {
		if ok, _ := limiter.Allow("chiave"); !ok {
			t.Fatalf("gettone %d non concesso a secchio pieno", i+1)
		}
	}
	if ok, _ := limiter.Allow("chiave"); ok {
		t.Fatal("il secchio ha superato la sua capienza")
	}
}

// Chiavi diverse hanno secchi diversi: senza questa proprietà il primo
// attaccante bloccherebbe il login a tutti gli altri.
func TestLeChiaviSonoIndipendenti(t *testing.T) {
	limiter, _ := newLimiter(t, ratelimit.Rule{Burst: 1, Window: time.Hour})

	if ok, _ := limiter.Allow("primo"); !ok {
		t.Fatal("primo tentativo rifiutato")
	}
	if ok, _ := limiter.Allow("primo"); ok {
		t.Fatal("secondo tentativo sulla stessa chiave ammesso")
	}
	if ok, _ := limiter.Allow("secondo"); !ok {
		t.Fatal("una chiave diversa è stata bloccata dalla prima")
	}
}

// Reset serve dopo un accesso riuscito: i tentativi sbagliati di chi poi indovina
// la propria password non devono restare a suo carico, o un utente distratto si
// autoescluderebbe.
func TestResetRestituisceIlCredito(t *testing.T) {
	limiter, _ := newLimiter(t, ratelimit.Rule{Burst: 2, Window: time.Hour})

	limiter.Allow("chiave")
	limiter.Allow("chiave")
	if ok, _ := limiter.Allow("chiave"); ok {
		t.Fatal("tentativo ammesso a credito esaurito")
	}

	limiter.Reset("chiave")
	if ok, _ := limiter.Allow("chiave"); !ok {
		t.Fatal("dopo Reset il credito non è tornato")
	}
}

// Le chiavi arrivano dall'esterno: senza un tetto, chi ne genera una diversa a
// ogni richiesta usa il limitatore come vettore di esaurimento della memoria.
func TestLaMemoriaRestaLimitata(t *testing.T) {
	const maxKeys = 32
	limiter, _ := newLimiter(t, ratelimit.Rule{Burst: 1, Window: time.Hour}, ratelimit.WithMaxKeys(maxKeys))

	for i := range maxKeys * 10 {
		limiter.Allow(fmt.Sprintf("chiave-%d", i))
	}
	if tracked := limiter.Tracked(); tracked > maxKeys {
		t.Fatalf("chiavi tracciate = %d, il tetto è %d", tracked, maxKeys)
	}
}

// Quando la mappa è piena di secchi in uso non si nega tutto: negare al
// riempimento trasformerebbe l'attacco in un blocco totale del login.
func TestConLaMappaPienaIlLoginNonSiBlocca(t *testing.T) {
	const maxKeys = 8
	limiter, _ := newLimiter(t, ratelimit.Rule{Burst: 1, Window: time.Hour}, ratelimit.WithMaxKeys(maxKeys))

	// Riempie la mappa di secchi vuoti, cioè non recuperabili.
	for i := range maxKeys {
		limiter.Allow(fmt.Sprintf("attaccante-%d", i))
	}
	if ok, _ := limiter.Allow("utente-legittimo"); !ok {
		t.Fatal("un utente nuovo è stato rifiutato perché la mappa era piena")
	}
}

// Le chiavi tornate a credito pieno sono le prime a essere liberate: non portano
// informazione, perché reinserirle darebbe lo stesso risultato.
func TestLeChiaviRecuperateVengonoLiberatePerPrime(t *testing.T) {
	const maxKeys = 4
	limiter, clk := newLimiter(t, ratelimit.Rule{Burst: 2, Window: time.Minute},
		ratelimit.WithMaxKeys(maxKeys))

	for i := range maxKeys {
		limiter.Allow(fmt.Sprintf("vecchia-%d", i))
	}
	// Passata la finestra, tutti i secchi sono di nuovo pieni.
	clk.advance(2 * time.Minute)

	limiter.Allow("nuova")
	if tracked := limiter.Tracked(); tracked > maxKeys {
		t.Fatalf("chiavi tracciate = %d, il tetto è %d", tracked, maxKeys)
	}
	// La chiave appena inserita deve avere il suo credito, non quello di una
	// vecchia riciclata.
	if ok, _ := limiter.Allow("nuova"); !ok {
		t.Error("la chiave nuova ha ereditato un secchio esaurito")
	}
}

// Il limitatore è consultato dagli handler HTTP, cioè da molte goroutine.
// Il test serve al race detector: `make ci` esegue `go test -race`.
func TestAllowEUtilizzabileInConcorrenza(t *testing.T) {
	limiter, err := ratelimit.New(ratelimit.Rule{Burst: 100, Window: time.Minute})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 50 {
				limiter.Allow(fmt.Sprintf("chiave-%d", i%4))
				if i%10 == 0 {
					limiter.Reset(fmt.Sprintf("chiave-%d", worker%4))
				}
			}
		}()
	}
	wg.Wait()
}

// MustNew esiste per le regole costanti scritte nel codice: una regola non valida
// lì è un bug, e deve fermare il programma invece di degradare in silenzio.
func TestMustNewPanicaSuUnaRegolaNonValida(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("atteso un panic")
		}
	}()
	ratelimit.MustNew(ratelimit.Rule{})
}
