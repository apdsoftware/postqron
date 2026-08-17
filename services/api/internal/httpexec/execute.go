package httpexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
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
	req, err := e.request(ctx, occ.Job)
	if err != nil {
		return dispatch.Result{}, err
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return dispatch.Result{}, requestError(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Il corpo non letto per intero — è la norma, il tetto di R40 è lì
			// per quello — rende la connessione non riusabile e la chiusura può
			// lamentarsene. Non è un fatto dell'esecuzione e non va sulla riga.
			e.log.DebugContext(ctx, "httpexec: chiusura del corpo della risposta", slog.Any("err", err))
		}
	}()

	body, err := e.excerpt(resp.Body)
	if err != nil {
		return dispatch.Result{ResponseStatus: resp.StatusCode}, responseError(err)
	}
	return dispatch.Result{ResponseStatus: resp.StatusCode, ResponseExcerpt: body}, nil
}

// request costruisce la richiesta a partire dal bersaglio del job (R1).
//
// Gli errori di questa funzione non citano mai il valore che li ha causati: URL
// e header possono contenere segreti risolti (R43), e questo messaggio finisce
// nella colonna `error`, che l'utente rilegge dall'API.
func (e *Executor) request(ctx context.Context, job scheduler.Job) (*http.Request, error) {
	target, err := url.Parse(job.URL)
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

	headers, err := job.HeaderMap()
	if err != nil {
		// L'errore originale non si propaga: cita il nome del job e potrebbe
		// citare il JSON che non ha saputo decodificare, cioè gli header.
		return nil, errors.New("header del job non decodificabili")
	}

	method := strings.ToUpper(strings.TrimSpace(job.Method))
	if method == "" {
		method = http.MethodGet
	}

	// strings.NewReader dà a NewRequestWithContext sia ContentLength sia GetBody:
	// il secondo è ciò che permette al client di ripetere il corpo su un redirect
	// 307 o 308, che senza sarebbe una richiesta rifatta a vuoto.
	var body io.Reader
	if job.Body != nil && *job.Body != "" {
		body = strings.NewReader(*job.Body)
	}

	// L'URL si passa come l'utente l'ha scritto, non nella forma riscritta da
	// url.String(): la richiesta lo riparserà con la stessa funzione, e una
	// normalizzazione in mezzo cambierebbe il percorso davvero chiamato rispetto
	// a quello che l'utente legge nel proprio job.
	req, err := http.NewRequestWithContext(ctx, method, job.URL, body)
	if err != nil {
		return nil, errors.New("richiesta del job non costruibile: metodo o URL non validi")
	}

	for name, value := range headers {
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

// excerpt legge l'estratto della risposta da conservare (R6, R40).
//
// Legge al massimo [Options.MaxResponseBytes] byte e **non tocca il resto**:
// drenare il corpo per poter riusare la connessione significherebbe leggere per
// intero il gigabyte che il tetto esiste per non leggere.
func (e *Executor) excerpt(body io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(body, e.maxBytes))
	if err != nil {
		return "", err
	}
	return storable(raw), nil
}

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
