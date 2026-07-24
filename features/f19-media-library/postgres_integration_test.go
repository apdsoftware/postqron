package medialibrary

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("F19_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F19_DATABASE_URL is not configured")
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
	now := time.Now().UTC()
	upload := Upload{
		ID: "upload_integration_" + suffix, AssetID: "media_integration_" + suffix,
		WorkspaceID: "workspace-integration-" + suffix, CreatedBy: "account-integration",
		StorageKey: "integration/" + suffix, OriginalName: "integration.jpg",
		DeclaredType: "image/jpeg", ReservedSizeBytes: 128,
		IdempotencyKey: "integration-" + suffix, Status: UploadPending,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now,
	}
	stored, created, err := repository.CreateUpload(ctx, upload)
	if err != nil || !created || stored.ID != upload.ID {
		t.Fatalf("stored = %#v, created = %v, error = %v", stored, created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM f19_media_assets WHERE workspace_id = $1",
			upload.WorkspaceID,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM f19_media_uploads WHERE workspace_id = $1",
			upload.WorkspaceID,
		)
	})
	again, created, err := repository.CreateUpload(ctx, Upload{
		ID: "upload_other_" + suffix, AssetID: "media_other_" + suffix,
		WorkspaceID: upload.WorkspaceID, CreatedBy: upload.CreatedBy,
		StorageKey: "integration/other/" + suffix, OriginalName: upload.OriginalName,
		DeclaredType: upload.DeclaredType, ReservedSizeBytes: upload.ReservedSizeBytes,
		IdempotencyKey: upload.IdempotencyKey, Status: UploadPending,
		ExpiresAt: upload.ExpiresAt, CreatedAt: upload.CreatedAt,
	})
	if err != nil || created || again.ID != upload.ID {
		t.Fatalf("idempotent = %#v, created = %v, error = %v", again, created, err)
	}

	asset := Asset{
		ID: upload.AssetID, WorkspaceID: upload.WorkspaceID, CreatedBy: upload.CreatedBy,
		StorageKey: upload.StorageKey, OriginalName: upload.OriginalName,
		Kind: MediaImage, ContentType: "image/jpeg", SizeBytes: 128,
		Width: 100, Height: 100,
		ChecksumSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Tags:           []string{}, Status: StatusReady, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	asset, created, err = repository.CompleteUpload(ctx, upload, asset)
	if err != nil || !created {
		t.Fatalf("asset = %#v, created = %v, error = %v", asset, created, err)
	}
	asset.OriginalName = "renamed.jpg"
	asset.Tags = []string{"campaign"}
	asset.UpdatedAt = now.Add(time.Second)
	asset, err = repository.UpdateMetadata(ctx, asset, 1)
	if err != nil || asset.Revision != 2 {
		t.Fatalf("updated = %#v, error = %v", asset, err)
	}
	if _, err := repository.UpdateMetadata(ctx, asset, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}
	found, err := repository.Search(ctx, upload.WorkspaceID, SearchQuery{
		Text: "renamed", Tags: []string{"campaign"}, Limit: 10,
	})
	if err != nil || len(found) != 1 {
		t.Fatalf("search = %#v, error = %v", found, err)
	}
	asset, err = repository.Archive(
		ctx, upload.WorkspaceID, asset.ID, asset.Revision, now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkPurged(
		ctx, upload.WorkspaceID, asset.ID, asset.Revision, now.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
}
