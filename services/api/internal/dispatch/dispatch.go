// Package dispatch esegue le occorrenze che lo scheduler accoda (R3, R4).
//
// Sta subito a valle di internal/scheduler e ne implementa l'interfaccia
// [scheduler.Dispatcher]: lo scheduler decide *che cosa* è dovuto e scrive la
// riga `pending` su `job_executions`; questo package decide *quando* e *con
// quale worker* eseguirla, e porta quella riga fino a uno stato terminale.
//
// # L'isolamento è il requisito, non un effetto collaterale
//
// R3 non chiede «più goroutine»: chiede che **un job lento non ritardi gli
// altri**. La distinzione conta perché la distribuzione dei tempi di esecuzione
// è sbilanciata per costruzione — mille webhook che rispondono in un secondo
// convivono con un job che ha un timeout di sessanta — e la forma ovvia del
// worker pool, una coda FIFO condivisa fra N worker, non regge quel carico:
//
//   - un job a `every: 1s` che impiega 60 secondi accumula arretrato, e la sua
//     coda finisce davanti a quella di tutti gli altri;
//   - i worker si riempiono di occorrenze di quel solo job, perché sono le
//     prime della fila;
//   - da lì in poi ogni job rapido aspetta che si liberi un worker occupato per
//     un minuto. Il job lento non ha rallentato sé stesso: ha rallentato il
//     servizio.
//
// Le due scelte che lo impediscono sono nella coda ([queue]), non nei worker:
//
//  1. **Una coda per job, servita a turno.** L'ordine di uscita non è quello di
//     arrivo ma un round robin fra i job che hanno lavoro pronto. L'arretrato di
//     un job resta un fatto suo: non si mette davanti a nessuno.
//  2. **Un tetto di esecuzioni in volo per job** ([Options.MaxInFlightPerJob]).
//     È ciò che impedisce a un solo job di tenere occupato l'intero pool anche
//     quando è l'unico ad avere lavoro pronto. Senza questo tetto il round robin
//     da solo non basta: con la coda di un job sola non c'è nessuno con cui
//     alternarsi.
//
// C'è una terza scelta, meno visibile ma dello stesso peso: **il worker non
// tiene una connessione al database mentre la chiamata HTTP è in corso**. Prende
// la riga, la chiude, esegue, riapre per scrivere l'esito. Un pool da 32 worker
// che tenesse la connessione per tutta la durata della chiamata esaurirebbe le
// 10 connessioni di internal/database al terzo job lento, e da lì in poi
// rallenterebbe anche l'API.
//
// # Il cancello di R4
//
// Lo scheduler garantisce che un'occorrenza *nasca* una volta sola: la chiave
// primaria naturale di `job_executions` glielo assicura. Non garantisce che
// venga *consegnata* una volta sola — la terza clausola di [scheduler.Dispatcher]
// dice esplicitamente il contrario: una riga rimasta `pending` viene riofferta,
// ed è così che un riavvio non perde lavoro.
//
// Il secondo cancello è qui, ed è l'aggiornamento condizionato di
// [PostgresStore.Claim]:
//
//	UPDATE job_executions SET status = 'running', started_at = now()
//	 WHERE job_id = $1 AND scheduled_for = $2 AND environment = $3
//	   AND attempt = $4 AND status = 'pending'
//
// Si esegue **solo se quell'UPDATE ha toccato una riga**. Chi arriva secondo
// trova `running` — o `skipped`, se lo scheduler l'ha nel frattempo dichiarata
// scaduta — non aggiorna niente, e si ferma senza fare la chiamata. La
// condizione non è un'ottimizzazione: toglierla significa che due processi, o lo
// stesso processo dopo una ripresa, chiamano due volte il bersaglio dell'utente.
//
// # L'arresto
//
// Alla chiusura ci sono tre popolazioni, e ognuna ha una risposta diversa:
//
//   - **In coda, mai prese.** Si buttano. Le loro righe sono ancora `pending` e
//     nessuno le ha toccate: sono esattamente ciò che [scheduler.Engine.Recover]
//     ritrova al riavvio, per la via che lo scheduler ha già previsto. Scrivere
//     qualcosa su di esse sarebbe lavoro inutile su un processo che sta uscendo.
//   - **In volo, che fanno in tempo.** Si aspettano fino a
//     [Options.DrainTimeout] e finiscono normalmente, con il loro esito vero.
//   - **In volo, che non fanno in tempo.** Si annulla il loro contesto e la riga
//     torna a `pending` con [PostgresStore.Release]. Da lì la riprende lo stesso
//     recupero di prima: `job_executions_in_flight_idx` (migrazione 0006) copre
//     sia `pending` sia `running`, e la lettura del recupero filtra su
//     `created_at`, che una riga rilasciata non cambia — quindi viene riofferta
//     alla prima passata utile, non dopo [scheduler.DefaultReclaimAfter].
//
// Il compromesso del terzo caso è dichiarato: un'occorrenza rilasciata **può
// aver già raggiunto il bersaglio**. La garanzia esatta di R4 è sulla riga, non
// sull'effetto lato utente; oltre il tempo di drenaggio si preferisce rieseguire
// piuttosto che lasciare un'esecuzione `running` per sempre — che sarebbe una
// riga che nessuno chiude più, dentro l'indice che deve restare piccolo, e
// un'esecuzione che l'utente vede eternamente in corso.
//
// # Che cosa si scrive nella riga, e perché non è una stringa
//
// I due testi che l'esecuzione lascia sulla riga — l'estratto della risposta e
// il testo dell'errore — sono [secrets.Excerpt], non `string`. Questo package
// non conosce i segreti del workspace e non può redigerli: li risolve chi
// esegue, ed è quindi chi esegue a doverli togliere. Un tipo che non si può
// costruire senza passare dalla redazione è il modo di chiederglielo una volta
// sola, al compilatore, invece che a ogni revisione (R43, issue #496).
//
// La conseguenza pratica sta in [record]: il messaggio dell'`error` restituito
// dall'esecutore **non viene mai scritto**. L'errore serve a classificare
// l'esito, il testo arriva da [Result.ErrorText].
//
// # Che cosa non c'è
//
// L'esecuzione HTTP vera è la issue #390 e sta dietro [Executor]; il blocco
// degli indirizzi interni (R38) è la #455 e sta dietro [Guard], che questo
// package chiama a ogni occorrenza e che di default non blocca niente; il retry
// (R5) è la #392 e nascerà da [Outcome], non da qui.
package dispatch

import (
	"context"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// Outcome è l'esito di un'occorrenza. I valori coincidono con quelli del tipo
// `execution_status` (migrazione 0001) e ci vengono scritti così come sono:
// tenerli allineati per costruzione evita una tabella di traduzione che
// diverge.
type Outcome string

const (
	// Succeeded è una risposta HTTP sotto il 400.
	Succeeded Outcome = "succeeded"
	// Failed copre l'errore di rete, la risposta ≥ 400 e il rifiuto del
	// [Guard]: tre cause diverse, un solo stato, distinte dal testo in `error`.
	Failed Outcome = "failed"
	// TimedOut è il timeout del job (R40).
	TimedOut Outcome = "timed_out"
	// Skipped è l'occorrenza che il motore ha deciso di non eseguire: qui
	// significa job messo in pausa fra l'accodamento e l'esecuzione.
	Skipped Outcome = "skipped"
)

// Result è ciò che l'esecutore riporta di una chiamata riuscita a partire.
//
// «Riuscita a partire» non vuol dire «andata bene»: un 500 è un Result valido
// con errore nullo, ed è questo package a deciderne l'esito. La distinzione
// serve a chi implementa [Executor], che non deve conoscere gli stati di
// `job_executions`.
//
// # I due testi sono [secrets.Excerpt] e non stringhe
//
// È la garanzia di R43 spostata sul compilatore (issue #496). Entrambi i campi
// finiscono in `job_executions`, che l'utente rilegge dall'API e che restiamo a
// conservare (privacy policy §2.2); entrambi nascono da testo che **non
// controlliamo** — la risposta del bersaglio e l'errore di chi sta sotto — e che
// può contenere il valore di un segreto risolto in esecuzione.
//
// [secrets.Excerpt] non è costruibile fuori da internal/secrets: l'unico modo di
// ottenerne uno con del testo dentro è chiamare [secrets.Redactor.Excerpt] o
// [secrets.Redactor.ErrorText]. Con questi due campi tipati così, un esecutore
// che salta la redazione **non compila**. Con delle `string` sarebbe stata una
// regola da ricordare in revisione, e le regole da ricordare si dimenticano.
type Result struct {
	// ResponseStatus è lo status HTTP. Zero significa «nessuna risposta»: la
	// colonna resta NULL.
	ResponseStatus int
	// ResponseExcerpt è l'estratto della risposta da conservare (R6), già
	// redatto. Il troncamento al limite dello schema è compito dello store, non
	// di chi esegue: il tetto è una regola della tabella.
	ResponseExcerpt secrets.Excerpt
	// ErrorText è il testo dell'errore da conservare, già redatto.
	//
	// Va valorizzato **insieme** all'errore restituito da [Executor.Execute], ed
	// è l'unico testo che questo package scrive nella colonna `error` per un
	// guasto dell'esecuzione: il messaggio dell'`error` non ci arriva mai. Vedi
	// la quarta clausola di [Executor].
	ErrorText secrets.Excerpt
}

// Executor esegue una singola occorrenza. È l'implementazione HTTP della issue
// #390; qui è un'interfaccia perché il worker pool è verificabile senza rete.
//
// Il contratto ha quattro clausole:
//
//  1. **Rispettare il contesto.** Il pool passa un contesto con la scadenza del
//     job (R40) e lo annulla all'arresto. Un esecutore che lo ignora tiene
//     occupato un worker, che è ciò che R3 vieta.
//  2. **Non toccare `job_executions`.** Lo stato della riga è di questo package,
//     dall'UPDATE condizionato che la prende fino alla scrittura dell'esito.
//  3. **Errore uguale «non è arrivata risposta».** Una risposta con status ≥ 400
//     è un [Result] con errore nullo, non un errore.
//  4. **Chi restituisce un errore ne porta anche il testo**, redatto, in
//     [Result.ErrorText]. L'`error` serve a questo package solo per *classificare*
//     l'esito — `errors.Is(err, context.DeadlineExceeded)` distingue `timed_out`
//     da `failed` — e il suo messaggio non viene mai scritto sulla riga. Il
//     motivo è che quel messaggio nasce fuori di qui: è l'errore di un driver, di
//     una libreria TLS, di un redirect, e nessuno di loro ha promesso di non
//     citare l'indirizzo a cui stava andando, che da #458 in poi contiene i
//     segreti del workspace risolti (R43). Solo chi ha risolto quei valori
//     possiede il redattore che li riconosce.
type Executor interface {
	Execute(ctx context.Context, occ scheduler.Occurrence) (Result, error)
}

// ExecutorFunc adatta una funzione a [Executor].
type ExecutorFunc func(ctx context.Context, occ scheduler.Occurrence) (Result, error)

// Execute implementa [Executor].
func (f ExecutorFunc) Execute(ctx context.Context, occ scheduler.Occurrence) (Result, error) {
	return f(ctx, occ)
}

// Guard decide se un'occorrenza può uscire verso la rete (R38).
//
// È l'innesto della issue #455: il controllo vero — loopback, indirizzi privati,
// link-local, endpoint di metadata, e la ripetizione del controllo su ogni
// redirect — vive lì, e questo package non ne sa niente oltre al fatto che va
// chiamato **prima** di ogni esecuzione e che un errore la ferma.
//
// La chiamata avviene dopo che la riga è stata presa, non prima: un'occorrenza
// rifiutata deve lasciare un'esecuzione `failed` con scritto il perché, non
// sparire. Un bersaglio interno è una cosa che l'utente deve vedere in
// dashboard.
//
// Il default non blocca niente. È deliberato e temporaneo: fino a quando la #455
// non atterra, il posto esiste ma è vuoto — e questo package non è il posto in
// cui scrivere un mezzo controllo che poi ne nasconde uno vero.
type Guard interface {
	Allow(ctx context.Context, occ scheduler.Occurrence) error
}

// GuardFunc adatta una funzione a [Guard].
type GuardFunc func(ctx context.Context, occ scheduler.Occurrence) error

// Allow implementa [Guard].
func (f GuardFunc) Allow(ctx context.Context, occ scheduler.Occurrence) error { return f(ctx, occ) }

// openGuard è il guard di default: lascia passare tutto. Vedi [Guard].
type openGuard struct{}

func (openGuard) Allow(context.Context, scheduler.Occurrence) error { return nil }

// Record è l'esito da scrivere sulla riga dell'occorrenza.
type Record struct {
	Outcome Outcome
	// ResponseStatus fuori dall'intervallo 100–599 vale «nessuno»: la colonna ha
	// un CHECK, e una violazione farebbe perdere l'intero esito invece del solo
	// status.
	ResponseStatus int
	// ResponseExcerpt ed Error sono i due testi che l'utente rilegge dall'API, e
	// sono [secrets.Excerpt] per la ragione spiegata in [Result]: un percorso che
	// ci scrive del testo non redatto non compila. Il troncamento al limite dello
	// schema è dello store.
	ResponseExcerpt secrets.Excerpt
	Error           secrets.Excerpt
}

// Store è ciò che il pool sa di `job_executions`: quattro transizioni di stato,
// tutte condizionate sullo stato di partenza.
//
// Tutte restituiscono un booleano che dice se la riga è stata davvero
// aggiornata. Non è una comodità: è il valore su cui si regge R4. `false` da
// [Store.Claim] significa «l'occorrenza è di qualcun altro» ed è l'unica cosa
// che impedisce una seconda esecuzione.
//
// L'interfaccia esiste perché l'isolamento di R3 è una proprietà della coda, non
// del database, e va provato senza: vedi TestUnJobLentoNonRitardaGliAltri, che
// mette in volo un job lento e mille rapidi senza toccare PostgreSQL.
// L'implementazione vera è [PostgresStore] ed è provata contro il database
// reale.
type Store interface {
	// Claim porta l'occorrenza da `pending` a `running`. È il secondo cancello
	// dell'idempotenza: vedi la documentazione del package.
	Claim(ctx context.Context, occ scheduler.Occurrence) (bool, error)
	// Finish chiude un'occorrenza presa da questo pool.
	Finish(ctx context.Context, occ scheduler.Occurrence, rec Record) (bool, error)
	// Skip chiude come `skipped` un'occorrenza ancora `pending`, senza averla
	// mai fatta partire.
	//
	// `reason` è una `string` e non un [secrets.Excerpt] perché lo scarto avviene
	// **prima** dell'esecuzione: il testo lo compone questo package, e l'unico
	// motivo che esiste è il job messo in pausa. Ciò che arriva dall'esecutore
	// passa invece da [Record], dove i due testi sono tipati.
	Skip(ctx context.Context, occ scheduler.Occurrence, reason string) (bool, error)
	// Release riporta a `pending` un'occorrenza presa e non portata a termine,
	// perché il recupero dello scheduler la ritrovi.
	Release(ctx context.Context, occ scheduler.Occurrence) (bool, error)
}

// Stats è la fotografia del pool: le due grandezze istantanee e i contatori
// cumulativi. Serve alle metriche (R7) e ai test.
type Stats struct {
	// Queued sono le occorrenze accodate e non ancora prese da un worker,
	// InFlight quelle che un worker sta eseguendo adesso.
	Queued   int
	InFlight int

	// Accepted sono le occorrenze accettate da [Pool.Dispatch]; Duplicated
	// quelle riofferte mentre erano già in coda o in volo, scartate senza
	// rumore; Refused quelle rifiutate perché la coda era piena o il pool in
	// arresto, e le cui righe restano `pending`.
	Accepted   int64
	Duplicated int64
	Refused    int64

	// Claimed sono le occorrenze di cui questo pool ha vinto l'aggiornamento
	// condizionato; Lost quelle che al momento della presa erano già di qualcun
	// altro. Lost > 0 non è un errore: è R4 che funziona.
	Claimed int64
	Lost    int64

	// Gli esiti, per come sono finiti sulla riga.
	Succeeded int64
	Failed    int64
	TimedOut  int64
	Skipped   int64
	// Blocked sono le occorrenze fermate dal [Guard] (R38). Contano anche fra le
	// Failed, che è lo stato con cui vengono scritte.
	Blocked int64

	// Released sono le occorrenze riportate a `pending` dall'arresto.
	Released int64
	// Errors sono gli errori del database durante una transizione di stato.
	Errors int64
}

// I valori di partenza. Vedi [Options] per il ragionamento su ciascuno.
const (
	// DefaultWorkers è quanti worker girano insieme.
	//
	// Il numero è dimensionato sulla macchina descritta da SPEC §2 — API, motore
	// e PostgreSQL sulla stessa VPS — dove il collo di bottiglia misurato dalla
	// issue #386 è il database, non la rete. Il conto è questo: ogni occorrenza
	// costa **due statement brevi** (la presa e l'esito) attorno a una chiamata
	// HTTP che dura ordini di grandezza di più, e durante la quale il worker non
	// tiene nessuna connessione. Con 32 worker, anche nel caso peggiore in cui
	// tutti battano sul database nello stesso istante, la richiesta di
	// connessioni resta sotto le 10 di internal/database per il tempo di due
	// UPDATE su chiave primaria; nel caso normale l'occupazione è una frazione di
	// connessione. Alzarlo a centinaia sposterebbe il costo dove fa male — sulla
	// contesa del database, che è condiviso con l'API — per guadagnare
	// parallelismo su chiamate che sono già in attesa di rete.
	//
	// 32 è anche una frequenza di uscita che l'IP condiviso regge: la reputazione
	// dell'indirizzo è un bene di tutti i clienti (R39).
	DefaultWorkers = 32

	// DefaultMaxInFlightPerJob è quante occorrenze **dello stesso job** possono
	// essere in esecuzione insieme.
	//
	// Un ottavo del pool di default, ed è il numero che rende l'isolamento una
	// garanzia invece che una speranza: un job a `every: 1s` che impiega un
	// minuto accumula arretrato all'infinito, e senza tetto se lo porterebbe
	// dentro tutti e 32 i worker nel giro di un minuto. Con il tetto ne occupa al
	// massimo quattro, e gli altri 28 restano ai job rapidi qualunque cosa
	// succeda.
	//
	// Non è la politica delle esecuzioni sovrapposte di R41 — saltare, accodare o
	// consentire — che è una scelta per job e vive su una colonna che oggi non
	// esiste. È un tetto di risorse del processo: le occorrenze eccedenti non
	// vengono saltate, aspettano il loro turno nella coda del job. Quando R41
	// arriverà, si innesterà qui, sulla stessa coda per job.
	DefaultMaxInFlightPerJob = 4

	// DefaultQueueDepth è quante occorrenze il pool tiene in attesa in totale.
	//
	// La coda è un ammortizzatore, non un magazzino: la riga sul database è già
	// la coda durevole, e ciò che non entra qui non è perso — resta `pending` e
	// lo ritrova il recupero. 2048 assorbe qualche passata piena dello scheduler
	// (fino a [scheduler.DefaultBatch] job per passata) senza che la memoria del
	// processo dipenda dall'arretrato accumulato durante un fermo.
	DefaultQueueDepth = 2048

	// DefaultQueueDepthPerJob è quante occorrenze di uno stesso job stanno in
	// coda. Poco più di [scheduler.DefaultMaxPerJob], cioè un'ondata intera di
	// recupero di quel job: abbastanza per non rifiutare lavoro legittimo,
	// abbastanza poco perché un job con tre ore di arretrato non riempia da solo
	// la coda comune e faccia rifiutare i job sani.
	DefaultQueueDepthPerJob = 128

	// DefaultDrainTimeout è quanto si aspettano le esecuzioni in volo prima di
	// rilasciarle. Trenta secondi sono il timeout di default di un job
	// (`jobs.timeout_seconds`, migrazione 0005): un'esecuzione ordinaria fa in
	// tempo a chiudersi con il proprio esito vero, e solo quelle con un timeout
	// esplicito più lungo vengono rilasciate. Aspettare il tetto massimo di 300
	// secondi trasformerebbe ogni deploy in cinque minuti di attesa.
	DefaultDrainTimeout = 30 * time.Second

	// DefaultStoreTimeout è il tetto di una singola transizione di stato. Sono
	// UPDATE su chiave primaria: se non tornano in cinque secondi il database ha
	// un problema che non si risolve aspettando ancora.
	DefaultStoreTimeout = 5 * time.Second

	// DefaultMaxTimeout è il tetto di durata di un'esecuzione, quale che sia il
	// timeout del job (R40). Coincide con il massimo che lo schema accetta su
	// `jobs.timeout_seconds`, ed è qui perché un worker che non si libera è un
	// worker in meno per tutti: il tetto del piano è un'altra cosa e vive altrove.
	// Vale come garanzia solo per un esecutore che rispetta il proprio contesto —
	// che è la prima clausola di [Executor].
	DefaultMaxTimeout = 300 * time.Second
)
