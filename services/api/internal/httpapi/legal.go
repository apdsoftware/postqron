package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/auth"
	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// Il contratto delle rotte del consenso ai documenti legali (R46), in breve.
//
//	GET  /legal/consents   cosa ho accettato, cosa manca, cosa cambia   200
//	POST /legal/consents   accetto                                      200
//
// La risorsa è **il consenso**, non il documento: i testi li serve il sito, con
// le sue pagine legali. Un'API che restituisse il contenuto dei documenti
// duplicherebbe una responsabilità che è già di qualcun altro, e ne creerebbe
// una seconda copia da tenere allineata.
//
// Codici di errore, stabili e pensati per il branching applicativo (R53):
//
//	400 validation_failed             documento o lingua fuori dominio, elenco vuoto
//	400 invalid_request               corpo illeggibile o campo sconosciuto
//	401 unauthenticated               sessione assente o scaduta
//	409 legal_version_not_in_force    accettata una versione che non è quella in vigore
//	429 rate_limited                  tetto tecnico
//
// `legal_version_not_in_force` porta nel corpo le due versioni, e non è una
// cortesia: la differenza fra «hai accettato una versione superata» e «hai
// accettato una versione non ancora in vigore» è la differenza fra ricaricare la
// pagina e aspettare, e senza entrambi i numeri nessun client può dirlo.
type legalAPI struct {
	*guard
	svc *legal.Service
	log *slog.Logger
}

func newLegalAPI(guard *guard, logger *slog.Logger, svc *legal.Service) *legalAPI {
	return &legalAPI{guard: guard, svc: svc, log: logger}
}

// routes registra le rotte del consenso.
//
// **Tutte esigono una sessione** (`authenticated`), non una Identity, ed è la
// scelta più severa fra quelle disponibili — la stessa di `/keys`, `/secrets` e
// `/account/deletion`. La ragione qui è diversa dalle altre e più semplice:
// accettare un contratto è un atto della persona. Una chiave API che potesse
// farlo permetterebbe a una credenziale di servizio, dimenticata in un file di
// configurazione, di vincolare qualcuno a un documento che non ha letto — e la
// prova che ne resterebbe direbbe il falso.
func (a *legalAPI) routes(mux router) {
	mux.HandleFunc("GET /legal/consents", a.authenticated(a.state))
	mux.HandleFunc("POST /legal/consents", a.authenticated(a.accept))
}

// ------------------------------------------------------------------- corpi

type acceptConsentsRequest struct {
	// Language è la lingua in cui l'utente ha letto i testi.
	Language string `json:"language"`
	// Documents elenca cosa accetta, con la versione che il client dichiara di
	// aver mostrato.
	Documents []acceptConsentDocument `json:"documents"`
}

type acceptConsentDocument struct {
	Document string `json:"document"`
	// Version è la versione mostrata all'utente. È obbligatoria e verificata:
	// vedi [legalAPI.accept].
	Version string `json:"version"`
}

// LegalConsentResponse è una prova di consenso già prestata.
type LegalConsentResponse struct {
	Document string `json:"document"`
	Version  string `json:"version"`
	// Language è la lingua del testo mostrato, che può non essere quella
	// dell'interfaccia: il consenso vale su ciò che l'utente ha letto.
	Language string `json:"language"`
	// DocumentChecksum è l'impronta SHA-256 del testo accettato. È ciò che
	// permette di verificare *quale* testo era, senza fidarsi del numero di
	// versione.
	DocumentChecksum string    `json:"document_checksum"`
	Source           string    `json:"source"`
	AcceptedAt       time.Time `json:"accepted_at"`
}

// LegalRequirementResponse è un documento in vigore che manca da accettare.
type LegalRequirementResponse struct {
	Document string `json:"document"`
	Version  string `json:"version"`
	// Language è la lingua in cui il testo verrà mostrato, che non è sempre
	// quella chiesta: una traduzione che non esiste ancora si risolve
	// nell'inglese (SPEC §8-bis).
	Language      string    `json:"language"`
	EffectiveDate time.Time `json:"effective_date"`
	// AcceptedVersion è la versione accettata in precedenza, assente se non ce
	// n'è nessuna. È ciò che distingue «non hai ancora accettato niente» da «è
	// cambiato», che per l'utente sono due schermate diverse.
	AcceptedVersion string `json:"accepted_version,omitempty"`
}

// LegalChangeResponse è un cambiamento annunciato e non ancora in vigore.
//
// È la metà dei Termini §9 che si può mostrare: c'è una versione nuova, prende
// effetto quel giorno, e fino ad allora vincola ancora quella accettata. Chi non
// la accetta può chiudere l'account prima (R45).
type LegalChangeResponse struct {
	Document      string    `json:"document"`
	Version       string    `json:"version"`
	EffectiveDate time.Time `json:"effective_date"`
	AnnouncedAt   time.Time `json:"announced_at"`
	// Material dice se il cambiamento tocca materialmente i diritti, cioè se è
	// quello a cui i Termini §9 legano i trenta giorni di preavviso.
	Material bool `json:"material"`
}

// LegalConsentsResponse è lo stato completo dei consensi di un utente.
type LegalConsentsResponse struct {
	// Accepted è la storia, versioni superate comprese: sono la prova di cosa
	// vincolava allora.
	Accepted    []LegalConsentResponse     `json:"accepted"`
	Outstanding []LegalRequirementResponse `json:"outstanding"`
	Upcoming    []LegalChangeResponse      `json:"upcoming"`
}

func legalStateResponse(st legal.State) LegalConsentsResponse {
	// Le tre liste si costruiscono sempre non nulle: un array vuoto e un `null`
	// sono la stessa cosa per una persona e due cose diverse per un client, che
	// sul secondo deve scrivere un controllo in più.
	body := LegalConsentsResponse{
		Accepted:    make([]LegalConsentResponse, 0, len(st.Accepted)),
		Outstanding: make([]LegalRequirementResponse, 0, len(st.Outstanding)),
		Upcoming:    make([]LegalChangeResponse, 0, len(st.Upcoming)),
	}
	for _, c := range st.Accepted {
		body.Accepted = append(body.Accepted, LegalConsentResponse{
			Document:         string(c.Document),
			Version:          c.Version,
			Language:         string(c.Language),
			DocumentChecksum: c.Checksum,
			Source:           string(c.Source),
			AcceptedAt:       c.AcceptedAt,
		})
	}
	for _, r := range st.Outstanding {
		body.Outstanding = append(body.Outstanding, LegalRequirementResponse{
			Document:        string(r.Document),
			Version:         r.Version,
			Language:        string(r.Language),
			EffectiveDate:   r.Effective,
			AcceptedVersion: r.AcceptedVersion,
		})
	}
	for _, c := range st.Upcoming {
		body.Upcoming = append(body.Upcoming, LegalChangeResponse{
			Document:      string(c.Document),
			Version:       c.Version,
			EffectiveDate: c.Effective,
			AnnouncedAt:   c.Announced,
			Material:      c.Material,
		})
	}
	return body
}

// ------------------------------------------------------------------ handler

// state racconta cosa l'utente ha accettato, cosa gli manca e cosa sta per
// cambiare.
//
// Il parametro `language` decide in che lingua i testi da accettare verranno
// mostrati — non tocca i consensi già prestati, che portano la lingua che
// avevano allora, ed è tutto il punto di registrarla. Assente significa
// l'inglese, che è la lingua sorgente dei contenuti (SPEC §8-bis) e quella che
// l'utente vede comunque finché una traduzione non esiste.
func (a *legalAPI) state(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	language, ok := a.language(w, r, r.URL.Query().Get("language"))
	if !ok {
		return
	}

	st, err := a.svc.State(r.Context(), user.ID, language)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, legalStateResponse(st))
}

// accept registra i consensi e restituisce lo stato aggiornato.
//
// **Il client deve dichiarare quale versione ha mostrato.** Non è una
// formalità: fra il momento in cui la pagina si carica e quello in cui l'utente
// preme «accetto» può entrare in vigore una versione nuova, e registrare
// «l'ultima» produrrebbe una prova falsa proprio nel caso in cui conta — quello
// in cui il testo è cambiato. Dichiarandola, il disallineamento diventa un 409 e
// il client ripresenta il testo giusto.
//
// **200 e non 201**: la risorsa è lo stato dei consensi dell'utente, che esiste
// già e viene aggiornato. Riaccettare una versione già accettata non è un
// errore e non crea niente — e non sposta la data della prima accettazione, che
// è l'istante in cui l'utente si è vincolato davvero.
func (a *legalAPI) accept(w http.ResponseWriter, r *http.Request, user auth.User, _ auth.Session) {
	var body acceptConsentsRequest
	if !a.decode(w, r, &body) {
		return
	}
	language, ok := a.language(w, r, body.Language)
	if !ok {
		return
	}
	if len(body.Documents) == 0 {
		a.invalidField(w, r, "documents", "required", "Indica quali documenti stai accettando.")
		return
	}

	in := legal.AcceptInput{Language: language}
	for _, d := range body.Documents {
		document, err := legal.ParseDocument(d.Document)
		if err != nil {
			a.invalidField(w, r, "documents", "unknown_document",
				"Il documento «"+d.Document+"» non esiste.")
			return
		}
		if d.Version == "" {
			a.invalidField(w, r, "documents", "required",
				"Indica quale versione di «"+d.Document+"» è stata mostrata.")
			return
		}
		in.Accept = append(in.Accept, legal.Acceptance{Document: document, Version: d.Version})
	}

	st, err := a.svc.Accept(r.Context(), user.ID, in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, r, a.log, http.StatusOK, legalStateResponse(st))
}

// ------------------------------------------------------------------- errori

func (a *legalAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	var superata *legal.VersionNotInForceError
	if errors.As(err, &superata) {
		// Il messaggio dice quale versione vincola adesso: senza, il client
		// dovrebbe fare una seconda richiesta per costruire la schermata che sta
		// per ripresentare.
		writeErrorDetail(w, r, a.log, http.StatusConflict, ErrorDetail{
			Code: "legal_version_not_in_force",
			Message: "La versione accettata di «" + string(superata.Document) + "» non è quella in vigore: " +
				"hai accettato la " + superata.Offered + ", vige la " + superata.InForce + ". " +
				"Ricarica il testo e riprova.",
			Details: []FieldErrorBody{{
				Field:   "documents",
				Code:    "version_not_in_force",
				Message: string(superata.Document) + ": in vigore la " + superata.InForce + ".",
			}},
		})
		return
	}

	switch {
	case errors.Is(err, legal.ErrDuplicateDocument):
		a.invalidField(w, r, "documents", "duplicate_document",
			"Ogni documento va indicato una volta sola.")

	case errors.Is(err, legal.ErrNoDocuments):
		a.invalidField(w, r, "documents", "required",
			"Indica quali documenti stai accettando.")

	case errors.Is(err, legal.ErrUnknownDocument), errors.Is(err, legal.ErrUnknownLanguage):
		// Il livello HTTP li ha già filtrati: arrivarci significa che il dominio
		// e la validazione non sono più d'accordo, e la risposta giusta è la
		// stessa che avrebbe dato la validazione.
		a.invalidField(w, r, "documents", "invalid", "Documento o lingua non validi.")

	default:
		// Il resto — 401, 429 in testa — lo traduce writeAuthError, che è l'unico
		// posto in cui la corrispondenza fra errori e status esiste.
		a.guard.failAuth(w, r, err)
	}
}

// ------------------------------------------------------------------ supporto

// language riconosce la lingua dichiarata, con l'inglese come predefinito.
func (a *legalAPI) language(w http.ResponseWriter, r *http.Request, declared string) (legal.Language, bool) {
	if declared == "" {
		return legal.SourceLanguage, true
	}
	language, err := legal.ParseLanguage(declared)
	if err != nil {
		a.invalidField(w, r, "language", "unsupported",
			"La lingua «"+declared+"» non è fra quelle supportate.")
		return "", false
	}
	return language, true
}

func (a *legalAPI) invalidField(w http.ResponseWriter, r *http.Request, field, code, message string) {
	writeErrorDetail(w, r, a.log, http.StatusBadRequest, ErrorDetail{
		Code:    "validation_failed",
		Message: "La richiesta contiene campi non validi.",
		Details: []FieldErrorBody{{Field: field, Code: code, Message: message}},
	})
}

func (a *legalAPI) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := decodeJSON(w, r, dst)
	if err == nil {
		return true
	}
	status, code := http.StatusBadRequest, "invalid_request"
	if errors.Is(err, errBodyTooLarge) {
		status, code = http.StatusRequestEntityTooLarge, "body_too_large"
	}
	// Qui il messaggio del decodificatore può tornare al client: in questo corpo
	// non c'è niente di segreto — nomi di documenti e numeri di versione — e
	// sapere *dove* il JSON è rotto è ciò che serve a chi sta scrivendo il client.
	writeError(w, r, a.log, status, code, err.Error())
	return false
}
