package scheduler

import (
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/schedule"
)

// Il calcolo delle occorrenze non tocca il database, e si prova senza: il
// comportamento dopo un fermo lungo, il tetto per passata e il caso della
// schedulazione che finisce sono tutti esprimibili in memoria. Ciò che invece
// dipende da PostgreSQL — l'idempotenza, il riavvio, il piano della query calda
// — sta in scheduler_test.go e in plan_test.go, contro il database vero.

var origin = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

func mustEvery(t *testing.T, d time.Duration) schedule.Schedule {
	t.Helper()
	s, err := schedule.NewInterval(d)
	if err != nil {
		t.Fatalf("schedule.NewInterval(%s): %v", d, err)
	}
	return s
}

func mustCron(t *testing.T, expr, tz string) schedule.Schedule {
	t.Helper()
	s, err := schedule.ParseCron(expr, tz)
	if err != nil {
		t.Fatalf("schedule.ParseCron(%q): %v", expr, err)
	}
	return s
}

func defaultPolicy() Policy {
	return Policy{CatchUp: DefaultCatchUp, MaxPerJob: DefaultMaxPerJob}
}

func TestUnaSolaOccorrenzaDovutaAvanzaDiUnPasso(t *testing.T) {
	s := mustEvery(t, time.Minute)
	cursor := origin

	p := planOccurrences(s, cursor, origin, defaultPolicy())

	if len(p.due) != 1 || !p.due[0].Equal(cursor) {
		t.Fatalf("occorrenze dovute = %v, attesa la sola %s", p.due, cursor)
	}
	if !p.hasNext || !p.next.Equal(cursor.Add(time.Minute)) {
		t.Fatalf("prossima = %s (%t), attesa %s", p.next, p.hasNext, cursor.Add(time.Minute))
	}
	if p.capped || p.droppedCount != 0 {
		t.Fatalf("nessun arretrato atteso: capped=%t scartate=%d", p.capped, p.droppedCount)
	}
}

func TestNessunaOccorrenzaFuturaVieneAccodataInAnticipo(t *testing.T) {
	s := mustEvery(t, time.Minute)
	cursor := origin.Add(30 * time.Second)

	p := planOccurrences(s, cursor, origin, defaultPolicy())

	if len(p.due) != 0 {
		t.Fatalf("occorrenze dovute = %v, attesa nessuna", p.due)
	}
	if !p.next.Equal(cursor) {
		t.Fatalf("prossima = %s, attesa invariata a %s", p.next, cursor)
	}
}

func TestDentroLaFinestraSiRecuperanoTutteLeOccorrenzeMancate(t *testing.T) {
	// Un job al minuto, fermo tre minuti: la finestra di recupero è cinque.
	s := mustEvery(t, time.Minute)
	cursor := origin.Add(-3 * time.Minute)

	p := planOccurrences(s, cursor, origin, defaultPolicy())

	if len(p.due) != 4 {
		t.Fatalf("occorrenze dovute = %d (%v), attese 4", len(p.due), p.due)
	}
	for i, at := range p.due {
		want := cursor.Add(time.Duration(i) * time.Minute)
		if !at.Equal(want) {
			t.Fatalf("occorrenza %d = %s, attesa %s", i, at, want)
		}
	}
	if p.droppedCount != 0 {
		t.Fatalf("scartate = %d, attese 0: erano tutte dentro la finestra", p.droppedCount)
	}
	if !p.next.Equal(origin.Add(time.Minute)) {
		t.Fatalf("prossima = %s, attesa %s", p.next, origin.Add(time.Minute))
	}
}

func TestFuoriDallaFinestraLeOccorrenzeSiScartanoESiContano(t *testing.T) {
	// Fermo di venti minuti su un job al minuto, finestra di cinque: le prime
	// quindici occorrenze non si eseguono, le ultime cinque sì.
	s := mustEvery(t, time.Minute)
	cursor := origin.Add(-20 * time.Minute)

	p := planOccurrences(s, cursor, origin, defaultPolicy())

	if p.droppedCount != 15 {
		t.Fatalf("scartate = %d, attese 15", p.droppedCount)
	}
	if !p.droppedFrom.Equal(cursor) {
		t.Fatalf("prima scartata = %s, attesa %s", p.droppedFrom, cursor)
	}
	if want := origin.Add(-6 * time.Minute); !p.droppedTo.Equal(want) {
		t.Fatalf("ultima scartata = %s, attesa %s", p.droppedTo, want)
	}
	if p.droppedTruncated {
		t.Fatal("conteggio dichiarato troncato su quindici occorrenze")
	}
	if len(p.due) != 6 {
		// Da -5 minuti a origin compresi.
		t.Fatalf("occorrenze dovute = %d (%v), attese 6", len(p.due), p.due)
	}
	if want := origin.Add(-5 * time.Minute); !p.due[0].Equal(want) {
		t.Fatalf("prima dovuta = %s, attesa %s", p.due[0], want)
	}
}

func TestIlTettoPerPassataLasciaIlRestoAllaPassataSuccessiva(t *testing.T) {
	// Un job a un secondo fermo per tre minuti: dentro la finestra, ma sono 180
	// occorrenze e il tetto è 120.
	s := mustEvery(t, time.Second)
	cursor := origin.Add(-3 * time.Minute)
	policy := Policy{CatchUp: DefaultCatchUp, MaxPerJob: 120}

	first := planOccurrences(s, cursor, origin, policy)
	if len(first.due) != 120 {
		t.Fatalf("occorrenze dovute = %d, attese 120 (il tetto)", len(first.due))
	}
	if !first.capped {
		t.Fatal("arretrato non segnalato: la passata successiva non ripartirebbe subito")
	}
	if want := cursor.Add(120 * time.Second); !first.next.Equal(want) {
		t.Fatalf("prossima = %s, attesa %s: si riprende esattamente da dove ci si è fermati", first.next, want)
	}

	// La passata successiva, allo stesso istante, prende il resto e nulla si
	// perde per strada.
	second := planOccurrences(s, first.next, origin, policy)
	if len(second.due) != 61 {
		t.Fatalf("occorrenze dovute alla seconda passata = %d, attese 61", len(second.due))
	}
	if second.capped {
		t.Fatal("arretrato ancora segnalato: era finito")
	}
	if !second.due[0].Equal(first.next) {
		t.Fatalf("la seconda passata riparte da %s invece che da %s", second.due[0], first.next)
	}
}

func TestUnFermoLunghissimoNonBloccaLaPassata(t *testing.T) {
	// Un job a un secondo fermo per tre giorni: 259.200 occorrenze scavalcate,
	// oltre il tetto di scansione. Il conteggio si dichiara troncato e il motore
	// riparte comunque dall'occorrenza giusta.
	s := mustEvery(t, time.Second)
	cursor := origin.Add(-72 * time.Hour)

	p := planOccurrences(s, cursor, origin, defaultPolicy())

	if !p.droppedTruncated {
		t.Fatal("conteggio non dichiarato troncato: sarebbe un numero esatto sbagliato")
	}
	if p.droppedCount != scanLimit {
		t.Fatalf("scartate = %d, atteso il tetto di scansione %d", p.droppedCount, scanLimit)
	}
	if len(p.due) == 0 {
		t.Fatal("nessuna occorrenza dovuta: il motore non sarebbe ripartito")
	}
	if want := origin.Add(-DefaultCatchUp); p.due[0].Before(want) {
		t.Fatalf("prima dovuta = %s, prima del bordo della finestra %s", p.due[0], want)
	}
	if !p.due[0].Equal(origin.Add(-DefaultCatchUp)) {
		t.Fatalf("prima dovuta = %s, attesa esattamente sul bordo %s", p.due[0], origin.Add(-DefaultCatchUp))
	}
}

func TestUnaSchedulazioneSenzaFuturoNonLasciaUnaProssimaOccorrenza(t *testing.T) {
	// Il 30 febbraio non arriva mai: il job va tolto dall'indice del dispatch,
	// non riesaminato per sempre.
	s := mustCron(t, "0 0 30 2 *", "UTC")

	p := planOccurrences(s, origin, origin, defaultPolicy())

	if len(p.due) != 1 {
		// L'occorrenza corrente è dovuta comunque: è il passato che conta.
		t.Fatalf("occorrenze dovute = %v, attesa quella corrente", p.due)
	}
	if p.hasNext {
		t.Fatalf("prossima occorrenza = %s, attesa nessuna", p.next)
	}
}

func TestIlCronRispettaIlFusoDelJob(t *testing.T) {
	// Mezzanotte a Roma non è mezzanotte UTC: la schedulazione è ancorata
	// all'orologio da parete del job (R2). Qui si verifica solo che il motore
	// usi il fuso; le regole sull'ora legale sono di internal/schedule.
	s := mustCron(t, "0 0 * * *", "Europe/Rome")
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	p := planOccurrences(s, now, now, defaultPolicy())

	want := time.Date(2026, 8, 17, 22, 0, 0, 0, time.UTC) // mezzanotte del 18 a Roma, ora legale
	if !p.hasNext || !p.next.Equal(want) {
		t.Fatalf("prossima = %s, attesa %s", p.next.UTC(), want)
	}
}

func TestLaPrimaOccorrenzaDiUnJobNuovoEStrettamenteFutura(t *testing.T) {
	s := mustEvery(t, time.Minute)

	// `now` cade esattamente su un'occorrenza della griglia: la prima assegnata
	// dev'essere comunque quella dopo, o un job appena creato partirebbe
	// nell'istante in cui viene salvato.
	first, ok := firstOccurrence(s, origin)
	if !ok {
		t.Fatal("nessuna prima occorrenza per un intervallo")
	}
	if !first.After(origin) {
		t.Fatalf("prima occorrenza = %s, non successiva a %s", first, origin)
	}
}
