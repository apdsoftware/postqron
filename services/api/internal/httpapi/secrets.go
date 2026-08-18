package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/secrets"
)

// Il contratto delle rotte dei segreti del workspace (R42), in breve.
//
//	GET    /secrets          elenco                     200
//	POST   /secrets          creazione                  201
//	PATCH  /secrets/{id}     nuovo valore, nuova nota   200
//	DELETE /secrets/{id}     revoca                     204
//
// **Non esiste nessuna rotta che restituisca un valore**, e non è una rotta
// dimenticata: non c'è `GET /secrets/{id}`, non c'è un parametro `reveal`, non
// c'è una variante amministrativa. La risposta serializza [SecretResponse], che
// il campo non ce l'ha — la stessa scelta di [APIKeyResponse] per le chiavi API,
// e per la stessa ragione: un filtro si dimentica, un campo assente no.
//
// La differenza rispetto alle chiavi API è che qui il valore non torna **nemmeno
// alla creazione**. Lì il segreto lo generavamo noi, e la risposta alla creazione
// era l'unica occasione di consegnarlo; qui ce l'ha già l'utente, che l'ha appena
// incollato nel form.
//
// Parametro di elenco: `include_revoked` (`true`/`false`), perché un segreto
// revocato resta in tabella come traccia e chi indaga su un'esecuzione fallita
// vuole vederlo.
//
// Codici di errore, stabili e pensati per il branching applicativo (R53):
//
//	400 validation_failed      nome, valore o nota non validi, con `details`
//	400 invalid_request        corpo illeggibile o campo sconosciuto
//	401 unauthenticated        sessione assente o scaduta
//	404 secret_not_found       inesistente, di un altro workspace, o revocato
//	409 secret_name_taken      esiste già un segreto vivo con quel nome
//	409 too_many_secrets       tetto di secrets.MaxSecretsPerWorkspace
//
// secretsAPI raccoglie le rotte dei segreti.
type secretsAPI struct {
	*guard
	svc *secrets.Service
	log *slog.Logger
}

func newSecretsAPI(guard *guard, logger *slog.Logger, svc *secrets.Service) *secretsAPI {
	return &secretsAPI{guard: guard, svc: svc, log: logger}
}

// routes registra le rotte dei segreti.
//
// **Tutte esigono una sessione** (`authenticated`), non una Identity, ed è la
// stessa scelta delle rotte `/keys`. Il ragionamento però è diverso e vale la
// pena scriverlo: lì il rischio era l'escalation — una chiave di sola lettura a
// un passo dall'emetterne una di scrittura. Qui il rischio è più diretto. I
// segreti sono le credenziali con cui i job dell'utente si autenticano verso i
// suoi servizi; una chiave API che potesse *sostituirne* il valore dirotterebbe
// quelle credenziali verso un bersaglio scelto da chi ha la chiave, senza che
// nessun log dell'utente mostri un valore cambiato. Per toccare i segreti serve
// la credenziale che dimostra di *essere* l'utente.
func (a *secretsAPI) routes(mux router) {
	mux.HandleFunc("GET /secrets", a.authenticated(a.list))
	mux.HandleFunc("POST /secrets", a.authenticated(a.create))
	mux.HandleFunc("PATCH /secrets/{id}", a.authenticated(a.update))
	mux.HandleFunc("DELETE /secrets/{id}", a.authenticated(a.revoke))
}

// ------------------------------------------------------------------- corpi

type createSecretRequest struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

type updateSecretRequest struct {
	Value string `json:"value"`
	// Description assente lascia la nota com'era; presente e vuota la cancella.
	Description *string `json:"description"`
}

// SecretResponse è un segreto come lo vede il client.
//
// **Non contiene il valore, e nemmeno un suo pezzo.** Non è un filtro applicato
// in questa funzione: è un campo che questa struttura non ha, perché quello che
// non si serializza non può sfuggire per errore. Non c'è nemmeno un'anteprima
// tipo `last_four`: quella ha senso per una chiave di un fornitore noto, che si
// riconosce dalla coda, non per un valore arbitrario del cliente che potrebbe
// *essere* quattro caratteri significativi.
//
// A riconoscere un segreto basta il nome che gli ha dato l'utente, che è anche
// ciò che scrive nel suo `cron.yaml`.
type SecretResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Reference è il testo da incollare in `cron.yaml`. È ridondante rispetto a
	// `name` e c'è apposta: è ciò che si copia con un click, e senza di esso
	// l'utente deve sapere a memoria che la sintassi è `${...}`.
	Reference   string `json:"reference"`
	Description string `json:"description,omitempty"`

	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Revoked    bool       `json:"revoked"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// SecretListResponse è l'elenco dei segreti.
type SecretListResponse struct {
	Secrets []SecretResponse `json:"secrets"`
}

// ------------------------------------------------------------------ handler

// list elenca i segreti del workspace.
func (a *secretsAPI) list(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	invalid := &queryErrors{}
	includeRevoked := false
	if value := invalid.optionalBool("include_revoked", r.URL.Query().Get("include_revoked")); value != nil {
		includeRevoked = *value
	}
	if invalid.fail(w, r, a.log) {
		return
	}

	found, err := a.svc.List(r.Context(), user.ID, includeRevoked)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, SecretListResponse{Secrets: secretResponses(found)})
}

// create registra un segreto nuovo.
func (a *secretsAPI) create(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	var body createSecretRequest
	if !a.decode(w, r, &body) {
		return
	}

	secret, err := a.svc.Create(r.Context(), user.ID, secrets.CreateInput{
		Name:        body.Name,
		Value:       secrets.Value(body.Value),
		Description: body.Description,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	// `Cache-Control: no-store` benché la risposta non contenga il valore: la
	// *richiesta* sì, e un intermediario che memorizzasse lo scambio ne
	// conserverebbe una copia. È la stessa precauzione della creazione di una
	// chiave API, applicata al verso opposto del canale.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, a.log, http.StatusCreated, secretResponse(secret))
}

// update sostituisce il valore di un segreto.
//
// Non esiste un modo di cambiare il **nome**: rinominare romperebbe in silenzio
// ogni `cron.yaml` che lo riferisce, e la rottura si vedrebbe alla prima
// esecuzione invece che al sync — il contrario esatto di ciò che R43 chiede.
func (a *secretsAPI) update(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	var body updateSecretRequest
	if !a.decode(w, r, &body) {
		return
	}

	secret, err := a.svc.Update(r.Context(), user.ID, r.PathValue("id"), secrets.UpdateInput{
		Value:       secrets.Value(body.Value),
		Description: body.Description,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, a.log, http.StatusOK, secretResponse(secret))
}

// revoke revoca un segreto.
//
// L'effetto è immediato e definitivo: la revoca cancella il testo cifrato, quindi
// il valore smette di esistere. I job che lo riferiscono cominciano a fallire, e
// devono — il registro delle esecuzioni dirà quale nome manca.
func (a *secretsAPI) revoke(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	if err := a.svc.Revoke(r.Context(), user.ID, r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------- errori

func (a *secretsAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	if invalid, ok := secrets.AsValidation(err); ok {
		writeErrorDetail(w, r, a.log, http.StatusBadRequest, ErrorDetail{
			Code:    "validation_failed",
			Message: "La richiesta contiene campi non validi.",
			Details: secretFieldErrors(invalid.Fields),
		})
		return
	}

	switch {
	case errors.Is(err, secrets.ErrSecretNotFound):
		writeError(w, r, a.log, http.StatusNotFound, "secret_not_found", "Segreto non trovato.")

	case errors.Is(err, secrets.ErrDuplicateName):
		writeError(w, r, a.log, http.StatusConflict, "secret_name_taken",
			"Esiste già un segreto con questo nome. Aggiornane il valore invece di crearne un altro, "+
				"oppure revoca quello esistente.")

	case errors.Is(err, secrets.ErrTooManySecrets):
		writeError(w, r, a.log, http.StatusConflict, "too_many_secrets",
			"Hai raggiunto il numero massimo di segreti: revocane uno prima di crearne un altro.")

	default:
		// Il resto — 401 in testa — lo traduce writeAuthError, che è l'unico posto
		// in cui la corrispondenza fra errori e status esiste.
		a.guard.failAuth(w, r, err)
	}
}

// ------------------------------------------------------------------ supporto

func (a *secretsAPI) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := decodeJSON(w, r, dst)
	if err == nil {
		return true
	}
	status, code := http.StatusBadRequest, "invalid_request"
	if errors.Is(err, errBodyTooLarge) {
		status, code = http.StatusRequestEntityTooLarge, "body_too_large"
	}
	// Il messaggio dell'errore di decodifica **non** viene rimandato al client
	// così com'è su queste rotte: `json.Decoder` cita il testo che non è riuscito
	// a leggere, e su una richiesta che porta un segreto quel testo può essere il
	// segreto. Le altre rotte se lo permettono perché nel loro corpo non c'è
	// niente da nascondere.
	a.log.InfoContext(r.Context(), "corpo della richiesta non valido su /secrets",
		slog.String("code", code))
	writeError(w, r, a.log, status, code,
		"Il corpo della richiesta non è JSON valido, oppure contiene campi sconosciuti. "+
			"Attesi: `name`, `value`, `description`.")
	return false
}

func secretResponse(secret secrets.Secret) SecretResponse {
	return SecretResponse{
		ID:          secret.ID,
		Name:        secret.Name,
		Reference:   "${" + secret.Name + "}",
		Description: secret.Description,
		LastUsedAt:  secret.LastUsedAt,
		RevokedAt:   secret.RevokedAt,
		Revoked:     secret.Revoked(),
		CreatedAt:   secret.CreatedAt,
		UpdatedAt:   secret.UpdatedAt,
	}
}

func secretResponses(found []secrets.Secret) []SecretResponse {
	out := make([]SecretResponse, 0, len(found))
	for _, secret := range found {
		out = append(out, secretResponse(secret))
	}
	return out
}

func secretFieldErrors(fields []secrets.FieldError) []FieldErrorBody {
	out := make([]FieldErrorBody, 0, len(fields))
	for _, field := range fields {
		out = append(out, FieldErrorBody{Field: field.Field, Code: field.Code, Message: field.Message})
	}
	return out
}
