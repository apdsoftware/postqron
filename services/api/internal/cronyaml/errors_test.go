package cronyaml_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/cronyaml"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
)

// I test di questo file riguardano **i messaggi**, non il rifiuto.
//
// Provare che un file sbagliato viene rifiutato è la metà facile, ed è anche la
// metà che non contiene il valore: un parser che rifiuta tutto la supera. La
// metà che conta è che chi ha sbagliato riesca a correggere, e quella si prova
// solo guardando cosa gli arriva scritto.

// ------------------------------------------- l'invariante su ogni messaggio

// broken è il campionario dei modi in cui un `cron.yaml` può essere sbagliato.
//
// Non serve a verificare i singoli casi — quelli hanno i loro test qui sotto —
// ma a passare **ogni errore che il parser sa produrre** attraverso
// l'invariante di [TestOgniErroreDiceDoveEComeCorreggere]. Aggiungere qui un
// caso nuovo è il modo di accorgersi che un messaggio nuovo non è all'altezza
// degli altri.
var broken = []struct {
	nome    string
	source  string
	options cronyaml.Options
}{
	{"file vuoto", "\n   \n", options()},
	{"solo commenti", "# niente\n", options()},
	{"radice non è una mappa", "- uno\n- due\n", options()},
	{"più documenti", "version: 1\njobs: []\n---\nversion: 1\njobs: []\n", options()},
	{"tabulazione", "version: 1\njobs:\n\t- name: uno\n", options()},
	{"indentazione sbagliata", "version: 1\njobs:\n  - name: uno\n     every: 1m\n", options()},
	{"due punti nel valore", "version: 1\njobs:\n  - name: uno\n    body: {\"a\":1}: no\n", options()},
	{"virgoletta non chiusa", "version: 1\njobs:\n  - name: \"uno\n", options()},

	{"version assente", "jobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n", options()},
	{"version futura", "version: 7\njobs: []\n", options()},
	{"version illeggibile", "version: uno\njobs: []\n", options()},

	{"jobs assente", "version: 1\n", options()},
	{"jobs non è una lista", "version: 1\njobs:\n  uno: 1\n", options()},
	{"job non è una mappa", "version: 1\njobs:\n  - uno\n", options()},

	{"chiave sconosciuta simile", "version: 1\njobs:\n  - name: uno\n    schedul: \"0 9 * * *\"\n    request: { url: https://esempio.it/x }\n", options()},
	{"chiave sconosciuta lontana", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    quantita: 3\n    request: { url: https://esempio.it/x }\n", options()},
	{"chiave ripetuta", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    every: 2m\n    request: { url: https://esempio.it/x }\n", options()},
	{"chiave di fusione", "version: 1\njobs:\n  - name: uno\n    every: &o 1m\n  - name: due\n    <<: *o\n", options()},
	{"defaults sconosciuto", "version: 1\ndefaults:\n  timezon: UTC\njobs: []\n", options()},
	{"defaults non è una mappa", "version: 1\ndefaults: [uno]\njobs: []\n", options()},

	{"nessuna modalità", "version: 1\njobs:\n  - name: uno\n    request: { url: https://esempio.it/x }\n", options()},
	{"entrambe le modalità", "version: 1\njobs:\n  - name: uno\n    schedule: \"0 9 * * *\"\n    every: 1m\n    request: { url: https://esempio.it/x }\n", options()},
	{"cron malformato", "version: 1\njobs:\n  - name: uno\n    schedule: \"0 9 *\"\n    request: { url: https://esempio.it/x }\n", options()},
	{"fuso sconosciuto", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    timezone: Europa/Roma\n    request: { url: https://esempio.it/x }\n", options()},
	{"fuso Local", "version: 1\ndefaults:\n  timezone: Local\njobs:\n  - name: uno\n    every: 1m\n    request: { url: https://esempio.it/x }\n", options()},

	{"durata senza unità", "version: 1\njobs:\n  - name: uno\n    every: 30\n    request: { url: https://esempio.it/x }\n", options()},
	{"durata illeggibile", "version: 1\njobs:\n  - name: uno\n    every: 1x\n    request: { url: https://esempio.it/x }\n", options()},
	{"durata sotto il secondo", "version: 1\njobs:\n  - name: uno\n    every: 500ms\n    request: { url: https://esempio.it/x }\n", options()},
	{"durata zero", "version: 1\njobs:\n  - name: uno\n    every: 0s\n    request: { url: https://esempio.it/x }\n", options()},
	{"durata negativa", "version: 1\njobs:\n  - name: uno\n    every: -5s\n    request: { url: https://esempio.it/x }\n", options()},
	{"durata è una mappa", "version: 1\njobs:\n  - name: uno\n    every: { min: 1 }\n    request: { url: https://esempio.it/x }\n", options()},
	{"timeout fuori intervallo", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    timeout: 900s\n    request: { url: https://esempio.it/x }\n", options()},

	{"nome assente", "version: 1\njobs:\n  - every: 1m\n    request: { url: https://esempio.it/x }\n", options()},
	{"nome con spazi", "version: 1\njobs:\n  - name: il mio job\n    every: 1m\n    request: { url: https://esempio.it/x }\n", options()},
	{"nome troppo lungo", "version: 1\njobs:\n  - name: " + strings.Repeat("a", 101) + "\n    every: 1m\n    request: { url: https://esempio.it/x }\n", options()},
	{"nome duplicato", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request: { url: https://esempio.it/x }\n  - name: uno\n    every: 2m\n    request: { url: https://esempio.it/y }\n", options()},
	{"nome non testuale", "version: 1\njobs:\n  - name: [uno]\n    every: 1m\n    request: { url: https://esempio.it/x }\n", options()},

	{"request assente", "version: 1\njobs:\n  - name: uno\n    every: 1m\n", options()},
	{"request non è una mappa", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request: https://esempio.it/x\n", options()},
	{"url assente", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request: { method: GET }\n", options()},
	{"url senza schema", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request: { url: esempio.it/x }\n", options()},
	{"url con schema vietato", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request: { url: \"ftp://esempio.it/x\" }\n", options()},
	{"metodo sconosciuto", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request: { url: https://esempio.it/x, method: FETCH }\n", options()},
	{"header riservato", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      headers: { Host: altro.it }\n", options()},
	{"nome di header non valido", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      headers: { \"X Api Key\": v }\n", options()},
	{"header con a capo", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      headers: { X-Api-Key: \"a\\nb\" }\n", options()},
	{"headers non è una mappa", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      headers: [uno]\n", options()},
	{"corpo non testuale", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      body: { kind: daily }\n", options()},

	{"ambienti vuoti", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    environments: []\n    request: { url: https://esempio.it/x }\n", options()},
	{"ambiente sconosciuto", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    environments: [collaudo]\n    request: { url: https://esempio.it/x }\n", options()},
	{"ambiente ripetuto", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    environments: [production, production]\n    request: { url: https://esempio.it/x }\n", options()},
	{"ambienti non è una lista", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    environments: production\n    request: { url: https://esempio.it/x }\n", options()},

	{"retries fuori intervallo", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    retries: { max: 99 }\n    request: { url: https://esempio.it/x }\n", options()},
	{"retries non numerico", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    retries: { max: tre }\n    request: { url: https://esempio.it/x }\n", options()},
	{"backoff sconosciuto", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    retries: { backoff: veloce }\n    request: { url: https://esempio.it/x }\n", options()},
	{"retries non è una mappa", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    retries: 3\n    request: { url: https://esempio.it/x }\n", options()},
	{"canale sconosciuto", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    alerts: { on_failure: [piccione] }\n    request: { url: https://esempio.it/x }\n", options()},
	{"canale ripetuto", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    alerts: { on_failure: [email, email] }\n    request: { url: https://esempio.it/x }\n", options()},
	{"alerts non è una mappa", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    alerts: email\n    request: { url: https://esempio.it/x }\n", options()},

	{"segreto inesistente, workspace con segreti", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      headers: { Authorization: \"Bearer ${ASSENTE}\" }\n", options("PRESENTE", "ALTRO")},
	{"segreto inesistente, workspace senza segreti", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: \"https://esempio.it/x?t=${TOKEN}\"\n", options()},
	{"riferimento minuscolo", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      body: \"${token}\"\n", options("TOKEN")},
	{"riferimento non chiuso", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      body: \"${TOKEN\"\n", options("TOKEN")},
	{"riferimento vuoto", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      body: \"${}\"\n", options("TOKEN")},
	{"riferimento nel nome di un header", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    request:\n      url: https://esempio.it/x\n      headers: { \"${TOKEN}\": v }\n", options("TOKEN")},

	{"risoluzione oltre il piano", "version: 1\njobs:\n  - name: uno\n    every: 1s\n    request: { url: https://esempio.it/x }\n", cronyaml.Options{Plan: jobs.FreePlan}},
	{"ambienti oltre il piano", "version: 1\njobs:\n  - name: uno\n    every: 1m\n    environments: [staging]\n    request: { url: https://esempio.it/x }\n", cronyaml.Options{Plan: jobs.FreePlan}},
	{"numero di job oltre il piano", tooManyJobs(21), cronyaml.Options{Plan: jobs.FreePlan}},
	{"numero di job oltre il piano, con job altrove", tooManyJobs(5), cronyaml.Options{Plan: jobs.FreePlan, OtherJobs: 18}},
}

// tooManyJobs costruisce un file con n job validi.
func tooManyJobs(n int) string {
	var out strings.Builder
	out.WriteString("version: 1\njobs:\n")
	for i := range n {
		out.WriteString("  - name: job-")
		out.WriteString(string(rune('a' + i%26)))
		out.WriteString(string(rune('a' + i/26)))
		out.WriteString("\n    every: 1m\n    request: { url: https://esempio.it/x }\n")
	}
	return out.String()
}

// TestOgniErroreDiceDoveEComeCorreggere è **il test della issue**.
//
// Un errore di questo package non è un valore di ritorno: è il prodotto. Chi lo
// riceve ha fatto `git push` e sta aspettando; non ha un debugger, non vede il
// nostro codice, e non può riprovare senza lasciare un commit nella cronologia
// del repository. Quindi ogni messaggio deve dire tre cose, e questo test le
// pretende tutte e tre su ogni errore che il parser sa produrre:
//
//  1. **dove** — riga e colonna dentro il file, mai zero, mai fuori dal file;
//  2. **quale campo** — il percorso che un client può evidenziare;
//  3. **come si corregge** — vedi [hasRemedy].
//
// Il terzo è quello che si perde per primo, ed è quello per cui il test esiste:
// «`every` non valido» supera qualunque asserzione sul codice di errore e non
// dice a chi legge niente che non sapesse già.
func TestOgniErroreDiceDoveEComeCorreggere(t *testing.T) {
	for _, caso := range broken {
		t.Run(caso.nome, func(t *testing.T) {
			source := yamlSource(caso.source)
			lines := strings.Count(string(source), "\n") + 1

			items := mustReject(t, caso.source, caso.options)
			for _, item := range items {
				if item.Line < 1 || item.Column < 1 {
					t.Errorf("posizione %s: un errore senza riga e colonna manda a cercare una riga che non esiste\n  %s",
						item.Position, item.Message)
				}
				if item.Line > lines {
					t.Errorf("posizione %s oltre la fine del file (%d righe)\n  %s", item.Position, lines, item.Message)
				}
				if item.Path == "" && !fileLevel(item.Code) {
					t.Errorf("errore senza campo (codice %q): solo i rifiuti dell'intero file possono non averne uno\n  %s",
						item.Code, item.Message)
				}
				if item.Code == "" {
					t.Errorf("errore senza codice su %s: un client non può farci branching\n  %s", item.Path, item.Message)
				}
				if !hasRemedy(item.Message) {
					t.Errorf("il messaggio su %s dice cosa non va ma non cosa scrivere al suo posto:\n  %s",
						item.Path, item.Message)
				}
			}
		})
	}
}

// fileLevel sono i codici che riguardano il file intero e non un campo: per
// loro non esiste un percorso da indicare.
func fileLevel(code string) bool {
	switch code {
	case cronyaml.CodeSyntax, cronyaml.CodeEmpty, cronyaml.CodeTooLarge, cronyaml.CodeWrongKind:
		return true
	default:
		return false
	}
}

// typeable riconosce un frammento fra backtick che si può battere nel file:
// `every: 10s`, `version: 1`, `retries: { max: 3 }`. È la forma più forte di
// rimedio, perché non descrive la correzione — la mostra.
var typeable = regexp.MustCompile("`[^`]*: [^`]*`")

// imperatives sono i verbi con cui un messaggio dice cosa fare.
var imperatives = []string{
	"scrivi", "aggiungi", "togli", "usa ", "sostituisci", "accorcia",
	"correggi", "dàgliene", "crealo", "sposta", "dividi", "unisci",
	"controlla", "passa a un piano", "manda meno", "allarga", "chiudi",
	"forse intendevi", "per esempio", "l'alternativa è",
}

// enumerations sono i modi in cui un messaggio elenca i valori ammessi. Anche
// quello è un rimedio: la correzione è uno dei valori dell'elenco.
var enumerations = []string{"ammess", "disponibil", "sono `"}

// hasRemedy decide se un messaggio dice come correggere.
//
// La regola non è «il messaggio è lungo» né «il messaggio è gentile»: è che
// contenga qualcosa da **fare**. Le tre forme ammesse sono quelle che in un
// file di configurazione hanno senso — il frammento da battere, il verbo che
// dice cosa cambiare, l'elenco dei valori fra cui scegliere — e un messaggio
// che non ha nessuna delle tre descrive un problema senza offrire una via
// d'uscita.
func hasRemedy(message string) bool {
	if typeable.MatchString(message) {
		return true
	}
	lower := strings.ToLower(message)
	for _, verb := range imperatives {
		if strings.Contains(lower, verb) {
			return true
		}
	}
	for _, list := range enumerations {
		if strings.Contains(lower, list) {
			return true
		}
	}
	return false
}

// TestIlTestSuiMessaggiSaFallire è la controprova del test qui sopra: se
// [hasRemedy] accettasse qualunque cosa, la sua asserzione sarebbe decorativa e
// nessuno se ne accorgerebbe finché i messaggi non peggiorano.
func TestIlTestSuiMessaggiSaFallire(t *testing.T) {
	senzaRimedio := []string{
		"invalid yaml",
		"`every` non valido.",
		"Il nome supera 100 caratteri.",
		"`jobs[0].timeout` dev'essere una durata, non una mappa.",
		"Errore di validazione nel campo request.url.",
	}
	for _, message := range senzaRimedio {
		if hasRemedy(message) {
			t.Errorf("hasRemedy accetta un messaggio che non dice come correggere: %q", message)
		}
	}

	conRimedio := []string{
		"Scrivi `version: 1`.",
		"I metodi ammessi sono GET, POST.",
		"La chiave `schedul` non esiste. Forse intendevi `schedule`.",
	}
	for _, message := range conRimedio {
		if !hasRemedy(message) {
			t.Errorf("hasRemedy rifiuta un messaggio che dice come correggere: %q", message)
		}
	}
}

// ---------------------------------------------- tutti gli errori, in un giro

// TestTuttiGliErroriArrivanoInUnGiroSolo: chi ha sbagliato tre job vuole
// saperlo in un push, non in tre.
//
// Il ciclo di questo file è correggi → commit → push → aspetta, e ogni giro
// resta nella cronologia del repository per sempre. Un parser che si ferma al
// primo rifiuto trasforma tre refusi in tre commit di correzione.
func TestTuttiGliErroriArrivanoInUnGiroSolo(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: primo
    every: 1x
    request:
      url: https://esempio.it/uno
  - name: secondo
    schedule: "0 9 *"
    request:
      url: nonunurl
  - name: terzo
    request:
      url: https://esempio.it/tre
      headers: { Authorization: "Bearer ${ASSENTE}" }
`, options("PRESENTE"))

	// Un errore per ciascun job, e ciascuno sul job giusto: quattro in tutto,
	// perché il secondo job ne ha due.
	vogliamo := []string{
		"jobs[0].every",
		"jobs[1].schedule",
		"jobs[1].request.url",
		"jobs[2]",
		"jobs[2].request.headers.Authorization",
	}
	if len(items) != len(vogliamo) {
		t.Fatalf("errori = %d, attesi %d:\n%s", len(items), len(vogliamo), format(items))
	}
	for i, path := range vogliamo {
		if items[i].Path != path {
			t.Errorf("errore %d su %q, atteso %q\n%s", i, items[i].Path, path, format(items))
		}
	}

	// E l'ordine è quello del file, che è l'ordine in cui una persona li
	// incontra scorrendolo.
	for i := 1; i < len(items); i++ {
		if items[i-1].Line > items[i].Line {
			t.Errorf("gli errori non sono in ordine di riga: %d dopo %d", items[i].Line, items[i-1].Line)
		}
	}
}

// TestIlTestoDelParseErrorSiLegge fissa la forma dell'elenco: `file:riga:colonna`
// è la convenzione dei compilatori, ed è ciò che un editor sa trasformare in un
// salto alla riga giusta.
func TestIlTestoDelParseErrorSiLegge(t *testing.T) {
	_, err := cronyaml.Parse(t.Context(), yamlSource(`
version: 1
jobs:
  - name: uno
    every: 1x
    request: { url: https://esempio.it/x }
`), options())
	if err == nil {
		t.Fatal("Parse non ha rifiutato il file")
	}

	text := err.Error()
	if !strings.HasPrefix(text, "cron.yaml: 1 errore.") {
		t.Errorf("l'intestazione non accorda il conteggio:\n%s", text)
	}
	if !strings.Contains(text, "cron.yaml:4:12: jobs[0].every: ") {
		t.Errorf("manca l'ancora `file:riga:colonna: campo`:\n%s", text)
	}
}

// ---------------------------------------- le due modalità (SPEC §9, regola 1)

// TestDichiarareEntrambeLeModalitaEUnErrore. L'errore si mostra su `every` — il
// secondo dei due — e nomina la riga dell'altro: è dove sta la cosa da togliere.
func TestDichiarareEntrambeLeModalitaEUnErrore(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    schedule: "0 9 * * *"
    every: 10s
    request: { url: https://esempio.it/x }
`, options())

	if len(items) != 1 {
		t.Fatalf("errori = %d, atteso 1:\n%s", len(items), format(items))
	}
	item := items[0]
	if item.Code != "schedule_conflict" {
		t.Errorf("codice = %q", item.Code)
	}
	at(t, item, 5, 12)
	contains(t, item, "riga 4")
	contains(t, item, "Togline uno")
}

// TestNonDichiarareNessunaModalitaEUnErrore. Qui non c'è una riga da indicare —
// il campo manca — quindi l'errore si ancora al job, e il messaggio contiene
// entrambe le forme che si possono scrivere.
func TestNonDichiarareNessunaModalitaEUnErrore(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    request: { url: https://esempio.it/x }
`, options())

	if len(items) != 1 {
		t.Fatalf("errori = %d, atteso 1:\n%s", len(items), format(items))
	}
	item := items[0]
	if item.Code != "schedule_required" || item.Path != "jobs[0]" {
		t.Errorf("codice = %q su %q", item.Code, item.Path)
	}
	at(t, item, 3, 5)
	contains(t, item, "schedule:")
	contains(t, item, "every: 10s")
}

// TestUnaDurataIllegibileNonDiventaUnaModalitaMancante: chi ha scritto
// `every: 1x` **ha** dichiarato la modalità, ed è la durata a non leggersi.
// Dirgli «serve `schedule` oppure `every`» sarebbe una diagnosi sbagliata su una
// riga giusta, cioè il modo più efficace di far perdere tempo.
func TestUnaDurataIllegibileNonDiventaUnaModalitaMancante(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: 1x
    request: { url: https://esempio.it/x }
`, options())

	if len(items) != 1 {
		t.Fatalf("errori = %d, atteso 1:\n%s", len(items), format(items))
	}
	if items[0].Code != cronyaml.CodeInvalidDuration {
		t.Errorf("codice = %q, atteso %q\n%s", items[0].Code, cronyaml.CodeInvalidDuration, format(items))
	}
}

// ----------------------------------------- i segreti (R43, SPEC §9 regola 3)

// TestUnSegretoInesistenteFallisceAlSyncENonAlleTreDiNotte è il cuore di R43.
//
// La verifica è sui **nomi**, senza decifrare niente, ed è la stessa funzione di
// espansione che l'esecutore usa per risolverli: se fossero due parser distinti,
// la promessa «ciò che passa il sync non fallisce di notte» dipenderebbe dal
// fatto che restino d'accordo per sempre.
func TestUnSegretoInesistenteFallisceAlSyncENonAlleTreDiNotte(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    request:
      url: "https://esempio.it/x?t=${MANCANTE}"
      headers:
        Authorization: "Bearer ${DIGEST_TOKEN}"
      body: '{"chiave":"${ALTRO_MANCANTE}"}'
`, options("DIGEST_TOKEN"))

	if len(items) != 2 {
		t.Fatalf("errori = %d, attesi 2:\n%s", len(items), format(items))
	}

	url := find(t, items, "jobs[0].request.url")
	at(t, url, 6, 12)
	contains(t, url, "MANCANTE")
	contains(t, url, "DIGEST_TOKEN") // l'elenco di ciò che c'è è la risposta al refuso

	body := find(t, items, "jobs[0].request.body")
	at(t, body, 9, 13)
	contains(t, body, "ALTRO_MANCANTE")
}

// TestIRiferimentiRestanoNonRisoltiNelJob: il parser non decifra e non
// sostituisce niente. Ciò che viene scritto nel database è il testo del file.
func TestIRiferimentiRestanoNonRisoltiNelJob(t *testing.T) {
	file := mustParse(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    request:
      url: "https://esempio.it/x?t=${TOKEN}"
      headers:
        Authorization: "Bearer ${TOKEN}"
      body: '{"k":"${TOKEN}"}'
`, options("TOKEN"))

	job := file.Jobs[0].Job
	if job.URL != "https://esempio.it/x?t=${TOKEN}" {
		t.Errorf("url = %q", job.URL)
	}
	if job.Headers["Authorization"] != "Bearer ${TOKEN}" {
		t.Errorf("header = %q", job.Headers["Authorization"])
	}
	if job.Body != `{"k":"${TOKEN}"}` {
		t.Errorf("body = %q", job.Body)
	}
}

// TestUnRiferimentoNellHostNonImpedisceLaLettura: SPEC §9 e R43 ammettono i
// `${VAR}` nell'URL, host compreso. Un URL con un riferimento nell'autorità non
// è leggibile da `net/url`, e senza un accorgimento verrebbe rifiutato come
// «URL non leggibile» — un errore su una riga corretta.
func TestUnRiferimentoNellHostNonImpedisceLaLettura(t *testing.T) {
	file := mustParse(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    request:
      url: "https://${HOST}/hook"
`, options("HOST"))

	if got := file.Jobs[0].Job.URL; got != "https://${HOST}/hook" {
		t.Errorf("url = %q", got)
	}
}

// TestLoSchemaNonPuoEssereUnSegreto: il riferimento può stare nell'host, non al
// posto di `https://`. Il vincolo `jobs_url_scheme_check` della 0005 pretende il
// prefisso letterale, quindi accettarlo qui produrrebbe un INSERT rifiutato.
func TestLoSchemaNonPuoEssereUnSegreto(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    request:
      url: "${BASE}/hook"
`, options("BASE"))

	item := find(t, items, "jobs[0].request.url")
	contains(t, item, "https://")
}

// --------------------------------------- i limiti di piano (SPEC §8, regola 5)

// TestUnEveryTroppoFineVieneRifiutatoDicendoQualePianoLoConsente: SPEC §9 dice
// «rifiutato con un messaggio esplicito, non silenziosamente degradato». Un
// limite applicato in silenzio è, per chi lo subisce, indistinguibile da un
// guasto del prodotto.
func TestUnEveryTroppoFineVieneRifiutatoDicendoQualePianoLoConsente(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: 1s
    request: { url: https://esempio.it/x }
`, cronyaml.Options{Plan: jobs.FreePlan})

	if len(items) != 1 {
		t.Fatalf("errori = %d, atteso 1:\n%s", len(items), format(items))
	}
	item := items[0]
	at(t, item, 4, 12)
	if item.Path != "jobs[0].every" {
		t.Errorf("campo = %q", item.Path)
	}
	if item.Limit != jobs.LimitResolution || item.Plan != "free" {
		t.Errorf("limite = %q su piano %q: il client deve poter distinguere «correggi il file» da «serve un piano superiore»",
			item.Limit, item.Plan)
	}
	contains(t, item, "Free")
	contains(t, item, "1m")
	contains(t, item, "every: 1s")
}

// TestLoStessoEveryPassaSuUnPianoCheLoConsente è l'altra metà: il limite viene
// dal piano, non dal parser.
func TestLoStessoEveryPassaSuUnPianoCheLoConsente(t *testing.T) {
	source := `
version: 1
jobs:
  - name: uno
    every: 1s
    request: { url: https://esempio.it/x }
`
	mustParse(t, source, cronyaml.Options{Plan: teamPlan()})

	items := mustReject(t, source, cronyaml.Options{Plan: proPlan()})
	if items[0].Limit != jobs.LimitResolution {
		t.Errorf("su Pro (10s) un `every: 1s` dev'essere rifiutato dal limite di risoluzione, non da %q", items[0].Code)
	}
}

// TestIlTettoAlNumeroDiJobContaAncheIJobFuoriDalFile: il limite di SPEC §8 è
// sull'utente, non sul file. Il messaggio deve dirlo, perché «il piano consente
// 20 job» detto a chi nel file ne ha scritti 5 è incomprensibile.
func TestIlTettoAlNumeroDiJobContaAncheIJobFuoriDalFile(t *testing.T) {
	items := mustReject(t, tooManyJobs(5), cronyaml.Options{Plan: jobs.FreePlan, OtherJobs: 18})

	item := findCode(t, items, string(jobs.LimitJobs))
	if item.Limit != jobs.LimitJobs {
		t.Errorf("limite = %q", item.Limit)
	}
	contains(t, item, "18")
	contains(t, item, "Togline 3")
	// L'ancora è il primo job che non ci sta: è quello da togliere.
	if item.Path != "jobs[2]" {
		t.Errorf("campo = %q, atteso jobs[2]: 18 job altrove più i primi 2 del file riempiono i 20 del piano", item.Path)
	}
}

// TestIlTettoAlNumeroDiJobNonScattaSuUnPianoSenzaTetto.
func TestIlTettoAlNumeroDiJobNonScattaSuUnPianoSenzaTetto(t *testing.T) {
	mustParse(t, tooManyJobs(30), cronyaml.Options{Plan: teamPlan(), OtherJobs: 5000})
}

// TestIlPianoAssenteValeFree: un'opzione dimenticata dal chiamante non deve
// diventare un aggiramento silenzioso del listino, che è l'unico tipo di difetto
// che nessun test nota perché tutto continua a funzionare.
func TestIlPianoAssenteValeFree(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: 1s
    request: { url: https://esempio.it/x }
`, cronyaml.Options{})

	item := findCode(t, items, string(jobs.LimitResolution))
	if item.Plan != jobs.FreePlan.Code {
		t.Errorf("piano = %q, atteso %q", item.Plan, jobs.FreePlan.Code)
	}
}

// TestUnErroreDiPianoSiDistingueDaUnErroreDelFile.
func TestUnErroreDiPianoSiDistingueDaUnErroreDelFile(t *testing.T) {
	_, err := cronyaml.Parse(t.Context(), yamlSource(`
version: 1
jobs:
  - name: uno
    every: 1s
    request: { url: https://esempio.it/x }
`), cronyaml.Options{Plan: jobs.FreePlan})
	parse, _ := cronyaml.AsParseError(err)
	if !parse.PlanLimited() {
		t.Error("PlanLimited() è falso su un rifiuto che viene dal piano")
	}

	_, err = cronyaml.Parse(t.Context(), yamlSource(`
version: 1
jobs:
  - name: uno
    every: 1x
    request: { url: https://esempio.it/x }
`), cronyaml.Options{Plan: jobs.FreePlan})
	parse, _ = cronyaml.AsParseError(err)
	if parse.PlanLimited() {
		t.Error("PlanLimited() è vero su un rifiuto che viene dalla forma del file")
	}
}

// ------------------------------------------------- version (SPEC §9, regola 6)

// TestVersionEObbligatorio. Il file resta invalido, ma la lettura prosegue: chi
// ha dimenticato `version` deve ricevere anche gli altri errori, non tornare qui
// una seconda volta.
func TestVersionEObbligatorio(t *testing.T) {
	items := mustReject(t, `
jobs:
  - name: uno
    every: 1x
    request: { url: https://esempio.it/x }
`, options())

	item := find(t, items, "version")
	if item.Code != cronyaml.CodeRequired {
		t.Errorf("codice = %q", item.Code)
	}
	contains(t, item, "version: 1")
	if len(items) != 2 {
		t.Errorf("errori = %d, attesi 2: la mancanza di `version` non deve nascondere il resto del file\n%s",
			len(items), format(items))
	}
}

// TestUnaVersioneSconosciutaFermaLaLettura è l'opposto, ed è il motivo per cui
// `version` esiste: di uno schema che non conosciamo non sappiamo dire niente, e
// un elenco di chiavi «sconosciute» che nella versione 2 sono quelle giuste
// sarebbe peggio del silenzio.
func TestUnaVersioneSconosciutaFermaLaLettura(t *testing.T) {
	items := mustReject(t, `
version: 2
jobs:
  - nome: uno
    ogni: 1m
`, options())

	if len(items) != 1 {
		t.Fatalf("errori = %d, atteso solo quello sulla versione:\n%s", len(items), format(items))
	}
	if items[0].Code != cronyaml.CodeUnsupportedVersion {
		t.Errorf("codice = %q", items[0].Code)
	}
	contains(t, items[0], "version: 1")
}

// -------------------------------------------- le chiavi che non esistono

// TestUnaChiaveSconosciutaSuggerisceQuellaGiusta.
func TestUnaChiaveSconosciutaSuggerisceQuellaGiusta(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    schedul: "0 9 * * *"
    request: { url: https://esempio.it/x }
`, options())

	item := find(t, items, "jobs[0].schedul")
	if item.Code != cronyaml.CodeUnknownKey {
		t.Errorf("codice = %q", item.Code)
	}
	at(t, item, 4, 5)
	contains(t, item, "Forse intendevi `schedule`")
}

// TestUnaChiaveSconosciutaSenzaSomiglianzeElencaQuelleAmmesse: un suggerimento
// sbagliato costa più del silenzio, perché manda a provare qualcosa che non
// funzionerà.
func TestUnaChiaveSconosciutaSenzaSomiglianzeElencaQuelleAmmesse(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    priorita: alta
    request: { url: https://esempio.it/x }
`, options())

	item := find(t, items, "jobs[0].priorita")
	contains(t, item, "`schedule`")
	if strings.Contains(item.Message, "Forse intendevi") {
		t.Errorf("`priorita` non assomiglia a nessuna chiave: il messaggio non deve indovinare\n  %s", item.Message)
	}
}

// TestUnaChiaveRipetutaNonSiRisolveInSilenzio: due `schedule` nello stesso job
// sono due orari, e la scelta che quasi tutti i lettori YAML fanno — tenere
// l'ultimo — significherebbe eseguire il job a un'ora che l'utente non ha mai
// letto nel proprio file.
func TestUnaChiaveRipetutaNonSiRisolveInSilenzio(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    schedule: "0 9 * * *"
    schedule: "0 18 * * *"
    request: { url: https://esempio.it/x }
`, options())

	item := findCode(t, items, cronyaml.CodeDuplicateKey)
	at(t, item, 5, 5)
	contains(t, item, "riga 4")
	contains(t, item, "togline una")
}

// TestDueJobConLoStessoNomeSonoUnErrore: `name` è l'identità su cui la
// riconciliazione decide se creare, aggiornare o disattivare (SPEC §9, R13).
// Due voci con lo stesso nome si contendono la stessa identità, e qualunque cosa
// il sync ne facesse sarebbe una regola che l'utente non ha scritto.
func TestDueJobConLoStessoNomeSonoUnErrore(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: digest
    every: 1m
    request: { url: https://esempio.it/uno }
  - name: digest
    every: 2m
    request: { url: https://esempio.it/due }
`, options())

	item := findCode(t, items, cronyaml.CodeDuplicateName)
	at(t, item, 6, 5)
	contains(t, item, "riga 3")
	contains(t, item, "digest")
}

// ---------------------------------------------------- la sintassi YAML

// TestUnErroreDiSintassiNonSiRiportaCosiComEArriva.
//
// «yaml: line 3: found character that cannot start any token» è, per chi ha
// scritto il file, un messaggio in una lingua che non parla, su una libreria di
// cui non sa l'esistenza, che descrive lo stato interno di un tokenizzatore. La
// causa vera è una sola e concreta, e dirla è la differenza fra un file corretto
// in trenta secondi e un file abbandonato.
func TestUnErroreDiSintassiNonSiRiportaCosiComEArriva(t *testing.T) {
	casi := []struct {
		nome   string
		source string
		riga   int
		attesa string
	}{
		{"tabulazione", "version: 1\njobs:\n\t- name: uno\n", 3, "tabulazione"},
		{"due punti nel valore", "version: 1\njobs:\n  - name: uno\n    body: {\"a\":1}: no\n", 4, "virgolette"},
		{"virgoletta non chiusa", "version: 1\njobs:\n  - name: \"uno\n", 3, "Chiudi"},
	}

	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			items := mustReject(t, caso.source, options())
			item := findCode(t, items, cronyaml.CodeSyntax)
			if item.Line != caso.riga {
				t.Errorf("riga = %d, attesa %d\n  %s", item.Line, caso.riga, item.Message)
			}
			if strings.Contains(item.Message, "yaml:") {
				t.Errorf("il messaggio della libreria è passato all'utente così com'è:\n  %s", item.Message)
			}
			contains(t, item, caso.attesa)
		})
	}
}

// TestUnSecondoDocumentoNonVieneIgnoratoInSilenzio è la forma peggiore di errore
// che questo file possa avere: il push riesce, la validazione passa, e metà
// delle schedulazioni non esistono senza che nessuno lo dica.
func TestUnSecondoDocumentoNonVieneIgnoratoInSilenzio(t *testing.T) {
	items := mustReject(t, `
version: 1
jobs:
  - name: uno
    every: 1m
    request: { url: https://esempio.it/uno }
---
version: 1
jobs:
  - name: due
    every: 1m
    request: { url: https://esempio.it/due }
`, options())

	item := findCode(t, items, cronyaml.CodeSyntax)
	contains(t, item, "---")
}

// TestUnFileTroppoGrandeVieneFermatoPrimaDiLeggerlo: la sorgente è un file
// dentro un repository qualsiasi, cioè un input che non controlliamo.
func TestUnFileTroppoGrandeVieneFermatoPrimaDiLeggerlo(t *testing.T) {
	source := "version: 1\njobs: []\n# " + strings.Repeat("x", cronyaml.MaxFileSize) + "\n"

	items := mustReject(t, source, options())
	if len(items) != 1 || items[0].Code != cronyaml.CodeTooLarge {
		t.Fatalf("atteso un solo errore di dimensione:\n%s", format(items))
	}
}

// ------------------------------------------------- l'ancora sui defaults

// TestUnErroreNeiDefaultsSiMostraSuiDefaults.
//
// Un `defaults.timeout` fuori intervallo si manifesta come errore *di ogni job*,
// perché è a ogni job che viene applicato. Senza un'ancora sui defaults,
// l'utente riceverebbe cinquanta errori che puntano a cinquanta job corretti
// invece di cinquanta errori che puntano tutti alla stessa riga sbagliata.
func TestUnErroreNeiDefaultsSiMostraSuiDefaults(t *testing.T) {
	items := mustReject(t, `
version: 1
defaults:
  timeout: 900s
jobs:
  - name: uno
    every: 1m
    request: { url: https://esempio.it/uno }
  - name: due
    every: 1m
    request: { url: https://esempio.it/due }
`, options())

	if len(items) != 2 {
		t.Fatalf("errori = %d, attesi 2 (uno per job):\n%s", len(items), format(items))
	}
	for _, item := range items {
		if item.Path != "defaults.timeout" {
			t.Errorf("campo = %q, atteso defaults.timeout: è lì la riga da correggere", item.Path)
		}
		at(t, item, 3, 12)
	}
}

// TestUnFusoSbagliatoSiMostraSulFusoAncheSuUnJobAIntervallo.
//
// È il caso in cui l'ancora è più facile da sbagliare: [jobs] riporta al campo
// `every` qualunque errore di schedulazione di un job a intervallo, fuso
// compreso. L'errore finirebbe così sulla riga di `every`, che è scritta bene,
// mentre quella sbagliata sta dentro `defaults` — cioè in un altro punto del
// documento, che nessuna riga dell'elenco indicherebbe.
func TestUnFusoSbagliatoSiMostraSulFusoAncheSuUnJobAIntervallo(t *testing.T) {
	items := mustReject(t, `
version: 1
defaults:
  timezone: Europe/Roma
jobs:
  - name: uno
    every: 10s
    request: { url: https://esempio.it/x }
`, options())

	if len(items) != 1 {
		t.Fatalf("errori = %d, atteso 1:\n%s", len(items), format(items))
	}
	item := items[0]
	if item.Path != "defaults.timezone" {
		t.Errorf("campo = %q, atteso defaults.timezone: è lì la riga da correggere, non su `every`", item.Path)
	}
	at(t, item, 3, 13)
	contains(t, item, "Europe/Rome")
	if strings.Contains(item.Message, "intervallo si scrive") {
		t.Errorf("il rimedio è quello di `every` su un errore che riguarda il fuso:\n  %s", item.Message)
	}
}

// TestUnJobCheSovrascriveIDefaultsSiAncoraASeStesso.
func TestUnJobCheSovrascriveIDefaultsSiAncoraASeStesso(t *testing.T) {
	items := mustReject(t, `
version: 1
defaults:
  timeout: 30s
jobs:
  - name: uno
    every: 1m
    timeout: 900s
    request: { url: https://esempio.it/uno }
`, options())

	item := find(t, items, "jobs[0].timeout")
	at(t, item, 7, 14)
}
