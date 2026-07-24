package composer

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("F06_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F06_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	draft := Draft{
		ID:          "draft_integration_" + suffix,
		WorkspaceID: "workspace-integration-" + suffix,
		CreatedBy:   "account-integration",
		Content:     DraftContent{Text: "first"},
		Revision:    1,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	created, err := repository.Create(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if created.Content.Media == nil || created.Content.Destinations == nil {
		t.Fatalf("created content must preserve empty arrays: %#v", created.Content)
	}
	var mediaType, destinationsType string
	if err := pool.QueryRow(ctx, `
		SELECT
			jsonb_typeof(content -> 'media'),
			jsonb_typeof(content -> 'destinations')
		FROM f06_composer_drafts
		WHERE id = $1
	`, created.ID).Scan(&mediaType, &destinationsType); err != nil {
		t.Fatal(err)
	}
	if mediaType != "array" || destinationsType != "array" {
		t.Fatalf(
			"stored content types = media %q, destinations %q",
			mediaType,
			destinationsType,
		)
	}
	t.Cleanup(func() {
		_ = repository.Delete(
			context.Background(),
			created.WorkspaceID,
			created.ID,
			2,
		)
	})

	created.Content.Text = "second"
	created.UpdatedAt = created.UpdatedAt.Add(time.Second)
	updated, err := repository.Update(ctx, created, 1)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Content.Text != "second" {
		t.Fatalf("updated = %#v", updated)
	}
	if _, err := repository.Update(ctx, updated, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	list, err := repository.List(ctx, draft.WorkspaceID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %#v, err = %v", list, err)
	}
	if err := repository.Delete(ctx, draft.WorkspaceID, draft.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(ctx, draft.WorkspaceID, draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete error = %v", err)
	}
}
