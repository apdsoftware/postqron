package jobs_test

import (
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// I piani riproducono le righe che la migrazione 0003 inserisce (SPEC §8). Sono
// copiati qui e non importati perché i test devono rompersi se il listino
// cambia: un limite che cambia in silenzio è esattamente ciò che R15 vieta.
func planPro() jobs.Plan {
	max := 200
	return jobs.Plan{
		Code: "pro", Name: "Pro",
		MaxJobs:             &max,
		MinInterval:         10 * time.Second,
		LogRetention:        15 * 24 * time.Hour,
		EnvironmentsEnabled: true,
	}
}

func planTeam() jobs.Plan {
	fairUse := 1000
	return jobs.Plan{
		Code: "team", Name: "Team",
		FairUseJobs:         &fairUse,
		MinInterval:         time.Second,
		LogRetention:        30 * 24 * time.Hour,
		EnvironmentsEnabled: true,
	}
}

func planAgency() jobs.Plan {
	return jobs.Plan{
		Code: "agency", Name: "Agency",
		MinInterval:         time.Second,
		LogRetention:        90 * 24 * time.Hour,
		EnvironmentsEnabled: true,
	}
}

func planLimit(t *testing.T, err error) *jobs.PlanLimitError {
	t.Helper()
	limit, ok := jobs.AsPlanLimit(err)
	if !ok {
		t.Fatalf("atteso un limite di piano, ottenuto %v", err)
	}
	return limit
}

// TestRisoluzioneMinimaPerPiano è la tabella di SPEC §8: 1 minuto su Free, 10
// secondi su Pro, 1 secondo su Team e Agency.
func TestRisoluzioneMinimaPerPiano(t *testing.T) {
	cases := []struct {
		piano   jobs.Plan
		every   time.Duration
		ammesso bool
	}{
		{jobs.FreePlan, time.Minute, true},
		{jobs.FreePlan, 5 * time.Minute, true},
		{jobs.FreePlan, 59 * time.Second, false},
		{jobs.FreePlan, 10 * time.Second, false},
		{jobs.FreePlan, time.Second, false},

		{planPro(), 10 * time.Second, true},
		{planPro(), 9 * time.Second, false},
		{planPro(), time.Second, false},

		{planTeam(), time.Second, true},
		{planAgency(), time.Second, true},
	}

	for _, tc := range cases {
		err := tc.piano.CheckResolution(tc.every)
		if tc.ammesso && err != nil {
			t.Errorf("%s con every=%s: rifiutato (%v)", tc.piano.Code, tc.every, err)
			continue
		}
		if !tc.ammesso {
			if err == nil {
				t.Errorf("%s con every=%s: accettato, doveva essere rifiutato", tc.piano.Code, tc.every)
				continue
			}
			limit := planLimit(t, err)
			if limit.Limit != jobs.LimitResolution {
				t.Errorf("%s con every=%s: limite = %q, atteso %q", tc.piano.Code, tc.every, limit.Limit, jobs.LimitResolution)
			}
			if limit.Field != "every" {
				t.Errorf("il rifiuto deve indicare il campo `every`, indica %q", limit.Field)
			}
		}
	}
}

// TestEveryUnSecondoSuFreeVieneRifiutatoConIlMotivo è il caso nominato da
// SPEC §9: rifiutato con un messaggio esplicito, **non degradato in silenzio**.
func TestEveryUnSecondoSuFreeVieneRifiutatoConIlMotivo(t *testing.T) {
	job := validJob()
	job.Schedule = ""
	job.Every = time.Second

	err := validate(t, &job, jobs.FreePlan)
	limit := planLimit(t, err)

	if limit.Limit != jobs.LimitResolution {
		t.Fatalf("limite = %q, atteso %q", limit.Limit, jobs.LimitResolution)
	}
	if limit.Plan != "free" {
		t.Errorf("piano = %q, atteso \"free\"", limit.Plan)
	}
	// Il messaggio deve contenere il limite e il valore chiesto: senza, l'utente
	// non sa né perché è stato rifiutato né cosa scrivere al posto.
	for _, atteso := range []string{"Free", "1m", "1s"} {
		if !strings.Contains(limit.Error(), atteso) {
			t.Errorf("il messaggio non contiene %q: %s", atteso, limit.Error())
		}
	}
	// E il job non dev'essere stato modificato: degradare a un minuto sarebbe
	// eseguire qualcosa di diverso da ciò che è stato chiesto.
	if job.Every != time.Second {
		t.Errorf("il job è stato degradato a %s invece di essere rifiutato", job.Every)
	}
}

// TestLaModalitaCronNonPuoViolareLaRisoluzione: un'espressione cron ha
// granularità minima di un minuto per costruzione, quindi non c'è piano che
// possa rifiutarla per risoluzione.
func TestLaModalitaCronNonPuoViolareLaRisoluzione(t *testing.T) {
	for _, plan := range []jobs.Plan{jobs.FreePlan, planPro(), planTeam(), planAgency()} {
		job := validJob()
		job.Schedule = "* * * * *"
		if err := validate(t, &job, plan); err != nil {
			t.Errorf("piano %s: `* * * * *` rifiutato: %v", plan.Code, err)
		}
	}
}

func TestTettoAlNumeroDiJob(t *testing.T) {
	if err := jobs.FreePlan.CheckJobCount(19); err != nil {
		t.Fatalf("19 job su Free rifiutati: %v", err)
	}
	err := jobs.FreePlan.CheckJobCount(20)
	limit := planLimit(t, err)
	if limit.Limit != jobs.LimitJobs {
		t.Errorf("limite = %q, atteso %q", limit.Limit, jobs.LimitJobs)
	}

	// Team è venduto come illimitato: la soglia di fair use non rifiuta, la 0003
	// la tiene distinta da `max_jobs` esattamente per questo.
	if err := planTeam().CheckJobCount(5000); err != nil {
		t.Errorf("il fair use di Team non deve rifiutare: %v", err)
	}
}

func TestAmbientiPerPiano(t *testing.T) {
	if err := jobs.FreePlan.CheckEnvironments([]jobs.Environment{jobs.EnvironmentProduction}); err != nil {
		t.Fatalf("production su Free rifiutato: %v", err)
	}

	err := jobs.FreePlan.CheckEnvironments([]jobs.Environment{jobs.EnvironmentStaging})
	limit := planLimit(t, err)
	if limit.Limit != jobs.LimitEnvironments {
		t.Errorf("limite = %q, atteso %q", limit.Limit, jobs.LimitEnvironments)
	}

	err = jobs.FreePlan.CheckEnvironments([]jobs.Environment{jobs.EnvironmentStaging, jobs.EnvironmentProduction})
	if _, ok := jobs.AsPlanLimit(err); !ok {
		t.Errorf("due ambienti su Free devono essere rifiutati, ottenuto %v", err)
	}

	if err := planPro().CheckEnvironments([]jobs.Environment{jobs.EnvironmentStaging, jobs.EnvironmentProduction}); err != nil {
		t.Errorf("due ambienti su Pro rifiutati: %v", err)
	}
}

// TestBudgetDeiTriggerManualiDerivaDalListino: il numero non è una riga nuova
// del listino, è la portata che la schedulazione del piano già concede.
func TestBudgetDeiTriggerManualiDerivaDalListino(t *testing.T) {
	cases := []struct {
		piano  jobs.Plan
		burst  int
		window time.Duration
		ok     bool
	}{
		{jobs.FreePlan, 20, time.Minute, true},
		{planPro(), 200, 10 * time.Second, true},
		{planTeam(), 1000, time.Second, true},
		// Agency non dichiara né tetto né soglia: non c'è un numero da cui
		// derivare, e inventarlo sarebbe una decisione commerciale presa dal
		// codice.
		{planAgency(), 0, 0, false},
	}

	for _, tc := range cases {
		burst, window, ok := tc.piano.ManualBudget()
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, atteso %v", tc.piano.Code, ok, tc.ok)
			continue
		}
		if ok && (burst != tc.burst || window != tc.window) {
			t.Errorf("%s: budget = %d ogni %s, atteso %d ogni %s",
				tc.piano.Code, burst, window, tc.burst, tc.window)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		time.Second:      "1s",
		10 * time.Second: "10s",
		time.Minute:      "1m",
		90 * time.Second: "90s",
		time.Hour:        "1h",
		5 * time.Minute:  "5m",
	}
	for d, want := range cases {
		if got := jobs.FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%s) = %q, atteso %q", d, got, want)
		}
	}
}
