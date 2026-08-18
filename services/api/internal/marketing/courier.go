package marketing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
	"github.com/apdsoftware/postqron/services/api/internal/mailronix"
)

// Renderer è la parte di [emailrender.Renderer] che serve al corriere.
type Renderer interface {
	Render(event emailrender.Event, language string, data any) (emailrender.Message, error)
}

// Sender è la parte del client Mailronix che serve al corriere. Averla come
// interfaccia è ciò che permette di provare tutto questo package senza una sola
// chiamata di rete.
type Sender interface {
	Send(ctx context.Context, email mailronix.Email) (mailronix.Receipt, error)
}

// Content è il testo di una comunicazione in **una** lingua.
type Content struct {
	Headline   string
	Paragraphs []string

	CallToActionLabel string
	CallToActionURL   string
}

// Update è una comunicazione di prodotto, nelle lingue in cui è stata scritta.
//
// Il testo arriva da chi prepara l'invio e non dai file dei testi, perché **è**
// il messaggio e cambia ogni volta: vedi [emailrender.ProductUpdateData]. Le
// lingue seguono la regola di tutto il resto (R33, SPEC §8-bis): si scrive in
// inglese e si traduce, la lingua del profilo decide, e ciò che manca ricade
// sull'inglese.
type Update struct {
	// Content è il testo per lingua, con le chiavi di
	// [emailrender.Languages]. L'inglese è obbligatorio: è la sorgente ed è il
	// ripiego, e senza non ci sarebbe niente da mandare a chi ha scelto una
	// lingua non tradotta.
	Content map[string]Content
}

// Validate rifiuta una comunicazione che non si potrebbe mandare a tutti.
func (u Update) Validate() error {
	if len(u.Content) == 0 {
		return errors.New("marketing: comunicazione senza testo")
	}
	if _, ok := u.Content[emailrender.DefaultLanguage]; !ok {
		return fmt.Errorf(
			"marketing: manca il testo in %q, che è la lingua sorgente e il ripiego: "+
				"senza, chi ha scelto una lingua non tradotta non riceverebbe niente",
			emailrender.DefaultLanguage)
	}
	for language := range u.Content {
		if emailrender.NormalizeLanguage(language) != language {
			return fmt.Errorf("marketing: %q non è una delle lingue del prodotto", language)
		}
	}
	return nil
}

// forLanguage sceglie il testo, ricadendo sull'inglese.
//
// La ricaduta è **per lingua** e non per chiave, al contrario dei testi delle
// email: un capoverso tradotto a metà non è un capoverso, è una frase che si
// interrompe. Ciò che ha senso mescolare sono le etichette, non la prosa.
func (u Update) forLanguage(language string) Content {
	if content, ok := u.Content[language]; ok {
		return content
	}
	return u.Content[emailrender.DefaultLanguage]
}

// CourierOptions configura il [Courier]. Sono tutti obbligatori tranne il
// logger.
type CourierOptions struct {
	Service  *Service
	Renderer Renderer
	Sender   Sender
	Logger   *slog.Logger
}

// Courier manda una comunicazione di prodotto a chi l'ha chiesta.
//
// È il gemello di [notify.Courier], e le due differenze sono esattamente le due
// regole di §2.8: qui si verifica un consenso prima di ogni invio, e ogni
// messaggio porta un link di disiscrizione. Non c'è una coda, e non è una
// mancanza: una comunicazione di prodotto non nasce da un evento che va
// raccontato subito, nasce da qualcuno che decide di scriverla — il ritentare e
// il raggruppare difendono da una tempesta di avvisi che qui non può accadere.
//
// # Non è agganciato a cmd/api, ed è una decisione
//
// Lo chiama la suite di test e nient'altro. **Non è un pezzo lasciato a metà:
// è il canale costruito e tenuto spento di proposito**, e chi arriva qui
// pensando di finire un lavoro incompiuto sta per disfare una scelta.
//
// Il motivo è in [mailronix]. `POST /email/send` assegna la categoria
// `transaction_receipt` **lato server, senza modo di sceglierla dalla
// richiesta**: le comunicazioni di prodotto partirebbero sulla stessa categoria
// con cui mandiamo gli avvisi di job fallito, le variazioni di piano e gli
// eventi di sicurezza.
//
// # Che cosa succederebbe ad agganciarlo prima
//
// Un solo destinatario che segnala una promozione come spam — legittimamente,
// perché una promozione è esattamente ciò che quel pulsante serve a segnalare —
// porta il proprio indirizzo nella suppression list di Mailronix. Da quel
// momento **non riceve più nemmeno le email transazionali**: non l'avviso che
// il suo job è rotto da tre giorni, non quello che qualcuno gli ha creato una
// chiave API. Il danno non è la promozione persa, è il canale del servizio.
//
// E non ce ne accorgeremmo. Per R20.1 la risposta di Mailronix è `202`
// identica verso un destinatario recapitabile e verso uno soppresso: non
// esiste, in questo codice, un modo di distinguere «arrivata» da «scartata in
// silenzio». È lo stesso rischio che la doc di [notify.Policy] descrive per la
// tempesta di avvisi — «la perdita silenziosa di tutto il canale» — con la
// differenza che lì difendiamo con il raggruppamento, e qui non c'è niente da
// raggruppare: basta un reclamo.
//
// # Che cosa deve cambiare: niente, e la risposta è arrivata
//
// Avevamo chiesto a Mailronix un campo `category` sulla richiesta e una
// suppression per categoria (docs/reference/mailronix-change-request.md). La
// risposta del 2026-08-18 è **no su entrambi**, e il motivo rende obsoleta la
// domanda invece di rimandarla.
//
// **Mailronix consegna via AWS SES, con la suppression list a livello di
// account AWS, `BOUNCE` e `COMPLAINT` fra le ragioni.** Quella lista sta
// **sotto** la loro API: vale per tutti i clienti e per tutte le categorie,
// presenti e future. Il campo che avevamo chiesto non avrebbe protetto niente —
// dopo un reclamo il transazionale sarebbe stato fermato comunque, un livello
// più in basso. Sarebbe stato esattamente il campo che illude, cioè la cosa che
// avevamo chiesto di evitarci.
//
// Loro possono escludere il transazionale da quella lista, e hanno deciso di
// non farlo. La ragione che conta non è la loro policy: **non funzionerebbe
// comunque.** Chi ha premuto «segnala spam» ha addestrato il proprio provider,
// e Gmail, Outlook e Apple scarteranno il messaggio successivo qualunque cosa
// faccia SES. Ne uscirebbe un `202` e un evento di consegna verso un utente che
// non ha ricevuto niente — di nuovo R20.1, un livello più in là.
//
// Due conseguenze che questo codice non può cambiare e con cui va convissuto:
//
//  1. **un reclamo costa l'indirizzo per tutte le categorie**, in modo
//     silenzioso e non recuperabile da noi;
//  2. **la soppressione attraversa i confini fra clienti**: un reclamo generato
//     da un altro cliente della piattaforma sullo stesso indirizzo può bloccarci
//     quel destinatario senza che nulla ce lo segnali.
//
// # Perché questo tipo resta senza chiamante, adesso definitivamente
//
// Non «finché Mailronix non cambia». **Il marketing non deve passare da qui**, ed
// è anche il consiglio esplicito di chi opera la piattaforma: ogni comunicazione
// promozionale è un'occasione di reclamo, e un reclamo costa il canale
// transazionale a livello di account, non di categoria.
//
// Il giorno in cui il marketing partirà davvero, partirà da un'altra parte. Il
// consenso e la disiscrizione restano nostri — sono legati al rapporto con
// l'utente, non al vettore — ed è per questo che tutto il resto di questo
// package è vivo e collegato.
//
// # Che cosa funziona nel frattempo
//
// Tutto ciò che la privacy policy promette **all'utente**: prestare il
// consenso, ritirarlo, disiscriversi con il link senza accedere, e la traccia
// che dimostra quando l'ha fatto. La macchina è pronta e non manda — che è la
// direzione sicura in cui questa mancanza poteva cadere.
//
// Chi costruirà la superficie d'invio innesta qui e non si scrive un percorso
// suo: è questo il punto in cui «senza consenso non parte niente» è verificato,
// e un secondo percorso significherebbe due verifiche libere di divergere.
type Courier struct {
	svc      *Service
	renderer Renderer
	sender   Sender
	log      *slog.Logger
}

// NewCourier costruisce il corriere.
func NewCourier(opts CourierOptions) (*Courier, error) {
	switch {
	case opts.Service == nil:
		return nil, errors.New("marketing: NewCourier richiede un Service")
	case opts.Renderer == nil:
		return nil, errors.New("marketing: NewCourier richiede un Renderer")
	case opts.Sender == nil:
		return nil, errors.New("marketing: NewCourier richiede un Sender")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Courier{svc: opts.Service, renderer: opts.Renderer, sender: opts.Sender, log: logger}, nil
}

// Result è l'esito di un invio.
type Result string

const (
	// Sent: il messaggio è stato consegnato a Mailronix. **Non significa
	// recapitato** (R20.1).
	Sent Result = "sent"
	// NoConsent: l'utente non ha un consenso in vigore. Non è un errore: è il
	// funzionamento normale per la maggioranza degli account.
	NoConsent Result = "no_consent"
	// NoRecipient: l'account non esiste o è chiuso.
	NoRecipient Result = "no_recipient"
)

// Send manda una comunicazione a un destinatario, se ha acconsentito.
//
// L'ordine delle prime due istruzioni è la promessa di §2.8: si legge il
// destinatario **e il suo consenso** con una sola query, e senza consenso non si
// compila nemmeno il messaggio. Non c'è un percorso alternativo, un flag di
// forzatura o un elenco di eccezioni — se ne servisse uno, sarebbe il posto in
// cui la promessa si romperebbe.
func (c *Courier) Send(ctx context.Context, userID string, update Update) (Result, error) {
	if strings.TrimSpace(userID) == "" {
		return "", errors.New("marketing: invio senza destinatario")
	}
	if err := update.Validate(); err != nil {
		return "", err
	}

	recipient, err := c.svc.store.Recipient(ctx, userID)
	switch {
	case errors.Is(err, ErrNoRecipient):
		return NoRecipient, nil
	case err != nil:
		return "", fmt.Errorf("marketing: lettura del destinatario: %w", err)
	}

	if !recipient.Consented {
		c.log.DebugContext(ctx, "nessuna email di marketing: consenso assente o ritirato",
			slog.String("user_id", userID))
		return NoConsent, nil
	}

	language := emailrender.NormalizeLanguage(recipient.Language)
	content := update.forLanguage(language)

	message, err := c.render(language, content, c.svc.UnsubscribeURL(userID))
	if err != nil {
		return "", err
	}

	receipt, err := c.sender.Send(ctx, mailronix.Email{
		To:      recipient.Email,
		Subject: message.Subject,
		HTML:    message.HTML,
		Text:    message.Text,
	})
	if err != nil {
		return "", fmt.Errorf("marketing: recapito a %s: %w", userID, err)
	}

	// Si registra l'identificativo e nulla di più: né l'indirizzo, che è un dato
	// personale, né una parola sul recapito, che questa risposta non dice
	// (R20.1).
	c.log.InfoContext(ctx, "comunicazione di prodotto accodata presso Mailronix",
		slog.String("user_id", userID),
		slog.String("language", message.Language),
		slog.String("email_log_id", receipt.EmailLogID))
	return Sent, nil
}

// render compila il messaggio, dopo essersi accertato di star compilando
// marketing.
//
// Il controllo è il gemello di quello di internal/notify, e le due direzioni
// dell'errore sono opposte: là non deve passare marketing, qui non deve passare
// altro. Insieme rendono impossibile che un template cambi famiglia senza che
// uno dei due se ne accorga.
func (c *Courier) render(language string, content Content, unsubscribeURL string) (emailrender.Message, error) {
	const event = emailrender.EventProductUpdate

	kind, declared := emailrender.KindOf(event)
	switch {
	case !declared:
		return emailrender.Message{}, fmt.Errorf("marketing: %q non dichiara la propria natura", event)
	case kind != emailrender.KindMarketing:
		return emailrender.Message{}, fmt.Errorf(
			"marketing: %q è dichiarato %q, e questo corriere manda solo marketing: "+
				"un'email transazionale mandata da qui verificherebbe un consenso che §2.7 non richiede, "+
				"e porterebbe un link di disiscrizione che §2.7 vieta", event, kind)
	}

	message, err := c.renderer.Render(event, language, emailrender.ProductUpdateData{
		Headline:          content.Headline,
		Paragraphs:        content.Paragraphs,
		CallToActionLabel: content.CallToActionLabel,
		CallToActionURL:   content.CallToActionURL,
		UnsubscribeURL:    unsubscribeURL,
	})
	if err != nil {
		return emailrender.Message{}, fmt.Errorf("marketing: compilazione della comunicazione: %w", err)
	}
	return message, nil
}
