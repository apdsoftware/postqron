package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/marketing"
)

// Il contratto delle rotte del marketing (Privacy Policy §2.8), in breve.
//
//	GET    /marketing/consent       stato del consenso            200  JSON
//	POST   /marketing/consent       presta il consenso            200  JSON
//	DELETE /marketing/consent       ritira il consenso            200  JSON
//	GET    /marketing/unsubscribe   pagina di conferma            200  HTML
//	POST   /marketing/unsubscribe   esegue la disiscrizione       200  HTML
//
// Sono due gruppi con due credenziali diverse, e la differenza **è** §2.8.
//
// # Il consenso si presta da solo
//
// §2.8 dice che il consenso è «asked for on its own and never bundled with
// accepting the terms or creating an account». Nel codice significa che
// `POST /marketing/consent` non fa nient'altro: non ha un corpo, non accetta
// campi, non tocca il profilo. Non esiste un `marketing_consent` fra i campi di
// `POST /auth/register`, e non deve esistere: un consenso raccolto insieme a
// qualcos'altro non è il consenso che il documento descrive.
//
// **Solo sessione**, come `/keys` e `/secrets`. Una chiave API dimenticata in un
// file di configurazione non deve poter prestare, a nome del suo proprietario,
// un consenso che vale come base giuridica.
//
// # La disiscrizione funziona senza accedere
//
// §2.8 dice che il link «works with one click and without signing in». Le due
// rotte stanno quindi **fuori** dal guard dell'identità, e la loro credenziale è
// la firma del token — vedi [marketing.Signer].
//
// La `GET` **non cambia niente**, e non è pedanteria sui verbi HTTP. Quel link
// arriva dentro un'email, e le email vengono aperte anche da chi non è
// l'utente: gli scanner antivirus dei server di posta aziendali visitano ogni
// indirizzo di ogni messaggio in arrivo, i browser fanno prefetch, i crawler
// raccolgono. Se la `GET` disiscrivesse, quelle persone smetterebbero di
// ricevere le comunicazioni **senza aver mai cliccato** e senza saperlo: un
// danno silenzioso, in cui perdiamo il contatto con chi lo voleva e non ce ne
// accorgiamo — R20.1 ci toglie anche il modo di misurarlo.
//
// La conferma è quindi una `POST`, e la pagina che la `GET` restituisce è un
// form con un pulsante solo. Il costo è **un clic in più** rispetto alla lettera
// di §2.8, ed è dichiarato: la frase del documento va letta come «un clic per
// aprirlo, uno per confermare». In cambio quella pagina è anche l'unico posto in
// cui possiamo dire *prima* di agire che le email transazionali continuano ad
// arrivare — che è un'altra promessa di §2.8, e che una disiscrizione silenziosa
// su `GET` non avrebbe modo di mantenere.
//
// # Perché queste due rispondono HTML e tutto il resto JSON
//
// Perché non le apre il nostro frontend: le apre un client di posta. Servite da
// qui funzionano senza JavaScript, senza cookie, senza CORS e senza che i
// frontend statici siano in piedi — cioè con il minor numero possibile di cose
// che possano impedire a una promessa legale di essere mantenuta.
//
// Codici di errore delle rotte del consenso, stabili per il branching
// applicativo (R53):
//
//	401 unauthenticated   sessione assente o scaduta
//	429 rate_limited      troppi tentativi, con `Retry-After`
//
// Le due rotte di disiscrizione non rispondono con [ErrorBody]: rispondono con
// una pagina, perché chi le apre è una persona davanti a un browser e non un
// client che fa branching su un codice.
type marketingAPI struct {
	*guard
	svc  *marketing.Service
	page *marketing.Page
	log  *slog.Logger
}

func newMarketingAPI(guard *guard, logger *slog.Logger, svc *marketing.Service, page *marketing.Page) *marketingAPI {
	return &marketingAPI{guard: guard, svc: svc, page: page, log: logger}
}

// routes registra le rotte del marketing.
func (a *marketingAPI) routes(mux router) {
	mux.HandleFunc("GET /marketing/consent", a.authenticated(a.status))
	mux.HandleFunc("POST /marketing/consent", a.authenticated(a.grant))
	mux.HandleFunc("DELETE /marketing/consent", a.authenticated(a.withdraw))

	// Fuori dal guard: §2.8 promette che funzionino senza accedere.
	mux.HandleFunc("GET "+marketing.UnsubscribePath, a.unsubscribePage)
	mux.HandleFunc("POST "+marketing.UnsubscribePath, a.unsubscribe)
}

// ------------------------------------------------------------------- corpi

// MarketingConsentResponse è lo stato del consenso alle email di prodotto.
//
// Non contiene l'indirizzo email e non contiene la traccia: il primo l'utente lo
// sa già, la seconda è la prova che teniamo noi e non un dato da esporre a ogni
// caricamento della pagina delle impostazioni.
type MarketingConsentResponse struct {
	// Consented è vero solo se l'ultima decisione è un consenso.
	Consented bool `json:"consented"`
	// Decided è falso per chi non ha mai scelto. È distinto da `consented`
	// falso, e la distinzione serve alla dashboard: «non hai ancora scelto» e
	// «hai detto di no» si mostrano in modo diverso, e nessuno dei due fa
	// partire un'email.
	Decided bool `json:"decided"`
	// DecidedAt è quando quella decisione è stata presa. Assente per chi non ha
	// mai scelto.
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

// ------------------------------------------------------------------ handler

func (a *marketingAPI) status(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	state, err := a.svc.Status(r.Context(), user.ID)
	if err != nil {
		a.guard.failAuth(w, r, err)
		return
	}
	a.writeState(w, r, state)
}

// grant registra il consenso dell'Art. 6(1)(a).
//
// **Non legge un corpo**, e non è una semplificazione: un corpo sarebbe il posto
// in cui, un giorno, qualcuno infilerebbe un secondo campo — e §2.8 promette che
// questo consenso non viaggi insieme a nient'altro. La richiesta è il consenso.
func (a *marketingAPI) grant(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	a.decide(w, r, user, a.svc.Grant)
}

// withdraw ritira il consenso dalle impostazioni dell'account.
//
// È la strada di chi è già collegato. L'altra — il link nell'email, senza
// sessione — è [marketingAPI.unsubscribe], e §2.8 le promette entrambe.
func (a *marketingAPI) withdraw(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	a.decide(w, r, user, a.svc.Withdraw)
}

// decide applica una delle due decisioni e risponde con lo stato risultante.
//
// Le due rotte differiscono per una funzione sola, e tenerle così è ciò che
// garantisce che rispondano nello stesso modo: se il consenso e la revoca
// avessero due handler completi, la prima cosa a divergere sarebbe la forma
// della risposta.
func (a *marketingAPI) decide(
	w http.ResponseWriter,
	r *http.Request,
	user auth.User,
	apply func(ctx context.Context, userID, ip string) (marketing.Applied, error),
) {
	if _, err := apply(r.Context(), user.ID, ClientIP(r, a.trustedProxies).String()); err != nil {
		a.guard.failAuth(w, r, err)
		return
	}

	// Si rilegge lo stato invece di dedurlo dalla decisione appena scritta: è
	// una richiesta in più su un percorso freddo, e in cambio la risposta dice
	// ciò che il database contiene davvero — compreso il caso in cui la
	// decisione non è stata scritta perché non cambiava niente.
	state, err := a.svc.Status(r.Context(), user.ID)
	if err != nil {
		a.guard.failAuth(w, r, err)
		return
	}
	a.writeState(w, r, state)
}

func (a *marketingAPI) writeState(w http.ResponseWriter, r *http.Request, state marketing.State) {
	body := MarketingConsentResponse{Consented: state.Consented, Decided: state.Decided}
	if state.Decided {
		decidedAt := state.OccurredAt
		body.DecidedAt = &decidedAt
	}
	writeJSON(w, r, a.log, http.StatusOK, body)
}

// ------------------------------------------------------------ disiscrizione

// unsubscribePage risponde alla `GET`: verifica il token e **non cambia niente**.
func (a *marketingAPI) unsubscribePage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")

	preview, err := a.svc.Preview(r.Context(), token)
	switch {
	case errors.Is(err, marketing.ErrInvalidToken):
		a.writePage(w, r, marketing.PageInvalid, "", "", http.StatusNotFound)
		return
	case err != nil:
		a.log.ErrorContext(r.Context(), "pagina di disiscrizione non costruita", slog.Any("error", err))
		a.writePage(w, r, marketing.PageInvalid, "", "", http.StatusInternalServerError)
		return
	}

	state := marketing.PageConfirm
	if !preview.Consented {
		state = marketing.PageAlready
	}
	a.writePage(w, r, state, preview.Language, token, http.StatusOK)
}

// unsubscribe risponde alla `POST`: esegue la revoca.
//
// Il token arriva dal form e non dalla query, così che la richiesta che cambia
// lo stato porti con sé la propria credenziale nel corpo — e non nella riga di
// un URL che finisce nei log di ogni intermediario.
//
// Il corpo è `application/x-www-form-urlencoded` e non JSON, ed è l'unica rotta
// del servizio a esserlo: lo manda il form della pagina precedente, che deve
// funzionare senza JavaScript.
func (a *marketingAPI) unsubscribe(w http.ResponseWriter, r *http.Request) {
	// Il tetto è quello delle altre rotte: il corpo è un campo solo.
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	if err := r.ParseForm(); err != nil {
		a.writePage(w, r, marketing.PageInvalid, "", "", http.StatusBadRequest)
		return
	}

	outcome, err := a.svc.Unsubscribe(r.Context(), r.PostFormValue("token"),
		ClientIP(r, a.trustedProxies).String())
	switch {
	case errors.Is(err, marketing.ErrInvalidToken):
		a.writePage(w, r, marketing.PageInvalid, "", "", http.StatusNotFound)
		return
	case err != nil:
		a.log.ErrorContext(r.Context(), "disiscrizione non eseguita", slog.Any("error", err))
		a.writePage(w, r, marketing.PageInvalid, "", "", http.StatusInternalServerError)
		return
	}

	// `Unchanged` non è un errore: è il secondo clic, o il rinvio del form. La
	// pagina lo dice con parole diverse perché all'utente interessa sapere che è
	// a posto, non che la sua richiesta era ridondante.
	state := marketing.PageDone
	if outcome.Applied == marketing.Unchanged {
		state = marketing.PageAlready
	}
	a.writePage(w, r, state, outcome.Language, "", http.StatusOK)
}

// writePage scrive la pagina di disiscrizione.
//
// `no-store` non è cerimoniale: la pagina di conferma contiene il token nel
// campo nascosto del form, e una cache condivisa — un proxy aziendale, per
// esempio — la servirebbe a chi chiede lo stesso indirizzo. `noindex` è nel
// documento, e qui c'è la testata che vale anche per chi non legge l'HTML.
func (a *marketingAPI) writePage(
	w http.ResponseWriter, r *http.Request,
	state marketing.PageState, language, token string, status int,
) {
	html, err := a.page.Render(state, language, token)
	if err != nil {
		// La pagina non si compila: resta solo da non mentire sullo stato. Non si
		// ripiega su un'altra pagina, che fallirebbe allo stesso modo.
		a.log.ErrorContext(r.Context(), "pagina di disiscrizione non compilata",
			slog.String("state", string(state)), slog.Any("error", err))
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Robots-Tag", "noindex, nofollow")
	header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")
	header.Set("Referrer-Policy", "no-referrer")

	w.WriteHeader(status)
	if _, err := w.Write(html); err != nil {
		a.log.ErrorContext(r.Context(), "scrittura della pagina di disiscrizione fallita", slog.Any("error", err))
	}
}
