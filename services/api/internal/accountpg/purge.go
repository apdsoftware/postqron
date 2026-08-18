package accountpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/apdsoftware/postqron/services/api/internal/account"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
)

// DueForPurge elenca gli account la cui grazia è scaduta.
//
// `purge_after <= $1` e non `<`: la scadenza è un istante, e chi ci arriva
// esattamente sopra l'ha raggiunta. L'ordine è per scadenza perché con il tetto
// di [account.PurgeOptions.MaxAccounts] qualcuno resta indietro, e a restare
// indietro dev'essere chi ha aspettato di meno.
//
// La query è servita da `users_pending_purge_idx` (0017), parziale sulla stessa
// condizione.
func (s *Store) DueForPurge(ctx context.Context, now time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id::text
		   FROM users
		  WHERE deletion_requested_at IS NOT NULL
		    AND purge_after <= $1
		  ORDER BY purge_after
		  LIMIT $2`, now, limit)
	if err != nil {
		return nil, annotate("ricerca degli account scaduti", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, annotate("lettura di un account scaduto", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, annotate("ricerca degli account scaduti", err)
	}
	return out, nil
}

// Purge rimuove l'account e tutto ciò che gli appartiene.
//
// # Perché due fasi e non una transazione sola
//
// Per il volume di `job_executions`, e il numero merita di essere scritto. Un
// job del piano Agency a un secondo di risoluzione produce 86.400 righe al
// giorno per ambiente; su una retention di novanta giorni sono **15.552.000
// righe per un job solo**, e la 0006 è progettata attorno a questo numero. Una
// `DELETE FROM users` che le portasse via in cascata sarebbe una transazione
// unica su quindici milioni di righe: un pezzo di WAL proporzionale, l'orizzonte
// di vacuum fermo per tutta la sua durata, e i lock di riga presi su una tabella
// su cui il motore sta scrivendo in questo istante — sulla stessa macchina, che
// è la macchina (SPEC §2).
//
// Quindi: prima le esecuzioni, a lotti e con una pausa, come fa
// internal/retention e per le stesse ragioni misurate lì; poi tutto il resto in
// una transazione, che a quel punto è piccola.
//
// # Che cosa costa, in pratica
//
// A 5.000 righe per lotto e 200 ms di pausa, quei quindici milioni e mezzo sono
// 3.110 lotti, cioè **oltre dieci minuti di sola attesa** più il tempo delle
// cancellazioni. Il tetto di [DefaultMaxBatches] li spezza su più passate orarie
// invece di tenere occupato il database per tutto quel tempo di seguito: la
// passata dichiara [account.Purged.Truncated] e riprende un'ora dopo. Un account
// del piano Free — 1.440 righe al giorno per tre giorni — sta in un lotto solo.
//
// # Perché un'interruzione non lascia macerie
//
// La fase a lotti è ripetibile: ogni lotto è la sua transazione, e ciò che è
// stato cancellato resta cancellato. Se il processo muore a metà, l'account è
// ancora lì con `purge_after` scaduta, e la passata successiva riparte da dove
// era arrivata. L'unico stato che non esiste è «mezzo cancellato e non più
// riconoscibile».
func (s *Store) Purge(ctx context.Context, userID string) (account.Purged, error) {
	if !looksLikeUUID(userID) {
		return account.Purged{}, account.ErrNotFound
	}
	purged := account.Purged{UserID: userID}

	jobIDs, err := s.jobIDs(ctx, userID)
	if err != nil {
		return purged, err
	}

	// ------------------------------------------------- fase 1: le esecuzioni
	if len(jobIDs) > 0 {
		executions, batches, truncated, err := s.deleteExecutions(ctx, jobIDs)
		purged.Executions, purged.Batches, purged.Truncated = executions, batches, truncated
		if err != nil {
			return purged, err
		}
		if truncated {
			// L'account resta in piedi con la sua scadenza scaduta: la passata
			// successiva lo riprende. Proseguire adesso significherebbe fare in una
			// transazione unica proprio il lavoro che i lotti servono a evitare.
			return purged, nil
		}
	}

	// ------------------------------------------------------ fase 2: il resto
	if err := s.purgeRemainder(ctx, userID, &purged); err != nil {
		return purged, err
	}
	return purged, nil
}

// jobIDs legge gli identificativi dei job dell'utente.
//
// Servono alla cancellazione delle esecuzioni: `job_executions` non ha
// `user_id`, e la strada per arrivarci è `job_id`. Leggerli una volta invece di
// ripetere una join a ogni lotto è anche ciò che rende ogni lotto una lettura di
// indice — la chiave primaria della 0006 ha `job_id` in testa, quindi ogni
// partizione risponde con una scansione di indice invece che di tabella.
func (s *Store) jobIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM jobs WHERE user_id = $1::uuid`, userID)
	if err != nil {
		return nil, annotate("lettura dei job dell'account", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, annotate("lettura di un job dell'account", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, annotate("lettura dei job dell'account", err)
	}
	return out, nil
}

// deleteExecutionsSQL cancella un lotto di esecuzioni dei job indicati.
//
// # Perché `job_id = ANY(...)` e non una join su `jobs`
//
// Perché è la condizione che l'indice sa usare. La chiave primaria della 0006 è
// `(job_id, scheduled_for, environment, attempt)`: con `job_id` noto, ogni
// partizione risponde con una scansione di indice. Una join su `jobs.user_id`
// costringerebbe invece a leggere le righe per scoprire a chi appartengono.
//
// **Non c'è potatura di partizioni**, e va saputo: `job_id` non è la chiave di
// partizionamento, quindi ogni lotto interroga l'indice di **ogni** partizione
// viva — un centinaio, con novanta giorni di retention e due settimane di
// margine. Sono un centinaio di sonde su indici, non un centinaio di scansioni,
// e non c'è modo di fare meglio: l'insieme da cancellare è definito da chi
// possiede il job, non da quando l'esecuzione era prevista.
//
// # `FOR UPDATE ... SKIP LOCKED`
//
// Una riga che il motore sta toccando adesso non si aspetta: si salta. Alla
// purga arrivano solo account fermi da giorni, quindi il caso è raro — ma «raro»
// e «impossibile» si comportano diversamente sotto carico, e la differenza si
// paga in esecuzioni bloccate di **altri** utenti, perché il lock in coda ferma
// anche loro. Ciò che si salta lo prende la cascata della fase 2, che a quel
// punto ha pochissimo da portare via.
//
// # Perché la quaterna e non un id
//
// `job_executions` non ha chiave surrogata, per la ragione dichiarata nella
// 0006: un `id uuid` costerebbe un secondo indice sull'unica tabella con questo
// volume di scritture.
const deleteExecutionsSQL = `
	WITH doomed AS (
	    SELECT e.job_id, e.scheduled_for, e.environment, e.attempt
	      FROM job_executions e
	     WHERE e.job_id = ANY($1::uuid[])
	     LIMIT $2
	     FOR UPDATE OF e SKIP LOCKED
	)
	DELETE FROM job_executions e
	 USING doomed d
	 WHERE e.job_id = d.job_id
	   AND e.scheduled_for = d.scheduled_for
	   AND e.environment = d.environment
	   AND e.attempt = d.attempt`

// deleteExecutions cancella a lotti le esecuzioni dei job indicati.
func (s *Store) deleteExecutions(ctx context.Context, jobIDs []string) (int64, int, bool, error) {
	var (
		total   int64
		batches int
	)

	for batches < s.maxBatches {
		if err := ctx.Err(); err != nil {
			return total, batches, true, nil
		}

		affected, err := s.deleteExecutionBatch(ctx, jobIDs)
		if err != nil {
			if isLockTimeout(err) {
				// Qualcuno stava scrivendo e gli è stata lasciata la precedenza. Non
				// è un guasto: si riprende alla passata successiva.
				return total, batches, true, nil
			}
			return total, batches, false, annotate("cancellazione delle esecuzioni", err)
		}
		if affected == 0 {
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

func (s *Store) deleteExecutionBatch(ctx context.Context, jobIDs []string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := s.applyLockTimeout(ctx, tx); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, deleteExecutionsSQL, jobIDs, s.batch)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ------------------------------------------------------------ fase 2

// purgeRemainder rimuove tutto il resto in una transazione.
//
// L'ordine non è di comodo: le due tabelle senza `user_id` si risolvono
// **attraverso** righe che la cascata sta per portare via, quindi vanno trattate
// prima che spariscano. `DELETE FROM users` è l'ultima istruzione, ed è quella
// che fa cadere in cascata tutto ciò che ha una foreign key verso di lui.
func (s *Store) purgeRemainder(ctx context.Context, userID string, purged *account.Purged) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return annotate("apertura della transazione di purga", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, jobspg.UserJobsLockKey(userID)); err != nil {
		return annotate("lock sull'utente", err)
	}

	if err := s.purgeAudit(ctx, tx, userID, purged); err != nil {
		return err
	}
	if err := s.purgePaddleEvents(ctx, tx, userID, purged); err != nil {
		return err
	}
	if err := s.purgeGitHubDeliveries(ctx, tx, userID, purged); err != nil {
		return err
	}

	// Il conteggio dei job **prima** della cascata: dopo non c'è più niente da
	// contare, e il numero serve a dire cosa è stato rimosso.
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE user_id = $1::uuid`, userID).Scan(&purged.Jobs); err != nil {
		return annotate("conteggio dei job", err)
	}

	// Da qui in poi non c'è un annulla. Tutto ciò che ha una foreign key verso
	// `users` cade adesso: `api_keys`, `subscriptions`, `repositories`, `jobs` —
	// e con loro `job_executions`, che a questo punto contiene al più le righe
	// saltate dai lotti — `ai_credentials`, `notifications`, `sessions`,
	// `user_tokens`, `workspace_secrets`, `paddle_checkout_intents`. Su
	// `audit_log` la foreign key è `ON DELETE SET NULL` (0008): è ciò che rende
	// anonime le righe sopravvissute alla purga qui sopra.
	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	if err != nil {
		return annotate("cancellazione dell'account", err)
	}
	if tag.RowsAffected() == 0 {
		// Già purgato da una passata concorrente: non è un errore, è la stessa
		// promessa mantenuta due volte.
		return nil
	}

	if err := tx.Commit(ctx); err != nil {
		return annotate("conferma della purga", err)
	}
	return nil
}

// purgeAudit applica ad `audit_log` la distinzione che conta: **di chi sono i
// dati di questa riga?**
//
// La 0008 tiene la tabella append-only e le foreign key a `ON DELETE SET NULL`
// «perché chiudere un account non basti a cancellarne la storia». L'invariante
// che quella scelta protegge è preciso, e più stretto di come suona: **un admin
// non deve poter far sparire la traccia della propria impersonificazione
// convincendo l'utente a chiudere l'account** (SPEC §4.3).
//
// Da qui le due istruzioni:
//
//  1. **Se l'unica persona coinvolta è il cancellato, la riga va via.** Sono le
//     sue azioni sui suoi dati: nessun altro ne è l'oggetto e nessun altro ne ha
//     bisogno. La privacy policy §5 dice «remove the data», e questa è quella
//     data.
//  2. **Se ad agire è stato un altro, la riga resta e si ripulisce.** I
//     riferimenti al cancellato li azzera già la foreign key; qui si svuota
//     `metadata`, che è l'unico campo in cui potrebbe finire qualcosa che lo
//     riguarda. `ip_address` e `user_agent` **non si toccano**: sono dell'admin
//     che ha agito, non del cancellato, e cancellarli significherebbe cancellare
//     dati di un'altra persona proprio sulla riga che documenta cosa ha fatto.
//
// Ciò che sopravvive non è più un dato personale del cancellato: dice che a un
// certo istante un certo attore ha compiuto una certa azione, e la persona su
// cui l'ha compiuta non è più ricostruibile da nessuna colonna.
//
// **Oggi nessun codice Go scrive in questa tabella** — l'audit log è di
// un'altra issue. Le due istruzioni non toccano quindi una riga, ed è
// deliberato che esistano lo stesso: chi comincerà a scriverci troverà la regola
// già applicata invece di doverla ricordare.
func (s *Store) purgeAudit(ctx context.Context, tx pgx.Tx, userID string, purged *account.Purged) error {
	tag, err := tx.Exec(ctx,
		`DELETE FROM audit_log
		  WHERE actor_user_id = $1::uuid
		    AND impersonated_user_id IS NULL
		    AND (target_user_id IS NULL OR target_user_id = $1::uuid)`, userID)
	if err != nil {
		return annotate("cancellazione delle righe di audit dell'account", err)
	}
	purged.AuditDeleted = tag.RowsAffected()

	if _, err := tx.Exec(ctx,
		`UPDATE audit_log SET metadata = '{}'::jsonb
		  WHERE (impersonated_user_id = $1::uuid OR target_user_id = $1::uuid)
		    AND metadata <> '{}'::jsonb`, userID); err != nil {
		return annotate("anonimizzazione delle righe di audit altrui", err)
	}

	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM audit_log
		  WHERE actor_user_id = $1::uuid
		     OR impersonated_user_id = $1::uuid
		     OR target_user_id = $1::uuid`, userID).Scan(&purged.AuditKept); err != nil {
		return annotate("conteggio delle righe di audit conservate", err)
	}
	return nil
}

// purgePaddleEvents rimuove il registro di idempotenza degli eventi di questo
// utente.
//
// `paddle_webhook_events` non ha `user_id`: la strada è
// `paddle_subscription_id`, che la 0003 tiene unico su `subscriptions` — quindi
// una sottoscrizione appartiene a un utente solo e la sottoquery non può
// prendere righe di altri.
//
// # Perché non basta lasciare fare alla retention della 0013
//
// Perché quella cancella per **data** (novanta giorni), non per proprietario:
// fino a tre mesi dopo la purga resterebbero righe con l'identificativo di
// sottoscrizione di un account che dichiariamo rimosso. È un identificativo
// assegnato da Paddle a una persona, cioè un dato personale pseudonimo, e la
// privacy policy §5 non ne prevede la sopravvivenza.
//
// # Il residuo che resta, e perché è un altro discorso
//
// Un evento che arriva **dopo** la purga reinserisce quell'identificativo, e ci
// resta finché la retention della 0013 non lo porta via. Non è lo stesso dato:
// è ciò che Paddle ci racconta di un contratto che dalla loro parte è ancora
// vivo, ed è il motivo per cui quel contratto va annullato presso Paddle —
// vedi [account.Service.RequestDeletion].
func (s *Store) purgePaddleEvents(ctx context.Context, tx pgx.Tx, userID string, purged *account.Purged) error {
	tag, err := tx.Exec(ctx,
		`DELETE FROM paddle_webhook_events
		  WHERE paddle_subscription_id IN (
		        SELECT paddle_subscription_id
		          FROM subscriptions
		         WHERE user_id = $1::uuid
		           AND paddle_subscription_id IS NOT NULL
		  )`, userID)
	if err != nil {
		return annotate("cancellazione degli eventi Paddle dell'account", err)
	}
	purged.PaddleEvents = tag.RowsAffected()
	return nil
}

// purgeGitHubDeliveries rimuove le consegne del webhook che riguardano i
// repository di questo utente.
//
// `github_webhook_deliveries` non ha `user_id`, e questa è **l'istruzione più
// pericolosa del file**: le consegne si identificano per repository, e la 0004
// dichiara esplicitamente che «utenti diversi possono collegare lo stesso
// repository pubblico». Una condizione sul solo `repository_external_id`
// porterebbe via le consegne di un altro utente sullo stesso repository, che è
// esattamente il modo in cui una cancellazione a cascata scritta male fa danni
// invisibili — nessuno se ne accorge finché quell'altro utente non chiede perché
// un sync non è mai partito.
//
// La condizione è quindi in due parti:
//
//   - la consegna corrisponde a un repository **di questo utente**, per la
//     coppia (installazione, identificativo del repository). `IS NOT DISTINCT
//     FROM` e non `=` perché entrambe le colonne ammettono NULL, e `NULL = NULL`
//     è NULL, cioè «non corrisponde» proprio dove corrisponde;
//   - **e nessun altro utente segue lo stesso repository.** Se lo segue,
//     l'installazione può essere condivisa (una GitHub App installata su
//     un'organizzazione a cui appartengono due nostri clienti) e la consegna non
//     è solo sua: si lascia stare, e la porta via la retention della 0011 ai
//     trenta giorni.
//
// La direzione dell'errore è deliberata: nel dubbio si conserva una riga che
// contiene il nome di un repository per altri trenta giorni, invece di
// cancellare il registro di deduplicazione di un utente che non c'entra.
func (s *Store) purgeGitHubDeliveries(ctx context.Context, tx pgx.Tx, userID string, purged *account.Purged) error {
	tag, err := tx.Exec(ctx,
		`DELETE FROM github_webhook_deliveries d
		  WHERE EXISTS (
		        SELECT 1 FROM repositories r
		         WHERE r.user_id = $1::uuid
		           AND r.external_id IS NOT DISTINCT FROM d.repository_external_id
		           AND r.installation_id IS NOT DISTINCT FROM d.installation_id
		  )
		    AND NOT EXISTS (
		        SELECT 1 FROM repositories o
		         WHERE o.user_id <> $1::uuid
		           AND o.external_id IS NOT DISTINCT FROM d.repository_external_id
		  )`, userID)
	if err != nil {
		return annotate("cancellazione delle consegne GitHub dell'account", err)
	}
	purged.GitHubDeliveries = tag.RowsAffected()
	return nil
}

// ------------------------------------------------------------------ supporto

// applyLockTimeout limita l'attesa sui lock alla transazione corrente.
//
// `SET LOCAL` e non `SET`: la connessione torna al pool con i suoi valori
// originali, e un'altra parte del processo che la riceve dopo non eredita in
// silenzio un `lock_timeout` che non ha chiesto. Il valore è interpolato perché
// `SET` non accetta parametri — è un comando di utilità, non una query — e ciò
// che entra è un intero calcolato qui, non una stringa che arrivi da fuori.
func (s *Store) applyLockTimeout(ctx context.Context, tx pgx.Tx) error {
	millis := s.lockTimeout.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = %d", millis)); err != nil {
		return annotate("impostazione di lock_timeout", err)
	}
	return nil
}

// isLockTimeout riconosce la rinuncia a un lock (SQLSTATE 55P03), che è il modo
// in cui `lock_timeout` si manifesta. Va distinta da un errore vero perché è
// **l'esito voluto**: qualcuno stava scrivendo e la purga gli ha lasciato la
// precedenza.
func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}

// sleep aspetta rispettando il contesto.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
