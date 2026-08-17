package retry_test

import (
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/retry"
)

// noJitter è la sorgente che sceglie sempre l'estremo inferiore della finestra.
// Serve ai test che guardano la decisione, non il ritardo.
func noJitter(int64) int64 { return 0 }

// ---------------------------------------------------------------- che cosa si ritenta

// TestCosaSiRitentaECosaNo è la tabella delle decisioni di R5, ed è il posto in
// cui la scelta si discute: la riga per un `4xx` dice esplicitamente che non si
// ritenta, e se qualcuno la cambia deve cambiare anche il motivo scritto qui
// accanto.
func TestCosaSiRitentaECosaNo(t *testing.T) {
	casi := []struct {
		nome    string
		out     retry.Outcome
		ritenta bool
		motivo  retry.Reason
	}{
		{"errore di rete: nessuna risposta", retry.Outcome{Status: 0}, true, retry.Network},
		{"timeout del job", retry.Outcome{TimedOut: true}, true, retry.Timeout},
		{"timeout con risposta parziale", retry.Outcome{Status: 200, TimedOut: true}, true, retry.Timeout},
		{"500: guasto del bersaglio", retry.Outcome{Status: 500}, true, retry.ServerError},
		{"502: guasto a monte del bersaglio", retry.Outcome{Status: 502}, true, retry.ServerError},
		{"503: il bersaglio è giù", retry.Outcome{Status: 503}, true, retry.ServerError},
		{"429: il bersaglio chiede di rallentare", retry.Outcome{Status: 429}, true, retry.Throttled},
		{"408: il bersaglio ha smesso di aspettarci", retry.Outcome{Status: 408}, true, retry.Timeout},

		// I quattro casi che consumerebbero quota per un esito già noto.
		{"400: richiesta malformata", retry.Outcome{Status: 400}, false, retry.ClientError},
		{"401: credenziale rifiutata", retry.Outcome{Status: 401}, false, retry.ClientError},
		{"404: la risorsa non esiste", retry.Outcome{Status: 404}, false, retry.ClientError},
		{"422: corpo non accettato", retry.Outcome{Status: 422}, false, retry.ClientError},

		{"guasto dichiarato permanente", retry.Outcome{Status: 500, Permanent: true}, false, retry.Permanent},
		{"nessun fallimento", retry.Outcome{Status: 200}, false, retry.NoFailure},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			ritenta, motivo := retry.Retryable(caso.out)
			if ritenta != caso.ritenta {
				t.Errorf("Retryable(%+v) = %v, atteso %v", caso.out, ritenta, caso.ritenta)
			}
			if motivo != caso.motivo {
				t.Errorf("motivo = %v (%s), atteso %v", motivo, motivo, caso.motivo)
			}
		})
	}
}

// TestUnPermanenteVinceSuTutto verifica l'ordine dei controlli: un guasto
// dichiarato permanente non si ritenta nemmeno se l'esito, guardato da solo,
// sarebbe la cosa più ritentabile che c'è.
func TestUnPermanenteVinceSuTutto(t *testing.T) {
	out := retry.Outcome{TimedOut: true, Status: 0, Permanent: true, RetryAfter: time.Second}
	if ritenta, motivo := retry.Retryable(out); ritenta {
		t.Fatalf("un guasto permanente è stato ritentato, motivo %v", motivo)
	}
}

// ------------------------------------------------------------------- il tetto

// TestIlTettoDelJobFermaLaCatena: la politica dichiarata dall'utente decide
// quanti tentativi ci sono, e il tentativo che li supera non c'è.
func TestIlTettoDelJobFermaLaCatena(t *testing.T) {
	planner := retry.New(retry.Limits{Rand: noJitter})
	policy := retry.Policy{MaxRetries: 3, Backoff: retry.Exponential}
	failure := retry.Outcome{Status: 500}

	// Il tentativo 1 fallito ne concede un secondo, e così fino al quarto: dopo
	// il quarto i tre retry dichiarati sono finiti.
	for attempt := 1; attempt <= 3; attempt++ {
		if d := planner.Plan(policy, attempt, failure); !d.Retry {
			t.Fatalf("tentativo %d: nessun retry, motivo %v", attempt, d.Reason)
		}
	}
	d := planner.Plan(policy, 4, failure)
	if d.Retry {
		t.Fatalf("il quarto tentativo ne ha prodotto un quinto: %+v", d)
	}
	if d.Reason != retry.Exhausted {
		t.Fatalf("motivo = %v, atteso Exhausted", d.Reason)
	}
}

// TestIlTettoDelServizioVinceSuQuelloDelJob è il quinto punto di R5: la politica
// sta nel job, ma l'ultima parola è del servizio. Un job che chiedesse mille
// tentativi ne ottiene quanti il servizio ne concede, e nemmeno uno di più.
func TestIlTettoDelServizioVinceSuQuelloDelJob(t *testing.T) {
	planner := retry.New(retry.Limits{MaxRetries: 2, Rand: noJitter})
	policy := retry.Policy{MaxRetries: 1000, Backoff: retry.Exponential}
	failure := retry.Outcome{Status: 500}

	for attempt := 1; attempt <= 2; attempt++ {
		if d := planner.Plan(policy, attempt, failure); !d.Retry {
			t.Fatalf("tentativo %d: nessun retry, motivo %v", attempt, d.Reason)
		}
	}
	if d := planner.Plan(policy, 3, failure); d.Retry {
		t.Fatalf("il tetto del servizio è stato scavalcato: %+v", d)
	}
}

// TestUnJobPuoChiedereNessunTentativo: `max_retries: 0` è una scelta legittima —
// un job non idempotente non va ritentato da noi — e non deve degradare al
// default.
func TestUnJobPuoChiedereNessunTentativo(t *testing.T) {
	planner := retry.New(retry.Limits{Rand: noJitter})
	d := planner.Plan(retry.Policy{MaxRetries: 0}, 1, retry.Outcome{Status: 500})
	if d.Retry {
		t.Fatalf("un job senza retry ne ha ottenuto uno: %+v", d)
	}
	if d.Reason != retry.Disabled {
		t.Fatalf("motivo = %v, atteso Disabled", d.Reason)
	}
}

// TestIlMotivoDistingueIlQuattroCentoDaiTentativiFiniti: due «no» che si
// somigliano nel risultato e non nella diagnosi. Chi legge il log deve poter
// capire da che parte cercare il problema.
func TestIlMotivoDistingueIlQuattroCentoDaiTentativiFiniti(t *testing.T) {
	planner := retry.New(retry.Limits{Rand: noJitter})
	policy := retry.Policy{MaxRetries: 5}

	if d := planner.Plan(policy, 1, retry.Outcome{Status: 404}); d.Reason != retry.ClientError {
		t.Fatalf("un 404 al primo tentativo: motivo = %v, atteso ClientError", d.Reason)
	}
	if d := planner.Plan(policy, 6, retry.Outcome{Status: 500}); d.Reason != retry.Exhausted {
		t.Fatalf("un 500 a tentativi finiti: motivo = %v, atteso Exhausted", d.Reason)
	}
}

// TestUnaPoliticaScrittaMaleNonConcedeTentativiInfiniti: un numero negativo
// arrivato da chissà dove toglie i retry, non li moltiplica.
func TestUnaPoliticaScrittaMaleNonConcedeTentativiInfiniti(t *testing.T) {
	planner := retry.New(retry.Limits{Rand: noJitter})
	if d := planner.Plan(retry.Policy{MaxRetries: -1}, 1, retry.Outcome{Status: 500}); d.Retry {
		t.Fatalf("una politica negativa ha concesso un tentativo: %+v", d)
	}
}

// TestILimitiAZeroPrendonoIDefault: [retry.Limits] è una struttura di valori, e
// il pool la passa anche quando nessuno l'ha configurata.
func TestILimitiAZeroPrendonoIDefault(t *testing.T) {
	limits := retry.New(retry.Limits{}).Limits()
	if limits.MaxRetries != retry.DefaultMaxRetries {
		t.Errorf("MaxRetries = %d, atteso %d", limits.MaxRetries, retry.DefaultMaxRetries)
	}
	if limits.Base != retry.DefaultBase {
		t.Errorf("Base = %v, atteso %v", limits.Base, retry.DefaultBase)
	}
	if limits.MaxDelay != retry.DefaultMaxDelay {
		t.Errorf("MaxDelay = %v, atteso %v", limits.MaxDelay, retry.DefaultMaxDelay)
	}
	if limits.Rand == nil {
		t.Error("la sorgente della dispersione è nil: il pianificatore andrebbe in panico al primo ritardo")
	}
}

// TestIlTettoDelServizioCoincideConQuelloDelloSchema tiene insieme due numeri
// che vivono in file diversi e devono restare lo stesso numero: il `CHECK
// (max_retries BETWEEN 0 AND 10)` della migrazione 0005 e il tetto di default di
// questo package. Se qualcuno alza l'uno senza l'altro, questo test lo dice.
func TestIlTettoDelServizioCoincideConQuelloDelloSchema(t *testing.T) {
	const schemaMaxRetries = 10
	if retry.DefaultMaxRetries != schemaMaxRetries {
		t.Fatalf("DefaultMaxRetries = %d, ma `jobs.max_retries` ne ammette %d (migrazione 0005)",
			retry.DefaultMaxRetries, schemaMaxRetries)
	}
}
