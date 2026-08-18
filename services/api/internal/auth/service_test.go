package auth_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/authtest"
	"github.com/apdsoftware/postqron/services/api/internal/legal"
	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// ---------------------------------------------------------------- impalcatura

// testClock è un orologio pilotato dal test: le scadenze in gioco sono giorni, e
// aspettarle non è un'opzione.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// countingHasher avvolge un [auth.Hasher] contando le operazioni.
//
// Serve alla verifica strutturale della protezione contro l'enumerazione per via
// temporale: il percorso «utente inesistente» deve eseguire **lo stesso numero**
// di operazioni di hashing di quello «password sbagliata». Contarle è una prova
// deterministica; misurare i millisecondi è una prova statistica, e servono
// entrambe — la prima dice *perché* i tempi coincidono, la seconda che
// coincidono davvero.
type countingHasher struct {
	inner *auth.Hasher

	mu    sync.Mutex
	ops   []string
	delay time.Duration
}

func (h *countingHasher) record(op string) {
	h.mu.Lock()
	delay := h.delay
	h.ops = append(h.ops, op)
	h.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
}

func (h *countingHasher) Hash(ctx context.Context, password string) (string, error) {
	h.record("hash")
	return h.inner.Hash(ctx, password)
}

func (h *countingHasher) Verify(ctx context.Context, encoded, password string) (bool, bool, error) {
	h.record("verify")
	return h.inner.Verify(ctx, encoded, password)
}

func (h *countingHasher) VerifyDecoy(ctx context.Context, password string) error {
	h.record("verify")
	return h.inner.VerifyDecoy(ctx, password)
}

// operations restituisce la sequenza registrata e azzera il contatore.
func (h *countingHasher) operations() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops := slices.Clone(h.ops)
	h.ops = nil
	return ops
}

var _ auth.PasswordHasher = (*countingHasher)(nil)

// fixture è l'ambiente di un test: servizio, persistenza finta, mailer finto,
// orologio pilotato.
type fixture struct {
	t      *testing.T
	svc    *auth.Service
	store  *authtest.Store
	mailer *auth.MemoryMailer
	hasher *countingHasher
	clock  *testClock
	keys   auth.Keyring
	logs   *logRecorder
}

// logRecorder trattiene i record di log per poterli ispezionare: nessun segreto
// deve finire lì (SPEC §5).
type logRecorder struct {
	mu      sync.Mutex
	handler slog.Handler
	records []slog.Record
	attrs   []slog.Attr
}

func newLogRecorder() *logRecorder {
	return &logRecorder{handler: slog.NewTextHandler(io.Discard, nil)}
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *logRecorder) Handle(_ context.Context, record slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record.Clone())
	return nil
}

func (r *logRecorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attrs = append(r.attrs, attrs...)
	return r
}

func (r *logRecorder) WithGroup(string) slog.Handler { return r }

// text è tutto ciò che è stato registrato, messaggi e attributi, in una stringa.
func (r *logRecorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []byte
	for _, record := range r.records {
		out = append(out, record.Message...)
		out = append(out, ' ')
		record.Attrs(func(attr slog.Attr) bool {
			out = append(out, attr.Key...)
			out = append(out, '=')
			out = append(out, attr.Value.String()...)
			out = append(out, ' ')
			return true
		})
		out = append(out, '\n')
	}
	return string(out)
}

func newFixture(t *testing.T, tune ...func(*auth.Options)) *fixture {
	t.Helper()

	store := authtest.NewStore()
	mailer := &auth.MemoryMailer{}
	clock := newTestClock()
	keys := newTestKeyring(t)
	hasher := &countingHasher{inner: newTestHasher(t, cheapParams)}
	logs := newLogRecorder()

	opts := auth.Options{
		Store:   store,
		Hasher:  hasher,
		Keyring: keys,
		Mailer:  mailer,
		Logger:  slog.New(logs),
		Now:     clock.Now,
	}
	for _, fn := range tune {
		fn(&opts)
	}

	svc, err := auth.NewService(opts)
	if err != nil {
		t.Fatalf("costruzione del Service: %v", err)
	}
	// Il lavoro in coda non deve sopravvivere al test: senza attesa, una
	// goroutine che scrive nel memStore mentre il test successivo lo legge
	// farebbe scattare il race detector su un guasto che non esiste.
	t.Cleanup(svc.Wait)

	return &fixture{
		t: t, svc: svc, store: store, mailer: mailer,
		hasher: hasher, clock: clock, keys: keys, logs: logs,
	}
}

const (
	testEmail    = "mario.rossi@example.com"
	testPassword = "una-password-lunga-abbastanza"
)

var testClient = auth.Client{
	IP:        netip.MustParseAddr("203.0.113.7"),
	UserAgent: "Postqron-Test/1.0",
}

// register crea un account e restituisce l'utente.
func (f *fixture) register(email, password string) auth.User {
	f.t.Helper()
	if err := f.svc.Register(f.t.Context(), auth.RegisterInput{
		Email: email, Password: password, FullName: "Mario Rossi", Client: testClient,
	}); err != nil {
		f.t.Fatalf("Register: %v", err)
	}
	f.svc.Wait()
	user, err := f.store.UserByEmail(f.t.Context(), email)
	if err != nil {
		f.t.Fatalf("l'account non è stato creato: %v", err)
	}
	return user
}

// login apre una sessione.
func (f *fixture) login(email, password string) auth.LoginResult {
	f.t.Helper()
	result, err := f.svc.Login(f.t.Context(), auth.LoginInput{
		Email: email, Password: password, Client: testClient,
	})
	if err != nil {
		f.t.Fatalf("Login: %v", err)
	}
	return result
}

// -------------------------------------------------------------- costruzione

func TestNewServiceEsigeLeDipendenze(t *testing.T) {
	valid := func() auth.Options {
		return auth.Options{
			Store:   authtest.NewStore(),
			Hasher:  newTestHasher(t, cheapParams),
			Keyring: newTestKeyring(t),
			Mailer:  &auth.MemoryMailer{},
		}
	}

	tests := map[string]func(*auth.Options){
		"senza Store":                  func(o *auth.Options) { o.Store = nil },
		"senza Hasher":                 func(o *auth.Options) { o.Hasher = nil },
		"senza Keyring":                func(o *auth.Options) { o.Keyring = auth.Keyring{} },
		"senza Mailer":                 func(o *auth.Options) { o.Mailer = nil },
		"inattività oltre la scadenza": func(o *auth.Options) { o.SessionTTL = time.Hour; o.SessionIdleTTL = 2 * time.Hour },
	}
	for name, broken := range tests {
		t.Run(name, func(t *testing.T) {
			opts := valid()
			broken(&opts)
			if _, err := auth.NewService(opts); err == nil {
				t.Fatal("atteso un errore")
			}
		})
	}

	if _, err := auth.NewService(valid()); err != nil {
		t.Fatalf("le opzioni valide sono state rifiutate: %v", err)
	}
}

// --------------------------------------------------------------- registrazione

func TestRegisterCreaLAccountEChiedeLaConfermaDellIndirizzo(t *testing.T) {
	f := newFixture(t)

	user := f.register(testEmail, testPassword)

	if user.Email != testEmail {
		t.Errorf("email = %q, attesa %q", user.Email, testEmail)
	}
	if user.EmailVerified() {
		t.Error("un account appena creato non ha l'indirizzo confermato")
	}
	// La password non è conservata in chiaro da nessuna parte.
	hash := f.store.PasswordHash(user.ID)
	if hash == testPassword || hash == "" {
		t.Fatalf("hash della password inatteso: %q", hash)
	}
	ok, _, err := f.hasher.Verify(t.Context(), hash, testPassword)
	if err != nil || !ok {
		t.Errorf("l'hash memorizzato non verifica la password: ok=%v err=%v", ok, err)
	}

	msg, found := f.mailer.Last(auth.KindEmailVerification)
	if !found {
		t.Fatal("nessuna email di conferma messa in coda")
	}
	if msg.To != testEmail {
		t.Errorf("destinatario = %q", msg.To)
	}
	if msg.Token == "" {
		t.Error("l'email di conferma non porta il token")
	}
	if !msg.ExpiresAt.Equal(f.clock.Now().Add(auth.DefaultEmailVerificationTTL)) {
		t.Errorf("scadenza = %s", msg.ExpiresAt)
	}
}

// TestRegisterRegistraIlConsensoAiQuattroDocumenti verifica che il consenso
// nasca con l'account (R46).
//
// I Termini si aprono con «By creating an account you accept them, together with
// the Acceptable Use Policy and the Privacy Policy»: se la registrazione non
// scrivesse la prova, quella frase resterebbe vera in un documento e
// indimostrabile nel sistema.
func TestRegisterRegistraIlConsensoAiQuattroDocumenti(t *testing.T) {
	f := newFixture(t)
	// L'orologio di prova sta al 17 agosto, e due dei quattro documenti entrano
	// in vigore il 18: senza questo salto il test verificherebbe il caso in cui
	// il registro ha ragione a registrarne solo due, che è un'altra proprietà.
	f.clock.advance(48 * time.Hour)

	if err := f.svc.Register(t.Context(), auth.RegisterInput{
		Email: testEmail, Password: testPassword, FullName: "Mario Rossi",
		Client: testClient, Language: legal.Italian,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	f.svc.Wait()

	user, err := f.store.UserByEmail(t.Context(), testEmail)
	if err != nil {
		t.Fatalf("l'account non è stato creato: %v", err)
	}

	consensi := f.store.ConsentsOf(user.ID)
	if len(consensi) != len(legal.Documents()) {
		t.Fatalf("%d consensi registrati, attesi %d (uno per documento)", len(consensi), len(legal.Documents()))
	}

	registro := legal.Current()
	versioni := map[legal.Document]string{}
	for _, c := range consensi {
		versioni[c.Document] = c.Version

		inVigore, ok := registro.InForce(c.Document, f.clock.Now())
		if !ok {
			t.Errorf("%s: consenso registrato su un documento che non è in vigore", c.Document)
			continue
		}
		if c.Version != inVigore.Version {
			t.Errorf("%s: consenso alla %s, in vigore la %s", c.Document, c.Version, inVigore.Version)
		}
		// La lingua è quella del testo **mostrato**: l'utente ha chiesto
		// l'italiano, ma finché la traduzione non esiste ha letto l'inglese, e
		// la prova non deve dire il contrario.
		if c.Language != inVigore.Presented(legal.Italian) {
			t.Errorf("%s: lingua registrata %q, mostrata %q", c.Document, c.Language, inVigore.Presented(legal.Italian))
		}
		if c.Source != legal.SourceRegistration {
			t.Errorf("%s: origine %q, attesa %q", c.Document, c.Source, legal.SourceRegistration)
		}
		if !c.AcceptedAt.Equal(f.clock.Now()) {
			t.Errorf("%s: data del consenso %s, atteso l'istante della registrazione %s",
				c.Document, c.AcceptedAt, f.clock.Now())
		}
	}

	// E le versioni sono diverse fra loro: è la ragione per cui la prova ha una
	// riga per documento invece di un numero solo per l'insieme.
	if versioni[legal.TermsOfService] == versioni[legal.CookiePolicy] {
		t.Errorf("Termini e cookie policy risultano alla stessa versione (%s): "+
			"o il registro è sbagliato, o la registrazione sta appiattendo quattro documenti in uno",
			versioni[legal.TermsOfService])
	}
}

// La proprietà centrale della registrazione: un indirizzo già registrato e uno
// libero producono la stessa risposta. L'unico modo lecito di dire «esisti già»
// è dirlo al proprietario dell'indirizzo, per email.
func TestRegisterNonRivelaCheLIndirizzoEGiaPreso(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	f.mailer.Reset()

	// Un secondo tentativo sullo stesso indirizzo, anche con maiuscole diverse.
	err := f.svc.Register(t.Context(), auth.RegisterInput{
		Email: "MARIO.ROSSI@Example.com", Password: "un-altra-password-diversa", Client: testClient,
	})
	f.svc.Wait()

	if err != nil {
		t.Fatalf("la registrazione su indirizzo preso ha restituito un errore: %v", err)
	}
	if n := f.store.UserCount(); n != 1 {
		t.Errorf("account presenti = %d, atteso 1: un duplicato è stato creato", n)
	}

	// La password del titolare non è stata toccata.
	user, _ := f.store.UserByEmail(t.Context(), testEmail)
	ok, _, _ := f.hasher.Verify(t.Context(), user.PasswordHash, testPassword)
	if !ok {
		t.Error("la password dell'account esistente è stata sovrascritta")
	}

	// Il proprietario riceve l'avviso; nessuna email di conferma parte, perché
	// non c'è niente da confermare.
	if _, found := f.mailer.Last(auth.KindRegistrationAttempt); !found {
		t.Error("nessun avviso al proprietario dell'indirizzo")
	}
	if _, found := f.mailer.Last(auth.KindEmailVerification); found {
		t.Error("è partita un'email di conferma per un account che esisteva già")
	}
}

// Le due strade devono fare lo stesso lavoro osservabile: in particolare la
// registrazione su indirizzo preso calcola comunque l'hash Argon2id, che è la
// parte che domina il tempo di risposta. Senza, il tempo direbbe ciò che il
// corpo della risposta si rifiuta di dire.
func TestRegisterCalcolaLHashAncheSeLIndirizzoEPreso(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	f.hasher.operations()

	if err := f.svc.Register(t.Context(), auth.RegisterInput{
		Email: testEmail, Password: "un-altra-password-diversa", Client: testClient,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	f.svc.Wait()

	if ops := f.hasher.operations(); !slices.Equal(ops, []string{"hash"}) {
		t.Errorf("operazioni di hashing = %v, atteso esattamente un hash", ops)
	}
}

func TestRegisterRifiutaIngressiNonValidi(t *testing.T) {
	tests := map[string]struct {
		email    string
		password string
		wantErr  error
	}{
		"email malformata":  {"non-una-email", testPassword, auth.ErrInvalidEmail},
		"email vuota":       {"", testPassword, auth.ErrInvalidEmail},
		"password corta":    {testEmail, "corta", auth.ErrPasswordTooShort},
		"password di spazi": {testEmail, "              ", auth.ErrPasswordBlank},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			err := f.svc.Register(t.Context(), auth.RegisterInput{
				Email: tc.email, Password: tc.password, Client: testClient,
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("errore = %v, atteso %v", err, tc.wantErr)
			}
			if n := f.store.UserCount(); n != 0 {
				t.Errorf("account creati = %d, atteso 0", n)
			}
			// Un ingresso non valido non deve nemmeno costare un Argon2id: sarebbe
			// il modo più economico di far consumare CPU al servizio.
			if ops := f.hasher.operations(); len(ops) != 0 {
				t.Errorf("operazioni di hashing = %v, atteso nessuna", ops)
			}
		})
	}
}

func TestRegisterELimitatoPerIndirizzoIP(t *testing.T) {
	f := newFixture(t, func(o *auth.Options) {
		o.Limits.RegisterPerIP = ratelimit.Rule{Burst: 2, Window: time.Hour}
	})

	for i := range 2 {
		err := f.svc.Register(t.Context(), auth.RegisterInput{
			Email: emailN(i), Password: testPassword, Client: testClient,
		})
		if err != nil {
			t.Fatalf("registrazione %d rifiutata: %v", i, err)
		}
	}
	f.svc.Wait()

	err := f.svc.Register(t.Context(), auth.RegisterInput{
		Email: emailN(99), Password: testPassword, Client: testClient,
	})
	var limited *auth.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("errore = %v, atteso RateLimitedError", err)
	}
	if limited.RetryAfter <= 0 {
		t.Error("RetryAfter deve essere positivo")
	}

	// Un altro indirizzo IP non è toccato dal limite del primo.
	other := auth.Client{IP: netip.MustParseAddr("198.51.100.4")}
	if err := f.svc.Register(t.Context(), auth.RegisterInput{
		Email: emailN(100), Password: testPassword, Client: other,
	}); err != nil {
		t.Fatalf("un altro indirizzo IP è stato bloccato: %v", err)
	}
	f.svc.Wait()
}

func emailN(n int) string {
	return "utente" + string(rune('a'+n%26)) + string(rune('a'+(n/26)%26)) + "@example.com"
}

// ---------------------------------------------------------------------- login

func TestLoginRiuscito(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)

	result := f.login(testEmail, testPassword)

	if result.User.ID != user.ID {
		t.Errorf("utente = %q, atteso %q", result.User.ID, user.ID)
	}
	if result.Token == "" {
		t.Fatal("nessun token di sessione restituito")
	}
	// Nel database resta solo l'impronta.
	stored := f.store.Session(result.Session.ID)
	if stored.TokenHash != f.keys.SessionHash(result.Token) {
		t.Error("l'impronta memorizzata non corrisponde al token consegnato")
	}
	if stored.TokenHash == result.Token {
		t.Error("il token è memorizzato in chiaro")
	}
	if !stored.ExpiresAt.Equal(f.clock.Now().Add(auth.DefaultSessionTTL)) {
		t.Errorf("scadenza della sessione = %s", stored.ExpiresAt)
	}
	if stored.UserAgent != testClient.UserAgent {
		t.Errorf("user agent = %q", stored.UserAgent)
	}
	if stored.IPAddress == nil || *stored.IPAddress != testClient.IP {
		t.Errorf("indirizzo = %v", stored.IPAddress)
	}
	// L'accesso lascia traccia della data.
	if f.store.User(user.ID).LastLoginAt == nil {
		t.Error("last_login_at non è stato aggiornato")
	}

	// L'indirizzo si può scrivere con qualunque combinazione di maiuscole.
	if _, err := f.svc.Login(t.Context(), auth.LoginInput{
		Email: "Mario.Rossi@EXAMPLE.com", Password: testPassword, Client: testClient,
	}); err != nil {
		t.Errorf("login con maiuscole diverse rifiutato: %v", err)
	}
}

// Password sbagliata, indirizzo inesistente, indirizzo malformato e account senza
// password devono essere lo stesso errore: qualunque differenza è una risposta
// alla domanda «questo account esiste?».
func TestLoginRifiutaTuttoConLoStessoErrore(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)

	tests := map[string]auth.LoginInput{
		"password sbagliata":    {Email: testEmail, Password: "password-completamente-altra"},
		"indirizzo inesistente": {Email: "nessuno@example.com", Password: testPassword},
		"indirizzo malformato":  {Email: "non-una-email", Password: testPassword},
		"password vuota":        {Email: testEmail, Password: ""},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			in.Client = testClient
			_, err := f.svc.Login(t.Context(), in)
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Fatalf("errore = %v, atteso ErrInvalidCredentials", err)
			}
		})
	}

	// Un account senza password (nato da un provider esterno) non è distinguibile
	// da uno con la password sbagliata.
	f.store.SetPasswordHash(user.ID, "")
	_, err := f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: testPassword, Client: testClient,
	})
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("account senza password: errore = %v, atteso ErrInvalidCredentials", err)
	}
}

// La verifica strutturale della protezione temporale: il percorso «utente
// inesistente» esegue esattamente le stesse operazioni di hashing di quello
// «password sbagliata». Se qualcuno rimuovesse la verifica civetta per
// «ottimizzare», questo test lo direbbe subito e senza dipendere da una misura.
func TestLoginEseguelHashAncheSeLUtenteNonEsiste(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	f.hasher.operations()

	_, _ = f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: "password-sbagliata-ma-lunga", Client: testClient,
	})
	conKnownAccount := f.hasher.operations()

	_, _ = f.svc.Login(t.Context(), auth.LoginInput{
		Email: "nessuno@example.com", Password: "password-sbagliata-ma-lunga", Client: testClient,
	})
	senzaAccount := f.hasher.operations()

	if !slices.Equal(conKnownAccount, []string{"verify"}) {
		t.Errorf("con account esistente: operazioni = %v, atteso [verify]", conKnownAccount)
	}
	if !slices.Equal(senzaAccount, conKnownAccount) {
		t.Errorf("operazioni diverse fra i due percorsi: %v contro %v", senzaAccount, conKnownAccount)
	}
}

// La verifica per misura: i tempi dei due percorsi devono essere dello stesso
// ordine di grandezza. È la prova che il meccanismo strutturale funziona anche
// sull'orologio, che è ciò che un attaccante osserva davvero.
//
// La soglia è larga di proposito (un fattore due) perché la misura è rumorosa su
// una macchina condivisa con la CI; serve a distinguere «uguali» da «uno dei due
// non fa l'hash», che è un fattore cento, non un fattore due.
func TestLoginNonRivelaLEsistenzaDellAccountDalTempo(t *testing.T) {
	// Il Hasher reale, non quello strumentato: il tempo che si misura deve essere
	// quello di Argon2id.
	f := newFixture(t, func(o *auth.Options) {
		o.Hasher = newTestHasher(t, cheapParams)
		// I limiti non devono scattare in mezzo alla misura: falserebbero i
		// tempi rispondendo senza fare nulla.
		o.Limits.LoginPerIP = ratelimit.Rule{Burst: 10_000, Window: time.Hour}
		o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 10_000, Window: time.Hour}
	})
	f.register(testEmail, testPassword)

	const samples = 15
	esistente := make([]time.Duration, 0, samples)
	inesistente := make([]time.Duration, 0, samples)

	measure := func(email string) time.Duration {
		start := time.Now()
		_, _ = f.svc.Login(t.Context(), auth.LoginInput{
			Email: email, Password: "password-sbagliata-ma-lunga", Client: testClient,
		})
		return time.Since(start)
	}

	// I due casi si alternano: se la macchina rallenta a metà del test, rallenta
	// per entrambi, e la mediana resta confrontabile.
	for range samples {
		esistente = append(esistente, measure(testEmail))
		inesistente = append(inesistente, measure("nessuno@example.com"))
	}

	conAccount, senzaAccount := median(esistente), median(inesistente)
	t.Logf("mediana con account = %s, senza account = %s", conAccount, senzaAccount)

	if conAccount <= 0 || senzaAccount <= 0 {
		t.Fatal("misura non utilizzabile")
	}
	ratio := float64(conAccount) / float64(senzaAccount)
	if ratio < 0.5 || ratio > 2 {
		t.Errorf("i tempi differiscono di un fattore %.2f: il percorso senza account non fa lo stesso lavoro "+
			"(con account %s, senza %s)", ratio, conAccount, senzaAccount)
	}
}

func median(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

func TestLoginELimitatoPerAccountEPerIndirizzoIP(t *testing.T) {
	t.Run("per account", func(t *testing.T) {
		f := newFixture(t, func(o *auth.Options) {
			o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 3, Window: 15 * time.Minute}
			o.Limits.LoginPerIP = ratelimit.Rule{Burst: 1000, Window: 15 * time.Minute}
		})
		f.register(testEmail, testPassword)

		for i := range 3 {
			_, err := f.svc.Login(t.Context(), auth.LoginInput{
				Email: testEmail, Password: "sbagliata-ma-lunga-abbastanza", Client: testClient,
			})
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Fatalf("tentativo %d: errore = %v", i, err)
			}
		}

		// Il quarto tentativo è fermato dal limite, e lo è anche se la password è
		// quella giusta: il tetto vale sul tentativo, non sull'esito.
		_, err := f.svc.Login(t.Context(), auth.LoginInput{
			Email: testEmail, Password: testPassword, Client: testClient,
		})
		var limited *auth.RateLimitedError
		if !errors.As(err, &limited) {
			t.Fatalf("errore = %v, atteso RateLimitedError", err)
		}

		// Il limite è per account: cambiare indirizzo IP non lo aggira.
		_, err = f.svc.Login(t.Context(), auth.LoginInput{
			Email: testEmail, Password: testPassword,
			Client: auth.Client{IP: netip.MustParseAddr("198.51.100.9")},
		})
		if !errors.As(err, &limited) {
			t.Errorf("il limite per account è stato aggirato cambiando indirizzo IP: %v", err)
		}

		// E vale anche scrivendo l'indirizzo con maiuscole diverse: un limite
		// aggirabile con Shift non è un limite.
		_, err = f.svc.Login(t.Context(), auth.LoginInput{
			Email: "MARIO.ROSSI@EXAMPLE.COM", Password: testPassword, Client: testClient,
		})
		if !errors.As(err, &limited) {
			t.Errorf("il limite per account è stato aggirato con le maiuscole: %v", err)
		}
	})

	t.Run("per indirizzo IP", func(t *testing.T) {
		f := newFixture(t, func(o *auth.Options) {
			o.Limits.LoginPerIP = ratelimit.Rule{Burst: 3, Window: 15 * time.Minute}
			o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 1000, Window: 15 * time.Minute}
		})

		// Indirizzi diversi a ogni tentativo: il limite per account non scatta,
		// quello per IP sì. È lo schema della scansione di indirizzi.
		for i := range 3 {
			_, err := f.svc.Login(t.Context(), auth.LoginInput{
				Email: emailN(i), Password: testPassword, Client: testClient,
			})
			if !errors.Is(err, auth.ErrInvalidCredentials) {
				t.Fatalf("tentativo %d: errore = %v", i, err)
			}
		}
		_, err := f.svc.Login(t.Context(), auth.LoginInput{
			Email: emailN(50), Password: testPassword, Client: testClient,
		})
		var limited *auth.RateLimitedError
		if !errors.As(err, &limited) {
			t.Fatalf("errore = %v, atteso RateLimitedError", err)
		}
	})

	t.Run("il 429 non dipende dall'esistenza dell'account", func(t *testing.T) {
		f := newFixture(t, func(o *auth.Options) {
			o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 1, Window: 15 * time.Minute}
		})
		f.register(testEmail, testPassword)

		var limited *auth.RateLimitedError
		for _, email := range []string{testEmail, "nessuno@example.com"} {
			// Primo tentativo: consuma il gettone.
			_, _ = f.svc.Login(t.Context(), auth.LoginInput{
				Email: email, Password: "sbagliata-ma-lunga-abbastanza", Client: testClient,
			})
			// Secondo: fermato allo stesso modo per entrambi.
			_, err := f.svc.Login(t.Context(), auth.LoginInput{
				Email: email, Password: "sbagliata-ma-lunga-abbastanza", Client: testClient,
			})
			if !errors.As(err, &limited) {
				t.Fatalf("%s: errore = %v, atteso RateLimitedError", email, err)
			}
		}
	})
}

// Un accesso riuscito azzera i contatori: chi sbaglia due volte e poi ricorda la
// password non deve restare a un tentativo dall'esclusione.
func TestLoginRiuscitoAzzeraIContatori(t *testing.T) {
	f := newFixture(t, func(o *auth.Options) {
		o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 3, Window: 15 * time.Minute}
	})
	f.register(testEmail, testPassword)

	for range 2 {
		_, _ = f.svc.Login(t.Context(), auth.LoginInput{
			Email: testEmail, Password: "sbagliata-ma-lunga-abbastanza", Client: testClient,
		})
	}
	f.login(testEmail, testPassword)

	// Il credito è tornato pieno: tre tentativi sbagliati devono essere di nuovo
	// possibili.
	for i := range 3 {
		_, err := f.svc.Login(t.Context(), auth.LoginInput{
			Email: testEmail, Password: "sbagliata-ma-lunga-abbastanza", Client: testClient,
		})
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("tentativo %d dopo il reset: errore = %v", i, err)
		}
	}
}

// Un account sospeso si riconosce solo a password corretta: prima di quel punto la
// risposta è indistinguibile da qualunque altro rifiuto.
func TestLoginSuAccountSospeso(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	f.store.Suspend(user.ID, f.clock.Now())

	_, err := f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: "sbagliata-ma-lunga-abbastanza", Client: testClient,
	})
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("password sbagliata su account sospeso: errore = %v", err)
	}

	_, err = f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: testPassword, Client: testClient,
	})
	if !errors.Is(err, auth.ErrAccountSuspended) {
		t.Errorf("password corretta su account sospeso: errore = %v, atteso ErrAccountSuspended", err)
	}
}

// L'hash si rigenera al primo login utile dopo un aumento dei parametri di costo:
// è l'unico momento in cui la password in chiaro esiste.
func TestLoginRigeneraUnHashConParametriObsoleti(t *testing.T) {
	weakParams := cheapParams
	f := newFixture(t, func(o *auth.Options) {
		o.Hasher = newTestHasher(t, weakParams)
	})
	user := f.register(testEmail, testPassword)
	oldHash := f.store.PasswordHash(user.ID)

	// Il servizio riparte con parametri più costosi, sullo stesso archivio.
	strongParams := weakParams
	strongParams.Memory *= 2
	stronger, err := auth.NewService(auth.Options{
		Store:   f.store,
		Hasher:  newTestHasher(t, strongParams),
		Keyring: f.keys,
		Mailer:  f.mailer,
		Logger:  slog.New(newLogRecorder()),
		Now:     f.clock.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if _, err := stronger.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: testPassword, Client: testClient,
	}); err != nil {
		t.Fatalf("Login: %v", err)
	}

	newHash := f.store.PasswordHash(user.ID)
	if newHash == oldHash {
		t.Fatal("l'hash non è stato rigenerato con i parametri correnti")
	}
	// E la password continua a funzionare.
	if _, err := stronger.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: testPassword, Client: testClient,
	}); err != nil {
		t.Errorf("dopo la rigenerazione il login non funziona più: %v", err)
	}
}

// ------------------------------------------------------------------- sessioni

func TestAuthenticate(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	result := f.login(testEmail, testPassword)

	got, session, err := f.svc.Authenticate(t.Context(), result.Token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != user.ID || session.ID != result.Session.ID {
		t.Errorf("utente = %q, sessione = %q", got.ID, session.ID)
	}
}

func TestAuthenticateRifiutaLeSessioniNonValide(t *testing.T) {
	tests := map[string]func(*fixture, auth.LoginResult) string{
		"token assente": func(*fixture, auth.LoginResult) string { return "" },
		"token inventato": func(*fixture, auth.LoginResult) string {
			return "un-token-che-non-e-mai-esistito"
		},
		"sessione revocata": func(f *fixture, r auth.LoginResult) string {
			if err := f.svc.Logout(f.t.Context(), r.Token); err != nil {
				f.t.Fatalf("Logout: %v", err)
			}
			return r.Token
		},
		"scadenza assoluta superata": func(f *fixture, r auth.LoginResult) string {
			f.clock.advance(auth.DefaultSessionTTL + time.Minute)
			return r.Token
		},
		"inattività superata": func(f *fixture, r auth.LoginResult) string {
			// L'orologio avanza meno della scadenza assoluta ma più
			// dell'inattività: è il caso che le due scadenze distinte devono
			// coprire e che una sola non coprirebbe.
			f.clock.advance(auth.DefaultSessionIdleTTL + time.Minute)
			return r.Token
		},
	}
	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.register(testEmail, testPassword)
			result := f.login(testEmail, testPassword)

			token := prepare(f, result)
			if _, _, err := f.svc.Authenticate(t.Context(), token); !errors.Is(err, auth.ErrUnauthenticated) {
				t.Fatalf("errore = %v, atteso ErrUnauthenticated", err)
			}
		})
	}
}

// La sospensione ha effetto sulle sessioni già aperte: un account bloccato non
// resta operativo per trenta giorni perché era già collegato.
func TestAuthenticateRifiutaUnAccountSospesoConSessioneAperta(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	result := f.login(testEmail, testPassword)

	f.store.Suspend(user.ID, f.clock.Now())

	if _, _, err := f.svc.Authenticate(t.Context(), result.Token); !errors.Is(err, auth.ErrAccountSuspended) {
		t.Fatalf("errore = %v, atteso ErrAccountSuspended", err)
	}
}

// `last_used_at` si aggiorna, ma non a ogni richiesta: senza soglia ogni lettura
// autenticata sarebbe anche una scrittura.
func TestAuthenticateAggiornaLUltimoUtilizzoConParsimonia(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	result := f.login(testEmail, testPassword)

	before := f.store.CallCount("TouchSession")
	// Due richieste ravvicinate: nessuna scrittura.
	for range 2 {
		if _, _, err := f.svc.Authenticate(t.Context(), result.Token); err != nil {
			t.Fatalf("Authenticate: %v", err)
		}
	}
	if got := f.store.CallCount("TouchSession") - before; got != 0 {
		t.Errorf("scritture = %d, attese 0 per richieste ravvicinate", got)
	}

	// Passata la soglia, la sessione si aggiorna.
	f.clock.advance(10 * time.Minute)
	if _, _, err := f.svc.Authenticate(t.Context(), result.Token); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got := f.store.CallCount("TouchSession") - before; got != 1 {
		t.Errorf("scritture = %d, attesa 1 dopo la soglia", got)
	}
	if used := f.store.Session(result.Session.ID).LastUsedAt; !used.Equal(f.clock.Now()) {
		t.Errorf("last_used_at = %s, atteso %s", used, f.clock.Now())
	}
}

func TestLogout(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	result := f.login(testEmail, testPassword)

	if err := f.svc.Logout(t.Context(), result.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, _, err := f.svc.Authenticate(t.Context(), result.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("la sessione è ancora valida dopo il logout: %v", err)
	}

	// Un logout ripetuto, o su un token inesistente, non è un errore: il
	// risultato voluto è già vero, e distinguere darebbe a chi ha in mano un
	// token la conferma che era valido.
	for _, token := range []string{result.Token, "token-inventato", ""} {
		if err := f.svc.Logout(t.Context(), token); err != nil {
			t.Errorf("Logout su %q: %v", token, err)
		}
	}
}

func TestGestioneDelleSessioni(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)

	primo := f.login(testEmail, testPassword)
	f.clock.advance(time.Minute)
	secondo := f.login(testEmail, testPassword)
	f.clock.advance(time.Minute)
	terzo := f.login(testEmail, testPassword)

	sessions, err := f.svc.ListSessions(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("sessioni = %d, attese 3", len(sessions))
	}

	// Revoca puntuale.
	if err := f.svc.RevokeSession(t.Context(), user.ID, primo.Session.ID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, _, err := f.svc.Authenticate(t.Context(), primo.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("la sessione revocata è ancora valida: %v", err)
	}
	// Una seconda revoca della stessa sessione non trova più nulla.
	if err := f.svc.RevokeSession(t.Context(), user.ID, primo.Session.ID); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Errorf("errore = %v, atteso ErrSessionNotFound", err)
	}

	// «Chiudi le altre»: risparmia quella indicata.
	n, err := f.svc.RevokeOtherSessions(t.Context(), user.ID, terzo.Session.ID)
	if err != nil {
		t.Fatalf("RevokeOtherSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("sessioni revocate = %d, attesa 1", n)
	}
	if _, _, err := f.svc.Authenticate(t.Context(), secondo.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("la seconda sessione è ancora valida: %v", err)
	}
	if _, _, err := f.svc.Authenticate(t.Context(), terzo.Token); err != nil {
		t.Errorf("la sessione da risparmiare è stata chiusa: %v", err)
	}
}

// L'ambito della revoca è l'utente: l'identificativo di una sessione altrui non
// basta a chiuderla.
func TestRevokeSessionNonTraversaGliAccount(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	vittima := f.login(testEmail, testPassword)

	f.register("altro@example.com", testPassword)
	attaccante := f.login("altro@example.com", testPassword)

	err := f.svc.RevokeSession(t.Context(), attaccante.User.ID, vittima.Session.ID)
	if !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("errore = %v, atteso ErrSessionNotFound", err)
	}
	if _, _, err := f.svc.Authenticate(t.Context(), vittima.Token); err != nil {
		t.Errorf("la sessione della vittima è stata chiusa da un altro account: %v", err)
	}
}

// ------------------------------------------------------- recupero password

func TestRequestPasswordResetInviaIlLinkSoloSeLAccountEsiste(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	f.mailer.Reset()

	if err := f.svc.RequestPasswordReset(t.Context(), testEmail, testClient); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	f.svc.Wait()

	msg, found := f.mailer.Last(auth.KindPasswordReset)
	if !found {
		t.Fatal("nessun link di recupero messo in coda")
	}
	if msg.Token == "" || msg.UserID != user.ID {
		t.Errorf("messaggio inatteso: %+v", msg)
	}
	if !msg.ExpiresAt.Equal(f.clock.Now().Add(auth.DefaultPasswordResetTTL)) {
		t.Errorf("scadenza = %s", msg.ExpiresAt)
	}
}

// La risposta è identica per un indirizzo registrato e per uno che non lo è: né
// errore, né messaggio, né lavoro svolto prima di rispondere. Il secondo caso non
// mette in coda nulla, ma il chiamante non può accorgersene.
func TestRequestPasswordResetNonRivelaSeLAccountEsiste(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	f.mailer.Reset()

	for _, email := range []string{testEmail, "nessuno@example.com", "non-una-email"} {
		if err := f.svc.RequestPasswordReset(t.Context(), email, testClient); err != nil {
			t.Errorf("%s: errore = %v, atteso nessuno", email, err)
		}
	}
	f.svc.Wait()

	// Solo il proprietario di un account riceve qualcosa.
	sent := f.mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("messaggi in coda = %d, atteso 1", len(sent))
	}
	if sent[0].To != testEmail {
		t.Errorf("destinatario = %q", sent[0].To)
	}
}

// La risposta parte prima che si sappia se l'account esiste. Il test lo dimostra
// facendo rallentare la persistenza: se la ricerca fosse sul percorso della
// risposta, il rallentamento si vedrebbe nel tempo di ritorno.
func TestRequestPasswordResetRispondePrimaDiCercareLAccount(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)

	// Prima che la goroutine possa concludere, la chiamata deve essere già
	// tornata: si osserva che al ritorno il messaggio non c'è ancora.
	blocked := make(chan struct{})
	slow := &blockingMailer{release: blocked}
	svc, err := auth.NewService(auth.Options{
		Store:   f.store,
		Hasher:  f.hasher,
		Keyring: f.keys,
		Mailer:  slow,
		Logger:  slog.New(newLogRecorder()),
		Now:     f.clock.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	if err := svc.RequestPasswordReset(t.Context(), testEmail, testClient); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	// Se l'invio fosse sincrono, saremmo bloccati sopra e non arriveremmo qui.
	if slow.done() {
		t.Error("l'invio è avvenuto sul percorso della risposta")
	}
	close(blocked)
	svc.Wait()
	if !slow.done() {
		t.Error("l'invio non è avvenuto nemmeno dopo il rilascio")
	}
}

// blockingMailer si ferma finché il test non lo rilascia.
type blockingMailer struct {
	release chan struct{}
	mu      sync.Mutex
	sent    bool
}

func (m *blockingMailer) Send(_ context.Context, _ auth.Message) error {
	<-m.release
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = true
	return nil
}

func (m *blockingMailer) done() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent
}

// Un guasto dell'invio non deve cambiare ciò che il client osserva: se lo
// cambiasse, la differenza fra un indirizzo con account e uno senza tornerebbe
// visibile (e SPEC R20.1 dice che il recapito non è osservabile comunque).
func TestRequestPasswordResetIgnoraIGuastiDellInvio(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	f.mailer.Err = errors.New("mailronix non risponde")

	if err := f.svc.RequestPasswordReset(t.Context(), testEmail, testClient); err != nil {
		t.Fatalf("un guasto dell'invio è arrivato al chiamante: %v", err)
	}
	f.svc.Wait()

	// Il guasto va nel log, senza il token.
	logged := f.logs.text()
	if !strings.Contains(logged, "messaggio non messo in coda") {
		t.Error("il guasto dell'invio non è stato registrato")
	}
	if msg, ok := f.mailer.Last(auth.KindPasswordReset); ok && strings.Contains(logged, msg.Token) {
		t.Error("il token compare nel log")
	}
}

// Il limite per indirizzo email è quello che impedisce di usare Postqron per
// bombardare la casella di qualcun altro.
func TestRequestPasswordResetELimitato(t *testing.T) {
	f := newFixture(t, func(o *auth.Options) {
		o.Limits.PasswordResetPerAccount = ratelimit.Rule{Burst: 2, Window: time.Hour}
		o.Limits.PasswordResetPerIP = ratelimit.Rule{Burst: 100, Window: time.Hour}
	})
	f.register(testEmail, testPassword)

	for i := range 2 {
		if err := f.svc.RequestPasswordReset(t.Context(), testEmail, testClient); err != nil {
			t.Fatalf("richiesta %d: %v", i, err)
		}
	}
	f.svc.Wait()

	var limited *auth.RateLimitedError
	// Anche cambiando maiuscole e indirizzo IP.
	err := f.svc.RequestPasswordReset(t.Context(), "MARIO.ROSSI@example.com",
		auth.Client{IP: netip.MustParseAddr("198.51.100.1")})
	if !errors.As(err, &limited) {
		t.Fatalf("errore = %v, atteso RateLimitedError", err)
	}

	// Il tetto vale anche per un indirizzo che non ha un account: se valesse solo
	// per quelli veri, la differenza fra 202 e 429 sarebbe la risposta alla
	// domanda «questo account esiste?».
	f2 := newFixture(t, func(o *auth.Options) {
		o.Limits.PasswordResetPerAccount = ratelimit.Rule{Burst: 1, Window: time.Hour}
	})
	if err := f2.svc.RequestPasswordReset(t.Context(), "nessuno@example.com", testClient); err != nil {
		t.Fatalf("prima richiesta: %v", err)
	}
	if err := f2.svc.RequestPasswordReset(t.Context(), "nessuno@example.com", testClient); !errors.As(err, &limited) {
		t.Errorf("errore = %v, atteso RateLimitedError anche per un indirizzo senza account", err)
	}
	f2.svc.Wait()
}

// Un account sospeso non riceve il link: reimpostare la password non gli
// restituirebbe l'accesso. Al chiamante non cambia niente.
func TestRequestPasswordResetNonScriveAUnAccountSospeso(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	f.store.Suspend(user.ID, f.clock.Now())
	f.mailer.Reset()

	if err := f.svc.RequestPasswordReset(t.Context(), testEmail, testClient); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	f.svc.Wait()

	if _, found := f.mailer.Last(auth.KindPasswordReset); found {
		t.Error("un account sospeso ha ricevuto un link di recupero")
	}
}

// ---------------------------------------------------- reimpostazione password

// resetToken chiede un recupero e restituisce il token ricevuto per email.
func (f *fixture) resetToken(email string) string {
	f.t.Helper()
	if err := f.svc.RequestPasswordReset(f.t.Context(), email, testClient); err != nil {
		f.t.Fatalf("RequestPasswordReset: %v", err)
	}
	f.svc.Wait()
	msg, found := f.mailer.Last(auth.KindPasswordReset)
	if !found {
		f.t.Fatal("nessun token di recupero ricevuto")
	}
	return msg.Token
}

func TestResetPassword(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	vecchiaSessione := f.login(testEmail, testPassword)
	token := f.resetToken(testEmail)

	const nuova = "una-password-nuova-e-lunga"
	if err := f.svc.ResetPassword(t.Context(), token, nuova, testClient); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	// La password nuova funziona, la vecchia no.
	if _, err := f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: nuova, Client: testClient,
	}); err != nil {
		t.Errorf("la password nuova non funziona: %v", err)
	}
	if _, err := f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: testPassword, Client: testClient,
	}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("la password vecchia funziona ancora: %v", err)
	}

	// Tutte le sessioni precedenti sono chiuse: se il recupero serviva perché
	// qualcun altro aveva l'accesso, lasciargliene una vanificherebbe tutto.
	if _, _, err := f.svc.Authenticate(t.Context(), vecchiaSessione.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("una sessione precedente è sopravvissuta alla reimpostazione: %v", err)
	}

	// Aver ricevuto il link dimostra il controllo della casella.
	if !f.store.User(user.ID).EmailVerified() {
		t.Error("l'indirizzo non è stato confermato dalla reimpostazione riuscita")
	}

	// E parte l'avviso di sicurezza (SPEC R21).
	f.svc.Wait()
	if _, found := f.mailer.Last(auth.KindPasswordChanged); !found {
		t.Error("nessun avviso di cambio password")
	}
}

// Un token di recupero vale una volta sola: è la proprietà che impedisce a chi
// intercetta un'email di riusare il link dopo il legittimo proprietario.
func TestResetPasswordConsumaIlTokenUnaVoltaSola(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	token := f.resetToken(testEmail)

	if err := f.svc.ResetPassword(t.Context(), token, "una-password-nuova-e-lunga", testClient); err != nil {
		t.Fatalf("primo uso: %v", err)
	}
	err := f.svc.ResetPassword(t.Context(), token, "un-altra-password-ancora", testClient)
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("secondo uso: errore = %v, atteso ErrInvalidToken", err)
	}
}

func TestResetPasswordRifiutaITokenNonUtilizzabili(t *testing.T) {
	tests := map[string]func(*fixture) string{
		"inesistente": func(*fixture) string { return "un-token-inventato" },
		"vuoto":       func(*fixture) string { return "" },
		"scaduto": func(f *fixture) string {
			token := f.resetToken(testEmail)
			f.clock.advance(auth.DefaultPasswordResetTTL + time.Minute)
			return token
		},
		"emesso per un altro scopo": func(f *fixture) string {
			// Il token di conferma indirizzo non deve valere come token di
			// recupero password: gli scopi sono separati nello schema e devono
			// esserlo anche nel controllo.
			msg, found := f.mailer.Last(auth.KindEmailVerification)
			if !found {
				f.t.Fatal("nessun token di conferma disponibile")
			}
			return msg.Token
		},
		"invalidato da una richiesta successiva": func(f *fixture) string {
			primo := f.resetToken(testEmail)
			f.mailer.Reset()
			f.resetToken(testEmail)
			return primo
		},
	}
	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.register(testEmail, testPassword)

			token := prepare(f)
			err := f.svc.ResetPassword(t.Context(), token, "una-password-nuova-e-lunga", testClient)
			if !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("errore = %v, atteso ErrInvalidToken", err)
			}
			// La password non è cambiata.
			if _, err := f.svc.Login(t.Context(), auth.LoginInput{
				Email: testEmail, Password: testPassword, Client: testClient,
			}); err != nil {
				t.Errorf("la password è stata cambiata da un token non valido: %v", err)
			}
		})
	}
}

// Una password che non rispetta la policy non deve bruciare il link: altrimenti
// un errore di battitura costringerebbe a ricominciare da capo.
func TestResetPasswordNonConsumaIlTokenSeLaPasswordENonValida(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	token := f.resetToken(testEmail)

	if err := f.svc.ResetPassword(t.Context(), token, "corta", testClient); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("errore = %v, atteso ErrPasswordTooShort", err)
	}

	// Il token è ancora valido.
	if err := f.svc.ResetPassword(t.Context(), token, "una-password-nuova-e-lunga", testClient); err != nil {
		t.Fatalf("il token è stato consumato da un tentativo non valido: %v", err)
	}
}

func TestResetPasswordELimitatoPerIndirizzoIP(t *testing.T) {
	f := newFixture(t, func(o *auth.Options) {
		o.Limits.TokenPerIP = ratelimit.Rule{Burst: 2, Window: 15 * time.Minute}
	})
	f.register(testEmail, testPassword)

	for range 2 {
		_ = f.svc.ResetPassword(t.Context(), "token-inventato", "una-password-nuova-e-lunga", testClient)
	}
	err := f.svc.ResetPassword(t.Context(), "token-inventato", "una-password-nuova-e-lunga", testClient)
	var limited *auth.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("errore = %v, atteso RateLimitedError", err)
	}
}

// ---------------------------------------------------------- cambio password

func TestChangePassword(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	corrente := f.login(testEmail, testPassword)
	altra := f.login(testEmail, testPassword)

	const nuova = "una-password-nuova-e-lunga"
	result, err := f.svc.ChangePassword(t.Context(), user, corrente.Session, testPassword, nuova)
	if err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// La sessione corrente ha un token nuovo, e il vecchio non vale più: se
	// qualcun altro lo aveva, il cambio password lo esclude.
	if result.Token == corrente.Token {
		t.Error("il token della sessione corrente non è stato rinnovato")
	}
	if _, _, err := f.svc.Authenticate(t.Context(), corrente.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("il token precedente è ancora valido: %v", err)
	}
	if _, _, err := f.svc.Authenticate(t.Context(), result.Token); err != nil {
		t.Errorf("il token nuovo non è valido: %v", err)
	}
	// E le altre sessioni sono chiuse.
	if _, _, err := f.svc.Authenticate(t.Context(), altra.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("un'altra sessione è sopravvissuta al cambio password: %v", err)
	}

	// Il contesto della sessione si conserva nella nuova.
	if result.Session.UserAgent != corrente.Session.UserAgent {
		t.Errorf("user agent = %q, atteso %q", result.Session.UserAgent, corrente.Session.UserAgent)
	}

	if _, err := f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: nuova, Client: testClient,
	}); err != nil {
		t.Errorf("la password nuova non funziona: %v", err)
	}

	f.svc.Wait()
	if _, found := f.mailer.Last(auth.KindPasswordChanged); !found {
		t.Error("nessun avviso di sicurezza dopo il cambio password")
	}
}

// Senza la password corrente una sessione rubata basterebbe a prendere possesso
// dell'account in modo definitivo.
func TestChangePasswordEsigeLaPasswordCorrente(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	corrente := f.login(testEmail, testPassword)

	_, err := f.svc.ChangePassword(t.Context(), user, corrente.Session,
		"password-sbagliata-ma-lunga", "una-password-nuova-e-lunga")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("errore = %v, atteso ErrInvalidCredentials", err)
	}

	// Niente è cambiato: la sessione e la password restano quelle di prima.
	if _, _, err := f.svc.Authenticate(t.Context(), corrente.Token); err != nil {
		t.Errorf("la sessione è stata invalidata da un tentativo fallito: %v", err)
	}
	if _, err := f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: testPassword, Client: testClient,
	}); err != nil {
		t.Errorf("la password è cambiata nonostante il tentativo fallito: %v", err)
	}
}

func TestChangePasswordRifiutaUnaPasswordDebole(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	corrente := f.login(testEmail, testPassword)

	if _, err := f.svc.ChangePassword(t.Context(), user, corrente.Session, testPassword, "corta"); !errors.Is(err, auth.ErrPasswordTooShort) {
		t.Fatalf("errore = %v, atteso ErrPasswordTooShort", err)
	}
}

// I tentativi di indovinare la password corrente da una sessione rubata
// incontrano lo stesso tetto del login.
func TestChangePasswordELimitato(t *testing.T) {
	f := newFixture(t, func(o *auth.Options) {
		o.Limits.LoginPerAccount = ratelimit.Rule{Burst: 3, Window: 15 * time.Minute}
	})
	user := f.register(testEmail, testPassword)
	corrente := f.login(testEmail, testPassword)

	for range 3 {
		_, _ = f.svc.ChangePassword(t.Context(), user, corrente.Session,
			"sbagliata-ma-lunga-abbastanza", "una-password-nuova-e-lunga")
	}
	_, err := f.svc.ChangePassword(t.Context(), user, corrente.Session,
		testPassword, "una-password-nuova-e-lunga")
	var limited *auth.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("errore = %v, atteso RateLimitedError", err)
	}
}

// -------------------------------------------------------- conferma indirizzo

func TestVerifyEmail(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)

	msg, found := f.mailer.Last(auth.KindEmailVerification)
	if !found {
		t.Fatal("nessun token di conferma")
	}

	if err := f.svc.VerifyEmail(t.Context(), msg.Token, testClient); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	verified := f.store.User(user.ID)
	if !verified.EmailVerified() {
		t.Fatal("l'indirizzo non risulta confermato")
	}
	if !verified.EmailVerifiedAt.Equal(f.clock.Now()) {
		t.Errorf("email_verified_at = %s, atteso %s", verified.EmailVerifiedAt, f.clock.Now())
	}

	// Il token vale una volta.
	if err := f.svc.VerifyEmail(t.Context(), msg.Token, testClient); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("secondo uso: errore = %v, atteso ErrInvalidToken", err)
	}
}

func TestVerifyEmailRifiutaUnTokenDiRecuperoPassword(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	token := f.resetToken(testEmail)

	// Gli scopi sono separati: un token di recupero password non deve poter
	// confermare un indirizzo, e viceversa.
	if err := f.svc.VerifyEmail(t.Context(), token, testClient); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("errore = %v, atteso ErrInvalidToken", err)
	}
}

func TestResendEmailVerification(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	primo, _ := f.mailer.Last(auth.KindEmailVerification)
	f.mailer.Reset()

	if err := f.svc.ResendEmailVerification(t.Context(), user, testClient); err != nil {
		t.Fatalf("ResendEmailVerification: %v", err)
	}
	f.svc.Wait()

	secondo, found := f.mailer.Last(auth.KindEmailVerification)
	if !found {
		t.Fatal("nessun secondo token inviato")
	}
	if secondo.Token == primo.Token {
		t.Error("il token è stato riemesso identico")
	}
	// Il precedente non vale più: un link nuovo deve sostituire il vecchio, non
	// aggiungersi a lui.
	if err := f.svc.VerifyEmail(t.Context(), primo.Token, testClient); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("il token precedente è ancora valido: %v", err)
	}
	if n := f.store.PendingTokens(user.ID, auth.PurposeEmailVerification); n != 1 {
		t.Errorf("token pendenti = %d, atteso 1", n)
	}

	// Su un indirizzo già confermato non c'è niente da rimandare.
	if err := f.svc.VerifyEmail(t.Context(), secondo.Token, testClient); err != nil {
		t.Fatalf("VerifyEmail: %v", err)
	}
	f.mailer.Reset()
	if err := f.svc.ResendEmailVerification(t.Context(), f.store.User(user.ID), testClient); err != nil {
		t.Fatalf("ResendEmailVerification: %v", err)
	}
	f.svc.Wait()
	if _, found := f.mailer.Last(auth.KindEmailVerification); found {
		t.Error("è partita una conferma per un indirizzo già confermato")
	}
}

// ---------------------------------------------------------------------- log

// SPEC §5: i log non contengono segreti né dati personali non necessari. Il test
// percorre l'intero flusso e cerca nel log tutto ciò che non dovrebbe esserci.
func TestNessunSegretoNeiLog(t *testing.T) {
	f := newFixture(t)

	user := f.register(testEmail, testPassword)
	result := f.login(testEmail, testPassword)
	_, _ = f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: "password-sbagliata-ma-lunga", Client: testClient,
	})
	_, _ = f.svc.Login(t.Context(), auth.LoginInput{
		Email: "nessuno@example.com", Password: testPassword, Client: testClient,
	})
	token := f.resetToken(testEmail)
	if err := f.svc.ResetPassword(t.Context(), token, "una-password-nuova-e-lunga", testClient); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	f.svc.Wait()

	logged := f.logs.text()
	if logged == "" {
		t.Fatal("nessun log registrato: il test non prova niente")
	}

	forbidden := map[string]string{
		"la password in chiaro":            testPassword,
		"la password nuova":                "una-password-nuova-e-lunga",
		"il token di recupero":             token,
		"il token di sessione":             result.Token,
		"l'impronta del token di sessione": result.Session.TokenHash,
		"l'hash della password":            f.store.PasswordHash(user.ID),
		"l'indirizzo email":                testEmail,
	}
	for name, secret := range forbidden {
		if secret == "" {
			t.Fatalf("%s: valore vuoto, il controllo non prova niente", name)
		}
		if strings.Contains(logged, secret) {
			t.Errorf("%s compare nei log", name)
		}
	}

	// Il log deve però restare utile: l'identificativo dell'utente e l'indirizzo
	// IP ci sono, altrimenti un abuso non sarebbe indagabile.
	if !strings.Contains(logged, user.ID) {
		t.Error("l'identificativo dell'utente non compare nei log: un abuso non sarebbe indagabile")
	}
	if !strings.Contains(logged, testClient.IP.String()) {
		t.Error("l'indirizzo IP non compare nei log")
	}
}

// ------------------------------------------------------------------ shutdown

// L'arresto attende il lavoro in coda: senza, un riavvio farebbe sparire in
// silenzio le email delle ultime richieste servite.
func TestShutdownAttendeIlLavoroInCoda(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)
	f.mailer.Reset()

	if err := f.svc.RequestPasswordReset(t.Context(), testEmail, testClient); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	if err := f.svc.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if _, found := f.mailer.Last(auth.KindPasswordReset); !found {
		t.Error("il lavoro in coda non è stato completato prima dell'arresto")
	}
}

func TestShutdownRispettaIlContesto(t *testing.T) {
	f := newFixture(t)
	f.register(testEmail, testPassword)

	blocked := make(chan struct{})
	slow := &blockingMailer{release: blocked}
	svc, err := auth.NewService(auth.Options{
		Store: f.store, Hasher: f.hasher, Keyring: f.keys, Mailer: slow,
		Logger: slog.New(newLogRecorder()), Now: f.clock.Now,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.RequestPasswordReset(t.Context(), testEmail, testClient); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := svc.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("errore = %v, atteso context.Canceled", err)
	}

	close(blocked)
	svc.Wait()
}

// TestConfirmPasswordVerificaSenzaCambiareNiente copre la conferma che la
// cancellazione dell'account pretende (R45).
//
// Le tre proprietà che contano: la password giusta passa, quella sbagliata
// riceve [auth.ErrInvalidCredentials], e **niente cambia** — le sessioni aperte
// restano tali, perché confermare non è cambiare la password.
func TestConfirmPasswordVerificaSenzaCambiareNiente(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)
	sessione := f.login(testEmail, testPassword)

	if err := f.svc.ConfirmPassword(t.Context(), user.ID, testPassword); err != nil {
		t.Fatalf("ConfirmPassword con la password giusta: %v", err)
	}
	if _, _, err := f.svc.Authenticate(t.Context(), sessione.Token); err != nil {
		t.Errorf("la conferma ha invalidato la sessione: %v", err)
	}

	err := f.svc.ConfirmPassword(t.Context(), user.ID, "password-sbagliata-ma-lunga")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("ConfirmPassword con la password sbagliata: %v, atteso ErrInvalidCredentials", err)
	}
}

// TestConfirmPasswordUsaIlSecchioDelLogin: chi prova password a tentativi da una
// sessione rubata deve incontrare lo stesso tetto del login, non un secondo
// secchio con le proprie soglie.
//
// Senza, la rotta di cancellazione sarebbe un oracolo su cui indovinare la
// password senza limiti — e il premio, in quel caso, è la distruzione
// dell'account.
func TestConfirmPasswordUsaIlSecchioDelLogin(t *testing.T) {
	f := newFixture(t)
	user := f.register(testEmail, testPassword)

	var limited *auth.RateLimitedError
	for i := 0; i < 50; i++ {
		err := f.svc.ConfirmPassword(t.Context(), user.ID, "password-sbagliata-ma-lunga")
		if errors.As(err, &limited) {
			break
		}
	}
	if limited == nil {
		t.Fatal("cinquanta tentativi di conferma non hanno incontrato nessun tetto")
	}

	// E il tetto è quello dell'account: il login con la password giusta lo
	// incontra a sua volta, perché è lo stesso secchio.
	if _, err := f.svc.Login(t.Context(), auth.LoginInput{
		Email: testEmail, Password: testPassword,
	}); !errors.As(err, &limited) {
		t.Errorf("il login non ha incontrato il tetto riempito dalle conferme: %v", err)
	}
}
