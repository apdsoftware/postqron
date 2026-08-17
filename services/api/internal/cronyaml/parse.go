package cronyaml

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// Le chiavi che lo schema conosce, per livello. Servono a due cose: rifiutare
// ciò che non è previsto, e — quando la chiave sconosciuta assomiglia a una di
// queste — suggerire quale si intendeva.
var (
	rootKeys     = []string{"version", "defaults", "jobs"}
	defaultsKeys = []string{"timezone", "timeout", "retries", "on_overlap"}
	jobKeys      = []string{"name", "schedule", "every", "timezone", "environments", "request", "timeout", "retries", "on_overlap", "alerts"}
	requestKeys  = []string{"url", "method", "headers", "body"}
	retriesKeys  = []string{"max", "backoff"}
	alertsKeys   = []string{"on_failure"}
)

// Parse legge un `cron.yaml` e restituisce i job che descrive.
//
// L'errore, quando c'è, è sempre un [*ParseError] e contiene **tutti** i motivi
// di rifiuto, ciascuno con riga, colonna, campo e correzione. Il [File] è nil
// quando l'errore non lo è: un file che non passa la validazione non produce
// niente da riconciliare, che è il senso di R13 — «un file non valido non
// modifica lo stato esistente».
//
// `ctx` serve al solo controllo sui target ([Options.Guard]), che risolve nomi
// di rete.
func Parse(ctx context.Context, source []byte, opts Options) (*File, error) {
	opts = opts.withDefaults()
	errs := &errorList{source: opts.Source}
	read := &reader{errs: errs}

	root, ok := decode(source, errs)
	if !ok {
		return nil, errs.orNil()
	}

	rootMap, ok := read.mapping(root, "", "Un `cron.yaml` comincia con `version: 1` e contiene `jobs:` con almeno una voce.")
	if !ok {
		return nil, errs.orNil()
	}

	version, ok := parseVersion(read, rootMap)
	if !ok {
		// Una versione che non conosciamo non si interpreta: ogni errore
		// successivo sarebbe una supposizione su che cosa quella versione
		// intendesse, e un elenco di chiavi «sconosciute» che in realtà sono
		// quelle giuste dello schema nuovo è peggio del silenzio.
		return nil, errs.orNil()
	}

	defs := parseDefaults(read, rootMap)
	entries, declared := parseJobs(ctx, read, rootMap, defs, opts)
	read.rejectUnknown(rootMap, "", rootKeys)

	rejectDuplicateNames(errs, entries)
	checkJobCount(errs, rootMap, entries, declared, opts)

	if err := errs.orNil(); err != nil {
		return nil, err
	}
	return &File{Source: opts.Source, Version: version, Jobs: entries}, nil
}

// ------------------------------------------------------------- decodifica

// decode porta i byte a un albero YAML, o spiega perché non ci arrivano.
func decode(source []byte, errs *errorList) (*yaml.Node, bool) {
	start := Position{Line: 1, Column: 1}

	if len(source) > MaxFileSize {
		errs.add(start, "", CodeTooLarge,
			"Il file è di %d byte e il massimo è %d. Un `cron.yaml` scritto a mano non ci arriva nemmeno vicino: se il file è generato, generane uno più piccolo o dividi i job su più repository.",
			len(source), MaxFileSize)
		return nil, false
	}
	if len(bytes.TrimSpace(source)) == 0 {
		errs.add(start, "", CodeEmpty,
			"Il file è vuoto. Il minimo che può contenere è `version: %d` seguito da `jobs:` con almeno una voce.",
			SupportedVersion)
		return nil, false
	}

	decoder := yaml.NewDecoder(bytes.NewReader(source))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			errs.add(start, "", CodeEmpty,
				"Il file non contiene nessun documento YAML: c'è solo testo commentato o spazi. Serve almeno `version: %d` e una voce in `jobs`.",
				SupportedVersion)
			return nil, false
		}
		position, message := syntax(err)
		errs.add(position, "", CodeSyntax, "%s", message)
		return nil, false
	}

	// Un secondo documento — `---` a metà file — sarebbe letto da noi e ignorato
	// in silenzio, e i job che contiene non verrebbero mai eseguiti senza che
	// nessuno lo dica. È la forma peggiore di errore che questo file possa avere:
	// il push riesce, la validazione passa, e metà delle schedulazioni non
	// esistono.
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		errs.add(at(&extra), "", CodeSyntax,
			"Il file contiene più di un documento YAML. Togli la riga `---` che li separa e metti tutti i job sotto un unico `jobs:`: altrimenti verrebbe letto solo il primo documento, e le schedulazioni del secondo non esisterebbero senza che nessuno lo dica.")
		return nil, false
	} else if !errors.Is(err, io.EOF) {
		position, message := syntax(err)
		errs.add(position, "", CodeSyntax, "%s", message)
		return nil, false
	}

	if len(document.Content) == 0 {
		errs.add(start, "", CodeEmpty,
			"Il documento è vuoto. Serve almeno `version: %d` e una voce in `jobs`.", SupportedVersion)
		return nil, false
	}
	return deref(document.Content[0]), true
}

// yamlLine estrae la riga dai messaggi di yaml.v3, che hanno la forma
// `yaml: line 12: ...`.
var yamlLine = regexp.MustCompile(`line (\d+):`)

// syntax traduce l'errore del decodificatore in qualcosa che si possa usare.
//
// # Perché non si lascia passare com'è
//
// «yaml: line 7: did not find expected key» è, per chi ha scritto il file, un
// messaggio in una lingua che non parla, su una libreria di cui non sa
// l'esistenza, che descrive lo stato interno di un tokenizzatore invece di ciò
// che ha sbagliato. La causa reale è quasi sempre una sola e concreta —
// un'indentazione, una tabulazione, un due punti dentro un valore — e dirla è
// la differenza fra un file corretto in trenta secondi e un file abbandonato.
//
// Ciò che non si riconosce viene comunque riportato, con la riga e con l'unico
// consiglio sempre vero: guardare l'indentazione di quella riga e di quella
// sopra.
func syntax(err error) (Position, string) {
	raw := strings.TrimPrefix(err.Error(), "yaml: ")
	position := Position{Line: 1, Column: 1}
	if match := yamlLine.FindStringSubmatch(err.Error()); match != nil {
		if line, convErr := strconv.Atoi(match[1]); convErr == nil && line > 0 {
			position.Line = line
		}
		_, raw, _ = strings.Cut(raw, ": ")
	}

	switch {
	case strings.Contains(raw, "found character that cannot start any token"):
		// In pratica sempre una tabulazione: è l'unico carattere che una
		// tastiera produce e che YAML vieta all'inizio di un token.
		return position, "In questa riga c'è un carattere che YAML non ammette, quasi sempre una **tabulazione**: l'indentazione di un file YAML si fa con spazi. Sostituisci le tabulazioni con due spazi per livello."
	case strings.Contains(raw, "did not find expected key"),
		strings.Contains(raw, "did not find expected node content"):
		return position, "L'indentazione di questa riga non corrisponde a quella del blocco in cui si trova. Le chiavi allo stesso livello vanno allineate sulla stessa colonna, e le voci di un elenco cominciano con `- `."
	case strings.Contains(raw, "mapping values are not allowed"):
		return position, "In questa riga c'è un `:` dentro un valore. Un valore che contiene i due punti va messo fra virgolette, per esempio `body: '{\"kind\":\"daily\"}'`."
	case strings.Contains(raw, "could not find expected ':'"):
		return position, "Manca il `:` fra la chiave e il suo valore, oppure la riga precedente è rimasta a metà."
	case strings.Contains(raw, "found unexpected end of stream"),
		strings.Contains(raw, "found unexpected end of document"):
		return position, "Il file finisce prima di quanto la sintassi si aspetti. Chiudi la virgoletta, la parentesi quadra o la graffa rimasta aperta."
	case strings.Contains(raw, "found duplicate anchor"):
		return position, "Due ancore YAML (`&nome`) hanno lo stesso nome: dàgliene uno diverso."
	case strings.Contains(raw, "unknown anchor"):
		return position, "Questo alias (`*nome`) riferisce un'ancora che non esiste: l'ancora (`&nome`) va dichiarata prima di essere usata."
	case strings.Contains(raw, "found a tab character"):
		return position, "In questa riga c'è una tabulazione. L'indentazione di un file YAML si fa con spazi: sostituiscile con due spazi per livello."
	default:
		return position, fmt.Sprintf(
			"Questa riga non è YAML valido (%s). Controlla l'indentazione di questa riga e di quella sopra: nei file YAML è quasi sempre lì il problema, e si risolve allineando le chiavi dello stesso livello sulla stessa colonna.",
			raw)
	}
}

// --------------------------------------------------------------- versione

// parseVersion legge `version`. Il secondo valore è falso quando la lettura del
// resto del file non ha più senso.
func parseVersion(read *reader, root *mapping) (int, bool) {
	node, present := root.value("version")
	if !present {
		// Assente si tratta come «la versione che conosciamo»: è quasi sempre
		// una dimenticanza, e fermarsi qui priverebbe l'utente di tutti gli
		// altri errori del file, che è l'opposto di quello che questo package
		// promette. L'errore viene comunque segnalato, e il file resta invalido.
		read.errs.add(at(root.node), "version", CodeRequired,
			"Manca `version`, che è obbligatorio: aggiungi `version: %d` come prima riga del file. Serve a poter cambiare lo schema in futuro senza rompere i `cron.yaml` già scritti.",
			SupportedVersion)
		return SupportedVersion, true
	}

	value, ok := read.integer(node, "version", fmt.Sprintf("La versione dello schema è un numero: scrivi `version: %d`.", SupportedVersion))
	if !ok {
		return SupportedVersion, true
	}
	if value != SupportedVersion {
		read.errs.add(at(node), "version", CodeUnsupportedVersion,
			"`version: %d` non è uno schema che questa versione di Postqron sa leggere: l'unico supportato è il %d. Scrivi `version: %d`.",
			value, SupportedVersion, SupportedVersion)
		return 0, false
	}
	return value, true
}

// --------------------------------------------------------------- defaults

// fileDefaults sono i valori di `defaults`, con il punto del file da cui
// vengono.
//
// Le posizioni servono a un caso che sarebbe altrimenti diagnosticato male: un
// `defaults.timeout` fuori intervallo si manifesta come errore *di ogni job*,
// perché è a ogni job che viene applicato. Senza queste ancore, l'utente
// riceverebbe cinquanta errori che puntano a cinquanta job corretti, invece di
// cinquanta errori che puntano tutti alla stessa riga sbagliata.
type fileDefaults struct {
	timezone    string
	hasTimezone bool
	timeout     time.Duration
	hasTimeout  bool
	retries     retries
	overlap     string
	hasOverlap  bool
	anchors     anchors
}

// retries è il blocco `retries: { max, backoff }`, di `defaults` o di un job.
type retries struct {
	max        int
	hasMax     bool
	backoff    string
	hasBackoff bool
}

func parseDefaults(read *reader, root *mapping) *fileDefaults {
	out := &fileDefaults{anchors: anchors{}}

	node, present := root.value("defaults")
	if !present {
		return out
	}
	m, ok := read.mapping(node, "defaults", "Contiene i valori comuni a tutti i job: `timezone`, `timeout`, `retries`.")
	if !ok {
		return out
	}

	if value, ok := m.value("timezone"); ok {
		if text, ok := read.text(value, "defaults.timezone", "Un fuso orario è un nome IANA, per esempio `Europe/Rome` oppure `UTC`."); ok {
			out.timezone, out.hasTimezone = strings.TrimSpace(text), true
			out.anchors.set("timezone", m.pos("timezone"), "defaults.timezone")
		}
	}
	if value, ok := m.value("timeout"); ok {
		if duration, ok := read.duration(value, "defaults.timeout", timeoutHint); ok {
			out.timeout, out.hasTimeout = duration, true
			out.anchors.set("timeout", m.pos("timeout"), "defaults.timeout")
		}
	}
	if value, ok := m.value("retries"); ok {
		out.retries = parseRetries(read, value, "defaults.retries", out.anchors)
	}
	if value, ok := m.value("on_overlap"); ok {
		if text, ok := read.text(value, "defaults.on_overlap", overlapHint()); ok {
			out.overlap, out.hasOverlap = strings.ToLower(strings.TrimSpace(text)), true
			out.anchors.set("on_overlap", m.pos("on_overlap"), "defaults.on_overlap")
		}
	}

	read.rejectUnknown(m, "defaults", defaultsKeys)
	return out
}

// parseRetries legge `retries: { max, backoff }`.
//
// Le ancore si registrano sotto `retries.max` e `retries.backoff` — i nomi che
// [jobs.FieldError] usa — indipendentemente da dove il blocco si trovi: è ciò
// che permette a un errore nato da `defaults.retries.max` di essere mostrato
// sulla riga di `defaults`, e a uno nato dal job di essere mostrato sulla sua.
func parseRetries(read *reader, node *yaml.Node, path string, anc anchors) retries {
	var out retries
	m, ok := read.mapping(node, path, "Si scrive `retries: { max: 3, backoff: exponential }`.")
	if !ok {
		return out
	}
	if value, ok := m.value("max"); ok {
		if number, ok := read.integer(value, path+".max", "È il numero di tentativi dopo il primo, per esempio `max: 3`."); ok {
			out.max, out.hasMax = number, true
			anc.set("retries.max", m.pos("max"), path+".max")
		}
	}
	if value, ok := m.value("backoff"); ok {
		hint := fmt.Sprintf("Le politiche ammesse sono %s.", quoteJoin(backoffNames()))
		if text, ok := read.text(value, path+".backoff", hint); ok {
			out.backoff, out.hasBackoff = strings.ToLower(strings.TrimSpace(text)), true
			anc.set("retries.backoff", m.pos("backoff"), path+".backoff")
		}
	}
	read.rejectUnknown(m, path, retriesKeys)
	return out
}

// -------------------------------------------------------------------- job

// parseJobs legge la sequenza `jobs`. Il secondo valore è il numero di voci
// dichiarate, che non coincide con quelle lette bene: al tetto di piano
// contano anche i job scritti male, perché il file li dichiara comunque.
func parseJobs(ctx context.Context, read *reader, root *mapping, defs *fileDefaults, opts Options) ([]Entry, int) {
	node, present := root.value("jobs")
	if !present {
		read.errs.add(at(root.node), "jobs", CodeRequired,
			"Manca `jobs`, che è l'elenco dei job del repository. Aggiungilo, con almeno una voce:\n"+
				"    jobs:\n"+
				"      - name: healthcheck\n"+
				"        every: 10s\n"+
				"        request: { url: https://esempio.it/health, method: GET }")
		return nil, 0
	}

	node = deref(node)
	if node == nil || node.Kind != yaml.SequenceNode {
		read.wrongKind(node, "jobs", "una lista", "Ogni job è una voce di elenco, per esempio `- name: healthcheck`.")
		return nil, 0
	}

	// Una lista vuota è legittima e significa «questo repository non ha più
	// job»: la riconciliazione (#423) li archivierà tutti. È una richiesta
	// esplicita e scritta nel file, che è esattamente il modo in cui questo
	// prodotto vuole che le cose accadano; cancellare il file per ottenere lo
	// stesso effetto sarebbe meno visibile in una pull request, non di più.
	declared := len(node.Content)
	items := node.Content
	if declared > MaxJobsPerFile {
		read.errs.add(at(node), "jobs", CodeTooManyJobs,
			"Il file dichiara %d job e il massimo per un singolo `cron.yaml` è %d. Dividi i job su più repository.",
			declared, MaxJobsPerFile)
		items = items[:MaxJobsPerFile]
	}

	entries := make([]Entry, 0, len(items))
	for index, item := range items {
		if entry, ok := parseJob(ctx, read, item, index, defs, opts); ok {
			entries = append(entries, entry)
		}
	}
	return entries, declared
}

// parseJob legge una voce di `jobs` e la valida.
//
// Il secondo valore è falso quando la voce non è nemmeno una mappa: in quel
// caso non c'è niente da validare, e i motivi sono già negli errori.
func parseJob(ctx context.Context, read *reader, node *yaml.Node, index int, defs *fileDefaults, opts Options) (Entry, bool) {
	path := fmt.Sprintf("jobs[%d]", index)

	m, ok := read.mapping(node, path, "Ogni job è una mappa che comincia con `- name: ...`.")
	if !ok {
		return Entry{}, false
	}

	// Le ancore partono da quelle di `defaults`: un valore che il job non
	// dichiara viene da lì, e da lì deve venire anche l'errore che lo riguarda.
	anc := anchors{}
	anc.inherit(defs.anchors)
	anc.set("", at(m.node), path)

	job := jobs.NewJob()
	if defs.hasTimezone {
		job.Timezone = defs.timezone
	}
	if defs.hasTimeout {
		job.Timeout = defs.timeout
	}
	if defs.retries.hasMax {
		job.MaxRetries = defs.retries.max
	}
	if defs.retries.hasBackoff {
		job.RetryBackoff = jobs.Backoff(defs.retries.backoff)
	}
	if defs.hasOverlap {
		job.OverlapPolicy = jobs.OverlapPolicy(defs.overlap)
	}

	// broken sono i campi la cui **forma** non si è lasciata leggere. Sui campi
	// che ci finiscono la validazione semantica viene taciuta: dire «serve
	// `schedule` oppure `every`» a chi ha scritto `every: 1x` sarebbe una
	// diagnosi sbagliata, perché `every` l'ha scritto — è la durata che non si
	// legge, e quello glielo abbiamo già detto sulla riga giusta.
	broken := map[string]bool{}

	if value, ok := m.value("name"); ok {
		if text, ok := read.text(value, path+".name", "Il nome è l'identità stabile del job, per esempio `daily-digest`."); ok {
			job.Name = strings.TrimSpace(text)
		} else {
			broken["name"] = true
		}
		anc.set("name", m.pos("name"), path+".name")
	}

	if value, ok := m.value("schedule"); ok {
		if text, ok := read.text(value, path+".schedule", "Un'espressione cron a cinque campi, per esempio `\"0 9 * * *\"`."); ok {
			job.Schedule = text
		} else {
			broken["schedule"] = true
		}
		anc.set("schedule", m.pos("schedule"), path+".schedule")
	}

	if value, ok := m.value("every"); ok {
		if duration, ok := read.duration(value, path+".every", everyHint); ok {
			job.Every = duration
		} else {
			broken["every"] = true
		}
		anc.set("every", m.pos("every"), path+".every")
	}

	if value, ok := m.value("timezone"); ok {
		if text, ok := read.text(value, path+".timezone", "Un fuso orario è un nome IANA, per esempio `Europe/Rome` oppure `UTC`."); ok {
			job.Timezone = strings.TrimSpace(text)
		} else {
			broken["timezone"] = true
		}
		anc.set("timezone", m.pos("timezone"), path+".timezone")
	}

	if value, ok := m.value("environments"); ok {
		hint := fmt.Sprintf("Si scrive `environments: [production]`; gli ambienti sono %s.", quoteJoin(environmentNames()))
		// Le singole voci non hanno un'ancora propria, e non è una svista:
		// [jobs.Job.Validate] riporta gli errori sugli ambienti al campo
		// `environments` nel suo insieme, e un'ancora che nessuno interroga
		// darebbe l'impressione di una precisione che non c'è.
		if values, ok := read.sequence(value, path+".environments", hint); ok {
			job.Environments = job.Environments[:0]
			for _, item := range values {
				job.Environments = append(job.Environments, jobs.Environment(strings.ToLower(strings.TrimSpace(item))))
			}
		} else {
			broken["environments"] = true
		}
		anc.set("environments", m.pos("environments"), path+".environments")
	}

	if value, ok := m.value("request"); ok {
		parseRequest(read, value, path+".request", &job, anc, broken)
	} else {
		// Senza `request` non c'è niente da chiamare. Lo dice comunque
		// jobs.Job.Validate con «l'URL è obbligatorio», ma quel messaggio manda
		// a cercare un campo dentro un blocco che non c'è.
		read.errs.add(at(m.node), path+".request", CodeRequired,
			"Manca `request`, cioè la chiamata che il job deve fare. Il minimo è `request: { url: https://esempio.it/hook, method: POST }`.")
		broken["request.url"] = true
	}

	if value, ok := m.value("timeout"); ok {
		if duration, ok := read.duration(value, path+".timeout", timeoutHint); ok {
			job.Timeout = duration
		} else {
			broken["timeout"] = true
		}
		anc.set("timeout", m.pos("timeout"), path+".timeout")
	}

	if value, ok := m.value("retries"); ok {
		got := parseRetries(read, value, path+".retries", anc)
		if got.hasMax {
			job.MaxRetries = got.max
		}
		if got.hasBackoff {
			job.RetryBackoff = jobs.Backoff(got.backoff)
		}
	}

	if value, ok := m.value("on_overlap"); ok {
		if text, ok := read.text(value, path+".on_overlap", overlapHint()); ok {
			job.OverlapPolicy = jobs.OverlapPolicy(strings.ToLower(strings.TrimSpace(text)))
		} else {
			broken["on_overlap"] = true
		}
		anc.set("on_overlap", m.pos("on_overlap"), path+".on_overlap")
	}

	if value, ok := m.value("alerts"); ok {
		parseAlerts(read, value, path+".alerts", &job, anc, broken)
	}

	read.rejectUnknown(m, path, jobKeys)

	validateJob(ctx, read.errs, &job, anc, broken, opts)

	return Entry{Job: job, Position: at(m.node), Path: path}, true
}

// parseRequest legge il blocco `request`.
func parseRequest(read *reader, node *yaml.Node, path string, job *jobs.Job, anc anchors, broken map[string]bool) {
	m, ok := read.mapping(node, path, "Si scrive `request: { url: https://esempio.it/hook, method: POST }`.")
	if !ok {
		broken["request.url"] = true
		return
	}
	anc.set("request", at(m.node), path)

	if value, ok := m.value("url"); ok {
		if text, ok := read.text(value, path+".url", "L'URL di destinazione, per esempio `https://api.esempio.it/tasks/digest`."); ok {
			job.URL = strings.TrimSpace(text)
		} else {
			broken["request.url"] = true
		}
		anc.set("request.url", m.pos("url"), path+".url")
	}

	if value, ok := m.value("method"); ok {
		hint := fmt.Sprintf("I metodi ammessi sono %s.", quoteJoin(methodNames()))
		if text, ok := read.text(value, path+".method", hint); ok {
			// Le maiuscole si mettono qui invece di rifiutare `get`: il metodo
			// HTTP è maiuscolo per convenzione del protocollo, non per una
			// scelta di questo file, e rimandare indietro un errore per un
			// dettaglio che sappiamo correggere è pedanteria, non rigore.
			job.Method = jobs.Method(strings.ToUpper(strings.TrimSpace(text)))
		} else {
			broken["request.method"] = true
		}
		anc.set("request.method", m.pos("method"), path+".method")
	}

	if value, ok := m.value("headers"); ok {
		hint := "Si scrive `headers: { Authorization: \"Bearer ${TOKEN}\" }`, una chiave per testata."
		if values, positions, ok := read.textMap(value, path+".headers", hint); ok {
			job.Headers = values
			for name, position := range positions {
				anc.set("request.headers."+name, position, path+".headers."+name)
			}
		} else {
			broken["request.headers"] = true
		}
		anc.set("request.headers", m.pos("headers"), path+".headers")
	}

	if value, ok := m.value("body"); ok {
		hint := "Il corpo è testo: se mandi del JSON, scrivilo fra apici singoli, per esempio `body: '{\"kind\":\"daily\"}'`."
		if text, ok := read.text(value, path+".body", hint); ok {
			job.Body = text
		} else {
			broken["request.body"] = true
		}
		anc.set("request.body", m.pos("body"), path+".body")
	}

	read.rejectUnknown(m, path, requestKeys)
}

// parseAlerts legge il blocco `alerts`.
func parseAlerts(read *reader, node *yaml.Node, path string, job *jobs.Job, anc anchors, broken map[string]bool) {
	m, ok := read.mapping(node, path, "Si scrive `alerts: { on_failure: [email] }`.")
	if !ok {
		broken["alerts.on_failure"] = true
		return
	}
	anc.set("alerts", at(m.node), path)

	if value, ok := m.value("on_failure"); ok {
		hint := fmt.Sprintf("Si scrive `on_failure: [email]`; i canali sono %s. Un elenco vuoto significa nessun avviso.", quoteJoin(channelNames()))
		if values, ok := read.sequence(value, path+".on_failure", hint); ok {
			job.AlertOnFailure = make([]jobs.AlertChannel, 0, len(values))
			for _, item := range values {
				job.AlertOnFailure = append(job.AlertOnFailure, jobs.AlertChannel(strings.ToLower(strings.TrimSpace(item))))
			}
		} else {
			broken["alerts.on_failure"] = true
		}
		anc.set("alerts.on_failure", m.pos("on_failure"), path+".on_failure")
	}

	read.rejectUnknown(m, path, alertsKeys)
}

// ---------------------------------------------------------- validazione

// validateJob applica al job le regole che questo package non possiede.
//
// Sono tre passaggi, tutti delegati, e nessuno dei tre ha qui una seconda
// implementazione:
//
//   - [jobs.Job.Validate] per la forma di dominio, le schedulazioni (che gira
//     su [schedule]) e i limiti di piano;
//   - [secrets.NameSet.Validate] per i `${VAR}`;
//   - niente per la riconciliazione, che è #423.
//
// Ciò che resta a questo package è tradurre: prendere un errore ancorato a un
// campo e ancorarlo a una riga.
func validateJob(ctx context.Context, errs *errorList, job *jobs.Job, anc anchors, broken map[string]bool, opts Options) {
	job.Normalize()

	// L'URL può contenere `${VAR}`, e con un riferimento nell'autorità non è
	// nemmeno un URL leggibile per net/url. Si valida quindi una copia con i
	// riferimenti sostituiti da un segnaposto: la forma (schema, presenza
	// dell'host, lunghezza) resta quella vera, mentre ciò che si conserva e si
	// eseguirà è il testo originale, con i riferimenti intatti.
	probe := *job
	probe.URL = probeURL(job.URL)

	// Il controllo sui target risolve nomi di rete, e con un riferimento
	// nell'host non c'è un nome da risolvere: il segnaposto non è il bersaglio
	// vero, e controllarlo direbbe qualcosa di un host che non esiste. Non è
	// una falla — quel controllo anticipa la diagnosi e non è la difesa: la
	// difesa è l'apertura della connessione, che avviene sull'URL risolto e che
	// non passa di qui (vedi [jobs.TargetGuard]).
	guard := opts.Guard
	if authorityHasReference(job.URL) {
		guard = nil
	}

	if err := probe.Validate(ctx, opts.Plan, guard); err != nil {
		if invalid, ok := jobs.AsValidation(err); ok {
			for _, field := range invalid.Fields {
				addFieldError(errs, anc, broken, field.Field, field.Code, field.Message)
			}
		}
		if limit, ok := jobs.AsPlanLimit(err); ok {
			target := anc.lookup(limit.Field)
			errs.addPlan(target.Position, target.Path, limit, planRemedy(limit))
		}
	}

	// I segreti si verificano **senza decifrarli** ed è il cuore di R43: un
	// `${VAR}` che non esiste fallisce al sync, davanti a chi ha appena fatto
	// push, e non alle tre di notte quando l'unico effetto è un'esecuzione
	// fallita in un registro che nessuno sta guardando.
	request := secrets.Request{URL: job.URL, Headers: job.Headers, Body: job.Body}
	if err := opts.Secrets.Validate(request); err != nil {
		if invalid, ok := secrets.AsValidation(err); ok {
			for _, field := range invalid.Fields {
				addFieldError(errs, anc, broken, field.Field, field.Code, field.Message)
			}
		}
	}
}

// addFieldError ancora al file un rifiuto che arriva ancorato a un campo.
func addFieldError(errs *errorList, anc anchors, broken map[string]bool, field, code, message string) {
	if broken[field] {
		// La forma di questo campo è già stata rifiutata, con la sua riga e la
		// sua correzione. Un secondo errore sullo stesso campo, dedotto da un
		// valore che non siamo riusciti a leggere, direbbe una cosa che non
		// sappiamo.
		return
	}

	// Le due facce dell'esclusività fra `schedule` ed `every` (SPEC §9) sono
	// riscritte qui, e sono le uniche. Il motivo non è il testo: è la
	// posizione. jobs.Job.Validate le ancora entrambe al campo `schedule`,
	// perché per l'API sono un errore sul corpo della richiesta; in un file,
	// «ne hai dichiarati due» va mostrato dove sta il secondo, e «non ne hai
	// dichiarato nessuno» va mostrato sul job, perché non c'è una riga da
	// indicare.
	switch code {
	case "schedule_conflict":
		if broken["every"] || broken["schedule"] {
			return
		}
		target := anc.lookup("every")
		other := anc.lookup("schedule")
		errs.add(target.Position, target.Path, code,
			"Questo job dichiara sia `every` sia `schedule` (riga %d), e i due sono mutuamente esclusivi: `schedule` fissa un orario dell'orologio, `every` un intervallo. Togline uno.",
			other.Line)
		return
	case "schedule_required":
		if broken["every"] || broken["schedule"] {
			return
		}
		target := anc.lookup("")
		errs.add(target.Position, target.Path, code,
			"Questo job non dice quando va eseguito: aggiungi `schedule: \"0 9 * * *\"` per un orario fisso, oppure `every: 10s` per un intervallo. Esattamente uno dei due.")
		return
	}

	// Un errore sul fuso orario di un job a intervallo arriva ancorato a `every`,
	// e va spostato. Non è una correzione a caso: [jobs] stesso distingue i due
	// casi guardando se il messaggio parla del fuso, ma lo fa solo nel ramo della
	// modalità cron — in quella a intervallo il campo `every` vince prima.
	//
	// Per l'API la differenza non si vede: il campo serve a evidenziare un input
	// in un form, e il form ha una sola casella per la schedulazione. In un file
	// si vede eccome. L'errore finirebbe sulla riga di `every`, che è scritta
	// bene, mentre quella sbagliata — che può stare dentro `defaults`, cioè in
	// un altro punto del documento — resterebbe senza niente che la indichi.
	if field == "every" && strings.Contains(message, "fuso orario") {
		field, code = "timezone", "invalid_schedule"
	}

	target := anc.lookup(field)
	if extra := remedy(field, code); extra != "" {
		message = terminate(message) + " " + extra
	}
	errs.add(target.Position, target.Path, code, "%s", capitalize(message))
}

// terminate chiude la frase quando chi l'ha scritta non l'ha chiusa.
//
// I messaggi di [jobs] e [secrets] finiscono a volte con il testo di un errore
// di libreria, che non ha punteggiatura: attaccarci il rimedio produrrebbe due
// frasi incollate («…unknown time zone Europe/Roma Il fuso è un nome IANA…»),
// che è il modo più economico di far sembrare sciatto un messaggio corretto.
func terminate(message string) string {
	trimmed := strings.TrimRight(message, " ")
	if trimmed == "" || strings.HasSuffix(trimmed, ".") || strings.HasSuffix(trimmed, ":") {
		return trimmed
	}
	return trimmed + "."
}

// remedy è la correzione da aggiungere ai messaggi che arrivano da [jobs].
//
// Quei messaggi nascono per il corpo JSON di una richiesta API, dove il client
// è un form che il campo lo evidenzia da solo e l'utente lo vede sullo schermo
// mentre lo compila. Qui il destinatario è una persona davanti a un terminale
// dopo un `git push`, che deve riaprire un file e ribattere qualcosa: quello che
// le manca non è il nome della regola, è la riga da scrivere.
//
// La tabella è indicizzata per `campo/codice`, e ricade sul solo codice quando
// la correzione non dipende dal campo. Restituire stringa vuota è legittimo: i
// messaggi che già contengono l'elenco dei valori ammessi non hanno bisogno di
// altro.
func remedy(field, code string) string {
	if text, ok := remedies[field+"/"+code]; ok {
		return text
	}
	return remedies["*/"+code]
}

// I suggerimenti sulle durate compaiono in più punti — `defaults` e ogni job —
// e sono gli stessi: due copie che divergono darebbero due consigli diversi per
// lo stesso campo a due righe di distanza.
const (
	everyHint   = "Un intervallo si scrive con l'unità, per esempio `every: 10s`, `every: 5m`, `every: 1h`."
	timeoutHint = "Un timeout si scrive con l'unità, per esempio `timeout: 30s`."
)

// overlapHint spiega `on_overlap` (R41). Non è una costante perché i valori
// ammessi vengono dall'elenco di internal/jobs: riscriverli qui a mano
// significherebbe che un valore aggiunto al dominio non compare nel
// suggerimento, e il file direbbe all'utente che non esiste.
func overlapHint() string {
	return fmt.Sprintf(
		"Si scrive `on_overlap: %s`, cioè cosa fare quando un'occorrenza scatta mentre la precedente è ancora in corso. I valori ammessi sono %s; senza, vale `%s`.",
		jobs.DefaultOverlapPolicy, quoteJoin(overlapNames()), jobs.DefaultOverlapPolicy)
}

var remedies = map[string]string{
	"name/required":       "Aggiungi `name: nome-del-job`: è la chiave su cui il sync decide se creare, aggiornare o disattivare, e rinominarla equivale a cancellare il job e crearne un altro.",
	"name/too_long":       "Accorcialo: per esempio `daily-digest` invece della frase intera.",
	"name/invalid_format": "Per esempio `daily-digest`, `report.mensile`, `sync_crm`.",

	"schedule/invalid_schedule": "Un'espressione cron ha cinque campi separati da spazi — minuto, ora, giorno del mese, mese, giorno della settimana — e va fra virgolette perché comincia con un numero: per esempio `schedule: \"0 9 * * *\"` sono le nove di ogni giorno.",
	"timezone/invalid_schedule": "Usa un nome IANA, per esempio `timezone: Europe/Rome`, `timezone: America/New_York` oppure `timezone: UTC`.",
	"every/invalid_interval":    everyHint,

	"environments/required": "Scrivi `environments: [production]`.",

	"request.url/required":           "Scrivi `request: { url: https://esempio.it/hook }`.",
	"request.url/invalid_format":     "Un URL completo comincia con `https://` e contiene un host, per esempio `https://api.esempio.it/tasks/digest`.",
	"request.url/unsupported_scheme": "Scrivi l'URL per intero, a cominciare da `https://` oppure `http://`: Postqron esegue solo chiamate HTTP (SPEC §10), e lo schema non può venire da un segreto.",
	"request.url/too_long":           "Sposta i parametri lunghi nel corpo della richiesta.",
	"request.url/target_not_allowed": "Controlla che l'URL punti a un host pubblico: Postqron chiama solo destinazioni raggiungibili da internet, non indirizzi di rete interna.",
	"request.headers/invalid_name":   "Per esempio `Authorization` oppure `X-Api-Key`: il nome di una testata non contiene spazi né due punti.",
	"request.headers/reserved_name":  "Toglila: la calcola l'esecutore al momento della chiamata.",
	"request.headers/invalid_value":  "Togli l'a capo: il valore di una testata sta su una riga sola.",
	"request.body/too_long":          "Manda meno dati, oppure fai scaricare il resto all'endpoint che stai chiamando.",

	"timeout/out_of_range":     timeoutHint,
	"timeout/invalid_format":   "Le durate di questo file sono in secondi interi: `30s`, `5m`, `1h`.",
	"retries.max/out_of_range": "Per esempio `retries: { max: 3 }`; `max: 0` significa nessun nuovo tentativo.",

	"on_overlap/unknown_value": "`skip` salta l'occorrenza in eccesso, `queue` la fa aspettare il proprio turno, `allow` la lascia partire insieme alla precedente.",

	"*/invalid_reference":        "Un riferimento a un segreto si scrive `${NOME}` in maiuscolo, per esempio `${API_TOKEN}`.",
	"*/reference_in_header_name": "Scrivi la testata con il suo nome e metti il riferimento nel valore, per esempio `Authorization: \"Bearer ${API_TOKEN}\"`.",
	"*/unknown_secret":           "Correggi il nome, oppure crea il segreto fra quelli del workspace prima di riferirlo dal file.",

	"*/too_long":  "Accorcialo entro il limite indicato.",
	"*/too_many":  "Togline qualcuno.",
	"*/duplicate": "Toglilo dall'elenco: ripeterlo non raddoppia niente, e per gli ambienti significherebbe due chiamate identiche a ogni occorrenza.",
}

// ------------------------------------------------------- controlli sul file

// rejectDuplicateNames segnala due job con lo stesso nome.
//
// `name` è l'identità del job (SPEC §9): è la chiave su cui la riconciliazione
// decide se creare, aggiornare o disattivare. Due voci con lo stesso nome sono
// due schedulazioni che si contendono la stessa identità, e qualunque cosa il
// sync ne facesse — l'ultima vince, la prima vince, due job — sarebbe una
// regola che l'utente non ha scritto da nessuna parte.
func rejectDuplicateNames(errs *errorList, entries []Entry) {
	first := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		if entry.Job.Name == "" {
			continue
		}
		if previous, seen := first[entry.Job.Name]; seen {
			errs.add(entry.Position, entry.Path+".name", CodeDuplicateName,
				"C'è già un job che si chiama %q alla riga %d. Il nome è l'identità del job e dev'essere unico nel file: dàgliene uno diverso, oppure unisci i due job in uno.",
				entry.Job.Name, previous.Line)
			continue
		}
		first[entry.Job.Name] = entry
	}
}

// checkJobCount applica il tetto di piano al numero di job (SPEC §8, R15).
//
// Il tetto è sull'utente, non sul file: contano anche i job creati da dashboard
// e quelli che arrivano da altri repository, che è ciò che [Options.OtherJobs]
// porta dentro. Il messaggio lo dice, perché «il piano consente 20 job» detto a
// chi nel file ne ha scritti 5 sarebbe incomprensibile.
func checkJobCount(errs *errorList, root *mapping, entries []Entry, declared int, opts Options) {
	if declared == 0 {
		return
	}
	limit, isLimit := jobs.AsPlanLimit(opts.Plan.CheckJobCount(opts.OtherJobs + declared - 1))
	if !isLimit {
		return
	}

	// L'errore si ancora al primo job che non ci sta: è quello da togliere, e
	// indicare invece l'inizio dell'elenco lascerebbe da capire quale.
	position, path := at(root.node), "jobs"
	if allowed := *opts.Plan.MaxJobs - opts.OtherJobs; allowed >= 0 && allowed < len(entries) {
		position, path = entries[allowed].Position, entries[allowed].Path
	}

	message := fmt.Sprintf("Il piano %s consente %d job in tutto e questo file ne dichiara %d",
		planLabel(opts.Plan), *opts.Plan.MaxJobs, declared)
	if opts.OtherJobs > 0 {
		message += fmt.Sprintf(", oltre ai %d che hai già fuori da questo file", opts.OtherJobs)
	}
	message += fmt.Sprintf(". Togline %d, oppure passa a un piano superiore.", opts.OtherJobs+declared-*opts.Plan.MaxJobs)

	errs.addLimit(position, path, limit, message)
}

// planRemedy aggiunge al rifiuto di un limite di piano la correzione che
// riguarda **il file**, accanto a quella che riguarda l'abbonamento.
//
// I messaggi di [jobs.Plan] finiscono con «passa a un piano superiore», che è
// vero e che è l'unica risposta che l'API può dare a un form. Qui c'è anche
// l'altra: un file si può correggere, e chi non vuole cambiare piano deve poter
// leggere quale riga togliere.
func planRemedy(limit *jobs.PlanLimitError) string {
	switch limit.Limit {
	case jobs.LimitResolution:
		return "Nel file, l'alternativa è allargare `every` fino alla risoluzione concessa."
	case jobs.LimitEnvironments:
		return "Nel file, l'alternativa è scrivere `environments: [production]`."
	default:
		return ""
	}
}

func planLabel(plan jobs.Plan) string {
	if plan.Name != "" {
		return plan.Name
	}
	return plan.Code
}

// ----------------------------------------------------------------- ancore

// anchor è il punto del file a cui un campo del dominio corrisponde.
type anchor struct {
	Position
	Path string
}

// anchors lega i campi di [jobs.FieldError] e [secrets.FieldError] — `name`,
// `request.url`, `retries.max` — al punto del file da cui vengono.
//
// È l'unica cosa che questo package aggiunge alla validazione, ed è tutto ciò
// che serve: le regole stanno altrove, la riga sta qui. La chiave vuota è la
// posizione del job intero, ed è la risposta quando non c'è niente di più
// preciso da indicare.
type anchors map[string]anchor

func (a anchors) set(field string, position Position, path string) {
	a[field] = anchor{Position: position, Path: path}
}

// inherit copia le ancore di `defaults`, che valgono per ogni job che non
// sovrascrive quel campo.
func (a anchors) inherit(from anchors) {
	for field, value := range from {
		a[field] = value
	}
}

// lookup risale dal campo al blocco che lo contiene finché non trova una
// posizione: `request.headers.Authorization` ricade su `request.headers`, poi
// su `request`, poi sul job.
func (a anchors) lookup(field string) anchor {
	for {
		if found, ok := a[field]; ok {
			return found
		}
		cut := strings.LastIndexByte(field, '.')
		if cut < 0 {
			break
		}
		field = field[:cut]
	}
	return a[""]
}

// ---------------------------------------------------------------- supporto

// referenceSyntax riconosce un `${...}` in un testo. È volutamente più larga di
// [secrets.ValidName]: qui serve a sapere se c'è un riferimento, non se è
// scritto bene — di quello risponde [secrets.NameSet.Validate], che è l'unico
// posto in cui la sintassi dei riferimenti è definita.
var referenceSyntax = regexp.MustCompile(`\$\{[^}]*\}`)

// probeURL sostituisce i riferimenti con un segnaposto, per poter validare la
// **forma** di un URL che a riposo contiene dei `${VAR}`.
//
// Il valore restituito non viene conservato né eseguito: serve solo a rispondere
// alle domande che non dipendono dal valore del segreto — c'è lo schema? c'è un
// host? la lunghezza sta nei limiti? Il segnaposto è una lettera perché deve
// essere valido ovunque un riferimento possa comparire, host compreso.
func probeURL(raw string) string {
	if !strings.Contains(raw, "${") {
		return raw
	}
	return referenceSyntax.ReplaceAllString(raw, "x")
}

// authorityHasReference indica se un riferimento cade nell'host o nella porta,
// cioè nella parte che decide con chi si parla.
func authorityHasReference(raw string) bool {
	rest := raw
	if _, after, found := strings.Cut(raw, "://"); found {
		rest = after
	}
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
		rest = rest[:cut]
	}
	return referenceSyntax.MatchString(rest)
}

func environmentNames() []string { return stringsOf(jobs.Environments) }
func methodNames() []string      { return stringsOf(jobs.Methods) }
func backoffNames() []string     { return stringsOf(jobs.Backoffs) }
func channelNames() []string     { return stringsOf(jobs.AlertChannels) }
func overlapNames() []string     { return stringsOf(jobs.OverlapPolicies) }

func stringsOf[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}
