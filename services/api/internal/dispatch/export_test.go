package dispatch

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

// MaxTextLength è il tetto di `response_excerpt` e `error`, in caratteri.
const MaxTextLength = maxTextLength
