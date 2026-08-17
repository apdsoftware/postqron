package schedule

import (
	"slices"
	"time"
)

// wallClock è un orario da calendario senza fuso: «29 marzo 2026, 02:30».
//
// Non è un istante, ed è per questo che non è un time.Time: il 29 marzo 2026
// alle 02:30 a Roma non esiste nessun istante, e il 25 ottobre ne esistono due.
// Tenere separate le due nozioni — l'orario che l'utente scrive e l'istante in
// cui il job parte — è ciò che permette di decidere il caso patologico invece
// di subirlo.
//
// Dentro c'è comunque un time.Time, tenuto in UTC per convenzione: l'aritmetica
// di calendario (fine mese, anni bisestili) è già scritta e corretta, e UTC è
// l'unico fuso in cui nessun salto d'ora la può falsare. La Location del campo
// non ha alcun significato e non va mai letta.
type wallClock struct{ t time.Time }

// wallOf estrae l'orario da parete di un istante, così come lo si leggerebbe su
// un orologio nel fuso di `t`, troncato al minuto.
func wallOf(t time.Time) wallClock {
	y, mo, d := t.Date()
	h, mi, _ := t.Clock()
	return wallClock{time.Date(y, mo, d, h, mi, 0, 0, time.UTC)}
}

func (w wallClock) year() int         { return w.t.Year() }
func (w wallClock) month() time.Month { return w.t.Month() }
func (w wallClock) day() int          { return w.t.Day() }
func (w wallClock) hour() int         { return w.t.Hour() }
func (w wallClock) minute() int       { return w.t.Minute() }

// weekdayIndex è il giorno della settimana con la numerazione di cron: 0 è
// domenica.
func (w wallClock) weekdayIndex() int { return int(w.t.Weekday()) }

// nextMinute avanza di un minuto.
func (w wallClock) nextMinute() wallClock { return wallClock{w.t.Add(time.Minute)} }

// nextHour avanza all'ora successiva, azzerando i minuti.
func (w wallClock) nextHour() wallClock {
	y, mo, d := w.t.Date()
	return wallClock{time.Date(y, mo, d, w.t.Hour()+1, 0, 0, 0, time.UTC)}
}

// nextDay avanza al giorno successivo, a mezzanotte.
func (w wallClock) nextDay() wallClock {
	y, mo, d := w.t.Date()
	return wallClock{time.Date(y, mo, d+1, 0, 0, 0, 0, time.UTC)}
}

// nextMonth avanza al primo giorno del mese successivo, a mezzanotte.
func (w wallClock) nextMonth() wallClock {
	y, mo, _ := w.t.Date()
	return wallClock{time.Date(y, mo+1, 1, 0, 0, 0, 0, time.UTC)}
}

func (w wallClock) String() string { return w.t.Format("2006-01-02 15:04") }

// resolution dice quale dei tre casi si è presentato nel tradurre un orario da
// parete in un istante. Serve ai test e ai messaggi diagnostici: il chiamante
// che vuole solo l'istante può ignorarla.
type resolution int

const (
	// resolutionExact — l'orario esiste una volta sola. È il caso di ogni
	// giorno dell'anno tranne due.
	resolutionExact resolution = iota
	// resolutionGap — l'orario non esiste: l'orologio è saltato oltre.
	// L'occorrenza viene spostata in avanti al primo istante che esiste.
	resolutionGap
	// resolutionAmbiguous — l'orario esiste due volte. Vale la prima.
	resolutionAmbiguous
)

func (r resolution) String() string {
	switch r {
	case resolutionGap:
		return "inesistente"
	case resolutionAmbiguous:
		return "ambiguo"
	default:
		return "esatto"
	}
}

// resolve traduce l'orario da parete nell'istante in cui il job deve partire,
// applicando le regole documentate nel commento di pacchetto: al primo istante
// esistente se l'orario è dentro un buco, alla prima delle due se è ripetuto.
//
// L'istante restituito è espresso nella Location `loc`.
func (w wallClock) resolve(loc *time.Location) (time.Time, resolution) {
	offsets := candidateOffsets(w.t, loc)

	// Un istante `x` corrisponde all'orario da parete `w` se e solo se
	// x + offset(x) == w. Per ogni offset plausibile calcoliamo il candidato e
	// verifichiamo che a quell'istante l'offset sia davvero quello: è il
	// controllo che distingue i tre casi senza doverne assumere nessuno.
	var valid []time.Time
	for _, off := range offsets {
		cand := w.t.Add(-time.Duration(off) * time.Second)
		if _, actual := cand.In(loc).Zone(); actual == off {
			valid = append(valid, cand)
		}
	}

	switch len(valid) {
	case 0:
		return w.gapEnd(loc, offsets), resolutionGap
	case 1:
		return valid[0].In(loc), resolutionExact
	default:
		earliest := slices.MinFunc(valid, func(a, b time.Time) int { return a.Compare(b) })
		return earliest.In(loc), resolutionAmbiguous
	}
}

// candidateOffsets raccoglie gli offset dal meridiano di Greenwich che possono
// valere per l'istante cercato.
//
// L'istante che corrisponde a un orario da parete dista da esso al massimo
// quanto l'offset stesso, cioè meno di quindici ore in entrambe le direzioni.
// Sondare il fuso in una manciata di punti dentro quella finestra basta a
// pescare gli offset in gioco: fra due sonde consecutive un fuso reale non
// cambia più di una volta, e un offset in più o in meno non fa danno perché
// ogni candidato viene comunque verificato.
func candidateOffsets(naive time.Time, loc *time.Location) []int {
	probes := [...]time.Duration{
		-26 * time.Hour, -15 * time.Hour, -2 * time.Hour, -time.Hour,
		0, time.Hour, 2 * time.Hour, 13 * time.Hour, 26 * time.Hour,
	}
	offsets := make([]int, 0, len(probes))
	for _, p := range probes {
		_, off := naive.Add(p).In(loc).Zone()
		if !slices.Contains(offsets, off) {
			offsets = append(offsets, off)
		}
	}
	return offsets
}

// gapEnd trova l'istante in cui finisce il buco che contiene l'orario `w`, che
// per la regola scelta è l'istante in cui il job parte.
//
// Il buco è delimitato: con `min` e `max` fra gli offset plausibili, l'istante
// `w - max` cade sicuramente prima del salto e `w - min` sicuramente dopo o
// sopra. In mezzo la funzione «istante → orario da parete» cresce di un secondo
// al secondo e fa un unico scalino verso l'alto, quindi una ricerca binaria
// trova il primo istante il cui orario da parete raggiunge `w`: è il salto.
func (w wallClock) gapEnd(loc *time.Location, offsets []int) time.Time {
	target := w.t.Unix()
	fallback := time.Date(w.year(), w.month(), w.day(), w.hour(), w.minute(), 0, 0, loc)

	if len(offsets) < 2 {
		// Irraggiungibile: con un solo offset in gioco non c'è nessun salto e
		// ogni orario da parete ha il suo istante. Se ci arriviamo, il fuso è
		// più strano di quanto questo codice sappia trattare: meglio un
		// istante reale scelto da time.Date che un valore inventato.
		return fallback
	}

	lo := target - int64(slices.Max(offsets))
	hi := target - int64(slices.Min(offsets))
	if wallSeconds(lo, loc) >= target || wallSeconds(hi, loc) <= target {
		// Stessa ragione di sopra: gli estremi non racchiudono il salto, quindi
		// l'ipotesi su cui la ricerca binaria si regge non vale qui.
		return fallback
	}

	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if wallSeconds(mid, loc) < target {
			lo = mid
		} else {
			hi = mid
		}
	}
	return time.Unix(hi, 0).In(loc)
}

// wallSeconds è l'orario da parete di un istante, espresso nella stessa unità
// del campo `t` di wallClock: secondi Unix a cui è sommato l'offset del fuso.
func wallSeconds(unix int64, loc *time.Location) int64 {
	_, off := time.Unix(unix, 0).In(loc).Zone()
	return unix + int64(off)
}
