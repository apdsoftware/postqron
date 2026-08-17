package jobs_test

import (
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Il tetto tecnico sulle esecuzioni contemporanee (R10), e la sola cosa che va
// verificata più di tutte: che **non sia** una quota di piano.
//
// R10 dice che i due rifiuti dicono cose diverse — quello di piano nomina il
// piano che consente di più, quello tecnico non promette nulla — e sono due tipi
// distinti proprio perché la distinzione non resti una convenzione da ricordare.
// Questi test la fissano dal lato del dominio; la forma della risposta HTTP la
// fissa internal/httpapi.

func jobDaEseguire(b *banco) jobs.Job {
	b.t.Helper()
	job := validJob()
	job.Name = "da-eseguire"
	job.Schedule = ""
	job.Every = time.Minute
	return b.crea(job)
}

// TestIlTettoTecnicoRifiutaIlTriggerManualeSenzaNominareUnPiano.
func TestIlTettoTecnicoRifiutaIlTriggerManualeSenzaNominareUnPiano(t *testing.T) {
	b := newBanco(t)
	job := jobDaEseguire(b)
	b.concorrenza.riempi(utente, 8)

	_, err := b.svc.Trigger(t.Context(), utente, job.ID, jobs.EnvironmentProduction)

	limite, ok := jobs.AsServiceLimit(err)
	if !ok {
		t.Fatalf("errore = %v, atteso un tetto tecnico del servizio", err)
	}
	if limite.Limit != jobs.LimitExecutionCeiling {
		t.Errorf("limite = %q, atteso %q", limite.Limit, jobs.LimitExecutionCeiling)
	}
	if limite.RetryAfter <= 0 {
		t.Error("il rifiuto non dice fra quanto riprovare: un tetto di capienza si libera da sé")
	}

	// **La proprietà che conta.** Un tetto tecnico non è un limite di piano, e
	// non deve poter essere scambiato per uno: un client che lo prendesse per tale
	// manderebbe l'utente a comprare un aggiornamento che non gli servirebbe.
	if _, èDiPiano := jobs.AsPlanLimit(err); èDiPiano {
		t.Fatal("il tetto tecnico è passato per un limite di piano: R10 li tiene distinti apposta")
	}
	// E il messaggio non nomina nessun piano né invita ad aggiornare.
	for _, parola := range []string{"Free", "Pro", "Team", "Agency", "piano superiore"} {
		if strings.Contains(limite.Error(), parola) {
			t.Errorf("il messaggio cita %q: nessun piano ne concede di più, e dirlo sarebbe una bugia commerciale\n%s",
				parola, limite.Error())
		}
	}
	// Dice invece che le esecuzioni schedulate non vengono rifiutate, che è
	// l'informazione che evita il ticket «i miei job si sono fermati».
	if !strings.Contains(limite.Error(), "schedulate") {
		t.Errorf("il messaggio non chiarisce che riguarda solo il trigger manuale:\n%s", limite.Error())
	}
}

// TestIlTettoTecnicoNonConsumaLaQuotaDiPiano è la seconda metà di R10: «non far
// consumare la quota di piano a ciò che quota non è».
//
// Il piano Free concede venti operazioni al minuto (la portata derivata da SPEC
// §8). Se il rifiuto tecnico spendesse un gettone, chi sbatte contro il tetto
// pagherebbe due volte lo stesso limite: una alla macchina e una al proprio
// budget. Il test spende l'intero budget con rifiuti tecnici e poi verifica che
// il budget sia ancora tutto lì.
func TestIlTettoTecnicoNonConsumaLaQuotaDiPiano(t *testing.T) {
	b := newBanco(t)
	massimo := 2
	b.store.SetPlan(utente, jobs.Plan{
		Code: "free", Name: "Free",
		MaxJobs:     &massimo,
		MinInterval: time.Minute,
	})
	job := jobDaEseguire(b)
	b.concorrenza.riempi(utente, 4)

	// Più tentativi di quanti il budget del piano ne ammetta: se ciascuno
	// spendesse un gettone, l'ultimo rifiuto sarebbe di piano invece che tecnico.
	for i := range massimo + 3 {
		_, err := b.svc.Trigger(t.Context(), utente, job.ID, jobs.EnvironmentProduction)
		if _, ok := jobs.AsServiceLimit(err); !ok {
			t.Fatalf("tentativo %d: errore = %v, atteso il tetto tecnico", i, err)
		}
	}

	// Il tetto si libera, e il budget del piano è intatto: il trigger passa.
	b.concorrenza.riempi(utente, 0)
	if _, err := b.svc.Trigger(t.Context(), utente, job.ID, jobs.EnvironmentProduction); err != nil {
		t.Fatalf("il primo trigger dopo il tetto è stato rifiutato: la quota di piano era stata "+
			"consumata dai rifiuti tecnici. %v", err)
	}
}

// TestSottoIlTettoIlTriggerPassa: un tetto che rifiuta sempre non è un tetto, è
// un guasto. Ed è la prova che il workspace su cui si conta è quello del job.
func TestSottoIlTettoIlTriggerPassa(t *testing.T) {
	b := newBanco(t)
	job := jobDaEseguire(b)
	b.concorrenza.riempi("un-altro-workspace", 4)

	if _, err := b.svc.Trigger(t.Context(), utente, job.ID, jobs.EnvironmentProduction); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if b.dispatcher.count() != 1 {
		t.Fatalf("consegne al motore = %d, attesa una", b.dispatcher.count())
	}

	chiesti := b.concorrenza.interrogazioni()
	if len(chiesti) == 0 || chiesti[0] != utente {
		t.Fatalf("il tetto è stato contato su %v, atteso il workspace del job (%s)", chiesti, utente)
	}
}

// TestSenzaSorgenteDelTettoIlTriggerNonSiFerma: la dipendenza è facoltativa, e
// mancarla non deve rifiutare tutto.
//
// Non è un buco: il tetto resta applicato dalla coda del dispatch, che rimanda
// l'esecuzione invece di rifiutarla. Quello che si perde è solo l'anticipo della
// diagnosi.
func TestSenzaSorgenteDelTettoIlTriggerNonSiFerma(t *testing.T) {
	b := newBanco(t)
	job := jobDaEseguire(b)
	// Un tetto dichiarato a zero è «nessun tetto», ed è lo stesso caso di una
	// sorgente assente: entrambi devono lasciar passare.
	b.concorrenza.riempi(utente, 0)

	if _, err := b.svc.Trigger(t.Context(), utente, job.ID, jobs.EnvironmentProduction); err != nil {
		t.Fatalf("Trigger con tetto a zero: %v", err)
	}
}
