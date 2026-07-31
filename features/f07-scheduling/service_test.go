package scheduling

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type authorizerStub struct {
	allowed bool
	err     error
}

func (stub authorizerStub) CanManageScheduling(
	context.Context,
	string,
	string,
) (bool, error) {
	return stub.allowed, stub.err
}

type contentGatewayStub struct {
	mutex              sync.Mutex
	validatedDraft     []string
	validatedChannels  [][]string
	duplicateKeys      []string
	duplicateDrafts    map[string]int64
	duplicates         int
	validationRevision int64
	duplicateRevision  int64
	err                error
}

func (stub *contentGatewayStub) ValidateForScheduling(
	_ context.Context,
	_, _ string,
	draftID string,
	channelIDs []string,
) (ValidatedDraft, error) {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	stub.validatedDraft = append(stub.validatedDraft, draftID)
	stub.validatedChannels = append(
		stub.validatedChannels,
		append([]string(nil), channelIDs...),
	)
	if stub.err != nil {
		return ValidatedDraft{}, stub.err
	}
	revision := stub.validationRevision
	if duplicatedRevision, ok := stub.duplicateDrafts[draftID]; ok {
		revision = duplicatedRevision
	}
	if revision < 1 {
		revision = 7
	}
	return ValidatedDraft{
		DraftID:       draftID,
		DraftRevision: revision,
		ChannelIDs:    append([]string(nil), channelIDs...),
	}, nil
}

func (stub *contentGatewayStub) DuplicateDraft(
	_ context.Context,
	_, _, sourceDraftID string,
	_ int64,
	idempotencyKey string,
) (DuplicatedDraft, error) {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	if stub.err != nil {
		return DuplicatedDraft{}, stub.err
	}
	stub.duplicates++
	stub.duplicateKeys = append(stub.duplicateKeys, idempotencyKey)
	revision := stub.duplicateRevision
	if revision < 1 {
		revision = 1
	}
	draftID := fmt.Sprintf("%s-copy-%d", sourceDraftID, stub.duplicates)
	if stub.duplicateDrafts == nil {
		stub.duplicateDrafts = make(map[string]int64)
	}
	stub.duplicateDrafts[draftID] = revision
	return DuplicatedDraft{
		DraftID:       draftID,
		DraftRevision: revision,
	}, nil
}

var testNow = time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)

func newTestService(
	t *testing.T,
) (*Service, *MemoryRepository, *contentGatewayStub) {
	t.Helper()
	repository := NewMemoryRepository()
	service, content := newTestServiceWithRepository(t, repository)
	return service, repository, content
}

func newTestServiceWithRepository(
	t *testing.T,
	repository Repository,
) (*Service, *contentGatewayStub) {
	t.Helper()
	content := &contentGatewayStub{}
	var sequence atomic.Uint64
	service, err := NewService(
		repository,
		authorizerStub{allowed: true},
		content,
		WithClock(func() time.Time { return testNow }),
		WithRandom(func(destination []byte) error {
			value := sequence.Add(1)
			for index := range destination {
				destination[index] = byte(index + 1)
			}
			binary.BigEndian.PutUint64(destination[len(destination)-8:], value)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, content
}

type repositoryFaults struct {
	Repository
	failScheduleAfterCommit   atomic.Bool
	failDuplicateBeforeCommit atomic.Bool
	failDuplicateAfterCommit  atomic.Bool
	beforeDuplicateComplete   func()
}

func (repository *repositoryFaults) CompleteScheduleOperation(
	ctx context.Context,
	operation IdempotencyOperation,
	post ScheduledPost,
	command PublicationCommand,
	now time.Time,
) (ScheduledPost, error) {
	created, err := repository.Repository.CompleteScheduleOperation(
		ctx,
		operation,
		post,
		command,
		now,
	)
	if err == nil && repository.failScheduleAfterCommit.CompareAndSwap(true, false) {
		return ScheduledPost{}, errors.New("ambiguous response after committed schedule")
	}
	return created, err
}

func (repository *repositoryFaults) CompleteDuplicateOperation(
	ctx context.Context,
	operation IdempotencyOperation,
	post ScheduledPost,
	command PublicationCommand,
	now time.Time,
) (ScheduledPost, error) {
	if repository.beforeDuplicateComplete != nil {
		repository.beforeDuplicateComplete()
	}
	if repository.failDuplicateBeforeCommit.CompareAndSwap(true, false) {
		return ScheduledPost{}, errors.New("injected failure after clone before F7 commit")
	}
	created, err := repository.Repository.CompleteDuplicateOperation(
		ctx,
		operation,
		post,
		command,
		now,
	)
	if err == nil && repository.failDuplicateAfterCommit.CompareAndSwap(true, false) {
		return ScheduledPost{}, errors.New("ambiguous response after committed duplicate")
	}
	return created, err
}

func TestScheduleEditRescheduleDuplicateAndCancelInvalidateCommands(t *testing.T) {
	ctx := context.Background()
	service, repository, content := newTestService(t)
	created, err := service.SchedulePost(ctx, SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "schedule-lifecycle-1",
		DraftID:        "draft-1",
		ChannelIDs:     []string{"facebook-1", "instagram-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-25T10:00:00",
			TimeZone:      "Europe/Rome",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstCommandID := created.ActiveCommandID
	if created.Revision != 1 ||
		created.DraftRevision != 7 ||
		created.Status != StatusScheduled ||
		created.ScheduledForUTC.Format(time.RFC3339) != "2026-07-25T08:00:00Z" ||
		created.TimeZone != "Europe/Rome" ||
		created.UTCOffsetMinutes != 120 {
		t.Fatalf("created post = %#v", created)
	}

	edited, err := service.EditPost(ctx, EditPostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		PostID:           created.ID,
		ExpectedRevision: 1,
		DraftID:          "draft-2",
		ChannelIDs:       []string{"instagram-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstCommand, err := repository.GetPublicationCommand(
		ctx,
		"workspace-1",
		firstCommandID,
	)
	if err != nil || firstCommand.State != CommandInvalidated ||
		firstCommand.InvalidatedAt == nil {
		t.Fatalf("first command = %#v, err = %v", firstCommand, err)
	}
	secondCommandID := edited.ActiveCommandID

	rescheduled, err := service.ReschedulePost(ctx, ReschedulePostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		PostID:           created.ID,
		ExpectedRevision: 2,
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-26T09:15:00",
			TimeZone:      "America/New_York",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondCommand, err := repository.GetPublicationCommand(
		ctx,
		"workspace-1",
		secondCommandID,
	)
	if err != nil || secondCommand.State != CommandInvalidated {
		t.Fatalf("second command = %#v, err = %v", secondCommand, err)
	}
	if rescheduled.Revision != 3 ||
		rescheduled.ScheduledForUTC.Format(time.RFC3339) != "2026-07-26T13:15:00Z" {
		t.Fatalf("rescheduled = %#v", rescheduled)
	}

	duplicate, err := service.DuplicatePost(ctx, DuplicatePostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		IdempotencyKey:   "duplicate-lifecycle-1",
		PostID:           created.ID,
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID == created.ID ||
		duplicate.DraftID != "draft-2-copy-1" ||
		duplicate.DraftRevision != 1 ||
		duplicate.DuplicatedFromPostID != created.ID ||
		duplicate.ActiveCommandID == "" {
		t.Fatalf("duplicate = %#v", duplicate)
	}

	cancelled, err := service.CancelPost(ctx, CancelPostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		PostID:           created.ID,
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != StatusCancelled ||
		cancelled.Revision != 4 ||
		cancelled.ActiveCommandID != "" ||
		cancelled.CancelledAt == nil {
		t.Fatalf("cancelled = %#v", cancelled)
	}
	commands, err := repository.ListPublicationCommands(ctx, "workspace-1", created.ID)
	if err != nil || len(commands) != 3 {
		t.Fatalf("commands = %#v, err = %v", commands, err)
	}
	for _, command := range commands {
		if command.State != CommandInvalidated || command.InvalidatedAt == nil {
			t.Fatalf("old command remains executable: %#v", command)
		}
	}
	duplicateCommands, err := repository.ListPublicationCommands(
		ctx,
		"workspace-1",
		duplicate.ID,
	)
	if err != nil || len(duplicateCommands) != 1 ||
		duplicateCommands[0].State != CommandPending ||
		duplicateCommands[0].DraftRevision != duplicate.DraftRevision {
		t.Fatalf("duplicate commands = %#v, err = %v", duplicateCommands, err)
	}
	for _, command := range append(commands, duplicateCommands...) {
		want := fmt.Sprintf("%s:%d", command.PostID, command.Generation)
		if command.InvalidationKey != want {
			t.Fatalf(
				"command idempotency key = %q, want %q",
				command.InvalidationKey,
				want,
			)
		}
	}
	if len(content.validatedDraft) != 3 {
		t.Fatalf("validated drafts = %#v", content.validatedDraft)
	}
	if len(content.duplicateKeys) != 1 || content.duplicateKeys[0] == "" {
		t.Fatalf("duplicate keys = %#v", content.duplicateKeys)
	}
}

func TestScheduleRetryAfterAmbiguousCommitReplaysAndRejectsPayloadMismatch(t *testing.T) {
	base := NewMemoryRepository()
	faults := &repositoryFaults{Repository: base}
	faults.failScheduleAfterCommit.Store(true)
	service, content := newTestServiceWithRepository(t, faults)
	command := SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "test-key-1",
		DraftID:        "draft-1",
		ChannelIDs:     []string{"instagram-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-25T10:00:00",
			TimeZone:      "UTC",
		},
	}
	if _, err := service.SchedulePost(context.Background(), command); err == nil {
		t.Fatal("first request should observe the injected ambiguous response")
	}
	replayed, err := service.SchedulePost(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.IdempotencyReplayed || len(base.posts) != 1 || len(base.commands) != 1 {
		t.Fatalf("replayed=%#v posts=%d commands=%d", replayed, len(base.posts), len(base.commands))
	}
	if len(content.validatedDraft) != 1 {
		t.Fatalf("composer validation calls=%d, want one", len(content.validatedDraft))
	}
	mismatched := command
	mismatched.Schedule.LocalDateTime = "2026-07-25T11:00:00"
	if _, err := service.SchedulePost(context.Background(), mismatched); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("payload mismatch error=%v", err)
	}
	otherAuthorizedMember := command
	otherAuthorizedMember.ActorID = "account-2"
	if replay, err := service.SchedulePost(context.Background(), otherAuthorizedMember); err != nil ||
		!replay.IdempotencyReplayed {
		t.Fatalf("workspace-scoped replay=%#v error=%v", replay, err)
	}
}

func TestDuplicateSagaRecoversCloneAfterFailureBeforeF7Commit(t *testing.T) {
	base := NewMemoryRepository()
	faults := &repositoryFaults{Repository: base}
	service, content := newTestServiceWithRepository(t, faults)
	source, err := service.SchedulePost(context.Background(), SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "schedule-duplicate-source-1",
		DraftID:        "draft-1",
		ChannelIDs:     []string{"instagram-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-25T10:00:00",
			TimeZone:      "UTC",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	faults.failDuplicateBeforeCommit.Store(true)
	command := DuplicatePostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		IdempotencyKey:   "duplicate-recovery-1",
		PostID:           source.ID,
		ExpectedRevision: source.Revision,
	}
	if _, err := service.DuplicatePost(context.Background(), command); err == nil {
		t.Fatal("first duplicate should fail after the F6 clone recovery point")
	}
	if content.duplicates != 1 {
		t.Fatalf("clones after injected failure=%d, want one", content.duplicates)
	}
	duplicate, err := service.DuplicatePost(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.DuplicatedFromPostID != source.ID || content.duplicates != 1 {
		t.Fatalf("duplicate=%#v clone calls=%d", duplicate, content.duplicates)
	}
	replay, err := service.DuplicatePost(context.Background(), command)
	if err != nil || replay.ID != duplicate.ID || !replay.IdempotencyReplayed {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	mismatch := command
	mismatch.Schedule = &ScheduleInput{
		LocalDateTime: "2026-07-26T10:00:00",
		TimeZone:      "UTC",
	}
	if _, err := service.DuplicatePost(context.Background(), mismatch); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("duplicate payload mismatch error=%v", err)
	}
}

func TestDuplicateCompletionRejectsSourceEditOrCancelAndKeepsCloneReachable(t *testing.T) {
	for _, mutation := range []string{"edit", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			base := NewMemoryRepository()
			faults := &repositoryFaults{Repository: base}
			service, content := newTestServiceWithRepository(t, faults)
			source, err := service.SchedulePost(context.Background(), SchedulePostCommand{
				WorkspaceID: "workspace-1", ActorID: "account-1",
				IdempotencyKey: "race-source-" + mutation, DraftID: "draft-1",
				ChannelIDs: []string{"instagram-1"},
				Schedule: ScheduleInput{
					LocalDateTime: "2026-07-25T10:00:00",
					TimeZone:      "UTC",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			var mutationErr error
			var once sync.Once
			faults.beforeDuplicateComplete = func() {
				once.Do(func() {
					if mutation == "cancel" {
						_, mutationErr = base.Cancel(
							context.Background(), source.WorkspaceID, source.ID,
							source.Revision, testNow.Add(time.Minute),
						)
						return
					}
					replacement := clonePost(source)
					replacement.DraftID = "draft-edited"
					replacement.DraftRevision++
					replacement.Revision++
					replacement.ActiveCommandID = "pubcmd_race_edit"
					replacement.UpdatedAt = testNow.Add(time.Minute)
					_, mutationErr = base.Replace(
						context.Background(), replacement, source.Revision,
						commandFor(replacement, replacement.ActiveCommandID, replacement.UpdatedAt),
					)
				})
			}
			command := DuplicatePostCommand{
				WorkspaceID: source.WorkspaceID, ActorID: "account-1",
				IdempotencyKey: "race-duplicate-" + mutation,
				PostID:         source.ID, ExpectedRevision: source.Revision,
			}
			if _, err := service.DuplicatePost(context.Background(), command); !errors.Is(err, ErrConflict) {
				t.Fatalf("duplicate error=%v", err)
			}
			if mutationErr != nil {
				t.Fatalf("source mutation error=%v", mutationErr)
			}
			operation := base.operations[operationKey(
				source.WorkspaceID,
				OperationDuplicate,
				idempotencyKeyDigest(command.IdempotencyKey),
			)]
			_, duplicatePostExists := base.posts[schedulingKey(source.WorkspaceID, operation.PostID)]
			_, duplicateCommandExists := base.commands[schedulingKey(source.WorkspaceID, operation.PublicationCommandID)]
			if operation.State != OperationCloneCreated ||
				operation.CloneDraftID == "" || duplicatePostExists ||
				duplicateCommandExists || content.duplicates != 1 {
				t.Fatalf("operation=%#v posts=%d commands=%d clones=%d", operation, len(base.posts), len(base.commands), content.duplicates)
			}
			if _, err := service.DuplicatePost(context.Background(), command); !errors.Is(err, ErrConflict) || content.duplicates != 1 {
				t.Fatalf("recovery conflict=%v clone calls=%d", err, content.duplicates)
			}
		})
	}
}

func TestDuplicateRetryAfterAmbiguousCommitReplaysSamePostAndClone(t *testing.T) {
	base := NewMemoryRepository()
	faults := &repositoryFaults{Repository: base}
	service, content := newTestServiceWithRepository(t, faults)
	source, err := service.SchedulePost(context.Background(), SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "schedule-duplicate-ambiguous-source",
		DraftID:        "draft-1",
		ChannelIDs:     []string{"channel-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-25T10:00:00",
			TimeZone:      "UTC",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	faults.failDuplicateAfterCommit.Store(true)
	command := DuplicatePostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		IdempotencyKey:   "test-key-1",
		PostID:           source.ID,
		ExpectedRevision: source.Revision,
	}
	if _, err := service.DuplicatePost(context.Background(), command); err == nil {
		t.Fatal("expected ambiguous duplicate response")
	}
	replay, err := service.DuplicatePost(context.Background(), command)
	if err != nil || !replay.IdempotencyReplayed || content.duplicates != 1 {
		t.Fatalf("replay=%#v err=%v clone calls=%d", replay, err, content.duplicates)
	}
}

func TestIdempotencyLeaseGenerationFencesStaleOwner(t *testing.T) {
	repository := NewMemoryRepository()
	candidate := IdempotencyOperation{
		WorkspaceID:          "workspace-1",
		Kind:                 OperationDuplicate,
		IdempotencyKey:       "duplicate-fencing-1",
		PayloadFingerprint:   strings.Repeat("a", 64),
		PostID:               "post_fencing",
		PublicationCommandID: "pubcmd_fencing",
	}
	stale, err := repository.ReserveOperation(context.Background(), candidate, testNow)
	if err != nil {
		t.Fatal(err)
	}
	current, err := repository.ReserveOperation(
		context.Background(),
		candidate,
		testNow.Add(operationLease),
	)
	if err != nil {
		t.Fatal(err)
	}
	source := ScheduledPost{
		ID: "post_source", WorkspaceID: "workspace-1", DraftID: "draft-1",
		DraftRevision: 1, Revision: 1, ChannelIDs: []string{"channel-1"},
		Status: StatusScheduled,
	}
	repository.posts[schedulingKey(source.WorkspaceID, source.ID)] = clonePost(source)
	schedule := resolvedSchedule{utc: testNow.Add(time.Hour), local: "2026-07-24T17:00:00", timeZone: "UTC"}
	if _, err := repository.PrepareDuplicateOperation(
		context.Background(), stale, source, schedule, testNow,
	); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("stale owner error=%v", err)
	}
	if _, err := repository.PrepareDuplicateOperation(
		context.Background(), current, source, schedule, testNow,
	); err != nil {
		t.Fatalf("current owner error=%v", err)
	}
}

func TestScheduleRejectsPastBeforeWritingPostOrCommand(t *testing.T) {
	service, repository, content := newTestService(t)
	_, err := service.SchedulePost(context.Background(), SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "test-key-1",
		DraftID:        "draft-1",
		ChannelIDs:     []string{"instagram-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-24T11:59:00",
			TimeZone:      "America/New_York",
		},
	})
	assertFieldCode(t, err, "scheduled_time_not_future")
	if len(repository.posts) != 0 || len(repository.commands) != 0 {
		t.Fatalf(
			"past schedule wrote state: posts=%d commands=%d",
			len(repository.posts),
			len(repository.commands),
		)
	}
	if len(content.validatedDraft) != 0 {
		t.Fatal("past schedule should fail before invoking composer validation")
	}
}

func TestConcurrentRescheduleAllowsOneWinnerAndOnePendingCommand(t *testing.T) {
	ctx := context.Background()
	service, repository, _ := newTestService(t)
	created, err := service.SchedulePost(ctx, SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "schedule-concurrent-reschedule-1",
		DraftID:        "draft-1",
		ChannelIDs:     []string{"instagram-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-25T10:00:00",
			TimeZone:      "UTC",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	const contenders = 32
	var successes atomic.Int64
	var unexpected atomic.Int64
	var waitGroup sync.WaitGroup
	waitGroup.Add(contenders)
	for contender := 0; contender < contenders; contender++ {
		contender := contender
		go func() {
			defer waitGroup.Done()
			_, rescheduleErr := service.ReschedulePost(ctx, ReschedulePostCommand{
				WorkspaceID:      "workspace-1",
				ActorID:          "account-1",
				PostID:           created.ID,
				ExpectedRevision: 1,
				Schedule: ScheduleInput{
					LocalDateTime: fmt.Sprintf(
						"2026-07-26T%02d:%02d:00",
						8+contender/12,
						(contender%12)*5,
					),
					TimeZone: "UTC",
				},
			})
			switch {
			case rescheduleErr == nil:
				successes.Add(1)
			case errors.Is(rescheduleErr, ErrConflict):
			default:
				unexpected.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	if successes.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf(
			"successes=%d unexpected=%d",
			successes.Load(),
			unexpected.Load(),
		)
	}
	commands, err := repository.ListPublicationCommands(ctx, "workspace-1", created.ID)
	if err != nil || len(commands) != 2 {
		t.Fatalf("commands = %#v, err = %v", commands, err)
	}
	var pending, invalidated int
	for _, command := range commands {
		switch command.State {
		case CommandPending:
			pending++
		case CommandInvalidated:
			invalidated++
		}
	}
	if pending != 1 || invalidated != 1 {
		t.Fatalf("pending=%d invalidated=%d commands=%#v", pending, invalidated, commands)
	}
}

func TestCalendarFiltersByDateChannelAndStatus(t *testing.T) {
	ctx := context.Background()
	service, _, _ := newTestService(t)
	first, err := service.SchedulePost(ctx, SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "schedule-calendar-1",
		DraftID:        "draft-1",
		ChannelIDs:     []string{"facebook-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-25T09:00:00",
			TimeZone:      "UTC",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SchedulePost(ctx, SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "schedule-calendar-2",
		DraftID:        "draft-2",
		ChannelIDs:     []string{"instagram-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-26T09:00:00",
			TimeZone:      "UTC",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelPost(ctx, CancelPostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		PostID:           first.ID,
		ExpectedRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}

	entries, err := service.Calendar(
		ctx,
		"workspace-1",
		"account-1",
		CalendarFilter{
			FromUTC:   time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC),
			UntilUTC:  time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC),
			ChannelID: "facebook-1",
			Status:    StatusCancelled,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 ||
		entries[0].PostID != first.ID ||
		entries[0].Status != StatusCancelled ||
		entries[0].ChannelIDs[0] != "facebook-1" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestServiceAuthorizationAndImmutablePost(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	created, err := service.SchedulePost(ctx, SchedulePostCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		IdempotencyKey: "schedule-immutable-1",
		DraftID:        "draft-1",
		ChannelIDs:     []string{"channel-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-25T09:00:00",
			TimeZone:      "UTC",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.CancelPost(ctx, CancelPostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		PostID:           created.ID,
		ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ReschedulePost(ctx, ReschedulePostCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		PostID:           created.ID,
		ExpectedRevision: cancelled.Revision,
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-27T09:00:00",
			TimeZone:      "UTC",
		},
	})
	if !errors.Is(err, ErrImmutable) {
		t.Fatalf("immutable reschedule error = %v", err)
	}

	denied, err := NewService(
		NewMemoryRepository(),
		authorizerStub{},
		&contentGatewayStub{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = denied.Calendar(ctx, "workspace-1", "account-1", CalendarFilter{
		FromUTC:  testNow,
		UntilUTC: testNow.Add(time.Hour),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("authorization error = %v", err)
	}
}
