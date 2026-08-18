package httpapi_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	// Le due eccezioni sono dichiarate, non tollerate: `POST /secrets` e le rotte
	// delle chiavi AI redigono il messaggio invece di rimandarlo, perché
	// `json.Decoder` cita il testo che non è riuscito a leggere e nel corpo di
	// quelle richieste quel testo può essere il segreto (R43). Riusano lo stesso
	// stato e lo stesso codice, non `err.Error()`.
	eccezioni := []string{"secrets.go", "aicreds.go"}

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
		if slices.Contains(eccezioni, nome) {
			// L'eccezione deve restare tale: se smettesse di redigere, sarebbe da
			// ricondurre all'helper invece che lasciata a metà.
			if !strings.Contains(testo, "errBodyTooLarge") {
				t.Errorf("%s è dichiarato eccezione ma non riconosce errBodyTooLarge: "+
					"o traduce da sé, o usa writeDecodeError", nome)
			}
			continue
		}
		if !strings.Contains(testo, "writeDecodeError") {
			t.Errorf("%s legge un corpo JSON ma non usa writeDecodeError: "+
				"è il difetto del checkout, dove ogni errore di decodifica finiva in un solo "+
				"400 e un corpo troppo grande non diventava mai 413", nome)
		}
	}

	// Se il nome della funzione cambiasse, il ciclo non troverebbe niente e il
	// test passerebbe senza aver guardato nulla.
	if visti < 5 {
		t.Fatalf("rotte che leggono un corpo trovate = %d, attese almeno 5: "+
			"il test non sta più guardando ciò che crede", visti)
	}
}
