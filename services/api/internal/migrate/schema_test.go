package migrate_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaFixture applica lo schema e crea un utente con un job, restituendo i
// due identificativi.
type schemaFixture struct {
	pool   *pgxpool.Pool
	userID string
	jobID  string
}

func newSchemaFixture(t *testing.T) schemaFixture {
	t.Helper()
	pool := newTestDatabase(t)
	applyAll(t, pool)

	fixture := schemaFixture{pool: pool}
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO users (email, password_hash) VALUES ('dev@example.com', 'hash')
		 RETURNING id`).Scan(&fixture.userID); err != nil {
		t.Fatalf("creazione dell'utente: %v", err)
	}
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO jobs (user_id, name, schedule, url)
		 VALUES ($1, 'daily-digest', '0 9 * * *', 'https://api.example.com/tasks/digest')
		 RETURNING id`, fixture.userID).Scan(&fixture.jobID); err != nil {
		t.Fatalf("creazione del job: %v", err)
	}
	return fixture
}

// SPEC §9: `schedule` ed `every` sono mutuamente esclusivi, e uno dei due
// dev'esserci. Il vincolo vive nel database perché un job può arrivare da API o
// dashboard senza passare dal parser di `cron.yaml`.
func TestJobScheduleAndEveryAreMutuallyExclusive(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	tests := []struct {
		name         string
		schedule     any
		everySeconds any
		wantAccepted bool
	}{
		{"solo cron", "*/5 * * * *", nil, true},
		{"solo intervallo", nil, 1, true},
		{"entrambi", "0 9 * * *", 10, false},
		{"nessuno dei due", nil, nil, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := fixture.pool.Exec(ctx,
				`INSERT INTO jobs (user_id, name, schedule, every_seconds, url)
				 VALUES ($1, $2, $3, $4, 'https://api.example.com/health')`,
				fixture.userID, "job-"+strings.ReplaceAll(test.name, " ", "-"),
				test.schedule, test.everySeconds)

			if test.wantAccepted && err != nil {
				t.Fatalf("inserimento rifiutato ma atteso valido: %v", err)
			}
			if !test.wantAccepted {
				if err == nil {
					t.Fatal("inserimento accettato ma atteso rifiutato")
				}
				if !strings.Contains(err.Error(), "jobs_schedule_xor_every_check") {
					t.Errorf("rifiutato dal vincolo sbagliato: %v", err)
				}
			}
		})
	}
}

// R22: la risoluzione arriva a un secondo. Il valore minimo dev'essere
// accettato dallo schema; sotto, non esiste.
func TestJobIntervalAcceptsOneSecondAndRefusesZero(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO jobs (user_id, name, every_seconds, url)
		 VALUES ($1, 'healthcheck', 1, 'https://api.example.com/health')`,
		fixture.userID); err != nil {
		t.Fatalf("un intervallo di un secondo dev'essere ammesso (R22): %v", err)
	}

	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO jobs (user_id, name, every_seconds, url)
		 VALUES ($1, 'impossibile', 0, 'https://api.example.com/health')`,
		fixture.userID); err == nil {
		t.Error("un intervallo di zero secondi dev'essere rifiutato")
	}
}

// SPEC §10: PostQron chiama endpoint HTTP e nient'altro.
func TestJobTargetMustBeHTTP(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	for _, url := range []string{"ftp://example.com/x", "file:///etc/passwd", "echo ciao"} {
		if _, err := fixture.pool.Exec(ctx,
			`INSERT INTO jobs (user_id, name, every_seconds, url)
			 VALUES ($1, 'target', 10, $2)`, fixture.userID, url); err == nil {
			t.Errorf("target non HTTP accettato: %q", url)
		}
	}
}

// R23: un job vive in uno o più ambienti, senza ripetizioni.
func TestJobEnvironmentsMustBeNonEmptyAndUnique(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	insert := func(name, environments string) error {
		_, err := fixture.pool.Exec(ctx,
			`INSERT INTO jobs (user_id, name, every_seconds, url, environments)
			 VALUES ($1, $2, 10, 'https://api.example.com/health', $3::environment[])`,
			fixture.userID, name, environments)
		return err
	}

	if err := insert("entrambi", "{staging,production}"); err != nil {
		t.Fatalf("due ambienti distinti devono essere ammessi: %v", err)
	}
	if err := insert("vuoto", "{}"); err == nil {
		t.Error("un job senza ambienti dev'essere rifiutato")
	}
	if err := insert("duplicato", "{production,production}"); err == nil {
		t.Error("un ambiente ripetuto dev'essere rifiutato")
	}
}

// R13: il nome è l'identità del job. Dentro un repository è unico; fra
// repository diversi dello stesso utente no, perché due progetti possono avere
// entrambi un `healthcheck`.
func TestJobNameIdentityScope(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	var firstRepo, secondRepo string
	for _, repo := range []struct {
		name string
		dest *string
	}{{"api", &firstRepo}, {"web", &secondRepo}} {
		if err := fixture.pool.QueryRow(ctx,
			`INSERT INTO repositories (user_id, owner, name) VALUES ($1, 'acme', $2) RETURNING id`,
			fixture.userID, repo.name).Scan(repo.dest); err != nil {
			t.Fatalf("creazione del repository %s: %v", repo.name, err)
		}
	}

	insert := func(repositoryID any) error {
		_, err := fixture.pool.Exec(ctx,
			`INSERT INTO jobs (user_id, repository_id, name, every_seconds, url)
			 VALUES ($1, $2, 'healthcheck', 10, 'https://api.example.com/health')`,
			fixture.userID, repositoryID)
		return err
	}

	if err := insert(firstRepo); err != nil {
		t.Fatalf("primo repository: %v", err)
	}
	if err := insert(secondRepo); err != nil {
		t.Errorf("due repository dello stesso utente devono poter avere lo stesso nome di job: %v", err)
	}
	if err := insert(firstRepo); err == nil {
		t.Error("un nome ripetuto dentro lo stesso repository dev'essere rifiutato")
	}

	// I job creati a mano non hanno repository: lì l'ambito è l'utente.
	if err := insert(nil); err != nil {
		t.Fatalf("job senza repository: %v", err)
	}
	if err := insert(nil); err == nil {
		t.Error("un nome ripetuto fra i job senza repository dev'essere rifiutato")
	}
}

// R4: ogni occorrenza è eseguita una sola volta. La chiave primaria di
// job_executions è il lock: il secondo inserimento della stessa occorrenza
// fallisce, e questo vale anche fra due processi e fra riavvii.
func TestExecutionOccurrenceIsUniquePerEnvironmentAndAttempt(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	occurrence := time.Now().UTC().Truncate(time.Second)
	insert := func(environment string, attempt int) error {
		_, err := fixture.pool.Exec(ctx,
			`INSERT INTO job_executions (job_id, scheduled_for, environment, attempt)
			 VALUES ($1, $2, $3::environment, $4)`,
			fixture.jobID, occurrence, environment, attempt)
		return err
	}

	if err := insert("production", 1); err != nil {
		t.Fatalf("primo tentativo: %v", err)
	}
	if err := insert("production", 1); err == nil {
		t.Error("la stessa occorrenza è stata inserita due volte: l'idempotenza (R4) non è garantita")
	}
	// Un retry è un tentativo diverso della stessa occorrenza (R5).
	if err := insert("production", 2); err != nil {
		t.Errorf("il secondo tentativo dev'essere ammesso: %v", err)
	}
	// Lo stesso istante in un altro ambiente è un'altra esecuzione (R23).
	if err := insert("staging", 1); err != nil {
		t.Errorf("la stessa occorrenza in un altro ambiente dev'essere ammessa: %v", err)
	}
}

// R6: la durata è calcolata dal database a partire dagli istanti, così non può
// divergere da loro.
func TestExecutionDurationIsDerived(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	start := time.Now().UTC().Truncate(time.Second)
	var duration *int
	if err := fixture.pool.QueryRow(ctx,
		`INSERT INTO job_executions
		     (job_id, scheduled_for, environment, status, started_at, finished_at, response_status)
		 VALUES ($1, $2, 'production', 'succeeded', $3, $4, 200)
		 RETURNING duration_ms`,
		fixture.jobID, start, start, start.Add(1500*time.Millisecond)).Scan(&duration); err != nil {
		t.Fatalf("inserimento: %v", err)
	}
	if duration == nil || *duration != 1500 {
		t.Errorf("duration_ms = %v, atteso 1500", duration)
	}

	// Un'esecuzione conclusa prima di iniziare è un dato corrotto.
	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO job_executions
		     (job_id, scheduled_for, environment, status, started_at, finished_at)
		 VALUES ($1, $2, 'production', 'succeeded', $3, $4)`,
		fixture.jobID, start.Add(time.Minute), start.Add(time.Minute), start); err == nil {
		t.Error("una fine precedente all'inizio dev'essere rifiutata")
	}
}

// job_executions è partizionata per giorno su scheduled_for: è ciò che rende
// applicabile la retention di R6 senza cancellare riga per riga.
func TestExecutionsArePartitionedByDay(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	var partitioned bool
	if err := fixture.pool.QueryRow(ctx,
		`SELECT c.relkind = 'p' FROM pg_class c WHERE c.oid = 'job_executions'::regclass`).
		Scan(&partitioned); err != nil {
		t.Fatalf("lettura di pg_class: %v", err)
	}
	if !partitioned {
		t.Fatal("job_executions non è una tabella partizionata")
	}

	occurrence := time.Now().UTC().Truncate(time.Second)
	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO job_executions (job_id, scheduled_for, environment)
		 VALUES ($1, $2, 'production')`, fixture.jobID, occurrence); err != nil {
		t.Fatalf("inserimento: %v", err)
	}

	// La riga dev'essere finita nella partizione del suo giorno, non in una
	// qualsiasi: è il presupposto perché eliminare quella partizione elimini
	// esattamente le esecuzioni di quel giorno.
	var partition string
	if err := fixture.pool.QueryRow(ctx,
		`SELECT tableoid::regclass::text FROM job_executions WHERE job_id = $1`,
		fixture.jobID).Scan(&partition); err != nil {
		t.Fatalf("lettura della partizione: %v", err)
	}
	if want := "job_executions_" + occurrence.Format("20060102"); partition != want {
		t.Errorf("riga nella partizione %s, attesa %s", partition, want)
	}
}

// La retention lunga si applica eliminando partizioni intere (R6, SPEC §8).
func TestPartitionMaintenanceFunctions(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	countPartitions := func() int {
		t.Helper()
		var n int
		if err := fixture.pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_inherits WHERE inhparent = 'job_executions'::regclass`).
			Scan(&n); err != nil {
			t.Fatalf("conteggio delle partizioni: %v", err)
		}
		return n
	}

	initial := countPartitions()
	if initial == 0 {
		t.Fatal("la migrazione non ha creato alcuna partizione iniziale")
	}

	// La creazione è idempotente: rieseguirla non aggiunge nulla.
	if _, err := fixture.pool.Exec(ctx, `SELECT job_executions_ensure_partitions()`); err != nil {
		t.Fatalf("job_executions_ensure_partitions: %v", err)
	}
	if after := countPartitions(); after != initial {
		t.Errorf("la seconda esecuzione ha cambiato il numero di partizioni: %d → %d", initial, after)
	}

	// Estendere la finestra in avanti ne aggiunge.
	if _, err := fixture.pool.Exec(ctx, `SELECT job_executions_ensure_partitions(30, 1)`); err != nil {
		t.Fatalf("job_executions_ensure_partitions(30, 1): %v", err)
	}
	extended := countPartitions()
	if extended <= initial {
		t.Errorf("la finestra estesa non ha aggiunto partizioni: %d → %d", initial, extended)
	}

	// Il taglio elimina solo le partizioni interamente più vecchie della data.
	var dropped int
	if err := fixture.pool.QueryRow(ctx,
		`SELECT job_executions_drop_partitions_before((now() AT TIME ZONE 'UTC')::date)`).
		Scan(&dropped); err != nil {
		t.Fatalf("job_executions_drop_partitions_before: %v", err)
	}
	if dropped == 0 {
		t.Error("nessuna partizione eliminata: la finestra iniziale ne contiene una precedente a oggi")
	}

	// La partizione di oggi dev'essere sopravvissuta: contiene esecuzioni ancora
	// dentro qualunque retention.
	var today bool
	if err := fixture.pool.QueryRow(ctx,
		`SELECT to_regclass('job_executions_' || to_char((now() AT TIME ZONE 'UTC')::date, 'YYYYMMDD')) IS NOT NULL`).
		Scan(&today); err != nil {
		t.Fatal(err)
	}
	if !today {
		t.Error("il taglio ha eliminato anche la partizione di oggi")
	}
}

// SPEC §8: la retention è un dato del piano, non una costante nel codice.
func TestPlansCarryTheApprovedLimits(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	want := map[string]struct {
		minIntervalSeconds int
		logRetentionDays   int
	}{
		"free":   {60, 3},
		"pro":    {10, 15},
		"team":   {1, 30},
		"agency": {1, 90},
	}

	for code, expected := range want {
		var minInterval, retention int
		if err := fixture.pool.QueryRow(ctx,
			`SELECT min_interval_seconds, log_retention_days FROM plans WHERE code = $1`, code).
			Scan(&minInterval, &retention); err != nil {
			t.Fatalf("piano %s non trovato: %v", code, err)
		}
		if minInterval != expected.minIntervalSeconds {
			t.Errorf("piano %s: risoluzione minima %ds, attesa %ds", code, minInterval, expected.minIntervalSeconds)
		}
		if retention != expected.logRetentionDays {
			t.Errorf("piano %s: retention %d giorni, attesi %d", code, retention, expected.logRetentionDays)
		}
	}
}

// R16: la sottoscrizione è la fonte di verità del piano, quindi non ce ne può
// essere più di una viva per utente.
func TestOnlyOneLiveSubscriptionPerUser(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	insert := func(plan, status string) error {
		_, err := fixture.pool.Exec(ctx,
			`INSERT INTO subscriptions (user_id, plan_code, status, canceled_at)
			 VALUES ($1, $2, $3::subscription_status, CASE WHEN $3 = 'canceled' THEN now() END)`,
			fixture.userID, plan, status)
		return err
	}

	if err := insert("free", "active"); err != nil {
		t.Fatalf("prima sottoscrizione: %v", err)
	}
	if err := insert("pro", "active"); err == nil {
		t.Error("due sottoscrizioni vive per lo stesso utente sono state accettate")
	}
	// Le sottoscrizioni chiuse restano come storico e non partecipano al vincolo.
	if err := insert("pro", "canceled"); err != nil {
		t.Errorf("una sottoscrizione chiusa dev'essere ammessa accanto a una viva: %v", err)
	}
}

// R18: la chiave AI entra cifrata, e la tabella non ha una colonna dove
// potrebbe finire in chiaro.
func TestAICredentialsStoreOnlyCiphertext(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO ai_credentials (user_id, provider, ciphertext, nonce, last_four)
		 VALUES ($1, 'anthropic', '\x0102', '\x030405', 'ab12')`, fixture.userID); err != nil {
		t.Fatalf("inserimento: %v", err)
	}

	// Una seconda credenziale per lo stesso provider è un aggiornamento, non un
	// duplicato da disambiguare al momento dell'uso.
	if _, err := fixture.pool.Exec(ctx,
		`INSERT INTO ai_credentials (user_id, provider, ciphertext, nonce)
		 VALUES ($1, 'anthropic', '\x0607', '\x08090a')`, fixture.userID); err == nil {
		t.Error("due credenziali per lo stesso provider sono state accettate")
	}

	rows, err := fixture.pool.Query(ctx,
		`SELECT column_name FROM information_schema.columns
		  WHERE table_name = 'ai_credentials' AND table_schema = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(column, "plain") || column == "api_key" || column == "secret" {
			t.Errorf("colonna sospetta in ai_credentials: %q", column)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// Le due query calde della issue devono poter usare un indice. Il planner, su
// tabelle vuote, sceglierebbe comunque la scansione sequenziale: disabilitarla
// verifica ciò che interessa qui, cioè che un indice adatto esista.
func TestHotQueriesUseTheirIndexes(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	tests := []struct {
		name  string
		query string
		args  []any
		// L'indice atteso, per sottostringa: sulle partizioni di
		// job_executions il nome porta il suffisso della partizione
		// (`job_executions_20260817_pkey`).
		index string
	}{
		{
			name: "occorrenze dovute adesso",
			query: `SELECT id FROM jobs
			         WHERE enabled AND archived_at IS NULL AND next_run_at <= now()
			         ORDER BY next_run_at LIMIT 100`,
			index: "jobs_due_idx",
		},
		{
			name: "log di un job ordinati per data",
			query: `SELECT scheduled_for, status FROM job_executions
			         WHERE job_id = $1
			         ORDER BY scheduled_for DESC LIMIT 50`,
			args:  []any{fixture.jobID},
			index: "_pkey",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := explain(ctx, t, fixture.pool, test.query, test.args...)
			if !strings.Contains(plan, "Index Scan") {
				t.Errorf("la query non usa alcun indice:\n%s", plan)
			}
			if !strings.Contains(plan, test.index) {
				t.Errorf("la query non usa %s:\n%s", test.index, plan)
			}
		})
	}
}

// explain restituisce il piano di esecuzione con la scansione sequenziale
// disattivata.
//
// Tutto dentro una transazione annullata: `SET LOCAL` non sopravvive al
// rollback, e la connessione torna al pool con le impostazioni di partenza.
func explain(ctx context.Context, t *testing.T, pool *pgxpool.Pool, query string, args ...any) string {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("apertura della transazione: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("disattivazione della scansione sequenziale: %v", err)
	}

	rows, err := tx.Query(ctx, "EXPLAIN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan.String()
}
