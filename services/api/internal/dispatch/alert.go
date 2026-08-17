package dispatch

import (
	"context"
	"log/slog"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// FailureKind classifica il motivo di un fallimento definitivo, e **non è un
// messaggio d'errore**.
//
// La distinzione è la stessa che vale per [Result.ErrorText], e qui pesa di più:
// quel testo finisce in `job_executions`, che l'utente legge dall'API, mentre
// questo esce dal servizio dentro un'email. Il testo grezzo di un errore di rete
// o di un redirect può citare l'indirizzo verso cui si stava andando, e da #458
// in poi quell'indirizzo contiene i segreti del workspace risolti (R43). Una
// classificazione non può portarli.
//
// I valori coincidono con quelli di internal/notify, che li traduce in una frase
// nella lingua del destinatario. Questo package non conosce quel package: la
// direzione è la stessa di [scheduler.Dispatcher], cioè chi sta a monte dichiara
// l'interfaccia e chi sta a valle la implementa.
type FailureKind string

const (
	FailureTimeout    FailureKind = "timeout"
	FailureHTTPStatus FailureKind = "http_status"
	FailureUnknown    FailureKind = "unknown"
)

// Failure è un'esecuzione chiusa male **per cui non ci saranno altri
// tentativi**.
//
// «Definitivo» è la parola che conta: un fallimento seguito da un retry non è
// una notizia per l'utente, è il funzionamento normale di R5. Ciò che merita un
// avviso è la fine della catena.
type Failure struct {
	// UserID è il proprietario del job, JobID e JobName il job.
	UserID  string
	JobID   string
	JobName string
	// Environment distingue staging da production: sono due esiti separati
	// (R23), e due avvisi separati.
	Environment string

	// ScheduledFor e Attempt individuano la riga di `job_executions`.
	ScheduledFor time.Time
	Attempt      int
	// OccurredAt è l'istante dell'ultimo tentativo.
	OccurredAt time.Time

	Kind FailureKind
	// HTTPStatus è lo status della risposta, zero se non ce n'è stata una.
	HTTPStatus int
}

// Alerter riceve i fallimenti definitivi.
//
// L'implementazione è internal/notify, che decide **se** e **quando** l'avviso
// diventa un'email: qui non c'è nessuna politica anti-spam, e non deve
// essercene una. Un motore che si ricordasse di non avvisare troppo spesso
// sarebbe un motore con dentro una seconda politica, scritta peggio e impossibile
// da cambiare senza toccare il dispatch.
//
// Il contratto ha due clausole:
//
//   - **Non deve bloccare a lungo.** Viene chiamata da un worker, e un worker
//     fermo è ciò che R3 vieta. Accodare e uscire è il comportamento previsto.
//   - **Un errore non ferma niente.** Un avviso che non parte non deve cambiare
//     l'esito dell'esecuzione, che è già stato scritto: viene registrato e basta.
type Alerter interface {
	JobFailed(ctx context.Context, failure Failure) error
}

// alert avvisa che un'esecuzione è fallita in via definitiva.
//
// Si chiama **dopo** che l'esito è stato scritto e solo quando la catena dei
// tentativi è chiusa: prima sarebbe un allarme su un fallimento che il retry
// successivo potrebbe rimediare da solo.
func (p *Pool) alert(occ scheduler.Occurrence, rec Record, res Result) {
	if p.alerter == nil {
		return
	}
	if rec.Outcome == Succeeded || rec.Outcome == Skipped {
		return
	}

	ctx, cancel := p.storeCtx()
	defer cancel()

	failure := Failure{
		UserID:       occ.Job.UserID,
		JobID:        occ.Job.ID,
		JobName:      occ.Job.Name,
		Environment:  occ.Environment,
		ScheduledFor: occ.ScheduledFor,
		Attempt:      int(occ.Attempt),
		OccurredAt:   p.now(),
		Kind:         failureKind(rec, res),
		HTTPStatus:   res.ResponseStatus,
	}

	if err := p.alerter.JobFailed(ctx, failure); err != nil {
		// L'avviso non parte, l'esecuzione resta chiusa com'era: un'email
		// mancata non riscrive la storia di un job.
		p.log.ErrorContext(ctx, "dispatch: avviso di fallimento non accodato",
			slog.String("occorrenza", occ.String()),
			slog.Any("error", err))
	}
}

// failureKind classifica l'esito con quello che il pool sa, e niente di più.
//
// Le tre classi sono meno di quelle che internal/notify accetta, e la
// differenza è dichiarata: distinguere un errore di DNS da uno di TLS
// richiederebbe di leggere il testo dell'errore dell'esecutore, che è
// esattamente il testo che non deve arrivare fin qui. Meglio `unknown` — che il
// template racconta come «per un motivo che il motore non ha saputo
// classificare» — che una classificazione ottenuta frugando in una stringa che
// può contenere un segreto.
func failureKind(rec Record, res Result) FailureKind {
	switch {
	case rec.Outcome == TimedOut:
		return FailureTimeout
	case res.ResponseStatus >= 100 && res.ResponseStatus <= 599:
		return FailureHTTPStatus
	default:
		return FailureUnknown
	}
}
