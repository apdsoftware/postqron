package legal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Questo file mette insieme le due metà del registro, e la divisione fra loro è
// una decisione, non una comodità.
//
// **In Go sta ciò che una revisione decide una volta e non deve poter cambiare
// in silenzio**: quali versioni esistono, da quando vincolano, se un cambiamento
// è materiale (Termini §9) e che impronta ha il testo inglese. Sono i fatti su
// cui poggia una prova di consenso, e vederli cambiare deve costare una riga di
// diff leggibile in review.
//
// **Nei file sta ciò che cambia da sé nel tempo**: se una traduzione è stata
// rivista. `legal/README.md` lo dice esplicitamente — «approvare è quindi una
// riga sola, `pending-review` → `approved`» — e una riga sola deve restare una
// riga sola. Se lo stato fosse anche in Go, approvare l'italiano sarebbe due
// modifiche in due posti, e il giorno in cui qualcuno ne fa una sola il sito
// mostrerebbe un testo e la prova ne dichiarerebbe un altro.
//
// Da qui la forma: [Current] è lo scheletro dichiarato e non legge niente;
// [Load] legge `legal/`, **verifica** che i file dicano quello che il registro
// dichiara, e restituisce un registro completo di traduzioni e stati.

// DirName è la directory dei documenti legali, relativa alla radice del
// repository.
//
// Sta lì e non dentro il modulo Go perché è lì che la vogliono la spec e il
// sito, che la legge in fase di build; e da lì non si può includere nel binario
// con `go:embed`, che non attraversa il confine del modulo. È lo stesso vincolo
// di `db/migrations` e di `emails/templates`, risolto allo stesso modo — vedi
// [migrate.FindDir] e [emailrender.FindDir]. Chi distribuisce il servizio porta
// con sé questa directory; chi preferisce incorporarla passa a [LoadFS] un
// `fs.FS` qualunque.
const DirName = "legal"

// DirEnvVar sovrascrive la ricerca automatica.
const DirEnvVar = "POSTQRON_LEGAL_DIR"

// FindDir individua la directory dei documenti legali.
//
// Nell'ordine: la variabile POSTQRON_LEGAL_DIR, se impostata; altrimenti
// `legal/` cercata risalendo da start. La risalita serve perché il processo
// viene lanciato tanto dalla radice del monorepo quanto da services/api, e i
// test girano nella directory del proprio package.
func FindDir(start string) (string, error) {
	if fromEnv := strings.TrimSpace(os.Getenv(DirEnvVar)); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err != nil || !info.IsDir() {
			return "", fmt.Errorf("%s non è una directory leggibile: %q", DirEnvVar, fromEnv)
		}
		return fromEnv, nil
	}

	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, DirName)
		if info, err := os.Stat(filepath.Join(candidate, string(SourceLanguage))); err == nil && info.IsDir() {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"%s non trovata risalendo da %s; indicala con %s: %w",
				DirName, start, DirEnvVar, fs.ErrNotExist)
		}
		dir = parent
	}
}

// Load legge i documenti dalla directory indicata e restituisce il registro
// completo.
func Load(dir string) (*Registry, error) {
	return LoadFS(os.DirFS(dir))
}

// LoadFS è [Load] su un filesystem qualunque.
//
// # Che cosa verifica, e perché fallisce invece di adattarsi
//
// Ogni file dev'essere **un rilascio che il registro dichiara**: stesso
// documento, stessa versione, stessa data, stessa lingua della directory che lo
// contiene. Un file che non corrisponde non viene ignorato e non viene accolto:
// fa fallire il caricamento.
//
// È severo di proposito, e la ragione sta nel difetto che chiude. Un documento
// legale che il registro non conosce è un documento che **nessuno accetterà
// mai** e di cui non esisterà nessuna prova di consenso: non ha nessun altro
// sintomo, non rompe niente, e si scopre il giorno in cui qualcuno lo contesta.
// Lo stesso vale al rovescio per un testo riscritto sotto la stessa versione:
// chi l'aveva accettato ha accettato qualcos'altro.
//
// La differenza fra le due cose che possono comparire in `legal/` è deliberata:
//
//   - una **lingua nuova** di un documento già dichiarato entra da sola. È una
//     traduzione, non una decisione: il registro la prende, la segna con lo
//     stato che il suo front matter dichiara, e nessuno deve toccare il Go;
//   - un **documento nuovo**, o una **versione nuova**, no. Quelle sono
//     decisioni — da quando vincola, se la modifica è materiale — e vanno
//     dichiarate da una persona in un posto che si legge in review.
func LoadFS(fsys fs.FS) (*Registry, error) {
	dichiarato, err := NewRegistry(declared)
	if err != nil {
		return nil, err
	}

	// I testi si accumulano qui, e solo alla fine sostituiscono quelli
	// dichiarati: un caricamento che fallisce a metà non deve lasciare un
	// registro mezzo aggiornato in mano a nessuno.
	trovati := map[Document]map[string]map[Language]Text{}

	for _, lang := range Languages() {
		voci, err := fs.ReadDir(fsys, string(lang))
		if errors.Is(err, fs.ErrNotExist) {
			if lang == SourceLanguage {
				return nil, fmt.Errorf("legal: manca %s/, che è la lingua sorgente (SPEC §8-bis)", lang)
			}
			// Una lingua non ancora tradotta non è un errore: è il caso che
			// [Release.Presented] gestisce mostrando l'inglese.
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("legal: lettura di %s/: %w", lang, err)
		}

		for _, voce := range voci {
			if voce.IsDir() || !strings.HasSuffix(voce.Name(), ".md") {
				continue
			}
			percorso := path.Join(string(lang), voce.Name())
			contenuto, err := fs.ReadFile(fsys, percorso)
			if err != nil {
				return nil, fmt.Errorf("legal: lettura di %s: %w", percorso, err)
			}

			doc, rel, text, err := interpreta(dichiarato, percorso, lang, voce.Name(), contenuto)
			if err != nil {
				return nil, err
			}
			if trovati[doc] == nil {
				trovati[doc] = map[string]map[Language]Text{}
			}
			if trovati[doc][rel.Version] == nil {
				trovati[doc][rel.Version] = map[Language]Text{}
			}
			if _, doppio := trovati[doc][rel.Version][lang]; doppio {
				return nil, fmt.Errorf("legal: %s %s (%s): due file dichiarano lo stesso documento", doc, rel.Version, lang)
			}
			trovati[doc][rel.Version][lang] = text
		}
	}

	completo := map[Document][]Release{}
	for _, doc := range Documents() {
		rilasci := dichiarato.Releases(doc)
		for i, rel := range rilasci {
			testi := trovati[doc][rel.Version]
			if _, ok := testi[SourceLanguage]; !ok {
				return nil, fmt.Errorf(
					"legal: %s %s: il registro dichiara una versione di cui non esiste il testo inglese (%s)",
					doc, rel.Version, path.Join(string(SourceLanguage), rel.FileName(doc, SourceLanguage)))
			}
			rilasci[i].Texts = testi
		}
		completo[doc] = rilasci
	}
	return NewRegistry(completo)
}

// interpreta legge un file e lo riconosce come rilascio dichiarato.
func interpreta(
	reg *Registry, percorso string, lang Language, nomeFile string, contenuto []byte,
) (Document, Release, Text, error) {
	fallisci := func(format string, args ...any) (Document, Release, Text, error) {
		return "", Release{}, Text{}, fmt.Errorf("legal: %s: "+format, append([]any{percorso}, args...)...)
	}

	fm, corpo, err := parseFrontMatter(contenuto)
	if err != nil {
		return fallisci("%w", err)
	}

	doc, err := ParseDocument(fm["document"])
	if err != nil {
		// È il caso per cui questo controllo esiste: un quinto documento legale
		// che compare in `legal/` e che nessuno accetterà mai.
		return fallisci(
			"dichiara `document: %s`, che il registro non conosce. Un documento legale che il registro ignora "+
				"è un documento che nessuno accetterà mai, e di cui non esisterà nessuna prova di consenso (R46): "+
				"va dichiarato in internal/legal, con la sua data di entrata in vigore",
			fm["document"])
	}
	if fm["language"] != string(lang) {
		return fallisci("dichiara `language: %s` ma sta in %s/", fm["language"], lang)
	}

	var rilascio Release
	var trovato bool
	for _, rel := range reg.Releases(doc) {
		if rel.Version == fm["version"] {
			rilascio, trovato = rel, true
			break
		}
	}
	if !trovato {
		return fallisci(
			"è alla versione %s, che il registro non dichiara per %s. Una versione nuova è una decisione — "+
				"da quando vincola, e se la modifica è materiale (Termini §9) — e va dichiarata in internal/legal",
			fm["version"], doc)
	}
	if atteso := rilascio.FileName(doc, lang); nomeFile != atteso {
		return fallisci("il registro si aspetta la %s nel file %s", rilascio.Version, atteso)
	}

	data, err := time.Parse(time.DateOnly, fm["effective_date"])
	if err != nil {
		return fallisci("`effective_date` illeggibile: %w", err)
	}
	if !data.UTC().Equal(rilascio.Effective) {
		return fallisci(
			"entra in vigore il %s, il registro dice %s. Le traduzioni portano la data dell'originale (legal/README.md)",
			data.Format(time.DateOnly), rilascio.Effective.Format(time.DateOnly))
	}

	stato, err := statoDichiarato(lang, fm["status"])
	if err != nil {
		return fallisci("%w", err)
	}

	somma := sha256.Sum256(corpo)
	impronta := hex.EncodeToString(somma[:])

	// L'impronta dell'inglese è dichiarata in Go, ed è qui che serve: è il
	// controllo che impedisce di riscrivere un testo in vigore senza cambiargli
	// versione. Le traduzioni non ne hanno una dichiarata — sono in revisione, e
	// una revisione le corregge — ma la loro impronta finisce comunque nella
	// prova di ogni consenso, che è dove serve davvero.
	if attesa := rilascio.Texts[lang].SHA256; lang == SourceLanguage && attesa != "" && attesa != impronta {
		return fallisci(
			"il testo è cambiato senza cambiare versione.\n  impronta del file:   %s\n  impronta dichiarata: %s\n"+
				"Un documento in vigore non si modifica (legal/README.md): chi ha accettato la %s ha accettato un testo "+
				"che adesso non esiste più, e la sua prova non ha più oggetto. Se la modifica è voluta, è una versione nuova",
			impronta, attesa, rilascio.Version)
	}

	return doc, rilascio, Text{
		File:   rilascio.Texts[lang].File,
		SHA256: impronta,
		Status: stato,
	}, nil
}

// statoDichiarato traduce il campo `status` del front matter.
//
// L'inglese non lo porta e non lo deve portare: è la lingua sorgente, non ha una
// revisione da attendere. Una traduzione senza `status` è invece un file
// incompleto, e trattarla come approvata sarebbe il modo più silenzioso di
// pubblicare un testo che nessuno ha rivisto.
func statoDichiarato(lang Language, dichiarato string) (Status, error) {
	if lang == SourceLanguage {
		if dichiarato != "" {
			return "", fmt.Errorf(
				"l'inglese dichiara `status: %s`: è la lingua sorgente, non ha una revisione da attendere (legal/README.md)",
				dichiarato)
		}
		return StatusSource, nil
	}
	switch Status(dichiarato) {
	case StatusPendingReview, StatusApproved:
		return Status(dichiarato), nil
	case "":
		return "", errors.New(
			"il front matter non dichiara `status`. Una traduzione è `pending-review` finché non è `approved`, " +
				"e finché non lo è l'applicazione mostra l'originale inglese (legal/README.md)")
	default:
		return "", fmt.Errorf("`status: %s` non è né `pending-review` né `approved`", dichiarato)
	}
}

// ------------------------------------------------------------- front matter

var frontMatterPattern = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n+`)

// parseFrontMatter separa i campi dal corpo.
//
// Il corpo è ciò che entra nell'impronta, e comincia **dopo** il front matter e
// le righe vuote che lo seguono: è lo stesso taglio che fa
// apps/web/utils/legal.ts, e devono restare lo stesso taglio, altrimenti le due
// parti del sistema parlerebbero di due testi diversi chiamandoli con lo stesso
// nome.
//
// I campi si leggono riga per riga e non con un parser YAML perché sono cinque
// scalari: il formato è fissato da `legal/README.md`, e una dipendenza in più
// per leggerlo sarebbe sproporzionata.
func parseFrontMatter(contenuto []byte) (map[string]string, []byte, error) {
	m := frontMatterPattern.FindSubmatchIndex(contenuto)
	if m == nil {
		return nil, nil, errors.New("non comincia con un blocco front matter fra `---`")
	}

	campi := map[string]string{}
	for _, riga := range strings.Split(string(contenuto[m[2]:m[3]]), "\n") {
		chiave, valore, ok := strings.Cut(riga, ":")
		if !ok {
			continue
		}
		campi[strings.TrimSpace(chiave)] = strings.TrimSpace(valore)
	}
	for _, obbligatorio := range []string{"document", "version", "effective_date", "language"} {
		if campi[obbligatorio] == "" {
			return nil, nil, fmt.Errorf("il front matter non dichiara `%s`", obbligatorio)
		}
	}
	return campi, contenuto[m[1]:], nil
}

// ------------------------------------------------------------------ stato

// Translation è lo stato di una traduzione, come lo vede chi deve sapere a che
// punto è la revisione.
type Translation struct {
	Document Document
	Version  string
	Language Language
	Status   Status
}

// Translations elenca le traduzioni dei rilasci in vigore, in ordine di
// documento e lingua.
//
// L'inglese non c'è: non è una traduzione. Serve a rispondere alla domanda che
// [Release.Presented] non risponde — «a che punto siamo con le lingue?» — che è
// una domanda editoriale, non una che decide cosa finisce in una prova.
func (r *Registry) Translations(at time.Time) []Translation {
	var out []Translation
	for _, dr := range r.InForceAll(at) {
		for _, lang := range Languages() {
			if lang == SourceLanguage {
				continue
			}
			text, ok := dr.Release.Texts[lang]
			if !ok {
				continue
			}
			out = append(out, Translation{
				Document: dr.Document,
				Version:  dr.Release.Version,
				Language: lang,
				Status:   text.Status,
			})
		}
	}
	slices.SortStableFunc(out, func(a, b Translation) int {
		if d := slices.Index(Documents(), a.Document) - slices.Index(Documents(), b.Document); d != 0 {
			return d
		}
		return slices.Index(Languages(), a.Language) - slices.Index(Languages(), b.Language)
	})
	return out
}
