// Package billingpg è l'implementazione PostgreSQL di billing.Store.
//
// Sta in un package a parte per la stessa ragione di internal/jobspg:
// internal/billing non deve dipendere da pgx, ed è ciò che permette di provare
// la traduzione da prezzo a piano senza un database in piedi.
//
// Qui non c'è però soltanto SQL di appoggio: **la regola di R58 è una
// istruzione di questo file**, e ci sta di proposito. Contare i job attivi in Go
// e poi sospenderli è una corsa — fra il conteggio e l'aggiornamento una
// creazione concorrente cambia il numero su cui la decisione è stata presa — ed
// è la stessa ragione per cui il tetto di piano vive dentro l'INSERT di jobspg.
package billingpg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apdsoftware/postqron/services/api/internal/billing"
	"github.com/apdsoftware/postqron/services/api/internal/jobspg"
	"github.com/apdsoftware/postqron/services/api/internal/paddle"
)

// Store implementa [billing.Store] su un pool pgx.
type Store struct {
	pool *pgxpool.Pool
}

// New costruisce lo store.
func New(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("billingpg: il pool è obbligatorio")
	}
	return &Store{pool: pool}, nil
}

var _ billing.Store = (*Store)(nil)

// NewService compone il servizio degli entitlement sul pool dato.
//
// Il catalogo, il token del checkout e l'ambiente vengono tutti dall'ambiente
// (CREDENTIALS §1). Un catalogo vuoto **non** impedisce di costruire il
// servizio: gli eventi Paddle vanno comunque applicati o rifiutati
// rumorosamente, ed è il checkout a non essere disponibile — vedi
// [billing.Service.CanSell].
//
// `notifier` comunica all'utente le variazioni di piano (R21). Può essere nil:
// su una macchina senza email configurate il piano si applica lo stesso.
func NewService(
	pool *pgxpool.Pool,
	getenv func(string) string,
	logger *slog.Logger,
	notifier billing.Notifier,
) (*billing.Service, error) {
	catalog, err := paddle.CatalogFromEnv(getenv)
	if err != nil {
		return nil, err
	}
	store, err := New(pool)
	if err != nil {
		return nil, err
	}
	return billing.NewService(billing.Options{
		Store:       store,
		Catalog:     catalog,
		ClientToken: getenv("PADDLE_CLIENT_TOKEN"),
		Environment: getenv(paddle.EnvironmentEnvVar),
		Notifier:    notifier,
		Logger:      logger,
	})
}

// ------------------------------------------------------------ sottoscrizioni

// SaveSubscription scrive la sottoscrizione rispettando la filigrana.
//
// # Cosa significa «il piano dell'utente»
//
// C'è una sola colonna che risponde: `subscriptions.plan_code` della riga non
// annullata, che è ciò che legge [jobspg.Store.PlanForUser]. Questo metodo è
// l'unico posto in cui quella colonna viene scritta a partire da Paddle, ed è
// quindi **l'unico posto in cui si decide se uno stato dà diritto al piano**.
// La decisione arriva già presa da internal/billing, che porta `PlanCode: free`
// quando lo stato non entitla: chi legge non deve avere un'opinione sugli stati,
// o le opinioni diventerebbero due e divergerebbero.
//
// Su uno stato non pagante il piano diventa `free` ma **`paddle_price_id` non
// viene azzerato**: resta la traccia di che cosa l'utente stava pagando, che è
// ciò che serve all'assistenza e alle statistiche di ricavo (R17).
//
// # L'ordine dei passi
//
//  1. si risolve l'utente, perché senza non c'è niente da scrivere;
//  2. si prende il lock per utente — lo stesso della creazione dei job, vedi
//     [jobspg.UserJobsLockKey] — così che il conteggio su cui R58 deciderà fra un
//     istante non cambi sotto;
//  3. si confronta la filigrana **dentro la transazione**: letta prima e
//     confrontata in Go, due consegne concorrenti passerebbero entrambe;
//  4. si annullano le altre sottoscrizioni vive dell'utente, perché l'indice
//     parziale `subscriptions_one_live_per_user_idx` ne ammette una sola e
//     l'INSERT successivo la troverebbe occupata;
//  5. si scrive la riga.
func (s *Store) SaveSubscription(ctx context.Context, change billing.SubscriptionChange) (billing.SaveResult, error) {
	if change.PaddleSubscriptionID == "" {
		return billing.SaveResult{}, errors.New("billingpg: sottoscrizione senza identificativo Paddle")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return billing.SaveResult{}, fmt.Errorf("billingpg: apertura della transazione: %w", err)
	}
	// Il rollback dopo un commit riuscito è un'operazione senza effetto: serve
	// solo a chiudere i percorsi d'uscita anticipata.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	userID, err := resolveUser(ctx, tx, change)
	if err != nil {
		return billing.SaveResult{}, err
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, jobspg.UserJobsLockKey(userID)); err != nil {
		return billing.SaveResult{}, fmt.Errorf("billingpg: lock sull'utente: %w", err)
	}

	// La filigrana si legge con `FOR UPDATE`: la riga resta bloccata fino al
	// commit, quindi fra il confronto e la scrittura nessun'altra consegna può
	// infilarsi. È il punto in cui l'ordinamento diventa vero.
	var watermark *time.Time
	err = tx.QueryRow(ctx,
		`SELECT paddle_event_occurred_at FROM subscriptions
		  WHERE paddle_subscription_id = $1 FOR UPDATE`,
		change.PaddleSubscriptionID).Scan(&watermark)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Prima volta che vediamo questa sottoscrizione: non c'è un ordine da
		// rispettare, perché non c'è niente che la preceda.
	case err != nil:
		return billing.SaveResult{}, fmt.Errorf("billingpg: lettura della filigrana: %w", err)
	case watermark != nil && change.OccurredAt.Before(*watermark):
		// Evento fuori ordine. Non è un errore e non va rilavorato: è una consegna
		// che si comporta correttamente e arriva tardi.
		return billing.SaveResult{Applied: false, UserID: userID}, nil
	}

	// Il piano in forza **prima** di questa scrittura. Serve alla notifica di
	// R21, che deve dire all'utente da dove a dove si è spostato, e va letto
	// adesso: fra un'istruzione e l'altra la riga viene annullata e riscritta, e
	// dopo non c'è più niente da cui ricavarlo. La lettura è dentro la
	// transazione e sotto il lock per utente già preso, quindi non può
	// raccontare un piano diverso da quello che l'UPDATE sta per sostituire.
	//
	// L'indice `subscriptions_one_live_per_user_idx` (0003) garantisce che di
	// righe non annullate ce ne sia al più una: nessun `ORDER BY` da scegliere,
	// e nessun ordine da cui il risultato possa dipendere.
	var previousPlan string
	err = tx.QueryRow(ctx,
		`SELECT plan_code FROM subscriptions
		  WHERE user_id = $1::uuid AND status <> 'canceled'`,
		userID).Scan(&previousPlan)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Nessuna sottoscrizione viva: l'utente era sul piano d'ingresso, che
		// non ha una riga finché non compra (R59 — il Free *è* il piano
		// gratuito, non una prova).
		previousPlan = paddle.PlanFree
	case err != nil:
		return billing.SaveResult{}, fmt.Errorf("billingpg: lettura del piano precedente: %w", err)
	}

	// `IS DISTINCT FROM` e non `<>`: le sottoscrizioni del piano Free hanno
	// `paddle_subscription_id` a NULL, e `NULL <> 'sub_01'` è NULL, cioè non
	// annullerebbe proprio la riga che dev'essere annullata.
	if _, err := tx.Exec(ctx,
		`UPDATE subscriptions
		    SET status = 'canceled',
		        canceled_at = coalesce(canceled_at, $2)
		  WHERE user_id = $1::uuid
		    AND status <> 'canceled'
		    AND paddle_subscription_id IS DISTINCT FROM $3`,
		userID, change.OccurredAt, change.PaddleSubscriptionID); err != nil {
		return billing.SaveResult{}, fmt.Errorf("billingpg: chiusura delle sottoscrizioni precedenti: %w", err)
	}

	// `subscriptions_canceled_check` esige un istante di annullamento quando lo
	// stato è `canceled`. Paddle lo manda, ma non su ogni variante del payload:
	// l'istante dell'evento è il ripiego corretto, perché è quando il fatto è
	// avvenuto secondo chi lo dichiara.
	canceledAt := change.CanceledAt
	if change.Status == string(paddle.SubscriptionCanceled) && canceledAt == nil {
		canceledAt = &change.OccurredAt
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO subscriptions
		     (user_id, plan_code, status, billing_period,
		      paddle_subscription_id, paddle_customer_id, paddle_price_id,
		      current_period_start, current_period_end, cancel_at, canceled_at,
		      paddle_event_occurred_at)
		 VALUES ($1::uuid, $2, $3::subscription_status, $4::text::billing_period,
		         $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (paddle_subscription_id) WHERE paddle_subscription_id IS NOT NULL
		 DO UPDATE SET
		     plan_code = excluded.plan_code,
		     status = excluded.status,
		     billing_period = excluded.billing_period,
		     paddle_customer_id = coalesce(excluded.paddle_customer_id, subscriptions.paddle_customer_id),
		     -- Non si azzera: è la traccia di cosa l'utente stava pagando.
		     paddle_price_id = coalesce(excluded.paddle_price_id, subscriptions.paddle_price_id),
		     current_period_start = coalesce(excluded.current_period_start, subscriptions.current_period_start),
		     current_period_end = coalesce(excluded.current_period_end, subscriptions.current_period_end),
		     cancel_at = excluded.cancel_at,
		     canceled_at = coalesce(excluded.canceled_at, subscriptions.canceled_at),
		     paddle_event_occurred_at = excluded.paddle_event_occurred_at`,
		userID, change.PlanCode, change.Status, nullText(string(change.Period)),
		change.PaddleSubscriptionID, nullText(change.PaddleCustomerID), nullText(change.PaddlePriceID),
		nullTime(change.CurrentPeriodStart), nullTime(change.CurrentPeriodEnd),
		change.CancelAt, canceledAt,
		change.OccurredAt)
	if err != nil {
		return billing.SaveResult{}, fmt.Errorf("billingpg: scrittura della sottoscrizione: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return billing.SaveResult{}, fmt.Errorf("billingpg: commit della sottoscrizione: %w", err)
	}
	return billing.SaveResult{
		Applied:          true,
		UserID:           userID,
		PlanCode:         change.PlanCode,
		PreviousPlanCode: previousPlan,
	}, nil
}

// resolveUser trova l'account a cui attribuire la sottoscrizione.
//
// La prima strada è `custom_data.user_id`, che ci mettiamo noi al checkout. La
// seconda è la riga già legata a quell'identificativo Paddle, e serve a tutti
// gli eventi successivi al primo — e a una sottoscrizione creata a mano dal
// pannello di Paddle su un cliente che avevamo già. Senza nessuna delle due non
// si indovina: vedi [billing.ErrUnknownSubscriber].
func resolveUser(ctx context.Context, tx pgx.Tx, change billing.SubscriptionChange) (string, error) {
	// La forma dell'identificativo si controlla **prima** della query, e non
	// lasciando fallire il cast: dentro una transazione un errore di PostgreSQL
	// la aborta, e ogni istruzione successiva risponde `25P02` invece di dire cosa
	// è andato storto. `custom_data.user_id` ce lo mettiamo noi al checkout, ma
	// fa un giro fuori e dentro: un valore manomesso deve semplicemente non
	// corrispondere a nessun account.
	if looksLikeUUID(change.UserID) {
		var id string
		err := tx.QueryRow(ctx,
			`SELECT id::text FROM users WHERE id = $1::uuid AND deleted_at IS NULL`,
			change.UserID).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("billingpg: lettura dell'utente: %w", err)
		}
		// L'identificativo non corrisponde a nessun account vivo: si prova comunque
		// dalla sottoscrizione, perché un account cancellato con una sottoscrizione
		// ancora attiva è un caso da registrare, non da far fallire in modo
		// indistinguibile da un guasto.
	}

	var id string
	err := tx.QueryRow(ctx,
		`SELECT user_id::text FROM subscriptions WHERE paddle_subscription_id = $1`,
		change.PaddleSubscriptionID).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "", fmt.Errorf("%w: sottoscrizione %s", billing.ErrUnknownSubscriber, change.PaddleSubscriptionID)
	case err != nil:
		return "", fmt.Errorf("billingpg: risoluzione dell'utente: %w", err)
	}
	return id, nil
}

// ----------------------------------------------------------------------- R58

// EnforcePlanLimits sospende i job che il piano di destinazione non regge più.
//
// # Perché una sola istruzione
//
// La decisione dipende da un conteggio, e il conteggio cambia. Fatto in Go
// sarebbe: conta, decidi, aggiorna — con due finestre in cui una creazione
// concorrente rende falsa la premessa. Qui il conteggio è una CTE della stessa
// istruzione che aggiorna, quindi vede lo stesso snapshot; il lock per utente
// preso da [Store.SaveSubscription] chiude anche la finestra verso le creazioni,
// perché è lo stesso lock che jobspg prende prima di inserire.
//
// # Le due regole, e perché non sono la stessa
//
//	`sfora`      i job attivi **superano** il tetto del piano. Allora si
//	             sospendono **tutti**, compresi quelli che ci starebbero: la
//	             scelta di quali salvare è dell'utente, e R58 spiega per esteso
//	             perché non può essere nostra. `>` e non `>=`: chi sta esattamente
//	             sul tetto non lo supera.
//
//	risoluzione  il job è schedulato più fitto di quanto il piano consenta.
//	             Allora si sospende **quel job soltanto**, e non perché sia meno
//	             grave: perché non c'è nessuna scelta da offrire. Riaccenderne un
//	             altro non libera posto, e l'unico rimedio è cambiare la
//	             schedulazione. Vale **anche quando il numero non sfora**, ed è la
//	             lettura discussa nella documentazione di internal/billing: i
//	             Termini §4 dichiarano che la risoluzione minima è un tetto vero,
//	             e un `every: 1s` che continua a girare su un piano fermo al
//	             minuto renderebbe quella frase falsa.
//
// Il `CASE` sul motivo dà la precedenza alla risoluzione: un job che viola
// entrambe le regole va etichettato con quella che l'utente deve risolvere per
// prima, perché riaccenderlo dopo aver liberato posto fallirebbe comunque.
//
// # Cosa questa istruzione non fa
//
// **Non cancella niente**, ed è la promessa di R58 e dei Termini §4.1. Non tocca
// i job archiviati, che sono già fuori dal `cron.yaml` dell'utente, né quelli già
// spenti — un job in pausa non è attivo, non occupa un posto fra gli attivi e non
// c'è ragione di marcarlo come sospeso da noi.
//
// `next_run_at` viene azzerata perché un job spento non ha una prossima
// occorrenza: lasciarcela terrebbe una riga di `jobs_due_idx` che promette
// un'esecuzione che non avverrà.
//
// È **idempotente**: applicata due volte di seguito, la seconda non trova più
// job attivi da sospendere e non tocca niente. Serve, perché una consegna fallita
// e ripetuta la riesegue.
func (s *Store) EnforcePlanLimits(ctx context.Context, userID, planCode string, at time.Time) (billing.Suspension, error) {
	if at.IsZero() {
		at = time.Now()
	}

	rows, err := s.pool.Query(ctx,
		`WITH piano AS (
		     SELECT max_jobs, min_interval_seconds FROM plans WHERE code = $2
		 ),
		 attivi AS (
		     SELECT count(*) AS n FROM jobs
		      WHERE user_id = $1::uuid AND enabled AND archived_at IS NULL
		 ),
		 sospesi AS (
		     UPDATE jobs j
		        SET enabled = false,
		            suspended_at = $3,
		            suspended_reason = CASE
		                WHEN j.every_seconds IS NOT NULL
		                     AND j.every_seconds < p.min_interval_seconds
		                THEN 'plan_resolution'::job_suspension_reason
		                ELSE 'plan_job_limit'::job_suspension_reason
		            END,
		            next_run_at = NULL
		       FROM piano p, attivi a
		      WHERE j.user_id = $1::uuid
		        AND j.enabled
		        AND j.archived_at IS NULL
		        AND (
		              (p.max_jobs IS NOT NULL AND a.n > p.max_jobs)
		              OR (j.every_seconds IS NOT NULL AND j.every_seconds < p.min_interval_seconds)
		            )
		     RETURNING j.suspended_reason
		 )
		 SELECT suspended_reason::text, count(*) FROM sospesi GROUP BY 1`,
		userID, planCode, at)
	if err != nil {
		return billing.Suspension{}, fmt.Errorf("billingpg: sospensione dei job: %w", err)
	}
	defer rows.Close()

	var suspension billing.Suspension
	for rows.Next() {
		var reason string
		var count int
		if err := rows.Scan(&reason, &count); err != nil {
			return billing.Suspension{}, fmt.Errorf("billingpg: lettura delle sospensioni: %w", err)
		}
		switch billing.SuspensionReason(reason) {
		case billing.ReasonJobLimit:
			suspension.ByJobLimit = count
		case billing.ReasonResolution:
			suspension.ByResolution = count
		}
	}
	if err := rows.Err(); err != nil {
		return billing.Suspension{}, fmt.Errorf("billingpg: sospensione dei job: %w", err)
	}
	return suspension, nil
}

// ------------------------------------------------------------------ letture

// Plan legge una riga di listino.
func (s *Store) Plan(ctx context.Context, code string) (billing.PlanSummary, error) {
	var plan billing.PlanSummary
	err := s.pool.QueryRow(ctx,
		`SELECT code, name, is_public FROM plans WHERE code = $1`, code).
		Scan(&plan.Code, &plan.Name, &plan.IsPublic)
	if errors.Is(err, pgx.ErrNoRows) {
		return billing.PlanSummary{}, fmt.Errorf("billingpg: piano %q inesistente", code)
	}
	if err != nil {
		return billing.PlanSummary{}, fmt.Errorf("billingpg: lettura del piano: %w", err)
	}
	return plan, nil
}

// RecordCheckoutIntent registra la dichiarazione di uso professionale (R63).
func (s *Store) RecordCheckoutIntent(ctx context.Context, intent billing.CheckoutIntent) error {
	createdAt := intent.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO paddle_checkout_intents
		     (user_id, plan_code, billing_period, paddle_price_id, business_use, vat_number, created_at)
		 VALUES ($1::uuid, $2, $3::billing_period, $4, $5, $6, $7)`,
		intent.UserID, intent.PlanCode, string(intent.Period), intent.PriceID,
		intent.BusinessUse, nullText(intent.VATNumber), createdAt)
	if err != nil {
		return fmt.Errorf("billingpg: registrazione dell'intento di checkout: %w", err)
	}
	return nil
}

// Entitlement legge il piano in forza e i job che un cambio di piano ha spento.
//
// I conteggi dei sospesi stanno nella stessa lettura del piano, e non in una
// rotta a sé, perché è insieme che servono: R58 dice che l'interfaccia deve
// *dire* cosa è successo, e uno stato del piano che non nomina i job fermi è
// esattamente il modo in cui l'utente scopre il problema quando il job che gli
// serviva non è partito.
//
// La sottoscrizione viva è al massimo una — lo garantisce
// `subscriptions_one_live_per_user_idx` — e chi non ne ha ricade su `free`, come
// in [jobspg.Store.PlanForUser]. Le due letture devono restare d'accordo: quella
// decide cosa l'utente *può fare*, questa cosa gli viene *mostrato*, e una
// differenza fra le due è un utente a cui l'API rifiuta ciò che la dashboard gli
// dice di poter fare.
func (s *Store) Entitlement(ctx context.Context, userID string) (billing.Entitlement, error) {
	var (
		ent          billing.Entitlement
		period       *string
		minInterval  int
		retentionDay int
	)
	err := s.pool.QueryRow(ctx,
		`WITH viva AS (
		     SELECT plan_code, status, billing_period, current_period_end, cancel_at
		       FROM subscriptions
		      WHERE user_id = $1::uuid AND status <> 'canceled'
		      LIMIT 1
		 )
		 SELECT p.code, p.name,
		        coalesce((SELECT status::text FROM viva), 'active'),
		        (SELECT billing_period::text FROM viva),
		        (SELECT current_period_end FROM viva),
		        (SELECT cancel_at FROM viva),
		        p.max_jobs, p.min_interval_seconds, p.log_retention_days,
		        (SELECT count(*) FROM jobs
		          WHERE user_id = $1::uuid AND enabled AND archived_at IS NULL),
		        (SELECT count(*) FROM jobs
		          WHERE user_id = $1::uuid AND archived_at IS NULL
		            AND suspended_reason = 'plan_job_limit'),
		        (SELECT count(*) FROM jobs
		          WHERE user_id = $1::uuid AND archived_at IS NULL
		            AND suspended_reason = 'plan_resolution')
		   FROM plans p
		  WHERE p.code = coalesce((SELECT plan_code FROM viva), 'free')`,
		userID).Scan(
		&ent.PlanCode, &ent.PlanName, &ent.Status, &period,
		&ent.CurrentPeriodEnd, &ent.CancelAt, &ent.MaxJobs,
		&minInterval, &retentionDay,
		&ent.ActiveJobs, &ent.Suspended.ByJobLimit, &ent.Suspended.ByResolution)
	if errors.Is(err, pgx.ErrNoRows) {
		return billing.Entitlement{}, errors.New("billingpg: nessun piano trovato: la migrazione 0003 non è stata applicata")
	}
	if err != nil {
		return billing.Entitlement{}, fmt.Errorf("billingpg: lettura dell'entitlement: %w", err)
	}
	if period != nil {
		ent.Period = paddle.Period(*period)
	}
	// Le stesse due colonne, e la stessa conversione, di [jobspg.Store.PlanForUser]:
	// lì diventano i limiti che *decidono*, qui i numeri che l'interfaccia *dice*
	// (R15). Leggerle nella stessa riga del piano è ciò che impedisce alle due
	// letture di raccontare tetti diversi.
	ent.MinInterval = time.Duration(minInterval) * time.Second
	ent.LogRetention = time.Duration(retentionDay) * 24 * time.Hour
	return ent, nil
}

// ------------------------------------------------------------------ supporto

// Le colonne facoltative hanno vincoli che la stringa vuota viola
// (`paddle_customer_id <> ”`): il valore assente si scrive NULL.
func nullText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func nullTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

// looksLikeUUID riconosce la forma canonica `8-4-4-4-12` in esadecimale.
//
// Non valida la versione né la variante: serve soltanto a sapere se il valore
// può essere passato a un cast `::uuid` senza far abortire la transazione.
func looksLikeUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') ||
				(r >= 'a' && r <= 'f') ||
				(r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}
