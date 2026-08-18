package metrics

import (
	"sync/atomic"
	"time"
)

// lagBuckets sono i confini superiori dell'istogramma del ritardo di dispatch,
// in secondi.
//
// Sono scelti attorno alla tolleranza dichiarata (un secondo, R47), che è il
// punto in cui la misura smette di essere una curiosità e diventa un impegno
// non mantenuto: quattro confini sotto di essa dicono *quanto* margine c'è
// quando le cose vanno bene, e la coda lunga fino a cinque minuti serve a
// distinguere un motore in affanno da uno che è appena ripartito dopo un fermo
// — sono due forme diverse della stessa curva, e con confini troppo stretti si
// leggerebbero uguali.
//
// Undici confini sono anche un costo dichiarato: la registrazione è una scansione
// lineare su questo vettore, cioè al massimo undici confronti fra float, e la
// pagina esposta cresce di undici righe. Un istogramma a cinquanta confini
// misurerebbe la stessa cosa e costerebbe cinque volte tanto a chi raccoglie.
var lagBuckets = [...]float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300}

// histogram è la distribuzione del ritardo, in atomiche.
//
// Non c'è nessun lock: i contatori sono indipendenti e chi legge accetta di
// vedere una fotografia leggermente incoerente — un'osservazione può essere
// finita in un secchio e non ancora nella somma. È la stessa garanzia che dà
// qualunque istogramma raccolto senza fermare il mondo, e su una misura di
// tendenza non cambia niente.
type histogram struct {
	// counts ha un secchio per confine, più l'ultimo che è `+Inf`.
	counts [len(lagBuckets) + 1]atomic.Int64
	// sum è la somma dei ritardi in nanosecondi, ed è tenuta in interi perché
	// un `float64` sommato atomicamente richiederebbe un CAS in ciclo per
	// guadagnare una precisione che a queste grandezze non serve.
	sum atomic.Int64
	// max è il ritardo massimo osservato dall'avvio. Esiste perché un istogramma
	// non lo sa dire: il secchio più alto dice «oltre trecento secondi», non
	// quanto oltre — e nel giorno storto è proprio quel numero che si vuole.
	max atomic.Int64
}

// observe registra un ritardo.
//
// Un ritardo negativo — un'occorrenza consegnata prima del suo orario teorico —
// non è un errore di misura di cui lamentarsi ma nemmeno un dato: succede solo
// se l'orologio è tornato indietro, e vale zero. Contarlo con il segno
// abbasserebbe la somma di una quantità inventata.
func (h *histogram) observe(d time.Duration) {
	if d < 0 {
		d = 0
	}
	seconds := d.Seconds()

	i := len(lagBuckets)
	for j, bound := range lagBuckets {
		if seconds <= bound {
			i = j
			break
		}
	}
	h.counts[i].Add(1)
	h.sum.Add(int64(d))

	for {
		current := h.max.Load()
		if int64(d) <= current || h.max.CompareAndSwap(current, int64(d)) {
			break
		}
	}
}

// snapshot restituisce i secchi **cumulativi**, la somma in secondi, il totale e
// il massimo.
//
// Cumulativi perché è la forma che il formato di esposizione chiede: il valore
// di `le="1"` è «quante osservazioni sono state al massimo un secondo», non
// «quante sono cadute in quell'intervallo».
func (h *histogram) snapshot() (buckets [len(lagBuckets) + 1]int64, sum float64, count, max int64) {
	var running int64
	for i := range h.counts {
		running += h.counts[i].Load()
		buckets[i] = running
	}
	return buckets, time.Duration(h.sum.Load()).Seconds(), running, h.max.Load()
}
