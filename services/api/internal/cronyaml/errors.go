package cronyaml

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// Position è un punto nel file, in coordinate 1-based come le conta un editor.
//
// Riga **e** colonna: la riga da sola non basta su una riga come
// `retries: { max: 3, backoff: exponenzial }`, dove ciò che va corretto è il
// terzo valore e non la riga.
type Position struct {
	Line   int
	Column int
}

// Valid indica una posizione utilizzabile. Una posizione non valida non deve
// finire in un errore: vedi il commento di [Error].
func (p Position) Valid() bool { return p.Line > 0 && p.Column > 0 }

func (p Position) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Column) }

// Error è un singolo motivo di rifiuto di un `cron.yaml`.
//
// # Perché tre informazioni e non una
//
// Questo file lo scrive una persona in un editor, e quello che riceve dopo un
// `git push` è tutto ciò che ha per correggerlo: non può mettere un breakpoint,
// non può stampare una variabile, non vede il nostro codice. Un «invalid yaml»
// è, per lei, indistinguibile da un guasto nostro.
//
// Quindi ogni errore dice tre cose, e ne manca una è un difetto:
//
//   - **dove**, con [Position] — riga e colonna, che è ciò che l'editor sa
//     usare;
//   - **quale campo**, con [Error.Path] — `jobs[1].request.url`, che è ciò che
//     un client può evidenziare senza interpretare il testo;
//   - **cosa scrivere invece**, dentro [Error.Message] — non la regola violata,
//     la correzione.
//
// L'ultimo punto è quello che si perde per primo, ed è quello che vale di più.
// «`every` non valido» dice a chi legge esattamente quello che già sapeva;
// «le durate si scrivono con l'unità: `10s`, `5m`, `1h`» gli dice cosa battere.
// TestOgniErroreDiceDoveEComeCorreggere in questo package rifiuta un messaggio
// che non contiene un rimedio.
type Error struct {
	Position

	// Path è il campo responsabile nella notazione che il file usa:
	// `version`, `defaults.timeout`, `jobs[0].request.headers.Authorization`.
	// L'indice fra parentesi è la posizione nella sequenza `jobs`, non il nome
	// del job: il nome può mancare o essere proprio ciò che non va.
	Path string

	// Code è il motivo in forma stabile, per i client che ci fanno branching.
	Code string

	// Message è il testo per una persona: cosa non va e cosa scrivere al suo
	// posto. È in italiano come il resto della diagnostica del prodotto.
	Message string

	// Limit è valorizzato quando il rifiuto viene da un limite di piano (R15,
	// SPEC §8) invece che dalla forma del file.
	//
	// Non è un dettaglio del messaggio: è la differenza fra «hai scritto una
	// cosa sbagliata» e «hai scritto una cosa che il tuo piano non comprende»,
	// e il secondo caso porta a una pagina di upgrade invece che a una
	// correzione del file. Vedi [jobs.PlanLimitError], che fa la stessa
	// distinzione dal lato dell'API.
	Limit jobs.LimitKind

	// Plan è il codice del piano che ha rifiutato, valorizzato con Limit.
	Plan string
}

func (e Error) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.Position, e.Path, e.Message)
}

// Codici di errore emessi da questo package.
//
// Sono stabili: un client può usarli per decidere cosa mostrare, e cambiarne
// uno è un cambiamento di contratto. I codici che arrivano da [jobs] e
// [secrets] passano invariati — è la stessa tassonomia dell'API, e due nomi
// diversi per lo stesso rifiuto costringerebbero il client a conoscerli
// entrambi.
const (
	// CodeSyntax è un file che YAML non riesce nemmeno a leggere.
	CodeSyntax = "yaml_syntax"
	// CodeEmpty è un file vuoto o senza contenuto.
	CodeEmpty = "empty_file"
	// CodeTooLarge è un file oltre [MaxFileSize].
	CodeTooLarge = "file_too_large"
	// CodeWrongKind è un valore del tipo sbagliato: una lista dove serve una
	// mappa, un numero dove serve testo.
	CodeWrongKind = "wrong_kind"
	// CodeUnknownKey è una chiave che lo schema non prevede.
	CodeUnknownKey = "unknown_key"
	// CodeDuplicateKey è una chiave ripetuta nella stessa mappa.
	CodeDuplicateKey = "duplicate_key"
	// CodeRequired è una chiave obbligatoria assente.
	CodeRequired = "required"
	// CodeUnsupportedVersion è un `version` che questo parser non conosce.
	CodeUnsupportedVersion = "unsupported_version"
	// CodeInvalidDuration è una durata illeggibile o non esprimibile in
	// secondi interi.
	CodeInvalidDuration = "invalid_duration"
	// CodeInvalidNumber è un intero illeggibile.
	CodeInvalidNumber = "invalid_number"
	// CodeDuplicateName sono due job con lo stesso `name` nello stesso file.
	CodeDuplicateName = "duplicate_name"
	// CodeTooManyJobs è un file oltre [MaxJobsPerFile].
	CodeTooManyJobs = "too_many_jobs"
	// CodeMergeKey è la chiave di fusione `<<`, non supportata.
	CodeMergeKey = "merge_key"
)

// ParseError è **tutto** ciò che non va nel file, non il primo problema
// incontrato.
//
// La ragione è nel ciclo di lavoro che questo file ha: si corregge, si fa
// commit, si fa push, si aspetta il webhook. Un errore per volta trasforma tre
// refusi in tre giri di quel ciclo, e ogni giro è nella cronologia del
// repository per sempre. Il parser prosegue quindi oltre il primo rifiuto
// ovunque possa farlo senza inventare: un job con la forma rotta viene
// diagnosticato per intero e non blocca i job successivi.
//
// L'unico punto in cui la raccolta si ferma è la sintassi YAML: se il documento
// non si legge non ci sono job da esaminare, e ogni errore successivo sarebbe
// una congettura su cosa l'utente intendeva scrivere.
type ParseError struct {
	// Source è il nome del file negli errori, `cron.yaml` se non specificato.
	Source string
	// Errors sono i motivi, ordinati per posizione nel file: è l'ordine in cui
	// una persona li incontra scorrendolo.
	Errors []Error
}

func (e *ParseError) Error() string {
	if len(e.Errors) == 0 {
		// Non dovrebbe accadere — orNil non restituisce mai un errore vuoto —
		// ma un Error() che mente è peggio di uno inutile.
		return e.Source + ": non valido"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%s: %s.", e.Source, countErrors(len(e.Errors)))
	for _, item := range e.Errors {
		fmt.Fprintf(&out, "\n  %s:%s: %s: %s", e.Source, item.Position, item.Path, item.Message)
	}
	return out.String()
}

// PlanLimited indica che almeno un rifiuto viene da un limite di piano. È ciò
// che distingue «correggi il file» da «ti serve un piano superiore».
func (e *ParseError) PlanLimited() bool {
	for _, item := range e.Errors {
		if item.Limit != "" {
			return true
		}
	}
	return false
}

// countErrors accorda il conteggio, perché «1 errori» in un messaggio di
// prodotto si nota.
func countErrors(n int) string {
	if n == 1 {
		return "1 errore"
	}
	return fmt.Sprintf("%d errori", n)
}

// AsParseError estrae un [*ParseError] dalla catena, se c'è.
func AsParseError(err error) (*ParseError, bool) {
	var parse *ParseError
	if errors.As(err, &parse) {
		return parse, true
	}
	return nil, false
}

// ---------------------------------------------------------------- accumulo

// errorList accumula i rifiuti durante la lettura del file.
type errorList struct {
	source string
	items  []Error
}

// add accoda un rifiuto.
//
// Una posizione non valida viene sostituita dall'inizio del file invece che
// scritta com'è: un `0:0` in un messaggio manda l'utente a cercare una riga che
// non esiste, e la promessa di questo package è che la posizione ci sia sempre.
func (l *errorList) add(pos Position, path, code, format string, args ...any) {
	if !pos.Valid() {
		pos = Position{Line: 1, Column: 1}
	}
	l.items = append(l.items, Error{
		Position: pos,
		Path:     path,
		Code:     code,
		Message:  fmt.Sprintf(format, args...),
	})
}

// addPlan accoda un rifiuto dovuto a un limite di piano, con il messaggio che
// il piano stesso produce e la correzione che riguarda il file.
func (l *errorList) addPlan(pos Position, path string, limit *jobs.PlanLimitError, remedy string) {
	message := capitalize(limit.Error())
	if remedy != "" {
		message += " " + remedy
	}
	l.addLimit(pos, path, limit, message)
}

// addLimit accoda un rifiuto dovuto a un limite di piano con un messaggio
// riscritto: serve al tetto sul numero di job, che [jobs.Plan] formula per una
// creazione singola («ne hai già 20») e che in un file va detto altrimenti.
func (l *errorList) addLimit(pos Position, path string, limit *jobs.PlanLimitError, message string) {
	if !pos.Valid() {
		pos = Position{Line: 1, Column: 1}
	}
	l.items = append(l.items, Error{
		Position: pos,
		Path:     path,
		Code:     string(limit.Limit),
		Message:  message,
		Limit:    limit.Limit,
		Plan:     limit.Plan,
	})
}

func (l *errorList) empty() bool { return len(l.items) == 0 }

// orNil restituisce l'errore complessivo, ordinato per posizione.
//
// L'ordinamento è stabile e per posizione perché l'elenco viene letto accanto
// al file: leggerlo nell'ordine in cui il parser ha lavorato — prima tutta la
// forma, poi tutta la semantica, poi i piani — costringerebbe a saltare avanti
// e indietro nel documento.
func (l *errorList) orNil() error {
	if l.empty() {
		return nil
	}
	items := make([]Error, len(l.items))
	copy(items, l.items)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Line != items[j].Line {
			return items[i].Line < items[j].Line
		}
		if items[i].Column != items[j].Column {
			return items[i].Column < items[j].Column
		}
		return items[i].Path < items[j].Path
	})
	return &ParseError{Source: l.source, Errors: items}
}

// capitalize alza la prima lettera: i messaggi di [jobs] e [secrets] nascono
// come frammenti di una frase più lunga («richiesta non valida: il nome è…»),
// qui invece ciascuno è una frase intera sotto la propria riga.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	// Solo se la prima parola non è già un identificatore o un letterale: in
	// «`every` non è ammesso» il backtick apre codice, e in «jobs[0] …» il nome
	// del campo va lasciato com'è.
	if runes[0] == '`' || runes[0] == '$' {
		return s
	}
	upper := strings.ToUpper(string(runes[0]))
	return upper + string(runes[1:])
}
