package analytics

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryIncrementalSyncAndOverview(t *testing.T) {
	databaseURL := os.Getenv("F18_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F18_DATABASE_URL is not configured")
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

	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := now.Format("20060102150405.000000")
	workspaceID := "workspace-f18-" + suffix
	target := SyncTarget{
		ID: targetID(PublishedContent{
			WorkspaceID:  workspaceID,
			Provider:     "meta",
			ConnectionID: "connection-" + suffix,
			RemoteID:     "remote-" + suffix,
		}),
		WorkspaceID:  workspaceID,
		ContentID:    "content-" + suffix,
		ChannelID:    "channel-" + suffix,
		ChannelType:  ChannelInstagramProfessional,
		Provider:     "meta",
		ConnectionID: "connection-" + suffix,
		RemoteID:     "remote-" + suffix,
		PublishedAt:  now.Add(-time.Hour),
		State:        TargetPending,
		NextSyncAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM f18_analytics_observations WHERE target_id = $1",
			target.ID,
		)
		_, _ = pool.Exec(
			context.Background(),
			"DELETE FROM f18_analytics_targets WHERE id = $1",
			target.ID,
		)
	})

	registered, err := repository.Register(ctx, target)
	if err != nil || !registered.Created {
		t.Fatalf("register = %#v, %v", registered, err)
	}
	duplicate, err := repository.Register(ctx, target)
	if err != nil || duplicate.Created || duplicate.TargetID != target.ID {
		t.Fatalf("idempotent register = %#v, %v", duplicate, err)
	}
	claimed, found, err := repository.ClaimDue(
		ctx,
		now,
		now.Add(time.Minute),
		"lease-"+suffix,
	)
	if err != nil || !found {
		t.Fatalf("claim = %#v, %v, %v", claimed, found, err)
	}
	zero := int64(0)
	observations := make([]Observation, 0)
	for _, metric := range []MetricName{
		MetricReach,
		MetricLikes,
		MetricComments,
		MetricShares,
		MetricSaved,
		MetricViews,
		MetricPlays,
	} {
		observations = append(observations, Observation{
			TargetID:     target.ID,
			Metric:       metric,
			OriginalName: string(metric),
			Period:       "lifetime",
			ObservedAt:   now,
			Value:        &zero,
			State:        MetricAvailable,
			APIVersion:   "v24.0",
		})
	}
	if err := repository.SaveSuccess(
		ctx,
		target.ID,
		claimed.LeaseToken,
		observations,
		"cursor-1",
		TargetCurrent,
		now.Add(time.Hour),
		now,
	); err != nil {
		t.Fatal(err)
	}
	overview, err := repository.Overview(ctx, OverviewQuery{
		WorkspaceID: workspaceID,
		From:        now.Add(-2 * time.Hour),
		To:          now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Channels) != 1 ||
		overview.Channels[0].ContentCount != 1 ||
		overview.Channels[0].Metrics[0].Value == nil ||
		*overview.Channels[0].Metrics[0].Value != 0 {
		t.Fatalf("overview = %#v", overview)
	}
}
