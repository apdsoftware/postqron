package emailrender

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// baseContext è ciò che ogni template vede sotto il punto.
//
// Le traduzioni passano da metodi e non da funzioni del template: una FuncMap
// che chiude sulla lingua andrebbe riassociata a ogni invio con Funcs(), che
// muta il template già compilato e lo rende inutilizzabile da due goroutine
// insieme. Un metodo sul contesto porta la lingua con sé e lascia i template
// immutabili dopo il caricamento.
type baseContext struct {
	// Language è la lingua effettiva del messaggio, dopo la normalizzazione.
	Language string
	// Site sono i valori di prodotto: nome, URL, indirizzo di supporto.
	Site Site
	// Subject è l'oggetto già compilato. Vale solo durante il rendering del
	// corpo HTML, dove serve a riempire il <title>.
	Subject string
	// Accent è il colore della fascia e del pulsante di questo evento.
	Accent string
	// Year è l'anno del piè di pagina, preso dall'orologio del renderer.
	Year int

	catalog *catalog
}

func (c *baseContext) setSubject(subject string) { c.Subject = subject }

// T traduce una chiave, sostituendo i segnaposto con le coppie nome/valore
// passate di seguito: {{.T "welcome.heading" "product" .Site.ProductName}}.
func (c baseContext) T(key string, args ...string) (string, error) {
	return c.catalog.text(c.Language, key, args)
}

// TN traduce la forma singolare o plurale di una chiave. Il conteggio è
// disponibile nel testo come `{count}` senza passarlo fra gli argomenti.
func (c baseContext) TN(key string, count int, args ...string) (string, error) {
	return c.catalog.plural(c.Language, key, count, args)
}

// URL compone un indirizzo del sito pubblico nella lingua del messaggio.
//
// Il prefisso di lingua non è un vezzo: i frontend sono generati staticamente e
// ogni lingua è un albero di rotte a sé (SPEC §8-bis). Un link senza prefisso
// finirebbe sulla radice, che è solo uno smistatore e rimanderebbe l'utente
// alla lingua indovinata dal browser invece che a quella che ha scelto.
func (c baseContext) URL(path string) string {
	return joinURL(c.Site.PublicBaseURL, c.Language, path)
}

// AppURL compone un indirizzo della dashboard nella lingua del messaggio.
func (c baseContext) AppURL(path string) string {
	return joinURL(c.Site.AppBaseURL, c.Language, path)
}

func joinURL(base, language, path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + "/" + language + path
}

// Time formatta un istante per l'email.
//
// UTC e formato ISO, sempre, con l'unità dichiarata accanto. La data locale del
// destinatario non è nota al renderer, e il formato numerico corto è la cosa
// più facile da leggere al contrario: 03/04 è il tre aprile per un lettore e il
// quattro marzo per un altro. Un alert che sbaglia il giorno di dodici mesi su
// dodici è peggio di uno un po' burocratico.
func (c baseContext) Time(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04 MST")
}

// ---------------------------------------------------------------- benvenuto

type welcomeContext struct {
	baseContext
	Data WelcomeData
}

// Greeting sceglie fra saluto con nome e saluto impersonale.
//
// Il nome può mancare: la registrazione chiede l'email, non l'anagrafe. Un
// template che scrivesse «Hi ,» avrebbe un'aria di posta automatica mal
// configurata proprio nel primo messaggio che l'utente riceve.
func (c welcomeContext) Greeting() (string, error) {
	if strings.TrimSpace(c.Data.RecipientName) == "" {
		return c.T("common.greeting_anonymous")
	}
	return c.T("common.greeting_named", "name", c.Data.RecipientName)
}

// ------------------------------------------------------------- job fallito

type jobFailedContext struct {
	baseContext
	Data JobFailedData
}

// EnvironmentLabel traduce l'ambiente: «Production», non «production».
func (c jobFailedContext) EnvironmentLabel() (string, error) {
	return c.T("common.environment_" + string(c.Data.Environment))
}

// FailureReason descrive l'esito dell'ultimo tentativo nella lingua del
// messaggio, a partire dalla classificazione e non da un errore testuale.
func (c jobFailedContext) FailureReason() (string, error) {
	if c.Data.FailureKind == FailureHTTPStatus {
		return c.T("job_failed.failure_http_status", "status", strconv.Itoa(c.Data.HTTPStatus))
	}
	return c.T("job_failed.failure_" + string(c.Data.FailureKind))
}

// JobURL è il link alla cronologia delle esecuzioni del job.
func (c jobFailedContext) JobURL() string {
	return c.AppURL("/jobs/" + url.PathEscape(c.Data.JobID) + "/executions")
}

// ----------------------------------------------------------- cambio di piano

type planChangedContext struct {
	baseContext
	Data PlanChangedData
}

// -------------------------------------------------------------- sicurezza

type securityAlertContext struct {
	baseContext
	Data SecurityAlertData
}

// KindDescription racconta in una frase che cosa è successo.
func (c securityAlertContext) KindDescription() (string, error) {
	return c.T("security_alert.kind_" + string(c.Data.Kind))
}
