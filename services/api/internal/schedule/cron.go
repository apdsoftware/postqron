package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron è una schedulazione espressa come espressione cron a cinque campi,
// ancorata all'orologio da parete di un fuso.
//
// La sintassi accettata è quella del crontab classico: `*`, valori singoli,
// intervalli `a-b`, liste separate da virgola e passi `*/n` o `a-b/n`. I mesi
// accettano i nomi `JAN`–`DEC` e i giorni della settimana `SUN`–`SAT`, senza
// distinzione di maiuscole; `7` vale domenica come `0`.
//
// Non sono ammessi: le abbreviazioni `@daily` e simili, il campo dei secondi
// delle espressioni a sei campi, e le estensioni Quartz `?`, `L`, `W`, `#`.
// Ognuna viene rifiutata con un messaggio che dice cosa scrivere al suo posto.
type Cron struct {
	expr string
	loc  *time.Location

	minute bitmask
	hour   bitmask
	dom    bitmask
	month  bitmask
	dow    bitmask

	// Quando entrambi i campi dei giorni sono ristretti, l'espressione vale se
	// **almeno uno** dei due combacia; se uno dei due è `*`, valgono entrambi.
	// È la regola del cron di Vixie, e senza di essa `0 0 13 * 5` — «il 13 del
	// mese oppure ogni venerdì» — significherebbe «solo i venerdì 13».
	domRestricted bool
	dowRestricted bool
}

var _ Schedule = (*Cron)(nil)

// searchYears limita quanto in là si cerca prima di dichiarare che
// un'occorrenza non esiste. Otto anni coprono il caso peggiore reale, il 29
// febbraio: fra il 2096 e il 2104 ce ne sono otto di distanza, perché il 2100
// non è bisestile.
const searchYears = 8

// FieldError è un errore che sa a quale campo dell'espressione si riferisce.
// Serve a chi valida un `cron.yaml` e deve riportare l'errore all'utente con
// riga e colonna (SPEC §9): senza il campo, il messaggio direbbe solo che
// l'espressione è sbagliata, non dove.
type FieldError struct {
	// Field è il nome del campo, in italiano, come lo legge un utente.
	Field string
	// Value è il testo del campo così com'è stato scritto.
	Value string
	// Problem descrive che cosa non va.
	Problem string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("campo %s (%q): %s", e.Field, e.Value, e.Problem)
}

type fieldDef struct {
	name     string
	min, max int
	names    map[string]int
}

// I cinque campi, nell'ordine in cui compaiono nell'espressione. Il giorno
// della settimana arriva fino a 7 perché il crontab classico accetta sia `0`
// sia `7` per la domenica; la normalizzazione a `0` avviene subito dopo il
// parsing.
var cronFields = [5]fieldDef{
	{name: "minuti", min: 0, max: 59},
	{name: "ore", min: 0, max: 23},
	{name: "giorno del mese", min: 1, max: 31},
	{name: "mese", min: 1, max: 12, names: monthNames},
	{name: "giorno della settimana", min: 0, max: 7, names: dayNames},
}

var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var dayNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

// macroReplacements mappa le abbreviazioni del crontab sull'espressione
// equivalente. Non le accettiamo — il vincolo `jobs_schedule_shape_check` in
// `db/migrations/0005_jobs.up.sql` pretende cinque campi, e un'espressione che
// il parser accetta ma il database rifiuta è peggio di un rifiuto netto — ma le
// riconosciamo per poter dire che cosa scrivere al loro posto.
var macroReplacements = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// ParseCron compila un'espressione cron a cinque campi nel fuso indicato. Un
// fuso vuoto vale `UTC`.
func ParseCron(expr, timezone string) (*Cron, error) {
	loc, err := loadLocation(timezone)
	if err != nil {
		return nil, err
	}

	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return nil, ErrNoMode
	}
	if replacement, ok := macroReplacements[strings.ToLower(trimmed)]; ok {
		return nil, fmt.Errorf("abbreviazione %q non supportata: scrivere %q", trimmed, replacement)
	}
	if strings.HasPrefix(trimmed, "@") {
		return nil, fmt.Errorf("abbreviazione %q non supportata: serve un'espressione a cinque campi", trimmed)
	}

	fields := strings.Fields(trimmed)
	if len(fields) == 6 {
		return nil, fmt.Errorf(
			"espressione a 6 campi non supportata: il cron si ferma al minuto — per una risoluzione più fine usare la modalità a intervallo (`every`)")
	}
	if len(fields) != 5 {
		return nil, fmt.Errorf("espressione %q: servono 5 campi (minuti ore giorno mese giorno-settimana), ne sono stati trovati %d", trimmed, len(fields))
	}

	c := &Cron{expr: strings.Join(fields, " "), loc: loc}
	masks := [5]*bitmask{&c.minute, &c.hour, &c.dom, &c.month, &c.dow}
	for i, def := range cronFields {
		mask, err := parseField(fields[i], def)
		if err != nil {
			return nil, err
		}
		*masks[i] = mask
	}

	// `7` e `0` sono lo stesso giorno: fondiamo il bit alto su quello basso,
	// così il confronto con time.Weekday non deve più saperlo.
	if c.dow.has(7) {
		c.dow.clear(7)
		c.dow.set(0)
	}

	c.domRestricted = fields[2] != "*"
	c.dowRestricted = fields[4] != "*"

	return c, nil
}

// Next restituisce la prima occorrenza strettamente successiva ad `after`,
// espressa nel fuso del job.
//
// La ricerca avanza sull'orologio da parete, non sugli istanti: è l'orologio
// che l'espressione descrive. Ogni orario candidato viene poi tradotto in
// istante applicando le regole sull'ora legale, e scartato se non è
// strettamente successivo ad `after`. Quel confronto finale fa due lavori in
// uno: garantisce l'avanzamento e, senza codice dedicato, fa sì che le
// occorrenze collassate sullo stesso istante dentro un buco di ora legale
// producano una sola esecuzione.
func (c *Cron) Next(after time.Time) (time.Time, bool) {
	w := wallOf(after.In(c.loc)).nextMinute()
	limit := w.year() + searchYears

	for w.year() <= limit {
		switch {
		case !c.month.has(int(w.month())):
			w = w.nextMonth()
		case !c.matchesDay(w):
			w = w.nextDay()
		case !c.hour.has(w.hour()):
			w = w.nextHour()
		case !c.minute.has(w.minute()):
			w = w.nextMinute()
		default:
			t, _ := w.resolve(c.loc)
			if t.After(after) {
				return t, true
			}
			w = w.nextMinute()
		}
	}
	return time.Time{}, false
}

// matchesDay applica la regola dei due campi dei giorni descritta su
// [Cron.domRestricted].
func (c *Cron) matchesDay(w wallClock) bool {
	dom := c.dom.has(w.day())
	dow := c.dow.has(w.weekdayIndex())
	if c.domRestricted && c.dowRestricted {
		return dom || dow
	}
	return dom && dow
}

// String restituisce l'espressione normalizzata seguita dal fuso.
func (c *Cron) String() string { return c.expr + " [" + c.loc.String() + "]" }

// Timezone è il nome IANA del fuso in cui l'espressione va letta.
func (c *Cron) Timezone() string { return c.loc.String() }

// parseField compila un campo dell'espressione nell'insieme dei valori
// ammessi.
func parseField(field string, def fieldDef) (bitmask, error) {
	fail := func(format string, args ...any) (bitmask, error) {
		return 0, &FieldError{Field: def.name, Value: field, Problem: fmt.Sprintf(format, args...)}
	}
	var mask bitmask
	for _, term := range strings.Split(field, ",") {
		if term == "" {
			return fail("elemento vuoto nella lista")
		}

		body, step := term, 1
		if slash := strings.Index(term, "/"); slash >= 0 {
			body = term[:slash]
			raw := term[slash+1:]
			n, err := strconv.Atoi(raw)
			if err != nil {
				return fail("passo %q non è un numero", raw)
			}
			if n < 1 {
				return fail("il passo dev'essere almeno 1, trovato %d", n)
			}
			step = n
			// Il crontab classico ammette il passo solo dopo `*` o dopo un
			// intervallo: `5/15` non vuol dire niente di definito — chi lo
			// scrive di solito intende `5-59/15` — e accettarlo con
			// un'interpretazione a caso è peggio che rifiutarlo.
			if body != "*" && !strings.Contains(body, "-") {
				return fail("il passo si applica a `*` o a un intervallo: scrivere %q oppure %q", body+"-"+strconv.Itoa(def.max)+"/"+raw, "*/"+raw)
			}
		}

		lo, hi, err := parseBounds(body, def)
		if err != nil {
			return fail("%s", err)
		}
		for v := lo; v <= hi; v += step {
			mask.set(v)
		}
	}
	return mask, nil
}

// parseBounds interpreta la parte di un elemento che precede l'eventuale passo:
// `*`, un valore singolo o un intervallo `a-b`.
func parseBounds(body string, def fieldDef) (int, int, error) {
	if body == "*" {
		return def.min, def.max, nil
	}
	if dash := strings.Index(body, "-"); dash >= 0 {
		lo, err := parseValue(body[:dash], def)
		if err != nil {
			return 0, 0, err
		}
		hi, err := parseValue(body[dash+1:], def)
		if err != nil {
			return 0, 0, err
		}
		if lo > hi {
			// Il crontab classico non ammette intervalli che scavalcano il
			// fondo scala. `FRI-MON` sembra sensato ma non lo è: rifiutarlo
			// costringe a scrivere `FRI-SAT,SUN-MON`, che dice la stessa cosa
			// senza ambiguità.
			return 0, 0, fmt.Errorf("intervallo rovesciato: %d è maggiore di %d", lo, hi)
		}
		return lo, hi, nil
	}
	v, err := parseValue(body, def)
	if err != nil {
		return 0, 0, err
	}
	return v, v, nil
}

// parseValue interpreta un singolo valore, numerico o per nome.
func parseValue(text string, def fieldDef) (int, error) {
	if text == "" {
		return 0, fmt.Errorf("valore mancante")
	}
	if def.names != nil {
		if v, ok := def.names[strings.ToUpper(text)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(text)
	if err != nil {
		if def.names != nil {
			return 0, fmt.Errorf("%q non è né un numero né un nome ammesso", text)
		}
		return 0, fmt.Errorf("%q non è un numero", text)
	}
	if v < def.min || v > def.max {
		return 0, fmt.Errorf("%d è fuori dall'intervallo ammesso %d-%d", v, def.min, def.max)
	}
	return v, nil
}

// bitmask è l'insieme dei valori ammessi per un campo. Nessun campo di
// un'espressione cron supera i 64 valori distinti, quindi ci sta tutto in una
// parola.
type bitmask uint64

func (b bitmask) has(v int) bool { return v >= 0 && v < 64 && b&(1<<uint(v)) != 0 }
func (b *bitmask) set(v int)     { *b |= 1 << uint(v) }
func (b *bitmask) clear(v int)   { *b &^= 1 << uint(v) }
