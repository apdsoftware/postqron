package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// ------------------------------------------------------ partizioni future (1)

// ensurePartitionsSQL delega alla funzione della 0006 invece di ricomporre i
// confini qui.
//
// È la stessa ragione per cui `githubhookpg.Purge` delega alla propria: i
// confini di una partizione sono la definizione di dove finiscono le righe, e
// due posti che la esprimono divergono al primo fuso orario diverso. Quella
// funzione li calcola in UTC esplicito proprio perché non dipendano dalla
// sessione che la chiama.
const ensurePartitionsSQL = `SELECT job_executions_ensure_partitions($1::integer, $2::integer)`

// ensurePartitions prepara la finestra di partizioni future. Restituisce quante
// ne ha create o confermate e se ha rinunciato al lock.
//
// Va prima di ogni cancellazione, ed è il passo che rende questo pacchetto
// necessario anche a chi non ha nulla da cancellare: la 0006 crea quattordici
// giorni in avanti al momento della migrazione, e da lì in poi nessuno. Senza
// questa chiamata gli inserimenti falliscono — verificato: `23514`, «no
// partition of relation "job_executions" found for row» — due settimane dopo il
// deploy, e il motore smette di scrivere per una ragione che nel codice non è
// scritta da nessuna parte.
//
// # Anche questo passo va sotto lock_timeout
//
// `CREATE TABLE ... PARTITION OF` prende ACCESS EXCLUSIVE sulla tabella padre
// esattamente come il DROP: crea la partizione **e la attacca**, e attaccare
// cambia il descrittore del padre. Misurato: dietro una transazione di
// scrittura aperta, la creazione di una partizione nuova resta in attesa e con
// lei ogni scrittura successiva.
//
// La cosa che rende il problema piccolo invece che quotidiano è che il caso
// normale non prende nessun lock: `CREATE TABLE IF NOT EXISTS` su una partizione
// che esiste già è un no-op che non tocca il padre — misurato in un millisecondo
// e mezzo contro lo stesso scrittore che faceva scadere il caso precedente. Il
// lock serve una volta al giorno, per la giornata nuova in fondo alla finestra.
//
// Rinunciare è quindi sicuro: la finestra è di due settimane e le passate sono
// orarie, cioè ci sono trecento tentativi fra la prima rinuncia e la prima
// scrittura che fallirebbe. È comunque un fatto da contare — vedi
// [Stats.EnsureDeferred] — perché una rinuncia che si ripete per giorni è
// l'unico preavviso che quel margine esiste.
func (s *Service) ensurePartitions(ctx context.Context) (int, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, annotate("apertura della transazione delle partizioni", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.applyLockTimeout(ctx, tx); err != nil {
		return 0, false, err
	}

	var ensured int
	if err := tx.QueryRow(ctx, ensurePartitionsSQL, s.daysAhead, daysBehind).Scan(&ensured); err != nil {
		if isLockTimeout(err) {
			s.log.Warn("retention: creazione delle partizioni future rimandata, lock occupato",
				slog.Int("giorni_di_margine", s.daysAhead),
				slog.Duration("attesa", s.lockTimeout))
			return 0, true, nil
		}
		return 0, false, annotate("preparazione delle partizioni future", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, annotate("conferma delle partizioni future", err)
	}
	return ensured, false, nil
}

// ------------------------------------------------------- finestra dei piani (2)

// retentionWindowSQL restituisce la retention più lunga e la più corta fra i
// piani **rappresentati**, in giorni.
//
// Il piano `free` compare sempre nell'unione, e non è una svista: chi non ha una
// sottoscrizione viva ricade su `free` (vedi `jobspg.Store.PlanForUser`, che
// usa lo stesso `coalesce` e lo stesso `status <> 'canceled'`), e la
// registrazione non crea una riga in `subscriptions`. Includerlo sempre sbaglia
// nella direzione sicura in entrambi gli estremi: non può alzare il massimo,
// perché tre giorni sono il minimo del listino, e può solo abbassare il minimo,
// cioè far esaminare più righe di quante servano — mai cancellarne una in più.
//
// `status <> 'canceled'` è la definizione di «viva» che il database stesso dà,
// nell'indice parziale `subscriptions_one_live_per_user_idx`. Riscriverla qui
// come elenco di stati la farebbe divergere al primo stato aggiunto all'enum.
const retentionWindowSQL = `
	SELECT max(days), min(days)
	  FROM (
	        SELECT p.log_retention_days AS days
	          FROM plans p
	         WHERE p.code = 'free'
	        UNION ALL
	        SELECT p.log_retention_days
	          FROM plans p
	          JOIN subscriptions s
	            ON s.plan_code = p.code AND s.status <> 'canceled'
	       ) AS represented`

// window sono i due estremi della retention in circolazione, in giorni.
type window struct {
	// longest è il taglio del DROP: una partizione si elimina solo quando è
	// superflua per **l'ultimo** dei piani rappresentati.
	longest int
	// shortest delimita da dove la cancellazione a righe comincia a guardare.
	// Non è un taglio: è il filtro più largo possibile, quello che permette a
	// PostgreSQL di potare le partizioni ancora calde invece di leggerle.
	shortest int
}

// retentionWindow legge i due estremi dal listino.
func (s *Service) retentionWindow(ctx context.Context) (window, error) {
	var longest, shortest *int32
	if err := s.pool.QueryRow(ctx, retentionWindowSQL).Scan(&longest, &shortest); err != nil {
		return window{}, annotate("lettura della retention dei piani", err)
	}
	if longest == nil || shortest == nil {
		// L'unione contiene sempre `free`, a meno che `plans` sia vuota. Dirlo è
		// meglio che applicare un taglio inventato a una tabella di log.
		return window{}, errors.New("retention: nessun piano trovato: la migrazione 0003 non è stata applicata")
	}
	return window{longest: int(*longest), shortest: int(*shortest)}, nil
}

// ------------------------------------------------- DROP delle partizioni (3)

// dropPartitionsSQL delega alla funzione della 0006, che elimina le partizioni
// interamente anteriori al taglio.
const dropPartitionsSQL = `SELECT job_executions_drop_partitions_before($1::date)`

// dropExpiredPartitions elimina le partizioni interamente oltre la retention più
// lunga in circolazione. Restituisce quante ne ha eliminate e se ha rinunciato
// al lock.
//
// # Il taglio
//
// `oggi(UTC) − longest`. La partizione del giorno D copre `[D, D+1)`, e la
// funzione elimina quelle con `D < taglio`: la riga più recente che se ne va ha
// quindi `scheduled_for < (oggi − longest) 00:00`, che precede
// `now − longest` per qualunque ora del giorno. Il conto è lo stesso di
// [jobs.Plan.RetentionFloor] e sbaglia dalla parte del conservare.
//
// # Il lock, che è il punto
//
// Eliminare una partizione richiede ACCESS EXCLUSIVE **sulla tabella padre**:
// non è un'operazione locale alla partizione vecchia, tocca il descrittore di
// `job_executions`. Un DROP che aspetta quel lock non aspetta da solo — la coda
// dei lock è ordinata, e ogni INSERT che arriva dopo si accoda dietro di lui,
// anche verso la partizione di oggi che con quella vecchia non c'entra nulla.
// Misurato su PostgreSQL vero: sei secondi di attesa per un inserimento
// estraneo, con una sola transazione di scrittura aperta.
//
// Da qui `SET LOCAL lock_timeout`: se il lock non si prende subito, la passata
// rinuncia e riprova fra un'ora. La retention si misura in giorni — rimandarla
// non costa niente — mentre fermare il dispatch costa esecuzioni in ritardo.
//
// # Perché una sola transazione per tutte le partizioni
//
// Perché la funzione della 0006 è scritta così, ed è la scelta giusta a regime:
// a passata oraria c'è al massimo una partizione da eliminare al giorno, quindi
// la transazione tiene il lock per il tempo di un `DROP TABLE` su una tabella
// che nessuno sta leggendo. Il caso in cui ce ne sono molte — una prima passata
// dopo un fermo lungo — è anche il caso in cui il tutto-o-niente è desiderabile:
// meglio riprovare l'intero arretrato con il lock libero che prenderlo e
// rilasciarlo cento volte di fila.
func (s *Service) dropExpiredPartitions(ctx context.Context, longest int) (int, bool, error) {
	cutoff := s.now().UTC().AddDate(0, 0, -longest)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, annotate("apertura della transazione di DROP", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.applyLockTimeout(ctx, tx); err != nil {
		return 0, false, err
	}

	var dropped int
	if err := tx.QueryRow(ctx, dropPartitionsSQL, cutoff).Scan(&dropped); err != nil {
		if isLockTimeout(err) {
			// Esito voluto, non guasto: qualcuno stava scrivendo e gli è stata
			// lasciata la precedenza.
			s.log.Warn("retention: DROP di partizione rimandato, lock occupato",
				slog.String("taglio", cutoff.Format(time.DateOnly)),
				slog.Duration("attesa", s.lockTimeout))
			return 0, true, nil
		}
		return 0, false, annotate("eliminazione delle partizioni scadute", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, annotate("conferma dell'eliminazione delle partizioni", err)
	}
	return dropped, false, nil
}

// -------------------------------------------- cancellazione a righe (4)

// deleteBatchSQL cancella un lotto di righe scadute rispetto al piano del **loro
// proprietario**.
//
// # La risoluzione del piano
//
// È la stessa di `jobspg.Store.PlanForUser`: la sottoscrizione non annullata, e
// `free` per chi non ne ha. L'indice parziale
// `subscriptions_one_live_per_user_idx` garantisce che ce ne sia al massimo una,
// quindi la LEFT JOIN non moltiplica le righe. Deve essere la stessa risoluzione
// che l'API usa in lettura: R10-bis nasconde le righe oltre il confine del
// piano, e se qui il confine fosse un altro l'utente vedrebbe sparire righe che
// l'API gli stava ancora mostrando, oppure — peggio — resterebbero righe che
// nessuno può più vedere e che la privacy policy dice cancellate.
//
// # Perché due condizioni su `scheduled_for`
//
// La prima, contro il taglio più largo del listino, è una costante e serve a far
// potare le partizioni a PostgreSQL. La seconda è il confine vero, ed è per
// utente — ma dipende da una colonna che arriva da una join, quindi non pota
// niente: da sola, ogni lotto leggerebbe anche le partizioni calde, quelle su
// cui il motore sta scrivendo in questo istante, per scoprire che non
// contengono nulla di scaduto.
//
// Verificato con EXPLAIN: con il taglio a tre giorni la scansione si ferma alla
// partizione di tre giorni fa e le successive non vengono aperte. La lista dei
// bersagli del DELETE resta invece l'intera tabella — PostgreSQL espande le
// relazioni di destinazione al momento del piano — e va saputo: significa un
// RowExclusiveLock per partizione, che è compatibile con gli inserimenti del
// motore e non li mette in coda. È la differenza fra un costo di pianificazione
// e uno di lettura, e solo il secondo cresce con il volume dei log.
//
// # `FOR UPDATE ... SKIP LOCKED`
//
// Una riga che qualcun altro sta toccando adesso non si aspetta: si salta, e la
// prende la passata dopo. È ciò che impedisce alla cancellazione di mettersi in
// mezzo al dispatch. In pratica il caso è raro — la retention più corta è tre
// giorni e l'orizzonte di recupero del motore è due — ma «raro» e «impossibile»
// si comportano diversamente sotto carico, e la differenza si paga in
// esecuzioni bloccate.
//
// # Perché la quaterna e non un id
//
// `job_executions` non ha chiave surrogata, per la ragione dichiarata nella
// 0006: un `id uuid` costerebbe un secondo indice sull'unica tabella con questo
// volume di scritture. Le righe si indirizzano quindi per chiave naturale, che è
// anche l'unica cosa che le identifica davvero.
const deleteBatchSQL = `
	WITH doomed AS (
	    SELECT e.job_id, e.scheduled_for, e.environment, e.attempt
	      FROM job_executions e
	      JOIN jobs j ON j.id = e.job_id
	      LEFT JOIN subscriptions s
	             ON s.user_id = j.user_id AND s.status <> 'canceled'
	      JOIN plans p ON p.code = coalesce(s.plan_code, 'free')
	     WHERE e.scheduled_for < $1
	       AND e.scheduled_for < $2::timestamptz - make_interval(days => p.log_retention_days)
	     LIMIT $3
	     FOR UPDATE OF e SKIP LOCKED
	)
	DELETE FROM job_executions e
	 USING doomed d
	 WHERE e.scheduled_for < $1
	   AND e.job_id = d.job_id
	   AND e.scheduled_for = d.scheduled_for
	   AND e.environment = d.environment
	   AND e.attempt = d.attempt`

// deleteExpiredRows cancella a lotti le righe dei piani a retention corta rimaste
// dentro partizioni ancora vive. Restituisce righe, lotti e se ha lasciato
// lavoro indietro.
//
// # Perché a lotti, e perché con una pausa
//
// Un DELETE unico su milioni di righe prende un numero enorme di lock di riga,
// scrive un pezzo di WAL proporzionale e tiene aperta una transazione lunga —
// che a sua volta blocca l'orizzonte di vacuum, cioè impedisce di riusare lo
// spazio che sta liberando. A un secondo di risoluzione il motore sta scrivendo
// sulla stessa tabella mentre tutto questo accade. Lotti separati significano
// transazioni brevi; la pausa fra un lotto e l'altro è ciò che li rende davvero
// separati, dando ad autovacuum e al flush del WAL il tempo di stare al passo.
//
// # Perché il tetto ai lotti
//
// Perché una passata deve finire. Se il cancellatore è stato fermo abbastanza a
// lungo da accumulare decine di milioni di righe, la prima passata utile ne
// porta via quante ne può e dichiara in [Stats.Truncated] di aver lasciato il
// resto: un tetto silenzioso si legge come «ho finito» quando non è vero.
func (s *Service) deleteExpiredRows(ctx context.Context, shortest int) (int64, int, bool, error) {
	var (
		total   int64
		batches int
	)

	for batches < s.maxBatches {
		if err := ctx.Err(); err != nil {
			return total, batches, true, nil
		}

		affected, err := s.deleteBatch(ctx, shortest)
		if err != nil {
			if isLockTimeout(err) {
				// Come per il DROP: qualcuno stava scrivendo. Si riprova fra un'ora.
				s.log.Warn("retention: lotto rimandato, lock occupato",
					slog.Int("lotti_completati", batches))
				return total, batches, true, nil
			}
			return total, batches, false, annotate("cancellazione delle esecuzioni scadute", err)
		}
		if affected == 0 {
			// Niente più righe scadute — o tutte quelle rimaste sono in mano a
			// qualcun altro, ed è la passata successiva a doversene occupare.
			return total, batches, false, nil
		}

		total += affected
		batches++

		if err := sleep(ctx, s.pause); err != nil {
			return total, batches, true, nil
		}
	}

	return total, batches, true, nil
}

// deleteBatch cancella un lotto solo, nella propria transazione.
func (s *Service) deleteBatch(ctx context.Context, shortest int) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.applyLockTimeout(ctx, tx); err != nil {
		return 0, err
	}

	now := s.now().UTC()
	// Il filtro largo che pota le partizioni: nessuna riga può essere scaduta
	// prima della retention più corta del listino.
	horizon := now.AddDate(0, 0, -shortest)

	tag, err := tx.Exec(ctx, deleteBatchSQL, horizon, now, s.batch)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// applyLockTimeout limita l'attesa sui lock alla transazione corrente.
//
// `SET LOCAL` e non `SET`: la connessione torna al pool con i suoi valori
// originali, e un'altra parte del processo che la riceve dopo non eredita in
// silenzio un `lock_timeout` che non ha chiesto.
//
// Il valore è interpolato e non passato come parametro perché `SET` non accetta
// parametri: è un comando di utilità, non una query. L'interpolazione è sicura
// perché ciò che entra è un intero — i millisecondi, che è anche l'unità in cui
// PostgreSQL legge `lock_timeout` senza suffisso — e non una stringa che arrivi
// da fuori.
func (s *Service) applyLockTimeout(ctx context.Context, tx pgx.Tx) error {
	millis := s.lockTimeout.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = %d", millis)); err != nil {
		return annotate("impostazione di lock_timeout", err)
	}
	return nil
}
