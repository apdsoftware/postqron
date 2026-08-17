package jobspg_test

import (
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Le due colonne della 0013 attraversano lo store, e il giro d'andata e ritorno
// è l'unico modo di sapere che `jobColumns` e `scanJob` sono rimasti allineati:
// una colonna aggiunta in una sola delle quattro query è un errore che il
// compilatore non vede.
func TestLaSospensioneAttraversaLoStore(t *testing.T) {
	store, userID, pool := newStore(t)

	creato, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("creazione: %v", err)
	}
	if creato.Suspended() {
		t.Fatal("un job appena creato non è sospeso")
	}

	// La sospensione la scrive internal/billingpg, con l'istruzione di R58: qui
	// si prova solo che lo store la sappia leggere e sciogliere.
	quando := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET enabled = false, suspended_at = $2,
		        suspended_reason = 'plan_job_limit', next_run_at = NULL
		  WHERE id = $1::uuid`, creato.ID, quando); err != nil {
		t.Fatalf("sospensione: %v", err)
	}

	letto, err := store.JobByID(t.Context(), userID, creato.ID)
	if err != nil {
		t.Fatalf("lettura: %v", err)
	}
	switch {
	case !letto.Suspended():
		t.Fatal("sospensione non letta")
	case letto.SuspendedReason != jobs.SuspendedByJobLimit:
		t.Fatalf("motivo = %q", letto.SuspendedReason)
	case letto.Enabled:
		t.Fatal("un job sospeso è anche spento: il motore non deve continuare a eseguirlo")
	}

	// La riaccensione scioglie la sospensione, e lo fa **nella query**: un job
	// funzionante che l'interfaccia continuasse a mostrare come fermo sarebbe
	// peggio di nessun messaggio.
	letto.Enabled = true
	riacceso, err := store.UpdateJob(t.Context(), letto, true)
	if err != nil {
		t.Fatalf("riaccensione: %v", err)
	}
	if riacceso.Suspended() || riacceso.SuspendedReason != "" {
		t.Fatalf("job riacceso ma ancora sospeso: %+v", riacceso)
	}
}

// Una modifica che **non** riaccende non deve cancellare la sospensione: un job
// sospeso resta modificabile ed esportabile (R58), e correggerne l'URL non è
// un modo per farlo tornare acceso di nascosto.
func TestModificareUnJobSospesoNonLoSblocca(t *testing.T) {
	store, userID, pool := newStore(t)

	creato, err := store.CreateJob(t.Context(), sampleJob(userID), nil)
	if err != nil {
		t.Fatalf("creazione: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET enabled = false, suspended_at = now(),
		        suspended_reason = 'plan_resolution'
		  WHERE id = $1::uuid`, creato.ID); err != nil {
		t.Fatalf("sospensione: %v", err)
	}

	sospeso, err := store.JobByID(t.Context(), userID, creato.ID)
	if err != nil {
		t.Fatalf("lettura: %v", err)
	}
	sospeso.URL = "https://esempio.test/nuovo"
	aggiornato, err := store.UpdateJob(t.Context(), sospeso, false)
	if err != nil {
		t.Fatalf("modifica: %v", err)
	}
	switch {
	case aggiornato.URL != "https://esempio.test/nuovo":
		t.Errorf("modifica non applicata: %q", aggiornato.URL)
	case !aggiornato.Suspended():
		t.Error("la sospensione è stata sciolta da una modifica che non riaccende")
	case aggiornato.SuspendedReason != jobs.SuspendedByResolution:
		t.Errorf("motivo = %q", aggiornato.SuspendedReason)
	}
}

// Il conteggio della capacità è distinto da quello del catalogo: dopo un
// downgrade il secondo è già oltre il limite, e usarlo per la riaccensione
// impedirebbe all'utente di riaccendere anche un solo job.
func TestConteggioDeiJobAttivi(t *testing.T) {
	store, userID, pool := newStore(t)

	for i := range 4 {
		job := sampleJob(userID)
		job.Name = "job-" + string(rune('a'+i))
		if _, err := store.CreateJob(t.Context(), job, nil); err != nil {
			t.Fatalf("creazione: %v", err)
		}
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET enabled = false WHERE user_id = $1::uuid AND name IN ('job-a', 'job-b')`,
		userID); err != nil {
		t.Fatalf("pausa: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`UPDATE jobs SET archived_at = now() WHERE user_id = $1::uuid AND name = 'job-c'`,
		userID); err != nil {
		t.Fatalf("archiviazione: %v", err)
	}

	catalogo, err := store.CountJobs(t.Context(), userID)
	if err != nil {
		t.Fatalf("conteggio del catalogo: %v", err)
	}
	attivi, err := store.CountActiveJobs(t.Context(), userID)
	if err != nil {
		t.Fatalf("conteggio degli attivi: %v", err)
	}

	// Tre non archiviati, uno solo acceso.
	if catalogo != 3 {
		t.Errorf("catalogo = %d, atteso 3", catalogo)
	}
	if attivi != 1 {
		t.Errorf("attivi = %d, atteso 1", attivi)
	}
}
