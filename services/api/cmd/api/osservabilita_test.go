package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/health"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
	"github.com/apdsoftware/postqron/services/api/internal/metrics"
)

// La prova che chiude R7 dal lato del cablaggio.
//
// I tre pezzi hanno i propri test, e ognuno prova la propria regola contro il
// database vero. Quello che nessuno di loro può provare è che siano **collegati
// al motore che gira davvero**: un osservatore innestato al posto sbagliato non
// rompe niente, non fallisce nessun test di pacchetto, e si scopre il giorno in
// cui si va a guardare una pagina di metriche ferma a zero mentre il servizio
// sta lavorando.

// bersaglioRotto è un servizio che risponde sempre 503: il modo più semplice di
// avere un job che fallisce senza inventarsi niente sul motore.
func bersaglioRotto(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// avvisatore raccoglie i fallimenti definitivi al posto di internal/notify.
//
// Sta al posto della coda vera per una ragione di confine: qui si verifica che
// il **motore** dichiari il guasto e che le metriche lo contino. Che quel
// fallimento diventi un'email — con quale finestra di raggruppamento e quale
// tetto — è la politica di internal/notify, ed è provata lì.
type avvisatore struct {
	mu    sync.Mutex
	visti []dispatch.Failure
}

func (a *avvisatore) JobFailed(_ context.Context, f dispatch.Failure) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.visti = append(a.visti, f)
	return nil
}

func (a *avvisatore) letti() []dispatch.Failure {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]dispatch.Failure(nil), a.visti...)
}

// TestIlMotoreCheGiraSiVedeNelleMetricheENegliAvvisi.
//
// Un job che punta a un bersaglio rotto, il motore vero, e i **due**
// destinatari del fallimento definitivo che ricevono lo stesso fatto: chi
// avvisa l'utente (R21) e chi conta (R7). Sono due perché fanno due cose
// diverse — l'uno decide se scrivere a una persona e ha una politica anti-spam
// per farlo, l'altro deve vedere anche i guasti che quella politica sopprime —
// e questo test è il posto in cui si verifica che nessuno dei due sia rimasto
// scollegato.
func TestIlMotoreCheGiraSiVedeNelleMetricheENegliAvvisi(t *testing.T) {
	rotto := bersaglioRotto(t)

	var registry *metrics.Registry
	avvisi := &avvisatore{}
	b := nuovoBanco(t, func(o *engineOptions) {
		registry = metrics.New(metrics.Options{Version: "prova", Env: config.EnvDevelopment})
		o.Metrics = registry
		o.Alerter = avvisi
	})
	registry.UsePool(b.eng.Workers())

	b.pianoTeam()
	jobID := b.creaJob(map[string]any{
		"name":    "job-rotto",
		"every":   "1s",
		"request": map[string]any{"url": rotto.URL + "/rotto", "method": "POST"},
		"retries": map[string]any{"max": 0},
		"alerts":  map[string]any{"on_failure": []string{"email"}},
	})

	attendi(t, 30*time.Second, func() bool { return len(avvisi.letti()) > 0 },
		"nessun fallimento dichiarato per il job %s", jobID)

	// Chi avvisa e chi conta ricevono **lo stesso fatto**: la classe che finirà
	// nell'email è la stessa che finisce nell'etichetta della metrica.
	f := avvisi.letti()[0]
	if f.Kind != dispatch.FailureHTTPStatus {
		t.Errorf("classe = %q, attesa %q", f.Kind, dispatch.FailureHTTPStatus)
	}
	if f.HTTPStatus != 503 {
		t.Errorf("status = %d, atteso 503", f.HTTPStatus)
	}
	if f.JobName == "" || f.UserID == "" {
		t.Errorf("il fallimento non porta job e proprietario: %+v", f)
	}

	var pagina strings.Builder
	if _, err := registry.WriteTo(&pagina); err != nil {
		t.Fatalf("scrittura delle metriche: %v", err)
	}
	testo := pagina.String()
	if strings.Contains(testo, "postqron_dispatch_lag_seconds_count 0\n") {
		t.Errorf("l'istogramma del ritardo è vuoto mentre il motore sta accodando:\n%s", testo)
	}
	if strings.Contains(testo, "postqron_dispatch_occurrences_failed_total 0\n") {
		t.Errorf("nessun fallimento definitivo contato mentre il job è rotto:\n%s", testo)
	}
	if strings.Contains(testo, `postqron_dispatch_failures_total{kind="http_status"} 0`) {
		t.Errorf("la classe del fallimento non è contata:\n%s", testo)
	}
}

// TestGliEndpointDiOsservabilitaVedonoIlMotoreVero verifica l'altro capo del
// cablaggio: che le sonde interroghino il database di questo processo e che il
// router le serva.
func TestGliEndpointDiOsservabilitaVedonoIlMotoreVero(t *testing.T) {
	const token = "token-di-prova-dell-operatore"

	var registry *metrics.Registry
	b := nuovoBanco(t, func(o *engineOptions) {
		registry = metrics.New(metrics.Options{Version: "prova", Env: config.EnvDevelopment})
		o.Metrics = registry
	})
	registry.UsePool(b.eng.Workers())

	healthSvc, err := health.New(health.Options{
		Pool:   b.pool,
		Engine: registry,
		Logger: testLogger(t),
	})
	if err != nil {
		t.Fatalf("health.New: %v", err)
	}
	registry.UseHealth(healthSvc)

	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione di default non valida: %v", err)
	}
	router := httpapi.NewRouter(cfg, "prova", testLogger(t), httpapi.Deps{
		Readiness:    healthSvc,
		Metrics:      registry,
		MetricsToken: token,
	})

	// Prima di guardare, il servizio non è pronto: è la verità, non un difetto.
	if rec := chiediA(router, "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz prima delle sonde = %d, atteso 503", rec.Code)
	}

	// Lo scheduler gira da quando `nuovoBanco` l'ha avviato: la sonda del battito
	// lo trova, quella delle partizioni trova la finestra della 0006.
	attendi(t, 10*time.Second, func() bool {
		return healthSvc.Check(t.Context()).Status == health.StatusOK
	}, "il motore non si è mai dichiarato pronto: %+v", healthSvc.Snapshot().Checks)

	if rec := chiediA(router, "/readyz", ""); rec.Code != http.StatusOK {
		t.Fatalf("/readyz = %d, atteso 200: %s", rec.Code, rec.Body.String())
	}

	rec := chiediA(router, "/metrics", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics = %d, atteso 200", rec.Code)
	}
	corpo := rec.Body.String()
	if !strings.Contains(corpo, "postqron_ready 1") {
		t.Errorf("la pagina non riporta la prontezza:\n%s", corpo)
	}
	// Il margine di partizioni arriva dal database vero: la 0006 ne prepara
	// quattordici in avanti al momento della migrazione.
	if !strings.Contains(corpo, "postqron_partition_horizon_days 14") {
		t.Errorf("il margine di partizioni non è quello del database:\n%s", corpo)
	}
	if !strings.Contains(corpo, "postqron_scheduler_passes_total") {
		t.Errorf("le passate dello scheduler non sono osservate:\n%s", corpo)
	}
}

// ------------------------------------------------------------------ supporto

func chiediA(h http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// attendi sonda una condizione fino alla scadenza.
//
// L'attesa è su goroutine che stanno lavorando, non su una finestra temporale da
// far scadere: il test fallisce per timeout invece di misurare quanto è veloce
// la macchina.
func attendi(t *testing.T, entro time.Duration, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(entro)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf(format, args...)
}
