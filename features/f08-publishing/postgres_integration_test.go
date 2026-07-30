package publishing

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStorePersistsLeaseRemoteIDAndDeadLetter(t *testing.T) {
	databaseURL := os.Getenv("F08_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F08_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	now := time.Now().UTC()
	job := Job{
		ID:              "pubjob_integration_" + suffix,
		CommandID:       "pubcmd_integration_" + suffix,
		WorkspaceID:     "workspace-integration-" + suffix,
		PostID:          "post-integration-" + suffix,
		DraftID:         "draft-integration",
		Generation:      1,
		InvalidationKey: "post-integration-" + suffix + ":1",
		Status:          JobQueued,
		ExecuteAtUTC:    now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	capabilities := AdapterCapabilities{
		Version:           "integration-v1",
		Mode:              PublishingModeAuto,
		NativeIdempotency: true,
	}
	input := DestinationInput{
		ChannelID:         "channel-integration",
		Provider:          "meta",
		ConnectionID:      "connection-integration",
		Mode:              PublishingModeAuto,
		DraftRevision:     1,
		CapabilityID:      "meta.text",
		CapabilityVersion: capabilities.Version,
		Payload:           []byte(`{"text":"integration"}`),
	}
	payload, err := canonicalJSON(input.Payload)
	if err != nil {
		t.Fatal(err)
	}
	snapshotHash := destinationSnapshotHash(input, capabilities, payload)
	job.Destinations = []Destination{
		{
			ID:            "pubdst_integration_" + suffix,
			JobID:         job.ID,
			CommandID:     job.CommandID,
			WorkspaceID:   job.WorkspaceID,
			PostID:        job.PostID,
			Generation:    job.Generation,
			DraftRevision: 1,
			ChannelID:     "channel-integration",
			Provider:      "meta",
			ConnectionID:  "connection-integration",
			Mode:          PublishingModeAuto,
			CapabilityID:  input.CapabilityID,
			Capabilities:  capabilities,
			Payload:       payload,
			SnapshotHash:  snapshotHash,
			IdempotencyKey: destinationIdempotencyKey(
				job.InvalidationKey,
				"channel-integration",
				1,
				snapshotHash,
			),
			Status:        DestinationPending,
			MaxAttempts:   2,
			NextAttemptAt: now,
		},
	}
	created, err := store.Enqueue(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM f08_publication_dead_letters WHERE job_id = $1",
			created.JobID,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM f08_publication_attempts WHERE destination_id = $1",
			job.Destinations[0].ID,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM f08_publication_destinations WHERE job_id = $1",
			created.JobID,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM f08_publication_jobs WHERE id = $1",
			created.JobID,
		)
	})

	duplicate, err := store.Enqueue(ctx, job)
	if err != nil || duplicate.Created || duplicate.JobID != created.JobID {
		t.Fatalf("idempotent enqueue=%#v error=%v", duplicate, err)
	}
	mutated := job
	mutated.Destinations = append([]Destination(nil), job.Destinations...)
	mutated.Destinations[0] = job.Destinations[0]
	mutated.Destinations[0].SnapshotHash =
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err := store.Enqueue(ctx, mutated); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutated immutable enqueue error=%v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE f08_publication_destinations
		    SET payload = '{"text":"mutated"}'::jsonb
		  WHERE id = $1`,
		job.Destinations[0].ID,
	); err == nil {
		t.Fatal("database allowed immutable destination payload mutation")
	}
	claimed, found, err := store.ClaimDue(
		ctx,
		now,
		now.Add(time.Second),
		"lease_integration_"+suffix,
	)
	if err != nil || !found || claimed.AttemptCount != 1 {
		t.Fatalf("claim=%#v found=%v error=%v", claimed, found, err)
	}
	reclaimed, found, err := store.ClaimDue(
		ctx,
		now.Add(2*time.Second),
		now.Add(time.Minute),
		"lease_reclaim_integration_"+suffix,
	)
	if err != nil || !found || !reclaimed.NeedsReconciliation ||
		reclaimed.AttemptCount != 2 {
		t.Fatalf("reclaim=%#v found=%v error=%v", reclaimed, found, err)
	}
	if err := store.MarkPublished(
		ctx,
		reclaimed.ID,
		reclaimed.LeaseToken,
		PublishResult{
			Complete: true,
			RemoteID: "remote-integration-" + suffix,
		},
		now.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	persisted, err := store.GetJob(ctx, job.WorkspaceID, job.ID)
	if err != nil ||
		persisted.Status != JobPublished ||
		persisted.Destinations[0].RemoteID == "" {
		t.Fatalf("persisted job=%#v error=%v", persisted, err)
	}
}
