package schedule

import (
	"testing"
	"time"
)

// Le date di questo file sono reali e verificate contro il database dei fusi
// incorporato nel binario. I salti usati:
//
//	Europe/Rome         2026-03-29T01:00Z  01:59 (+1)    -> 03:00 (+2)     buco  [02:00, 03:00)
//	Europe/Rome         2026-10-25T01:00Z  02:59 (+2)    -> 02:00 (+1)     doppia [02:00, 03:00)
//	America/New_York    2026-03-08T07:00Z  01:59 (-5)    -> 03:00 (-4)     buco  [02:00, 03:00)
//	America/New_York    2026-11-01T06:00Z  01:59 (-4)    -> 01:00 (-5)     doppia [01:00, 02:00)
//	Australia/Sydney    2026-10-03T16:00Z  01:59 (+10)   -> 03:00 (+11)    buco  [02:00, 03:00)
//	Australia/Lord_Howe 2026-10-03T15:30Z  01:59 (+10:30)-> 02:30 (+11)    buco  [02:00, 02:30)
//	Australia/Lord_Howe 2026-04-04T15:00Z  01:59 (+11)   -> 01:30 (+10:30) doppia [01:30, 02:00)
//	Pacific/Chatham     2026-09-26T14:00Z  02:44 (+12:45)-> 03:45 (+13:45) buco  [02:45, 03:45)
//
// Il test TestSaltiDiOraLegaleSonoQuelliAttesi li riverifica: se una futura
// versione di tzdata li spostasse, il primo errore arriva da lì e non da un
// caso oscuro venti righe più in basso.

func TestSaltiDiOraLegaleSonoQuelliAttesi(t *testing.T) {
	casi := []struct {
		zona     string
		istante  string
		prima    string // orario locale un minuto prima del salto
		dopo     string // orario locale al salto
		offPrima int    // offset in secondi prima
		offDopo  int    // offset in secondi dopo
	}{
		{"Europe/Rome", "2026-03-29T01:00:00Z", "01:59", "03:00", 3600, 7200},
		{"Europe/Rome", "2026-10-25T01:00:00Z", "02:59", "02:00", 7200, 3600},
		{"America/New_York", "2026-03-08T07:00:00Z", "01:59", "03:00", -18000, -14400},
		{"America/New_York", "2026-11-01T06:00:00Z", "01:59", "01:00", -14400, -18000},
		{"Australia/Sydney", "2026-10-03T16:00:00Z", "01:59", "03:00", 36000, 39600},
		{"Australia/Lord_Howe", "2026-10-03T15:30:00Z", "01:59", "02:30", 37800, 39600},
		{"Australia/Lord_Howe", "2026-04-04T15:00:00Z", "01:59", "01:30", 39600, 37800},
		{"Pacific/Chatham", "2026-09-26T14:00:00Z", "02:44", "03:45", 45900, 49500},
	}
	for _, caso := range casi {
		loc, err := time.LoadLocation(caso.zona)
		if err != nil {
			t.Fatalf("fuso %s non caricabile: %v", caso.zona, err)
		}
		salto := utc(t, caso.istante)
		prima := salto.Add(-time.Minute).In(loc)
		dopo := salto.In(loc)
		if got := prima.Format("15:04"); got != caso.prima {
			t.Errorf("%s: un minuto prima del salto = %s, atteso %s", caso.zona, got, caso.prima)
		}
		if got := dopo.Format("15:04"); got != caso.dopo {
			t.Errorf("%s: al salto = %s, atteso %s", caso.zona, got, caso.dopo)
		}
		if _, off := prima.Zone(); off != caso.offPrima {
			t.Errorf("%s: offset prima = %d, atteso %d", caso.zona, off, caso.offPrima)
		}
		if _, off := dopo.Zone(); off != caso.offDopo {
			t.Errorf("%s: offset dopo = %d, atteso %d", caso.zona, off, caso.offDopo)
		}
	}
}

// ------------------------------------------------------- il risolutore diretto

func TestResolveClassificaITreCasi(t *testing.T) {
	casi := []struct {
		zona    string
		anno    int
		mese    time.Month
		giorno  int
		ora     int
		minuto  int
		attesa  resolution
		istante string
	}{
		{"Europe/Rome", 2026, 3, 29, 1, 30, resolutionExact, "2026-03-29T00:30:00Z"},
		{"Europe/Rome", 2026, 3, 29, 2, 30, resolutionGap, "2026-03-29T01:00:00Z"},
		{"Europe/Rome", 2026, 3, 29, 2, 0, resolutionGap, "2026-03-29T01:00:00Z"},
		{"Europe/Rome", 2026, 3, 29, 2, 59, resolutionGap, "2026-03-29T01:00:00Z"},
		{"Europe/Rome", 2026, 3, 29, 3, 0, resolutionExact, "2026-03-29T01:00:00Z"},
		{"Europe/Rome", 2026, 10, 25, 1, 30, resolutionExact, "2026-10-24T23:30:00Z"},
		{"Europe/Rome", 2026, 10, 25, 2, 0, resolutionAmbiguous, "2026-10-25T00:00:00Z"},
		{"Europe/Rome", 2026, 10, 25, 2, 30, resolutionAmbiguous, "2026-10-25T00:30:00Z"},
		{"Europe/Rome", 2026, 10, 25, 2, 59, resolutionAmbiguous, "2026-10-25T00:59:00Z"},
		{"Europe/Rome", 2026, 10, 25, 3, 0, resolutionExact, "2026-10-25T02:00:00Z"},
		{"America/New_York", 2026, 3, 8, 2, 30, resolutionGap, "2026-03-08T07:00:00Z"},
		{"America/New_York", 2026, 11, 1, 1, 30, resolutionAmbiguous, "2026-11-01T05:30:00Z"},
		{"Australia/Lord_Howe", 2026, 10, 4, 2, 15, resolutionGap, "2026-10-03T15:30:00Z"},
		{"Australia/Lord_Howe", 2026, 4, 5, 1, 45, resolutionAmbiguous, "2026-04-04T14:45:00Z"},
		{"Pacific/Chatham", 2026, 9, 27, 3, 0, resolutionGap, "2026-09-26T14:00:00Z"},
		{"Asia/Kolkata", 2026, 3, 29, 2, 30, resolutionExact, "2026-03-28T21:00:00Z"},
	}
	for _, caso := range casi {
		loc, err := time.LoadLocation(caso.zona)
		if err != nil {
			t.Fatalf("fuso %s non caricabile: %v", caso.zona, err)
		}
		w := wallClock{time.Date(caso.anno, caso.mese, caso.giorno, caso.ora, caso.minuto, 0, 0, time.UTC)}
		istante, res := w.resolve(loc)
		if res != caso.attesa {
			t.Errorf("%s %s: classificazione %s, attesa %s", caso.zona, w, res, caso.attesa)
		}
		if got := istante.UTC().Format(time.RFC3339); got != caso.istante {
			t.Errorf("%s %s: istante %s, atteso %s", caso.zona, w, got, caso.istante)
		}
	}
}

// Le regole sono scelte, non ereditate: per entrambi i casi patologici il
// risultato differisce da quello che `time.Date` produrrebbe da solo. Se un
// giorno qualcuno «semplificasse» il risolutore delegando alla libreria
// standard, questo test glielo direbbe.
func TestLeRegoleNonSonoQuelleDiTimeDate(t *testing.T) {
	roma, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Fatalf("fuso non caricabile: %v", err)
	}
	casi := []struct {
		nome       string
		w          wallClock
		nostro     string
		diTimeDate string
	}{
		{
			nome:       "ora inesistente",
			w:          wallClock{time.Date(2026, 3, 29, 2, 30, 0, 0, time.UTC)},
			nostro:     "2026-03-29T01:00:00Z", // primo istante che esiste, le 03:00 locali
			diTimeDate: "2026-03-29T01:30:00Z", // time.Date sposta di un'ora intera: le 03:30
		},
		{
			nome:       "ora doppia",
			w:          wallClock{time.Date(2026, 10, 25, 2, 30, 0, 0, time.UTC)},
			nostro:     "2026-10-25T00:30:00Z", // la prima delle due
			diTimeDate: "2026-10-25T01:30:00Z", // time.Date sceglie la seconda
		},
	}
	for _, caso := range casi {
		istante, _ := caso.w.resolve(roma)
		if got := istante.UTC().Format(time.RFC3339); got != caso.nostro {
			t.Errorf("%s: risolto in %s, atteso %s", caso.nome, got, caso.nostro)
		}
		standard := time.Date(caso.w.year(), caso.w.month(), caso.w.day(), caso.w.hour(), caso.w.minute(), 0, 0, roma)
		if got := standard.UTC().Format(time.RFC3339); got != caso.diTimeDate {
			t.Errorf("%s: time.Date ora produce %s invece di %s — il confronto che questo test documenta è cambiato",
				caso.nome, got, caso.diTimeDate)
		}
	}
}

// ------------------------------------------------- l'ora che non esiste (cron)

// Regola: l'occorrenza dentro il buco non si salta, si sposta al primo istante
// che esiste. Un backup giornaliero deve girare anche l'ultima domenica di
// marzo.
func TestCronOraInesistenteVieneSpostataInAvanti(t *testing.T) {
	c := mustCron(t, "30 2 * * *", "Europe/Rome")
	confronta(t, "02:30 a Roma attraverso il buco di primavera",
		sequenza(t, c, utc(t, "2026-03-27T12:00:00Z"), 3),
		[]string{
			"2026-03-28T01:30:00Z", // 02:30 CET, giorno normale
			"2026-03-29T01:00:00Z", // le 02:30 non esistono: si parte alle 03:00 CEST
			"2026-03-30T00:30:00Z", // 02:30 CEST, giorno normale
		})
}

// Quattro occorrenze nominali cadono dentro lo stesso buco: devono collassare
// in una sola esecuzione, non in quattro sullo stesso istante.
func TestCronOccorrenzeNelBucoCollassanoInUna(t *testing.T) {
	c := mustCron(t, "*/15 2 * * *", "Europe/Rome")
	confronta(t, "*/15 nell'ora che non esiste",
		sequenza(t, c, utc(t, "2026-03-28T23:00:00Z"), 5),
		[]string{
			"2026-03-29T01:00:00Z", // una sola volta, all'istante del salto
			"2026-03-30T00:00:00Z", // 02:00 CEST del giorno dopo
			"2026-03-30T00:15:00Z",
			"2026-03-30T00:30:00Z",
			"2026-03-30T00:45:00Z",
		})
}

// Un job dichiarato alle 03:00 e uno dichiarato alle 02:30 finiscono sullo
// stesso istante nel giorno del buco. È una conseguenza diretta della regola, e
// va vista: sono due job distinti, non un'esecuzione doppia dello stesso.
func TestCronBucoFaCoinciderePiuOrariNominali(t *testing.T) {
	dueETrenta := mustCron(t, "30 2 * * *", "Europe/Rome")
	treInPunto := mustCron(t, "0 3 * * *", "Europe/Rome")
	from := utc(t, "2026-03-28T23:00:00Z")
	a, _ := dueETrenta.Next(from)
	b, _ := treInPunto.Next(from)
	if !a.Equal(b) {
		t.Errorf("nel giorno del buco 02:30 = %s e 03:00 = %s, attesi coincidenti", a, b)
	}
	if got, want := a.UTC().Format(time.RFC3339), "2026-03-29T01:00:00Z"; got != want {
		t.Errorf("istante = %s, atteso %s", got, want)
	}
}

// Il buco di mezz'ora di Lord Howe e quello di un'ora di Chatham che non parte
// da un orario tondo: la regola non deve dipendere dalla forma del salto.
func TestCronOraInesistenteConSaltiIrregolari(t *testing.T) {
	confronta(t, "02:15 a Lord Howe, buco di mezz'ora",
		sequenza(t, mustCron(t, "15 2 * * *", "Australia/Lord_Howe"), utc(t, "2026-10-03T00:00:00Z"), 1),
		[]string{"2026-10-03T15:30:00Z"})
	confronta(t, "03:00 alle Chatham, buco da 02:45 a 03:45",
		sequenza(t, mustCron(t, "0 3 * * *", "Pacific/Chatham"), utc(t, "2026-09-26T00:00:00Z"), 1),
		[]string{"2026-09-26T14:00:00Z"})
	confronta(t, "02:30 a Sydney, emisfero sud",
		sequenza(t, mustCron(t, "30 2 * * *", "Australia/Sydney"), utc(t, "2026-10-03T00:00:00Z"), 1),
		[]string{"2026-10-03T16:00:00Z"})
	confronta(t, "02:30 a New York",
		sequenza(t, mustCron(t, "30 2 * * *", "America/New_York"), utc(t, "2026-03-07T12:00:00Z"), 1),
		[]string{"2026-03-08T07:00:00Z"})
}

// ------------------------------------------------ l'ora che accade due volte

// Regola: si esegue solo alla prima delle due occorrenze. Il giorno dopo si
// torna alla normalità, con l'offset invernale.
func TestCronOraDoppiaEsegueSoloLaPrima(t *testing.T) {
	c := mustCron(t, "30 2 * * *", "Europe/Rome")
	confronta(t, "02:30 a Roma attraverso l'ora doppia",
		sequenza(t, c, utc(t, "2026-10-23T12:00:00Z"), 3),
		[]string{
			"2026-10-24T00:30:00Z", // 02:30 CEST, giorno normale
			"2026-10-25T00:30:00Z", // prima passata, offset ancora estivo — la seconda (01:30Z) è saltata
			"2026-10-26T01:30:00Z", // 02:30 CET, giorno normale
		})
}

// La conseguenza dichiarata della regola: nel giorno dell'ora doppia l'ora
// ripetuta è interamente saltata. Fra le 02:30 e le 03:00 dell'orologio passano
// novanta minuti veri.
func TestCronOraDoppiaSaltaLOraRipetuta(t *testing.T) {
	c := mustCron(t, "*/30 * * * *", "Europe/Rome")
	confronta(t, "*/30 attraverso l'ora ripetuta",
		sequenza(t, c, utc(t, "2026-10-24T23:30:00Z"), 4),
		[]string{
			"2026-10-25T00:00:00Z", // 02:00 CEST
			"2026-10-25T00:30:00Z", // 02:30 CEST
			"2026-10-25T02:00:00Z", // 03:00 CET — novanta minuti dopo
			"2026-10-25T02:30:00Z", // 03:30 CET
		})
}

// Il caso in cui la ricerca deve scartare un candidato: se il dispatch
// interroga lo scheduler *dentro* la seconda passata dell'ora ripetuta,
// l'orario da parete 02:30 combacia ancora, ma il suo istante — la prima
// passata — è già passato. L'occorrenza va scartata e la risposta è quella del
// giorno dopo, non un istante nel passato.
func TestCronOraDoppiaNonTornaIndietro(t *testing.T) {
	c := mustCron(t, "30 2 * * *", "Europe/Rome")
	dentroLaSecondaPassata := utc(t, "2026-10-25T01:10:00Z") // 02:10 CET
	next, ok := c.Next(dentroLaSecondaPassata)
	if !ok {
		t.Fatal("nessuna occorrenza")
	}
	if !next.After(dentroLaSecondaPassata) {
		t.Fatalf("Next = %s, non è successivo a %s", next, dentroLaSecondaPassata)
	}
	if got, want := next.UTC().Format(time.RFC3339), "2026-10-26T01:30:00Z"; got != want {
		t.Errorf("Next = %s, atteso %s", got, want)
	}
}

func TestCronOraDoppiaAltriFusi(t *testing.T) {
	confronta(t, "01:30 a New York",
		sequenza(t, mustCron(t, "30 1 * * *", "America/New_York"), utc(t, "2026-10-31T12:00:00Z"), 1),
		[]string{"2026-11-01T05:30:00Z"}) // EDT, la prima delle due
	confronta(t, "01:45 a Lord Howe, ora doppia di mezz'ora",
		sequenza(t, mustCron(t, "45 1 * * *", "Australia/Lord_Howe"), utc(t, "2026-04-04T00:00:00Z"), 1),
		[]string{"2026-04-04T14:45:00Z"})
}

// ---------------------------------------------- i salti che scavalcano la mezzanotte

// Non tutti i fusi cambiano l'ora alle due di notte. All'Avana, a Santiago e al
// Cairo il salto è a mezzanotte, e la mezzanotte è l'orario più usato che
// esista: `0 0 * * *`. Sono i casi in cui il buco o l'ora doppia cadono sul
// confine fra due date, cioè dove una ricerca che avanza sull'orologio da
// parete ha più occasioni di sbagliare giorno.
func TestCronSaltiSullaMezzanotte(t *testing.T) {
	casi := []struct {
		nome   string
		expr   string
		zona   string
		da     string
		attese []string
	}{
		{
			// 2026-03-08: l'orologio passa dalle 23:59 del 7 all'01:00 dell'8.
			// La mezzanotte dell'8 non esiste.
			nome: "L'Avana, mezzanotte inesistente",
			expr: "0 0 * * *", zona: "America/Havana", da: "2026-03-06T12:00:00Z",
			attese: []string{
				"2026-03-07T05:00:00Z", // 00:00 EST
				"2026-03-08T05:00:00Z", // la mezzanotte non esiste: si parte all'01:00 CDT
				"2026-03-09T04:00:00Z", // 00:00 CDT
			},
		},
		{
			// 2026-11-01: l'orologio torna dalle 00:59 alle 00:00. La
			// mezzanotte accade due volte.
			nome: "L'Avana, mezzanotte doppia",
			expr: "0 0 * * *", zona: "America/Havana", da: "2026-10-30T12:00:00Z",
			attese: []string{
				"2026-10-31T04:00:00Z", // 00:00 CDT
				"2026-11-01T04:00:00Z", // prima passata; la seconda (05:00Z) è saltata
				"2026-11-02T05:00:00Z", // 00:00 EST
			},
		},
		{
			// 2026-09-06: dalle 23:59 del 5 all'01:00 del 6.
			nome: "Santiago, mezzanotte inesistente",
			expr: "0 0 * * *", zona: "America/Santiago", da: "2026-09-04T12:00:00Z",
			attese: []string{
				"2026-09-05T04:00:00Z",
				"2026-09-06T04:00:00Z", // buco: si parte all'01:00
				"2026-09-07T03:00:00Z",
			},
		},
		{
			// 2026-04-04: dalle 23:59 si torna alle 23:00, stesso giorno.
			// L'ora doppia è l'ultima del giorno, non la seconda della notte.
			nome: "Santiago, ultima ora del giorno doppia",
			expr: "30 23 * * *", zona: "America/Santiago", da: "2026-04-03T12:00:00Z",
			attese: []string{
				"2026-04-04T02:30:00Z",
				"2026-04-05T02:30:00Z", // prima passata
				"2026-04-06T03:30:00Z",
			},
		},
		{
			// 2026-04-24: dalle 23:59 del 23 all'01:00 del 24.
			nome: "Il Cairo, mezzanotte inesistente",
			expr: "0 0 * * *", zona: "Africa/Cairo", da: "2026-04-22T12:00:00Z",
			attese: []string{
				"2026-04-22T22:00:00Z",
				"2026-04-23T22:00:00Z", // buco: si parte all'01:00
				"2026-04-24T21:00:00Z",
			},
		},
	}
	for _, caso := range casi {
		c := mustCron(t, caso.expr, caso.zona)
		confronta(t, caso.nome, sequenza(t, c, utc(t, caso.da), len(caso.attese)), caso.attese)
	}
}

// -------------------------------------------- il conto delle esecuzioni al giorno

// conta le occorrenze nell'intervallo [da, a).
func conta(t *testing.T, s Schedule, da, a time.Time) int {
	t.Helper()
	n := 0
	cur := da.Add(-time.Nanosecond) // così un'occorrenza esattamente su `da` conta
	for {
		next, ok := s.Next(cur)
		if !ok || !next.Before(a) {
			return n
		}
		n++
		cur = next
		if n > 10_000 {
			t.Fatalf("%s: troppe occorrenze, la ricerca non avanza", s)
		}
	}
}

// Il confronto che riassume la differenza fra le due modalità: `every` conta il
// tempo, `schedule` conta i rintocchi dell'orologio. Nel giorno da 25 ore le
// due risposte divergono, ed è giusto che divergano.
func TestOgniOraControOgniOraPiena(t *testing.T) {
	orario := mustCron(t, "0 * * * *", "Europe/Rome")
	intervallo, err := NewInterval(time.Hour)
	if err != nil {
		t.Fatalf("NewInterval: %v", err)
	}

	casi := []struct {
		nome             string
		mezzanotte, dopo string
		attesoCron       int
		attesoIntervallo int
	}{
		// 29 marzo 2026: il giorno dura 23 ore.
		{"giorno del buco", "2026-03-28T23:00:00Z", "2026-03-29T22:00:00Z", 23, 23},
		// 25 ottobre 2026: il giorno dura 25 ore, ma le ore piene dell'orologio
		// restano 24.
		{"giorno dell'ora doppia", "2026-10-24T22:00:00Z", "2026-10-25T23:00:00Z", 24, 25},
		// Un giorno qualunque: le due modalità coincidono.
		{"giorno normale", "2026-08-16T22:00:00Z", "2026-08-17T22:00:00Z", 24, 24},
	}
	for _, caso := range casi {
		da, a := utc(t, caso.mezzanotte), utc(t, caso.dopo)
		if got := conta(t, orario, da, a); got != caso.attesoCron {
			t.Errorf("%s: `0 * * * *` ha prodotto %d esecuzioni, attese %d", caso.nome, got, caso.attesoCron)
		}
		if got := conta(t, intervallo, da, a); got != caso.attesoIntervallo {
			t.Errorf("%s: `every 1h` ha prodotto %d esecuzioni, attese %d", caso.nome, got, caso.attesoIntervallo)
		}
	}
}

// ------------------------------------------------------ gli intervalli e l'ora legale

// Un intervallo è una durata assoluta: attraverso un cambio d'ora la distanza
// fra due occorrenze consecutive resta esattamente quella dichiarata.
func TestIntervalloIndifferenteAiCambiDOra(t *testing.T) {
	casi := []struct {
		nome   string
		durata time.Duration
		da     string
		attese []string
	}{
		{
			nome:   "un'ora attraverso il buco di Roma",
			durata: time.Hour,
			da:     "2026-03-28T23:30:00Z",
			attese: []string{"2026-03-29T00:00:00Z", "2026-03-29T01:00:00Z", "2026-03-29T02:00:00Z"},
		},
		{
			nome:   "un'ora attraverso l'ora doppia di Roma",
			durata: time.Hour,
			da:     "2026-10-25T00:30:00Z",
			attese: []string{"2026-10-25T01:00:00Z", "2026-10-25T02:00:00Z", "2026-10-25T03:00:00Z"},
		},
		{
			nome:   "dieci secondi a cavallo del salto",
			durata: 10 * time.Second,
			da:     "2026-03-29T00:59:45Z",
			attese: []string{"2026-03-29T00:59:50Z", "2026-03-29T01:00:00Z", "2026-03-29T01:00:10Z"},
		},
	}
	for _, caso := range casi {
		i, err := NewInterval(caso.durata)
		if err != nil {
			t.Fatalf("%s: NewInterval: %v", caso.nome, err)
		}
		got := sequenza(t, i, utc(t, caso.da), len(caso.attese))
		confronta(t, caso.nome, got, caso.attese)

		// La distanza fra occorrenze consecutive è la durata dichiarata, senza
		// eccezioni intorno al salto.
		precedente := utc(t, caso.da)
		for _, s := range got {
			corrente := utc(t, s)
			if d := corrente.Sub(precedente); precedente != utc(t, caso.da) && d != caso.durata {
				t.Errorf("%s: distanza %s fra %s e %s, attesa %s", caso.nome, d, precedente, corrente, caso.durata)
			}
			precedente = corrente
		}
	}
}

// ------------------------------------------------------------------ invarianti

// Su un anno intero che contiene entrambi i salti, la successione non deve mai
// tornare indietro né ripetere un istante: è la premessa dell'idempotenza (R4),
// che usa `scheduled_for` come parte della chiave.
func TestSuccessioneMonotonaSuUnAnnoIntero(t *testing.T) {
	espressioni := []string{"*/15 * * * *", "30 2 * * *", "0 * * * *", "*/5 1-4 * * *"}
	for _, expr := range espressioni {
		c := mustCron(t, expr, "Europe/Rome")
		visti := make(map[int64]bool)
		cur := utc(t, "2026-01-01T00:00:00Z")
		fine := utc(t, "2027-01-01T00:00:00Z")
		for {
			next, ok := c.Next(cur)
			if !ok {
				t.Fatalf("%s: la successione si è interrotta a %s", expr, cur)
			}
			if !next.After(cur) {
				t.Fatalf("%s: Next(%s) = %s, la successione è tornata indietro", expr, cur, next)
			}
			if visti[next.Unix()] {
				t.Fatalf("%s: istante %s restituito due volte", expr, next)
			}
			visti[next.Unix()] = true
			if !next.Before(fine) {
				break
			}
			cur = next
		}
	}
}
