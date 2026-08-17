package retention_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/retention"
)

// TestRunCancellaSubitoEPoiSiFermaAlContesto copre il ciclo, che è la parte per
// cui questa issue esiste: le funzioni della 0006 c'erano già, quello che
// mancava era qualcuno che le chiamasse.
//
// Due cose insieme. La **prima passata è immediata**, non alla scadenza del
// primo intervallo: con un intervallo di un'ora, aspettarla significherebbe che
// un processo riavviato spesso non cancella mai niente. E il ciclo **esce alla
// chiusura del contesto**, restituendo l'errore del contesto: è così che
// l'arresto del processo lo raccoglie senza scriverlo nei log come un guasto.
func TestRunCancellaSubitoEPoiSiFermaAlContesto(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	free := newJob(t, pool, newUser(t, pool, ""))
	insertExecution(t, pool, free, time.Now().UTC().Add(-10*day), 1)

	// Intervallo lungo: se la prima passata non fosse immediata, questo test
	// scadrebbe invece di passare.
	svc := newService(t, pool, retention.Options{Interval: time.Hour})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stopped := make(chan error, 1)
	go func() { stopped <- svc.Run(ctx) }()

	deadline := time.After(10 * time.Second)
	for countExecutions(t, pool, free) != 0 {
		select {
		case <-deadline:
			t.Fatal("la prima passata non è arrivata entro dieci secondi")
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run ha restituito %v, atteso context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run non è uscito alla chiusura del contesto")
	}
}
