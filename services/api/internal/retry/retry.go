// Package retry decide se un tentativo fallito ne merita un altro, e fra quanto
// (R5).
//
// È deliberatamente puro: niente database, niente rete, niente orologio. Prende
// il numero del tentativo appena concluso e com'è andato, e restituisce una
// [Decision]. Chi la esegue è internal/dispatch, che possiede la riga di
// `job_executions` e sa che cosa farne; qui c'è solo la politica, ed è l'unico
// modo di provarla per intero senza far passare il tempo davvero.
//
// # Cosa si ritenta, e cosa no
//
// R5 elenca «timeout, status ≥ 400, errore di rete». La prima e la terza voce
// non hanno alternative sensate: un nome che non risolve, una connessione
// rifiutata, un bersaglio che non risponde in tempo sono guasti che il secondo
// tentativo può benissimo non incontrare.
//
// La seconda voce va invece letta più stretta di com'è scritta, e questa è la
// scelta di progetto di questo package: **un `4xx` non si ritenta**. Un `4xx` è
// una risposta, non un guasto: il bersaglio ha ricevuto la richiesta, l'ha
// capita e ha risposto che è sbagliata. Un `404` su un URL che non esiste, un
// `401` con una credenziale scaduta, un `422` su un corpo malformato non
// cambiano esito perché li si rimanda identici tre volte: si spende quota
// dell'utente, si consuma la reputazione dell'IP condiviso (R39) e si riempie
// il registro di fallimenti indistinguibili dal primo.
//
// Le eccezioni sono i due `4xx` che *non* dicono «la tua richiesta è sbagliata»:
//
//   - `408 Request Timeout` — il bersaglio dice di aver smesso di aspettare la
//     richiesta, che è un timeout dalla sua parte, non un difetto della nostra;
//   - `429 Too Many Requests` — il bersaglio dice esplicitamente «più tardi», che
//     è una richiesta di riprovare, non un rifiuto.
//
// Un `5xx` si ritenta sempre: è il bersaglio che dichiara un guasto proprio, e
// «il servizio tornerà su» è precisamente il caso per cui il retry esiste.
//
// # Il backoff, e perché da solo non basta
//
// Il ritardo cresce come dice `jobs.retry_backoff` — esponenziale, lineare o
// fisso — ma non è quello il punto delicato. Il punto è che mille job che
// falliscono insieme sono mille job che ritentano insieme: se il ritardo è una
// funzione del solo numero di tentativo, il raddoppio non disperde niente,
// sposta la raffica di due secondi e la ripete più forte. Un bersaglio in
// difficoltà — e se falliscono tutti insieme, di norma, il bersaglio *è* uno
// solo in difficoltà — la riceve tutta in una volta, come la prima.
//
// Perciò il ritardo restituito non è mai il valore nudo del backoff: è quel
// valore **disperso** su una finestra ampia quanto lui. Vedi [Planner.Delay].
//
// # Il tetto è del job, l'ultima parola è del servizio
//
// `jobs.max_retries` è la politica dichiarata dall'utente (SPEC §9). [Limits]
// è il tetto del servizio, e vince: un job non deve poter chiedere mille
// tentativi verso un bersaglio che ne rifiuta uno, perché la quota che
// spenderebbe non è solo la sua — sono worker occupati, connessioni in uscita e
// reputazione dell'IP condivisi con tutti gli altri clienti (R39, R40).
//
// # Cosa non c'è
//
// Non c'è la collisione con l'occorrenza successiva: per sapere quando tocca di
// nuovo a un job serve la sua schedulazione, che questo package non ha e non
// deve avere. La decide internal/dispatch, che ha il job intero sotto mano.
package retry

import (
	"net/http"
	"time"
)

// Backoff è la forma con cui il ritardo cresce. I valori coincidono con quelli
// del tipo `retry_backoff` (migrazione 0001) e arrivano da `jobs.retry_backoff`
// così come sono: tenerli allineati per costruzione evita una tabella di
// traduzione che diverge.
type Backoff string

const (
	// Exponential raddoppia il ritardo a ogni tentativo. È il default dello
	// schema ed è la forma che R5 chiede.
	Exponential Backoff = "exponential"
	// Linear aggiunge un passo fisso a ogni tentativo. Serve ai job che
	// ritentano contro un bersaglio a capacità nota, dove il raddoppio arriva
	// troppo presto a ritardi lunghi.
	Linear Backoff = "linear"
	// Fixed non cresce affatto: stesso ritardo a ogni tentativo. Resta comunque
	// disperso, che è ciò che lo distingue da «tutti insieme fra due secondi».
	Fixed Backoff = "fixed"
)

// I tetti di partenza del servizio. Vedi [Limits] per il ragionamento.
const (
	// DefaultMaxRetries è quanti tentativi successivi al primo il servizio
	// concede al massimo, quale che sia la politica del job.
	//
	// Coincide con il massimo che lo schema accetta su `jobs.max_retries`
	// (migrazione 0005, `CHECK (max_retries BETWEEN 0 AND 10)`): il vincolo del
	// database ferma i valori scritti da API e da `cron.yaml`, questo ferma anche
	// ciò che arrivasse per altre strade. Sono due controlli sulla stessa
	// grandezza, ed è voluto — quello nello schema non può essere aggirato,
	// questo non può essere dimenticato da chi costruisce un [Policy] a mano.
	DefaultMaxRetries = 10

	// DefaultBase è il ritardo del primo tentativo successivo, cioè il passo da
	// cui il backoff parte.
	//
	// Due secondi sono il compromesso fra le due schedulazioni che convivono
	// (SPEC §9): per un job giornaliero qualunque valore sotto il minuto è
	// indifferente, per un job a `every: 10s` è l'unico ordine di grandezza che
	// lascia spazio a un paio di tentativi prima che l'occorrenza successiva sia
	// dovuta. Più corto sarebbe una seconda raffica addosso a un bersaglio che
	// ha appena fallito; più lungo renderebbe il retry inutile per l'intera
	// famiglia dei job sub-minuto, che è quella dei piani a pagamento (R22).
	DefaultBase = 2 * time.Second

	// DefaultMaxDelay è il tetto del singolo ritardo, dispersione esclusa.
	//
	// Cinque minuti sono il punto oltre il quale un tentativo smette di essere un
	// retry: non ritenta più *quell'occorrenza*, ripete una chiamata che il
	// bersaglio non riconduce più a niente, e nel frattempo la schedulazione del
	// job ne ha prodotte altre. Serve anche a tenere in piedi la garanzia di
	// [Planner.Delay] con backoff esponenziali lunghi, dove il raddoppio arriva
	// alle ore in una decina di tentativi.
	DefaultMaxDelay = 5 * time.Minute

	// DefaultMaxRetryAfter è quanto si onora al massimo di un `Retry-After`.
	//
	// La testata arriva dal bersaglio, cioè da fuori: `Retry-After: 86400` è una
	// risposta legale, e onorarla alla lettera significherebbe tenere in memoria
	// un tentativo per un giorno. Il tetto è lo stesso di [DefaultMaxDelay] per
	// la stessa ragione: oltre quella soglia non si sta più ritentando
	// un'occorrenza, la si sta rifacendo in un altro momento.
	DefaultMaxRetryAfter = 5 * time.Minute
)

// Limits sono i tetti del servizio (R40). Il campo a zero prende il proprio
// default.
type Limits struct {
	// MaxRetries è il tetto ai tentativi successivi al primo. La politica del
	// job non può superarlo; può stare sotto.
	MaxRetries int
	// Base è il ritardo del primo tentativo successivo.
	Base time.Duration
	// MaxDelay è il tetto della finestra di backoff, prima della dispersione.
	MaxDelay time.Duration
	// MaxRetryAfter è il tetto di ciò che si onora quando il bersaglio chiede
	// lui di riprovare più tardi.
	MaxRetryAfter time.Duration

	// Rand è la sorgente della dispersione: restituisce un intero in [0, n).
	// Nil significa quella di sistema.
	//
	// È un campo e non una variabile di package perché la dispersione è una
	// proprietà che va **misurata**: un test che verifichi la larghezza della
	// finestra ha bisogno di una sorgente che sappia produrne gli estremi, e un
	// test che verifichi il backoff nudo ha bisogno di poterla spegnere. Vedi
	// TestLaDispersioneCopreLaFinestra.
	Rand func(n int64) int64
}

// withDefaults completa i campi lasciati a zero. Un valore negativo vale zero:
// una configurazione sbagliata deve degradare al default, non produrre ritardi
// negativi o tetti che tolgono ogni tentativo.
func (l Limits) withDefaults() Limits {
	if l.MaxRetries <= 0 {
		// Zero è ambiguo su un intero, e la lettura giusta è «non configurato»:
		// il tetto che toglie ogni retry è una scelta del job (`max_retries: 0`),
		// non un default plausibile per il servizio.
		l.MaxRetries = DefaultMaxRetries
	}
	if l.Base <= 0 {
		l.Base = DefaultBase
	}
	if l.MaxDelay <= 0 {
		l.MaxDelay = DefaultMaxDelay
	}
	if l.MaxRetryAfter <= 0 {
		l.MaxRetryAfter = DefaultMaxRetryAfter
	}
	if l.Rand == nil {
		l.Rand = systemRand
	}
	return l
}

// Policy è la politica del singolo job, come vive su `jobs` (migrazione 0005).
type Policy struct {
	// MaxRetries è `jobs.max_retries`: quanti tentativi dopo il primo. Zero
	// significa nessuno, ed è una scelta legittima — un job non idempotente non
	// deve essere ritentato da noi.
	MaxRetries int
	// Backoff è `jobs.retry_backoff`. Un valore che questo package non conosce
	// vale [Exponential], che è il default dello schema: se l'enum cresce prima
	// di questo codice, la forma predefinita è una risposta migliore di un
	// panico o di un ritardo nullo.
	Backoff Backoff
}

// Outcome è com'è andato il tentativo appena concluso, nella sola misura che
// serve a decidere se ne merita un altro.
//
// Non c'è nessun testo, ed è deliberato: i due testi di un'esecuzione sono
// `secrets.Excerpt` perché possono contenere segreti risolti (R43, issue #496),
// e una politica che non li guarda non ha modo di farne uscire uno. Qui
// arrivano solo numeri e booleani.
type Outcome struct {
	// Status è lo status HTTP della risposta. Zero significa «nessuna risposta»:
	// errore di rete, nome che non risolve, connessione rifiutata.
	Status int
	// TimedOut è vero quando il tentativo è finito per scadenza del tempo (R40).
	TimedOut bool
	// Permanent dichiara un guasto che un secondo tentativo identico non può
	// cambiare, e che quindi non va ritentato per nessun motivo.
	//
	// Lo imposta chi esegue, perché è l'unico a saperlo: una destinazione
	// rifiutata da netguard (R38) è rifiutata anche fra dieci secondi, e un
	// `${VAR}` che non si risolve non si risolverà da solo (R43). Sono
	// configurazioni sbagliate travestite da guasti, e ritentarle è lo stesso
	// spreco di un `4xx`.
	Permanent bool
	// RetryAfter è il ritardo che il bersaglio ha chiesto con la testata
	// `Retry-After`. Zero significa che non l'ha chiesto.
	RetryAfter time.Duration
}

// Reason è il motivo della decisione. Serve a chi la registra e a chi la conta
// (R7): «non ho ritentato» senza il perché è un log che non si può usare.
type Reason int

const (
	// NoFailure è l'esito che non è un fallimento: non c'è niente da ritentare.
	NoFailure Reason = iota
	// Network è l'assenza di risposta: rete, DNS, TLS, connessione rifiutata.
	Network
	// Timeout è il tempo scaduto, nostro (R40) o dichiarato dal bersaglio (408).
	Timeout
	// ServerError è un `5xx`: il bersaglio dichiara un guasto proprio.
	ServerError
	// Throttled è il bersaglio che chiede di rallentare (429).
	Throttled
	// ClientError è un `4xx`: una risposta, non un guasto. Non si ritenta.
	ClientError
	// Permanent è un guasto deterministico dichiarato da chi ha eseguito.
	Permanent
	// Exhausted è il tetto dei tentativi raggiunto.
	Exhausted
	// Disabled è il job che non prevede tentativi successivi.
	Disabled
)

// String è la forma leggibile del motivo, per i log e per la dashboard.
func (r Reason) String() string {
	switch r {
	case NoFailure:
		return "nessun fallimento da ritentare"
	case Network:
		return "nessuna risposta dal bersaglio"
	case Timeout:
		return "tempo scaduto"
	case ServerError:
		return "il bersaglio ha risposto con un errore proprio (5xx)"
	case Throttled:
		return "il bersaglio ha chiesto di rallentare (429)"
	case ClientError:
		return "il bersaglio ha rifiutato la richiesta (4xx): ritentarla identica fallirebbe di nuovo"
	case Permanent:
		return "guasto che un secondo tentativo identico non può cambiare"
	case Exhausted:
		return "tentativi esauriti"
	case Disabled:
		return "il job non prevede tentativi successivi"
	}
	return "motivo sconosciuto"
}

// Decision è ciò che il pianificatore risponde.
type Decision struct {
	// Retry dice se fare un altro tentativo.
	Retry bool
	// Delay è fra quanto, valido solo se Retry è vero.
	Delay time.Duration
	// Reason è il perché, in entrambi i casi.
	Reason Reason
}

// Planner applica [Limits] alle politiche dei job. Va costruito con [New].
type Planner struct {
	limits Limits
}

// New costruisce il pianificatore con i tetti del servizio.
func New(limits Limits) *Planner { return &Planner{limits: limits.withDefaults()} }

// Limits sono i tetti in vigore, con i default già applicati.
func (p *Planner) Limits() Limits { return p.limits }

// Plan decide se il tentativo numero `attempt`, andato come dice `out`, ne
// merita un altro.
//
// `attempt` è il numero del tentativo **appena concluso**: 1 è il primo, cioè
// l'occorrenza nata dalla schedulazione. Il tentativo successivo sarà
// `attempt + 1`, che è anche il valore che finirà nella chiave primaria di
// `job_executions`.
//
// L'ordine dei controlli è quello che produce il motivo più utile da leggere.
// Prima si guarda **se** l'esito è ritentabile, poi se ci sono ancora tentativi:
// un `404` su un job con dieci retry disponibili non è «tentativi esauriti», è
// un `404`, e scriverlo nell'altro modo manderebbe chi legge a cercare il
// problema dalla parte sbagliata.
func (p *Planner) Plan(pol Policy, attempt int, out Outcome) Decision {
	ok, reason := Retryable(out)
	if !ok {
		return Decision{Reason: reason}
	}

	allowed := p.allowance(pol.MaxRetries)
	if allowed == 0 {
		return Decision{Reason: Disabled}
	}
	if attempt < 1 {
		attempt = 1
	}
	// I tentativi già fatti dopo il primo sono `attempt - 1`.
	if attempt-1 >= allowed {
		return Decision{Reason: Exhausted}
	}

	return Decision{Retry: true, Delay: p.Delay(pol.Backoff, attempt, out), Reason: reason}
}

// allowance è quanti tentativi successivi al primo sono davvero concessi: la
// politica del job, tagliata al tetto del servizio.
//
// È qui che «il tetto è per job ma non illimitato» diventa una riga di codice.
// Un valore negativo vale zero: una politica scritta male toglie i retry, non ne
// concede all'infinito.
func (p *Planner) allowance(jobMax int) int {
	if jobMax <= 0 {
		return 0
	}
	return min(jobMax, p.limits.MaxRetries)
}

// Retryable classifica un esito. Il secondo valore è il motivo, e vale in
// entrambi i casi.
//
// È esportata perché è la parte della politica che si discute: chi vuole sapere
// se un `429` si ritenta deve poterlo leggere — e provare — senza costruire un
// [Planner]. Il ragionamento su ciascun caso è nella documentazione del package.
func Retryable(out Outcome) (bool, Reason) {
	switch {
	case out.Permanent:
		return false, Permanent
	case out.TimedOut:
		return true, Timeout
	case out.Status == 0:
		// Nessuna risposta: la richiesta non è arrivata a destinazione, oppure la
		// risposta non è tornata. È il caso in cui un secondo tentativo ha più
		// probabilità di riuscire.
		return true, Network
	case out.Status == http.StatusTooManyRequests:
		return true, Throttled
	case out.Status == http.StatusRequestTimeout:
		// L'unico altro `4xx` che descrive un'attesa e non un difetto della
		// richiesta: il bersaglio ha smesso di aspettarci.
		return true, Timeout
	case out.Status >= 500:
		return true, ServerError
	case out.Status >= 400:
		return false, ClientError
	default:
		// `2xx` e `3xx` non sono fallimenti e non arrivano fin qui: chi esegue
		// chiama il pianificatore solo su un esito fallito. Se ci arrivano, la
		// risposta giusta è comunque «niente da ritentare».
		return false, NoFailure
	}
}
