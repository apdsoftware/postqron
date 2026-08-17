package httpexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// Execute implementa [dispatch.Executor]: fa la richiesta e riporta com'è
// andata.
//
// La distinzione fra le due uscite è quella del contratto, e non è la
// distinzione fra «bene» e «male»: un 500 è un [dispatch.Result] con errore
// nullo, perché la richiesta è arrivata a destinazione e la risposta è un fatto
// da registrare. L'errore significa **non è arrivata risposta** — nome che non
// risolve, connessione rifiutata, destinazione bloccata, tempo scaduto — ed è
// chi possiede la riga a decidere che esito scriverne.
//
// L'unico caso in cui le due uscite convivono è la risposta cominciata e non
// finita: lo status è arrivato, il corpo si è interrotto a metà. Lo status si
// riporta comunque, perché è successo, e l'errore dice che la risposta non è
// completa — mentire su una risposta troncata dalla rete sarebbe scrivere un
// `succeeded` su una chiamata che l'utente ha visto fallire dall'altra parte.
func (e *Executor) Execute(ctx context.Context, occ scheduler.Occurrence) (dispatch.Result, error) {
	// I segreti del workspace si risolvono **prima** di comporre la richiesta, ed
	// è l'unico punto in cui i loro valori tornano in chiaro (R43). Da qui in poi
	// ogni testo destinato alla riga dell'esecuzione passa dal redattore che la
	// risoluzione si porta dietro: è quello, e non la diligenza di chi scrive, a
	// impedire che il valore torni indietro dentro la risposta del bersaglio.
	//
	// Sul percorso d'errore `resolved` è il valore zero, il cui redattore è vuoto:
	// non c'è niente da togliere, perché nessun valore è stato espanso. È lo
	// stesso codice per i due casi, che è ciò che chiede la sesta clausola del
	// contratto — una richiesta senza segreti segue la stessa strada.
	resolved, err := e.resolve(ctx, occ.Job)
	if err != nil {
		return dispatch.Result{ErrorText: resolved.Redactor().ErrorText(err, e.limit())}, err
	}

	req, err := e.request(ctx, occ.Job.Method, resolved)
	if err != nil {
		return dispatch.Result{ErrorText: resolved.Redactor().ErrorText(err, e.limit())}, err
	}

	resp, err := e.client.Do(req)
	if err != nil {
		err = requestError(err)
		return dispatch.Result{ErrorText: resolved.Redactor().ErrorText(err, e.limit())}, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Il corpo non letto per intero — è la norma, il tetto di R40 è lì
			// per quello — rende la connessione non riusabile e la chiusura può
			// lamentarsene. Non è un fatto dell'esecuzione e non va sulla riga.
			e.log.DebugContext(ctx, "httpexec: chiusura del corpo della risposta", slog.Any("err", err))
		}
	}()

	// La testata `Retry-After` si legge qui e non si onora qui: dice a chi possiede
	// la riga fra quanto il bersaglio è disposto a riparlarci, e la decisione su
	// che farne è di internal/retry (R5). Vedi il confine con il retry, in fondo
	// alla documentazione del package.
	after := retryAfter(resp.Header, time.Now())

	body, err := e.excerpt(resp.Body, resolved.Redactor())
	if err != nil {
		err = responseError(err)
		return dispatch.Result{
			ResponseStatus: resp.StatusCode,
			ErrorText:      resolved.Redactor().ErrorText(err, e.limit()),
			RetryAfter:     after,
		}, err
	}
	return dispatch.Result{
		ResponseStatus:  resp.StatusCode,
		ResponseExcerpt: body,
		RetryAfter:      after,
	}, nil
}

// retryAfter legge la testata `Retry-After` nelle due forme che RFC 9110 §10.2.3
// ammette: un numero di secondi, oppure una data HTTP.
//
// Non è un testo che finisce sulla riga e non passa dalla redazione, ed è
// legittimo che sia così: ciò che esce di qui è una `time.Duration`, cioè un
// numero. Qualunque cosa il bersaglio abbia scritto nella testata — compreso un
// segreto che gli avessimo appena mandato — o si legge come durata o vale zero.
// È l'unica informazione della risposta che possiamo far uscire da qui senza il
// tipo di internal/secrets, e la ragione è che le abbiamo tolto la forma di
// testo.
//
// Un valore illeggibile, negativo o già passato vale zero, cioè «il bersaglio non
// ha chiesto niente»: la politica applicherà il proprio backoff, che è il
// comportamento giusto per una testata scritta male.
func retryAfter(h http.Header, now time.Time) time.Duration {
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(raw); err == nil {
		if d := at.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// resolve espande i riferimenti `${VAR}` del job con i segreti del workspace
// (R42, R43).
//
// La lettura degli header viene prima perché è la sola parte che può fallire per
// un motivo che non riguarda i segreti, e perché i riferimenti stanno anche lì:
// un `Authorization: Bearer ${TOKEN}` è il caso normale, non un caso limite.
func (e *Executor) resolve(ctx context.Context, job scheduler.Job) (secrets.Resolved, error) {
	headers, err := job.HeaderMap()
	if err != nil {
		// L'errore originale non si propaga: cita il nome del job e potrebbe
		// citare il JSON che non ha saputo decodificare, cioè gli header.
		return secrets.Resolved{}, errors.New("header del job non decodificabili")
	}

	var body string
	if job.Body != nil {
		body = *job.Body
	}

	resolved, err := e.secrets.Resolve(ctx, job.UserID, secrets.Request{
		URL: job.URL, Headers: headers, Body: body,
	})
	if err != nil {
		if _, invalid := secrets.AsValidation(err); !invalid {
			// Un guasto nostro — il database che non risponde, una chiave di
			// cifratura che il processo non ha più — va detto a chi gestisce il
			// servizio, perché sulla riga dell'esecuzione ci finisce solo la frase
			// generica di [resolveError]. Nel log non ci sono valori: gli errori di
			// internal/secrets citano i nomi dei segreti, mai il loro contenuto.
			e.log.ErrorContext(ctx, "httpexec: risoluzione dei segreti del workspace fallita",
				slog.String("job", job.ID), slog.Any("err", err))
		}
		return secrets.Resolved{}, resolveError(err)
	}
	return resolved, nil
}

// request costruisce la richiesta a partire dal bersaglio del job **risolto**
// (R1).
//
// Gli errori di questa funzione non citano mai il valore che li ha causati: URL
// e header contengono i segreti risolti (R43), e questo messaggio finisce nella
// colonna `error`, che l'utente rilegge dall'API.
func (e *Executor) request(ctx context.Context, jobMethod string, resolved secrets.Resolved) (*http.Request, error) {
	target, err := url.Parse(resolved.URL())
	if err != nil {
		return nil, errors.New("URL del job illeggibile")
	}
	switch strings.ToLower(target.Scheme) {
	case "http", "https":
	default:
		// Lo schema è già ristretto dal database (`jobs_url_scheme_check`) e
		// dalla validazione dell'API. Ricontrollarlo costa una riga e vale per il
		// giorno in cui una riga arriva da una porta di servizio: Postqron esegue
		// esclusivamente chiamate HTTP (SPEC §10).
		return nil, errors.New("schema dell'URL non ammesso: sono consentiti solo http e https")
	}

	method := strings.ToUpper(strings.TrimSpace(jobMethod))
	if method == "" {
		method = http.MethodGet
	}

	// strings.NewReader dà a NewRequestWithContext sia ContentLength sia GetBody:
	// il secondo è ciò che permette al client di ripetere il corpo su un redirect
	// 307 o 308, che senza sarebbe una richiesta rifatta a vuoto.
	var body io.Reader
	if resolved.Body() != "" {
		body = strings.NewReader(resolved.Body())
	}

	// L'URL si passa come l'utente l'ha scritto — a meno dei segreti espansi — e
	// non nella forma riscritta da url.String(): la richiesta lo riparserà con la
	// stessa funzione, e una normalizzazione in mezzo cambierebbe il percorso
	// davvero chiamato rispetto a quello che l'utente legge nel proprio job.
	req, err := http.NewRequestWithContext(ctx, method, resolved.URL(), body)
	if err != nil {
		return nil, errors.New("richiesta del job non costruibile: metodo o URL non validi")
	}

	for name, value := range resolved.Headers() {
		// `Host` non è una testata come le altre: nella richiesta di Go vive in un
		// campo a sé, e impostarla nella mappa non avrebbe alcun effetto. Serve a
		// chi punta a un reverse proxy che smista sul nome.
		if strings.EqualFold(name, "Host") {
			req.Host = value
			continue
		}
		req.Header.Set(name, value)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", e.userAgent)
	}
	return req, nil
}

// excerpt legge l'estratto della risposta da conservare (R6, R40) e lo redige
// (R43).
//
// Legge al massimo [Options.MaxResponseBytes] byte e **non tocca il resto**:
// drenare il corpo per poter riusare la connessione significherebbe leggere per
// intero il gigabyte che il tetto esiste per non leggere.
//
// # L'ordine dei due passaggi
//
// Prima si rende il testo conservabile ([storable]), poi lo si redige. Il verso
// conta perché la redazione cerca il valore dei segreti *carattere per
// carattere*: cercarlo nel testo che verrà davvero scritto è l'unico modo di
// essere sicuri che ciò che si scrive non lo contenga. Il verso opposto è
// impossibile per costruzione, ed è voluto — un [secrets.Excerpt] non si può
// riaprire per trasformarlo, altrimenti sarebbe un `string` con un altro nome.
//
// Resta il caso, dichiarato in [secrets.Redactor.Redact], del valore che torna
// indietro *trasformato*: percent-encoded, in base64, o tagliato a metà dal
// tetto di lettura. La redazione è l'ultima difesa, non la prima; la prima è che
// il valore non finisca mai in un testo nostro, e quella è garantita dai tipi.
func (e *Executor) excerpt(body io.Reader, redactor secrets.Redactor) (secrets.Excerpt, error) {
	raw, err := io.ReadAll(io.LimitReader(body, e.maxBytes))
	if err != nil {
		return secrets.Excerpt{}, err
	}
	return redactor.Excerpt([]byte(storable(raw)), e.limit()), nil
}

// limit è il tetto in caratteri dei testi che finiscono sulla riga.
//
// Coincide con il tetto di lettura (R40) — non più caratteri di quanti byte se
// ne sono letti — e non è ridondante, perché si applica **dopo** la redazione,
// che il testo può allungarlo: `${TOKEN_DI_PRODUZIONE}` è più lungo di parecchi
// valori che sostituisce. Il troncamento al limite dello schema resta di chi
// scrive ([dispatch.PostgresStore]): quello è un vincolo della tabella, non una
// scelta di chi esegue.
func (e *Executor) limit() int { return int(e.maxBytes) }

// storable rende l'estratto scrivibile su una colonna `text`.
//
// Due trasformazioni, e nessuna delle due è cosmetica:
//
//  1. **UTF-8 valido.** Il corpo di una risposta è una sequenza di byte
//     qualunque — un PDF, un'immagine, una pagina in Latin-1 — e il tetto di
//     lettura può in più troncarla nel mezzo di un carattere. PostgreSQL
//     rifiuta una stringa che non è UTF-8 valido: senza questa conversione si
//     perderebbe **l'intero esito** dell'esecuzione, cioè il fatto che è
//     avvenuta e com'è andata, per colpa di un byte nel corpo della risposta.
//  2. **Niente NUL.** `\x00` è UTF-8 perfettamente valido e PostgreSQL non lo
//     accetta comunque in una colonna di testo, in nessuna forma. È il caso che
//     sfugge al controllo precedente e produce lo stesso guasto.
//
// Quello che resta può essere illeggibile, ed è giusto così: è ciò che il
// bersaglio ha risposto, e l'utente deve poterlo vedere per quello che è.
func storable(raw []byte) string {
	text := strings.ToValidUTF8(string(raw), "�")
	if !strings.ContainsRune(text, 0) {
		return text
	}
	return strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, text)
}

// requestError toglie l'URL dagli errori del client HTTP.
//
// `http.Client` avvolge tutto in un `*url.Error`, il cui `Error()` è nella forma
// `Post "https://api.example.com/hook?token=…": dial tcp: …`. Quel prefisso è
// **l'URL completo del job, query compresa**, e da #458 in poi ci vivono dentro
// i segreti del workspace risolti in esecuzione. La colonna `error` è visibile
// all'utente dall'API (R6, R43): l'involucro va tolto e la causa conservata, che
// è la parte che spiega il guasto e non contiene l'indirizzo.
//
// La conservazione non è solo cortesia diagnostica: `record` di internal/dispatch
// distingue il timeout dal fallimento con `errors.Is(err, context.DeadlineExceeded)`,
// quindi un errore riscritto senza `%w` trasformerebbe ogni timeout in un
// generico `failed`.
func requestError(err error) error {
	var wrapped *url.Error
	if errors.As(err, &wrapped) && wrapped.Err != nil {
		err = wrapped.Err
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		// Il tetto è quello del job tagliato a quello del servizio (R40), e
		// copre l'intera richiesta: connessione, attesa e lettura del corpo.
		return fmt.Errorf("timeout: la richiesta non si è conclusa entro il tempo consentito: %w",
			context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		// L'arresto del processo: il worker pool riconosce il caso e rilascia la
		// riga invece di scriverci un esito che non c'è stato.
		return fmt.Errorf("esecuzione interrotta prima della risposta: %w", context.Canceled)
	}
	return fmt.Errorf("richiesta non riuscita: %w", err)
}

// resolveError traduce il guasto della risoluzione dei segreti in un errore da
// mostrare all'utente.
//
// I due casi non si somigliano.
//
// Un [*secrets.ValidationError] è un riferimento che non si risolve: il segreto è
// stato revocato o rinominato **dopo** il sync. Il suo testo dice quale nome
// manca e quali esistono, non contiene nessun valore, ed è la stessa frase che
// l'utente avrebbe letto al `git push` se il segreto fosse mancato già allora —
// R43 vuole che l'errore arrivi lì, e quando arriva qui è perché il mondo è
// cambiato in mezzo. Passa così com'è.
//
// Tutto il resto è un guasto nostro: il database che non risponde, un testo
// cifrato che non si apre. Il suo messaggio non arriva alla colonna `error` — è
// visibile all'utente e non gli direbbe niente su cui possa agire — ma la causa
// resta agganciata, perché è su di lei che internal/dispatch distingue il
// timeout dal fallimento e che il pool riconosce l'arresto.
func resolveError(err error) error {
	if invalid, ok := secrets.AsValidation(err); ok {
		return fmt.Errorf("segreti del workspace non risolvibili: %w", invalid)
	}
	return opaqueError{
		text:  "risoluzione dei segreti del workspace non riuscita",
		cause: err,
	}
}

// opaqueError separa il testo che l'utente legge dalla causa che il chiamante
// classifica: `Error()` è la frase da scrivere sulla riga, `Unwrap()` conserva
// l'errore vero per `errors.Is`.
type opaqueError struct {
	text  string
	cause error
}

func (e opaqueError) Error() string { return e.text }
func (e opaqueError) Unwrap() error { return e.cause }

// responseError descrive una risposta cominciata e non finita.
func responseError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("timeout: la risposta è arrivata ma non si è conclusa entro il tempo consentito: %w",
			context.DeadlineExceeded)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("lettura della risposta interrotta: %w", context.Canceled)
	}
	return fmt.Errorf("lettura della risposta non riuscita: %w", err)
}
