package accountprivacyruntime

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	accountprivacy "github.com/apdsoftware/postqron/features/f12-account-privacy"
)

// AccountAccessBoundary is the deliberately narrow F3 adapter required by the
// privacy runtime. #228 can implement it without exposing auth persistence or
// credentials to F12.
type AccountAccessBoundary interface {
	Freeze(context.Context, string) error
	Restore(context.Context, string) error
	Finalize(context.Context, string) error
}

type ProviderRevocationBoundary interface {
	RevokeForDeletion(context.Context, accountprivacy.DeletionRequest, []string) error
}

type ownershipResolver struct {
	database *sql.DB
}

func (resolver ownershipResolver) Resolve(
	ctx context.Context,
	accountID string,
	scope accountprivacy.DeletionScope,
	workspaceID string,
	actions []accountprivacy.OwnershipAction,
) (accountprivacy.OwnershipPlan, error) {
	rows, err := resolver.database.QueryContext(ctx, `
		SELECT workspace.id
		FROM f04_workspaces workspace
		JOIN f04_memberships membership ON membership.workspace_id = workspace.id
		WHERE membership.account_id = $1
		  AND membership.role = 'owner'
		  AND membership.status = 'active'
		  AND workspace.status = 'active'
		  AND ($2 = '' OR workspace.id = $2)
		ORDER BY workspace.id`, accountID, workspaceID)
	if err != nil {
		return accountprivacy.OwnershipPlan{}, fmt.Errorf("resolve owned workspaces: %w", err)
	}
	defer rows.Close()
	owned := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return accountprivacy.OwnershipPlan{}, err
		}
		owned[id] = true
	}
	if err := rows.Err(); err != nil {
		return accountprivacy.OwnershipPlan{}, err
	}
	if scope == accountprivacy.DeleteWorkspace && !owned[workspaceID] {
		return accountprivacy.OwnershipPlan{}, accountprivacy.ErrForbidden
	}
	provided := make(map[string]accountprivacy.OwnershipAction, len(actions))
	for _, action := range actions {
		action.WorkspaceID = strings.TrimSpace(action.WorkspaceID)
		action.TransferAccountID = strings.TrimSpace(action.TransferAccountID)
		if !owned[action.WorkspaceID] || provided[action.WorkspaceID].WorkspaceID != "" {
			return accountprivacy.OwnershipPlan{}, accountprivacy.ErrInvalidArgument
		}
		switch action.Action {
		case accountprivacy.DeleteOwnedSpace:
			action.TransferAccountID = ""
		case accountprivacy.TransferWorkspace:
			if action.TransferAccountID == "" || action.TransferAccountID == accountID {
				return accountprivacy.OwnershipPlan{}, accountprivacy.ErrInvalidArgument
			}
			var eligible bool
			if err := resolver.database.QueryRowContext(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM f04_memberships
					WHERE workspace_id = $1 AND account_id = $2 AND status = 'active'
				)`, action.WorkspaceID, action.TransferAccountID).Scan(&eligible); err != nil {
				return accountprivacy.OwnershipPlan{}, err
			}
			if !eligible {
				return accountprivacy.OwnershipPlan{}, accountprivacy.ErrInvalidArgument
			}
		default:
			return accountprivacy.OwnershipPlan{}, accountprivacy.ErrInvalidArgument
		}
		provided[action.WorkspaceID] = action
	}
	if len(provided) != len(owned) {
		return accountprivacy.OwnershipPlan{}, accountprivacy.ErrInvalidArgument
	}
	plan := accountprivacy.OwnershipPlan{Actions: make([]accountprivacy.OwnershipAction, 0, len(owned))}
	for _, action := range actions {
		plan.Actions = append(plan.Actions, provided[action.WorkspaceID])
	}
	return plan, nil
}

type deletionSafety struct {
	database  *sql.DB
	access    AccountAccessBoundary
	providers ProviderRevocationBoundary
	now       func() time.Time
}

func (safety deletionSafety) Deactivate(
	ctx context.Context,
	request accountprivacy.DeletionRequest,
) (accountprivacy.DeactivationReceipt, error) {
	frozen := false
	if request.Scope == accountprivacy.DeleteAccount {
		if err := safety.access.Freeze(ctx, request.AccountID); err != nil {
			return accountprivacy.DeactivationReceipt{}, err
		}
		frozen = true
	}
	workspaceIDs, err := safety.targetWorkspaces(ctx, request)
	if err != nil {
		return accountprivacy.DeactivationReceipt{}, safety.compensate(ctx, request.AccountID, frozen, err)
	}
	if err := safety.providers.RevokeForDeletion(ctx, request, workspaceIDs); err != nil {
		return accountprivacy.DeactivationReceipt{}, safety.compensate(ctx, request.AccountID, frozen, err)
	}
	transaction, err := safety.database.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return accountprivacy.DeactivationReceipt{}, safety.compensate(ctx, request.AccountID, frozen, err)
	}
	defer transaction.Rollback()
	now := safety.now().UTC()
	for _, workspaceID := range workspaceIDs {
		if _, err := transaction.ExecContext(ctx, `
			UPDATE f07_scheduled_posts
			SET status = 'cancelled', active_command_id = NULL,
			    cancelled_at = COALESCE(cancelled_at, $2), updated_at = $2
			WHERE workspace_id = $1 AND status IN ('scheduled', 'failed')`,
			workspaceID, now); err != nil {
			return accountprivacy.DeactivationReceipt{}, safety.compensate(
				ctx, request.AccountID, frozen, fmt.Errorf("cancel future posts: %w", err),
			)
		}
		if _, err := transaction.ExecContext(ctx, `
			UPDATE f07_publication_commands
			SET state = 'invalidated', invalidated_at = COALESCE(invalidated_at, $2)
			WHERE workspace_id = $1 AND state = 'pending'`,
			workspaceID, now); err != nil {
			return accountprivacy.DeactivationReceipt{}, safety.compensate(
				ctx, request.AccountID, frozen, fmt.Errorf("invalidate publication jobs: %w", err),
			)
		}
		if _, err := transaction.ExecContext(ctx, `
			UPDATE f05_social_connections
			SET status = 'revoked', access_token_key_id = NULL,
			    access_token_ciphertext = NULL, refresh_token_key_id = NULL,
			    refresh_token_ciphertext = NULL, token_expires_at = NULL,
			    refresh_locked_until = NULL, revoked_at = COALESCE(revoked_at, $2),
			    updated_at = $2
			WHERE workspace_id = $1 AND status <> 'revoked'`,
			workspaceID, now); err != nil {
			return accountprivacy.DeactivationReceipt{}, safety.compensate(
				ctx, request.AccountID, frozen, fmt.Errorf("delete local provider tokens: %w", err),
			)
		}
	}
	if request.Scope == accountprivacy.DeleteWorkspace {
		if _, err := transaction.ExecContext(ctx,
			`UPDATE f04_workspaces SET status = 'deletion_pending', updated_at = $2 WHERE id = $1`,
			request.WorkspaceID, now); err != nil {
			return accountprivacy.DeactivationReceipt{}, safety.compensate(ctx, request.AccountID, frozen, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return accountprivacy.DeactivationReceipt{}, safety.compensate(ctx, request.AccountID, frozen, err)
	}
	return accountprivacy.DeactivationReceipt{
		AccessFrozen:                true,
		SessionsRevoked:             true,
		ProviderRevocationAttempted: true,
		LocalTokensDeleted:          true,
		FutureJobsCancelled:         true,
	}, nil
}

func (safety deletionSafety) compensate(
	ctx context.Context,
	accountID string,
	frozen bool,
	cause error,
) error {
	if !frozen {
		return cause
	}
	if restoreErr := safety.access.Restore(ctx, accountID); restoreErr != nil {
		return errors.Join(cause, fmt.Errorf("compensating access restore: %w", restoreErr))
	}
	return cause
}

func (safety deletionSafety) RestoreAccess(
	ctx context.Context,
	request accountprivacy.DeletionRequest,
) error {
	if request.Scope == accountprivacy.DeleteAccount {
		return safety.access.Restore(ctx, request.AccountID)
	}
	_, err := safety.database.ExecContext(ctx,
		`UPDATE f04_workspaces SET status = 'active', updated_at = $2
		 WHERE id = $1 AND status = 'deletion_pending'`,
		request.WorkspaceID, safety.now().UTC())
	return err
}

func (safety deletionSafety) targetWorkspaces(
	ctx context.Context,
	request accountprivacy.DeletionRequest,
) ([]string, error) {
	if request.Scope == accountprivacy.DeleteWorkspace {
		return []string{request.WorkspaceID}, nil
	}
	rows, err := safety.database.QueryContext(ctx, `
		SELECT workspace_id FROM f04_memberships
		WHERE account_id = $1 AND status = 'active'
		ORDER BY workspace_id`, request.AccountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type eraser struct {
	database *sql.DB
	access   AccountAccessBoundary
}

func (eraser eraser) Erase(
	ctx context.Context,
	request accountprivacy.DeletionRequest,
	now time.Time,
) (accountprivacy.ErasureReceipt, error) {
	if request.Scope == accountprivacy.DeleteAccount {
		if err := eraser.access.Finalize(ctx, request.AccountID); err != nil {
			return accountprivacy.ErasureReceipt{}, err
		}
	}
	transaction, err := eraser.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return accountprivacy.ErasureReceipt{}, err
	}
	defer transaction.Rollback()
	for _, action := range request.Ownership.Actions {
		if action.Action == accountprivacy.TransferWorkspace {
			if _, err := transaction.ExecContext(ctx, `
				UPDATE f04_memberships SET role = 'member', updated_at = $3
				WHERE workspace_id = $1 AND account_id = $2;
				UPDATE f04_memberships SET role = 'owner', updated_at = $3
				WHERE workspace_id = $1 AND account_id = $4`,
				action.WorkspaceID, request.AccountID, now, action.TransferAccountID); err != nil {
				return accountprivacy.ErasureReceipt{}, err
			}
		} else if err := deleteWorkspaceData(ctx, transaction, action.WorkspaceID); err != nil {
			return accountprivacy.ErasureReceipt{}, err
		}
	}
	anonymous := "deleted:" + request.ID
	if request.Scope == accountprivacy.DeleteAccount {
		if _, err := transaction.ExecContext(ctx,
			`UPDATE f06_composer_drafts SET created_by_account_id = $2 WHERE created_by_account_id = $1;
			 UPDATE f07_scheduled_posts SET created_by_account_id = $2 WHERE created_by_account_id = $1;
			 UPDATE f08_publication_dead_letters SET retried_by_account_id = NULL WHERE retried_by_account_id = $1;
			 DELETE FROM f04_memberships WHERE account_id = $1;
			 DELETE FROM account_privacy_profiles WHERE account_id = $1`,
			request.AccountID, anonymous); err != nil {
			return accountprivacy.ErasureReceipt{}, fmt.Errorf("erase account attribution: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return accountprivacy.ErasureReceipt{}, err
	}
	tombstone := make([]byte, 18)
	if _, err := rand.Read(tombstone); err != nil {
		return accountprivacy.ErasureReceipt{}, err
	}
	return accountprivacy.ErasureReceipt{
		IdentifyingDataDeleted:      true,
		SharedAttributionAnonymized: true,
		WorkspaceDataDeleted:        request.Scope == accountprivacy.DeleteWorkspace || allDelete(request.Ownership),
		OwnershipApplied:            true,
		TombstoneID:                 hex.EncodeToString(tombstone),
		TombstoneExpiresAt:          now.Add(accountprivacy.TombstoneRetention),
		DatabaseCompletedAt:         now,
		MediaDeletionDueAt:          now,
	}, nil
}

func allDelete(plan accountprivacy.OwnershipPlan) bool {
	for _, action := range plan.Actions {
		if action.Action != accountprivacy.DeleteOwnedSpace {
			return false
		}
	}
	return true
}

func deleteWorkspaceData(ctx context.Context, transaction *sql.Tx, workspaceID string) error {
	queries := []string{
		`DELETE FROM f09_manual_retry_outbox WHERE workspace_id = $1`,
		`DELETE FROM f09_notification_outbox WHERE workspace_id = $1`,
		`DELETE FROM f09_publication_status_events WHERE workspace_id = $1`,
		`DELETE FROM f09_destination_status WHERE workspace_id = $1`,
		`DELETE FROM f09_post_status WHERE workspace_id = $1`,
		`DELETE FROM f08_publication_dead_letters WHERE job_id IN (SELECT id FROM f08_publication_jobs WHERE workspace_id = $1)`,
		`DELETE FROM f08_publication_attempts WHERE destination_id IN (SELECT id FROM f08_publication_destinations WHERE workspace_id = $1)`,
		`DELETE FROM f08_publication_destinations WHERE workspace_id = $1`,
		`DELETE FROM f08_publication_jobs WHERE workspace_id = $1`,
		`DELETE FROM f07_publication_commands WHERE workspace_id = $1`,
		`DELETE FROM f07_scheduled_posts WHERE workspace_id = $1`,
		`DELETE FROM f06_composer_drafts WHERE workspace_id = $1`,
		`DELETE FROM f05_social_outbox WHERE workspace_id = $1`,
		`DELETE FROM f05_social_connections WHERE workspace_id = $1`,
		`DELETE FROM f05_selection_resources WHERE selection_id IN (SELECT id FROM f05_resource_selections WHERE workspace_id = $1)`,
		`DELETE FROM f05_resource_selections WHERE workspace_id = $1`,
		`DELETE FROM f05_oauth_attempts WHERE workspace_id = $1`,
		`DELETE FROM f04_workspaces WHERE id = $1`,
	}
	for _, query := range queries {
		if _, err := transaction.ExecContext(ctx, query, workspaceID); err != nil {
			return fmt.Errorf("erase workspace data: %w", err)
		}
	}
	return nil
}
