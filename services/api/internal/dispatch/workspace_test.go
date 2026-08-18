package dispatch_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// I test del tetto tecnico per workspace (R10).
//
// La difesa ha due facce e vanno provate tutte e due, perché una sola è
// soddisfacibile sbagliando: un tetto che *ferma* il workspace in eccesso ma
// ferma anche gli altri non è una difesa, è un guasto distribuito
// equamente.

// occorrenzaDi costruisce un'occorrenza di un job che appartiene al workspace
// indicato, con `on_overlap: allow` — qui si misura il tetto del workspace, non
// la politica di R41.
func occorrenzaDi(workspace, jobID string, secondi int) scheduler.Occurrence {
	occ := fakeOccurrence(jobID, secondi)
	occ.Job.UserID = workspace
	return occ
}

// TestIlTettoPerWorkspaceLimitaLeEsecuzioniContemporanee.
//
// Un workspace con più job di quanti il tetto ne ammetta: ne partono esattamente
// quante il tetto concede, e le altre **restano in coda**. Le occorrenze
// schedulate non si rifiutano — il tetto è di capienza istantanea, non di
// ammissione — e la coda è il posto in cui aspettano.
func TestIlTettoPerWorkspaceLimitaLeEsecuzioniContemporanee(t *testing.T) {
	const (
		tetto = 3
		job   = 8
	)

	store := newMemStore()
	conta := nuovaConcorrenza()
	sblocca := make(chan struct{})

	// Un esecutore che conta sul workspace invece che sulla corsia: la grandezza
	// che il tetto limita è quella.
	exec := dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		conta.entra(occ.Job.UserID)
		defer conta.esce(occ.Job.UserID)
		select {
		case <-sblocca:
		case <-ctx.Done():
			return dispatch.Result{}, ctx.Err()
		}
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newPool(t, dispatch.Options{
		Store:                   store,
		Executor:                exec,
		Workers:                 16,
		MaxInFlightPerWorkspace: tetto,
		DrainTimeout:            time.Second,
	})

	// Job diversi, così il tetto per job non c'entra: quello che si osserva è
	// solo l'aggregato del workspace.
	for i := range job {
		occ := occorrenzaDi("acme", fmt.Sprintf("job-%d", i), 0)
		if err := hand(t, pool, store, occ); err != nil {
			t.Fatalf("occorrenza %d rifiutata: %v", i, err)
		}
	}

	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == tetto },
		"non sono partite %d esecuzioni: %+v", tetto, pool.Stats())

	// Un istante di assestamento: se il tetto non tenesse, il picco salirebbe
	// oltre proprio adesso, con dodici worker liberi e cinque occorrenze pronte.
	time.Sleep(100 * time.Millisecond)
	if picco, _ := conta.leggi("acme"); picco != tetto {
		t.Fatalf("picco di esecuzioni contemporanee del workspace = %d, atteso il tetto %d", picco, tetto)
	}

	// L'eccedenza aspetta, non viene rifiutata: la riga di un'occorrenza
	// schedulata non deve tornare `pending` per un tetto che si libera da sé.
	st := pool.Stats()
	if st.Queued != job-tetto {
		t.Fatalf("in attesa = %d, attese %d", st.Queued, job-tetto)
	}
	if st.Refused != 0 || st.Overlapped != 0 {
		t.Fatalf("stats = %+v: l'eccedenza doveva aspettare, non essere rifiutata", st)
	}

	// E il tetto è quello che il pool dichiara a chi lo interroga: è il numero
	// su cui il trigger manuale decide di rifiutare.
	used, limit := pool.WorkspaceInFlight("acme")
	if used != tetto || limit != tetto {
		t.Fatalf("WorkspaceInFlight = (%d, %d), atteso (%d, %d)", used, limit, tetto, tetto)
	}
	if used, limit := pool.WorkspaceInFlight("nessuno"); used != 0 || limit != tetto {
		t.Fatalf("WorkspaceInFlight di un workspace fermo = (%d, %d), atteso (0, %d)", used, limit, tetto)
	}

	close(sblocca)
	eventually(t, 10*time.Second, func() bool { return pool.Stats().Succeeded == job },
		"l'arretrato del workspace non è stato smaltito: %+v", pool.Stats())
}

// TestUnWorkspaceAlTettoNonRallentaGliAltri è **il punto** della difesa.
//
// Un workspace saturo di job che non finiscono mai, e un altro che ha lavoro
// rapido. Se il tetto si applicasse mettendo i worker in attesa invece di
// saltare il workspace pieno, il secondo non passerebbe mai: il test non
// diventerebbe lento, andrebbe in timeout. Non c'è nessuna soglia temporale da
// tarare — o i job dell'altro workspace passano mentre il primo è fermo al
// tetto, o non passano affatto.
func TestUnWorkspaceAlTettoNonRallentaGliAltri(t *testing.T) {
	const (
		tetto        = 2
		occupanti    = 20
		lavoroAltrui = 300
	)

	store := newMemStore()
	sblocca := make(chan struct{})
	var altrui sync.WaitGroup
	altrui.Add(lavoroAltrui)

	exec := dispatch.ExecutorFunc(func(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
		if occ.Job.UserID == "ingombrante" {
			select {
			case <-sblocca:
			case <-ctx.Done():
				return dispatch.Result{}, ctx.Err()
			}
			return dispatch.Result{ResponseStatus: 200}, nil
		}
		altrui.Done()
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newPool(t, dispatch.Options{
		Store:                   store,
		Executor:                exec,
		Workers:                 8,
		MaxInFlightPerWorkspace: tetto,
		QueueDepth:              4096,
		DrainTimeout:            time.Second,
	})

	// Prima il workspace ingombrante, e in quantità: con un tetto applicato
	// aspettando, i suoi job sarebbero in testa a tutte le code e i worker
	// resterebbero fermi lì.
	for i := range occupanti {
		occ := occorrenzaDi("ingombrante", fmt.Sprintf("pesante-%d", i), 0)
		if err := hand(t, pool, store, occ); err != nil {
			t.Fatalf("occorrenza ingombrante %d: %v", i, err)
		}
	}
	for i := range lavoroAltrui {
		// Ogni occorrenza da un workspace diverso: è il caso reale di una VPS
		// condivisa da molti clienti, non di due.
		occ := occorrenzaDi(fmt.Sprintf("cliente-%d", i), fmt.Sprintf("rapido-%d", i), 0)
		if err := hand(t, pool, store, occ); err != nil {
			t.Fatalf("occorrenza altrui %d: %v", i, err)
		}
	}

	fine := make(chan struct{})
	go func() { altrui.Wait(); close(fine) }()
	select {
	case <-fine:
	case <-time.After(30 * time.Second):
		t.Fatalf("gli altri workspace non sono passati mentre uno solo era fermo al proprio tetto: %+v",
			pool.Stats())
	}

	// E il workspace ingombrante è rimasto esattamente al proprio tetto per tutto
	// il tempo: né di più, né bloccato del tutto.
	if used, _ := pool.WorkspaceInFlight("ingombrante"); used != tetto {
		t.Fatalf("il workspace ingombrante ha %d esecuzioni in volo, il tetto è %d", used, tetto)
	}

	close(sblocca)
}

// TestIlTettoPerWorkspaceNonSuperaIlPool: sopra il numero di worker non sarebbe
// più un tetto, sarebbe un numero scritto.
func TestIlTettoPerWorkspaceNonSuperaIlPool(t *testing.T) {
	pool, err := dispatch.New(dispatch.Options{
		Store:                   newMemStore(),
		Executor:                dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) { return dispatch.Result{}, nil }),
		Workers:                 2,
		MaxInFlightPerWorkspace: 100,
	})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	if _, limit := pool.WorkspaceInFlight("chiunque"); limit != 2 {
		t.Fatalf("tetto per workspace = %d su un pool da 2 worker, atteso 2", limit)
	}
}

// TestIDueTettiSiCompongonoEVinceIlPiuStretto.
//
// Il tetto per job e quello per workspace misurano cose diverse — uno protegge
// gli altri job dello stesso cliente, l'altro protegge la macchina da un
// cliente — e non c'è nessuna ragione per cui l'aggregato debba essere più largo
// del dettaglio. Quando l'aggregato è più stretto, è quello che si sente: il
// tetto per job resta la regola che decide *quale* job usa quanto di ciò che il
// workspace ha.
func TestIDueTettiSiCompongonoEVinceIlPiuStretto(t *testing.T) {
	store := newMemStore()
	conta := nuovaConcorrenza()
	sblocca := make(chan struct{})

	pool := newPool(t, dispatch.Options{
		Store:                   store,
		Executor:                esecutoreBloccante(conta, sblocca),
		Workers:                 16,
		MaxInFlightPerJob:       4,
		MaxInFlightPerWorkspace: 2,
		QueueDepthPerJob:        64,
		DrainTimeout:            time.Second,
	})

	// Un job solo, che da solo potrebbe tenerne quattro: il tetto del suo
	// workspace lo ferma a due.
	for i := range 10 {
		if err := hand(t, pool, store, occorrenzaDi("acme", "unico", i)); err != nil {
			t.Fatalf("occorrenza %d: %v", i, err)
		}
	}

	eventually(t, 5*time.Second, func() bool { return pool.Stats().InFlight == 2 },
		"non sono partite due esecuzioni: %+v", pool.Stats())
	time.Sleep(100 * time.Millisecond)
	if picco, _ := conta.leggi("unico/production"); picco != 2 {
		t.Fatalf("picco = %d: il tetto per workspace (2) è più stretto di quello per job (4) e deve vincere", picco)
	}

	close(sblocca)
	eventually(t, 10*time.Second, func() bool { return pool.Stats().Succeeded == 10 },
		"l'arretrato non è stato smaltito: %+v", pool.Stats())
}

// TestIlTettoPerWorkspaceLasciaTraccia.
//
// Il tetto di R10 si applica saltando il job in [queue.pick]: senza un
// contatore, l'unico modo di sapere che ha morso sarebbe leggere il codice. La
// misura è quella che dice se il numero scelto da #457 è quello giusto — e
// [dispatch.DefaultMaxInFlightPerWorkspace] dichiara di sé che il valore giusto
// si conosce solo osservandolo.
func TestIlTettoPerWorkspaceLasciaTraccia(t *testing.T) {
	const tetto = 2

	store := newMemStore()
	sblocca := make(chan struct{})
	t.Cleanup(func() { close(sblocca) })

	exec := dispatch.ExecutorFunc(func(ctx context.Context, _ scheduler.Occurrence) (dispatch.Result, error) {
		select {
		case <-sblocca:
		case <-ctx.Done():
			return dispatch.Result{}, ctx.Err()
		}
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newPool(t, dispatch.Options{
		Store:                   store,
		Executor:                exec,
		Workers:                 8,
		MaxInFlightPerWorkspace: tetto,
		DrainTimeout:            time.Second,
	})

	// Quattro job dello stesso workspace: due partono, due restano in coda con i
	// worker liberi davanti. È quella l'attesa che va contata.
	for i := range 4 {
		if err := hand(t, pool, store, occorrenzaDi("acme", fmt.Sprintf("job-%d", i), 0)); err != nil {
			t.Fatalf("occorrenza %d rifiutata: %v", i, err)
		}
	}

	eventually(t, 5*time.Second, func() bool { return pool.Stats().WorkspaceStalls > 0 },
		"il tetto per workspace non ha lasciato traccia: %+v", pool.Stats())

	st := pool.Stats()
	if st.InFlight != tetto {
		t.Errorf("in volo = %d, atteso il tetto %d", st.InFlight, tetto)
	}
	if st.Queued != 4-tetto {
		t.Errorf("in attesa = %d, attese %d", st.Queued, 4-tetto)
	}
}

// TestSenzaTettoNessunoStallo verifica che il contatore non si muova quando il
// tetto non c'entra: un contatore che sale sempre non distingue niente.
func TestSenzaTettoNessunoStallo(t *testing.T) {
	store := newMemStore()
	exec := dispatch.ExecutorFunc(func(context.Context, scheduler.Occurrence) (dispatch.Result, error) {
		return dispatch.Result{ResponseStatus: 200}, nil
	})

	pool := newPool(t, dispatch.Options{
		Store: store, Executor: exec, Workers: 8, MaxInFlightPerWorkspace: 8,
	})

	for i := range 4 {
		if err := hand(t, pool, store, occorrenzaDi("acme", fmt.Sprintf("job-%d", i), 0)); err != nil {
			t.Fatalf("occorrenza %d rifiutata: %v", i, err)
		}
	}
	eventually(t, 5*time.Second, func() bool { return pool.Stats().Succeeded == 4 },
		"non sono finite tutte: %+v", pool.Stats())

	if st := pool.Stats(); st.WorkspaceStalls != 0 {
		t.Errorf("stalli per workspace = %d, attesi 0 con il tetto largo", st.WorkspaceStalls)
	}
}
