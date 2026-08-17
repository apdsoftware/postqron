package notify

import (
	"fmt"
	"time"
)

// Policy è la difesa dalla tempesta di avvisi, e le scelte che contiene sono di
// prodotto prima che tecniche.
//
// # Il problema
//
// Un job a `every: 1s` che fallisce in continuazione produce 86.400 fallimenti
// al giorno. Un'email per fallimento sarebbe il modo più rapido di finire in
// suppression list — e per R20.1 non lo scopriremmo dalla risposta all'invio,
// che resta `202` identica. Da quel momento l'utente **non riceve più nemmeno le
// email che contano**: l'avviso di sicurezza, la variazione di piano, il
// benvenuto di un secondo account. Il costo di sbagliare qui non è una casella
// piena: è la perdita silenziosa di tutto il canale.
//
// # La scelta: raggruppare, poi limitare, e ripetere
//
// Tre regole, in quest'ordine.
//
//  1. **Una finestra di grazia** ([FailureGrace], cinque minuti). L'avviso non
//     parte all'istante: resta in coda qualche minuto, e ogni fallimento che
//     arriva nel frattempo ne alza il conteggio invece di generare una seconda
//     email. Una raffica diventa **un** messaggio che dice quante volte è
//     successo, che è anche l'informazione più utile: «è fallito ventitré volte»
//     dice più di ventitré email identiche.
//
//  2. **Un tetto per finestra** ([FailureWindow], un'ora). La chiave di
//     deduplicazione contiene l'ora di calendario, e l'indice unico della 0008
//     fa il resto: per una coppia (job, ambiente) esiste **al più un avviso
//     all'ora**, garantito dal database e non da un `if` che due connessioni
//     concorrenti possono attraversare insieme. Il peggio possibile diventa 24
//     email al giorno per job, invece di 86.400.
//
//  3. **La ripetizione è voluta.** L'alternativa ovvia — un avviso solo, alla
//     rottura, e silenzio finché il job non torna a funzionare — manda meno
//     email e sarebbe la scelta sbagliata **proprio per R20.1**: se quell'unico
//     messaggio finisse in una suppression list non lo sapremmo, e l'utente
//     resterebbe con un job fermo da settimane e nessun secondo avviso in
//     arrivo. Ripetere ogni ora è ciò che rende il canale ridondante rispetto a
//     un recapito che non possiamo osservare. Il testo lo dichiara
//     (`job_failed.delivery_note`): l'arrivo di un avviso non prova che gli
//     avvisi funzionino, e la sua assenza non prova che vada tutto bene — la
//     cronologia in dashboard è il registro su cui fare conto (R57).
//
// # Il bordo dei bucket, dichiarato
//
// L'ora è un'ora di calendario, non una finestra scorrevole: un job che fallisce
// alle 10:59 e di nuovo alle 11:01 produce due email a due minuti di distanza.
// È il prezzo di far applicare il tetto all'indice unico invece che a una query
// «esiste un avviso nell'ultima ora?», che fra il SELECT e l'INSERT lascia
// passare due concorrenti — cioè fallisce nel caso denso, che è l'unico che
// conta. Due email di troppo al giorno per job, nel caso peggiore, contro un
// tetto che non regge sotto carico: lo scambio è dichiarato ed è questo.
//
// # La sicurezza ha una finestra sua, e più corta
//
// Anche gli eventi di sicurezza si raggruppano ([SecurityWindow], cinque
// minuti), e non per simmetria: chi si è impossessato di una sessione può
// creare diecimila chiavi API e sommergere la casella del proprietario, che è
// il modo di far finire in suppression list **proprio l'avviso che racconta
// l'intrusione**. La finestra è corta perché un avviso di sicurezza in ritardo
// vale meno di uno puntuale, e la chiave contiene il **tipo** di evento: un
// evento di natura diversa non viene mai soppresso da uno precedente.
type Policy struct {
	// FailureGrace è l'attesa prima di mandare un avviso di job fallito, durante
	// la quale i fallimenti successivi si raggruppano in quello già in coda.
	FailureGrace time.Duration
	// FailureWindow è l'ampiezza del bucket che limita gli avvisi per job e
	// ambiente. Dev'essere un divisore dell'ora, o un multiplo intero di essa,
	// perché il troncamento sia stabile.
	FailureWindow time.Duration
	// SecurityWindow è l'ampiezza del bucket degli eventi di sicurezza, per
	// utente e tipo di evento.
	SecurityWindow time.Duration
}

// I valori predefiniti. Sono scelte, non numeri di comodo: la motivazione di
// ciascuno sta nella documentazione di [Policy].
const (
	DefaultFailureGrace   = 5 * time.Minute
	DefaultFailureWindow  = time.Hour
	DefaultSecurityWindow = 5 * time.Minute
)

// DefaultPolicy è la politica applicata quando non se ne indica una.
func DefaultPolicy() Policy {
	return Policy{
		FailureGrace:   DefaultFailureGrace,
		FailureWindow:  DefaultFailureWindow,
		SecurityWindow: DefaultSecurityWindow,
	}
}

// withDefaults riempie i campi lasciati a zero.
//
// Una finestra a zero non è «nessun raggruppamento»: sarebbe un'email per
// fallimento, cioè il comportamento che questa struttura esiste per impedire.
// Un valore mancante ricade sul predefinito, che è la scelta sicura.
func (p Policy) withDefaults() Policy {
	if p.FailureGrace <= 0 {
		p.FailureGrace = DefaultFailureGrace
	}
	if p.FailureWindow <= 0 {
		p.FailureWindow = DefaultFailureWindow
	}
	if p.SecurityWindow <= 0 {
		p.SecurityWindow = DefaultSecurityWindow
	}
	return p
}

// failureKey è la chiave di raggruppamento di un avviso di job fallito.
//
// Contiene l'ambiente perché una prova in staging e un guasto in produzione sono
// due fatti distinti con esiti separati (R23): sopprimere il secondo perché il
// primo è già stato annunciato sarebbe il peggiore dei silenzi.
func (p Policy) failureKey(jobID, environment string, at time.Time) string {
	return fmt.Sprintf("job_failed:%s:%s:%d",
		jobID, environment, bucket(at, p.FailureWindow).Unix())
}

// securityKey raggruppa per utente e **tipo** di evento: due eventi di natura
// diversa non si sopprimono mai a vicenda.
func (p Policy) securityKey(userID string, kind SecurityKind, at time.Time) string {
	return fmt.Sprintf("security:%s:%s:%d",
		userID, kind, bucket(at, p.SecurityWindow).Unix())
}

// welcomeKey è per utente e senza finestra: il benvenuto si manda una volta
// sola nella vita di un account, e l'indice unico della 0008 è il posto giusto
// in cui dirlo. Una registrazione ripetuta o un `VerifyEmail` chiamato due volte
// non producono un secondo messaggio.
func welcomeKey(userID string) string {
	return "welcome:" + userID
}

// planKey identifica **la variazione**, non l'istante in cui la si è saputa.
//
// Paddle ripete le consegne dei webhook, e la scrittura del piano è idempotente
// per costruzione (filigrana in billingpg): se la chiave contenesse `now()` una
// ripetizione produrrebbe una seconda email per lo stesso fatto. Contiene invece
// il piano di destinazione e il momento di decorrenza, che sono ciò che
// distingue una variazione da un'altra.
func planKey(userID, newPlan string, effectiveAt time.Time) string {
	return fmt.Sprintf("plan_changed:%s:%s:%d", userID, newPlan, effectiveAt.UTC().Unix())
}

// bucket tronca un istante all'inizio della finestra che lo contiene.
//
// Il troncamento è in UTC e su un istante assoluto: [time.Time.Truncate] lavora
// dall'epoca zero e ignora il fuso, che è ciò che serve — la finestra dev'essere
// la stessa per tutti i job del mondo, non spostarsi con il fuso di chi guarda.
func bucket(at time.Time, window time.Duration) time.Time {
	return at.UTC().Truncate(window)
}
