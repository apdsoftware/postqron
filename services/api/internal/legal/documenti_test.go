package legal_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/legal"
)

// Questo file non prova il codice: prova che il codice e i quattro documenti
// legali dicano le stesse cose.
//
// È la stessa forma del test della grazia in internal/account, e per lo stesso
// motivo. Il registro di internal/legal è una copia in Go del frontmatter di
// `legal/`, e una copia che nessuno confronta diverge: il giorno in cui qualcuno
// corregge una frase dei Termini senza toccare il registro, la prova del
// consenso continuerebbe a dire «1.2.0» su un testo che non è più quello. È
// esattamente il difetto che R46 esiste per rendere impossibile.
//
// La direzione conta, e vale la pena scriverla qui come è scritta là: se questi
// test falliscono, non si allinea il documento al codice. Si guarda quale dei
// due è quello giusto, e quasi sempre è il documento — perché è quello che
// l'utente ha letto e accettato.

// frontmatter è il blocco YAML in testa a ogni documento. Si legge con quattro
// espressioni regolari e non con un parser YAML perché sono quattro campi
// scalari: una dipendenza in più per leggerli sarebbe sproporzionata, e il
// formato è fissato da `legal/README.md`.
type frontmatter struct {
	document      string
	version       string
	effectiveDate time.Time
	language      string
}

var campoFrontmatter = map[string]*regexp.Regexp{
	"document":       regexp.MustCompile(`(?m)^document:\s*(\S+)\s*$`),
	"version":        regexp.MustCompile(`(?m)^version:\s*(\S+)\s*$`),
	"effective_date": regexp.MustCompile(`(?m)^effective_date:\s*(\S+)\s*$`),
	"language":       regexp.MustCompile(`(?m)^language:\s*(\S+)\s*$`),
}

func leggiFrontmatter(t *testing.T, percorso string, contenuto []byte) frontmatter {
	t.Helper()

	testa, _, trovato := strings.Cut(strings.TrimPrefix(string(contenuto), "---\n"), "\n---")
	if !trovato {
		t.Fatalf("%s: non comincia con un blocco frontmatter fra `---`", percorso)
	}

	valore := func(campo string) string {
		m := campoFrontmatter[campo].FindStringSubmatch(testa)
		if m == nil {
			t.Fatalf("%s: il frontmatter non dichiara `%s`", percorso, campo)
		}
		return m[1]
	}

	data, err := time.Parse("2006-01-02", valore("effective_date"))
	if err != nil {
		t.Fatalf("%s: `effective_date` illeggibile: %v", percorso, err)
	}
	return frontmatter{
		document:      valore("document"),
		version:       valore("version"),
		effectiveDate: data.UTC(),
		language:      valore("language"),
	}
}

// TestIlRegistroDescriveIFileVeri confronta ogni rilascio dichiarato con il file
// che dice di descrivere.
//
// Il confronto dell'impronta è la parte che conta di più, ed è quella che
// nessun'altra cosa fa: versione e data si possono correggere a occhio, il testo
// no. Con l'impronta, **modificare una virgola di un documento in vigore rende
// rossa la CI** — che è il modo in cui `legal/README.md` («non si modifica un
// documento in vigore: si crea una versione nuova») smette di essere una
// raccomandazione e diventa un controllo.
func TestIlRegistroDescriveIFileVeri(t *testing.T) {
	radice := radiceDelRepository(t)
	registro := legal.Current()

	for _, doc := range legal.Documents() {
		rilasci := registro.Releases(doc)
		if len(rilasci) == 0 {
			t.Errorf("%s: nessun rilascio dichiarato", doc)
			continue
		}

		for _, rel := range rilasci {
			for _, lang := range legal.Languages() {
				impronta, dichiarato := rel.Texts[lang]
				if !dichiarato {
					continue
				}
				percorso := filepath.Join(radice, "legal", string(lang), rel.FileName(doc, lang))
				contenuto, err := os.ReadFile(percorso)
				if err != nil {
					t.Errorf("%s %s (%s): il registro dichiara un testo che non esiste: %v",
						doc, rel.Version, lang, err)
					continue
				}

				fm := leggiFrontmatter(t, percorso, contenuto)
				if fm.document != string(doc) {
					t.Errorf("%s: il file dichiara `document: %s`", percorso, fm.document)
				}
				if fm.version != rel.Version {
					t.Errorf("%s: il file è alla versione %s, il registro dichiara %s.\n"+
						"Se il testo è cambiato, è una versione nuova: il registro va aggiornato, non il file riscritto sotto un consenso già prestato (R46).",
						percorso, fm.version, rel.Version)
				}
				if !fm.effectiveDate.Equal(rel.Effective) {
					t.Errorf("%s: il file entra in vigore il %s, il registro dice %s",
						percorso, fm.effectiveDate.Format(time.DateOnly), rel.Effective.Format(time.DateOnly))
				}
				if fm.language != string(lang) {
					t.Errorf("%s: il file dichiara `language: %s`, il registro lo elenca come %s",
						percorso, fm.language, lang)
				}

				somma := sha256.Sum256(contenuto)
				if calcolata := hex.EncodeToString(somma[:]); calcolata != impronta.SHA256 {
					t.Errorf("%s: il testo è cambiato senza cambiare versione.\n"+
						"  impronta del file:     %s\n"+
						"  impronta dichiarata:   %s\n"+
						"Un documento in vigore non si modifica (legal/README.md): chi ha accettato la %s ha accettato "+
						"un testo che adesso non esiste più, e la sua prova non ha più oggetto. "+
						"Se la modifica è voluta, è una versione nuova.",
						percorso, calcolata, impronta.SHA256, rel.Version)
				}
			}
		}
	}
}

// TestOgniDocumentoDiLegalÈNelRegistro chiude la strada opposta: un documento
// che esiste nel repository e che il registro non conosce.
//
// Senza questo controllo, aggiungere un quinto documento legale sarebbe un
// documento che nessuno accetta mai e di cui non esiste nessuna prova di
// consenso — un buco che si scopre solo quando qualcuno lo contesta.
func TestOgniDocumentoDiLegalÈNelRegistro(t *testing.T) {
	radice := radiceDelRepository(t)
	registro := legal.Current()

	dichiarati := map[string]bool{}
	for _, doc := range legal.Documents() {
		for _, rel := range registro.Releases(doc) {
			for lang := range rel.Texts {
				dichiarati[filepath.Join(string(lang), rel.FileName(doc, lang))] = true
			}
		}
	}

	for _, lang := range legal.Languages() {
		dir := filepath.Join(radice, "legal", string(lang))
		voci, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			// La lingua non è ancora tradotta (#447): non è un errore, è il
			// caso che [legal.Release.Presented] gestisce mostrando l'inglese.
			continue
		}
		if err != nil {
			t.Fatalf("lettura di %s: %v", dir, err)
		}
		for _, voce := range voci {
			if voce.IsDir() || !strings.HasSuffix(voce.Name(), ".md") {
				continue
			}
			chiave := filepath.Join(string(lang), voce.Name())
			if !dichiarati[chiave] {
				t.Errorf("legal/%s esiste e il registro non lo conosce: nessuno lo accetterà mai, "+
					"e di quel documento non esisterà nessuna prova di consenso (R46)", chiave)
			}
		}
	}
}

// TestIlPreavvisoÈQuelloDeiTermini rilegge dai Termini §9 il numero di giorni,
// invece di fidarsi della costante.
//
// È lo stesso controllo che internal/account fa sulla grazia della privacy
// policy, sull'altra promessa di calendario che i documenti fanno: se una
// revisione legale porta il preavviso da trenta a sessanta giorni, il registro
// deve accorgersene: continuare a validare rilasci con trenta giorni di
// preavviso significherebbe pubblicare una modifica materiale senza il
// preavviso che il documento promette.
func TestIlPreavvisoÈQuelloDeiTermini(t *testing.T) {
	percorso := filepath.Join(radiceDelRepository(t), "legal", "en", "terms-of-service.md")
	testo, err := os.ReadFile(percorso)
	if err != nil {
		t.Fatalf("Termini non leggibili (%s): %v", percorso, err)
	}

	// Il documento spezza la frase su più righe: «we give you\n30 days'\nnotice».
	// L'espressione tollera qualunque spaziatura proprio per questo — la
	// formattazione del paragrafo cambia a ogni riedizione, e un test legato agli
	// a capo fallirebbe per un motivo che non interessa a nessuno.
	preavviso := regexp.MustCompile(`(?is)we\s+give\s+you\s+(\d+)\s+days'?\s+notice`)
	m := preavviso.FindSubmatch(testo)
	if m == nil {
		t.Fatalf("in %s non si trova più la frase «we give you N days' notice» (§9): "+
			"o il documento è cambiato e il registro va riallineato, o questo test guarda il file sbagliato", percorso)
	}

	giorni, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("numero di giorni non leggibile: %v", err)
	}
	dichiarato := time.Duration(giorni) * 24 * time.Hour

	if legal.MaterialChangeNotice != dichiarato {
		t.Fatalf("i Termini §9 promettono %d giorni di preavviso, il registro ne pretende %v.\n"+
			"Non allineare il documento al codice senza guardare: il numero che l'utente ha letto e accettato è quello del documento (R46).",
			giorni, legal.MaterialChangeNotice)
	}
}

// TestLeQuattroVersioniNonSonoAllineate è la verifica del fatto che ha deciso la
// forma di questa traccia.
//
// I quattro documenti stanno a versioni diverse — 1.2.0, 1.1.0, 1.0.0, 1.0.0 — e
// finché è così, un consenso registrato con **una versione sola per tutti e
// quattro** è falso. Il test esiste per rendere quel fatto una condizione
// verificata invece di una frase in un commento: se un giorno le versioni si
// riallineassero per caso, questo test si spegnerebbe da solo dicendolo, invece
// di lasciar credere che stia ancora controllando qualcosa.
func TestLeQuattroVersioniNonSonoAllineate(t *testing.T) {
	registro := legal.Current()
	adesso := time.Now()

	versioni := map[string][]legal.Document{}
	for _, dr := range registro.InForceAll(adesso) {
		versioni[dr.Release.Version] = append(versioni[dr.Release.Version], dr.Document)
	}
	if len(versioni) == 1 {
		t.Skipf("i quattro documenti sono tutti alla %s: oggi una versione sola basterebbe, "+
			"ma la prima riedizione di uno solo di loro li disallinea di nuovo", chiaveSola(versioni))
	}
	if len(versioni) < 2 {
		t.Fatalf("nessun documento in vigore: il registro non descrive niente")
	}
	t.Logf("versioni in vigore: %v", versioni)
}

func chiaveSola[V any](m map[string]V) string {
	for k := range m {
		return k
	}
	return ""
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
