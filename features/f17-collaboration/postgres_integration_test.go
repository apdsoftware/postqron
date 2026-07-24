package collaboration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("F17_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F17_DATABASE_URL is not configured")
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
	workspaceID := "workspace-integration-" + suffix
	draftID := "draft-integration-" + suffix
	t.Cleanup(func() {
		cleanupContext := context.Background()
		_, _ = pool.Exec(
			cleanupContext,
			"DELETE FROM f17_collaboration_outbox WHERE workspace_id = $1",
			workspaceID,
		)
		_, _ = pool.Exec(
			cleanupContext,
			"DELETE FROM f17_collaboration_audit_events WHERE workspace_id = $1",
			workspaceID,
		)
		_, _ = pool.Exec(
			cleanupContext,
			"DELETE FROM f17_collaboration_reviews WHERE workspace_id = $1",
			workspaceID,
		)
		_, _ = pool.Exec(
			cleanupContext,
			"DELETE FROM f17_collaboration_comments WHERE workspace_id = $1",
			workspaceID,
		)
	})

	now := time.Now().UTC()
	comment := Comment{
		ID:          "comment_integration_" + suffix,
		WorkspaceID: workspaceID,
		DraftID:     draftID,
		AuthorID:    "member-integration",
		Body:        "Resolve before approval.",
		CreatedAt:   now,
	}
	audit, event := integrationRecords(
		suffix+"-comment",
		workspaceID,
		draftID,
		"comment.created",
		comment.ID,
		now,
	)
	if _, err = repository.CreateComment(ctx, comment, audit, event); err != nil {
		t.Fatal(err)
	}
	review := Review{
		ID:            "review_integration_" + suffix,
		WorkspaceID:   workspaceID,
		DraftID:       draftID,
		DraftRevision: 2,
		Status:        ReviewPending,
		RequestedBy:   "member-integration",
		RequestedAt:   now.Add(time.Second),
	}
	audit, event = integrationRecords(
		suffix+"-review",
		workspaceID,
		draftID,
		"review.requested",
		review.ID,
		review.RequestedAt,
	)
	created, wasCreated, err := repository.RequestReview(ctx, review, audit, event)
	if err != nil || !wasCreated || created.ID != review.ID {
		t.Fatalf("request review = %#v, %v, %v", created, wasCreated, err)
	}
	duplicate, wasCreated, err := repository.RequestReview(ctx, review, audit, event)
	if err != nil || wasCreated || duplicate.ID != review.ID {
		t.Fatalf("repeat review = %#v, %v, %v", duplicate, wasCreated, err)
	}
	approveAudit, approveEvent := integrationRecords(
		suffix+"-approve",
		workspaceID,
		draftID,
		"review.approved",
		review.ID,
		now.Add(2*time.Second),
	)
	approveAudit.ActorID = "owner-integration"
	if _, err = repository.DecideReview(
		ctx,
		workspaceID,
		draftID,
		review.ID,
		DecisionApprove,
		"",
		now.Add(2*time.Second),
		approveAudit,
		approveEvent,
	); !errors.Is(err, ErrUnresolvedComment) {
		t.Fatalf("approval with open comment error = %v", err)
	}
	resolveAudit, resolveEvent := integrationRecords(
		suffix+"-resolve",
		workspaceID,
		draftID,
		"comment.resolved",
		comment.ID,
		now.Add(2*time.Second),
	)
	resolveAudit.ActorID = "owner-integration"
	if _, err = repository.ResolveComment(
		ctx,
		workspaceID,
		draftID,
		comment.ID,
		"owner-integration",
		now.Add(2*time.Second),
		resolveAudit,
		resolveEvent,
	); err != nil {
		t.Fatal(err)
	}
	approved, err := repository.DecideReview(
		ctx,
		workspaceID,
		draftID,
		review.ID,
		DecisionApprove,
		"",
		now.Add(3*time.Second),
		approveAudit,
		approveEvent,
	)
	if err != nil || approved.Status != ReviewApproved {
		t.Fatalf("approve = %#v, %v", approved, err)
	}
	events, err := repository.PendingEvents(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range events {
		if candidate.ID == approveEvent.ID {
			found = true
			if err := repository.MarkEventPublished(ctx, candidate.ID, now.Add(4*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("approval event not found in outbox")
	}
}

func integrationRecords(
	suffix, workspaceID, draftID, action, targetID string,
	now time.Time,
) (AuditEvent, Event) {
	return AuditEvent{
			ID:          "audit_" + suffix,
			WorkspaceID: workspaceID,
			ActorID:     "member-integration",
			TargetType:  "review",
			TargetID:    targetID,
			Action:      action,
			Outcome:     "succeeded",
			OccurredAt:  now,
		}, Event{
			ID:            "event_" + suffix,
			Type:          "collaboration." + action + ".v1",
			WorkspaceID:   workspaceID,
			ActorID:       "member-integration",
			DraftID:       draftID,
			CorrelationID: targetID,
			OccurredAt:    now,
			Data:          map[string]any{},
		}
}
