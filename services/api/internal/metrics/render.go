package metrics

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/health"
)

// ContentType è il tipo del formato di esposizione testuale, nella versione
// 0.0.4. È quello che qualunque raccoglitore compatibile con Prometheus si
// aspetta, e dichiararlo è ciò che gli permette di non indovinarlo.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// WriteTo scrive tutte le serie nel formato di esposizione testuale.
//
// L'ordine è fisso e le serie di una stessa famiglia sono contigue: il formato
// lo richiede per le famiglie con etichette, e per le altre è comunque ciò che
// rende leggibile una pagina che qualcuno prima o poi apre con `curl`.
//
// Le grandezze istantanee — la coda, le esecuzioni in volo, la prontezza — si
// leggono qui, al momento della raccolta, e non vengono tenute in una copia che
// si aggiorna da sola: una copia sarebbe una seconda verità con un ritardo
// proprio, e la sorgente è già in memoria.
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	out := &writer{w: bufio.NewWriter(w)}
	now := r.now()

	// ------------------------------------------------------------- processo
	out.help("postqron_build_info", "Versione e ambiente del binario in esecuzione. Il valore è sempre 1: contano le etichette.", gauge)
	out.labelled("postqron_build_info", 1, "version", r.version, "env", r.env)

	out.help("postqron_uptime_seconds", "Da quanto il processo è vivo.", gauge)
	out.value("postqron_uptime_seconds", now.Sub(r.started).Seconds())

	// ------------------------------------------------------------ scheduler
	out.help("postqron_scheduler_passes_total", "Passate dello scheduler, riuscite e no.", counter)
	out.value("postqron_scheduler_passes_total", float64(r.sched.ticks.Load()))

	out.help("postqron_scheduler_pass_failures_total", "Passate interrotte da un errore. Una serie che cresce senza sosta è il motore che non sta dispacciando.", counter)
	out.value("postqron_scheduler_pass_failures_total", float64(r.sched.failures.Load()))

	out.help("postqron_scheduler_pass_seconds_total", "Tempo totale speso dentro le passate.", counter)
	out.value("postqron_scheduler_pass_seconds_total", time.Duration(r.sched.tickNanos.Load()).Seconds())

	out.help("postqron_scheduler_last_pass_timestamp_seconds", "Istante dell'ultima passata riuscita. Zero se non ce n'è ancora stata una.", gauge)
	out.value("postqron_scheduler_last_pass_timestamp_seconds", unixSeconds(r.sched.lastTickUnixNano.Load()))

	out.help("postqron_scheduler_last_attempt_timestamp_seconds", "Istante dell'ultima passata tentata, riuscita o no. La distanza dalla precedente è il tempo in cui il motore ha girato a vuoto.", gauge)
	out.value("postqron_scheduler_last_attempt_timestamp_seconds", unixSeconds(r.sched.lastAttemptUnixNs.Load()))

	out.help("postqron_scheduler_backlog", "1 quando l'ultima passata ha lasciato indietro lavoro già dovuto.", gauge)
	out.value("postqron_scheduler_backlog", boolValue(r.sched.backlog.Load()))

	out.help("postqron_scheduler_jobs_examined_total", "Job dovuti esaminati.", counter)
	out.value("postqron_scheduler_jobs_examined_total", float64(r.sched.jobs.Load()))

	out.help("postqron_scheduler_jobs_seeded_total", "Job che hanno ricevuto la loro prima occorrenza.", counter)
	out.value("postqron_scheduler_jobs_seeded_total", float64(r.sched.seeded.Load()))

	out.help("postqron_scheduler_jobs_invalid_total", "Job con una schedulazione illeggibile, messi in quarantena.", counter)
	out.value("postqron_scheduler_jobs_invalid_total", float64(r.sched.invalid.Load()))

	out.help("postqron_scheduler_occurrences_total", "Occorrenze passate dallo scheduler, per esito dell'accodamento.", counter)
	out.labelled("postqron_scheduler_occurrences_total", float64(r.sched.enqueued.Load()), "esito", "enqueued")
	out.labelled("postqron_scheduler_occurrences_total", float64(r.sched.recovered.Load()), "esito", "recovered")
	out.labelled("postqron_scheduler_occurrences_total", float64(r.sched.conflicts.Load()), "esito", "conflict")
	out.labelled("postqron_scheduler_occurrences_total", float64(r.sched.rejected.Load()), "esito", "rejected")
	out.labelled("postqron_scheduler_occurrences_total", float64(r.sched.overlapped.Load()), "esito", "overlapped")
	out.labelled("postqron_scheduler_occurrences_total", float64(r.sched.dropped.Load()), "esito", "dropped")
	out.labelled("postqron_scheduler_occurrences_total", float64(r.sched.expired.Load()), "esito", "expired")

	// ----------------------------------------------- la misura del prodotto
	out.help("postqron_dispatch_lag_seconds", "Ritardo fra l'orario dovuto di un'occorrenza e la sua consegna al dispatch (R47). Le occorrenze riprese dopo un fermo non ci sono: il loro ritardo misura il fermo, non la precisione.", histogramType)
	buckets, sum, count, max := r.lag.snapshot()
	for i, bound := range lagBuckets {
		out.labelled("postqron_dispatch_lag_seconds_bucket", float64(buckets[i]), "le", formatFloat(bound))
	}
	out.labelled("postqron_dispatch_lag_seconds_bucket", float64(buckets[len(lagBuckets)]), "le", "+Inf")
	out.value("postqron_dispatch_lag_seconds_sum", sum)
	out.value("postqron_dispatch_lag_seconds_count", float64(count))

	out.help("postqron_dispatch_lag_seconds_max", "Ritardo massimo osservato dall'avvio. L'istogramma non lo sa dire: il secchio più alto dice «oltre», non quanto.", gauge)
	out.value("postqron_dispatch_lag_seconds_max", time.Duration(max).Seconds())

	out.help("postqron_dispatch_tolerance_seconds", "Tolleranza di dispatch dichiarata (R47). È l'impegno accanto a cui va letta la misura qui sopra.", gauge)
	out.value("postqron_dispatch_tolerance_seconds", r.tolerance.Seconds())

	out.help("postqron_dispatch_late_total", "Occorrenze consegnate oltre la tolleranza dichiarata.", counter)
	out.value("postqron_dispatch_late_total", float64(r.sched.late.Load()))

	// ---------------------------------------------------- coda ed esecuzione
	if r.pool != nil {
		st := r.pool.Stats()

		out.help("postqron_dispatch_queued", "Occorrenze in attesa di un worker.", gauge)
		out.value("postqron_dispatch_queued", float64(st.Queued))

		out.help("postqron_dispatch_in_flight", "Esecuzioni in corso adesso.", gauge)
		out.value("postqron_dispatch_in_flight", float64(st.InFlight))

		out.help("postqron_dispatch_accepted_total", "Occorrenze accettate in coda.", counter)
		out.value("postqron_dispatch_accepted_total", float64(st.Accepted))

		out.help("postqron_dispatch_duplicated_total", "Occorrenze riofferte mentre erano già in carico, scartate senza rumore.", counter)
		out.value("postqron_dispatch_duplicated_total", float64(st.Duplicated))

		out.help("postqron_dispatch_refused_total", "Occorrenze rifiutate perché la coda era piena o il pool in arresto. Le loro righe restano `pending` e il recupero le riprende.", counter)
		out.value("postqron_dispatch_refused_total", float64(st.Refused))

		out.help("postqron_dispatch_overlap_skipped_total", "Occorrenze non eseguite perché la precedente della stessa corsia era in corso e il job dichiara `on_overlap: skip` (R41).", counter)
		out.value("postqron_dispatch_overlap_skipped_total", float64(st.Overlapped))

		out.help("postqron_dispatch_workspace_ceiling_stalls_total", "Scelte in cui il tetto tecnico per workspace (R10) ha tolto di mezzo del lavoro pronto. Se cresce con la coda corta, il tetto è troppo basso per il carico vero.", counter)
		out.value("postqron_dispatch_workspace_ceiling_stalls_total", float64(st.WorkspaceStalls))

		out.help("postqron_dispatch_claims_total", "Prese dell'occorrenza, per esito dell'aggiornamento condizionato che chiude R4. `lost` non è un errore: è l'idempotenza che funziona.", counter)
		out.labelled("postqron_dispatch_claims_total", float64(st.Claimed), "esito", "claimed")
		out.labelled("postqron_dispatch_claims_total", float64(st.Lost), "esito", "lost")

		out.help("postqron_dispatch_executions_total", "Tentativi conclusi, per esito scritto sulla riga.", counter)
		out.labelled("postqron_dispatch_executions_total", float64(st.Succeeded), "esito", "succeeded")
		out.labelled("postqron_dispatch_executions_total", float64(st.Failed), "esito", "failed")
		out.labelled("postqron_dispatch_executions_total", float64(st.TimedOut), "esito", "timed_out")
		out.labelled("postqron_dispatch_executions_total", float64(st.Skipped), "esito", "skipped")

		out.help("postqron_dispatch_occurrences_failed_total", "Occorrenze fallite in via definitiva: nessun tentativo successivo le seguirà. Non è la somma dei tentativi falliti — un retry riuscito non ci finisce dentro.", counter)
		out.value("postqron_dispatch_occurrences_failed_total", float64(st.FailedFinal))

		out.help("postqron_dispatch_blocked_total", "Esecuzioni fermate dal controllo sulla destinazione (R38).", counter)
		out.value("postqron_dispatch_blocked_total", float64(st.Blocked))

		out.help("postqron_dispatch_released_total", "Esecuzioni riportate a `pending` dall'arresto del processo.", counter)
		out.value("postqron_dispatch_released_total", float64(st.Released))

		out.help("postqron_dispatch_store_errors_total", "Errori del database durante una transizione di stato dell'esecuzione.", counter)
		out.value("postqron_dispatch_store_errors_total", float64(st.Errors))

		out.help("postqron_dispatch_retries_total", "Tentativi successivi al primo, per esito della decisione (R5).", counter)
		out.labelled("postqron_dispatch_retries_total", float64(st.Retried), "esito", "armed")
		out.labelled("postqron_dispatch_retries_total", float64(st.RetryExhausted), "esito", "exhausted")
		out.labelled("postqron_dispatch_retries_total", float64(st.RetryOverrun), "esito", "overrun")
		out.labelled("postqron_dispatch_retries_total", float64(st.RetryAbandoned), "esito", "abandoned")
	}

	out.help("postqron_dispatch_failures_total", "Fallimenti definitivi per classe. È la stessa classificazione che finisce nell'avviso di R21, e si ricava dal tipo dell'errore — mai dal suo testo, che può contenere segreti del workspace (R43).", counter)
	for _, kind := range kinds {
		out.labelled("postqron_dispatch_failures_total", float64(r.failures[kind].Load()), "kind", string(kind))
	}

	// --------------------------------------------------------- manutenzione
	out.help("postqron_retention_sweeps_total", "Passate della retention.", counter)
	out.value("postqron_retention_sweeps_total", float64(r.reten.sweeps.Load()))

	out.help("postqron_retention_sweep_failures_total", "Passate della retention concluse con un errore.", counter)
	out.value("postqron_retention_sweep_failures_total", float64(r.reten.failures.Load()))

	out.help("postqron_retention_partitions_ensured_total", "Partizioni giornaliere create o confermate.", counter)
	out.value("postqron_retention_partitions_ensured_total", float64(r.reten.ensured.Load()))

	out.help("postqron_retention_partitions_dropped_total", "Partizioni eliminate perché interamente oltre la retention più lunga in circolazione.", counter)
	out.value("postqron_retention_partitions_dropped_total", float64(r.reten.dropped.Load()))

	out.help("postqron_retention_rows_deleted_total", "Esecuzioni cancellate riga per riga.", counter)
	out.value("postqron_retention_rows_deleted_total", float64(r.reten.rows.Load()))

	out.help("postqron_retention_batches_total", "Lotti di cancellazione eseguiti.", counter)
	out.value("postqron_retention_batches_total", float64(r.reten.batches.Load()))

	out.help("postqron_retention_deferrals_total", "Passate che hanno rinunciato a un lock. Una rinuncia è la scelta giusta; una serie ininterrotta è qualcuno che tiene aperta una transazione lunga — e su `ensure` significa che il margine di partizioni sta finendo.", counter)
	out.labelled("postqron_retention_deferrals_total", float64(r.reten.ensDeferred.Load()), "fase", "ensure")
	out.labelled("postqron_retention_deferrals_total", float64(r.reten.dropDeferred.Load()), "fase", "drop")

	out.help("postqron_retention_truncated_total", "Passate fermate al proprio tetto di lotti, che hanno lasciato righe scadute sul tavolo.", counter)
	out.value("postqron_retention_truncated_total", float64(r.reten.truncated.Load()))

	out.help("postqron_retention_longest_days", "Retention più lunga fra i piani rappresentati, in giorni. È ciò che spiega quante partizioni se ne vanno.", gauge)
	out.value("postqron_retention_longest_days", float64(r.reten.longestRetention.Load()))

	// -------------------------------------------------------------- prontezza
	if r.health != nil {
		report := r.health.Snapshot()

		out.help("postqron_ready", "1 quando il motore può fare il proprio mestiere adesso. Degradato conta come pronto.", gauge)
		out.value("postqron_ready", boolValue(report.Ready()))

		out.help("postqron_health_check_up", "Esito di ogni sonda di prontezza: 1 ok, 0.5 degradato, 0 fuori uso.", gauge)
		for _, check := range report.Checks {
			out.labelled("postqron_health_check_up", statusValue(check.Status), "check", check.Name)
		}

		out.help("postqron_partition_horizon_days", "Giorni di margine prima che `job_executions` non abbia più una partizione in cui scrivere. Negativo significa che manca già quella di oggi, cioè che il motore non sta più registrando niente.", gauge)
		out.value("postqron_partition_horizon_days", float64(report.PartitionHorizonDays))
	}

	return out.done()
}

// ------------------------------------------------------------------ formato

type metricType string

const (
	counter       metricType = "counter"
	gauge         metricType = "gauge"
	histogramType metricType = "histogram"
)

// writer scrive il formato di esposizione e tiene il conto dei byte e del primo
// errore.
//
// Il primo errore ferma tutto il resto: se il client ha chiuso la connessione a
// metà pagina, continuare a formattare quaranta serie è lavoro per nessuno.
type writer struct {
	w   *bufio.Writer
	n   int64
	err error
}

func (o *writer) write(s string) {
	if o.err != nil {
		return
	}
	n, err := o.w.WriteString(s)
	o.n += int64(n)
	o.err = err
}

// help scrive le due righe di intestazione di una famiglia.
func (o *writer) help(name, text string, kind metricType) {
	o.write("# HELP " + name + " " + escapeHelp(text) + "\n")
	o.write("# TYPE " + name + " " + string(kind) + "\n")
}

func (o *writer) value(name string, v float64) {
	o.write(name + " " + formatFloat(v) + "\n")
}

// labelled scrive una serie con etichette, in coppie nome/valore.
func (o *writer) labelled(name string, v float64, pairs ...string) {
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(pairs[i])
		b.WriteString(`="`)
		b.WriteString(escapeLabel(pairs[i+1]))
		b.WriteByte('"')
	}
	b.WriteString("} ")
	b.WriteString(formatFloat(v))
	b.WriteByte('\n')
	o.write(b.String())
}

func (o *writer) done() (int64, error) {
	if o.err != nil {
		return o.n, o.err
	}
	return o.n, o.w.Flush()
}

// formatFloat scrive un numero nella forma più corta che lo rappresenta esatto.
//
// `-1` come precisione è la scelta che tiene gli interi senza virgola — un
// contatore si legge `42`, non `42.000000` — e i decimali senza cifre inventate.
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// escapeHelp e escapeLabel applicano le fughe che il formato richiede.
//
// Nessuno dei testi di questo pacchetto ne ha bisogno oggi: sono frasi scritte
// qui accanto, e i valori delle etichette sono costanti del codice. Le fughe
// esistono perché *un giorno* un nome di sonda potrebbe arrivare da altrove, e
// una riga malformata in mezzo alla pagina non produce un errore — produce
// metriche silenziosamente sbagliate, che è la forma di guasto peggiore per
// qualcosa che serve ad accorgersi dei guasti.
func escapeHelp(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(s)
}

func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// statusValue traduce l'esito di una sonda in un numero.
//
// Il mezzo per il degradato non è un vezzo: è ciò che permette a una regola di
// allarme di distinguere «intervieni adesso» da «intervieni questa settimana»
// con una soglia sola, invece che con due serie separate.
func statusValue(s health.Status) float64 {
	switch s {
	case health.StatusOK:
		return 1
	case health.StatusDegraded:
		return 0.5
	default:
		return 0
	}
}

// unixSeconds traduce un istante in nanosecondi nel timestamp che il formato
// usa. Zero resta zero: significa «non è mai successo», e va distinto
// dall'istante zero di Unix.
func unixSeconds(nanos int64) float64 {
	if nanos == 0 {
		return 0
	}
	return float64(nanos) / float64(time.Second)
}
