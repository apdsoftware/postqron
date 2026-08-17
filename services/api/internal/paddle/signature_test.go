package paddle_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

var (
	segreto = []byte("pdl_ntfset_segreto_di_prova_lungo_abbastanza")
	corpo   = []byte(`{"event_id":"evt_01","event_type":"subscription.updated","occurred_at":"2026-08-17T10:00:00.000000Z","data":{}}`)
	adesso  = time.Date(2026, 8, 17, 10, 0, 5, 0, time.UTC)
)

func TestFirmaValida(t *testing.T) {
	firma := paddle.Sign(segreto, corpo, adesso)

	if err := paddle.VerifySignature(segreto, corpo, firma, adesso, paddle.DefaultTolerance); err != nil {
		t.Fatalf("firma prodotta da Sign rifiutata da VerifySignature: %v", err)
	}
}

func TestFirmaRifiutata(t *testing.T) {
	valida := paddle.Sign(segreto, corpo, adesso)

	casi := []struct {
		nome  string
		firma string
		corpo []byte
	}{
		{"assente", "", corpo},
		{"segreto diverso", paddle.Sign([]byte("un altro segreto lungo abbastanza"), corpo, adesso), corpo},
		{
			// Il caso che dà il nome a tutta la difesa: la firma è autentica, il
			// corpo no. Senza verificare l'HMAC sui byte esatti ricevuti, questa
			// consegna passerebbe con dentro il piano che vuole chi l'ha alterata.
			"corpo alterato dopo la firma",
			valida,
			[]byte(`{"event_id":"evt_01","event_type":"subscription.updated","occurred_at":"2026-08-17T10:00:00.000000Z","data":{"id":"sub_intruso"}}`),
		},
		{"senza campo h1", "ts=" + unix(adesso), corpo},
		{"senza campo ts", strings.SplitN(valida, ";", 2)[1], corpo},
		{"ts riscritto", "ts=" + unix(adesso.Add(time.Minute)) + ";" + strings.SplitN(valida, ";", 2)[1], corpo},
		{"h1 non esadecimale", "ts=" + unix(adesso) + ";h1=non-esadecimale", corpo},
		{"h1 troncato", valida[:len(valida)-2], corpo},
		{"algoritmo dichiarato diverso", strings.Replace(valida, ";h1=", ";h9=", 1), corpo},
		{"ts ripetuto", "ts=" + unix(adesso) + ";" + valida, corpo},
		{"ts illeggibile", "ts=ieri;" + strings.SplitN(valida, ";", 2)[1], corpo},
		{"campi vuoti", ";;", corpo},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			err := paddle.VerifySignature(segreto, caso.corpo, caso.firma, adesso, paddle.DefaultTolerance)
			if !errors.Is(err, paddle.ErrInvalidSignature) {
				t.Fatalf("atteso ErrInvalidSignature, ottenuto %v", err)
			}
		})
	}
}

// La tolleranza guarda in entrambe le direzioni: un `ts` nel futuro è anomalo
// quanto uno nel passato, e accettarlo significherebbe accettare una firma che
// diventerà valida più tardi.
func TestFirmaFuoriTolleranza(t *testing.T) {
	casi := map[string]time.Time{
		"troppo vecchia": adesso.Add(-paddle.DefaultTolerance - time.Second),
		"nel futuro":     adesso.Add(paddle.DefaultTolerance + time.Second),
	}

	for nome, emessa := range casi {
		t.Run(nome, func(t *testing.T) {
			firma := paddle.Sign(segreto, corpo, emessa)
			err := paddle.VerifySignature(segreto, corpo, firma, adesso, paddle.DefaultTolerance)
			if !errors.Is(err, paddle.ErrInvalidSignature) {
				t.Fatalf("atteso ErrInvalidSignature, ottenuto %v", err)
			}
		})
	}

	// Ai bordi la firma vale ancora: la tolleranza è una distanza massima
	// ammessa, non una esclusa.
	for nome, emessa := range map[string]time.Time{
		"al limite nel passato": adesso.Add(-paddle.DefaultTolerance),
		"al limite nel futuro":  adesso.Add(paddle.DefaultTolerance),
	} {
		t.Run(nome, func(t *testing.T) {
			firma := paddle.Sign(segreto, corpo, emessa)
			if err := paddle.VerifySignature(segreto, corpo, firma, adesso, paddle.DefaultTolerance); err != nil {
				t.Fatalf("firma al limite della tolleranza rifiutata: %v", err)
			}
		})
	}
}

// Con tolleranza zero il controllo sul tempo non si applica. Serve ai test che
// costruiscono consegne datate, e non è una scorciatoia per la produzione: il
// costruttore del servizio sostituisce lo zero con [paddle.DefaultTolerance].
func TestTolleranzaZeroNonGuardaIlTempo(t *testing.T) {
	firma := paddle.Sign(segreto, corpo, adesso.Add(-30*24*time.Hour))

	if err := paddle.VerifySignature(segreto, corpo, firma, adesso, 0); err != nil {
		t.Fatalf("con tolleranza zero il tempo non deve contare: %v", err)
	}
}

func TestFirmaSenzaSegretoNonVerificaNiente(t *testing.T) {
	// Il segreto vuoto non deve produrre un HMAC valido sotto la chiave vuota: la
	// firma corrispondente esiste, ed è calcolabile da chiunque.
	firma := paddle.Sign(nil, corpo, adesso)

	if err := paddle.VerifySignature(nil, corpo, firma, adesso, paddle.DefaultTolerance); !errors.Is(err, paddle.ErrInvalidSignature) {
		t.Fatalf("atteso ErrInvalidSignature, ottenuto %v", err)
	}
}

// Durante la rotazione del segreto la testata può portare più di una firma:
// accettarne una qualsiasi non allarga niente, perché ciascuna dev'essere
// comunque un HMAC valido sotto la nostra chiave.
func TestPiuFirmeDichiarate(t *testing.T) {
	valida := paddle.Sign(segreto, corpo, adesso)
	altra := paddle.Sign([]byte("il segreto precedente, lungo abbastanza"), corpo, adesso)

	// La nostra è la seconda: fermarsi alla prima renderebbe la rotazione una
	// finestra di consegne rifiutate.
	composta := "ts=" + unix(adesso) + ";" +
		strings.SplitN(altra, ";", 2)[1] + ";" +
		strings.SplitN(valida, ";", 2)[1]

	if err := paddle.VerifySignature(segreto, corpo, composta, adesso, paddle.DefaultTolerance); err != nil {
		t.Fatalf("firma valida fra più dichiarate rifiutata: %v", err)
	}

	// Nessuna delle due nostra: rifiutata.
	soloAltre := "ts=" + unix(adesso) + ";" + strings.SplitN(altra, ";", 2)[1]
	if err := paddle.VerifySignature(segreto, corpo, soloAltre, adesso, paddle.DefaultTolerance); !errors.Is(err, paddle.ErrInvalidSignature) {
		t.Fatalf("atteso ErrInvalidSignature, ottenuto %v", err)
	}
}

// Un campo sconosciuto non deve spegnere il webhook: Paddle può aggiungerne uno
// senza preavviso, e ciò che ci serve resta leggibile.
func TestCampiSconosciutiNellaTestataVengonoIgnorati(t *testing.T) {
	firma := paddle.Sign(segreto, corpo, adesso) + ";v2=qualcosa"

	if err := paddle.VerifySignature(segreto, corpo, firma, adesso, paddle.DefaultTolerance); err != nil {
		t.Fatalf("campo sconosciuto ha fatto fallire la verifica: %v", err)
	}
}

func unix(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}
