package schedule

import (
	"fmt"
	"math"
	"time"
)

// Interval è una schedulazione a intervallo: il job parte ogni N secondi. È la
// modalità che copre la risoluzione sub-minuto dei piani Pro e Team (R22),
// fuori portata per un'espressione cron.
//
// # L'ancoraggio
//
// «Ogni dieci secondi» non basta a individuare le occorrenze: serve sapere da
// quando si conta. Le occorrenze cadono a `epoch + k·intervallo`, con epoch
// l'inizio del tempo Unix. La scelta ha tre conseguenze volute:
//
//   - **Le occorrenze sono deterministiche.** Non dipendono da quando il
//     processo è partito, né da quando il job è stato creato. Un riavvio non
//     sposta la griglia, e due repliche del motore calcolano gli stessi
//     istanti: è la premessa dell'idempotenza di R4, che si appoggia a
//     `scheduled_for` come chiave.
//   - **Non c'è deriva.** «L'ultima esecuzione più l'intervallo» accumula il
//     ritardo di ogni dispatch; una griglia fissa no.
//   - **Gli istanti sono allineati.** Un `every: 10s` cade ai secondi 0, 10,
//     20…; un `every: 1h` allo scoccare dell'ora UTC. Per i fusi con offset non
//     intero — l'India è a +05:30 — «l'ora piena UTC» non è l'ora piena locale:
//     un intervallo non ha un orologio da parete, e chi ne vuole uno usa
//     `schedule`.
//
// # L'ora legale
//
// Un intervallo è una durata assoluta e i cambi d'ora non lo toccano: la
// griglia è calcolata sul tempo Unix, che di fusi non sa niente. Nel giorno in
// cui l'orologio di Roma perde un'ora, un `every: 1h` produce 23 esecuzioni; in
// quello in cui la guadagna, 25. È la differenza rispetto al cron, ed è il
// motivo per cui questa modalità esiste e non è solo una scorciatoia di
// sintassi.
type Interval struct {
	seconds int64
}

var _ Schedule = (*Interval)(nil)

// maxIntervalSeconds è il limite della colonna `jobs.every_seconds`, che è un
// `integer` di PostgreSQL. Un intervallo più lungo non sarebbe memorizzabile:
// tanto vale rifiutarlo qui, dove il messaggio è comprensibile, invece di
// lasciarlo fallire con un errore del driver.
const maxIntervalSeconds = math.MaxInt32

// NewInterval costruisce una schedulazione a intervallo. La durata dev'essere
// un numero intero di secondi, da 1 in su.
func NewInterval(d time.Duration) (*Interval, error) {
	switch {
	case d <= 0:
		return nil, fmt.Errorf("intervallo %s: dev'essere positivo", d)
	case d%time.Second != 0:
		// `jobs.every_seconds` è un intero di secondi. Accettare `1500ms` qui
		// vorrebbe dire troncarlo alla scrittura, e il job partirebbe con una
		// cadenza diversa da quella scritta nel `cron.yaml` senza che nessuno
		// lo abbia detto all'utente.
		return nil, fmt.Errorf("intervallo %s: dev'essere un numero intero di secondi", d)
	case d > maxIntervalSeconds*time.Second:
		return nil, fmt.Errorf("intervallo %s: il massimo è %d secondi", d, int64(maxIntervalSeconds))
	}
	return &Interval{seconds: int64(d / time.Second)}, nil
}

// Next restituisce la prima occorrenza della griglia strettamente successiva ad
// `after`, in UTC. Il secondo valore è sempre vero: una griglia infinita non ha
// buchi.
func (i *Interval) Next(after time.Time) (time.Time, bool) {
	// Divisione con arrotondamento verso il basso, non verso lo zero: prima
	// dell'epoch i secondi Unix sono negativi e il troncamento di Go
	// restituirebbe l'occorrenza sbagliata.
	seconds := after.Unix()
	k := seconds / i.seconds
	if seconds < 0 && k*i.seconds != seconds {
		k--
	}
	next := (k + 1) * i.seconds

	// `after.Unix()` tronca i nanosecondi: se `after` cade esattamente su
	// un'occorrenza ma con una frazione di secondo in più, il troncamento l'ha
	// già portato indietro sull'occorrenza stessa e `next` è comunque quella
	// dopo. La disuguaglianza resta stretta in entrambi i casi.
	return time.Unix(next, 0).UTC(), true
}

// Duration è la durata fra due occorrenze.
func (i *Interval) Duration() time.Duration { return time.Duration(i.seconds) * time.Second }

// String è la forma leggibile della schedulazione.
func (i *Interval) String() string { return "every " + i.Duration().String() }
