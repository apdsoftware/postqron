package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/apdsoftware/postqron/services/api/internal/health"
)

// MetricsTokenEnvVar è la variabile che protegge `/metrics` e il dettaglio di
// `/readyz`.
//
// Sta qui come costante e non in internal/config per la stessa ragione di
// [ParseTrustedProxies]: è una regola di questo router, e il posto in cui si
// legge l'ambiente è `main`.
const MetricsTokenEnvVar = "POSTQRON_METRICS_TOKEN"

// Readiness è la sorgente della prontezza del motore (R7). In esercizio è
// `*health.Service`.
type Readiness interface {
	// Snapshot è l'ultimo esito osservato. **Non deve interrogare il database**:
	// `/readyz` non chiede credenziali, e un endpoint pubblico che apre una
	// query per ogni chiamata è un modo di far cadere il servizio spingendoci
	// sopra.
	Snapshot() health.Report
}

// Metrics è la pagina delle metriche nel formato di esposizione testuale. In
// esercizio è `*metrics.Registry`.
type Metrics interface {
	io.WriterTo
}

// healthResponse è il corpo di `/readyz`.
//
// # Cosa c'è dentro, e per chi
//
// Il campo `status` è pubblico: è ciò che un bilanciatore deve poter leggere
// senza credenziali. `checks` no, e non è prudenza esagerata — «restano due
// giorni di margine di partizioni», «lo scheduler è fermo da quattro minuti»
// raccontano a chiunque com'è fatto il servizio e **quando è debole**. Compaiono
// solo per chi presenta il token di [MetricsTokenEnvVar], che è la stessa
// credenziale con cui si leggono le metriche: sono la stessa classe di
// informazione.
type healthResponse struct {
	Status string `json:"status"`
	Ready  bool   `json:"ready"`

	Checks []checkResponse `json:"checks,omitempty"`
	// PartitionHorizonDays e SchedulerAgeSeconds compaiono con i `checks`, per
	// la stessa ragione.
	PartitionHorizonDays *int     `json:"partition_horizon_days,omitempty"`
	SchedulerAgeSeconds  *float64 `json:"scheduler_age_seconds,omitempty"`
}

type checkResponse struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// observabilityAPI serve la prontezza e le metriche.
type observabilityAPI struct {
	log     *slog.Logger
	ready   Readiness
	metrics Metrics
	token   operatorToken
}

func newObservabilityAPI(logger *slog.Logger, deps Deps) *observabilityAPI {
	return &observabilityAPI{
		log:     logger,
		ready:   deps.Readiness,
		metrics: deps.Metrics,
		token:   newOperatorToken(deps.MetricsToken),
	}
}

func (a *observabilityAPI) routes(mux *http.ServeMux) {
	if a.ready != nil {
		mux.HandleFunc("GET /readyz", a.readyz)
	}
	// `/metrics` esiste **solo** se c'è un token con cui proteggerlo. Registrarla
	// aperta «tanto è solo un endpoint di metriche» significherebbe pubblicare il
	// carico del servizio, il numero di clienti attivi e i tempi di risposta dei
	// loro bersagli: è la stessa regola dei webhook senza segreto, che non
	// vengono registrati affatto invece di essere registrati e non verificati.
	if a.metrics != nil && a.token.enabled {
		mux.HandleFunc("GET /metrics", a.serveMetrics)
	}
}

// readyz risponde alla domanda «il motore sta funzionando adesso?».
//
// Lo stato HTTP è la risposta vera, e il corpo la ripete in forma leggibile: un
// bilanciatore guarda il codice, una persona il corpo. `503` significa «non
// mandarmi traffico»; `200` con `status: degraded` significa «funziona, e
// qualcuno deve intervenire prima che smetta».
//
// La risposta non si mette in cache in nessun punto della catena: un `/readyz`
// servito da una cache è la stessa cosa di un `/readyz` che risponde sempre di
// sì, con in più l'apparenza di funzionare.
func (a *observabilityAPI) readyz(w http.ResponseWriter, r *http.Request) {
	report := a.ready.Snapshot()

	body := healthResponse{Status: string(report.Status), Ready: report.Ready()}
	if a.token.allow(r) {
		body.Checks = make([]checkResponse, 0, len(report.Checks))
		for _, check := range report.Checks {
			body.Checks = append(body.Checks, checkResponse{
				Name: check.Name, Status: string(check.Status), Detail: check.Detail,
			})
		}
		horizon := report.PartitionHorizonDays
		age := report.SchedulerAge.Seconds()
		body.PartitionHorizonDays = &horizon
		body.SchedulerAgeSeconds = &age
	}

	status := http.StatusOK
	if !report.Ready() {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, a.log, status, body)
}

// serveMetrics scrive la pagina delle metriche.
func (a *observabilityAPI) serveMetrics(w http.ResponseWriter, r *http.Request) {
	if !a.token.allow(r) {
		// `WWW-Authenticate` dice **come** autenticarsi, che è l'unica cosa utile
		// da restituire a chi non c'è riuscito. Non dice se il token era sbagliato
		// o assente: sono la stessa risposta per chi prova a indovinarlo.
		w.Header().Set("WWW-Authenticate", `Bearer realm="postqron-metrics"`)
		writeError(w, r, a.log, http.StatusUnauthorized, "unauthenticated",
			"le metriche del servizio richiedono il token dell'operatore.")
		return
	}

	w.Header().Set("Content-Type", metricsContentType)
	w.Header().Set("Cache-Control", "no-store")
	if _, err := a.metrics.WriteTo(w); err != nil {
		// L'intestazione è già partita: non si può cambiare lo stato della
		// risposta, si può solo lasciarne traccia.
		a.log.ErrorContext(r.Context(), "scrittura della pagina delle metriche fallita",
			slog.Any("error", err))
	}
}

// metricsContentType è il tipo del formato di esposizione testuale, versione
// 0.0.4. È ripetuto qui invece di importare internal/metrics perché il router
// non deve conoscere l'implementazione delle metriche: gli basta [Metrics], che
// è `io.WriterTo`.
const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

// ------------------------------------------------------- token dell'operatore

// operatorToken è la credenziale con cui si leggono le metriche e il dettaglio
// della prontezza.
//
// # Perché un token dedicato e non una chiave API
//
// Perché queste non sono informazioni di un utente: sono informazioni sul
// **servizio**. Legarle a una chiave API (R9) significherebbe che per guardare
// il motore serve l'account di qualche cliente, e che revocare quella chiave
// spegne il monitoraggio; legarle a una sessione significherebbe che a
// raccoglierle debba essere un browser. Il token è una variabile d'ambiente,
// come i segreti dei webhook, e come loro **la sua assenza toglie la rotta**
// invece di lasciarla aperta.
//
// # Perché non un elenco di indirizzi
//
// Perché davanti all'API c'è Cloudflare (SPEC §2): l'indirizzo della connessione
// è quello del proxy per tutti, e filtrare su di esso significherebbe o non
// filtrare niente o fidarsi di `X-Forwarded-For`, che la scrive il client. La
// scelta ortogonale — mettere in ascolto le metriche solo sul loopback — resta
// possibile e non esclude questa: chi raccoglie da fuori la macchina ha comunque
// bisogno di una credenziale.
//
// # Il confronto
//
// Sull'impronta SHA-256 e a tempo costante. Confrontare le stringhe con `==`
// rivelerebbe la lunghezza del token e, un carattere per volta, il token stesso:
// è la stessa precauzione che il resto del backend prende sulle impronte delle
// chiavi API, e costa un hash per richiesta su un endpoint che viene chiamato
// una volta ogni quindici secondi.
type operatorToken struct {
	digest  [sha256.Size]byte
	enabled bool
}

func newOperatorToken(raw string) operatorToken {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return operatorToken{}
	}
	return operatorToken{digest: sha256.Sum256([]byte(raw)), enabled: true}
}

// allow dice se la richiesta porta il token giusto. Senza token configurato
// nessuno è autorizzato — mai «tutti», che è il modo in cui una protezione
// dimenticata diventa un endpoint pubblico.
func (t operatorToken) allow(r *http.Request) bool {
	if !t.enabled {
		return false
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	got := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return subtle.ConstantTimeCompare(got[:], t.digest[:]) == 1
}
