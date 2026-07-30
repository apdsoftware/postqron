package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type onboardingDocument struct {
	ClientKey   string
	DocumentKey string
	Version     string
	DigestSHA   string
}

func (repository *PostgresRepository) AppSession(
	ctx context.Context,
	account AppSessionAccount,
) (AppSession, error) {
	workspaces, selectedID, err := repository.listAppWorkspaces(ctx, account.ID)
	if err != nil {
		return AppSession{}, err
	}
	return buildAppSession(account, workspaces, selectedID), nil
}

func (repository *PostgresRepository) CompleteOnboarding(
	ctx context.Context,
	command CompleteOnboardingCommand,
	now time.Time,
) (AppSession, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return AppSession{}, false, fmt.Errorf("begin onboarding transaction: %w", err)
	}
	defer transaction.Rollback()

	documents, err := loadCurrentOnboardingDocuments(ctx, transaction, now)
	if err != nil {
		return AppSession{}, false, err
	}

	var current AppWorkspace
	created := false
	switch command.Workspace.Mode {
	case "create":
		var workspace Workspace
		workspace, created, err = ensurePersonalWorkspaceTx(
			ctx,
			transaction,
			command.Account.ID,
			strings.TrimSpace(command.Workspace.Name),
			workspaceIDSeed(command.Account.ID),
			now,
		)
		if err != nil {
			return AppSession{}, false, err
		}
		current = AppWorkspace{
			ID:   workspace.ID,
			Name: workspace.Name,
			Role: RoleOwner,
		}
	case "select":
		current, err = appWorkspaceAccessTx(
			ctx,
			transaction,
			command.Account.ID,
			strings.TrimSpace(command.Workspace.ID),
			true,
		)
		if err != nil {
			return AppSession{}, false, err
		}
	default:
		return AppSession{}, false, fmt.Errorf("%w: unsupported onboarding workspace mode", ErrInvalidArgument)
	}

	if err = verifyOnboardingConsents(command.Consents, documents); err != nil {
		return AppSession{}, false, err
	}
	if err = upsertWorkspaceSelectionTx(
		ctx,
		transaction,
		command.Account.ID,
		current.ID,
		now,
	); err != nil {
		return AppSession{}, false, err
	}
	for _, receipt := range command.Consents {
		document := documents[receipt.DocumentKey]
		if err = appendOnboardingConsentTx(
			ctx,
			transaction,
			command.Account,
			current.ID,
			receipt,
			document,
			now,
		); err != nil {
			return AppSession{}, false, err
		}
	}

	workspaces, selectedID, err := listAppWorkspacesTx(ctx, transaction, command.Account.ID)
	if err != nil {
		return AppSession{}, false, err
	}
	session := buildAppSession(command.Account, workspaces, selectedID)
	if err = transaction.Commit(); err != nil {
		return AppSession{}, false, fmt.Errorf("commit onboarding transaction: %w", err)
	}
	return session, created, nil
}

func (repository *PostgresRepository) SelectWorkspace(
	ctx context.Context,
	account AppSessionAccount,
	workspaceID string,
	now time.Time,
) error {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return fmt.Errorf("begin workspace selection transaction: %w", err)
	}
	defer transaction.Rollback()

	if _, err = appWorkspaceAccessTx(ctx, transaction, account.ID, workspaceID, true); err != nil {
		return err
	}
	if err = upsertWorkspaceSelectionTx(ctx, transaction, account.ID, workspaceID, now); err != nil {
		return err
	}
	if err = transaction.Commit(); err != nil {
		return fmt.Errorf("commit workspace selection: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) CurrentWorkspace(
	ctx context.Context,
	accountID string,
) (Workspace, Role, error) {
	workspaces, selectedID, err := repository.listAppWorkspaces(ctx, accountID)
	if err != nil {
		return Workspace{}, "", err
	}
	current, ok := resolveCurrentWorkspace(workspaces, selectedID)
	if !ok {
		return Workspace{}, "", ErrNotFound
	}
	workspace, err := repository.GetWorkspace(ctx, current.ID, accountID)
	if err != nil {
		return Workspace{}, "", err
	}
	return workspace, current.Role, nil
}

func (repository *PostgresRepository) CurrentMembers(
	ctx context.Context,
	accountID string,
) ([]RuntimeMember, error) {
	workspaces, selectedID, err := repository.listAppWorkspaces(ctx, accountID)
	if err != nil {
		return nil, err
	}
	current, ok := resolveCurrentWorkspace(workspaces, selectedID)
	if !ok {
		return nil, ErrNotFound
	}
	rows, err := repository.database.QueryContext(
		ctx,
		`SELECT membership.account_id,
		        COALESCE(account.normalized_email, ''),
		        membership.role,
		        membership.status,
		        membership.created_at,
		        membership.updated_at
		 FROM f04_memberships membership
		 LEFT JOIN auth_accounts account
		   ON account.id = membership.account_id
		 WHERE membership.workspace_id = $1
		   AND membership.status = 'active'
		   AND EXISTS (
		       SELECT 1
		       FROM f04_memberships actor
		       JOIN f04_workspaces workspace
		         ON workspace.id = actor.workspace_id
		       WHERE actor.workspace_id = $1
		         AND actor.account_id = $2
		         AND actor.status = 'active'
		         AND workspace.status = 'active'
		   )
		 ORDER BY membership.account_id`,
		current.ID,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list current workspace members: %w", err)
	}
	defer rows.Close()

	var members []RuntimeMember
	for rows.Next() {
		var member RuntimeMember
		if err = rows.Scan(
			&member.AccountID,
			&member.Email,
			&member.Role,
			&member.Status,
			&member.CreatedAt,
			&member.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan current workspace member: %w", err)
		}
		member.ID = member.AccountID
		members = append(members, member)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current workspace members: %w", err)
	}
	if len(members) == 0 {
		return nil, ErrForbidden
	}
	return members, nil
}

func (repository *PostgresRepository) ConsumeOnboardingRequired(
	ctx context.Context,
	event OnboardingRequiredEvent,
	now time.Time,
) (Workspace, bool, error) {
	transaction, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
	})
	if err != nil {
		return Workspace{}, false, fmt.Errorf("begin onboarding event transaction: %w", err)
	}
	defer transaction.Rollback()

	workspace, created, err := ensurePersonalWorkspaceTx(
		ctx,
		transaction,
		event.AccountID,
		defaultPersonalWorkspaceName(event.DisplayName, event.Email),
		workspaceIDSeed(event.AccountID),
		now,
	)
	if err != nil {
		return Workspace{}, false, err
	}
	if selectedID, found, selectionErr := selectedWorkspaceIDTx(ctx, transaction, event.AccountID); selectionErr != nil {
		return Workspace{}, false, selectionErr
	} else if !found || strings.TrimSpace(selectedID) == "" {
		if err = upsertWorkspaceSelectionTx(ctx, transaction, event.AccountID, workspace.ID, now); err != nil {
			return Workspace{}, false, err
		}
	}
	if err = transaction.Commit(); err != nil {
		return Workspace{}, false, fmt.Errorf("commit onboarding event transaction: %w", err)
	}
	return workspace, created, nil
}

func (repository *PostgresRepository) listAppWorkspaces(
	ctx context.Context,
	accountID string,
) ([]AppWorkspace, string, error) {
	workspaces, err := listAppWorkspacesWithQuery(ctx, repository.database, accountID)
	if err != nil {
		return nil, "", err
	}
	selectedID, _, err := selectedWorkspaceID(ctx, repository.database, accountID)
	if err != nil {
		return nil, "", err
	}
	return workspaces, selectedID, nil
}

func listAppWorkspacesTx(
	ctx context.Context,
	transaction *sql.Tx,
	accountID string,
) ([]AppWorkspace, string, error) {
	workspaces, err := listAppWorkspacesWithQuery(ctx, transaction, accountID)
	if err != nil {
		return nil, "", err
	}
	selectedID, _, err := selectedWorkspaceIDTx(ctx, transaction, accountID)
	if err != nil {
		return nil, "", err
	}
	return workspaces, selectedID, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func listAppWorkspacesWithQuery(
	ctx context.Context,
	query queryer,
	accountID string,
) ([]AppWorkspace, error) {
	rows, err := query.QueryContext(
		ctx,
		`SELECT workspace.id, workspace.name, membership.role
		 FROM f04_memberships membership
		 JOIN f04_workspaces workspace
		   ON workspace.id = membership.workspace_id
		 WHERE membership.account_id = $1
		   AND membership.status = 'active'
		   AND workspace.status = 'active'
		 ORDER BY workspace.created_at, workspace.id`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list app workspaces: %w", err)
	}
	defer rows.Close()

	var workspaces []AppWorkspace
	for rows.Next() {
		var workspace AppWorkspace
		if err = rows.Scan(&workspace.ID, &workspace.Name, &workspace.Role); err != nil {
			return nil, fmt.Errorf("scan app workspace: %w", err)
		}
		workspaces = append(workspaces, workspace)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate app workspaces: %w", err)
	}
	return workspaces, nil
}

func selectedWorkspaceID(
	ctx context.Context,
	query queryer,
	accountID string,
) (string, bool, error) {
	row := query.QueryRowContext(
		ctx,
		`SELECT workspace_id
		 FROM f04_workspace_selections
		 WHERE account_id = $1`,
		accountID,
	)
	var workspaceID string
	err := row.Scan(&workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read selected workspace: %w", err)
	}
	return workspaceID, true, nil
}

func selectedWorkspaceIDTx(
	ctx context.Context,
	transaction *sql.Tx,
	accountID string,
) (string, bool, error) {
	return selectedWorkspaceID(ctx, transaction, accountID)
}

func appWorkspaceAccessTx(
	ctx context.Context,
	transaction *sql.Tx,
	accountID, workspaceID string,
	lock bool,
) (AppWorkspace, error) {
	query := `SELECT workspace.id, workspace.name, membership.role
		FROM f04_memberships membership
		JOIN f04_workspaces workspace
		  ON workspace.id = membership.workspace_id
		WHERE membership.account_id = $1
		  AND membership.workspace_id = $2
		  AND membership.status = 'active'
		  AND workspace.status = 'active'`
	if lock {
		query += " FOR UPDATE OF workspace, membership"
	}
	var workspace AppWorkspace
	err := transaction.QueryRowContext(ctx, query, accountID, workspaceID).Scan(
		&workspace.ID,
		&workspace.Name,
		&workspace.Role,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AppWorkspace{}, ErrForbidden
	}
	if err != nil {
		return AppWorkspace{}, fmt.Errorf("read selected workspace access: %w", err)
	}
	return workspace, nil
}

func ensurePersonalWorkspaceTx(
	ctx context.Context,
	transaction *sql.Tx,
	accountID, name, workspaceID string,
	now time.Time,
) (Workspace, bool, error) {
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
		return Workspace{}, false, fmt.Errorf("ensure personal workspace in runtime onboarding: %w", err)
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
			return Workspace{}, false, fmt.Errorf("create onboarding owner membership: %w", err)
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
	return workspace, created, nil
}

func loadCurrentOnboardingDocuments(
	ctx context.Context,
	query queryer,
	now time.Time,
) (map[string]onboardingDocument, error) {
	rows, err := query.QueryContext(
		ctx,
		`SELECT document_key, version, digest_sha256
		 FROM compliance_legal_documents
		 WHERE document_key IN ('terms_it', 'privacy_it')
		   AND jurisdiction = 'IT'
		   AND locale = 'it-IT'
		   AND content_status = 'approved'
		   AND published_at IS NOT NULL
		   AND effective_at <= $1
		   AND (superseded_at IS NULL OR superseded_at > $1)`,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("read onboarding legal documents: %w", err)
	}
	defer rows.Close()

	documents := make(map[string]onboardingDocument, 2)
	for rows.Next() {
		var document onboardingDocument
		if err = rows.Scan(&document.DocumentKey, &document.Version, &document.DigestSHA); err != nil {
			return nil, fmt.Errorf("scan onboarding legal document: %w", err)
		}
		switch document.DocumentKey {
		case "terms_it":
			document.ClientKey = "terms"
		case "privacy_it":
			document.ClientKey = "privacy"
		default:
			continue
		}
		documents[document.ClientKey] = document
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate onboarding legal documents: %w", err)
	}
	if len(documents) != 2 {
		return nil, ErrRuntimeUnavailable
	}
	return documents, nil
}

func verifyOnboardingConsents(
	receipts []OnboardingConsentReceipt,
	documents map[string]onboardingDocument,
) error {
	for _, receipt := range receipts {
		document, ok := documents[receipt.DocumentKey]
		if !ok ||
			receipt.Version != document.Version ||
			!strings.EqualFold(receipt.DigestSHA256, document.DigestSHA) {
			return ErrConsentOutdated
		}
	}
	return nil
}

func appendOnboardingConsentTx(
	ctx context.Context,
	transaction *sql.Tx,
	account AppSessionAccount,
	workspaceID string,
	receipt OnboardingConsentReceipt,
	document onboardingDocument,
	now time.Time,
) error {
	eventID := fmt.Sprintf(
		"f04-onboarding:%s:%s:%s:%s",
		account.ID,
		workspaceID,
		document.DocumentKey,
		document.Version,
	)
	idempotencyKey := fmt.Sprintf(
		"f04-onboarding:%s:%s:%s:%s:%s",
		account.ID,
		workspaceID,
		document.DocumentKey,
		document.Version,
		receipt.Purpose,
	)
	correlationID := fmt.Sprintf("f04-onboarding:%s:%s", account.ID, workspaceID)
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO compliance_consent_events (
			event_id, subject_kind, subject_id, workspace_id, document_key,
			document_version, document_digest_sha256, purpose, action,
			occurred_at, locale, contractual_country, surface, correlation_id,
			idempotency_key, control_text_version
		 ) VALUES (
			$1, 'authenticated_user', $2, $3, $4, $5, $6, $7, 'accepted',
			$8, 'it-IT', 'IT', $9, $10, $11, $12
		 )
		 ON CONFLICT (idempotency_key) DO NOTHING`,
		eventID,
		account.ID,
		workspaceID,
		document.DocumentKey,
		document.Version,
		document.DigestSHA,
		receipt.Purpose,
		now,
		receipt.Surface,
		correlationID,
		idempotencyKey,
		receipt.ControlTextID,
	); err != nil {
		return fmt.Errorf("record onboarding consent: %w", err)
	}
	return nil
}

func upsertWorkspaceSelectionTx(
	ctx context.Context,
	transaction *sql.Tx,
	accountID, workspaceID string,
	now time.Time,
) error {
	if _, err := transaction.ExecContext(
		ctx,
		`INSERT INTO f04_workspace_selections (
			account_id, workspace_id, selected_at, updated_at
		 ) VALUES ($1, $2, $3, $3)
		 ON CONFLICT (account_id) DO UPDATE
		    SET workspace_id = EXCLUDED.workspace_id,
		        selected_at = EXCLUDED.selected_at,
		        updated_at = EXCLUDED.updated_at`,
		accountID,
		workspaceID,
		now,
	); err != nil {
		return fmt.Errorf("persist workspace selection: %w", err)
	}
	return nil
}

func buildAppSession(
	account AppSessionAccount,
	workspaces []AppWorkspace,
	selectedID string,
) AppSession {
	session := AppSession{
		Workspaces: append([]AppWorkspace(nil), workspaces...),
	}
	session.Account.ID = account.ID
	session.Account.DisplayName = account.DisplayName
	session.Account.Email = account.Email
	session.Account.Locale = account.Locale
	session.OnboardingRequired = len(workspaces) == 0
	if current, ok := resolveCurrentWorkspace(workspaces, selectedID); ok {
		session.CurrentWorkspace = &current
	}
	return session
}

func resolveCurrentWorkspace(
	workspaces []AppWorkspace,
	selectedID string,
) (AppWorkspace, bool) {
	for _, workspace := range workspaces {
		if workspace.ID == selectedID {
			return workspace, true
		}
	}
	if len(workspaces) == 0 {
		return AppWorkspace{}, false
	}
	return workspaces[0], true
}

func defaultPersonalWorkspaceName(displayName, email string) string {
	if strings.TrimSpace(displayName) != "" {
		return strings.TrimSpace(displayName) + "'s workspace"
	}
	localPart, _, found := strings.Cut(strings.TrimSpace(email), "@")
	if found && strings.TrimSpace(localPart) != "" {
		return strings.TrimSpace(localPart) + "'s workspace"
	}
	return "Personal workspace"
}

func workspaceIDSeed(accountID string) string {
	return fmt.Sprintf("personal-%s-%d", accountID, time.Now().UnixNano())
}
