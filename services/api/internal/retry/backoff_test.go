package retry_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/retry"
)

// I due estremi della dispersione. Con questi due pianificatori il ritardo
// diventa deterministico e si può parlare della **finestra** invece che di un
// numero: `sotto` sceglie sempre il primo istante utile, `sopra` sempre
// l'ultimo.
func estremi(limits retry.Limits) (sotto, sopra *retry.Planner) {
	basso := limits
	basso.Rand = func(int64) int64 { return 0 }
	alto := limits
	alto.Rand = func(n int64) int64 { return n - 1 }
	return retry.New(basso), retry.New(alto)
}

// TestIlBackoffEsponenzialeRaddoppia è la prima metà di R5: il ritardo cresce, e
// cresce raddoppiando.
//
// L'asserzione è sulla finestra, non su un valore: la dispersione è parte del
// ritardo e non un'aggiunta da spegnere per poterlo misurare. Ogni tentativo cade
// in [finestra/2, finestra), e la finestra raddoppia.
func TestIlBackoffEsponenzialeRaddoppia(t *testing.T) {
	sotto, sopra := estremi(retry.Limits{Base: time.Second, MaxDelay: time.Hour})

	finestra := time.Second
	for attempt := 1; attempt <= 6; attempt++ {
		minimo := sotto.Delay(retry.Exponential, attempt, retry.Outcome{})
		massimo := sopra.Delay(retry.Exponential, attempt, retry.Outcome{})

		if minimo != finestra/2 {
			t.Errorf("tentativo %d: ritardo minimo = %v, atteso %v", attempt, minimo, finestra/2)
		}
		if massimo != finestra-1 {
			t.Errorf("tentativo %d: ritardo massimo = %v, atteso %v", attempt, massimo, finestra-1)
		}
		finestra *= 2
	}
}

// TestLeAltreFormeDiBackoffCresconoComeDichiarato: `retry_backoff` è un enum a
// tre valori (migrazione 0001) e tutti e tre devono fare ciò che dicono.
func TestLeAltreFormeDiBackoffCresconoComeDichiarato(t *testing.T) {
	sotto, _ := estremi(retry.Limits{Base: time.Second, MaxDelay: time.Hour})

	casi := []struct {
		backoff  retry.Backoff
		finestre []time.Duration
	}{
		{retry.Linear, []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second}},
		{retry.Fixed, []time.Duration{time.Second, time.Second, time.Second, time.Second}},
		{retry.Exponential, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}},
		// Un valore che questo package non conosce — l'enum del database è
		// cresciuto e il codice no — vale la forma predefinita dello schema.
		{retry.Backoff("decorrelated"), []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}},
	}

	for _, caso := range casi {
		t.Run(string(caso.backoff), func(t *testing.T) {
			for i, finestra := range caso.finestre {
				attempt := i + 1
				if d := sotto.Delay(caso.backoff, attempt, retry.Outcome{}); d != finestra/2 {
					t.Errorf("tentativo %d: ritardo minimo = %v, atteso %v", attempt, d, finestra/2)
				}
			}
		})
	}
}

// TestIlBackoffSiFermaAlTetto: senza tetto, il raddoppio arriva alle ore in una
// decina di tentativi, e un tentativo fra un'ora non ritenta più niente.
func TestIlBackoffSiFermaAlTetto(t *testing.T) {
	const tetto = 10 * time.Second
	sotto, sopra := estremi(retry.Limits{Base: time.Second, MaxDelay: tetto})

	for attempt := 5; attempt <= 40; attempt++ {
		minimo := sotto.Delay(retry.Exponential, attempt, retry.Outcome{})
		massimo := sopra.Delay(retry.Exponential, attempt, retry.Outcome{})

		if minimo != tetto/2 {
			t.Fatalf("tentativo %d: ritardo minimo = %v, atteso il tetto dimezzato %v", attempt, minimo, tetto/2)
		}
		if massimo >= tetto {
			t.Fatalf("tentativo %d: ritardo massimo = %v, oltre il tetto %v", attempt, massimo, tetto)
		}
	}
}

// TestUnBackoffNonEMaiNegativo copre l'aritmetica che nessuno guarda: `base <<
// 62` è negativo, e un ritardo negativo è un tentativo immediato — cioè il
// contrario esatto di un backoff — proprio sul tentativo più avanzato.
func TestUnBackoffNonEMaiNegativo(t *testing.T) {
	_, sopra := estremi(retry.Limits{Base: time.Hour, MaxDelay: time.Minute})
	for _, attempt := range []int{0, 1, 62, 63, 1000, 1 << 20} {
		if d := sopra.Delay(retry.Exponential, attempt, retry.Outcome{}); d <= 0 || d >= time.Minute {
			t.Errorf("tentativo %d: ritardo = %v, fuori da (0, tetto)", attempt, d)
		}
	}
}

// -------------------------------------------------------------- la dispersione

// TestLaDispersioneCopreLaFinestra: il terzo punto di R5 in piccolo. Con una
// sorgente vera, mille estrazioni dello stesso tentativo devono spargersi su
// tutta la finestra e non addensarsi su un valore.
func TestLaDispersioneCopreLaFinestra(t *testing.T) {
	const estrazioni = 1000
	finestra := 8 * time.Second

	// Seme fisso: la dispersione è casuale, il test no. Un test statistico che
	// dipende dal seme è un test che prima o poi fallisce da solo, di notte, su
	// una macchina di qualcun altro.
	sorgente := rand.New(rand.NewPCG(392, 5))
	planner := retry.New(retry.Limits{
		Base:     finestra,
		MaxDelay: time.Hour,
		Rand:     sorgente.Int64N,
	})

	distinti := map[time.Duration]struct{}{}
	minimo, massimo := time.Duration(1<<62), time.Duration(0)
	for range estrazioni {
		d := planner.Delay(retry.Exponential, 1, retry.Outcome{})
		if d < finestra/2 || d >= finestra {
			t.Fatalf("ritardo = %v, fuori dalla finestra [%v, %v)", d, finestra/2, finestra)
		}
		distinti[d] = struct{}{}
		minimo = min(minimo, d)
		massimo = max(massimo, d)
	}

	// Su mille estrazioni in una finestra di quattro secondi al nanosecondo, due
	// valori uguali sarebbero già una coincidenza notevole; qualche centinaio di
	// valori distinti è la soglia sotto cui qualcosa si è rotto.
	if len(distinti) < estrazioni/2 {
		t.Errorf("solo %d ritardi distinti su %d: la dispersione non disperde", len(distinti), estrazioni)
	}
	// Gli estremi devono essere davvero raggiunti, non solo ammessi: una
	// dispersione che si tenesse nel mezzo lascerebbe i job addensati.
	if minimo > finestra/2+finestra/16 {
		t.Errorf("il ritardo più corto è %v: la parte bassa della finestra resta vuota", minimo)
	}
	if massimo < finestra-finestra/16 {
		t.Errorf("il ritardo più lungo è %v: la parte alta della finestra resta vuota", massimo)
	}
}

// TestMilleJobFallitiInsiemeNonRitentanoInsieme è il terzo punto di R5 nella
// forma in cui conta davvero.
//
// Mille job che falliscono nello stesso istante contro lo stesso bersaglio in
// difficoltà — è il caso normale, non quello raro: se falliscono tutti insieme è
// perché il bersaglio è uno solo — e la domanda è se il retry produce una seconda
// raffica coordinata.
//
// Il test misura l'addensamento: la finestra viene divisa in venti fette e
// nessuna deve contenere una porzione sproporzionata dei tentativi. Il confronto
// con il raddoppio nudo, verificato nella seconda metà, è la ragione per cui il
// backoff da solo non basta — senza dispersione tutte e mille finirebbero nella
// stessa fetta.
func TestMilleJobFallitiInsiemeNonRitentanoInsieme(t *testing.T) {
	const (
		job   = 1000
		fette = 20
	)
	finestra := 4 * time.Second

	sorgente := rand.New(rand.NewPCG(392, 11))
	planner := retry.New(retry.Limits{Base: finestra, MaxDelay: time.Hour, Rand: sorgente.Int64N})
	policy := retry.Policy{MaxRetries: 3, Backoff: retry.Exponential}

	conteggio := make([]int, fette)
	for range job {
		d := planner.Plan(policy, 1, retry.Outcome{Status: 503})
		if !d.Retry {
			t.Fatalf("un 503 non è stato ritentato: %+v", d)
		}
		// La finestra utile è [finestra/2, finestra): è lì che cadono i ritardi.
		fetta := int((d.Delay - finestra/2) * fette / (finestra / 2))
		if fetta < 0 || fetta >= fette {
			t.Fatalf("ritardo %v fuori dalla finestra attesa", d.Delay)
		}
		conteggio[fetta]++
	}

	// Distribuite uniformemente sarebbero cinquanta per fetta. Il doppio della
	// media è una soglia larghissima: serve a riconoscere una raffica, non a
	// misurare la qualità del generatore.
	const tetto = 2 * job / fette
	for fetta, n := range conteggio {
		if n > tetto {
			t.Errorf("la fetta %d contiene %d tentativi su %d: sono tornati quasi tutti insieme",
				fetta, n, job)
		}
		if n == 0 {
			t.Errorf("la fetta %d è vuota: la dispersione non copre tutta la finestra", fetta)
		}
	}

	// La controprova: senza dispersione, gli stessi mille job tornano tutti nello
	// stesso identico istante. È il comportamento che il raddoppio da solo
	// produrrebbe, ed è ciò che questo test esiste per escludere.
	nudo := retry.New(retry.Limits{Base: finestra, MaxDelay: time.Hour, Rand: func(int64) int64 { return 0 }})
	primo := nudo.Plan(policy, 1, retry.Outcome{Status: 503}).Delay
	for range job {
		if d := nudo.Plan(policy, 1, retry.Outcome{Status: 503}).Delay; d != primo {
			t.Fatalf("la controprova non è deterministica: %v ≠ %v", d, primo)
		}
	}
}

// ------------------------------------------------------------- il Retry-After

// TestIlRetryAfterDelBersaglioEUnPavimento: quando il bersaglio dice «non prima
// di allora», il ritardo parte da lì e la dispersione si somma sopra. Tornare
// prima significherebbe ignorare l'unica indicazione attendibile che abbiamo sul
// suo stato.
func TestIlRetryAfterDelBersaglioEUnPavimento(t *testing.T) {
	const chiesto = 30 * time.Second
	finestra := 2 * time.Second
	sotto, sopra := estremi(retry.Limits{Base: finestra, MaxDelay: time.Minute, MaxRetryAfter: time.Hour})

	casi := []struct {
		nome    string
		status  int
		onorato bool
	}{
		{"429: il bersaglio chiede di rallentare", 429, true},
		{"503: il bersaglio è giù e dice per quanto", 503, true},
		{"500: la testata vale anche su un guasto più grave", 500, true},
		{"nessuna risposta: non c'è nessuna testata da onorare", 0, false},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			out := retry.Outcome{Status: caso.status, RetryAfter: chiesto}
			minimo := sotto.Delay(retry.Exponential, 1, out)
			massimo := sopra.Delay(retry.Exponential, 1, out)

			pavimento := time.Duration(0)
			if caso.onorato {
				pavimento = chiesto
			}
			if minimo != pavimento+finestra/2 {
				t.Errorf("ritardo minimo = %v, atteso %v", minimo, pavimento+finestra/2)
			}
			if massimo != pavimento+finestra-1 {
				t.Errorf("ritardo massimo = %v, atteso %v", massimo, pavimento+finestra-1)
			}
			if caso.onorato && minimo < chiesto {
				t.Errorf("ritardo %v: siamo tornati prima di quanto il bersaglio ha chiesto (%v)", minimo, chiesto)
			}
		})
	}
}

// TestIlRetryAfterEDisperso: mille job che ricevono lo stesso `Retry-After` dallo
// stesso bersaglio non devono tornare tutti al sessantesimo secondo. Il pavimento
// toglie il *quando*, non il *tutti insieme*.
func TestIlRetryAfterEDisperso(t *testing.T) {
	sorgente := rand.New(rand.NewPCG(392, 17))
	planner := retry.New(retry.Limits{
		Base:          4 * time.Second,
		MaxDelay:      time.Minute,
		MaxRetryAfter: time.Hour,
		Rand:          sorgente.Int64N,
	})
	out := retry.Outcome{Status: 429, RetryAfter: time.Minute}

	distinti := map[time.Duration]struct{}{}
	for range 1000 {
		d := planner.Delay(retry.Exponential, 1, out)
		if d < time.Minute {
			t.Fatalf("ritardo %v: prima del minuto chiesto dal bersaglio", d)
		}
		distinti[d] = struct{}{}
	}
	if len(distinti) < 500 {
		t.Fatalf("solo %d ritardi distinti: con un Retry-After uguale per tutti si torna in raffica", len(distinti))
	}
}

// TestUnRetryAfterAssurdoVieneTagliato: la testata arriva da fuori. `Retry-After:
// 86400` è una risposta legale, e onorarla alla lettera vorrebbe dire tenere un
// tentativo in memoria per un giorno.
func TestUnRetryAfterAssurdoVieneTagliato(t *testing.T) {
	const tetto = 2 * time.Minute
	_, sopra := estremi(retry.Limits{Base: time.Second, MaxDelay: 10 * time.Second, MaxRetryAfter: tetto})

	d := sopra.Delay(retry.Exponential, 1, retry.Outcome{Status: 503, RetryAfter: 24 * time.Hour})
	if d > tetto+10*time.Second {
		t.Fatalf("ritardo = %v: il tetto su Retry-After (%v) non è stato applicato", d, tetto)
	}
	if d < tetto {
		t.Fatalf("ritardo = %v: il taglio ha accorciato l'attesa sotto il proprio tetto %v", d, tetto)
	}
}

// TestUnQuattroCentoConRetryAfterRestaSenzaTentativi: la testata non trasforma
// un rifiuto in una richiesta di riprovare. Un `404` con `Retry-After` è un `404`.
func TestUnQuattroCentoConRetryAfterRestaSenzaTentativi(t *testing.T) {
	planner := retry.New(retry.Limits{Rand: noJitter})
	d := planner.Plan(retry.Policy{MaxRetries: 5}, 1, retry.Outcome{Status: 404, RetryAfter: time.Second})
	if d.Retry {
		t.Fatalf("un 404 con Retry-After è stato ritentato: %+v", d)
	}
}
