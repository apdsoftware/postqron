package githubapp_test

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/githubapp"
)

// chiaveDiProva è generata una volta per l'intero pacchetto: RSA a 2048 bit
// costa qualche decimo di secondo, e generarla per ogni test renderebbe la
// suite lenta senza provare niente in più.
var chiaveDiProva = func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
}()

// finto è un GitHub minimo: emette token di installazione e serve un file.
type finto struct {
	server *httptest.Server

	contenuto  string
	statoFile  int
	statoToken int
	scadenza   time.Time

	tokenEmessi atomic.Int64
	fileLetti   atomic.Int64

	// ultimoJWT e ultimoToken sono ciò che il client ha presentato: è così che
	// un test verifica che la seconda chiamata riusi il token invece di
	// chiederne un altro.
	ultimoJWT   atomic.Value
	ultimoToken atomic.Value
	ultimoPath  atomic.Value
	ultimoRef   atomic.Value
}

func nuovoFinto(t *testing.T, contenuto string) *finto {
	t.Helper()
	f := &finto{contenuto: contenuto, statoFile: http.StatusOK, statoToken: http.StatusCreated}
	f.server = httptest.NewServer(http.HandlerFunc(f.servi))
	t.Cleanup(f.server.Close)
	return f
}

func (f *finto) servi(w http.ResponseWriter, r *http.Request) {
	autorizzazione := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")

	if strings.HasSuffix(r.URL.Path, "/access_tokens") {
		f.tokenEmessi.Add(1)
		f.ultimoJWT.Store(autorizzazione)
		if f.statoToken != http.StatusCreated {
			w.WriteHeader(f.statoToken)
			return
		}
		scadenza := f.scadenza
		if scadenza.IsZero() {
			scadenza = time.Now().Add(time.Hour)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "ghs-token-di-installazione",
			"expires_at": scadenza.Format(time.RFC3339),
		})
		return
	}

	f.fileLetti.Add(1)
	f.ultimoToken.Store(autorizzazione)
	f.ultimoPath.Store(r.URL.Path)
	f.ultimoRef.Store(r.URL.Query().Get("ref"))
	if f.statoFile != http.StatusOK {
		w.WriteHeader(f.statoFile)
		return
	}
	_, _ = w.Write([]byte(f.contenuto))
}

func (f *finto) client(t *testing.T, opts githubapp.Options) *githubapp.Client {
	t.Helper()
	opts.AppID = 12345
	opts.PrivateKey = chiaveDiProva
	opts.BaseURL = f.server.URL
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	}
	client, err := githubapp.New(opts)
	if err != nil {
		t.Fatalf("costruzione del client: %v", err)
	}
	return client
}

func TestUnFileVieneLetto(t *testing.T) {
	gh := nuovoFinto(t, "version: 1\njobs: []\n")
	client := gh.client(t, githubapp.Options{})

	contenuto, trovato, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234")
	if err != nil {
		t.Fatalf("FileAtRef: %v", err)
	}
	if !trovato {
		t.Fatal("trovato = false")
	}
	if string(contenuto) != "version: 1\njobs: []\n" {
		t.Errorf("contenuto = %q", contenuto)
	}
	if path := gh.ultimoPath.Load(); path != "/repos/acme/api/contents/cron.yaml" {
		t.Errorf("percorso richiesto = %v", path)
	}
	if ref := gh.ultimoRef.Load(); ref != "abc1234" {
		t.Errorf("ref richiesto = %v, atteso il commit della push", ref)
	}
}

// Il 404 è una risposta, non un guasto: distinguerli è ciò che permette a
// internal/reposync di non archiviare i job di un utente perché GitHub ha avuto
// trenta secondi difficili.
func TestUnFileAssenteNonEUnErrore(t *testing.T) {
	gh := nuovoFinto(t, "")
	gh.statoFile = http.StatusNotFound
	client := gh.client(t, githubapp.Options{})

	contenuto, trovato, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234")
	if err != nil {
		t.Fatalf("FileAtRef = %v, atteso nil: un file che non c'è non è un guasto", err)
	}
	if trovato {
		t.Error("trovato = true")
	}
	if contenuto != nil {
		t.Errorf("contenuto = %q, atteso nil", contenuto)
	}
}

func TestUnGuastoDiGitHubEUnErrore(t *testing.T) {
	gh := nuovoFinto(t, "")
	gh.statoFile = http.StatusBadGateway
	client := gh.client(t, githubapp.Options{})

	if _, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234"); err == nil {
		t.Fatal("FileAtRef = nil: un 502 va distinto da un file assente")
	}
}

func TestIlTokenDiInstallazioneVieneRiusato(t *testing.T) {
	gh := nuovoFinto(t, "version: 1\njobs: []\n")
	client := gh.client(t, githubapp.Options{})

	for range 3 {
		if _, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234"); err != nil {
			t.Fatalf("FileAtRef: %v", err)
		}
	}

	if emessi := gh.tokenEmessi.Load(); emessi != 1 {
		t.Errorf("token emessi = %d, atteso 1: la cache evita una chiamata in più per ogni push", emessi)
	}
	if letti := gh.fileLetti.Load(); letti != 3 {
		t.Errorf("file letti = %d, attesi 3", letti)
	}
	if token := gh.ultimoToken.Load(); token != "ghs-token-di-installazione" {
		t.Errorf("token presentato = %v", token)
	}
}

func TestUnTokenVicinoAllaScadenzaVieneRinnovato(t *testing.T) {
	gh := nuovoFinto(t, "version: 1\njobs: []\n")
	adesso := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	// Trenta secondi di vita residua: sotto il margine, quindi inservibile.
	gh.scadenza = adesso.Add(30 * time.Second)

	client := gh.client(t, githubapp.Options{Now: func() time.Time { return adesso }})

	for range 2 {
		if _, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234"); err != nil {
			t.Fatalf("FileAtRef: %v", err)
		}
	}
	if emessi := gh.tokenEmessi.Load(); emessi != 2 {
		t.Errorf("token emessi = %d, attesi 2: un token che scade fra 30 secondi arriva scaduto", emessi)
	}
}

// Un 401 sul file significa che il token in cache non vale più prima della sua
// scadenza dichiarata: si scarta e si riprova **una volta sola**.
func TestUnTokenRifiutatoVieneRinnovatoUnaVoltaSola(t *testing.T) {
	gh := nuovoFinto(t, "")
	gh.statoFile = http.StatusUnauthorized
	client := gh.client(t, githubapp.Options{})

	_, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234")
	if !errors.Is(err, githubapp.ErrForbidden) {
		t.Fatalf("FileAtRef = %v, atteso ErrForbidden", err)
	}
	if emessi := gh.tokenEmessi.Load(); emessi != 2 {
		t.Errorf("token emessi = %d, attesi 2: un solo nuovo tentativo, non un ciclo", emessi)
	}
	if letti := gh.fileLetti.Load(); letti != 2 {
		t.Errorf("file letti = %d, attesi 2", letti)
	}
}

func TestUnInstallazioneRevocataNonEUnGuastoDaRipetere(t *testing.T) {
	gh := nuovoFinto(t, "")
	gh.statoToken = http.StatusNotFound
	client := gh.client(t, githubapp.Options{})

	_, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234")
	if !errors.Is(err, githubapp.ErrForbidden) {
		t.Fatalf("FileAtRef = %v, atteso ErrForbidden", err)
	}
}

// Il JWT è la sola cosa che questo package firma: se non è verificabile con la
// chiave pubblica dell'App, GitHub rifiuta tutto e l'errore non dice perché.
func TestIlJWTEFirmatoConLaChiaveDellApp(t *testing.T) {
	gh := nuovoFinto(t, "version: 1\njobs: []\n")
	adesso := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	client := gh.client(t, githubapp.Options{Now: func() time.Time { return adesso }})

	if _, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234"); err != nil {
		t.Fatalf("FileAtRef: %v", err)
	}

	token, _ := gh.ultimoJWT.Load().(string)
	parti := strings.Split(token, ".")
	if len(parti) != 3 {
		t.Fatalf("il JWT ha %d parti, attese 3", len(parti))
	}

	firma, err := base64.RawURLEncoding.DecodeString(parti[2])
	if err != nil {
		t.Fatalf("firma non decodificabile: %v", err)
	}
	digest := sha256.Sum256([]byte(parti[0] + "." + parti[1]))
	if err := rsa.VerifyPKCS1v15(&chiaveDiProva.PublicKey, crypto.SHA256, digest[:], firma); err != nil {
		t.Fatalf("la firma del JWT non si verifica con la chiave dell'App: %v", err)
	}

	claims := decodifica(t, parti[1])
	if iss := claims["iss"]; iss != float64(12345) {
		t.Errorf("iss = %v, atteso 12345", iss)
	}
	// `iat` nel passato e `exp` entro dieci minuti: sono i due limiti che
	// GitHub applica, e sforarli produce un rifiuto che non li nomina.
	iat, exp := int64(claims["iat"].(float64)), int64(claims["exp"].(float64))
	if iat >= adesso.Unix() {
		t.Errorf("iat = %d, atteso nel passato per assorbire la deriva degli orologi", iat)
	}
	if durata := exp - iat; durata > int64((10 * time.Minute).Seconds()) {
		t.Errorf("exp - iat = %ds, oltre i dieci minuti che GitHub accetta", durata)
	}

	header := decodifica(t, parti[0])
	if header["alg"] != "RS256" {
		t.Errorf("alg = %v, atteso RS256", header["alg"])
	}
}

func decodifica(t *testing.T, segmento string) map[string]any {
	t.Helper()
	grezzo, err := base64.RawURLEncoding.DecodeString(segmento)
	if err != nil {
		t.Fatalf("segmento non decodificabile: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(grezzo, &out); err != nil {
		t.Fatalf("segmento non leggibile: %v", err)
	}
	return out
}

func TestUnFileOltreIlTettoVieneRifiutato(t *testing.T) {
	gh := nuovoFinto(t, strings.Repeat("x", 1025))
	client := gh.client(t, githubapp.Options{MaxBytes: 1024})

	if _, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234"); err == nil {
		t.Fatal("FileAtRef = nil: un file oltre il tetto non va portato in memoria e basta")
	}
}

func TestUnFileEsattamenteAlTettoPassa(t *testing.T) {
	gh := nuovoFinto(t, strings.Repeat("x", 1024))
	client := gh.client(t, githubapp.Options{MaxBytes: 1024})

	contenuto, trovato, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234")
	if err != nil || !trovato {
		t.Fatalf("FileAtRef = (%d byte, %v, %v)", len(contenuto), trovato, err)
	}
}

func TestUnPercorsoConSottocartelleRestaTale(t *testing.T) {
	gh := nuovoFinto(t, "version: 1\njobs: []\n")
	client := gh.client(t, githubapp.Options{})

	if _, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", ".github/cron.yaml", "abc1234"); err != nil {
		t.Fatalf("FileAtRef: %v", err)
	}
	if path := gh.ultimoPath.Load(); path != "/repos/acme/api/contents/.github/cron.yaml" {
		t.Errorf("percorso = %v: gli slash separano segmenti, non vanno codificati", path)
	}
}

func TestSenzaInstallazioneNonSiChiedeNemmenoUnToken(t *testing.T) {
	gh := nuovoFinto(t, "")
	client := gh.client(t, githubapp.Options{})

	_, _, err := client.FileAtRef(t.Context(), 0, "acme", "api", "cron.yaml", "abc1234")
	if !errors.Is(err, githubapp.ErrForbidden) {
		t.Fatalf("FileAtRef = %v, atteso ErrForbidden", err)
	}
	if emessi := gh.tokenEmessi.Load(); emessi != 0 {
		t.Errorf("token richiesti = %d, atteso 0", emessi)
	}
}

func TestArgomentiObbligatori(t *testing.T) {
	gh := nuovoFinto(t, "")
	client := gh.client(t, githubapp.Options{})

	casi := map[string][4]string{
		"senza owner":      {"", "api", "cron.yaml", "abc1234"},
		"senza repository": {"acme", "", "cron.yaml", "abc1234"},
		"senza percorso":   {"acme", "api", "", "abc1234"},
		"senza ref":        {"acme", "api", "cron.yaml", ""},
	}
	for nome, args := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, _, err := client.FileAtRef(t.Context(), 99, args[0], args[1], args[2], args[3]); err == nil {
				t.Error("FileAtRef = nil, atteso un errore")
			}
		})
	}
}

// -------------------------------------------------------------- costruzione

func TestSenzaVariabiliDAmbienteIlClientEAssenteMaNonEUnErrore(t *testing.T) {
	client, err := githubapp.LoadFromEnv(func(string) string { return "" }, githubapp.Options{})
	if err != nil {
		t.Fatalf("LoadFromEnv = %v: una macchina senza GitHub App deve avviarsi", err)
	}
	if client != nil {
		t.Error("client non nil senza configurazione")
	}
}

func TestUnaConfigurazioneSbagliataFermaLAvvio(t *testing.T) {
	dir := t.TempDir()
	pemPath := filepath.Join(dir, "app.pem")
	scriviChiave(t, pemPath, chiaveDiProva)

	casi := map[string]map[string]string{
		"identificativo non numerico": {
			githubapp.AppIDEnvVar:          "non-un-numero",
			githubapp.PrivateKeyPathEnvVar: pemPath,
		},
		"identificativo negativo": {
			githubapp.AppIDEnvVar:          "-1",
			githubapp.PrivateKeyPathEnvVar: pemPath,
		},
		"chiave inesistente": {
			githubapp.AppIDEnvVar:          "12345",
			githubapp.PrivateKeyPathEnvVar: filepath.Join(dir, "non-esiste.pem"),
		},
	}

	for nome, ambiente := range casi {
		t.Run(nome, func(t *testing.T) {
			_, err := githubapp.LoadFromEnv(func(k string) string { return ambiente[k] }, githubapp.Options{})
			if err == nil {
				t.Error("LoadFromEnv = nil: una configurazione a metà che si degrada in silenzio è peggio")
			}
			// Il messaggio non deve contenere materiale della chiave.
			if err != nil && strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Errorf("l'errore contiene il PEM: %v", err)
			}
		})
	}
}

func TestLaChiaveSiLeggeInPKCS1EInPKCS8(t *testing.T) {
	pkcs1 := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(chiaveDiProva),
	})
	der, err := x509.MarshalPKCS8PrivateKey(chiaveDiProva)
	if err != nil {
		t.Fatalf("PKCS#8: %v", err)
	}
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	for nome, contenuto := range map[string][]byte{"PKCS#1": pkcs1, "PKCS#8": pkcs8} {
		t.Run(nome, func(t *testing.T) {
			key, err := githubapp.ParsePrivateKey(contenuto)
			if err != nil {
				t.Fatalf("ParsePrivateKey: %v", err)
			}
			if !key.Equal(chiaveDiProva) {
				t.Error("la chiave letta non è quella scritta")
			}
		})
	}
}

func TestUnaChiaveNonPEMVieneRifiutata(t *testing.T) {
	if _, err := githubapp.ParsePrivateKey([]byte("non è un pem")); err == nil {
		t.Error("ParsePrivateKey = nil")
	}
}

func TestIlClientNonSiCostruisceSenzaChiaveOIdentificativo(t *testing.T) {
	if _, err := githubapp.New(githubapp.Options{PrivateKey: chiaveDiProva}); err == nil {
		t.Error("New senza AppID = nil")
	}
	if _, err := githubapp.New(githubapp.Options{AppID: 1}); err == nil {
		t.Error("New senza chiave = nil")
	}
}

// Il client finisce nei log all'avvio: quello che ne esce non deve contenere né
// la chiave né i token.
func TestIlClientLoggatoNonEspelleSegreti(t *testing.T) {
	var uscita bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&uscita, nil))

	gh := nuovoFinto(t, "version: 1\njobs: []\n")
	client := gh.client(t, githubapp.Options{Logger: logger})
	if _, _, err := client.FileAtRef(t.Context(), 99, "acme", "api", "cron.yaml", "abc1234"); err != nil {
		t.Fatalf("FileAtRef: %v", err)
	}
	logger.Info("client", slog.Any("github_app", client))

	testo := uscita.String()
	for _, proibito := range []string{"PRIVATE KEY", "ghs-token-di-installazione", chiaveDiProva.D.String()} {
		if strings.Contains(testo, proibito) {
			t.Errorf("il log contiene un segreto (%.20s...):\n%s", proibito, testo)
		}
	}
}

func scriviChiave(t *testing.T, path string, key *rsa.PrivateKey) {
	t.Helper()
	contenuto := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(path, contenuto, 0o600); err != nil {
		t.Fatalf("scrittura della chiave di prova: %v", err)
	}
}
