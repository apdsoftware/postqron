package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
)

// Il contratto delle rotte delle chiavi API (R9), in breve.
//
//	GET    /keys          elenco delle chiavi        200
//	POST   /keys          creazione                  201
//	DELETE /keys/{id}     revoca                     204
//	GET    /keys/scopes   scope assegnabili          200
//
// Non esiste una `GET /keys/{id}`, e la creazione non manda quindi un `Location`:
// di una singola chiave non c'è niente da leggere che l'elenco non dia già, e
// ogni rotta di lettura in più è un altro posto in cui la proprietà «il valore in
// chiaro non si rilegge» deve continuare a valere.
//
// Parametro di elenco: `include_revoked` (`true`/`false`), perché una chiave
// revocata resta in tabella come traccia storica e chi indaga su un incidente
// vuole vederla.
//
// Codici di errore, stabili e pensati per il branching applicativo (R53):
//
//	400 validation_failed      nome o scope non validi, con `details` per campo
//	400 invalid_request        corpo illeggibile o campo sconosciuto
//	401 unauthenticated        sessione assente o scaduta
//	404 api_key_not_found      inesistente, di un altro utente, o già revocata
//	409 too_many_api_keys      tetto di apikeys.MaxActiveKeys
//	429 rate_limited           troppe creazioni, con `Retry-After`
//
// keysAPI raccoglie le rotte delle chiavi API.
type keysAPI struct {
	*guard
	svc *apikeys.Service
	log *slog.Logger
}

func newKeysAPI(guard *guard, logger *slog.Logger, svc *apikeys.Service) *keysAPI {
	return &keysAPI{guard: guard, svc: svc, log: logger}
}

// routes registra le rotte delle chiavi.
//
// **Tutte e tre esigono una sessione** (`authenticated`), non una Identity: una
// chiave API non può creare, elencare né revocare chiavi, e non è una
// dimenticanza. Se potesse, una chiave di sola lettura sarebbe a una richiesta di
// distanza dall'emetterne una di scrittura, e l'intero sistema di scope
// diventerebbe una formalità aggirabile — non c'è nessuno scope che si possa
// assegnare a «gestisci le chiavi» che non abbia questo problema. Emettere una
// credenziale nuova richiede la credenziale che dimostra di *essere* l'utente,
// cioè la password che ha aperto la sessione.
//
// La conseguenza pratica è che l'automazione non può ruotarsi le chiavi da sola.
// È voluta: la rotazione è un'operazione che una persona decide.
func (a *keysAPI) routes(mux router) {
	mux.HandleFunc("GET /keys", a.authenticated(a.list))
	mux.HandleFunc("POST /keys", a.authenticated(a.create))
	mux.HandleFunc("DELETE /keys/{id}", a.authenticated(a.revoke))

	// L'elenco degli scope assegnabili è servito dal backend perché il form che
	// crea una chiave ha bisogno di conoscerlo: duplicarlo nel frontend
	// significherebbe due elenchi che divergono al primo scope aggiunto.
	mux.HandleFunc("GET /keys/scopes", a.authenticated(a.scopes))
}

// ------------------------------------------------------------------- corpi

type createKeyRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// ExpiresAt è facoltativo: assente significa «non scade».
	ExpiresAt *time.Time `json:"expires_at"`
}

// APIKeyResponse è una chiave come la vede il client.
//
// **Non contiene il valore in chiaro, e nemmeno la sua impronta.** Non è un
// filtro applicato in questa funzione: sono campi che questa struttura non ha,
// perché quello che non si serializza non può sfuggire per errore. È anche il
// motivo per cui non esiste una variante «di dettaglio» di questa risposta con
// qualcosa in più — non c'è niente di più da dare.
type APIKeyResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Prefix è la parte iniziale della chiave, in chiaro: serve a riconoscerla in
	// elenco, e non basta a ricostruirla.
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

// APIKeyListResponse è l'elenco delle chiavi.
type APIKeyListResponse struct {
	Keys []APIKeyResponse `json:"keys"`
}

// APIKeyCreatedResponse è la risposta alla creazione di una chiave.
//
// È l'unica risposta dell'intera API che contiene `secret`, e l'unica volta in
// cui quel valore esiste: dopo questa risposta non è più recuperabile da nessuna
// parte, per nessuno, nemmeno da un amministratore. `warning` esiste perché
// questa proprietà va detta al chiamante nel momento in cui gli serve saperla —
// un client che non la conosce mostra la chiave in un toast che scompare.
type APIKeyCreatedResponse struct {
	Key     APIKeyResponse `json:"key"`
	Secret  string         `json:"secret"`
	Warning string         `json:"warning"`
}

// ScopesResponse elenca gli scope assegnabili. Serve al form che crea una chiave:
// senza, l'elenco dei permessi sarebbe duplicato nel frontend e divergerebbe.
type ScopesResponse struct {
	Scopes []string `json:"scopes"`
}

// ------------------------------------------------------------------ handler

// list elenca le chiavi dell'utente.
func (a *keysAPI) list(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	invalid := &queryErrors{}
	includeRevoked := false
	if value := invalid.optionalBool("include_revoked", r.URL.Query().Get("include_revoked")); value != nil {
		includeRevoked = *value
	}
	if invalid.fail(w, r, a.log) {
		return
	}

	keys, err := a.svc.List(r.Context(), user.ID, includeRevoked)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, APIKeyListResponse{Keys: apiKeyResponses(keys)})
}

// create emette una chiave nuova e la mostra in chiaro, una volta sola.
func (a *keysAPI) create(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	var body createKeyRequest
	if !a.decode(w, r, &body) {
		return
	}

	scopes := make([]apikeys.Scope, 0, len(body.Scopes))
	for _, scope := range body.Scopes {
		scopes = append(scopes, apikeys.Scope(scope))
	}

	created, err := a.svc.Create(r.Context(), user.ID, apikeys.CreateInput{
		Name:      body.Name,
		Scopes:    scopes,
		ExpiresAt: body.ExpiresAt,
		Client:    apikeys.Client{IP: ClientIP(r, a.trustedProxies)},
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	// `Cache-Control: no-store` sulla sola risposta che contiene un segreto: senza,
	// un proxy o la cache del browser potrebbero conservarne una copia, che è
	// esattamente ciò che «la chiave si vede una volta» promette di non fare.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, a.log, http.StatusCreated, APIKeyCreatedResponse{
		Key:    apiKeyResponse(created.Key),
		Secret: created.Secret,
		Warning: "Copia questa chiave adesso: non sarà più possibile rileggerla. " +
			"Se la perdi, revocala e creane un'altra.",
	})
}

// scopes elenca gli scope assegnabili.
func (a *keysAPI) scopes(w http.ResponseWriter, r *http.Request, _ auth.User, _ auth.Session) {
	available := apikeys.Scopes()
	out := make([]string, 0, len(available))
	for _, scope := range available {
		out = append(out, string(scope))
	}
	writeJSON(w, r, a.log, http.StatusOK, ScopesResponse{Scopes: out})
}

// revoke revoca una chiave. L'effetto è immediato: la chiave non funziona già al
// tentativo successivo, senza attese di scadenza o di cache.
func (a *keysAPI) revoke(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	if err := a.svc.Revoke(r.Context(), user.ID, r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------- errori

func (a *keysAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	if invalid, ok := apikeys.AsValidation(err); ok {
		writeErrorDetail(w, r, a.log, http.StatusBadRequest, ErrorDetail{
			Code:    "validation_failed",
			Message: "La richiesta contiene campi non validi.",
			Details: keyFieldErrors(invalid.Fields),
		})
		return
	}
	// Il resto — 401, 404, 409, 429 — lo traduce writeAuthError, che è l'unico
	// posto in cui la corrispondenza fra errori e status esiste.
	a.guard.failAuth(w, r, err)
}

// ------------------------------------------------------------------ supporto

func (a *keysAPI) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := decodeJSON(w, r, dst)
	if err == nil {
		return true
	}
	return writeDecodeError(w, r, a.log, err)
}

func apiKeyResponse(key apikeys.Key) APIKeyResponse {
	scopes := make([]string, 0, len(key.Scopes))
	for _, scope := range key.Scopes {
		scopes = append(scopes, string(scope))
	}
	return APIKeyResponse{
		ID:         key.ID,
		Name:       key.Name,
		Prefix:     key.Prefix,
		Scopes:     scopes,
		CreatedAt:  key.CreatedAt,
		LastUsedAt: key.LastUsedAt,
		ExpiresAt:  key.ExpiresAt,
		RevokedAt:  key.RevokedAt,
		Revoked:    key.Revoked(),
	}
}

func apiKeyResponses(keys []apikeys.Key) []APIKeyResponse {
	out := make([]APIKeyResponse, 0, len(keys))
	for _, key := range keys {
		out = append(out, apiKeyResponse(key))
	}
	return out
}

func keyFieldErrors(fields []apikeys.FieldError) []FieldErrorBody {
	out := make([]FieldErrorBody, 0, len(fields))
	for _, field := range fields {
		out = append(out, FieldErrorBody{Field: field.Field, Code: field.Code, Message: field.Message})
	}
	return out
}
