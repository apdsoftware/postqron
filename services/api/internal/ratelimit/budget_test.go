package ratelimit_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// orologio è un tempo che avanza solo quando il test lo decide.
type orologio struct {
	mu  sync.Mutex
	now time.Time
}

func nuovoOrologio() *orologio {
	return &orologio{now: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}
}

func (o *orologio) Now() time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.now
}

func (o *orologio) avanza(d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.now = o.now.Add(d)
}

// TestBudgetApplicaUnaRegolaPerFamiglia: è la ragione per cui il Budget esiste.
// Due chiamanti con piani diversi hanno regole diverse, e un limitatore solo ne
// applicherebbe una sola.
func TestBudgetApplicaUnaRegolaPerFamiglia(t *testing.T) {
	budget := ratelimit.NewBudget(ratelimit.WithClock(nuovoOrologio().Now))

	stretta := ratelimit.Rule{Burst: 1, Window: time.Minute}
	larga := ratelimit.Rule{Burst: 3, Window: time.Minute}

	if ok, _ := budget.Allow("free", "utente", stretta); !ok {
		t.Fatal("il primo tentativo va concesso")
	}
	if ok, retry := budget.Allow("free", "utente", stretta); ok || retry <= 0 {
		t.Fatalf("secondo tentativo: ok = %v, retry = %s, atteso un rifiuto con attesa", ok, retry)
	}

	// Stesso utente, famiglia diversa: contatore diverso. Senza, un utente che
	// cambia piano si porterebbe dietro il credito consumato con l'altro.
	for i := range 3 {
		if ok, _ := budget.Allow("pro", "utente", larga); !ok {
			t.Fatalf("tentativo %d sulla famiglia larga rifiutato", i+1)
		}
	}
	if ok, _ := budget.Allow("pro", "utente", larga); ok {
		t.Error("la famiglia larga non ha applicato il proprio tetto")
	}
}

// TestBudgetTieneSeparateLeChiavi: la famiglia è la regola, la chiave è il
// chiamante. Due utenti dello stesso piano non si consumano il credito a
// vicenda.
func TestBudgetTieneSeparateLeChiavi(t *testing.T) {
	budget := ratelimit.NewBudget(ratelimit.WithClock(nuovoOrologio().Now))
	rule := ratelimit.Rule{Burst: 1, Window: time.Minute}

	if ok, _ := budget.Allow("free", "utente-a", rule); !ok {
		t.Fatal("primo utente rifiutato")
	}
	if ok, _ := budget.Allow("free", "utente-b", rule); !ok {
		t.Error("il credito di un utente è stato consumato da un altro")
	}
	if budget.Families() != 1 {
		t.Errorf("famiglie = %d, atteso 1: due chiavi non sono due regole", budget.Families())
	}
}

// TestBudgetSegueLaRegolaQuandoCambia: la matrice dei piani è un dato, e
// correggere un limite non deve richiedere un riavvio.
func TestBudgetSegueLaRegolaQuandoCambia(t *testing.T) {
	budget := ratelimit.NewBudget(ratelimit.WithClock(nuovoOrologio().Now))

	stretta := ratelimit.Rule{Burst: 1, Window: time.Minute}
	if ok, _ := budget.Allow("free", "utente", stretta); !ok {
		t.Fatal("primo tentativo rifiutato")
	}
	if ok, _ := budget.Allow("free", "utente", stretta); ok {
		t.Fatal("il tetto non è stato applicato")
	}

	// La riga di `plans` è stata corretta: da qui in poi vale il numero nuovo.
	corretta := ratelimit.Rule{Burst: 5, Window: time.Minute}
	if ok, _ := budget.Allow("free", "utente", corretta); !ok {
		t.Error("la regola corretta non è stata applicata: il limitatore vecchio è sopravvissuto")
	}
}

// TestBudgetNonLimitaConUnaRegolaAssurda: i numeri arrivano da una tabella, e
// una riga sbagliata non deve diventare il blocco totale del prodotto.
func TestBudgetNonLimitaConUnaRegolaAssurda(t *testing.T) {
	budget := ratelimit.NewBudget()

	for _, rule := range []ratelimit.Rule{
		{Burst: 0, Window: time.Minute},
		{Burst: 10, Window: 0},
		{Burst: -1, Window: -1},
	} {
		for i := range 3 {
			if ok, _ := budget.Allow("assurda", "utente", rule); !ok {
				t.Fatalf("%+v: tentativo %d rifiutato da una regola non valida", rule, i+1)
			}
		}
	}
	if budget.Families() != 0 {
		t.Errorf("famiglie = %d, atteso 0: una regola non valida non costruisce niente", budget.Families())
	}
}

// TestBudgetRestituisceIlCreditoNelTempo verifica che il secchio si riempia,
// perché è ciò che rende `Retry-After` una promessa mantenuta.
func TestBudgetRestituisceIlCreditoNelTempo(t *testing.T) {
	clock := nuovoOrologio()
	budget := ratelimit.NewBudget(ratelimit.WithClock(clock.Now))
	rule := ratelimit.Rule{Burst: 2, Window: time.Minute}

	budget.Allow("free", "utente", rule)
	budget.Allow("free", "utente", rule)
	ok, retry := budget.Allow("free", "utente", rule)
	if ok {
		t.Fatal("il tetto non è stato applicato")
	}

	clock.avanza(retry)
	if ok, _ := budget.Allow("free", "utente", rule); !ok {
		t.Errorf("dopo l'attesa dichiarata (%s) il tentativo è ancora rifiutato", retry)
	}
}

// TestFingerprintNonConservaLIngresso: l'impronta esiste per non tenere in
// memoria indirizzi e token, e per contare allo stesso modo identità vere e
// inventate.
func TestFingerprintNonConservaLIngresso(t *testing.T) {
	segreto := "pq_live_qualcosa_di_segreto"
	impronta := ratelimit.Fingerprint("api_key", segreto)

	if strings.Contains(impronta, segreto) {
		t.Fatal("l'impronta contiene il valore di partenza")
	}
	if len(impronta) != 32 {
		t.Errorf("lunghezza = %d, attesa 32", len(impronta))
	}

	// Un'identità inventata produce un'impronta della stessa forma e dello
	// stesso costo di una vera: è ciò che impedisce al limitatore di diventare
	// un oracolo sull'esistenza di un account o di una chiave.
	if inventata := ratelimit.Fingerprint("api_key", "pq_live_mai_esistita"); len(inventata) != len(impronta) {
		t.Errorf("impronte di lunghezza diversa: %d contro %d", len(inventata), len(impronta))
	}
}

func TestFingerprintNormalizzaEDistingue(t *testing.T) {
	if ratelimit.Fingerprint(" Mario@Example.COM ") != ratelimit.Fingerprint("mario@example.com") {
		t.Error("la normalizzazione non è applicata: un limite aggirabile con le maiuscole non è un limite")
	}

	// Senza un separatore che non può stare dentro una parte, ("ab", "c") e
	// ("a", "bc") sarebbero lo stesso secchio: due chiamanti diversi conterebbero
	// insieme.
	if ratelimit.Fingerprint("ab", "c") == ratelimit.Fingerprint("a", "bc") {
		t.Error("due chiavi diverse producono la stessa impronta")
	}

	if ratelimit.Fingerprint("session", "x") == ratelimit.Fingerprint("api_key", "x") {
		t.Error("famiglie diverse di credenziali finiscono nello stesso secchio")
	}
}
