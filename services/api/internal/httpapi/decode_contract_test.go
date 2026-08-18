package httpapi_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestOgniRottaCheLeggeUnCorpoTraduceGliErroriAlloStessoModo guarda il sorgente,
// non il comportamento, e lo fa di proposito.
//
// Il difetto che questo test esiste per impedire non era in un gestore: era
// nella *forma* del codice. La traduzione dell'errore di `decodeJSON` era
// ricopiata a mano in sei punti, identica cinque volte, e la sesta — il checkout
// — non riconosceva `errBodyTooLarge` affatto. Nessun test di quel gestore
// poteva accorgersene, perché quel gestore faceva esattamente ciò che c'era
// scritto: mancava un confronto fra file, che è una cosa che i test di
// comportamento non fanno.
//
// Provare il comportamento di ogni rotta una per una lo coprirebbe, ma solo per
// le rotte che esistono oggi. Questo test copre anche la settima.
func TestOgniRottaCheLeggeUnCorpoTraduceGliErroriAlloStessoModo(t *testing.T) {
	// L'invariante non è «usa l'helper»: è **nessuno può lasciar cadere in
	// silenzio `errBodyTooLarge`**. Sono due cose diverse, e la prima stesura di
	// questo test confondeva la seconda con la prima tenendo un elenco di nomi di
	// file da aggiornare a mano — cioè la stessa fragilità che il test esiste per
	// impedire. Se n'è accorta la issue #460 al primo rebase: `account.go` era
	// un'eccezione legittima che l'elenco non conosceva.
	//
	// Le eccezioni sono quelle rotte il cui corpo porta materiale che non si può
	// citare — un segreto, una chiave AI, una password — e che quindi traducono
	// da sé riusando stato e codice ma **non** `err.Error()`, perché
	// `json.Decoder` cita il testo che non è riuscito a leggere (R43). Si
	// dichiarano nel codice, nominando `errBodyTooLarge`: chi lo nomina ha
	// deciso, chi non lo nomina ha dimenticato. È la distinzione che separava le
	// cinque copie giuste dal checkout.
	sorgenti, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	// La firma del difetto: un file che chiama `decodeJSON` senza passare dalla
	// traduzione condivisa e senza essere una delle eccezioni dichiarate.
	chiama := regexp.MustCompile(`decodeJSON(Limit)?\(`)

	var visti int
	for _, f := range sorgenti {
		if strings.HasSuffix(f, "_test.go") || f == "httpapi.go" {
			continue // httpapi.go è dove la traduzione vive
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		testo := string(b)
		if !chiama.MatchString(testo) {
			continue
		}
		visti++

		nome := filepath.Base(f)
		if strings.Contains(testo, "writeDecodeError") || strings.Contains(testo, "errBodyTooLarge") {
			continue
		}
		t.Errorf("%s legge un corpo JSON e non nomina né writeDecodeError né errBodyTooLarge: "+
			"è il difetto del checkout, dove ogni errore di decodifica finiva in un solo 400 e "+
			"un corpo troppo grande non diventava mai 413. Usa writeDecodeError; se il corpo di "+
			"questa rotta porta materiale che non si può citare, traduci da te riusando stato e "+
			"codice ma non err.Error(), come fanno secrets.go, aicreds.go e account.go", nome)
	}

	// Se il nome della funzione cambiasse, il ciclo non troverebbe niente e il
	// test passerebbe senza aver guardato nulla.
	if visti < 5 {
		t.Fatalf("rotte che leggono un corpo trovate = %d, attese almeno 5: "+
			"il test non sta più guardando ciò che crede", visti)
	}
}
