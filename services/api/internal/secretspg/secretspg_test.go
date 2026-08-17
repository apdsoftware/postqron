package secretspg_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/migrate"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
	"github.com/apdsoftware/postqron/services/api/internal/secretspg"
)

// ---------------------------------------------------------------- impalcatura

type fixture struct {
	t     *testing.T
	pool  *pgxpool.Pool
	store *secretspg.Store
	user  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	pool := newTestDatabase(t)
	store, err := secretspg.New(pool)
	if err != nil {
		t.Fatalf("secretspg.New: %v", err)
	}
	return &fixture{t: t, pool: pool, store: store, user: createUser(t, pool, "alice@example.com")}
}

// sealed compone una riga finta. Il materiale «cifrato» qui è testo
// riconoscibile: questo package non cifra niente, e mescolarci una cifratura
// vera renderebbe i test dipendenti da due cose invece che da una.
func sealed(userID, name string) secrets.Sealed {
	return secrets.Sealed{
		Secret: secrets.Secret{
			UserID:      userID,
			Name:        name,
			KeyVersion:  1,
			Description: "nota di " + name,
			CreatedAt:   time.Now().UTC(),
		},
		Ciphertext: []byte("testo-cifrato-di-" + name),
		Nonce:      []byte("nonce-di-" + name),
	}
}

func (f *fixture) crea(name string) secrets.Secret {
	f.t.Helper()
	secret, err := f.store.CreateSecret(f.t.Context(), sealed(f.user, name))
	if err != nil {
		f.t.Fatalf("CreateSecret(%s): %v", name, err)
	}
	return secret
}

// -------------------------------------------------- lo schema che protegge

// R42: la tabella non ha una colonna in cui il valore in chiaro potrebbe
// finire, e le colonne che ci sono sono quelle che ci aspettiamo.
//
// È lo stesso controllo che la 0007 ha per `ai_credentials`, e vale la pena
// ripeterlo: una colonna `value` aggiunta da una migrazione futura per comodità
// sarebbe la fine di R42, e non lo direbbe nessuno.
func TestSchemaHasNoColumnForThePlaintext(t *testing.T) {
	f := newFixture(t)

	rows, err := f.pool.Query(t.Context(),
		`SELECT column_name FROM information_schema.columns
		  WHERE table_name = 'workspace_secrets' AND table_schema = 'public'
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

	vietate := []string{"value", "plaintext", "plain", "secret_value", "preview", "last_four"}
	for _, column := range columns {
		for _, vietata := range vietate {
			if strings.Contains(column, vietata) {
				t.Errorf("colonna sospetta in workspace_secrets: %q", column)
			}
		}
	}

	attese := []string{
		"ciphertext", "created_at", "description", "id", "key_version",
		"last_used_at", "name", "nonce", "revoked_at", "updated_at", "user_id",
	}
	if !slices.Equal(columns, attese) {
		t.Errorf("colonne =\n  %v\nattese\n  %v", columns, attese)
	}
}

// Il `CHECK` sul nome è nel database e non solo nel codice: un segreto può
// nascere solo da qui, ma il vincolo che il parser di `cron.yaml` applicherà al
// file dev'essere lo stesso che la colonna ammette.
func TestSchemaEnforcesTheNameFormat(t *testing.T) {
	f := newFixture(t)

	for _, name := range []string{"digest", "DIGEST-TOKEN", "1TOKEN", "", strings.Repeat("A", 65)} {
		_, err := f.pool.Exec(t.Context(),
			`INSERT INTO workspace_secrets (user_id, name, ciphertext, nonce)
			 VALUES ($1, $2, '\x01', '\x02')`, f.user, name)
		if err == nil {
			t.Errorf("il nome %q è stato accettato", name)
		}
	}

	if _, err := f.pool.Exec(t.Context(),
		`INSERT INTO workspace_secrets (user_id, name, ciphertext, nonce)
		 VALUES ($1, 'DIGEST_TOKEN_2', '\x01', '\x02')`, f.user); err != nil {
		t.Errorf("un nome legittimo è stato rifiutato: %v", err)
	}
}

// **Revocare significa che il valore smette di esistere**, e il vincolo lega le
// due cose in modo che non possano separarsi: non si data la revoca lasciando il
// testo cifrato, e non si svuota il testo cifrato lasciando la riga
// utilizzabile.
func TestSchemaTiesRevocationToTheEmptyCiphertext(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN")

	// Revocare senza svuotare: rifiutato.
	if _, err := f.pool.Exec(t.Context(),
		`UPDATE workspace_secrets SET revoked_at = now() WHERE id = $1`, secret.ID); err == nil {
		t.Error("è stata datata una revoca lasciando il testo cifrato in tabella")
	}

	// Svuotare senza revocare: rifiutato.
	if _, err := f.pool.Exec(t.Context(),
		`UPDATE workspace_secrets SET ciphertext = '\x'::bytea, nonce = '\x'::bytea WHERE id = $1`,
		secret.ID); err == nil {
		t.Error("il testo cifrato è stato svuotato lasciando la riga viva")
	}

	// Le due cose insieme: è ciò che fa RevokeSecret.
	if err := f.store.RevokeSecret(t.Context(), f.user, secret.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}

	var ciphertext, nonce []byte
	var revokedAt *time.Time
	if err := f.pool.QueryRow(t.Context(),
		`SELECT ciphertext, nonce, revoked_at FROM workspace_secrets WHERE id = $1`,
		secret.ID).Scan(&ciphertext, &nonce, &revokedAt); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) != 0 || len(nonce) != 0 {
		t.Error("la revoca ha lasciato materiale cifrato in tabella")
	}
	if revokedAt == nil {
		t.Error("la revoca non ha datato la riga")
	}
}

// ----------------------------------------------------------- unicità del nome

// Il nome è unico **fra i soli vivi**: revocato un segreto, il nome torna
// disponibile. Senza, la revoca brucerebbe per sempre il riferimento scritto nel
// `cron.yaml`.
func TestNameIsUniqueAmongTheLivingOnly(t *testing.T) {
	f := newFixture(t)
	primo := f.crea("DIGEST_TOKEN")

	if _, err := f.store.CreateSecret(t.Context(), sealed(f.user, "DIGEST_TOKEN")); !errors.Is(err, secrets.ErrDuplicateName) {
		t.Fatalf("errore = %v, atteso ErrDuplicateName", err)
	}

	// Un altro workspace può avere lo stesso nome.
	bob := createUser(t, f.pool, "bob@example.com")
	if _, err := f.store.CreateSecret(t.Context(), sealed(bob, "DIGEST_TOKEN")); err != nil {
		t.Fatalf("lo stesso nome in un altro workspace: %v", err)
	}

	// Revocato il primo, il nome torna libero.
	if err := f.store.RevokeSecret(t.Context(), f.user, primo.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}
	rinato, err := f.store.CreateSecret(t.Context(), sealed(f.user, "DIGEST_TOKEN"))
	if err != nil {
		t.Fatalf("il nome non è tornato disponibile dopo la revoca: %v", err)
	}
	if rinato.ID == primo.ID {
		t.Error("il segreto rinato è la stessa riga")
	}
}

// --------------------------------------------------------------- letture

// La lettura dell'elenco **non** porta con sé il materiale cifrato: è la sola
// query della risoluzione a leggerlo.
func TestListDoesNotCarryTheCiphertext(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN")

	found, err := f.store.ListSecrets(t.Context(), f.user, false)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("segreti = %d", len(found))
	}
	if found[0].Name != "DIGEST_TOKEN" || found[0].Description != "nota di DIGEST_TOKEN" {
		t.Errorf("segreto = %+v", found[0])
	}
	if found[0].KeyVersion != 1 {
		t.Errorf("KeyVersion = %d", found[0].KeyVersion)
	}
}

// L'elenco è per workspace e distingue i revocati.
func TestListScopeAndRevoked(t *testing.T) {
	f := newFixture(t)
	primo := f.crea("PRIMO_TOKEN")
	f.crea("SECONDO_TOKEN")
	if err := f.store.RevokeSecret(t.Context(), f.user, primo.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}

	vivi, err := f.store.ListSecrets(t.Context(), f.user, false)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(vivi) != 1 || vivi[0].Name != "SECONDO_TOKEN" {
		t.Errorf("vivi = %+v", vivi)
	}

	tutti, err := f.store.ListSecrets(t.Context(), f.user, true)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(tutti) != 2 {
		t.Errorf("tutti = %d", len(tutti))
	}

	bob := createUser(t, f.pool, "bob@example.com")
	altrui, err := f.store.ListSecrets(t.Context(), bob, true)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	if len(altrui) != 0 {
		t.Errorf("un altro workspace vede %d segreti", len(altrui))
	}
}

// La query della risoluzione prende i nomi che le servono, restituisce il
// materiale cifrato, e non vede i revocati né quelli di un altro workspace.
func TestLiveByNames(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN")
	revocato := f.crea("REVOCATO")
	f.crea("NON_RICHIESTO")
	if err := f.store.RevokeSecret(t.Context(), f.user, revocato.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}
	bob := createUser(t, f.pool, "bob@example.com")
	if _, err := f.store.CreateSecret(t.Context(), sealed(bob, "DIGEST_TOKEN")); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	rows, err := f.store.LiveByNames(t.Context(), f.user,
		[]string{"DIGEST_TOKEN", "REVOCATO", "MAI_ESISTITO"})
	if err != nil {
		t.Fatalf("LiveByNames: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("righe = %d, attesa 1: %+v", len(rows), rows)
	}
	if rows[0].Name != "DIGEST_TOKEN" || rows[0].UserID != f.user {
		t.Errorf("riga = %+v", rows[0].Secret)
	}
	if string(rows[0].Ciphertext) != "testo-cifrato-di-DIGEST_TOKEN" {
		t.Errorf("Ciphertext = %q", rows[0].Ciphertext)
	}
	if string(rows[0].Nonce) != "nonce-di-DIGEST_TOKEN" {
		t.Errorf("Nonce = %q", rows[0].Nonce)
	}

	// Un elenco vuoto non fa nemmeno la query.
	empty, err := f.store.LiveByNames(t.Context(), f.user, nil)
	if err != nil || len(empty) != 0 {
		t.Errorf("LiveByNames(nil) = %v, %v", empty, err)
	}
}

// La lettura della validazione al sync non tocca le colonne cifrate.
func TestLiveNames(t *testing.T) {
	f := newFixture(t)
	f.crea("B_TOKEN")
	f.crea("A_TOKEN")
	revocato := f.crea("C_TOKEN")
	if err := f.store.RevokeSecret(t.Context(), f.user, revocato.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}

	names, err := f.store.LiveNames(t.Context(), f.user)
	if err != nil {
		t.Fatalf("LiveNames: %v", err)
	}
	if !slices.Equal(names, []string{"A_TOKEN", "B_TOKEN"}) {
		t.Errorf("LiveNames = %v", names)
	}
}

func TestSecretByIDIsScoped(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN")
	bob := createUser(t, f.pool, "bob@example.com")

	if _, err := f.store.SecretByID(t.Context(), f.user, secret.ID); err != nil {
		t.Fatalf("SecretByID: %v", err)
	}
	if _, err := f.store.SecretByID(t.Context(), bob, secret.ID); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("errore = %v, atteso ErrNotFound", err)
	}

	if err := f.store.RevokeSecret(t.Context(), f.user, secret.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}
	if _, err := f.store.SecretByID(t.Context(), f.user, secret.ID); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("un segreto revocato non si legge: errore = %v", err)
	}
}

// ------------------------------------------------------------- scritture

// L'aggiornamento è ancorato al workspace e ai soli segreti vivi: riscrivere un
// segreto revocato lo farebbe tornare in vita violando il vincolo, cioè con un
// 500 al posto di un 404.
func TestUpdateScopeAndRevoked(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN")
	bob := createUser(t, f.pool, "bob@example.com")

	nuovo := secrets.Sealed{
		Ciphertext: []byte("testo-cifrato-nuovo"),
		Nonce:      []byte("nonce-nuovo"),
		Secret:     secrets.Secret{KeyVersion: 2},
	}

	if _, err := f.store.UpdateSecret(t.Context(), bob, secret.ID, nuovo, nil); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("aggiornamento da un altro workspace: errore = %v", err)
	}

	nota := "ruotato"
	aggiornato, err := f.store.UpdateSecret(t.Context(), f.user, secret.ID, nuovo, &nota)
	if err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}
	if aggiornato.KeyVersion != 2 || aggiornato.Description != nota {
		t.Errorf("aggiornato = %+v", aggiornato)
	}
	// **`updated_at` non va confrontato con `created_at`: sono due orologi.**
	//
	// `created_at` arriva dal processo Go (`in.CreatedAt`), `updated_at` dal
	// trigger dentro PostgreSQL. In sviluppo il database sta in un container, e
	// l'orologio della VM che lo ospita insegue quello dell'host a strappi: fra
	// una risincronizzazione e l'altra la deriva è di millisecondi — misurata
	// qui a 1,9 ms come minimo, vista arrivare a 14,5 ms in un fallimento reale.
	// Un `updated_at` scritto *dopo* può quindi risultare *precedente*, e nessun
	// ordine delle scritture lo impedisce.
	//
	// Il confronto giusto è con il valore che il trigger stesso aveva scritto
	// prima: stesso orologio, e per giunta è l'unica forma che dimostra davvero
	// che il trigger sia scattato. `After` stretto qui è sicuro perché le due
	// transazioni sono distinte e `now()` ha risoluzione al microsecondo.
	if !aggiornato.UpdatedAt.After(secret.UpdatedAt) {
		t.Errorf("il trigger non ha aggiornato updated_at: %s non è successivo a %s",
			aggiornato.UpdatedAt, secret.UpdatedAt)
	}

	// Senza nota, la nota resta com'era.
	invariato, err := f.store.UpdateSecret(t.Context(), f.user, secret.ID, nuovo, nil)
	if err != nil {
		t.Fatalf("UpdateSecret: %v", err)
	}
	if invariato.Description != nota {
		t.Errorf("la nota è cambiata senza che fosse indicata: %q", invariato.Description)
	}

	if err := f.store.RevokeSecret(t.Context(), f.user, secret.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}
	if _, err := f.store.UpdateSecret(t.Context(), f.user, secret.ID, nuovo, nil); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("un segreto revocato è stato aggiornato: errore = %v", err)
	}
}

// La revoca è ancorata al workspace e non è idempotente: la seconda non sposta
// la data, che è l'unica informazione che la riga porta ancora.
func TestRevokeScopeAndIdempotence(t *testing.T) {
	f := newFixture(t)
	secret := f.crea("DIGEST_TOKEN")
	bob := createUser(t, f.pool, "bob@example.com")

	if err := f.store.RevokeSecret(t.Context(), bob, secret.ID, time.Now()); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatalf("revoca da un altro workspace: errore = %v", err)
	}
	if err := f.store.RevokeSecret(t.Context(), f.user, secret.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}
	if err := f.store.RevokeSecret(t.Context(), f.user, secret.ID, time.Now()); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("seconda revoca: errore = %v", err)
	}
}

func TestTouchAndCount(t *testing.T) {
	f := newFixture(t)
	primo := f.crea("PRIMO_TOKEN")
	secondo := f.crea("SECONDO_TOKEN")

	count, err := f.store.CountLiveSecrets(t.Context(), f.user)
	if err != nil || count != 2 {
		t.Fatalf("CountLiveSecrets = %d, %v", count, err)
	}

	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := f.store.TouchSecrets(t.Context(), []string{primo.ID, secondo.ID}, at); err != nil {
		t.Fatalf("TouchSecrets: %v", err)
	}
	found, err := f.store.ListSecrets(t.Context(), f.user, false)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	for _, secret := range found {
		if secret.LastUsedAt == nil {
			t.Errorf("%s non ha last_used_at", secret.Name)
		}
	}

	// Un segreto revocato non si tocca, e un elenco vuoto non fa la query.
	if err := f.store.RevokeSecret(t.Context(), f.user, primo.ID, time.Now()); err != nil {
		t.Fatalf("RevokeSecret: %v", err)
	}
	if err := f.store.TouchSecrets(t.Context(), []string{primo.ID}, at.Add(time.Hour)); err != nil {
		t.Fatalf("TouchSecrets: %v", err)
	}
	if err := f.store.TouchSecrets(t.Context(), nil, at); err != nil {
		t.Fatalf("TouchSecrets(nil): %v", err)
	}

	if count, err = f.store.CountLiveSecrets(t.Context(), f.user); err != nil || count != 1 {
		t.Errorf("CountLiveSecrets dopo la revoca = %d, %v", count, err)
	}
}

// La cancellazione dell'account porta via i segreti (R45): la chiave esterna è
// `ON DELETE CASCADE`, e senza resterebbero righe cifrate di un utente che non
// esiste più.
func TestSecretsFollowTheAccount(t *testing.T) {
	f := newFixture(t)
	f.crea("DIGEST_TOKEN")

	if _, err := f.pool.Exec(t.Context(), `DELETE FROM users WHERE id = $1`, f.user); err != nil {
		t.Fatalf("cancellazione dell'account: %v", err)
	}

	var count int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT count(*) FROM workspace_secrets`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("sono rimasti %d segreti dopo la cancellazione dell'account", count)
	}
}

// migrationName è la migrazione dei segreti, senza il numero.
//
// Il numero **non** è scritto nei test. Questa migrazione è nata 0011 e ha
// dovuto diventare 0012 perché il webhook GitHub (#421) si è preso il numero
// per primo: un test che dicesse `Down(ctx, 1)` fidandosi di essere l'ultimo
// avrebbe continuato a passare annullando la migrazione di qualcun altro. Il
// nome, quello, non collide.
const migrationName = "workspace_secrets"

// La migrazione dei segreti è reversibile (AGENTS.md §5): la `down` toglie la
// tabella e il suo trigger, e la `up` che segue la rimette senza inciampare in
// ciò che la `down` avesse lasciato indietro.
//
// Il test annulla e riapplica **la migrazione vera**, non un `DROP TABLE`
// scritto qui: un `down` che dimentica qualcosa fallirebbe solo alla riapplicazione,
// che è esattamente ciò che questo giro verifica.
func TestMigrationIsReversible(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	exists := func() bool {
		t.Helper()
		var found bool
		if err := f.pool.QueryRow(ctx,
			`SELECT to_regclass('public.workspace_secrets') IS NOT NULL`).Scan(&found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	if !exists() {
		t.Fatal("la tabella non esiste dopo le migrazioni")
	}

	dir, err := migrate.FindDir(".")
	if err != nil {
		t.Fatalf("directory delle migrazioni: %v", err)
	}
	migrations, err := migrate.Load(dir)
	if err != nil {
		t.Fatalf("caricamento delle migrazioni: %v", err)
	}

	// L'annullamento parte dall'ultima applicata, quindi questo giro ha senso solo
	// se l'ultima è la nostra. Se un giorno non lo fosse, il test lo dice invece di
	// annullare in silenzio la migrazione di un'altra issue.
	ultima := migrations[len(migrations)-1]
	if ultima.Name != migrationName {
		t.Fatalf(
			"l'ultima migrazione è %s, non %s: questo test annulla l'ultima applicata "+
				"e con un'altra davanti annullerebbe quella. Rinumera, oppure riscrivi il test "+
				"per annullare fino alla propria versione.",
			ultima, migrationName)
	}

	conn, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquisizione della connessione: %v", err)
	}
	defer conn.Release()
	migrator := migrate.New(conn, migrations, nil)

	if _, err := migrator.Down(ctx, 1); err != nil {
		t.Fatalf("annullamento di %s: %v", ultima, err)
	}
	if exists() {
		t.Fatal("la tabella esiste ancora dopo l'annullamento")
	}

	if _, err := migrator.Up(ctx, 1); err != nil {
		t.Fatalf("riapplicazione di %s: %v", ultima, err)
	}
	if !exists() {
		t.Fatal("la tabella non è tornata dopo la riapplicazione")
	}

	// E la tabella riapplicata funziona: il trigger di `updated_at` è tornato con
	// lei, ed è la parte che un `down` incompleto lascerebbe indietro.
	secret, err := f.store.CreateSecret(ctx, sealed(f.user, "DOPO_LA_RIAPPLICAZIONE"))
	if err != nil {
		t.Fatalf("CreateSecret dopo la riapplicazione: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`UPDATE workspace_secrets SET description = 'toccata' WHERE id = $1`, secret.ID); err != nil {
		t.Fatalf("UPDATE dopo la riapplicazione: %v", err)
	}
	var updatedAt time.Time
	if err := f.pool.QueryRow(ctx,
		`SELECT updated_at FROM workspace_secrets WHERE id = $1`, secret.ID).Scan(&updatedAt); err != nil {
		t.Fatal(err)
	}
	// Come sopra: il paragone è con l'`updated_at` scritto all'inserimento, non
	// con `created_at`, che viene da un altro orologio.
	if !updatedAt.After(secret.UpdatedAt) {
		t.Errorf("il trigger di updated_at non è tornato con la tabella: %s non è successivo a %s",
			updatedAt, secret.UpdatedAt)
	}
}

func TestNewRejectsANilPool(t *testing.T) {
	if _, err := secretspg.New(nil); err == nil {
		t.Fatal("New(nil) non ha segnalato niente")
	}
}
