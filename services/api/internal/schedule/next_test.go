package schedule

import (
	"testing"
	"time"
)

// sequenza raccoglie le prime `n` occorrenze dopo `from`, in RFC 3339 UTC.
// È la forma in cui i test leggono meglio: un elenco di istanti, non una
// catena di chiamate.
func sequenza(t *testing.T, s Schedule, from time.Time, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	cur := from
	for range n {
		next, ok := s.Next(cur)
		if !ok {
			t.Fatalf("%s: nessuna occorrenza dopo %s", s, cur.UTC().Format(time.RFC3339))
		}
		if !next.After(cur) {
			t.Fatalf("%s: Next(%s) = %s, non è successivo", s,
				cur.UTC().Format(time.RFC3339), next.UTC().Format(time.RFC3339))
		}
		out = append(out, next.UTC().Format(time.RFC3339))
		cur = next
	}
	return out
}

func confronta(t *testing.T, etichetta string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d occorrenze, attese %d\n  ottenute: %v\n  attese:   %v", etichetta, len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: occorrenza %d = %s, attesa %s", etichetta, i, got[i], want[i])
		}
	}
}

func TestCronNextGiornaliero(t *testing.T) {
	c := mustCron(t, "0 9 * * *", "UTC")
	confronta(t, "0 9 * * *",
		sequenza(t, c, utc(t, "2026-08-17T00:00:00Z"), 3),
		[]string{"2026-08-17T09:00:00Z", "2026-08-18T09:00:00Z", "2026-08-19T09:00:00Z"})
}

// Next è strettamente successivo: partire esattamente da un'occorrenza deve
// dare la successiva, mai la stessa. È la garanzia su cui si appoggia il
// dispatch quando ripassa l'ultimo istante eseguito.
func TestCronNextEStrettamenteSuccessivo(t *testing.T) {
	c := mustCron(t, "0 9 * * *", "UTC")
	next, ok := c.Next(utc(t, "2026-08-17T09:00:00Z"))
	if !ok {
		t.Fatal("nessuna occorrenza")
	}
	if got, want := next.UTC().Format(time.RFC3339), "2026-08-18T09:00:00Z"; got != want {
		t.Errorf("Next dall'occorrenza stessa = %s, atteso %s", got, want)
	}
}

// I secondi e i nanosecondi dell'istante di partenza non devono far saltare
// l'occorrenza del minuto in corso... né farla ripetere.
func TestCronNextIgnoraISecondiDiPartenza(t *testing.T) {
	c := mustCron(t, "* * * * *", "UTC")
	casi := map[string]string{
		"2026-08-17T09:00:00Z":           "2026-08-17T09:01:00Z",
		"2026-08-17T09:00:00.000000001Z": "2026-08-17T09:01:00Z",
		"2026-08-17T09:00:59Z":           "2026-08-17T09:01:00Z",
	}
	for from, want := range casi {
		next, ok := c.Next(utc(t, from))
		if !ok {
			t.Fatalf("nessuna occorrenza dopo %s", from)
		}
		if got := next.UTC().Format(time.RFC3339); got != want {
			t.Errorf("Next(%s) = %s, atteso %s", from, got, want)
		}
	}
}

func TestCronNextConPasso(t *testing.T) {
	c := mustCron(t, "*/15 * * * *", "UTC")
	confronta(t, "*/15 * * * *",
		sequenza(t, c, utc(t, "2026-08-17T09:07:00Z"), 4),
		[]string{"2026-08-17T09:15:00Z", "2026-08-17T09:30:00Z", "2026-08-17T09:45:00Z", "2026-08-17T10:00:00Z"})
}

func TestCronNextGiorniFeriali(t *testing.T) {
	// 2026-08-17 è un lunedì; il venerdì è il 21, il lunedì dopo il 24.
	c := mustCron(t, "0 9 * * MON-FRI", "UTC")
	confronta(t, "0 9 * * MON-FRI",
		sequenza(t, c, utc(t, "2026-08-20T12:00:00Z"), 3),
		[]string{"2026-08-21T09:00:00Z", "2026-08-24T09:00:00Z", "2026-08-25T09:00:00Z"})
}

// Domenica si scrive `0` o `7`: le due forme devono produrre gli stessi
// istanti.
func TestCronDomenicaZeroOSette(t *testing.T) {
	from := utc(t, "2026-08-17T00:00:00Z")
	zero := sequenza(t, mustCron(t, "0 0 * * 0", "UTC"), from, 3)
	sette := sequenza(t, mustCron(t, "0 0 * * 7", "UTC"), from, 3)
	confronta(t, "domenica come 0 contro 7", sette, zero)
	// 2026-08-23 è una domenica.
	confronta(t, "domenica", zero,
		[]string{"2026-08-23T00:00:00Z", "2026-08-30T00:00:00Z", "2026-09-06T00:00:00Z"})
}

// Regola del crontab classico: se **entrambi** i campi dei giorni sono
// ristretti, vale l'unione, non l'intersezione. `0 0 13 * FRI` è «il 13 del
// mese oppure ogni venerdì», non «i venerdì 13».
func TestCronGiornoDelMeseOppureDellaSettimana(t *testing.T) {
	c := mustCron(t, "0 0 13 * FRI", "UTC")
	// Novembre 2026: venerdì 6, venerdì 13, venerdì 20, venerdì 27.
	confronta(t, "13 del mese oppure venerdì",
		sequenza(t, c, utc(t, "2026-11-01T00:00:00Z"), 4),
		[]string{"2026-11-06T00:00:00Z", "2026-11-13T00:00:00Z", "2026-11-20T00:00:00Z", "2026-11-27T00:00:00Z"})
}

// Se uno dei due campi è `*`, l'unione diventerebbe «tutti i giorni»: in quel
// caso vale l'intersezione, cioè il solo campo ristretto.
func TestCronUnSoloCampoDeiGiorniRistretto(t *testing.T) {
	confronta(t, "solo lunedì",
		sequenza(t, mustCron(t, "0 0 * * MON", "UTC"), utc(t, "2026-11-01T00:00:00Z"), 3),
		[]string{"2026-11-02T00:00:00Z", "2026-11-09T00:00:00Z", "2026-11-16T00:00:00Z"})
	confronta(t, "solo il 13",
		sequenza(t, mustCron(t, "0 0 13 * *", "UTC"), utc(t, "2026-11-01T00:00:00Z"), 3),
		[]string{"2026-11-13T00:00:00Z", "2026-12-13T00:00:00Z", "2027-01-13T00:00:00Z"})
}

// `*/2` non è `*`: restringe, quindi fa scattare la regola dell'unione. È il
// comportamento del crontab di Vixie, che guarda la presenza dell'asterisco
// nudo e non l'insieme dei valori che ne risulta.
func TestCronPassoSuiGiorniRestringe(t *testing.T) {
	c := mustCron(t, "0 0 */10 * MON", "UTC")
	// Novembre 2026: giorni 1, 11, 21, 31 dal passo; lunedì 2, 9, 16, 23, 30.
	confronta(t, "*/10 oppure lunedì",
		sequenza(t, c, utc(t, "2026-11-01T00:00:00Z"), 5),
		[]string{
			"2026-11-02T00:00:00Z", "2026-11-09T00:00:00Z", "2026-11-11T00:00:00Z",
			"2026-11-16T00:00:00Z", "2026-11-21T00:00:00Z",
		})
}

func TestCronAnnoBisestile(t *testing.T) {
	c := mustCron(t, "0 0 29 2 *", "UTC")
	confronta(t, "29 febbraio",
		sequenza(t, c, utc(t, "2026-01-01T00:00:00Z"), 2),
		[]string{"2028-02-29T00:00:00Z", "2032-02-29T00:00:00Z"})
}

// Una data che non esiste in nessun calendario non ha occorrenze: il chiamante
// deve ricevere un «no», non un ciclo infinito.
func TestCronDataImpossibile(t *testing.T) {
	c := mustCron(t, "0 0 30 2 *", "UTC")
	if next, ok := c.Next(utc(t, "2026-01-01T00:00:00Z")); ok {
		t.Errorf("il 30 febbraio non esiste, ma Next ha restituito %s", next)
	}
}

func TestCronCambioDiMeseEDiAnno(t *testing.T) {
	c := mustCron(t, "0 0 1 * *", "UTC")
	confronta(t, "primo del mese",
		sequenza(t, c, utc(t, "2026-11-15T00:00:00Z"), 3),
		[]string{"2026-12-01T00:00:00Z", "2027-01-01T00:00:00Z", "2027-02-01T00:00:00Z"})
}

// Il fuso sposta l'istante ma non l'orario da parete: le 09:00 di Roma in
// agosto sono le 07:00 UTC, in gennaio le 08:00.
func TestCronFusoSenzaCambiDOra(t *testing.T) {
	c := mustCron(t, "0 9 * * *", "Europe/Rome")
	confronta(t, "09:00 a Roma d'estate",
		sequenza(t, c, utc(t, "2026-08-17T00:00:00Z"), 1),
		[]string{"2026-08-17T07:00:00Z"})
	confronta(t, "09:00 a Roma d'inverno",
		sequenza(t, c, utc(t, "2026-01-15T00:00:00Z"), 1),
		[]string{"2026-01-15T08:00:00Z"})
}

// Un fuso con offset non intero non deve confondere l'aritmetica.
func TestCronFusoConOffsetNonIntero(t *testing.T) {
	c := mustCron(t, "30 9 * * *", "Asia/Kolkata") // +05:30, senza ora legale
	confronta(t, "09:30 a Calcutta",
		sequenza(t, c, utc(t, "2026-08-17T00:00:00Z"), 2),
		[]string{"2026-08-17T04:00:00Z", "2026-08-18T04:00:00Z"})
}

// Next restituisce l'istante nel fuso del job: chi legge un log deve vedere
// l'orario che ha scritto nel cron.yaml.
func TestCronNextRestituisceIlFusoDelJob(t *testing.T) {
	c := mustCron(t, "0 9 * * *", "Europe/Rome")
	next, ok := c.Next(utc(t, "2026-08-17T00:00:00Z"))
	if !ok {
		t.Fatal("nessuna occorrenza")
	}
	if got, want := next.Format("2006-01-02 15:04 MST"), "2026-08-17 09:00 CEST"; got != want {
		t.Errorf("Next reso nel fuso del job = %q, atteso %q", got, want)
	}
}
