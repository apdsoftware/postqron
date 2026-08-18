package marketing_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// importPath è il percorso con cui questo package viene importato.
const importPath = "github.com/apdsoftware/postqron/services/api/internal/marketing"

// perimetro elenca i package che possono conoscere il consenso al marketing.
//
// L'elenco è corto **apposta**, ed è la forma verificabile di una frase di
// §2.8: «refusing costs you nothing: the service works the same».
//
// Quella promessa non si dimostra leggendo il codice di oggi — si dimostra
// impedendo al codice di domani di romperla. Il modo in cui si romperebbe non è
// un `if` scritto male: è che qualcuno, fra un anno, consulti questo consenso
// per decidere qualcosa che non è un'email di prodotto. Una funzionalità
// riservata a chi «resta in contatto», un onboarding che salta un passo, una
// segnalazione in dashboard. Nessuno lo farebbe di proposito; tutti lo
// troverebbero comodo.
//
// Da qui la regola: chi ha bisogno di questo package deve prima aggiungersi a
// questo elenco, e in quel momento leggere perché l'elenco esiste.
var perimetro = []string{
	// Le rotte del consenso e della disiscrizione (§2.8).
	"internal/httpapi",
	// L'implementazione PostgreSQL della traccia.
	"internal/marketingpg",
	// La composizione all'avvio.
	"cmd/api",
}

// Nessun package fuori dal perimetro conosce il consenso al marketing.
//
// È il controllo che tiene vera la frase «rifiutare non costa niente»: finché
// solo questi tre importano il package, non esiste un posto in cui una scelta
// sul marketing possa cambiare il servizio.
func TestNessunAltroPackageConosceIlConsenso(t *testing.T) {
	radice, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("radice del modulo: %v", err)
	}

	var fuori []string
	err = filepath.WalkDir(radice, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(raw), `"`+importPath+`"`) {
			return nil
		}

		dir, err := filepath.Rel(radice, filepath.Dir(path))
		if err != nil {
			return err
		}
		dir = filepath.ToSlash(dir)
		// Il package stesso e i suoi test non sono «un altro package».
		if dir == "internal/marketing" {
			return nil
		}
		if !slices.Contains(perimetro, dir) && !slices.Contains(fuori, dir) {
			fuori = append(fuori, dir)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scansione dei sorgenti: %v", err)
	}

	for _, dir := range fuori {
		t.Errorf("%s importa il consenso al marketing, e non è fra i package che devono conoscerlo.\n"+
			"La privacy policy §2.8 dice «refusing costs you nothing: the service works the same»: "+
			"ogni punto del prodotto che legge questa scelta è un punto in cui rifiutare comincia a costare qualcosa.\n"+
			"Se il nuovo uso è davvero un'email di prodotto, aggiungi il package a `perimetro`; "+
			"se non lo è, la risposta non sta in questo consenso.", dir)
	}
}

// E il perimetro non deve invecchiare: un package elencato che non importa più
// niente è una voce che dice di sorvegliare qualcosa che non c'è.
func TestIlPerimetroNonElencaPackageCheNonImportano(t *testing.T) {
	radice, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("radice del modulo: %v", err)
	}

	for _, dir := range perimetro {
		voci, err := os.ReadDir(filepath.Join(radice, dir))
		if err != nil {
			t.Errorf("%s: elencato nel perimetro ma non esiste: %v", dir, err)
			continue
		}

		importa := false
		for _, voce := range voci {
			if voce.IsDir() || !strings.HasSuffix(voce.Name(), ".go") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(radice, dir, voce.Name()))
			if err != nil {
				t.Fatalf("lettura di %s: %v", voce.Name(), err)
			}
			if strings.Contains(string(raw), `"`+importPath+`"`) {
				importa = true
				break
			}
		}
		if !importa {
			t.Errorf("%s è nel perimetro e non importa più il consenso al marketing: "+
				"toglilo, altrimenti l'elenco dice di sorvegliare qualcosa che non c'è", dir)
		}
	}
}
