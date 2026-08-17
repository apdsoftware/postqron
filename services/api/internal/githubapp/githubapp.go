// Package githubapp legge file dai repository degli utenti autenticandosi come
// GitHub App (R12, R13; docs/CREDENTIALS.md §5).
//
// # Perché esiste
//
// Il webhook di R11 porta l'identità del repository e l'`installation_id`, **non
// il contenuto**: il payload di una push contiene i commit, non i file. Per
// riconciliare bisogna quindi andare a prendere `cron.yaml` nel repository, e
// per prenderlo bisogna avere il diritto di leggerlo — cosa che una GitHub App
// ottiene in due passaggi, non in uno:
//
//  1. l'App firma un JWT con la propria chiave privata (RS256). Quel JWT dice
//     «sono l'App numero N» e non dà accesso a nessun repository;
//  2. con quel JWT l'App chiede un **token di installazione**, che vale un'ora
//     ed è limitato ai repository su cui *quell'utente* ha installato l'App.
//
// La separazione è il motivo per cui si usa una App e non un personal access
// token: il token che legge il repository di un cliente non può leggere quello
// di un altro, e nessuno dei due sopravvive a un'ora.
//
// # Cosa non fa
//
// Non conosce `cron.yaml`, non conosce i job, non conosce il database. È un
// client HTTP con una politica di autenticazione: la riconciliazione è
// internal/reposync, che riceve i byte e li dà a internal/cronyaml.
//
// # Sui segreti
//
// La chiave privata e i token di installazione non compaiono in nessun log e in
// nessun messaggio d'errore, nemmeno troncati. [Client.LogValue] è ciò che il
// logger vede se qualcuno logga il client per intero.
package githubapp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Variabili d'ambiente della GitHub App (docs/CREDENTIALS.md §5).
//
// La chiave privata è un **percorso** e non il PEM inline: un PEM multiriga in
// una variabile d'ambiente sopravvive male al passaggio da `.env`, da systemd e
// da Docker, e ogni forma di codifica che lo renderebbe monoriga è un posto in
// cui la chiave finisce per essere manipolata a mano.
const (
	AppIDEnvVar          = "GITHUB_APP_ID"
	PrivateKeyPathEnvVar = "GITHUB_APP_PRIVATE_KEY_PATH"
)

// DefaultBaseURL è l'API pubblica di GitHub. È sovrascrivibile per i test, non
// per GitHub Enterprise: quel caso non è nel prodotto.
const DefaultBaseURL = "https://api.github.com"

// DefaultMaxBytes è il tetto alla dimensione di un file scaricato.
//
// Serve perché il corpo della risposta arriva da un repository che non
// controlliamo: senza tetto, un file da un gigabyte diventa un gigabyte di
// memoria del nostro processo. Il chiamante passa il proprio — la
// riconciliazione usa [cronyaml.MaxFileSize], che è il limite vero — e questo è
// solo la riserva per chi non lo passa.
const DefaultMaxBytes = 1 << 20

// Errori riconoscibili dal chiamante.
var (
	// ErrNotConfigured indica che l'ambiente non descrive nessuna GitHub App.
	ErrNotConfigured = errors.New("githubapp: GitHub App non configurata")

	// ErrForbidden indica un'installazione che non esiste più o che non dà
	// accesso al repository richiesto. Non è un guasto nostro e ripetere la
	// richiesta non la farà riuscire: è l'utente che ha disinstallato l'App o le
	// ha tolto il repository.
	ErrForbidden = errors.New("githubapp: l'installazione non dà accesso al repository")
)

// Options configura il [Client].
type Options struct {
	// AppID è l'identificativo numerico della GitHub App (`GITHUB_APP_ID`).
	AppID int64

	// PrivateKey è la chiave con cui l'App firma i propri JWT. Non viene mai
	// registrata né trasmessa: serve solo a firmare.
	PrivateKey *rsa.PrivateKey

	// BaseURL è la radice dell'API. Vuoto vale [DefaultBaseURL].
	BaseURL string

	// MaxBytes è il tetto alla dimensione di un file. Zero vale
	// [DefaultMaxBytes].
	MaxBytes int64

	// HTTPClient è il trasporto. nil costruisce un client con un timeout
	// esplicito: un client senza timeout è una richiesta che può non tornare
	// mai, e questa gira dentro la lavorazione di un webhook.
	HTTPClient *http.Client

	Logger *slog.Logger

	// Now sostituisce l'orologio. Serve ai test: la validità di un JWT e la
	// scadenza di un token sono definite in termini di tempo, e provarle con
	// l'orologio vero significa provarle una volta sola.
	Now func() time.Time
}

// requestTimeout è il tetto a una singola chiamata verso GitHub, quando il
// chiamante non passa un client proprio.
//
// Sta sotto il tempo che il livello HTTP concede alla lavorazione di una
// consegna (`webhookProcessingTimeout`, oggi 8 secondi), e deliberatamente ben
// sotto: una push richiede **due** chiamate — il token e il file — più la
// transazione di riconciliazione. Una singola chiamata che si prendesse tutto
// il budget lascerebbe scadere la lavorazione prima che l'esito della consegna
// venga registrato, e la ripetizione successiva la ritroverebbe in stato «in
// lavorazione» invece che fallita.
//
// È comunque una riserva: il contesto passato da chi chiama porta già la
// scadenza vera, ed è quella a governare.
const requestTimeout = 5 * time.Second

// Client parla con l'API di GitHub per conto della App.
//
// È sicuro da usare da più goroutine: la cache dei token è protetta, ed è la
// sola cosa mutabile.
type Client struct {
	appID   int64
	key     *rsa.PrivateKey
	baseURL string
	max     int64
	http    *http.Client
	log     *slog.Logger
	now     func() time.Time

	mu     sync.Mutex
	tokens map[int64]installationToken
}

// New costruisce il client.
func New(opts Options) (*Client, error) {
	if opts.AppID <= 0 {
		return nil, fmt.Errorf("githubapp: %s deve essere un intero positivo", AppIDEnvVar)
	}
	if opts.PrivateKey == nil {
		return nil, errors.New("githubapp: la chiave privata è obbligatoria")
	}

	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Client{
		appID:   opts.AppID,
		key:     opts.PrivateKey,
		baseURL: baseURL,
		max:     maxBytes,
		http:    httpClient,
		log:     logger,
		now:     now,
		tokens:  make(map[int64]installationToken),
	}, nil
}

// LoadFromEnv costruisce il client dalle variabili di docs/CREDENTIALS.md §5.
//
// **Restituisce (nil, nil) se l'App non è configurata.** È la stessa scelta di
// [githubhookpg.NewService] per il segreto del webhook, e per la stessa ragione:
// la macchina di sviluppo di chi non ha la GitHub App deve avviarsi, non
// fallire. Chi riceve nil lo dice nel log e prosegue senza sincronizzare.
//
// Se invece le variabili ci sono ma sono **sbagliate** — un identificativo che
// non è un numero, un file di chiave che non si legge — l'errore risale e
// ferma l'avvio: una configurazione a metà che si degrada in silenzio produce
// un prodotto in cui il sync non funziona e nessuno sa perché.
func LoadFromEnv(getenv func(string) string, opts Options) (*Client, error) {
	rawID := strings.TrimSpace(getenv(AppIDEnvVar))
	keyPath := strings.TrimSpace(getenv(PrivateKeyPathEnvVar))
	if rawID == "" || keyPath == "" {
		return nil, nil
	}

	appID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || appID <= 0 {
		return nil, fmt.Errorf("githubapp: %s non è un identificativo valido", AppIDEnvVar)
	}

	// Il contenuto del file non compare nell'errore: un PEM in un log è una
	// chiave privata in un log.
	pemBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("githubapp: chiave privata non leggibile da %s: %w", PrivateKeyPathEnvVar, err)
	}
	key, err := ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, err
	}

	opts.AppID = appID
	opts.PrivateKey = key
	return New(opts)
}

// ParsePrivateKey legge una chiave RSA in PEM.
//
// GitHub distribuisce la chiave in PKCS#1 (`-----BEGIN RSA PRIVATE KEY-----`),
// ma un passaggio da `openssl pkcs8` la trasforma in PKCS#8 e resta la stessa
// chiave: rifiutarla costringerebbe chi la converte per abitudine a scoprire da
// un errore oscuro perché.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("githubapp: la chiave privata non è in formato PEM")
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// L'errore di x509 non contiene materiale della chiave, ma non dice
		// nemmeno niente di utile: quello che serve è quali formati accettiamo.
		return nil, errors.New("githubapp: la chiave privata non è RSA in PKCS#1 o PKCS#8")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("githubapp: la chiave privata non è RSA")
	}
	return key, nil
}

// LogValue è ciò che il logger vede del client: mai la chiave, mai i token.
func (c *Client) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("app_id", c.appID),
		slog.String("base_url", c.baseURL),
	)
}
