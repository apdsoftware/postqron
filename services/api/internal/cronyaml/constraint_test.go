package cronyaml_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/cronyaml"
)

// Questo file prova una cosa sola, e non è una proprietà del parser: è che il
// parser e il database **dicano la stessa cosa**.
//
// Il vincolo di SPEC §9 — una modalità di schedulazione e una sola — vive in due
// posti. Nel database è `jobs_schedule_xor_every_check` della migrazione 0005;
// nel codice è [schedule.Parse], che il parser raggiunge attraverso
// [jobs.Job.Validate]. Due implementazioni attente non bastano a tenerli
// allineati: bastano finché qualcuno non ne modifica una sola, e quel qualcuno
// non ha modo di sapere che l'altra esiste.
//
// Il confronto si fa **leggendo la migrazione**, non riscrivendone la regola qui
// dentro. Una costante `attesoXOR = true` sarebbe una terza copia, e la terza
// copia è quella che diverge per prima.
//
// Le proprietà che dipendono davvero da PostgreSQL — che il vincolo esista, che
// respinga l'INSERT — sono provate in internal/jobspg contro il database vero
// (TestIlVincoloXOREsiste). Qui non serve un database in piedi, e quindi questo
// controllo gira in `make ci` su qualunque macchina: è la differenza fra un test
// che protegge sempre e uno che protegge quando qualcuno si ricorda di alzare il
// container.

// migration è il testo della migrazione che crea la tabella `jobs`.
func migration(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", "db", "migrations", "0005_jobs.up.sql")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("la migrazione della tabella `jobs` non è leggibile da %s: %v", path, err)
	}
	return string(source)
}

// checkExpression estrae l'espressione di un `CHECK` con nome, bilanciando le
// parentesi invece di fidarsi della prima chiusa.
func checkExpression(t *testing.T, sql, constraint string) string {
	t.Helper()

	start := strings.Index(sql, "CONSTRAINT "+constraint)
	if start < 0 {
		t.Fatalf("la migrazione non contiene più il vincolo %q.\n"+
			"Se è stato rinominato o rimosso, il parser e il database non sono più confrontati da nessuno: aggiorna questo test guardando che cosa il vincolo dice adesso.",
			constraint)
	}
	open := strings.Index(sql[start:], "CHECK (")
	if open < 0 {
		t.Fatalf("il vincolo %q non è più un CHECK", constraint)
	}
	open += start + len("CHECK ")

	depth := 0
	for i := open; i < len(sql); i++ {
		switch sql[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.Join(strings.Fields(sql[open+1:i]), " ")
			}
		}
	}
	t.Fatalf("le parentesi del vincolo %q non si chiudono", constraint)
	return ""
}

// xorShape riconosce la **sola** forma di vincolo che questo test sa valutare:
// `(colonna IS NULL) <> (colonna IS NULL)`, che in SQL fra due booleani è lo
// XOR. Qualunque altra forma fa fallire il test invece di essere interpretata a
// caso: se il vincolo cambia, la coincidenza fra le due verità va ristabilita da
// una persona, non indovinata da una regexp.
var xorShape = regexp.MustCompile(`^\(\s*(\w+) IS NULL\s*\) <> \(\s*(\w+) IS NULL\s*\)$`)

// TestIlVincoloDelDatabaseEQuelloDelParser confronta le due verità su tutte e
// quattro le combinazioni possibili.
//
// Se questo test fallisce, uno dei due lati accetta ciò che l'altro rifiuta. Nel
// verso «parser più permissivo» il sintomo è un 500 al sync, con l'INSERT
// respinto da PostgreSQL dopo che l'utente ha già fatto push; nel verso opposto
// è un file corretto rifiutato senza che nessuna regola scritta lo vieti.
func TestIlVincoloDelDatabaseEQuelloDelParser(t *testing.T) {
	expression := checkExpression(t, migration(t), "jobs_schedule_xor_every_check")

	match := xorShape.FindStringSubmatch(expression)
	if match == nil {
		t.Fatalf("il vincolo `jobs_schedule_xor_every_check` non ha più la forma `(a IS NULL) <> (b IS NULL)` ma:\n  %s\n"+
			"Questo test non lo interpreta: rileggilo e riscrivi qui il confronto con ciò che il parser fa.", expression)
	}
	if match[1] != "schedule" || match[2] != "every_seconds" {
		t.Fatalf("il vincolo mette in XOR %q e %q, non `schedule` ed `every_seconds`: la modalità di schedulazione non è più quella che il parser legge",
			match[1], match[2])
	}

	// Le quattro combinazioni. `dichiara` scrive il job con o senza ciascuna
	// delle due chiavi; `nullo` è ciò che finirebbe nella colonna.
	casi := []struct {
		nome         string
		schedule     string
		every        string
		scheduleNull bool
		everyNull    bool
	}{
		{"solo schedule", "    schedule: \"0 9 * * *\"\n", "", false, true},
		{"solo every", "", "    every: 1m\n", true, false},
		{"entrambe", "    schedule: \"0 9 * * *\"\n", "    every: 1m\n", false, false},
		{"nessuna", "", "", true, true},
	}

	accettati := 0
	for _, caso := range casi {
		t.Run(caso.nome, func(t *testing.T) {
			// Il database: l'espressione del vincolo, valutata.
			database := caso.scheduleNull != caso.everyNull

			source := "version: 1\njobs:\n  - name: uno\n" + caso.schedule + caso.every +
				"    request: { url: https://esempio.it/x }\n"
			_, err := cronyaml.Parse(t.Context(), yamlSource(source), options())
			parser := err == nil

			if parser != database {
				verso := "il parser accetta ciò che il database rifiuterebbe: il sync fallirebbe con un 500 dopo il push"
				if database {
					verso = "il parser rifiuta ciò che il database accetterebbe: un file legittimo viene respinto"
				}
				t.Fatalf("%s\n  vincolo: %s\n  errore del parser: %v", verso, expression, err)
			}
			if parser {
				accettati++
			}
		})
	}

	// Una controprova sul test stesso: se il confronto passasse perché entrambi
	// i lati dicono sempre la stessa cosa — per esempio perché il parser rifiuta
	// tutto — il test sopra sarebbe verde e inutile.
	if accettati != 2 {
		t.Errorf("combinazioni accettate = %d, attese 2: lo XOR deve accettarne esattamente due", accettati)
	}
}

// TestLaFormaDellEspressioneCronEQuellaDelDatabase.
//
// `jobs_schedule_shape_check` pretende cinque campi separati da spazi. È il
// motivo per cui il job normalizza l'espressione prima di conservarla:
// `"0  9 * * *"`, che una persona scrive senza accorgersene e che il parser cron
// accetta, verrebbe respinto dalla colonna.
func TestLaFormaDellEspressioneCronEQuellaDelDatabase(t *testing.T) {
	expression := checkExpression(t, migration(t), "jobs_schedule_shape_check")

	pattern := regexp.MustCompile(`schedule ~ '([^']+)'`).FindStringSubmatch(expression)
	if pattern == nil {
		t.Fatalf("il vincolo sulla forma di `schedule` non contiene più un confronto con un'espressione regolare:\n  %s", expression)
	}
	shape, err := regexp.Compile(pattern[1])
	if err != nil {
		t.Fatalf("l'espressione regolare del database non è compilabile in Go (%v): il confronto va rifatto a mano", err)
	}

	// Espressioni che una persona scrive davvero, spaziate in modi diversi.
	for _, scritta := range []string{`"0 9 * * *"`, `"0  9  *  *  *"`, `"*/15 2 * * 1"`, `" 0 9 * * * "`} {
		file := mustParse(t, "version: 1\njobs:\n  - name: uno\n    schedule: "+scritta+
			"\n    request: { url: https://esempio.it/x }\n", options())

		conservata := file.Jobs[0].Job.Schedule
		if !shape.MatchString(conservata) {
			t.Errorf("da %s il parser conserva %q, che la colonna `schedule` rifiuterebbe (%s)",
				scritta, conservata, pattern[1])
		}
	}
}

// TestIlMinimoDiEverySecondsEQuelloDelDatabase: la colonna è
// `every_seconds integer CHECK (every_seconds >= 1)`, quindi un secondo è il
// minimo assoluto e le durate sono **secondi interi**. È la ragione per cui il
// parser rifiuta `500ms` invece di arrotondarlo: arrotondare significherebbe
// eseguire qualcosa di diverso da ciò che il file dice.
func TestIlMinimoDiEverySecondsEQuelloDelDatabase(t *testing.T) {
	sql := migration(t)

	match := regexp.MustCompile(`every_seconds integer CHECK \(every_seconds >= (\d+)\)`).FindStringSubmatch(sql)
	if match == nil {
		t.Fatalf("la colonna `every_seconds` non dichiara più un minimo: il confronto con il parser va rifatto")
	}
	minimo, _ := strconv.Atoi(match[1])
	if minimo != 1 {
		t.Fatalf("il minimo della colonna è %d secondi: il parser lo conosce come 1, aggiornalo", minimo)
	}

	// Il minimo del database passa (su un piano che lo consente).
	mustParse(t, fmt.Sprintf("version: 1\njobs:\n  - name: uno\n    every: %ds\n    request: { url: https://esempio.it/x }\n", minimo), options())

	// Tutto ciò che sta sotto viene fermato **prima** della query.
	for _, sotto := range []string{"0s", "500ms", "-1s"} {
		items := mustReject(t, "version: 1\njobs:\n  - name: uno\n    every: "+sotto+
			"\n    request: { url: https://esempio.it/x }\n", options())
		item := find(t, items, "jobs[0].every")
		if item.Code != cronyaml.CodeInvalidDuration {
			t.Errorf("`every: %s` rifiutato con il codice %q invece che come durata non valida", sotto, item.Code)
		}
	}
}

// TestIlNomeAccettatoDalParserEQuelloAccettatoDalDatabase.
//
// `name` è l'identità del job, cioè la chiave della riconciliazione (R13): un
// nome che il parser accetta e la colonna rifiuta sarebbe un job che il sync non
// riesce a scrivere e che nessun messaggio spiega.
func TestIlNomeAccettatoDalParserEQuelloAccettatoDalDatabase(t *testing.T) {
	expression := checkExpression(t, migration(t), "jobs_name_format_check")

	pattern := regexp.MustCompile(`name ~ '([^']+)'`).FindStringSubmatch(expression)
	length := regexp.MustCompile(`char_length\(name\) <= (\d+)`).FindStringSubmatch(expression)
	if pattern == nil || length == nil {
		t.Fatalf("il vincolo sul nome non ha più la forma attesa:\n  %s", expression)
	}
	shape, err := regexp.Compile(pattern[1])
	if err != nil {
		t.Fatalf("l'espressione regolare del database non è compilabile in Go: %v", err)
	}
	limite, _ := strconv.Atoi(length[1])

	nomi := []string{
		"daily-digest", "a", "a.b-c_d1", "job1",
		strings.Repeat("a", limite),
		strings.Repeat("a", limite+1),
		"il mio job", "-inizio", "fine-", ".punto", "con/slash", "accentàto", "",
	}

	for _, nome := range nomi {
		t.Run(fmt.Sprintf("%.20q", nome), func(t *testing.T) {
			database := shape.MatchString(nome) && len([]rune(nome)) <= limite

			source := "version: 1\njobs:\n  - name: \"" + nome + "\"\n    every: 1m\n    request: { url: https://esempio.it/x }\n"
			_, err := cronyaml.Parse(t.Context(), yamlSource(source), options())
			parser := err == nil

			if parser != database {
				t.Errorf("il nome %q: parser=%v, database=%v (%s, max %d)\n  %v",
					nome, parser, database, pattern[1], limite, err)
			}
		})
	}
}

// TestGliIntervalliNumericiSonoQuelliDelDatabase confronta i `BETWEEN` delle
// colonne `timeout_seconds` e `max_retries` con ciò che il parser lascia
// passare, agli estremi e appena fuori.
func TestGliIntervalliNumericiSonoQuelliDelDatabase(t *testing.T) {
	sql := migration(t)

	casi := []struct {
		colonna string
		scrivi  func(int) string
	}{
		{"timeout_seconds", func(v int) string {
			return fmt.Sprintf("version: 1\njobs:\n  - name: uno\n    every: 1m\n    timeout: %ds\n    request: { url: https://esempio.it/x }\n", v)
		}},
		{"max_retries", func(v int) string {
			return fmt.Sprintf("version: 1\njobs:\n  - name: uno\n    every: 1m\n    retries: { max: %d }\n    request: { url: https://esempio.it/x }\n", v)
		}},
	}

	for _, caso := range casi {
		t.Run(caso.colonna, func(t *testing.T) {
			match := regexp.MustCompile(caso.colonna + `[^,]*BETWEEN (\d+) AND (\d+)`).FindStringSubmatch(sql)
			if match == nil {
				t.Fatalf("la colonna %s non dichiara più un intervallo: il confronto con il parser va rifatto", caso.colonna)
			}
			basso, _ := strconv.Atoi(match[1])
			alto, _ := strconv.Atoi(match[2])

			for _, valore := range []int{basso - 1, basso, alto, alto + 1} {
				dentro := valore >= basso && valore <= alto
				_, err := cronyaml.Parse(t.Context(), yamlSource(caso.scrivi(valore)), options())
				if (err == nil) != dentro {
					t.Errorf("%s = %d: il database lo vuole dentro=%v, il parser accettato=%v\n  %v",
						caso.colonna, valore, dentro, err == nil, err)
				}
			}
		})
	}
}
