package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/health"
	"github.com/apdsoftware/postqron/services/api/internal/httpapi"
)

// ------------------------------------------------------------------- fixture

const tokenDiProva = "un-token-di-prova-lungo-abbastanza"

// sonde è una [httpapi.Readiness] sotto controllo del test.
type sonde struct{ report health.Report }

func (s sonde) Snapshot() health.Report { return s.report }

// pagina è una [httpapi.Metrics] che scrive un testo riconoscibile.
type pagina struct{ testo string }

func (p pagina) WriteTo(w io.Writer) (int64, error) {
	n, err := io.WriteString(w, p.testo)
	return int64(n), err
}

func sano() health.Report {
	return health.Report{
		Status:               health.StatusOK,
		Checks:               []health.Check{{Name: "database", Status: health.StatusOK, Detail: "raggiungibile e in scrittura"}},
		PartitionHorizonDays: 14,
		SchedulerAge:         120 * time.Millisecond,
	}
}

func routerConOsservabilita(t *testing.T, mutate func(*httpapi.Deps)) http.Handler {
	t.Helper()
	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione di default non valida: %v", err)
	}
	deps := httpapi.Deps{
		Readiness:    sonde{report: sano()},
		Metrics:      pagina{testo: "postqron_ready 1\n"},
		MetricsToken: tokenDiProva,
	}
	if mutate != nil {
		mutate(&deps)
	}
	return httpapi.NewRouter(cfg, "test", slog.New(slog.NewTextHandler(io.Discard, nil)), deps)
}

func chiedi(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ------------------------------------------------------------------ prontezza

// TestHealthzRestaLaLiveness.
//
// `/healthz` dichiara che il processo è in piedi, e continua a farlo anche
// mentre il motore è fuori uso. Non è una svista: una liveness che fallisce per
// colpa del database farebbe ammazzare e riavviare il processo in un ciclo che
// non risolve niente, perché il problema è altrove.
func TestHealthzRestaLaLiveness(t *testing.T) {
	router := routerConOsservabilita(t, func(d *httpapi.Deps) {
		d.Readiness = sonde{report: health.Report{Status: health.StatusDown}}
	})

	if rec := chiedi(t, router, "/healthz", ""); rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, atteso 200 anche con il motore fuori uso", rec.Code)
	}
	if rec := chiedi(t, router, "/readyz", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d, atteso 503: le due domande devono dare due risposte", rec.Code)
	}
}

// TestReadyzRispondeConLoStato verifica i due codici che contano: `200` per chi
// può ricevere traffico, `503` per chi no.
func TestReadyzRispondeConLoStato(t *testing.T) {
	casi := []struct {
		nome   string
		stato  health.Status
		codice int
	}{
		{"sano", health.StatusOK, http.StatusOK},
		// Degradato **resta pronto**: c'è un problema da risolvere questa
		// settimana, e togliere traffico adesso non rimedierebbe niente.
		{"degradato", health.StatusDegraded, http.StatusOK},
		{"fuori uso", health.StatusDown, http.StatusServiceUnavailable},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			router := routerConOsservabilita(t, func(d *httpapi.Deps) {
				d.Readiness = sonde{report: health.Report{Status: caso.stato}}
			})
			rec := chiedi(t, router, "/readyz", "")
			if rec.Code != caso.codice {
				t.Fatalf("status = %d, atteso %d", rec.Code, caso.codice)
			}

			var body struct {
				Status string `json:"status"`
				Ready  bool   `json:"ready"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("corpo non decodificabile: %v", err)
			}
			if body.Status != string(caso.stato) {
				t.Errorf("status nel corpo = %q, atteso %q", body.Status, caso.stato)
			}
			if body.Ready != (caso.codice == http.StatusOK) {
				t.Errorf("ready = %v, incoerente con lo stato HTTP %d", body.Ready, rec.Code)
			}
		})
	}
}

// TestIlDettaglioDellaProntezzaNonEPubblico.
//
// «Restano due giorni di margine di partizioni» e «lo scheduler è fermo da
// quattro minuti» raccontano a chiunque com'è fatto il servizio e quando è
// debole. Lo stato complessivo dev'essere pubblico — lo legge un bilanciatore
// senza credenziali — il resto no.
func TestIlDettaglioDellaProntezzaNonEPubblico(t *testing.T) {
	router := routerConOsservabilita(t, nil)

	pubblica := chiedi(t, router, "/readyz", "").Body.String()
	if strings.Contains(pubblica, "checks") || strings.Contains(pubblica, "partition_horizon_days") {
		t.Fatalf("la risposta pubblica contiene il dettaglio operativo: %s", pubblica)
	}
	if !strings.Contains(pubblica, `"status":"ok"`) {
		t.Fatalf("la risposta pubblica non dice lo stato: %s", pubblica)
	}

	var body struct {
		Checks []struct {
			Name   string `json:"name"`
			Detail string `json:"detail"`
		} `json:"checks"`
		PartitionHorizonDays *int     `json:"partition_horizon_days"`
		SchedulerAgeSeconds  *float64 `json:"scheduler_age_seconds"`
	}
	rec := chiedi(t, router, "/readyz", tokenDiProva)
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("corpo non decodificabile: %v", err)
	}
	if len(body.Checks) != 1 || body.Checks[0].Name != "database" {
		t.Fatalf("sonde nella risposta autenticata = %+v", body.Checks)
	}
	if body.PartitionHorizonDays == nil || *body.PartitionHorizonDays != 14 {
		t.Errorf("margine di partizioni = %v, atteso 14", body.PartitionHorizonDays)
	}
	if body.SchedulerAgeSeconds == nil || *body.SchedulerAgeSeconds != 0.12 {
		t.Errorf("età del battito = %v s, attesi 0.12", body.SchedulerAgeSeconds)
	}
}

// TestReadyzNonSiMetteInCache.
//
// Un `/readyz` servito da una cache è la stessa cosa di un `/readyz` che
// risponde sempre di sì, con in più l'apparenza di funzionare — e davanti
// all'API c'è una CDN (SPEC §2).
func TestReadyzNonSiMetteInCache(t *testing.T) {
	rec := chiedi(t, routerConOsservabilita(t, nil), "/readyz", "")
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, atteso no-store", got)
	}
}

// -------------------------------------------------------------------- metriche

// TestLeMetricheNonSonoPubbliche.
//
// Un endpoint di metriche aperto racconta al mondo il carico del servizio, il
// numero di clienti attivi e i tempi di risposta dei loro bersagli.
func TestLeMetricheNonSonoPubbliche(t *testing.T) {
	router := routerConOsservabilita(t, nil)

	rec := chiedi(t, router, "/metrics", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("senza token: status = %d, atteso 401", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "postqron_ready") {
		t.Fatalf("la risposta non autenticata contiene le metriche: %s", body)
	}
	// `WWW-Authenticate` dice **come** autenticarsi, che è l'unica cosa utile da
	// restituire a chi non c'è riuscito.
	if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Errorf("WWW-Authenticate = %q, atteso uno schema Bearer", got)
	}

	if rec := chiedi(t, router, "/metrics", "un-token-sbagliato"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("con token sbagliato: status = %d, atteso 401", rec.Code)
	}

	rec = chiedi(t, router, "/metrics", tokenDiProva)
	if rec.Code != http.StatusOK {
		t.Fatalf("con il token giusto: status = %d, atteso 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "postqron_ready 1") {
		t.Errorf("corpo = %q: la pagina delle metriche non è arrivata", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, atteso il formato di esposizione testuale", got)
	}
}

// TestSenzaTokenLaRottaDelleMetricheNonEsiste.
//
// È la stessa regola dei webhook senza segreto: la rotta non viene registrata
// affatto. Un `403` direbbe comunque a chi passa che qui c'è un endpoint di
// metriche; un `404` dice che non c'è, che è la verità.
func TestSenzaTokenLaRottaDelleMetricheNonEsiste(t *testing.T) {
	router := routerConOsservabilita(t, func(d *httpapi.Deps) { d.MetricsToken = "" })

	if rec := chiedi(t, router, "/metrics", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, atteso 404", rec.Code)
	}
	// E il dettaglio della prontezza resta chiuso: senza una credenziale
	// configurata non c'è modo di autorizzare nessuno, e «nessuno» è la risposta
	// giusta — non «tutti».
	body := chiedi(t, router, "/readyz", tokenDiProva).Body.String()
	if strings.Contains(body, "checks") {
		t.Fatalf("senza token configurato il dettaglio è comunque uscito: %s", body)
	}
}

// TestSenzaSondeLaProntezzaNonEsiste: un processo che non ha un motore da
// osservare non deve rispondere «pronto» a una domanda su di esso.
func TestSenzaSondeLaProntezzaNonEsiste(t *testing.T) {
	router := routerConOsservabilita(t, func(d *httpapi.Deps) { d.Readiness = nil })
	if rec := chiedi(t, router, "/readyz", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, atteso 404", rec.Code)
	}
}
