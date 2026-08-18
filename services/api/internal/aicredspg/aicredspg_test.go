package aicredspg_test

import (
	"bytes"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/aicreds"
	"github.com/apdsoftware/postqron/services/api/internal/aicredspg"
	"github.com/apdsoftware/postqron/services/api/internal/migrate"
	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// ---------------------------------------------------------------- impalcatura

type fixture struct {
	t     *testing.T
	pool  *pgxpool.Pool
	store *aicredspg.Store
	user  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	pool := newTestDatabase(t)
	store, err := aicredspg.New(pool)
	if err != nil {
		t.Fatalf("aicredspg.New: %v", err)
	}
	return &fixture{t: t, pool: pool, store: store, user: createUser(t, pool, "alice@example.com")}
}

// sealed compone una riga finta. Il materiale «cifrato» qui è testo
// riconoscibile: questo package non cifra niente, e mescolarci una cifratura
// vera renderebbe i test dipendenti da due cose invece che da una. La cifratura
// vera ha il suo giro in fondo, con TestRoundTripWithTheRealCipher.
func sealed(userID string, provider aicreds.Provider) aicreds.Sealed {
	return aicreds.Sealed{
		Credential: aicreds.Credential{
			UserID:     userID,
			Provider:   provider,
			Label:      "chiave di " + string(provider),
			KeyVersion: 1,
			CreatedAt:  time.Now().UTC(),
		},
		Ciphertext: []byte("materiale-cifrato-di-" + provider),
		Nonce:      []byte("nonce-di-" + provider),
	}
}

func (f *fixture) salva(provider aicreds.Provider) aicreds.Credential {
	f.t.Helper()
	credential, err := f.store.UpsertCredential(f.t.Context(), sealed(f.user, provider))
	if err != nil {
		f.t.Fatalf("UpsertCredential(%s): %v", provider, err)
	}
	return credential
}

// -------------------------------------------------- lo schema che protegge

// R18: la tabella non ha una colonna in cui la chiave in chiaro potrebbe
// finire, e le colonne che ci sono sono **esattamente** quelle che ci
// aspettiamo.
//
// L'elenco è chiuso e non un insieme di nomi vietati, per la ragione che
// internal/secretspg scrive per `workspace_secrets`: una colonna `value`, o
// `key_prefix`, aggiunta da una migrazione futura per comodità sarebbe la fine
// di R18, e non lo direbbe nessuno. `last_four` è nell'elenco dei nomi vietati
// perché la 0007 ce l'aveva: la 0016 l'ha tolta, e questo test è ciò che
// impedisce che torni.
func TestSchemaHasNoColumnForThePlaintext(t *testing.T) {
	f := newFixture(t)

	rows, err := f.pool.Query(t.Context(),
		`SELECT column_name FROM information_schema.columns
		  WHERE table_name = 'ai_credentials' AND table_schema = 'public'
		  ORDER BY column_name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	vietate := []string{
		"value", "plaintext", "plain", "api_key", "secret",
		"last_four", "preview", "hint", "suffix", "prefix",
	}
	for _, column := range columns {
		for _, vietata := range vietate {
			if strings.Contains(column, vietata) {
				t.Errorf("colonna sospetta in ai_credentials: %q", column)
			}
		}
	}

	attese := []string{
		"ciphertext", "created_at", "id", "key_version", "label",
		"last_used_at", "nonce", "provider", "revoked_at", "updated_at", "user_id",
	}
	if !slices.Equal(columns, attese) {
		t.Errorf("colonne =\n  %v\nattese\n  %v", columns, attese)
	}
}

// **Revocare significa che la chiave smette di esistere**, e il vincolo della
// 0016 lega le due cose in modo che non possano separarsi: non si data la revoca
// lasciando il materiale, e non si svuota il materiale lasciando la riga
// utilizzabile.
func TestSchemaTiesRevocationToTheEmptyCiphertext(t *testing.T) {
	f := newFixture(t)
	credential := f.salva(aicreds.Anthropic)

	// Revocare senza svuotare: rifiutato.
	if _, err := f.pool.Exec(t.Context(),
		`UPDATE ai_credentials SET revoked_at = now() WHERE id = $1`, credential.ID); err == nil {
		t.Error("è stata datata una revoca lasciando il materiale cifrato in tabella")
	}

	// Svuotare senza revocare: rifiutato.
	if _, err := f.pool.Exec(t.Context(),
		`UPDATE ai_credentials SET ciphertext = '\x'::bytea, nonce = '\x'::bytea WHERE id = $1`,
		credential.ID); err == nil {
		t.Error("il materiale cifrato è stato svuotato lasciando la riga viva")
	}

	// Le due cose insieme: è ciò che fa RevokeCredential.
	if err := f.store.RevokeCredential(t.Context(), f.user, credential.ID, time.Now()); err != nil {
		t.Fatalf("RevokeCredential: %v", err)
	}

	var ciphertext, nonce []byte
	var revokedAt *time.Time
	if err := f.pool.QueryRow(t.Context(),
		`SELECT ciphertext, nonce, revoked_at FROM ai_credentials WHERE id = $1`,
		credential.ID).Scan(&ciphertext, &nonce, &revokedAt); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) != 0 || len(nonce) != 0 {
		t.Error("la revoca ha lasciato materiale cifrato in tabella")
	}
	if revokedAt == nil {
		t.Error("la revoca non ha datato la riga")
	}
}

// Lo schema rifiuta un provider che non è nell'enumerato della 0001: l'insieme
// è chiuso da entrambi i lati, e il controllo nel codice non è l'unico.
func TestSchemaEnforcesTheProviderEnum(t *testing.T) {
	f := newFixture(t)

	if _, err := f.pool.Exec(t.Context(),
		`INSERT INTO ai_credentials (user_id, provider, ciphertext, nonce)
		 VALUES ($1, 'claude', '\x01', '\x02')`, f.user); err == nil {
		t.Error("un provider fuori dall'enumerato è stato accettato")
	}
}

// ------------------------------------------------------ unicità fra i vivi

// Una chiave viva per provider, e il vincolo è dell'indice — non di una lettura
// precedente del chiamante. Revocata, il provider torna libero: senza,
// la prima revoca brucerebbe per sempre lo slot.
func TestOneLiveKeyPerProvider(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	prima := f.salva(aicreds.Anthropic)

	// Un secondo inserimento è una sostituzione, non una seconda riga.
	seconda := sealed(f.user, aicreds.Anthropic)
	seconda.Ciphertext = []byte("materiale-nuovo")
	seconda.Label = "ruotata"
	aggiornata, err := f.store.UpsertCredential(ctx, seconda)
	if err != nil {
		t.Fatalf("UpsertCredential: %v", err)
	}
	if aggiornata.ID != prima.ID {
		t.Error("la sostituzione ha creato una seconda riga viva")
	}
	if aggiornata.Label != "ruotata" {
		t.Errorf("etichetta = %q, attesa \"ruotata\"", aggiornata.Label)
	}

	// Un inserimento grezzo, che aggira lo store, deve essere rifiutato
	// dall'indice: è la proprietà che il database garantisce anche sotto
	// concorrenza, dove due letture potrebbero vedere entrambe «non c'è».
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO ai_credentials (user_id, provider, ciphertext, nonce)
		 VALUES ($1, 'anthropic', '\x03', '\x04')`, f.user); err == nil {
		t.Error("due chiavi vive per lo stesso provider sono state accettate")
	}

	// Revocata, il provider è di nuovo libero.
	if err := f.store.RevokeCredential(ctx, f.user, prima.ID, time.Now()); err != nil {
		t.Fatalf("RevokeCredential: %v", err)
	}
	nuova := f.salva(aicreds.Anthropic)
	if nuova.ID == prima.ID {
		t.Error("la chiave nuova ha riusato la riga revocata")
	}

	// E le due righe convivono: quella revocata resta come traccia.
	tutte, err := f.store.ListCredentials(ctx, f.user, true)
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	if len(tutte) != 2 {
		t.Errorf("righe = %d, attese 2 (una viva, una revocata)", len(tutte))
	}
}

// Provider diversi convivono, e la revoca dell'uno non tocca l'altro.
func TestProvidersAreIndependent(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	anthropic := f.salva(aicreds.Anthropic)
	f.salva(aicreds.OpenAI)
	f.salva(aicreds.Google)

	if err := f.store.RevokeCredential(ctx, f.user, anthropic.ID, time.Now()); err != nil {
		t.Fatalf("RevokeCredential: %v", err)
	}

	vive, err := f.store.ListCredentials(ctx, f.user, false)
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	if len(vive) != 2 {
		t.Fatalf("chiavi vive = %d, attese 2", len(vive))
	}
	for _, credential := range vive {
		if credential.Provider == aicreds.Anthropic {
			t.Error("la chiave revocata compare fra le vive")
		}
	}

	if _, err := f.store.LiveByProvider(ctx, f.user, aicreds.Anthropic); !errors.Is(err, aicreds.ErrNotFound) {
		t.Errorf("LiveByProvider su una revocata = %v, atteso ErrNotFound", err)
	}
	if _, err := f.store.LiveByProvider(ctx, f.user, aicreds.OpenAI); err != nil {
		t.Errorf("LiveByProvider su una viva: %v", err)
	}
}

// ------------------------------------------------------- il materiale non esce

// L'elenco **non porta il materiale cifrato**: le colonne non sono nella SELECT,
// e non è un'omissione da correggere. Tutto ciò che l'API mostra passa da lì.
func TestListDoesNotCarryTheMaterial(t *testing.T) {
	f := newFixture(t)
	f.salva(aicreds.Anthropic)

	credentials, err := f.store.ListCredentials(t.Context(), f.user, true)
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	if len(credentials) != 1 {
		t.Fatalf("chiavi = %d, attesa 1", len(credentials))
	}

	// Il tipo restituito non ha nemmeno i campi. Il test serializza per verbo,
	// che è la strada da cui il materiale finirebbe in un log o in una risposta.
	logs := &bytes.Buffer{}
	slog.New(slog.NewTextHandler(logs, nil)).Info("elenco", slog.Any("credential", credentials[0]))
	for _, vietato := range []string{"materiale-cifrato", "nonce-di-"} {
		if strings.Contains(logs.String(), vietato) {
			t.Errorf("l'elenco porta con sé %q:\n%s", vietato, logs)
		}
	}
}

// L'ambito sull'utente è nella query e non in un controllo applicativo:
// l'identificativo di una chiave altrui non basta a leggerla né a revocarla.
func TestScopeIsInTheQuery(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	altro := createUser(t, f.pool, "bob@example.com")

	credential := f.salva(aicreds.Anthropic)

	if _, err := f.store.LiveByProvider(ctx, altro, aicreds.Anthropic); !errors.Is(err, aicreds.ErrNotFound) {
		t.Errorf("LiveByProvider di un altro utente = %v, atteso ErrNotFound", err)
	}
	if err := f.store.RevokeCredential(ctx, altro, credential.ID, time.Now()); !errors.Is(err, aicreds.ErrNotFound) {
		t.Errorf("RevokeCredential di un altro utente = %v, atteso ErrNotFound", err)
	}
	if credenziali, err := f.store.ListCredentials(ctx, altro, true); err != nil || len(credenziali) != 0 {
		t.Errorf("ListCredentials di un altro utente = %d righe, %v", len(credenziali), err)
	}

	// E la chiave dell'utente è ancora viva: nessuno dei tentativi l'ha toccata.
	if _, err := f.store.LiveByProvider(ctx, f.user, aicreds.Anthropic); err != nil {
		t.Errorf("la chiave è stata toccata da un altro utente: %v", err)
	}
}

// Revocare due volte non è un'operazione riuscita, e revocare una chiave che
// non esiste dà lo stesso errore: distinguerli direbbe a chiunque se un
// identificativo altrui è vivo.
func TestRevokeIsNotIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	credential := f.salva(aicreds.Anthropic)

	if err := f.store.RevokeCredential(ctx, f.user, credential.ID, time.Now()); err != nil {
		t.Fatalf("prima revoca: %v", err)
	}
	if err := f.store.RevokeCredential(ctx, f.user, credential.ID, time.Now()); !errors.Is(err, aicreds.ErrNotFound) {
		t.Errorf("seconda revoca = %v, atteso ErrNotFound", err)
	}
	inesistente := "00000000-0000-0000-0000-000000000000"
	if err := f.store.RevokeCredential(ctx, f.user, inesistente, time.Now()); !errors.Is(err, aicreds.ErrNotFound) {
		t.Errorf("revoca di una chiave inesistente = %v, atteso ErrNotFound", err)
	}
}

// `last_used_at` si scrive, e `updated_at` lo scrive il trigger.
//
// Il paragone è con l'`updated_at` **precedente**, mai con `created_at`:
// quest'ultimo lo scrive Go e il primo il trigger di PostgreSQL, cioè due
// orologi diversi, e il container insegue l'host con qualche millesimo di
// deriva. Confrontarli sarebbe un fallimento a caso.
func TestTouchAndTheUpdatedAtTrigger(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	credential := f.salva(aicreds.Anthropic)

	quando := time.Now().UTC().Truncate(time.Millisecond)
	if err := f.store.TouchCredential(ctx, credential.ID, quando); err != nil {
		t.Fatalf("TouchCredential: %v", err)
	}

	sealed, err := f.store.LiveByProvider(ctx, f.user, aicreds.Anthropic)
	if err != nil {
		t.Fatalf("LiveByProvider: %v", err)
	}
	if sealed.LastUsedAt == nil || !sealed.LastUsedAt.Equal(quando) {
		t.Errorf("last_used_at = %v, atteso %v", sealed.LastUsedAt, quando)
	}
	if !sealed.UpdatedAt.After(credential.UpdatedAt) {
		t.Errorf("il trigger di updated_at non ha toccato la riga: %s non è successivo a %s",
			sealed.UpdatedAt, credential.UpdatedAt)
	}

	// E il materiale è quello scritto: la query lunga è l'unica che lo legge.
	if !bytes.Equal(sealed.Ciphertext, []byte("materiale-cifrato-di-anthropic")) {
		t.Errorf("materiale = %q", sealed.Ciphertext)
	}
}

// Le chiavi seguono l'account: cancellato l'utente, spariscono. È la `ON DELETE
// CASCADE` della 0007, ed è ciò che R45 chiede alla cancellazione.
func TestCredentialsFollowTheAccount(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.salva(aicreds.Anthropic)
	f.salva(aicreds.OpenAI)

	if _, err := f.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, f.user); err != nil {
		t.Fatalf("cancellazione dell'account: %v", err)
	}

	var rimaste int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM ai_credentials WHERE user_id = $1`, f.user).Scan(&rimaste); err != nil {
		t.Fatal(err)
	}
	if rimaste != 0 {
		t.Errorf("chiavi rimaste dopo la cancellazione dell'account: %d", rimaste)
	}
}

// ------------------------------------------------- il giro con la cifratura vera

// Il giro completo con il cifrario vero: **ciò che sta a riposo in PostgreSQL
// non è la chiave**, in nessuna colonna e in nessun byte, e si riapre solo con
// il keyring.
//
// Gli altri test di questo package usano materiale finto apposta, per non
// dipendere da due cose insieme. Questo esiste perché la domanda di R18 è
// esattamente su ciò che finisce nel database vero — un dump, una replica, un
// backup — e la risposta va data guardando i byte che ci sono dentro.
func TestRoundTripWithTheRealCipher(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	const chiave = "sk-ant-api03-chiave-che-non-deve-comparire-nel-database-1234567890"
	keyring, err := secretbox.NewKeyring("MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	svc, err := aicreds.NewService(aicreds.Options{
		Store:   f.store,
		Keyring: keyring,
		Logger:  slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	credential, err := svc.Save(ctx, f.user, aicreds.SaveInput{
		Provider: "anthropic",
		Key:      secretbox.Plaintext(chiave),
		Label:    "chiave vera",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Nessuna colonna della riga contiene la chiave, nemmeno un pezzo. Il
	// controllo è su **tutte** le colonne rese testo, non solo su quelle che ci
	// aspettiamo: è il modo di accorgersi di una colonna aggiunta domani.
	var riga string
	if err := f.pool.QueryRow(ctx,
		`SELECT to_jsonb(c)::text FROM ai_credentials c WHERE id = $1`, credential.ID).Scan(&riga); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(riga, chiave) {
		t.Fatalf("la riga contiene la chiave in chiaro: %s", riga)
	}
	// E nemmeno una sua coda: `last_four` non c'è più, e questo verifica che non
	// sia tornata sotto un altro nome.
	if coda := chiave[len(chiave)-4:]; strings.Contains(riga, coda) {
		t.Errorf("la riga contiene la coda della chiave (%q): %s", coda, riga)
	}

	// Riletta, è quella scritta.
	riletta, err := svc.Reveal(ctx, f.user, aicreds.Anthropic)
	if err != nil {
		t.Fatalf("Reveal: %v", err)
	}
	if riletta.Reveal() != chiave {
		t.Error("la chiave riletta non è quella scritta")
	}

	// Revocata, il materiale sparisce dal database e non torna più.
	if err := svc.Revoke(ctx, f.user, credential.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	var ciphertext, nonce []byte
	if err := f.pool.QueryRow(ctx,
		`SELECT ciphertext, nonce FROM ai_credentials WHERE id = $1`,
		credential.ID).Scan(&ciphertext, &nonce); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) != 0 || len(nonce) != 0 {
		t.Error("la revoca ha lasciato materiale cifrato nel database")
	}
	if _, err := svc.Reveal(ctx, f.user, aicreds.Anthropic); !errors.Is(err, aicreds.ErrNoLiveKey) {
		t.Errorf("Reveal dopo la revoca = %v, atteso ErrNoLiveKey", err)
	}
}

// -------------------------------------------------------------- migrazione

// Il numero **non** è scritto nel test: questa migrazione è la 0016 oggi, e il
// nome è ciò che non collide se un'altra issue si prende il numero per prima.
const migrationName = "ai_credentials_revocation"

// La migrazione è reversibile (AGENTS.md §5): la `down` rimette lo schema della
// 0007 — `last_four`, i due `CHECK` separati, l'unicità totale — e la `up` che
// segue lo riporta a quello della 0016 senza inciampare in ciò che la `down`
// avesse lasciato indietro.
//
// Il test annulla e riapplica **la migrazione vera**, non un `ALTER TABLE`
// scritto qui: una `down` che dimentica un vincolo fallirebbe solo alla
// riapplicazione, che è esattamente ciò che questo giro verifica.
func TestMigrationIsReversible(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	// Prima di annullare, una riga revocata: è il caso che la `down` deve saper
	// gestire, perché nello schema della 0007 non ha un posto dove stare.
	revocata := f.salva(aicreds.Anthropic)
	if err := f.store.RevokeCredential(ctx, f.user, revocata.ID, time.Now()); err != nil {
		t.Fatalf("RevokeCredential: %v", err)
	}
	f.salva(aicreds.Anthropic)

	haColonna := func(nome string) bool {
		t.Helper()
		var found bool
		if err := f.pool.QueryRow(ctx,
			`SELECT count(*) > 0 FROM information_schema.columns
			  WHERE table_name = 'ai_credentials' AND table_schema = 'public'
			    AND column_name = $1`, nome).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	if !haColonna("revoked_at") || haColonna("last_four") {
		t.Fatal("lo schema non è quello della 0016 dopo le migrazioni")
	}

	dir, err := migrate.FindDir(".")
	if err != nil {
		t.Fatalf("directory delle migrazioni: %v", err)
	}
	migrations, err := migrate.Load(dir)
	if err != nil {
		t.Fatalf("caricamento delle migrazioni: %v", err)
	}

	// L'annullamento parte sempre dall'ultima applicata: `passi` è quante ce ne
	// sono da qui in avanti, così che il test non si leghi all'ordine di arrivo
	// delle issue. È la stessa forma di internal/secretspg, e il motivo è che la
	// versione che pretendeva di essere l'ultima è già fallita una volta.
	indice := slices.IndexFunc(migrations, func(m migrate.Migration) bool {
		return m.Name == migrationName
	})
	if indice < 0 {
		t.Fatalf("migrazione %s non trovata fra quelle caricate", migrationName)
	}
	passi := len(migrations) - indice

	conn, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquisizione della connessione: %v", err)
	}
	defer conn.Release()
	migrator := migrate.New(conn, migrations, nil)

	if _, err := migrator.Down(ctx, passi); err != nil {
		t.Fatalf("annullamento fino a %s: %v", migrationName, err)
	}
	if haColonna("revoked_at") || !haColonna("last_four") {
		t.Fatal("l'annullamento non ha rimesso lo schema della 0007")
	}
	// La riga revocata è sparita, quella viva è rimasta: nello schema della 0007
	// una riga senza materiale non è rappresentabile.
	var rimaste int
	if err := f.pool.QueryRow(ctx,
		`SELECT count(*) FROM ai_credentials WHERE user_id = $1`, f.user).Scan(&rimaste); err != nil {
		t.Fatal(err)
	}
	if rimaste != 1 {
		t.Errorf("righe dopo l'annullamento = %d, attesa 1 (la viva)", rimaste)
	}

	if _, err := migrator.Up(ctx, passi); err != nil {
		t.Fatalf("riapplicazione fino a %s: %v", migrationName, err)
	}
	if !haColonna("revoked_at") || haColonna("last_four") {
		t.Fatal("la riapplicazione non ha rimesso lo schema della 0016")
	}

	// E lo schema riapplicato funziona: il vincolo della revoca è tornato con
	// lui, ed è la parte che una `up` incompleta lascerebbe indietro.
	viva, err := f.store.ListCredentials(ctx, f.user, false)
	if err != nil || len(viva) != 1 {
		t.Fatalf("ListCredentials dopo la riapplicazione = %d righe, %v", len(viva), err)
	}
	if _, err := f.pool.Exec(ctx,
		`UPDATE ai_credentials SET revoked_at = now() WHERE id = $1`, viva[0].ID); err == nil {
		t.Error("il vincolo che lega la revoca allo svuotamento non è tornato con la migrazione")
	}
	if err := f.store.RevokeCredential(ctx, f.user, viva[0].ID, time.Now()); err != nil {
		t.Errorf("RevokeCredential dopo la riapplicazione: %v", err)
	}
}

func TestNewRejectsANilPool(t *testing.T) {
	if _, err := aicredspg.New(nil); err == nil {
		t.Fatal("New(nil) non ha segnalato niente")
	}
}
