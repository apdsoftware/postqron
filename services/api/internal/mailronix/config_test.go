package mailronix

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// env costruisce una Getenv da una mappa, senza toccare l'ambiente del
// processo di test.
func env(values map[string]string) Getenv {
	return func(key string) string { return values[key] }
}

func TestLoadConfig(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		EnvAPIKey:    testKey,
		EnvAPIURL:    "https://api.mailronix.com/",
		EnvFromEmail: "  noreply@postqron.com  ",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIKey != testKey {
		t.Errorf("APIKey non caricata")
	}
	if cfg.BaseURL != "https://api.mailronix.com" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.From != "noreply@postqron.com" {
		t.Errorf("From = %q", cfg.From)
	}
}

// TestLoadConfigSenzaChiave: in sviluppo la chiave non c'è, ed è una condizione
// prevista, non un errore di configurazione.
func TestLoadConfigSenzaChiave(t *testing.T) {
	for _, value := range []string{"", "   "} {
		_, err := LoadConfig(env(map[string]string{
			EnvAPIKey:    value,
			EnvFromEmail: "noreply@postqron.com",
		}))
		if !errors.Is(err, ErrNotConfigured) {
			t.Errorf("chiave %q: errore = %v, atteso ErrNotConfigured", value, err)
		}
	}
}

func TestLoadConfigURLPredefinito(t *testing.T) {
	cfg, err := LoadConfig(env(map[string]string{
		EnvAPIKey:    testKey,
		EnvFromEmail: "noreply@postqron.com",
	}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q, atteso %q", cfg.BaseURL, DefaultBaseURL)
	}
}

func TestLoadConfigConfigurazioneSbagliata(t *testing.T) {
	cases := map[string]map[string]string{
		"mittente mancante": {EnvAPIKey: testKey},
		"mittente con nome visualizzato": {
			EnvAPIKey:    testKey,
			EnvFromEmail: "PostQron <noreply@postqron.com>",
		},
		"URL senza schema": {
			EnvAPIKey:    testKey,
			EnvFromEmail: "noreply@postqron.com",
			EnvAPIURL:    "api.mailronix.com",
		},
	}
	for name, values := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadConfig(env(values))
			if err == nil {
				t.Fatal("errore atteso")
			}
			if errors.Is(err, ErrNotConfigured) {
				t.Fatal("una configurazione sbagliata non è una configurazione assente")
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := map[string]time.Duration{
		"":       0,
		"   ":    0,
		"0":      0,
		"-5":     0,
		"30":     30 * time.Second,
		"boh":    0,
		"  12  ": 12 * time.Second,
	}
	for value, want := range cases {
		if got := parseRetryAfter(value); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, atteso %v", value, got, want)
		}
	}

	// La forma con data: si accetta solo se è nel futuro.
	futuro := time.Now().UTC().Add(90 * time.Second).Format(http.TimeFormat)
	if got := parseRetryAfter(futuro); got <= 0 || got > 90*time.Second {
		t.Errorf("parseRetryAfter(data futura) = %v, atteso circa 90s", got)
	}
	passato := time.Now().UTC().Add(-time.Hour).Format(http.TimeFormat)
	if got := parseRetryAfter(passato); got != 0 {
		t.Errorf("parseRetryAfter(data passata) = %v, atteso 0", got)
	}
}

func TestDefaultBackoff(t *testing.T) {
	if got := defaultBackoff(1); got != 500*time.Millisecond {
		t.Errorf("defaultBackoff(1) = %v", got)
	}
	if got := defaultBackoff(0); got != 500*time.Millisecond {
		t.Errorf("defaultBackoff(0) = %v: un tentativo fuori scala non deve azzerare l'attesa", got)
	}
	// Il tetto vale anche per i valori assurdi, dove lo shift traboccherebbe.
	for _, attempt := range []int{5, 10, 64, 1000} {
		if got := defaultBackoff(attempt); got != 8*time.Second {
			t.Errorf("defaultBackoff(%d) = %v, atteso il tetto di 8s", attempt, got)
		}
	}
}

func TestRetryableSuErroriEstranei(t *testing.T) {
	// Nel dubbio si preferisce un'email non partita a una partita due volte.
	if Retryable(errors.New("qualcosa")) {
		t.Error("Retryable = true su un errore sconosciuto")
	}
	if Retryable(nil) {
		t.Error("Retryable = true su nil")
	}
	if code := Code(errors.New("qualcosa")); code != "" {
		t.Errorf("Code = %q, atteso vuoto", code)
	}
}
