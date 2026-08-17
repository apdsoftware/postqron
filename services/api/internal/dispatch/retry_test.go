package dispatch_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/retry"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// ------------------------------------------------------------------ fixture

// orologio è il tempo del test: un istante fermo e i timer del backoff sotto
// controllo.
//
// Serve a provare il backoff invece di **aspettarlo**. Un test che facesse
// passare davvero due secondi per verificare che il primo tentativo arriva dopo
// due secondi misurerebbe il carico della macchina, non il motore, e comincerebbe
// a fallire da solo il giorno in cui la macchina è occupata.
type orologio struct {
	mu       sync.Mutex
	adesso   time.Time
	attese   []time.Duration
	immediat bool
}

func (o *orologio) now() time.Time {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.adesso
}

// after registra l'attesa richiesta. Se l'orologio è «immediato» il tentativo
// parte subito: l'attesa è già stata misurata, e farla passare davvero non
// aggiungerebbe niente.
func (o *orologio) after(d time.Duration) <-chan time.Time {
	o.mu.Lock()
	o.attese = append(o.attese, d)
	immediat := o.immediat
	o.mu.Unlock()

	ch := make(chan time.Time, 1)
	if immediat {
		ch <- o.now()
	}
	return ch
}

func (o *orologio) misurate() []time.Duration {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]time.Duration(nil), o.attese...)
}

// newRetryPool costruisce un pool con il tempo del test già innestato. Non usa
// [newPool] perché l'orologio va sostituito **prima** dell'avvio.
func newRetryPool(t *testing.T, opts dispatch.Options, clock *orologio) *dispatch.Pool {
	t.Helper()
	if opts.Logger == nil {
		opts.Logger = testLogger(t)
	}
	if opts.Workers == 0 {
		opts.Workers = 4
	}
	pool, err := dispatch.New(opts)
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	pool.UseTestClock(clock.now, clock.after)
	pool.Start()
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })
	return pool
}

// senzaDispersione rende il ritardo deterministico: resta la metà inferiore
// della finestra, che è il valore su cui si possono scrivere asserzioni esatte.
// La dispersione ha i propri test, in internal/retry.
func senzaDispersione(base, tetto time.Duration) retry.Limits {
	return retry.Limits{Base: base, MaxDelay: tetto, Rand: func(int64) int64 { return 0 }}
}

// occorrenzaConRetry è l'occorrenza di un job che dichiara una politica di retry
// (SPEC §9). L'istante teorico è quello dell'orologio del test, così che
// l'occorrenza successiva cada davvero fra `every`.
func occorrenzaConRetry(clock *orologio, jobID string, every time.Duration, maxRetries int) scheduler.Occurrence {
	return scheduler.Occurrence{
		Job: scheduler.Job{
			ID:           jobID,
			Name:         jobID,
			Every:        every,
			Timezone:     "UTC",
			URL:          "https://example.com/hook",
			Method:       "POST",
			Timeout:      30 * time.Second,
			MaxRetries:   maxRetries,
			RetryBackoff: string(retry.Exponential),
			Enabled:      true,
		},
		ScheduledFor: clock.now(),
		Environment:  "production",
		Attempt:      1,
	}
}

// tentativo è l'occorrenza allo stesso istante teorico, al numero di tentativo
// indicato: è la forma in cui il retry deve comparire su `job_executions`.
func tentativo(occ scheduler.Occurrence, n int16) scheduler.Occurrence {
	occ.Attempt = n
	return occ
}

// ------------------------------------------------- il retry è la stessa occorrenza

// TestIlTentativoSuccessivoELaStessaOccorrenza è il primo punto di R5: un retry
// non è un'occorrenza nuova.
//
// L'asserzione che conta non è «ci sono due righe», è **quali** due righe: stesso
// `scheduled_for`, stesso ambiente, `attempt` incrementato. È ciò che permette di
// leggere la storia di un fallimento come una cosa sola invece che come due
// esecuzioni scollegate.
func TestIlTentativoSuccessivoELaStessaOccorrenza(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}

	var mu sync.Mutex
	var visti []int16
	exec := dispatch.ExecutorFunc(func(_ context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		mu.Lock()
		visti = append(visti, occ.Attempt)
		mu.Unlock()
		if occ.Attempt == 1 {
			return dispatch.Result{ResponseStatus: 503}, nil
		}
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec,
		Retry: senzaDispersione(2*time.Second, time.Minute),
	}, clock)

	occ := occorrenzaConRetry(clock, "job-con-retry", time.Hour, 3)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	secondo := tentativo(occ, 2)
	eventually(t, 2*time.Second, func() bool { return store.statusOf(secondo) == "succeeded" },
		"il secondo tentativo non è arrivato a buon fine: stato %q", store.statusOf(secondo))

	if got := store.statusOf(occ); got != "failed" {
		t.Errorf("il primo tentativo è %q, atteso failed", got)
	}
	// La prova che è la *stessa* occorrenza: le due righe si distinguono solo per
	// `attempt`, e la seconda esiste perché la prima è fallita.
	if !secondo.ScheduledFor.Equal(occ.ScheduledFor) {
		t.Errorf("scheduled_for del retry = %v, atteso %v", secondo.ScheduledFor, occ.ScheduledFor)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(visti) != 2 || visti[0] != 1 || visti[1] != 2 {
		t.Errorf("tentativi eseguiti = %v, attesi [1 2]", visti)
	}
	if s := pool.Stats(); s.Retried != 1 {
		t.Errorf("Stats.Retried = %d, atteso 1", s.Retried)
	}
}

// TestIlRitardoDelTentativoCresceAOgniFallimento è il backoff visto dal pool: la
// catena dei tentativi di R5, con le attese che raddoppiano e il tetto del job
// che la chiude.
func TestIlRitardoDelTentativoCresceAOgniFallimento(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 500}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec,
		Retry: senzaDispersione(4*time.Second, time.Hour),
	}, clock)

	occ := occorrenzaConRetry(clock, "job-che-fallisce-sempre", time.Hour, 3)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	quarto := tentativo(occ, 4)
	eventually(t, 2*time.Second, func() bool { return store.statusOf(quarto) == "failed" },
		"il quarto tentativo non è stato eseguito: stato %q", store.statusOf(quarto))
	eventually(t, 2*time.Second, func() bool { return pool.Stats().RetryExhausted == 1 },
		"la catena non si è chiusa: %+v", pool.Stats())

	// Con la dispersione spenta il ritardo è la metà inferiore della finestra, e
	// la finestra raddoppia: 4s → 2s, 8s → 4s, 16s → 8s.
	attese := clock.misurate()
	volute := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	if len(attese) != len(volute) {
		t.Fatalf("attese misurate = %v, attese volute %v", attese, volute)
	}
	for i, voluta := range volute {
		if attese[i] != voluta {
			t.Errorf("attesa del tentativo %d = %v, attesa %v", i+2, attese[i], voluta)
		}
	}

	if s := pool.Stats(); s.Retried != 3 {
		t.Errorf("Stats.Retried = %d, atteso 3 (il tetto del job)", s.Retried)
	}
	// Il quinto tentativo non esiste: il tetto del job è un tetto.
	if got := store.statusOf(tentativo(occ, 5)); got != "" {
		t.Errorf("esiste un quinto tentativo in stato %q", got)
	}
}

// TestUnQuattroCentoQuattroNonProduceTentativi: la scelta di leggere R5 più
// stretta di com'è scritta, provata dove ha effetto. Il bersaglio ha risposto che
// la richiesta è sbagliata: rimandarla identica è quota spesa per un esito già
// noto.
func TestUnQuattroCentoQuattroNonProduceTentativi(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 404}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec,
		Retry: senzaDispersione(time.Second, time.Minute),
	}, clock)

	occ := occorrenzaConRetry(clock, "job-verso-il-nulla", time.Hour, 5)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	eventually(t, 2*time.Second, func() bool { return store.statusOf(occ) == "failed" },
		"il primo tentativo non si è chiuso: stato %q", store.statusOf(occ))

	if got := store.statusOf(tentativo(occ, 2)); got != "" {
		t.Fatalf("un 404 ha prodotto un secondo tentativo in stato %q", got)
	}
	if attese := clock.misurate(); len(attese) != 0 {
		t.Fatalf("è stato armato un tentativo: attese = %v", attese)
	}
	if s := pool.Stats(); s.Retried != 0 || s.RetryExhausted != 0 {
		t.Fatalf("contatori del retry mossi da un 404: %+v", s)
	}
}

// TestUnaDestinazioneRifiutataNonSiRitenta: il guard di R38 rifiuta un
// indirizzo, non un momento. Fra dieci secondi lo rifiuterà uguale.
func TestUnaDestinazioneRifiutataNonSiRitenta(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		t.Error("l'esecutore è stato chiamato su una destinazione rifiutata")
		return dispatch.Result{}, nil
	})
	guard := dispatch.GuardFunc(func(context.Context, scheduler.Occurrence) error {
		return errors.New("indirizzo interno")
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec, Guard: guard,
		Retry: senzaDispersione(time.Second, time.Minute),
	}, clock)

	occ := occorrenzaConRetry(clock, "job-verso-l-interno", time.Hour, 5)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	eventually(t, 2*time.Second, func() bool { return store.statusOf(occ) == "failed" },
		"l'occorrenza rifiutata non si è chiusa: stato %q", store.statusOf(occ))

	if got := store.statusOf(tentativo(occ, 2)); got != "" {
		t.Fatalf("una destinazione rifiutata ha prodotto un secondo tentativo in stato %q", got)
	}
}

// -------------------------------------------- il confine con l'occorrenza successiva

// TestIlTentativoNonScavalcaLOccorrenzaSuccessiva è il quarto punto di R5, ed è
// la regola che decide chi vince fra il retry e la schedulazione.
//
// Il job vive a `every: 10s` e il backoff chiede di riprovare fra mezzo minuto:
// a quel punto l'occorrenza delle 12:00:10 e quella delle 12:00:20 sono già
// dovute, e un tentativo sull'occorrenza delle 12:00:00 sarebbe una chiamata in
// più con dati più vecchi. Vince la schedulazione.
func TestIlTentativoNonScavalcaLOccorrenzaSuccessiva(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 500}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec,
		// Finestra da un minuto: il ritardo vale mezzo minuto, l'occorrenza
		// successiva arriva fra dieci secondi.
		Retry: senzaDispersione(time.Minute, time.Hour),
	}, clock)

	occ := occorrenzaConRetry(clock, "healthcheck", 10*time.Second, 5)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	eventually(t, 2*time.Second, func() bool { return pool.Stats().RetryOverrun == 1 },
		"il retry non è stato rinunciato: %+v", pool.Stats())

	if got := store.statusOf(tentativo(occ, 2)); got != "" {
		t.Errorf("il retry ha scavalcato l'occorrenza successiva: secondo tentativo in stato %q", got)
	}
	if attese := clock.misurate(); len(attese) != 0 {
		t.Errorf("un tentativo è stato messo in attesa: %v", attese)
	}
	// Il retry rinunciato non lascia una riga: la seconda metà della regola, e
	// quella che tiene fuori 86.400 righe al giorno da un job a un secondo.
	if s := pool.Stats(); s.Skipped != 0 {
		t.Errorf("Stats.Skipped = %d: il retry rinunciato ha lasciato una riga", s.Skipped)
	}
}

// TestUnJobRadoRitentaSenzaScavalcareNiente è il complemento del test
// precedente, ed è ciò che lo rende una regola invece che un divieto: lo stesso
// backoff, su un job la cui occorrenza successiva è lontana, produce il
// tentativo.
func TestUnJobRadoRitentaSenzaScavalcareNiente(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}

	exec := dispatch.ExecutorFunc(func(_ context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		if occ.Attempt == 1 {
			return dispatch.Result{ResponseStatus: 500}, nil
		}
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newRetryPool(t, dispatch.Options{
		Store: store, Executor: exec,
		Retry: senzaDispersione(time.Minute, time.Hour),
	}, clock)

	occ := occorrenzaConRetry(clock, "digest-giornaliero", 24*time.Hour, 5)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	eventually(t, 2*time.Second, func() bool { return store.statusOf(tentativo(occ, 2)) == "succeeded" },
		"il retry non è stato eseguito: stato %q", store.statusOf(tentativo(occ, 2)))

	if s := pool.Stats(); s.RetryOverrun != 0 {
		t.Errorf("Stats.RetryOverrun = %d su un job giornaliero", s.RetryOverrun)
	}
}

// ---------------------------------------------------------------- l'arresto

// TestLArrestoNonLasciaTentativiInSospeso: un retry in attesa del proprio
// backoff quando il processo si ferma non deve lasciare dietro di sé una riga
// `pending`.
//
// Sarebbe la peggiore delle uscite: il recupero dello scheduler salta di
// proposito le righe con `triggered_by = 'retry'`, quindi quella riga non la
// riprenderebbe nessuno — resterebbe in dashboard come un'esecuzione eternamente
// in attesa e dentro l'indice che la migrazione 0006 tiene piccolo apposta.
func TestLArrestoNonLasciaTentativiInSospeso(t *testing.T) {
	store := newMemStore()
	// Timer che non scattano mai: il tentativo è in attesa quando arriva
	// l'arresto.
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)}

	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 500}, nil
	})

	pool, err := dispatch.New(dispatch.Options{
		Store: store, Executor: exec, Logger: testLogger(t), Workers: 2,
		Retry: senzaDispersione(time.Minute, time.Hour),
	})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	pool.UseTestClock(clock.now, clock.after)
	pool.Start()

	occ := occorrenzaConRetry(clock, "job-interrotto", time.Hour, 3)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	eventually(t, 2*time.Second, func() bool { return len(clock.misurate()) == 1 },
		"il tentativo non è mai stato armato")

	if err := pool.Shutdown(context.Background()); err != nil {
		t.Fatalf("arresto: %v", err)
	}

	if s := pool.Stats(); s.RetryAbandoned != 1 {
		t.Errorf("Stats.RetryAbandoned = %d, atteso 1: %+v", s.RetryAbandoned, s)
	}
	if got := store.statusOf(tentativo(occ, 2)); got != "" {
		t.Errorf("l'arresto ha lasciato il secondo tentativo in stato %q", got)
	}
}

// TestUnTentativoInterrottoDallArrestoSiChiude copre l'altra metà: il retry era
// già partito ed è l'arresto a interromperlo. Non si può rilasciare a `pending`
// come si fa con un'occorrenza dello scheduler — nessuno lo riprenderebbe — e
// quindi si chiude con ciò che è davvero successo.
func TestUnTentativoInterrottoDallArrestoSiChiude(t *testing.T) {
	store := newMemStore()
	clock := &orologio{adesso: time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC), immediat: true}

	inVolo := make(chan struct{})
	var una sync.Once
	exec := dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		if occ.Attempt == 1 {
			return dispatch.Result{ResponseStatus: 500}, nil
		}
		// Il secondo tentativo non finisce da solo: lo interrompe l'arresto.
		una.Do(func() { close(inVolo) })
		<-ctx.Done()
		return dispatch.Result{}, ctx.Err()
	})

	pool, err := dispatch.New(dispatch.Options{
		Store: store, Executor: exec, Logger: testLogger(t), Workers: 2,
		Retry:        senzaDispersione(time.Second, time.Minute),
		DrainTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	pool.UseTestClock(clock.now, clock.after)
	pool.Start()

	occ := occorrenzaConRetry(clock, "job-lento-al-secondo-giro", time.Hour, 3)
	if err := hand(t, pool, store, occ); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	<-inVolo

	// L'arresto restituisce un errore solo se ha rilasciato qualcosa: un
	// tentativo non si rilascia, si chiude.
	if err := pool.Shutdown(context.Background()); err != nil {
		t.Fatalf("arresto: %v", err)
	}

	secondo := tentativo(occ, 2)
	if got := store.statusOf(secondo); got != "failed" {
		t.Fatalf("il tentativo interrotto è in stato %q, atteso failed", got)
	}
	if s := pool.Stats(); s.Released != 0 {
		t.Errorf("Stats.Released = %d: un tentativo è stato rilasciato a `pending`", s.Released)
	}
}
