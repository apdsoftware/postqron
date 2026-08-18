// Package emailrender compila le email di Postqron a partire dai template
// versionati in emails/templates/ (R19, R20).
//
// # Due famiglie, e la domanda obbligatoria
//
// Le email non sono tutte la stessa cosa: le quattro transazionali di R21 non
// hanno un link di disiscrizione (privacy policy §2.7), quella di marketing ne
// ha sempre uno (§2.8). Il piè di pagina lo sceglie il layout a partire da
// [KindOf], e **nessun evento si compila senza aver dichiarato la propria
// natura**. Il perché la domanda sia obbligatoria invece che facoltativa sta in
// [Kind], ed è la cosa più importante di questo package.
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
//
// Un evento che non ha dichiarato la propria natura in [kinds] **non si compila
// affatto**: vedi [Kind] per il perché la domanda «è marketing?» è obbligatoria.
func (r *Renderer) Render(event Event, language string, data any) (Message, error) {
	language = NormalizeLanguage(language)

	kind, declared := KindOf(event)
	if !declared {
		return Message{}, fmt.Errorf(
			"evento %q senza natura dichiarata: aggiungilo a `kinds` dicendo se è %q o %q. "+
				"Non c'è un valore predefinito perché sbagliarlo significa o un link di disiscrizione "+
				"su un avviso di sicurezza, o una promozione senza (privacy policy §2.7 e §2.8)",
			event, KindTransactional, KindMarketing)
	}

	base := baseContext{
		Language:  language,
		Site:      r.site,
		Accent:    accents[event],
		Year:      r.now().UTC().Year(),
		Marketing: kind == KindMarketing,
		catalog:   r.catalog,
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
	case EventProductUpdate:
		typed, err := coerce[ProductUpdateData](event, data)
		if err != nil {
			return Message{}, err
		}
		// `validate` ha già preteso che l'indirizzo ci sia e sia utilizzabile:
		// qui si porta nel contesto, dove il layout lo trova. È l'unico punto in
		// cui un link di disiscrizione entra in un'email, ed è raggiungibile
		// solo da un evento che [kinds] dichiara di marketing.
		base.UnsubscribeURL = typed.UnsubscribeURL
		ctx = &productUpdateContext{baseContext: base, Data: typed}
	default:
		// Irraggiungibile finché [kinds] e questo switch elencano gli stessi
		// eventi, e `TestOgniEventoDichiaraLaPropriaNatura` tiene il conto. Resta
		// perché la coerenza fra i due elenchi è una proprietà verificata, non
		// una garanzia del compilatore.
		return Message{}, fmt.Errorf("evento sconosciuto: %q", event)
	}

	// Le due incoerenze, guardate insieme perché sono la stessa proprietà vista
	// dai due lati. Oggi nessuna delle due è raggiungibile — un solo ramo dello
	// switch valorizza il campo, e lo fa con un valore già convalidato — e i
	// controlli ci sono per l'edizione che verrà: il secondo evento di
	// marketing, aggiunto da chi non ha letto questa funzione, è esattamente il
	// caso in cui il link si dimentica. Costano un confronto, e difendono due
	// frasi di un documento legale.
	if base.Marketing && base.UnsubscribeURL == "" {
		return Message{}, fmt.Errorf(
			"evento %q è di marketing e non ha un link di disiscrizione: §2.8 lo promette in ogni messaggio", event)
	}
	if !base.Marketing && base.UnsubscribeURL != "" {
		return Message{}, fmt.Errorf(
			"evento %q è transazionale e porta un link di disiscrizione: §2.7 dice che da queste email "+
				"non ci si disiscrive, e un link che l'utente userebbe gli toglierebbe gli avvisi del servizio", event)
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

// Text traduce una chiave del catalogo nella lingua indicata.
//
// Esiste per la pagina di disiscrizione (§2.8), che è l'unica superficie fuori
// da un'email a dover parlare le stesse cinque lingue con gli stessi testi. Fare
// altrimenti avrebbe voluto dire un secondo catalogo, con le sue chiavi e la sua
// regola di ricaduta: due cataloghi che dicono la stessa cosa divergono, e a
// divergere sarebbe stata proprio la frase che spiega che le email transazionali
// continuano ad arrivare.
//
// Vale la regola dell'email: lingua sconosciuta ricade sull'inglese, e la
// ricaduta è **per chiave**.
func (r *Renderer) Text(language, key string, args ...string) (string, error) {
	return r.catalog.text(NormalizeLanguage(language), key, args)
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
