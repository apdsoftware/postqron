package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// maxJobRequestBody è il tetto al corpo delle rotte dei job.
//
// È più alto di [maxRequestBody] perché un job porta con sé il corpo della
// richiesta che invierà (fino a [jobs.MaxBodyLength]) e i suoi header: ottomila
// byte, che bastano e avanzano a un login, qui rifiuterebbero job legittimi. Il
// tetto resta, perché senza il corpo di una richiesta è memoria che il chiamante
// decide di far allocare.
const maxJobRequestBody = 64 << 10

// Il contratto delle rotte di R8, in breve. La specifica OpenAPI è la issue
// #465; qui c'è ciò che serve a leggerne il codice.
//
//	GET    /jobs                     elenco paginato          200
//	POST   /jobs                     creazione                201 + Location
//	GET    /jobs/{id}                lettura                  200
//	PATCH  /jobs/{id}                modifica parziale        200
//	DELETE /jobs/{id}                eliminazione             204
//	GET    /jobs/{id}/executions     registro paginato        200
//	POST   /jobs/{id}/executions     trigger manuale          202
//
// Parametri di elenco: `limit`, `cursor`, e poi `enabled`, `environment`,
// `include_archived` per i job; `status`, `trigger`, `environment`, `since`,
// `until` per le esecuzioni. La paginazione è a cursore in entrambi i casi, con
// la stessa busta `page`: su un job a un secondo il registro cresce di 86.400
// righe al giorno, e un `offset` costringerebbe il database a scartarne
// migliaia per servirne cinquanta.
//
// I codici di errore sono stabili e sono ciò su cui il client fa branching
// (R53). `message` è testo per una persona e può cambiare; `code` no.
//
//	400 validation_failed           campi non validi, con `details` per campo
//	400 invalid_request             corpo illeggibile o campo sconosciuto
//	400 invalid_cursor              cursore di paginazione non valido
//	400 empty_patch                 modifica senza campi
//	401 unauthenticated             sessione assente o scaduta
//	401 invalid_api_key             chiave API assente, revocata o scaduta (R9)
//	403 insufficient_scope          la chiave non ha il permesso richiesto (R9)
//	403 plan_limit_jobs             tetto al numero di job (R15)
//	403 plan_limit_resolution       risoluzione minima del piano (R15, R22)
//	403 plan_limit_environments     ambienti non inclusi nel piano (R23)
//	404 job_not_found               inesistente, oppure di un altro utente
//	409 job_name_taken              il nome è l'identità del job
//	409 job_managed_by_repository   definito in un `cron.yaml` (R13)
//	409 job_archived                non più presente nel file d'origine
//	409 job_disabled                trigger manuale su un job in pausa
//	409 execution_already_exists    occorrenza già registrata (R4)
//	413 body_too_large              corpo oltre maxJobRequestBody
//	429 plan_limit_manual_trigger   con `Retry-After` e `retry_after`
//	503 executions_unavailable      partizione giornaliera mancante (0006)
//
// jobsAPI raccoglie le rotte dei job e delle esecuzioni (R8).
type jobsAPI struct {
	*guard
	svc *jobs.Service
	log *slog.Logger
}

func newJobsAPI(guard *guard, logger *slog.Logger, svc *jobs.Service) *jobsAPI {
	return &jobsAPI{guard: guard, svc: svc, log: logger}
}

// routes registra le rotte con lo scope che ciascuna richiede (R9).
//
// Lo scope sta qui, accanto al metodo e al percorso, e non dentro l'handler:
// così l'elenco dei permessi dell'API si legge in dodici righe, e una rotta
// registrata senza scope si vede a occhio. L'applicazione è in guard.scoped
// (identity.go), che è l'unico punto da cui passano tutte queste rotte.
//
// Le sessioni passano da tutte: gli scope limitano le deleghe, non il titolare.
func (a *jobsAPI) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /jobs", a.scoped(apikeys.ScopeJobsRead, a.list))
	mux.HandleFunc("POST /jobs", a.scoped(apikeys.ScopeJobsWrite, a.create))
	mux.HandleFunc("GET /jobs/{id}", a.scoped(apikeys.ScopeJobsRead, a.get))
	mux.HandleFunc("PATCH /jobs/{id}", a.scoped(apikeys.ScopeJobsWrite, a.update))
	mux.HandleFunc("DELETE /jobs/{id}", a.scoped(apikeys.ScopeJobsWrite, a.delete))

	// Le esecuzioni sono una sottorisorsa del job, e il trigger manuale ne crea
	// una: `POST` sulla stessa collezione che `GET` elenca. Una rotta
	// `/jobs/{id}/trigger` sarebbe un verbo travestito da risorsa, e nasconderebbe
	// che ciò che si ottiene è esattamente una riga del registro — con la stessa
	// forma, lo stesso identificativo naturale e lo stesso tetto.
	//
	// Il trigger ha uno scope proprio, distinto da `jobs:write`: cambiare la
	// definizione di un job e far partire adesso una chiamata verso l'esterno sono
	// due poteri diversi, e una chiave da cruscotto vuole il secondo senza il primo.
	mux.HandleFunc("GET /jobs/{id}/executions", a.scoped(apikeys.ScopeExecutionsRead, a.listExecutions))
	mux.HandleFunc("POST /jobs/{id}/executions", a.scoped(apikeys.ScopeExecutionsTrigger, a.trigger))
}

// ------------------------------------------------------------------ handler

func (a *jobsAPI) list(w http.ResponseWriter, r *http.Request, identity Identity) {
	query := r.URL.Query()

	opts := jobs.ListOptions{
		Environment: jobs.Environment(query.Get("environment")),
		Cursor:      query.Get("cursor"),
	}

	invalid := &queryErrors{}
	opts.Limit = invalid.limit(query.Get("limit"))
	opts.Enabled = invalid.optionalBool("enabled", query.Get("enabled"))
	// Anche questo passa dal controllo di forma: un `include_archived=1` letto
	// come «no» darebbe una risposta plausibile e sbagliata, e il client se ne
	// accorgerebbe solo notando che manca un job che sa di avere.
	if archived := invalid.optionalBool("include_archived", query.Get("include_archived")); archived != nil {
		opts.IncludeArchived = *archived
	}
	if invalid.fail(w, r, a.log) {
		return
	}

	page, err := a.svc.List(r.Context(), identity.UserID(), opts)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, JobListResponse{
		Jobs: jobResponses(page.Items),
		Page: pageResponse(page.Limit, page.NextCursor),
	})
}

func (a *jobsAPI) create(w http.ResponseWriter, r *http.Request, identity Identity) {
	var body JobPayload
	if !a.decode(w, r, &body) {
		return
	}

	// La creazione parte dai valori predefiniti della migrazione 0005 e ci
	// sovrappone quello che il client ha mandato: così la risposta contiene i
	// default effettivi, invece di zeri che il database sostituirà in silenzio.
	job := jobs.NewJob()
	body.applyTo(&job)

	created, err := a.svc.Create(r.Context(), identity.UserID(), job)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/jobs/"+created.ID)
	writeJSON(w, r, a.log, http.StatusCreated, jobResponse(created))
}

func (a *jobsAPI) get(w http.ResponseWriter, r *http.Request, identity Identity) {
	job, err := a.svc.Get(r.Context(), identity.UserID(), r.PathValue("id"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, jobResponse(job))
}

func (a *jobsAPI) update(w http.ResponseWriter, r *http.Request, identity Identity) {
	var body JobPayload
	if !a.decode(w, r, &body) {
		return
	}
	patch := body.patch()
	if patch.Empty() {
		writeError(w, r, a.log, http.StatusBadRequest, "empty_patch",
			"La richiesta non contiene nessun campo da modificare.")
		return
	}

	updated, err := a.svc.Update(r.Context(), identity.UserID(), r.PathValue("id"), patch)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, jobResponse(updated))
}

func (a *jobsAPI) delete(w http.ResponseWriter, r *http.Request, identity Identity) {
	if err := a.svc.Delete(r.Context(), identity.UserID(), r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *jobsAPI) listExecutions(w http.ResponseWriter, r *http.Request, identity Identity) {
	query := r.URL.Query()

	opts := jobs.ExecutionOptions{
		Environment: jobs.Environment(query.Get("environment")),
		Cursor:      query.Get("cursor"),
	}

	invalid := &queryErrors{}
	opts.Limit = invalid.limit(query.Get("limit"))
	opts.Since = invalid.timestamp("since", query.Get("since"))
	opts.Until = invalid.timestamp("until", query.Get("until"))
	for _, value := range splitList(query["status"]) {
		opts.Status = append(opts.Status, jobs.ExecutionStatus(value))
	}
	for _, value := range splitList(query["trigger"]) {
		opts.TriggeredBy = append(opts.TriggeredBy, jobs.ExecutionTrigger(value))
	}
	if invalid.fail(w, r, a.log) {
		return
	}

	page, err := a.svc.Executions(r.Context(), identity.UserID(), r.PathValue("id"), opts)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, ExecutionListResponse{
		Executions: executionResponses(page.Items),
		Page:       pageResponse(page.Limit, page.NextCursor),
	})
}

// trigger registra un'esecuzione manuale (R8).
//
// Risponde 202 e non 201: la riga è stata creata, ma l'esecuzione non è
// avvenuta — la farà il motore. Un 201 prometterebbe che il target è già stato
// chiamato, e il client che ne leggesse subito l'esito non troverebbe niente.
func (a *jobsAPI) trigger(w http.ResponseWriter, r *http.Request, identity Identity) {
	var body TriggerPayload
	if !a.decodeOptional(w, r, &body) {
		return
	}

	exec, err := a.svc.Trigger(r.Context(), identity.UserID(), r.PathValue("id"),
		jobs.Environment(body.Environment))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusAccepted, executionResponse(exec))
}

// ------------------------------------------------------------------- errori

// fail traduce un errore del dominio in una risposta HTTP.
//
// I codici sono stabili e pensati per il branching applicativo (R53): un client
// che riceve `plan_limit_jobs` porta alla pagina dei piani, uno che riceve
// `validation_failed` evidenzia i campi, uno che riceve
// `job_managed_by_repository` rimanda al `cron.yaml`. Nessuna di queste
// decisioni si può prendere leggendo un messaggio in italiano.
func (a *jobsAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	if invalid, ok := jobs.AsValidation(err); ok {
		writeErrorDetail(w, r, a.log, http.StatusBadRequest, ErrorDetail{
			Code:    "validation_failed",
			Message: "La richiesta contiene campi non validi.",
			Details: fieldErrors(invalid.Fields),
		})
		return
	}

	if limit, ok := jobs.AsPlanLimit(err); ok {
		a.failPlanLimit(w, r, limit)
		return
	}

	switch {
	case errors.Is(err, jobs.ErrNotFound):
		// «Non esiste» e «non è tuo» rispondono allo stesso modo: distinguerli
		// direbbe a chiunque se un identificativo altrui è vivo.
		writeError(w, r, a.log, http.StatusNotFound, "job_not_found", "Job non trovato.")

	case errors.Is(err, jobs.ErrNameTaken):
		writeError(w, r, a.log, http.StatusConflict, "job_name_taken",
			"Hai già un job con questo nome: il nome è l'identità stabile del job.")

	case errors.Is(err, jobs.ErrManaged):
		writeError(w, r, a.log, http.StatusConflict, "job_managed_by_repository",
			"Questo job è definito nel `cron.yaml` del tuo repository: modificalo lì, la sincronizzazione riporterebbe indietro qualunque modifica fatta da qui. Puoi solo metterlo in pausa.")

	case errors.Is(err, jobs.ErrArchived):
		writeError(w, r, a.log, http.StatusConflict, "job_archived",
			"Il job è archiviato: non è più presente nel `cron.yaml` da cui proveniva.")

	case errors.Is(err, jobs.ErrDisabled):
		writeError(w, r, a.log, http.StatusConflict, "job_disabled",
			"Il job è in pausa: riattivalo prima di eseguirlo.")

	case errors.Is(err, jobs.ErrExecutionExists):
		writeError(w, r, a.log, http.StatusConflict, "execution_already_exists", err.Error())

	case errors.Is(err, jobs.ErrInvalidCursor):
		writeError(w, r, a.log, http.StatusBadRequest, "invalid_cursor",
			"Il cursore di paginazione non è valido: ricomincia dalla prima pagina.")

	case errors.Is(err, jobs.ErrPartitionMissing):
		// Non è colpa del client: è la manutenzione periodica delle partizioni
		// di `job_executions` che non ha girato (0006). Un 500 la farebbe
		// sembrare un difetto del codice; un 503 con Retry-After dice che
		// riprovare ha senso, e il log dice a noi che c'è da intervenire.
		a.log.ErrorContext(r.Context(), "partizione delle esecuzioni mancante: eseguire job_executions_ensure_partitions()",
			slog.String("path", r.URL.Path), slog.Any("error", err))
		w.Header().Set("Retry-After", "60")
		writeError(w, r, a.log, http.StatusServiceUnavailable, "executions_unavailable",
			"Registro delle esecuzioni temporaneamente non disponibile. Riprova fra poco.")

	case writeAuthError(w, r, a.log, err):
		// Errore dell'autenticazione arrivato fin qui.

	default:
		a.log.ErrorContext(r.Context(), "errore interno nelle rotte dei job",
			slog.String("path", r.URL.Path), slog.Any("error", err))
		writeError(w, r, a.log, http.StatusInternalServerError, "internal_error",
			"Errore interno. Riprova più tardi.")
	}
}

func (a *jobsAPI) failPlanLimit(w http.ResponseWriter, r *http.Request, limit *jobs.PlanLimitError) {
	detail := ErrorDetail{
		Code:    "plan_limit_" + string(limit.Limit),
		Message: limit.Error(),
		Limit:   string(limit.Limit),
		Plan:    limit.Plan,
	}
	if limit.Field != "" {
		detail.Details = []FieldErrorBody{{Field: limit.Field, Code: detail.Code, Message: limit.Error()}}
	}

	// 429 per i limiti di frequenza, 403 per quelli di capienza. La differenza
	// non è formale: sul primo il client riprova, sul secondo deve cambiare
	// qualcosa — il piano o la richiesta.
	status := http.StatusForbidden
	if limit.RetryAfter > 0 {
		status = http.StatusTooManyRequests
		seconds := int(limit.RetryAfter.Round(time.Second).Seconds())
		if seconds < 1 {
			seconds = 1
		}
		detail.RetryAfter = seconds
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeErrorDetail(w, r, a.log, status, detail)
}

// ------------------------------------------------------------------ supporto

func (a *jobsAPI) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	return a.handleDecode(w, r, decodeJSONLimit(w, r, dst, maxJobRequestBody))
}

// decodeOptional accetta anche una richiesta senza corpo.
//
// Serve al trigger manuale, il cui unico campo è facoltativo: pretendere
// `Content-Type: application/json` e un `{}` per dire «esegui adesso» sarebbe
// attrito senza contropartita. Il corpo assente non apre una via al CSRF via
// form, perché un form del browser un Content-Type lo manda sempre — e uno dei
// tre che [decodeJSONLimit] rifiuta.
func (a *jobsAPI) decodeOptional(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.ContentLength == 0 {
		return true
	}
	return a.decode(w, r, dst)
}

func (a *jobsAPI) handleDecode(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return true
	}
	status, code := http.StatusBadRequest, "invalid_request"
	if errors.Is(err, errBodyTooLarge) {
		status, code = http.StatusRequestEntityTooLarge, "body_too_large"
	}
	writeError(w, r, a.log, status, code, err.Error())
	return false
}

// queryErrors raccoglie i parametri di query malformati.
//
// Un `limit=abc` silenziosamente ignorato produce una pagina di dimensione
// diversa da quella chiesta, e il client se ne accorge solo contando le righe.
type queryErrors struct {
	fields []jobs.FieldError
}

func (q *queryErrors) add(field, code, message string) {
	q.fields = append(q.fields, jobs.FieldError{Field: field, Code: code, Message: message})
}

func (q *queryErrors) limit(raw string) int {
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		q.add("limit", "invalid_format", "`limit` dev'essere un intero positivo.")
		return 0
	}
	// Oltre il massimo non è un errore: è una richiesta ridimensionata, e il
	// client se ne accorge dal `page.limit` della risposta.
	return value
}

func (q *queryErrors) optionalBool(field, raw string) *bool {
	switch raw {
	case "":
		return nil
	case "true":
		value := true
		return &value
	case "false":
		value := false
		return &value
	default:
		q.add(field, "invalid_format", "`"+field+"` accetta `true` o `false`.")
		return nil
	}
}

func (q *queryErrors) timestamp(field, raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		q.add(field, "invalid_format", "`"+field+"` dev'essere un istante in formato RFC 3339.")
		return time.Time{}
	}
	return value.UTC()
}

func (q *queryErrors) fail(w http.ResponseWriter, r *http.Request, logger *slog.Logger) bool {
	if len(q.fields) == 0 {
		return false
	}
	writeErrorDetail(w, r, logger, http.StatusBadRequest, ErrorDetail{
		Code:    "validation_failed",
		Message: "La richiesta contiene parametri non validi.",
		Details: fieldErrors(q.fields),
	})
	return true
}

// splitList accetta sia `?status=failed&status=timed_out` sia
// `?status=failed,timed_out`: la prima è la forma che i client HTTP generano da
// soli, la seconda quella che si scrive a mano.
func splitList(values []string) []string {
	var out []string
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

func fieldErrors(fields []jobs.FieldError) []FieldErrorBody {
	out := make([]FieldErrorBody, 0, len(fields))
	for _, field := range fields {
		out = append(out, FieldErrorBody{Field: field.Field, Code: field.Code, Message: field.Message})
	}
	return out
}
