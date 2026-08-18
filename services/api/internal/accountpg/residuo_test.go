package accountpg_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Questo file contiene i due test che tengono in piedi la frase della privacy
// policy §5, e sono due perché le cose che possono andare storte sono due e
// opposte.
//
//	TestDopoLaPurgaNonSopravviveNienteDellUtente   cancellare troppo poco
//	TestLaPurgaNonTocca...                        cancellare troppo
//
// Nessuno dei due contiene un elenco di tabelle scritto a mano. È il punto: una
// lista compilata oggi non conosce la tabella che arriverà con la prossima
// migrazione, e il giorno in cui qualcuno la aggiungerà questi test
// continuerebbero a passare mentre la promessa smette di essere vera. Le tabelle
// si chiedono allo schema.

// tablesInSchema elenca le tabelle ordinarie del database, partizioni escluse.
//
// Le partizioni si escludono perché le loro righe si vedono già dalla tabella
// padre: contarle due volte non aggiunge copertura, e i loro nomi cambiano ogni
// giorno (`job_executions_20260818`), il che renderebbe l'elenco diverso a ogni
// esecuzione.
//
// `schema_migrations` resta dentro di proposito: non contiene dati di utenti, e
// se un giorno ne contenesse è esattamente il caso che questo test deve vedere.
func tablesInSchema(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT c.relname
		   FROM pg_class c
		   JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE n.nspname = 'public'
		    AND c.relkind IN ('r', 'p')
		    AND NOT c.relispartition
		  ORDER BY c.relname`)
	if err != nil {
		t.Fatalf("elenco delle tabelle: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("lettura di una tabella: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("elenco delle tabelle: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("nessuna tabella trovata: le migrazioni non sono state applicate")
	}
	return out
}

// rowsMentioning restituisce le righe di `table` in cui compare `marker`, in
// forma leggibile.
//
// La ricerca è su `to_jsonb(t.*)::text`, cioè **su tutte le colonne insieme**,
// comprese quelle aggiunte da una migrazione che ancora non esiste. È la
// differenza fra un test che verifica le tabelle che conosceva chi lo ha scritto
// e uno che verifica quelle che ci sono.
//
// I `bytea` finiscono in jsonb come esadecimale, quindi un marcatore cifrato non
// si troverebbe cercando il testo: il seme lo aggira scrivendo nei `ciphertext`
// dei valori il cui testo in chiaro è il marcatore, e la ricerca guarda anche la
// forma esadecimale — vedi [hexOf].
func rowsMentioning(t *testing.T, pool *pgxpool.Pool, table, marker string) []string {
	t.Helper()

	query := fmt.Sprintf(
		`SELECT to_jsonb(t.*)::text
		   FROM %q t
		  WHERE to_jsonb(t.*)::text ILIKE '%%' || $1 || '%%'
		     OR to_jsonb(t.*)::text ILIKE '%%' || $2 || '%%'
		  LIMIT 5`, table)

	rows, err := pool.Query(t.Context(), query, marker, hexOf(marker))
	if err != nil {
		t.Fatalf("ricerca di %q in %q: %v", marker, table, err)
	}
	defer rows.Close()

	var found []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatalf("lettura di una riga di %q: %v", table, err)
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("ricerca di %q in %q: %v", marker, table, err)
	}
	return found
}

// hexOf è la forma con cui un `bytea` compare dentro `to_jsonb`.
func hexOf(marker string) string {
	var sb strings.Builder
	for _, b := range []byte(marker) {
		fmt.Fprintf(&sb, "%02x", b)
	}
	return sb.String()
}

// TestDopoLaPurgaNonSopravviveNienteDellUtente è la verifica della frase «then
// remove the data» della privacy policy §5.
//
// Il metodo: si popola un account in ogni tabella che possa contenere qualcosa
// di suo, si purga, e si cerca **ogni suo marcatore in ogni colonna di ogni
// tabella dello schema**. Non c'è una lista da mantenere allineata: le tabelle
// arrivano da `pg_class`, le colonne da `to_jsonb`.
//
// La prima metà del test verifica il test stesso: prima della purga i marcatori
// devono esserci. Senza quel controllo, una ricerca sbagliata — un `ILIKE` che
// non combacia mai, un seme che non ha scritto niente — passerebbe come se
// avesse verificato qualcosa.
func TestDopoLaPurgaNonSopravviveNienteDellUtente(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "alfa", 1)

	tables := tablesInSchema(t, pool)

	// Controprova: prima della purga ogni marcatore si trova da qualche parte.
	// Se questo pezzo fallisce, il problema è il seme o la ricerca, e la seconda
	// metà del test non proverebbe niente.
	for _, marker := range te.Markers {
		if !mentionedAnywhere(t, pool, tables, marker) {
			t.Fatalf("il marcatore %q non compare da nessuna parte prima della purga: "+
				"il seme o la ricerca non funzionano, e il resto del test non proverebbe niente", marker)
		}
	}

	requestNow(t, store, te.UserID, 0)
	if _, err := store.Purge(t.Context(), te.UserID); err != nil {
		t.Fatalf("purga: %v", err)
	}

	for _, marker := range te.Markers {
		for _, table := range tables {
			if rows := rowsMentioning(t, pool, table, marker); len(rows) > 0 {
				t.Errorf("dopo la purga %q compare ancora in %q:\n  %s",
					marker, table, strings.Join(rows, "\n  "))
			}
		}
	}
}

func mentionedAnywhere(t *testing.T, pool *pgxpool.Pool, tables []string, marker string) bool {
	t.Helper()
	for _, table := range tables {
		if len(rowsMentioning(t, pool, table, marker)) > 0 {
			return true
		}
	}
	return false
}

// TestLaPurgaNonToccaNienteDellAltroAccount è la verifica opposta: che una
// cancellazione a cascata non porti via righe di **altri**.
//
// Il metodo è lo stesso rovesciato: due account popolati allo stesso modo, se ne
// cancella uno, e del secondo si confronta **ogni riga di ogni tabella**, prima e
// dopo, in forma jsonb. Non un conteggio — un conteggio non vede una colonna
// azzerata — ma il contenuto.
//
// Le due tabelle senza `user_id` sono la ragione per cui questo test esiste:
// `github_webhook_deliveries` si identifica per repository, e la 0004 dichiara
// esplicitamente che «utenti diversi possono collegare lo stesso repository
// pubblico». Il caso condiviso ha un test suo — vedi
// TestLeConsegneDiUnRepositorySeguitoDaDueUtentiRestano — questo copre il caso
// normale, che è quello che una cascata scritta male rompe per primo.
func TestLaPurgaNonToccaNienteDellAltroAccount(t *testing.T) {
	store, pool := newStore(t)
	vittima := seedTenant(t, pool, "beta", 2)
	spettatore := seedTenant(t, pool, "gamma", 3)

	tables := tablesInSchema(t, pool)
	prima := snapshot(t, pool, tables)

	requestNow(t, store, vittima.UserID, 0)
	if _, err := store.Purge(t.Context(), vittima.UserID); err != nil {
		t.Fatalf("purga: %v", err)
	}

	dopo := snapshot(t, pool, tables)

	// Di ogni tabella si confrontano le righe che nominano lo spettatore: quelle
	// della vittima devono essere sparite, le sue devono essere identiche.
	for _, table := range tables {
		suePrima := filterMentioning(prima[table], spettatore.Markers)
		sueDopo := filterMentioning(dopo[table], spettatore.Markers)

		if len(suePrima) != len(sueDopo) {
			t.Errorf("%s: lo spettatore aveva %d righe e ne ha %d dopo la purga di un altro account",
				table, len(suePrima), len(sueDopo))
			continue
		}
		for i := range suePrima {
			if suePrima[i] != sueDopo[i] {
				t.Errorf("%s: una riga dello spettatore è cambiata dopo la purga di un altro account\n  prima: %s\n  dopo:  %s",
					table, suePrima[i], sueDopo[i])
			}
		}
	}
}

// snapshot legge ogni riga di ogni tabella in forma jsonb, ordinata.
//
// L'ordinamento è sul testo della riga e non su una chiave primaria: le tabelle
// non hanno tutte la stessa, e ciò che serve qui è un ordine **stabile** fra due
// letture, non un ordine significativo.
func snapshot(t *testing.T, pool *pgxpool.Pool, tables []string) map[string][]string {
	t.Helper()
	out := make(map[string][]string, len(tables))
	for _, table := range tables {
		rows, err := pool.Query(t.Context(),
			fmt.Sprintf(`SELECT to_jsonb(t.*)::text FROM %q t`, table))
		if err != nil {
			t.Fatalf("lettura di %q: %v", table, err)
		}
		var content []string
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				rows.Close()
				t.Fatalf("lettura di una riga di %q: %v", table, err)
			}
			content = append(content, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatalf("lettura di %q: %v", table, err)
		}
		sort.Strings(content)
		out[table] = content
	}
	return out
}

func filterMentioning(rows []string, markers []string) []string {
	var out []string
	for _, row := range rows {
		for _, marker := range markers {
			if strings.Contains(row, marker) || strings.Contains(row, hexOf(marker)) {
				out = append(out, row)
				break
			}
		}
	}
	return out
}

// TestOgniTabellaChePuntaAUsersÈStataConsiderata chiude la strada che il test
// dei marcatori da solo lascia aperta.
//
// La ricerca per marcatori trova ciò che **contiene** un valore dell'utente. Una
// tabella futura potrebbe però riferirlo senza contenerne il testo — con una
// chiave esterna verso `jobs`, per esempio, e nient'altro di riconoscibile. Qui
// la strada è l'altra: si chiede allo schema quali tabelle hanno una chiave
// verso `users`, direttamente o attraverso altre, e si verifica che dopo la
// purga nessuna riga arrivi ancora all'utente cancellato.
//
// Le due insieme sono la copertura: la prima vede i valori, la seconda le
// relazioni. Se una migrazione futura aggiunge una tabella che riferisce
// l'utente, questa la trova senza che nessuno debba ricordarsi di aggiornare un
// elenco.
func TestOgniTabellaChePuntaAUsersÈStataConsiderata(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "delta", 4)

	// Le tabelle con una chiave esterna verso `users`, con il nome della colonna.
	rows, err := pool.Query(t.Context(),
		`SELECT c.conrelid::regclass::text, a.attname, c.confdeltype
		   FROM pg_constraint c
		   JOIN unnest(c.conkey) AS k(attnum) ON true
		   JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
		  WHERE c.contype = 'f'
		    AND c.confrelid = 'users'::regclass
		  ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("elenco delle chiavi esterne verso users: %v", err)
	}
	defer rows.Close()

	type reference struct {
		table, column, onDelete string
	}
	var refs []reference
	for rows.Next() {
		var r reference
		if err := rows.Scan(&r.table, &r.column, &r.onDelete); err != nil {
			t.Fatalf("lettura di una chiave esterna: %v", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("elenco delle chiavi esterne verso users: %v", err)
	}
	if len(refs) == 0 {
		t.Fatal("nessuna chiave esterna verso `users`: lo schema non è quello atteso")
	}

	requestNow(t, store, te.UserID, 0)
	if _, err := store.Purge(t.Context(), te.UserID); err != nil {
		t.Fatalf("purga: %v", err)
	}

	for _, r := range refs {
		n := count(t, pool,
			fmt.Sprintf(`SELECT count(*) FROM %s WHERE %s = $1::uuid`, r.table, r.column),
			te.UserID)
		if n == 0 {
			continue
		}
		// `n` diverso da zero significa che una riga riferisce ancora l'utente
		// cancellato. Con `ON DELETE SET NULL` (audit_log, 0008) è impossibile per
		// costruzione; con `CASCADE` lo è altrettanto. Resta possibile su una
		// chiave futura dichiarata `NO ACTION`, ed è esattamente il caso che questo
		// test deve far fallire invece di lasciar passare in silenzio.
		t.Errorf("%s.%s: %d righe riferiscono ancora l'account cancellato (ON DELETE %q)",
			r.table, r.column, n, r.onDelete)
	}
}

// TestLaPurgaNonAvvieneFinchéLaGraziaNonÈScaduta verifica il periodo di
// sicurezza di R45 dai due lati.
//
// È la parte del documento che dice «during which you can change your mind»:
// purgare prima significherebbe togliere all'utente i giorni che gli abbiamo
// promesso, e non purgare dopo significherebbe non mantenere l'altra metà della
// stessa frase.
func TestLaPurgaNonAvvieneFinchéLaGraziaNonÈScaduta(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "epsilon", 5)

	richiesta := requestNow(t, store, te.UserID, time.Hour)

	// Un istante prima della scadenza non c'è niente da purgare.
	due, err := store.DueForPurge(t.Context(), richiesta.Add(time.Hour-time.Second), 10)
	if err != nil {
		t.Fatalf("ricerca degli account scaduti: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("account trovato scaduto prima della fine della grazia: %v", due)
	}
	if n := count(t, pool, `SELECT count(*) FROM users WHERE id = $1::uuid`, te.UserID); n != 1 {
		t.Fatal("l'account non esiste più durante la grazia")
	}

	// Alla scadenza sì. `<=` e non `<`: chi ci arriva esattamente sopra l'ha
	// raggiunta.
	due, err = store.DueForPurge(t.Context(), richiesta.Add(time.Hour), 10)
	if err != nil {
		t.Fatalf("ricerca degli account scaduti: %v", err)
	}
	if len(due) != 1 || due[0] != te.UserID {
		t.Fatalf("account scaduti = %v, atteso [%s]", due, te.UserID)
	}
}

// TestUnAccountSenzaRichiestaNonVieneMaiPurgato è il rovescio del test
// precedente, e copre l'errore più grave possibile: purgare qualcuno che non ha
// chiesto niente.
func TestUnAccountSenzaRichiestaNonVieneMaiPurgato(t *testing.T) {
	store, pool := newStore(t)
	te := seedTenant(t, pool, "zeta", 6)

	due, err := store.DueForPurge(t.Context(), time.Now().AddDate(10, 0, 0), 10)
	if err != nil {
		t.Fatalf("ricerca degli account scaduti: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("account senza richiesta trovato scaduto fra dieci anni: %v", due)
	}
	if n := count(t, pool, `SELECT count(*) FROM users WHERE id = $1::uuid`, te.UserID); n != 1 {
		t.Fatal("l'account è sparito senza che nessuno lo avesse chiesto")
	}
}
