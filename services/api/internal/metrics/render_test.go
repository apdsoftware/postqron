package metrics_test

import (
	"strings"
	"testing"
	"time"

	"github.com/apdsoftware/postqron/services/api/internal/dispatch"
	"github.com/apdsoftware/postqron/services/api/internal/health"
	"github.com/apdsoftware/postqron/services/api/internal/metrics"
)

// TestLaPaginaRispettaIlFormatoDiEsposizione.
//
// Una riga malformata in mezzo alla pagina non produce un errore: produce
// metriche silenziosamente sbagliate, cioè la forma di guasto peggiore per
// qualcosa che serve ad accorgersi dei guasti. Il raccoglitore scarta ciò che
// non capisce e va avanti, e la serie che manca somiglia in tutto a una serie
// ferma a zero.
//
// Le tre regole verificate sono quelle che si violano per distrazione:
// dichiarare una famiglia due volte, scrivere un campione prima del proprio
// `# TYPE`, e spezzare i campioni di una famiglia in due punti della pagina.
func TestLaPaginaRispettaIlFormatoDiEsposizione(t *testing.T) {
	r := metrics.New(metrics.Options{
		Version: "1.2.3",
		Env:     "production",
		Now:     (&orologio{adesso: partenza}).now,
	})
	r.UsePool(pool{stats: dispatch.Stats{Queued: 3, InFlight: 12}})
	r.UseHealth(sonde{report: health.Report{
		Status: health.StatusOK,
		Checks: []health.Check{{Name: "database", Status: health.StatusOK}},
	}})
	r.Enqueued(occorrenza(300*time.Millisecond, false))

	var buf strings.Builder
	if _, err := r.WriteTo(&buf); err != nil {
		t.Fatalf("scrittura: %v", err)
	}

	dichiarate := map[string]bool{}
	chiuse := map[string]bool{}
	famiglia := ""

	for line := range strings.SplitSeq(strings.TrimRight(buf.String(), "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "# HELP "):
			name := campo(line, 2)
			if dichiarate[name] {
				t.Errorf("famiglia %q dichiarata due volte", name)
			}
			dichiarate[name] = true
			if famiglia != "" {
				chiuse[famiglia] = true
			}
			famiglia = name
		case strings.HasPrefix(line, "# TYPE "):
			if name := campo(line, 2); name != famiglia {
				t.Errorf("# TYPE di %q dopo l'# HELP di %q", name, famiglia)
			}
		case strings.HasPrefix(line, "#"):
			t.Errorf("commento non riconosciuto: %q", line)
		default:
			name := nomeSerie(line)
			base := radice(name)
			if !dichiarate[base] {
				t.Errorf("campione %q senza # TYPE dichiarato", name)
			}
			if chiuse[base] {
				t.Errorf("campioni della famiglia %q spezzati in due punti della pagina", base)
			}
		}
	}

	// Le due serie che devono esistere sempre, perché sono quelle che si guardano
	// per prime quando qualcosa non va.
	p := raccogli(t, r)
	if got := p.valore(t, `postqron_build_info{version="1.2.3",env="production"}`); got != 1 {
		t.Errorf("build_info = %v, atteso 1 con le etichette della versione", got)
	}
	if _, ok := p["postqron_dispatch_tolerance_seconds"]; !ok {
		t.Error("la tolleranza dichiarata non è esposta: la misura del ritardo resterebbe senza un metro")
	}
}

// TestSenzaMotoreLaPaginaResta.
//
// Un processo senza worker pool e senza sonde — la configurazione dei test e di
// chi serve solo l'API — non deve produrre serie a zero che si leggerebbero come
// «la coda è vuota» quando la verità è «non c'è nessuna coda». Le serie del
// motore semplicemente non ci sono.
func TestSenzaMotoreLaPaginaResta(t *testing.T) {
	r := metrics.New(metrics.Options{Now: (&orologio{adesso: partenza}).now})
	p := raccogli(t, r)

	if _, ok := p["postqron_dispatch_queued"]; ok {
		t.Error("la coda è esposta senza un worker pool da cui leggerla")
	}
	if _, ok := p["postqron_ready"]; ok {
		t.Error("la prontezza è esposta senza sonde che l'abbiano misurata")
	}
	if _, ok := p["postqron_uptime_seconds"]; !ok {
		t.Error("le serie del processo devono esserci comunque")
	}
}

// campo restituisce l'n-esimo campo separato da spazi.
func campo(line string, n int) string {
	fields := strings.Fields(line)
	if n >= len(fields) {
		return ""
	}
	return fields[n]
}

// nomeSerie è il nome con le etichette, cioè tutto ciò che precede il valore.
func nomeSerie(line string) string {
	if i := strings.LastIndex(line, " "); i >= 0 {
		return line[:i]
	}
	return line
}

// radice è la famiglia a cui un campione appartiene: si tolgono le etichette e i
// suffissi che il formato riserva agli istogrammi.
func radice(series string) string {
	name := series
	if i := strings.IndexByte(name, '{'); i >= 0 {
		name = name[:i]
	}
	for _, suffix := range []string{"_bucket", "_sum", "_count"} {
		if trimmed, ok := strings.CutSuffix(name, suffix); ok {
			// `postqron_dispatch_lag_seconds_max` non è un suffisso riservato: è
			// una famiglia sua, e va lasciata stare.
			return trimmed
		}
	}
	return name
}
