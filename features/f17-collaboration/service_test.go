package collaboration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testTime = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

type permissionAuthorizer struct {
	permissions map[string]map[Permission]bool
}

func (authorizer permissionAuthorizer) Authorize(
	_ context.Context,
	_ string,
	accountID string,
	permission Permission,
) error {
	if !authorizer.permissions[accountID][permission] {
		return ErrForbidden
	}
	return nil
}

type draftReader struct {
	mutex sync.Mutex
	draft DraftSnapshot
}

func (reader *draftReader) Draft(
	_ context.Context,
	workspaceID, draftID string,
) (DraftSnapshot, error) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	if reader.draft.WorkspaceID != workspaceID || reader.draft.ID != draftID {
		return DraftSnapshot{}, ErrNotFound
	}
	return reader.draft, nil
}

func (reader *draftReader) setRevision(revision int64) {
	reader.mutex.Lock()
	defer reader.mutex.Unlock()
	reader.draft.Revision = revision
}

func testService(t *testing.T) (*Service, *MemoryRepository, *draftReader) {
	t.Helper()
	repository := NewMemoryRepository()
	reader := &draftReader{draft: DraftSnapshot{
		ID:          "draft-1",
		WorkspaceID: "workspace-1",
		Revision:    3,
		Valid:       true,
	}}
	authorizer := permissionAuthorizer{permissions: map[string]map[Permission]bool{
		"member": {
			PermissionComment:       true,
			PermissionRequestReview: true,
		},
		"owner": {
			PermissionComment:       true,
			PermissionResolve:       true,
			PermissionRequestReview: true,
			PermissionApprove:       true,
		},
	}}
	var sequence atomic.Uint32
	service, err := NewService(
		repository,
		authorizer,
		reader,
		WithClock(func() time.Time { return testTime }),
		WithRandom(func(destination []byte) error {
			value := byte(sequence.Add(1))
			for index := range destination {
				destination[index] = value
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, reader
}

func TestCommentsRequireMembershipPermissionAndBlockApprovalUntilResolved(t *testing.T) {
	service, repository, _ := testService(t)
	ctx := context.Background()

	if _, err := service.AddComment(ctx, CreateCommentCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ActorID:     "outsider",
		Body:        "No access",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider comment error = %v, want forbidden", err)
	}
	comment, err := service.AddComment(ctx, CreateCommentCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ActorID:     "member",
		Body:        "  Correggere l’accento.  ",
	})
	if err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if comment.Body != "Correggere l’accento." {
		t.Fatalf("comment body = %q", comment.Body)
	}
	review, _, err := service.RequestReview(ctx, RequestReviewCommand{
		WorkspaceID:      "workspace-1",
		DraftID:          "draft-1",
		ActorID:          "member",
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatalf("request review: %v", err)
	}
	if _, err = service.DecideReview(ctx, DecideReviewCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ReviewID:    review.ID,
		ActorID:     "owner",
		Decision:    DecisionApprove,
	}); !errors.Is(err, ErrUnresolvedComment) {
		t.Fatalf("approve with open comment error = %v", err)
	}
	if _, err = service.ResolveComment(ctx, ResolveCommentCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		CommentID:   comment.ID,
		ActorID:     "member",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member resolve error = %v, want forbidden", err)
	}
	if _, err = service.ResolveComment(ctx, ResolveCommentCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		CommentID:   comment.ID,
		ActorID:     "owner",
	}); err != nil {
		t.Fatalf("owner resolve: %v", err)
	}
	approved, err := service.DecideReview(ctx, DecideReviewCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ReviewID:    review.ID,
		ActorID:     "owner",
		Decision:    DecisionApprove,
		Note:        "Pronto.",
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != ReviewApproved || approved.DecidedBy != "owner" {
		t.Fatalf("approved review = %#v", approved)
	}
	if got := len(repository.AuditEvents()); got != 4 {
		t.Fatalf("audit count = %d, want 4", got)
	}
	events, err := service.PendingEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].Data["comment_id"] != comment.ID {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		if _, copiedBody := event.Data["body"]; copiedBody {
			t.Fatal("comment body leaked to F9 event")
		}
	}
}

func TestReviewStateMachineRequiresAuthorizedIndependentApproval(t *testing.T) {
	service, _, _ := testService(t)
	ctx := context.Background()
	review, created, err := service.RequestReview(ctx, RequestReviewCommand{
		WorkspaceID:      "workspace-1",
		DraftID:          "draft-1",
		ActorID:          "member",
		ExpectedRevision: 3,
	})
	if err != nil || !created {
		t.Fatalf("request review = %#v, %v, %v", review, created, err)
	}
	repeated, created, err := service.RequestReview(ctx, RequestReviewCommand{
		WorkspaceID:      "workspace-1",
		DraftID:          "draft-1",
		ActorID:          "member",
		ExpectedRevision: 3,
	})
	if err != nil || created || repeated.ID != review.ID {
		t.Fatalf("idempotent request = %#v, %v, %v", repeated, created, err)
	}
	if _, err = service.DecideReview(ctx, DecideReviewCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ReviewID:    review.ID,
		ActorID:     "member",
		Decision:    DecisionApprove,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member approval error = %v, want forbidden", err)
	}

	ownerReview, _, err := service.RequestReview(ctx, RequestReviewCommand{
		WorkspaceID:      "workspace-1",
		DraftID:          "draft-1",
		ActorID:          "owner",
		ExpectedRevision: 3,
	})
	if !errors.Is(err, ErrReviewPending) || ownerReview.ID != "" {
		t.Fatalf("second pending review = %#v, %v", ownerReview, err)
	}

	changes, err := service.DecideReview(ctx, DecideReviewCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ReviewID:    review.ID,
		ActorID:     "owner",
		Decision:    DecisionRequestChanges,
		Note:        "Aggiungere la CTA.",
	})
	if err != nil || changes.Status != ReviewChangesRequested {
		t.Fatalf("changes decision = %#v, %v", changes, err)
	}

	selfReview, _, err := service.RequestReview(ctx, RequestReviewCommand{
		WorkspaceID:      "workspace-1",
		DraftID:          "draft-1",
		ActorID:          "owner",
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatalf("owner review request: %v", err)
	}
	if _, err = service.DecideReview(ctx, DecideReviewCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ReviewID:    selfReview.ID,
		ActorID:     "owner",
		Decision:    DecisionApprove,
	}); !errors.Is(err, ErrSelfApproval) {
		t.Fatalf("self approval error = %v", err)
	}
}

func TestSchedulingGateIsFailClosedAndRevisionBound(t *testing.T) {
	service, repository, reader := testService(t)
	ctx := context.Background()
	request := ScheduleAuthorization{
		WorkspaceID:   "workspace-1",
		DraftID:       "draft-1",
		DraftRevision: 3,
		CorrelationID: "schedule-1",
	}
	if err := service.AuthorizeScheduling(ctx, request); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("unreviewed scheduling error = %v", err)
	}
	review, _, err := service.RequestReview(ctx, RequestReviewCommand{
		WorkspaceID:      request.WorkspaceID,
		DraftID:          request.DraftID,
		ActorID:          "member",
		ExpectedRevision: request.DraftRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.DecideReview(ctx, DecideReviewCommand{
		WorkspaceID: request.WorkspaceID,
		DraftID:     request.DraftID,
		ReviewID:    review.ID,
		ActorID:     "owner",
		Decision:    DecisionApprove,
	}); err != nil {
		t.Fatal(err)
	}
	if err = service.AuthorizeScheduling(ctx, request); err != nil {
		t.Fatalf("approved scheduling: %v", err)
	}
	reader.setRevision(4)
	request.DraftRevision = 4
	request.CorrelationID = "schedule-2"
	if err = service.AuthorizeScheduling(ctx, request); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("changed revision scheduling error = %v", err)
	}
	audits := repository.AuditEvents()
	if audits[0].Action != "scheduling.blocked" ||
		audits[len(audits)-1].Action != "scheduling.blocked" {
		t.Fatalf("scheduling denials not audited: %#v", audits)
	}
}

func TestCommentAddedAfterApprovalBlocksSchedulingUntilResolved(t *testing.T) {
	service, _, _ := testService(t)
	ctx := context.Background()
	review, _, err := service.RequestReview(ctx, RequestReviewCommand{
		WorkspaceID:      "workspace-1",
		DraftID:          "draft-1",
		ActorID:          "member",
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.DecideReview(ctx, DecideReviewCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ReviewID:    review.ID,
		ActorID:     "owner",
		Decision:    DecisionApprove,
	}); err != nil {
		t.Fatal(err)
	}
	comment, err := service.AddComment(ctx, CreateCommentCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ActorID:     "member",
		Body:        "Verifica finale.",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ScheduleAuthorization{
		WorkspaceID:   "workspace-1",
		DraftID:       "draft-1",
		DraftRevision: 3,
		CorrelationID: "schedule-after-comment",
	}
	if err = service.AuthorizeScheduling(ctx, request); !errors.Is(err, ErrUnresolvedComment) {
		t.Fatalf("scheduling with new comment error = %v", err)
	}
	if _, err = service.ResolveComment(ctx, ResolveCommentCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		CommentID:   comment.ID,
		ActorID:     "owner",
	}); err != nil {
		t.Fatal(err)
	}
	if err = service.AuthorizeScheduling(ctx, request); err != nil {
		t.Fatalf("scheduling after resolve: %v", err)
	}
}

func TestConcurrentReviewRequestsCreateOnePendingReview(t *testing.T) {
	service, _, _ := testService(t)
	ctx := context.Background()
	const attempts = 20
	var wait sync.WaitGroup
	var created int
	var createdMutex sync.Mutex
	errorsFound := make(chan error, attempts)
	for index := range attempts {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			review, wasCreated, err := service.RequestReview(ctx, RequestReviewCommand{
				WorkspaceID:      "workspace-1",
				DraftID:          "draft-1",
				ActorID:          "member",
				ExpectedRevision: 3,
			})
			if err != nil {
				errorsFound <- fmt.Errorf("request %d: %w", index, err)
				return
			}
			if review.Status != ReviewPending {
				errorsFound <- fmt.Errorf("request %d returned %s", index, review.Status)
			}
			if wasCreated {
				createdMutex.Lock()
				created++
				createdMutex.Unlock()
			}
		}(index)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if created != 1 {
		t.Fatalf("created reviews = %d, want 1", created)
	}
}

func TestOutboxPublicationIsIdempotent(t *testing.T) {
	service, _, _ := testService(t)
	ctx := context.Background()
	if _, err := service.AddComment(ctx, CreateCommentCommand{
		WorkspaceID: "workspace-1",
		DraftID:     "draft-1",
		ActorID:     "member",
		Body:        "Commento",
	}); err != nil {
		t.Fatal(err)
	}
	events, err := service.PendingEvents(ctx, 1)
	if err != nil || len(events) != 1 {
		t.Fatalf("pending events = %#v, %v", events, err)
	}
	if err = service.MarkEventPublished(ctx, events[0].ID, testTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = service.MarkEventPublished(ctx, events[0].ID, testTime.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	events, err = service.PendingEvents(ctx, 1)
	if err != nil || len(events) != 0 {
		t.Fatalf("pending after publish = %#v, %v", events, err)
	}
}
