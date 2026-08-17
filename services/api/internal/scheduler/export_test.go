package scheduler

// Le query calde sono esposte ai test per poterne verificare il **piano di
// esecuzione**, non solo il risultato. Che l'indice esista lo dice la
// migrazione; che il pianificatore lo usi davvero lo dice solo un EXPLAIN sulla
// query esatta che gira in produzione — ed è per questo che i test devono poter
// leggere proprio queste costanti, non una loro copia scritta a mano che
// potrebbe divergere.
var (
	DueJobsSQL            = dueJobsSQL
	UnscheduledJobsSQL    = unscheduledJobsSQL
	PendingOccurrencesSQL = pendingOccurrencesSQL
)
