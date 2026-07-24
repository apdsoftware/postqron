package email

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"strings"
	texttemplate "text/template"
)

type Renderer struct {
	brand Brand
	html  *template.Template
	text  *texttemplate.Template
}

type templateDefinition struct {
	Subject string
	HTML    string
	Text    string
}

type renderView struct {
	Brand     renderBrand
	Recipient Recipient
	Data      TemplateData
}

type renderBrand struct {
	Name          string
	LogoURL       template.URL
	Canvas        template.CSS
	Surface       template.CSS
	Text          template.CSS
	TextMuted     template.CSS
	Brand         template.CSS
	TextInverse   template.CSS
	Border        template.CSS
	Danger        template.CSS
	DangerSurface template.CSS
	FontFamily    template.CSS
}

func NewRenderer(brand Brand) (*Renderer, error) {
	if strings.TrimSpace(brand.Name) == "" ||
		strings.TrimSpace(brand.LogoURL) == "" ||
		strings.TrimSpace(brand.FontFamily) == "" {
		return nil, errors.New("resolved F1 brand is required")
	}
	if err := validateHTTPSURL(brand.LogoURL); err != nil {
		return nil, errors.New("resolved F1 logo URL must use HTTPS")
	}
	if strings.ContainsAny(brand.FontFamily, "{};<>") ||
		strings.Contains(strings.ToLower(brand.FontFamily), "url(") {
		return nil, errors.New("resolved F1 font family is invalid")
	}
	for _, value := range []string{
		brand.Canvas,
		brand.Surface,
		brand.Text,
		brand.TextMuted,
		brand.Brand,
		brand.TextInverse,
		brand.Border,
		brand.Danger,
		brand.DangerSurface,
	} {
		if !validHexColor(value) {
			return nil, errors.New("resolved F1 brand contains an invalid color")
		}
	}

	htmlRoot := template.New("email").Option("missingkey=error")
	textRoot := texttemplate.New("email").Option("missingkey=error")
	for id, definition := range templateDefinitions {
		var err error
		htmlRoot, err = htmlRoot.New(string(id)).Parse(definition.HTML)
		if err != nil {
			return nil, fmt.Errorf("parse HTML template %s: %w", id, err)
		}
		textRoot, err = textRoot.New(string(id)).Parse(definition.Text)
		if err != nil {
			return nil, fmt.Errorf("parse text template %s: %w", id, err)
		}
	}
	return &Renderer{brand: brand, html: htmlRoot, text: textRoot}, nil
}

func (renderer *Renderer) Render(message Message) (RenderedMessage, error) {
	if err := validateMessage(message); err != nil {
		return RenderedMessage{}, err
	}
	definition, ok := templateDefinitions[message.Template]
	if !ok {
		return RenderedMessage{}, ErrTemplateChannel
	}

	view := renderView{
		Brand:     safeRenderBrand(renderer.brand),
		Recipient: message.Recipient,
		Data:      message.Data,
	}
	var htmlOutput bytes.Buffer
	if err := renderer.html.ExecuteTemplate(&htmlOutput, string(message.Template), view); err != nil {
		return RenderedMessage{}, fmt.Errorf("render HTML template: %w", err)
	}
	var textOutput bytes.Buffer
	if err := renderer.text.ExecuteTemplate(&textOutput, string(message.Template), view); err != nil {
		return RenderedMessage{}, fmt.Errorf("render text template: %w", err)
	}

	headers := map[string]string{}
	if message.Channel == ChannelMarketing {
		headers["List-Unsubscribe"] = "<" + message.Data.UnsubscribeURL + ">"
		headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
	}
	return RenderedMessage{
		MessageID:       message.ID,
		IdempotencyKey:  message.IdempotencyKey,
		Channel:         message.Channel,
		Template:        message.Template,
		TemplateVersion: message.TemplateVersion,
		Recipient:       message.Recipient,
		Subject:         renderSubject(definition.Subject, view),
		HTML:            htmlOutput.String(),
		Text:            textOutput.String(),
		Headers:         headers,
	}, nil
}

// Values are converted to trusted template types only after NewRenderer has
// validated the F1 palette and LoadBrandFromF1 has validated the HTTPS asset
// URL and font syntax.
func safeRenderBrand(brand Brand) renderBrand {
	return renderBrand{
		Name:          brand.Name,
		LogoURL:       template.URL(brand.LogoURL),
		Canvas:        template.CSS(brand.Canvas),
		Surface:       template.CSS(brand.Surface),
		Text:          template.CSS(brand.Text),
		TextMuted:     template.CSS(brand.TextMuted),
		Brand:         template.CSS(brand.Brand),
		TextInverse:   template.CSS(brand.TextInverse),
		Border:        template.CSS(brand.Border),
		Danger:        template.CSS(brand.Danger),
		DangerSurface: template.CSS(brand.DangerSurface),
		FontFamily:    template.CSS(brand.FontFamily),
	}
}

func renderSubject(source string, view renderView) string {
	parsed, err := texttemplate.New("subject").Option("missingkey=error").Parse(source)
	if err != nil {
		return ""
	}
	var output bytes.Buffer
	if err := parsed.Execute(&output, view); err != nil {
		return ""
	}
	return output.String()
}

const baseHTML = `<!doctype html>
<html lang="it">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>{{.Data.Heading}}</title>
  <style>
    @media only screen and (max-width: 620px) {
      .email-shell { width: 100% !important; }
      .email-content { padding: 24px 20px !important; }
      .email-action { display: block !important; width: 100% !important; box-sizing: border-box !important; }
    }
    @media (prefers-reduced-motion: reduce) {
      * { scroll-behavior: auto !important; }
    }
  </style>
</head>
<body style="margin:0;padding:0;background:{{.Brand.Canvas}};color:{{.Brand.Text}};font-family:{{.Brand.FontFamily}};">
  <div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">{{.Data.Intro}}</div>
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="width:100%;background:{{.Brand.Canvas}};">
    <tr>
      <td align="center" style="padding:24px 12px;">
        <table role="presentation" class="email-shell" width="600" cellpadding="0" cellspacing="0" border="0" style="width:600px;max-width:100%;background:{{.Brand.Surface}};border:1px solid {{.Brand.Border}};border-radius:16px;">
          <tr>
            <td class="email-content" style="padding:32px 36px;">
              <img src="{{.Brand.LogoURL}}" width="152" alt="{{.Brand.Name}}" style="display:block;width:152px;max-width:100%;height:auto;border:0;margin:0 0 32px;">
              <main>
                <h1 style="margin:0 0 16px;font-size:28px;line-height:1.2;color:{{.Brand.Text}};">{{.Data.Heading}}</h1>
                <p style="margin:0 0 16px;font-size:16px;line-height:1.6;color:{{.Brand.Text}};">{{.Data.Intro}}</p>
                {{if .Data.Body}}<p style="margin:0 0 20px;font-size:16px;line-height:1.6;color:{{.Brand.Text}};">{{.Data.Body}}</p>{{end}}
                {{if .Data.Detail}}<div role="note" style="margin:0 0 20px;padding:16px;border-left:4px solid {{.Brand.Danger}};background:{{.Brand.DangerSurface}};font-size:15px;line-height:1.6;color:{{.Brand.Text}};">{{.Data.Detail}}</div>{{end}}
                {{if .Data.ActionURL}}<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0;"><tr><td>
                  <a class="email-action" href="{{.Data.ActionURL}}" style="display:inline-block;min-height:44px;padding:12px 20px;background:{{.Brand.Brand}};color:{{.Brand.TextInverse}};font-size:16px;font-weight:600;line-height:20px;text-align:center;text-decoration:none;border-radius:10px;">{{.Data.ActionLabel}}</a>
                </td></tr></table>{{end}}
              </main>
              <footer style="margin-top:32px;padding-top:20px;border-top:1px solid {{.Brand.Border}};">
                <p style="margin:0;font-size:13px;line-height:1.6;color:{{.Brand.TextMuted}};">Messaggio inviato da {{.Brand.Name}}.</p>
                {{if .Data.UnsubscribeURL}}<p style="margin:10px 0 0;font-size:13px;line-height:1.6;color:{{.Brand.TextMuted}};">Non vuoi più ricevere comunicazioni promozionali? <a href="{{.Data.UnsubscribeURL}}" style="color:{{.Brand.Brand}};">Annulla l’iscrizione</a>.</p>{{end}}
              </footer>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`

const baseText = `{{.Data.Heading}}

{{.Data.Intro}}{{if .Data.Body}}

{{.Data.Body}}{{end}}{{if .Data.Detail}}

Dettaglio: {{.Data.Detail}}{{end}}{{if .Data.ActionURL}}

{{.Data.ActionLabel}}: {{.Data.ActionURL}}{{end}}

Messaggio inviato da {{.Brand.Name}}.{{if .Data.UnsubscribeURL}}

Annulla l'iscrizione alle comunicazioni promozionali: {{.Data.UnsubscribeURL}}{{end}}
`

var templateDefinitions = map[TemplateID]templateDefinition{
	TemplateWelcome: {
		Subject: "Benvenuto su {{.Brand.Name}}",
		HTML:    baseHTML,
		Text:    baseText,
	},
	TemplatePlanChanged: {
		Subject: "Il tuo piano {{.Brand.Name}} è stato aggiornato",
		HTML:    baseHTML,
		Text:    baseText,
	},
	TemplatePublicationFailed: {
		Subject: "Un post non è stato pubblicato",
		HTML:    baseHTML,
		Text:    baseText,
	},
	TemplateSecurityAlert: {
		Subject: "Avviso di sicurezza per il tuo account",
		HTML:    baseHTML,
		Text:    baseText,
	},
	TemplateMarketingUpdate: {
		Subject: "{{.Data.Heading}}",
		HTML:    baseHTML,
		Text:    baseText,
	},
}
