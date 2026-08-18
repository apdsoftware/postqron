package account_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/account"
)

// Questo file contiene un test solo, e non prova il codice: prova che il codice
// e un documento legale dicano lo stesso numero.
//
// Il rischio che copre è preciso. `DefaultGrace` è una costante con accanto un
// commento che dice «sono i trenta giorni della privacy policy». Un commento non
// si accorge di niente: il giorno in cui qualcuno alza la costante a sessanta
// giorni, o il giorno in cui una revisione legale abbassa il documento a
// quattordici, i due valori divergono e nessun test se ne accorge — mentre la
// cosa che si è rotta è **una dichiarazione scritta in un documento pubblicato**,
// cioè il tipo di errore che scopre un'autorità e non un utente.
//
// La direzione conta: se questo test fallisce, non si allinea il documento al
// codice. Si guarda quale dei due è quello giusto, e quasi sempre è il
// documento — perché è quello che l'utente ha letto e accettato (R46).

// graziaDichiarata cerca la frase della privacy policy §5.
//
// Il documento la scrive spezzata su più righe:
//
//	«then remove the data after a grace period of
//	 30 days,
//	 during which you can change your mind»
//
// L'espressione regolare tollera qualunque spaziatura fra le parole proprio per
// questo: la formattazione del paragrafo cambia a ogni riedizione del testo, e
// un test legato alla posizione degli a capo fallirebbe per un motivo che non
// interessa a nessuno.
var graziaDichiarata = regexp.MustCompile(`(?is)grace\s+period\s+of\s+(\d+)\s+days`)

func TestLaGraziaPredefinitaÈQuellaDellaPrivacyPolicy(t *testing.T) {
	percorso := filepath.Join(radiceDelRepository(t), "legal", "en", "privacy-policy.md")
	testo, err := os.ReadFile(percorso)
	if err != nil {
		t.Fatalf("privacy policy non leggibile (%s): %v", percorso, err)
	}

	corrispondenza := graziaDichiarata.FindSubmatch(testo)
	if corrispondenza == nil {
		t.Fatalf("in %s non si trova più la frase «grace period of N days»: "+
			"o il documento è cambiato e il codice va riallineato, o questo test sta guardando il file sbagliato",
			percorso)
	}

	giorni, err := strconv.Atoi(string(corrispondenza[1]))
	if err != nil {
		t.Fatalf("numero di giorni non leggibile: %v", err)
	}
	dichiarata := time.Duration(giorni) * 24 * time.Hour

	if account.DefaultGrace != dichiarata {
		t.Fatalf("la privacy policy promette %d giorni, il codice ne applica %v.\n"+
			"Non allineare il documento al codice senza guardare: il numero che l'utente ha letto e accettato è quello del documento (R46).",
			giorni, account.DefaultGrace)
	}
}

// radiceDelRepository risale dalla directory del test fino a trovare `legal/`.
//
// Il numero di livelli non è scritto a mano perché cambierebbe al primo
// spostamento del package: si risale finché la directory cercata non compare.
func radiceDelRepository(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("directory corrente: %v", err)
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, "legal", "en")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("radice del repository non trovata: nessuna directory `legal/en` risalendo da qui")
		}
		dir = parent
	}
}
