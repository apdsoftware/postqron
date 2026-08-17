package authpg_test

import (
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/authpg"
)

// Le proprietà provate qui sono quelle che dipendono da PostgreSQL e che un
// archivio in memoria non potrebbe garantire: l'unicità dell'indirizzo come
// arbitro di una corsa, l'atomicità del consumo di un token, l'ambito della
// revoca scritto nella clausola WHERE, i vincoli dello schema. La logica sta in
// internal/auth e si prova là, senza database.

const (
	testEmail  = "mario.rossi@example.com"
	testHash   = "$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGE"
	tokenHashA = "1111111111111111111111111111111111111111111111111111111111111111"
	tokenHashB = "2222222222222222222222222222222222222222222222222222222222222222"
	tokenHashC = "3333333333333333333333333333333333333333333333333333333333333333"
)

var (
	testIP  = netip.MustParseAddr("203.0.113.7")
	testNow = time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
)

func newStore(t *testing.T) (*authpg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := newTestDatabase(t)
	store, err := authpg.New(pool)
	if err != nil {
		t.Fatalf("authpg.New: %v", err)
	}
	return store, pool
}

func createUser(t *testing.T, store *authpg.Store, email string) auth.User {
	t.Helper()
	user, err := store.CreateUser(t.Context(), email, testHash, "Mario Rossi")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

// ---------------------------------------------------------------------- users

func TestCreateUserELetturaPerIndirizzo(t *testing.T) {
	store, _ := newStore(t)

	user := createUser(t, store, testEmail)
	if user.ID == "" {
		t.Fatal("identificativo vuoto")
	}
	if user.Email != testEmail {
		t.Errorf("email = %q", user.Email)
	}
	if user.PasswordHash != testHash {
		t.Errorf("hash = %q", user.PasswordHash)
	}
	if user.FullName != "Mario Rossi" {
		t.Errorf("full_name = %q", user.FullName)
	}
	// I default dello schema (0002) arrivano nel modello.
	if user.Role != "user" {
		t.Errorf("role = %q, atteso user", user.Role)
	}
	if user.Timezone != "UTC" {
		t.Errorf("timezone = %q, atteso UTC", user.Timezone)
	}
	if user.EmailVerified() || user.Suspended() || user.LastLoginAt != nil {
		t.Error("un account appena creato non è confermato, né sospeso, né ha accessi")
	}
	if user.CreatedAt.IsZero() {
		t.Error("created_at non valorizzato")
	}

	// La ricerca è insensibile alle maiuscole, come l'indice unico su
	// lower(email) della 0002.
	for _, variant := range []string{testEmail, "MARIO.ROSSI@EXAMPLE.COM", "Mario.Rossi@Example.com"} {
		found, err := store.UserByEmail(t.Context(), variant)
		if err != nil {
			t.Fatalf("UserByEmail(%q): %v", variant, err)
		}
		if found.ID != user.ID {
			t.Errorf("UserByEmail(%q) ha restituito %q", variant, found.ID)
		}
	}

	found, err := store.UserByID(t.Context(), user.ID)
	if err != nil || found.ID != user.ID {
		t.Errorf("UserByID: %q, %v", found.ID, err)
	}
}

func TestUserByEmailNonTrovato(t *testing.T) {
	store, _ := newStore(t)

	if _, err := store.UserByEmail(t.Context(), "nessuno@example.com"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("errore = %v, atteso ErrNotFound", err)
	}
	// Un identificativo che non è un uuid non deve produrre un errore diverso da
	// «non trovato» per chi legge: sarebbe un canale in più.
	if _, err := store.UserByID(t.Context(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("errore = %v, atteso ErrNotFound", err)
	}
}

// L'unicità dell'indirizzo la garantisce l'indice, non una SELECT di controllo.
func TestCreateUserRifiutaUnIndirizzoGiaPreso(t *testing.T) {
	store, _ := newStore(t)
	createUser(t, store, testEmail)

	for _, variant := range []string{testEmail, "MARIO.ROSSI@EXAMPLE.COM"} {
		_, err := store.CreateUser(t.Context(), variant, testHash, "")
		if !errors.Is(err, auth.ErrEmailTaken) {
			t.Errorf("CreateUser(%q): errore = %v, atteso ErrEmailTaken", variant, err)
		}
	}
}

// Due registrazioni simultanee sullo stesso indirizzo: l'arbitro è l'indice
// unico, e il risultato deve essere un vincitore e un ErrEmailTaken. Con una
// SELECT di controllo prima dell'INSERT passerebbero entrambe.
func TestCreateUserConcorrenteHaUnSoloVincitore(t *testing.T) {
	store, pool := newStore(t)

	const attempts = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		created int
		taken   int
		others  []error
	)
	start := make(chan struct{})
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.CreateUser(t.Context(), testEmail, testHash, "")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created++
			case errors.Is(err, auth.ErrEmailTaken):
				taken++
			default:
				others = append(others, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(others) > 0 {
		t.Fatalf("errori inattesi: %v", others)
	}
	if created != 1 {
		t.Errorf("account creati = %d, atteso 1", created)
	}
	if taken != attempts-1 {
		t.Errorf("conflitti = %d, attesi %d", taken, attempts-1)
	}

	var count int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM users WHERE lower(email) = lower($1)`, testEmail).Scan(&count); err != nil {
		t.Fatalf("conteggio: %v", err)
	}
	if count != 1 {
		t.Errorf("righe in tabella = %d, attesa 1", count)
	}
}

// Un indirizzo liberato da una cancellazione logica torna disponibile, e l'account
// cancellato non è più raggiungibile: è il comportamento dell'indice parziale
// della 0002, e l'autenticazione ci si appoggia.
func TestUnAccountCancellatoNonEPiuRaggiungibile(t *testing.T) {
	store, pool := newStore(t)
	user := createUser(t, store, testEmail)

	if _, err := pool.Exec(t.Context(),
		`UPDATE users SET deleted_at = now() WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("cancellazione logica: %v", err)
	}

	if _, err := store.UserByEmail(t.Context(), testEmail); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("UserByEmail su account cancellato: errore = %v, atteso ErrNotFound", err)
	}
	if _, err := store.UserByID(t.Context(), user.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("UserByID su account cancellato: errore = %v, atteso ErrNotFound", err)
	}
	// E l'indirizzo si può riusare.
	if _, err := store.CreateUser(t.Context(), testEmail, testHash, ""); err != nil {
		t.Errorf("l'indirizzo non è tornato disponibile: %v", err)
	}
	// Una scrittura su un account cancellato non passa.
	if err := store.UpdatePasswordHash(t.Context(), user.ID, testHash); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("UpdatePasswordHash su account cancellato: errore = %v, atteso ErrNotFound", err)
	}
}

func TestAggiornamentiSullUtente(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)

	const nuovoHash = testHash + "x"
	if err := store.UpdatePasswordHash(t.Context(), user.ID, nuovoHash); err != nil {
		t.Fatalf("UpdatePasswordHash: %v", err)
	}
	if err := store.TouchLastLogin(t.Context(), user.ID, testNow); err != nil {
		t.Fatalf("TouchLastLogin: %v", err)
	}
	if err := store.MarkEmailVerified(t.Context(), user.ID, testNow); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}

	updated, err := store.UserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("UserByID: %v", err)
	}
	if updated.PasswordHash != nuovoHash {
		t.Errorf("hash = %q", updated.PasswordHash)
	}
	if updated.LastLoginAt == nil || !updated.LastLoginAt.Equal(testNow) {
		t.Errorf("last_login_at = %v", updated.LastLoginAt)
	}
	if updated.EmailVerifiedAt == nil || !updated.EmailVerifiedAt.Equal(testNow) {
		t.Errorf("email_verified_at = %v", updated.EmailVerifiedAt)
	}

	// Una seconda conferma non sposta la data della prima: la condizione
	// `email_verified_at IS NULL` rende l'operazione idempotente.
	if err := store.MarkEmailVerified(t.Context(), user.ID, testNow.Add(time.Hour)); err != nil {
		t.Fatalf("MarkEmailVerified: %v", err)
	}
	again, _ := store.UserByID(t.Context(), user.ID)
	if !again.EmailVerifiedAt.Equal(testNow) {
		t.Errorf("la data di conferma è stata spostata: %v", again.EmailVerifiedAt)
	}
}

// ------------------------------------------------------------------- sessioni

func newSession(t *testing.T, store *authpg.Store, userID, tokenHash string, at time.Time) auth.Session {
	t.Helper()
	session, err := store.CreateSession(t.Context(), auth.Session{
		UserID:     userID,
		TokenHash:  tokenHash,
		CreatedAt:  at,
		LastUsedAt: at,
		ExpiresAt:  at.Add(30 * 24 * time.Hour),
		IPAddress:  &testIP,
		UserAgent:  "Postqron-Test/1.0",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return session
}

func TestCicloDiVitaDiUnaSessione(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)

	session := newSession(t, store, user.ID, tokenHashA, testNow)
	if session.ID == "" {
		t.Fatal("identificativo vuoto")
	}
	if session.IPAddress == nil || *session.IPAddress != testIP {
		t.Errorf("ip_address = %v, atteso %v", session.IPAddress, testIP)
	}
	if session.UserAgent != "Postqron-Test/1.0" {
		t.Errorf("user_agent = %q", session.UserAgent)
	}
	if session.RevokedAt != nil {
		t.Error("una sessione nuova non è revocata")
	}

	found, err := store.SessionByTokenHash(t.Context(), tokenHashA)
	if err != nil {
		t.Fatalf("SessionByTokenHash: %v", err)
	}
	if found.ID != session.ID {
		t.Errorf("sessione = %q, attesa %q", found.ID, session.ID)
	}

	later := testNow.Add(10 * time.Minute)
	if err := store.TouchSession(t.Context(), session.ID, later); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	found, _ = store.SessionByTokenHash(t.Context(), tokenHashA)
	if !found.LastUsedAt.Equal(later) {
		t.Errorf("last_used_at = %s, atteso %s", found.LastUsedAt, later)
	}

	if err := store.RevokeSession(t.Context(), user.ID, session.ID, later); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	found, _ = store.SessionByTokenHash(t.Context(), tokenHashA)
	if found.RevokedAt == nil {
		t.Error("revoked_at non valorizzato")
	}
	// La riga resta come traccia: l'elenco dei dispositivi deve poter mostrare
	// «chiusa ieri».
	if found.ID != session.ID {
		t.Error("la sessione revocata è stata cancellata invece di essere marcata")
	}
	// Una seconda revoca non trova più nulla da revocare.
	if err := store.RevokeSession(t.Context(), user.ID, session.ID, later); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("seconda revoca: errore = %v, atteso ErrNotFound", err)
	}

	if _, err := store.SessionByTokenHash(t.Context(), tokenHashB); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("impronta inesistente: errore = %v, atteso ErrNotFound", err)
	}
}

// L'ambito sull'utente è nella clausola WHERE, non in un controllo applicativo:
// l'identificativo di una sessione altrui non basta a chiuderla.
func TestRevokeSessionEVincolataAllUtente(t *testing.T) {
	store, _ := newStore(t)
	vittima := createUser(t, store, testEmail)
	attaccante := createUser(t, store, "attaccante@example.com")

	session := newSession(t, store, vittima.ID, tokenHashA, testNow)

	err := store.RevokeSession(t.Context(), attaccante.ID, session.ID, testNow)
	if !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("errore = %v, atteso ErrNotFound", err)
	}
	found, _ := store.SessionByTokenHash(t.Context(), tokenHashA)
	if found.RevokedAt != nil {
		t.Error("la sessione della vittima è stata revocata da un altro account")
	}
}

func TestListSessionsEscludeRevocateEScadute(t *testing.T) {
	store, pool := newStore(t)
	user := createUser(t, store, testEmail)
	altro := createUser(t, store, "altro@example.com")

	viva := newSession(t, store, user.ID, tokenHashA, testNow)
	revocata := newSession(t, store, user.ID, tokenHashB, testNow.Add(time.Minute))
	scaduta := newSession(t, store, user.ID, tokenHashC, testNow.Add(2*time.Minute))
	// Una sessione di un altro utente non deve comparire nell'elenco.
	newSession(t, store, altro.ID, strings.Repeat("4", 64), testNow)

	if err := store.RevokeSession(t.Context(), user.ID, revocata.ID, testNow); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE sessions SET expires_at = created_at + interval '1 second' WHERE id = $1`,
		scaduta.ID); err != nil {
		t.Fatalf("scadenza forzata: %v", err)
	}

	sessions, err := store.ListSessions(t.Context(), user.ID, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessioni = %d, attesa 1: %+v", len(sessions), sessions)
	}
	if sessions[0].ID != viva.ID {
		t.Errorf("sessione = %q, attesa %q", sessions[0].ID, viva.ID)
	}
}

func TestListSessionsOrdinataPerUltimoUtilizzo(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)

	prima := newSession(t, store, user.ID, tokenHashA, testNow)
	seconda := newSession(t, store, user.ID, tokenHashB, testNow.Add(time.Minute))

	// La prima viene usata dopo la seconda: deve passare in testa.
	if err := store.TouchSession(t.Context(), prima.ID, testNow.Add(time.Hour)); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	sessions, err := store.ListSessions(t.Context(), user.ID, testNow.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessioni = %d, attese 2", len(sessions))
	}
	if sessions[0].ID != prima.ID || sessions[1].ID != seconda.ID {
		t.Errorf("ordine inatteso: %q, %q", sessions[0].ID, sessions[1].ID)
	}
}

func TestRevokeUserSessions(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)
	altro := createUser(t, store, "altro@example.com")

	tenuta := newSession(t, store, user.ID, tokenHashA, testNow)
	newSession(t, store, user.ID, tokenHashB, testNow)
	newSession(t, store, user.ID, tokenHashC, testNow)
	newSession(t, store, altro.ID, strings.Repeat("5", 64), testNow)

	n, err := store.RevokeUserSessions(t.Context(), user.ID, tenuta.ID, testNow)
	if err != nil {
		t.Fatalf("RevokeUserSessions: %v", err)
	}
	if n != 2 {
		t.Errorf("revocate = %d, attese 2", n)
	}
	sessions, _ := store.ListSessions(t.Context(), user.ID, testNow.Add(time.Hour))
	if len(sessions) != 1 || sessions[0].ID != tenuta.ID {
		t.Errorf("la sessione da risparmiare non è quella attesa: %+v", sessions)
	}
	// L'altro utente non è toccato.
	if others, _ := store.ListSessions(t.Context(), altro.ID, testNow.Add(time.Hour)); len(others) != 1 {
		t.Errorf("sessioni dell'altro utente = %d, attesa 1", len(others))
	}

	// Senza eccezioni, cade anche l'ultima.
	if n, err = store.RevokeUserSessions(t.Context(), user.ID, "", testNow); err != nil {
		t.Fatalf("RevokeUserSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("revocate = %d, attesa 1", n)
	}
	if sessions, _ := store.ListSessions(t.Context(), user.ID, testNow.Add(time.Hour)); len(sessions) != 0 {
		t.Errorf("sessioni residue = %d, attese 0", len(sessions))
	}
}

func TestRevokeSessionByTokenHash(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)
	newSession(t, store, user.ID, tokenHashA, testNow)

	if err := store.RevokeSessionByTokenHash(t.Context(), tokenHashA, testNow); err != nil {
		t.Fatalf("RevokeSessionByTokenHash: %v", err)
	}
	found, _ := store.SessionByTokenHash(t.Context(), tokenHashA)
	if found.RevokedAt == nil {
		t.Error("la sessione non è stata revocata")
	}
	// Un token inesistente, o già revocato, non trova nulla.
	for name, hash := range map[string]string{"già revocato": tokenHashA, "inesistente": tokenHashB} {
		if err := store.RevokeSessionByTokenHash(t.Context(), hash, testNow); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("%s: errore = %v, atteso ErrNotFound", name, err)
		}
	}
}

// La cancellazione dell'account porta via le sue sessioni (ON DELETE CASCADE
// della 0009): non devono restare righe orfane che riferiscono un utente che non
// c'è più.
func TestLeSessioniCadonoConLAccount(t *testing.T) {
	store, pool := newStore(t)
	user := createUser(t, store, testEmail)
	newSession(t, store, user.ID, tokenHashA, testNow)

	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("cancellazione dell'account: %v", err)
	}
	if _, err := store.SessionByTokenHash(t.Context(), tokenHashA); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("errore = %v, atteso ErrNotFound", err)
	}
}

// I vincoli della 0009 non sono decorativi: un'impronta che non ha la forma
// attesa, o una scadenza prima della creazione, devono essere rifiutate dal
// database. È la rete che regge se il codice sbaglia.
func TestLoSchemaRifiutaLeSessioniMalformate(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)

	tests := map[string]auth.Session{
		"impronta corta": {
			UserID: user.ID, TokenHash: "abc",
			CreatedAt: testNow, LastUsedAt: testNow, ExpiresAt: testNow.Add(time.Hour),
		},
		"impronta non esadecimale": {
			UserID: user.ID, TokenHash: strings.Repeat("z", 64),
			CreatedAt: testNow, LastUsedAt: testNow, ExpiresAt: testNow.Add(time.Hour),
		},
		"impronta in maiuscolo": {
			UserID: user.ID, TokenHash: strings.Repeat("A", 64),
			CreatedAt: testNow, LastUsedAt: testNow, ExpiresAt: testNow.Add(time.Hour),
		},
		"scadenza prima della creazione": {
			UserID: user.ID, TokenHash: tokenHashA,
			CreatedAt: testNow, LastUsedAt: testNow, ExpiresAt: testNow.Add(-time.Hour),
		},
	}
	for name, session := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := store.CreateSession(t.Context(), session); err == nil {
				t.Fatal("il database ha accettato una sessione malformata")
			}
		})
	}

	// E due sessioni non possono condividere l'impronta.
	newSession(t, store, user.ID, tokenHashA, testNow)
	if _, err := store.CreateSession(t.Context(), auth.Session{
		UserID: user.ID, TokenHash: tokenHashA,
		CreatedAt: testNow, LastUsedAt: testNow, ExpiresAt: testNow.Add(time.Hour),
	}); err == nil {
		t.Error("due sessioni hanno la stessa impronta")
	}
}

// ---------------------------------------------------------------- user_tokens

func newToken(t *testing.T, store *authpg.Store, userID string, purpose auth.TokenPurpose, hash string, at time.Time) auth.UserToken {
	t.Helper()
	token, err := store.CreateUserToken(t.Context(), auth.UserToken{
		UserID:      userID,
		Purpose:     purpose,
		TokenHash:   hash,
		CreatedAt:   at,
		ExpiresAt:   at.Add(time.Hour),
		RequestedIP: &testIP,
	})
	if err != nil {
		t.Fatalf("CreateUserToken: %v", err)
	}
	return token
}

func TestCicloDiVitaDiUnToken(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)

	token := newToken(t, store, user.ID, auth.PurposePasswordReset, tokenHashA, testNow)
	if token.ID == "" || token.Purpose != auth.PurposePasswordReset {
		t.Fatalf("token inatteso: %+v", token)
	}
	if token.ConsumedAt != nil {
		t.Error("un token nuovo non è consumato")
	}
	if token.RequestedIP == nil || *token.RequestedIP != testIP {
		t.Errorf("requested_ip = %v", token.RequestedIP)
	}

	consumed, err := store.ConsumeUserToken(t.Context(), tokenHashA, auth.PurposePasswordReset, testNow)
	if err != nil {
		t.Fatalf("ConsumeUserToken: %v", err)
	}
	if consumed.UserID != user.ID {
		t.Errorf("user_id = %q", consumed.UserID)
	}
	if consumed.ConsumedAt == nil || !consumed.ConsumedAt.Equal(testNow) {
		t.Errorf("consumed_at = %v", consumed.ConsumedAt)
	}

	// Un token si consuma una volta sola.
	if _, err := store.ConsumeUserToken(t.Context(), tokenHashA, auth.PurposePasswordReset, testNow); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("secondo consumo: errore = %v, atteso ErrNotFound", err)
	}
}

func TestConsumeUserTokenRifiutaITokenNonUtilizzabili(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)

	newToken(t, store, user.ID, auth.PurposePasswordReset, tokenHashA, testNow)

	tests := map[string]struct {
		hash    string
		purpose auth.TokenPurpose
		now     time.Time
	}{
		"impronta inesistente": {tokenHashB, auth.PurposePasswordReset, testNow},
		// Lo scopo è parte della condizione: un token di recupero password non
		// deve poter confermare un indirizzo.
		"scopo diverso": {tokenHashA, auth.PurposeEmailVerification, testNow},
		"scaduto":       {tokenHashA, auth.PurposePasswordReset, testNow.Add(2 * time.Hour)},
		// Il confronto è `expires_at > now`: al momento esatto della scadenza il
		// token è già inutilizzabile.
		"esattamente scaduto": {tokenHashA, auth.PurposePasswordReset, testNow.Add(time.Hour)},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := store.ConsumeUserToken(t.Context(), tc.hash, tc.purpose, tc.now); !errors.Is(err, auth.ErrNotFound) {
				t.Fatalf("errore = %v, atteso ErrNotFound", err)
			}
		})
	}
}

// Due richieste concorrenti con lo stesso token devono produrre un vincitore e un
// rifiuto. È la proprietà che una SELECT seguita da un UPDATE non ha, e che rende
// necessario l'UPDATE ... RETURNING condizionato.
func TestConsumeUserTokenHaUnSoloVincitore(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)
	newToken(t, store, user.ID, auth.PurposePasswordReset, tokenHashA, testNow)

	const attempts = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		consumed int
		refused  int
		others   []error
	)
	start := make(chan struct{})
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.ConsumeUserToken(t.Context(), tokenHashA, auth.PurposePasswordReset, testNow)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				consumed++
			case errors.Is(err, auth.ErrNotFound):
				refused++
			default:
				others = append(others, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(others) > 0 {
		t.Fatalf("errori inattesi: %v", others)
	}
	if consumed != 1 {
		t.Errorf("consumi riusciti = %d, atteso 1: un link di recupero è servito più di una volta", consumed)
	}
	if refused != attempts-1 {
		t.Errorf("rifiuti = %d, attesi %d", refused, attempts-1)
	}
}

func TestRevokeUserTokens(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)
	altro := createUser(t, store, "altro@example.com")

	newToken(t, store, user.ID, auth.PurposePasswordReset, tokenHashA, testNow)
	newToken(t, store, user.ID, auth.PurposePasswordReset, tokenHashB, testNow)
	// Scopo diverso: non deve essere toccato.
	newToken(t, store, user.ID, auth.PurposeEmailVerification, tokenHashC, testNow)
	// Utente diverso: nemmeno.
	newToken(t, store, altro.ID, auth.PurposePasswordReset, strings.Repeat("6", 64), testNow)

	n, err := store.RevokeUserTokens(t.Context(), user.ID, auth.PurposePasswordReset, testNow)
	if err != nil {
		t.Fatalf("RevokeUserTokens: %v", err)
	}
	if n != 2 {
		t.Errorf("invalidati = %d, attesi 2", n)
	}

	for _, hash := range []string{tokenHashA, tokenHashB} {
		if _, err := store.ConsumeUserToken(t.Context(), hash, auth.PurposePasswordReset, testNow); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("%s è ancora utilizzabile: %v", hash, err)
		}
	}
	if _, err := store.ConsumeUserToken(t.Context(), tokenHashC, auth.PurposeEmailVerification, testNow); err != nil {
		t.Errorf("il token di scopo diverso è stato invalidato: %v", err)
	}
	if _, err := store.ConsumeUserToken(t.Context(), strings.Repeat("6", 64), auth.PurposePasswordReset, testNow); err != nil {
		t.Errorf("il token dell'altro utente è stato invalidato: %v", err)
	}

	// Una seconda invalidazione non trova più nulla.
	if n, err = store.RevokeUserTokens(t.Context(), user.ID, auth.PurposePasswordReset, testNow); err != nil || n != 0 {
		t.Errorf("seconda invalidazione: n = %d, err = %v", n, err)
	}
}

func TestLoSchemaRifiutaITokenMalformati(t *testing.T) {
	store, _ := newStore(t)
	user := createUser(t, store, testEmail)

	tests := map[string]auth.UserToken{
		"impronta corta": {
			UserID: user.ID, Purpose: auth.PurposePasswordReset, TokenHash: "abc",
			CreatedAt: testNow, ExpiresAt: testNow.Add(time.Hour),
		},
		"scadenza prima della creazione": {
			UserID: user.ID, Purpose: auth.PurposePasswordReset, TokenHash: tokenHashA,
			CreatedAt: testNow, ExpiresAt: testNow.Add(-time.Hour),
		},
		"scopo fuori dal dominio": {
			UserID: user.ID, Purpose: "qualcosa_altro", TokenHash: tokenHashA,
			CreatedAt: testNow, ExpiresAt: testNow.Add(time.Hour),
		},
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := store.CreateUserToken(t.Context(), token); err == nil {
				t.Fatal("il database ha accettato un token malformato")
			}
		})
	}
}

func TestITokenCadonoConLAccount(t *testing.T) {
	store, pool := newStore(t)
	user := createUser(t, store, testEmail)
	newToken(t, store, user.ID, auth.PurposePasswordReset, tokenHashA, testNow)

	if _, err := pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1`, user.ID); err != nil {
		t.Fatalf("cancellazione dell'account: %v", err)
	}
	if _, err := store.ConsumeUserToken(t.Context(), tokenHashA, auth.PurposePasswordReset, testNow); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("errore = %v, atteso ErrNotFound", err)
	}
}

// ------------------------------------------------------------- manutenzione

// Le funzioni di pulizia della 0009 sono l'unico modo previsto per liberare le
// due tabelle: la condizione vive lì e non sparsa nel codice applicativo.
func TestFunzioniDiPulizia(t *testing.T) {
	store, pool := newStore(t)
	user := createUser(t, store, testEmail)

	// Una sessione viva, una scaduta da tempo, una revocata da tempo.
	newSession(t, store, user.ID, tokenHashA, testNow)
	vecchia := newSession(t, store, user.ID, tokenHashB, testNow)
	revocata := newSession(t, store, user.ID, tokenHashC, testNow)
	if _, err := pool.Exec(t.Context(),
		`UPDATE sessions SET created_at = now() - interval '200 days',
		                     expires_at = now() - interval '170 days' WHERE id = $1`, vecchia.ID); err != nil {
		t.Fatalf("invecchiamento: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE sessions SET revoked_at = now() - interval '200 days' WHERE id = $1`, revocata.ID); err != nil {
		t.Fatalf("revoca antica: %v", err)
	}

	var removed int64
	if err := pool.QueryRow(t.Context(),
		`SELECT sessions_purge_expired(interval '30 days')`).Scan(&removed); err != nil {
		t.Fatalf("sessions_purge_expired: %v", err)
	}
	if removed != 2 {
		t.Errorf("sessioni rimosse = %d, attese 2", removed)
	}
	if _, err := store.SessionByTokenHash(t.Context(), tokenHashA); err != nil {
		t.Errorf("la sessione viva è stata rimossa: %v", err)
	}

	// Un `grace` negativo è un errore d'uso, non qualcosa da interpretare.
	if err := pool.QueryRow(t.Context(),
		`SELECT sessions_purge_expired(interval '-1 day')`).Scan(&removed); err == nil {
		t.Error("un grace negativo è stato accettato")
	}

	// Stessa cosa per i token.
	newToken(t, store, user.ID, auth.PurposePasswordReset, tokenHashA, testNow)
	if _, err := pool.Exec(t.Context(),
		`UPDATE user_tokens SET created_at = now() - interval '90 days',
		                        expires_at = now() - interval '89 days'`); err != nil {
		t.Fatalf("invecchiamento dei token: %v", err)
	}
	if err := pool.QueryRow(t.Context(),
		`SELECT user_tokens_purge_expired(interval '7 days')`).Scan(&removed); err != nil {
		t.Fatalf("user_tokens_purge_expired: %v", err)
	}
	if removed != 1 {
		t.Errorf("token rimossi = %d, atteso 1", removed)
	}
}

// ---------------------------------------------------------------- migrazione

// La 0009 deve essere reversibile: il `down` riporta lo schema allo stato
// precedente, e il `up` successivo lo ricostruisce. Senza questa proprietà una
// migrazione sbagliata non si può annullare in produzione (AGENTS.md §5).
func TestLaMigrazione0009EReversibile(t *testing.T) {
	pool := newTestDatabase(t)
	migrator := applyMigrations(t, pool)
	ctx := t.Context()

	esiste := func(query string, args ...any) bool {
		t.Helper()
		var present bool
		if err := pool.QueryRow(ctx, query, args...).Scan(&present); err != nil {
			t.Fatalf("verifica di esistenza: %v", err)
		}
		return present
	}
	const tabella = `SELECT EXISTS (SELECT 1 FROM pg_class WHERE relname = $1 AND relkind = 'r')`
	const tipo = `SELECT EXISTS (SELECT 1 FROM pg_type WHERE typname = $1)`
	const funzione = `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE proname = $1)`

	for _, name := range []string{"sessions", "user_tokens"} {
		if !esiste(tabella, name) {
			t.Fatalf("la tabella %q non esiste dopo il `up`", name)
		}
	}
	if !esiste(tipo, "user_token_purpose") {
		t.Fatal("il tipo user_token_purpose non esiste dopo il `up`")
	}
	for _, name := range []string{"sessions_purge_expired", "user_tokens_purge_expired"} {
		if !esiste(funzione, name) {
			t.Fatalf("la funzione %q non esiste dopo il `up`", name)
		}
	}

	// Annulla tutto ciò che sta sopra la 0008: la 0009 e qualunque migrazione
	// sia arrivata dopo di lei. Il conteggio non può essere fisso a uno, o il
	// test smette di provare la reversibilità della 0009 — e comincia a fallire
	// — nel momento in cui un'altra issue aggiunge la propria migrazione.
	version, err := migrator.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if _, err := migrator.Down(ctx, version-8); err != nil {
		t.Fatalf("Down: %v", err)
	}
	for _, name := range []string{"sessions", "user_tokens"} {
		if esiste(tabella, name) {
			t.Errorf("la tabella %q sopravvive al `down`", name)
		}
	}
	if esiste(tipo, "user_token_purpose") {
		t.Error("il tipo user_token_purpose sopravvive al `down`")
	}
	for _, name := range []string{"sessions_purge_expired", "user_tokens_purge_expired"} {
		if esiste(funzione, name) {
			t.Errorf("la funzione %q sopravvive al `down`", name)
		}
	}
	// Le tabelle delle migrazioni precedenti sono intatte.
	if !esiste(tabella, "users") {
		t.Error("il `down` della 0009 ha portato via `users`")
	}

	// E si riapplica.
	if _, err := migrator.Up(ctx, 0); err != nil {
		t.Fatalf("Up dopo il Down: %v", err)
	}
	for _, name := range []string{"sessions", "user_tokens"} {
		if !esiste(tabella, name) {
			t.Errorf("la tabella %q non è tornata dopo il secondo `up`", name)
		}
	}
	// Lo schema ricostruito è utilizzabile.
	store, err := authpg.New(pool)
	if err != nil {
		t.Fatalf("authpg.New: %v", err)
	}
	user := createUser(t, store, testEmail)
	newSession(t, store, user.ID, tokenHashA, testNow)
}
