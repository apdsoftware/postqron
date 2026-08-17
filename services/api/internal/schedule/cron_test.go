package schedule

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// mustCron compila un'espressione che il test dà per valida.
func mustCron(t *testing.T, expr, tz string) *Cron {
	t.Helper()
	c, err := ParseCron(expr, tz)
	if err != nil {
		t.Fatalf("ParseCron(%q, %q): %v", expr, tz, err)
	}
	return c
}

// utc costruisce un istante UTC dalla sua forma RFC 3339.
func utc(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("istante %q non interpretabile: %v", s, err)
	}
	return parsed.UTC()
}

func TestParseCronAccetta(t *testing.T) {
	valide := []string{
		"* * * * *",
		"0 9 * * *",
		"*/15 * * * *",
		"0 0 1 1 *",
		"30 2 * * *",
		"0 9 * * MON-FRI",
		"0 0 * JAN,JUL *",
		"5-10 * * * *",
		"0-30/10 * * * *",
		"0 0 * * 7",
		"0 0 * * 0",
		"0 0 29 2 *",
		"0,15,30,45 8-18 * * 1-5",
		"0 0 * * sun",
		"  0   9  *  *  *  ", // spazi ridondanti
	}
	for _, expr := range valide {
		if _, err := ParseCron(expr, "UTC"); err != nil {
			t.Errorf("ParseCron(%q) ha rifiutato un'espressione valida: %v", expr, err)
		}
	}
}

func TestParseCronRifiuta(t *testing.T) {
	casi := []struct {
		expr    string
		attende string // frammento che il messaggio deve contenere
	}{
		{"* * * *", "5 campi"},
		{"* * * * * *", "every"},
		{"0 0 * * * *", "every"},
		{"@daily", `"0 0 * * *"`},
		{"@hourly", `"0 * * * *"`},
		{"@reboot", "cinque campi"},
		{"", ""},
		{"60 * * * *", "fuori dall'intervallo"},
		{"* 24 * * *", "fuori dall'intervallo"},
		{"0 0 32 * *", "fuori dall'intervallo"},
		{"0 0 0 * *", "fuori dall'intervallo"},
		{"0 0 * 13 *", "fuori dall'intervallo"},
		{"0 0 * * 8", "fuori dall'intervallo"},
		{"*/0 * * * *", "passo"},
		{"*/-1 * * * *", "passo"},
		{"*/a * * * *", "passo"},
		{"5/15 * * * *", "il passo si applica"},
		{"10-5 * * * *", "rovesciato"},
		{"1-99 * * * *", "fuori dall'intervallo"},
		{"1-x * * * *", "non è un numero"},
		{"1-5/0 * * * *", "passo"},
		{"a * * * *", "non è un numero"},
		{"0 0 * * MONDAY", "nome ammesso"},
		{"0,,15 * * * *", "elemento vuoto"},
		{"0, * * * *", "elemento vuoto"},
		{"-5 * * * *", "valore mancante"},
		{"0 0 ? * *", "non è un numero"},
		{"0 0 L * *", "non è un numero"},
		{"0 0 * * 5#2", "nome ammesso"},
	}
	for _, caso := range casi {
		_, err := ParseCron(caso.expr, "UTC")
		if err == nil {
			t.Errorf("ParseCron(%q) ha accettato un'espressione non valida", caso.expr)
			continue
		}
		if caso.attende != "" && !strings.Contains(err.Error(), caso.attende) {
			t.Errorf("ParseCron(%q) = %q, atteso un messaggio che contenga %q", caso.expr, err, caso.attende)
		}
	}
}

// L'espressione a sei campi è l'errore che ci si aspetta da chi cerca la
// risoluzione sub-minuto: il messaggio deve indirizzarlo all'altra modalità,
// non limitarsi a dire che ha sbagliato a contare i campi.
func TestParseCronSeiCampiRimandaAEvery(t *testing.T) {
	_, err := ParseCron("*/10 * * * * *", "UTC")
	if err == nil {
		t.Fatal("un'espressione a 6 campi dev'essere rifiutata")
	}
	if !strings.Contains(err.Error(), "`every`") {
		t.Errorf("messaggio = %q, atteso un rimando alla modalità a intervallo", err)
	}
}

// Chi valida un cron.yaml deve poter dire *quale* campo è sbagliato, non solo
// che l'espressione lo è (SPEC §9: errori con riga e colonna).
func TestParseCronErroreIdentificaIlCampo(t *testing.T) {
	_, err := ParseCron("0 0 * 13 *", "UTC")
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("errore = %T (%v), atteso *FieldError", err, err)
	}
	if fieldErr.Field != "mese" {
		t.Errorf("campo = %q, atteso %q", fieldErr.Field, "mese")
	}
	if fieldErr.Value != "13" {
		t.Errorf("valore = %q, atteso %q", fieldErr.Value, "13")
	}
}

func TestParseCronFusoOrario(t *testing.T) {
	if _, err := ParseCron("0 9 * * *", "Nowhere/Nothing"); err == nil {
		t.Error("un fuso inesistente dev'essere rifiutato")
	}
	if _, err := ParseCron("0 9 * * *", "Local"); err == nil {
		t.Error(`il fuso "Local" dev'essere rifiutato: dipende dall'host`)
	}
	c := mustCron(t, "0 9 * * *", "")
	if c.Timezone() != "UTC" {
		t.Errorf("fuso vuoto = %q, atteso UTC", c.Timezone())
	}
}

func TestCronString(t *testing.T) {
	c := mustCron(t, "  30   2 * * *", "Europe/Rome")
	if got, want := c.String(), "30 2 * * * [Europe/Rome]"; got != want {
		t.Errorf("String() = %q, atteso %q", got, want)
	}
}
