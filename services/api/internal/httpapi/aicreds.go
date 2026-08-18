package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/aicreds"
	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/secretbox"
)

// Il contratto delle rotte delle chiavi AI (R18, BYOK), in breve.
//
//	GET    /ai/keys        elenco                 200
//	POST   /ai/keys        inserisce o sostituisce 201
//	DELETE /ai/keys/{id}   revoca                 204
//
// **Non esiste nessuna rotta che restituisca una chiave**, e non è una rotta
// dimenticata: non c'è `GET /ai/keys/{id}`, non c'è un parametro `reveal`, non
// c'è una variante amministrativa. La risposta serializza [AIKeyResponse], che
// il campo non ce l'ha. L'unico punto del prodotto in cui una chiave torna in
// chiaro è `aicreds.Service.Reveal`, che il backend chiama per parlare con il
// fornitore e che da qui non è raggiungibile.
//
// La risposta non contiene **nemmeno un frammento** della chiave. La 0007
// prevedeva `last_four` per farla riconoscere in dashboard; la 0016 ha tolto la
// colonna, e la ragione per esteso sta nel commento di quella migrazione: qui
// c'è una chiave per provider, quindi «quale chiave è questa» lo dice il
// provider e «è quella che ho appena ruotato» lo dice `updated_at`. Quattro
// caratteri della chiave in chiaro dentro ogni backup non pagavano ciò che
// aggiungevano.
//
// # Perché la creazione è una POST che sostituisce
//
// Perché una chiave viva per provider è ciò che il database ammette (indice
// `ai_credentials_live_provider_key`), e la seconda volta che un utente incolla
// la sua chiave Anthropic non sta creando un duplicato: la sta aggiornando.
// Rispondere `409` lo costringerebbe a revocare prima di incollare, cioè a
// passare da uno stato in cui il debugging AI non funziona per fare una cosa
// che voleva solo aggiornarlo. Non c'è quindi un `PATCH`: non ci sarebbe niente
// che possa fare e che la `POST` non faccia già.
//
// Parametro di elenco: `include_revoked` (`true`/`false`), perché una chiave
// revocata resta in tabella come traccia e chi indaga su un'analisi che ha
// smesso di funzionare vuole vederla.
//
// Codici di errore, stabili e pensati per il branching applicativo (R53):
//
//	400 validation_failed      provider, chiave o etichetta non validi, con `details`
//	400 invalid_request        corpo illeggibile o campo sconosciuto
//	401 unauthenticated        sessione assente o scaduta
//	404 ai_key_not_found       inesistente, di un altro utente, o già revocata
//
// aiKeysAPI raccoglie le rotte delle chiavi AI.
type aiKeysAPI struct {
	*guard
	svc *aicreds.Service
	log *slog.Logger
}

func newAIKeysAPI(guard *guard, logger *slog.Logger, svc *aicreds.Service) *aiKeysAPI {
	return &aiKeysAPI{guard: guard, svc: svc, log: logger}
}

// routes registra le rotte delle chiavi AI.
//
// **Tutte esigono una sessione** (`authenticated`), non una Identity, ed è la
// stessa scelta delle rotte `/keys` e `/secrets`. Il ragionamento è quello dei
// segreti applicato a una credenziale che non è nostra: una chiave API di
// Postqron che potesse *sostituire* la chiave AI dell'utente dirotterebbe verso
// un bersaglio scelto da chi ha la chiave le richieste che noi facciamo per suo
// conto, e gliele farebbe pagare sul conto del fornitore. Per toccare le chiavi
// AI serve la credenziale che dimostra di *essere* l'utente.
func (a *aiKeysAPI) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ai/keys", a.authenticated(a.list))
	mux.HandleFunc("POST /ai/keys", a.authenticated(a.save))
	mux.HandleFunc("DELETE /ai/keys/{id}", a.authenticated(a.revoke))
}

// ------------------------------------------------------------------- corpi

type saveAIKeyRequest struct {
	Provider string `json:"provider"`
	Key      string `json:"key"`
	Label    string `json:"label"`
}

// AIKeyResponse è una chiave AI come la vede il client.
//
// **Non contiene la chiave, e nemmeno un suo pezzo.** Non è un filtro applicato
// in questa funzione: è un campo che questa struttura non ha, perché quello che
// non si serializza non può sfuggire per errore. È la stessa scelta di
// [SecretResponse] e [APIKeyResponse] — un filtro si dimentica, un campo assente
// no.
type AIKeyResponse struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Label    string `json:"label,omitempty"`

	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Revoked    bool       `json:"revoked"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// AIKeyListResponse è l'elenco delle chiavi AI.
//
// `providers` elenca i fornitori ammessi: senza, la dashboard dovrebbe tenerne
// una copia, e le due liste divergerebbero il giorno in cui l'enumerato della
// 0001 cambia.
type AIKeyListResponse struct {
	Keys      []AIKeyResponse `json:"keys"`
	Providers []string        `json:"providers"`
}

// ------------------------------------------------------------------ handler

// list elenca le chiavi AI dell'utente.
func (a *aiKeysAPI) list(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
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
	writeJSON(w, r, a.log, http.StatusOK, AIKeyListResponse{
		Keys:      aiKeyResponses(found),
		Providers: providerNames(),
	})
}

// save registra la chiave di un provider, sostituendo quella viva se c'è.
func (a *aiKeysAPI) save(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	var body saveAIKeyRequest
	if !a.decode(w, r, &body) {
		return
	}

	credential, err := a.svc.Save(r.Context(), user.ID, aicreds.SaveInput{
		Provider: body.Provider,
		Key:      secretbox.Plaintext(body.Key),
		Label:    body.Label,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	// `Cache-Control: no-store` benché la risposta non contenga la chiave: la
	// *richiesta* sì, e un intermediario che memorizzasse lo scambio ne
	// conserverebbe una copia. È la stessa precauzione di `POST /secrets`.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, a.log, http.StatusCreated, aiKeyResponse(credential))
}

// revoke revoca una chiave AI.
//
// L'effetto è immediato e definitivo: la revoca cancella il materiale cifrato,
// quindi la chiave smette di esistere da noi. Il provider torna libero e
// l'utente può incollarne subito un'altra.
func (a *aiKeysAPI) revoke(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	if err := a.svc.Revoke(r.Context(), user.ID, r.PathValue("id")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------- errori

func (a *aiKeysAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	if invalid, ok := aicreds.AsValidation(err); ok {
		writeErrorDetail(w, r, a.log, http.StatusBadRequest, ErrorDetail{
			Code:    "validation_failed",
			Message: "La richiesta contiene campi non validi.",
			Details: aiKeyFieldErrors(invalid.Fields),
		})
		return
	}

	if errors.Is(err, aicreds.ErrCredentialNotFound) {
		writeError(w, r, a.log, http.StatusNotFound, "ai_key_not_found", "Chiave AI non trovata.")
		return
	}

	// Il resto — 401 in testa — lo traduce writeAuthError, che è l'unico posto in
	// cui la corrispondenza fra errori e status esiste.
	a.guard.failAuth(w, r, err)
}

// ------------------------------------------------------------------ supporto

func (a *aiKeysAPI) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := decodeJSON(w, r, dst)
	if err == nil {
		return true
	}
	status, code := http.StatusBadRequest, "invalid_request"
	if errors.Is(err, errBodyTooLarge) {
		status, code = http.StatusRequestEntityTooLarge, "body_too_large"
	}
	// Il messaggio dell'errore di decodifica **non** viene rimandato al client su
	// questa rotta: `json.Decoder` cita il testo che non è riuscito a leggere, e
	// su una richiesta che porta una chiave AI quel testo può essere la chiave.
	// È la stessa cautela di `POST /secrets`.
	a.log.InfoContext(r.Context(), "corpo della richiesta non valido su /ai/keys",
		slog.String("code", code))
	writeError(w, r, a.log, status, code,
		"Il corpo della richiesta non è JSON valido, oppure contiene campi sconosciuti. "+
			"Attesi: `provider`, `key`, `label`.")
	return false
}

func aiKeyResponse(credential aicreds.Credential) AIKeyResponse {
	return AIKeyResponse{
		ID:         credential.ID,
		Provider:   string(credential.Provider),
		Label:      credential.Label,
		LastUsedAt: credential.LastUsedAt,
		RevokedAt:  credential.RevokedAt,
		Revoked:    credential.Revoked(),
		CreatedAt:  credential.CreatedAt,
		UpdatedAt:  credential.UpdatedAt,
	}
}

func aiKeyResponses(found []aicreds.Credential) []AIKeyResponse {
	out := make([]AIKeyResponse, 0, len(found))
	for _, credential := range found {
		out = append(out, aiKeyResponse(credential))
	}
	return out
}

func aiKeyFieldErrors(fields []aicreds.FieldError) []FieldErrorBody {
	out := make([]FieldErrorBody, 0, len(fields))
	for _, field := range fields {
		out = append(out, FieldErrorBody{Field: field.Field, Code: field.Code, Message: field.Message})
	}
	return out
}

func providerNames() []string {
	providers := aicreds.Providers()
	out := make([]string, 0, len(providers))
	for _, provider := range providers {
		out = append(out, string(provider))
	}
	return out
}
