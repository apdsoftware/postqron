package publishing

import (
	"context"
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
	job.Destinations = []Destination{
		{
			ID:             "pubdst_integration_" + suffix,
			JobID:          job.ID,
			CommandID:      job.CommandID,
			WorkspaceID:    job.WorkspaceID,
			PostID:         job.PostID,
			Generation:     job.Generation,
			ChannelID:      "channel-integration",
			Provider:       "meta",
			ConnectionID:   "connection-integration",
			Payload:        []byte(`{"text":"integration"}`),
			IdempotencyKey: destinationIdempotencyKey(job.InvalidationKey, "channel-integration"),
			Status:         DestinationPending,
			MaxAttempts:    2,
			NextAttemptAt:  now,
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
	claimed, found, err := store.ClaimDue(
		ctx,
		now,
		now.Add(time.Minute),
		"lease_integration_"+suffix,
	)
	if err != nil || !found || claimed.AttemptCount != 1 {
		t.Fatalf("claim=%#v found=%v error=%v", claimed, found, err)
	}
	if err := store.MarkPublished(
		ctx,
		claimed.ID,
		claimed.LeaseToken,
		"remote-integration-"+suffix,
		now.Add(time.Second),
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
