package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/apikeys"
	"github.com/apdsoftware/postqron/services/api/internal/jobs"
	"github.com/apdsoftware/postqron/services/api/internal/ratelimit"
)

// R10 chiede due cose diverse — lo dice la spec, ed è questo file a tenerle
// diverse.
//
// **Il tetto tecnico** è una difesa del servizio. Vale per ogni chiamante, è lo
// stesso su tutti i piani e non è in vendita: nessun piano ne concede di più,
// perché non protegge una capacità venduta ma la macchina su cui gira tutto.
// Chi lo legge fra sei mesi non deve scambiarlo per un prezzo — non lo è, e
// alzarlo non è un upgrade, è una decisione di esercizio.
//
// **La quota di piano** è la capacità che l'utente ha comprato, e viene dalla
// matrice di SPEC §8: la stessa portata da cui #395 deriva il tetto ai trigger
// manuali, applicata alle scritture dell'API pubblica.
//
// La distinzione arriva fino alla risposta, ed è la ragione per cui questo file
// scrive due 429 diversi:
//
//   - `rate_limited` non promette niente e non nomina nessun piano. Suggerire un
//     upgrade a chi ha superato un tetto tecnico sarebbe una bugia commerciale:
//     l'upgrade non servirebbe.
//   - `plan_limit_write_rate` dice quale piano concede quanto, perché lì
//     l'upgrade è davvero la risposta. Un 429 muto costringerebbe l'utente a
//     indovinare se ha sbagliato lui o se deve pagare.
//
// # Perché entrambi stanno qui e non negli handler
//
// Per la stessa ragione per cui #397 ha messo gli scope in guard.scoped: un
// limite verificato dentro al singolo gestore è un limite che il gestore
// successivo dimentica, e la dimenticanza non si vede — la rotta funziona,
// funziona solo anche per chi doveva essere fermato. E perché un limite deve
// scattare **prima del lavoro**: un rifiuto emesso dopo la query ha già speso
// ciò che doveva proteggere.

// defaultRequestCeiling è il tetto tecnico per credenziale presentata.
//
// **Non è una riga del listino e non deve diventarlo.** SPEC §8 non dichiara un
// numero di richieste, e inventarne uno per piano sarebbe una decisione
// commerciale presa dal codice; questo invece è un numero di esercizio, della
// stessa famiglia di quelli che #396 ha scelto per l'autenticazione (20 login
// ogni quarto d'ora per indirizzo) e #397 per le chiavi (30 tentativi ogni
// cinque minuti). Sta nel codice perché è una scelta di sicurezza, non un
// parametro d'esercizio da configurare.
//
// Trecento richieste al minuto sono cinque al secondo sostenute, con una
// scorta di trecento per le raffiche: è molto sopra qualunque uso reale — una
// dashboard che si ricarica ogni cinque secondi ne fa una cinquantina — e molto
// sotto ciò che un ciclo produce. È il confine fra un client che lavora e uno
// che gira a vuoto, ed è quello che serve riconoscere.
var defaultRequestCeiling = ratelimit.Rule{Burst: 300, Window: time.Minute}

// planCacheTTL è per quanto la portata del piano resta valida in memoria.
//
// Il piano di un utente cambia quando cambia la sottoscrizione, cioè quasi mai
// e comunque non durante una raffica. Rileggerlo a ogni richiesta significa
// interrogare il database per decidere se rifiutare la richiesta che quel
// database doveva proteggere: il limitatore pagherebbe il prezzo che è lì per
// non far pagare.
//
// Un minuto è il compromesso: chi passa a un piano superiore vede la quota
// nuova entro un minuto — e nel frattempo ha quella vecchia, mai una inventata
// — mentre chi martella l'API paga una lettura al minuto invece di una per
// richiesta. La cache vale **solo per il limitatore**: la creazione di un job
// rilegge il piano vera ogni volta, perché lì una lettura stantia
// concederebbe un job che il piano non concede più.
const planCacheTTL = time.Minute

// maxCachedPlans limita la memoria della cache. Le chiavi sono identificativi di
// utenti autenticati, quindi non arrivano dall'esterno come quelle di
// [ratelimit.Limiter]; il tetto c'è comunque perché una mappa senza tetto in un
// processo di lunga vita è un problema che si scopre tardi.
const maxCachedPlans = 10_000

// RateLimits sono i tetti tecnici del servizio (R10).
//
// Il campo zero usa [defaultRequestCeiling]. Esiste perché i test devono poter
// provare il rifiuto senza fare trecento richieste.
type RateLimits struct {
	// Requests è il tetto per credenziale presentata.
	Requests ratelimit.Rule
}

// PlanBudgets è la sorgente della portata di un piano.
//
// È un'interfaccia e non `*jobs.Service` perché ciò che serve qui è una domanda
// sola — «quanto concede il piano di questo utente» — e perché le rotte non
// devono conoscere la matrice di SPEC §8: la conosce internal/jobs, che è il
// package a cui R15 appartiene.
type PlanBudgets interface {
	// Budget restituisce la portata del piano dell'utente, già risolta:
	// R25-bis compreso.
	Budget(ctx context.Context, userID string) (jobs.PlanBudget, error)
}

// writeScopes sono gli scope le cui operazioni consumano la capacità venduta dal
// piano, e a cui quindi si applica la quota di piano (R10, R15).
//
// `executions:trigger` **non** è qui, e non per dimenticanza: il trigger manuale
// ha già il proprio tetto, applicato da [jobs.Service.Trigger] con la stessa
// portata e in più il tetto per singolo job che gli dà la chiave naturale di
// `job_executions`. Contarlo anche qui gli farebbe consumare due contatori
// diversi per la stessa operazione, e il messaggio di rifiuto direbbe un numero
// che non corrisponde a quello che ha fermato la richiesta.
//
// Le letture non sono qui perché **un rate limit è una difesa del servizio, non
// una leva commerciale**: far consumare la portata del piano a chi legge
// renderebbe la dashboard inutilizzabile sul piano Free, cioè punirebbe l'uso
// normale per proteggersi dall'abuso. Le letture restano sotto il tetto tecnico,
// che è lì apposta.
var writeScopes = map[apikeys.Scope]bool{
	apikeys.ScopeJobsWrite: true,
}

// La quota di scrittura e il tetto al numero di job si toccano, e vale la pena
// dirlo perché non è ovvio leggendo l'uno o l'altro.
//
// La portata di SPEC §8 è la stessa da cui viene il tetto al catalogo: venti job
// su Free, e venti scritture al minuto. Chi crea i propri venti job in una
// raffica esaurisce la quota **nello stesso istante** in cui riempie il
// catalogo, e il ventunesimo tentativo riceve il rifiuto della quota (429) al
// posto di quello del tetto (403), che è il più informativo dei due.
//
// Non è un difetto che si possa togliere spostando i controlli: per sapere che
// il catalogo è pieno bisogna contarlo, cioè fare la query che il limite è lì
// per evitare. Resta accettabile perché **i due rifiuti indicano lo stesso
// rimedio** — entrambi nominano il piano ed entrambi portano alla stessa pagina
// — e perché la differenza dura quanto un gettone: tre secondi su Free,
// cinquanta millesimi su Pro. Alzare il burst della quota per evitarlo
// significherebbe inventare un numero che SPEC §8 non ha, che è precisamente
// ciò che questo file non fa.

// quota applica i due limiti di R10 sulla riga di routing.
type quota struct {
	ceiling *ratelimit.Limiter
	writes  *ratelimit.Budget

	// budgets è nil quando il servizio dei job non è configurato: in quel caso
	// resta il solo tetto tecnico, e la quota di piano non ha una sorgente da cui
	// nascere. È la stessa forma degli altri servizi facoltativi del router.
	budgets PlanBudgets

	trustedProxies []netip.Prefix
	log            *slog.Logger
	now            func() time.Time

	mu    sync.Mutex
	plans map[string]cachedBudget
}

type cachedBudget struct {
	budget jobs.PlanBudget
	readAt time.Time
}

func newQuota(logger *slog.Logger, deps Deps) *quota {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	rule := deps.RateLimits.Requests
	if rule.Burst <= 0 || rule.Window <= 0 {
		rule = defaultRequestCeiling
	}

	q := &quota{
		ceiling:        ratelimit.MustNew(rule, ratelimit.WithClock(now)),
		writes:         ratelimit.NewBudget(ratelimit.WithClock(now)),
		trustedProxies: deps.TrustedProxies,
		log:            logger,
		now:            now,
		plans:          map[string]cachedBudget{},
	}
	if deps.Jobs != nil {
		q.budgets = deps.Jobs
	}
	return q
}

// allowCeiling applica il tetto tecnico **prima** di riconoscere il chiamante.
//
// Prima, e non dopo, perché riconoscere costa una lettura del database — la
// sessione o la chiave — e un limite che rifiuta dopo averla fatta ha già speso
// ciò che doveva proteggere. Il prezzo di una richiesta rifiutata qui è
// un'impronta e una moltiplicazione.
//
// # La chiave, e l'enumerazione
//
// La chiave è l'impronta della **credenziale presentata**, non dell'utente: a
// questo punto non si sa ancora chi sia, ed è esattamente ciò che rende la
// difesa onesta. Una chiave API inventata e una vera producono un'impronta della
// stessa forma, finiscono in un secchio dello stesso tipo, consumano lo stesso
// credito e ricevono lo stesso 429 allo stesso tentativo: il limitatore non
// aggiunge nessun segnale a quello che l'autenticazione già dà. È la regola che
// #396 ha stabilito e che questo file non deve rompere — un contatore che si
// comporta diversamente per le identità vere risponde, da solo, alla domanda che
// l'enumerazione si pone.
//
// Chi non presenta nessuna credenziale finisce nel secchio del proprio indirizzo:
// senza, una raffica anonima non avrebbe nessun tetto prima dell'autenticazione.
// Il tentativo di *indovinare* credenziali diverse a ogni richiesta non è
// fermato da qui — ogni token inventato è un secchio nuovo — ma dai limiti per
// indirizzo che #396 e #397 hanno già messo dentro auth e apikeys.
func (q *quota) allowCeiling(w http.ResponseWriter, r *http.Request, key string) bool {
	allowed, retryAfter := q.ceiling.Allow(key)
	if allowed {
		return true
	}

	// Nel log niente credenziale, nemmeno in impronta: basta il percorso a dire
	// che il tetto sta lavorando, e la richiesta rifiutata non è un incidente da
	// ricostruire caso per caso.
	q.log.InfoContext(r.Context(), "richiesta rifiutata dal tetto tecnico",
		slog.String("path", r.URL.Path))

	seconds := retryAfterSeconds(retryAfter)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeErrorDetail(w, r, q.log, http.StatusTooManyRequests, ErrorDetail{
		Code: "rate_limited",
		// Niente `plan` e niente `limit`: sono i campi su cui un client decide se
		// mostrare un invito all'upgrade, e qui l'upgrade non servirebbe.
		Message: "Troppe richieste. È un tetto tecnico del servizio, uguale su tutti i piani: " +
			"nessun piano ne concede di più. Riprova fra " +
			strconv.Itoa(seconds) + " secondi rallentando le richieste.",
		RetryAfter: seconds,
	})
	return false
}

// allowWrite applica la quota di piano a un'operazione di scrittura (R10, R15).
//
// Sta fra il riconoscimento del chiamante e il gestore, che è l'unico punto in
// cui si sa **di chi** è la quota e non si è ancora speso niente per applicarla.
func (q *quota) allowWrite(w http.ResponseWriter, r *http.Request, identity Identity) bool {
	if q.budgets == nil {
		return true
	}

	budget, ok := q.budgetFor(r, identity.UserID())
	if !ok || !budget.Limited {
		return true
	}

	allowed, retryAfter := q.writes.Allow(
		"write:"+budget.Plan.Code,
		ratelimit.Fingerprint(identity.UserID()),
		budget.Rule,
	)
	if allowed {
		return true
	}

	q.log.InfoContext(r.Context(), "scrittura rifiutata dalla quota di piano",
		slog.String("path", r.URL.Path),
		slog.String("plan", budget.Plan.Code))

	failPlanLimit(w, r, q.log, budget.Reject(jobs.LimitWriteRate, "scritture", retryAfter))
	return false
}

// budgetFor legge la portata del piano, tenendosela per [planCacheTTL].
//
// Il secondo valore è falso quando la portata non si è potuta leggere. In quel
// caso **non si limita**: un guasto nella lettura del piano è già un problema, e
// trasformarlo nel rifiuto di tutte le scritture di tutti gli utenti lo
// moltiplicherebbe. Non è un buco, perché il tetto tecnico non dipende da questa
// lettura e resta applicato: è il motivo per cui i due limiti sono due.
func (q *quota) budgetFor(r *http.Request, userID string) (jobs.PlanBudget, bool) {
	now := q.now()

	q.mu.Lock()
	entry, ok := q.plans[userID]
	q.mu.Unlock()
	if ok && now.Sub(entry.readAt) < planCacheTTL {
		return entry.budget, true
	}

	budget, err := q.budgets.Budget(r.Context(), userID)
	if err != nil {
		q.log.ErrorContext(r.Context(), "portata del piano non leggibile: quota di piano non applicata",
			slog.String("path", r.URL.Path), slog.Any("error", err))
		return jobs.PlanBudget{}, false
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.plans) >= maxCachedPlans {
		q.evict(now)
	}
	q.plans[userID] = cachedBudget{budget: budget, readAt: now}
	return budget, true
}

// evict fa spazio nella cache. Va chiamata con il lock preso.
//
// Prima scarta le voci scadute, che è il caso normale. Se non ne libera nessuna
// — mappa piena di voci fresche — svuota tutto: ricostruire la cache costa una
// lettura per utente attivo, mentre tenere una struttura di scadenze ordinata
// per un caso che non capita è complessità che va mantenuta per sempre.
func (q *quota) evict(now time.Time) {
	for key, entry := range q.plans {
		if now.Sub(entry.readAt) >= planCacheTTL {
			delete(q.plans, key)
		}
	}
	if len(q.plans) >= maxCachedPlans {
		clear(q.plans)
	}
}

// credentialKey è l'impronta su cui il tetto tecnico conta.
//
// Vedi [quota.allowCeiling] per il perché è la credenziale presentata e non
// l'utente. Il prefisso distingue le famiglie: senza, una chiave API e un token
// di sessione con lo stesso valore — impossibile, ma non per costruzione —
// sarebbero lo stesso secchio.
func (g *guard) credentialKey(r *http.Request) string {
	if token := g.apiKeyToken(r); token != "" {
		return ratelimit.Fingerprint("api_key", token)
	}
	if token := g.sessionToken(r); token != "" {
		return ratelimit.Fingerprint("session", token)
	}
	return ratelimit.Fingerprint("ip", ClientIP(r, g.trustedProxies).String())
}

// failPlanLimit risponde a un limite di piano (R15).
//
// È una funzione del package e non un metodo di una rotta perché i limiti di
// piano scattano in due punti — dentro i gestori dei job e qui, sulla riga di
// routing — e la forma della risposta dev'essere la stessa: è quella forma che
// il client usa per decidere se mandare l'utente alla pagina dei piani.
func failPlanLimit(w http.ResponseWriter, r *http.Request, logger *slog.Logger, limit *jobs.PlanLimitError) {
	detail := ErrorDetail{
		Code:    "plan_limit_" + string(limit.Limit),
		Message: limit.Error(),
		Limit:   string(limit.Limit),
		Plan:    limit.Plan,
	}
	if limit.Field != "" {
		detail.Details = []FieldErrorBody{{Field: limit.Field, Code: detail.Code, Message: limit.Error()}}
	}

	// 429 per i limiti di frequenza, 403 per quelli di capienza. La differenza
	// non è formale: sul primo il client riprova, sul secondo deve cambiare
	// qualcosa — il piano o la richiesta.
	status := http.StatusForbidden
	if limit.RetryAfter > 0 {
		status = http.StatusTooManyRequests
		seconds := retryAfterSeconds(limit.RetryAfter)
		detail.RetryAfter = seconds
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
	writeErrorDetail(w, r, logger, status, detail)
}

// failServiceLimit risponde a un tetto tecnico del servizio (R10).
//
// È il gemello di [failPlanLimit] e la differenza fra i due è tutto il punto di
// R10, quindi vale la pena vederla scritta una accanto all'altra: quella
// valorizza `plan` e `limit`, che sono i campi su cui un client decide di
// mostrare un invito all'upgrade; questa **li lascia vuoti**, perché
// l'aggiornamento non servirebbe e proporlo sarebbe una bugia commerciale. Il
// codice porta il nome del tetto, non un prefisso `plan_limit_`.
//
// Sempre 429 e mai 403: un tetto tecnico di capienza istantanea si libera da sé,
// e riprovare è esattamente la cosa giusta da fare. È il contrario del 403 di
// [failPlanLimit], dove riprovare non cambierebbe niente finché non cambia il
// piano o la richiesta.
func failServiceLimit(w http.ResponseWriter, r *http.Request, logger *slog.Logger, limit *jobs.ServiceLimitError) {
	seconds := retryAfterSeconds(limit.RetryAfter)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeErrorDetail(w, r, logger, http.StatusTooManyRequests, ErrorDetail{
		Code:       string(limit.Limit),
		Message:    limit.Error(),
		RetryAfter: seconds,
	})
}

// retryAfterSeconds arrotonda un'attesa alla forma della testata `Retry-After`,
// che è in secondi interi. Mai zero: «riprova fra 0 secondi» è un invito a
// riprovare subito, cioè a sbattere di nuovo contro lo stesso limite.
func retryAfterSeconds(d time.Duration) int {
	seconds := int(d.Round(time.Second).Seconds())
	if seconds < 1 {
		return 1
	}
	return seconds
}
