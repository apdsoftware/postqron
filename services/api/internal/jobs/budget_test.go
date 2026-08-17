package jobs_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// pianoAgency è la riga di SPEC §8: nessun tetto rigido, nessuna soglia di fair
// use, workspace isolati.
func pianoAgency() jobs.Plan {
	return jobs.Plan{
		Code: "agency", Name: "Agency",
		MinInterval:         time.Second,
		LogRetention:        90 * 24 * time.Hour,
		EnvironmentsEnabled: true,
		MultiWorkspace:      true,
	}
}

// TestLaPortataDiAgencyEQuellaDiTeamPerWorkspace è R25-bis.
//
// Agency non dichiara né tetto né soglia, e la lettura ingenua è «illimitato».
// R25-bis dice il contrario, e dice anche perché: Agency non è «Team con più
// potenza», è «Team moltiplicato» — un workspace Agency serve un cliente finale
// con gli stessi job di un cliente Team, quindi non ha ragione di eseguire di
// più. Ciò che scala è il numero di workspace, cioè ciò per cui l'agenzia paga.
func TestLaPortataDiAgencyEQuellaDiTeamPerWorkspace(t *testing.T) {
	b := newBanco(t)
	fairUse := 2
	// I numeri stanno nel **listino**, non qui: il test li mette nella riga di
	// `team` e verifica che siano quelli a comparire nella portata di Agency. Se
	// un giorno qualcuno li scrivesse nel codice, questo test resterebbe verde
	// solo per caso — perciò sono deliberatamente diversi da quelli veri.
	b.store.SetPlanByCode(jobs.Plan{
		Code: "team", Name: "Team",
		FairUseJobs: &fairUse,
		MinInterval: time.Minute,
	})
	b.store.SetPlan(utente, pianoAgency())

	budget, err := b.svc.Budget(t.Context(), utente)
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}
	if !budget.Limited {
		t.Fatal("Agency risulta illimitato: R25-bis non è applicato")
	}
	if !budget.PerWorkspace {
		t.Error("la portata di Agency non è marcata come per workspace")
	}
	if budget.BasisPlan.Code != "team" {
		t.Errorf("piano di riferimento = %q, atteso \"team\"", budget.BasisPlan.Code)
	}
	if budget.Rule.Burst != fairUse || budget.Rule.Window != time.Minute {
		t.Errorf("portata = %d ogni %s, attesa quella del listino di Team (%d ogni %s)",
			budget.Rule.Burst, budget.Rule.Window, fairUse, time.Minute)
	}
}

// TestIlBudgetDiAgencyRifiutaEDiceDaDoveViene: il rifiuto non deve invitare a
// un upgrade che non esiste. Agency è in cima al listino, e la sua capacità
// cresce aggiungendo workspace, non cambiando piano.
func TestIlBudgetDiAgencyRifiutaEDiceDaDoveViene(t *testing.T) {
	b := newBanco(t)
	fairUse := 2
	b.store.SetPlanByCode(jobs.Plan{
		Code: "team", Name: "Team",
		FairUseJobs: &fairUse,
		MinInterval: time.Minute,
	})
	b.store.SetPlan(utente, pianoAgency())

	var creati []jobs.Job
	for i := range fairUse + 1 {
		job := validJob()
		job.Name = fmt.Sprintf("job-%d", i)
		creati = append(creati, b.crea(job))
	}

	for i := range fairUse {
		if _, err := b.svc.Trigger(t.Context(), utente, creati[i].ID, ""); err != nil {
			t.Fatalf("trigger %d: %v", i, err)
		}
	}

	// Ogni job ha la propria casella: a fermare il terzo è solo l'aggregato, che
	// su Agency esiste soltanto grazie a R25-bis.
	_, err := b.svc.Trigger(t.Context(), utente, creati[fairUse].ID, "")
	limit := planLimit(t, err)
	if limit.Plan != "agency" {
		t.Errorf("piano = %q, atteso \"agency\"", limit.Plan)
	}
	if limit.RetryAfter <= 0 {
		t.Errorf("retry_after = %s, atteso positivo", limit.RetryAfter)
	}

	messaggio := limit.Error()
	for _, atteso := range []string{"Agency", "Team", "workspace"} {
		if !strings.Contains(messaggio, atteso) {
			t.Errorf("il messaggio non contiene %q: %q", atteso, messaggio)
		}
	}
	// Un invito a comprare ciò che si ha già è una bugia commerciale.
	if strings.Contains(messaggio, "piano superiore") {
		t.Errorf("il rifiuto invita a un upgrade che non esiste: %q", messaggio)
	}
}

// TestSenzaIlPianoDiRiferimentoNonSiInventaUnaPortata: il listino non ha più la
// riga da cui R25-bis deriva. Rifiutare tutto il traffico di un cliente Agency
// perché una riga è stata rinominata sarebbe molto peggio del limite mancato.
func TestSenzaIlPianoDiRiferimentoNonSiInventaUnaPortata(t *testing.T) {
	b := newBanco(t)
	b.store.SetPlan(utente, pianoAgency())

	budget, err := b.svc.Budget(t.Context(), utente)
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}
	if budget.Limited {
		t.Errorf("portata derivata dal nulla: %+v", budget.Rule)
	}

	job := b.crea(validJob())
	if _, err := b.svc.Trigger(t.Context(), utente, job.ID, ""); err != nil {
		t.Errorf("il trigger è stato rifiutato senza una portata da applicare: %v", err)
	}
}

// TestLaPortataDiUnPianoNormaleNonPassaDalRiferimento: solo i piani che
// moltiplicano un altro piano vanno a cercarne uno. Free ha i propri numeri, e
// leggerne altri sarebbe un giro inutile e un limite sbagliato.
func TestLaPortataDiUnPianoNormaleNonPassaDalRiferimento(t *testing.T) {
	b := newBanco(t)
	b.store.FailOn["PlanByCode"] = fmt.Errorf("nessuno deve leggere il listino qui")

	budget, err := b.svc.Budget(t.Context(), utente)
	if err != nil {
		t.Fatalf("Budget: %v", err)
	}
	if !budget.Limited || budget.PerWorkspace {
		t.Fatalf("portata di Free = %+v", budget)
	}
	if budget.Rule.Burst != 20 || budget.Rule.Window != time.Minute {
		t.Errorf("portata = %d ogni %s, attesi 20 ogni minuto (SPEC §8)",
			budget.Rule.Burst, budget.Rule.Window)
	}
}

// ------------------------------------------------------------------ retention

// TestIlRegistroSiFermaAllaRetentionDelPiano è R15 sulla riga «Retention log» di
// SPEC §8: il limite è applicato lato backend, non solo mostrato nella UI.
func TestIlRegistroSiFermaAllaRetentionDelPiano(t *testing.T) {
	b := newBanco(t)
	job := b.crea(validJob())

	recente := b.clock.Now().Add(-24 * time.Hour)
	antica := b.clock.Now().Add(-10 * 24 * time.Hour)
	for _, quando := range []time.Time{recente, antica} {
		b.store.SeedExecution(jobs.Execution{
			JobID:        job.ID,
			ScheduledFor: quando,
			Environment:  jobs.EnvironmentProduction,
			Attempt:      1,
			Status:       jobs.StatusSucceeded,
			TriggeredBy:  jobs.TriggerSchedule,
		})
	}

	// Senza finestra richiesta: la retention del piano Free è tre giorni, e ciò
	// che sta oltre non compare. Non è un rifiuto mascherato — l'utente non ha
	// chiesto quelle righe — ed è la stessa risposta che darà la cancellazione
	// periodica (#393) quando avrà girato.
	page, err := b.svc.Executions(t.Context(), utente, job.ID, jobs.ExecutionOptions{})
	if err != nil {
		t.Fatalf("Executions: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("righe = %d, attesa solo quella dentro la retention", len(page.Items))
	}
	if !page.Items[0].ScheduledFor.Equal(recente) {
		t.Errorf("riga restituita = %s, attesa %s", page.Items[0].ScheduledFor, recente)
	}
}

// TestChiedereOltreLaRetentionVieneRifiutatoConIlPiano: chi chiede
// esplicitamente uno storico che il piano non conserva riceve un rifiuto che
// nomina il piano, non una risposta corta e silenziosa da interpretare.
func TestChiedereOltreLaRetentionVieneRifiutatoConIlPiano(t *testing.T) {
	b := newBanco(t)
	job := b.crea(validJob())

	_, err := b.svc.Executions(t.Context(), utente, job.ID, jobs.ExecutionOptions{
		Since: b.clock.Now().Add(-30 * 24 * time.Hour),
	})
	limit := planLimit(t, err)

	if limit.Limit != jobs.LimitRetention {
		t.Errorf("limite = %q, atteso %q", limit.Limit, jobs.LimitRetention)
	}
	if limit.Plan != "free" {
		t.Errorf("piano = %q, atteso \"free\"", limit.Plan)
	}
	if limit.Field != "since" {
		t.Errorf("campo = %q, atteso \"since\"", limit.Field)
	}
	if limit.RetryAfter != 0 {
		t.Errorf("retry_after = %s: non è un limite di frequenza, riprovare non aiuta", limit.RetryAfter)
	}
	if !strings.Contains(limit.Error(), "Free") || !strings.Contains(limit.Error(), "3 giorni") {
		t.Errorf("il messaggio non dice quale piano conserva quanto: %q", limit.Error())
	}
}

// TestUnPianoConRetentionPiuLungaLegge: il limite viene dalla matrice, non da
// una costante. Lo stesso registro, con un piano diverso, si legge tutto.
func TestUnPianoConRetentionPiuLungaLegge(t *testing.T) {
	b := newBanco(t)
	job := b.crea(validJob())
	b.store.SeedExecution(jobs.Execution{
		JobID:        job.ID,
		ScheduledFor: b.clock.Now().Add(-10 * 24 * time.Hour),
		Environment:  jobs.EnvironmentProduction,
		Attempt:      1,
		Status:       jobs.StatusSucceeded,
		TriggeredBy:  jobs.TriggerSchedule,
	})

	max := 200
	b.store.SetPlan(utente, jobs.Plan{
		Code: "pro", Name: "Pro", MaxJobs: &max,
		MinInterval:  10 * time.Second,
		LogRetention: 15 * 24 * time.Hour,
	})

	page, err := b.svc.Executions(t.Context(), utente, job.ID, jobs.ExecutionOptions{
		Since: b.clock.Now().Add(-14 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Executions su Pro: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("righe = %d, attesa 1: i quindici giorni di Pro coprono la richiesta", len(page.Items))
	}
}
