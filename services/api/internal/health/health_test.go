package health_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/health"
)

// L'orologio dei test è fermo, e non c'è nessuna attesa in tutto il file: le
// grandezze osservate — l'età del battito, il margine di partizioni — sono
// differenze fra istanti, e un test che le facesse passare davvero misurerebbe
// il carico della macchina invece della logica.
var adesso = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// newService costruisce il servizio con l'orologio del test e un battito
// appena dato: le sonde che non sono oggetto della prova devono stare zitte.
func newService(t *testing.T, pool *pgxpool.Pool, mutate func(*health.Options)) *health.Service {
	t.Helper()
	opts := health.Options{
		Pool:   pool,
		Engine: battito{last: adesso.Add(-time.Second), ok: true},
		Logger: testLogger(t),
		Now:    func() time.Time { return adesso },
	}
	if mutate != nil {
		mutate(&opts)
	}
	svc, err := health.New(opts)
	if err != nil {
		t.Fatalf("health.New: %v", err)
	}
	return svc
}

// checkOf trova l'esito di una sonda per nome.
func checkOf(t *testing.T, r health.Report, name string) health.Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("sonda %q assente dal resoconto: %+v", name, r.Checks)
	return health.Check{}
}

// TestUnMotoreSanoSiDichiaraPronto è il caso normale, e serve da termine di
// paragone: senza, i test che seguono direbbero solo che qualcosa non va.
func TestUnMotoreSanoSiDichiaraPronto(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	report := newService(t, pool, nil).Check(t.Context())

	if report.Status != health.StatusOK {
		t.Fatalf("stato = %q, atteso %q: %+v", report.Status, health.StatusOK, report.Checks)
	}
	if !report.Ready() {
		t.Error("un motore sano non si è dichiarato pronto")
	}
	// La 0006 prepara quattordici giorni in avanti: il margine è quello, e il
	// numero esce dal resoconto perché è la grandezza da guardare.
	if report.PartitionHorizonDays != 14 {
		t.Errorf("margine di partizioni = %d giorni, atteso 14", report.PartitionHorizonDays)
	}
}

// TestSenzaPartizioneDiOggiIlMotoreNonEPronto.
//
// È il guasto che la issue chiede di rendere visibile: senza la partizione del
// giorno, l'inserimento di un'esecuzione fallisce (0006) e il motore smette di
// scrivere. Un processo in quello stato non è sano, è malato, e va detto.
func TestSenzaPartizioneDiOggiIlMotoreNonEPronto(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	// Resta solo il passato: è ciò che rimane quando la manutenzione ha smesso
	// di preparare le partizioni future abbastanza a lungo.
	dropPartitionsAfter(t, pool, time.Now().UTC().AddDate(0, 0, -1))

	report := newService(t, pool, nil).Check(t.Context())

	if report.Status != health.StatusDown {
		t.Fatalf("stato = %q, atteso %q: %+v", report.Status, health.StatusDown, report.Checks)
	}
	if report.Ready() {
		t.Error("un motore che non può scrivere si è dichiarato pronto")
	}
	if report.PartitionHorizonDays >= 0 {
		t.Errorf("margine = %d giorni, atteso negativo", report.PartitionHorizonDays)
	}
}

// TestNessunaPartizioneENonPronto copre l'altro modo di non avere dove
// scrivere: la tabella senza nemmeno una partizione.
func TestNessunaPartizioneENonPronto(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)
	dropAllPartitions(t, pool)

	report := newService(t, pool, nil).Check(t.Context())

	if report.Status != health.StatusDown {
		t.Fatalf("stato = %q, atteso %q: %+v", report.Status, health.StatusDown, report.Checks)
	}
}

// TestUnMargineCheSiAccorciaSiVedePrimaDiFinire.
//
// È il punto di tutto il pacchetto. Il margine di partizioni si consuma di un
// giorno al giorno, in silenzio, e il momento in cui se ne accorge qualcuno è
// quello in cui il motore ha già smesso di scrivere. Sotto la soglia il
// servizio si dichiara **degradato** — c'è un problema, e c'è ancora tempo — e
// resta pronto: togliere traffico adesso non rimedierebbe niente e fermerebbe i
// job di tutti.
func TestUnMargineCheSiAccorciaSiVedePrimaDiFinire(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	// Due giorni di margine: sotto la soglia di tre, sopra lo zero.
	dropPartitionsAfter(t, pool, time.Now().UTC().AddDate(0, 0, 2))

	report := newService(t, pool, nil).Check(t.Context())

	if report.Status != health.StatusDegraded {
		t.Fatalf("stato = %q, atteso %q: %+v", report.Status, health.StatusDegraded, report.Checks)
	}
	if !report.Ready() {
		t.Error("degradato non è non pronto: il motore sta ancora eseguendo")
	}
	if report.PartitionHorizonDays != 2 {
		t.Errorf("margine = %d giorni, atteso 2", report.PartitionHorizonDays)
	}
	if got := checkOf(t, report, "partizioni").Status; got != health.StatusDegraded {
		t.Errorf("sonda delle partizioni = %q, attesa %q", got, health.StatusDegraded)
	}
}

// TestUnoSchedulerFermoNonEPronto.
//
// «Il motore non dispaccia da tre minuti» è un problema nostro, e questa è la
// sonda che lo dice. Il database qui è sano: senza il battito, un processo con
// lo scheduler morto si dichiarerebbe pronto.
func TestUnoSchedulerFermoNonEPronto(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	svc := newService(t, pool, func(o *health.Options) {
		o.Engine = battito{last: adesso.Add(-3 * time.Minute), ok: true}
	})
	report := svc.Check(t.Context())

	if report.Status != health.StatusDown {
		t.Fatalf("stato = %q, atteso %q: %+v", report.Status, health.StatusDown, report.Checks)
	}
	if report.SchedulerAge != 3*time.Minute {
		t.Errorf("età del battito = %s, attesi 3m", report.SchedulerAge)
	}
	if got := checkOf(t, report, "database").Status; got != health.StatusOK {
		t.Errorf("sonda del database = %q: il database è sano, e le due cose vanno tenute distinte", got)
	}
}

// TestUnoSchedulerCheNonHaMaiFinitoUnaPassataNonEPronto.
//
// Lo scheduler passa quattro volte al secondo: «nessuna passata riuscita» non è
// uno stato d'avvio che si risolve da sé, è un motore che non ne ha mai portata
// a termine una.
func TestUnoSchedulerCheNonHaMaiFinitoUnaPassataNonEPronto(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	svc := newService(t, pool, func(o *health.Options) { o.Engine = battito{} })
	if report := svc.Check(t.Context()); report.Status != health.StatusDown {
		t.Fatalf("stato = %q, atteso %q: %+v", report.Status, health.StatusDown, report.Checks)
	}
}

// TestPrimaDelleSondeIlServizioNonEPronto.
//
// Il valore zero di un resoconto direbbe «pronto», e sarebbe la bugia peggiore:
// un processo appena avviato non ha ancora verificato di poter lavorare, e
// dichiararsi pronto per difetto è il modo di far arrivare traffico a
// un'istanza che non sa nemmeno se il database c'è.
func TestPrimaDelleSondeIlServizioNonEPronto(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	svc := newService(t, pool, nil)
	if report := svc.Snapshot(); report.Ready() {
		t.Fatalf("il servizio si è dichiarato pronto prima di guardare: %+v", report)
	}

	svc.Check(t.Context())
	if report := svc.Snapshot(); !report.Ready() {
		t.Fatalf("dopo la prima passata il servizio non è pronto: %+v", report)
	}
}

// TestLaFotografiaNonInterrogaIlDatabase.
//
// È il vincolo di costo del pacchetto, e va provato invece che dichiarato: gli
// endpoint di prontezza leggono l'ultimo esito, e un `/readyz` che interrogasse
// il database a ogni chiamata sarebbe un modo di far cadere il servizio
// spingendo su un endpoint che non chiede credenziali.
//
// La prova è che il mondo cambi sotto la fotografia senza che la fotografia se
// ne accorga: si portano via le partizioni future — un guasto vero, quello che
// la sonda esiste per vedere — e `Snapshot` continua a raccontare il margine di
// prima. Poi una sonda vera lo trova, che è ciò che rende la prima metà una
// prova invece di una coincidenza.
func TestLaFotografiaNonInterrogaIlDatabase(t *testing.T) {
	t.Parallel()
	pool := newTestDatabase(t)

	svc := newService(t, pool, nil)
	primo := svc.Check(t.Context())
	if primo.Status != health.StatusOK || primo.PartitionHorizonDays != 14 {
		t.Fatalf("stato iniziale = %q con %d giorni di margine: %+v",
			primo.Status, primo.PartitionHorizonDays, primo.Checks)
	}

	dropPartitionsAfter(t, pool, time.Now().UTC().AddDate(0, 0, -1))

	fotografia := svc.Snapshot()
	if fotografia.Status != health.StatusOK || fotografia.PartitionHorizonDays != 14 {
		t.Fatalf("la fotografia è cambiata senza una nuova passata: ha interrogato il database")
	}

	if report := svc.Check(t.Context()); report.Status != health.StatusDown {
		t.Fatalf("la sonda non ha visto le partizioni sparite: stato = %q, %+v",
			report.Status, report.Checks)
	}
}
