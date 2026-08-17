package cronyaml

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Questo file è l'unico punto che tocca `yaml.Node`: sopra di lui il parser
// lavora su valori Go e su [Position], e non deve sapere che esiste un albero.
//
// La struttura ad albero si legge a mano invece di decodificarla in una `struct`
// con i tag `yaml:"..."`. Non è una preferenza di stile: la decodifica in
// struttura **perde la posizione**. `yaml.Unmarshal` in una struct sa dire «riga
// 12» solo per gli errori di tipo che genera lui, e non sa dire niente per un
// campo che ha letto benissimo e che è sbagliato per una ragione nostra — un
// `every` che il piano non consente, un `${VAR}` che non esiste. Quelli sono la
// maggioranza degli errori che l'utente vede, e sono esattamente quelli per cui
// serve la riga.

// at è la posizione di un nodo, nelle coordinate 1-based di yaml.v3, che sono
// già quelle di un editor.
func at(node *yaml.Node) Position {
	if node == nil {
		return Position{}
	}
	return Position{Line: node.Line, Column: node.Column}
}

// deref segue un alias YAML (`*nome`) fino al nodo ancorato.
//
// Gli alias si risolvono perché un `cron.yaml` con venti job che condividono
// header o retry è la ragione per cui esistono, e rifiutarli costringerebbe a
// ripetere lo stesso blocco venti volte — cioè a rendere il file peggiore di
// quello che sostituisce. La **posizione** resta però quella del punto in cui
// l'alias è usato, non quella dell'ancora: chi legge l'errore deve andare dove
// il valore è finito, non dove è stato scritto.
//
// yaml.v3 limita da sé l'espansione degli alias, quindi un file costruito per
// esplodere in memoria («billion laughs») viene fermato dal decodificatore
// prima di arrivare qui.
func deref(node *yaml.Node) *yaml.Node {
	if node == nil || node.Kind != yaml.AliasNode {
		return node
	}
	return relocate(node.Alias, at(node), 0)
}

// relocate copia un sottoalbero portando ogni nodo alla posizione indicata.
//
// Serve alla regola descritta in [deref]: il valore viene dall'ancora, l'errore
// va mostrato dove l'alias è usato. Senza la copia, due job che condividono la
// stessa ancora riceverebbero due errori che puntano entrambi dentro il primo,
// e il secondo job — quello che l'utente sta leggendo — non comparirebbe da
// nessuna parte se non nel nome del campo.
//
// Il limite di profondità è la difesa contro un'ancora che riferisce se stessa:
// la copia è ricorsiva e senza un tetto seguirebbe il ciclo fino a esaurire lo
// stack.
func relocate(node *yaml.Node, site Position, depth int) *yaml.Node {
	if node == nil || depth > 64 {
		return node
	}
	if node.Kind == yaml.AliasNode {
		return relocate(node.Alias, site, depth+1)
	}
	clone := *node
	clone.Line, clone.Column = site.Line, site.Column
	if len(node.Content) > 0 {
		clone.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			clone.Content[i] = relocate(child, site, depth+1)
		}
	}
	return &clone
}

// kindOf descrive il tipo di un nodo come lo direbbe una persona, per i
// messaggi che spiegano che cosa è stato trovato al posto di cosa.
func kindOf(node *yaml.Node) string {
	node = deref(node)
	if node == nil {
		return "niente"
	}
	switch node.Kind {
	case yaml.MappingNode:
		return "una mappa"
	case yaml.SequenceNode:
		return "una lista"
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return "un valore vuoto"
		case "!!int", "!!float":
			return "un numero"
		case "!!bool":
			return "un booleano"
		default:
			return "un testo"
		}
	default:
		return "un valore"
	}
}

// isNull indica un valore assente scritto esplicitamente (`timeout:` senza
// niente dopo, oppure `~`, oppure `null`).
func isNull(node *yaml.Node) bool {
	node = deref(node)
	return node == nil || (node.Kind == yaml.ScalarNode && node.Tag == "!!null")
}

// ------------------------------------------------------------------- mappe

// mapping è una mappa YAML letta con le sue posizioni.
//
// Tiene traccia delle chiavi consumate perché «rifiuta ciò che non conosci»
// (SPEC §9, la regola di `version`) si applica anche alle chiavi: ciò che resta
// non consumato dopo la lettura è, per definizione, ciò che lo schema non
// prevede.
type mapping struct {
	node *yaml.Node
	// keys sono le chiavi nell'ordine in cui compaiono nel file.
	keys   []string
	values map[string]*yaml.Node
	keyPos map[string]Position
	used   map[string]bool
}

// pos è la posizione del valore di una chiave, o della chiave stessa quando il
// valore non ha una posizione propria.
func (m *mapping) pos(key string) Position {
	if value, ok := m.values[key]; ok {
		if p := at(value); p.Valid() {
			return p
		}
	}
	if p, ok := m.keyPos[key]; ok {
		return p
	}
	return at(m.node)
}

// value restituisce il valore di una chiave e la segna come letta.
func (m *mapping) value(key string) (*yaml.Node, bool) {
	m.used[key] = true
	node, ok := m.values[key]
	if !ok || isNull(node) {
		// Una chiave scritta senza valore (`timeout:`) è trattata come assente:
		// è quasi sempre una riga lasciata a metà, e il messaggio giusto è
		// quello del campo obbligatorio mancante, non un errore di tipo.
		return nil, false
	}
	return deref(node), true
}

// unread sono le chiavi che nessuno ha letto, nell'ordine del file.
func (m *mapping) unread() []string {
	var rest []string
	for _, key := range m.keys {
		if !m.used[key] {
			rest = append(rest, key)
		}
	}
	return rest
}

// ------------------------------------------------------------------ lettore

// reader legge l'albero accumulando i rifiuti invece di fermarsi al primo.
type reader struct{ errs *errorList }

// mapping legge un nodo come mappa, segnalando le chiavi ripetute e la chiave
// di fusione.
func (r *reader) mapping(node *yaml.Node, path, hint string) (*mapping, bool) {
	node = deref(node)
	if node == nil || node.Kind != yaml.MappingNode {
		r.wrongKind(node, path, "una mappa", hint)
		return nil, false
	}

	out := &mapping{
		node:   node,
		values: make(map[string]*yaml.Node, len(node.Content)/2),
		keyPos: make(map[string]Position, len(node.Content)/2),
		used:   make(map[string]bool, len(node.Content)/2),
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valueNode := node.Content[i], node.Content[i+1]
		key := keyNode.Value

		if key == "<<" {
			// La chiave di fusione copierebbe dentro questa mappa il contenuto
			// di un'altra. Non è supportata, e il motivo è che il file è la
			// fonte di verità di ciò che verrà eseguito: una regola che decide
			// silenziosamente quale valore vince fra quello ereditato e quello
			// scritto qui sotto renderebbe il contenuto effettivo di un job
			// diverso da quello che si legge nel suo blocco. `defaults` copre
			// il caso che serve davvero, e lo fa in modo visibile.
			r.errs.add(at(keyNode), path, CodeMergeKey,
				"La chiave di fusione `<<` non è supportata in `cron.yaml`. "+
					"Usa `defaults` per i valori comuni a tutti i job, e scrivi negli altri campi ciò che deve valere per questo job.")
			out.used[key] = true
			continue
		}

		if _, duplicate := out.values[key]; duplicate {
			// YAML non vieta la ripetizione, e i decodificatori in genere
			// tengono l'ultimo valore in silenzio. Qui è un rifiuto: due
			// `schedule` nello stesso job sono due orari, e sceglierne uno
			// senza dirlo significa eseguire il job a un'ora che l'utente non
			// ha mai letto nel proprio file.
			r.errs.add(at(keyNode), joinPath(path, key), CodeDuplicateKey,
				"La chiave `%s` è già stata dichiarata alla riga %d: togline una. Fra le due non c'è un modo giusto di scegliere, e tenere l'ultima in silenzio — che è ciò che fanno quasi tutti i lettori YAML — significherebbe eseguire il job con un valore che non hai mai letto nel tuo file.",
				key, out.keyPos[key].Line)
			continue
		}

		out.keys = append(out.keys, key)
		out.values[key] = valueNode
		out.keyPos[key] = at(keyNode)
	}
	return out, true
}

// rejectUnknown segnala le chiavi che lo schema non prevede.
//
// È l'altra metà della promessa di `version` (SPEC §9): il numero di versione
// serve a poter cambiare lo schema fra un anno senza rompere i file esistenti,
// e questo funziona solo se un file che dichiara `version: 1` viene letto con
// le regole della versione 1 — comprese quelle su ciò che non esiste. Accettare
// in silenzio una chiave sconosciuta significa che il giorno in cui quella
// chiave verrà introdotta, i file che la contenevano per errore cambieranno
// comportamento da soli.
func (r *reader) rejectUnknown(m *mapping, path string, known []string) {
	for _, key := range m.unread() {
		message := fmt.Sprintf("La chiave `%s` non esiste nello schema di `cron.yaml`.", key)
		if best, ok := nearest(key, known); ok {
			message += fmt.Sprintf(" Forse intendevi `%s`.", best)
		} else {
			message += fmt.Sprintf(" Qui sono ammesse: %s.", quoteJoin(known))
		}
		r.errs.add(m.keyPos[key], joinPath(path, key), CodeUnknownKey, "%s", message)
	}
}

// ---------------------------------------------------------------- scalari

// wrongKind segnala un valore del tipo sbagliato.
func (r *reader) wrongKind(node *yaml.Node, path, want, hint string) {
	message := fmt.Sprintf("%s dev'essere %s, non %s.", label(path), want, kindOf(node))
	if hint != "" {
		message += " " + hint
	}
	r.errs.add(at(node), path, CodeWrongKind, "%s", message)
}

// label nomina un campo in un messaggio. Il percorso vuoto è la radice del
// documento, che non ha un nome da scrivere fra backtick.
func label(path string) string {
	if path == "" {
		return "Il contenuto del file"
	}
	return "`" + path + "`"
}

// text legge uno scalare come testo.
//
// Uno scalare non testuale viene accettato nella sua forma letterale — `3`
// diventa `"3"` — perché in un header o in un corpo è quasi sempre ciò che si
// intendeva, e rifiutarlo costringerebbe a mettere fra apici ogni numero. Le
// mappe e le liste no: lì la differenza è strutturale e va detta.
func (r *reader) text(node *yaml.Node, path, hint string) (string, bool) {
	node = deref(node)
	if node == nil || node.Kind != yaml.ScalarNode {
		r.wrongKind(node, path, "un testo", hint)
		return "", false
	}
	return node.Value, true
}

// integer legge uno scalare come intero.
func (r *reader) integer(node *yaml.Node, path, hint string) (int, bool) {
	node = deref(node)
	if node == nil || node.Kind != yaml.ScalarNode {
		r.wrongKind(node, path, "un numero intero", hint)
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(node.Value))
	if err != nil {
		message := fmt.Sprintf("`%s: %s` non è un numero intero.", path, node.Value)
		if hint != "" {
			message += " " + hint
		}
		r.errs.add(at(node), path, CodeInvalidNumber, "%s", message)
		return 0, false
	}
	return value, true
}

// duration legge una durata nella forma di SPEC §9: `1s`, `10s`, `5m`, `1h`.
//
// # Perché l'unità è obbligatoria
//
// `timeout: 30` è ambiguo per chi lo legge — trenta secondi o trenta minuti? —
// e il costo di sbagliare non è simmetrico: un timeout inteso in minuti e
// applicato in secondi tronca ogni esecuzione, e il sintomo (job che fallisce
// per timeout) non assomiglia alla causa (unità mancante). L'unità costa un
// carattere e toglie la domanda.
//
// # Perché solo secondi interi
//
// È il vincolo delle colonne: `timeout_seconds` e `every_seconds` della 0005
// sono `integer`. Accettare `1500ms` qui significherebbe scriverne 1 o 2 nel
// database, cioè eseguire qualcosa di diverso da quello che il file dice.
func (r *reader) duration(node *yaml.Node, path, hint string) (time.Duration, bool) {
	node = deref(node)
	if node == nil || node.Kind != yaml.ScalarNode {
		r.wrongKind(node, path, "una durata", hint)
		return 0, false
	}
	raw := strings.TrimSpace(node.Value)

	reject := func(format string, args ...any) (time.Duration, bool) {
		r.errs.add(at(node), path, CodeInvalidDuration, format, args...)
		return 0, false
	}

	if raw == "" {
		return reject("`%s` è vuoto: una durata si scrive con l'unità, per esempio `30s`, `5m` oppure `1h`.", path)
	}
	// Un numero senza unità è il caso di gran lunga più frequente, e ha una
	// correzione esatta da suggerire invece della regola generale.
	if _, err := strconv.Atoi(raw); err == nil {
		return reject("`%s: %s` non ha l'unità di misura: scrivi `%s: %ss` se intendevi secondi, `%s: %sm` se intendevi minuti.",
			path, raw, path, raw, path, raw)
	}

	value, err := time.ParseDuration(raw)
	if err != nil {
		return reject("`%s: %s` non è una durata leggibile: usa un numero seguito da `s`, `m` oppure `h`, per esempio `30s`, `5m`, `1h`.", path, raw)
	}
	switch {
	case value < 0:
		return reject("`%s: %s` è negativo: una durata va da `1s` in su.", path, raw)
	case value == 0:
		return reject("`%s: %s` è zero: una durata va da `1s` in su.", path, raw)
	case value%time.Second != 0:
		return reject("`%s: %s` non è un numero intero di secondi: la risoluzione più fine è il secondo, scrivi per esempio `%s`.",
			path, raw, jobs.FormatDuration(roundUpToSecond(value)))
	}
	return value, true
}

// roundUpToSecond è il valore intero più vicino verso l'alto, per suggerire una
// correzione concreta a chi ha scritto una durata con i millisecondi.
func roundUpToSecond(d time.Duration) time.Duration {
	rounded := d.Truncate(time.Second)
	if rounded < d {
		rounded += time.Second
	}
	if rounded == 0 {
		rounded = time.Second
	}
	return rounded
}

// sequence legge una lista di testi.
//
// Le voci non restituiscono la propria posizione, e la ragione è che nessuno
// la userebbe: gli unici elenchi dello schema sono `environments` e
// `alerts.on_failure`, e [jobs.Job.Validate] riporta i loro errori al campo
// nel suo insieme. Restituirla comunque prometterebbe una precisione che i
// messaggi non hanno.
func (r *reader) sequence(node *yaml.Node, path, hint string) ([]string, bool) {
	node = deref(node)
	if node == nil || node.Kind != yaml.SequenceNode {
		r.wrongKind(node, path, "una lista", hint)
		return nil, false
	}
	values := make([]string, 0, len(node.Content))
	for i, item := range node.Content {
		text, ok := r.text(item, fmt.Sprintf("%s[%d]", path, i), hint)
		if !ok {
			continue
		}
		values = append(values, text)
	}
	return values, true
}

// textMap legge una mappa di testi, con la posizione di ogni valore: è la forma
// di `request.headers`, ed è l'unico posto in cui le chiavi non sono note in
// anticipo.
func (r *reader) textMap(node *yaml.Node, path, hint string) (map[string]string, map[string]Position, bool) {
	m, ok := r.mapping(node, path, hint)
	if !ok {
		return nil, nil, false
	}
	values := make(map[string]string, len(m.keys))
	positions := make(map[string]Position, len(m.keys))
	for _, key := range m.keys {
		child, present := m.value(key)
		if !present {
			// Un header senza valore è una testata vuota, che è legittima.
			values[key] = ""
			positions[key] = m.keyPos[key]
			continue
		}
		text, ok := r.text(child, joinPath(path, key), hint)
		if !ok {
			continue
		}
		values[key] = text
		positions[key] = at(child)
	}
	return values, positions, true
}

// ---------------------------------------------------------------- supporto

// joinPath compone il percorso di un campo nella notazione del file.
func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// quoteJoin elenca dei nomi fra backtick.
func quoteJoin(values []string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, "`"+value+"`")
	}
	return strings.Join(parts, ", ")
}

// nearest è il candidato più simile a una chiave sconosciuta, se ce n'è uno
// abbastanza vicino da poterlo suggerire senza indovinare.
//
// La soglia è due modifiche, e non di più: oltre, il suggerimento smette di
// essere una correzione e diventa una supposizione — e un suggerimento sbagliato
// costa più del silenzio, perché manda a provare qualcosa che non funzionerà.
func nearest(unknown string, known []string) (string, bool) {
	target := strings.ToLower(unknown)
	best, bestDistance := "", 3
	for _, candidate := range known {
		distance := editDistance(target, strings.ToLower(candidate))
		if distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	return best, best != ""
}

// editDistance è la distanza di Levenshtein fra due stringhe.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	previous := make([]int, len(br)+1)
	current := make([]int, len(br)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		current[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(br)]
}
