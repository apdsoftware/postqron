package composer

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("F06_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F06_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository, err := NewPostgresRepository(database)
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
	if err := database.QueryRowContext(ctx, `
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
	updated, err := repository.Update(ctx, created, 1, "integration-save-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Content.Text != "second" {
		t.Fatalf("updated = %#v", updated)
	}
	if _, err := repository.Update(ctx, updated, 1, "integration-save-2"); !errors.Is(err, ErrConflict) {
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

func TestPostgresDuplicateOperationFencingIntegration(t *testing.T) {
	databaseURL := os.Getenv("F06_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F06_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	workspaceID := "workspace-duplicate-op-" + suffix
	operation := duplicateOperation{
		WorkspaceID:      workspaceID,
		IdempotencyKey:   "test-idempotency-fencing",
		SourceDraftID:    "draft-source",
		SourceRevision:   1,
		CreatedByAccount: "account-1",
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(
			context.Background(),
			`DELETE FROM f06_composer_duplicate_operations WHERE workspace_id = $1`,
			workspaceID,
		)
	})

	t.Run("stale owner cannot complete or abandon after lease reclaim", func(t *testing.T) {
		now := time.Now().UTC()
		first, replayed, err := repository.ReserveDuplicateOperation(ctx, operation, now)
		if err != nil || replayed {
			t.Fatalf("first reserve = %#v replayed=%v err=%v", first, replayed, err)
		}
		second, replayed, err := repository.ReserveDuplicateOperation(
			ctx,
			operation,
			now.Add(duplicateOperationLease+time.Second),
		)
		if err != nil || replayed {
			t.Fatalf("second reserve = %#v replayed=%v err=%v", second, replayed, err)
		}
		if second.LeaseGeneration <= first.LeaseGeneration {
			t.Fatalf("lease generation did not advance: first=%#v second=%#v", first, second)
		}
		if err := repository.CompleteDuplicateOperation(
			ctx,
			first,
			"draft-clone-stale",
			1,
			now.Add(duplicateOperationLease+2*time.Second),
		); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale complete error = %v", err)
		}
		if abandoned, err := repository.AbandonDuplicateOperation(ctx, first); err != nil || abandoned {
			t.Fatalf("stale abandon = %v abandoned=%v", err, abandoned)
		}
		if err := repository.CompleteDuplicateOperation(
			ctx,
			second,
			"draft-clone-canonical",
			1,
			now.Add(duplicateOperationLease+2*time.Second),
		); err != nil {
			t.Fatalf("canonical complete error = %v", err)
		}
		stored, replayed, err := repository.ReserveDuplicateOperation(
			ctx,
			operation,
			now.Add(duplicateOperationLease+3*time.Second),
		)
		if err != nil || !replayed || stored.CloneDraftID != "draft-clone-canonical" {
			t.Fatalf("replayed completed op = %#v replayed=%v err=%v", stored, replayed, err)
		}
	})

	t.Run("completed dangling operation can be reclaimed with CAS", func(t *testing.T) {
		now := time.Now().UTC().Add(time.Hour)
		keyed := operation
		keyed.IdempotencyKey = "test-idempotency-dangling"
		reserved, replayed, err := repository.ReserveDuplicateOperation(ctx, keyed, now)
		if err != nil || replayed {
			t.Fatalf("reserve dangling = %#v replayed=%v err=%v", reserved, replayed, err)
		}
		if err := repository.CompleteDuplicateOperation(
			ctx,
			reserved,
			"draft-clone-missing",
			1,
			now.Add(time.Second),
		); err != nil {
			t.Fatalf("complete dangling = %v", err)
		}
		completed, replayed, err := repository.ReserveDuplicateOperation(
			ctx,
			keyed,
			now.Add(2*time.Second),
		)
		if err != nil || !replayed {
			t.Fatalf("reserve completed dangling = %#v replayed=%v err=%v", completed, replayed, err)
		}
		reset, ok, err := repository.ResetDanglingCompletedDuplicateOperation(
			ctx,
			completed,
			now.Add(3*time.Second),
		)
		if err != nil || !ok {
			t.Fatalf("reset dangling = %#v ok=%v err=%v", reset, ok, err)
		}
		if reset.Status != duplicateOperationPending ||
			reset.CloneDraftID != "" ||
			reset.LeaseGeneration <= completed.LeaseGeneration {
			t.Fatalf("reset state = %#v from %#v", reset, completed)
		}
		if staleReset, ok, err := repository.ResetDanglingCompletedDuplicateOperation(
			ctx,
			completed,
			now.Add(4*time.Second),
		); err != nil || ok || staleReset != (duplicateOperation{}) {
			t.Fatalf("stale reset = %#v ok=%v err=%v", staleReset, ok, err)
		}
	})
}
