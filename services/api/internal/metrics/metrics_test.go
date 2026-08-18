package metrics_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/health"
	"github.com/apdsoftware/postqron/services/api/internal/metrics"
	"github.com/apdsoftware/postqron/services/api/internal/retention"
	"github.com/apdsoftware/postqron/services/api/internal/scheduler"
)

// In tutto questo file non si aspetta niente. Le grandezze osservate sono
// contatori e differenze fra istanti: un test che facesse passare del tempo vero
// per far scadere una finestra legherebbe la CI all'orologio invece che alla
// logica, e comincerebbe a fallire da solo il giorno in cui la macchina è
// occupata. L'orologio è un valore, e lo muove il test.

// orologio è il tempo del test.
type orologio struct{ adesso time.Time }

func (o *orologio) now() time.Time { return o.adesso }

func (o *orologio) avanza(d time.Duration) { o.adesso = o.adesso.Add(d) }

var partenza = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// pagina raccoglie le serie esposte in una mappa `serie -> valore`, dove la
// serie comprende le etichette nella forma esatta in cui sono scritte.
type pagina map[string]float64

func raccogli(t *testing.T, r *metrics.Registry) pagina {
	t.Helper()
	var buf strings.Builder
	n, err := r.WriteTo(&buf)
	if err != nil {
		t.Fatalf("scrittura delle metriche: %v", err)
	}
	if int(n) != buf.Len() {
		t.Fatalf("byte dichiarati = %d, scritti = %d", n, buf.Len())
	}

	out := pagina{}
	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndex(line, " ")
		if i < 0 {
			t.Fatalf("riga senza valore: %q", line)
		}
		value, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			t.Fatalf("valore non numerico in %q: %v", line, err)
		}
		if _, dup := out[line[:i]]; dup {
			t.Fatalf("serie ripetuta: %q", line[:i])
		}
		out[line[:i]] = value
	}
	return out
}

func (p pagina) valore(t *testing.T, serie string) float64 {
	t.Helper()
	v, ok := p[serie]
	if !ok {
		t.Fatalf("serie assente: %q", serie)
	}
	return v
}

// occorrenza costruisce un'occorrenza con il ritardo indicato.
func occorrenza(lag time.Duration, recuperata bool) scheduler.Occurrence {
	return scheduler.Occurrence{
		Job:          scheduler.Job{ID: "job", Name: "job", UserID: "ws"},
		ScheduledFor: partenza,
		EnqueuedAt:   partenza.Add(lag),
		Environment:  "production",
		Attempt:      1,
		Recovered:    recuperata,
	}
}

// TestIlRitardoFinisceNellIstogramma.
//
// È la misura del prodotto (R47): quanto un'occorrenza aspetta fra l'orario
// dovuto e la partenza. Il test verifica i secchi **cumulativi**, che è la forma
// che il formato richiede, la somma, il conteggio e il massimo — che
// l'istogramma da solo non sa dire.
func TestIlRitardoFinisceNellIstogramma(t *testing.T) {
	clock := &orologio{adesso: partenza}
	r := metrics.New(metrics.Options{Now: clock.now})

	for _, lag := range []time.Duration{
		100 * time.Millisecond,
		400 * time.Millisecond,
		3 * time.Second,
		45 * time.Second,
	} {
		r.Enqueued(occorrenza(lag, false))
	}

	p := raccogli(t, r)
	if got := p.valore(t, `postqron_dispatch_lag_seconds_count`); got != 4 {
		t.Errorf("osservazioni = %v, attese 4", got)
	}
	if got := p.valore(t, `postqron_dispatch_lag_seconds_sum`); got != 48.5 {
		t.Errorf("somma = %v s, attesi 48.5", got)
	}
	if got := p.valore(t, `postqron_dispatch_lag_seconds_max`); got != 45 {
		t.Errorf("massimo = %v s, attesi 45", got)
	}

	// Cumulativi: `le="0.5"` conta tutte le osservazioni fino a mezzo secondo,
	// non solo quelle cadute in quell'intervallo.
	for serie, atteso := range map[string]float64{
		`postqron_dispatch_lag_seconds_bucket{le="0.05"}`: 0,
		`postqron_dispatch_lag_seconds_bucket{le="0.1"}`:  1,
		`postqron_dispatch_lag_seconds_bucket{le="0.5"}`:  2,
		`postqron_dispatch_lag_seconds_bucket{le="1"}`:    2,
		`postqron_dispatch_lag_seconds_bucket{le="5"}`:    3,
		`postqron_dispatch_lag_seconds_bucket{le="30"}`:   3,
		`postqron_dispatch_lag_seconds_bucket{le="60"}`:   4,
		`postqron_dispatch_lag_seconds_bucket{le="+Inf"}`: 4,
	} {
		if got := p.valore(t, serie); got != atteso {
			t.Errorf("%s = %v, atteso %v", serie, got, atteso)
		}
	}
}

// TestUnOccorrenzaRipresaNonSporcaLaMisura.
//
// Il ritardo di un'occorrenza ripresa dopo un fermo misura **il fermo**, non la
// precisione dello scheduler. Mescolarla sposterebbe la coda della distribuzione
// di ore per un fatto che non c'entra, ed è la stessa esclusione che
// `scheduler.Stats.MaxLag` fa già.
func TestUnOccorrenzaRipresaNonSporcaLaMisura(t *testing.T) {
	clock := &orologio{adesso: partenza}
	r := metrics.New(metrics.Options{Now: clock.now})

	r.Enqueued(occorrenza(200*time.Millisecond, false))
	r.Enqueued(occorrenza(3*time.Hour, true))

	p := raccogli(t, r)
	if got := p.valore(t, `postqron_dispatch_lag_seconds_count`); got != 1 {
		t.Errorf("osservazioni = %v, attesa 1: l'occorrenza ripresa è entrata nella misura", got)
	}
	if got := p.valore(t, `postqron_dispatch_lag_seconds_max`); got != 0.2 {
		t.Errorf("massimo = %v s, attesi 0.2", got)
	}
}

// TestUnRitardoNegativoValeZero.
//
// Succede solo se l'orologio torna indietro. Non è un dato, e contarlo con il
// segno abbasserebbe la somma di una quantità inventata.
func TestUnRitardoNegativoValeZero(t *testing.T) {
	r := metrics.New(metrics.Options{Now: (&orologio{adesso: partenza}).now})
	r.Enqueued(occorrenza(-5*time.Second, false))

	p := raccogli(t, r)
	if got := p.valore(t, `postqron_dispatch_lag_seconds_sum`); got != 0 {
		t.Errorf("somma = %v, attesi 0", got)
	}
	if got := p.valore(t, `postqron_dispatch_lag_seconds_count`); got != 1 {
		t.Errorf("osservazioni = %v, attesa 1: l'osservazione va contata comunque", got)
	}
}

// TestIlBattitoAvanzaSoloConLePassateRiuscite.
//
// È la proprietà su cui si regge la sonda di prontezza dello scheduler: un
// motore che fallisce ogni passata contro un database che non risponde chiama
// comunque `Tick` quattro volte al secondo, e se il battito avanzasse lo stesso
// direbbe che va tutto bene mentre non parte più niente.
func TestIlBattitoAvanzaSoloConLePassateRiuscite(t *testing.T) {
	clock := &orologio{adesso: partenza}
	r := metrics.New(metrics.Options{Now: clock.now})

	if _, ok := r.LastTick(); ok {
		t.Fatal("il battito esiste prima della prima passata")
	}

	r.Tick(scheduler.Stats{Duration: 3 * time.Millisecond})
	buono, ok := r.LastTick()
	if !ok || !buono.Equal(partenza) {
		t.Fatalf("battito = %s (presente: %v), atteso %s", buono, ok, partenza)
	}

	clock.avanza(time.Minute)
	r.Tick(scheduler.Stats{Failed: true, Duration: time.Millisecond})

	fermo, _ := r.LastTick()
	if !fermo.Equal(partenza) {
		t.Fatalf("battito = %s: una passata fallita l'ha fatto avanzare", fermo)
	}

	p := raccogli(t, r)
	if got := p.valore(t, "postqron_scheduler_passes_total"); got != 2 {
		t.Errorf("passate = %v, attese 2", got)
	}
	if got := p.valore(t, "postqron_scheduler_pass_failures_total"); got != 1 {
		t.Errorf("passate fallite = %v, attesa 1", got)
	}
	// L'ultimo tentativo è più recente dell'ultima passata riuscita: la distanza
	// fra i due è il tempo in cui il motore ha girato a vuoto.
	riuscita := p.valore(t, "postqron_scheduler_last_pass_timestamp_seconds")
	tentata := p.valore(t, "postqron_scheduler_last_attempt_timestamp_seconds")
	if tentata-riuscita != 60 {
		t.Errorf("distanza fra ultimo tentativo e ultima riuscita = %v s, attesi 60", tentata-riuscita)
	}
}

// TestLeOccorrenzeSaltateSonoContate.
//
// «Un'occorrenza saltata è un fatto che va contato, non solo scritto in un log»:
// i tetti introdotti da #457 producono occorrenze che non si eseguiranno mai, e
// senza queste serie l'unico modo di sommarle sarebbe leggere i log.
func TestLeOccorrenzeSaltateSonoContate(t *testing.T) {
	r := metrics.New(metrics.Options{Now: (&orologio{adesso: partenza}).now})
	r.UsePool(pool{stats: dispatch.Stats{Overlapped: 7, WorkspaceStalls: 3, Refused: 2}})
	r.Tick(scheduler.Stats{Overlapped: 7, Expired: 4})
	r.Dropped(scheduler.Dropped{Count: 120})

	p := raccogli(t, r)
	for serie, atteso := range map[string]float64{
		`postqron_scheduler_occurrences_total{esito="overlapped"}`: 7,
		`postqron_scheduler_occurrences_total{esito="expired"}`:    4,
		`postqron_scheduler_occurrences_total{esito="dropped"}`:    120,
		`postqron_dispatch_overlap_skipped_total`:                  7,
		`postqron_dispatch_workspace_ceiling_stalls_total`:         3,
		`postqron_dispatch_refused_total`:                          2,
	} {
		if got := p.valore(t, serie); got != atteso {
			t.Errorf("%s = %v, atteso %v", serie, got, atteso)
		}
	}
}

// TestLeClassiDeiFallimentiSonoTutteEsposte.
//
// Le serie con etichetta devono esserci **tutte**, anche a zero: una serie che
// compare solo quando il guasto è già accaduto non permette di accorgersi che è
// appena cominciato — non c'è niente da cui vedere il salto.
//
// L'elenco delle classi è quello di internal/dispatch, non una copia scritta
// qui: le stesse sei finiscono in un'email (R21) e in un grafico, e due elenchi
// smetterebbero di combaciare al primo valore aggiunto — con la metrica che non
// conta più, in silenzio, proprio la classe nuova.
func TestLeClassiDeiFallimentiSonoTutteEsposte(t *testing.T) {
	r := metrics.New(metrics.Options{Now: (&orologio{adesso: partenza}).now})
	r.Failed(context.Background(), dispatch.Failure{Kind: dispatch.FailureDNS})
	r.Failed(context.Background(), dispatch.Failure{Kind: dispatch.FailureDNS})
	// Una classe che non conosciamo non si perde: finisce fra le sconosciute.
	r.Failed(context.Background(), dispatch.Failure{Kind: "qualcosa-di-nuovo"})

	p := raccogli(t, r)
	if len(dispatch.FailureKinds()) != 6 {
		t.Fatalf("classi dichiarate = %d: l'elenco è cambiato e questo test va riletto",
			len(dispatch.FailureKinds()))
	}
	for _, kind := range dispatch.FailureKinds() {
		serie := `postqron_dispatch_failures_total{kind="` + string(kind) + `"}`
		atteso := float64(0)
		switch kind {
		case dispatch.FailureDNS:
			atteso = 2
		case dispatch.FailureUnknown:
			atteso = 1
		}
		if got := p.valore(t, serie); got != atteso {
			t.Errorf("%s = %v, atteso %v", serie, got, atteso)
		}
	}
}

// TestLaProntezzaEIlMarginePartizioniSonoMetriche.
//
// Il margine di partizioni è il numero che rende visibile, con due settimane di
// anticipo, il giorno in cui il motore smetterà di poter scrivere. Serve come
// metrica e non solo come endpoint, perché è su una serie storica che si vede
// che sta scendendo.
func TestLaProntezzaEIlMarginePartizioniSonoMetriche(t *testing.T) {
	r := metrics.New(metrics.Options{Now: (&orologio{adesso: partenza}).now})
	r.UseHealth(sonde{report: health.Report{
		Status:               health.StatusDegraded,
		PartitionHorizonDays: 2,
		Checks: []health.Check{
			{Name: "database", Status: health.StatusOK},
			{Name: "partizioni", Status: health.StatusDegraded},
			{Name: "scheduler", Status: health.StatusOK},
		},
	}})

	p := raccogli(t, r)
	if got := p.valore(t, "postqron_ready"); got != 1 {
		t.Errorf("pronto = %v: degradato conta come pronto", got)
	}
	if got := p.valore(t, "postqron_partition_horizon_days"); got != 2 {
		t.Errorf("margine = %v giorni, attesi 2", got)
	}
	if got := p.valore(t, `postqron_health_check_up{check="partizioni"}`); got != 0.5 {
		t.Errorf("sonda delle partizioni = %v, atteso 0.5 (degradato)", got)
	}
	if got := p.valore(t, `postqron_health_check_up{check="database"}`); got != 1 {
		t.Errorf("sonda del database = %v, atteso 1", got)
	}
}

// TestLaManutenzioneRacconta verifica che il resoconto della retention arrivi
// intero, rinunce comprese: è la rinuncia ripetuta sulla creazione delle
// partizioni a preannunciare il guasto.
func TestLaManutenzioneRacconta(t *testing.T) {
	r := metrics.New(metrics.Options{Now: (&orologio{adesso: partenza}).now})
	r.Swept(retention.Stats{PartitionsEnsured: 16, RowsDeleted: 5000, EnsureDeferred: true, LongestRetention: 90}, nil)
	r.Swept(retention.Stats{PartitionsEnsured: 0, EnsureDeferred: true, LongestRetention: 90}, context.DeadlineExceeded)

	p := raccogli(t, r)
	for serie, atteso := range map[string]float64{
		"postqron_retention_sweeps_total":                   2,
		"postqron_retention_sweep_failures_total":           1,
		"postqron_retention_partitions_ensured_total":       16,
		"postqron_retention_rows_deleted_total":             5000,
		`postqron_retention_deferrals_total{fase="ensure"}`: 2,
		`postqron_retention_deferrals_total{fase="drop"}`:   0,
		"postqron_retention_longest_days":                   90,
	} {
		if got := p.valore(t, serie); got != atteso {
			t.Errorf("%s = %v, atteso %v", serie, got, atteso)
		}
	}
}

// ------------------------------------------------------------------ sorgenti

type pool struct{ stats dispatch.Stats }

func (p pool) Stats() dispatch.Stats { return p.stats }

type sonde struct{ report health.Report }

func (s sonde) Snapshot() health.Report { return s.report }
