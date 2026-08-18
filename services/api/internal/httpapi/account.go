package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/account"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
)

// Il contratto delle rotte di cancellazione dell'account (R45), in breve.
//
//	GET    /account/deletion   stato della richiesta      200
//	POST   /account/deletion   chiede la cancellazione    202
//	DELETE /account/deletion   annulla la richiesta       200
//
// La risorsa è **la richiesta di cancellazione**, non l'account: è ciò che si
// crea, si consulta e si ritira, e ha uno stato che dura trenta giorni. Un
// `DELETE /account` avrebbe suggerito un'operazione istantanea, che è
// esattamente ciò che questa non è.
//
// Codici di errore, stabili e pensati per il branching applicativo (R53):
//
//	400 validation_failed          password mancante
//	400 invalid_request            corpo illeggibile o campo sconosciuto
//	401 unauthenticated            sessione assente o scaduta
//	401 invalid_credentials        password di conferma sbagliata
//	409 deletion_already_requested c'è già una cancellazione in corso
//	409 deletion_not_requested     non c'è niente da annullare
//	409 subscription_active        manca la presa d'atto sull'abbonamento
//	429 rate_limited               troppi tentativi di conferma, con `Retry-After`
//
// `subscription_active` porta nel corpo il piano e l'identificativo della
// sottoscrizione: la dashboard deve poter dire *cosa* resterà vivo, e senza
// quei due valori dovrebbe fare una seconda richiesta per costruire il
// messaggio.
type accountAPI struct {
	*guard
	svc *account.Service
	log *slog.Logger
}

func newAccountAPI(guard *guard, logger *slog.Logger, svc *account.Service) *accountAPI {
	return &accountAPI{guard: guard, svc: svc, log: logger}
}

// routes registra le rotte della cancellazione.
//
// **Tutte esigono una sessione** (`authenticated`), non una Identity, ed è la
// scelta più severa fra quelle disponibili — la stessa di `/keys` e `/secrets`,
// con la posta in gioco più alta di tutte. Una chiave API che potesse chiedere
// la cancellazione dell'account renderebbe una credenziale di servizio, magari
// dimenticata in un file di configurazione, sufficiente a distruggere il lavoro
// di qualcuno. Per cancellare un account serve la credenziale che dimostra di
// *essere* l'utente — e in aggiunta, sulla richiesta, la sua password.
func (a *accountAPI) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /account/deletion", a.authenticated(a.status))
	mux.HandleFunc("POST /account/deletion", a.authenticated(a.request))
	mux.HandleFunc("DELETE /account/deletion", a.authenticated(a.cancel))
}

// ------------------------------------------------------------------- corpi

type requestDeletionRequest struct {
	// Password è la conferma di R45.
	Password string `json:"password"`

	// SubscriptionAcknowledged è la presa d'atto che chiudere l'account non
	// annulla l'abbonamento presso Paddle. Serve **solo** quando ce n'è uno
	// vivo, e il client lo scopre dal rifiuto `subscription_active` oppure da
	// `GET /account/deletion`, che dice la stessa cosa prima di provarci.
	SubscriptionAcknowledged bool `json:"subscription_acknowledged"`
}

// DeletionStatusResponse è lo stato della cancellazione.
type DeletionStatusResponse struct {
	Requested   bool       `json:"requested"`
	RequestedAt *time.Time `json:"requested_at,omitempty"`
	PurgeAfter  *time.Time `json:"purge_after,omitempty"`

	// GraceHours è il periodo di ripensamento in vigore, in ore. Serve alla
	// schermata di conferma, che deve dire quanto tempo si ha per cambiare idea
	// **prima** che l'utente chieda. In ore e non in giorni perché la
	// configurazione ammette qualunque durata, e un ambiente di prova a un'ora
	// non deve arrotondare a zero giorni.
	GraceHours float64 `json:"grace_hours"`

	// Subscription è presente solo se c'è un abbonamento a pagamento vivo.
	Subscription *DeletionSubscription `json:"subscription,omitempty"`
}

// DeletionSubscription è l'abbonamento che la cancellazione **non** annulla.
type DeletionSubscription struct {
	PlanCode             string `json:"plan_code"`
	PaddleSubscriptionID string `json:"paddle_subscription_id"`
}

// DeletionReceiptResponse è ciò che la richiesta ha fermato.
//
// I numeri ci sono perché la privacy policy §5 promette che le esecuzioni si
// fermano e le chiavi si revocano *immediatamente*: una risposta che dicesse
// solo «va bene» lascerebbe all'utente il compito di verificarlo andando a
// guardare altrove.
type DeletionReceiptResponse struct {
	RequestedAt time.Time `json:"requested_at"`
	PurgeAfter  time.Time `json:"purge_after"`

	JobsStopped     int `json:"jobs_stopped"`
	KeysRevoked     int `json:"api_keys_revoked"`
	SecretsRevoked  int `json:"secrets_revoked"`
	AIKeysRevoked   int `json:"ai_keys_revoked"`
	SessionsRevoked int `json:"sessions_revoked"`
}

// DeletionRestoredResponse è ciò che l'annullamento ha rimesso in piedi.
//
// Non contiene le chiavi, e la loro assenza è il fatto: la revoca ha svuotato il
// materiale cifrato, quindi non c'è niente da restituire. Chi torna indietro
// ritrova i propri job e i propri dati, e si riemette le chiavi.
type DeletionRestoredResponse struct {
	JobsResumed int `json:"jobs_resumed"`
}

// ------------------------------------------------------------------ handler

// status racconta la cancellazione in corso, se c'è.
func (a *accountAPI) status(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	st, err := a.svc.Status(r.Context(), user.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}

	body := DeletionStatusResponse{
		Requested:  st.Requested,
		GraceHours: a.svc.Grace().Hours(),
	}
	if st.Requested {
		requestedAt, purgeAfter := st.RequestedAt, st.PurgeAfter
		body.RequestedAt, body.PurgeAfter = &requestedAt, &purgeAfter
	}
	if st.Subscription.Paid {
		body.Subscription = &DeletionSubscription{
			PlanCode:             st.Subscription.PlanCode,
			PaddleSubscriptionID: st.Subscription.PaddleSubscriptionID,
		}
	}
	writeJSON(w, r, a.log, http.StatusOK, body)
}

// request apre la finestra di ripensamento, ferma le esecuzioni e revoca le
// chiavi.
//
// **202 e non 200**: la richiesta è presa in carico e ciò che promette — la
// rimozione dei dati — avverrà fra trenta giorni. Un 200 direbbe che è fatta.
func (a *accountAPI) request(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	var body requestDeletionRequest
	if !a.decode(w, r, &body) {
		return
	}
	if body.Password == "" {
		// Il controllo è qui e non nel servizio perché è una proprietà del corpo
		// della richiesta: senza, il campo mancante arriverebbe alla verifica come
		// una password sbagliata, cioè un 401 dove l'utente ha semplicemente
		// lasciato vuoto un campo del form.
		writeErrorDetail(w, r, a.log, http.StatusBadRequest, ErrorDetail{
			Code:    "validation_failed",
			Message: "La richiesta contiene campi non validi.",
			Details: []FieldErrorBody{{
				Field:   "password",
				Code:    "required",
				Message: "Conferma la cancellazione con la tua password.",
			}},
		})
		return
	}

	receipt, err := a.svc.RequestDeletion(r.Context(), user.ID, account.RequestInput{
		Password:                 body.Password,
		SubscriptionAcknowledged: body.SubscriptionAcknowledged,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	writeJSON(w, r, a.log, http.StatusAccepted, DeletionReceiptResponse{
		RequestedAt:     receipt.RequestedAt,
		PurgeAfter:      receipt.PurgeAfter,
		JobsStopped:     receipt.JobsStopped,
		KeysRevoked:     receipt.KeysRevoked,
		SecretsRevoked:  receipt.SecretsRevoked,
		AIKeysRevoked:   receipt.AIKeysRevoked,
		SessionsRevoked: receipt.SessionsRevoked,
	})
}

// cancel ritira la richiesta durante la grazia.
func (a *accountAPI) cancel(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	restored, err := a.svc.CancelDeletion(r.Context(), user.ID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, DeletionRestoredResponse{
		JobsResumed: restored.JobsResumed,
	})
}

// ------------------------------------------------------------------- errori

func (a *accountAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	var active *account.SubscriptionActiveError
	if errors.As(err, &active) {
		// Il messaggio dice le tre cose che servono a decidere: che l'abbonamento
		// resta vivo, chi lo gestisce e cosa succede al periodo già pagato (Termini
		// §4.3). Non è una formalità: senza rimborso pro rata, un utente che chiude
		// l'account senza annullare presso Paddle continua a pagare e non c'è nulla
		// che possiamo fare per lui dopo.
		writeErrorDetail(w, r, a.log, http.StatusConflict, ErrorDetail{
			Code: "subscription_active",
			Message: "Il tuo abbonamento è gestito da Paddle e questa cancellazione non lo annulla: " +
				"annullalo presso Paddle, oppure conferma di averne preso atto. " +
				"Il periodo già pagato arriva comunque a termine e non viene rimborsato.",
			Plan: active.Subscription.PlanCode,
		})
		return
	}

	switch {
	case errors.Is(err, account.ErrAlreadyRequested):
		writeError(w, r, a.log, http.StatusConflict, "deletion_already_requested",
			"La cancellazione è già in corso. Puoi annullarla finché il periodo di ripensamento non scade.")

	case errors.Is(err, account.ErrNotRequested):
		writeError(w, r, a.log, http.StatusConflict, "deletion_not_requested",
			"Non c'è nessuna cancellazione da annullare.")

	case errors.Is(err, account.ErrNotFound):
		// Chi ha una sessione valida ha un account: arrivarci significa che è stato
		// purgato fra il riconoscimento e questa istruzione. Il codice è quello
		// dell'autenticazione perché il rimedio è quello — rifare il login, e
		// scoprire che non c'è più niente a cui accedere.
		writeError(w, r, a.log, http.StatusUnauthorized, "unauthenticated",
			"Sessione assente o scaduta.")

	default:
		// Il resto — 401, 429 in testa — lo traduce writeAuthError, che è l'unico
		// posto in cui la corrispondenza fra errori e status esiste.
		a.guard.failAuth(w, r, err)
	}
}

// ------------------------------------------------------------------ supporto

func (a *accountAPI) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := decodeJSON(w, r, dst)
	if err == nil {
		return true
	}
	status, code := http.StatusBadRequest, "invalid_request"
	if errors.Is(err, errBodyTooLarge) {
		status, code = http.StatusRequestEntityTooLarge, "body_too_large"
	}
	// Come su `/secrets`: il messaggio del decodificatore **non** torna al client,
	// perché `json.Decoder` cita il testo che non è riuscito a leggere e nel corpo
	// di questa richiesta c'è una password.
	a.log.InfoContext(r.Context(), "corpo della richiesta non valido su /account/deletion",
		slog.String("code", code))
	writeError(w, r, a.log, status, code, "Corpo della richiesta non valido.")
	return false
}
