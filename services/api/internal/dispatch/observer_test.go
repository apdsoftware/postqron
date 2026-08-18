package dispatch_test

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// I test di ciò che il pool **dichiara** a chi osserva (R7).
//
// La proprietà che contano tutti è una sola, ed è quella che separa un problema
// del cliente da un intoppo del motore: un fallimento si osserva **quando non
// ci saranno altri tentativi**, non quando la riga viene scritta.

// osservatore raccoglie i fallimenti dichiarati.
type osservatore struct {
	mu    sync.Mutex
	visti []dispatch.Failure
}

func (o *osservatore) Failed(_ context.Context, f dispatch.Failure) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.visti = append(o.visti, f)
}

func (o *osservatore) letti() []dispatch.Failure {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]dispatch.Failure(nil), o.visti...)
}

// TestIlFallimentoSiDichiaraSoloQuandoNonCiSonoAltriTentativi.
//
// Un job con un tentativo di riserva che fallisce due volte: il primo
// fallimento non si dichiara — il motore sta ancora rimediando — il secondo sì.
// Se si dichiarassero entrambi, l'alert di R21 partirebbe per un guasto che il
// retry poteva ancora risolvere.
func TestIlFallimentoSiDichiaraSoloQuandoNonCiSonoAltriTentativi(t *testing.T) {
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}
	store := newMemStore()
	obs := &osservatore{}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 500}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store:    store,
		Executor: exec,
		Observer: obs,
		Retry:    senzaDispersione(time.Second, time.Minute),
	}, clock)

	// `every` lungo: la regola che rinuncia al retry quando l'occorrenza
	// successiva arriva prima non deve entrare in questa misura.
	occ := occorrenzaConRetry(clock, "job-a", time.Hour, 1)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("occorrenza rifiutata: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Failed == 2 },
		"i due tentativi non sono arrivati in fondo: %+v", pool.Stats())
	eventually(t, 5*time.Second, func() bool { return len(obs.letti()) == 1 },
		"fallimenti dichiarati = %d, atteso 1", len(obs.letti()))
	// Un istante di assestamento: se il primo tentativo dichiarasse anche lui,
	// il secondo evento arriverebbe proprio adesso.
	time.Sleep(100 * time.Millisecond)

	visti := obs.letti()
	if len(visti) != 1 {
		t.Fatalf("fallimenti dichiarati = %d, atteso 1: %+v", len(visti), visti)
	}
	f := visti[0]
	if f.Attempt != 2 {
		t.Errorf("tentativo dichiarato = %d, atteso il secondo", f.Attempt)
	}
	if f.Outcome != dispatch.Failed {
		t.Errorf("esito = %q, atteso %q", f.Outcome, dispatch.Failed)
	}
	if f.Kind != dispatch.FailureHTTPStatus {
		t.Errorf("classe = %q, attesa %q", f.Kind, dispatch.FailureHTTPStatus)
	}
	if f.HTTPStatus != 500 {
		t.Errorf("status = %d, atteso 500", f.HTTPStatus)
	}

	if st := pool.Stats(); st.Failed != 2 || st.FailedFinal != 1 {
		t.Errorf("tentativi falliti = %d (attesi 2), occorrenze fallite = %d (attesa 1)",
			st.Failed, st.FailedFinal)
	}
}

// TestUnRetryRiuscitoNonDichiaraNessunFallimento.
//
// È l'altra metà della stessa regola, e la più importante per chi manda le
// email: il primo tentativo fallisce, il secondo riesce, e l'utente non deve
// sapere niente. Il fallimento resta scritto sulla riga — è lì che si legge la
// storia di un'occorrenza — ma non è un fatto da raccontare a una persona.
func TestUnRetryRiuscitoNonDichiaraNessunFallimento(t *testing.T) {
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}
	store := newMemStore()
	obs := &osservatore{}

	var n int
	var mu sync.Mutex
	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		mu.Lock()
		n++
		primo := n == 1
		mu.Unlock()
		if primo {
			return dispatch.Result{ResponseStatus: 503}, nil
		}
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store:    store,
		Executor: exec,
		Observer: obs,
		Retry:    senzaDispersione(time.Second, time.Minute),
	}, clock)

	occ := occorrenzaConRetry(clock, "job-b", time.Hour, 3)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("occorrenza rifiutata: %v", err)
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Succeeded == 1 },
		"il secondo tentativo non è riuscito: %+v", pool.Stats())
	time.Sleep(100 * time.Millisecond)

	if visti := obs.letti(); len(visti) != 0 {
		t.Fatalf("un retry riuscito ha comunque dichiarato %d fallimenti: %+v", len(visti), visti)
	}
	if st := pool.Stats(); st.FailedFinal != 0 {
		t.Errorf("occorrenze fallite in via definitiva = %d, attese 0", st.FailedFinal)
	}
}

// TestLaClasseDelFallimentoArrivaDallErroreNonDalTesto.
//
// La classe serve a chi deve spiegare il guasto a una persona (R21), e si ricava
// da `errors.As` sull'errore vero — che l'esecutore conserva con `%w` — non dal
// testo della riga, che è redatto e non si deve leggere (R43).
func TestLaClasseDelFallimentoArrivaDallErroreNonDalTesto(t *testing.T) {
	casi := []struct {
		nome   string
		res    dispatch.Result
		err    error
		attesa dispatch.FailureKind
	}{
		{"nome che non risolve", dispatch.Result{}, &net.DNSError{Err: "no such host", Name: "esempio.test"}, dispatch.FailureDNS},
		{"certificato non verificabile", dispatch.Result{}, &tls.CertificateVerificationError{Err: errors.New("scaduto")}, dispatch.FailureTLS},
		{"connessione rifiutata", dispatch.Result{}, &net.OpError{Op: "dial", Err: errors.New("connection refused")}, dispatch.FailureConnection},
		{"tempo scaduto", dispatch.Result{}, fmt.Errorf("timeout: %w", context.DeadlineExceeded), dispatch.FailureTimeout},
		{"risposta di errore", dispatch.Result{ResponseStatus: 502}, nil, dispatch.FailureHTTPStatus},
		{"guasto senza categoria", dispatch.Result{}, errors.New("qualcos'altro"), dispatch.FailureUnknown},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}
			store := newMemStore()
			obs := &osservatore{}

			res := caso.res
			if caso.err != nil {
				// La quarta clausola di [dispatch.Executor]: chi restituisce un
				// errore ne porta anche il testo, già redatto.
				res.ErrorText = secrets.Redactor{}.Excerpt([]byte("testo redatto"), 0)
			}
			exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
				return res, caso.err
			})

			// Nessun tentativo di riserva: il primo fallimento è già definitivo.
			pool := newRetryPool(t, dispatch.Options{
				Store: store, Executor: exec, Observer: obs,
			}, clock)

			occ := occorrenzaConRetry(clock, "job-"+caso.nome, time.Hour, 0)
			if err := hand(t, pool, store, occ); err != nil {
				t.Fatalf("occorrenza rifiutata: %v", err)
			}

			eventually(t, 5*time.Second, func() bool { return len(obs.letti()) == 1 },
				"nessun fallimento dichiarato: %+v", pool.Stats())
			if got := obs.letti()[0].Kind; got != caso.attesa {
				t.Errorf("classe = %q, attesa %q", got, caso.attesa)
			}
		})
	}
}
