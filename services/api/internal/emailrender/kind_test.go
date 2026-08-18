// I controlli che tengono separate le due famiglie di email.
//
// La privacy policy le distingue in ogni aspetto — §2.7 dice che dalle
// transazionali non ci si disiscrive, §2.8 che ogni messaggio di marketing porta
// un link per farlo — e la distinzione non regge su una convenzione. Questi test
// la rendono una proprietà: un evento nuovo non passa di qui senza aver
// risposto alla domanda, e un piè di pagina scambiato fa rosso.
package emailrender_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/emailrender"
)

// Ogni evento dichiara se è marketing. Non è una formalità: è la domanda che
// decide quale piè di pagina viene scritto, e non ha un valore predefinito da
// cui ricadere.
func TestOgniEventoDichiaraLaPropriaNatura(t *testing.T) {
	for _, event := range emailrender.Events() {
		kind, declared := emailrender.KindOf(event)
		if !declared {
			t.Errorf("%s non dichiara la propria natura: aggiungilo a `kinds`. "+
				"Senza, non si sa se il messaggio debba portare un link di disiscrizione "+
				"(privacy policy §2.8) o non debba portarlo (§2.7)", event)
			continue
		}
		if kind != emailrender.KindTransactional && kind != emailrender.KindMarketing {
			t.Errorf("%s dichiara la natura %q, che non è né %q né %q",
				event, kind, emailrender.KindTransactional, emailrender.KindMarketing)
		}
	}
}

// Un evento che non ha dichiarato la propria natura non si compila.
//
// È il meccanismo che rende obbligatoria la domanda: chi aggiunge un template
// non trova un comportamento predefinito ragionevole su cui appoggiarsi, trova
// un errore che nomina la scelta da fare.
func TestUnEventoSenzaNaturaNonSiCompila(t *testing.T) {
	r := newRenderer(t)

	_, err := r.Render(emailrender.Event("job_recovered"), "en", emailrender.WelcomeData{})
	if err == nil {
		t.Fatal("un evento senza natura dichiarata è stato compilato")
	}
	if !strings.Contains(err.Error(), "natura dichiarata") {
		t.Errorf("l'errore non dice qual è la scelta mancante: %v", err)
	}
}

// Il link di disiscrizione compare in tutte e sole le email di marketing.
//
// I due lati contano allo stesso modo, e sono due difetti diversi: un link
// mancante su una promozione è un illecito, un link presente su un avviso di
// sicurezza è un link che l'utente userebbe — smettendo di ricevere gli avvisi
// di un servizio che paga.
func TestIlLinkDiDisiscrizioneStaSoloNelMarketing(t *testing.T) {
	r := newRenderer(t)
	data := sampleData()

	for _, event := range emailrender.Events() {
		t.Run(string(event), func(t *testing.T) {
			message, err := r.Render(event, "en", data[event])
			if err != nil {
				t.Fatalf("Render: %v", err)
			}

			marketing, declared := emailrender.IsMarketing(event)
			if !declared {
				t.Fatalf("%s non dichiara la propria natura", event)
			}

			for corpo, testo := range map[string]string{"HTML": message.HTML, "testo": message.Text} {
				contiene := strings.Contains(testo, sampleUnsubscribeURL)
				switch {
				case marketing && !contiene:
					t.Errorf("il corpo %s di un'email di marketing non porta il link di disiscrizione: "+
						"§2.8 dice «every marketing message carries an unsubscribe link»", corpo)
				case !marketing && contiene:
					t.Errorf("il corpo %s di un'email transazionale porta un link di disiscrizione: "+
						"§2.7 dice che da queste email non ci si disiscrive", corpo)
				}

				// Il controllo sul solo URL non basterebbe: un piè di pagina che
				// invitasse a disiscriversi *senza* un link sarebbe comunque una
				// promessa che l'email transazionale non può mantenere.
				if !marketing && strings.Contains(strings.ToLower(testo), "unsubscribe") {
					t.Errorf("il corpo %s di un'email transazionale nomina la disiscrizione: "+
						"§2.7 dice che non si può, e nominarla è offrirla", corpo)
				}
			}
		})
	}
}

// Un messaggio di marketing senza link di disiscrizione non esiste.
//
// Il rifiuto è nella convalida del contesto e non in una revisione del testo:
// §2.8 dice *every*, e l'unico modo di renderlo vero senza contare
// sull'attenzione di chi prepara il prossimo invio è che quel messaggio non si
// possa compilare.
func TestIlMarketingSenzaDisiscrizioneNonSiCompila(t *testing.T) {
	r := newRenderer(t)

	casi := map[string]emailrender.ProductUpdateData{
		"nessun indirizzo": {
			Headline:   "Novità",
			Paragraphs: []string{"Testo."},
		},
		"indirizzo relativo": {
			Headline:       "Novità",
			Paragraphs:     []string{"Testo."},
			UnsubscribeURL: "/marketing/unsubscribe?token=x",
		},
		"indirizzo in chiaro": {
			Headline:       "Novità",
			Paragraphs:     []string{"Testo."},
			UnsubscribeURL: "http://api.postqron.com/marketing/unsubscribe?token=x",
		},
	}

	for nome, data := range casi {
		t.Run(nome, func(t *testing.T) {
			if _, err := r.Render(emailrender.EventProductUpdate, "en", data); err == nil {
				t.Error("il messaggio è stato compilato senza un link di disiscrizione utilizzabile")
			}
		})
	}
}

// Nessun contesto transazionale ha un campo in cui far entrare un link di
// disiscrizione.
//
// È il vincolo espresso come proprietà del tipo, sullo stesso modello di
// TestDataTypesCarryNoSecrets: il controllo sul rendering verifica ciò che esce
// oggi, questo impedisce che domani ci sia un modo di farcelo entrare. Chi
// aggiungesse un `UnsubscribeURL` a [emailrender.SecurityAlertData] per «dare
// all'utente la possibilità di non ricevere più questi avvisi» troverebbe
// questo test rosso prima di trovarci un revisore d'accordo.
func TestSoloIlMarketingHaUnCampoPerLaDisiscrizione(t *testing.T) {
	transazionali := map[emailrender.Event]reflect.Type{
		emailrender.EventWelcome:       reflect.TypeOf(emailrender.WelcomeData{}),
		emailrender.EventJobFailed:     reflect.TypeOf(emailrender.JobFailedData{}),
		emailrender.EventPlanChanged:   reflect.TypeOf(emailrender.PlanChangedData{}),
		emailrender.EventSecurityAlert: reflect.TypeOf(emailrender.SecurityAlertData{}),
	}

	// L'elenco qui sopra deve coprire ogni evento transazionale: se ne comparisse
	// uno nuovo, un controllo che non lo guarda approverebbe senza averlo visto.
	for _, event := range emailrender.Events() {
		marketing, declared := emailrender.IsMarketing(event)
		if !declared || marketing {
			continue
		}
		if _, ok := transazionali[event]; !ok {
			t.Errorf("%s è transazionale e non compare in questo controllo: "+
				"aggiungi il suo tipo di dati, altrimenti nessuno guarda i suoi campi", event)
		}
	}

	for _, tipo := range transazionali {
		for i := range tipo.NumField() {
			nome := tipo.Field(i).Name
			if strings.Contains(strings.ToLower(nome), "unsubscribe") {
				t.Errorf("%s.%s: un'email transazionale non porta un link di disiscrizione (§2.7). "+
					"Se l'utente deve poter smettere di ricevere qualcosa, quel qualcosa è marketing, "+
					"e va dichiarato tale in `kinds`", tipo.Name(), nome)
			}
		}
	}

	// E il contrario: il tipo di marketing ce l'ha, ed è obbligatorio.
	prodotto := reflect.TypeOf(emailrender.ProductUpdateData{})
	if _, ok := prodotto.FieldByName("UnsubscribeURL"); !ok {
		t.Error("ProductUpdateData non ha più UnsubscribeURL: senza, §2.8 non è implementabile")
	}
}
