package paddle_test

import (
	"testing"

	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

func ambiente(valori map[string]string) func(string) string {
	return func(k string) string { return valori[k] }
}

var listinoCompleto = map[string]string{
	"PADDLE_PRICE_PRO_MONTHLY":    "pri_pro_m",
	"PADDLE_PRICE_PRO_YEARLY":     "pri_pro_y",
	"PADDLE_PRICE_TEAM_MONTHLY":   "pri_team_m",
	"PADDLE_PRICE_AGENCY_MONTHLY": "pri_agency_m",
}

func TestCatalogoNelleDueDirezioni(t *testing.T) {
	catalogo, err := paddle.CatalogFromEnv(ambiente(listinoCompleto))
	if err != nil {
		t.Fatalf("costruzione del catalogo: %v", err)
	}

	// Dal piano al prezzo: è la lettura del checkout.
	ref, ok := catalogo.Price(paddle.PlanPro, paddle.PeriodYearly)
	if !ok || ref.PriceID != "pri_pro_y" {
		t.Fatalf("Price(pro, yearly) = %+v, %v", ref, ok)
	}

	// Dal prezzo al piano: è la lettura del webhook.
	ref, ok = catalogo.Plan("pri_team_m")
	if !ok || ref.PlanCode != paddle.PlanTeam || ref.Period != paddle.PeriodMonthly {
		t.Fatalf("Plan(pri_team_m) = %+v, %v", ref, ok)
	}
}

// R62: l'annuale esiste **solo** su Pro. Non è un `if` che qualcuno può
// cancellare — Team e Agency non hanno una casella annuale da riempire, quindi
// non hanno un prezzo da comprare.
func TestAnnualeSoloSuPro(t *testing.T) {
	catalogo, err := paddle.CatalogFromEnv(ambiente(listinoCompleto))
	if err != nil {
		t.Fatalf("costruzione del catalogo: %v", err)
	}

	for _, piano := range []string{paddle.PlanTeam, paddle.PlanAgency} {
		if ref, ok := catalogo.Price(piano, paddle.PeriodYearly); ok {
			t.Errorf("il piano %s non ha un annuale in listino, trovato %q", piano, ref.PriceID)
		}
		if periodi := catalogo.Periods(piano); len(periodi) != 1 || periodi[0] != paddle.PeriodMonthly {
			t.Errorf("periodicità di %s = %v, attesa la sola mensile", piano, periodi)
		}
	}

	if periodi := catalogo.Periods(paddle.PlanPro); len(periodi) != 2 {
		t.Errorf("periodicità di pro = %v, attese entrambe", periodi)
	}

	// Il piano Free non si compra: non è in listino e non ha un prezzo.
	if _, ok := catalogo.Price(paddle.PlanFree, paddle.PeriodMonthly); ok {
		t.Error("il piano Free non deve essere acquistabile")
	}
}

// Il difetto da copia-incolla che nessun test funzionale trova: il checkout
// venderebbe il piano giusto e il webhook, tornando indietro dal prezzo, ne
// assegnerebbe uno a caso.
func TestPrezzoRipetutoNonSiCostruisce(t *testing.T) {
	valori := map[string]string{
		"PADDLE_PRICE_PRO_MONTHLY":  "pri_uguale",
		"PADDLE_PRICE_TEAM_MONTHLY": "pri_uguale",
	}

	if _, err := paddle.CatalogFromEnv(ambiente(valori)); err == nil {
		t.Fatal("un prezzo assegnato a due piani deve impedire l'avvio")
	}
}

// Una casella vuota non è un errore: è la macchina di sviluppo su cui è stato
// configurato il solo Pro.
func TestCatalogoParziale(t *testing.T) {
	catalogo, err := paddle.CatalogFromEnv(ambiente(map[string]string{
		"PADDLE_PRICE_PRO_MONTHLY": "  pri_pro_m  ",
	}))
	if err != nil {
		t.Fatalf("costruzione del catalogo: %v", err)
	}
	if catalogo.Empty() {
		t.Fatal("il catalogo non è vuoto")
	}
	if ref, ok := catalogo.Price(paddle.PlanPro, paddle.PeriodMonthly); !ok || ref.PriceID != "pri_pro_m" {
		t.Fatalf("gli spazi ai bordi non sono stati tolti: %+v", ref)
	}
	if _, ok := catalogo.Price(paddle.PlanTeam, paddle.PeriodMonthly); ok {
		t.Error("un piano senza prezzo configurato non è acquistabile qui")
	}
}

func TestCatalogoVuoto(t *testing.T) {
	catalogo, err := paddle.CatalogFromEnv(ambiente(nil))
	if err != nil {
		t.Fatalf("costruzione del catalogo: %v", err)
	}
	if !catalogo.Empty() {
		t.Fatal("senza variabili il catalogo è vuoto")
	}
	// Un prezzo sconosciuto non ha un ripiego: assegnare un piano a caso sarebbe
	// peggio che non assegnarne nessuno.
	if _, ok := catalogo.Plan("pri_qualsiasi"); ok {
		t.Error("un prezzo sconosciuto non deve risolvere a un piano")
	}
}

// L'ordine è quello del listino di SPEC §8: è l'ordine in cui il checkout
// presenta le opzioni.
func TestOrdineDelListino(t *testing.T) {
	catalogo, err := paddle.CatalogFromEnv(ambiente(listinoCompleto))
	if err != nil {
		t.Fatalf("costruzione del catalogo: %v", err)
	}

	atteso := []paddle.PriceRef{
		{PlanCode: paddle.PlanPro, Period: paddle.PeriodMonthly, PriceID: "pri_pro_m"},
		{PlanCode: paddle.PlanPro, Period: paddle.PeriodYearly, PriceID: "pri_pro_y"},
		{PlanCode: paddle.PlanTeam, Period: paddle.PeriodMonthly, PriceID: "pri_team_m"},
		{PlanCode: paddle.PlanAgency, Period: paddle.PeriodMonthly, PriceID: "pri_agency_m"},
	}
	entries := catalogo.Entries()
	if len(entries) != len(atteso) {
		t.Fatalf("righe = %d, attese %d", len(entries), len(atteso))
	}
	for i, ref := range entries {
		if ref != atteso[i] {
			t.Errorf("riga %d = %+v, attesa %+v", i, ref, atteso[i])
		}
	}
}
