package marketing

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"html/template"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

//go:embed unsubscribe.html.tmpl
var unsubscribeTemplate string

// PageState è ciò che la pagina di disiscrizione deve dire.
//
// Sono quattro perché quattro sono le situazioni in cui ci si può trovare
// aprendo quel link, e dirle con lo stesso testo sarebbe stato peggio che
// tacere: chi ha già revocato deve sapere che non c'è niente da fare, e chi ha
// un link scaduto deve sapere che il suo diritto resta e come esercitarlo.
type PageState string

const (
	// PageConfirm chiede conferma. È ciò che risponde la `GET`, e il motivo per
	// cui la `GET` non revoca: vedi [Service.Preview].
	PageConfirm PageState = "confirm"
	// PageDone è la revoca appena eseguita.
	PageDone PageState = "done"
	// PageAlready è la revoca già in vigore. Non è un errore, è il secondo clic.
	PageAlready PageState = "already"
	// PageInvalid è il token che non si verifica.
	PageInvalid PageState = "invalid"
)

// Texts è la parte di [emailrender.Renderer] che serve alla pagina.
//
// La pagina attinge allo **stesso** catalogo delle email, e non a uno suo: le
// frasi che deve dire — che la revoca ferma solo il marketing, che le
// transazionali continuano — sono le stesse che il piè di pagina dell'email
// dice, e due cataloghi che dicono la stessa cosa divergono.
type Texts interface {
	Text(language, key string, args ...string) (string, error)
}

// Page compila la pagina di disiscrizione nelle cinque lingue.
type Page struct {
	texts Texts
	site  emailrender.Site
	tmpl  *template.Template
}

// NewPage costruisce il compilatore della pagina.
func NewPage(texts Texts, site emailrender.Site) (*Page, error) {
	if texts == nil {
		return nil, errors.New("marketing: NewPage richiede i testi")
	}
	if err := site.Validate(); err != nil {
		return nil, fmt.Errorf("marketing: site non valido: %w", err)
	}
	tmpl, err := template.New("unsubscribe").Parse(unsubscribeTemplate)
	if err != nil {
		return nil, fmt.Errorf("marketing: template della pagina di disiscrizione: %w", err)
	}
	return &Page{texts: texts, site: site, tmpl: tmpl}, nil
}

// pageContext è ciò che il template vede.
type pageContext struct {
	Language    string
	ProductName string
	Title       string
	Heading     string
	Paragraphs  []string
	Support     string

	// ShowForm, Action, Token e Confirm valgono solo per [PageConfirm]: sono il
	// pulsante che esegue la revoca, e la revoca è una `POST`.
	ShowForm bool
	Action   string
	Token    string
	Confirm  string
}

// Render compila la pagina.
//
// `token` serve solo a [PageConfirm], dove finisce nel campo nascosto del form.
// Negli altri tre stati non c'è niente da confermare e il token non compare
// nella risposta.
func (p *Page) Render(state PageState, language, token string) ([]byte, error) {
	language = emailrender.NormalizeLanguage(language)

	support, err := p.texts.Text(language, "unsubscribe.support", "support", p.site.SupportEmail)
	if err != nil {
		return nil, fmt.Errorf("marketing: pagina di disiscrizione: %w", err)
	}

	ctx := pageContext{
		Language:    language,
		ProductName: p.site.ProductName,
		Support:     support,
		Action:      UnsubscribePath,
	}

	// Le chiavi per stato. La tabella è esplicita e non composta a partire dal
	// nome dello stato: uno stato nuovo deve costringere chi lo aggiunge a
	// scrivere che cosa dice, non a ereditare per convenzione una chiave che
	// magari non esiste.
	var err2 error
	text := func(key string, args ...string) string {
		value, e := p.texts.Text(language, key, args...)
		if e != nil && err2 == nil {
			err2 = e
		}
		return value
	}

	switch state {
	case PageConfirm:
		ctx.Title = text("unsubscribe.title")
		ctx.Heading = text("unsubscribe.question", "product", p.site.ProductName)
		ctx.Paragraphs = []string{text("unsubscribe.scope")}
		ctx.Confirm = text("unsubscribe.confirm")
		ctx.ShowForm = true
		ctx.Token = token

	case PageDone:
		ctx.Title = text("unsubscribe.done_title")
		ctx.Heading = text("unsubscribe.done_title")
		ctx.Paragraphs = []string{
			text("unsubscribe.done_body"),
			text("unsubscribe.scope"),
			text("unsubscribe.resubscribe"),
		}

	case PageAlready:
		ctx.Title = text("unsubscribe.already_title")
		ctx.Heading = text("unsubscribe.already_title")
		ctx.Paragraphs = []string{
			text("unsubscribe.already_body"),
			text("unsubscribe.resubscribe"),
		}

	case PageInvalid:
		ctx.Title = text("unsubscribe.invalid_title")
		ctx.Heading = text("unsubscribe.invalid_title")
		ctx.Paragraphs = []string{
			text("unsubscribe.invalid_body", "support", p.site.SupportEmail),
		}

	default:
		return nil, fmt.Errorf("marketing: stato della pagina sconosciuto: %q", state)
	}

	if err2 != nil {
		return nil, fmt.Errorf("marketing: pagina di disiscrizione: %w", err2)
	}

	var buf bytes.Buffer
	if err := p.tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("marketing: pagina di disiscrizione: %w", err)
	}
	return buf.Bytes(), nil
}
