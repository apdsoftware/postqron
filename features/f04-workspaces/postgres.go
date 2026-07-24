package workspaces

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type PostgresRepository struct {
	database *sql.DB
}

func NewPostgresRepository(database *sql.DB) (*PostgresRepository, error) {
	if database == nil {
		return nil, fmt.Errorf("%w: database is required", ErrInvalidArgument)
	}
	return &PostgresRepository{database: database}, nil
}

func (repository *PostgresRepository) EnsurePersonal(
	ctx context.Context,
	accountID string,
	name string,
	workspaceID string,
	now time.Time,
) (Workspace, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return Workspace{}, false, fmt.Errorf("begin personal workspace transaction: %w", err)
	}
	defer transaction.Rollback()

	workspace, err := scanWorkspace(transaction.QueryRowContext(
		ctx,
		`INSERT INTO f04_workspaces (
			id, personal_account_id, name, status, created_at, updated_at
		) VALUES ($1, $2, $3, 'active', $4, $4)
		ON CONFLICT (personal_account_id) DO NOTHING
		RETURNING id, personal_account_id, name, status, created_at, updated_at`,
		workspaceID,
		accountID,
		name,
		now,
	))
	created := err == nil
	if errors.Is(err, sql.ErrNoRows) {
		workspace, err = scanWorkspace(transaction.QueryRowContext(
			ctx,
			`SELECT id, personal_account_id, name, status, created_at, updated_at
			 FROM f04_workspaces
			 WHERE personal_account_id = $1
			 FOR UPDATE`,
			accountID,
		))
	}
	if err != nil {
		return Workspace{}, false, fmt.Errorf("ensure personal workspace: %w", err)
	}
	if created {
		if _, err = transaction.ExecContext(
			ctx,
			`INSERT INTO f04_memberships (
				workspace_id, account_id, role, status, created_at, updated_at
			 ) VALUES ($1, $2, 'owner', 'active', $3, $3)`,
			workspace.ID,
			accountID,
			now,
		); err != nil {
			return Workspace{}, false, fmt.Errorf("create personal Owner membership: %w", err)
		}
		if err = insertAudit(
			ctx,
			transaction,
			workspace.ID,
			accountID,
			accountID,
			"workspace.personal_created",
			now,
		); err != nil {
			return Workspace{}, false, err
		}
	}
	if err = transaction.Commit(); err != nil {
		return Workspace{}, false, fmt.Errorf("commit personal workspace: %w", err)
	}
	return workspace, created, nil
}

func (repository *PostgresRepository) Role(
	ctx context.Context,
	workspaceID, accountID string,
) (Role, error) {
	var role Role
	err := repository.database.QueryRowContext(
		ctx,
		`SELECT role
		 FROM f04_memberships
		 WHERE workspace_id = $1
		   AND account_id = $2
		   AND status = 'active'`,
		workspaceID,
		accountID,
	).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", fmt.Errorf("read membership role: %w", err)
	}
	return role, nil
}

func (repository *PostgresRepository) WorkspaceStatus(
	ctx context.Context,
	workspaceID string,
) (WorkspaceStatus, error) {
	var status WorkspaceStatus
	err := repository.database.QueryRowContext(
		ctx,
		`SELECT status FROM f04_workspaces WHERE id = $1`,
		workspaceID,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read workspace status: %w", err)
	}
	return status, nil
}

func (repository *PostgresRepository) GetWorkspace(
	ctx context.Context,
	workspaceID, actorID string,
) (Workspace, error) {
	workspace, err := scanWorkspace(repository.database.QueryRowContext(
		ctx,
		`SELECT workspace.id, workspace.personal_account_id, workspace.name,
		        workspace.status, workspace.created_at, workspace.updated_at
		 FROM f04_workspaces workspace
		 JOIN f04_memberships actor
		   ON actor.workspace_id = workspace.id
		  AND actor.account_id = $2
		  AND actor.status = 'active'
		 WHERE workspace.id = $1`,
		workspaceID,
		actorID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Workspace{}, ErrForbidden
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("read workspace: %w", err)
	}
	return workspace, nil
}

func (repository *PostgresRepository) ListMemberships(
	ctx context.Context,
	workspaceID, actorID string,
) ([]Membership, error) {
	rows, err := repository.database.QueryContext(
		ctx,
		`SELECT workspace_id, account_id, role, status, created_at, updated_at
		 FROM f04_memberships
		 WHERE workspace_id = $1 AND status = 'active'
		   AND EXISTS (
		       SELECT 1
		       FROM f04_memberships actor
		       WHERE actor.workspace_id = $1
		         AND actor.account_id = $2
		         AND actor.status = 'active'
		   )
		 ORDER BY account_id`,
		workspaceID,
		actorID,
	)
	if err != nil {
		return nil, fmt.Errorf("list workspace memberships: %w", err)
	}
	defer rows.Close()
	var memberships []Membership
	for rows.Next() {
		membership, scanErr := scanMembership(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan workspace membership: %w", scanErr)
		}
		memberships = append(memberships, membership)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace memberships: %w", err)
	}
	return memberships, nil
}

func (repository *PostgresRepository) ListPendingInvitations(
	ctx context.Context,
	workspaceID, actorID string,
	now time.Time,
) ([]Invitation, error) {
	transaction, err := repository.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer transaction.Rollback()
	if err = lockWorkspace(ctx, transaction, workspaceID, true); err != nil {
		return nil, err
	}
	if err = requireOwner(ctx, transaction, workspaceID, actorID); err != nil {
		return nil, err
	}
	if err = expireInvitations(ctx, transaction, workspaceID, now); err != nil {
		return nil, err
	}
	rows, err := transaction.QueryContext(
		ctx,
		`SELECT id, workspace_id, status, expires_at,
		        COALESCE(accepted_by_account_id, ''), created_at, updated_at
		 FROM f04_invitations
		 WHERE workspace_id = $1 AND status = 'pending'
		 ORDER BY id`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending invitations: %w", err)
	}
	var invitations []Invitation
	for rows.Next() {
		invitation, scanErr := scanInvitation(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan pending invitation: %w", scanErr)
		}
		invitations = append(invitations, invitation)
	}
	if err = rows.Close(); err != nil {
		return nil, fmt.Errorf("close pending invitations: %w", err)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending invitations: %w", err)
	}
	if err = transaction.Commit(); err != nil {
		return nil, fmt.Errorf("commit pending invitation list: %w", err)
	}
	return invitations, nil
}

func (repository *PostgresRepository) RenameWorkspace(
	ctx context.Context,
	workspaceID, actorID, name string,
	now time.Time,
) (Workspace, error) {
	transaction, err := repository.ownerTransaction(ctx, workspaceID, actorID, true)
	if err != nil {
		return Workspace{}, err
	}
	defer transaction.Rollback()
	workspace, err := scanWorkspace(transaction.QueryRowContext(
		ctx,
		`UPDATE f04_workspaces
		 SET name = $2, updated_at = $3
		 WHERE id = $1
		 RETURNING id, personal_account_id, name, status, created_at, updated_at`,
		workspaceID,
		name,
		now,
	))
	if err != nil {
		return Workspace{}, fmt.Errorf("rename workspace: %w", err)
	}
	if err = insertAudit(
		ctx,
		transaction,
		workspaceID,
		actorID,
		workspaceID,
		"workspace.renamed",
		now,
	); err != nil {
		return Workspace{}, err
	}
	if err = transaction.Commit(); err != nil {
		return Workspace{}, fmt.Errorf("commit workspace rename: %w", err)
	}
	return workspace, nil
}

func (repository *PostgresRepository) CreateInvitation(
	ctx context.Context,
	command CreateInvitationCommand,
) (Invitation, bool, error) {
	transaction, err := repository.begin(ctx)
	if err != nil {
		return Invitation{}, false, err
	}
	defer transaction.Rollback()

	if err = lockWorkspace(ctx, transaction, command.WorkspaceID, true); err != nil {
		return Invitation{}, false, err
	}
	if err = requireOwner(ctx, transaction, command.WorkspaceID, command.ActorID); err != nil {
		return Invitation{}, false, err
	}
	if err = expireInvitations(ctx, transaction, command.WorkspaceID, command.Now); err != nil {
		return Invitation{}, false, err
	}

	invitation, err := scanInvitation(transaction.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, status, expires_at,
		        COALESCE(accepted_by_account_id, ''), created_at, updated_at
		 FROM f04_invitations
		 WHERE workspace_id = $1
		   AND email_digest = $2
		   AND status = 'pending'
		 FOR UPDATE`,
		command.WorkspaceID,
		command.EmailDigest,
	))
	if err == nil {
		invitation, err = scanInvitation(transaction.QueryRowContext(
			ctx,
			`UPDATE f04_invitations
			 SET token_digest = $2, expires_at = $3, updated_at = $4
			 WHERE id = $1
			 RETURNING id, workspace_id, status, expires_at,
			           COALESCE(accepted_by_account_id, ''), created_at, updated_at`,
			invitation.ID,
			command.TokenDigest,
			command.ExpiresAt,
			command.Now,
		))
		if err != nil {
			return Invitation{}, false, fmt.Errorf("reissue invitation: %w", err)
		}
		if err = insertAudit(
			ctx,
			transaction,
			command.WorkspaceID,
			command.ActorID,
			invitation.ID,
			"invitation.reissued",
			command.Now,
		); err != nil {
			return Invitation{}, false, err
		}
		if err = transaction.Commit(); err != nil {
			return Invitation{}, false, fmt.Errorf("commit invitation reissue: %w", err)
		}
		return invitation, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Invitation{}, false, fmt.Errorf("find pending invitation: %w", err)
	}

	usage, err := memberUsage(ctx, transaction, command.WorkspaceID, command.Now)
	if err != nil {
		return Invitation{}, false, err
	}
	if command.MemberLimit > 0 && usage >= command.MemberLimit {
		return Invitation{}, false, ErrMemberLimitReached
	}
	invitation, err = scanInvitation(transaction.QueryRowContext(
		ctx,
		`INSERT INTO f04_invitations (
			id, workspace_id, email_digest, token_digest, status,
			expires_at, created_at, updated_at
		 ) VALUES ($1, $2, $3, $4, 'pending', $5, $6, $6)
		 RETURNING id, workspace_id, status, expires_at,
		           COALESCE(accepted_by_account_id, ''), created_at, updated_at`,
		command.ID,
		command.WorkspaceID,
		command.EmailDigest,
		command.TokenDigest,
		command.ExpiresAt,
		command.Now,
	))
	if err != nil {
		return Invitation{}, false, fmt.Errorf("create invitation: %w", err)
	}
	if err = insertAudit(
		ctx,
		transaction,
		command.WorkspaceID,
		command.ActorID,
		invitation.ID,
		"invitation.created",
		command.Now,
	); err != nil {
		return Invitation{}, false, err
	}
	if err = transaction.Commit(); err != nil {
		return Invitation{}, false, fmt.Errorf("commit invitation: %w", err)
	}
	return invitation, false, nil
}

func (repository *PostgresRepository) InvitationWorkspace(
	ctx context.Context,
	tokenDigest []byte,
) (string, error) {
	var workspaceID string
	err := repository.database.QueryRowContext(
		ctx,
		`SELECT workspace_id
		 FROM f04_invitations
		 WHERE token_digest = $1`,
		tokenDigest,
	).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find invitation workspace: %w", err)
	}
	return workspaceID, nil
}

func (repository *PostgresRepository) AcceptInvitation(
	ctx context.Context,
	command AcceptInvitationCommand,
) (Membership, error) {
	transaction, err := repository.begin(ctx)
	if err != nil {
		return Membership{}, err
	}
	defer transaction.Rollback()

	var workspaceID string
	err = transaction.QueryRowContext(
		ctx,
		`SELECT workspace_id
		 FROM f04_invitations
		 WHERE token_digest = $1`,
		command.TokenDigest,
	).Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("find invitation: %w", err)
	}
	if err = lockWorkspace(ctx, transaction, workspaceID, true); err != nil {
		return Membership{}, err
	}

	var invitation Invitation
	var emailDigest []byte
	invitation, emailDigest, err = scanInvitationWithEmail(transaction.QueryRowContext(
		ctx,
		`SELECT id, workspace_id, status, expires_at,
		        COALESCE(accepted_by_account_id, ''), created_at, updated_at,
		        email_digest
		 FROM f04_invitations
		 WHERE token_digest = $1
		 FOR UPDATE`,
		command.TokenDigest,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrNotFound
	}
	if err != nil {
		return Membership{}, fmt.Errorf("lock invitation: %w", err)
	}
	switch invitation.Status {
	case InvitationAccepted:
		if invitation.AcceptedByAccountID != command.AccountID {
			return Membership{}, ErrInvitationAccepted
		}
		membership, scanErr := findActiveMembership(
			ctx,
			transaction,
			workspaceID,
			command.AccountID,
			false,
		)
		if scanErr != nil {
			return Membership{}, scanErr
		}
		if err = transaction.Commit(); err != nil {
			return Membership{}, fmt.Errorf("commit invitation retry: %w", err)
		}
		return membership, nil
	case InvitationRevoked:
		return Membership{}, ErrInvitationRevoked
	case InvitationExpired:
		return Membership{}, ErrInvitationExpired
	case InvitationPending:
	default:
		return Membership{}, ErrConflict
	}
	if !invitation.ExpiresAt.After(command.Now) {
		if _, err = transaction.ExecContext(
			ctx,
			`UPDATE f04_invitations
			 SET status = 'expired', updated_at = $2
			 WHERE id = $1`,
			invitation.ID,
			command.Now,
		); err != nil {
			return Membership{}, fmt.Errorf("expire invitation: %w", err)
		}
		if err = insertAudit(
			ctx,
			transaction,
			workspaceID,
			"",
			invitation.ID,
			"invitation.expired",
			command.Now,
		); err != nil {
			return Membership{}, err
		}
		if err = transaction.Commit(); err != nil {
			return Membership{}, fmt.Errorf("commit invitation expiration: %w", err)
		}
		return Membership{}, ErrInvitationExpired
	}
	if !bytes.Equal(emailDigest, command.EmailDigest) {
		return Membership{}, ErrEmailMismatch
	}
	if err = expireInvitations(ctx, transaction, workspaceID, command.Now); err != nil {
		return Membership{}, err
	}

	membership, alreadyActive, err := existingActiveMembership(
		ctx,
		transaction,
		workspaceID,
		command.AccountID,
	)
	if err != nil {
		return Membership{}, err
	}
	if !alreadyActive {
		usage, usageErr := memberUsage(ctx, transaction, workspaceID, command.Now)
		if usageErr != nil {
			return Membership{}, usageErr
		}
		if command.MemberLimit > 0 && usage > command.MemberLimit {
			return Membership{}, ErrMemberLimitReached
		}
		membership, err = upsertAcceptedMembership(
			ctx,
			transaction,
			workspaceID,
			command.AccountID,
			command.Now,
		)
		if err != nil {
			return Membership{}, err
		}
	}
	if _, err = transaction.ExecContext(
		ctx,
		`UPDATE f04_invitations
		 SET status = 'accepted', accepted_by_account_id = $2, updated_at = $3
		 WHERE id = $1`,
		invitation.ID,
		command.AccountID,
		command.Now,
	); err != nil {
		return Membership{}, fmt.Errorf("accept invitation: %w", err)
	}
	if err = insertAudit(
		ctx,
		transaction,
		workspaceID,
		command.AccountID,
		command.AccountID,
		"invitation.accepted",
		command.Now,
	); err != nil {
		return Membership{}, err
	}
	if err = transaction.Commit(); err != nil {
		return Membership{}, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	return membership, nil
}

func (repository *PostgresRepository) RevokeInvitation(
	ctx context.Context,
	workspaceID, actorID, invitationID string,
	now time.Time,
) error {
	transaction, err := repository.begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err = lockWorkspace(ctx, transaction, workspaceID, true); err != nil {
		return err
	}
	if err = requireOwner(ctx, transaction, workspaceID, actorID); err != nil {
		return err
	}

	var status InvitationStatus
	var expiresAt time.Time
	err = transaction.QueryRowContext(
		ctx,
		`SELECT status, expires_at
		 FROM f04_invitations
		 WHERE id = $1 AND workspace_id = $2
		 FOR UPDATE`,
		invitationID,
		workspaceID,
	).Scan(&status, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock invitation: %w", err)
	}
	if status == InvitationRevoked {
		return transaction.Commit()
	}
	if status != InvitationPending {
		return ErrConflict
	}
	nextStatus := InvitationRevoked
	eventType := "invitation.revoked"
	resultErr := error(nil)
	if !expiresAt.After(now) {
		nextStatus = InvitationExpired
		eventType = "invitation.expired"
		resultErr = ErrInvitationExpired
	}
	if _, err = transaction.ExecContext(
		ctx,
		`UPDATE f04_invitations SET status = $2, updated_at = $3 WHERE id = $1`,
		invitationID,
		nextStatus,
		now,
	); err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	if err = insertAudit(ctx, transaction, workspaceID, actorID, invitationID, eventType, now); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit invitation revocation: %w", err)
	}
	return resultErr
}

func (repository *PostgresRepository) ChangeRole(
	ctx context.Context,
	workspaceID, actorID, accountID string,
	role Role,
	now time.Time,
) error {
	transaction, err := repository.ownerTransaction(ctx, workspaceID, actorID, true)
	if err != nil {
		return err
	}
	defer transaction.Rollback()

	membership, err := findActiveMembership(ctx, transaction, workspaceID, accountID, true)
	if err != nil {
		return err
	}
	if membership.Role == role {
		return transaction.Commit()
	}
	if membership.Role == RoleOwner && role == RoleMember {
		count, countErr := ownerCount(ctx, transaction, workspaceID)
		if countErr != nil {
			return countErr
		}
		if count <= 1 {
			return ErrLastOwner
		}
	}
	if _, err = transaction.ExecContext(
		ctx,
		`UPDATE f04_memberships
		 SET role = $3, updated_at = $4
		 WHERE workspace_id = $1 AND account_id = $2`,
		workspaceID,
		accountID,
		role,
		now,
	); err != nil {
		return fmt.Errorf("change membership role: %w", err)
	}
	eventType := "membership.promoted"
	if role == RoleMember {
		eventType = "membership.demoted"
	}
	if err = insertAudit(ctx, transaction, workspaceID, actorID, accountID, eventType, now); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit role change: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) TransferOwnership(
	ctx context.Context,
	workspaceID, actorID, targetAccountID string,
	now time.Time,
) error {
	transaction, err := repository.ownerTransaction(ctx, workspaceID, actorID, true)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	target, err := findActiveMembership(ctx, transaction, workspaceID, targetAccountID, true)
	if err != nil {
		return err
	}
	if target.Role != RoleMember {
		return ErrConflict
	}
	if _, err = transaction.ExecContext(
		ctx,
		`UPDATE f04_memberships
		 SET role = CASE account_id
		     WHEN $2 THEN 'member'::f04_workspace_role
		     WHEN $3 THEN 'owner'::f04_workspace_role
		 END,
		 updated_at = $4
		 WHERE workspace_id = $1 AND account_id IN ($2, $3)`,
		workspaceID,
		actorID,
		targetAccountID,
		now,
	); err != nil {
		return fmt.Errorf("transfer ownership: %w", err)
	}
	if err = insertAudit(
		ctx,
		transaction,
		workspaceID,
		actorID,
		targetAccountID,
		"ownership.transferred",
		now,
	); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit ownership transfer: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) RemoveMember(
	ctx context.Context,
	workspaceID, actorID, targetAccountID string,
	now time.Time,
) error {
	transaction, err := repository.ownerTransaction(ctx, workspaceID, actorID, true)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	target, err := findActiveMembership(ctx, transaction, workspaceID, targetAccountID, true)
	if err != nil {
		return err
	}
	if target.Role == RoleOwner {
		count, countErr := ownerCount(ctx, transaction, workspaceID)
		if countErr != nil {
			return countErr
		}
		if count <= 1 {
			return ErrLastOwner
		}
	}
	if err = removeMembership(
		ctx,
		transaction,
		workspaceID,
		targetAccountID,
		actorID,
		"membership.removed",
		now,
	); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit member removal: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) LeaveWorkspace(
	ctx context.Context,
	workspaceID, accountID string,
	now time.Time,
) error {
	transaction, err := repository.begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err = lockWorkspace(ctx, transaction, workspaceID, false); err != nil {
		return err
	}
	membership, err := findActiveMembership(ctx, transaction, workspaceID, accountID, true)
	if err != nil {
		return err
	}
	if membership.Role == RoleOwner {
		count, countErr := ownerCount(ctx, transaction, workspaceID)
		if countErr != nil {
			return countErr
		}
		if count <= 1 {
			return ErrLastOwner
		}
	}
	if err = removeMembership(
		ctx,
		transaction,
		workspaceID,
		accountID,
		accountID,
		"membership.left",
		now,
	); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit workspace leave: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) RequestDeletion(
	ctx context.Context,
	workspaceID, actorID string,
	now time.Time,
) error {
	transaction, err := repository.ownerTransaction(ctx, workspaceID, actorID, false)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	var status WorkspaceStatus
	if err = transaction.QueryRowContext(
		ctx,
		`SELECT status FROM f04_workspaces WHERE id = $1`,
		workspaceID,
	).Scan(&status); err != nil {
		return fmt.Errorf("read workspace status: %w", err)
	}
	if status == WorkspaceDeletionPending {
		return transaction.Commit()
	}
	if _, err = transaction.ExecContext(
		ctx,
		`UPDATE f04_workspaces
		 SET status = 'deletion_pending', updated_at = $2
		 WHERE id = $1`,
		workspaceID,
		now,
	); err != nil {
		return fmt.Errorf("request workspace deletion: %w", err)
	}
	if err = insertAudit(
		ctx,
		transaction,
		workspaceID,
		actorID,
		workspaceID,
		"workspace.deletion_requested",
		now,
	); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit workspace deletion request: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) begin(ctx context.Context) (*sql.Tx, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return nil, fmt.Errorf("begin workspace transaction: %w", err)
	}
	return transaction, nil
}

func (repository *PostgresRepository) ownerTransaction(
	ctx context.Context,
	workspaceID, actorID string,
	requireActiveWorkspace bool,
) (*sql.Tx, error) {
	transaction, err := repository.begin(ctx)
	if err != nil {
		return nil, err
	}
	if err = lockWorkspace(ctx, transaction, workspaceID, requireActiveWorkspace); err != nil {
		_ = transaction.Rollback()
		return nil, err
	}
	if err = requireOwner(ctx, transaction, workspaceID, actorID); err != nil {
		_ = transaction.Rollback()
		return nil, err
	}
	return transaction, nil
}

func lockWorkspace(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID string,
	requireActive bool,
) error {
	var status WorkspaceStatus
	err := transaction.QueryRowContext(
		ctx,
		`SELECT status FROM f04_workspaces WHERE id = $1 FOR UPDATE`,
		workspaceID,
	).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	if requireActive && status != WorkspaceActive {
		return ErrWorkspaceInactive
	}
	return nil
}

func requireOwner(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, actorID string,
) error {
	membership, err := findActiveMembership(ctx, transaction, workspaceID, actorID, false)
	if err != nil {
		return err
	}
	if membership.Role != RoleOwner {
		return ErrForbidden
	}
	return nil
}

func findActiveMembership(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, accountID string,
	lock bool,
) (Membership, error) {
	query := `SELECT workspace_id, account_id, role, status, created_at, updated_at
	          FROM f04_memberships
	          WHERE workspace_id = $1 AND account_id = $2 AND status = 'active'`
	if lock {
		query += " FOR UPDATE"
	}
	membership, err := scanMembership(transaction.QueryRowContext(
		ctx,
		query,
		workspaceID,
		accountID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, ErrForbidden
	}
	if err != nil {
		return Membership{}, fmt.Errorf("read active membership: %w", err)
	}
	return membership, nil
}

func upsertAcceptedMembership(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, accountID string,
	now time.Time,
) (Membership, error) {
	membership, err := scanMembership(transaction.QueryRowContext(
		ctx,
		`SELECT workspace_id, account_id, role, status, created_at, updated_at
		 FROM f04_memberships
		 WHERE workspace_id = $1 AND account_id = $2
		 FOR UPDATE`,
		workspaceID,
		accountID,
	))
	switch {
	case err == nil && membership.Status == MembershipActive:
		return membership, nil
	case err == nil:
		membership, err = scanMembership(transaction.QueryRowContext(
			ctx,
			`UPDATE f04_memberships
			 SET role = 'member', status = 'active', updated_at = $3
			 WHERE workspace_id = $1 AND account_id = $2
			 RETURNING workspace_id, account_id, role, status, created_at, updated_at`,
			workspaceID,
			accountID,
			now,
		))
	case errors.Is(err, sql.ErrNoRows):
		membership, err = scanMembership(transaction.QueryRowContext(
			ctx,
			`INSERT INTO f04_memberships (
				workspace_id, account_id, role, status, created_at, updated_at
			 ) VALUES ($1, $2, 'member', 'active', $3, $3)
			 RETURNING workspace_id, account_id, role, status, created_at, updated_at`,
			workspaceID,
			accountID,
			now,
		))
	}
	if err != nil {
		return Membership{}, fmt.Errorf("activate invited membership: %w", err)
	}
	return membership, nil
}

func existingActiveMembership(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, accountID string,
) (Membership, bool, error) {
	membership, err := scanMembership(transaction.QueryRowContext(
		ctx,
		`SELECT workspace_id, account_id, role, status, created_at, updated_at
		 FROM f04_memberships
		 WHERE workspace_id = $1 AND account_id = $2 AND status = 'active'
		 FOR UPDATE`,
		workspaceID,
		accountID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Membership{}, false, nil
	}
	if err != nil {
		return Membership{}, false, fmt.Errorf("read invited account membership: %w", err)
	}
	return membership, true, nil
}

func ownerCount(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID string,
) (int, error) {
	var count int
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT count(*)
		 FROM f04_memberships
		 WHERE workspace_id = $1 AND role = 'owner' AND status = 'active'`,
		workspaceID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count workspace Owners: %w", err)
	}
	return count, nil
}

func memberUsage(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID string,
	now time.Time,
) (int, error) {
	var usage int
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT
			(SELECT count(*) FROM f04_memberships
			 WHERE workspace_id = $1 AND status = 'active')
			+
			(SELECT count(*) FROM f04_invitations
			 WHERE workspace_id = $1 AND status = 'pending' AND expires_at > $2)`,
		workspaceID,
		now,
	).Scan(&usage); err != nil {
		return 0, fmt.Errorf("count member usage: %w", err)
	}
	return usage, nil
}

func expireInvitations(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID string,
	now time.Time,
) error {
	rows, err := transaction.QueryContext(
		ctx,
		`UPDATE f04_invitations
		 SET status = 'expired', updated_at = $2
		 WHERE workspace_id = $1 AND status = 'pending' AND expires_at <= $2
		 RETURNING id`,
		workspaceID,
		now,
	)
	if err != nil {
		return fmt.Errorf("expire invitations: %w", err)
	}
	defer rows.Close()
	var invitationIDs []string
	for rows.Next() {
		var invitationID string
		if err = rows.Scan(&invitationID); err != nil {
			return fmt.Errorf("scan expired invitation: %w", err)
		}
		invitationIDs = append(invitationIDs, invitationID)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate expired invitations: %w", err)
	}
	for _, invitationID := range invitationIDs {
		if err = insertAudit(
			ctx,
			transaction,
			workspaceID,
			"",
			invitationID,
			"invitation.expired",
			now,
		); err != nil {
			return err
		}
	}
	return nil
}

func removeMembership(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, accountID, actorID, eventType string,
	now time.Time,
) error {
	if _, err := transaction.ExecContext(
		ctx,
		`UPDATE f04_memberships
		 SET status = 'removed', updated_at = $3
		 WHERE workspace_id = $1 AND account_id = $2`,
		workspaceID,
		accountID,
		now,
	); err != nil {
		return fmt.Errorf("remove membership: %w", err)
	}
	return insertAudit(ctx, transaction, workspaceID, actorID, accountID, eventType, now)
}

func insertAudit(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, actorID, subjectID, eventType string,
	now time.Time,
) error {
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO f04_workspace_audit_events (
			workspace_id, actor_account_id, subject_id, event_type, outcome, occurred_at
		 ) VALUES ($1, NULLIF($2, ''), $3, $4, 'succeeded', $5)`,
		workspaceID,
		actorID,
		subjectID,
		eventType,
		now,
	); err != nil {
		return fmt.Errorf("record workspace audit event: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(destinations ...any) error
}

func scanWorkspace(row rowScanner) (Workspace, error) {
	var workspace Workspace
	err := row.Scan(
		&workspace.ID,
		&workspace.PersonalAccountID,
		&workspace.Name,
		&workspace.Status,
		&workspace.CreatedAt,
		&workspace.UpdatedAt,
	)
	return workspace, err
}

func scanMembership(row rowScanner) (Membership, error) {
	var membership Membership
	err := row.Scan(
		&membership.WorkspaceID,
		&membership.AccountID,
		&membership.Role,
		&membership.Status,
		&membership.CreatedAt,
		&membership.UpdatedAt,
	)
	return membership, err
}

func scanInvitation(row rowScanner) (Invitation, error) {
	var invitation Invitation
	err := row.Scan(
		&invitation.ID,
		&invitation.WorkspaceID,
		&invitation.Status,
		&invitation.ExpiresAt,
		&invitation.AcceptedByAccountID,
		&invitation.CreatedAt,
		&invitation.UpdatedAt,
	)
	return invitation, err
}

func scanInvitationWithEmail(row rowScanner) (Invitation, []byte, error) {
	var invitation Invitation
	var emailDigest []byte
	err := row.Scan(
		&invitation.ID,
		&invitation.WorkspaceID,
		&invitation.Status,
		&invitation.ExpiresAt,
		&invitation.AcceptedByAccountID,
		&invitation.CreatedAt,
		&invitation.UpdatedAt,
		&emailDigest,
	)
	return invitation, emailDigest, err
}
