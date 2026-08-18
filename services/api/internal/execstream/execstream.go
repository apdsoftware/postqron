// Package execstream è la sorgente dello streaming in tempo reale del registro
// delle esecuzioni (SPEC §4.2, R6).
//
// Non conosce HTTP: qui c'è chi decide **cosa** consegnare e **quando**, mentre
// come si scrive un evento Server-Sent Events sta in internal/httpapi. La
// divisione è la stessa di internal/jobs rispetto a internal/httpapi, e serve a
// che le proprietà difficili — la ripresa senza buchi, il client lento, il tetto
// alle connessioni — si verifichino senza una connessione di rete vera.
//
// # Il vincolo che ha deciso il progetto: una VPS sola
//
// API, motore e database stanno sulla stessa macchina (SPEC §2). Una connessione
// aperta è una risorsa trattenuta, e il modo più semplice di rovinare tutto è
// farne trattenere una **del pool PostgreSQL**: cento dashboard aperte
// esaurirebbero `max_connections` e farebbero fallire, a caso, tutto il resto del
// servizio. Le tre forme che lo provocano sono note e qui non ce n'è nessuna:
//
//   - `LISTEN/NOTIFY`, che pretende una connessione dedicata per ascoltatore;
//   - una transazione o un cursore lasciati aperti per la durata del flusso;
//   - un poller **per connessione**, che a un secondo di periodo e cento
//     connessioni chiede cento acquisizioni contemporanee a un pool che ne ha
//     una dozzina, e le fa aspettare tutte.
//
// Quello che c'è invece è **una sola goroutine di lettura per l'intero
// processo**, che a ogni battito scorre le sottoscrizioni una dopo l'altra. Il
// numero di connessioni del pool usate dallo streaming è quindi **uno**, in
// qualunque istante e con qualunque numero di client. È il vincolo che questo
// package esiste per garantire, e non è un'ottimizzazione: è la differenza fra
// una funzionalità e un guasto intermittente da diagnosticare per giorni.
//
// Il prezzo è che il ritardo di consegna cresce con il numero di flussi: a
// [DefaultInterval] e [DefaultMaxStreams] il giro completo è di duecento query
// puntuali sull'indice, largamente sotto il battito. Se un giro sfora, il
// battito successivo parte in ritardo e nessuno perde niente — ogni
// sottoscrizione ha il proprio cursore, quindi un giro saltato si recupera al
// giro dopo, non si perde.
//
// # La consegna: due tipi di evento, e uno solo porta la posizione
//
// Una riga di `job_executions` viene **aggiornata sul posto**: nasce `pending`,
// diventa `running`, si chiude in uno stato terminale. Consegnare uno stato
// intermedio con un identificativo di ripresa sarebbe un errore, e non sottile:
// il client che si riconnettesse da lì non rivedrebbe mai l'esito, perché la
// ripresa è «tutto ciò che viene dopo» e l'esito sta sulla **stessa** riga.
//
// Quindi:
//
//   - un'esecuzione **conclusa** viaggia con la propria posizione come `id:`, ed
//     è un fatto definitivo;
//   - un'esecuzione **in corso** viaggia senza `id:`. Non è una dimenticanza: SSE
//     dice che un evento privo di `id` non cambia il `Last-Event-ID` del client,
//     quindi lo stato intermedio si vede in dashboard senza entrare nella storia
//     ripresa alla riconnessione. È esattamente il meccanismo giusto usato per la
//     ragione giusta.
//
// # Il cursore avanza solo sul prefisso concluso
//
// Le occorrenze non si concludono per forza nell'ordine in cui sono
// programmate: con `on_overlap: allow` (R41) una lunga può finire dopo una
// breve che la segue. Se il cursore saltasse alla più recente conclusa, quella
// rimasta indietro non verrebbe mai consegnata a chi si riconnette — un buco.
//
// Il cursore avanza perciò solo lungo il **prefisso contiguo di esecuzioni
// concluse**, e si ferma alla prima ancora in corso. Oltre la testa si consegna
// soltanto ciò che è **ancora in corso**: un'esecuzione già conclusa che sta più
// avanti aspetta il proprio turno, perché consegnarla subito senza posizione e
// poi di nuovo con la posizione vorrebbe dire mandare due volte lo stesso fatto.
// I due insiemi restano così disgiunti — senza `id:` solo gli stati intermedi,
// con `id:` solo quelli definitivi — ed è da lì che discende «nessun doppione».
//
// Il predefinito dichiarato di R41 è `skip`, e `queue` serializza: per i job che
// non chiedono esplicitamente la sovrapposizione il caso non si presenta mai, e
// per quelli che la chiedono l'attesa è al più il timeout dell'occorrenza che
// sta davanti (R40) — durante il quale l'esecuzione in corso è comunque visibile.
//
// Ne segue la garanzia che conta, ed è la ragione di tutto quanto sopra: **fra
// una connessione e la sua ripresa non c'è né un buco né un doppione.** Ogni
// esecuzione conclusa è consegnata una volta sola, in ordine, con una posizione
// da cui riprendere.
package execstream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// I tetti tecnici dello streaming (R10).
//
// **Nessuno di questi è una riga di listino, e non deve diventarlo.** SPEC §8
// non dichiara un numero di connessioni, e inventarne uno per piano sarebbe una
// decisione commerciale presa dal codice. Sono numeri d'esercizio, della stessa
// famiglia di quelli di internal/httpapi/quota.go: difendono la macchina, sono
// uguali su tutti i piani, e il rifiuto che ne consegue non nomina nessun piano.
const (
	// DefaultMaxPerUser è il numero di flussi che un utente può tenere aperti.
	//
	// Cinque perché un flusso è **per job**: la dashboard ne apre uno per la
	// pagina del job che si sta guardando, e cinque schede aperte insieme sono
	// già un uso generoso. Sopra questo numero non c'è un utente che guarda, c'è
	// un client che apre connessioni e non le chiude — che è precisamente ciò che
	// il tetto deve riconoscere.
	DefaultMaxPerUser = 5

	// DefaultMaxStreams è il numero di flussi aperti nell'intero processo.
	//
	// Duecento è il tetto della macchina, non dell'utente. Ogni flusso costa una
	// goroutine di scrittura, un descrittore di file e un buffer di
	// [DefaultBuffer] eventi: dell'ordine di qualche decina di kilobyte, quindi
	// una manciata di megabyte in tutto. Ciò che **non** costa è una connessione
	// del pool, ed è per quello che il numero può essere così alto: vedi la nota
	// in testa al package.
	DefaultMaxStreams = 200

	// DefaultBuffer è quanti eventi si accodano per un client che non legge.
	//
	// Sessantaquattro è abbastanza per assorbire una raffica — un job al secondo
	// che si conclude mentre la rete del client fa una pausa — e abbastanza poco
	// da non diventare memoria trattenuta per conto di qualcuno che non c'è più.
	// Oltre, la connessione si chiude: vedi [Stream.Events].
	DefaultBuffer = 64

	// DefaultBatch è il numero massimo di righe lette per flusso a ogni battito.
	//
	// È anche il tetto alla memoria dello stato per flusso: lo stato è ciò che si
	// è già consegnato delle righe **non ancora concluse**, e non può contenere
	// più di quante se ne leggono in un giro.
	DefaultBatch = 200
)

// DefaultInterval è il periodo del battito di lettura.
//
// Un secondo perché è la risoluzione più fine che il listino vende (SPEC §8,
// piano Team): interrogare più spesso di quanto una cosa possa succedere è
// lavoro speso per non vedere niente di nuovo. È anche il ritardo massimo con
// cui un'esecuzione conclusa compare in dashboard, ed è sotto la soglia in cui
// una persona percepisce un'attesa.
const DefaultInterval = time.Second

// pollTimeout è il tetto a una singola lettura.
//
// Il giro è sequenziale: una lettura che non torna terrebbe fermi tutti gli
// altri flussi. Cinque secondi coincidono con [dispatch.DefaultStoreTimeout] e
// misurano la stessa cosa — una query puntuale sull'indice che non torna in
// cinque secondi ha un problema che non si risolve aspettando ancora.
const pollTimeout = 5 * time.Second

// maxPollFailures è quante letture consecutive fallite un flusso sopporta.
//
// Non una: un errore isolato è il database che si riavvia o una connessione del
// pool caduta, e chiudere la connessione del client per quello significherebbe
// far ricominciare tutte le dashboard a ogni singhiozzo. Non infinite: un flusso
// che non riesce più a leggere non sta streammando niente, e tenerlo aperto
// mentirebbe al client — meglio chiuderlo e lasciare che si riconnetta, che è
// ciò che SSE fa da solo.
const maxPollFailures = 5

// streamCeilingRetryAfter è cosa si risponde a chi ha sbattuto contro il tetto
// delle connessioni.
//
// Non è ricavato da un contatore, perché non c'è: il posto si libera quando
// qualcuno chiude una scheda, e quando succede non dipende da noi. Dieci secondi
// sono un'attesa che ha senso per un client che riprova da solo e non
// trasformano un rifiuto temporaneo in un invito a smettere.
const streamCeilingRetryAfter = 10 * time.Second

// Source è la lettura in avanti del registro di un job **già autorizzato**.
//
// L'implementazione d'esercizio è [*jobs.Follower], che incorpora il controllo
// di proprietà fatto all'apertura e il confine della retention (R10-bis). Questo
// package non sa autorizzare niente e non deve: se accettasse un identificativo
// di job avrebbe bisogno di saperlo, e ci sarebbe un secondo posto in cui
// sbagliare.
type Source interface {
	// Next restituisce fino a `limit` esecuzioni successive alla posizione
	// indicata, in ordine crescente. `from` nil significa «dall'inizio della
	// finestra».
	Next(ctx context.Context, from *jobs.StreamPosition, limit int) ([]jobs.Execution, error)
}

// Event è ciò che il flusso consegna.
type Event struct {
	// Execution è lo stato dell'esecuzione al momento della lettura.
	Execution jobs.Execution

	// ID è la posizione di ripresa, e **è vuoto per le esecuzioni non concluse**.
	// Vedi la nota in testa al package: un evento senza `id:` non sposta il
	// `Last-Event-ID` del client, che è ciò che rende la ripresa esatta.
	ID string
}

// Limits sostituisce i tetti predefiniti. Il campo zero usa il valore di
// esercizio: serve ai test, che devono poter provare il rifiuto senza aprire
// duecento connessioni.
type Limits struct {
	MaxPerUser int
	MaxStreams int
	Buffer     int
	Batch      int
	Interval   time.Duration
}

func (l Limits) orDefaults() Limits {
	if l.MaxPerUser <= 0 {
		l.MaxPerUser = DefaultMaxPerUser
	}
	if l.MaxStreams <= 0 {
		l.MaxStreams = DefaultMaxStreams
	}
	if l.Buffer <= 0 {
		l.Buffer = DefaultBuffer
	}
	if l.Batch <= 0 {
		l.Batch = DefaultBatch
	}
	if l.Interval <= 0 {
		l.Interval = DefaultInterval
	}
	return l
}

// Options sono le dipendenze dell'hub.
type Options struct {
	// Logger è obbligatorio.
	Logger *slog.Logger

	// Limits sostituisce i tetti predefiniti.
	Limits Limits

	// Tick sostituisce il battito. Nil usa un [time.Ticker] su
	// [Limits.Interval]; i test ne passano uno che scatta a comando, perché una
	// suite che aspetta un secondo per vedere un evento ha legato la CI
	// all'orologio invece che alla logica.
	Tick <-chan time.Time
}

// Hub tiene le sottoscrizioni aperte e le serve con **una sola** goroutine di
// lettura. Vedi la nota in testa al package per il perché è una sola.
type Hub struct {
	limits Limits
	log    *slog.Logger
	tick   <-chan time.Time

	stop     chan struct{}
	stopOnce sync.Once

	mu      sync.Mutex
	streams []*Stream
	perUser map[string]int
}

// New costruisce l'hub e avvia il **suo unico** ciclo di lettura.
//
// La goroutine parte subito e vive quanto l'hub, anche quando non c'è nessuna
// sottoscrizione. Farla nascere e morire con le sottoscrizioni sarebbe stato più
// parsimonioso di una goroutine ferma su un canale, e avrebbe aperto la
// possibilità che due cicli coesistano per un istante durante il passaggio da
// zero a uno — cioè che due lettori tocchino lo stato dello stesso flusso. Una
// goroutine in più per processo è un prezzo che non si nota; due lettori sono
// una corsa che si nota solo in produzione.
func New(opts Options) (*Hub, error) {
	if opts.Logger == nil {
		return nil, errors.New("execstream: il Logger è obbligatorio")
	}
	h := &Hub{
		limits:  opts.Limits.orDefaults(),
		log:     opts.Logger,
		tick:    opts.Tick,
		stop:    make(chan struct{}),
		perUser: map[string]int{},
	}
	go h.run()
	return h, nil
}

// Stop ferma il ciclo di lettura. È idempotente.
func (h *Hub) Stop() { h.stopOnce.Do(func() { close(h.stop) }) }

// errTooManyStreams è il rifiuto del tetto tecnico sulle connessioni aperte.
//
// È un [jobs.ServiceLimitError] e non un errore qualunque perché la forma della
// risposta deve essere quella di R10: nessun piano nominato, nessun invito
// all'upgrade, un `Retry-After` che dice che riprovare ha senso.
func errTooManyStreams(message string) error {
	return jobs.NewServiceLimit(jobs.LimitStreamCeiling, streamCeilingRetryAfter, message+
		" Il registro resta comunque leggibile da `GET /jobs/{id}/executions`.")
}

// Subscribe apre un flusso.
//
// Rifiuta con [jobs.ServiceLimitError] quando l'utente o il processo hanno
// raggiunto il proprio tetto. I due tetti sono due e non uno perché rispondono a
// domande diverse: quello per utente impedisce a un client di prendersi tutta la
// macchina, quello complessivo impedisce alla macchina di essere presa da molti
// client che si comportano bene.
//
// Il chiamante **deve** chiamare [Stream.Close], anche sui percorsi d'errore:
// senza, il posto resta occupato per sempre e il tetto si abbassa da solo fino a
// zero.
func (h *Hub) Subscribe(userID string, src Source, start *jobs.StreamPosition) (*Stream, error) {
	if src == nil {
		return nil, errors.New("execstream: la sorgente è obbligatoria")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.check(userID); err != nil {
		return nil, err
	}

	stream := &Stream{
		hub:    h,
		userID: userID,
		src:    src,
		events: make(chan Event, h.limits.Buffer),
		done:   make(chan struct{}),
		seen:   map[jobs.StreamPosition]jobs.ExecutionStatus{},
	}
	if start != nil {
		position := *start
		stream.cursor = &position
	}

	h.streams = append(h.streams, stream)
	h.perUser[userID]++
	return stream, nil
}

// Admit verifica i tetti **senza** aprire niente.
//
// Serve a rifiutare prima del lavoro, com'è la regola di R10: aprire un flusso
// costa la lettura del job e quella del piano, e un rifiuto emesso dopo le ha
// già spese. Non è la verifica autorevole — quella è dentro [Hub.Subscribe], che
// prende il lock una volta sola per contare e inserire — ed è giusto così: fra
// le due può passare qualcun altro, e in quel caso è la seconda a decidere.
func (h *Hub) Admit(userID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.check(userID)
}

// Open è il numero di flussi aperti. Serve alle prove e all'osservabilità.
func (h *Hub) Open() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.streams)
}

// check applica i due tetti. Va chiamata con il lock preso.
//
// I due tetti sono due e non uno perché rispondono a domande diverse: quello per
// utente impedisce a un client di prendersi tutta la macchina, quello
// complessivo impedisce alla macchina di essere presa da molti client che si
// comportano tutti bene.
func (h *Hub) check(userID string) error {
	if len(h.streams) >= h.limits.MaxStreams {
		// Nel log ci va, perché è la macchina a essere piena e non un utente a
		// esagerare: è il numero che dice se il tetto va rivisto.
		h.log.Warn("flusso rifiutato: tetto complessivo delle connessioni di streaming raggiunto",
			slog.Int("tetto", h.limits.MaxStreams))
		return errTooManyStreams(fmt.Sprintf(
			"il servizio ha %d flussi di log aperti, che è il suo tetto tecnico: "+
				"è uguale su tutti i piani e nessun piano ne concede di più. Riprova fra poco.",
			h.limits.MaxStreams))
	}
	if h.perUser[userID] >= h.limits.MaxPerUser {
		return errTooManyStreams(fmt.Sprintf(
			"hai già %d flussi di log aperti, che è il tetto tecnico del servizio: "+
				"è uguale su tutti i piani e nessun piano ne concede di più. "+
				"Chiudi una scheda della dashboard e riprova.",
			h.limits.MaxPerUser))
	}
	return nil
}

// remove libera il posto occupato da un flusso. È idempotente: la chiamano sia
// il chiamante (il client se n'è andato) sia il ciclo di lettura (il client non
// legge più), e scalare due volte lo stesso contatore abbasserebbe il tetto
// dell'utente di uno a ogni disconnessione, fino a zero.
func (h *Hub) remove(s *Stream) {
	h.mu.Lock()
	defer h.mu.Unlock()

	found := false
	for i, candidate := range h.streams {
		if candidate != s {
			continue
		}
		h.streams = append(h.streams[:i], h.streams[i+1:]...)
		found = true
		break
	}
	if !found {
		return
	}
	if h.perUser[s.userID]--; h.perUser[s.userID] <= 0 {
		delete(h.perUser, s.userID)
	}
}

// run è il ciclo di lettura: **uno per processo**, non uno per connessione.
func (h *Hub) run() {
	tick := h.tick
	if tick == nil {
		ticker := time.NewTicker(h.limits.Interval)
		defer ticker.Stop()
		tick = ticker.C
	}
	for {
		select {
		case <-h.stop:
			return
		case <-tick:
			h.Poll()
		}
	}
}

// Poll esegue un giro di lettura, scorrendo le sottoscrizioni **una alla
// volta**.
//
// La sequenzialità è la garanzia, non un dettaglio realizzativo: è ciò che rende
// vero che lo streaming usa al più una connessione del pool in qualunque
// istante. Parallelizzare qui sarebbe la modifica di una riga e riporterebbe
// esattamente il problema che questo package esiste per non avere.
//
// È esportata perché la chiamano in due: il ciclo dell'hub a ogni battito, e i
// test a comando. La seconda ragione non è cosmetica — una suite che aspettasse
// il battito vero per vedere un evento costerebbe un secondo per prova, per
// sempre, e legherebbe la CI all'orologio invece che alla logica.
func (h *Hub) Poll() {
	h.mu.Lock()
	streams := make([]*Stream, len(h.streams))
	copy(streams, h.streams)
	h.mu.Unlock()

	for _, stream := range streams {
		select {
		case <-h.stop:
			return
		default:
		}
		stream.advance(h)
	}
}

// ------------------------------------------------------------------ il flusso

// Stream è una sottoscrizione aperta.
type Stream struct {
	hub    *Hub
	userID string
	src    Source

	// events lo scrive **solo** il ciclo di lettura, ed è per questo che lo
	// chiude solo lui: chiudere un canale mentre qualcuno ci scrive è un panic,
	// non una corsa che si nota ogni tanto. Il chiamante che se ne va non lo
	// chiude — chiude `done`, che è un segnale e non un canale di dati.
	events chan Event
	done   chan struct{}

	closeOnce sync.Once
	endOnce   sync.Once

	// Da qui in giù si tocca **solo dal ciclo di lettura**, che è uno solo e
	// sequenziale: niente lock, e non per pigrizia — un lock qui suggerirebbe che
	// possa esistere un secondo lettore, che è precisamente ciò che non deve
	// esistere.
	cursor   *jobs.StreamPosition
	seen     map[jobs.StreamPosition]jobs.ExecutionStatus
	failures int
	overrun  atomicBool
}

// atomicBool è `overrun` letto dal chiamante e scritto dal ciclo di lettura.
// È l'unico campo che attraversa le due goroutine, e attraversarlo con una
// variabile nuda sarebbe una corsa che il `-race` della CI trova subito.
type atomicBool struct {
	mu    sync.Mutex
	value bool
}

func (b *atomicBool) set()      { b.mu.Lock(); b.value = true; b.mu.Unlock() }
func (b *atomicBool) get() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.value }

// Events è il canale degli eventi da scrivere sulla connessione.
//
// Si chiude quando il flusso finisce, e i due modi in cui finisce vanno
// distinti con [Stream.Overrun]:
//
//   - il chiamante ha chiuso, perché il client se n'è andato;
//   - **il client non legge abbastanza in fretta.** Il buffer è pieno e l'hub
//     smette di accodare: non si cresce all'infinito per conto di chi non
//     consuma, e non si blocca il ciclo di lettura — che è uno solo — aspettando
//     una scrittura sulla rete. La connessione si chiude, il client si
//     riconnette da solo e riprende dal proprio `Last-Event-ID` senza buchi. È il
//     motivo per cui la ripresa doveva funzionare prima ancora che qualcuno
//     chiedesse la riconnessione.
func (s *Stream) Events() <-chan Event { return s.events }

// Overrun indica che il flusso si è chiuso perché il client non leggeva.
func (s *Stream) Overrun() bool { return s.overrun.get() }

// Close libera il posto. È idempotente e va chiamata sempre, anche sui percorsi
// d'errore del chiamante.
//
// Non chiude il canale degli eventi: lo fa il ciclo di lettura, che è l'unico a
// scriverci. Vedi [Stream.events].
func (s *Stream) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.hub.remove(s)
	})
}

// end chiude il flusso dal lato del ciclo di lettura.
func (s *Stream) end() {
	s.hub.remove(s)
	s.endOnce.Do(func() { close(s.events) })
}

// advance legge il tratto successivo e ne consegna gli eventi.
//
// La regola sta tutta qui, e vale la pena leggerla accanto alla nota in testa al
// package: si avanza il cursore **solo** sul prefisso contiguo di esecuzioni
// concluse; alla prima ancora in corso il cursore si ferma, e da lì in poi si
// consegna senza posizione.
func (s *Stream) advance(h *Hub) {
	// Il chiamante può essersene andato fra la copia dell'elenco e adesso: una
	// lettura per un flusso già chiuso sarebbe una query pagata per niente.
	select {
	case <-s.done:
		return
	default:
	}

	ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
	defer cancel()

	rows, err := s.src.Next(ctx, s.cursor, h.limits.Batch)
	if err != nil {
		s.failures++
		h.log.Warn("lettura del flusso delle esecuzioni fallita",
			slog.String("user_id", s.userID),
			slog.Int("consecutivi", s.failures),
			slog.Any("error", err))
		if s.failures >= maxPollFailures {
			h.log.Error("flusso chiuso: la lettura del registro continua a fallire",
				slog.String("user_id", s.userID))
			s.end()
		}
		return
	}
	s.failures = 0

	// Lo stato di ciò che è già stato consegnato si ricostruisce a ogni giro
	// dalle righe appena lette: così è limitato dal lotto per costruzione, e non
	// c'è nessuna potatura da ricordarsi di fare. È la differenza fra una mappa
	// che non può crescere e una che non cresce finché nessuno sbaglia.
	seen := make(map[jobs.StreamPosition]jobs.ExecutionStatus, len(rows))
	head := true

	for _, exec := range rows {
		position := jobs.PositionOf(exec)
		settled := jobs.Settled(exec.Status)

		if head && settled {
			if !s.emit(Event{Execution: exec, ID: position.Encode()}) {
				// Il buffer è pieno: il cursore **non** avanza. Se avanzasse, il
				// client si riconnetterebbe da una posizione che non ha mai
				// ricevuto, e quello sarebbe un buco.
				s.overflow(h)
				return
			}
			cursor := position
			s.cursor = &cursor
			continue
		}
		head = false

		// Oltre la testa **si consegna solo ciò che non è concluso**, e questa è la
		// riga che rende vero «né un buco né un doppione». Un'esecuzione conclusa
		// che sta oltre la testa aspetta: consegnarla adesso senza posizione e poi
		// di nuovo con la posizione, quando la testa la raggiunge, vorrebbe dire
		// mandare due volte lo stesso fatto. Così invece i due insiemi sono
		// disgiunti — senza `id:` viaggiano solo gli stati intermedi, con `id:`
		// solo quelli definitivi — e nessuno stato viene consegnato due volte.
		if settled {
			continue
		}

		// Un'esecuzione in corso si consegna solo quando cambia stato: senza,
		// ogni battito rimanderebbe la stessa riga `running` finché non finisce.
		seen[position] = exec.Status
		if s.seen[position] == exec.Status {
			continue
		}
		if !s.emit(Event{Execution: exec}) {
			s.overflow(h)
			return
		}
	}
	s.seen = seen
}

func (s *Stream) emit(e Event) bool {
	select {
	case s.events <- e:
		return true
	default:
		return false
	}
}

// overflow chiude un flusso il cui client non legge. Vedi [Stream.Events].
func (s *Stream) overflow(h *Hub) {
	s.overrun.set()
	h.log.Info("flusso chiuso: il client non legge e il buffer è pieno",
		slog.String("user_id", s.userID),
		slog.Int("buffer", h.limits.Buffer))
	s.end()
}
