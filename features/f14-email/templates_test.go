package email

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
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
		source, "Postqron", "https://assets.example.test/logo-primary.svg",
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

func testMessage(templateID TemplateID) Message {
	count := int64(12345)
	amount := int64(123456)
	return Message{
		ID: "email_1", IdempotencyKey: "event:1",
		Channel: ChannelTransactional, Template: templateID,
		TemplateVersion: "1.0.0",
		Recipient: Recipient{
			ID: "account_1", Email: "persona@example.test", Name: "Persona",
			Locale: "it-IT",
		},
		Data: TemplateData{
			Detail: "Workspace Europa", ActionURL: "https://app.example.test/activity",
			OccurredAt: time.Date(2026, 7, 24, 12, 30, 0, 0, time.UTC),
			TimeZone:   "Europe/Rome", Count: &count, AmountMinor: &amount,
			Currency: "EUR",
		},
		CreatedAt:   time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC),
		MaxAttempts: 5,
	}
}

func TestEveryTemplateRendersAllLocalesWithCompleteAlternatives(t *testing.T) {
	renderer := testRenderer(t)
	for templateID := range templateCatalog {
		for _, locale := range SupportedLocales {
			t.Run(string(templateID)+"/"+string(locale), func(t *testing.T) {
				message := testMessage(templateID)
				message.Recipient.Locale = string(locale)
				rendered, err := renderer.Render(message)
				if err != nil {
					t.Fatal(err)
				}
				if rendered.Locale != locale || rendered.Subject == "" ||
					rendered.Preheader == "" || rendered.HTML == "" || rendered.Text == "" {
					t.Fatalf("incomplete rendering: %#v", rendered)
				}
				for _, wanted := range []string{
					`<html lang="` + string(locale) + `"`,
					`name="viewport"`, `role="presentation"`, `min-height:44px`,
					rendered.Preheader, "Workspace Europa",
					"https://app.example.test/activity",
				} {
					if !strings.Contains(rendered.HTML, wanted) {
						t.Fatalf("HTML does not contain %q", wanted)
					}
				}
				for _, forbidden := range []string{"ZgotmplZ", "{{", "unsubscribe"} {
					if strings.Contains(strings.ToLower(rendered.HTML), forbidden) {
						t.Fatalf("HTML unexpectedly contains %q", forbidden)
					}
				}
			})
		}
	}
}

func TestLocaleFallbackReplayAndLocalizedValues(t *testing.T) {
	renderer := testRenderer(t)
	message := testMessage(TemplateBilling)
	message.Recipient.Locale = "pt-BR"
	rendered, err := renderer.Render(message)
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Locale != LocaleEnglish ||
		!strings.Contains(rendered.Text, "Jul 24, 2026, 2:30 PM CEST") ||
		!strings.Contains(rendered.Text, "12,345") ||
		!strings.Contains(rendered.Text, "1,234.56 EUR") {
		t.Fatalf("English fallback/localized values missing:\n%s", rendered.Text)
	}
	message.Recipient.Locale = "de-DE"
	german, err := renderer.Render(message)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(german.Text, "24.07.2026, 14:30 CEST") ||
		!strings.Contains(german.Text, "12.345") ||
		!strings.Contains(german.Text, "1.234,56 €") {
		t.Fatalf("German localized values missing:\n%s", german.Text)
	}
	if rendered.TemplateVersion != german.TemplateVersion ||
		rendered.IdempotencyKey != german.IdempotencyKey {
		t.Fatal("rendering changed immutable replay fields")
	}
}

func TestAccountVerificationRequiresHTTPSActionURL(t *testing.T) {
	renderer := testRenderer(t)
	message := testMessage(TemplateAccountVerification)
	message.Data.ActionURL = ""
	if _, err := renderer.Render(message); err == nil {
		t.Fatal("Render() accepted account verification without action URL")
	}
	message.Data.ActionURL = "http://app.example.test/verify?verification_token=abc123"
	if _, err := renderer.Render(message); err == nil {
		t.Fatal("Render() accepted account verification with non-HTTPS action URL")
	}
	message.Data.ActionURL = "https://app.example.test/verify?verification_token=abc123"
	rendered, err := renderer.Render(message)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{rendered.HTML, rendered.Text} {
		if !strings.Contains(body, "https://app.example.test/verify?verification_token=abc123") {
			t.Fatalf("verification action URL missing from rendered body: %q", body)
		}
	}
	if rendered.Subject == "" || rendered.Preheader == "" {
		t.Fatalf("account verification copy incomplete: %#v", rendered)
	}
}

func TestRendererEscapesLongFrenchAndGermanContent(t *testing.T) {
	renderer := testRenderer(t)
	for _, locale := range []string{"fr", "de"} {
		message := testMessage(TemplateOperationalAlert)
		message.Recipient.Locale = locale
		message.Data.Detail = strings.Repeat("Sehr longue information & ", 30) +
			`<script>alert("x")</script>`
		rendered, err := renderer.Render(message)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(rendered.HTML, "&lt;script&gt;") ||
			strings.Contains(rendered.HTML, "<script>alert") {
			t.Fatalf("%s detail was not escaped", locale)
		}
		if !strings.Contains(rendered.HTML, "@media only screen") {
			t.Fatalf("%s lost responsive rule", locale)
		}
	}
}

func TestLoadBrandRejectsMissingF1ValuesInsteadOfFallingBack(t *testing.T) {
	_, err := LoadBrandFromF1(
		strings.NewReader(`{"color":{"semantic":{"light":{}}}}`),
		"Postqron", "https://assets.example.test/logo.svg",
	)
	if err == nil {
		t.Fatal("LoadBrandFromF1() accepted incomplete F1 tokens")
	}
}

func TestNormalizedTemplateCatalogSnapshot(t *testing.T) {
	renderer := testRenderer(t)
	ids := make([]string, 0, len(templateCatalog))
	for templateID := range templateCatalog {
		ids = append(ids, string(templateID))
	}
	sort.Strings(ids)
	hash := sha256.New()
	for _, rawID := range ids {
		for _, locale := range SupportedLocales {
			message := testMessage(TemplateID(rawID))
			message.Recipient.Locale = string(locale)
			rendered, err := renderer.Render(message)
			if err != nil {
				t.Fatal(err)
			}
			normalized := strings.Join(strings.Fields(
				rawID+"\n"+string(locale)+"\n"+rendered.Subject+"\n"+
					rendered.Preheader+"\n"+rendered.HTML+"\n"+
					rendered.Text+"\n"+message.Data.ActionURL,
			), " ")
			_, _ = hash.Write([]byte(normalized))
		}
	}
	const expected = "0cf8be7cc33b5a560a02fb076755fc548066be0665b92437736d459d8a9eb52c"
	if actual := hex.EncodeToString(hash.Sum(nil)); actual != expected {
		t.Fatalf("normalized template snapshot = %s, want %s", actual, expected)
	}
}
