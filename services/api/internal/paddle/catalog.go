package paddle

import (
	"fmt"
	"slices"
	"strings"
)

// Period è la periodicità di fatturazione, uguale all'enumerato
// `billing_period` della migrazione 0001.
type Period string

const (
	PeriodMonthly Period = "monthly"
	PeriodYearly  Period = "yearly"
)

// Codici dei piani di SPEC §8. Sono le chiavi della tabella `plans`, e stanno
// qui perché il catalogo deve nominarli: quali *limiti* portino resta un dato
// del database, questo è solo il nome sotto cui cercarli.
const (
	PlanFree   = "free"
	PlanPro    = "pro"
	PlanTeam   = "team"
	PlanAgency = "agency"
)

// PriceRef è una riga del catalogo: quale piano, con quale periodicità, dietro
// quale prezzo Paddle.
//
// **Non contiene l'importo**, e non è una dimenticanza. Il catalogo Paddle è la
// fonte di verità dei prezzi (R61): una copia qui sarebbe una seconda cifra
// libera di divergere dalla prima, e la prima è quella che il cliente paga.
// `PriceID` è un riferimento a quella riga, non il suo contenuto.
type PriceRef struct {
	PlanCode string
	Period   Period
	PriceID  string
}

// slot è una casella del catalogo: la variabile d'ambiente che porta il prezzo,
// e cosa quel prezzo compra.
//
// **L'elenco è la forma del listino, ed è qui che vive R62.** Team e Agency non
// hanno una casella annuale — non perché ce ne siamo dimenticati, ma perché
// l'annuale esiste solo su Pro, ed è una scelta deliberata di SPEC §8 e R62. La
// conseguenza è che un annuale su Team non è rifiutato da un `if` che qualcuno
// può cancellare: **non ha un prezzo da comprare**, che è un modo molto più
// difficile di sbagliare.
//
// L'ordine è quello del listino (SPEC §8): è l'ordine in cui il checkout
// presenta le opzioni a chi le chiede.
var slots = []struct {
	EnvVar   string
	PlanCode string
	Period   Period
}{
	{"PADDLE_PRICE_PRO_MONTHLY", PlanPro, PeriodMonthly},
	{"PADDLE_PRICE_PRO_YEARLY", PlanPro, PeriodYearly},
	{"PADDLE_PRICE_TEAM_MONTHLY", PlanTeam, PeriodMonthly},
	{"PADDLE_PRICE_AGENCY_MONTHLY", PlanAgency, PeriodMonthly},
}

// Catalog collega i prezzi Paddle ai piani di SPEC §8, nelle due direzioni.
//
// Le due direzioni servono a due chiamanti diversi e vanno entrambe: il checkout
// parte da un piano e ha bisogno del prezzo; il webhook parte dal prezzo che
// l'utente ha effettivamente comprato e ha bisogno del piano. Farlo con una sola
// mappa e una scansione lineare funzionerebbe — sono quattro righe — ma
// renderebbe possibile che due prezzi diversi rispondano allo stesso piano senza
// che nessuno se ne accorga: vedi [CatalogFromEnv], che quel caso lo rifiuta.
//
// # Perché il catalogo sta nell'ambiente e non in `plans`
//
// La migrazione 0003 ha le colonne `paddle_price_id_monthly` e
// `paddle_price_id_yearly`, e restano vuote. Non è una svista: **gli
// identificativi di prezzo sono diversi in sandbox e in produzione**, mentre le
// righe di `plans` sono le stesse ovunque, inserite dalla migrazione. Metterli lì
// significherebbe o una migrazione che vale in un solo ambiente, o un UPDATE a
// mano dopo ogni deploy che qualcuno prima o poi non fa — e un `price_id` di
// sandbox in produzione è un checkout che non incassa. La configurazione che
// cambia con l'ambiente sta nell'ambiente; le colonne restano per un catalogo
// gestito dall'area admin, se un giorno servirà.
type Catalog struct {
	byPrice map[string]PriceRef
	entries []PriceRef
}

// CatalogFromEnv costruisce il catalogo dalle variabili di CREDENTIALS §1.
//
// Una casella vuota non è un errore: significa che quel piano non è acquistabile
// su questa macchina, ed è la situazione normale di uno sviluppo in cui è stato
// configurato il solo Pro. Un catalogo interamente vuoto nemmeno — è la macchina
// di chi non ha Paddle, e chi lo riceve non registra le rotte di checkout.
//
// **Due caselle con lo stesso `price_id` sono invece un errore d'avvio.** È il
// difetto da copia-incolla che nessun test funzionale trova: il checkout
// venderebbe il piano giusto e il webhook, tornando indietro dal prezzo,
// assegnerebbe quello sbagliato — a caso, secondo l'ordine di iterazione di una
// mappa. Meglio non partire.
func CatalogFromEnv(getenv func(string) string) (Catalog, error) {
	catalog := Catalog{byPrice: make(map[string]PriceRef, len(slots))}
	for _, slot := range slots {
		priceID := strings.TrimSpace(getenv(slot.EnvVar))
		if priceID == "" {
			continue
		}
		if existing, taken := catalog.byPrice[priceID]; taken {
			return Catalog{}, fmt.Errorf(
				"paddle: %s ripete il prezzo già assegnato a %s/%s: un prezzo non può comprare due piani",
				slot.EnvVar, existing.PlanCode, existing.Period)
		}
		ref := PriceRef{PlanCode: slot.PlanCode, Period: slot.Period, PriceID: priceID}
		catalog.byPrice[priceID] = ref
		catalog.entries = append(catalog.entries, ref)
	}
	return catalog, nil
}

// Empty indica un catalogo senza nessun prezzo configurato.
func (c Catalog) Empty() bool { return len(c.entries) == 0 }

// Entries elenca le righe configurate, nell'ordine del listino.
func (c Catalog) Entries() []PriceRef { return slices.Clone(c.entries) }

// Plan risale dal prezzo comprato al piano che dà.
//
// È la lettura del webhook: la sottoscrizione dichiara i propri `price_id`, e
// questo è l'unico punto in cui diventano un piano. Il secondo valore è falso per
// un prezzo che non conosciamo — un prodotto creato a mano nel pannello di
// Paddle, o un catalogo di produzione letto con la configurazione di sandbox — e
// **quel caso non ha un ripiego**: assegnare un piano a caso sarebbe peggio che
// non assegnarne nessuno.
func (c Catalog) Plan(priceID string) (PriceRef, bool) {
	ref, ok := c.byPrice[strings.TrimSpace(priceID)]
	return ref, ok
}

// Price risale dal piano e dalla periodicità al prezzo da comprare.
//
// È la lettura del checkout. Il secondo valore è falso quando la combinazione
// non è in listino, e i due casi che la producono sono diversi fra loro benché
// la risposta sia la stessa: un piano non configurato su questa macchina, e un
// annuale su un piano che l'annuale non ce l'ha (R62). Chi chiama li distingue
// nel messaggio, non qui.
func (c Catalog) Price(planCode string, period Period) (PriceRef, bool) {
	planCode = strings.TrimSpace(planCode)
	for _, entry := range c.entries {
		if entry.PlanCode == planCode && entry.Period == period {
			return entry, true
		}
	}
	return PriceRef{}, false
}

// Periods elenca le periodicità disponibili per un piano, nell'ordine del
// listino. Vuoto per un piano non acquistabile.
func (c Catalog) Periods(planCode string) []Period {
	planCode = strings.TrimSpace(planCode)
	var out []Period
	for _, entry := range c.entries {
		if entry.PlanCode == planCode {
			out = append(out, entry.Period)
		}
	}
	return out
}
