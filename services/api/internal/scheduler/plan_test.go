package scheduler_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// Che un indice esista lo dice la migrazione. Che il pianificatore lo usi lo
// dice solo un EXPLAIN sulla query esatta che gira in produzione, su una tabella
// abbastanza popolata da rendere la scelta significativa — con dieci righe
// PostgreSQL scandisce comunque tutto, e un test su dieci righe non
// distinguerebbe mai un indice che serve da uno che non c'è.
//
// La scala non è teorica: un job a un secondo produce 86.400 occorrenze al
// giorno, mille job così ne producono 86 milioni. Le query di questo package
// girano quattro volte al secondo su quei numeri.

const (
	jobCount       = 5000
	executionCount = 50_000
)

// explain restituisce il piano della query, in JSON, come testo.
//
// Senza ANALYZE: il piano si vuole, non l'esecuzione. Le query calde hanno un
// `FOR UPDATE` e non è il caso di eseguirle davvero solo per vederne il piano.
func explain(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var plan []byte
	if err := pool.QueryRow(t.Context(), "EXPLAIN (FORMAT JSON) "+sql, args...).Scan(&plan); err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	return string(plan)
}

// seedJobs popola `jobs` con un catalogo realistico: quasi tutti i job hanno una
// prossima occorrenza nel futuro, una manciata è dovuta adesso. È lo stato
// normale di un motore in esercizio — la maggior parte del catalogo non ha
// niente da fare in questo istante — ed è proprio quello in cui una scansione
// completa costerebbe di più.
func seedJobs(t *testing.T, pool *pgxpool.Pool, userID string) {
	t.Helper()

	if _, err := pool.Exec(t.Context(),
		`INSERT INTO jobs (user_id, name, every_seconds, url, next_run_at)
		 SELECT $1::uuid, 'carico-' || g, 60, 'https://example.com/hook',
		        now() + (g || ' seconds')::interval
		   FROM generate_series(1, $2) AS g`, userID, jobCount); err != nil {
		t.Fatalf("popolamento di jobs: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `ANALYZE jobs`); err != nil {
		t.Fatalf("ANALYZE jobs: %v", err)
	}
}

func TestIlPianoDellaQueryCaldaUsaJobsDueIdx(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)
	seedJobs(t, pool, user)

	plan := explain(t, pool, scheduler.DueJobsSQL, time.Now().UTC(), 200)

	if !strings.Contains(plan, "jobs_due_idx") {
		t.Fatalf("il piano non usa jobs_due_idx su %d job:\n%s", jobCount, plan)
	}
	if strings.Contains(plan, "Seq Scan") {
		t.Fatalf("il piano contiene una scansione sequenziale:\n%s", plan)
	}
	// L'ordinamento dev'essere quello dell'indice, non un Sort a valle: con
	// `LIMIT` un Sort costringerebbe a leggere tutte le righe candidate prima di
	// scartarne la maggior parte.
	if strings.Contains(plan, `"Node Type": "Sort"`) {
		t.Fatalf("il piano ordina a valle invece di seguire l'indice:\n%s", plan)
	}
}

func TestIlPianoDeiJobSenzaProssimaOccorrenzaUsaIlSuoIndice(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)
	seedJobs(t, pool, user)

	// Un pugno di job appena creati, senza prossima occorrenza: è tutto ciò che
	// l'indice parziale della 0010 deve contenere.
	for range 3 {
		createJob(t, pool, user, jobSpec{Every: time.Minute})
	}
	if _, err := pool.Exec(t.Context(), `ANALYZE jobs`); err != nil {
		t.Fatalf("ANALYZE jobs: %v", err)
	}

	plan := explain(t, pool, scheduler.UnscheduledJobsSQL, 200)

	if !strings.Contains(plan, "jobs_unscheduled_idx") {
		t.Fatalf("il piano non usa jobs_unscheduled_idx su %d job:\n%s", jobCount, plan)
	}
	if strings.Contains(plan, "Seq Scan") {
		t.Fatalf("il piano contiene una scansione sequenziale:\n%s", plan)
	}
}

func TestIlPianoDelRecuperoUsaLIndiceDelleEsecuzioniInVolo(t *testing.T) {
	pool := newTestDatabase(t)
	user := createUser(t, pool)
	jobID := createJob(t, pool, user, jobSpec{Every: time.Second})

	// Una giornata di esecuzioni concluse, e due sole rimaste in sospeso: è la
	// proporzione reale, ed è il motivo per cui `job_executions_in_flight_idx`
	// è parziale.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment, status, started_at, finished_at)
		 SELECT $1::uuid,
		        date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' + (g || ' seconds')::interval,
		        'production', 'succeeded', now(), now()
		   FROM generate_series(1, $2) AS g`, jobID, executionCount); err != nil {
		t.Fatalf("popolamento di job_executions: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `ANALYZE job_executions`); err != nil {
		t.Fatalf("ANALYZE job_executions: %v", err)
	}

	now := time.Now().UTC()
	plan := explain(t, pool, scheduler.PendingOccurrencesSQL,
		now.Add(-48*time.Hour), now, now, 200)

	// Le partizioni vuote dei giorni vicini vengono scandite sequenzialmente, ed
	// è giusto così: su zero righe l'indice costerebbe di più. Quella che conta è
	// la partizione piena, e su quella il piano deve passare dall'indice.
	//
	// Il nome che compare nel piano è quello dell'indice **della partizione**,
	// non `job_executions_in_flight_idx`: un indice su tabella partizionata è
	// solo il padre di un indice per partizione, con un nome generato. È la
	// stessa lezione del vincolo violato in TestConflittoDiIdempotenzaPortaIlNomeDellaPartizione,
	// e vale la pena vederla anche da questo lato.
	children := partitionIndexes(t, pool, "job_executions_in_flight_idx")
	if len(children) == 0 {
		t.Fatal("nessun indice di partizione discende da job_executions_in_flight_idx")
	}
	used := false
	for _, name := range children {
		if strings.Contains(plan, name) {
			used = true
			break
		}
	}
	if !used {
		t.Fatalf("il piano non usa nessuno degli indici %v:\n%s", children, plan)
	}
}

// partitionIndexes elenca gli indici di partizione che discendono da un indice
// dichiarato sulla tabella padre.
func partitionIndexes(t *testing.T, pool *pgxpool.Pool, parent string) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(),
		`SELECT c.relname
		   FROM pg_class c
		   JOIN pg_inherits i ON i.inhrelid = c.oid
		  WHERE i.inhparent = $1::regclass`, parent)
	if err != nil {
		t.Fatalf("lettura degli indici di partizione: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("lettura di un indice di partizione: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("lettura degli indici di partizione: %v", err)
	}
	return out
}
