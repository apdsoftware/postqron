// Package schedule risponde a una domanda sola: dato un job, qual è la prima
// occorrenza dopo un certo istante.
//
// # Due modalità, una sola astrazione
//
// Un job si schedula in uno di due modi (SPEC §9, R22), e il database li rende
// mutuamente esclusivi con un vincolo XOR su `jobs.schedule` e
// `jobs.every_seconds`:
//
//   - `schedule` — espressione cron a cinque campi, granularità minima 1 minuto;
//   - `every` — un intervallo, da 1 secondo in su, che è ciò che rende
//     raggiungibili i 10 secondi del piano Pro e il secondo del piano Team.
//
// Le due modalità stanno dietro la stessa interfaccia [Schedule]. Chi consuma
// questo pacchetto chiama [Schedule.Next] e non sa quale modalità abbia in
// mano: la scelta è risolta al momento del parsing, non a ogni dispatch. È una
// decisione deliberata di BACKLOG §E11 — «aggiungerlo dopo significa
// riscrivere il cuore del motore».
//
// # L'ora legale
//
// Un'espressione cron è ancorata all'orologio da parete di un fuso: «tutti i
// giorni alle 02:30 a Roma». Due volte l'anno quell'orologio mente, e la
// domanda «quando sono le 02:30?» ha zero risposte o due. Le regole qui sotto
// sono scelte, non ereditate dalla libreria sottostante: `time.Date` di Go, a
// cui sarebbe comodo delegare, risolve entrambi i casi in modo diverso da
// quello che vogliamo (per il buco sposta l'occorrenza di un'ora intera, per
// l'ora doppia sceglie la seconda passata).
//
// **Ora inesistente — «primavera».** L'ultima domenica di marzo a Roma
// l'orologio salta dalle 02:00 alle 03:00: le 02:30 non accadono. Un job
// dichiarato alle 02:30 **viene eseguito, al primo istante che esiste**, cioè
// esattamente all'istante del cambio (le 03:00 locali). Non lo si salta: un
// backup giornaliero che una volta l'anno non parte è un guasto silenzioso, e
// chi lo subisce non lo attribuisce mai all'ora legale. Se più occorrenze
// cadono dentro lo stesso buco — `*/15 2 * * *` ne ha quattro — collassano
// tutte sullo stesso istante e il job **parte una volta sola**: [Schedule.Next]
// restituisce sempre un istante strettamente successivo a quello richiesto,
// quindi il collasso non produce duplicati.
//
// **Ora doppia — «autunno».** L'ultima domenica di ottobre a Roma l'orologio
// torna dalle 03:00 alle 02:00: le 02:30 accadono due volte, a un'ora di
// distanza. Il job **parte solo alla prima**, quella con l'offset ancora
// estivo. Eseguirlo due volte violerebbe «una volta al giorno» in modo che il
// database non intercetta: le due occorrenze hanno `scheduled_for` diversi,
// quindi la chiave naturale di `job_executions` le accetterebbe entrambe come
// legittime (R4) e l'utente si troverebbe due fatture, due digest, due invii.
//
// La conseguenza di questa seconda regola va detta per intero: nel giorno
// dell'ora doppia **l'ora ripetuta viene saltata**. Un `*/30 * * * *` a Roma il
// 25 ottobre 2026 parte alle 02:00 e alle 02:30 (offset estivo) e poi non più
// fino alle 03:00 (offset invernale), che è novanta minuti dopo. È il
// comportamento corretto per una schedulazione ancorata all'orologio da parete,
// ed è anche il motivo per cui esiste l'altra modalità.
//
// **Gli intervalli non hanno ora legale.** `every` è una durata assoluta
// ancorata all'epoch Unix: le occorrenze cadono a `epoch + k·intervallo` e
// nessun cambio d'ora le sposta. Nel giorno dell'ora doppia un `every: 1h` a
// Roma produce 25 esecuzioni e un `0 * * * *` ne produce 24; nel giorno del
// buco, 23 contro 23. Non è un'incoerenza fra le due modalità: è la differenza
// fra «ogni ora» e «a ogni ora piena dell'orologio», e chi sceglie `every`
// sceglie la prima.
//
// # Fuso orario
//
// Il fuso è un nome IANA (`Europe/Rome`). La stringa vuota vale `UTC`, come il
// default della colonna `jobs.timezone`. `Local` è rifiutato: dipende
// dall'ambiente del processo, e uno scheduler che cambia comportamento a
// seconda della macchina su cui gira non è uno scheduler.
package schedule

import (
	"errors"
	"fmt"
	"strings"
	"time"

	// Il database dei fusi orari viene incorporato nel binario invece di
	// essere letto dal sistema. Senza, `time.LoadLocation("Europe/Rome")`
	// fallisce su qualunque immagine che non installi tzdata — le `scratch` e
	// le `distroless` non lo fanno — e fallirebbe *a runtime*, sul primo job
	// con un fuso esplicito, non alla build. Il costo è meno di un megabyte di
	// binario; l'alternativa è un guasto che si manifesta in produzione e solo
	// per alcuni clienti.
	_ "time/tzdata"
)

// Schedule è il modello di schedulazione del motore. L'unica cosa che sa fare è
// dire quando tocca al job la prossima volta.
type Schedule interface {
	// Next restituisce la prima occorrenza **strettamente successiva** ad
	// `after`. Il secondo valore è falso quando l'occorrenza non esiste:
	// succede alle espressioni cron su date impossibili (`0 0 30 2 *`), mai
	// agli intervalli.
	//
	// La stretta disuguaglianza è la garanzia su cui si appoggia il dispatch:
	// ripassare l'ultimo istante eseguito produce sempre un avanzamento, e
	// nessuna occorrenza può essere restituita due volte.
	Next(after time.Time) (time.Time, bool)

	// String è la forma leggibile della schedulazione, per log e messaggi
	// d'errore.
	String() string
}

// Spec è la schedulazione come arriva da fuori: dal database (`jobs.schedule`,
// `jobs.every_seconds`, `jobs.timezone`) o da un `cron.yaml` (SPEC §9). I due
// modi sono mutuamente esclusivi ed esattamente uno dev'essere presente.
type Spec struct {
	// Expression è l'espressione cron a cinque campi. Vuota se il job usa un
	// intervallo.
	Expression string
	// Every è l'intervallo. Zero se il job usa un'espressione cron.
	Every time.Duration
	// Timezone è un nome IANA. Vuoto vale `UTC`. Conta solo per la modalità
	// cron, ma viene validato in entrambe: un fuso scritto male è un errore
	// dell'utente anche quando non cambia il risultato.
	Timezone string
}

var (
	// ErrNoMode segnala una schedulazione che non dichiara né `schedule` né
	// `every`.
	ErrNoMode = errors.New("schedulazione assente: serve `schedule` (cron) oppure `every` (intervallo)")
	// ErrBothModes segnala una schedulazione che dichiara entrambe le
	// modalità. Non c'è una lettura ragionevole da preferire all'altra: è lo
	// stesso vincolo che il database esprime con `jobs_schedule_xor_every_check`.
	ErrBothModes = errors.New("`schedule` ed `every` sono mutuamente esclusivi: dichiararne uno solo")
)

// Parse costruisce la schedulazione dalla sua forma esterna, applicando il
// vincolo di esclusività fra le due modalità.
func Parse(spec Spec) (Schedule, error) {
	hasCron := strings.TrimSpace(spec.Expression) != ""
	hasEvery := spec.Every != 0

	switch {
	case hasCron && hasEvery:
		return nil, ErrBothModes
	case !hasCron && !hasEvery:
		return nil, ErrNoMode
	case hasCron:
		return ParseCron(spec.Expression, spec.Timezone)
	default:
		// Il fuso non serve a un intervallo, ma se è stato scritto va
		// controllato lo stesso: `defaults.timezone` in `cron.yaml` si applica
		// a tutti i job del file, e un refuso lì dentro deve emergere subito,
		// non il giorno in cui qualcuno converte quel job a `schedule`.
		if _, err := loadLocation(spec.Timezone); err != nil {
			return nil, err
		}
		return NewInterval(spec.Every)
	}
}

// loadLocation risolve un nome IANA. La stringa vuota vale `UTC`.
func loadLocation(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.UTC, nil
	}
	if name == "Local" {
		return nil, errors.New(`fuso orario "Local" non ammesso: dipende dall'ambiente del processo, serve un nome IANA esplicito (es. "Europe/Rome")`)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("fuso orario %q sconosciuto: %w", name, err)
	}
	return loc, nil
}
