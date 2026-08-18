// Package accountpg è l'implementazione PostgreSQL di account.Store.
//
// Sta in un package a parte per la stessa ragione di internal/authpg e
// internal/secretspg: internal/account non deve dipendere da pgx, ed è ciò che
// permette di provare la conferma, la presa d'atto e il calcolo della scadenza
// senza un database in piedi.
//
// La differenza con gli altri `*pg` è che **qui la logica c'è**, e sta tutta
// nelle istruzioni. Una cancellazione sbagliata non si scopre leggendo il codice
// che la ordina: si scopre leggendo le istruzioni che la eseguono, ed è il
// motivo per cui ognuna porta scritto accanto perché è ancorata dove è ancorata.
//
// # La regola che vale per ogni istruzione di questo file
//
// **Ogni riga che si tocca dev'essere di questo utente, e la condizione deve
// dimostrarlo.** Le tabelle con un `user_id` lo dimostrano da sole; le due che
// non ce l'hanno — `github_webhook_deliveries` e `paddle_webhook_events` — hanno
// una condizione scritta apposta, e il commento accanto dice cosa succederebbe
// senza. Non c'è un annulla.
package accountpg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/account"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
)

// I valori predefiniti della cancellazione a lotti delle esecuzioni. Sono quelli
// di internal/retention, e non per simmetria: cancellano righe della stessa
// tabella, sulla stessa macchina, mentre il motore ci sta scrivendo.
const (
	// DefaultBatch è quante esecuzioni si cancellano per lotto.
	DefaultBatch = 5000
	// DefaultPause è la pausa fra due lotti: è ciò che li rende davvero
	// separati, dando ad autovacuum e al flush del WAL il tempo di stare al
	// passo. Senza, i lotti tornano a essere un DELETE unico scritto in modo più
	// complicato.
	DefaultPause = 200 * time.Millisecond
	// DefaultMaxBatches è il tetto di lotti per account e per passata.
	//
	// Duemila lotti sono dieci milioni di righe, cioè più di quante ne possieda
	// quasi chiunque; chi ne ha di più — un job Agency a un secondo su novanta
	// giorni ne fa quindici milioni e mezzo da solo — viene ripreso dalla
	// passata successiva, e il fatto che sia avanzato è scritto in
	// [account.Purged.Truncated] invece di essere taciuto.
	DefaultMaxBatches = 2000
	// DefaultLockTimeout è quanto la purga aspetta un lock prima di rinunciare.
	DefaultLockTimeout = 2 * time.Second
)

// Options configura lo [Store]. Pool è obbligatorio; le altre manopole hanno i
// valori qui sopra.
type Options struct {
	Pool        *pgxpool.Pool
	Batch       int
	Pause       time.Duration
	MaxBatches  int
	LockTimeout time.Duration
}

// Store implementa [account.Store] su un pool pgx.
type Store struct {
	pool        *pgxpool.Pool
	batch       int
	pause       time.Duration
	maxBatches  int
	lockTimeout time.Duration
}

// New costruisce lo store con i valori predefiniti.
func New(pool *pgxpool.Pool) (*Store, error) {
	return NewWithOptions(Options{Pool: pool})
}

// NewWithOptions costruisce lo store. Serve ai test, che devono poter abbassare
// il lotto per provare che i lotti esistono davvero.
func NewWithOptions(opts Options) (*Store, error) {
	if opts.Pool == nil {
		return nil, errors.New("accountpg: il pool è obbligatorio")
	}
	if opts.Batch < 0 || opts.Pause < 0 || opts.MaxBatches < 0 || opts.LockTimeout < 0 {
		return nil, errors.New("accountpg: nessuna delle manopole ammette valori negativi")
	}
	s := &Store{
		pool:        opts.Pool,
		batch:       opts.Batch,
		pause:       opts.Pause,
		maxBatches:  opts.MaxBatches,
		lockTimeout: opts.LockTimeout,
	}
	if s.batch == 0 {
		s.batch = DefaultBatch
	}
	if opts.Pause == 0 {
		s.pause = DefaultPause
	}
	if s.maxBatches == 0 {
		s.maxBatches = DefaultMaxBatches
	}
	if s.lockTimeout == 0 {
		s.lockTimeout = DefaultLockTimeout
	}
	return s, nil
}

var _ account.Store = (*Store)(nil)

// ------------------------------------------------------------------- lettura

// statusSQL legge la finestra di ripensamento e la sottoscrizione viva.
//
// # Che cosa conta come «a pagamento»
//
// Una sottoscrizione non annullata **con un identificativo Paddle**. Le due
// condizioni insieme, e non il codice del piano, perché la domanda a cui questa
// query risponde non è «che piano ha» ma «c'è qualcosa che Paddle continuerà a
// fatturargli dopo che questo account non esisterà più». La 0003 lo dice
// esplicitamente: `paddle_subscription_id` è «NULL sul piano Free, che non passa
// da Paddle».
//
// `status <> 'canceled'` è la definizione di «viva» che il database stesso dà,
// nell'indice parziale `subscriptions_one_live_per_user_idx` — che garantisce
// anche che di righe vive ce ne sia al più una, quindi la LEFT JOIN non
// moltiplica e non c'è nessun ORDER BY da scegliere. Riscrivere qui la
// definizione come elenco di stati la farebbe divergere al primo stato aggiunto
// all'enum.
const statusSQL = `
	SELECT u.deletion_requested_at,
	       u.purge_after,
	       coalesce(s.plan_code, ''),
	       coalesce(s.paddle_subscription_id, '')
	  FROM users u
	  LEFT JOIN subscriptions s
	         ON s.user_id = u.id
	        AND s.status <> 'canceled'
	        AND s.paddle_subscription_id IS NOT NULL
	 WHERE u.id = $1::uuid`

// Status legge lo stato della cancellazione e la sottoscrizione viva.
func (s *Store) Status(ctx context.Context, userID string) (account.Status, error) {
	if !looksLikeUUID(userID) {
		// Il cast fallirebbe con `22P02`, che è indistinguibile da un guasto per
		// chi legge il messaggio. Un identificativo malformato non è un account.
		return account.Status{}, account.ErrNotFound
	}

	var (
		requestedAt, purgeAfter *time.Time
		planCode, paddleID      string
	)
	err := s.pool.QueryRow(ctx, statusSQL, userID).Scan(&requestedAt, &purgeAfter, &planCode, &paddleID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return account.Status{}, account.ErrNotFound
	case err != nil:
		return account.Status{}, annotate("lettura dello stato dell'account", err)
	}

	status := account.Status{
		Requested: requestedAt != nil,
		Subscription: account.Subscription{
			Paid:                 paddleID != "",
			PlanCode:             planCode,
			PaddleSubscriptionID: paddleID,
		},
	}
	if requestedAt != nil {
		status.RequestedAt = *requestedAt
	}
	if purgeAfter != nil {
		status.PurgeAfter = *purgeAfter
	}
	return status, nil
}

// ------------------------------------------------------------- la richiesta

// RequestDeletion apre la finestra e ferma tutto, in una transazione sola.
//
// # Perché una transazione sola
//
// La privacy policy §5 dice «we stop execution and revoke keys **immediately**».
// Spezzata in due chiamate, ci sarebbe un istante — piccolo quanto si vuole, ma
// esistente — in cui l'account è dichiarato in cancellazione e il motore lo sta
// ancora eseguendo. È lo stesso istante in cui il motore chiama il bersaglio di
// un cliente per conto di un account che ha appena chiesto di sparire.
//
// # Il lock consultivo
//
// È lo stesso di [jobspg.UserJobsLockKey], che internal/jobspg prende prima di
// inserire un job e internal/billingpg prima di sospenderne. Senza condividerlo,
// una creazione in volo inserirebbe un job **acceso** subito dopo che tutti gli
// altri sono stati spenti: un account in cancellazione con un job che continua a
// girare, e nessuno a saperlo.
//
// # Che cosa si ferma, e che cosa si svuota
//
//	jobs                spenti e senza prossima occorrenza. `suspended_reason`
//	                    dice **chi** li ha fermati, ed è ciò che permette
//	                    all'annullamento di riaccendere solo questi.
//	api_keys            revocate. L'impronta resta: non è la chiave.
//	workspace_secrets   revocati **e svuotati**. Il vincolo della 0012 non ammette
//	ai_credentials      che le due cose si separino, ed è il punto: qui il testo
//	                    cifrato *è* il segreto, e conservarlo dopo la revoca
//	                    significherebbe che «revocato» dipende dal fatto che
//	                    nessuno lo decifri più. Vale anche per le chiavi AI dalla
//	                    0016.
//	sessions            revocate: chi è collegato altrove viene disconnesso. Non
//	                    impedisce di rientrare — l'account esiste ancora, e la
//	                    privacy policy promette che si può cambiare idea.
//	user_tokens         consumati senza usarli: un link di reimpostazione partito
//	                    ieri non deve restare valido.
//
// **I repository non si toccano.** Fermare il sync scrivendo `enabled = false`
// avrebbe richiesto di ricordare quali erano accesi per rimetterli come stavano
// all'annullamento; il sync si ferma invece perché internal/reposyncpg non
// riconosce più i repository di un account in cancellazione. Un meccanismo solo,
// nessuno stato da restaurare.
func (s *Store) RequestDeletion(
	ctx context.Context,
	userID string,
	at, purgeAfter time.Time,
) (account.Receipt, error) {
	if !looksLikeUUID(userID) {
		return account.Receipt{}, account.ErrNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return account.Receipt{}, annotate("apertura della transazione di richiesta", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, jobspg.UserJobsLockKey(userID)); err != nil {
		return account.Receipt{}, annotate("lock sull'utente", err)
	}

	// `deletion_requested_at IS NULL` nella WHERE e non in un controllo
	// applicativo: due richieste concorrenti devono produrre un vincitore e un
	// [account.ErrAlreadyRequested], non due finestre con scadenze diverse.
	tag, err := tx.Exec(ctx,
		`UPDATE users
		    SET deletion_requested_at = $2, purge_after = $3
		  WHERE id = $1::uuid AND deletion_requested_at IS NULL`,
		userID, at, purgeAfter)
	if err != nil {
		return account.Receipt{}, annotate("apertura della finestra di cancellazione", err)
	}
	if tag.RowsAffected() == 0 {
		return account.Receipt{}, s.explainMissedUpdate(ctx, tx, userID, account.ErrAlreadyRequested)
	}

	receipt := account.Receipt{RequestedAt: at, PurgeAfter: purgeAfter}

	// Tutto ciò che è acceso si spegne, archiviati compresi: la condizione è
	// `enabled` e basta, ed è simmetrica a quella dell'annullamento. Un job
	// archiviato e acceso oggi non viene dispatchato — `jobs_due_idx` lo esclude
	// — ma «oggi non parte» e «non può partire» sono due cose diverse, e su una
	// cancellazione conta la seconda.
	if receipt.JobsStopped, err = s.exec(ctx, tx, "arresto dei job",
		`UPDATE jobs
		    SET enabled = false,
		        suspended_at = $2,
		        suspended_reason = 'account_deletion',
		        next_run_at = NULL
		  WHERE user_id = $1::uuid AND enabled`, userID, at); err != nil {
		return account.Receipt{}, err
	}

	if receipt.KeysRevoked, err = s.exec(ctx, tx, "revoca delle chiavi API",
		`UPDATE api_keys SET revoked_at = $2
		  WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID, at); err != nil {
		return account.Receipt{}, err
	}

	// La data e lo svuotamento sono nello stesso UPDATE perché
	// `workspace_secrets_revoked_is_empty_check` non ammette che si separino: non
	// esiste un istante in cui la riga è revocata e il testo cifrato è ancora lì,
	// nemmeno dentro questa transazione.
	if receipt.SecretsRevoked, err = s.exec(ctx, tx, "revoca dei segreti del workspace",
		`UPDATE workspace_secrets
		    SET revoked_at = $2, ciphertext = ''::bytea, nonce = ''::bytea
		  WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID, at); err != nil {
		return account.Receipt{}, err
	}

	// Stesso vincolo, dalla 0016: `ai_credentials_revoked_is_empty_check`.
	if receipt.AIKeysRevoked, err = s.exec(ctx, tx, "revoca delle chiavi AI",
		`UPDATE ai_credentials
		    SET revoked_at = $2, ciphertext = ''::bytea, nonce = ''::bytea
		  WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID, at); err != nil {
		return account.Receipt{}, err
	}

	if receipt.SessionsRevoked, err = s.exec(ctx, tx, "chiusura delle sessioni",
		`UPDATE sessions SET revoked_at = $2
		  WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID, at); err != nil {
		return account.Receipt{}, err
	}

	if receipt.TokensRevoked, err = s.exec(ctx, tx, "annullamento dei token pendenti",
		`UPDATE user_tokens SET consumed_at = $2
		  WHERE user_id = $1::uuid AND consumed_at IS NULL`, userID, at); err != nil {
		return account.Receipt{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return account.Receipt{}, annotate("conferma della richiesta di cancellazione", err)
	}
	return receipt, nil
}

// ---------------------------------------------------------- l'annullamento

// CancelDeletion chiude la finestra e riaccende i job che la richiesta aveva
// fermato.
//
// `suspended_reason = 'account_deletion'` è la condizione, e ci sta tutto il
// senso della colonna: riaccendere `WHERE user_id = $1 AND NOT enabled`
// rimetterebbe in moto anche i job che l'utente aveva messo in pausa mesi prima,
// e quelli che un cambio di piano aveva sospeso (R58) — cioè farebbe partire
// chiamate verso bersagli che nessuno voleva far ripartire, per conto di un
// utente che aveva appena chiesto di essere lasciato in pace.
//
// `next_run_at` resta NULL: la prossima occorrenza la calcola lo scheduler, che
// cerca proprio i job accesi senza (indice `jobs_unscheduled_idx`, 0010).
// Scriverla qui significherebbe indovinare un istante che il motore sa calcolare
// meglio.
func (s *Store) CancelDeletion(ctx context.Context, userID string) (account.Restored, error) {
	if !looksLikeUUID(userID) {
		return account.Restored{}, account.ErrNotFound
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return account.Restored{}, annotate("apertura della transazione di annullamento", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, jobspg.UserJobsLockKey(userID)); err != nil {
		return account.Restored{}, annotate("lock sull'utente", err)
	}

	tag, err := tx.Exec(ctx,
		`UPDATE users
		    SET deletion_requested_at = NULL, purge_after = NULL
		  WHERE id = $1::uuid AND deletion_requested_at IS NOT NULL`, userID)
	if err != nil {
		return account.Restored{}, annotate("chiusura della finestra di cancellazione", err)
	}
	if tag.RowsAffected() == 0 {
		return account.Restored{}, s.explainMissedUpdate(ctx, tx, userID, account.ErrNotRequested)
	}

	var restored account.Restored
	if restored.JobsResumed, err = s.exec(ctx, tx, "riaccensione dei job",
		`UPDATE jobs
		    SET enabled = true, suspended_at = NULL, suspended_reason = NULL
		  WHERE user_id = $1::uuid AND suspended_reason = 'account_deletion'`, userID); err != nil {
		return account.Restored{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return account.Restored{}, annotate("conferma dell'annullamento", err)
	}
	return restored, nil
}

// ------------------------------------------------------------------ supporto

// exec esegue un'istruzione della richiesta e ne restituisce le righe toccate.
func (s *Store) exec(ctx context.Context, tx pgx.Tx, what, sql string, args ...any) (int, error) {
	tag, err := tx.Exec(ctx, sql, args...)
	if err != nil {
		return 0, annotate(what, err)
	}
	return int(tag.RowsAffected()), nil
}

// explainMissedUpdate distingue «l'account non c'è» da «era già in quello
// stato»: sono due risposte diverse per il chiamante, e un UPDATE che non tocca
// righe non le distingue da solo.
func (s *Store) explainMissedUpdate(ctx context.Context, tx pgx.Tx, userID string, whenPresent error) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT true FROM users WHERE id = $1::uuid`, userID).Scan(&exists); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return account.ErrNotFound
		}
		return annotate("lettura dell'account", err)
	}
	return whenPresent
}

// looksLikeUUID controlla la forma prima del cast.
//
// Dentro una transazione un errore di PostgreSQL la aborta, e ogni istruzione
// successiva risponde `25P02` invece di dire cosa è andato storto: è la stessa
// ragione per cui internal/billingpg controlla la forma prima di interrogare.
func looksLikeUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

func annotate(what string, err error) error {
	return fmt.Errorf("accountpg: %s: %w", what, err)
}
