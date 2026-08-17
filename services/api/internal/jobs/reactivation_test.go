package jobs_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// L'altra metà di R58: il downgrade sospende, e la riaccensione è dell'utente.
// Senza un tetto qui la regola resterebbe a metà — l'utente riaccenderebbe tutto
// ciò che aveva, e il piano non varrebbe niente.
//
// Il tetto da applicare è quello dei job **accesi**, non quello del catalogo:
// dopo un downgrade il catalogo è per costruzione già oltre il limite, ed è
// esattamente per questo che i due conteggi sono distinti.

// sospesi prepara un utente sceso al piano Free con `totale` job in catalogo,
// tutti spenti da R58 come li avrebbe lasciati internal/billing.
func sospesi(t *testing.T, b *banco, totale int, every time.Duration) []jobs.Job {
	t.Helper()
	quando := istanteDiProva.Add(-time.Hour)
	var out []jobs.Job
	for i := range totale {
		job := jobs.NewJob()
		job.UserID = utente
		job.Name = fmt.Sprintf("job-%02d", i)
		job.Every = every
		job.URL = "https://esempio.test/hook"
		job.Enabled = false
		job.SuspendedAt = &quando
		job.SuspendedReason = jobs.SuspendedByJobLimit
		out = append(out, b.store.Seed(job))
	}
	return out
}

func riaccendi(t *testing.T, b *banco, jobID string) (jobs.Job, error) {
	t.Helper()
	acceso := true
	return b.svc.Update(t.Context(), utente, jobID, jobs.Patch{Enabled: &acceso})
}

// Il caso che R58 promette per iscritto: cinquanta job sospesi su un piano che
// ne consente venti, e l'utente ne riaccende venti. Non uno, che è ciò che
// succederebbe applicando il tetto del catalogo.
func TestDopoIlDowngradeSiRiaccendeFinoAlTetto(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, piano(3))
	elenco := sospesi(t, b, 8, time.Hour)

	for i := range 3 {
		acceso, err := riaccendi(t, b, elenco[i].ID)
		if err != nil {
			t.Fatalf("riaccensione %d rifiutata: %v", i, err)
		}
		if !acceso.Enabled {
			t.Fatalf("job %d non riacceso", i)
		}
		// La sospensione si scioglie: un job funzionante che l'interfaccia
		// continuasse a mostrare come fermo sarebbe peggio di nessun messaggio.
		if acceso.Suspended() || acceso.SuspendedReason != "" {
			t.Fatalf("job %d riacceso ma ancora marcato come sospeso: %+v", i, acceso)
		}
	}

	_, err := riaccendi(t, b, elenco[3].ID)
	limite, ok := jobs.AsPlanLimit(err)
	if !ok {
		t.Fatalf("atteso un limite di piano, ottenuto %v", err)
	}
	if limite.Limit != jobs.LimitJobs {
		t.Fatalf("limite = %q, atteso %q", limite.Limit, jobs.LimitJobs)
	}

	// I job che restano spenti restano **sospesi**, non diventano una pausa
	// dell'utente: nulla viene cancellato e nulla viene riscritto.
	rimasto, err := b.svc.Get(t.Context(), utente, elenco[7].ID)
	if err != nil {
		t.Fatalf("lettura: %v", err)
	}
	if !rimasto.Suspended() || rimasto.SuspendedReason != jobs.SuspendedByJobLimit {
		t.Fatalf("job non riacceso = %+v", rimasto)
	}
}

// «Un job schedulato più fitto di quanto il nuovo piano consenta non può essere
// riacceso finché non ne cambia la schedulazione, **anche se c'è posto**.»
//
// E l'interfaccia deve dirlo: il rifiuto nomina il limite e il campo, così che
// un form possa indicare cosa correggere invece di lasciare l'utente a
// interpretare un messaggio.
func TestUnJobTroppoFittoNonSiRiaccendeNemmeneConPosto(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, piano(20))
	elenco := sospesi(t, b, 1, time.Second)

	_, err := riaccendi(t, b, elenco[0].ID)
	limite, ok := jobs.AsPlanLimit(err)
	if !ok {
		t.Fatalf("atteso un limite di piano, ottenuto %v", err)
	}
	switch {
	case limite.Limit != jobs.LimitResolution:
		t.Errorf("limite = %q, atteso %q", limite.Limit, jobs.LimitResolution)
	case limite.Field != "every":
		t.Errorf("campo = %q, atteso `every`", limite.Field)
	}

	// Il rimedio è cambiare la schedulazione, e allora il job si riaccende: la
	// stessa richiesta che allarga l'intervallo e riaccende deve passare.
	acceso, largo := true, 5*time.Minute
	riacceso, err := b.svc.Update(t.Context(), utente, elenco[0].ID,
		jobs.Patch{Enabled: &acceso, Every: &largo})
	if err != nil {
		t.Fatalf("riaccensione dopo il cambio di schedulazione: %v", err)
	}
	if !riacceso.Enabled || riacceso.Suspended() {
		t.Fatalf("job = %+v", riacceso)
	}
}

// Il tetto della riaccensione conta la capacità, non il catalogo. Con il tetto
// sbagliato questo test fallirebbe al primo tentativo, che è precisamente il
// difetto che R58 produrrebbe in produzione.
func TestIlTettoDellaRiaccensioneNonEQuelloDelCatalogo(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, piano(3))
	elenco := sospesi(t, b, 20, time.Hour)

	if _, err := riaccendi(t, b, elenco[0].ID); err != nil {
		t.Fatalf("con venti job in catalogo e nessuno acceso la prima riaccensione deve passare: %v", err)
	}

	// La creazione, invece, resta governata dal catalogo: il tetto è pieno e un
	// job nuovo non ci sta. Sono due domande diverse con due risposte diverse, ed
	// è la ragione per cui i due conteggi esistono entrambi.
	nuovo := jobs.NewJob()
	nuovo.Name = "job-nuovo"
	nuovo.Every = time.Hour
	nuovo.URL = "https://esempio.test/hook"
	if _, err := b.svc.Create(t.Context(), utente, nuovo); err == nil {
		t.Fatal("la creazione doveva essere rifiutata dal tetto del catalogo")
	}
}

// Mettere in pausa non consuma capacità e non deve mai essere rifiutato: è il
// modo in cui l'utente fa posto.
func TestLaPausaNonPassaDalTetto(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, piano(1))

	job := jobs.NewJob()
	job.UserID = utente
	job.Name = "unico"
	job.Every = time.Hour
	job.URL = "https://esempio.test/hook"
	job.Enabled = true
	seminato := b.store.Seed(job)

	spento := false
	fermo, err := b.svc.Update(t.Context(), utente, seminato.ID, jobs.Patch{Enabled: &spento})
	if err != nil {
		t.Fatalf("pausa rifiutata: %v", err)
	}
	if fermo.Enabled {
		t.Fatal("job non messo in pausa")
	}
	// Una pausa dell'utente non è una sospensione nostra, e non deve fingersi
	// tale: R58 esiste per far vedere la differenza.
	if fermo.Suspended() {
		t.Fatal("una pausa decisa dall'utente non va marcata come sospensione di piano")
	}
}

// Un errore del conteggio non deve diventare una riaccensione riuscita.
func TestErroreDelConteggioNonRiaccende(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, piano(3))
	elenco := sospesi(t, b, 5, time.Hour)
	b.store.FailOn["CountActiveJobs"] = errors.New("database irraggiungibile")

	if _, err := riaccendi(t, b, elenco[0].ID); err == nil {
		t.Fatal("atteso un errore")
	}
}

// piano è il piano Free con un tetto scelto dal test.
func piano(maxJobs int) jobs.Plan {
	p := jobs.FreePlan
	p.MaxJobs = &maxJobs
	return p
}
