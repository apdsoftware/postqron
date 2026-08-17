package apikeys_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/apikeystest"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/authtest"
	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

const testSecret = "un-segreto-di-prova-abbastanza-lungo-da-essere-accettato"

// ---------------------------------------------------------------- impalcatura

// orologio è un orologio che i test fanno avanzare a mano: le scadenze e la
// soglia di `last_used_at` sono griglie temporali, e aspettarle davvero
// renderebbe la suite lenta.
type orologio struct{ now time.Time }

func (c *orologio) Now() time.Time { return c.now }

func (c *orologio) avanza(d time.Duration) { c.now = c.now.Add(d) }

type fixture struct {
	t       *testing.T
	svc     *apikeys.Service
	store   *apikeystest.Store
	users   *authtest.Store
	keyring auth.Keyring
	clock   *orologio
	logs    *bytes.Buffer
	user    auth.User
}

func newFixture(t *testing.T, tune ...func(*apikeys.Options)) *fixture {
	t.Helper()

	clock := &orologio{now: time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)}
	store := apikeystest.NewStore()
	users := authtest.NewStore()
	keyring, err := auth.NewKeyring(testSecret)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	opts := apikeys.Options{
		Store:   store,
		Users:   users,
		Keyring: keyring,
		Logger:  logger,
		Now:     clock.Now,
	}
	for _, fn := range tune {
		fn(&opts)
	}

	svc, err := apikeys.NewService(opts)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	user, err := users.CreateUser(context.Background(), "mario.rossi@example.com", "hash", "Mario Rossi")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	return &fixture{
		t: t, svc: svc, store: store, users: users,
		keyring: keyring, clock: clock, logs: logs, user: user,
	}
}

// crea emette una chiave con gli scope indicati.
func (f *fixture) crea(scopes ...apikeys.Scope) apikeys.Created {
	f.t.Helper()
	created, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
		Name:   "chiave di prova",
		Scopes: scopes,
		Client: apikeys.Client{IP: netip.MustParseAddr("203.0.113.7")},
	})
	if err != nil {
		f.t.Fatalf("Create: %v", err)
	}
	return created
}

// autentica risolve una chiave in chiaro.
func (f *fixture) autentica(token string) (auth.User, apikeys.Key, error) {
	f.t.Helper()
	return f.svc.Authenticate(context.Background(), token,
		apikeys.Client{IP: netip.MustParseAddr("203.0.113.7")})
}

// ------------------------------------------------------- la chiave in chiaro

// La chiave in chiaro esiste una volta sola: la creazione la restituisce, e da
// quel momento nessuna lettura la contiene più. Il test non si limita a
// verificare l'elenco: controlla che il valore non compaia in *nessun* campo di
// ciò che l'archivio conserva, che è la forma in cui la proprietà è vera anche
// dopo che qualcuno aggiungerà una colonna.
func TestLaChiaveInChiaroNonEsistePiuDopoLaCreazione(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	if created.Secret == "" {
		t.Fatal("la creazione non ha restituito la chiave in chiaro")
	}
	if created.Key.Hash == created.Secret {
		t.Fatal("l'impronta coincide con la chiave: non è un hash")
	}

	// A riposo: nessuna delle impronte conservate contiene il valore in chiaro.
	for _, hash := range f.store.Hashes() {
		if strings.Contains(hash, created.Secret) {
			t.Error("un'impronta conservata contiene la chiave in chiaro")
		}
	}

	// In lettura: l'elenco non ha modo di restituirla, perché apikeys.Key non ha
	// il campo. Ciò che si può verificare è che nessuna stringa della struttura
	// letta sia la chiave.
	keys, err := f.svc.List(context.Background(), f.user.ID, true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("chiavi in elenco = %d, attesa 1", len(keys))
	}
	letta := keys[0]
	for name, value := range map[string]string{
		"ID": letta.ID, "Name": letta.Name, "Prefix": letta.Prefix, "Hash": letta.Hash,
	} {
		if value == created.Secret {
			t.Errorf("il campo %s dell'elenco contiene la chiave in chiaro", name)
		}
	}
}

// L'impronta conservata è quella che il keyring produce, non un'altra
// costruzione: è ciò che garantisce che la ricerca per uguaglianza trovi la
// chiave.
func TestLImprontaConservataEQuellaDelKeyring(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	if got, want := created.Key.Hash, f.keyring.APIKeyHash(created.Secret); got != want {
		t.Errorf("impronta = %q, attesa %q", got, want)
	}
}

// Il prefisso conservato è la testa della chiave e serve a riconoscerla: deve
// combaciare con l'inizio del valore in chiaro, ed essere troppo corto per
// avvicinarsi a ricostruirla.
func TestIlPrefissoIdentificaLaChiaveSenzaRivelarla(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	if !strings.HasPrefix(created.Secret, apikeys.TokenPrefix) {
		t.Errorf("la chiave non comincia per %q: %q", apikeys.TokenPrefix, created.Key.Prefix)
	}
	if !strings.HasPrefix(created.Secret, created.Key.Prefix) {
		t.Error("il prefisso conservato non è la testa della chiave")
	}
	if created.Key.Prefix == created.Secret {
		t.Fatal("il prefisso è la chiave intera")
	}
	// Il vincolo `api_keys_prefix_check` della 0002 ammette 4..32 caratteri.
	if n := len(created.Key.Prefix); n < 4 || n > 32 {
		t.Errorf("lunghezza del prefisso = %d, fuori dai 4..32 che la 0002 ammette", n)
	}
}

// Due chiavi create di seguito non sono uguali: se lo fossero, il generatore non
// starebbe leggendo dal CSPRNG.
func TestDueChiaviSonoDiverse(t *testing.T) {
	f := newFixture(t)
	first := f.crea(apikeys.ScopeJobsRead)
	second := f.crea(apikeys.ScopeJobsRead)

	if first.Secret == second.Secret {
		t.Fatal("due chiavi identiche")
	}
	if first.Key.Hash == second.Key.Hash {
		t.Fatal("due impronte identiche")
	}
}

// ----------------------------------------------------------- autenticazione

// Il percorso felice: la chiave appena creata autentica il suo proprietario e
// porta i suoi scope.
func TestUnaChiaveValidaAutenticaIlProprietario(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead, apikeys.ScopeExecutionsTrigger)

	user, key, err := f.autentica(created.Secret)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if user.ID != f.user.ID {
		t.Errorf("utente = %q, atteso %q", user.ID, f.user.ID)
	}
	if key.ID != created.Key.ID {
		t.Errorf("chiave = %q, attesa %q", key.ID, created.Key.ID)
	}
	if !key.Allows(apikeys.ScopeJobsRead) || !key.Allows(apikeys.ScopeExecutionsTrigger) {
		t.Errorf("scope non riportati: %v", key.Scopes)
	}
	if key.Allows(apikeys.ScopeJobsWrite) {
		t.Error("la chiave porta uno scope che non le è stato assegnato")
	}
}

// La ricerca è una lettura per impronta, non una scansione: autenticare una
// chiave fra molte costa **una** chiamata a KeyByHash e nessuna a ListKeys,
// qualunque sia il numero di chiavi in archivio.
func TestLaRicercaNonScandisceLeChiavi(t *testing.T) {
	f := newFixture(t, func(opts *apikeys.Options) {
		// Il tetto alle creazioni non è ciò che si sta verificando qui.
		opts.Limits = apikeys.Limits{CreatePerUser: ratelimit.Rule{Burst: 100, Window: time.Hour}}
	})
	var target apikeys.Created
	for i := 0; i < 20; i++ {
		created := f.crea(apikeys.ScopeJobsRead)
		if i == 17 {
			target = created
		}
	}

	prima := f.store.CallCount("KeyByHash")
	if _, _, err := f.autentica(target.Secret); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	if letture := f.store.CallCount("KeyByHash") - prima; letture != 1 {
		t.Errorf("letture per impronta = %d, attesa 1", letture)
	}
	if scansioni := f.store.CallCount("ListKeys"); scansioni != 0 {
		t.Errorf("l'autenticazione ha elencato le chiavi %d volte: è una scansione", scansioni)
	}
}

// Una chiave che non è mai esistita non autentica, e non costa nemmeno una
// lettura se non ha la forma di una chiave: il prefisso è ciò che permette di
// scartarla prima del database.
func TestUnaChiaveInesistenteNonAutentica(t *testing.T) {
	f := newFixture(t)
	f.crea(apikeys.ScopeJobsRead)

	tests := map[string]struct {
		token         string
		attesaLettura bool
	}{
		"vuota":            {"", false},
		"senza prefisso":   {"non-e-una-chiave-postqron", false},
		"prefisso e nulla": {apikeys.TokenPrefix + "valore-inventato", true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			prima := f.store.CallCount("KeyByHash")
			_, _, err := f.autentica(tc.token)
			if !errors.Is(err, apikeys.ErrInvalidKey) {
				t.Fatalf("errore = %v, atteso ErrInvalidKey", err)
			}
			letta := f.store.CallCount("KeyByHash") > prima
			if letta != tc.attesaLettura {
				t.Errorf("lettura del database = %v, attesa %v", letta, tc.attesaLettura)
			}
		})
	}
}

// La revoca ha effetto al tentativo successivo. Non c'è nessuna attesa: la stessa
// chiave che ha appena funzionato non funziona più, e l'orologio non si è mosso.
func TestLaRevocaHaEffettoAlTentativoSuccessivo(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Fatalf("la chiave doveva funzionare prima della revoca: %v", err)
	}

	if err := f.svc.Revoke(context.Background(), f.user.ID, created.Key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Nessun avanzamento dell'orologio, nessuna invalidazione di cache: il
	// tentativo immediatamente successivo deve già fallire.
	if _, _, err := f.autentica(created.Secret); !errors.Is(err, apikeys.ErrInvalidKey) {
		t.Fatalf("errore dopo la revoca = %v, atteso ErrInvalidKey", err)
	}
}

// Revocare una chiave non tocca le altre: è il caso in cui una revoca fatta per
// precauzione non deve spegnere l'automazione che funziona.
func TestLaRevocaNonToccaLeAltreChiavi(t *testing.T) {
	f := newFixture(t)
	vittima := f.crea(apikeys.ScopeJobsRead)
	superstite := f.crea(apikeys.ScopeJobsRead)

	if err := f.svc.Revoke(context.Background(), f.user.ID, vittima.Key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, _, err := f.autentica(superstite.Secret); err != nil {
		t.Errorf("la seconda chiave non doveva essere toccata: %v", err)
	}
}

// Una chiave altrui non si revoca: l'ambito sull'utente è parte del contratto, e
// «non esiste» e «non è tua» rispondono allo stesso modo perché distinguerli
// direbbe a chiunque se un identificativo altrui è vivo.
func TestNonSiRevocaLaChiaveDiUnAltroUtente(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	altro, err := f.users.CreateUser(context.Background(), "altro@example.com", "hash", "Altro")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	err = f.svc.Revoke(context.Background(), altro.ID, created.Key.ID)
	if !errors.Is(err, apikeys.ErrKeyNotFound) {
		t.Fatalf("errore = %v, atteso ErrKeyNotFound", err)
	}
	// E la chiave deve essere ancora buona.
	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Errorf("la chiave è stata revocata da un altro utente: %v", err)
	}

	// Anche una seconda revoca da parte del proprietario è «non trovata»:
	// sovrascrivere la data sposterebbe in avanti il momento della revoca, che è
	// l'unica informazione che quella riga porta.
	if err := f.svc.Revoke(context.Background(), f.user.ID, created.Key.ID); err != nil {
		t.Fatalf("prima revoca: %v", err)
	}
	if err := f.svc.Revoke(context.Background(), f.user.ID, created.Key.ID); !errors.Is(err, apikeys.ErrKeyNotFound) {
		t.Errorf("seconda revoca = %v, atteso ErrKeyNotFound", err)
	}
}

// Una chiave scaduta non autentica, e la scadenza è valutata sull'orologio del
// servizio: è la stessa riga che prima funzionava.
func TestUnaChiaveScadutaNonAutentica(t *testing.T) {
	f := newFixture(t)
	scadenza := f.clock.now.Add(time.Hour)
	created, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
		Name:      "chiave a termine",
		Scopes:    []apikeys.Scope{apikeys.ScopeJobsRead},
		ExpiresAt: &scadenza,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Fatalf("prima della scadenza doveva funzionare: %v", err)
	}

	f.clock.avanza(time.Hour + time.Second)
	if _, _, err := f.autentica(created.Secret); !errors.Is(err, apikeys.ErrInvalidKey) {
		t.Fatalf("errore dopo la scadenza = %v, atteso ErrInvalidKey", err)
	}
}

// Un account sospeso non lavora nemmeno con una chiave valida, e l'errore è
// distinto: per arrivare qui bisogna già avere la chiave in mano, quindi non si
// rivela niente a chi non lo sappia, e il proprietario merita di capire perché.
func TestUnAccountSospesoNonAutenticaConLaChiave(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	f.users.Suspend(f.user.ID, f.clock.now)
	if _, _, err := f.autentica(created.Secret); !errors.Is(err, auth.ErrAccountSuspended) {
		t.Fatalf("errore = %v, atteso ErrAccountSuspended", err)
	}
}

// Una chiave il cui proprietario non c'è più non autentica nessuno.
func TestUnaChiaveOrfanaNonAutentica(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	f.users.Fail("UserByID", auth.ErrNotFound)
	if _, _, err := f.autentica(created.Secret); !errors.Is(err, apikeys.ErrInvalidKey) {
		t.Fatalf("errore = %v, atteso ErrInvalidKey", err)
	}
}

// Se l'archivio restituisce una riga la cui impronta non è quella cercata,
// nessuno viene autenticato: è il controllo che rende il contratto «solo
// l'impronta esatta autentica» una proprietà verificabile invece di una fiducia
// in ogni SELECT scritta altrove.
func TestUnaRigaConImprontaSbagliataNonAutentica(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	// Si sostituisce l'impronta conservata e si costringe l'archivio a restituire
	// comunque quella riga: è un archivio che ignora il parametro, cioè il difetto
	// contro cui il confronto a valle esiste.
	rotta := f.store.Key(created.Key.ID)
	rotta.Hash = f.keyring.APIKeyHash("una-chiave-completamente-diversa")
	f.store.Seed(rotta)
	f.store.AlwaysReturn(created.Key.ID)

	if _, _, err := f.autentica(created.Secret); !errors.Is(err, apikeys.ErrInvalidKey) {
		t.Fatalf("errore = %v, atteso ErrInvalidKey", err)
	}

	// Controprova: senza la manomissione dell'impronta, lo stesso archivio
	// «generoso» autentica — quindi il rifiuto di sopra viene dal confronto e non
	// dalla ricerca che non ha trovato nulla.
	sana := f.store.Key(created.Key.ID)
	sana.Hash = f.keyring.APIKeyHash(created.Secret)
	f.store.Seed(sana)
	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Fatalf("controprova: %v", err)
	}
}

// `last_used_at` si aggiorna, ma non a ogni richiesta: senza soglia, ogni
// richiesta autenticata sarebbe anche una scrittura, e per un'API le richieste
// autenticate sono il traffico.
func TestLUltimoUtilizzoSiAggiornaConUnaSoglia(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if scritture := f.store.CallCount("TouchKey"); scritture != 1 {
		t.Fatalf("scritture al primo uso = %d, attesa 1", scritture)
	}

	// Subito dopo: nessuna seconda scrittura.
	f.clock.avanza(time.Minute)
	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if scritture := f.store.CallCount("TouchKey"); scritture != 1 {
		t.Errorf("scritture entro la soglia = %d, attesa 1", scritture)
	}

	// Oltre la soglia: si riscrive.
	f.clock.avanza(10 * time.Minute)
	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if scritture := f.store.CallCount("TouchKey"); scritture != 2 {
		t.Errorf("scritture oltre la soglia = %d, attese 2", scritture)
	}
}

// Un guasto nell'aggiornamento di `last_used_at` non fa fallire la richiesta: la
// chiave è buona, e la traccia dell'uso non è la richiesta.
func TestUnGuastoSullUltimoUtilizzoNonRifiutaLaRichiesta(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead)

	f.store.Fail("TouchKey", errors.New("database non raggiungibile"))
	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Fatalf("Authenticate = %v, atteso nessun errore", err)
	}
}

// ------------------------------------------------------------------- scope

// Gli scope decidono, e una chiave non porta ciò che non le è stato dato. È la
// verifica del predicato; il rifiuto sulla rotta è in internal/httpapi.
func TestGliScopeAutorizzanoSoloQuelloCheContengono(t *testing.T) {
	key := apikeys.Key{Scopes: []apikeys.Scope{apikeys.ScopeJobsRead}}

	if !key.Allows(apikeys.ScopeJobsRead) {
		t.Error("lo scope assegnato non autorizza")
	}
	for _, negato := range []apikeys.Scope{
		apikeys.ScopeJobsWrite, apikeys.ScopeExecutionsRead, apikeys.ScopeExecutionsTrigger,
	} {
		if key.Allows(negato) {
			t.Errorf("lo scope %q non assegnato autorizza", negato)
		}
	}

	// Il valore zero non autorizza niente: una struttura non inizializzata non
	// deve aprire nessuna porta.
	var vuota apikeys.Key
	for _, scope := range apikeys.Scopes() {
		if vuota.Allows(scope) {
			t.Errorf("una chiave senza scope autorizza %q", scope)
		}
	}
}

// Una chiave senza scope non si crea: lo schema la ammette, ma è quasi sempre il
// campo dimenticato nella richiesta, e accettarla costa una segnalazione «la mia
// chiave dà 403 su tutto».
func TestUnaChiaveSenzaScopeNonSiCrea(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{Name: "senza scope"})
	invalid, ok := apikeys.AsValidation(err)
	if !ok {
		t.Fatalf("errore = %v, atteso un ValidationError", err)
	}
	if len(invalid.Fields) != 1 || invalid.Fields[0].Field != "scopes" {
		t.Errorf("campi rifiutati = %+v, atteso il solo `scopes`", invalid.Fields)
	}
	if f.store.Count() != 0 {
		t.Error("una chiave è stata scritta comunque")
	}
}

// Uno scope inventato non si assegna: se passasse, la chiave porterebbe un
// permesso che nessuna rotta riconosce, e chi l'ha creata crederebbe di avere un
// accesso che non ha.
func TestUnoScopeSconosciutoNonSiAssegna(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
		Name:   "chiave",
		Scopes: []apikeys.Scope{apikeys.ScopeJobsRead, "jobs:*"},
	})
	invalid, ok := apikeys.AsValidation(err)
	if !ok {
		t.Fatalf("errore = %v, atteso un ValidationError", err)
	}
	if len(invalid.Fields) == 0 || invalid.Fields[0].Code != "unknown_scope" {
		t.Errorf("campi rifiutati = %+v, atteso unknown_scope", invalid.Fields)
	}
}

// Gli scope ripetuti si deduplicano: la 0002 impone `has_unique_elements`, e un
// doppione arriverebbe fino al database diventando un 500 al posto di un 201.
func TestGliScopeRipetutiSiDeduplicano(t *testing.T) {
	f := newFixture(t)
	created := f.crea(apikeys.ScopeJobsRead, apikeys.ScopeJobsRead, apikeys.ScopeJobsWrite)

	if len(created.Key.Scopes) != 2 {
		t.Errorf("scope = %v, attesi 2 distinti", created.Key.Scopes)
	}
}

// ------------------------------------------------------------- validazione

// Il nome è obbligatorio: senza, in elenco le chiavi sono indistinguibili e non
// si sa quale revocare.
func TestIlNomeEObbligatorioEHaUnTetto(t *testing.T) {
	f := newFixture(t)

	tests := map[string]struct{ nome, code string }{
		"vuoto":        {"", "required"},
		"soli spazi":   {"   ", "required"},
		"troppo lungo": {strings.Repeat("a", 101), "too_long"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
				Name:   tc.nome,
				Scopes: []apikeys.Scope{apikeys.ScopeJobsRead},
			})
			invalid, ok := apikeys.AsValidation(err)
			if !ok {
				t.Fatalf("errore = %v, atteso un ValidationError", err)
			}
			if invalid.Fields[0].Field != "name" || invalid.Fields[0].Code != tc.code {
				t.Errorf("campo rifiutato = %+v, atteso name/%s", invalid.Fields[0], tc.code)
			}
		})
	}

	// Il nome accettato è quello con gli spazi ai bordi rimossi.
	created, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
		Name:   "  produzione  ",
		Scopes: []apikeys.Scope{apikeys.ScopeJobsRead},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Key.Name != "produzione" {
		t.Errorf("nome = %q, atteso %q", created.Key.Name, "produzione")
	}
}

// Una scadenza nel passato o troppo lontana non si accetta: la prima creerebbe
// una chiave nata morta, la seconda non è più una misura di sicurezza.
func TestLaScadenzaDeveEsserePlausibile(t *testing.T) {
	f := newFixture(t)

	tests := map[string]struct {
		scadenza time.Time
		code     string
	}{
		"nel passato": {f.clock.now.Add(-time.Hour), "in_the_past"},
		"adesso":      {f.clock.now, "in_the_past"},
		"fra dieci anni": {
			f.clock.now.Add(10 * 365 * 24 * time.Hour), "too_far",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			scadenza := tc.scadenza
			_, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
				Name:      "chiave",
				Scopes:    []apikeys.Scope{apikeys.ScopeJobsRead},
				ExpiresAt: &scadenza,
			})
			invalid, ok := apikeys.AsValidation(err)
			if !ok {
				t.Fatalf("errore = %v, atteso un ValidationError", err)
			}
			if invalid.Fields[0].Field != "expires_at" || invalid.Fields[0].Code != tc.code {
				t.Errorf("campo rifiutato = %+v, atteso expires_at/%s", invalid.Fields[0], tc.code)
			}
		})
	}
}

// Il tetto al numero di chiavi attive esiste per non lasciare una scrittura senza
// limite a un account autenticato. Non è un limite di piano: quello è R15.
func TestIlTettoAlleChiaviAttiveSiApplica(t *testing.T) {
	f := newFixture(t, func(opts *apikeys.Options) {
		// Il limite di creazione per utente non deve scattare prima del tetto che
		// si sta verificando.
		opts.Limits = apikeys.Limits{
			CreatePerUser: ratelimit.Rule{Burst: apikeys.MaxActiveKeys + 10, Window: time.Hour},
		}
	})

	for i := 0; i < apikeys.MaxActiveKeys; i++ {
		f.crea(apikeys.ScopeJobsRead)
	}
	_, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
		Name:   "una di troppo",
		Scopes: []apikeys.Scope{apikeys.ScopeJobsRead},
	})
	if !errors.Is(err, apikeys.ErrTooManyKeys) {
		t.Fatalf("errore = %v, atteso ErrTooManyKeys", err)
	}

	// Una chiave revocata libera un posto: il tetto è sulle chiavi vive, non su
	// quelle mai create.
	keys, err := f.svc.List(context.Background(), f.user.ID, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := f.svc.Revoke(context.Background(), f.user.ID, keys[0].ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := f.svc.Create(context.Background(), f.user.ID, apikeys.CreateInput{
		Name:   "al posto di quella revocata",
		Scopes: []apikeys.Scope{apikeys.ScopeJobsRead},
	}); err != nil {
		t.Errorf("Create dopo una revoca: %v", err)
	}
}

// ------------------------------------------------------------ rate limiting

// I tentativi con chiave sono limitati per indirizzo: le chiavi non sono
// indovinabili, ma «autenticati con una chiave qualunque» non deve diventare un
// modo di far leggere il database gratis.
func TestITentativiFallitiSonoLimitatiPerIndirizzo(t *testing.T) {
	f := newFixture(t, func(opts *apikeys.Options) {
		opts.Limits = apikeys.Limits{AuthPerIP: ratelimit.Rule{Burst: 3, Window: time.Minute}}
	})

	inventata := apikeys.TokenPrefix + "chiave-che-non-esiste"
	for i := 0; i < 3; i++ {
		if _, _, err := f.autentica(inventata); !errors.Is(err, apikeys.ErrInvalidKey) {
			t.Fatalf("tentativo %d: errore = %v, atteso ErrInvalidKey", i, err)
		}
	}

	var limited *auth.RateLimitedError
	_, _, err := f.autentica(inventata)
	if !errors.As(err, &limited) {
		t.Fatalf("errore al quarto tentativo = %v, atteso RateLimitedError", err)
	}
	if limited.RetryAfter <= 0 {
		t.Error("RetryAfter non indica un'attesa")
	}
}

// Il traffico legittimo non accumula: un successo restituisce il credito, quindi
// un client che funziona non si autoesclude mai — che è la ragione per cui questo
// tetto non è il rate limiting generale delle API (R10, #398).
func TestUnSuccessoRestituisceIlCredito(t *testing.T) {
	f := newFixture(t, func(opts *apikeys.Options) {
		opts.Limits = apikeys.Limits{AuthPerIP: ratelimit.Rule{Burst: 3, Window: time.Minute}}
	})
	created := f.crea(apikeys.ScopeJobsRead)

	// Due tentativi falliti, poi uno riuscito, poi molti riusciti: nessuno deve
	// incontrare il limite.
	inventata := apikeys.TokenPrefix + "chiave-che-non-esiste"
	for i := 0; i < 2; i++ {
		if _, _, err := f.autentica(inventata); !errors.Is(err, apikeys.ErrInvalidKey) {
			t.Fatalf("errore = %v, atteso ErrInvalidKey", err)
		}
	}
	for i := 0; i < 20; i++ {
		if _, _, err := f.autentica(created.Secret); err != nil {
			t.Fatalf("richiesta legittima %d rifiutata: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------- log

// Nessuna chiave nei log, in nessuno dei percorsi che li scrivono: né il valore
// in chiaro né la sua impronta (SPEC §5, DoD della issue). Il prefisso invece
// c'è, perché non è segreto ed è ciò che permette di capire quale chiave ha fatto
// cosa.
func TestNessunaChiaveNeiLog(t *testing.T) {
	f := newFixture(t)

	created := f.crea(apikeys.ScopeJobsRead)
	if _, _, err := f.autentica(created.Secret); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	// I percorsi di rifiuto, che sono quelli in cui è più facile far scivolare il
	// valore tentato in un messaggio di diagnostica.
	if _, _, err := f.autentica(apikeys.TokenPrefix + "chiave-inventata"); err == nil {
		t.Fatal("una chiave inventata non doveva autenticare")
	}
	if err := f.svc.Revoke(context.Background(), f.user.ID, created.Key.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, _, err := f.autentica(created.Secret); err == nil {
		t.Fatal("una chiave revocata non doveva autenticare")
	}

	logs := f.logs.String()
	if logs == "" {
		t.Fatal("nessun log prodotto: il test non sta verificando niente")
	}
	if strings.Contains(logs, created.Secret) {
		t.Error("i log contengono la chiave in chiaro")
	}
	// Anche il segreto privato del prefisso: `pq_live_` da solo comparirebbe
	// legittimamente nel prefisso mostrabile.
	if resto := strings.TrimPrefix(created.Secret, created.Key.Prefix); strings.Contains(logs, resto) {
		t.Error("i log contengono la coda della chiave")
	}
	if strings.Contains(logs, created.Key.Hash) {
		t.Error("i log contengono l'impronta della chiave")
	}
	if !strings.Contains(logs, created.Key.Prefix) {
		t.Error("i log non contengono il prefisso: non si può sapere quale chiave ha fatto cosa")
	}
}

// ------------------------------------------------------------- costruzione

// Il servizio non parte senza le sue dipendenze: scoprirlo alla prima richiesta
// autenticata sarebbe un 500 al posto di un errore d'avvio.
func TestNewServiceEsigeLeDipendenze(t *testing.T) {
	keyring, err := auth.NewKeyring(testSecret)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	valido := apikeys.Options{
		Store: apikeystest.NewStore(), Users: authtest.NewStore(), Keyring: keyring,
	}

	tests := map[string]func(*apikeys.Options){
		"senza Store":   func(o *apikeys.Options) { o.Store = nil },
		"senza Users":   func(o *apikeys.Options) { o.Users = nil },
		"senza Keyring": func(o *apikeys.Options) { o.Keyring = auth.Keyring{} },
	}
	for name, rompi := range tests {
		t.Run(name, func(t *testing.T) {
			opts := valido
			rompi(&opts)
			if _, err := apikeys.NewService(opts); err == nil {
				t.Fatal("atteso un errore")
			}
		})
	}

	if _, err := apikeys.NewService(valido); err != nil {
		t.Errorf("con tutte le dipendenze: %v", err)
	}
}
