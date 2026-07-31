package scheduling

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
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
	mutex          sync.Mutex
	validatedDraft []string
	duplicates     int
	err            error
}

func (stub *contentGatewayStub) ValidateForScheduling(
	_ context.Context,
	_, _ string,
	draftID string,
	_ []string,
) error {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	stub.validatedDraft = append(stub.validatedDraft, draftID)
	return stub.err
}

func (stub *contentGatewayStub) DuplicateDraft(
	_ context.Context,
	_, _, sourceDraftID string,
) (string, error) {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	if stub.err != nil {
		return "", stub.err
	}
	stub.duplicates++
	return fmt.Sprintf("%s-copy-%d", sourceDraftID, stub.duplicates), nil
}

var testNow = time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC)

func newTestService(
	t *testing.T,
) (*Service, *MemoryRepository, *contentGatewayStub) {
	t.Helper()
	repository := NewMemoryRepository()
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
	return service, repository, content
}

func TestScheduleEditRescheduleDuplicateAndCancelInvalidateCommands(t *testing.T) {
	ctx := context.Background()
	service, repository, content := newTestService(t)
	created, err := service.SchedulePost(ctx, SchedulePostCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
		ChannelIDs:  []string{"facebook-1", "instagram-1"},
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
		PostID:           created.ID,
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID == created.ID ||
		duplicate.DraftID != "draft-2-copy-1" ||
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
		duplicateCommands[0].State != CommandPending {
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
}

func TestScheduleRejectsPastBeforeWritingPostOrCommand(t *testing.T) {
	service, repository, content := newTestService(t)
	_, err := service.SchedulePost(context.Background(), SchedulePostCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
		ChannelIDs:  []string{"instagram-1"},
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
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
		ChannelIDs:  []string{"instagram-1"},
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
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
		ChannelIDs:  []string{"facebook-1"},
		Schedule: ScheduleInput{
			LocalDateTime: "2026-07-25T09:00:00",
			TimeZone:      "UTC",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.SchedulePost(ctx, SchedulePostCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-2",
		ChannelIDs:  []string{"instagram-1"},
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
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
		ChannelIDs:  []string{"channel-1"},
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
