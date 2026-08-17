// Package emailrender compila le email transazionali di Postqron a partire dai
// template versionati in emails/templates/ (R19, R20).
//
// # Che cosa produce, e che cosa non fa
//
// Il risultato è un Message già completo — oggetto, HTML, testo — pronto a
// diventare il corpo di POST /email/send. Mailronix è esclusivamente un motore
// di recapito: non ospita template, non sostituisce variabili, non conosce il
// prodotto (R20). Di conseguenza qui non c'è niente che assomigli a un
// `template_id`, e questo package non parla con nessuno: l'invio è la issue
// #419, l'aggancio agli eventi di dominio la #420.
//
// # HTML per client di posta
//
// I template sono scritti per Outlook e Gmail, non per un browser: tabelle al
// posto di flexbox, CSS inline, larghezza fissa a 600px, nessun JavaScript,
// nessuna immagine necessaria. Il ragionamento su ogni vincolo sta in
// emails/templates/README.md, accanto ai file a cui si applica.
//
// # Cinque lingue, una struttura
//
// La lingua del destinatario decide quella dell'email (R33). Struttura e stile
// stanno nei template, i testi in emails/templates/locales/<lingua>.json, e
// l'inglese è insieme la sorgente e il ripiego: una chiave non tradotta esce in
// inglese, per chiave e non per file. Oggi solo en.json è popolato — le altre
// quattro lingue sono la issue #446 e trovano la struttura già pronta.
//
// # Segreti
//
// Nessuna struttura dati di questo package ha un campo per una chiave, un token
// o una password, ed è una proprietà verificata da un test: un template non può
// interpolare ciò che non gli viene mai passato.
package emailrender

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"slices"
	"strings"
	texttemplate "text/template"
	"time"
)

// Message è un'email compilata, pronta per il client Mailronix (#419).
type Message struct {
	// Subject è l'oggetto, già tradotto.
	Subject string
	// HTML è il corpo HTML completo, da mandare come `html_body`.
	HTML string
	// Text è il corpo testuale, da mandare come `text_body`.
	Text string
	// Language è la lingua in cui il messaggio è stato davvero compilato, dopo
	// la normalizzazione della lingua richiesta.
	Language string
}

// Renderer compila i template caricati una volta all'avvio.
//
// È sicuro usarlo da più goroutine: dopo New nessun campo viene più scritto, e
// il contesto di ogni rendering è un valore a sé.
type Renderer struct {
	site    Site
	catalog *catalog
	html    map[Event]*template.Template
	text    map[Event]*texttemplate.Template
	now     func() time.Time
}

// Option modifica un Renderer in costruzione.
type Option func(*Renderer)

// WithClock sostituisce l'orologio, che serve solo per l'anno del piè di
// pagina. Esiste per rendere i test deterministici.
func WithClock(now func() time.Time) Option {
	return func(r *Renderer) { r.now = now }
}

// button e detail sono le uniche funzioni del template, e sono senza stato:
// impacchettano gli argomenti per i frammenti condivisi definiti nel layout,
// dove Go non offre un modo per passare più di un valore.
type buttonArgs struct {
	Accent string
	URL    string
	Label  string
}

type detailArgs struct {
	Label string
	Value any
}

var templateFuncs = map[string]any{
	"button": func(accent, url, label string) buttonArgs {
		return buttonArgs{Accent: accent, URL: url, Label: label}
	},
	"detail": func(label string, value any) detailArgs {
		return detailArgs{Label: label, Value: value}
	},
}

// New carica template e testi da un filesystem con il contenuto di
// emails/templates/.
//
// Il caricamento è integrale e severo: mancare un file o una chiave è un errore
// qui, all'avvio, e non al primo invio in produzione.
func New(fsys fs.FS, site Site, opts ...Option) (*Renderer, error) {
	if err := site.Validate(); err != nil {
		return nil, fmt.Errorf("site non valido: %w", err)
	}

	catalog, err := loadCatalog(fsys)
	if err != nil {
		return nil, err
	}

	r := &Renderer{
		site:    site,
		catalog: catalog,
		html:    make(map[Event]*template.Template, len(Events())),
		text:    make(map[Event]*texttemplate.Template, len(Events())),
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(r)
	}

	for _, event := range Events() {
		htmlTemplate, err := template.New(string(event)).
			Funcs(templateFuncs).
			ParseFS(fsys, "layout.html.tmpl", string(event)+".html.tmpl")
		if err != nil {
			return nil, fmt.Errorf("template HTML di %s: %w", event, err)
		}
		r.html[event] = htmlTemplate

		textTemplate, err := texttemplate.New(string(event)).
			Funcs(templateFuncs).
			ParseFS(fsys, "layout.txt.tmpl", string(event)+".txt.tmpl")
		if err != nil {
			return nil, fmt.Errorf("template testuale di %s: %w", event, err)
		}
		r.text[event] = textTemplate
	}

	return r, nil
}

// NewFromDir carica i template dalla directory indicata.
func NewFromDir(dir string, site Site, opts ...Option) (*Renderer, error) {
	fsys, err := openDir(dir)
	if err != nil {
		return nil, err
	}
	return New(fsys, site, opts...)
}

// Render compila l'email di un evento nella lingua indicata.
//
// data deve essere il tipo dell'evento — WelcomeData per EventWelcome e così
// via. Una lingua sconosciuta non è un errore: ricade sull'inglese, che è il
// comportamento previsto dalla spec quando nessuna lingua corrisponde.
func (r *Renderer) Render(event Event, language string, data any) (Message, error) {
	language = NormalizeLanguage(language)

	base := baseContext{
		Language: language,
		Site:     r.site,
		Accent:   accents[event],
		Year:     r.now().UTC().Year(),
		catalog:  r.catalog,
	}

	var ctx interface{ setSubject(string) }
	switch event {
	case EventWelcome:
		typed, err := coerce[WelcomeData](event, data)
		if err != nil {
			return Message{}, err
		}
		ctx = &welcomeContext{baseContext: base, Data: typed}
	case EventJobFailed:
		typed, err := coerce[JobFailedData](event, data)
		if err != nil {
			return Message{}, err
		}
		ctx = &jobFailedContext{baseContext: base, Data: typed}
	case EventPlanChanged:
		typed, err := coerce[PlanChangedData](event, data)
		if err != nil {
			return Message{}, err
		}
		ctx = &planChangedContext{baseContext: base, Data: typed}
	case EventSecurityAlert:
		typed, err := coerce[SecurityAlertData](event, data)
		if err != nil {
			return Message{}, err
		}
		ctx = &securityAlertContext{baseContext: base, Data: typed}
	default:
		return Message{}, fmt.Errorf("evento sconosciuto: %q", event)
	}

	// L'oggetto per primo: il corpo HTML lo rimette nel <title>, così la
	// scheda di un client che apre il messaggio a tutto schermo mostra la
	// stessa riga che l'utente ha letto nell'elenco.
	subject, err := execText(r.text[event], "subject", ctx)
	if err != nil {
		return Message{}, fmt.Errorf("oggetto di %s: %w", event, err)
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return Message{}, fmt.Errorf("oggetto di %s: risultato vuoto", event)
	}
	ctx.setSubject(subject)

	body, err := execText(r.text[event], "layout", ctx)
	if err != nil {
		return Message{}, fmt.Errorf("corpo testuale di %s: %w", event, err)
	}

	var htmlBuf bytes.Buffer
	if err := r.html[event].ExecuteTemplate(&htmlBuf, "layout", ctx); err != nil {
		return Message{}, fmt.Errorf("corpo HTML di %s: %w", event, err)
	}

	return Message{
		Subject:  subject,
		HTML:     htmlBuf.String(),
		Text:     strings.TrimSpace(body) + "\n",
		Language: language,
	}, nil
}

// coerce estrae il tipo atteso da data, convalidandolo.
func coerce[T interface{ validate() error }](event Event, data any) (T, error) {
	var zero T
	typed, ok := data.(T)
	if !ok {
		return zero, fmt.Errorf("evento %s: atteso %T, ricevuto %T", event, zero, data)
	}
	if err := typed.validate(); err != nil {
		return zero, fmt.Errorf("evento %s: %w", event, err)
	}
	return typed, nil
}

func execText(t *texttemplate.Template, name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// NormalizeLanguage riporta un tag di lingua a una delle cinque supportate.
//
// Accetta le forme con regione — `it-IT`, `fr_CA`, `EN` — perché è ciò che
// arriva da un profilo utente o da un header, e ripiega sull'inglese quando
// nessuna corrisponde: è la regola della spec, applicata alle email invece che
// alle rotte (SPEC §8-bis, R31).
func NormalizeLanguage(language string) string {
	tag := strings.ToLower(strings.TrimSpace(language))
	if index := strings.IndexAny(tag, "-_"); index >= 0 {
		tag = tag[:index]
	}
	if slices.Contains(Languages, tag) {
		return tag
	}
	return DefaultLanguage
}
