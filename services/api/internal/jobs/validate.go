package jobs

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/schedule"
)

// Limiti di forma. Sono gli stessi che i `CHECK` della migrazione 0005
// impongono, più quelli che il database non esprime (numero di header,
// lunghezza di un URL): il criterio è che nessuna richiesta accettata qui possa
// essere respinta là. Un 500 perché PostgreSQL ha rifiutato ciò che l'API aveva
// accettato è un difetto — l'errore deve arrivare come 400, con la ragione,
// prima della query.
const (
	// MaxNameLength è il tetto di `jobs_name_format_check`.
	MaxNameLength = 100
	// MaxDescriptionLength non ha un vincolo nel database: la colonna è `text`.
	// Il tetto esiste comunque, perché un campo libero senza limite è spazio su
	// disco offerto a chiunque abbia un account gratuito.
	MaxDescriptionLength = 2000
	// MaxURLLength è il limite pratico oltre il quale nessun server accetta la
	// riga di richiesta.
	MaxURLLength = 2048
	// MaxHeaders è il numero massimo di header di un target.
	MaxHeaders = 50
	// MaxHeaderNameLength e MaxHeaderValueLength limitano la singola voce.
	MaxHeaderNameLength  = 128
	MaxHeaderValueLength = 4096
	// MaxBodyLength è il corpo massimo della richiesta che il job invierà.
	MaxBodyLength = 16 << 10

	// MinTimeout e MaxTimeout sono i confini di `timeout_seconds` (0005), che
	// sono anche i tetti di esecuzione di R40.
	MinTimeout = 1 * time.Second
	MaxTimeout = 300 * time.Second

	// MaxRetriesAllowed è il tetto di `max_retries` (0005).
	MaxRetriesAllowed = 10
)

// nameFormat riproduce `jobs_name_format_check` della migrazione 0005.
//
// È una duplicazione, e va detto: la regola sta in due posti. La copia esiste
// perché la versione nel database produce un errore che nessun utente può
// leggere, e perché l'alternativa — provare l'INSERT e tradurre lo SQLSTATE —
// non permette di dire *quale* carattere non va. Il test
// TestNomeCoerenteConIlVincoloDelDatabase in internal/jobspg confronta le due
// contro il database vero, così che una modifica a una sola delle due rompa la
// suite.
var nameFormat = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`)

// headerNameFormat è il `token` di RFC 9110 §5.6.2: i caratteri che possono
// comparire nel nome di un header.
var headerNameFormat = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

// reservedHeaders sono gli header che il target non può dichiarare perché li
// decide l'esecutore.
//
// `Host` cambierebbe il bersaglio effettivo della richiesta senza cambiare
// l'URL, che è esattamente la forma di aggiramento che il blocco SSRF (R38,
// issue #455) deve poter escludere guardando l'URL. Gli altri sono calcolati
// dal client HTTP: sovrascriverli produce richieste malformate.
var reservedHeaders = []string{
	"host", "content-length", "connection", "transfer-encoding",
	"upgrade", "keep-alive", "proxy-authorization", "te", "trailer",
}

// TargetGuard decide se un URL di destinazione è raggiungibile.
//
// PostQron esegue richieste verso URL scelti dall'utente **dalla stessa
// macchina su cui girano API e database** (SPEC §3.7): senza questo controllo il
// prodotto è uno strumento d'attacco. Il controllo vero — indirizzo risolto e
// non nome, ripetuto su ogni redirect — è la issue #455 e non sta qui.
//
// Qui c'è l'interfaccia e il punto in cui viene chiamata, così che #455 abbia
// una sola riga da collegare in cmd/api. Con guard nil restano i soli controlli
// di forma (schema e host), che non sono una difesa: sono il minimo che il
// vincolo `jobs_url_scheme_check` già impone.
type TargetGuard interface {
	// CheckTarget restituisce un errore descrittivo se l'URL non è ammesso.
	CheckTarget(ctx context.Context, target *url.URL) error
}

// Validate verifica la forma di un job e la sua compatibilità con il piano.
//
// L'ordine conta: prima la forma, poi il piano. Un `every: 1s` scritto male —
// `every: 1x` — deve dire che la durata non si legge, non che il piano Free non
// la consente; e un job con dieci campi sbagliati non deve nascondere nove
// errori dietro il primo.
//
// I limiti di piano non finiscono fra i [FieldError]: sono un rifiuto di natura
// diversa, il client ci fa branching diverso, e hanno un tipo apposta
// ([PlanLimitError]).
func (j *Job) Validate(ctx context.Context, plan Plan, guard TargetGuard) error {
	j.Normalize()

	invalid := &ValidationError{}
	j.validateIdentity(invalid)
	j.validateSchedule(invalid)
	j.validateEnvironments(invalid)
	j.validateTarget(ctx, invalid, guard)
	j.validateExecution(invalid)

	if err := invalid.orNil(); err != nil {
		return err
	}

	// I limiti di piano si verificano solo su un job già valido: dire «il piano
	// Free non consente 1 secondo» a chi ha scritto un intervallo illeggibile
	// sarebbe una diagnosi sbagliata.
	if err := plan.CheckResolution(j.Every); err != nil {
		return err
	}
	return plan.CheckEnvironments(j.Environments)
}

// Normalize porta il job nella forma canonica in cui viene scritto.
//
// Serve a due cose: che l'utente non si trovi un nome con uno spazio in fondo
// che non riesce più a riscrivere, e che l'espressione cron soddisfi
// `jobs_schedule_shape_check` — che pretende cinque campi separati da spazi, e
// che rifiuterebbe `"0  9 * * *"` con due spazi se non lo normalizzassimo.
func (j *Job) Normalize() {
	j.Name = strings.TrimSpace(j.Name)
	j.Description = strings.TrimSpace(j.Description)
	j.Timezone = strings.TrimSpace(j.Timezone)
	j.URL = strings.TrimSpace(j.URL)
	j.Schedule = strings.Join(strings.Fields(j.Schedule), " ")

	if j.Timezone == "" {
		j.Timezone = "UTC"
	}
	if j.Headers == nil {
		j.Headers = map[string]string{}
	}
	if j.Environments == nil {
		j.Environments = []Environment{}
	}
	if j.AlertOnFailure == nil {
		j.AlertOnFailure = []AlertChannel{}
	}
}

func (j *Job) validateIdentity(invalid *ValidationError) {
	switch {
	case j.Name == "":
		invalid.add("name", "required", "il nome è obbligatorio: è l'identità stabile del job.")
	case len(j.Name) > MaxNameLength:
		invalid.add("name", "too_long", "il nome supera %d caratteri.", MaxNameLength)
	case !nameFormat.MatchString(j.Name):
		invalid.add("name", "invalid_format",
			"il nome può contenere lettere, cifre, punto, trattino e trattino basso, e deve cominciare e finire con una lettera o una cifra.")
	}

	if len(j.Description) > MaxDescriptionLength {
		invalid.add("description", "too_long", "la descrizione supera %d caratteri.", MaxDescriptionLength)
	}
}

// validateSchedule delega a internal/schedule invece di riscriverne le regole.
//
// È il validatore delle schedulazioni del prodotto (issue #387): riscrivere qui
// il controllo dei cinque campi significherebbe due parser che divergono, e
// l'API accetterebbe espressioni che il motore poi non sa eseguire.
func (j *Job) validateSchedule(invalid *ValidationError) {
	if _, err := schedule.Parse(j.Spec()); err != nil {
		switch {
		case errors.Is(err, schedule.ErrNoMode):
			invalid.add("schedule", "schedule_required",
				"serve `schedule` (espressione cron) oppure `every` (intervallo): esattamente uno dei due.")
		case errors.Is(err, schedule.ErrBothModes):
			invalid.add("schedule", "schedule_conflict",
				"`schedule` ed `every` sono mutuamente esclusivi: dichiararne uno solo.")
		case j.Every != 0:
			invalid.add("every", "invalid_interval", "%s", err.Error())
		default:
			// Il fuso è validato da Parse in entrambe le modalità: se
			// l'espressione cron c'è, l'errore può venire da lì o da lui.
			field := "schedule"
			if strings.Contains(err.Error(), "fuso orario") {
				field = "timezone"
			}
			invalid.add(field, "invalid_schedule", "%s", err.Error())
		}
	}
}

func (j *Job) validateEnvironments(invalid *ValidationError) {
	if len(j.Environments) == 0 {
		invalid.add("environments", "required", "serve almeno un ambiente.")
		return
	}
	seen := map[Environment]bool{}
	for _, env := range j.Environments {
		if !slices.Contains(Environments, env) {
			invalid.add("environments", "unknown_value",
				"ambiente %q sconosciuto: ammessi %s.", env, joinEnvironments(Environments))
			continue
		}
		if seen[env] {
			// Un duplicato non è un dettaglio estetico: ogni ambiente produce la
			// propria esecuzione per ciascuna occorrenza, quindi
			// `[production, production]` significherebbe due chiamate identiche.
			invalid.add("environments", "duplicate", "ambiente %q ripetuto.", env)
		}
		seen[env] = true
	}
}

func (j *Job) validateTarget(ctx context.Context, invalid *ValidationError, guard TargetGuard) {
	j.validateURL(ctx, invalid, guard)

	if !slices.Contains(Methods, j.Method) {
		invalid.add("request.method", "unknown_value",
			"metodo %q non ammesso: ammessi %s.", j.Method, joinMethods(Methods))
	}

	if len(j.Headers) > MaxHeaders {
		invalid.add("request.headers", "too_many", "al massimo %d header.", MaxHeaders)
	}
	// L'ordine di iterazione di una mappa in Go è casuale: senza l'ordinamento,
	// l'elenco degli errori cambierebbe a ogni richiesta e un client che ne
	// mostra il primo mostrerebbe un campo diverso ogni volta.
	for _, name := range slices.Sorted(maps.Keys(j.Headers)) {
		value := j.Headers[name]
		switch {
		case name == "" || !headerNameFormat.MatchString(name):
			invalid.add("request.headers", "invalid_name", "nome di header %q non valido.", name)
		case len(name) > MaxHeaderNameLength:
			invalid.add("request.headers", "too_long", "il nome di header %q supera %d caratteri.", name, MaxHeaderNameLength)
		case slices.Contains(reservedHeaders, strings.ToLower(name)):
			invalid.add("request.headers", "reserved_name",
				"l'header %q è deciso dall'esecutore e non può essere dichiarato dal job.", name)
		case len(value) > MaxHeaderValueLength:
			invalid.add("request.headers", "too_long", "il valore dell'header %q supera %d caratteri.", name, MaxHeaderValueLength)
		case strings.ContainsAny(value, "\r\n"):
			// Un a capo in un valore è una richiesta di iniettare header
			// aggiuntivi nella chiamata che faremo noi, dal nostro IP.
			invalid.add("request.headers", "invalid_value",
				"il valore dell'header %q contiene un a capo.", name)
		}
	}

	if len(j.Body) > MaxBodyLength {
		invalid.add("request.body", "too_long", "il corpo supera %d byte.", MaxBodyLength)
	}
}

func (j *Job) validateURL(ctx context.Context, invalid *ValidationError, guard TargetGuard) {
	if j.URL == "" {
		invalid.add("request.url", "required", "l'URL di destinazione è obbligatorio.")
		return
	}
	if len(j.URL) > MaxURLLength {
		invalid.add("request.url", "too_long", "l'URL supera %d caratteri.", MaxURLLength)
		return
	}
	target, err := url.Parse(j.URL)
	if err != nil {
		invalid.add("request.url", "invalid_format", "URL non leggibile: %v", err)
		return
	}
	scheme := strings.ToLower(target.Scheme)
	if scheme != "http" && scheme != "https" {
		// È anche `jobs_url_scheme_check`: PostQron non esegue comandi né
		// container, solo HTTP (SPEC §10).
		invalid.add("request.url", "unsupported_scheme",
			"sono ammessi solo gli schemi http e https: %q non è un target eseguibile.", target.Scheme)
		return
	}
	if target.Host == "" {
		invalid.add("request.url", "invalid_format", "l'URL non contiene un host.")
		return
	}
	if guard != nil {
		if err := guard.CheckTarget(ctx, target); err != nil {
			invalid.add("request.url", "target_not_allowed", "%s", err.Error())
		}
	}
}

func (j *Job) validateExecution(invalid *ValidationError) {
	switch {
	case j.Timeout < MinTimeout:
		invalid.add("timeout", "out_of_range", "il timeout minimo è %s.", FormatDuration(MinTimeout))
	case j.Timeout > MaxTimeout:
		invalid.add("timeout", "out_of_range", "il timeout massimo è %s.", FormatDuration(MaxTimeout))
	case j.Timeout%time.Second != 0:
		invalid.add("timeout", "invalid_format", "il timeout dev'essere un numero intero di secondi.")
	}

	if j.MaxRetries < 0 || j.MaxRetries > MaxRetriesAllowed {
		invalid.add("retries.max", "out_of_range", "i tentativi vanno da 0 a %d.", MaxRetriesAllowed)
	}
	if !slices.Contains(Backoffs, j.RetryBackoff) {
		invalid.add("retries.backoff", "unknown_value",
			"politica %q sconosciuta: ammesse %s.", j.RetryBackoff, joinBackoffs(Backoffs))
	}

	seen := map[AlertChannel]bool{}
	for _, channel := range j.AlertOnFailure {
		if !slices.Contains(AlertChannels, channel) {
			invalid.add("alerts.on_failure", "unknown_value",
				"canale %q sconosciuto: ammessi %s.", channel, joinChannels(AlertChannels))
			continue
		}
		if seen[channel] {
			invalid.add("alerts.on_failure", "duplicate", "canale %q ripetuto.", channel)
		}
		seen[channel] = true
	}
}

func joinEnvironments(values []Environment) string { return joinStringy(values) }
func joinMethods(values []Method) string           { return joinStringy(values) }
func joinBackoffs(values []Backoff) string         { return joinStringy(values) }
func joinChannels(values []AlertChannel) string    { return joinStringy(values) }

func joinStringy[T ~string](values []T) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, string(v))
	}
	return strings.Join(parts, ", ")
}
