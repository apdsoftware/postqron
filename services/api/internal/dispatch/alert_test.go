package dispatch_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// avvisatore raccoglie i fallimenti che il pool considera definitivi.
type avvisatore struct {
	mu      sync.Mutex
	visti   []dispatch.Failure
	errore  error
	chiamat int
}

func (a *avvisatore) JobFailed(_ context.Context, f dispatch.Failure) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.chiamat++
	if a.errore != nil {
		return a.errore
	}
	a.visti = append(a.visti, f)
	return nil
}

func (a *avvisatore) raccolti() []dispatch.Failure {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]dispatch.Failure(nil), a.visti...)
}

func (a *avvisatore) chiamate() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.chiamat
}

// Un fallimento intermedio non avvisa nessuno: è la catena dei tentativi di R5
// che fa il suo mestiere, non un guasto da raccontare. L'avviso parte quando la
// catena si chiude.
func TestSiAvvisaSoloQuandoLaCatenaDeiTentativiEChiusa(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}
	posta := &avvisatore{}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 500}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec, Alerter: posta,
		Retry: senzaDispersione(time.Second, time.Minute),
	}, clock)

	// Tre tentativi oltre il primo: quattro esecuzioni, un solo avviso.
	occ := occorrenzaConRetry(clock, "job-che-fallisce-sempre", time.Hour, 3)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	quarto := tentativo(occ, 4)
	eventually(t, 2*time.Second, func() bool { return store.statusOf(quarto) == "failed" },
		"il quarto tentativo non è stato eseguito: stato %q", store.statusOf(quarto))
	eventually(t, 2*time.Second, func() bool { return len(posta.raccolti()) == 1 },
		"avvisi raccolti: %d, atteso uno", len(posta.raccolti()))

	// Un attimo per lasciar sbagliare un'eventuale seconda chiamata.
	time.Sleep(50 * time.Millisecond)
	avvisi := posta.raccolti()
	if len(avvisi) != 1 {
		t.Fatalf("avvisi: %d, atteso uno alla fine della catena", len(avvisi))
	}

	f := avvisi[0]
	switch {
	case f.JobID != "job-che-fallisce-sempre":
		t.Errorf("job: %q", f.JobID)
	case f.Environment != "production":
		t.Errorf("ambiente: %q", f.Environment)
	case f.Attempt != 4:
		t.Errorf("tentativo: %d, atteso l'ultimo della catena", f.Attempt)
	case f.Kind != dispatch.FailureHTTPStatus:
		t.Errorf("classificazione: %q", f.Kind)
	case f.HTTPStatus != 500:
		t.Errorf("status: %d", f.HTTPStatus)
	case !f.ScheduledFor.Equal(occ.ScheduledFor):
		t.Errorf("occorrenza: %v, attesa %v", f.ScheduledFor, occ.ScheduledFor)
	}
}

// Un'esecuzione riuscita non avvisa nessuno.
func TestUnEsecuzioneRiuscitaNonAvvisaNessuno(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}
	posta := &avvisatore{}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newRetryPool(t, dispatch.Options{Store: store, Executor: exec, Alerter: posta}, clock)

	occ := occorrenzaConRetry(clock, "job-che-funziona", time.Hour, 3)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	eventually(t, 2*time.Second, func() bool { return store.statusOf(occ) == "succeeded" },
		"l'esecuzione non è riuscita: stato %q", store.statusOf(occ))

	time.Sleep(50 * time.Millisecond)
	if n := posta.chiamate(); n != 0 {
		t.Errorf("chiamate all'avvisatore: %d, attese zero", n)
	}
}

// Un avviso che non parte non cambia l'esito dell'esecuzione.
//
// È la stessa clausola che vale per la fatturazione, un livello più in basso:
// l'esito è già scritto su `job_executions`, e un'email mancata non lo riscrive.
// Se questo errore risalisse, il worker registrerebbe un guasto del motore per
// una casella di posta irraggiungibile.
func TestUnAvvisoCheNonParteNonCambiaLEsito(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}
	posta := &avvisatore{errore: errors.New("coda irraggiungibile")}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 500}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec, Alerter: posta,
		Retry: senzaDispersione(time.Second, time.Minute),
	}, clock)

	occ := occorrenzaConRetry(clock, "job-senza-posta", time.Hour, 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	eventually(t, 2*time.Second, func() bool { return store.statusOf(occ) == "failed" },
		"l'esito non è stato scritto: stato %q", store.statusOf(occ))
	eventually(t, 2*time.Second, func() bool { return posta.chiamate() == 1 },
		"l'avvisatore non è stato chiamato: %d", posta.chiamate())

	if got := store.recordOf(occ).Outcome; got != dispatch.Failed {
		t.Errorf("esito registrato: %q", got)
	}
	if s := pool.Stats(); s.Errors != 0 {
		t.Errorf("Stats.Errors = %d: un'email mancata non è un guasto del motore", s.Errors)
	}
}

// Senza avvisatore il motore gira come prima: una macchina senza email
// configurate esegue i job lo stesso.
func TestSenzaAvvisatoreIlMotoreGiraComePrima(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 500}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec,
		Retry: senzaDispersione(time.Second, time.Minute),
	}, clock)

	occ := occorrenzaConRetry(clock, "job-muto", time.Hour, 0)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	eventually(t, 2*time.Second, func() bool { return store.statusOf(occ) == "failed" },
		"l'esito non è stato scritto: stato %q", store.statusOf(occ))
}
