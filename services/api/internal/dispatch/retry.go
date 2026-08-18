package dispatch

import (
	"log/slog"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/retry"
	"github.com/apdsoftware/postqron/services/api/internal/schedule"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// planRetry decide se l'esecuzione appena chiusa merita un altro tentativo, e lo
// arma (R5).
//
// La politica sta in internal/retry; qui ci sono le due cose che quella non può
// sapere, più l'attesa.
//
// # Il retry non scavalca l'occorrenza successiva
//
// Un job a `every: 10s` che fallisce alle 12:00:00 e ritenta con un backoff che
// cresce arriverebbe, al terzo tentativo, dopo che le occorrenze delle 12:00:10 e
// delle 12:00:20 sono già passate. A quel punto la chiamata non ritenta più
// niente: ripete una richiesta che il bersaglio ha già ricevuto due volte, con
// dati più vecchi di quelli che gli sono appena arrivati, e raddoppia il carico
// proprio sul job che ne genera di più.
//
// **Vince la schedulazione.** Se l'istante del tentativo cade a ridosso o dopo
// l'occorrenza successiva, il retry non si fa: quell'occorrenza è un tentativo
// migliore, più fresco, che partirà comunque. Per i job radi — un `0 9 * * *`
// ritenta per ore senza avvicinarsi al giorno dopo — la regola non morde mai; per
// quelli a risoluzione di secondo morde quasi sempre, ed è giusto così: a
// `every: 1s` il retry *è* l'occorrenza successiva.
//
// È lo stesso terreno di R41, che non è ancora implementata: quando arriverà a
// dichiarare per job se le esecuzioni sovrapposte si saltano, si accodano o si
// consentono, questa regola sarà il caso particolare del retry e andrà riletta
// insieme a quella colonna. Fino ad allora il comportamento è dichiarato qui e
// osservabile in [Stats.RetryOverrun].
//
// # Non lascia una riga, e il motivo è il volume
//
// Un retry rinunciato non produce nessuna riga `skipped`. È la stessa scelta di
// [scheduler.Dropped], per la stessa aritmetica: un job a `every: 1s` che
// fallisce in continuazione produce già 86.400 righe al giorno, e scriverne una
// seconda per dire «non ho ritentato» le raddoppierebbe **esattamente sul caso
// peggiore**, cioè sull'unica tabella il cui costo è dominato dal volume. Il
// fatto resta osservabile — è un contatore e una riga di log — invece che
// conservato per giorni su disco.
//
// # Il job è quello letto all'accodamento
//
// La catena dei tentativi lavora sulla fotografia del job che è arrivata con
// l'occorrenza, [scheduler.Job]: se l'utente mette il job in pausa mentre i suoi
// retry sono in corso, quelli in corso arrivano in fondo. La finestra è breve e
// limitata due volte — dal tetto dei tentativi e dalla regola qui sopra — e il
// prezzo dell'alternativa sarebbe rileggere `jobs` a ogni fallimento, cioè una
// query in più sul percorso che si percorre proprio quando le cose vanno male.
// # Dove un fallimento diventa definitivo
//
// Le due uscite senza tentativo successivo sono anche i due punti in cui si sa
// che quell'occorrenza non verrà più eseguita, ed è lì che [Observer.Failed]
// viene chiamato — non alla scrittura dell'esito. La differenza è tutta pratica:
// un job con `max_retries: 3` che al secondo tentativo riesce ha fallito una
// volta e non è un job rotto, e avvisare l'utente al primo fallimento
// significherebbe mandargli un'email per un guasto che il motore stava già
// rimediando. Vedi [Observer].
func (p *Pool) planRetry(occ scheduler.Occurrence, rec Record, res Result, permanent bool, kind FailureKind) {
	if rec.Outcome == Succeeded || rec.Outcome == Skipped {
		return
	}

	decision := p.retry.Plan(policyOf(occ.Job), int(occ.Attempt), retry.Outcome{
		Status:     res.ResponseStatus,
		TimedOut:   rec.Outcome == TimedOut,
		Permanent:  permanent,
		RetryAfter: res.RetryAfter,
	})
	if !decision.Retry {
		if decision.Reason == retry.Exhausted {
			p.counters.retryExhausted.Add(1)
		}
		p.log.Debug("dispatch: nessun tentativo successivo",
			slog.String("occorrenza", occ.String()),
			slog.Int("tentativo", int(occ.Attempt)),
			slog.String("motivo", decision.Reason.String()))
		// Qui la catena è chiusa: è il momento in cui il fallimento diventa una
		// notizia per l'utente (R21) e un numero per chi guarda il motore (R7).
		// Prima no — un fallimento seguito da un tentativo è il funzionamento
		// normale di R5, non un guasto.
		p.failed(occ, rec, res, kind)
		return
	}

	if at, overruns := p.overrunsNextOccurrence(occ, decision.Delay); overruns {
		p.counters.retryOverrun.Add(1)
		p.log.Info("dispatch: tentativo successivo rinunciato, l'occorrenza successiva arriva prima",
			slog.String("occorrenza", occ.String()),
			slog.Int("tentativo", int(occ.Attempt)),
			slog.Duration("attesa", decision.Delay),
			slog.Time("occorrenza_successiva", at))
		// Anche qui non ci sarà un altro tentativo *di questa occorrenza*, e il
		// fallimento va raccontato. È il caso dei job a risoluzione di secondo,
		// dove morde quasi sempre: senza la politica anti-spam di internal/notify
		// sarebbe un avviso al secondo, ed è esattamente il motivo per cui quella
		// politica sta lì e non qui — mentre il **conteggio** li vuole tutti, ed
		// è il motivo per cui i due destinatari restano due.
		p.failed(occ, rec, res, kind)
		return
	}

	next := occ
	next.Attempt = occ.Attempt + 1
	next.Recovered = false
	p.arm(next, decision.Delay)
}

// failed consegna il fallimento definitivo ai suoi due destinatari.
//
// La [Failure] si costruisce **una volta sola** e la ricevono entrambi: sono lo
// stesso fatto, e due costruzioni separate sarebbero due descrizioni dello
// stesso guasto libere di divergere — l'istante di una e la classificazione
// dell'altra, viste da chi legge una metrica accanto a un'email che non
// combaciano.
//
// Il contatore si muove **prima** delle due consegne, e non dopo: conta i
// fallimenti, non le consegne riuscite, e un osservatore lento o un avviso non
// accodato non devono poter far sparire un guasto dai numeri.
//
// Il contesto dell'osservatore è quello del pool, non quello dell'esecuzione:
// l'esecuzione è finita, e il suo contesto porta la scadenza del job (R40) — chi
// osserva si troverebbe un tempo già consumato da una chiamata che non lo
// riguarda. Chi avvisa ne prende uno suo, con il tetto di una transizione di
// stato: vedi [Pool.alert].
func (p *Pool) failed(occ scheduler.Occurrence, rec Record, res Result, kind FailureKind) {
	if rec.Outcome == Succeeded || rec.Outcome == Skipped {
		return
	}

	failure := Failure{
		UserID:       occ.Job.UserID,
		JobID:        occ.Job.ID,
		JobName:      occ.Job.Name,
		Environment:  occ.Environment,
		ScheduledFor: occ.ScheduledFor,
		Attempt:      int(occ.Attempt),
		OccurredAt:   p.now(),
		Kind:         kind,
		HTTPStatus:   res.ResponseStatus,
		Outcome:      rec.Outcome,
	}

	p.counters.failedFinal.Add(1)
	p.obs.Failed(p.ctx, failure)
	p.alert(occ, failure)
}

// policyOf legge la politica di retry dichiarata dal job (SPEC §9, `jobs`).
//
// Il tetto del servizio non si applica qui: lo applica il pianificatore, che è
// l'unico posto in cui vive: vedi [retry.Planner.Plan].
func policyOf(job scheduler.Job) retry.Policy {
	return retry.Policy{MaxRetries: job.MaxRetries, Backoff: retry.Backoff(job.RetryBackoff)}
}

// overrunsNextOccurrence dice se il tentativo cadrebbe a ridosso o dopo
// l'occorrenza successiva dello stesso job, e quand'è quell'occorrenza.
//
// Il confronto è fra un istante **reale** — adesso più l'attesa — e l'istante
// teorico della prossima occorrenza, che è ciò che lo scheduler userà per
// accodarla. Il termine di paragone è l'occorrenza successiva a `scheduled_for`,
// non a `now`: è quella che il retry rischia di scavalcare, e per i tentativi
// oltre il primo è già passata — il che chiude da sé la catena.
//
// Una schedulazione illeggibile non ferma il retry. Sarebbe un guasto nostro —
// il database garantisce che una delle due modalità ci sia e sia ben formata — e
// togliere all'utente i tentativi che ha chiesto per un errore di lettura è la
// reazione sbagliata: il tentativo si fa, e il guasto finisce nei log dove
// qualcuno lo vede.
func (p *Pool) overrunsNextOccurrence(occ scheduler.Occurrence, delay time.Duration) (time.Time, bool) {
	sched, err := schedule.Parse(schedule.Spec{
		Expression: occ.Job.Expression,
		Every:      occ.Job.Every,
		Timezone:   occ.Job.Timezone,
	})
	if err != nil {
		p.log.Warn("dispatch: schedulazione del job illeggibile, il tentativo successivo procede",
			slog.String("occorrenza", occ.String()), slog.Any("err", err))
		return time.Time{}, false
	}

	next, ok := sched.Next(occ.ScheduledFor)
	if !ok {
		// Il job non ha più occorrenze: non c'è niente che il retry possa
		// scavalcare, ed è anzi l'ultima cosa che quell'occorrenza otterrà.
		return time.Time{}, false
	}
	return next, !p.now().Add(delay).Before(next)
}

// ------------------------------------------------------------------- attesa

// arm mette il tentativo in attesa del proprio ritardo.
//
// Una goroutine per tentativo in attesa, e non una coda ordinata per scadenza:
// i retry pendenti sono pochi per costruzione — sono i fallimenti recenti, non
// tutto il lavoro — e una goroutine parcheggiata su un canale costa qualche
// kilobyte di stack e nessun risveglio. La coda ordinata sarebbe la struttura
// giusta per migliaia di attese, che è un carico che questo non produce.
func (p *Pool) arm(occ scheduler.Occurrence, delay time.Duration) {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()

	if p.retriesClosed {
		// Il pool si sta fermando: il tentativo non parte e non lascia niente
		// dietro di sé. È il compromesso dichiarato nella documentazione del
		// package.
		p.counters.retryAbandoned.Add(1)
		p.log.Info("dispatch: tentativo successivo non armato, il pool si sta fermando",
			slog.String("occorrenza", occ.String()), slog.Int("tentativo", int(occ.Attempt)))
		return
	}

	p.log.Debug("dispatch: tentativo successivo in attesa",
		slog.String("occorrenza", occ.String()),
		slog.Int("tentativo", int(occ.Attempt)), slog.Duration("attesa", delay))

	p.retryWG.Add(1)
	go func() {
		defer p.retryWG.Done()
		select {
		case <-p.after(delay):
			p.fireRetry(occ)
		case <-p.retryStop:
			p.counters.retryAbandoned.Add(1)
			p.log.Info("dispatch: tentativo successivo interrotto dall'arresto",
				slog.String("occorrenza", occ.String()), slog.Int("tentativo", int(occ.Attempt)))
		}
	}()
}

// fireRetry crea la riga del tentativo e lo accoda.
//
// L'ordine — prima la riga, poi la coda — è quello dello scheduler, e per la
// stessa ragione: l'inserimento è il primo cancello di R4, e un'occorrenza che
// entrasse in coda senza la propria riga verrebbe scartata dal secondo cancello
// ([Store.Claim]) dopo aver occupato un worker per niente.
//
// Se la coda rifiuta il tentativo la riga viene chiusa subito come `skipped`.
// Lasciarla `pending` sarebbe la cosa peggiore possibile: il recupero dello
// scheduler salta di proposito ciò che ha `triggered_by = 'retry'`, quindi non la
// riprenderebbe nessuno, e resterebbe per sempre dentro
// `job_executions_in_flight_idx` — l'indice che la migrazione 0006 tiene piccolo
// apposta — e in dashboard come un'esecuzione eternamente in attesa.
func (p *Pool) fireRetry(occ scheduler.Occurrence) {
	ctx, cancel := p.storeCtx()
	created, err := p.store.Enqueue(ctx, occ)
	cancel()

	switch {
	case err != nil:
		p.counters.errs.Add(1)
		p.counters.retryAbandoned.Add(1)
		p.log.Error("dispatch: creazione del tentativo successivo fallita",
			slog.String("occorrenza", occ.String()),
			slog.Int("tentativo", int(occ.Attempt)), slog.Any("err", err))
		return
	case !created:
		// Il tentativo esisteva già: un'altra replica del motore ha eseguito la
		// stessa occorrenza ed è arrivata a questo punto prima di noi. È R4 che
		// funziona, non un errore.
		p.log.Debug("dispatch: tentativo successivo già creato da qualcun altro",
			slog.String("occorrenza", occ.String()), slog.Int("tentativo", int(occ.Attempt)))
		return
	}

	// L'occorrenza entra in coda adesso: è questo il momento che [Occurrence.Lag]
	// misura, e per un retry il ritardo rispetto all'orario teorico comprende per
	// costruzione tutta l'attesa del backoff.
	occ.EnqueuedAt = p.now()
	if err := p.Dispatch(p.ctx, occ); err != nil {
		p.counters.retryAbandoned.Add(1)
		p.log.Warn("dispatch: tentativo successivo non accodato",
			slog.String("occorrenza", occ.String()),
			slog.Int("tentativo", int(occ.Attempt)), slog.Any("err", err))
		p.skip(occ, "tentativo successivo non eseguito: il dispatch non ha potuto accodarlo")
		return
	}
	p.counters.retried.Add(1)
}

// stopRetries chiude i tentativi in attesa e aspetta che le loro goroutine
// escano.
//
// L'attesa serve a una cosa sola, ed è quella che conta: quando torna, nessuno
// sta più per inserire una riga di retry. Senza, l'arresto potrebbe lasciarsi
// dietro esattamente la riga `pending` che nessuno riprenderà più — vedi
// [Pool.fireRetry].
func (p *Pool) stopRetries() {
	p.retryMu.Lock()
	if !p.retriesClosed {
		p.retriesClosed = true
		close(p.retryStop)
	}
	p.retryMu.Unlock()

	p.retryWG.Wait()
}
