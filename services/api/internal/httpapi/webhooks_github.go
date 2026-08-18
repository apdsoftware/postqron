package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/githubhook"
)

// Il contratto della rotta del webhook GitHub (R11), in breve.
//
//	POST /webhooks/github    consegna di un evento    200 / 202
//
// **È l'unica rotta pubblica che scrive**, e non sta dietro il guard di
// identity.go: chi la chiama non ha né sessione né chiave API. A dimostrare la
// provenienza è la firma HMAC del corpo, che il servizio verifica prima di ogni
// altra cosa — è quella, e non un'identità nostra, la credenziale della
// richiesta.
//
// Esiti, tutti sotto forma di [GitHubWebhookResponse]:
//
//	202 accepted      push verificata e presa in carico
//	200 duplicate     consegna già ricevuta: nessun secondo effetto
//	200 ignored       verificata, niente da fare (evento non `push`)
//	200 pong          risposta al `ping` con cui GitHub prova il webhook
//
// Errori:
//
//	400 invalid_request      firmata ma malformata: testata o payload
//	401 invalid_signature    firma assente, sbagliata, o corpo alterato
//	413 payload_too_large    corpo oltre maxWebhookBody
//	500 internal_error       lavorazione fallita: GitHub ripeterà
//
// La distinzione fra 200 e 202 non è cosmetica: 202 significa che l'evento è
// stato passato al consumatore, 200 che non c'era niente da fare. Sono le due
// cose che si vogliono distinguere guardando il registro delle consegne
// dell'App dopo aver ripetuto una consegna a mano.

// maxWebhookBody è il tetto al corpo di una consegna.
//
// Il payload `push` di GitHub contiene al massimo venti commit, quindi in
// pratica sta in poche decine di kilobyte; due mebibyte lasciano un margine
// ampio e restano un tetto vero. Serve perché questa rotta è pubblica e il corpo
// va letto **per intero** prima di poter dire se la richiesta è legittima: senza
// un limite, chiunque potrebbe farci allocare memoria a piacere semplicemente
// mandando un corpo lungo e una firma qualsiasi.
const maxWebhookBody = 2 << 20

// webhookProcessingTimeout è il tempo massimo concesso alla lavorazione di una
// consegna. GitHub chiude la connessione dopo circa dieci secondi: superarli
// non produce una risposta che qualcuno legge, produce solo lavoro sospeso.
const webhookProcessingTimeout = 8 * time.Second

// githubWebhookAPI raccoglie la rotta del webhook GitHub.
type githubWebhookAPI struct {
	svc *githubhook.Service
	log *slog.Logger
}

func newGitHubWebhookAPI(logger *slog.Logger, svc *githubhook.Service) *githubWebhookAPI {
	return &githubWebhookAPI{svc: svc, log: logger}
}

func (a *githubWebhookAPI) routes(mux router) {
	mux.HandleFunc("POST /webhooks/github", a.receive)
}

// GitHubWebhookResponse è la risposta a una consegna verificata.
//
// Ripete l'identificativo della consegna e l'evento perché è ciò che rende
// leggibile il registro dell'App: accanto a ogni consegna GitHub mostra la
// risposta, e ritrovarci dentro il proprio `X-GitHub-Delivery` con l'esito
// accanto è ciò che permette di collaudare senza accedere ai nostri log.
//
// Non contiene niente che venga dal corpo della richiesta.
type GitHubWebhookResponse struct {
	Delivery string `json:"delivery"`
	Event    string `json:"event"`
	Outcome  string `json:"outcome"`
}

// receive riceve una consegna del webhook.
func (a *githubWebhookAPI) receive(w http.ResponseWriter, r *http.Request) {
	// Il corpo si legge grezzo, per intero e senza toccarlo: è sui byte esatti
	// che GitHub ha firmato che l'HMAC va calcolato. Nessun decodificatore JSON
	// prima di qui, e nessun `ParseForm` — che consumerebbe il corpo lasciando al
	// controllo della firma dei byte che non sono più quelli arrivati.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			// Il rifiuto precede la verifica della firma, e non è un'eccezione
			// alla regola: la richiesta non produce nessun effetto e il log non
			// dice cosa conteneva.
			a.log.WarnContext(r.Context(), "consegna GitHub rifiutata: corpo oltre il limite",
				slog.Int64("limit", maxWebhookBody))
			writeError(w, r, a.log, http.StatusRequestEntityTooLarge, "payload_too_large",
				"Il corpo della richiesta supera il limite consentito.")
			return
		}
		writeError(w, r, a.log, http.StatusBadRequest, "invalid_request",
			"Corpo della richiesta illeggibile.")
		return
	}

	// La lavorazione sopravvive alla disconnessione del chiamante: GitHub chiude
	// presto, e la registrazione della consegna è ciò che impedisce alla
	// ripetizione successiva di produrre un secondo effetto. Perderla perché il
	// mittente ha riagganciato significherebbe rilavorare quell'evento.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), webhookProcessingTimeout)
	defer cancel()

	result, err := a.svc.Receive(ctx, githubhook.Request{
		Signature: r.Header.Get(githubhook.HeaderSignature),
		Event:     r.Header.Get(githubhook.HeaderEvent),
		Delivery:  r.Header.Get(githubhook.HeaderDelivery),
		Body:      body,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	status := http.StatusOK
	if result.Outcome == githubhook.OutcomeAccepted {
		status = http.StatusAccepted
	}
	writeJSON(w, r, a.log, status, GitHubWebhookResponse{
		Delivery: result.Delivery,
		Event:    result.Event,
		Outcome:  string(result.Outcome),
	})
}

// fail traduce gli errori del servizio in status.
func (a *githubWebhookAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, githubhook.ErrInvalidSignature):
		// Un solo codice per firma assente, sbagliata o corpo alterato: la
		// risposta non deve dire a chi prova quale dei tre casi ha prodotto. Il
		// servizio ha già registrato il rifiuto, senza contenuto.
		writeError(w, r, a.log, http.StatusUnauthorized, "invalid_signature",
			"Firma della richiesta non valida.")
	case errors.Is(err, githubhook.ErrInvalidRequest):
		// Qui il messaggio può essere esplicito: la firma ha già dimostrato che
		// dall'altra parte c'è GitHub.
		writeError(w, r, a.log, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		// Il 500 è deliberato: è la risposta che fa ripetere GitHub, ed è ciò che
		// vogliamo quando la lavorazione non è riuscita. Il messaggio al chiamante
		// resta generico, il dettaglio sta nel log del servizio.
		a.log.ErrorContext(r.Context(), "consegna GitHub non lavorata", slog.Any("error", err))
		writeError(w, r, a.log, http.StatusInternalServerError, "internal_error",
			"Consegna non lavorata. Riprovare.")
	}
}
