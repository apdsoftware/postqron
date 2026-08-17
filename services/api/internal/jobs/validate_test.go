package jobs_test

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// validJob è un job che passa tutti i controlli. I test lo modificano in un
// punto solo: così ciò che il test verifica è esattamente ciò che ha cambiato.
func validJob() jobs.Job {
	job := jobs.NewJob()
	job.Name = "daily-digest"
	job.Schedule = "0 9 * * *"
	job.Timezone = "Europe/Rome"
	job.URL = "https://api.example.com/tasks/digest"
	return job
}

// codes estrae i codici degli errori di campo, per campo.
func codes(t *testing.T, err error) map[string]string {
	t.Helper()
	invalid, ok := jobs.AsValidation(err)
	if !ok {
		t.Fatalf("atteso un errore di validazione, ottenuto %v", err)
	}
	out := map[string]string{}
	for _, field := range invalid.Fields {
		out[field.Field] = field.Code
	}
	return out
}

// validate normalizza e valida in loco: Validate ha un ricevitore puntatore
// perché normalizza il job prima di controllarlo, ed è quella forma normalizzata
// che finisce nel database.
func validate(t *testing.T, job *jobs.Job, plan jobs.Plan) error {
	t.Helper()
	return job.Validate(t.Context(), plan, nil)
}

func TestJobValidoPassa(t *testing.T) {
	if err := validate(t, ptr(validJob()), jobs.FreePlan); err != nil {
		t.Fatalf("un job valido è stato rifiutato: %v", err)
	}
}

// --------------------------------------------------------------- schedulazione

func TestSchedulazioneRifiutata(t *testing.T) {
	cases := []struct {
		nome  string
		tune  func(*jobs.Job)
		campo string
		code  string
	}{
		{
			// È il vincolo `jobs_schedule_xor_every_check` della 0005. Se
			// arrivasse al database sarebbe un 500; qui è un 400 con la ragione.
			nome:  "entrambe le modalità",
			tune:  func(j *jobs.Job) { j.Every = 30 * time.Second },
			campo: "schedule", code: "schedule_conflict",
		},
		{
			nome:  "nessuna modalità",
			tune:  func(j *jobs.Job) { j.Schedule = "" },
			campo: "schedule", code: "schedule_required",
		},
		{
			nome:  "espressione cron incompleta",
			tune:  func(j *jobs.Job) { j.Schedule = "0 9 * *" },
			campo: "schedule", code: "invalid_schedule",
		},
		{
			nome:  "espressione a sei campi",
			tune:  func(j *jobs.Job) { j.Schedule = "0 0 9 * * *" },
			campo: "schedule", code: "invalid_schedule",
		},
		{
			nome:  "campo cron fuori intervallo",
			tune:  func(j *jobs.Job) { j.Schedule = "0 99 * * *" },
			campo: "schedule", code: "invalid_schedule",
		},
		{
			nome:  "abbreviazione @daily",
			tune:  func(j *jobs.Job) { j.Schedule = "@daily" },
			campo: "schedule", code: "invalid_schedule",
		},
		{
			nome:  "fuso sconosciuto",
			tune:  func(j *jobs.Job) { j.Timezone = "Europe/Atlantide" },
			campo: "timezone", code: "invalid_schedule",
		},
		{
			// `Local` dipende dall'ambiente del processo: uno scheduler che
			// cambia comportamento con la macchina non è uno scheduler.
			nome:  "fuso Local",
			tune:  func(j *jobs.Job) { j.Timezone = "Local" },
			campo: "timezone", code: "invalid_schedule",
		},
		{
			nome: "intervallo non intero di secondi",
			tune: func(j *jobs.Job) {
				j.Schedule = ""
				j.Every = 1500 * time.Millisecond
			},
			campo: "every", code: "invalid_interval",
		},
		{
			nome: "intervallo negativo",
			tune: func(j *jobs.Job) {
				j.Schedule = ""
				j.Every = -10 * time.Second
			},
			campo: "every", code: "invalid_interval",
		},
	}

	for _, tc := range cases {
		t.Run(tc.nome, func(t *testing.T) {
			job := validJob()
			tc.tune(&job)

			err := validate(t, &job, jobs.FreePlan)
			if got := codes(t, err)[tc.campo]; got != tc.code {
				t.Fatalf("codice sul campo %q = %q, atteso %q (errore: %v)", tc.campo, got, tc.code, err)
			}
		})
	}
}

// TestIntervalloValidoPassaSuUnPianoCheLoConsente separa i due rifiuti: la forma
// dell'intervallo e il limite di piano sono cose diverse e devono restarlo.
func TestIntervalloValidoPassaSuUnPianoCheLoConsente(t *testing.T) {
	job := validJob()
	job.Schedule = ""
	job.Every = 10 * time.Second

	if err := validate(t, &job, planPro()); err != nil {
		t.Fatalf("`every: 10s` rifiutato sul piano Pro: %v", err)
	}
}

// TestNormalizzazioneDellEspressioneCron protegge il vincolo
// `jobs_schedule_shape_check`, che pretende cinque campi separati da spazi
// singoli: senza normalizzazione un'espressione con due spazi passerebbe qui e
// sarebbe rifiutata dal database.
func TestNormalizzazioneDellEspressioneCron(t *testing.T) {
	job := validJob()
	job.Schedule = "  0   9  *  *  * "
	job.Name = "  daily-digest  "

	if err := validate(t, &job, jobs.FreePlan); err != nil {
		t.Fatalf("espressione con spazi multipli rifiutata: %v", err)
	}
	if job.Schedule != "0 9 * * *" {
		t.Errorf("schedule normalizzata = %q, attesa %q", job.Schedule, "0 9 * * *")
	}
	if job.Name != "daily-digest" {
		t.Errorf("nome normalizzato = %q", job.Name)
	}

	shape := regexp.MustCompile(`^\S+(\s+\S+){4}$`)
	if !shape.MatchString(job.Schedule) {
		t.Errorf("l'espressione normalizzata non soddisfa jobs_schedule_shape_check: %q", job.Schedule)
	}
}

// ------------------------------------------------------------------- identità

func TestNomeRifiutato(t *testing.T) {
	cases := map[string]struct {
		nome string
		code string
	}{
		"vuoto":               {"", "required"},
		"solo spazi":          {"   ", "required"},
		"con spazio interno":  {"daily digest", "invalid_format"},
		"inizia con trattino": {"-digest", "invalid_format"},
		"finisce con punto":   {"digest.", "invalid_format"},
		"carattere estraneo":  {"digest/2", "invalid_format"},
		"troppo lungo":        {strings.Repeat("a", jobs.MaxNameLength+1), "too_long"},
	}

	for nome, tc := range cases {
		t.Run(nome, func(t *testing.T) {
			job := validJob()
			job.Name = tc.nome

			err := validate(t, &job, jobs.FreePlan)
			if got := codes(t, err)["name"]; got != tc.code {
				t.Fatalf("codice = %q, atteso %q (errore: %v)", got, tc.code, err)
			}
		})
	}
}

// TestNomiAmmessi verifica che i nomi che il vincolo del database accetta non
// vengano rifiutati qui: una copia più severa dell'originale è un difetto tanto
// quanto una più permissiva.
func TestNomiAmmessi(t *testing.T) {
	for _, nome := range []string{"a", "A1", "daily-digest", "daily.digest", "daily_digest", "job1"} {
		job := validJob()
		job.Name = nome
		if err := validate(t, &job, jobs.FreePlan); err != nil {
			t.Errorf("nome %q rifiutato: %v", nome, err)
		}
	}
}

// -------------------------------------------------------------------- target

func TestTargetRifiutato(t *testing.T) {
	cases := []struct {
		nome  string
		tune  func(*jobs.Job)
		campo string
		code  string
	}{
		{"url mancante", func(j *jobs.Job) { j.URL = "" }, "request.url", "required"},
		{"schema file", func(j *jobs.Job) { j.URL = "file:///etc/passwd" }, "request.url", "unsupported_scheme"},
		{"schema ftp", func(j *jobs.Job) { j.URL = "ftp://example.com/x" }, "request.url", "unsupported_scheme"},
		{"senza host", func(j *jobs.Job) { j.URL = "https:///percorso" }, "request.url", "invalid_format"},
		{"url troppo lungo", func(j *jobs.Job) {
			j.URL = "https://example.com/" + strings.Repeat("a", jobs.MaxURLLength)
		}, "request.url", "too_long"},
		{"metodo sconosciuto", func(j *jobs.Job) { j.Method = "FETCH" }, "request.method", "unknown_value"},
		{"metodo minuscolo", func(j *jobs.Job) { j.Method = "post" }, "request.method", "unknown_value"},
		{"header Host", func(j *jobs.Job) { j.Headers = map[string]string{"Host": "interno"} }, "request.headers", "reserved_name"},
		{"header Content-Length", func(j *jobs.Job) { j.Headers = map[string]string{"content-length": "10"} }, "request.headers", "reserved_name"},
		{"nome di header non valido", func(j *jobs.Job) { j.Headers = map[string]string{"X Token": "v"} }, "request.headers", "invalid_name"},
		{"a capo nel valore", func(j *jobs.Job) {
			j.Headers = map[string]string{"X-Token": "v\r\nX-Altro: iniettato"}
		}, "request.headers", "invalid_value"},
		{"corpo troppo grande", func(j *jobs.Job) {
			j.Body = strings.Repeat("x", jobs.MaxBodyLength+1)
		}, "request.body", "too_long"},
	}

	for _, tc := range cases {
		t.Run(tc.nome, func(t *testing.T) {
			job := validJob()
			tc.tune(&job)

			err := validate(t, &job, jobs.FreePlan)
			if got := codes(t, err)[tc.campo]; got != tc.code {
				t.Fatalf("codice sul campo %q = %q, atteso %q (errore: %v)", tc.campo, got, tc.code, err)
			}
		})
	}
}

func TestTroppiHeader(t *testing.T) {
	job := validJob()
	job.Headers = map[string]string{}
	for i := 0; i <= jobs.MaxHeaders; i++ {
		job.Headers["X-Header-"+strings.Repeat("a", i+1)] = "v"
	}

	err := validate(t, &job, jobs.FreePlan)
	if got := codes(t, err)["request.headers"]; got != "too_many" {
		t.Fatalf("codice = %q, atteso \"too_many\" (errore: %v)", got, err)
	}
}

// guardBloccante è il doppio del blocco SSRF di R38 (issue #455): serve a
// verificare che il punto di innesto esista e venga davvero chiamato.
type guardBloccante struct{ chiamato bool }

func (g *guardBloccante) CheckTarget(_ context.Context, target *url.URL) error {
	g.chiamato = true
	return errors.New("indirizzo interno non raggiungibile: " + target.Host)
}

func TestIlTargetGuardVieneInterrogato(t *testing.T) {
	guard := &guardBloccante{}
	job := validJob()
	job.URL = "http://169.254.169.254/latest/meta-data/"

	err := job.Validate(t.Context(), jobs.FreePlan, guard)
	if !guard.chiamato {
		t.Fatal("il TargetGuard non è stato interrogato: il blocco SSRF non avrebbe alcun punto di innesto")
	}
	if got := codes(t, err)["request.url"]; got != "target_not_allowed" {
		t.Fatalf("codice = %q, atteso \"target_not_allowed\" (errore: %v)", got, err)
	}
}

// ------------------------------------------------------------------ esecuzione

func TestParametriDiEsecuzioneRifiutati(t *testing.T) {
	cases := []struct {
		nome  string
		tune  func(*jobs.Job)
		campo string
		code  string
	}{
		{"timeout a zero", func(j *jobs.Job) { j.Timeout = 0 }, "timeout", "out_of_range"},
		{"timeout oltre il tetto", func(j *jobs.Job) { j.Timeout = jobs.MaxTimeout + time.Second }, "timeout", "out_of_range"},
		{"timeout frazionario", func(j *jobs.Job) { j.Timeout = 1500 * time.Millisecond }, "timeout", "invalid_format"},
		{"retry negativi", func(j *jobs.Job) { j.MaxRetries = -1 }, "retries.max", "out_of_range"},
		{"retry oltre il tetto", func(j *jobs.Job) { j.MaxRetries = jobs.MaxRetriesAllowed + 1 }, "retries.max", "out_of_range"},
		{"backoff sconosciuto", func(j *jobs.Job) { j.RetryBackoff = "random" }, "retries.backoff", "unknown_value"},
		{"canale sconosciuto", func(j *jobs.Job) {
			j.AlertOnFailure = []jobs.AlertChannel{"piccione"}
		}, "alerts.on_failure", "unknown_value"},
		{"canale ripetuto", func(j *jobs.Job) {
			j.AlertOnFailure = []jobs.AlertChannel{jobs.AlertEmail, jobs.AlertEmail}
		}, "alerts.on_failure", "duplicate"},
		{"nessun ambiente", func(j *jobs.Job) { j.Environments = nil }, "environments", "required"},
		{"ambiente sconosciuto", func(j *jobs.Job) {
			j.Environments = []jobs.Environment{"collaudo"}
		}, "environments", "unknown_value"},
		{"ambiente ripetuto", func(j *jobs.Job) {
			j.Environments = []jobs.Environment{jobs.EnvironmentProduction, jobs.EnvironmentProduction}
		}, "environments", "duplicate"},
	}

	for _, tc := range cases {
		t.Run(tc.nome, func(t *testing.T) {
			job := validJob()
			tc.tune(&job)

			err := validate(t, &job, planTeam())
			if got := codes(t, err)[tc.campo]; got != tc.code {
				t.Fatalf("codice sul campo %q = %q, atteso %q (errore: %v)", tc.campo, got, tc.code, err)
			}
		})
	}
}

// TestGliErroriSonoRestituitiTutti: un form che si corregge un campo per volta è
// un dialogo a turni, e ogni turno è un round-trip.
func TestGliErroriSonoRestituitiTutti(t *testing.T) {
	job := jobs.NewJob()
	job.Name = "nome non valido"
	job.URL = "ftp://example.com"
	job.Method = "FETCH"
	job.Timeout = 0

	err := validate(t, &job, jobs.FreePlan)
	got := codes(t, err)
	for _, campo := range []string{"name", "schedule", "request.url", "request.method", "timeout"} {
		if _, ok := got[campo]; !ok {
			t.Errorf("manca l'errore sul campo %q; ottenuti %v", campo, got)
		}
	}
}

// TestOrdineDegliErroriStabile: con l'ordine di iterazione di una mappa, un
// client che mostra il primo errore ne mostrerebbe uno diverso a ogni richiesta.
func TestOrdineDegliErroriStabile(t *testing.T) {
	build := func() error {
		job := validJob()
		job.Headers = map[string]string{"Host": "a", "X Token": "b", "Content-Length": "3"}
		return validate(t, &job, jobs.FreePlan)
	}

	first, _ := jobs.AsValidation(build())
	for i := 0; i < 20; i++ {
		next, _ := jobs.AsValidation(build())
		if len(first.Fields) != len(next.Fields) {
			t.Fatalf("numero di errori instabile: %d poi %d", len(first.Fields), len(next.Fields))
		}
		for j := range first.Fields {
			if first.Fields[j] != next.Fields[j] {
				t.Fatalf("ordine instabile alla posizione %d: %v poi %v", j, first.Fields[j], next.Fields[j])
			}
		}
	}
}

// ptr rende indirizzabile un job costruito al volo.
func ptr(job jobs.Job) *jobs.Job { return &job }
