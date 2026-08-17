package cronyaml_test

import (
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/cronyaml"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/schedule"
)

// specExample è **letteralmente** il file di SPEC §9, riga per riga.
//
// Copiarlo invece di scriverne uno equivalente è il punto del test: la
// specifica mostra questo file a chi valuta il prodotto, e un parser che non
// legge l'esempio della propria documentazione è un difetto che nessun altro
// test scopre.
const specExample = `
version: 1

defaults:
  timezone: Europe/Rome
  timeout: 30s
  retries: { max: 3, backoff: exponential }
  on_overlap: skip               # R41 — predefinito dichiarato

jobs:
  - name: daily-digest          # identità stabile del job: chiave della riconciliazione
    schedule: "0 9 * * *"       # espressione cron — granularità 1 minuto
    timezone: Europe/Rome       # sovrascrive defaults
    environments: [production]
    request:
      url: https://api.example.com/tasks/digest
      method: POST
      headers:
        Authorization: "Bearer ${DIGEST_TOKEN}"   # segreto del workspace, mai in chiaro
      body: '{"kind":"daily"}'
    timeout: 60s
    retries: { max: 5, backoff: exponential }
    alerts:
      on_failure: [email, slack]

  - name: healthcheck
    every: 10s                  # modalità a intervallo — risoluzione sub-minuto
    environments: [staging, production]
    request:
      url: https://api.example.com/health
      method: GET
    timeout: 5s
    on_overlap: allow           # sovrascrive defaults: un healthcheck può convivere con sé stesso
`

func TestIlFileDiEsempioDellaSpecificaSiLegge(t *testing.T) {
	file := mustParse(t, specExample, options("DIGEST_TOKEN"))

	if file.Version != cronyaml.SupportedVersion {
		t.Errorf("version = %d, attesa %d", file.Version, cronyaml.SupportedVersion)
	}
	if got := file.Names(); len(got) != 2 || got[0] != "daily-digest" || got[1] != "healthcheck" {
		t.Fatalf("nomi = %v", got)
	}

	digest := file.Jobs[0].Job
	if digest.Schedule != "0 9 * * *" {
		t.Errorf("schedule = %q", digest.Schedule)
	}
	if digest.Every != 0 {
		t.Errorf("every = %s, atteso zero: il job usa la modalità cron", digest.Every)
	}
	if digest.Timezone != "Europe/Rome" {
		t.Errorf("timezone = %q", digest.Timezone)
	}
	if digest.Method != jobs.MethodPOST {
		t.Errorf("method = %q", digest.Method)
	}
	if digest.URL != "https://api.example.com/tasks/digest" {
		t.Errorf("url = %q", digest.URL)
	}
	// I riferimenti restano **non risolti**: il file e la colonna `jobs.headers`
	// non contengono mai un segreto in chiaro (SPEC §9, R43).
	if got := digest.Headers["Authorization"]; got != "Bearer ${DIGEST_TOKEN}" {
		t.Errorf("Authorization = %q: il riferimento dev'essere conservato com'è", got)
	}
	if digest.Body != `{"kind":"daily"}` {
		t.Errorf("body = %q", digest.Body)
	}
	if digest.Timeout != 60*time.Second {
		t.Errorf("timeout = %s: il valore del job sovrascrive quello di defaults", digest.Timeout)
	}
	if digest.MaxRetries != 5 || digest.RetryBackoff != jobs.BackoffExponential {
		t.Errorf("retries = %d/%s", digest.MaxRetries, digest.RetryBackoff)
	}
	if len(digest.AlertOnFailure) != 2 || digest.AlertOnFailure[0] != jobs.AlertEmail || digest.AlertOnFailure[1] != jobs.AlertSlack {
		t.Errorf("alerts.on_failure = %v", digest.AlertOnFailure)
	}
	if len(digest.Environments) != 1 || digest.Environments[0] != jobs.EnvironmentProduction {
		t.Errorf("environments = %v", digest.Environments)
	}
	if digest.OverlapPolicy != jobs.OverlapSkip {
		t.Errorf("on_overlap = %q: doveva ereditare `skip` da defaults", digest.OverlapPolicy)
	}

	health := file.Jobs[1].Job
	if health.Every != 10*time.Second {
		t.Errorf("every = %s", health.Every)
	}
	if health.Schedule != "" {
		t.Errorf("schedule = %q, atteso vuoto: il job usa la modalità a intervallo", health.Schedule)
	}
	if health.Method != jobs.MethodGET {
		t.Errorf("method = %q", health.Method)
	}
	if health.Timeout != 5*time.Second {
		t.Errorf("timeout = %s", health.Timeout)
	}
	// `defaults` si applica a ciò che il job non dichiara.
	if health.Timezone != "Europe/Rome" {
		t.Errorf("timezone = %q: doveva ereditare da defaults", health.Timezone)
	}
	if health.MaxRetries != 3 {
		t.Errorf("retries.max = %d: doveva ereditare da defaults", health.MaxRetries)
	}
	if len(health.Environments) != 2 {
		t.Errorf("environments = %v", health.Environments)
	}
	// Il valore del job vince su quello di `defaults`, come per il timeout: è la
	// riga per cui R41 chiede che la politica sia configurabile **per job**.
	if health.OverlapPolicy != jobs.OverlapAllow {
		t.Errorf("on_overlap = %q, atteso `allow`: il valore del job sovrascrive quello di defaults",
			health.OverlapPolicy)
	}
}

// TestIJobLettiSonoEseguibili è il controllo che il file non basta a fare:
// ciò che il parser restituisce dev'essere qualcosa che il motore sa
// schedulare. Se questo test fallisce, il sync scriverebbe nel database job che
// il dispatch non riesce a far partire.
func TestIJobLettiSonoEseguibili(t *testing.T) {
	file := mustParse(t, specExample, options("DIGEST_TOKEN"))

	for _, entry := range file.Jobs {
		parsed, err := schedule.Parse(entry.Job.Spec())
		if err != nil {
			t.Fatalf("il job %q non è schedulabile: %v", entry.Job.Name, err)
		}
		if _, ok := parsed.Next(time.Now()); !ok {
			t.Errorf("il job %q non ha una prossima occorrenza", entry.Job.Name)
		}
	}
}

// TestIlConfineVersoLaRiconciliazione fissa ciò su cui #423 si appoggia. Sono
// due proprietà che il parser non deve decidere e che non deve nemmeno
// sfiorare.
func TestIlConfineVersoLaRiconciliazione(t *testing.T) {
	file := mustParse(t, specExample, options("DIGEST_TOKEN"))

	for _, job := range file.Domain() {
		if job.RepositoryID != "" {
			t.Errorf("il job %q ha RepositoryID %q: lo valorizza #423, che è l'unico a sapere da quale repository la push arriva",
				job.Name, job.RepositoryID)
		}
		if !job.Enabled {
			t.Errorf("il job %q arriva già in pausa: `enabled` è una scelta dell'utente che deve sopravvivere al sync, e il file non la esprime", job.Name)
		}
		if job.Archived() {
			t.Errorf("il job %q arriva archiviato", job.Name)
		}
	}

	// La posizione sopravvive alla validazione: serve a #423 per riportare sulla
	// riga giusta i propri errori.
	if file.Jobs[0].Line != 10 || file.Jobs[0].Path != "jobs[0]" {
		t.Errorf("prima voce a %d (%s), attesa riga 10 come `jobs[0]`", file.Jobs[0].Line, file.Jobs[0].Path)
	}
	if file.Jobs[1].Line != 25 {
		t.Errorf("seconda voce a riga %d, attesa 25", file.Jobs[1].Line)
	}
}

// TestUnFileMinimo prova che tutto ciò che non è obbligatorio non lo è
// davvero: i default di [jobs.NewJob] valgono, e coincidono con quelli della
// migrazione 0005.
func TestUnFileMinimo(t *testing.T) {
	file := mustParse(t, `
version: 1
jobs:
  - name: minimo
    every: 1m
    request:
      url: https://esempio.it/hook
`, options())

	job := file.Jobs[0].Job
	if job.Timezone != "UTC" {
		t.Errorf("timezone = %q, atteso UTC", job.Timezone)
	}
	if job.Method != jobs.MethodPOST {
		t.Errorf("method = %q, atteso POST", job.Method)
	}
	if job.Timeout != 30*time.Second {
		t.Errorf("timeout = %s, atteso 30s", job.Timeout)
	}
	if job.MaxRetries != 3 || job.RetryBackoff != jobs.BackoffExponential {
		t.Errorf("retries = %d/%s", job.MaxRetries, job.RetryBackoff)
	}
	if len(job.Environments) != 1 || job.Environments[0] != jobs.EnvironmentProduction {
		t.Errorf("environments = %v, atteso [production]", job.Environments)
	}
	if len(job.AlertOnFailure) != 1 || job.AlertOnFailure[0] != jobs.AlertEmail {
		t.Errorf("alerts = %v, atteso [email]", job.AlertOnFailure)
	}
	if len(job.Headers) != 0 {
		t.Errorf("headers = %v, attesi vuoti", job.Headers)
	}
}

// TestUnElencoVuotoDisattivaTuttoEdEUnaSceltaLegittima: `jobs: []` è la
// richiesta esplicita di non avere più job da questo repository, ed è scritta
// nel file — cioè visibile in una pull request, che è il modo in cui questo
// prodotto vuole che le cose accadano.
func TestUnElencoVuotoDisattivaTuttoEdEUnaSceltaLegittima(t *testing.T) {
	file := mustParse(t, `
version: 1
jobs: []
`, options())

	if len(file.Jobs) != 0 {
		t.Errorf("job = %d, attesi zero", len(file.Jobs))
	}
}

// TestUnaChiaveJobsVuotaNonEUnElencoVuoto distingue la scelta dall'incidente:
// `jobs:` senza niente sotto è una riga lasciata a metà, e va detto.
func TestUnaChiaveJobsVuotaNonEUnElencoVuoto(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
`, options())

	item := find(t, items, "jobs")
	if item.Code != cronyaml.CodeRequired {
		t.Errorf("codice = %q, atteso %q", item.Code, cronyaml.CodeRequired)
	}
	contains(t, item, "- name:")
}

// ------------------------------------------------------- gli alias YAML

// TestGliAliasSiRisolvono: un `cron.yaml` con venti job che condividono gli
// header è la ragione per cui gli alias YAML esistono, e rifiutarli
// costringerebbe a ripetere lo stesso blocco venti volte — cioè a rendere il
// file peggiore di quello che sostituisce.
//
// L'ancora si dichiara al primo punto in cui il valore serve, non in un blocco
// a parte in cima al file: le chiavi di primo livello sono solo quelle dello
// schema, e questo è il prezzo — esplicito — di «rifiuta ciò che non conosci».
func TestGliAliasSiRisolvono(t *testing.T) {
	file := mustParse(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    request:
      url: https://esempio.it/uno
      headers: &comuni
        Authorization: "Bearer ${TOKEN}"
  - name: due
    every: 1m
    request:
      url: https://esempio.it/due
      headers: *comuni
`, options("TOKEN"))

	for _, entry := range file.Jobs {
		if got := entry.Job.Headers["Authorization"]; got != "Bearer ${TOKEN}" {
			t.Errorf("job %q: Authorization = %q", entry.Job.Name, got)
		}
	}
}

// TestUnAliasVersoUnSegretoInesistenteFallisceInOgniPuntoInCuiEUsato: la
// posizione dell'errore è quella dell'**uso**, non quella dell'ancora. Chi legge
// deve andare dove il valore è finito.
func TestUnAliasVersoUnSegretoInesistenteFallisceInOgniPuntoInCuiEUsato(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    request:
      url: https://esempio.it/uno
      headers: &comuni
        Authorization: "Bearer ${ASSENTE}"
  - name: due
    every: 1m
    request:
      url: https://esempio.it/due
      headers: *comuni
`, options("PRESENTE"))

	if len(items) != 2 {
		t.Fatalf("errori = %d, attesi 2 (uno per ciascun job):\n%s", len(items), format(items))
	}
	at(t, items[0], 8, 24)
	at(t, items[1], 13, 16)
	for _, item := range items {
		contains(t, item, "ASSENTE")
		contains(t, item, "PRESENTE")
	}
}

// TestLaChiaveDiFusioneVieneRifiutataConUnAlternativa: `<<` renderebbe il
// contenuto effettivo di un job diverso da quello che si legge nel suo blocco,
// che è l'opposto del motivo per cui le schedulazioni stanno in un file.
func TestLaChiaveDiFusioneVieneRifiutataConUnAlternativa(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: &ogni 1m
    request:
      url: https://esempio.it/uno
  - name: due
    <<: *ogni
    request:
      url: https://esempio.it/due
`, options())

	item := findCode(t, items, cronyaml.CodeMergeKey)
	at(t, item, 8, 5)
	contains(t, item, "`defaults`")
}
