// Il confine fra la coda transazionale e il marketing, verificato.
//
// La doc di questo package lo dichiara da sempre — «le comunicazioni di
// marketing […] non passano da questo package» — e fino alla issue #476 era una
// frase. Qui diventa una proprietà: ogni evento che la coda sa recapitare porta
// a un template dichiarato transazionale, e un template di marketing viene
// rifiutato anche se qualcuno lo mettesse nella tabella di traduzione.
//
// Il test sta **dentro** il package perché ciò che va guardato è la tabella di
// traduzione, che è un dettaglio interno: dall'esterno si vedrebbe solo l'email
// che esce, cioè l'effetto e non la regola.
package notify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

// Ogni evento della coda porta a un template transazionale.
//
// È il controllo che vale per gli eventi di domani: un caso aggiunto a
// [transactionalTemplate] che puntasse a un template di marketing renderebbe
// questo test rosso il giorno stesso, invece di scoprirlo da un'email partita
// senza consenso.
func TestOgniEventoDellaCodaPortaAUnTemplateTransazionale(t *testing.T) {
	for _, event := range Events() {
		t.Run(string(event), func(t *testing.T) {
			template, _, err := transactionalTemplate(Pending{Event: event})
			if err != nil {
				t.Fatalf("l'evento non ha un template: %v", err)
			}
			if err := assertTransactional(template); err != nil {
				t.Errorf("l'evento %q della coda porta al template %q: %v", event, template, err)
			}
		})
	}
}

// Il confine rifiuta un template di marketing, e dice perché.
//
// Il messaggio conta quanto il rifiuto: chi lo legge in un log deve capire che
// non è un guasto ma un confine, e dove sta scritto.
func TestIlConfineRifiutaUnTemplateDiMarketing(t *testing.T) {
	err := assertTransactional(emailrender.EventProductUpdate)
	if err == nil {
		t.Fatal("la coda transazionale ha accettato un template di marketing")
	}
	if !errors.Is(err, errMarketingInCodaTransazionale) {
		t.Errorf("il rifiuto non è quello del confine: %v", err)
	}
	if !strings.Contains(err.Error(), "§2.8") {
		t.Errorf("il rifiuto non dice dove sta la regola che lo impone: %v", err)
	}
}

// Un template senza natura dichiarata viene rifiutato come il marketing.
//
// Le due direzioni dell'errore non si equivalgono, e la scelta è dichiarata in
// [assertTransactional]: nel dubbio non si manda.
func TestUnTemplateSenzaNaturaVieneRifiutato(t *testing.T) {
	if err := assertTransactional(emailrender.Event("job_recovered")); err == nil {
		t.Error("un template senza natura dichiarata è passato come transazionale")
	}
}

// Ciò che la coda recapita davvero non porta un link di disiscrizione.
//
// Il controllo precedente guarda la regola, questo guarda il risultato: sono le
// due metà della stessa promessa di §2.7, e verificare solo la prima
// lascerebbe scoperto il caso in cui il layout cambia e la tabella no.
func TestNessunaEmailDellaCodaOffreLaDisiscrizione(t *testing.T) {
	dir, err := emailrender.FindDir(".")
	if err != nil {
		t.Fatalf("FindDir: %v", err)
	}
	renderer, err := emailrender.NewFromDir(dir, emailrender.Site{
		ProductName:   "Postqron",
		PublicBaseURL: "https://postqron.test",
		AppBaseURL:    "https://app.postqron.test",
		SupportEmail:  "support@postqron.test",
	})
	if err != nil {
		t.Fatalf("NewFromDir: %v", err)
	}

	courier, err := NewCourier(CourierOptions{
		Queue:    NewMemoryQueue(),
		Renderer: renderer,
		Sender:   &RecordingSender{},
	})
	if err != nil {
		t.Fatalf("NewCourier: %v", err)
	}

	for _, event := range Events() {
		t.Run(string(event), func(t *testing.T) {
			message, err := courier.render(pendingCampione(event))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for corpo, testo := range map[string]string{"HTML": message.HTML, "testo": message.Text} {
				if strings.Contains(strings.ToLower(testo), "unsubscribe") {
					t.Errorf("il corpo %s di %s nomina la disiscrizione: §2.7 dice che da un'email "+
						"transazionale non ci si disiscrive, e offrirla significherebbe togliere "+
						"all'utente gli avvisi del servizio", corpo, event)
				}
			}
		})
	}
}

// pendingCampione è una notifica valida per ciascun evento, con i soli campi che
// il rispettivo template pretende.
func pendingCampione(event Event) Pending {
	p := Pending{
		Event:     event,
		Recipient: Recipient{UserID: "u-1", Email: "sam@example.test", Name: "Sam", Language: "en"},
	}
	switch event {
	case EventJobFailed:
		p.JobID = "0f2d1c9e-3a44-4b7f-9c11-5d6e7f8a9b0c"
		p.Environment = string(emailrender.EnvironmentProduction)
		p.Payload = Payload{
			JobName:       "nightly-invoices",
			Failures:      3,
			LastAttemptAt: campioneIstante,
			FailureKind:   FailureHTTPStatus,
			HTTPStatus:    502,
		}
	case EventPlanChanged:
		p.Payload = Payload{PreviousPlan: "Free", NewPlan: "Pro", EffectiveAt: campioneIstante}
	case EventSecurity:
		p.Payload = Payload{SecurityKind: SecurityAPIKeyRevoked, OccurredAt: campioneIstante}
	}
	return p
}

// campioneIstante è la data dei campioni: fissa, così i corpi compilati non
// cambiano da un'esecuzione all'altra.
var campioneIstante = time.Date(2026, 8, 17, 6, 12, 0, 0, time.UTC)
