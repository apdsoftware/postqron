// Il controllo che impedisce al contratto OpenAPI di mentire (R51).
//
// # Il problema
//
// Un documento OpenAPI scritto a mano diverge dal codice entro un mese, e da
// quel momento è **peggio di non averlo**: chi lo legge costruisce un client su
// una promessa falsa, e il difetto si manifesta a casa sua, dove noi non lo
// vediamo e lui non ha modo di sapere che la colpa non è sua.
//
// # Perché il confronto e non la generazione
//
// Le strade erano tre.
//
//  1. **Generare il documento dal codice.** Il codice non contiene abbastanza:
//     `http.ServeMux` conosce metodo e percorso, non i codici di errore, non i
//     motivi, non le condizioni. Un generatore avrebbe prodotto un documento
//     esatto e muto — e per farlo parlare servirebbero annotazioni, cioè un
//     secondo linguaggio da tenere allineato al primo.
//  2. **Generare il codice dal documento.** È la strada che elimina la
//     divergenza per costruzione, e costa la riscrittura di tutte le rotte
//     esistenti sotto un generatore: gli handler attuali portano decisioni che
//     un generatore non esprime — la precedenza della chiave sul cookie, i due
//     429 di R10, la redazione dei messaggi di errore sulle rotte che portano
//     segreti. Barattare quelle con un contratto sarebbe stato un pessimo
//     cambio.
//  3. **Tenerli separati e confrontarli in CI.** È questa. Il documento resta
//     leggibile e ricco di motivazioni, il codice resta com'è, e la divergenza è
//     un test rosso invece di un difetto in produzione.
//
// La terza ha un costo dichiarato: **il confronto copre ciò che è meccanico** —
// rotte, permessi, codici, campi, enumerazioni — e non la prosa. Una descrizione
// può invecchiare senza che nessuno se ne accorga. Ciò su cui un client fa
// branching, invece, non può.
//
// # Come è fatto il confronto
//
// Ogni sorgente di verità è **il codice vero, non una sua copia**: l'elenco
// delle rotte viene dal router costruito come in esercizio, i codici di errore
// dal sorgente del package e dall'esecuzione delle due funzioni che li
// compongono, i campi dalle strutture serializzate, le enumerazioni dalle
// costanti che il servizio applica.
package httpapi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/apdsoftware/postqron/services/api/internal/aicreds"
	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/config"
	"github.com/apdsoftware/postqron/services/api/internal/execstream"
	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
	"github.com/apdsoftware/postqron/services/api/internal/health"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
	"github.com/apdsoftware/postqron/services/api/openapi"
)

// ------------------------------------------------------------------- rotte

// TestContrattoRotte confronta le rotte del documento con quelle del router.
//
// L'elenco del codice non è una seconda copia scritta a mano: è ciò che
// [register] registra, dalle stesse righe e con le stesse condizioni che
// governano l'esercizio. Una rotta aggiunta, tolta o rinominata rende rosso
// questo test finché il documento non la segue.
func TestContrattoRotte(t *testing.T) {
	nelCodice := rotteDelRouter(t)
	nelDocumento := rotteDelDocumento(t, caricaDocumento(t))

	confrontaInsiemi(t, "rotte", nelCodice, nelDocumento,
		"il codice registra questa rotta e il documento non la descrive: chi legge il contratto non sa che esiste",
		"il documento descrive questa rotta e il codice non la registra: chi la chiama riceve 404")
}

// rotteDelRouter elenca le rotte registrate con tutte le dipendenze presenti.
//
// Tutte, perché una dipendenza mancante toglie delle rotte (vedi [Deps]) e il
// contratto descrive il servizio configurato per intero. I servizi sono valori
// zero: nessun handler viene chiamato, qui si guarda solo chi si registra.
func rotteDelRouter(t *testing.T) []string {
	t.Helper()

	cfg, err := config.LoadFrom(func(string) string { return "" })
	if err != nil {
		t.Fatalf("configurazione di default non valida: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	hub, err := execstream.New(execstream.Options{Logger: logger})
	if err != nil {
		t.Fatalf("hub dello streaming non costruito: %v", err)
	}
	t.Cleanup(hub.Stop)

	rec := &registratoreDiRotte{}
	register(rec, cfg, "test", logger, Deps{
		Auth:             &auth.Service{},
		Jobs:             &jobs.Service{},
		APIKeys:          &apikeys.Service{},
		GitHubWebhook:    &githubhook.Service{},
		PaddleWebhook:    &paddle.Service{},
		Billing:          &billing.Service{},
		Secrets:          &secrets.Service{},
		AIKeys:           &aicreds.Service{},
		ExecutionStreams: hub,
		Readiness:        prontezzaFinta{},
		Metrics:          metricheFinte{},
		MetricsToken:     "token-di-prova",
	})
	return rec.patterns
}

// registratoreDiRotte è un [router] che non serve niente: prende nota e basta.
type registratoreDiRotte struct{ patterns []string }

func (r *registratoreDiRotte) HandleFunc(pattern string, _ func(http.ResponseWriter, *http.Request)) {
	r.patterns = append(r.patterns, pattern)
}

type prontezzaFinta struct{}

func (prontezzaFinta) Snapshot() health.Report { return health.Report{} }

type metricheFinte struct{}

func (metricheFinte) WriteTo(io.Writer) (int64, error) { return 0, nil }

// ------------------------------------------------------------------ permessi

// TestContrattoScope confronta i permessi dichiarati con quelli applicati (R9).
//
// Verifica due cose, e la seconda conta quanto la prima:
//
//  1. ogni rotta che il codice mette dietro uno scope dichiara nel documento
//     **quello** scope. Un documento che promettesse `jobs:read` dove il codice
//     pretende `jobs:write` manderebbe l'utente a creare una chiave che non
//     funziona;
//  2. **nessuna operazione può dichiarare di accettare una chiave API se il
//     codice non la registra dietro uno scope.** È la proprietà che protegge le
//     rotte delle credenziali e della spesa — `/keys`, `/secrets`, `/ai/keys`,
//     `/billing` — dal veder comparire nel contratto un metodo di autenticazione
//     che non hanno.
func TestContrattoScope(t *testing.T) {
	nelCodice := map[string]string{}
	for _, route := range (&jobsAPI{}).scopedRoutes() {
		nelCodice[route.Pattern] = string(route.Scope)
	}

	for pattern, op := range operazioni(t, caricaDocumento(t)) {
		scopeAtteso, conChiave := nelCodice[pattern]
		switch {
		case op.ammetteChiaveAPI() && !conChiave:
			t.Errorf("%s: il documento dice che accetta una chiave API, ma il codice non la registra dietro nessuno scope: "+
				"o è una promessa falsa, o è una rotta che sta esponendo più di quanto dovrebbe", pattern)
		case !op.ammetteChiaveAPI() && conChiave:
			t.Errorf("%s: il codice la registra con lo scope %q, ma il documento non dichiara `apiKey` fra i metodi di autenticazione",
				pattern, scopeAtteso)
		case conChiave && op.Scope != scopeAtteso:
			t.Errorf("%s: il documento dichiara `x-api-key-scope: %s`, il codice pretende %q",
				pattern, op.Scope, scopeAtteso)
		}
		delete(nelCodice, pattern)
	}

	for pattern, scope := range nelCodice {
		t.Errorf("%s (scope %q): rotta con scope assente dal documento", pattern, scope)
	}
}

// ------------------------------------------------------------ codici di errore

// TestContrattoCodiciDiErrore confronta l'enumerazione `ErrorCode` con i codici
// che il servizio scrive davvero.
//
// I codici sono ciò su cui un client fa branching (R53), quindi sono la parte
// del contratto che costa di più quando è sbagliata: un `code` documentato e mai
// emesso è un ramo morto, uno emesso e non documentato è un ramo che nessuno ha
// scritto e che al client arriva come «errore sconosciuto».
//
// La raccolta è di due tipi, perché di due tipi sono i codici:
//
//   - **letterali** — presi dal sorgente del package con go/ast, nelle due forme
//     in cui questo codice li scrive. Una terza forma non passa inosservata:
//     [raccogliCodiciLetterali] fallisce sulle chiamate che non sa leggere,
//     invece di ignorarle;
//   - **composti** — `plan_limit_<limite>` e i tetti tecnici non sono scritti da
//     nessuna parte come stringhe intere. Qui non si indovinano: si **eseguono**
//     le due funzioni che li compongono, per ogni limite che il package
//     internal/jobs dichiara, e si legge il codice dalla risposta prodotta.
func TestContrattoCodiciDiErrore(t *testing.T) {
	nelCodice := raccogliCodiciLetterali(t)
	for _, codice := range codiciDeiLimiti(t) {
		nelCodice = append(nelCodice, codice)
	}

	doc := caricaDocumento(t)
	nelDocumento := doc.Components.Schemas["ErrorCode"].Enum
	if len(nelDocumento) == 0 {
		t.Fatal("lo schema ErrorCode non elenca nessun codice")
	}

	confrontaInsiemi(t, "codici di errore", nelCodice, nelDocumento,
		"il servizio scrive questo codice e il documento non lo elenca: per un client è un errore sconosciuto",
		"il documento elenca questo codice e il servizio non lo scrive più: è un ramo morto nei client")
}

// raccogliCodiciLetterali legge dal sorgente del package i codici scritti come
// stringhe.
//
// Le due forme riconosciute sono quelle che questo package usa:
//
//	writeError(w, r, log, status, "invalid_cursor", "…")   // quinto argomento
//	ErrorDetail{Code: "rate_limited", …}                   // campo Code
//	status, code := http.StatusBadRequest, "invalid_request"  // variabile `code`
//
// Una forma diversa **fa fallire il test invece di essere saltata**. È la parte
// che rende il controllo affidabile nel tempo: un raccoglitore che ignora ciò
// che non capisce diventa muto a poco a poco, e la sua approvazione smette di
// significare qualcosa senza che nessuno lo noti.
func raccogliCodiciLetterali(t *testing.T) []string {
	t.Helper()

	var codici []string
	// Le funzioni che compongono un codice invece di scriverlo. Il loro esito è
	// verificato eseguendole, in [codiciDeiLimiti].
	composte := map[string]bool{"failPlanLimit": true, "failServiceLimit": true}

	for _, file := range sorgentiDelPackage(t, ".") {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			nome := fn.Name.Name
			ast.Inspect(fn, func(n ast.Node) bool {
				switch nodo := n.(type) {
				case *ast.CallExpr:
					ident, ok := nodo.Fun.(*ast.Ident)
					if !ok || ident.Name != "writeError" || len(nodo.Args) < 5 {
						return true
					}
					switch arg := nodo.Args[4].(type) {
					case *ast.BasicLit:
						codici = append(codici, valoreStringa(t, arg))
					case *ast.Ident:
						if arg.Name != "code" {
							t.Errorf("%s: writeError riceve il codice da %q, una forma che il controllo del contratto non sa leggere",
								nome, arg.Name)
						}
					default:
						t.Errorf("%s: writeError riceve il codice in una forma che il controllo del contratto non sa leggere", nome)
					}

				case *ast.CompositeLit:
					ident, ok := nodo.Type.(*ast.Ident)
					if !ok || ident.Name != "ErrorDetail" {
						return true
					}
					for _, elt := range nodo.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						chiave, ok := kv.Key.(*ast.Ident)
						if !ok || chiave.Name != "Code" {
							continue
						}
						switch valore := kv.Value.(type) {
						case *ast.BasicLit:
							codici = append(codici, valoreStringa(t, valore))
						case *ast.Ident:
							// Il codice arriva da un parametro: è il caso di writeError,
							// che lo riceve dai chiamanti — dove il letterale c'è ed è
							// già stato raccolto.
							if valore.Name != "code" {
								t.Errorf("%s: ErrorDetail.Code viene da %q, una forma che il controllo del contratto non sa leggere",
									nome, valore.Name)
							}
						default:
							if !composte[nome] {
								t.Errorf("%s: ErrorDetail.Code è composto qui, ma solo %v hanno un controllo che ne verifica l'esito",
									nome, chiavi(composte))
							}
						}
					}

				case *ast.AssignStmt:
					for i, lhs := range nodo.Lhs {
						ident, ok := lhs.(*ast.Ident)
						if !ok || ident.Name != "code" || i >= len(nodo.Rhs) {
							continue
						}
						if lit, ok := nodo.Rhs[i].(*ast.BasicLit); ok {
							codici = append(codici, valoreStringa(t, lit))
						}
					}
				}
				return true
			})
		}
	}
	if len(codici) == 0 {
		t.Fatal("nessun codice di errore trovato nel sorgente: il raccoglitore non sta leggendo niente")
	}
	return codici
}

// codiciDeiLimiti esegue le due funzioni che compongono un codice e legge il
// risultato dalla risposta.
//
// I limiti non sono elencati qui: vengono dalle costanti di internal/jobs, lette
// dal sorgente. Un limite nuovo entra da solo nel confronto, che è precisamente
// ciò che un elenco scritto a mano non farebbe.
func codiciDeiLimiti(t *testing.T) []string {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	leggi := func(rec *httptest.ResponseRecorder) string {
		var corpo ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
			t.Fatalf("risposta di limite illeggibile: %v", err)
		}
		return corpo.Error.Code
	}

	var codici []string
	for _, limite := range costantiDelTipo(t, "../jobs", "LimitKind") {
		rec := httptest.NewRecorder()
		failPlanLimit(rec, httptest.NewRequest(http.MethodGet, "/", nil), logger,
			&jobs.PlanLimitError{Limit: jobs.LimitKind(limite), Plan: "free"})
		codici = append(codici, leggi(rec))
	}
	for _, tetto := range costantiDelTipo(t, "../jobs", "ServiceLimitKind") {
		rec := httptest.NewRecorder()
		failServiceLimit(rec, httptest.NewRequest(http.MethodGet, "/", nil), logger,
			jobs.NewServiceLimit(jobs.ServiceLimitKind(tetto), time.Second, "tetto tecnico"))
		codici = append(codici, leggi(rec))
	}
	return codici
}

// TestContrattoDueQuattrocentoVentinove verifica la differenza che R10 pretende,
// sulla risposta vera e non sulla descrizione.
//
// È l'unica proprietà del contratto che vale la pena verificare due volte —
// negli schemi e qui — perché è quella che, se sbagliata, rimette nel prodotto
// la bugia commerciale che R10 vieta: un invito ad aggiornare piano mostrato a
// chi ha superato un tetto che nessun piano alza.
func TestContrattoDueQuattrocentoVentinove(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	t.Run("il tetto tecnico non nomina nessun piano", func(t *testing.T) {
		for _, tetto := range costantiDelTipo(t, "../jobs", "ServiceLimitKind") {
			rec := httptest.NewRecorder()
			failServiceLimit(rec, req, logger,
				jobs.NewServiceLimit(jobs.ServiceLimitKind(tetto), 3*time.Second, "tetto"))

			var corpo ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
				t.Fatalf("%s: risposta illeggibile: %v", tetto, err)
			}
			if corpo.Error.Plan != "" || corpo.Error.Limit != "" {
				t.Errorf("%s: il tetto tecnico nomina piano=%q limite=%q — sono i campi su cui un client "+
					"decide di proporre un aggiornamento, e qui l'aggiornamento non servirebbe (R10)",
					tetto, corpo.Error.Plan, corpo.Error.Limit)
			}
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("%s: status %d, atteso 429", tetto, rec.Code)
			}
			if corpo.Error.RetryAfter < 1 || rec.Header().Get("Retry-After") == "" {
				t.Errorf("%s: un tetto tecnico si libera da sé, quindi deve dire fra quanto riprovare", tetto)
			}
		}
	})

	t.Run("la quota di piano nomina il piano", func(t *testing.T) {
		rec := httptest.NewRecorder()
		failPlanLimit(rec, req, logger, &jobs.PlanLimitError{
			Limit: jobs.LimitWriteRate, Plan: "free", RetryAfter: 2 * time.Second,
		})
		var corpo ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
			t.Fatalf("risposta illeggibile: %v", err)
		}
		if corpo.Error.Plan == "" || corpo.Error.Limit == "" {
			t.Errorf("la quota di piano non nomina piano e limite: sono ciò che dice al client "+
				"che l'aggiornamento è davvero la risposta (piano=%q limite=%q)", corpo.Error.Plan, corpo.Error.Limit)
		}
	})
}

// --------------------------------------------------------------- SSE

// TestContrattoEventiSSE confronta i codici degli avvisi del flusso con
// l'enumerazione del documento.
//
// Non passano da [ErrorBody]: sono JSON scritto a mano dentro un evento SSE,
// perché a flusso aperto lo status non si può più cambiare. Restano comunque
// parte del contratto — un client li riceve e ci decide sopra — quindi vanno
// confrontati anche loro, con l'unica lettura possibile: il sorgente.
func TestContrattoEventiSSE(t *testing.T) {
	sorgente, err := os.ReadFile("executions_stream.go")
	if err != nil {
		t.Fatalf("sorgente dello streaming non leggibile: %v", err)
	}

	var nelCodice []string
	for _, m := range regexp.MustCompile(`"code":"([a-z_]+)"`).FindAllStringSubmatch(string(sorgente), -1) {
		nelCodice = append(nelCodice, m[1])
	}
	if len(nelCodice) == 0 {
		t.Fatal("nessun avviso di flusso trovato nel sorgente")
	}

	doc := caricaDocumento(t)
	confrontaInsiemi(t, "avvisi del flusso SSE", nelCodice, doc.Components.Schemas["StreamNoticeCode"].Enum,
		"il flusso manda questo avviso e il documento non lo elenca",
		"il documento elenca questo avviso e il flusso non lo manda più")
}

// ------------------------------------------------------------------- schemi

// legameDiSchema lega uno schema del documento alla struttura che il servizio
// serializza.
type legameDiSchema struct {
	schema string
	valore any
	// obbligatori verifica anche `required` e la nullabilità. Vale per le
	// risposte, dove «il campo c'è sempre» è una promessa: un client che lo
	// legge senza controllare deve poterlo fare.
	//
	// Non vale per i corpi delle richieste, dove l'obbligatorietà non sta nella
	// struttura ma nella validazione del dominio — e duplicarla qui sarebbe una
	// terza copia da tenere allineata.
	obbligatori bool
	// sottoinsieme accetta che il documento descriva **meno** campi della
	// struttura. Serve alle due viste ristrette dell'errore (R10), che
	// descrivono la stessa struttura Go promettendo di meno.
	sottoinsieme bool
}

// legami è la corrispondenza fra gli schemi del documento e le strutture del
// codice.
//
// Ogni schema del documento che descrive un corpo **deve** comparire qui: il
// test lo verifica, e la ragione è R43. Un campo aggiunto a una risposta senza
// passare da questo elenco è un campo che potrebbe portare fuori qualcosa —
// oggi il documento se ne accorge, e non passa la CI finché qualcuno non ha
// scritto cos'è.
var legami = []legameDiSchema{
	// Sistema.
	{schema: "Health", valore: Health{}, obbligatori: true},
	{schema: "Readiness", valore: healthResponse{}, obbligatori: true},
	{schema: "ReadinessCheck", valore: checkResponse{}, obbligatori: true},
	{schema: "Accepted", valore: acceptedResponse{}, obbligatori: true},

	// Errori. `Error` è la struttura piena; le due viste ristrette promettono di
	// meno sulla stessa struttura, ed è tutto il punto di R10.
	{schema: "ErrorEnvelope", valore: ErrorBody{}, obbligatori: true},
	{schema: "Error", valore: ErrorDetail{}, obbligatori: true},
	{schema: "FieldError", valore: FieldErrorBody{}, obbligatori: true},
	{schema: "PlanLimitEnvelope", valore: ErrorBody{}, sottoinsieme: true},
	{schema: "PlanLimitError", valore: ErrorDetail{}, sottoinsieme: true},
	{schema: "ServiceLimitEnvelope", valore: ErrorBody{}, sottoinsieme: true},
	{schema: "ServiceLimitError", valore: ErrorDetail{}, sottoinsieme: true},

	// Autenticazione.
	{schema: "User", valore: UserResponse{}, obbligatori: true},
	{schema: "Session", valore: SessionResponse{}, obbligatori: true},
	{schema: "SessionEnvelope", valore: SessionEnvelope{}, obbligatori: true},
	{schema: "SessionList", valore: SessionListResponse{}, obbligatori: true},
	{schema: "RevokedSessions", valore: RevokedSessionsResponse{}, obbligatori: true},
	{schema: "RegisterInput", valore: registerRequest{}},
	{schema: "LoginInput", valore: loginRequest{}},
	{schema: "ForgotPasswordInput", valore: forgotPasswordRequest{}},
	{schema: "ResetPasswordInput", valore: resetPasswordRequest{}},
	{schema: "ChangePasswordInput", valore: changePasswordRequest{}},
	{schema: "VerifyEmailInput", valore: verifyEmailRequest{}},

	// Job ed esecuzioni.
	{schema: "Job", valore: JobResponse{}, obbligatori: true},
	{schema: "JobTarget", valore: TargetResponse{}, obbligatori: true},
	{schema: "JobRetries", valore: RetriesResponse{}, obbligatori: true},
	{schema: "JobAlerts", valore: AlertsResponse{}, obbligatori: true},
	{schema: "JobSuspension", valore: SuspensionResponse{}, obbligatori: true},
	{schema: "JobList", valore: JobListResponse{}, obbligatori: true},
	{schema: "Page", valore: PageResponse{}, obbligatori: true},
	{schema: "Execution", valore: ExecutionResponse{}, obbligatori: true},
	{schema: "ExecutionList", valore: ExecutionListResponse{}, obbligatori: true},
	{schema: "JobInput", valore: JobPayload{}},
	{schema: "JobTargetInput", valore: targetPayload{}},
	{schema: "JobRetriesInput", valore: retriesPayload{}},
	{schema: "JobAlertsInput", valore: alertsPayload{}},
	{schema: "TriggerInput", valore: TriggerPayload{}},

	// Chiavi API, segreti, chiavi AI: le tre risposte che **non** contengono un
	// valore, e la sola che ne contiene uno.
	{schema: "ApiKey", valore: APIKeyResponse{}, obbligatori: true},
	{schema: "ApiKeyList", valore: APIKeyListResponse{}, obbligatori: true},
	{schema: "ApiKeyCreated", valore: APIKeyCreatedResponse{}, obbligatori: true},
	{schema: "ApiKeyScopes", valore: ScopesResponse{}, obbligatori: true},
	{schema: "ApiKeyInput", valore: createKeyRequest{}},
	{schema: "Secret", valore: SecretResponse{}, obbligatori: true},
	{schema: "SecretList", valore: SecretListResponse{}, obbligatori: true},
	{schema: "SecretInput", valore: createSecretRequest{}},
	{schema: "SecretUpdateInput", valore: updateSecretRequest{}},
	{schema: "AiKey", valore: AIKeyResponse{}, obbligatori: true},
	{schema: "AiKeyList", valore: AIKeyListResponse{}, obbligatori: true},
	{schema: "AiKeyInput", valore: saveAIKeyRequest{}},

	// Fatturazione.
	{schema: "Subscription", valore: SubscriptionResponse{}, obbligatori: true},
	{schema: "SuspendedJobs", valore: SuspendedJobsResponse{}, obbligatori: true},
	{schema: "Checkout", valore: CheckoutResponse{}, obbligatori: true},
	{schema: "CheckoutCustomData", valore: billing.CustomData{}, obbligatori: true},
	{schema: "CheckoutInput", valore: checkoutRequest{}},

	// Webhook.
	{schema: "GithubDelivery", valore: GitHubWebhookResponse{}, obbligatori: true},
	{schema: "PaddleDelivery", valore: PaddleWebhookResponse{}, obbligatori: true},
}

// schemiSenzaStruttura sono gli schemi che descrivono qualcosa che nessuna
// struttura Go serializza, e che quindi non si può confrontare per riflessione.
//
// L'elenco è corto **apposta**: è la lista delle eccezioni, e ogni voce è un
// pezzo di contratto che nessuno controlla. Sta qui perché si veda.
var schemiSenzaStruttura = map[string]string{
	// I payload `data:` degli avvisi SSE sono JSON scritto a mano dentro
	// executions_stream.go: i loro codici sono confrontati da
	// TestContrattoEventiSSE, la forma no.
	"StreamNotice": "JSON scritto a mano nel flusso SSE",
}

// TestContrattoSchemi confronta i campi di ogni schema con quelli serializzati.
//
// È il controllo che difende R43. Non perché sappia riconoscere un segreto —
// non lo sa — ma perché **rende impossibile aggiungere in silenzio un campo a
// una risposta**: la CI diventa rossa, e per farla tornare verde bisogna
// scrivere nel contratto che cos'è quel campo. È in quel momento che ci si
// accorge di stare esponendo qualcosa.
func TestContrattoSchemi(t *testing.T) {
	doc := caricaDocumento(t)
	descritti := map[string]bool{}

	for _, legame := range legami {
		schema, ok := doc.Components.Schemas[legame.schema]
		if !ok {
			t.Errorf("%s: schema assente dal documento", legame.schema)
			continue
		}
		descritti[legame.schema] = true

		tipo := reflect.TypeOf(legame.valore)
		campi := campiJSON(tipo)

		nelDocumento := chiaviOrdinate(schema.Properties)
		nelCodice := make([]string, 0, len(campi))
		for _, campo := range campi {
			nelCodice = append(nelCodice, campo.nome)
		}

		switch {
		case legame.sottoinsieme:
			for _, nome := range nelDocumento {
				if !slices.ContainsFunc(campi, func(c campoJSON) bool { return c.nome == nome }) {
					t.Errorf("%s: il documento descrive `%s`, che %s non serializza", legame.schema, nome, tipo)
				}
			}
		default:
			confrontaInsiemi(t, legame.schema, nelCodice, nelDocumento,
				"campo serializzato da "+tipo.String()+" e non descritto nel documento (R43: un campo non dichiarato è un campo di cui nessuno ha detto cos'è)",
				"campo descritto nel documento e non serializzato da "+tipo.String())
		}

		if !legame.obbligatori {
			continue
		}

		var attesiObbligatori []string
		for _, campo := range campi {
			if !campo.facoltativo {
				attesiObbligatori = append(attesiObbligatori, campo.nome)
			}
			// Un puntatore che non sparisce quando è vuoto arriva al client come
			// `null`: se il documento non lo dichiara nullabile, un client generato
			// da qui rifiuta una risposta legittima.
			if campo.nullabile {
				if tipi := tipiDichiarati(schema.Properties[campo.nome]); len(tipi) > 0 && !slices.Contains(tipi, "null") {
					t.Errorf("%s.%s: il campo è `null` quando è vuoto, e il documento lo dichiara %v",
						legame.schema, campo.nome, tipi)
				}
			}
		}
		confrontaInsiemi(t, legame.schema+".required", attesiObbligatori, schema.Required,
			"campo sempre presente nella risposta e non dichiarato obbligatorio",
			"campo dichiarato obbligatorio che può non esserci: il client lo leggerebbe come mancante")
	}

	// Ogni schema che descrive un corpo deve avere un legame. L'eccezione va
	// dichiarata, e dichiararla è il costo che la tiene rara.
	for nome, schema := range doc.Components.Schemas {
		if descritti[nome] || len(schema.Properties) == 0 {
			continue
		}
		if motivo, ok := schemiSenzaStruttura[nome]; ok {
			t.Logf("%s: non confrontato (%s)", nome, motivo)
			continue
		}
		t.Errorf("%s: schema con proprietà e nessuna struttura Go a cui confrontarlo. "+
			"Aggiungilo a `legami`, oppure dichiara in `schemiSenzaStruttura` perché non si può", nome)
	}
}

// ------------------------------------------------------------- enumerazioni

// TestContrattoEnumerazioni confronta le enumerazioni con le costanti che il
// servizio applica.
//
// Un valore ammesso dal dominio e assente dal documento è un valore che un
// client generato da qui rifiuta pur essendo legittimo; il contrario è un valore
// che il client manda e il servizio respinge.
func TestContrattoEnumerazioni(t *testing.T) {
	doc := caricaDocumento(t)

	casi := map[string][]string{
		"Environment":        stringheDi(jobs.Environments),
		"HttpMethod":         stringheDi(jobs.Methods),
		"RetryBackoff":       stringheDi(jobs.Backoffs),
		"OverlapPolicy":      stringheDi(jobs.OverlapPolicies),
		"AlertChannel":       stringheDi(jobs.AlertChannels),
		"ExecutionStatus":    stringheDi(jobs.ExecutionStatuses),
		"ExecutionTrigger":   stringheDi(jobs.ExecutionTriggers),
		"SuspensionReason":   stringheDi([]jobs.SuspensionReason{jobs.SuspendedByJobLimit, jobs.SuspendedByResolution}),
		"ApiKeyScope":        stringheDi(apikeys.Scopes()),
		"AiProvider":         stringheDi(aicreds.Providers()),
		"PlanCode":           {paddle.PlanFree, paddle.PlanPro, paddle.PlanTeam, paddle.PlanAgency},
		"BillingPeriod":      stringheDi([]paddle.Period{paddle.PeriodMonthly, paddle.PeriodYearly}),
		"SubscriptionStatus": stringheDi(statiSottoscrizione()),
	}

	for nome, nelCodice := range casi {
		schema, ok := doc.Components.Schemas[nome]
		if !ok {
			t.Errorf("%s: enumerazione assente dal documento", nome)
			continue
		}
		confrontaInsiemi(t, nome, nelCodice, schema.Enum,
			"valore ammesso dal servizio e non elencato nel documento",
			"valore elencato nel documento e non ammesso dal servizio")
	}

	// `PlanLimitError.limit` non è un tipo a sé nel documento: è l'enumerazione
	// in linea del campo, e i suoi valori sono i limiti di piano di R15.
	limiti := doc.Components.Schemas["PlanLimitError"].Properties["limit"].Enum
	confrontaInsiemi(t, "PlanLimitError.limit", costantiDelTipo(t, "../jobs", "LimitKind"), limiti,
		"limite di piano applicato dal servizio e non elencato nel documento",
		"limite di piano elencato nel documento e non più applicato")
}

func statiSottoscrizione() []paddle.SubscriptionStatus {
	return []paddle.SubscriptionStatus{
		paddle.SubscriptionActive, paddle.SubscriptionTrialing, paddle.SubscriptionPastDue,
		paddle.SubscriptionPaused, paddle.SubscriptionCanceled,
	}
}

// ------------------------------------------------------------------ tipi

// TestContrattoTipiDichiarati verifica che ogni `type` sia uno dei sette del
// formato.
//
// Sembra una banalità, ed è la falla misurata del validatore dello schema: lo
// schema di OpenAPI 3.1 delega gli oggetti-schema al dialetto JSON Schema
// 2020-12, e lì `scripts/openapi-validate.mjs` è permissivo — un `type: stringa`
// scritto male passa la validazione e rompe ogni generatore di client. Provato
// scrivendolo, non supposto. Otto righe qui chiudono il buco.
func TestContrattoTipiDichiarati(t *testing.T) {
	ammessi := []string{"string", "number", "integer", "boolean", "object", "array", "null"}

	var controlla func(percorso string, schema schemaDoc)
	controlla = func(percorso string, schema schemaDoc) {
		for _, tipo := range tipiDichiarati(schema) {
			if !slices.Contains(ammessi, tipo) {
				t.Errorf("%s: `type: %s` non è un tipo del formato (ammessi %v)", percorso, tipo, ammessi)
			}
		}
		for _, nome := range chiaviOrdinate(schema.Properties) {
			controlla(percorso+"."+nome, schema.Properties[nome])
		}
	}

	doc := caricaDocumento(t)
	for _, nome := range chiaviOrdinate(doc.Components.Schemas) {
		controlla(nome, doc.Components.Schemas[nome])
	}
}

// --------------------------------------------------------------- versione

// TestContrattoVersione verifica che il documento dichiari una versione
// leggibile.
//
// La **politica** di versionamento e deprecazione è R52 e appartiene alla issue
// #466: qui si verifica solo che il numero ci sia e abbia la forma che la
// politica potrà governare. Un contratto senza versione non è deprecabile, e
// scoprirlo il giorno in cui serve è tardi.
func TestContrattoVersione(t *testing.T) {
	doc := caricaDocumento(t)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(doc.Info.Version) {
		t.Errorf("info.version = %q: attesa la forma maggiore.minore.patch", doc.Info.Version)
	}
}

// ------------------------------------------------------------------ supporto

type documento struct {
	Info struct {
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]map[string]yaml.Node `yaml:"paths"`
	Components struct {
		Schemas map[string]schemaDoc `yaml:"schemas"`
	} `yaml:"components"`
}

type schemaDoc struct {
	Ref        string               `yaml:"$ref"`
	Type       yaml.Node            `yaml:"type"`
	Enum       []string             `yaml:"enum"`
	Properties map[string]schemaDoc `yaml:"properties"`
	Required   []string             `yaml:"required"`
}

type operazione struct {
	OperationID string                `yaml:"operationId"`
	Security    []map[string][]string `yaml:"security"`
	Scope       string                `yaml:"x-api-key-scope"`
}

func (o operazione) ammetteChiaveAPI() bool {
	return slices.ContainsFunc(o.Security, func(m map[string][]string) bool {
		_, ok := m["apiKey"]
		return ok
	})
}

// metodiHTTP sono le chiavi di un `paths` che descrivono un'operazione. Le
// altre — `parameters`, `summary` — descrivono il percorso.
var metodiHTTP = []string{"get", "post", "put", "patch", "delete", "head", "options"}

func caricaDocumento(t *testing.T) documento {
	t.Helper()
	var doc documento
	if err := yaml.Unmarshal(openapi.Spec, &doc); err != nil {
		t.Fatalf("documento OpenAPI illeggibile: %v", err)
	}
	if len(doc.Paths) == 0 {
		t.Fatal("il documento OpenAPI non descrive nessun percorso")
	}
	return doc
}

// rotteDelDocumento elenca le operazioni nella forma di http.ServeMux, che è la
// forma in cui il codice le scrive: confrontare due elenchi significa prima
// scriverli nella stessa lingua.
func rotteDelDocumento(t *testing.T, doc documento) []string {
	t.Helper()
	var rotte []string
	for pattern := range operazioni(t, doc) {
		rotte = append(rotte, pattern)
	}
	return rotte
}

func operazioni(t *testing.T, doc documento) map[string]operazione {
	t.Helper()
	out := map[string]operazione{}
	for percorso, voci := range doc.Paths {
		for chiave, nodo := range voci {
			if !slices.Contains(metodiHTTP, chiave) {
				continue
			}
			var op operazione
			if err := nodo.Decode(&op); err != nil {
				t.Fatalf("%s %s: operazione illeggibile: %v", chiave, percorso, err)
			}
			out[strings.ToUpper(chiave)+" "+percorso] = op
		}
	}
	return out
}

// campoJSON è un campo di una struttura come esce dal serializzatore.
type campoJSON struct {
	nome string
	// facoltativo è vero quando il campo sparisce dalla risposta se è vuoto
	// (`omitempty`).
	facoltativo bool
	// nullabile è vero quando il campo arriva al client come `null` invece di
	// sparire: un puntatore senza `omitempty`.
	nullabile bool
}

func campiJSON(tipo reflect.Type) []campoJSON {
	var campi []campoJSON
	for i := range tipo.NumField() {
		campo := tipo.Field(i)
		if !campo.IsExported() {
			continue
		}
		tag := campo.Tag.Get("json")
		if tag == "-" {
			continue
		}
		nome, opzioni, _ := strings.Cut(tag, ",")
		if nome == "" {
			nome = campo.Name
		}
		facoltativo := slices.Contains(strings.Split(opzioni, ","), "omitempty")
		campi = append(campi, campoJSON{
			nome:        nome,
			facoltativo: facoltativo,
			nullabile:   !facoltativo && campo.Type.Kind() == reflect.Pointer,
		})
	}
	return campi
}

// tipiDichiarati legge il `type` di uno schema, che in OpenAPI 3.1 può essere
// una stringa o un elenco (`[string, "null"]`).
func tipiDichiarati(schema schemaDoc) []string {
	switch schema.Type.Kind {
	case yaml.ScalarNode:
		return []string{schema.Type.Value}
	case yaml.SequenceNode:
		var tipi []string
		for _, nodo := range schema.Type.Content {
			tipi = append(tipi, nodo.Value)
		}
		return tipi
	default:
		return nil
	}
}

// sorgentiDelPackage legge i file .go di una directory, esclusi i test.
func sorgentiDelPackage(t *testing.T, dir string) []*ast.File {
	t.Helper()
	voci, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("directory %s non leggibile: %v", dir, err)
	}
	fset := token.NewFileSet()
	var file []*ast.File
	for _, voce := range voci {
		nome := voce.Name()
		if voce.IsDir() || !strings.HasSuffix(nome, ".go") || strings.HasSuffix(nome, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, filepath.Join(dir, nome), nil, 0)
		if err != nil {
			t.Fatalf("%s non analizzabile: %v", nome, err)
		}
		file = append(file, parsed)
	}
	if len(file) == 0 {
		t.Fatalf("nessun sorgente in %s", dir)
	}
	return file
}

// costantiDelTipo elenca i valori delle costanti di un tipo stringa dichiarato
// in un altro package.
//
// Serve perché internal/jobs non espone un elenco dei propri limiti, e scriverne
// uno qui sarebbe la copia che questo file esiste per non avere.
func costantiDelTipo(t *testing.T, dir, tipo string) []string {
	t.Helper()
	var valori []string
	for _, file := range sorgentiDelPackage(t, dir) {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				ident, ok := vs.Type.(*ast.Ident)
				if !ok || ident.Name != tipo {
					continue
				}
				for _, valore := range vs.Values {
					if lit, ok := valore.(*ast.BasicLit); ok {
						valori = append(valori, valoreStringa(t, lit))
					}
				}
			}
		}
	}
	if len(valori) == 0 {
		t.Fatalf("nessuna costante di tipo %s trovata in %s", tipo, dir)
	}
	return valori
}

func valoreStringa(t *testing.T, lit *ast.BasicLit) string {
	t.Helper()
	valore, err := strconv.Unquote(lit.Value)
	if err != nil {
		t.Fatalf("letterale %s non leggibile: %v", lit.Value, err)
	}
	return valore
}

func stringheDi[T ~string](valori []T) []string {
	out := make([]string, 0, len(valori))
	for _, valore := range valori {
		out = append(out, string(valore))
	}
	return out
}

func chiaviOrdinate[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for chiave := range m {
		out = append(out, chiave)
	}
	sort.Strings(out)
	return out
}

func chiavi(m map[string]bool) []string { return chiaviOrdinate(m) }

// confrontaInsiemi segnala le differenze fra due elenchi, con un messaggio che
// dice **cosa succede** a chi usa l'API quando la differenza c'è. Un test che si
// limita a stampare due elenchi diversi lascia a chi lo legge il lavoro di
// capire perché gli interessa.
func confrontaInsiemi(t *testing.T, cosa string, nelCodice, nelDocumento []string, soloNelCodice, soloNelDocumento string) {
	t.Helper()

	codice := map[string]bool{}
	for _, v := range nelCodice {
		codice[v] = true
	}
	documento := map[string]bool{}
	for _, v := range nelDocumento {
		documento[v] = true
	}

	for _, v := range chiaviOrdinate(codice) {
		if !documento[v] {
			t.Errorf("%s — %q: %s", cosa, v, soloNelCodice)
		}
	}
	for _, v := range chiaviOrdinate(documento) {
		if !codice[v] {
			t.Errorf("%s — %q: %s", cosa, v, soloNelDocumento)
		}
	}
}
