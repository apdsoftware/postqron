package jobs_test

import (
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// ------------------------------------------------------ la posizione di ripresa

// TestLaPosizioneSopravviveAlGiroDiCodifica: il `Last-Event-ID` è l'unica cosa
// che il client rimanda indietro, e se non tornasse identica la ripresa
// salterebbe righe senza dirlo a nessuno.
func TestLaPosizioneSopravviveAlGiroDiCodifica(t *testing.T) {
	originale := jobs.StreamPosition{
		// Un istante con i microsecondi valorizzati: `timestamptz` ha quella
		// risoluzione, e una codifica che li perdesse rimanderebbe il cursore
		// indietro di una frazione — cioè riconsegnerebbe una riga già vista.
		ScheduledFor: time.Date(2026, 8, 17, 23, 59, 59, 123456000, time.UTC),
		Environment:  jobs.EnvironmentStaging,
		Attempt:      3,
	}

	letta, err := jobs.ParseStreamPosition(originale.Encode())
	if err != nil {
		t.Fatalf("ParseStreamPosition: %v", err)
	}
	if !letta.ScheduledFor.Equal(originale.ScheduledFor) {
		t.Errorf("scheduled_for = %s, atteso %s", letta.ScheduledFor, originale.ScheduledFor)
	}
	if letta.Environment != originale.Environment || letta.Attempt != originale.Attempt {
		t.Errorf("posizione = %+v, attesa %+v", letta, originale)
	}
}

// TestUnCursoreDiPaginazioneNonEUnaPosizioneDiRipresa è la ragione per cui le
// due codifiche hanno un marcatore diverso: hanno lo stesso involucro e
// significano cose opposte — «tutto ciò che viene prima» contro «tutto ciò che
// viene dopo». Accettarlo consegnerebbe lo storico al contrario invece del tempo
// reale, senza nessun errore da nessuna parte.
func TestUnCursoreDiPaginazioneNonEUnaPosizioneDiRipresa(t *testing.T) {
	paginazione := jobs.ExecutionCursor{
		ScheduledFor: time.Now().UTC(),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
	}.Encode()

	if _, err := jobs.ParseStreamPosition(paginazione); err == nil {
		t.Fatal("un cursore di paginazione è stato accettato come posizione di ripresa")
	}
}

func TestUnaPosizioneMalformataVieneRifiutata(t *testing.T) {
	casi := map[string]string{
		"non base64": "questo-non-è-base64!!",
		"vuota":      "",
		"ambiente inesistente": jobs.StreamPosition{
			ScheduledFor: time.Now().UTC(), Environment: "collaudo", Attempt: 1,
		}.Encode(),
		"tentativo zero": jobs.StreamPosition{
			ScheduledFor: time.Now().UTC(), Environment: jobs.EnvironmentProduction, Attempt: 0,
		}.Encode(),
	}

	for nome, valore := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, err := jobs.ParseStreamPosition(valore); err == nil {
				t.Fatalf("%q è stata accettata", valore)
			}
		})
	}
}

// TestSettledDistingueGliStatiDefinitivi: è la funzione su cui si decide quali
// eventi portano una posizione e quali no. Un valore sbagliato qui produce o
// eventi ripetuti o esiti mai consegnati.
func TestSettledDistingueGliStatiDefinitivi(t *testing.T) {
	definitivi := map[jobs.ExecutionStatus]bool{
		jobs.StatusPending:   false,
		jobs.StatusRunning:   false,
		jobs.StatusSucceeded: true,
		jobs.StatusFailed:    true,
		jobs.StatusTimedOut:  true,
		jobs.StatusSkipped:   true,
	}
	for _, stato := range jobs.ExecutionStatuses {
		atteso, noto := definitivi[stato]
		if !noto {
			t.Fatalf("lo stato %q non è classificato: aggiungerlo qui e in jobs.Settled", stato)
		}
		if got := jobs.Settled(stato); got != atteso {
			t.Errorf("Settled(%q) = %v, atteso %v", stato, got, atteso)
		}
	}
}

// ------------------------------------------------------------------ apertura

// TestUnFlussoSuUnJobAltruiNonSiApre: l'autorizzazione avviene una volta sola,
// all'apertura, e da lì in poi è incorporata nel [jobs.Follower]. Questa è la
// prova che avviene davvero — senza, l'identificativo di un job altrui darebbe
// accesso a URL, stati ed estratti di risposta.
func TestUnFlussoSuUnJobAltruiNonSiApre(t *testing.T) {
	b := newBanco(t)
	job := b.crea(jobValido())

	if _, err := b.svc.OpenStream(t.Context(), altroUtente, job.ID, jobs.StreamOptions{}); err == nil {
		t.Fatal("il flusso di un job altrui si è aperto")
	}
}

// TestUnFlussoNuovoPartePocoPrimaDiAdesso: `scheduled_for` è l'istante teorico
// dell'occorrenza, non quello in cui è partita, e una finestra che cominciasse
// esattamente da adesso terrebbe invisibile l'esecuzione già in corso.
func TestUnFlussoNuovoPartePocoPrimaDiAdesso(t *testing.T) {
	b := newBanco(t)
	job := b.crea(jobValido())

	// Una già in corso, con l'istante teorico appena passato; una vecchia, che il
	// flusso non deve consegnare perché non è tempo reale.
	b.store.SeedExecution(jobs.Execution{
		JobID: job.ID, ScheduledFor: b.clock.Now().Add(-5 * time.Second),
		Environment: jobs.EnvironmentProduction, Attempt: 1,
		Status: jobs.StatusRunning, TriggeredBy: jobs.TriggerSchedule,
	})
	b.store.SeedExecution(jobs.Execution{
		JobID: job.ID, ScheduledFor: b.clock.Now().Add(-2 * time.Hour),
		Environment: jobs.EnvironmentProduction, Attempt: 1,
		Status: jobs.StatusSucceeded, TriggeredBy: jobs.TriggerSchedule,
	})

	follower, err := b.svc.OpenStream(t.Context(), utente, job.ID, jobs.StreamOptions{})
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if follower.Start() != nil {
		t.Error("un flusso nuovo non deve avere una posizione di ripresa")
	}

	rows, err := follower.Next(t.Context(), nil, 50)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("righe = %d, attesa 1 (solo quella dentro la finestra): %+v", len(rows), rows)
	}
	if rows[0].Status != jobs.StatusRunning {
		t.Errorf("riga inattesa: %+v", rows[0])
	}
}

// TestLaRetentionSiApplicaAncheAlFlusso è R10-bis vista dallo streaming, nei due
// modi in cui una finestra può essere chiesta.
func TestLaRetentionSiApplicaAncheAlFlusso(t *testing.T) {
	b := newBanco(t)
	job := b.crea(jobValido())

	// Il piano Free conserva tre giorni (SPEC §8).
	oltre := b.clock.Now().Add(-10 * 24 * time.Hour)

	casi := map[string]jobs.StreamOptions{
		"finestra esplicita": {Since: oltre},
		"ripresa antica": {Resume: jobs.StreamPosition{
			ScheduledFor: oltre, Environment: jobs.EnvironmentProduction, Attempt: 1,
		}.Encode()},
	}

	for nome, opts := range casi {
		t.Run(nome, func(t *testing.T) {
			_, err := b.svc.OpenStream(t.Context(), utente, job.ID, opts)
			limite, ok := jobs.AsPlanLimit(err)
			if !ok {
				t.Fatalf("errore = %v, atteso un limite di piano", err)
			}
			if limite.Limit != jobs.LimitRetention {
				t.Errorf("limite = %q, atteso %q", limite.Limit, jobs.LimitRetention)
			}
			if limite.Plan == "" {
				t.Error("un limite di piano deve nominare il piano")
			}
		})
	}
}

// TestIlFlussoNonConsegnaOltreLaRetention: la verifica all'apertura non basta,
// perché una connessione può restare aperta più a lungo della retention. Il
// confine si riapplica a ogni lettura, ed è lo stesso [jobs.Plan.RetentionFloor]
// che applica la cancellazione periodica.
func TestIlFlussoNonConsegnaOltreLaRetention(t *testing.T) {
	b := newBanco(t)
	job := b.crea(jobValido())

	// Dentro la retention al momento dell'apertura.
	dentro := b.clock.Now().Add(-2 * 24 * time.Hour)
	b.store.SeedExecution(jobs.Execution{
		JobID: job.ID, ScheduledFor: dentro,
		Environment: jobs.EnvironmentProduction, Attempt: 1,
		Status: jobs.StatusSucceeded, TriggeredBy: jobs.TriggerSchedule,
	})

	follower, err := b.svc.OpenStream(t.Context(), utente, job.ID,
		jobs.StreamOptions{Since: b.clock.Now().Add(-3 * 24 * time.Hour).Add(time.Minute)})
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if rows, err := follower.Next(t.Context(), nil, 50); err != nil || len(rows) != 1 {
		t.Fatalf("all'apertura: righe = %d, err = %v, attesa 1", len(rows), err)
	}

	// Passano due giorni con la connessione aperta: quella riga è ora fuori dalla
	// retention del piano Free, e non va più consegnata.
	b.clock.avanza(2 * 24 * time.Hour)
	rows, err := follower.Next(t.Context(), nil, 50)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("righe = %d, attese 0: la retention non si è riapplicata in lettura", len(rows))
	}
}

// TestLaRipresaLeggeInAvanti: il cursore è un confine **escluso**, e la lettura
// va dal più antico al più recente. Con il verso sbagliato un client che
// riprende riceverebbe lo storico invece del seguito.
func TestLaRipresaLeggeInAvanti(t *testing.T) {
	b := newBanco(t)
	job := b.crea(jobValido())

	for i := range 4 {
		b.store.SeedExecution(jobs.Execution{
			JobID: job.ID, ScheduledFor: b.clock.Now().Add(time.Duration(i) * time.Second),
			Environment: jobs.EnvironmentProduction, Attempt: 1,
			Status: jobs.StatusSucceeded, TriggeredBy: jobs.TriggerSchedule,
		})
	}

	posizione := jobs.StreamPosition{
		ScheduledFor: b.clock.Now().Add(time.Second),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
	}
	follower, err := b.svc.OpenStream(t.Context(), utente, job.ID,
		jobs.StreamOptions{Resume: posizione.Encode()})
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	rows, err := follower.Next(t.Context(), follower.Start(), 50)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("righe = %d, attese 2 (le due dopo il cursore): %+v", len(rows), rows)
	}
	if !rows[0].ScheduledFor.Before(rows[1].ScheduledFor) {
		t.Errorf("le righe non sono in ordine crescente: %s, %s",
			rows[0].ScheduledFor, rows[1].ScheduledFor)
	}
	if !rows[0].ScheduledFor.Equal(b.clock.Now().Add(2 * time.Second)) {
		t.Errorf("la prima riga è %s: il cursore non è un confine escluso", rows[0].ScheduledFor)
	}
}

// jobValido è un job che il piano Free accetta.
func jobValido() jobs.Job {
	job := jobs.NewJob()
	job.Name = "digest"
	job.Schedule = "0 9 * * *"
	job.URL = "https://api.example.com/tasks/digest"
	return job
}
