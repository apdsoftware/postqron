package mailronix

import (
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
)

// Variabili d'ambiente della sezione Mailronix di docs/CREDENTIALS.md §2.
//
// Non passano da internal/config: quel package appartiene a un'altra issue, e
// vale qui la stessa ragione — e lo stesso precedente — di
// POSTQRON_TRUSTED_PROXIES in cmd/api.
const (
	EnvAPIKey    = "MAILRONIX_API_KEY"
	EnvAPIURL    = "MAILRONIX_API_URL"
	EnvFromEmail = "MAILRONIX_FROM_EMAIL"
)

// KeyPrefix è il prefisso delle chiavi di produzione, `mrx_live_<segreto>`.
const KeyPrefix = "mrx_live_"

// APIKey è la chiave di Mailronix, in un tipo che non si lascia stampare.
//
// Il valore non esce da questo package se non attraverso reveal, che è privato
// e chiamato in un punto solo: la composizione dell'intestazione Authorization.
// String, GoString e LogValue restituiscono tutti la forma oscurata, così né un
// `%v`, né un `%+v` su una struttura che la contiene, né uno slog.Any possono
// farla finire in un log per distrazione.
type APIKey string

// String restituisce la forma oscurata: il prefisso, che non è segreto perché
// dice solo di che tipo di chiave si tratta, e nient'altro. Mai un carattere
// del segreto, nemmeno i primi: bastano a restringere una ricerca.
func (k APIKey) String() string {
	if strings.TrimSpace(string(k)) == "" {
		return "(nessuna chiave)"
	}
	// `mrx_live_<segreto>` → `mrx_live_***`. Una chiave di forma diversa perde
	// tutto: non sapendo dove finisca il prefisso, non se ne conserva nulla.
	if parts := strings.SplitN(string(k), "_", 3); len(parts) == 3 {
		return parts[0] + "_" + parts[1] + "_***"
	}
	return "***"
}

// GoString copre `%#v`.
func (k APIKey) GoString() string { return k.String() }

// LogValue copre slog.
func (k APIKey) LogValue() slog.Value { return slog.StringValue(k.String()) }

// reveal è l'unico modo di ottenere il valore, e non esce dal package.
func (k APIKey) reveal() string { return string(k) }

// Config è la configurazione del client.
type Config struct {
	// APIKey autentica le chiamate, nel formato `mrx_live_<segreto>`.
	APIKey APIKey
	// BaseURL è la radice dell'API, senza barra finale. Vuoto significa
	// [DefaultBaseURL].
	BaseURL string
	// From è l'indirizzo mittente. Il suo dominio dev'essere verificato presso
	// Mailronix, altrimenti ogni invio riceve `403 domain_not_verified`.
	From string
}

// Validate rifiuta una configurazione che produrrebbe solo errori a runtime.
func (c Config) Validate() error {
	if strings.TrimSpace(string(c.APIKey)) == "" {
		return fmt.Errorf("mailronix: %s è obbligatoria", EnvAPIKey)
	}
	// Il prefisso `mrx_live_` non si impone come errore: una chiave di staging
	// potrebbe averne un altro, e rifiutarla qui bloccherebbe l'ambiente
	// sbagliato.
	parsed, err := url.Parse(c.baseURL())
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("mailronix: %s deve essere un URL http(s) con host: %q", EnvAPIURL, c.baseURL())
	}
	address, err := mail.ParseAddress(c.From)
	if err != nil {
		return fmt.Errorf("mailronix: %s non è un indirizzo valido: %w", EnvFromEmail, err)
	}
	if address.Address != c.From {
		// Il contratto dichiara `from` con `format: email`: si manda
		// l'indirizzo nudo, non `Nome <indirizzo>`, per non rischiare un `400`
		// — che non è ritentabile — da un validatore severo. È anche il motivo
		// per cui MAILRONIX_FROM_NAME non finisce nel payload.
		return fmt.Errorf(
			"mailronix: %s deve essere il solo indirizzo, senza nome visualizzato: %q",
			EnvFromEmail, c.From)
	}
	return nil
}

// baseURL normalizza la radice: senza barra finale, perché [Path] ne ha già
// una e due di fila non sono lo stesso URL.
func (c Config) baseURL() string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(c.BaseURL), "/")
	if trimmed == "" {
		return DefaultBaseURL
	}
	return trimmed
}

// Getenv è la firma di os.Getenv. Parametrizzarla rende [LoadConfig] testabile
// senza toccare l'ambiente del processo di test.
type Getenv func(string) string

// ErrNotConfigured segnala l'assenza di MAILRONIX_API_KEY.
//
// Non è un errore di configurazione ma una condizione prevista in sviluppo,
// dove nessuno vuole che `go run ./cmd/api` richieda una chiave di un servizio
// esterno. Il chiamante la riconosce e ripiega su un mailer che non recapita.
var ErrNotConfigured = errors.New("mailronix: " + EnvAPIKey + " non impostata")

// LoadConfig legge la configurazione dall'ambiente.
//
// Restituisce [ErrNotConfigured] se la chiave manca; ogni altro errore è una
// configurazione sbagliata, che è giusto far fallire all'avvio invece che al
// primo invio.
func LoadConfig(getenv Getenv) (Config, error) {
	key := strings.TrimSpace(getenv(EnvAPIKey))
	if key == "" {
		return Config{}, ErrNotConfigured
	}
	cfg := Config{
		APIKey:  APIKey(key),
		BaseURL: strings.TrimSuffix(strings.TrimSpace(getenv(EnvAPIURL)), "/"),
		From:    strings.TrimSpace(getenv(EnvFromEmail)),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
