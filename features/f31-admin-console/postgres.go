package adminconsole

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	operations "github.com/apdsoftware/postqron/features/f15-operations"
)

const (
	dashboardServiceAPI                 = "api"
	dashboardServiceDatabase            = "database"
	dashboardServiceWorkerQueue         = "worker_queue"
	dashboardServiceSchedulerPublishing = "scheduler_publishing"
	dashboardCheckTimeout               = 2 * time.Second
	dashboardFreshness                  = 30 * time.Second
	dashboardSlowAPIThreshold           = 300 * time.Millisecond
)

type PostgresStore struct {
	database *sql.DB
	clock    func() time.Time
	newID    func() string
}

func NewPostgresStore(
	database *sql.DB,
	clock func() time.Time,
	newID func() string,
) *PostgresStore {
	return &PostgresStore{database: database, clock: clock, newID: newID}
}

func (store *PostgresStore) Session(
	ctx context.Context,
	token string,
) (Session, error) {
	digest := sha256.Sum256([]byte(token))
	var session Session
	err := store.database.QueryRowContext(ctx, `
		SELECT
			s.account_id,
			a.normalized_email,
			(
				a.email_verified_at IS NOT NULL
				OR EXISTS (
				SELECT 1
				FROM auth_provider_identities identity
				WHERE identity.account_id = a.id
				  AND lower(btrim(identity.provider_email)) = a.normalized_email
				)
			),
			s.authenticated_at,
			s.expires_at
		FROM auth_sessions s
		JOIN auth_accounts a ON a.id = s.account_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > $2`,
		hex.EncodeToString(digest[:]),
		store.clock().UTC(),
	).Scan(
		&session.AccountID,
		&session.Email,
		&session.EmailVerified,
		&session.AuthenticatedAt,
		&session.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	csrf := sha256.Sum256([]byte("postqron-admin-csrf\x00" + token))
	session.CSRFToken = hex.EncodeToString(csrf[:])
	return session, nil
}

func (store *PostgresStore) AccountIDByEmail(
	ctx context.Context,
	email string,
) (string, bool, error) {
	var accountID string
	err := store.database.QueryRowContext(ctx, `
		SELECT id
		FROM auth_accounts
		WHERE normalized_email = $1`,
		email,
	).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return accountID, err == nil, err
}

func (store *PostgresStore) Admin(
	ctx context.Context,
	accountID string,
) (AdminRecord, bool, error) {
	var record AdminRecord
	err := store.database.QueryRowContext(ctx, `
		SELECT account_id, email, active
		FROM f31_admin_records
		WHERE account_id = $1`,
		accountID,
	).Scan(&record.AccountID, &record.Email, &record.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminRecord{}, false, nil
	}
	return record, err == nil, err
}

func (store *PostgresStore) ListAdmins(
	ctx context.Context,
) ([]AdminRecord, error) {
	rows, err := store.database.QueryContext(ctx, `
		SELECT account_id, email, active
		FROM f31_admin_records
		ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []AdminRecord
	for rows.Next() {
		var record AdminRecord
		if err := rows.Scan(&record.AccountID, &record.Email, &record.Active); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (store *PostgresStore) SetAdmin(
	ctx context.Context,
	accountID string,
	enabled bool,
) error {
	result, err := store.database.ExecContext(ctx, `
		INSERT INTO f31_admin_records (account_id, email, active, updated_at)
		SELECT id, normalized_email, $2, $3
		FROM auth_accounts
		WHERE id = $1
		ON CONFLICT (account_id) DO UPDATE
		    SET email = EXCLUDED.email,
		        active = EXCLUDED.active,
		        updated_at = EXCLUDED.updated_at`,
		accountID,
		enabled,
		store.clock().UTC(),
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("admin account does not exist")
	}
	return nil
}

// Dashboard reports the current KPI and service-health projection. Every
// service status is derived from a real, timestamped signal collected on
// this call; a dependency that could not be checked is reported unknown
// rather than operational, and a database outage degrades the response
// instead of hiding the other services behind a full request failure.
func (store *PostgresStore) Dashboard(ctx context.Context) (Dashboard, error) {
	now := store.clock().UTC()
	thresholds := operations.DefaultAlertThresholds()

	pingCtx, cancel := context.WithTimeout(ctx, dashboardCheckTimeout)
	pingErr := store.database.PingContext(pingCtx)
	cancel()
	databaseReachable := pingErr == nil
	apiLatency := store.clock().UTC().Sub(now)

	result := Dashboard{
		Services: []ServiceHealth{
			projectedService(dashboardServiceAPI, now, operations.ServiceSignal{
				Present:   true,
				Reachable: true,
				Warning:   !databaseReachable || apiLatency > dashboardSlowAPIThreshold,
				CheckedAt: now,
			}),
			projectedService(dashboardServiceDatabase, now, operations.ServiceSignal{
				Present:   true,
				Reachable: databaseReachable,
				Critical:  !databaseReachable,
				CheckedAt: now,
			}),
		},
		Entitlements: []EntitlementSummary{},
		RecentAudit:  []AuditEvent{},
	}

	if !databaseReachable {
		result.Services = append(result.Services,
			projectedService(dashboardServiceWorkerQueue, now, operations.ServiceSignal{}),
			projectedService(dashboardServiceSchedulerPublishing, now, operations.ServiceSignal{}),
		)
		return result, nil
	}

	workerSignal, err := store.workerQueueSignal(ctx, now, thresholds)
	if err != nil {
		result.Services = append(result.Services, projectedService(dashboardServiceWorkerQueue, now, operations.ServiceSignal{}))
	} else {
		result.Services = append(result.Services, projectedService(dashboardServiceWorkerQueue, now, workerSignal))
	}

	schedulerSignal, err := store.schedulerSignal(ctx, now, thresholds)
	if err != nil {
		result.Services = append(result.Services, projectedService(dashboardServiceSchedulerPublishing, now, operations.ServiceSignal{}))
	} else {
		result.Services = append(result.Services, projectedService(dashboardServiceSchedulerPublishing, now, schedulerSignal))
	}

	rows, err := store.database.QueryContext(ctx, `
		SELECT
			billing.workspace_id::text,
			billing.plan_code,
			COALESCE(internal.active, false)
		FROM f10_workspace_billing billing
		LEFT JOIN f10_internal_entitlement_overrides internal
		  ON internal.workspace_id = billing.workspace_id
		ORDER BY billing.updated_at DESC
		LIMIT 100`)
	if err != nil {
		return Dashboard{}, err
	}
	for rows.Next() {
		var item EntitlementSummary
		if err := rows.Scan(&item.WorkspaceID, &item.PlanCode, &item.Internal); err != nil {
			rows.Close()
			return Dashboard{}, err
		}
		result.Entitlements = append(result.Entitlements, item)
	}
	if err := rows.Close(); err != nil {
		return Dashboard{}, err
	}

	auditRows, err := store.database.QueryContext(ctx, `
		SELECT
			id, code, actor_id, subject_id, reason, outcome,
			correlation_id, occurred_at
		FROM f31_admin_audit_events
		ORDER BY occurred_at DESC
		LIMIT 100`)
	if err != nil {
		return Dashboard{}, err
	}
	defer auditRows.Close()
	for auditRows.Next() {
		var event AuditEvent
		if err := auditRows.Scan(
			&event.ID,
			&event.Code,
			&event.ActorID,
			&event.SubjectID,
			&event.Reason,
			&event.Outcome,
			&event.CorrelationID,
			&event.OccurredAt,
		); err != nil {
			return Dashboard{}, err
		}
		result.RecentAudit = append(result.RecentAudit, event)
	}
	return result, auditRows.Err()
}

func projectedService(code string, now time.Time, signal operations.ServiceSignal) ServiceHealth {
	status := operations.ProjectServiceStatus(signal, now, dashboardFreshness)
	checkedAt := signal.CheckedAt
	if checkedAt.IsZero() {
		checkedAt = now
	}
	return ServiceHealth{Code: code, Status: string(status), CheckedAt: checkedAt}
}

// workerQueueSignal derives worker/queue health from the real F08 publishing
// backlog: a queue depth or oldest-waiting-job age beyond the shared F15
// thresholds is critical, and any unresolved dead letter is a warning.
func (store *PostgresStore) workerQueueSignal(
	ctx context.Context,
	now time.Time,
	thresholds operations.AlertThresholds,
) (operations.ServiceSignal, error) {
	var queueDepth int64
	var oldestSeconds float64
	var unresolvedFailures int64
	err := store.database.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (
				WHERE status IN ('pending', 'retry_wait', 'publishing')
			),
			COALESCE(GREATEST(EXTRACT(EPOCH FROM (
				$1::timestamptz - MIN(next_attempt_at) FILTER (
					WHERE status IN ('pending', 'retry_wait', 'publishing')
				)
			)), 0), 0),
			(
				SELECT COUNT(*)
				FROM f08_publication_dead_letters
				WHERE resolved_at IS NULL
			)
		FROM f08_publication_destinations`,
		now,
	).Scan(&queueDepth, &oldestSeconds, &unresolvedFailures)
	if err != nil {
		return operations.ServiceSignal{}, err
	}
	oldestAge := time.Duration(oldestSeconds * float64(time.Second))
	return operations.ServiceSignal{
		Present:   true,
		Reachable: true,
		Critical: queueDepth > thresholds.MaxQueueDepth ||
			oldestAge > thresholds.MaxOldestQueuedJobAge,
		Warning:   unresolvedFailures > 0,
		CheckedAt: now,
	}, nil
}

// schedulerSignal derives scheduler/publishing health from the real F07
// pending-command backlog, reusing the same shared F15 thresholds.
func (store *PostgresStore) schedulerSignal(
	ctx context.Context,
	now time.Time,
	thresholds operations.AlertThresholds,
) (operations.ServiceSignal, error) {
	var pending int64
	var oldestSeconds float64
	err := store.database.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE state = 'pending'),
			COALESCE(GREATEST(EXTRACT(EPOCH FROM (
				$1::timestamptz - MIN(execute_at_utc) FILTER (
					WHERE state = 'pending'
				)
			)), 0), 0)
		FROM f07_publication_commands`,
		now,
	).Scan(&pending, &oldestSeconds)
	if err != nil {
		return operations.ServiceSignal{}, err
	}
	oldestAge := time.Duration(oldestSeconds * float64(time.Second))
	return operations.ServiceSignal{
		Present:   true,
		Reachable: true,
		Critical:  oldestAge > thresholds.MaxOldestQueuedJobAge,
		Warning:   pending > thresholds.MaxQueueDepth,
		CheckedAt: now,
	}, nil
}

func (store *PostgresStore) Search(
	ctx context.Context,
	query string,
) (SearchResults, error) {
	pattern := "%" + escapeLike(query) + "%"
	result := SearchResults{
		Users:      []UserSummary{},
		Workspaces: []WorkspaceSummary{},
	}
	rows, err := store.database.QueryContext(ctx, `
		SELECT
			account.id,
			account.email,
			COALESCE(NULLIF(account.display_name, ''), account.email),
			EXISTS (
				SELECT 1
				FROM auth_provider_identities identity
				WHERE identity.account_id = account.id
				  AND lower(btrim(identity.provider_email)) =
				      account.normalized_email
			)
		FROM auth_accounts account
		WHERE account.normalized_email ILIKE $1 ESCAPE '\'
		   OR account.display_name ILIKE $1 ESCAPE '\'
		ORDER BY account.normalized_email
		LIMIT 50`,
		pattern,
	)
	if err != nil {
		return SearchResults{}, err
	}
	for rows.Next() {
		var item UserSummary
		if err := rows.Scan(
			&item.ID,
			&item.Email,
			&item.DisplayName,
			&item.EmailVerified,
		); err != nil {
			rows.Close()
			return SearchResults{}, err
		}
		result.Users = append(result.Users, item)
	}
	if err := rows.Close(); err != nil {
		return SearchResults{}, err
	}

	workspaceRows, err := store.database.QueryContext(ctx, `
		SELECT
			workspace.id,
			workspace.name,
			owner.email,
			COUNT(membership.account_id) FILTER (
				WHERE membership.status = 'active'
			)
		FROM f04_workspaces workspace
		JOIN auth_accounts owner
		  ON owner.id = workspace.personal_account_id
		LEFT JOIN f04_memberships membership
		  ON membership.workspace_id = workspace.id
		WHERE workspace.name ILIKE $1 ESCAPE '\'
		   OR owner.normalized_email ILIKE $1 ESCAPE '\'
		GROUP BY workspace.id, workspace.name, owner.email
		ORDER BY workspace.name
		LIMIT 50`,
		pattern,
	)
	if err != nil {
		return SearchResults{}, err
	}
	defer workspaceRows.Close()
	for workspaceRows.Next() {
		var item WorkspaceSummary
		if err := workspaceRows.Scan(
			&item.ID,
			&item.Name,
			&item.OwnerEmail,
			&item.MemberCount,
		); err != nil {
			return SearchResults{}, err
		}
		result.Workspaces = append(result.Workspaces, item)
	}
	return result, workspaceRows.Err()
}

func (store *PostgresStore) Change(
	ctx context.Context,
	change InternalPlanChange,
) error {
	transaction, err := store.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return err
	}
	defer transaction.Rollback()

	var ownerID string
	err = transaction.QueryRowContext(ctx, `
		SELECT personal_account_id
		FROM f04_workspaces
		WHERE id = $1
		  AND status = 'active'
		FOR UPDATE`,
		change.WorkspaceID,
	).Scan(&ownerID)
	if err != nil {
		return fmt.Errorf("resolve internal-plan owner: %w", err)
	}
	now := store.clock().UTC()
	switch change.Action {
	case "internal_plan.assign":
		var allowed bool
		err = transaction.QueryRowContext(ctx, `
			SELECT active
			FROM f11_internal_plan_allowlist
			WHERE account_id::text = $1
			  AND workspace_id::text = $2
			FOR UPDATE`,
			ownerID,
			change.WorkspaceID,
		).Scan(&allowed)
		if err != nil || !allowed {
			if err == nil {
				err = errors.New("internal-plan target is not allowlisted")
			}
			return fmt.Errorf("authorize internal-plan assignment: %w", err)
		}
		_, err = transaction.ExecContext(ctx, `
			INSERT INTO f11_internal_plan_bindings (
				workspace_id, account_id, active, assigned_at,
				assigned_by_account_id
			) VALUES ($1::uuid, $2::uuid, true, $3, $4::uuid)
			ON CONFLICT (workspace_id) DO UPDATE
			    SET account_id = EXCLUDED.account_id,
			        active = true,
			        assigned_at = EXCLUDED.assigned_at,
			        assigned_by_account_id = EXCLUDED.assigned_by_account_id,
			        revoked_at = NULL,
			        revoked_by_account_id = NULL`,
			change.WorkspaceID,
			ownerID,
			now,
			change.ActorAccountID,
		)
		if err == nil {
			_, err = transaction.ExecContext(ctx, `
				INSERT INTO f10_internal_entitlement_overrides (
					workspace_id, active, assigned_at
				) VALUES ($1::uuid, true, $2)
				ON CONFLICT (workspace_id) DO UPDATE
				    SET active = true,
				        assigned_at = EXCLUDED.assigned_at,
				        revoked_at = NULL`,
				change.WorkspaceID,
				now,
			)
		}
	case "internal_plan.revoke":
		_, err = transaction.ExecContext(ctx, `
			UPDATE f10_internal_entitlement_overrides
			   SET active = false,
			       revoked_at = $2
			 WHERE workspace_id::text = $1
			   AND active`,
			change.WorkspaceID,
			now,
		)
		if err == nil {
			_, err = transaction.ExecContext(ctx, `
				UPDATE f11_internal_plan_bindings
				   SET active = false,
				       revoked_at = $2,
				       revoked_by_account_id = $3::uuid
				 WHERE workspace_id::text = $1
				   AND active`,
				change.WorkspaceID,
				now,
				change.ActorAccountID,
			)
		}
	default:
		return ErrInvalidRequest
	}
	if err != nil {
		return err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO f31_admin_audit_events (
			id, code, actor_id, subject_id, reason, outcome,
			correlation_id, occurred_at
		) VALUES ($1, $2, $3, $4, $5, 'succeeded', $6, $7)`,
		store.newID(),
		change.Action,
		change.ActorAccountID,
		change.WorkspaceID,
		change.Reason,
		change.CorrelationID,
		now,
	); err != nil {
		return err
	}
	return transaction.Commit()
}

func (store *PostgresStore) Append(
	ctx context.Context,
	event AuditEvent,
) error {
	_, err := store.database.ExecContext(ctx, `
		INSERT INTO f31_admin_audit_events (
			id, code, actor_id, subject_id, reason, outcome,
			correlation_id, occurred_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.ID,
		event.Code,
		event.ActorID,
		event.SubjectID,
		event.Reason,
		event.Outcome,
		event.CorrelationID,
		event.OccurredAt,
	)
	return err
}

func (store *PostgresStore) Do(
	ctx context.Context,
	key string,
	action func() (MutationResult, error),
) (MutationResult, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(
		ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		key,
	); err != nil {
		return MutationResult{}, err
	}
	var result MutationResult
	err = transaction.QueryRowContext(ctx, `
		SELECT result_code, correlation_id
		FROM f31_admin_idempotency
		WHERE key = $1`,
		key,
	).Scan(&result.Code, &result.CorrelationID)
	switch {
	case err == nil:
		return result, transaction.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return MutationResult{}, err
	}
	result, err = action()
	if err != nil {
		return MutationResult{}, err
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO f31_admin_idempotency (
			key, result_code, correlation_id, created_at
		) VALUES ($1, $2, $3, $4)`,
		key,
		result.Code,
		result.CorrelationID,
		store.clock().UTC(),
	)
	if err != nil {
		return MutationResult{}, err
	}
	return result, transaction.Commit()
}

func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}
