package retry

import (
	"math/rand/v2"
	"net/http"
	"time"
)

// Delay è fra quanto va fatto il tentativo successivo a quello numero
// `attempt`.
//
// Il risultato è la somma di due cose diverse, ed è utile tenerle distinte:
//
//	ritardo = pavimento chiesto dal bersaglio + finestra di backoff dispersa
//
// # Il pavimento
//
// Se il bersaglio ha risposto `Retry-After`, quel valore è un **minimo**, non il
// ritardo: ha detto «non prima di allora», non «esattamente allora». Si onora
// per intero (tagliato a [Limits.MaxRetryAfter]) e la dispersione si somma
// sopra, mai sotto — sottrarla significherebbe tornare prima del momento in cui
// il bersaglio ha detto di essere pronto, cioè ignorare l'unica indicazione
// attendibile che abbiamo sul suo stato.
//
// Onorarlo alla lettera, però, sarebbe l'errore opposto: mille job che ricevono
// `Retry-After: 60` dallo stesso bersaglio in difficoltà tornerebbero tutti al
// sessantesimo secondo, in una raffica più compatta di quella che l'ha messo in
// difficoltà. Il pavimento toglie il *quando*, non il *tutti insieme*: la
// dispersione serve anche — soprattutto — in questo caso.
//
// # La finestra
//
// Cresce come dice il [Backoff] del job, tagliata a [Limits.MaxDelay]:
//
//	esponenziale   base, 2·base, 4·base, 8·base, …
//	lineare        base, 2·base, 3·base, 4·base, …
//	fisso          base, base, base, base, …
//
// # La dispersione
//
// Il ritardo restituito non è la finestra: è un istante **estratto a caso dentro
// la sua metà superiore**, cioè in [finestra/2, finestra).
//
// La scelta della metà superiore invece dell'intera finestra è il punto
// delicato. La dispersione piena — un valore qualunque fra zero e la finestra —
// disperde meglio, ma consente ritardi vicini a zero: il tentativo successivo
// arriverebbe *subito* addosso a un bersaglio che ha appena fallito, e il
// backoff smetterebbe di essere un backoff nel caso in cui serve di più. Con la
// metà superiore il ritardo minimo cresce comunque a ogni tentativo — che è la
// garanzia di R5 — e la finestra su cui i job si spargono resta ampia quanto il
// backoff stesso, che è quanto basta perché mille fallimenti simultanei non
// tornino insieme.
//
// La differenza si vede solo in aggregato, ed è per questo che va provata in
// aggregato: vedi TestLaDispersioneCopreLaFinestra e
// TestMilleJobFallitiInsiemeNonRitentanoInsieme.
func (p *Planner) Delay(b Backoff, attempt int, out Outcome) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return p.floor(out) + p.disperse(p.window(b, attempt))
}

// window è la finestra di backoff nuda, prima della dispersione.
//
// I controlli sul segno non sono cerimoniali: `base << 62` è negativo, e un
// ritardo negativo diventerebbe un tentativo immediato — esattamente il
// contrario di ciò che il backoff deve produrre. Qualunque conto che esce
// dall'intervallo utile finisce sul tetto, che è il valore giusto per un
// tentativo così avanzato.
func (p *Planner) window(b Backoff, attempt int) time.Duration {
	base := p.limits.Base
	var d time.Duration

	switch b {
	case Fixed:
		d = base
	case Linear:
		d = base * time.Duration(attempt)
	default:
		// Exponential, e ogni valore che questo package non conosce: vedi
		// [Policy.Backoff].
		shift := attempt - 1
		if shift >= 62 {
			return p.limits.MaxDelay
		}
		d = base << uint(shift)
	}

	if d <= 0 || d > p.limits.MaxDelay {
		return p.limits.MaxDelay
	}
	return d
}

// disperse estrae il ritardo dentro la metà superiore della finestra.
//
// Una finestra troppo corta perché la metà abbia senso — sotto i due nanosecondi
// — torna così com'è: non c'è niente da disperdere, e chiedere un intero in
// [0, 0) sarebbe un errore.
func (p *Planner) disperse(window time.Duration) time.Duration {
	half := window / 2
	if half <= 0 {
		return window
	}
	return half + time.Duration(p.limits.Rand(int64(half)))
}

// floor è il minimo chiesto dal bersaglio, zero se non ne ha chiesto uno.
func (p *Planner) floor(out Outcome) time.Duration {
	if out.RetryAfter <= 0 || !honoursRetryAfter(out.Status) {
		return 0
	}
	return min(out.RetryAfter, p.limits.MaxRetryAfter)
}

// honoursRetryAfter dice su quali risposte la testata `Retry-After` conta.
//
// Le due che la definiscono sono `429 Too Many Requests` e `503 Service
// Unavailable`, ed è il caso descritto da R5. La condizione è però scritta su
// tutti i `5xx` e non sul solo 503: un bersaglio che manda `Retry-After` insieme
// a un `500` o a un `502` sta dicendo la stessa cosa — «riprova fra tanto» — e
// non c'è nessuna ragione per ignorarlo proprio quando il guasto che dichiara è
// più grave.
//
// Sul resto la testata non si guarda. Su un `4xx` che non si ritenta non
// servirebbe a niente; sulle risposte riuscite `Retry-After` ha un significato
// diverso, che non riguarda i tentativi.
func honoursRetryAfter(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// systemRand è la sorgente di default della dispersione.
//
// `math/rand/v2` è già seminato a caso a ogni avvio e le sue funzioni di
// package sono sicure da più goroutine, che è ciò che serve qui: il
// pianificatore è chiamato da tutti i worker del pool insieme. Non serve un
// generatore crittografico — questi numeri non proteggono niente, spargono un
// carico — e un `sync.Mutex` attorno a un `*rand.Rand` sarebbe un punto di
// contesa sul percorso caldo per nessun guadagno.
func systemRand(n int64) int64 { return rand.Int64N(n) }
