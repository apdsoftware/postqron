package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Il contratto JSON dei job ricalca lo schema di `cron.yaml` (SPEC §9):
// `schedule` ed `every`, `request` con URL, metodo, header e corpo, `timeout`,
// `retries`, `alerts`. È deliberato. Le stesse persone scrivono i due, e un job
// creato dall'API e uno sincronizzato da un repository sono la stessa riga della
// stessa tabella: due vocabolari per la stessa cosa costringerebbero a tradurre
// mentalmente ogni volta, e la traduzione è il posto in cui nascono i difetti.
//
// Le durate sono stringhe (`"30s"`, `"5m"`, `"1h"`) e non numeri, per la stessa
// ragione: `every: 10s` in un file YAML e `"every": "10s"` in JSON si leggono
// allo stesso modo. Il costo è che il client fa un `parse` in più; il beneficio
// è che ciò che legge lo può rimandare indietro senza conversioni.

// JobResponse è un job come lo vede il client.
type JobResponse struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Schedule     *string  `json:"schedule"`
	Every        *string  `json:"every"`
	Timezone     string   `json:"timezone"`
	Environments []string `json:"environments"`

	Request TargetResponse  `json:"request"`
	Timeout string          `json:"timeout"`
	Retries RetriesResponse `json:"retries"`
	Alerts  AlertsResponse  `json:"alerts"`

	Enabled bool `json:"enabled"`

	// RepositoryID è valorizzato per i job che vengono da un `cron.yaml` (R13).
	// Il client ne ha bisogno per sapere in anticipo che le modifiche vanno
	// fatte nel file, invece di scoprirlo da un 409 dopo aver compilato un form.
	RepositoryID string `json:"repository_id,omitempty"`

	NextRunAt  *time.Time `json:"next_run_at"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// TargetResponse è il bersaglio HTTP del job (SPEC §10).
type TargetResponse struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    *string           `json:"body"`
}

// RetriesResponse è la politica di retry (R5).
type RetriesResponse struct {
	Max     int    `json:"max"`
	Backoff string `json:"backoff"`
}

// AlertsResponse sono i canali di avviso (R21, R29).
type AlertsResponse struct {
	OnFailure []string `json:"on_failure"`
}

// ExecutionResponse è un tentativo come lo vede il client (R6).
//
// Non ha un identificativo sintetico perché la riga non ne ha uno: la chiave è
// la quaterna naturale (0006), e inventarne uno qui produrrebbe un valore che
// non si può usare per rileggere niente.
type ExecutionResponse struct {
	JobID        string    `json:"job_id"`
	ScheduledFor time.Time `json:"scheduled_for"`
	Environment  string    `json:"environment"`
	Attempt      int       `json:"attempt"`

	Status      string `json:"status"`
	TriggeredBy string `json:"triggered_by"`

	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	DurationMS *int       `json:"duration_ms"`

	ResponseStatus  *int   `json:"response_status"`
	ResponseExcerpt string `json:"response_excerpt,omitempty"`
	Error           string `json:"error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

// PageResponse descrive la pagina restituita.
//
// `next_cursor` nullo significa «non c'è altro»: è l'unica condizione di fine, e
// non va dedotta dal numero di righe ricevute — una pagina piena può benissimo
// essere l'ultima.
type PageResponse struct {
	Limit      int     `json:"limit"`
	NextCursor *string `json:"next_cursor"`
}

// JobListResponse è la pagina dell'elenco dei job.
type JobListResponse struct {
	Jobs []JobResponse `json:"jobs"`
	Page PageResponse  `json:"page"`
}

// ExecutionListResponse è la pagina del registro delle esecuzioni.
type ExecutionListResponse struct {
	Executions []ExecutionResponse `json:"executions"`
	Page       PageResponse        `json:"page"`
}

func pageResponse(limit int, cursor string) PageResponse {
	page := PageResponse{Limit: limit}
	if cursor != "" {
		page.NextCursor = &cursor
	}
	return page
}

func jobResponse(job jobs.Job) JobResponse {
	out := JobResponse{
		ID:           job.ID,
		Name:         job.Name,
		Description:  job.Description,
		Timezone:     job.Timezone,
		Environments: stringsOf(job.Environments),
		Request: TargetResponse{
			URL:     job.URL,
			Method:  string(job.Method),
			Headers: job.Headers,
		},
		Timeout:      jobs.FormatDuration(job.Timeout),
		Retries:      RetriesResponse{Max: job.MaxRetries, Backoff: string(job.RetryBackoff)},
		Alerts:       AlertsResponse{OnFailure: stringsOf(job.AlertOnFailure)},
		Enabled:      job.Enabled,
		RepositoryID: job.RepositoryID,
		NextRunAt:    job.NextRunAt,
		ArchivedAt:   job.ArchivedAt,
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    job.UpdatedAt,
	}
	if out.Request.Headers == nil {
		out.Request.Headers = map[string]string{}
	}
	if job.Body != "" {
		body := job.Body
		out.Request.Body = &body
	}
	// Esattamente uno dei due è valorizzato, e l'altro è esplicitamente `null`
	// invece che assente: il client non deve distinguere «campo mancante» da
	// «modalità non in uso».
	if job.Schedule != "" {
		expression := job.Schedule
		out.Schedule = &expression
	}
	if job.Every != 0 {
		every := jobs.FormatDuration(job.Every)
		out.Every = &every
	}
	return out
}

func jobResponses(items []jobs.Job) []JobResponse {
	out := make([]JobResponse, 0, len(items))
	for _, job := range items {
		out = append(out, jobResponse(job))
	}
	return out
}

func executionResponse(exec jobs.Execution) ExecutionResponse {
	return ExecutionResponse{
		JobID:           exec.JobID,
		ScheduledFor:    exec.ScheduledFor,
		Environment:     string(exec.Environment),
		Attempt:         exec.Attempt,
		Status:          string(exec.Status),
		TriggeredBy:     string(exec.TriggeredBy),
		StartedAt:       exec.StartedAt,
		FinishedAt:      exec.FinishedAt,
		DurationMS:      exec.DurationMS,
		ResponseStatus:  exec.ResponseStatus,
		ResponseExcerpt: exec.ResponseExcerpt,
		Error:           exec.Error,
		CreatedAt:       exec.CreatedAt,
	}
}

func executionResponses(items []jobs.Execution) []ExecutionResponse {
	out := make([]ExecutionResponse, 0, len(items))
	for _, exec := range items {
		out = append(out, executionResponse(exec))
	}
	return out
}

func stringsOf[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

// ------------------------------------------------------------------ richieste

// JobPayload è il corpo di creazione e modifica di un job.
//
// Ogni campo è un [optional]: la creazione parte dai valori predefiniti e ci
// sovrappone quelli presenti, la modifica parte dal job esistente e fa lo
// stesso. Un unico tipo per le due operazioni, perché la semantica «ciò che non
// mandi non cambia» è la stessa — e perché due strutture parallele divergono al
// primo campo aggiunto a una sola delle due.
type JobPayload struct {
	Name        optional[string]   `json:"name"`
	Description optional[string]   `json:"description"`
	Schedule    optional[string]   `json:"schedule"`
	Every       optional[duration] `json:"every"`
	Timezone    optional[string]   `json:"timezone"`

	Environments optional[[]string] `json:"environments"`

	Request optional[targetPayload] `json:"request"`

	Timeout optional[duration]       `json:"timeout"`
	Retries optional[retriesPayload] `json:"retries"`
	Alerts  optional[alertsPayload]  `json:"alerts"`
	Enabled optional[bool]           `json:"enabled"`
}

type targetPayload struct {
	URL     optional[string]            `json:"url"`
	Method  optional[string]            `json:"method"`
	Headers optional[map[string]string] `json:"headers"`
	Body    optional[string]            `json:"body"`
}

type retriesPayload struct {
	Max     optional[int]    `json:"max"`
	Backoff optional[string] `json:"backoff"`
}

type alertsPayload struct {
	OnFailure optional[[]string] `json:"on_failure"`
}

// TriggerPayload è il corpo del trigger manuale. L'unico campo è facoltativo:
// serve solo ai job che vivono in più ambienti.
type TriggerPayload struct {
	Environment string `json:"environment"`
}

// applyTo sovrappone il corpo a un job.
func (p JobPayload) applyTo(job *jobs.Job) {
	p.patch().ApplyTo(job)
}

// patch traduce il corpo in una modifica del dominio.
//
// Non valida niente: i valori che non appartengono a un dominio chiuso — un
// metodo che non è un metodo, un canale di avviso che non esiste — passano di
// qui così come sono e vengono rifiutati da [jobs.Job.Validate] insieme a tutti
// gli altri. Rifiutarli qui produrrebbe un errore per volta e con un vocabolario
// diverso, mentre il client ne vuole l'elenco completo in un colpo solo.
func (p JobPayload) patch() jobs.Patch {
	var patch jobs.Patch

	patch.Name = p.Name.pointer()
	patch.Description = p.Description.pointer()
	patch.Timezone = p.Timezone.pointer()
	patch.Enabled = p.Enabled.pointer()

	if p.Schedule.set {
		// `"schedule": null` azzera la modalità cron; il campo resta «toccato»,
		// così il Service sa che l'utente ha voluto cambiare modalità.
		expression := p.Schedule.value
		patch.Schedule = &expression
	}
	if p.Every.set {
		every := time.Duration(p.Every.value)
		patch.Every = &every
	}

	if p.Environments.set {
		envs := make([]jobs.Environment, 0, len(p.Environments.value))
		for _, value := range p.Environments.value {
			envs = append(envs, jobs.Environment(value))
		}
		patch.Environments = &envs
	}

	if p.Request.set {
		target := p.Request.value
		patch.URL = target.URL.pointer()
		patch.Body = target.Body.pointer()
		if target.Method.set {
			// Il metodo si normalizza in maiuscolo prima del confronto: `post` è
			// un refuso ovvio, e rifiutarlo non protegge nessuno.
			method := jobs.Method(strings.ToUpper(strings.TrimSpace(target.Method.value)))
			patch.Method = &method
		}
		if target.Headers.set {
			headers := target.Headers.value
			if headers == nil {
				headers = map[string]string{}
			}
			patch.Headers = &headers
		}
	}

	if p.Timeout.set {
		timeout := time.Duration(p.Timeout.value)
		patch.Timeout = &timeout
	}

	if p.Retries.set {
		retries := p.Retries.value
		patch.MaxRetries = retries.Max.pointer()
		if retries.Backoff.set {
			backoff := jobs.Backoff(strings.TrimSpace(retries.Backoff.value))
			patch.RetryBackoff = &backoff
		}
	}

	if p.Alerts.set && p.Alerts.value.OnFailure.set {
		channels := make([]jobs.AlertChannel, 0, len(p.Alerts.value.OnFailure.value))
		for _, value := range p.Alerts.value.OnFailure.value {
			channels = append(channels, jobs.AlertChannel(value))
		}
		patch.AlertOnFailure = &channels
	}

	return patch
}

// ------------------------------------------------------------------- optional

// optional distingue «campo assente» da «campo a null» da «campo valorizzato».
//
// Serve perché un PATCH deve poter lasciare intatto ciò che non riceve: con un
// puntatore semplice, `null` e assente decodificano entrambi a nil, e l'unico
// modo di distinguerli sarebbe rileggere il JSON grezzo a mano.
type optional[T any] struct {
	set   bool
	value T
}

func (o *optional[T]) UnmarshalJSON(data []byte) error {
	o.set = true
	if string(data) == "null" {
		// `null` resta «toccato» con il valore vuoto: per una descrizione o un
		// corpo è ciò che l'utente intende quando li cancella, e per `schedule`
		// o `every` è il modo di dismettere una modalità di schedulazione.
		return nil
	}
	// Il decoder è ricostruito con DisallowUnknownFields perché quello del
	// chiamante non arriva fin qui: senza, un `requst` scritto male dentro un
	// oggetto annidato passerebbe inosservato — che è esattamente il difetto che
	// decodeJSON evita al livello di sopra.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(&o.value)
}

// pointer restituisce il valore da assegnare, o nil se il campo non c'era.
// Un `null` esplicito vale come «riporta al valore vuoto»: per una descrizione
// o un corpo è ciò che l'utente intende quando lo cancella.
func (o optional[T]) pointer() *T {
	if !o.set {
		return nil
	}
	value := o.value
	return &value
}

// duration è una durata nella forma di `cron.yaml`: `1s`, `10s`, `5m`, `1h`.
type duration time.Duration

func (d *duration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("durata attesa come stringa (`10s`, `5m`, `1h`), non %s", strings.TrimSpace(string(data)))
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("durata %q non leggibile: usa una forma come `10s`, `5m`, `1h`", raw)
	}
	*d = duration(parsed)
	return nil
}
