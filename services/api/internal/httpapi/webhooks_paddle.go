package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// Il contratto della rotta del webhook Paddle (R16), in breve.
//
//	POST /webhooks/paddle    consegna di un evento    200 / 202
//
// È la **seconda** rotta pubblica che scrive, dopo quella di GitHub, e non sta
// dietro il guard di identity.go: chi la chiama non ha né sessione né chiave
// API. A dimostrare la provenienza è la firma del corpo, che il servizio
// verifica prima di ogni altra cosa.
//
// La differenza con il webhook di GitHub non è di forma ma di posta in gioco: un
// evento accettato a torto qui **regala un piano a pagamento**. Per questo il
// segreto è obbligatorio e senza di esso la rotta non viene registrata affatto —
// vedi [Deps.PaddleWebhook].
//
// Esiti, tutti sotto forma di [PaddleWebhookResponse]:
//
//	202 applied       sottoscrizione verificata e applicata agli entitlement
//	200 duplicate     evento già ricevuto: nessun secondo effetto
//	200 stale         evento più vecchio di quanto già applicato: nessun effetto
//	200 ignored       verificato, niente da fare (evento non di sottoscrizione)
//
// Errori:
//
//	400 invalid_request      firmato ma malformato: payload illeggibile
//	401 invalid_signature    firma assente, sbagliata, corpo alterato o fuori tempo
//	413 payload_too_large    corpo oltre maxPaddleWebhookBody
//	500 internal_error       lavorazione fallita: Paddle ripeterà
//
// `duplicate` e `stale` sono due esiti distinti e la distinzione non è
// cosmetica: il primo dice che la stessa consegna è arrivata due volte, il
// secondo che una consegna **diversa e più vecchia** è arrivata dopo una più
// recente. Sono i due modi in cui un webhook di fatturazione si rompe, e
// guardando il cruscotto di Paddle si vuole sapere quale dei due si è visto.

// maxPaddleWebhookBody è il tetto al corpo di una consegna.
//
// I payload di Paddle sono oggetti di poche decine di campi; due mebibyte
// lasciano un margine ampio e restano un tetto vero. Serve perché questa rotta è
// pubblica e il corpo va letto **per intero** prima di poter dire se la
// richiesta è legittima: senza un limite, chiunque potrebbe farci allocare
// memoria a piacere mandando un corpo lungo e una firma qualsiasi.
const maxPaddleWebhookBody = 2 << 20

// paddleProcessingTimeout è il tempo massimo concesso alla lavorazione. Paddle
// chiude la connessione dopo pochi secondi: superarli non produce una risposta
// che qualcuno legge, produce solo lavoro sospeso.
const paddleProcessingTimeout = 8 * time.Second

type paddleWebhookAPI struct {
	svc *paddle.Service
	log *slog.Logger
}

func newPaddleWebhookAPI(logger *slog.Logger, svc *paddle.Service) *paddleWebhookAPI {
	return &paddleWebhookAPI{svc: svc, log: logger}
}

func (a *paddleWebhookAPI) routes(mux router) {
	mux.HandleFunc("POST /webhooks/paddle", a.receive)
}

// PaddleWebhookResponse è la risposta a una consegna verificata.
//
// Ripete l'identificativo dell'evento e il tipo perché è ciò che rende
// leggibile il registro delle notifiche di Paddle: accanto a ogni consegna il
// cruscotto mostra la risposta, e ritrovarci dentro il proprio `event_id` con
// l'esito accanto è ciò che permette di collaudare senza accedere ai nostri log.
//
// Non contiene niente che venga dal corpo della richiesta oltre a quei due
// valori, e in particolare **nessun dato di fatturazione**.
type PaddleWebhookResponse struct {
	EventID string `json:"event_id"`
	Type    string `json:"type"`
	Outcome string `json:"outcome"`
}

// receive riceve una consegna del webhook.
func (a *paddleWebhookAPI) receive(w http.ResponseWriter, r *http.Request) {
	// Il corpo si legge grezzo, per intero e senza toccarlo: è sui byte esatti
	// che Paddle ha firmato che l'HMAC va calcolato. Nessun decodificatore JSON
	// prima di qui, e nessun `ParseForm` — che consumerebbe il corpo lasciando al
	// controllo della firma dei byte che non sono più quelli arrivati.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPaddleWebhookBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			// Il rifiuto precede la verifica della firma, e non è un'eccezione alla
			// regola: la richiesta non produce nessun effetto e il log non dice cosa
			// conteneva.
			a.log.WarnContext(r.Context(), "consegna Paddle rifiutata: corpo oltre il limite",
				slog.Int64("limit", maxPaddleWebhookBody))
			writeError(w, r, a.log, http.StatusRequestEntityTooLarge, "payload_too_large",
				"Il corpo della richiesta supera il limite consentito.")
			return
		}
		writeError(w, r, a.log, http.StatusBadRequest, "invalid_request",
			"Corpo della richiesta illeggibile.")
		return
	}

	// La lavorazione sopravvive alla disconnessione del chiamante: Paddle chiude
	// presto, e la registrazione dell'evento è ciò che impedisce alla ripetizione
	// successiva di produrre un secondo effetto. Perderla perché il mittente ha
	// riagganciato significherebbe rilavorare quell'evento — cioè, nel caso
	// peggiore, applicare due volte un cambio di piano.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), paddleProcessingTimeout)
	defer cancel()

	result, err := a.svc.Receive(ctx, paddle.Request{
		Signature: r.Header.Get(paddle.HeaderSignature),
		Body:      body,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}

	status := http.StatusOK
	if result.Outcome == paddle.OutcomeApplied {
		status = http.StatusAccepted
	}
	writeJSON(w, r, a.log, status, PaddleWebhookResponse{
		EventID: result.EventID,
		Type:    result.Type,
		Outcome: string(result.Outcome),
	})
}

// fail traduce gli errori del servizio in status.
func (a *paddleWebhookAPI) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, paddle.ErrInvalidSignature):
		// Un solo codice per firma assente, sbagliata, corpo alterato o timestamp
		// fuori tolleranza: la risposta non deve dire a chi prova quale dei casi ha
		// prodotto. Il servizio ha già registrato il rifiuto, senza contenuto.
		writeError(w, r, a.log, http.StatusUnauthorized, "invalid_signature",
			"Firma della richiesta non valida.")
	case errors.Is(err, paddle.ErrInvalidRequest):
		// Qui il messaggio può essere esplicito: la firma ha già dimostrato che
		// dall'altra parte c'è Paddle.
		writeError(w, r, a.log, http.StatusBadRequest, "invalid_request", err.Error())
	default:
		// Il 500 è deliberato: è la risposta che fa ripetere Paddle, ed è ciò che
		// vogliamo quando la lavorazione non è riuscita — un utente che ha pagato e
		// non ha ricevuto il piano deve ottenere una seconda occasione, non un
		// silenzio. Il messaggio al chiamante resta generico, il dettaglio sta nel
		// log del servizio.
		a.log.ErrorContext(r.Context(), "consegna Paddle non lavorata", slog.Any("error", err))
		writeError(w, r, a.log, http.StatusInternalServerError, "internal_error",
			"Consegna non lavorata. Riprovare.")
	}
}
