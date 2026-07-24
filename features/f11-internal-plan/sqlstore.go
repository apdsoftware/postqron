package internalplan

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type SQLDB interface {
	Begin(context.Context) (pgx.Tx, error)
}

type SQLRepository struct {
	db         SQLDB
	auditTable string
}

func NewSQLRepository(db SQLDB) *SQLRepository {
	return &SQLRepository{
		db:         db,
		auditTable: pgx.Identifier{"operations", "sensitive_audit_events"}.Sanitize(),
	}
}

func (repository *SQLRepository) Apply(
	ctx context.Context,
	command ChangeCommand,
) (ChangeResult, error) {
	transaction, err := repository.db.Begin(ctx)
	if err != nil {
		return ChangeResult{}, fmt.Errorf("begin internal plan change: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(context.Background())
	}()

	var result ChangeResult
	switch command.Action {
	case ActionAssign:
		result, err = applyAssignment(
			ctx,
			transaction,
			repository.auditTable,
			command,
		)
	case ActionRevoke:
		result, err = applyRevocation(
			ctx,
			transaction,
			repository.auditTable,
			command,
		)
	default:
		err = ErrInvalidRequest
	}
	if err != nil {
		return ChangeResult{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return ChangeResult{}, fmt.Errorf("commit internal plan change: %w", err)
	}
	return result, nil
}

func (repository *SQLRepository) AppendAudit(
	ctx context.Context,
	event AuditEvent,
) error {
	transaction, err := repository.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin sensitive audit: %w", err)
	}
	defer func() {
		_ = transaction.Rollback(context.Background())
	}()
	if err := insertAudit(ctx, transaction, repository.auditTable, event); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit sensitive audit: %w", err)
	}
	return nil
}

func applyAssignment(
	ctx context.Context,
	transaction pgx.Tx,
	auditTable string,
	command ChangeCommand,
) (ChangeResult, error) {
	if err := lockBillingWorkspace(ctx, transaction, command.WorkspaceID); err != nil {
		return ChangeResult{}, err
	}
	var allowlisted bool
	err := transaction.QueryRow(ctx, `
		SELECT active
		  FROM f11_internal_plan_allowlist
		 WHERE account_id = $1
		   AND workspace_id = $2
		 FOR UPDATE
	`, command.TargetAccountID, command.WorkspaceID).Scan(&allowlisted)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return ChangeResult{}, fmt.Errorf("lock internal plan allowlist: %w", err)
		}
		allowlisted = false
	}
	if !allowlisted {
		if err := insertAudit(
			ctx,
			transaction,
			auditTable,
			command.audit(OutcomeDenied),
		); err != nil {
			return ChangeResult{}, err
		}
		if err := transaction.Commit(ctx); err != nil {
			return ChangeResult{}, fmt.Errorf("commit allowlist denial audit: %w", err)
		}
		return ChangeResult{}, ErrTargetNotAllowlisted
	}

	var (
		boundAccountID string
		bindingActive  bool
	)
	err = transaction.QueryRow(ctx, `
		SELECT account_id::text, active
		  FROM f11_internal_plan_bindings
		 WHERE workspace_id = $1
		 FOR UPDATE
	`, command.WorkspaceID).Scan(&boundAccountID, &bindingActive)
	switch {
	case err == nil && bindingActive && boundAccountID != command.TargetAccountID:
		if auditErr := insertAudit(
			ctx,
			transaction,
			auditTable,
			command.audit(OutcomeDenied),
		); auditErr != nil {
			return ChangeResult{}, auditErr
		}
		if commitErr := transaction.Commit(ctx); commitErr != nil {
			return ChangeResult{}, fmt.Errorf("commit binding denial audit: %w", commitErr)
		}
		return ChangeResult{}, ErrActiveBindingConflict
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return ChangeResult{}, fmt.Errorf("lock internal plan binding: %w", err)
	}

	bindingMissing := errors.Is(err, pgx.ErrNoRows)
	var overrideActive bool
	overrideErr := transaction.QueryRow(ctx, `
		SELECT active
		  FROM f10_internal_entitlement_overrides
		 WHERE workspace_id = $1
		 FOR UPDATE
	`, command.WorkspaceID).Scan(&overrideActive)
	if overrideErr != nil && !errors.Is(overrideErr, pgx.ErrNoRows) {
		return ChangeResult{}, fmt.Errorf("lock entitlement override: %w", overrideErr)
	}
	changed := bindingMissing ||
		!bindingActive ||
		errors.Is(overrideErr, pgx.ErrNoRows) ||
		!overrideActive

	_, err = transaction.Exec(ctx, `
		INSERT INTO f11_internal_plan_bindings (
			workspace_id,
			account_id,
			active,
			assigned_at,
			assigned_by_account_id
		) VALUES ($1, $2, true, $3, $4)
		ON CONFLICT (workspace_id) DO UPDATE
		    SET account_id = EXCLUDED.account_id,
		        active = true,
		        assigned_at = EXCLUDED.assigned_at,
		        assigned_by_account_id = EXCLUDED.assigned_by_account_id,
		        revoked_at = NULL,
		        revoked_by_account_id = NULL
		WHERE NOT f11_internal_plan_bindings.active
	`, command.WorkspaceID, command.TargetAccountID, command.OccurredAt, command.ActorAccountID)
	if err != nil {
		return ChangeResult{}, fmt.Errorf("record internal plan binding: %w", err)
	}
	_, err = transaction.Exec(ctx, `
		INSERT INTO f10_internal_entitlement_overrides (
			workspace_id,
			active,
			assigned_at
		) VALUES ($1, true, $2)
		ON CONFLICT (workspace_id) DO UPDATE
		    SET active = true,
		        assigned_at = EXCLUDED.assigned_at,
		        revoked_at = NULL
		WHERE NOT f10_internal_entitlement_overrides.active
	`, command.WorkspaceID, command.OccurredAt)
	if err != nil {
		return ChangeResult{}, fmt.Errorf("activate entitlement override: %w", err)
	}
	if err := insertAudit(
		ctx,
		transaction,
		auditTable,
		command.audit(OutcomeSucceeded),
	); err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{
		AuditEventID: command.EventID,
		Changed:      changed,
		Active:       true,
		OccurredAt:   command.OccurredAt,
	}, nil
}

func applyRevocation(
	ctx context.Context,
	transaction pgx.Tx,
	auditTable string,
	command ChangeCommand,
) (ChangeResult, error) {
	if err := lockBillingWorkspace(ctx, transaction, command.WorkspaceID); err != nil {
		return ChangeResult{}, err
	}
	var (
		boundAccountID string
		bindingActive  bool
	)
	err := transaction.QueryRow(ctx, `
		SELECT account_id::text, active
		  FROM f11_internal_plan_bindings
		 WHERE workspace_id = $1
		 FOR UPDATE
	`, command.WorkspaceID).Scan(&boundAccountID, &bindingActive)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ChangeResult{}, fmt.Errorf("lock internal plan binding: %w", err)
	}
	if err == nil {
		command.TargetAccountID = boundAccountID
	}

	tag, err := transaction.Exec(ctx, `
		UPDATE f10_internal_entitlement_overrides
		   SET active = false,
		       revoked_at = $2
		 WHERE workspace_id = $1
		   AND active
	`, command.WorkspaceID, command.OccurredAt)
	if err != nil {
		return ChangeResult{}, fmt.Errorf("revoke entitlement override: %w", err)
	}
	changed := tag.RowsAffected() > 0 || bindingActive

	if bindingActive {
		_, err = transaction.Exec(ctx, `
			UPDATE f11_internal_plan_bindings
			   SET active = false,
			       revoked_at = $2,
			       revoked_by_account_id = $3
			 WHERE workspace_id = $1
		`, command.WorkspaceID, command.OccurredAt, command.ActorAccountID)
		if err != nil {
			return ChangeResult{}, fmt.Errorf("revoke internal plan binding: %w", err)
		}
	}
	if err := insertAudit(
		ctx,
		transaction,
		auditTable,
		command.audit(OutcomeSucceeded),
	); err != nil {
		return ChangeResult{}, err
	}
	return ChangeResult{
		AuditEventID: command.EventID,
		Changed:      changed,
		Active:       false,
		OccurredAt:   command.OccurredAt,
	}, nil
}

func lockBillingWorkspace(
	ctx context.Context,
	transaction pgx.Tx,
	workspaceID string,
) error {
	var locked bool
	err := transaction.QueryRow(ctx, `
		SELECT true
		  FROM f10_workspace_billing
		 WHERE workspace_id = $1
		 FOR UPDATE
	`, workspaceID).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrInternalPlanUnavailable
	}
	if err != nil {
		return fmt.Errorf("lock workspace entitlement: %w", err)
	}
	return nil
}

func insertAudit(
	ctx context.Context,
	transaction pgx.Tx,
	auditTable string,
	event AuditEvent,
) error {
	targetType := "workspace"
	targetID := event.WorkspaceID
	if event.TargetAccountID != "" {
		targetType = "account"
		targetID = event.TargetAccountID
	}
	_, err := transaction.Exec(ctx, `
		INSERT INTO `+auditTable+` (
			event_id,
			occurred_at,
			actor_type,
			actor_id,
			workspace_id,
			action,
			target_type,
			target_id,
			outcome,
			correlation_id
		) VALUES ($1, $2, 'account', $3, $4, $5, $6, $7, $8, $9)
	`,
		event.EventID,
		event.OccurredAt,
		event.ActorAccountID,
		event.WorkspaceID,
		event.Action,
		targetType,
		targetID,
		event.Outcome,
		event.CorrelationID,
	)
	if err != nil {
		return fmt.Errorf("append sensitive internal plan audit: %w", err)
	}
	return nil
}

func (command ChangeCommand) audit(outcome Outcome) AuditEvent {
	return AuditEvent{
		EventID:         command.EventID,
		Action:          command.Action,
		Outcome:         outcome,
		ActorAccountID:  command.ActorAccountID,
		WorkspaceID:     command.WorkspaceID,
		TargetAccountID: command.TargetAccountID,
		CorrelationID:   command.CorrelationID,
		OccurredAt:      command.OccurredAt,
	}
}
