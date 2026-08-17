package emailrender

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// LocalesDir è la sottodirectory dei testi, dentro la directory dei template.
const LocalesDir = "locales"

// catalog tiene i testi di tutte le lingue, appiattiti a chiavi con il punto.
//
// L'inglese non è una lingua fra le altre: è la sorgente (SPEC §8-bis) e
// l'unica che deve essere completa. Le altre sono sovrapposizioni parziali, e
// la ricaduta avviene **per chiave**, non per file — una traduzione a metà
// mostra ciò che è tradotto e l'inglese per il resto, invece di rovesciare
// tutta l'email sulla lingua sbagliata al primo testo mancante.
type catalog struct {
	byLanguage map[string]map[string]string
}

// loadCatalog legge locales/<lingua>.json per ognuna delle lingue supportate.
//
// Un file mancante è un errore: la lingua è dichiarata nella spec, e scoprire
// solo al primo invio che il suo file non c'è è esattamente il genere di
// silenzio che il fallback dovrebbe coprire ma non nascondere.
func loadCatalog(fsys fs.FS) (*catalog, error) {
	c := &catalog{byLanguage: make(map[string]map[string]string, len(Languages))}

	for _, lang := range Languages {
		name := path.Join(LocalesDir, lang+".json")
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("lettura di %s: %w", name, err)
		}

		var nested map[string]any
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil, fmt.Errorf("%s: JSON non valido: %w", name, err)
		}

		flat := make(map[string]string)
		if err := flatten(nested, "", flat); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		c.byLanguage[lang] = flat
	}

	if len(c.byLanguage[DefaultLanguage]) == 0 {
		return nil, fmt.Errorf("%s/%s.json è vuoto: è la lingua sorgente e deve contenere tutti i testi",
			LocalesDir, DefaultLanguage)
	}

	// Una chiave tradotta che l'inglese non ha è il residuo di un testo tolto
	// dalla sorgente: non verrebbe mai usata, e resterebbe lì a far credere di
	// esserlo. Meglio fermarsi al caricamento che scoprirlo mai.
	source := c.byLanguage[DefaultLanguage]
	for _, lang := range Languages {
		if lang == DefaultLanguage {
			continue
		}
		orphans := make([]string, 0)
		for key := range c.byLanguage[lang] {
			if _, ok := source[key]; !ok {
				orphans = append(orphans, key)
			}
		}
		if len(orphans) > 0 {
			sort.Strings(orphans)
			return nil, fmt.Errorf("%s/%s.json contiene chiavi che %s.json non ha: %s",
				LocalesDir, lang, DefaultLanguage, strings.Join(orphans, ", "))
		}
	}

	return c, nil
}

// flatten trasforma l'annidamento del JSON in chiavi separate dal punto.
func flatten(node map[string]any, prefix string, out map[string]string) error {
	for _, key := range slices.Sorted(maps.Keys(node)) {
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		switch value := node[key].(type) {
		case string:
			out[full] = value
		case map[string]any:
			if err := flatten(value, full, out); err != nil {
				return err
			}
		default:
			return fmt.Errorf("la chiave %q non è né una stringa né un oggetto", full)
		}
	}
	return nil
}

// resolve individua la lingua che fornisce davvero la chiave: quella richiesta
// se la contiene, altrimenti l'inglese.
func (c *catalog) resolve(lang, key string) (string, bool) {
	if texts, ok := c.byLanguage[lang]; ok {
		if _, ok := texts[key]; ok {
			return lang, true
		}
	}
	if _, ok := c.byLanguage[DefaultLanguage][key]; ok {
		return DefaultLanguage, true
	}
	return "", false
}

// lookup restituisce il testo grezzo, senza segnaposto sostituiti.
func (c *catalog) lookup(lang, key string) (string, error) {
	source, ok := c.resolve(lang, key)
	if !ok {
		return "", fmt.Errorf("testo mancante: chiave %q assente sia in %q sia in %q",
			key, lang, DefaultLanguage)
	}
	return c.byLanguage[source][key], nil
}

// text traduce una chiave e ne sostituisce i segnaposto.
func (c *catalog) text(lang, key string, args []string) (string, error) {
	raw, err := c.lookup(lang, key)
	if err != nil {
		return "", err
	}
	out, err := interpolate(raw, args)
	if err != nil {
		return "", fmt.Errorf("chiave %q in %q: %w", key, lang, err)
	}
	return out, nil
}

// plural traduce la forma singolare o plurale di una chiave.
//
// La forma si sceglie con la regola della lingua che fornisce davvero il testo:
// se l'italiano non è ancora tradotto e si ricade sull'inglese, a decidere
// singolare e plurale dev'essere l'inglese, altrimenti si sceglie la forma con
// la regola di una lingua e si stampa la frase di un'altra.
func (c *catalog) plural(lang, key string, count int, args []string) (string, error) {
	source := DefaultLanguage
	if texts, ok := c.byLanguage[lang]; ok {
		if _, one := texts[key+"_one"]; one {
			source = lang
		} else if _, other := texts[key+"_other"]; other {
			source = lang
		}
	}

	form := key + "_" + pluralForm(source, count)
	raw, ok := c.byLanguage[source][form]
	if !ok {
		return "", fmt.Errorf("testo mancante: chiave %q assente in %q", form, source)
	}

	out, err := interpolate(raw, append([]string{"count", strconv.Itoa(count)}, args...))
	if err != nil {
		return "", fmt.Errorf("chiave %q in %q: %w", form, source, err)
	}
	return out, nil
}

// pluralForm applica la regola CLDR delle cinque lingue supportate.
//
// Quattro su cinque usano il singolare solo per l'uno. Il francese lo usa anche
// per lo zero: «0 tentative», non «0 tentatives».
func pluralForm(lang string, count int) string {
	if lang == "fr" {
		if count == 0 || count == 1 {
			return "one"
		}
		return "other"
	}
	if count == 1 {
		return "one"
	}
	return "other"
}

var placeholderPattern = regexp.MustCompile(`\{[a-z][a-z0-9_]*\}`)

// interpolate sostituisce i `{segnaposto}` con i valori passati a coppie
// nome/valore.
//
// Entrambe le asimmetrie sono errori, non tolleranze: un segnaposto rimasto nel
// testo finirebbe visibile nell'email, un argomento non usato significa che la
// traduzione ha perso per strada un dato che il mittente considerava
// necessario. Sono i due modi in cui una traduzione della issue #446 può
// rompersi in silenzio, e qui fanno fallire il rendering.
func interpolate(raw string, args []string) (string, error) {
	if len(args)%2 != 0 {
		return "", fmt.Errorf("argomenti dispari: attese coppie nome/valore, ricevuti %d valori", len(args))
	}

	values := make(map[string]string, len(args)/2)
	names := make([]string, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		if _, duplicate := values[args[i]]; duplicate {
			return "", fmt.Errorf("segnaposto %q passato due volte", args[i])
		}
		values[args[i]] = args[i+1]
		names = append(names, args[i])
	}

	// Una sola passata sul testo *sorgente*: i valori sostituiti non vengono
	// riesaminati. Un nome di job che contiene delle graffe è un dato, non un
	// segnaposto, e non deve poter innescare una seconda sostituzione.
	var unresolved []string
	used := make(map[string]bool, len(values))
	out := placeholderPattern.ReplaceAllStringFunc(raw, func(token string) string {
		name := token[1 : len(token)-1]
		value, ok := values[name]
		if !ok {
			unresolved = append(unresolved, token)
			return token
		}
		used[name] = true
		return value
	})

	if len(unresolved) > 0 {
		return "", fmt.Errorf("segnaposto senza valore: %s", strings.Join(unresolved, ", "))
	}

	var unused []string
	for _, name := range names {
		if !used[name] {
			unused = append(unused, name)
		}
	}
	if len(unused) > 0 {
		return "", fmt.Errorf("il testo non usa i segnaposto {%s}", strings.Join(unused, "}, {"))
	}

	return out, nil
}
