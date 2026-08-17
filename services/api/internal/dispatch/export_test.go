package dispatch

import "time"

// Gli statement di stato sono esposti ai test per due motivi che nessun altro
// controllo copre: verificarne il **piano di esecuzione** — che l'indice esista
// lo dice la migrazione, che il pianificatore lo usi lo dice solo un EXPLAIN — e
// poterli eseguire nella forma esatta che gira in produzione, invece che su una
// copia scritta a mano che con il tempo diverge.
var (
	ClaimSQL   = claimSQL
	FinishSQL  = finishSQL
	SkipSQL    = skipSQL
	ReleaseSQL = releaseSQL
)

// UseTestClock sostituisce l'orologio e il timer del retry.
//
// Sono le due sole dipendenze del pool dal tempo che passa, e stanno qui invece
// che in [Options] perché nessuno in esercizio ha motivo di cambiarle: sostituire
// [time.Now] in produzione non risolve nessun problema, mentre in un test è la
// differenza fra provare il backoff e **aspettarlo**. La regola del retry che
// dipende dall'orologio — un tentativo che scavalca l'occorrenza successiva —
// altrimenti si potrebbe provare solo con occorrenze reali e attese vere, cioè
// con un test lento e a tempo, che è la forma di test che comincia a fallire da
// sola.
//
// Va chiamata prima di [Pool.Start].
func (p *Pool) UseTestClock(now func() time.Time, after func(time.Duration) <-chan time.Time) {
	if now != nil {
		p.now = now
	}
	if after != nil {
		p.after = after
	}
}

// MaxTextLength è il tetto di `response_excerpt` e `error`, in caratteri.
const MaxTextLength = maxTextLength
