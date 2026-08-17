package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/retention"
)

// Questi test sono la parte della issue che non si può dedurre leggendo il
// codice: che la cancellazione non fermi il dispatch è un'affermazione sui lock
// di PostgreSQL, e va misurata contro PostgreSQL vero mentre qualcuno scrive.

// writer imita il motore: inserisce esecuzioni nella partizione di oggi finché
// non gli si dice di smettere, e riporta quanto ha atteso al peggio.
type writer struct {
	inserted int
	slowest  time.Duration
	err      error
}

// run scrive fino alla chiusura di stop. Non crea partizioni: usa quella di
// oggi, che esiste già, esattamente come farebbe il motore.
func (w *writer) run(ctx context.Context, pool *pgxpool.Pool, jobID string, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	// `scheduled_for` diverso per ogni riga, tutti dentro la giornata di oggi:
	// è la chiave naturale della 0006, e due righe non possono condividerla.
	base := time.Now().UTC().Truncate(time.Hour)
	for i := 0; ; i++ {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		default:
		}

		started := time.Now()
		_, err := pool.Exec(ctx,
			`INSERT INTO job_executions (job_id, scheduled_for, environment, status)
			 VALUES ($1::uuid, $2, 'production', 'pending')`,
			jobID, base.Add(time.Duration(i)*time.Millisecond))
		elapsed := time.Since(started)
		if err != nil {
			w.err = err
			return
		}
		w.inserted++
		if elapsed > w.slowest {
			w.slowest = elapsed
		}
	}
}

// TestLaCancellazioneNonFermaLeScrittureConcorrenti fa girare una passata di
// cancellazione a righe mentre qualcuno scrive sulla stessa tabella, che è la
// condizione d'esercizio: a un secondo di risoluzione il motore inserisce
// continuamente su `job_executions`.
//
// Ciò che il test verifica non è la velocità, è che nessuna scrittura venga
// rifiutata o messa in attesa lunga, e che a passata finita ci siano
// esattamente le righe che devono esserci: quelle nuove tutte, quelle scadute
// nessuna, quelle ancora dentro la finestra di un piano lungo intatte.
func TestLaCancellazioneNonFermaLeScrittureConcorrenti(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	// Un cliente Agency vivo alza il taglio delle partizioni a novanta giorni:
	// senza, il DROP porterebbe via la giornata in blocco e la cancellazione a
	// righe — che è quella sotto esame — non avrebbe niente da fare.
	agency := newJob(t, pool, newUser(t, pool, "agency"))
	insertExecution(t, pool, agency, time.Now().UTC().Add(-10*day), 1)

	free := newJob(t, pool, newUser(t, pool, "free"))
	const scadute = 2000
	vecchio := time.Now().UTC().Add(-10 * day)
	for i := 1; i <= scadute; i++ {
		insertExecution(t, pool, free, vecchio, i)
	}

	scrittore := newJob(t, pool, newUser(t, pool, "team"))
	var w writer
	stop := make(chan struct{})
	done := make(chan struct{})
	go w.run(t.Context(), pool, scrittore, stop, done)

	svc := newService(t, pool, retention.Options{Batch: 100, Pause: 2 * time.Millisecond})
	stats, err := svc.Sweep(t.Context())
	close(stop)
	<-done

	if err != nil {
		t.Fatalf("passata fallita: %v", err)
	}
	if w.err != nil {
		t.Fatalf("una scrittura concorrente è stata rifiutata durante la cancellazione: %v", w.err)
	}
	if w.inserted == 0 {
		t.Fatal("nessuna scrittura concorrente eseguita: il test non ha misurato niente")
	}
	t.Logf("scritture concorrenti: %d, attesa peggiore %s; righe cancellate: %d in %d lotti",
		w.inserted, w.slowest, stats.RowsDeleted, stats.Batches)

	// La soglia è larga di proposito: non misura la latenza di PostgreSQL, che
	// dipende dalla macchina, ma distingue «ha scritto mentre l'altro cancellava»
	// da «è rimasto fermo ad aspettare». Un DELETE unico su queste righe
	// produrrebbe attese nell'ordine dei secondi.
	if w.slowest > time.Second {
		t.Errorf("attesa peggiore di una scrittura = %s: la cancellazione sta bloccando il dispatch", w.slowest)
	}

	if stats.RowsDeleted != scadute {
		t.Errorf("righe cancellate = %d, attese %d", stats.RowsDeleted, scadute)
	}
	if stats.Batches < 2 {
		t.Errorf("lotti = %d: la cancellazione non è stata suddivisa", stats.Batches)
	}
	if n := countExecutions(t, pool, free); n != 0 {
		t.Errorf("righe scadute residue = %d, attese 0", n)
	}
	if n := countExecutions(t, pool, agency); n != 1 {
		t.Errorf("righe di Agency = %d, attesa 1: erano dentro la sua finestra", n)
	}
	if n := countExecutions(t, pool, scrittore); n != w.inserted {
		t.Errorf("righe scritte durante la passata = %d, ne risultano %d: la cancellazione ne ha portata via una nuova",
			w.inserted, n)
	}
}

// TestIlDropRinunciaInveceDiAccodareLeScritture è il test che giustifica
// `lock_timeout`, ed è nato da una misura.
//
// Eliminare una partizione richiede ACCESS EXCLUSIVE sulla **tabella padre**. Se
// una transazione di scrittura è aperta, il DROP si mette in coda; e siccome la
// coda dei lock di PostgreSQL è ordinata, ogni INSERT che arriva dopo si accoda
// dietro di lui — anche verso la partizione di oggi, che con quella vecchia non
// c'entra nulla. Misurato senza `lock_timeout`: sei secondi di attesa per un
// inserimento estraneo, con una sola transazione aperta.
//
// Con il timeout il DROP rinuncia e riprova alla passata dopo. La retention si
// misura in giorni; il dispatch in secondi.
func TestIlDropRinunciaInveceDiAccodareLeScritture(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	// Nessuna sottoscrizione: tutti su `free`, quindi la giornata di dieci
	// giorni fa è interamente scaduta e il DROP la vuole.
	free := newJob(t, pool, newUser(t, pool, ""))
	vecchio := time.Now().UTC().Add(-10 * day)
	insertExecution(t, pool, free, vecchio, 1)

	scrittore := newJob(t, pool, newUser(t, pool, ""))

	// La transazione che tiene il lock: scrive e non chiude. È un lettore lungo,
	// un backup, una migrazione — qualcosa che in esercizio capita.
	blocco, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("apertura della transazione bloccante: %v", err)
	}
	if _, err := blocco.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment, attempt, status)
		 VALUES ($1::uuid, now(), 'production', 1, 'pending')`, scrittore); err != nil {
		t.Fatalf("scrittura nella transazione bloccante: %v", err)
	}

	svc := newService(t, pool, retention.Options{LockTimeout: 500 * time.Millisecond})

	type esito struct {
		stats retention.Stats
		err   error
	}
	passata := make(chan esito, 1)
	go func() {
		stats, err := svc.Sweep(context.WithoutCancel(t.Context()))
		passata <- esito{stats, err}
	}()

	// Una scrittura indipendente, mentre il DROP sta aspettando: è quella che
	// senza `lock_timeout` restava ferma sei secondi.
	time.Sleep(100 * time.Millisecond)
	started := time.Now()
	_, insertErr := pool.Exec(t.Context(),
		`INSERT INTO job_executions (job_id, scheduled_for, environment, attempt, status)
		 VALUES ($1::uuid, now() + interval '1 minute', 'production', 1, 'pending')`, scrittore)
	attesa := time.Since(started)

	risultato := <-passata
	if err := blocco.Rollback(context.WithoutCancel(t.Context())); err != nil {
		t.Logf("chiusura della transazione bloccante: %v", err)
	}

	if insertErr != nil {
		t.Fatalf("la scrittura indipendente è fallita: %v", insertErr)
	}
	t.Logf("scrittura indipendente servita in %s durante la passata", attesa)
	if attesa > 2*time.Second {
		t.Errorf("la scrittura ha atteso %s: si è accodata dietro il DROP invece di passargli davanti", attesa)
	}

	if risultato.err != nil {
		t.Fatalf("la rinuncia al lock è stata riportata come guasto: %v", risultato.err)
	}
	if !risultato.stats.DropDeferred {
		t.Error("il DROP non ha dichiarato di aver rimandato")
	}
	if risultato.stats.PartitionsDropped != 0 {
		t.Errorf("partizioni eliminate = %d, attese 0: il lock era occupato", risultato.stats.PartitionsDropped)
	}
	if !hasPartition(t, pool, vecchio) {
		t.Error("la partizione è sparita nonostante il DROP avesse rinunciato")
	}

	// Rimandare non è rinunciare: chiusa la transazione, la passata successiva
	// porta via la giornata.
	stats, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("seconda passata fallita: %v", err)
	}
	if stats.DropDeferred {
		t.Fatal("il DROP ha rimandato di nuovo con il lock libero")
	}
	if stats.PartitionsDropped != 1 {
		t.Errorf("partizioni eliminate alla seconda passata = %d, attesa 1", stats.PartitionsDropped)
	}
	if hasPartition(t, pool, vecchio) {
		t.Error("la partizione scaduta esiste ancora dopo la seconda passata")
	}
}
