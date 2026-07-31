package composer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type PostgresRepository struct {
	database *sql.DB
	media    *PostgresMediaStore
}

func NewPostgresRepository(database *sql.DB) (*PostgresRepository, error) {
	if database == nil {
		return nil, fmt.Errorf("%w: postgres database is required", ErrInvalidArgument)
	}
	return &PostgresRepository{database: database}, nil
}

func (repository *PostgresRepository) BindMediaStore(media *PostgresMediaStore) {
	repository.media = media
}

func (repository *PostgresRepository) Create(
	ctx context.Context,
	draft Draft,
) (Draft, error) {
	draft.Content = contentForPostgres(draft.Content)
	content, err := json.Marshal(draft.Content)
	if err != nil {
		return Draft{}, fmt.Errorf("encode draft content: %w", err)
	}
	if repository.media != nil {
		if err := repository.media.ReconcileLifecycle(ctx, draft.WorkspaceID); err != nil {
			return Draft{}, err
		}
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, fmt.Errorf("begin composer create: %w", err)
	}
	defer transaction.Rollback()
	mutation := mediaLinkMutation{}
	if repository.media != nil {
		mutation, err = repository.media.prepareDraftMediaTx(
			ctx,
			transaction,
			draft.WorkspaceID,
			draft.ID,
			nil,
			draftMediaIDs(draft.Content),
			draft.UpdatedAt,
		)
		if err != nil {
			return Draft{}, repository.abortMediaMutation(
				ctx,
				transaction,
				mutation,
				err,
			)
		}
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO f06_composer_drafts (
			id, workspace_id, created_by_account_id,
			content, revision, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		draft.ID,
		draft.WorkspaceID,
		draft.CreatedBy,
		content,
		draft.Revision,
		draft.CreatedAt,
		draft.UpdatedAt,
	)
	if err != nil {
		err = repository.abortMediaMutation(ctx, transaction, mutation, err)
		if isUniqueViolation(err) {
			return Draft{}, ErrConflict
		}
		return Draft{}, fmt.Errorf("create composer draft: %w", err)
	}
	if err := insertRevision(ctx, transaction, draft, "", content); err != nil {
		return Draft{}, repository.abortMediaMutation(
			ctx,
			transaction,
			mutation,
			err,
		)
	}
	if repository.media != nil {
		if err := repository.media.applyDraftMediaTx(
			ctx,
			transaction,
			mutation,
		); err != nil {
			return Draft{}, repository.abortMediaMutation(
				ctx,
				transaction,
				mutation,
				err,
			)
		}
	}
	if err := transaction.Commit(); err != nil {
		return Draft{}, errors.Join(
			fmt.Errorf("commit composer create: %w", err),
			repository.compensateMedia(ctx, mutation),
		)
	}
	if repository.media != nil {
		repository.media.finalizeRemoved(ctx, mutation)
	}
	return cloneDraft(draft), nil
}

func (repository *PostgresRepository) Get(
	ctx context.Context,
	workspaceID, draftID string,
) (Draft, error) {
	return scanPostgresDraft(repository.database.QueryRowContext(ctx, `
		SELECT id, workspace_id, created_by_account_id,
		       content, revision, created_at, updated_at
		  FROM f06_composer_drafts
		 WHERE workspace_id = $1 AND id = $2`,
		workspaceID,
		draftID,
	))
}

func (repository *PostgresRepository) List(
	ctx context.Context,
	workspaceID string,
) ([]Draft, error) {
	rows, err := repository.database.QueryContext(ctx, `
		SELECT id, workspace_id, created_by_account_id,
		       content, revision, created_at, updated_at
		  FROM f06_composer_drafts
		 WHERE workspace_id = $1
		 ORDER BY updated_at DESC, id`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list composer drafts: %w", err)
	}
	defer rows.Close()
	drafts := make([]Draft, 0)
	for rows.Next() {
		draft, scanErr := scanPostgresDraft(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		drafts = append(drafts, draft)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate composer drafts: %w", err)
	}
	return drafts, nil
}

func (repository *PostgresRepository) Update(
	ctx context.Context,
	draft Draft,
	expectedRevision int64,
	autosaveKey string,
) (Draft, error) {
	draft.Content = contentForPostgres(draft.Content)
	content, err := json.Marshal(draft.Content)
	if err != nil {
		return Draft{}, fmt.Errorf("encode draft content: %w", err)
	}
	if repository.media != nil {
		if err := repository.media.ReconcileLifecycle(ctx, draft.WorkspaceID); err != nil {
			return Draft{}, err
		}
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return Draft{}, fmt.Errorf("begin composer update: %w", err)
	}
	defer transaction.Rollback()

	autosaveKey = strings.TrimSpace(autosaveKey)
	current, err := scanPostgresDraft(transaction.QueryRowContext(ctx, `
		SELECT id, workspace_id, created_by_account_id,
		       content, revision, created_at, updated_at
		  FROM f06_composer_drafts
		 WHERE workspace_id = $1 AND id = $2
		 FOR UPDATE`,
		draft.WorkspaceID,
		draft.ID,
	))
	if err != nil {
		return Draft{}, err
	}
	if autosaveKey != "" {
		replayed, found, replayErr := replayAutosave(
			ctx,
			transaction,
			draft.WorkspaceID,
			draft.ID,
			autosaveKey,
		)
		if replayErr != nil {
			return Draft{}, replayErr
		}
		if found {
			return replayed, nil
		}
	}
	if current.Revision != expectedRevision {
		return Draft{}, ErrConflict
	}
	mutation := mediaLinkMutation{}
	if repository.media != nil {
		mutation, err = repository.media.prepareDraftMediaTx(
			ctx,
			transaction,
			draft.WorkspaceID,
			draft.ID,
			draftMediaIDs(current.Content),
			draftMediaIDs(draft.Content),
			draft.UpdatedAt,
		)
		if err != nil {
			return Draft{}, repository.abortMediaMutation(
				ctx,
				transaction,
				mutation,
				err,
			)
		}
	}

	row := transaction.QueryRowContext(ctx, `
		UPDATE f06_composer_drafts
		   SET content = $4,
		       revision = revision + 1,
		       updated_at = $5
		 WHERE workspace_id = $1
		   AND id = $2
		   AND revision = $3
		RETURNING id, workspace_id, created_by_account_id,
		          content, revision, created_at, updated_at`,
		draft.WorkspaceID,
		draft.ID,
		expectedRevision,
		content,
		draft.UpdatedAt,
	)
	updated, err := scanPostgresDraft(row)
	if errors.Is(err, ErrNotFound) {
		err = classifyMissTx(ctx, transaction, draft.WorkspaceID, draft.ID)
	}
	if err != nil {
		return Draft{}, repository.abortMediaMutation(
			ctx,
			transaction,
			mutation,
			err,
		)
	}
	if err := insertRevision(ctx, transaction, updated, autosaveKey, content); err != nil {
		return Draft{}, repository.abortMediaMutation(
			ctx,
			transaction,
			mutation,
			err,
		)
	}
	if repository.media != nil {
		if err := repository.media.applyDraftMediaTx(
			ctx,
			transaction,
			mutation,
		); err != nil {
			return Draft{}, repository.abortMediaMutation(
				ctx,
				transaction,
				mutation,
				err,
			)
		}
	}
	if err := transaction.Commit(); err != nil {
		return Draft{}, errors.Join(
			fmt.Errorf("commit composer update: %w", err),
			repository.compensateMedia(ctx, mutation),
		)
	}
	if repository.media != nil {
		repository.media.finalizeRemoved(ctx, mutation)
	}
	return updated, nil
}

func (repository *PostgresRepository) Delete(
	ctx context.Context,
	workspaceID, draftID string,
	expectedRevision int64,
) error {
	if repository.media != nil {
		return repository.deleteWithMedia(
			ctx,
			workspaceID,
			draftID,
			expectedRevision,
		)
	}
	tag, err := repository.database.ExecContext(ctx, `
		DELETE FROM f06_composer_drafts
		 WHERE workspace_id = $1 AND id = $2 AND revision = $3`,
		workspaceID,
		draftID,
		expectedRevision,
	)
	if err != nil {
		return fmt.Errorf("delete composer draft: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected == 1 {
		return nil
	}
	return repository.classifyMiss(ctx, workspaceID, draftID)
}

func (repository *PostgresRepository) deleteWithMedia(
	ctx context.Context,
	workspaceID, draftID string,
	expectedRevision int64,
) error {
	if err := repository.media.ReconcileLifecycle(ctx, workspaceID); err != nil {
		return err
	}
	transaction, err := repository.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin composer delete: %w", err)
	}
	defer transaction.Rollback()
	current, err := scanPostgresDraft(transaction.QueryRowContext(ctx, `
		SELECT id, workspace_id, created_by_account_id,
		       content, revision, created_at, updated_at
		  FROM f06_composer_drafts
		 WHERE workspace_id = $1 AND id = $2
		 FOR UPDATE`,
		workspaceID,
		draftID,
	))
	if err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return ErrConflict
	}
	mutation, err := repository.media.prepareDraftMediaTx(
		ctx,
		transaction,
		workspaceID,
		draftID,
		draftMediaIDs(current.Content),
		nil,
		repository.media.clock().UTC(),
	)
	if err != nil {
		return repository.abortMediaMutation(ctx, transaction, mutation, err)
	}
	if err := repository.media.applyDraftMediaTx(
		ctx,
		transaction,
		mutation,
	); err != nil {
		return repository.abortMediaMutation(ctx, transaction, mutation, err)
	}
	tag, err := transaction.ExecContext(ctx, `
		DELETE FROM f06_composer_drafts
		 WHERE workspace_id = $1 AND id = $2 AND revision = $3`,
		workspaceID,
		draftID,
		expectedRevision,
	)
	if err != nil {
		return repository.abortMediaMutation(ctx, transaction, mutation, err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return repository.abortMediaMutation(ctx, transaction, mutation, ErrConflict)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit composer delete: %w", err)
	}
	repository.media.finalizeRemoved(ctx, mutation)
	return nil
}

func (repository *PostgresRepository) ListRevisions(
	ctx context.Context,
	workspaceID, draftID string,
) ([]DraftRevision, error) {
	rows, err := repository.database.QueryContext(ctx, `
		SELECT revision, content, COALESCE(autosave_key, ''), saved_at
		  FROM f06_composer_draft_revisions
		 WHERE workspace_id = $1 AND draft_id = $2
		 ORDER BY revision DESC`,
		workspaceID,
		draftID,
	)
	if err != nil {
		return nil, fmt.Errorf("list composer revisions: %w", err)
	}
	defer rows.Close()
	revisions := make([]DraftRevision, 0)
	for rows.Next() {
		var revision DraftRevision
		var content []byte
		revision.DraftID = draftID
		if err := rows.Scan(
			&revision.Revision,
			&content,
			&revision.AutosaveKey,
			&revision.SavedAt,
		); err != nil {
			return nil, fmt.Errorf("scan composer revision: %w", err)
		}
		if err := json.Unmarshal(content, &revision.Content); err != nil {
			return nil, fmt.Errorf("decode composer revision: %w", err)
		}
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate composer revisions: %w", err)
	}
	if len(revisions) == 0 {
		return nil, repository.classifyMiss(ctx, workspaceID, draftID)
	}
	return revisions, nil
}

func (repository *PostgresRepository) GetRevision(
	ctx context.Context,
	workspaceID, draftID string,
	revision int64,
) (DraftRevision, error) {
	var result DraftRevision
	var content []byte
	result.DraftID = draftID
	err := repository.database.QueryRowContext(ctx, `
		SELECT revision, content, COALESCE(autosave_key, ''), saved_at
		  FROM f06_composer_draft_revisions
		 WHERE workspace_id = $1
		   AND draft_id = $2
		   AND revision = $3`,
		workspaceID,
		draftID,
		revision,
	).Scan(
		&result.Revision,
		&content,
		&result.AutosaveKey,
		&result.SavedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return DraftRevision{}, classifyRevisionMissQuery(
			ctx,
			repository.database,
			workspaceID,
			draftID,
		)
	}
	if err != nil {
		return DraftRevision{}, fmt.Errorf("read composer revision: %w", err)
	}
	if err := json.Unmarshal(content, &result.Content); err != nil {
		return DraftRevision{}, fmt.Errorf("decode composer revision: %w", err)
	}
	return result, nil
}

func (repository *PostgresRepository) ReserveDuplicateOperation(
	ctx context.Context,
	operation duplicateOperation,
	now time.Time,
) (duplicateOperation, bool, error) {
	inserted, err := repository.database.ExecContext(ctx, `
		INSERT INTO f06_composer_duplicate_operations (
			workspace_id, idempotency_key, source_draft_id, source_revision,
			created_by_account_id, status, lease_generation, locked_until, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'pending', 1, $6, $7, $7)
		ON CONFLICT (workspace_id, idempotency_key) DO NOTHING`,
		operation.WorkspaceID,
		operation.IdempotencyKey,
		operation.SourceDraftID,
		operation.SourceRevision,
		operation.CreatedByAccount,
		now.UTC().Add(duplicateOperationLease),
		now.UTC(),
	)
	if err != nil {
		return duplicateOperation{}, false, fmt.Errorf("reserve composer duplicate operation: %w", err)
	}
	owned := false
	if affected, _ := inserted.RowsAffected(); affected == 1 {
		owned = true
	}
	var stored duplicateOperation
	var lockedUntil sql.NullTime
	err = repository.database.QueryRowContext(ctx, `
		SELECT workspace_id, idempotency_key, source_draft_id, source_revision,
		       created_by_account_id, status, COALESCE(clone_draft_id, ''),
		       COALESCE(clone_draft_revision, 0), lease_generation,
		       locked_until,
		       created_at, updated_at
		  FROM f06_composer_duplicate_operations
		 WHERE workspace_id = $1
		   AND idempotency_key = $2`,
		operation.WorkspaceID,
		operation.IdempotencyKey,
	).Scan(
		&stored.WorkspaceID,
		&stored.IdempotencyKey,
		&stored.SourceDraftID,
		&stored.SourceRevision,
		&stored.CreatedByAccount,
		&stored.Status,
		&stored.CloneDraftID,
		&stored.CloneDraftRevision,
		&stored.LeaseGeneration,
		&lockedUntil,
		&stored.CreatedAt,
		&stored.UpdatedAt,
	)
	if err != nil {
		return duplicateOperation{}, false, fmt.Errorf("read composer duplicate operation: %w", err)
	}
	if stored.SourceDraftID != operation.SourceDraftID ||
		stored.SourceRevision != operation.SourceRevision ||
		stored.CreatedByAccount != operation.CreatedByAccount {
		return duplicateOperation{}, false, ErrConflict
	}
	if lockedUntil.Valid {
		stored.LockedUntil = lockedUntil.Time.UTC()
	}
	if stored.Status == duplicateOperationCompleted {
		return stored, true, nil
	}
	if owned {
		return stored, false, nil
	}
	tag, err := repository.database.ExecContext(ctx, `
		UPDATE f06_composer_duplicate_operations
		   SET lease_generation = lease_generation + 1,
		       locked_until = $3,
		       updated_at = $4
		 WHERE workspace_id = $1
		   AND idempotency_key = $2
		   AND status = 'pending'
		   AND locked_until <= $4`,
		operation.WorkspaceID,
		operation.IdempotencyKey,
		now.UTC().Add(duplicateOperationLease),
		now.UTC(),
	)
	if err != nil {
		return duplicateOperation{}, false, fmt.Errorf("claim composer duplicate operation: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return duplicateOperation{}, false, ErrConflict
	}
	stored.LeaseGeneration++
	stored.LockedUntil = now.UTC().Add(duplicateOperationLease)
	stored.UpdatedAt = now.UTC()
	return stored, false, nil
}

func (repository *PostgresRepository) CompleteDuplicateOperation(
	ctx context.Context,
	operation duplicateOperation,
	cloneDraftID string,
	cloneDraftRevision int64,
	completedAt time.Time,
) error {
	tag, err := repository.database.ExecContext(ctx, `
		UPDATE f06_composer_duplicate_operations
		   SET status = 'completed',
		       clone_draft_id = $3,
		       clone_draft_revision = $4,
		       locked_until = NULL,
		       updated_at = $5
		 WHERE workspace_id = $1
		   AND idempotency_key = $2
		   AND status = 'pending'
		   AND lease_generation = $6`,
		operation.WorkspaceID,
		operation.IdempotencyKey,
		cloneDraftID,
		cloneDraftRevision,
		completedAt.UTC(),
		operation.LeaseGeneration,
	)
	if err != nil {
		return fmt.Errorf("complete composer duplicate operation: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return ErrConflict
	}
	return nil
}

func (repository *PostgresRepository) AbandonDuplicateOperation(
	ctx context.Context,
	operation duplicateOperation,
) (bool, error) {
	tag, err := repository.database.ExecContext(ctx, `
		DELETE FROM f06_composer_duplicate_operations
		 WHERE workspace_id = $1
		   AND idempotency_key = $2
		   AND status = 'pending'
		   AND lease_generation = $3`,
		operation.WorkspaceID,
		operation.IdempotencyKey,
		operation.LeaseGeneration,
	)
	if err != nil {
		return false, fmt.Errorf("abandon composer duplicate operation: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return false, nil
	}
	return true, nil
}

func (repository *PostgresRepository) ResetDanglingCompletedDuplicateOperation(
	ctx context.Context,
	operation duplicateOperation,
	now time.Time,
) (duplicateOperation, bool, error) {
	tag, err := repository.database.ExecContext(ctx, `
		UPDATE f06_composer_duplicate_operations
		   SET status = 'pending',
		       clone_draft_id = NULL,
		       clone_draft_revision = NULL,
		       lease_generation = lease_generation + 1,
		       locked_until = $3,
		       updated_at = $4
		 WHERE workspace_id = $1
		   AND idempotency_key = $2
		   AND status = 'completed'
		   AND lease_generation = $5
		   AND clone_draft_id = $6
		   AND clone_draft_revision = $7`,
		operation.WorkspaceID,
		operation.IdempotencyKey,
		now.UTC().Add(duplicateOperationLease),
		now.UTC(),
		operation.LeaseGeneration,
		operation.CloneDraftID,
		operation.CloneDraftRevision,
	)
	if err != nil {
		return duplicateOperation{}, false, fmt.Errorf("reset composer dangling duplicate operation: %w", err)
	}
	if affected, _ := tag.RowsAffected(); affected != 1 {
		return duplicateOperation{}, false, nil
	}
	operation.Status = duplicateOperationPending
	operation.CloneDraftID = ""
	operation.CloneDraftRevision = 0
	operation.LeaseGeneration++
	operation.LockedUntil = now.UTC().Add(duplicateOperationLease)
	operation.UpdatedAt = now.UTC()
	return operation, true, nil
}

func insertRevision(
	ctx context.Context,
	transaction *sql.Tx,
	draft Draft,
	autosaveKey string,
	content []byte,
) error {
	var key any
	if strings.TrimSpace(autosaveKey) != "" {
		key = strings.TrimSpace(autosaveKey)
	}
	_, err := transaction.ExecContext(ctx, `
		INSERT INTO f06_composer_draft_revisions (
			draft_id, workspace_id, revision, content,
			autosave_key, saved_at
		) VALUES ($1, $2, $3, $4, $5, $6)`,
		draft.ID,
		draft.WorkspaceID,
		draft.Revision,
		content,
		key,
		draft.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("record composer revision: %w", err)
	}
	return nil
}

func replayAutosave(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, draftID, autosaveKey string,
) (Draft, bool, error) {
	var draft Draft
	var content []byte
	err := transaction.QueryRowContext(ctx, `
		SELECT draft.id, draft.workspace_id, draft.created_by_account_id,
		       revision.content, revision.revision,
		       draft.created_at, revision.saved_at
		  FROM f06_composer_draft_revisions revision
		  JOIN f06_composer_drafts draft ON draft.id = revision.draft_id
		 WHERE revision.workspace_id = $1
		   AND revision.draft_id = $2
		   AND revision.autosave_key = $3`,
		workspaceID,
		draftID,
		autosaveKey,
	).Scan(
		&draft.ID,
		&draft.WorkspaceID,
		&draft.CreatedBy,
		&content,
		&draft.Revision,
		&draft.CreatedAt,
		&draft.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, fmt.Errorf("replay composer autosave: %w", err)
	}
	if err := json.Unmarshal(content, &draft.Content); err != nil {
		return Draft{}, false, fmt.Errorf("decode composer autosave: %w", err)
	}
	return draft, true, nil
}

func contentForPostgres(content DraftContent) DraftContent {
	content = cloneContent(content)
	if content.Media == nil {
		content.Media = []Media{}
	}
	if content.Thread == nil {
		content.Thread = []ThreadItem{}
	}
	if content.Destinations == nil {
		content.Destinations = []Destination{}
	}
	return content
}

func (repository *PostgresRepository) classifyMiss(
	ctx context.Context,
	workspaceID, draftID string,
) error {
	return classifyMissQuery(ctx, repository.database, workspaceID, draftID)
}

func classifyMissTx(
	ctx context.Context,
	transaction *sql.Tx,
	workspaceID, draftID string,
) error {
	return classifyMissQuery(ctx, transaction, workspaceID, draftID)
}

type existsQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func classifyMissQuery(
	ctx context.Context,
	query existsQuery,
	workspaceID, draftID string,
) error {
	var exists bool
	err := query.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM f06_composer_drafts
			 WHERE workspace_id = $1 AND id = $2
		)`,
		workspaceID,
		draftID,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("classify composer draft miss: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

func classifyRevisionMissQuery(
	ctx context.Context,
	query existsQuery,
	workspaceID, draftID string,
) error {
	var exists bool
	err := query.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM f06_composer_drafts
			 WHERE workspace_id = $1 AND id = $2
		)`,
		workspaceID,
		draftID,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("classify composer revision miss: %w", err)
	}
	if exists {
		return ErrConflict
	}
	return ErrNotFound
}

type postgresDraftRow interface {
	Scan(...any) error
}

func scanPostgresDraft(row postgresDraftRow) (Draft, error) {
	var draft Draft
	var content []byte
	err := row.Scan(
		&draft.ID,
		&draft.WorkspaceID,
		&draft.CreatedBy,
		&content,
		&draft.Revision,
		&draft.CreatedAt,
		&draft.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("scan composer draft: %w", err)
	}
	if err := json.Unmarshal(content, &draft.Content); err != nil {
		return Draft{}, fmt.Errorf("decode composer draft: %w", err)
	}
	return draft, nil
}

func isUniqueViolation(err error) bool {
	var postgresError interface{ SQLState() string }
	return errors.As(err, &postgresError) && postgresError.SQLState() == "23505"
}

func (repository *PostgresRepository) abortMediaMutation(
	ctx context.Context,
	transaction *sql.Tx,
	mutation mediaLinkMutation,
	cause error,
) error {
	rollbackErr := transaction.Rollback()
	if errors.Is(rollbackErr, sql.ErrTxDone) {
		rollbackErr = nil
	}
	return errors.Join(
		cause,
		rollbackErr,
		repository.compensateMedia(ctx, mutation),
	)
}

func (repository *PostgresRepository) compensateMedia(
	ctx context.Context,
	mutation mediaLinkMutation,
) error {
	if repository.media == nil || len(mutation.retained) == 0 {
		return nil
	}
	return repository.media.compensateRetained(ctx, mutation)
}

func draftMediaIDs(content DraftContent) []string {
	ids := make([]string, 0, len(content.Media))
	seen := make(map[string]struct{}, len(content.Media))
	for _, media := range content.Media {
		id := strings.TrimSpace(media.ID)
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}
