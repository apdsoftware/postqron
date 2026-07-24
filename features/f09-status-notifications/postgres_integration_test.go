package statusnotifications

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSQLRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("F09_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set F09_DATABASE_URL after applying the F9 migration")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatalf("database.Ping() error = %v", err)
	}
	repository, err := NewSQLRepository(database)
	if err != nil {
		t.Fatalf("NewSQLRepository() error = %v", err)
	}
	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UnixNano())
	workspaceID := "workspace-it-" + suffix
	postID := "post-it-" + suffix
	destinationID := "destination-it-" + suffix
	now := time.Now().UTC().Truncate(time.Microsecond)
	t.Cleanup(func() {
		_, _ = database.ExecContext(
			context.Background(),
			`DELETE FROM f09_notification_outbox WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.ExecContext(
			context.Background(),
			`DELETE FROM f09_manual_retry_outbox WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.ExecContext(
			context.Background(),
			`DELETE FROM f09_destination_status WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.ExecContext(
			context.Background(),
			`DELETE FROM f09_post_status WHERE workspace_id = $1`,
			workspaceID,
		)
		_, _ = database.ExecContext(
			context.Background(),
			`DELETE FROM f09_publication_status_events WHERE workspace_id = $1`,
			workspaceID,
		)
	})

	lifecycle := LifecycleEvent{
		EventID:     "lifecycle-it-" + suffix,
		WorkspaceID: workspaceID,
		PostID:      postID,
		DraftID:     "draft-it-" + suffix,
		Revision:    1,
		Status:      StatusScheduled,
		Destinations: []DestinationRef{
			{ID: destinationID, ChannelID: "channel-it-" + suffix},
		},
		OccurredAt: now,
	}
	first, err := repository.ApplyLifecycle(ctx, lifecycle)
	if err != nil || !first.FirstDelivery || !first.StateChanged {
		t.Fatalf("ApplyLifecycle() = %+v, %v", first, err)
	}
	duplicate, err := repository.ApplyLifecycle(ctx, lifecycle)
	if err != nil || duplicate.FirstDelivery || duplicate.StateChanged {
		t.Fatalf("duplicate ApplyLifecycle() = %+v, %v", duplicate, err)
	}

	failure := PublicationEvent{
		EventID:       "failure-it-" + suffix,
		WorkspaceID:   workspaceID,
		JobID:         "job-it-" + suffix,
		PostID:        postID,
		DestinationID: destinationID,
		ChannelID:     "channel-it-" + suffix,
		Status:        "dead_letter",
		Diagnostic: SourceDiagnostic{
			Code:   "permission_revoked",
			Detail: "token=secret owner@example.test",
		},
		OccurredAt: now.Add(time.Minute),
	}
	failed, err := repository.ApplyPublication(ctx, failure)
	if err != nil || !failed.StateChanged ||
		failed.View.Status != StatusFailed ||
		failed.View.Destinations[0].Diagnostic.Message == "" {
		t.Fatalf("ApplyPublication() = %+v, %v", failed, err)
	}

	notification := Notification{
		ID:            stableID("notification", "integration", suffix),
		SourceEventID: failure.EventID,
		Kind:          NotificationPublicationFailed,
		WorkspaceID:   workspaceID,
		PostID:        postID,
		DestinationID: destinationID,
		IdempotencyKey: notificationIdempotencyKey(
			NotificationPublicationFailed,
			failure.EventID,
		),
		State:         QueuePending,
		NextAttemptAt: now,
		CreatedAt:     now,
	}
	enqueued, err := repository.EnqueueNotification(ctx, notification)
	if err != nil || !enqueued.Created {
		t.Fatalf("EnqueueNotification() = %+v, %v", enqueued, err)
	}
	enqueued, err = repository.EnqueueNotification(ctx, notification)
	if err != nil || enqueued.Created {
		t.Fatalf("duplicate EnqueueNotification() = %+v, %v", enqueued, err)
	}
	claimedNotification, found, err := repository.ClaimNotification(
		ctx,
		now,
		now.Add(time.Minute),
		"lease-notification-"+suffix,
	)
	if err != nil || !found || claimedNotification.AttemptCount != 1 {
		t.Fatalf(
			"ClaimNotification() = %+v, %v, %v",
			claimedNotification,
			found,
			err,
		)
	}
	if err := repository.MarkNotificationDelivered(
		ctx,
		claimedNotification.ID,
		claimedNotification.LeaseToken,
		now,
	); err != nil {
		t.Fatalf("MarkNotificationDelivered() error = %v", err)
	}

	retry := ManualRetry{
		ID:             stableID("manual_retry", workspaceID, suffix),
		WorkspaceID:    workspaceID,
		PostID:         postID,
		DestinationID:  destinationID,
		FailureEventID: failure.EventID,
		ActorID:        "account-it-" + suffix,
		IdempotencyKey: "retry-it-" + suffix,
		State:          QueuePending,
		NextAttemptAt:  now,
		CreatedAt:      now,
	}
	retryResult, err := repository.EnqueueManualRetry(ctx, retry)
	if err != nil || !retryResult.Created {
		t.Fatalf("EnqueueManualRetry() = %+v, %v", retryResult, err)
	}
	retry.IdempotencyKey = "other-key-" + suffix
	retry.ID = stableID("manual_retry", workspaceID, "other", suffix)
	retryResult, err = repository.EnqueueManualRetry(ctx, retry)
	if err != nil || retryResult.Created {
		t.Fatalf("failure-cycle duplicate = %+v, %v", retryResult, err)
	}
	claimedRetry, found, err := repository.ClaimManualRetry(
		ctx,
		now,
		now.Add(time.Minute),
		"lease-retry-"+suffix,
	)
	if err != nil || !found || claimedRetry.AttemptCount != 1 {
		t.Fatalf("ClaimManualRetry() = %+v, %v, %v", claimedRetry, found, err)
	}
	if err := repository.MarkManualRetryDelivered(
		ctx,
		claimedRetry.ID,
		claimedRetry.LeaseToken,
		now,
	); err != nil {
		t.Fatalf("MarkManualRetryDelivered() error = %v", err)
	}
}
