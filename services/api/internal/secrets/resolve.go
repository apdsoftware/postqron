package secrets

import (
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// # I riferimenti `${VAR}`
//
// Sono la sintassi di SPEC §9: dentro `url`, `headers` e `body` di un job,
// `${NOME}` viene sostituito con il valore del segreto omonimo del workspace.
// La sostituzione avviene **al momento dell'esecuzione**; a riposo, nel file e
// nella colonna `jobs.headers`, restano i riferimenti.
//
// Regole della sintassi, tutte e tre necessarie:
//
//   - `${NOME}` è un riferimento. Il nome ha la forma di [nameFormat], la stessa
//     che il `CHECK` della 0011 impone alla colonna: un riferimento che il
//     database non potrebbe mai soddisfare è un errore di battitura, e va detto
//     invece di essere trattato come testo.
//   - `$${` è un `${` letterale. Serve a chi manda a un altro sistema un corpo
//     che contiene a sua volta dei segnaposto: senza una via di fuga quel corpo
//     sarebbe inesprimibile per sempre.
//   - un `$` che non inizia nessuna delle due sequenze è testo. Un corpo JSON
//     con dentro `"prezzo": "$5.00"` non ha bisogno di sapere che esiste
//     un'espansione.
//
// # Espansione non ricorsiva
//
// Il valore di un segreto non viene riesaminato: se contiene a sua volta
// `${ALTRO}`, quei caratteri finiscono nella richiesta così come sono. È voluto.
// Un'espansione ricorsiva renderebbe il contenuto di un segreto capace di
// leggerne un altro, e trasformerebbe un valore scritto da chi ha accesso a un
// solo segreto in una leva per estrarne altri.

// nameFormat è la forma di un nome di segreto: la stessa del `CHECK` della 0011.
//
// Solo maiuscole, e non è pignoleria: se `${digest}` e `${DIGEST}` fossero due
// nomi possibili, un errore di battitura sarebbe indistinguibile dal
// riferimento a un secondo segreto che qualcuno potrebbe creare.
var nameFormat = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// ValidName indica se il nome è utilizzabile come segreto e come `${VAR}`.
func ValidName(name string) bool { return nameFormat.MatchString(name) }

// I campi in cui i riferimenti sono cercati, nella notazione a punti che l'API
// usa per ancorare gli errori ai campi (vedi FieldErrorBody in internal/httpapi).
const (
	fieldURL     = "request.url"
	fieldBody    = "request.body"
	fieldHeaders = "request.headers"
)

// Request è la parte di un job in cui i segreti possono comparire.
//
// È una struttura di questo package e non [scheduler.Job] perché i chiamanti
// sono tre e diversi — il parser di `cron.yaml` (#422), le rotte dei job, e
// l'esecutore HTTP (#390) — e nessuno dei tre deve dipendere dagli altri due per
// poter validare un riferimento.
type Request struct {
	URL     string
	Headers map[string]string
	Body    string
}

// Reference è un `${VAR}` trovato in una richiesta.
type Reference struct {
	// Name è il nome del segreto riferito.
	Name string
	// Field è il campo in cui compare, in notazione a punti: `request.url`,
	// `request.headers.Authorization`, `request.body`. È ciò che permette
	// all'errore di sync di dire *dove* guardare.
	Field string
}

// References elenca i riferimenti presenti in una richiesta, in ordine
// deterministico: prima l'URL, poi gli header per nome crescente, poi il corpo.
//
// L'ordine conta perché finisce nel messaggio d'errore che l'utente legge dopo
// un `git push`, e un elenco che cambia ordine a ogni esecuzione è un elenco che
// non si può confrontare con quello di prima.
//
// L'errore restituito è un [*ValidationError] e riguarda la **sintassi**: un
// `${` non chiuso, un nome che non ha la forma ammessa. Non dice niente su quali
// segreti esistano — quello è [NameSet.Validate].
func References(req Request) ([]Reference, error) {
	var refs []Reference
	invalid := &ValidationError{}

	collect := func(field, text string) {
		expand(text, field, invalid, func(name string) (string, bool) {
			refs = append(refs, Reference{Name: name, Field: field})
			return "", true
		})
	}

	collect(fieldURL, req.URL)
	for _, key := range sortedKeys(req.Headers) {
		// Il **nome** di una testata non viene espanso, e un riferimento scritto lì
		// è un errore invece che testo letterale. Le alternative sono peggiori
		// entrambe: lasciarlo com'è manderebbe al bersaglio una testata che si
		// chiama `${X}`, che nessuno ha scritto di proposito; espanderlo
		// permetterebbe a due testate diverse di diventare la stessa dopo
		// l'espansione, e la richiesta partirebbe con una delle due sparita in
		// silenzio. Un segreto è una credenziale: sta nel valore.
		if strings.Contains(key, "${") {
			invalid.add(headerField(key), "reference_in_header_name",
				"I riferimenti ai segreti si usano nel valore di una testata, non nel suo nome.")
		}
		collect(headerField(key), req.Headers[key])
	}
	collect(fieldBody, req.Body)

	return refs, invalid.orNil()
}

// Names sono i nomi distinti riferiti da una richiesta, in ordine alfabetico.
func Names(refs []Reference) []string {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		seen[ref.Name] = struct{}{}
	}
	return slices.Sorted(maps.Keys(seen))
}

// ----------------------------------------------------- validazione al sync

// NameSet è l'insieme dei nomi dei segreti vivi di un workspace.
//
// Esiste come tipo perché la validazione di `cron.yaml` è **una lettura e N
// job**: il parser (#422) prende l'insieme una volta e valida tutte le voci del
// file contro di lui. Con un metodo che interroga il database per ogni job, un
// file da cinquanta job sarebbe cinquanta letture per dire una cosa sola.
type NameSet struct {
	names map[string]struct{}
}

// NewNameSet costruisce l'insieme.
func NewNameSet(names []string) NameSet {
	set := NameSet{names: make(map[string]struct{}, len(names))}
	for _, name := range names {
		set.names[name] = struct{}{}
	}
	return set
}

// Has indica se il workspace ha un segreto vivo con quel nome.
func (s NameSet) Has(name string) bool {
	_, ok := s.names[name]
	return ok
}

// Names elenca i nomi, in ordine alfabetico.
func (s NameSet) Names() []string { return slices.Sorted(maps.Keys(s.names)) }

// Validate è **il cuore di R43**: dice se una richiesta è risolvibile, senza
// risolverla e senza decifrare niente.
//
// Il parser di `cron.yaml` la chiama prima di scrivere qualunque cosa, e un
// riferimento a un segreto inesistente diventa un errore di sync — quello che
// l'utente vede quando fa `git push`, con il campo che lo causa. La stessa
// verifica *non* fatta qui diventerebbe un'esecuzione fallita alle tre di notte,
// che è la stessa informazione consegnata nel momento in cui non serve più a
// niente.
//
// I motivi si accumulano tutti invece di fermarsi al primo: chi ha sbagliato a
// scrivere due variabili vuole correggerle in un push solo.
func (s NameSet) Validate(req Request) error {
	refs, err := References(req)
	invalid := &ValidationError{}
	if syntax, ok := AsValidation(err); ok {
		invalid.Fields = append(invalid.Fields, syntax.Fields...)
	}

	// Un nome mancante viene segnalato una volta per campo in cui compare, non
	// una per occorrenza: `${TOKEN}` scritto due volte nello stesso corpo è un
	// errore solo, e ripeterlo farebbe sembrare che ce ne siano due.
	reported := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if s.Has(ref.Name) {
			continue
		}
		key := ref.Field + "\x00" + ref.Name
		if _, done := reported[key]; done {
			continue
		}
		reported[key] = struct{}{}
		invalid.add(ref.Field, "unknown_secret", unknownSecretMessage(ref.Name, s))
	}
	return invalid.orNil()
}

// unknownSecretMessage spiega che cosa manca e che cosa c'è.
//
// L'elenco dei nomi disponibili è nel messaggio perché il caso normale è un
// errore di battitura o un segreto creato in un altro workspace, e in entrambi i
// casi la risposta è nell'elenco. Non rivela niente: sono nomi scelti
// dall'utente, del suo workspace, e li sta già leggendo in dashboard.
func unknownSecretMessage(name string, set NameSet) string {
	available := set.Names()
	if len(available) == 0 {
		return fmt.Sprintf(
			"Il segreto %q non esiste in questo workspace, che non ne ha ancora nessuno. "+
				"Crealo prima di riferirlo da `cron.yaml`.", name)
	}
	return fmt.Sprintf(
		"Il segreto %q non esiste in questo workspace. Disponibili: %s.",
		name, strings.Join(available, ", "))
}

// ------------------------------------------------- risoluzione all'esecuzione

// Resolved è una richiesta con i riferimenti espansi: **contiene segreti in
// chiaro**.
//
// I campi sono privati e si leggono con metodi che si chiamano come si chiamano.
// La ragione è la stessa di [Value]: una struttura con `URL string` esportato
// finisce in `slog.Any("request", resolved)` o in un `%+v` alla prima
// diagnostica, e con lei il token che l'espansione ha appena messo dentro
// all'URL.
//
// Ciò che si stampa è invece la richiesta **non risolta**: [Resolved.LogValue] e
// [Resolved.String] mostrano i `${VAR}` come stavano nel `cron.yaml`, che è
// esattamente ciò che serve a capire quale job ha fatto cosa.
type Resolved struct {
	template Request

	url     string
	headers map[string]string
	body    string

	redactor Redactor
}

// URL è l'URL con i riferimenti espansi.
func (r Resolved) URL() string { return r.url }

// Headers sono gli header con i riferimenti espansi, in copia: chi li modifica
// non modifica la risoluzione.
func (r Resolved) Headers() map[string]string { return maps.Clone(r.headers) }

// Body è il corpo con i riferimenti espansi.
func (r Resolved) Body() string { return r.body }

// Template è la richiesta come sta a riposo, con i `${VAR}` non espansi. È la
// forma da usare in qualunque messaggio destinato a un umano.
func (r Resolved) Template() Request { return r.template }

// Redactor è ciò che serve a chi esegue per scrivere l'estratto della risposta
// senza scriverci dentro un segreto. Vedi [Redactor].
func (r Resolved) Redactor() Redactor { return r.redactor }

// String è la forma leggibile: l'URL **non risolto**.
func (r Resolved) String() string { return r.template.URL }

// LogValue è la forma con cui la richiesta risolta compare nei log: quella non
// risolta, più il conteggio dei segreti che sono stati espansi.
//
// Degli header compaiono i nomi e non i valori. I nomi servono a capire che cosa
// è stato mandato; i valori sono, in questo prodotto, il posto più probabile in
// cui un segreto si trovi.
func (r Resolved) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("url", r.template.URL),
		slog.Any("header_names", sortedKeys(r.template.Headers)),
		slog.Int("body_length", len(r.template.Body)),
		slog.Int("secrets_resolved", r.redactor.Len()),
	)
}

// ------------------------------------------------------------------ redazione

// Redactor sostituisce i valori dei segreti con i loro riferimenti dentro a un
// testo qualsiasi.
//
// # A che serve
//
// L'esecutore HTTP (#390) conserva un estratto della risposta e il testo
// dell'errore, e li mostra all'utente nel registro delle esecuzioni (R6). Sono
// due testi che **non controlliamo**: il bersaglio può riflettere nella propria
// risposta la testata che gli abbiamo mandato — molte API lo fanno nei messaggi
// d'errore, «token XYZ non valido» — e l'errore di rete di Go contiene l'URL
// completo della richiesta, cioè, se il segreto stava in querystring, il segreto.
//
// Senza una redazione, R43 sarebbe rispettata da noi e violata dal bersaglio.
//
// # Perché restituisce un tipo e non una stringa
//
// [Redactor.Excerpt] e [Redactor.ErrorText] restituiscono [Excerpt], un tipo che
// **non è costruibile fuori da qui**. Un campo dichiarato `Excerpt` può quindi
// contenere solo testo che è passato dalla redazione: non è una regola da
// ricordare in revisione, è l'unico modo di ottenere il valore. È la stessa
// scelta di [Secret], che il valore non ce l'ha.
//
// Il valore zero è utilizzabile e non redige niente: serve a chi deve produrre
// un estratto per un'esecuzione che non aveva segreti.
type Redactor struct {
	replacer *strings.Replacer
	count    int
}

// Len è il numero di segreti che il redattore conosce.
func (r Redactor) Len() int { return r.count }

// newRedactor costruisce il redattore a partire dai valori risolti.
func newRedactor(values map[string]Value) Redactor {
	// I valori si ordinano per lunghezza decrescente: se un segreto è prefisso di
	// un altro, va sostituito prima il più lungo, altrimenti resterebbe visibile
	// la sua coda.
	names := slices.Sorted(maps.Keys(values))
	sort.SliceStable(names, func(i, j int) bool {
		return values[names[i]].Len() > values[names[j]].Len()
	})

	pairs := make([]string, 0, len(names)*2)
	for _, name := range names {
		value := values[name]
		// Un valore troppo corto non entra nel redattore: cercarlo dentro a un
		// testo qualsiasi produrrebbe più falsi positivi che redazioni, e
		// riscriverebbe risposte che non contengono nessun segreto. La creazione
		// impone [MinValueLength] proprio per non arrivare mai qui.
		if value.Len() < MinValueLength {
			continue
		}
		pairs = append(pairs, value.Reveal(), "${"+name+"}")
	}
	if len(pairs) == 0 {
		return Redactor{}
	}
	return Redactor{replacer: strings.NewReplacer(pairs...), count: len(pairs) / 2}
}

// Redact sostituisce ogni occorrenza di un valore con il suo `${NOME}`.
//
// Che cosa **non** copre, e va saputo: un valore che il bersaglio restituisce
// trasformato — codificato in percentuale dentro un URL, in base64 dentro un
// JSON, troncato a metà — non viene riconosciuto. La redazione è l'ultima
// difesa, non la prima: la prima è che il segreto non finisca in un log nostro,
// e quella è garantita dai tipi.
func (r Redactor) Redact(s string) string {
	if r.replacer == nil {
		return s
	}
	return r.replacer.Replace(s)
}

// Excerpt è un testo che è passato dalla redazione.
//
// Non è costruibile fuori da questo package: il campo è privato e le uniche
// funzioni che lo valorizzano sono [Redactor.Excerpt] e [Redactor.ErrorText].
// Il valore zero è la stringa vuota, che è il testo più redatto che ci sia.
//
// È il tipo che l'esecutore HTTP (#390) deve usare per l'estratto della risposta
// e per il testo dell'errore. Un `string` al suo posto tornerebbe a essere un
// campo in cui *si può* mettere il testo redatto, e la differenza fra «si può» e
// «si deve» è tutta la issue.
type Excerpt struct {
	text string
}

// String è il testo redatto, pronto per essere scritto su `job_executions`.
func (e Excerpt) String() string { return e.text }

// Empty indica un estratto vuoto.
func (e Excerpt) Empty() bool { return e.text == "" }

// Excerpt redige la risposta e la tronca a `limit` caratteri (rune, non byte:
// troncare a metà una sequenza UTF-8 produrrebbe un testo che PostgreSQL
// rifiuta). Un limite non positivo non tronca.
//
// La redazione avviene **prima** del troncamento, non dopo: al contrario, un
// valore a cavallo del taglio resterebbe visibile a metà.
func (r Redactor) Excerpt(raw []byte, limit int) Excerpt {
	return Excerpt{text: truncate(r.Redact(string(raw)), limit)}
}

// ErrorText redige il testo di un errore e lo tronca.
//
// Esiste separata da [Redactor.Excerpt] perché il testo dell'errore è la via di
// fuga meno evidente delle due: `(*http.Client).Do` restituisce un errore che
// contiene l'URL completo della richiesta, quindi un segreto messo in
// querystring finirebbe nel registro delle esecuzioni **anche se la risposta non
// è mai arrivata**.
//
// Un errore nil produce un estratto vuoto.
func (r Redactor) ErrorText(err error, limit int) Excerpt {
	if err == nil {
		return Excerpt{}
	}
	return Excerpt{text: truncate(r.Redact(err.Error()), limit)}
}

// truncate taglia a `limit` rune.
func truncate(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	count := 0
	for index := range s {
		if count == limit {
			return s[:index]
		}
		count++
	}
	return s
}

// -------------------------------------------------------------- espansione

// expand percorre un testo risolvendo i riferimenti con `lookup`.
//
// È **una sola funzione** per la ricerca dei riferimenti e per la loro
// sostituzione, e non due che fanno la stessa analisi. È deliberato: se la
// validazione al sync e l'espansione all'esecuzione fossero due parser distinti,
// la promessa di R43 — «ciò che passa la validazione non fallisce di notte» —
// dipenderebbe dal fatto che i due restino d'accordo per sempre. Qui non possono
// non esserlo.
//
// `lookup` restituisce il valore da sostituire e se il nome è noto; il secondo
// valore falso lascia il riferimento intatto e non è un errore di sintassi —
// «questo segreto non esiste» è una verifica di [NameSet.Validate], non di
// questa funzione.
func expand(s, field string, invalid *ValidationError, lookup func(name string) (string, bool)) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		offset := strings.Index(s[i:], "${")
		if offset < 0 {
			out.WriteString(s[i:])
			break
		}
		start := i + offset

		// `$${` è un `${` letterale: si scrive il testo che precede il primo
		// dollaro, poi la sequenza, e si riparte dopo la graffa aperta.
		if start > i && s[start-1] == '$' {
			out.WriteString(s[i : start-1])
			out.WriteString("${")
			i = start + 2
			continue
		}

		out.WriteString(s[i:start])

		end := strings.IndexByte(s[start:], '}')
		if end < 0 {
			invalid.add(field, "unterminated_reference",
				"C'è un `${` che non viene mai chiuso da `}`. Se volevi scrivere `${` come testo, raddoppia il dollaro: `$${`.")
			out.WriteString(s[start:])
			break
		}

		name := s[start+2 : start+end]
		if !ValidName(name) {
			invalid.add(field, "invalid_reference", invalidReferenceMessage(name))
			// Il riferimento resta com'è: non c'è niente con cui sostituirlo, e
			// scrivere una stringa vuota al suo posto trasformerebbe un errore di
			// battitura in una richiesta partita senza credenziale.
			out.WriteString(s[start : start+end+1])
			i = start + end + 1
			continue
		}

		if value, known := lookup(name); known {
			out.WriteString(value)
		} else {
			out.WriteString(s[start : start+end+1])
		}
		i = start + end + 1
	}
	return out.String()
}

// invalidReferenceMessage spiega perché un nome non è ammesso.
func invalidReferenceMessage(name string) string {
	switch {
	case name == "":
		return "`${}` non riferisce nessun segreto: fra le graffe ci va il nome."
	case strings.ToUpper(name) != name && ValidName(strings.ToUpper(name)):
		// Il caso di gran lunga più frequente, e l'unico in cui si può indicare la
		// correzione esatta invece della regola.
		return fmt.Sprintf("I nomi dei segreti sono in maiuscolo: scrivi `${%s}` invece di `${%s}`.",
			strings.ToUpper(name), name)
	case len(name) > MaxNameLength:
		return fmt.Sprintf("Il nome del segreto non può superare %d caratteri.", MaxNameLength)
	default:
		return fmt.Sprintf(
			"`${%s}` non è un riferimento valido: i nomi cominciano con una lettera maiuscola "+
				"e proseguono con maiuscole, cifre e underscore.", name)
	}
}

// ------------------------------------------------------------------ supporto

// headerField è il percorso di un header nella notazione a punti.
func headerField(key string) string { return fieldHeaders + "." + key }

// sortedKeys restituisce le chiavi di una mappa in ordine alfabetico: senza,
// l'ordine degli errori e quello dei log dipenderebbero dall'iterazione casuale
// delle mappe di Go.
func sortedKeys(m map[string]string) []string { return slices.Sorted(maps.Keys(m)) }
