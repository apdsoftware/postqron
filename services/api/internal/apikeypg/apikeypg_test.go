package apikeypg_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/apikeypg"
	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
)

// Le proprietà provate qui sono quelle che **dipendono da PostgreSQL** e che i
// test in memoria di internal/apikeys non possono garantire: l'unicità
// dell'impronta come vincolo dello schema, l'ambito della revoca imposto dalla
// clausola WHERE, e il fatto che gli scope sopravvivano al giro attraverso una
// colonna `text[]`.

// ---------------------------------------------------------------- impalcatura

type fixture struct {
	t     *testing.T
	store *apikeypg.Store
	pool  *pgxpool.Pool
	user  string
	now   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := newTestDatabase(t)
	store, err := apikeypg.New(pool)
	if err != nil {
		t.Fatalf("apikeypg.New: %v", err)
	}
	return &fixture{
		t:     t,
		store: store,
		pool:  pool,
		user:  createUser(t, pool, "mario.rossi@example.com"),
		// Troncato al microsecondo: `timestamptz` non conserva i nanosecondi, e un
		// confronto esatto fallirebbe sul giro di andata e ritorno.
		now: time.Now().UTC().Truncate(time.Microsecond),
	}
}

// chiave compone una chiave da inserire.
func (f *fixture) chiave(nome, impronta string, scopes ...apikeys.Scope) apikeys.Key {
	return apikeys.Key{
		UserID:    f.user,
		Name:      nome,
		Prefix:    apikeys.TokenPrefix + "a1b2",
		Hash:      impronta,
		Scopes:    scopes,
		CreatedAt: f.now,
	}
}

func (f *fixture) crea(key apikeys.Key) apikeys.Key {
	f.t.Helper()
	created, err := f.store.CreateKey(f.t.Context(), key)
	if err != nil {
		f.t.Fatalf("CreateKey: %v", err)
	}
	return created
}

// ------------------------------------------------------- creazione e lettura

// Il giro di andata e ritorno conserva tutto ciò che la riga porta, scope
// compresi: sono l'unico campo che attraversa una colonna `text[]`, ed è il punto
// in cui una conversione sbagliata si vedrebbe solo in produzione.
func TestLaChiaveSopravviveAlGiroNelDatabase(t *testing.T) {
	f := newFixture(t)
	scadenza := f.now.Add(24 * time.Hour)

	in := f.chiave("produzione", strings.Repeat("a", 64),
		apikeys.ScopeJobsRead, apikeys.ScopeExecutionsTrigger)
	in.ExpiresAt = &scadenza

	created := f.crea(in)
	if created.ID == "" {
		t.Fatal("l'INSERT non ha restituito l'identificativo")
	}

	letta, err := f.store.KeyByHash(t.Context(), in.Hash)
	if err != nil {
		t.Fatalf("KeyByHash: %v", err)
	}
	if letta.ID != created.ID {
		t.Errorf("identificativo = %q, atteso %q", letta.ID, created.ID)
	}
	if letta.Name != "produzione" || letta.Prefix != in.Prefix || letta.Hash != in.Hash {
		t.Errorf("campi = %q/%q/%q, attesi %q/%q/%q",
			letta.Name, letta.Prefix, letta.Hash, in.Name, in.Prefix, in.Hash)
	}
	if len(letta.Scopes) != 2 ||
		!letta.Allows(apikeys.ScopeJobsRead) ||
		!letta.Allows(apikeys.ScopeExecutionsTrigger) {
		t.Errorf("scope = %v, attesi jobs:read ed executions:trigger", letta.Scopes)
	}
	if letta.ExpiresAt == nil || !letta.ExpiresAt.Equal(scadenza) {
		t.Errorf("scadenza = %v, attesa %v", letta.ExpiresAt, scadenza)
	}
	if letta.LastUsedAt != nil || letta.RevokedAt != nil {
		t.Error("una chiave appena creata non è né usata né revocata")
	}
}

// L'impronta è unica: è l'indice `api_keys_key_hash_key` della 0002, ed è la
// garanzia che una lettura per impronta non possa restituire più di una riga.
func TestLImprontaEUnica(t *testing.T) {
	f := newFixture(t)
	impronta := strings.Repeat("b", 64)

	f.crea(f.chiave("prima", impronta, apikeys.ScopeJobsRead))

	altro := createUser(t, f.pool, "altro@example.com")
	duplicata := f.chiave("seconda", impronta, apikeys.ScopeJobsRead)
	// Anche di un altro utente: l'unicità è globale, non per account.
	duplicata.UserID = altro

	if _, err := f.store.CreateKey(t.Context(), duplicata); err == nil {
		t.Fatal("un'impronta duplicata è stata accettata")
	}
}

// Una chiave inesistente è ErrNotFound, non un errore di lettura: è la
// distinzione su cui il Service decide di rispondere 401.
func TestUnaChiaveInesistenteENotFound(t *testing.T) {
	f := newFixture(t)

	_, err := f.store.KeyByHash(t.Context(), strings.Repeat("c", 64))
	if !errors.Is(err, apikeys.ErrNotFound) {
		t.Fatalf("errore = %v, atteso ErrNotFound", err)
	}
}

// La ricerca per impronta restituisce anche le chiavi revocate e scadute: è il
// Service a decidere che non valgono più, e filtrarle qui renderebbe impossibile
// distinguere nei log una chiave mai esistita da una ritirata.
func TestLaRicercaRestituisceAncheLeChiaviSpente(t *testing.T) {
	f := newFixture(t)

	revocata := f.crea(f.chiave("revocata", strings.Repeat("d", 64), apikeys.ScopeJobsRead))
	if err := f.store.RevokeKey(t.Context(), f.user, revocata.ID, f.now); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	scaduta := f.chiave("scaduta", strings.Repeat("e", 64), apikeys.ScopeJobsRead)
	passato := f.now.Add(-time.Hour)
	scaduta.ExpiresAt = &passato
	f.crea(scaduta)

	for nome, impronta := range map[string]string{
		"revocata": strings.Repeat("d", 64),
		"scaduta":  strings.Repeat("e", 64),
	} {
		letta, err := f.store.KeyByHash(t.Context(), impronta)
		if err != nil {
			t.Fatalf("KeyByHash(%s): %v", nome, err)
		}
		if letta.Active(f.now) {
			t.Errorf("la chiave %s risulta attiva", nome)
		}
	}
}

// -------------------------------------------------------------------- revoca

// La revoca è ambita sull'utente dalla clausola WHERE, non da un controllo
// applicativo: l'identificativo di una chiave altrui non basta a spegnerla.
func TestLaRevocaEAmbitaSullUtente(t *testing.T) {
	f := newFixture(t)
	created := f.crea(f.chiave("della vittima", strings.Repeat("f", 64), apikeys.ScopeJobsRead))
	altro := createUser(t, f.pool, "altro@example.com")

	err := f.store.RevokeKey(t.Context(), altro, created.ID, f.now)
	if !errors.Is(err, apikeys.ErrNotFound) {
		t.Fatalf("errore = %v, atteso ErrNotFound", err)
	}

	letta, err := f.store.KeyByHash(t.Context(), created.Hash)
	if err != nil {
		t.Fatalf("KeyByHash: %v", err)
	}
	if letta.Revoked() {
		t.Fatal("la chiave è stata revocata da un altro utente")
	}
}

// Una seconda revoca non trova nulla: sovrascrivere la data sposterebbe in avanti
// il momento della revoca, che è l'unica informazione che quella riga porta.
func TestLaRevocaNonSiSovrascrive(t *testing.T) {
	f := newFixture(t)
	created := f.crea(f.chiave("chiave", strings.Repeat("1", 64), apikeys.ScopeJobsRead))

	if err := f.store.RevokeKey(t.Context(), f.user, created.ID, f.now); err != nil {
		t.Fatalf("prima revoca: %v", err)
	}
	dopo := f.now.Add(time.Hour)
	if err := f.store.RevokeKey(t.Context(), f.user, created.ID, dopo); !errors.Is(err, apikeys.ErrNotFound) {
		t.Fatalf("seconda revoca = %v, atteso ErrNotFound", err)
	}

	letta, err := f.store.KeyByHash(t.Context(), created.Hash)
	if err != nil {
		t.Fatalf("KeyByHash: %v", err)
	}
	if letta.RevokedAt == nil || !letta.RevokedAt.Equal(f.now) {
		t.Errorf("revoked_at = %v, atteso %v: la data è stata spostata", letta.RevokedAt, f.now)
	}
}

// -------------------------------------------------------------------- elenco

// L'elenco è ambito sull'utente, ordinato dal più recente, e per default esclude
// le revocate — che restano recuperabili come traccia storica.
func TestLElencoEscludeLeRevocateSeNonRichieste(t *testing.T) {
	f := newFixture(t)

	viva := f.crea(f.chiave("viva", strings.Repeat("2", 64), apikeys.ScopeJobsRead))
	morta := f.chiave("morta", strings.Repeat("3", 64), apikeys.ScopeJobsRead)
	morta.CreatedAt = f.now.Add(-time.Hour)
	mortaCreata := f.crea(morta)
	if err := f.store.RevokeKey(t.Context(), f.user, mortaCreata.ID, f.now); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	// La chiave di un altro utente non deve comparire in nessuno dei due elenchi.
	altro := createUser(t, f.pool, "altro@example.com")
	estranea := f.chiave("estranea", strings.Repeat("4", 64), apikeys.ScopeJobsRead)
	estranea.UserID = altro
	f.crea(estranea)

	vive, err := f.store.ListKeys(t.Context(), f.user, false)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(vive) != 1 || vive[0].ID != viva.ID {
		t.Fatalf("chiavi vive = %d (%v), attesa la sola %q", len(vive), vive, viva.ID)
	}

	tutte, err := f.store.ListKeys(t.Context(), f.user, true)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(tutte) != 2 {
		t.Fatalf("chiavi comprese le revocate = %d, attese 2", len(tutte))
	}
	// Ordine: dalla più recente.
	if tutte[0].ID != viva.ID {
		t.Errorf("prima in elenco = %q, attesa la più recente %q", tutte[0].ID, viva.ID)
	}
}

// ------------------------------------------------------------------ conteggio

// Il conteggio delle chiavi vive ignora revocate e scadute: è ciò che rende il
// tetto un limite sulle chiavi utilizzabili e non su quelle mai create.
func TestIlConteggioIgnoraRevocateEScadute(t *testing.T) {
	f := newFixture(t)

	f.crea(f.chiave("viva", strings.Repeat("5", 64), apikeys.ScopeJobsRead))

	revocata := f.crea(f.chiave("revocata", strings.Repeat("6", 64), apikeys.ScopeJobsRead))
	if err := f.store.RevokeKey(t.Context(), f.user, revocata.ID, f.now); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	scaduta := f.chiave("scaduta", strings.Repeat("7", 64), apikeys.ScopeJobsRead)
	passato := f.now.Add(-time.Minute)
	scaduta.ExpiresAt = &passato
	f.crea(scaduta)

	// E una che scade fra un'ora: conta.
	futura := f.chiave("futura", strings.Repeat("8", 64), apikeys.ScopeJobsRead)
	domani := f.now.Add(time.Hour)
	futura.ExpiresAt = &domani
	f.crea(futura)

	count, err := f.store.CountActiveKeys(t.Context(), f.user, f.now)
	if err != nil {
		t.Fatalf("CountActiveKeys: %v", err)
	}
	if count != 2 {
		t.Errorf("chiavi attive = %d, attese 2 (viva, futura)", count)
	}
}

// ---------------------------------------------------------------- last_used_at

// L'ultimo utilizzo si scrive, e non si scrive su una chiave revocata: una
// richiesta autenticata appena prima di una revoca concorrente non deve
// riscrivere la riga di una chiave già spenta.
func TestLUltimoUtilizzoNonSiScriveSuUnaChiaveRevocata(t *testing.T) {
	f := newFixture(t)
	created := f.crea(f.chiave("chiave", strings.Repeat("9", 64), apikeys.ScopeJobsRead))

	uso := f.now.Add(time.Minute)
	if err := f.store.TouchKey(t.Context(), created.ID, uso); err != nil {
		t.Fatalf("TouchKey: %v", err)
	}
	letta, err := f.store.KeyByHash(t.Context(), created.Hash)
	if err != nil {
		t.Fatalf("KeyByHash: %v", err)
	}
	if letta.LastUsedAt == nil || !letta.LastUsedAt.Equal(uso) {
		t.Fatalf("last_used_at = %v, atteso %v", letta.LastUsedAt, uso)
	}

	if err := f.store.RevokeKey(t.Context(), f.user, created.ID, f.now.Add(2*time.Minute)); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	dopo := f.now.Add(3 * time.Minute)
	if err := f.store.TouchKey(t.Context(), created.ID, dopo); err != nil {
		t.Fatalf("TouchKey su una chiave revocata deve essere un non-fatto, non un errore: %v", err)
	}
	letta, err = f.store.KeyByHash(t.Context(), created.Hash)
	if err != nil {
		t.Fatalf("KeyByHash: %v", err)
	}
	if !letta.LastUsedAt.Equal(uso) {
		t.Errorf("last_used_at = %v, atteso invariato a %v", letta.LastUsedAt, uso)
	}
}

// ------------------------------------------------------------------- vincoli

// I vincoli della 0002 sono quelli che valgono anche se il codice applicativo
// sbaglia: uno scope duplicato, un nome vuoto e un prefisso troppo corto devono
// essere rifiutati dal database.
func TestLoSchemaRifiutaLeRigheMalformate(t *testing.T) {
	f := newFixture(t)

	tests := map[string]func(*apikeys.Key){
		"nome vuoto":         func(k *apikeys.Key) { k.Name = "" },
		"prefisso corto":     func(k *apikeys.Key) { k.Prefix = "pq" },
		"impronta corta":     func(k *apikeys.Key) { k.Hash = "corta" },
		"scope duplicati":    func(k *apikeys.Key) { k.Scopes = []apikeys.Scope{"jobs:read", "jobs:read"} },
		"utente inesistente": func(k *apikeys.Key) { k.UserID = "00000000-0000-0000-0000-000000000000" },
	}
	for name, rompi := range tests {
		t.Run(name, func(t *testing.T) {
			key := f.chiave("chiave", strings.Repeat("0", 64), apikeys.ScopeJobsRead)
			rompi(&key)
			if _, err := f.store.CreateKey(t.Context(), key); err == nil {
				t.Fatal("il database ha accettato una riga malformata")
			}
		})
	}
}

// Lo store non si costruisce senza il pool: scoprirlo alla prima query sarebbe un
// panic invece di un errore d'avvio.
func TestNewEsigeIlPool(t *testing.T) {
	if _, err := apikeypg.New(nil); err == nil {
		t.Fatal("atteso un errore con il pool nil")
	}
}
