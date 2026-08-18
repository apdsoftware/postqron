package jobs

import (
	"context"
	"strconv"
	"time"
)

// La lettura **in avanti** del registro delle esecuzioni: è ciò su cui si
// appoggia lo streaming in tempo reale (R6, SPEC §4.2).
//
// # Perché non è la stessa cosa di [Service.Executions]
//
// Il registro paginato legge **all'indietro** — dal più recente al più antico —
// perché è ciò che serve a chi apre una pagina e vuole vedere subito l'ultima
// esecuzione. Un flusso legge nella direzione opposta: parte da una posizione e
// consegna ciò che arriva dopo, per sempre. I due cursori camminano sullo stesso
// indice ma in due versi, e **hanno una codifica diversa apposta**: un cursore
// di paginazione riusato come posizione di ripresa vorrebbe dire «tutto ciò che
// viene prima» invece di «tutto ciò che viene dopo», cioè consegnerebbe lo
// storico al contrario invece del tempo reale. Vedi [StreamPosition.Encode].
//
// # L'identificatore, che è la parte difficile
//
// Un'esecuzione non ha un identificativo sintetico: la 0006 lo dice e ne dà la
// ragione — con 86.400 righe al giorno per job un `id uuid` costerebbe un
// secondo indice sulla tabella più calda del prodotto. La chiave è la quaterna
// naturale `(job_id, scheduled_for, environment, attempt)`, e siccome il flusso
// è già ancorato a un job, la posizione è la **terna** che resta.
//
// La terna è ordinabile ed è esattamente l'ordine della chiave primaria: da qui
// discende che «riprendi da qui» sia una disuguaglianza sull'indice che esiste
// già, e non una scansione. È anche il motivo per cui un retry non rompe niente:
// la 0006 e `enqueueRetrySQL` tengono lo stesso `scheduled_for` e alzano
// `attempt`, quindi il tentativo successivo cade **dopo** il precedente
// nell'ordine, non prima.

// StreamPosition è la posizione di un flusso dentro il registro di un job.
//
// È la chiave naturale dell'esecuzione meno il job, che nel flusso è fisso. Il
// valore zero significa «nessuna posizione»: si parte dal fondo della finestra
// invece che da una riga precisa.
type StreamPosition struct {
	ScheduledFor time.Time
	Environment  Environment
	Attempt      int
}

// cursorKindStream è il marcatore della codifica. È diverso da quello del
// cursore di paginazione perché i due significano cose opposte: vedi la nota in
// testa al file.
const cursorKindStream = "s1"

// Encode serializza la posizione nella forma che viaggia come `id:` di un evento
// SSE e torna indietro come `Last-Event-ID`.
//
// Non è un segreto e non deve esserlo: la query è comunque limitata al job, che
// è già stato autorizzato all'apertura del flusso. È **opaca** per poter
// cambiare la chiave di ordinamento senza rompere i client, esattamente come i
// cursori di paginazione.
func (p StreamPosition) Encode() string {
	return encodeCursor(cursorKindStream,
		strconv.FormatInt(p.ScheduledFor.UTC().UnixNano(), 10),
		string(p.Environment),
		strconv.Itoa(p.Attempt))
}

// Zero indica una posizione non valorizzata.
func (p StreamPosition) Zero() bool { return p.ScheduledFor.IsZero() }

// ParseStreamPosition legge una posizione di ripresa.
//
// Restituisce [ErrInvalidCursor] su qualunque forma che non sia stata prodotta
// da [StreamPosition.Encode] — compreso un cursore di paginazione, che ha lo
// stesso involucro e un marcatore diverso.
func ParseStreamPosition(raw string) (StreamPosition, error) {
	parts, err := decodeCursor(raw, cursorKindStream, 3)
	if err != nil {
		return StreamPosition{}, err
	}
	nanos, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return StreamPosition{}, ErrInvalidCursor
	}
	attempt, err := strconv.Atoi(parts[2])
	if err != nil || attempt < 1 {
		return StreamPosition{}, ErrInvalidCursor
	}
	env := Environment(parts[1])
	if EnvironmentRank(env) == len(Environments) {
		// Un ambiente che non esiste farebbe fallire il cast della query con un
		// errore del database invece che con un rifiuto della richiesta.
		return StreamPosition{}, ErrInvalidCursor
	}
	return StreamPosition{
		ScheduledFor: time.Unix(0, nanos).UTC(),
		Environment:  env,
		Attempt:      attempt,
	}, nil
}

// PositionOf è la posizione di un'esecuzione dentro il flusso del proprio job.
func PositionOf(exec Execution) StreamPosition {
	return StreamPosition{
		ScheduledFor: exec.ScheduledFor,
		Environment:  exec.Environment,
		Attempt:      exec.Attempt,
	}
}

// Settled indica un'esecuzione che non cambierà più stato.
//
// Conta per il flusso più di quanto sembri: `pending` e `running` sono stati che
// la stessa riga attraversa, e una riga aggiornata **sul posto** non si può
// consegnare come un fatto definitivo — chi la ricevesse con quel `id:` e poi si
// riconnettesse non rivedrebbe mai l'esito. Vedi internal/execstream, dove la
// distinzione decide quali eventi portano una posizione e quali no.
func Settled(status ExecutionStatus) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusTimedOut, StatusSkipped:
		return true
	default:
		return false
	}
}

// ------------------------------------------------------------------ apertura

// StreamBacklog è quanto indietro guarda un flusso aperto senza posizione.
//
// Non è zero, e la ragione è che `scheduled_for` è l'istante **teorico**
// dell'occorrenza, non quello in cui è partita: un'esecuzione già in corso
// all'apertura ha una posizione di poco anteriore ad adesso, e con una finestra
// che parte esattamente da `now` resterebbe invisibile fino alla successiva.
// Un minuto copre la tolleranza del dispatch e la risoluzione più larga del
// listino senza consegnare uno storico che il client non ha chiesto — quello si
// legge da `GET /jobs/{id}/executions`, che è paginato apposta.
const StreamBacklog = time.Minute

// StreamOptions sono i parametri di apertura di un flusso.
type StreamOptions struct {
	// Resume è il `Last-Event-ID` mandato dal client alla riconnessione. Vuoto
	// significa «apri un flusso nuovo».
	Resume string

	// Since è l'inizio esplicito della finestra, quando il client ne chiede uno.
	// È soggetto alla retention del piano come qualunque altra lettura (R10-bis).
	Since time.Time
}

// Follower è la lettura in avanti del registro di un job **già autorizzato**.
//
// Esiste come tipo, e non come una coppia di argomenti da ripassare a ogni
// lettura, per una ragione sola: chi legge in un ciclo — il flusso lo fa una
// volta al secondo, per tutta la durata della connessione — non deve rifare il
// controllo di proprietà del job a ogni giro, e non deve nemmeno poterlo
// saltare. L'autorizzazione avviene **una volta**, in [Service.OpenStream], e da
// lì in poi è incorporata nell'oggetto: non c'è un metodo che legga il registro
// di un job arbitrario senza passare da lì.
//
// Il prezzo dichiarato è che il piano e la proprietà del job sono fotografati
// all'apertura. È il motivo per cui la connessione ha una vita massima: vedi
// internal/httpapi, dove la riconnessione periodica rilegge sessione e piano.
type Follower struct {
	svc   *Service
	jobID string
	plan  Plan

	// floor è il confine inferiore della finestra, inclusivo. Si muove con la
	// retention: vedi [Follower.Next].
	floor time.Time

	// start è la posizione da cui riprendere, esclusiva. Nil per un flusso nuovo.
	start *StreamPosition
}

// JobID è il job osservato.
func (f *Follower) JobID() string { return f.jobID }

// Start è la posizione da cui il flusso riprende, o nil se parte dal fondo della
// finestra.
func (f *Follower) Start() *StreamPosition { return f.start }

// OpenStream autorizza un flusso sul registro di un job e ne fissa l'inizio.
//
// Fa, una volta sola, i tre controlli che la lettura paginata fa a ogni pagina:
// il job è dell'utente (senza, l'identificativo di un job altrui darebbe accesso
// a URL, stati ed estratti di risposta), la finestra sta dentro la retention del
// piano (R10-bis), e la posizione di ripresa è leggibile.
//
// Il rifiuto per retention è lo stesso di `GET /jobs/{id}/executions`, ed è
// deliberato che lo sia anche quando arriva da un `Last-Event-ID`: un client che
// si riconnette dopo essere stato via più della retention del proprio piano non
// può ottenere quelle righe, e dirglielo è meglio che consegnargli in silenzio
// una finestra ristretta di cui non conosce il confine.
func (s *Service) OpenStream(ctx context.Context, userID, jobID string, opts StreamOptions) (*Follower, error) {
	job, err := s.store.JobByID(ctx, userID, jobID)
	if err != nil {
		return nil, err
	}
	plan, err := s.store.PlanForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	now := s.now()
	follower := &Follower{svc: s, jobID: job.ID, plan: plan}

	switch {
	case opts.Resume != "":
		position, err := ParseStreamPosition(opts.Resume)
		if err != nil {
			return nil, err
		}
		if err := plan.CheckRetention(position.ScheduledFor, time.Time{}, now); err != nil {
			return nil, err
		}
		follower.start = &position
		follower.floor = position.ScheduledFor

	case !opts.Since.IsZero():
		if err := plan.CheckRetention(opts.Since, time.Time{}, now); err != nil {
			return nil, err
		}
		follower.floor = opts.Since

	default:
		// Nessuna finestra chiesta: si parte da poco prima di adesso, che è ciò
		// che «in tempo reale» significa. Il confine resta comunque dentro la
		// retention, che [Follower.Next] riapplica a ogni lettura.
		follower.floor = now.Add(-StreamBacklog)
	}

	return follower, nil
}

// Next legge le esecuzioni successive alla posizione indicata, in ordine
// crescente.
//
// `from` nil significa «dall'inizio della finestra». Il numero di righe è
// esattamente `limit`: chi chiama torna a chiedere al giro dopo, e non c'è
// nessuna riga in più da cui dedurre una pagina successiva — un flusso non
// finisce mai.
//
// La retention si riapplica qui e non solo all'apertura (R10-bis): una
// connessione che resta aperta più a lungo della retention del piano
// consegnerebbe altrimenti righe che il piano non conserva più. Il confine è
// [Plan.RetentionFloor], lo stesso che applica la cancellazione periodica.
func (f *Follower) Next(ctx context.Context, from *StreamPosition, limit int) ([]Execution, error) {
	if limit <= 0 {
		limit = DefaultPageSize
	}

	since := f.floor
	if retention := f.plan.RetentionFloor(f.svc.now()); !retention.IsZero() && retention.After(since) {
		since = retention
	}

	filter := ExecutionFilter{
		JobID: f.jobID,
		Since: since,
		Limit: limit,
	}
	if from != nil {
		filter.Cursor = &ExecutionCursor{
			ScheduledFor: from.ScheduledFor,
			Environment:  from.Environment,
			Attempt:      from.Attempt,
		}
	}
	return f.svc.store.ListExecutionsForward(ctx, filter)
}
