// Package httpexec esegue la chiamata HTTP di un'occorrenza (R1, R6, R40).
//
// È l'ultimo miglio del motore: internal/schedule sa *quando*,
// internal/scheduler decide *che cosa* è dovuto, internal/dispatch decide *con
// quale worker*, e qui si fa finalmente la richiesta verso il bersaglio
// dell'utente. Implementa [dispatch.Executor] e non conosce né
// `job_executions` né gli stati che ci vengono scritti: riporta ciò che è
// successo sulla rete, la traduzione in esito è di chi possiede la riga.
//
// # Il client non si costruisce qui
//
// [Options.Guard] è obbligatorio e in produzione è un `*netguard.Guard`. Non è
// una preferenza di stile: il blocco degli indirizzi interni (R38) vive nel
// `DialContext` del trasporto, non in una validazione dell'URL, e un
// `http.Client` costruito in questo file supererebbe ogni controllo restando
// **protetto da niente** — sarebbe il prodotto che torna a essere uno strumento
// d'attacco, con il pacchetto che lo impedisce ancora presente nel repository e
// mai chiamato. Per la stessa ragione da qui non si tocca nulla di ciò che quel
// client porta con sé: politica sui redirect ([netguard.Guard.CheckRedirect], al
// massimo [netguard.DefaultMaxRedirects] salti, R40) e verifica TLS, che è
// quella predefinita della libreria standard e resta tale perché nessuno la
// disattiva.
//
// L'interfaccia esiste per una necessità sola, la stessa che ha prodotto
// `netguard.AllowForTest`: un `httptest.Server` vive su 127.0.0.1, cioè
// esattamente su ciò che il guard ha il compito di rifiutare, e senza un punto
// di sostituzione l'esecutore non sarebbe verificabile contro un bersaglio HTTP
// vero. La deroga sta nel *chiamante*, che nei test è un test; qui non c'è
// nessun modo di ottenere un client non protetto se non passandocelo.
//
// # Il timeout copre tutta la richiesta
//
// Il contesto arriva già con la scadenza del job tagliata al tetto del servizio
// ([dispatch.Options.MaxTimeout], R40) e viene applicato alla richiesta: vale per la
// risoluzione del nome, la connessione, l'handshake TLS, l'attesa delle testate
// **e la lettura del corpo**. È l'unica forma che copre il caso classico, il
// bersaglio che accetta la connessione e poi non risponde mai: un timeout sulla
// sola connessione lo lascerebbe passare, e il worker resterebbe occupato finché
// il sistema operativo non si annoia.
//
// # Della risposta si conserva un estratto, e si legge solo quello
//
// R40 chiede un tetto alla dimensione della risposta «letta e conservata», e le
// due cose sono lo stesso tetto per una ragione pratica: un bersaglio che
// restituisce un gigabyte non deve arrivare in memoria per poi essere buttato.
// Si legge al massimo [Options.MaxResponseBytes] byte e il resto non si legge
// affatto — non si drena il corpo per fare contento il riuso della connessione,
// che è precisamente ciò che il tetto vieta.
//
// Quello che si è letto viene poi reso **conservabile** — UTF-8 valido e senza
// NUL, che è ciò che una colonna `text` di PostgreSQL accetta. Il troncamento al
// limite dello schema resta invece di chi scrive ([dispatch.PostgresStore]), che
// è dove vive quel vincolo.
//
// # I segreti si risolvono qui, e qui si redigono
//
// I segreti del workspace (R42, R43, issue #458) sono risolti **al momento
// dell'esecuzione** e iniettati in URL, header e corpo. Il posto è questo:
// [Executor.Execute] chiama [Secrets.Resolve] prima di comporre la richiesta, e
// prima di allora un `${VAR}` è testo, sia nella colonna `jobs.url` sia in
// `jobs.headers`. Un riferimento che non si risolve non deve arrivare fin qui —
// R43 vuole che l'errore l'utente lo veda al sync, e la stessa analisi
// (`secrets.NameSet.Validate`) gira lì — ma se ci arriva, perché il segreto è
// stato revocato dopo, l'esecuzione fallisce con scritto quale nome manca invece
// di partire con una credenziale sbagliata.
//
// Da quel momento la richiesta contiene valori veri, e **due testi tornano
// indietro senza che noi li controlliamo**: il corpo della risposta, che il
// bersaglio può riempire con la credenziale che gli abbiamo appena mandato
// («token XYZ non valido» è un messaggio d'errore comunissimo), e l'errore di
// chi sta sotto di noi. Il registro delle esecuzioni è visibile all'utente e la
// privacy policy dichiara che gli estratti sono conservati: entrambi passano
// dalla redazione di [secrets.Redactor], che rimette il `${NOME}` al posto del
// valore, e il risultato è un [secrets.Excerpt] — un tipo che
// [dispatch.Result] pretende e che non si può costruire saltando quel passaggio
// (issue #496).
//
// La difesa contro l'URL negli errori resta, e resta la prima: `http.Client`
// restituisce un `*url.Error`, il cui messaggio contiene **l'URL completo, query
// compresa**, e [requestError] toglie l'involucro conservando la causa. La
// redazione è la seconda, e serve perché togliere l'involucro protegge dal caso
// noto: la causa che resta è l'errore di un driver, di una libreria TLS, di un
// redirect, e nessuno di loro ha promesso di non citare l'indirizzo a cui stava
// andando. Vale anche per i log: questo pacchetto non registra né URL, né
// header, né corpo.
//
// # Il confine con il retry (#392)
//
// Qui si esegue **un tentativo e uno solo**. Non c'è nessun ciclo, nessuna
// attesa fra due prove, nessuna decisione su cosa valga la pena ritentare: un
// errore torna al chiamante e la riga si chiude. Il retry di R5 nasce da
// [dispatch.Outcome] e produce una *nuova* occorrenza con `attempt` successivo,
// che ripasserà da qui come una qualunque altra — che è il motivo per cui
// `attempt` è nella chiave primaria di `job_executions` invece che un contatore
// su una riga sola.
package httpexec

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/netguard"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// I valori di partenza.
const (
	// DefaultMaxResponseBytes è quanto si legge al massimo di una risposta
	// (R40).
	//
	// Il numero discende da quello che si può conservare. `response_excerpt`
	// ammette 8192 **caratteri** (migrazione 0006) e un carattere UTF-8 occupa
	// al massimo quattro byte: 32 KiB coprono già il caso peggiore, quindi 64
	// KiB garantiscono che sia sempre la colonna a decidere quanto se ne tiene,
	// mai la lettura. È il verso giusto — il limite di R6 è dichiarato sullo
	// schema, e un tetto di lettura più stretto lo renderebbe più corto senza
	// che da nessuna parte sia scritto perché.
	//
	// Verso l'alto il vincolo è la memoria: con [dispatch.DefaultWorkers]
	// esecuzioni in volo sono 2 MiB nel caso peggiore, cioè un costo che non si
	// nota. Un tetto a misura del corpo più grande immaginabile invece sì.
	DefaultMaxResponseBytes = 64 << 10

	// DefaultUserAgent è come Postqron si presenta al bersaglio.
	//
	// Non è cortesia: le chiamate escono tutte dallo stesso indirizzo e la sua
	// reputazione è un bene condiviso da tutti i clienti (R39). Chi riceve
	// traffico inatteso deve poter capire da chi arriva e a chi scrivere senza
	// dover risalire a un IP nudo. L'utente può sovrascriverlo con un header
	// `User-Agent` sul job.
	DefaultUserAgent = "Postqron/1.0 (+https://postqron.com)"
)

// Secrets risolve i riferimenti `${VAR}` di una richiesta con i segreti del
// workspace (R42, R43).
//
// L'implementazione d'esercizio è `*secrets.Service` — l'asserzione qui sotto lo
// mette per iscritto — e l'interfaccia esiste per la stessa ragione di [Guard]:
// questo pacchetto non deve conoscere né il database né la cifratura per fare
// una richiesta HTTP.
//
// Ciò che restituisce, il [secrets.Resolved], porta con sé due cose
// inseparabili: i valori espansi e il redattore che li riconosce. È deliberato
// nel progetto di internal/secrets, e qui è ciò che rende impossibile avere i
// primi senza il secondo.
type Secrets interface {
	Resolve(ctx context.Context, userID string, req secrets.Request) (secrets.Resolved, error)
}

// Guard è la sorgente del client HTTP.
//
// L'implementazione d'esercizio è `*netguard.Guard` — l'asserzione qui sotto lo
// mette per iscritto in modo che il compilatore lo verifichi — e la ragione per
// cui il client non si costruisce in questo pacchetto sta nella documentazione
// del package.
type Guard interface {
	// Client restituisce il client con cui fare le richieste. Viene chiamato
	// **una volta sola**, alla costruzione dell'esecutore: il client porta con sé
	// il proprio pool di connessioni, e uno nuovo per richiesta significherebbe
	// una connessione nuova per richiesta.
	Client() *http.Client
}

// L'esecutore è ciò che il worker pool si aspettava a valle, il guard è quello
// di #455 e la risoluzione dei segreti è quella di #458: tre asserzioni, tre
// confini che il compilatore tiene fermi.
var (
	_ dispatch.Executor = (*Executor)(nil)
	_ Guard             = (*netguard.Guard)(nil)
	_ Secrets           = (*secrets.Service)(nil)
)

// Options sono le dipendenze e i tetti dell'esecutore. Guard e Secrets sono
// obbligatori.
type Options struct {
	// Guard è la sorgente del client HTTP: in produzione `*netguard.Guard`.
	// Senza, [New] fallisce — non esiste un default, perché il default
	// plausibile sarebbe un client non protetto.
	Guard Guard
	// Secrets risolve i `${VAR}` del job (R43): in produzione `*secrets.Service`.
	// Senza, [New] fallisce, per lo stesso motivo per cui fallisce senza Guard:
	// il default plausibile — non risolvere niente — manderebbe al bersaglio una
	// testata `Authorization: Bearer ${TOKEN}` scritta così com'è, cioè una
	// credenziale sbagliata a ogni esecuzione. Vedi [secrets.Service.Resolve].
	Secrets Secrets
	// Logger riceve ciò che non finisce sulla riga dell'esecuzione. Nil
	// significa nessun log.
	Logger *slog.Logger
	// MaxResponseBytes è quanto si legge al massimo di una risposta (R40). Zero
	// significa [DefaultMaxResponseBytes].
	MaxResponseBytes int64
	// UserAgent è come ci si presenta quando il job non impone il proprio.
	// Vuoto significa [DefaultUserAgent].
	UserAgent string
}

// Executor esegue la chiamata HTTP di un'occorrenza. Va costruito con [New].
type Executor struct {
	client    *http.Client
	secrets   Secrets
	log       *slog.Logger
	maxBytes  int64
	userAgent string
}

// New costruisce l'esecutore.
func New(opts Options) (*Executor, error) {
	if opts.Guard == nil {
		return nil, errors.New("httpexec: Guard è obbligatorio: il client HTTP deve venire da netguard (R38, issue #455)")
	}
	if opts.Secrets == nil {
		return nil, errors.New(
			"httpexec: Secrets è obbligatorio: senza risoluzione un ${VAR} partirebbe letterale " +
				"verso il bersaglio dell'utente (R43, issue #496)")
	}
	client := opts.Guard.Client()
	if client == nil {
		return nil, errors.New("httpexec: il Guard non ha restituito un client")
	}

	e := &Executor{
		client:    client,
		secrets:   opts.Secrets,
		log:       opts.Logger,
		maxBytes:  opts.MaxResponseBytes,
		userAgent: opts.UserAgent,
	}
	if e.log == nil {
		e.log = slog.New(slog.DiscardHandler)
	}
	if e.maxBytes <= 0 {
		e.maxBytes = DefaultMaxResponseBytes
	}
	if e.userAgent == "" {
		e.userAgent = DefaultUserAgent
	}
	return e, nil
}
