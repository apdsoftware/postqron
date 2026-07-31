package publishing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMonotonicTIDAllocatorCASAndRestart(t *testing.T) {
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
	repository := "did:plc:postgres" + strings.ReplaceAll(suffix, ".", "")
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx,
			"DELETE FROM f08_bluesky_tid_allocations WHERE repository = $1",
			repository)
		_, _ = pool.Exec(ctx,
			"DELETE FROM f08_bluesky_tid_namespaces WHERE repository = $1",
			repository)
	})
	physical := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC).UnixMicro()
	exactCollisionKeys := []string{
		"publish_c89c98f88e44e5bbd531ed13d967f34619c5fc0e6fe4711c7c517f3b2473308f",
		"publish_8292a09311bfe2e4ca21ac76822f570371c77db6c8cb034fe53189d0e473308f",
	}
	first, err := store.AllocateMonotonicTID(
		ctx, repository, exactCollisionKeys[0], physical,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AllocateMonotonicTID(
		ctx, repository, exactCollisionKeys[1], physical,
	)
	if err != nil || second != first+1 {
		t.Fatalf("collision allocation first=%d second=%d error=%v",
			first, second, err)
	}

	// Reconstructing the store simulates a worker process restart. The
	// idempotency mapping must be read from PostgreSQL, not process memory.
	restarted, err := NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.AllocateMonotonicTID(
		ctx, repository, exactCollisionKeys[0], physical,
	)
	if err != nil || replayed != first {
		t.Fatalf("restart replay=%d first=%d error=%v", replayed, first, err)
	}

	const concurrent = 128
	realServiceKeys := make([]string, 0, concurrent)
	for index := 0; index < concurrent; index++ {
		realServiceKeys = append(realServiceKeys, destinationIdempotencyKey(
			fmt.Sprintf("command-cas-%d", index),
			"channel-bluesky",
			1,
			strings.Repeat("c", 64),
		))
	}
	start := make(chan struct{})
	values := make(chan uint64, concurrent)
	failures := make(chan error, concurrent)
	var group sync.WaitGroup
	for _, key := range realServiceKeys {
		group.Add(1)
		go func(key string) {
			defer group.Done()
			<-start
			value, allocateErr := restarted.AllocateMonotonicTID(
				ctx, repository, key, physical,
			)
			values <- value
			failures <- allocateErr
		}(key)
	}
	close(start)
	group.Wait()
	close(values)
	close(failures)
	for allocateErr := range failures {
		if allocateErr != nil {
			t.Fatal(allocateErr)
		}
	}
	ordered := make([]uint64, 0, concurrent)
	seen := make(map[uint64]struct{}, concurrent)
	for value := range values {
		if _, duplicate := seen[value]; duplicate {
			t.Fatalf("duplicate PostgreSQL CAS allocation %d", value)
		}
		seen[value] = struct{}{}
		ordered = append(ordered, value)
	}
	slices.Sort(ordered)
	for index := 1; index < len(ordered); index++ {
		if ordered[index] != ordered[index-1]+1 {
			t.Fatalf("non-monotonic CAS sequence at %d: %d then %d",
				index, ordered[index-1], ordered[index])
		}
	}
	later, err := restarted.AllocateMonotonicTID(
		ctx,
		repository,
		destinationIdempotencyKey(
			"command-physical-later",
			"channel-bluesky",
			1,
			strings.Repeat("c", 64),
		),
		physical+1,
	)
	if err != nil || later <= ordered[len(ordered)-1] ||
		int64(later>>10) < physical+1 {
		t.Fatalf("later=%d previous=%d error=%v",
			later, ordered[len(ordered)-1], err)
	}
}

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
