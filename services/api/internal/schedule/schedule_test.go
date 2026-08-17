package schedule

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Il vincolo XOR è lo stesso che il database esprime con
// `jobs_schedule_xor_every_check`: esattamente una delle due modalità.
func TestParseEsclusivitaDelleDueModalita(t *testing.T) {
	if _, err := Parse(Spec{}); !errors.Is(err, ErrNoMode) {
		t.Errorf("nessuna modalità = %v, atteso ErrNoMode", err)
	}
	if _, err := Parse(Spec{Timezone: "Europe/Rome"}); !errors.Is(err, ErrNoMode) {
		t.Errorf("solo il fuso = %v, atteso ErrNoMode", err)
	}
	if _, err := Parse(Spec{Expression: "0 9 * * *", Every: 10 * time.Second}); !errors.Is(err, ErrBothModes) {
		t.Errorf("entrambe le modalità = %v, atteso ErrBothModes", err)
	}
	// Uno `schedule` fatto di soli spazi non è una modalità dichiarata.
	if _, err := Parse(Spec{Expression: "   "}); !errors.Is(err, ErrNoMode) {
		t.Errorf("espressione vuota = %v, atteso ErrNoMode", err)
	}
}

// Il punto dell'astrazione: chi chiama non sa quale modalità ha in mano.
func TestParseRestituisceLaStessaAstrazione(t *testing.T) {
	casi := []struct {
		nome   string
		spec   Spec
		da     string
		atteso string
	}{
		{
			nome:   "cron",
			spec:   Spec{Expression: "0 9 * * *", Timezone: "Europe/Rome"},
			da:     "2026-08-17T00:00:00Z",
			atteso: "2026-08-17T07:00:00Z",
		},
		{
			nome:   "intervallo",
			spec:   Spec{Every: 10 * time.Second, Timezone: "Europe/Rome"},
			da:     "2026-08-17T09:00:03Z",
			atteso: "2026-08-17T09:00:10Z",
		},
	}
	for _, caso := range casi {
		s, err := Parse(caso.spec)
		if err != nil {
			t.Fatalf("%s: Parse: %v", caso.nome, err)
		}
		next, ok := s.Next(utc(t, caso.da))
		if !ok {
			t.Fatalf("%s: nessuna occorrenza", caso.nome)
		}
		if got := next.UTC().Format(time.RFC3339); got != caso.atteso {
			t.Errorf("%s: Next = %s, atteso %s", caso.nome, got, caso.atteso)
		}
		if s.String() == "" {
			t.Errorf("%s: String() vuota", caso.nome)
		}
	}
}

func TestParseTipiConcreti(t *testing.T) {
	s, err := Parse(Spec{Expression: "0 9 * * *"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := s.(*Cron); !ok {
		t.Errorf("tipo = %T, atteso *Cron", s)
	}
	s, err = Parse(Spec{Every: time.Minute})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, ok := s.(*Interval); !ok {
		t.Errorf("tipo = %T, atteso *Interval", s)
	}
}

// Il fuso non cambia il risultato di un intervallo, ma un refuso resta un
// errore: `defaults.timezone` di un `cron.yaml` si applica a tutti i job del
// file, e va segnalato subito.
func TestParseValidaIlFusoAncheConEvery(t *testing.T) {
	if _, err := Parse(Spec{Every: time.Minute, Timezone: "Europe/Roma"}); err == nil {
		t.Error("un fuso inesistente dev'essere rifiutato anche in modalità intervallo")
	}
	if _, err := Parse(Spec{Every: time.Minute, Timezone: "Local"}); err == nil {
		t.Error(`il fuso "Local" dev'essere rifiutato anche in modalità intervallo`)
	}
	if _, err := Parse(Spec{Every: time.Minute, Timezone: "Asia/Kolkata"}); err != nil {
		t.Errorf("un fuso valido non dev'essere un errore: %v", err)
	}
}

// Lo stesso intervallo produce gli stessi istanti in qualunque fuso sia
// dichiarato: è la verifica che il fuso resti davvero ininfluente e non entri
// nel calcolo per una svista.
func TestIntervalloIndipendenteDalFusoDichiarato(t *testing.T) {
	da := utc(t, "2026-10-25T00:30:00Z") // dentro l'ora doppia di Roma
	var riferimento []string
	for _, tz := range []string{"", "UTC", "Europe/Rome", "America/New_York", "Asia/Kolkata", "Pacific/Chatham"} {
		s, err := Parse(Spec{Every: time.Hour, Timezone: tz})
		if err != nil {
			t.Fatalf("fuso %q: Parse: %v", tz, err)
		}
		got := sequenza(t, s, da, 3)
		if riferimento == nil {
			riferimento = got
			continue
		}
		confronta(t, "intervallo con fuso "+tz, got, riferimento)
	}
}

func TestParseErroreDiEspressioneVienePropagato(t *testing.T) {
	_, err := Parse(Spec{Expression: "0 0 * 13 *"})
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("errore = %T (%v), atteso *FieldError", err, err)
	}
}

func TestParseErroreDiIntervalloVienePropagato(t *testing.T) {
	_, err := Parse(Spec{Every: 500 * time.Millisecond})
	if err == nil {
		t.Fatal("un intervallo sub-secondo dev'essere rifiutato")
	}
	if !strings.Contains(err.Error(), "numero intero di secondi") {
		t.Errorf("errore = %q, atteso un messaggio sui secondi interi", err)
	}
}

// Le due modalità coprono la matrice dei piani di SPEC §8: 1 minuto per Free,
// 10 secondi per Pro, 1 secondo per Team e Agency. Il cron arriva al minuto,
// l'intervallo al secondo — insieme non lasciano scoperto nessun piano.
func TestRisoluzioniDeiPianiSonoEsprimibili(t *testing.T) {
	casi := []struct {
		piano string
		spec  Spec
	}{
		{"Free — 1 minuto, cron", Spec{Expression: "* * * * *"}},
		{"Free — 1 minuto, intervallo", Spec{Every: time.Minute}},
		{"Pro — 10 secondi", Spec{Every: 10 * time.Second}},
		{"Team — 1 secondo", Spec{Every: time.Second}},
	}
	for _, caso := range casi {
		s, err := Parse(caso.spec)
		if err != nil {
			t.Errorf("%s: Parse: %v", caso.piano, err)
			continue
		}
		if _, ok := s.Next(utc(t, "2026-08-17T09:00:00Z")); !ok {
			t.Errorf("%s: nessuna occorrenza", caso.piano)
		}
	}
}
