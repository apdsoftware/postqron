package jobs

import (
	"fmt"
	"time"
)

// Plan è la parte della matrice di SPEC §8 che vincola un job (R15).
//
// Arriva dalla tabella `plans`, che è la fonte di verità: i limiti sono dati,
// non costanti nel codice, e correggerli non richiede un deploy (vedi il
// commento della migrazione 0003). Qui se ne legge un sottoinsieme — quello che
// la scrittura di un job deve verificare — e nient'altro.
type Plan struct {
	// Code è il codice del piano (`free`, `pro`, `team`, `agency`).
	Code string
	// Name è il nome commerciale, per i messaggi d'errore.
	Name string

	// MaxJobs è il tetto rigido al numero di job. nil significa «nessun tetto
	// rigido»: è il caso di Team e Agency, venduti come illimitati.
	MaxJobs *int
	// FairUseJobs è la soglia dichiarata di uso corretto per i piani senza
	// tetto rigido. La 0003 la tiene distinta da MaxJobs proprio perché un
	// limite commerciale morbido non deve diventare per errore un rifiuto
	// secco: qui non rifiuta niente, serve al budget dei trigger manuali e a
	// ciò che il client mostra.
	FairUseJobs *int

	// MinInterval è la risoluzione minima concessa (SPEC §8: 1 minuto su Free,
	// 10 secondi su Pro, 1 secondo su Team e Agency).
	MinInterval time.Duration

	// LogRetention è la conservazione dei log di esecuzione (R6).
	LogRetention time.Duration

	// EnvironmentsEnabled indica se il piano ha staging oltre a production
	// (R23). Su Free è falso: un solo ambiente, che è `production`.
	EnvironmentsEnabled bool
}

// FreePlan è il piano di riserva quando un utente non ha una sottoscrizione
// viva.
//
// Non è un default inventato: sono esattamente i valori che la migrazione 0003
// inserisce per il codice `free`. Esiste perché la registrazione (#396) non crea
// una riga in `subscriptions`, e finché non lo farà un utente appena iscritto
// deve comunque avere entitlement definiti — quelli minimi. Se un giorno il
// listino cambia, l'unica copia che conta resta quella nel database: questa
// serve solo a non lasciare l'utente senza piano.
var FreePlan = Plan{
	Code:                "free",
	Name:                "Free",
	MaxJobs:             intPtr(20),
	MinInterval:         time.Minute,
	LogRetention:        3 * 24 * time.Hour,
	EnvironmentsEnabled: false,
}

func intPtr(v int) *int { return &v }

// label è il nome da usare nei messaggi.
func (p Plan) label() string {
	if p.Name != "" {
		return p.Name
	}
	return p.Code
}

// CheckResolution verifica che l'intervallo richiesto sia concesso dal piano.
//
// Riguarda solo la modalità a intervallo: un'espressione cron ha granularità
// minima di un minuto per costruzione (SPEC §9), quindi non può violare nemmeno
// la risoluzione più larga del listino.
func (p Plan) CheckResolution(every time.Duration) error {
	if every <= 0 || p.MinInterval <= 0 || every >= p.MinInterval {
		return nil
	}
	return &PlanLimitError{
		Limit: LimitResolution,
		Plan:  p.Code,
		Field: "every",
		message: fmt.Sprintf(
			"il piano %s consente una risoluzione minima di %s: `every: %s` non è ammesso. Passa a un piano superiore oppure allarga l'intervallo.",
			p.label(), FormatDuration(p.MinInterval), FormatDuration(every)),
	}
}

// CheckJobCount verifica il tetto al numero di job.
//
// `current` è il numero di job non archiviati dell'utente. Contano anche quelli
// in pausa: `enabled = false` è una scelta reversibile dell'utente, e un job in
// pausa occupa comunque un posto nel suo catalogo. Contare solo quelli attivi
// renderebbe il tetto aggirabile creando job spenti e accendendoli a rotazione.
func (p Plan) CheckJobCount(current int) error {
	if p.MaxJobs == nil || current < *p.MaxJobs {
		return nil
	}
	return &PlanLimitError{
		Limit: LimitJobs,
		Plan:  p.Code,
		Field: "",
		message: fmt.Sprintf(
			"il piano %s consente %d job e ne hai già %d. Elimina un job esistente oppure passa a un piano superiore.",
			p.label(), *p.MaxJobs, current),
	}
}

// CheckEnvironments verifica che gli ambienti richiesti siano concessi (R23).
func (p Plan) CheckEnvironments(envs []Environment) error {
	if p.EnvironmentsEnabled {
		return nil
	}
	for _, env := range envs {
		if env != EnvironmentProduction {
			return &PlanLimitError{
				Limit: LimitEnvironments,
				Plan:  p.Code,
				Field: "environments",
				message: fmt.Sprintf(
					"il piano %s ha un solo ambiente (`production`): `%s` richiede un piano superiore.",
					p.label(), env),
			}
		}
	}
	if len(envs) > 1 {
		return &PlanLimitError{
			Limit:   LimitEnvironments,
			Plan:    p.Code,
			Field:   "environments",
			message: fmt.Sprintf("il piano %s ha un solo ambiente.", p.label()),
		}
	}
	return nil
}

// ManualBudget è il tetto aggregato ai trigger manuali di un utente: quante
// esecuzioni manuali in quanto tempo.
//
// Il numero **non è una riga nuova del listino**, ed è deliberato: è la stessa
// portata che la schedulazione del piano già concede. Un piano che permette N
// job alla risoluzione R può produrre N esecuzioni ogni R senza che nessuno lo
// consideri abuso; i trigger manuali si misurano con quello stesso metro. Free:
// 20 al minuto. Pro: 200 ogni 10 secondi. Team: 1.000 al secondo, dalla soglia
// di fair use.
//
// Il secondo valore è falso quando il piano non dichiara né tetto né soglia —
// il caso di Agency, venduto come illimitato: lì non c'è un numero da cui
// derivare, e inventarne uno sarebbe una decisione commerciale presa dal codice.
//
// Il tetto **per singolo job** non passa di qui: lo applica la chiave naturale
// di `job_executions`, vedi [Service.Trigger]. I due si dividono il lavoro in
// un modo che vale la pena rendere esplicito, perché non è ovvio:
//
//   - Sui piani con un tetto rigido al numero di job — Free e Pro — l'aggregato
//     **non scatta mai**, ed è corretto così: N job producono al massimo N
//     esecuzioni manuali per casella, e N non può superare `max_jobs`, che è il
//     burst. Il tetto per job basta da solo.
//   - Sui piani venduti come illimitati è l'unico che tiene. Un utente Team con
//     cinquemila job supererebbe di cinque volte la propria soglia di fair use
//     senza che nessuna casella si opponga, perché ogni job ha la sua. È lì che
//     questo tetto trasforma una soglia dichiarata in un limite applicato.
//
// È anche il punto in cui la issue #398 innesterà le quote generali di R10: la
// firma che serve è già quella di un limitatore per chiave utente.
func (p Plan) ManualBudget() (burst int, window time.Duration, ok bool) {
	window = p.MinInterval
	if window <= 0 {
		window = time.Minute
	}
	switch {
	case p.MaxJobs != nil:
		return *p.MaxJobs, window, true
	case p.FairUseJobs != nil:
		return *p.FairUseJobs, window, true
	default:
		return 0, 0, false
	}
}

// FormatDuration scrive una durata come la scriverebbe un `cron.yaml`: `1s`,
// `10s`, `5m`, `1h`.
//
// È la forma che l'API usa anche nelle risposte, ed è deliberato: `1m0s` di Go
// è leggibile ma non riscrivibile nel file da cui l'errore proviene, e un
// contratto in cui ciò che si legge non si può rimandare indietro costringe ogni
// client a scrivere una conversione.
func FormatDuration(d time.Duration) string {
	seconds := int64(d / time.Second)
	switch {
	case seconds <= 0:
		return d.String()
	case seconds%3600 == 0:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds%60 == 0:
		return fmt.Sprintf("%dm", seconds/60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
