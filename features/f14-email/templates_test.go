package email

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func testRenderer(t *testing.T) *Renderer {
	t.Helper()
	source, err := os.Open("../f01-brand/tokens/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	brand, err := LoadBrandFromF1(
		source,
		"Postqron",
		"https://assets.example.test/logo-primary.svg",
	)
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := NewRenderer(brand)
	if err != nil {
		t.Fatal(err)
	}
	return renderer
}

func testMessage(channel Channel, templateID TemplateID) Message {
	return Message{
		ID:              "email_1",
		IdempotencyKey:  "event:1",
		Channel:         channel,
		Template:        templateID,
		TemplateVersion: "1.0.0",
		Recipient: Recipient{
			ID:    "account_1",
			Email: "persona@example.test",
			Name:  "Persona",
		},
		Data: TemplateData{
			Heading:     "Il tuo riepilogo",
			Intro:       "Abbiamo completato l’operazione.",
			Body:        "Puoi consultare i dettagli nel tuo spazio.",
			ActionLabel: "Apri Postqron",
			ActionURL:   "https://app.example.test/activity",
		},
		CreatedAt:   time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		MaxAttempts: 5,
	}
}

func TestRendererProducesResponsiveAccessibleEscapedEmail(t *testing.T) {
	message := testMessage(ChannelTransactional, TemplatePublicationFailed)
	message.Data.Detail = `<script>alert("x")</script>`

	rendered, err := testRenderer(t).Render(message)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{
		`<html lang="it">`,
		`name="viewport"`,
		`@media only screen and (max-width: 620px)`,
		`role="presentation"`,
		`role="note"`,
		`alt="Postqron"`,
		`min-height:44px`,
		`&lt;script&gt;alert`,
	} {
		if !strings.Contains(rendered.HTML, wanted) {
			t.Fatalf("HTML does not contain %q", wanted)
		}
	}
	for _, forbidden := range []string{"ZgotmplZ", `<script>alert`, "Annulla l’iscrizione"} {
		if strings.Contains(rendered.HTML, forbidden) {
			t.Fatalf("HTML unexpectedly contains %q", forbidden)
		}
	}
	if !strings.Contains(rendered.Text, "Dettaglio: <script>") {
		t.Fatalf("plain-text alternative was not rendered: %q", rendered.Text)
	}
	if len(rendered.Headers) != 0 {
		t.Fatalf("transactional headers = %#v, want no marketing headers", rendered.Headers)
	}
}

func TestRendererSeparatesMarketingAndRequiresUnsubscribe(t *testing.T) {
	message := testMessage(ChannelMarketing, TemplateMarketingUpdate)
	if _, err := testRenderer(t).Render(message); !errors.Is(err, ErrUnsubscribeRequired) {
		t.Fatalf("Render() error = %v, want ErrUnsubscribeRequired", err)
	}
	message.Data.UnsubscribeURL = "https://app.example.test/email/unsubscribe?token=opaque"
	rendered, err := testRenderer(t).Render(message)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("missing one-click unsubscribe header: %#v", rendered.Headers)
	}
	if !strings.Contains(rendered.HTML, "Annulla l’iscrizione") ||
		!strings.Contains(rendered.Text, "Annulla l'iscrizione") {
		t.Fatal("marketing alternatives do not expose unsubscribe")
	}

	message.Channel = ChannelTransactional
	if _, err := testRenderer(t).Render(message); !errors.Is(err, ErrTemplateChannel) {
		t.Fatalf("cross-channel Render() error = %v, want ErrTemplateChannel", err)
	}
}

func TestLoadBrandRejectsMissingF1ValuesInsteadOfFallingBack(t *testing.T) {
	_, err := LoadBrandFromF1(
		strings.NewReader(`{"color":{"semantic":{"light":{}}}}`),
		"Postqron",
		"https://assets.example.test/logo.svg",
	)
	if err == nil {
		t.Fatal("LoadBrandFromF1() accepted incomplete F1 tokens")
	}
}
