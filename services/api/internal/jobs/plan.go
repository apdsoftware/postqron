package jobs

import (
	"fmt"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
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

	// MultiWorkspace indica se il piano include workspace isolati (R25, SPEC §8:
	// solo Agency, fino a dieci).
	//
	// Qui non serve a contare i workspace — non esistono ancora come entità, vedi
	// la migrazione 0012 — ma a riconoscere **la forma del piano**: R25-bis dice
	// che Agency non è «Team con più potenza» ma «Team moltiplicato», e la
	// colonna che dichiara la moltiplicazione è questa. È ciò che permette a
	// [Service.Budget] di derivare la portata di Agency senza inventarla.
	MultiWorkspace bool
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

// Throughput è la portata che il piano già vende: quante operazioni in quanto
// tempo.
//
// Il numero **non è una riga nuova del listino**, ed è deliberato: è la stessa
// portata che la schedulazione del piano concede. Un piano che permette N job
// alla risoluzione R può produrre N esecuzioni ogni R senza che nessuno lo
// consideri abuso; ciò che l'utente chiede a mano si misura con quello stesso
// metro. Free: 20 al minuto. Pro: 200 ogni 10 secondi. Team: 1.000 al secondo,
// dalla soglia di fair use.
//
// Il secondo valore è falso quando il piano non dichiara né tetto né soglia — il
// caso di Agency, venduto come illimitato: lì non c'è un numero **in questa
// riga** da cui derivare. Non significa «nessun limite»: significa che la
// portata di Agency sta altrove, e R25-bis dice dove. La risoluzione è in
// [Service.Budget], perché richiede di leggere un secondo piano e un metodo su
// una struttura di soli dati non può farlo.
//
// # Chi la usa
//
// Due limiti distinti, con la stessa misura e contatori separati:
//
//   - il tetto aggregato ai **trigger manuali** (R8), applicato in
//     [Service.Trigger];
//   - la quota delle **scritture** dell'API pubblica (R10), applicata sulla riga
//     di routing in internal/httpapi.
//
// Contatori separati perché sono due poteri diversi — cambiare il catalogo dei
// job e far partire adesso una chiamata verso l'esterno — e la stessa misura
// perché la capacità che consumano è la stessa.
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
func (p Plan) Throughput() (ratelimit.Rule, bool) {
	window := p.MinInterval
	if window <= 0 {
		window = time.Minute
	}
	switch {
	case p.MaxJobs != nil:
		return ratelimit.Rule{Burst: *p.MaxJobs, Window: window}, true
	case p.FairUseJobs != nil:
		return ratelimit.Rule{Burst: *p.FairUseJobs, Window: window}, true
	default:
		return ratelimit.Rule{}, false
	}
}

// RetentionFloor è il momento oltre il quale il piano non conserva più i log di
// esecuzione (R6, SPEC §8: 3, 15, 30, 90 giorni).
//
// Zero quando il piano non dichiara una retention: senza un numero non c'è un
// confine, e inventarlo taglierebbe lo storico di qualcuno.
func (p Plan) RetentionFloor(now time.Time) time.Time {
	if p.LogRetention <= 0 {
		return time.Time{}
	}
	return now.Add(-p.LogRetention)
}

// CheckRetention verifica che la finestra richiesta stia dentro la retention del
// piano (R10-bis, R15).
//
// # Perché un rifiuto e non una finestra ristretta in silenzio
//
// Restringere senza dirlo darebbe all'utente meno righe di quelle che ha chiesto
// e nessun modo di sapere perché: aprirebbe un ticket, e avremmo speso lavoro per
// produrre confusione. Vale qui la stessa regola di [PlanLimitError]: un limite
// applicato in silenzio è indistinguibile, per chi lo subisce, da un guasto.
//
// # Questa è metà del lavoro
//
// Lo dice R10-bis e va ripetuto qui, dove qualcuno potrebbe concludere che la
// retention è applicata e togliere l'altra metà: **non sostituisce la
// cancellazione periodica** delle righe e delle partizioni — che da #393 vive in
// `internal/retention`, sulle funzioni di appoggio della migrazione 0006 — la
// copre soltanto nell'intervallo fra due passate. Il confine che quel package
// applica per riga è esattamente [Plan.RetentionFloor], e deve restare lo
// stesso: se divergessero, o spariscono righe che questa API sta ancora
// mostrando, o ne restano di illeggibili che la policy dichiara cancellate. La
// privacy policy dichiara che i log sono
// conservati per il periodo del piano e poi *cancellati*: nascondere righe che
// continuano a esistere renderebbe quel documento inesatto, e un documento legale
// inesatto è un problema peggiore di un limite non applicato.
func (p Plan) CheckRetention(since, until, now time.Time) error {
	floor := p.RetentionFloor(now)
	if floor.IsZero() {
		return nil
	}

	// `until` prima del confine descrive una finestra che sta tutta oltre la
	// retention: `since` da solo non basta a riconoscerla, perché un `until`
	// antico con `since` assente chiede comunque righe che il piano non conserva.
	field, requested := "since", since
	if since.IsZero() || (!until.IsZero() && until.Before(since)) {
		field, requested = "until", until
	}
	if requested.IsZero() || !requested.Before(floor) {
		return nil
	}

	return &PlanLimitError{
		Limit: LimitRetention,
		Plan:  p.Code,
		Field: field,
		message: fmt.Sprintf(
			"il piano %s conserva i log di esecuzione per %s: `%s` non può precedere il %s. Passa a un piano superiore per uno storico più lungo.",
			p.label(), formatRetention(p.LogRetention), field, floor.UTC().Format(time.RFC3339)),
	}
}

// formatRetention scrive una retention come la scrive il listino: in giorni.
//
// [FormatDuration] direbbe «72h», che è esatto e non è la lingua di SPEC §8 —
// dove i tre giorni del piano Free sono tre giorni. Non è un dettaglio estetico:
// il messaggio serve a far riconoscere all'utente la riga del listino che ha
// letto prima di iscriversi.
func formatRetention(d time.Duration) string {
	const day = 24 * time.Hour
	if d <= 0 || d%day != 0 {
		return FormatDuration(d)
	}
	days := int64(d / day)
	if days == 1 {
		return "1 giorno"
	}
	return fmt.Sprintf("%d giorni", days)
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
