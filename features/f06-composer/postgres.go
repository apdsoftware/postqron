package composer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) (*PostgresRepository, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: postgres pool is required", ErrInvalidArgument)
	}
	return &PostgresRepository{pool: pool}, nil
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
	_, err = repository.pool.Exec(ctx, `
		INSERT INTO f06_composer_drafts (
			id,
			workspace_id,
			created_by_account_id,
			content,
			revision,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`,
		draft.ID,
		draft.WorkspaceID,
		draft.CreatedBy,
		content,
		draft.Revision,
		draft.CreatedAt,
		draft.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Draft{}, ErrConflict
		}
		return Draft{}, fmt.Errorf("create composer draft: %w", err)
	}
	return cloneDraft(draft), nil
}

func (repository *PostgresRepository) Get(
	ctx context.Context,
	workspaceID, draftID string,
) (Draft, error) {
	row := repository.pool.QueryRow(ctx, `
		SELECT
			id,
			workspace_id,
			created_by_account_id,
			content,
			revision,
			created_at,
			updated_at
		FROM f06_composer_drafts
		WHERE workspace_id = $1 AND id = $2
	`, workspaceID, draftID)
	return scanPostgresDraft(row)
}

func (repository *PostgresRepository) List(
	ctx context.Context,
	workspaceID string,
) ([]Draft, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT
			id,
			workspace_id,
			created_by_account_id,
			content,
			revision,
			created_at,
			updated_at
		FROM f06_composer_drafts
		WHERE workspace_id = $1
		ORDER BY updated_at DESC, id
	`, workspaceID)
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
) (Draft, error) {
	draft.Content = contentForPostgres(draft.Content)
	content, err := json.Marshal(draft.Content)
	if err != nil {
		return Draft{}, fmt.Errorf("encode draft content: %w", err)
	}
	row := repository.pool.QueryRow(ctx, `
		UPDATE f06_composer_drafts
		SET content = $4,
			revision = revision + 1,
			updated_at = $5
		WHERE workspace_id = $1
		  AND id = $2
		  AND revision = $3
		RETURNING
			id,
			workspace_id,
			created_by_account_id,
			content,
			revision,
			created_at,
			updated_at
	`,
		draft.WorkspaceID,
		draft.ID,
		expectedRevision,
		content,
		draft.UpdatedAt,
	)
	updated, err := scanPostgresDraft(row)
	if !errors.Is(err, ErrNotFound) {
		return updated, err
	}
	return Draft{}, repository.classifyMiss(ctx, draft.WorkspaceID, draft.ID)
}

func contentForPostgres(content DraftContent) DraftContent {
	content = cloneContent(content)
	if content.Media == nil {
		content.Media = []Media{}
	}
	if content.Destinations == nil {
		content.Destinations = []Destination{}
	}
	return content
}

func (repository *PostgresRepository) Delete(
	ctx context.Context,
	workspaceID, draftID string,
	expectedRevision int64,
) error {
	tag, err := repository.pool.Exec(ctx, `
		DELETE FROM f06_composer_drafts
		WHERE workspace_id = $1 AND id = $2 AND revision = $3
	`, workspaceID, draftID, expectedRevision)
	if err != nil {
		return fmt.Errorf("delete composer draft: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return repository.classifyMiss(ctx, workspaceID, draftID)
}

func (repository *PostgresRepository) classifyMiss(
	ctx context.Context,
	workspaceID, draftID string,
) error {
	var exists bool
	err := repository.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM f06_composer_drafts
			WHERE workspace_id = $1 AND id = $2
		)
	`, workspaceID, draftID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("classify composer draft miss: %w", err)
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
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("scan composer draft: %w", err)
	}
	if err := json.Unmarshal(content, &draft.Content); err != nil {
		return Draft{}, fmt.Errorf("decode composer draft content: %w", err)
	}
	return draft, nil
}

func isUniqueViolation(err error) bool {
	var postgresError interface{ SQLState() string }
	return errors.As(err, &postgresError) && postgresError.SQLState() == "23505"
}
