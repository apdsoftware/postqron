package migrate_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolation è il codice SQLSTATE che il motore userà per riconoscere
// «questa occorrenza l'ha già presa qualcun altro» e proseguire senza dispatch.
const uniqueViolation = "23505"

// insertOccurrence esegue l'inserimento di idempotenza: il motore lo fa *prima*
// di chiamare l'endpoint, e il suo esito decide se dispatchare (R4).
func insertOccurrence(ctx context.Context, pool *pgxpool.Pool, jobID string, occurrence time.Time, attempt int) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO job_executions (job_id, scheduled_for, environment, attempt)
		 VALUES ($1, $2, 'production', $3)`,
		jobID, occurrence, attempt)
	return err
}

func isDuplicate(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}

// R4 sotto contesa reale: più worker che corrono sulla stessa occorrenza devono
// produrre **una sola** esecuzione. È la garanzia su cui si regge la scelta di
// non avere una tabella di lock separata — il conflitto sulla chiave primaria
// *è* il lock.
func TestConcurrentWorkersClaimAnOccurrenceOnce(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	const workers = 24
	occurrence := time.Now().UTC().Truncate(time.Second)

	var claimed, duplicates atomic.Int64
	var unexpected []error
	var mu sync.Mutex

	// Il cancello fa partire tutti insieme: senza, le goroutine si
	// scaglionerebbero e la corsa non avverrebbe mai davvero.
	var gate sync.WaitGroup
	gate.Add(1)
	var done sync.WaitGroup

	for range workers {
		done.Add(1)
		go func() {
			defer done.Done()
			gate.Wait()
			switch err := insertOccurrence(ctx, fixture.pool, fixture.jobID, occurrence, 1); {
			case err == nil:
				claimed.Add(1)
			case isDuplicate(err):
				duplicates.Add(1)
			default:
				mu.Lock()
				unexpected = append(unexpected, err)
				mu.Unlock()
			}
		}()
	}
	gate.Done()
	done.Wait()

	if len(unexpected) > 0 {
		t.Fatalf("errori diversi dal conflitto atteso: %v", unexpected)
	}
	if claimed.Load() != 1 {
		t.Errorf("occorrenza presa da %d worker, atteso 1", claimed.Load())
	}
	if want := int64(workers - 1); duplicates.Load() != want {
		t.Errorf("%d conflitti, attesi %d", duplicates.Load(), want)
	}

	var rows int
	if err := fixture.pool.QueryRow(ctx,
		`SELECT count(*) FROM job_executions WHERE job_id = $1 AND scheduled_for = $2`,
		fixture.jobID, occurrence).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d righe per l'occorrenza, attesa 1", rows)
	}
}

// I worker perdenti devono ricevere un errore *riconoscibile* e uscire subito.
// Se il conflitto arrivasse come errore generico, il motore non saprebbe
// distinguerlo da un guasto e finirebbe per ritentare un'occorrenza già presa.
func TestLosingWorkerGetsARecognisableConflict(t *testing.T) {
	fixture := newSchemaFixture(t)
	ctx := t.Context()

	occurrence := time.Now().UTC().Truncate(time.Second)
	if err := insertOccurrence(ctx, fixture.pool, fixture.jobID, occurrence, 1); err != nil {
		t.Fatalf("primo inserimento: %v", err)
	}

	err := insertOccurrence(ctx, fixture.pool, fixture.jobID, occurrence, 1)
	if !isDuplicate(err) {
		t.Fatalf("errore = %v, atteso un conflitto %s sulla chiave primaria", err, uniqueViolation)
	}

	// Attenzione per chi implementerà il motore (issue 9): su una tabella
	// partizionata il vincolo violato è quello della **partizione**, quindi il
	// nome porta la data — `job_executions_20260817_pkey`, non
	// `job_executions_pkey`. Riconoscere il conflitto confrontando il nome con
	// una costante funzionerebbe in un test su tabella semplice e fallirebbe in
	// produzione ogni giorno diverso da quello in cui è stato scritto. Il
	// codice SQLSTATE, che è quello su cui ci si deve basare, non cambia.
	var pgErr *pgconn.PgError
	_ = errors.As(err, &pgErr)
	if !strings.HasPrefix(pgErr.ConstraintName, "job_executions_") ||
		!strings.HasSuffix(pgErr.ConstraintName, "_pkey") {
		t.Errorf("conflitto sul vincolo %q, atteso la chiave primaria di una partizione di job_executions",
			pgErr.ConstraintName)
	}
	if pgErr.ConstraintName == "job_executions_pkey" {
		t.Errorf("nome del vincolo inatteso: su tabella partizionata è quello della partizione, con la data")
	}
}

// BenchmarkOccurrenceInsert misura l'inserimento di idempotenza sotto
// concorrenza, che è il percorso caldo del motore: a 1 secondo di risoluzione
// gira 86.400 volte al giorno per job.
//
// Non gira in `make ci` — i benchmark non sono eseguiti da `go test` senza
// `-bench` — ma resta nel repository perché la misura sia ripetibile:
//
//	go test ./internal/migrate/ -run '^$' -bench OccurrenceInsert -benchtime 2000x
//
// Le due varianti isolano l'effetto della chiave primaria naturale sulla
// posizione degli inserimenti nel btree:
//
//   - `job-distinti` — ogni worker ha il suo job. La colonna di testa è un uuid
//     casuale, quindi gli inserimenti si distribuiscono su rami diversi
//     dell'indice: è il caso reale con più job attivi insieme.
//   - `job-condiviso` — tutti i worker inseriscono occorrenze consecutive dello
//     stesso job. Le chiavi sono monotone crescenti e cadono tutte sulla stessa
//     pagina di destra dell'indice: è il caso peggiore per la contesa, e serve a
//     misurare quanto costa la scelta di una chiave ordinata invece che casuale.
func BenchmarkOccurrenceInsert(b *testing.B) {
	for _, shared := range []bool{false, true} {
		name := "job-distinti"
		if shared {
			name = "job-condiviso"
		}
		b.Run(name, func(b *testing.B) {
			// Più connessioni dei worker paralleli: quello che si vuole misurare è
			// il database, non la coda sul pool.
			pool := newTestDatabaseWithConns(b, 24)
			applyAll(b, pool)
			ctx := b.Context()

			var userID string
			if err := pool.QueryRow(ctx,
				`INSERT INTO users (email) VALUES ('bench@example.com') RETURNING id`).
				Scan(&userID); err != nil {
				b.Fatalf("creazione dell'utente: %v", err)
			}

			// Un job per worker, o uno solo condiviso da tutti.
			const jobs = 16
			jobIDs := make([]string, jobs)
			for i := range jobIDs {
				if err := pool.QueryRow(ctx,
					`INSERT INTO jobs (user_id, name, every_seconds, url)
					 VALUES ($1, $2, 1, 'https://api.example.com/health') RETURNING id`,
					userID, fmt.Sprintf("bench-%02d", i)).Scan(&jobIDs[i]); err != nil {
					b.Fatalf("creazione del job: %v", err)
				}
			}

			base := time.Now().UTC().Truncate(time.Second)
			var worker atomic.Int64
			var offset atomic.Int64

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				index := int(worker.Add(1)-1) % jobs
				jobID := jobIDs[index]
				if shared {
					jobID = jobIDs[0]
				}
				for pb.Next() {
					// Occorrenze distinte: si misura l'inserimento, non il
					// costo di un conflitto.
					at := base.Add(time.Duration(offset.Add(1)) * time.Second)
					if err := insertOccurrence(ctx, pool, jobID, at, 1); err != nil {
						b.Fatalf("inserimento: %v", err)
					}
				}
			})
			b.StopTimer()

			var rows int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_executions`).Scan(&rows); err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(rows), "righe")
		})
	}
}
