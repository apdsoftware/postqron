package dispatch

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
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
	FailureDNS        FailureKind = "dns"
	FailureTLS        FailureKind = "tls"
	FailureConnection FailureKind = "connection"
	FailureHTTPStatus FailureKind = "http_status"
	FailureUnknown    FailureKind = "unknown"
)

// kinds è l'ordine stabile in cui le classi vengono esposte come etichetta di
// una metrica. Stabile perché un insieme che cambia ordine fra due raccolte è un
// grafico che salta.
var kinds = [...]FailureKind{
	FailureTimeout, FailureDNS, FailureTLS,
	FailureConnection, FailureHTTPStatus, FailureUnknown,
}

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

	// Outcome è l'esito scritto sulla riga: [Failed] o [TimedOut].
	//
	// Non serve a chi avvisa — [Failure.Kind] dice già all'utente cos'è successo,
	// in una forma che un template può rendere — e serve a chi conta, perché è la
	// grandezza con cui la metrica degli esiti si riconcilia. Averlo qui invece
	// che in una seconda struttura è ciò che tiene una descrizione sola del
	// fatto.
	Outcome Outcome
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

// alert consegna a chi avvisa l'utente il fallimento già descritto da
// [Pool.failed].
//
// La [Failure] arriva costruita perché il **conteggio** e l'**avviso** devono
// raccontare lo stesso guasto: costruirla due volte significherebbe due istanti
// e due classificazioni libere di divergere, che si noterebbe come una metrica
// che non torna con l'email accanto.
func (p *Pool) alert(occ scheduler.Occurrence, failure Failure) {
	if p.alerter == nil {
		return
	}

	ctx, cancel := p.storeCtx()
	defer cancel()

	if err := p.alerter.JobFailed(ctx, failure); err != nil {
		// L'avviso non parte, l'esecuzione resta chiusa com'era: un'email
		// mancata non riscrive la storia di un job.
		p.log.ErrorContext(ctx, "dispatch: avviso di fallimento non accodato",
			slog.String("occorrenza", occ.String()),
			slog.Any("error", err))
	}
}

// failureKind classifica l'esito **guardando il tipo dell'errore, mai il suo
// testo**.
//
// La distinzione è tutta qui, e vale la pena scriverla perché la versione
// precedente si fermava a tre classi proprio per non doverla fare. Il timore era
// giusto: il *messaggio* di un errore di rete o di un redirect può citare
// l'indirizzo verso cui si stava andando, e da #458 in poi quell'indirizzo
// contiene i segreti del workspace risolti (R43). Un `strings.Contains` su quel
// messaggio per riconoscere «DNS» sarebbe stato il modo di far entrare un
// segreto in un'email.
//
// `errors.As` non legge niente: risale la catena degli errori e confronta dei
// **tipi**. `*net.DNSError` è un nome che non si risolve quale che sia la frase
// che l'accompagna, e nessun valore transita. L'esecutore conserva la causa con
// `%w` apposta — è già così che si distingue un timeout da un fallimento — e
// questa funzione usa lo stesso aggancio.
//
// Le sei classi sono quelle che `notify.FailureKind` dichiara e che il template
// di R21 sa già rendere. Tre di esse — dns, tls, connection — erano dichiarate e
// irraggiungibili: un guasto di rete arrivava all'utente come «per un motivo che
// il motore non ha saputo classificare», che è vero solo finché nessuno guarda
// il tipo dell'errore.
//
// L'ordine dei casi è quello della specificità: un timeout durante una
// risoluzione DNS è un timeout — è il tetto del job ad aver deciso l'esito — e
// un errore TLS arriva incapsulato in un `*net.OpError`, quindi va riconosciuto
// prima di lui.
func failureKind(rec Record, res Result, err error) FailureKind {
	switch {
	case rec.Outcome == TimedOut:
		return FailureTimeout
	case err == nil && res.ResponseStatus >= 100 && res.ResponseStatus <= 599:
		return FailureHTTPStatus
	case err == nil:
		// Un esito non riuscito senza errore e senza status non esiste per
		// costruzione (vedi [record]): se compare, è un bug, e chiamarlo
		// sconosciuto è esattamente ciò che è.
		return FailureUnknown
	}

	var dns *net.DNSError
	if errors.As(err, &dns) {
		return FailureDNS
	}
	var cert *tls.CertificateVerificationError
	if errors.As(err, &cert) {
		return FailureTLS
	}
	var record tls.RecordHeaderError
	if errors.As(err, &record) {
		return FailureTLS
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return FailureConnection
	}
	if res.ResponseStatus >= 100 && res.ResponseStatus <= 599 {
		// La risposta è cominciata e si è interrotta leggendola: lo status è il
		// fatto più utile dei due.
		return FailureHTTPStatus
	}
	return FailureUnknown
}

// FailureKinds elenca le classi in ordine stabile.
//
// Esiste perché chi espone una metrica per classe deve poterle scrivere tutte,
// **anche quelle a zero**: una serie che compare solo quando il guasto è già
// accaduto non permette di vedere che è appena cominciato. Averla qui, accanto
// alle costanti, è ciò che impedisce a quell'elenco di essere una copia che
// diverge al primo valore aggiunto.
func FailureKinds() []FailureKind { return kinds[:] }
