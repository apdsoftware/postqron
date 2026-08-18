package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/execstream"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Lo streaming in tempo reale del registro delle esecuzioni (SPEC §4.2).
//
//	GET /jobs/{id}/executions/stream    text/event-stream    200
//
// Scope `executions:read`, lo stesso del registro paginato: è la stessa lettura
// consegnata in un altro modo, e chiederne uno diverso costringerebbe a creare
// una seconda chiave per vedere le stesse righe.
//
// # Il contratto sul filo
//
//	retry: 3000                      ritardo di riconnessione suggerito
//	: ping                           battito (commento SSE, nessun evento)
//	event: execution                 un'esecuzione, nella forma di ExecutionResponse
//	id: <posizione>                  presente solo sulle esecuzioni concluse
//	event: overflow                  il client non leggeva: si riconnetta
//	event: reopen                    fine della vita della connessione: si riconnetta
//
// Il corpo di `execution` è **la stessa struttura** del registro paginato,
// prodotta dalla **stessa funzione** ([executionResponse]). Non è una comodità:
// lo streaming è una via d'uscita nuova per dati che passano dalla redazione dei
// segreti del workspace (R43), e due proiezioni parallele sarebbero due punti in
// cui dimenticarsene — la seconda delle quali senza nessuno che la guardi. Ciò
// che esce di qui è, byte per byte, ciò che esce da `GET
// /jobs/{id}/executions`; la prova end-to-end sta in
// cmd/api/engine_test.go.
//
// # Perché `id:` non c'è sempre
//
// È la scelta che fa funzionare la riconnessione, ed è spiegata per esteso in
// internal/execstream: una riga di `job_executions` viene aggiornata sul posto,
// quindi solo uno stato **definitivo** può essere una posizione da cui
// riprendere. SSE dice che un evento senza `id:` non sposta il `Last-Event-ID`
// del client — quindi lo stato intermedio si vede, e non entra nella storia.
//
// # Errori all'apertura
//
// Sono quelli del registro paginato, con l'aggiunta del tetto sulle connessioni.
// Arrivano **prima** che il flusso cominci, quindi come JSON e con lo status
// giusto: dopo il primo byte di `text/event-stream` non c'è più modo di
// cambiare status, ed è per questo che tutto ciò che può fallire viene fatto
// prima di scrivere qualunque cosa.
//
//	400 invalid_cursor              `Last-Event-ID` illeggibile
//	400 validation_failed           `since` non è un istante RFC 3339
//	403 plan_limit_retention        la ripresa precede la retention del piano (R10-bis)
//	404 job_not_found               inesistente, oppure di un altro utente
//	429 stream_ceiling              tetto tecnico sulle connessioni aperte (R10)

// Tempi della connessione. Sono nel codice e non in configurazione per la stessa
// ragione dei tetti di quota.go: sono scelte d'esercizio, non parametri da
// girare in produzione.
const (
	// defaultHeartbeat è ogni quanto si manda un battito su una connessione
	// silenziosa.
	//
	// **Serve a due cose diverse, e il numero viene dalla più severa.**
	//
	// La prima è attraversare i proxy. In produzione davanti all'API c'è
	// Cloudflare (SPEC §2), che chiude una connessione proxata senza traffico
	// dopo un centinaio di secondi; nginx, che qualcuno metterà davanti prima o
	// poi, ha un `proxy_read_timeout` predefinito di 60 secondi. Venti secondi
	// stanno sotto il più corto dei due con un margine di tre battiti: due
	// battiti persi non bastano a far cadere la connessione.
	//
	// La seconda è **accorgersi del client sparito**. Un browser chiuso male non
	// manda un saluto: il socket resta aperto dalla nostra parte e nessuno ce lo
	// dice. L'unico modo di scoprirlo è scriverci sopra, e su una connessione
	// senza eventi il battito è l'unica scrittura che avviene. Senza, un flusso su
	// un job silenzioso resterebbe appeso fino al timeout di TCP, che si misura in
	// ore. Vedi [defaultWriteTimeout] per che cosa succede quando la scrittura non
	// passa.
	defaultHeartbeat = 20 * time.Second

	// defaultWriteTimeout è quanto si aspetta che una scrittura passi.
	//
	// È l'altra metà del rilevamento del client sparito. Quando il client non c'è
	// più, la scrittura riempie il buffer del socket e poi si blocca: senza una
	// scadenza, la goroutine che serve quella connessione resterebbe lì per
	// sempre — cioè il client sparito costerebbe una risorsa trattenuta a tempo
	// indeterminato, che è esattamente ciò che questo file deve evitare. Dieci
	// secondi sono larghissimi per scrivere qualche centinaio di byte su una
	// connessione viva, anche lenta.
	defaultWriteTimeout = 10 * time.Second

	// defaultLifetime è la vita massima di una connessione.
	//
	// Non è una difesa dalle risorse — a quello serve il tetto sul numero di
	// connessioni — ma **il modo in cui le credenziali e il piano restano
	// freschi**. Il flusso autorizza il job e legge il piano una volta sola, per
	// costruzione (vedi [jobs.Follower]): una connessione che vivesse un giorno
	// continuerebbe a consegnare righe a una sessione scaduta e con la retention
	// del piano che l'utente aveva ieri. Alla scadenza si chiude con `reopen`, il
	// client si riconnette da solo con il proprio `Last-Event-ID` e riprende senza
	// buchi — che è precisamente la proprietà che la riconnessione garantisce, qui
	// usata da noi invece che da un guasto della rete.
	defaultLifetime = 30 * time.Minute

	// clientRetry è il ritardo di riconnessione suggerito al client, in
	// millisecondi. Tre secondi: abbastanza da non trasformare un riavvio dell'API
	// in una raffica di riconnessioni, abbastanza poco da non far sembrare rotta
	// la dashboard.
	clientRetry = 3000
)

// StreamTimings sostituisce i tempi della connessione SSE.
//
// Il campo zero usa i valori d'esercizio. Esiste per la stessa ragione di
// [RateLimits]: una suite che aspettasse trenta minuti per verificare che la
// connessione si chiude avrebbe legato la CI all'orologio invece che alla logica.
type StreamTimings struct {
	Heartbeat    time.Duration
	WriteTimeout time.Duration
	Lifetime     time.Duration
}

func (t StreamTimings) orDefaults() StreamTimings {
	if t.Heartbeat <= 0 {
		t.Heartbeat = defaultHeartbeat
	}
	if t.WriteTimeout <= 0 {
		t.WriteTimeout = defaultWriteTimeout
	}
	if t.Lifetime <= 0 {
		t.Lifetime = defaultLifetime
	}
	return t
}

// streamExecutions serve il flusso.
func (a *jobsAPI) streamExecutions(w http.ResponseWriter, r *http.Request, identity Identity) {
	if a.streams == nil {
		writeError(w, r, a.log, http.StatusServiceUnavailable, "stream_unavailable",
			"Lo streaming dei log non è disponibile su questo servizio. Il registro resta leggibile da `GET /jobs/{id}/executions`.")
		return
	}

	// Il tetto tecnico prima del lavoro (R10): aprire un flusso costa la lettura
	// del job e quella del piano, e un rifiuto emesso dopo le ha già spese.
	if err := a.streams.Admit(identity.UserID()); err != nil {
		a.fail(w, r, err)
		return
	}

	opts, ok := a.streamOptions(w, r)
	if !ok {
		return
	}

	follower, err := a.svc.OpenStream(r.Context(), identity.UserID(), r.PathValue("id"), opts)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	stream, err := a.streams.Subscribe(identity.UserID(), follower, follower.Start())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// Sempre, anche sui percorsi d'errore: un posto non liberato abbassa il tetto
	// dell'utente di uno per sempre.
	defer stream.Close()

	a.pump(w, r, stream)
}

// streamOptions legge la posizione di ripresa e la finestra richiesta.
//
// `Last-Event-ID` è la testata che il browser rimanda da solo alla
// riconnessione; `?last_event_id=` esiste perché `EventSource` non permette di
// impostare testate, quindi un client che riprende *dopo un errore applicativo*
// — invece che dopo una caduta della rete — non avrebbe altro modo di dire da
// dove ripartire.
func (a *jobsAPI) streamOptions(w http.ResponseWriter, r *http.Request) (jobs.StreamOptions, bool) {
	query := r.URL.Query()

	resume := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if resume == "" {
		resume = strings.TrimSpace(query.Get("last_event_id"))
	}

	invalid := &queryErrors{}
	since := invalid.timestamp("since", query.Get("since"))
	if invalid.fail(w, r, a.log) {
		return jobs.StreamOptions{}, false
	}
	return jobs.StreamOptions{Resume: resume, Since: since}, true
}

// pump scrive il flusso finché c'è qualcuno che lo legge.
func (a *jobsAPI) pump(w http.ResponseWriter, r *http.Request, stream *execstream.Stream) {
	timings := a.timings

	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	// `no-store` e non `no-cache`: un flusso non ha una versione da rivalidare.
	header.Set("Cache-Control", "no-store")
	// Per i reverse proxy che accumulano la risposta prima di inoltrarla: senza,
	// gli eventi arriverebbero a blocchi e «in tempo reale» sarebbe una bugia.
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writer := &sseWriter{w: w, controller: http.NewResponseController(w), timeout: timings.WriteTimeout}

	// Il ritardo di riconnessione si dichiara subito, prima di qualunque evento:
	// se la connessione cade al primo secondo, il client deve già saperlo.
	if !writer.raw("retry: " + strconv.Itoa(clientRetry) + "\n\n") {
		return
	}

	heartbeat := time.NewTicker(timings.Heartbeat)
	defer heartbeat.Stop()
	lifetime := time.NewTimer(timings.Lifetime)
	defer lifetime.Stop()

	for {
		select {
		case <-r.Context().Done():
			// Il client ha chiuso davvero, oppure il processo si sta fermando.
			return

		case event, open := <-stream.Events():
			if !open {
				// L'hub ha chiuso il flusso. L'unico motivo che il client può
				// rimediare è di non aver letto abbastanza in fretta, e vale la pena
				// dirglielo: si riconnetterà dal proprio `Last-Event-ID` e non perderà
				// niente, ma se accade spesso il problema è suo.
				if stream.Overrun() {
					writer.event("overflow", "",
						`{"code":"stream_overflow","message":"Il client non stava leggendo abbastanza in fretta: riconnettiti, riprenderai da dove eri."}`)
				}
				return
			}
			payload, err := json.Marshal(executionResponse(event.Execution))
			if err != nil {
				// Non può succedere con questa struttura, e se succedesse
				// interrompere il flusso sarebbe peggio che saltare un evento — il
				// client lo rivedrà comunque nel registro paginato.
				a.log.ErrorContext(r.Context(), "esecuzione non serializzabile per il flusso",
					slog.Any("error", err))
				continue
			}
			if !writer.event("execution", event.ID, string(payload)) {
				return
			}

		case <-heartbeat.C:
			// Un commento SSE: attraversa i proxy come traffico e non arriva al
			// gestore degli eventi del client. Vedi [defaultHeartbeat].
			if !writer.raw(": ping\n\n") {
				return
			}

		case <-lifetime.C:
			writer.event("reopen", "",
				`{"code":"stream_reopen","message":"Fine della vita della connessione: riconnettiti, riprenderai da dove eri."}`)
			return
		}
	}
}

// ------------------------------------------------------------------ scrittura

// sseWriter scrive gli eventi con una scadenza.
//
// La scadenza è ciò che rende il client sparito un problema che si risolve da
// sé: senza, una scrittura verso un socket che non drena resterebbe bloccata a
// tempo indeterminato, e la goroutine che serve quella connessione con lei. Ogni
// metodo restituisce falso quando la connessione non è più scrivibile, e il
// chiamante esce — chiudendo la richiesta, cioè il socket.
type sseWriter struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
}

func (s *sseWriter) raw(payload string) bool {
	// Su un [httptest.ResponseRecorder] non c'è nessuna scadenza da impostare e
	// il controller lo dice con [http.ErrNotSupported]. Non è un guasto: è il
	// caso dei test, e trattarlo come un errore renderebbe non verificabile tutto
	// il resto.
	if err := s.controller.SetWriteDeadline(time.Now().Add(s.timeout)); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		return false
	}
	if _, err := s.w.Write([]byte(payload)); err != nil {
		return false
	}
	// Senza flush il pacchetto resta nel buffer di `net/http` finché non si
	// riempie: su un flusso di eventi radi vorrebbe dire consegnarli a manciate,
	// cioè non consegnarli in tempo reale.
	if err := s.controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return false
	}
	return true
}

// event scrive un evento. `id` vuoto omette il campo, che è il modo con cui SSE
// dice «questo non sposta la posizione di ripresa».
func (s *sseWriter) event(name, id, data string) bool {
	var b strings.Builder
	b.WriteString("event: ")
	b.WriteString(name)
	b.WriteByte('\n')
	if id != "" {
		b.WriteString("id: ")
		b.WriteString(id)
		b.WriteByte('\n')
	}
	b.WriteString("data: ")
	b.WriteString(data)
	b.WriteString("\n\n")
	return s.raw(b.String())
}
