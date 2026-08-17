package dispatch_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
)

// I test di questo file girano contro PostgreSQL vero, e non per abitudine: la
// transizione da `pending` a `running` è atomica perché lo è un UPDATE, e un
// finto store in memoria proverebbe soltanto che il finto store è scritto bene.

func newStore(t *testing.T) (*dispatch.PostgresStore, *pgxpool.Pool, string) {
	t.Helper()
	pool := newTestDatabase(t)
	user := createUser(t, pool)
	job := createJob(t, pool, user, jobSpec{Enabled: true})
	return dispatch.NewPostgresStore(pool), pool, job
}

// Il secondo cancello di R4. Otto pretendenti sulla stessa occorrenza, una sola
// presa: senza `AND status = 'pending'` sarebbero otto chiamate al bersaglio
// dell'utente.
func TestLaPresaRiesceUnaVoltaSola(t *testing.T) {
	store, pool, job := newStore(t)
	occ := newOccurrence(t, pool, job, time.Now().UTC().Truncate(time.Second))

	const contenders = 8
	var (
		start sync.WaitGroup
		done  sync.WaitGroup
		mu    sync.Mutex
		won   int
	)
	start.Add(1)
	done.Add(contenders)

	for range contenders {
		go func() {
			defer done.Done()
			start.Wait()
			ok, err := store.Claim(context.Background(), occ)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("presa: %v", err)
				return
			}
			if ok {
				won++
			}
		}()
	}
	start.Done()
	done.Wait()

	if won != 1 {
		t.Fatalf("la presa è riuscita %d volte, doveva riuscire una sola", won)
	}
	row := readExecution(t, pool, occ)
	if row.Status != "running" {
		t.Fatalf("atteso lo stato running, trovato %q", row.Status)
	}
	if row.StartedAt == nil {
		t.Fatal("la presa non ha scritto l'istante di inizio")
	}
}

// Lo scheduler chiude come `skipped` le occorrenze uscite dalla finestra di
// recupero. Una di quelle non è più eseguibile, e il cancello se ne accorge
// senza sapere niente di quella decisione.
func TestUnOccorrenzaGiaChiusaNonSiPuoPrendere(t *testing.T) {
	store, pool, job := newStore(t)
	occ := newOccurrence(t, pool, job, time.Now().UTC().Truncate(time.Second))

	if _, err := pool.Exec(t.Context(),
		`UPDATE job_executions SET status = 'skipped' WHERE job_id = $1::uuid`, job); err != nil {
		t.Fatalf("chiusura dell'occorrenza: %v", err)
	}

	ok, err := store.Claim(t.Context(), occ)
	if err != nil {
		t.Fatalf("presa: %v", err)
	}
	if ok {
		t.Fatal("un'occorrenza già chiusa è stata presa")
	}
}

// L'esito si scrive solo su una riga che questo pool ha preso: è la condizione
// speculare a quella della presa, e regge il caso in cui l'arresto ha rilasciato
// la riga mentre l'esecuzione era ancora in corso.
func TestLEsitoSiScriveSoloSuUnaRigaInEsecuzione(t *testing.T) {
	store, pool, job := newStore(t)
	occ := newOccurrence(t, pool, job, time.Now().UTC().Truncate(time.Second))

	rec := dispatch.Record{Outcome: dispatch.Succeeded, ResponseStatus: 204, ResponseExcerpt: estratto("ok")}

	updated, err := store.Finish(t.Context(), occ, rec)
	if err != nil {
		t.Fatalf("esito su riga in attesa: %v", err)
	}
	if updated {
		t.Fatal("l'esito è stato scritto su una riga mai presa")
	}

	if ok, err := store.Claim(t.Context(), occ); err != nil || !ok {
		t.Fatalf("presa: %v, %v", ok, err)
	}
	// Il tempo di un'esecuzione, perché la durata generata sia osservabile.
	time.Sleep(5 * time.Millisecond)

	updated, err = store.Finish(t.Context(), occ, rec)
	if err != nil {
		t.Fatalf("esito: %v", err)
	}
	if !updated {
		t.Fatal("l'esito non è stato scritto su una riga presa da noi")
	}

	row := readExecution(t, pool, occ)
	if row.Status != "succeeded" {
		t.Fatalf("atteso lo stato succeeded, trovato %q", row.Status)
	}
	if row.FinishedAt == nil || row.StartedAt == nil {
		t.Fatal("uno stato terminale senza istanti non dice quando è successo")
	}
	if row.DurationMS == nil {
		t.Fatal("la durata generata non è stata calcolata")
	}
	if row.ResponseStatus == nil || *row.ResponseStatus != 204 {
		t.Fatalf("status della risposta non registrato: %v", row.ResponseStatus)
	}
}

// Il rilascio dell'arresto: la riga torna dov'era, e l'istante di inizio del
// tentativo interrotto sparisce con lui.
func TestIlRilascioRiportaLaRigaInAttesa(t *testing.T) {
	store, pool, job := newStore(t)
	occ := newOccurrence(t, pool, job, time.Now().UTC().Truncate(time.Second))

	if ok, err := store.Claim(t.Context(), occ); err != nil || !ok {
		t.Fatalf("presa: %v, %v", ok, err)
	}
	released, err := store.Release(t.Context(), occ)
	if err != nil {
		t.Fatalf("rilascio: %v", err)
	}
	if !released {
		t.Fatal("il rilascio non ha toccato la riga")
	}

	row := readExecution(t, pool, occ)
	if row.Status != "pending" {
		t.Fatalf("atteso lo stato pending, trovato %q", row.Status)
	}
	if row.StartedAt != nil {
		t.Fatalf("l'istante di inizio di un tentativo che non c'è più: %v", row.StartedAt)
	}

	// Un rilascio su una riga già in attesa non fa niente: due arresti
	// sovrapposti non devono azzerare il lavoro di chi l'ha ripresa nel
	// frattempo.
	if released, err := store.Release(t.Context(), occ); err != nil || released {
		t.Fatalf("secondo rilascio: %v, %v", released, err)
	}
}

func TestLoScartoSiApplicaSoloAUnOccorrenzaMaiPartita(t *testing.T) {
	store, pool, job := newStore(t)
	occ := newOccurrence(t, pool, job, time.Now().UTC().Truncate(time.Second))

	skipped, err := store.Skip(t.Context(), occ, "job in pausa")
	if err != nil || !skipped {
		t.Fatalf("scarto: %v, %v", skipped, err)
	}

	row := readExecution(t, pool, occ)
	if row.Status != "skipped" {
		t.Fatalf("atteso lo stato skipped, trovato %q", row.Status)
	}
	if row.StartedAt != nil || row.FinishedAt != nil {
		t.Fatal("un'occorrenza mai partita non ha istanti di esecuzione")
	}
	if row.Error == nil || !strings.Contains(*row.Error, "pausa") {
		t.Fatalf("il motivo dello scarto non è stato registrato: %v", row.Error)
	}
}

// Il limite di 8 KiB su `response_excerpt` è un CHECK dello schema: superarlo
// farebbe fallire la scrittura dell'esito, cioè perderebbe il fatto che
// l'esecuzione è avvenuta per colpa di una risposta troppo lunga.
func TestUnaRispostaTroppoLungaNonFaPerdereLEsito(t *testing.T) {
	store, pool, job := newStore(t)
	occ := newOccurrence(t, pool, job, time.Now().UTC().Truncate(time.Second))

	if ok, err := store.Claim(t.Context(), occ); err != nil || !ok {
		t.Fatalf("presa: %v, %v", ok, err)
	}
	long := strings.Repeat("à", 20_000)
	if _, err := store.Finish(t.Context(), occ, dispatch.Record{
		Outcome:         dispatch.Failed,
		ResponseStatus:  500,
		ResponseExcerpt: estratto(long),
		Error:           estratto(long),
	}); err != nil {
		t.Fatalf("esito con risposta lunga: %v", err)
	}

	var excerpt, errText int
	if err := pool.QueryRow(t.Context(),
		`SELECT char_length(response_excerpt), char_length(error)
		   FROM job_executions WHERE job_id = $1::uuid`, job).Scan(&excerpt, &errText); err != nil {
		t.Fatalf("lettura delle lunghezze: %v", err)
	}
	if excerpt != dispatch.MaxTextLength || errText != dispatch.MaxTextLength {
		t.Fatalf("testi non troncati al limite: estratto %d, errore %d", excerpt, errText)
	}
}

// Uno status fuori dall'intervallo del CHECK vale «nessuna risposta»: si perde
// lo status, non l'esecuzione.
func TestUnoStatusFuoriIntervalloNonFaPerdereLEsito(t *testing.T) {
	store, pool, job := newStore(t)
	occ := newOccurrence(t, pool, job, time.Now().UTC().Truncate(time.Second))

	if ok, err := store.Claim(t.Context(), occ); err != nil || !ok {
		t.Fatalf("presa: %v, %v", ok, err)
	}
	if _, err := store.Finish(t.Context(), occ, dispatch.Record{
		Outcome: dispatch.Failed, ResponseStatus: 0, Error: estratto("connessione rifiutata"),
	}); err != nil {
		t.Fatalf("esito senza risposta: %v", err)
	}

	row := readExecution(t, pool, occ)
	if row.Status != "failed" {
		t.Fatalf("atteso lo stato failed, trovato %q", row.Status)
	}
	if row.ResponseStatus != nil {
		t.Fatalf("status della risposta inventato: %v", *row.ResponseStatus)
	}
}

// Che la chiave primaria esista lo dice la migrazione; che il pianificatore la
// usi per indirizzare la riga lo dice solo un EXPLAIN. Questi UPDATE girano una
// volta per esecuzione, cioè 86.400 volte al giorno per un job a un secondo: una
// scansione qui non sarebbe un dettaglio.
func TestIlPianoDelleTransizioniUsaLaChiavePrimaria(t *testing.T) {
	_, pool, job := newStore(t)
	occ := newOccurrence(t, pool, job, time.Now().UTC().Truncate(time.Second))

	// Una giornata di esecuzioni concluse: su una tabella vuota il pianificatore
	// sceglierebbe comunque la scansione, e il test non distinguerebbe un indice
	// che serve da uno che non c'è.
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment, status, started_at, finished_at)
		 SELECT $1::uuid,
		        date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' + (g || ' milliseconds')::interval,
		        'production', 'succeeded', now(), now()
		   FROM generate_series(1, 50000) AS g`, job); err != nil {
		t.Fatalf("popolamento di job_executions: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `ANALYZE job_executions`); err != nil {
		t.Fatalf("ANALYZE: %v", err)
	}

	key := []any{occ.Job.ID, occ.ScheduledFor, occ.Environment, occ.Attempt}
	statements := []struct {
		name string
		sql  string
		args []any
	}{
		{"presa", dispatch.ClaimSQL, key},
		{"esito", dispatch.FinishSQL, append(append([]any{}, key...), "succeeded", nil, nil, nil)},
		{"scarto", dispatch.SkipSQL, append(append([]any{}, key...), "motivo")},
		{"rilascio", dispatch.ReleaseSQL, key},
	}

	for _, st := range statements {
		var plan []byte
		if err := pool.QueryRow(t.Context(), "EXPLAIN (FORMAT JSON) "+st.sql, st.args...).Scan(&plan); err != nil {
			t.Fatalf("EXPLAIN della %s: %v", st.name, err)
		}
		if strings.Contains(string(plan), "Seq Scan") {
			t.Fatalf("il piano della %s contiene una scansione sequenziale:\n%s", st.name, plan)
		}
		if !strings.Contains(string(plan), "Index Scan") {
			t.Fatalf("il piano della %s non passa dall'indice:\n%s", st.name, plan)
		}
	}
}
