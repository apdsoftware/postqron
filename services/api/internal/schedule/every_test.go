package schedule

import (
	"math"
	"strings"
	"testing"
	"time"
)

func mustInterval(t *testing.T, d time.Duration) *Interval {
	t.Helper()
	i, err := NewInterval(d)
	if err != nil {
		t.Fatalf("NewInterval(%s): %v", d, err)
	}
	return i
}

func TestNewIntervalRifiuta(t *testing.T) {
	casi := []struct {
		durata  time.Duration
		attende string
	}{
		{0, "positivo"},
		{-time.Second, "positivo"},
		{1500 * time.Millisecond, "numero intero di secondi"},
		{999 * time.Millisecond, "numero intero di secondi"},
		{(math.MaxInt32 + 1) * time.Second, "massimo"},
	}
	for _, caso := range casi {
		_, err := NewInterval(caso.durata)
		if err == nil {
			t.Errorf("NewInterval(%s) ha accettato una durata non valida", caso.durata)
			continue
		}
		if !strings.Contains(err.Error(), caso.attende) {
			t.Errorf("NewInterval(%s) = %q, atteso un messaggio che contenga %q", caso.durata, err, caso.attende)
		}
	}
}

// Il secondo è la risoluzione minima del piano Team (SPEC §8) e il minimo
// assoluto del vincolo `every_seconds >= 1`.
func TestNewIntervalAccettaIlSecondo(t *testing.T) {
	i := mustInterval(t, time.Second)
	if got := i.Duration(); got != time.Second {
		t.Errorf("Duration() = %s, attesa 1s", got)
	}
	if got, want := i.String(), "every 1s"; got != want {
		t.Errorf("String() = %q, atteso %q", got, want)
	}
}

// Le occorrenze cadono sulla griglia `epoch + k·intervallo`: non dipendono da
// quando il calcolo viene fatto, che è ciò che le rende ricostruibili dopo un
// riavvio.
func TestIntervalloAncoratoAllEpoch(t *testing.T) {
	casi := []struct {
		durata time.Duration
		da     string
		atteso string
	}{
		{10 * time.Second, "2026-08-17T09:00:03Z", "2026-08-17T09:00:10Z"},
		{10 * time.Second, "2026-08-17T09:00:09Z", "2026-08-17T09:00:10Z"},
		{10 * time.Second, "2026-08-17T09:00:10Z", "2026-08-17T09:00:20Z"},
		{time.Second, "2026-08-17T09:00:10Z", "2026-08-17T09:00:11Z"},
		{time.Minute, "2026-08-17T09:00:01Z", "2026-08-17T09:01:00Z"},
		{time.Hour, "2026-08-17T09:30:00Z", "2026-08-17T10:00:00Z"},
		{90 * time.Second, "2026-08-17T09:00:00Z", "2026-08-17T09:01:30Z"},
		{24 * time.Hour, "2026-08-17T09:00:00Z", "2026-08-18T00:00:00Z"},
	}
	for _, caso := range casi {
		i := mustInterval(t, caso.durata)
		next, ok := i.Next(utc(t, caso.da))
		if !ok {
			t.Fatalf("every %s: nessuna occorrenza dopo %s", caso.durata, caso.da)
		}
		if got := next.UTC().Format(time.RFC3339); got != caso.atteso {
			t.Errorf("every %s da %s = %s, atteso %s", caso.durata, caso.da, got, caso.atteso)
		}
	}
}

// La griglia non dipende da quale istante la si interroga: due chiamate a
// partire da momenti diversi dentro lo stesso intervallo danno la stessa
// occorrenza. È ciò che permette a due repliche del motore di calcolare gli
// stessi `scheduled_for` (R4).
func TestIntervalloDeterministicoInDueIstantiDiversi(t *testing.T) {
	i := mustInterval(t, 10*time.Second)
	a, _ := i.Next(utc(t, "2026-08-17T09:00:01Z"))
	b, _ := i.Next(utc(t, "2026-08-17T09:00:09Z"))
	if !a.Equal(b) {
		t.Errorf("occorrenze diverse (%s e %s) per lo stesso intervallo di griglia", a, b)
	}
}

// I nanosecondi non devono far restituire un istante non successivo.
func TestIntervalloConFrazioniDiSecondo(t *testing.T) {
	i := mustInterval(t, 10*time.Second)
	da := utc(t, "2026-08-17T09:00:09.999999999Z")
	next, _ := i.Next(da)
	if !next.After(da) {
		t.Errorf("Next(%s) = %s, non è successivo", da, next)
	}
	if got, want := next.UTC().Format(time.RFC3339), "2026-08-17T09:00:10Z"; got != want {
		t.Errorf("Next = %s, atteso %s", got, want)
	}
}

// Prima dell'epoch i secondi Unix sono negativi: l'arrotondamento dev'essere
// verso il basso, non verso lo zero, altrimenti la griglia si specchia.
func TestIntervalloPrimaDellEpoch(t *testing.T) {
	i := mustInterval(t, 10*time.Second)
	casi := map[string]string{
		"1969-12-31T23:59:55Z": "1970-01-01T00:00:00Z",
		"1969-12-31T23:59:41Z": "1969-12-31T23:59:50Z",
		"1969-12-31T23:59:50Z": "1970-01-01T00:00:00Z",
	}
	for da, atteso := range casi {
		next, _ := i.Next(utc(t, da))
		if got := next.UTC().Format(time.RFC3339); got != atteso {
			t.Errorf("Next(%s) = %s, atteso %s", da, got, atteso)
		}
	}
}

// Un intervallo non ha date impossibili: la seconda risposta è sempre vera.
func TestIntervalloHaSempreUnaOccorrenza(t *testing.T) {
	i := mustInterval(t, time.Second)
	if _, ok := i.Next(utc(t, "2200-01-01T00:00:00Z")); !ok {
		t.Error("un intervallo non dovrebbe mai esaurire le occorrenze")
	}
}

// Il risultato è in UTC: un intervallo non ha un orologio da parete a cui
// riferirsi.
func TestIntervalloRestituisceUTC(t *testing.T) {
	i := mustInterval(t, time.Minute)
	roma, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Fatalf("fuso non caricabile: %v", err)
	}
	next, _ := i.Next(utc(t, "2026-08-17T09:00:00Z").In(roma))
	if next.Location() != time.UTC {
		t.Errorf("Location() = %s, atteso UTC", next.Location())
	}
}
