package smartqueue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type authorizerStub struct{ allowed bool }

func (stub authorizerStub) CanManageSmartQueue(context.Context, string, string) (bool, error) {
	return stub.allowed, nil
}

type entitlementsStub struct {
	mutex  sync.RWMutex
	limits PlanLimits
}

func (stub *entitlementsStub) SmartQueueLimits(context.Context, string) (PlanLimits, error) {
	stub.mutex.RLock()
	defer stub.mutex.RUnlock()
	return stub.limits, nil
}

func (stub *entitlementsStub) set(limits PlanLimits) {
	stub.mutex.Lock()
	defer stub.mutex.Unlock()
	stub.limits = limits
}

type schedulingStub struct {
	occupied []time.Time
}

func (stub schedulingStub) OccupiedInstants(
	context.Context, string, time.Time, time.Time,
) ([]time.Time, error) {
	return append([]time.Time(nil), stub.occupied...), nil
}

type deterministicRandom struct {
	mutex sync.Mutex
	next  byte
}

func (random *deterministicRandom) fill(destination []byte) error {
	random.mutex.Lock()
	defer random.mutex.Unlock()
	random.next++
	for index := range destination {
		destination[index] = random.next
	}
	return nil
}

func newTestService(
	t *testing.T,
) (*Service, *MemoryRepository, *entitlementsStub, *time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	entitlements := &entitlementsStub{limits: PlanLimits{
		Enabled: true, MaxQueues: 3, MaxPendingReservations: 10, MaxHorizonDays: 30,
	}}
	random := &deterministicRandom{}
	service, err := NewService(
		repository, authorizerStub{allowed: true}, entitlements, schedulingStub{},
		WithClock(func() time.Time { return now }),
		WithRandom(random.fill),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, entitlements, &now
}

func createMondayQueue(t *testing.T, service *Service) Queue {
	t.Helper()
	queue, err := service.CreateQueue(context.Background(), CreateQueueCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", Name: "Editorial",
		TimeZone: "UTC", IntervalMinutes: 30, HorizonDays: 14,
		Windows: []RecurringWindow{{
			Weekday: Monday, StartTime: "10:00", EndTime: "11:00",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func TestPreviewAndConfirmReserveFirstSlotAndEmitF7Command(t *testing.T) {
	service, _, _, _ := newTestService(t)
	queue := createMondayQueue(t, service)
	preview, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Slot.LocalDateTime != "2026-07-27T10:00:00" ||
		preview.Slot.TimeZone != "UTC" {
		t.Fatalf("preview = %#v", preview)
	}
	confirmation, err := service.Confirm(context.Background(), ConfirmCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
		PreviewToken: preview.Token, DraftID: "draft-1",
		ChannelIDs:     []string{"instagram-1", "instagram-1"},
		IdempotencyKey: "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !confirmation.Reservation.Slot.StartsAtUTC.Equal(preview.Slot.StartsAtUTC) ||
		confirmation.SchedulingCommand.ReservationID != confirmation.Reservation.ID ||
		confirmation.SchedulingCommand.IdempotencyKey != "f20:"+confirmation.Reservation.ID ||
		len(confirmation.Reservation.ChannelIDs) != 1 {
		t.Fatalf("confirmation = %#v", confirmation)
	}
	next, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if next.Slot.LocalDateTime != "2026-07-27T10:30:00" {
		t.Fatalf("next preview = %#v", next)
	}
}

func TestConfirmIsIdempotentAndRejectsMismatchedReplay(t *testing.T) {
	service, _, _, _ := newTestService(t)
	queue := createMondayQueue(t, service)
	preview, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := ConfirmCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
		PreviewToken: preview.Token, DraftID: "draft-1",
		ChannelIDs: []string{"channel-1"}, IdempotencyKey: "request-1",
	}
	first, err := service.Confirm(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Confirm(context.Background(), command)
	if err != nil || second.Reservation.ID != first.Reservation.ID {
		t.Fatalf("second=%#v error=%v", second, err)
	}
	command.DraftID = "draft-2"
	if _, err := service.Confirm(context.Background(), command); !errors.Is(err, ErrIdempotencyReplay) {
		t.Fatalf("error = %v", err)
	}
}

func TestConcurrentConfirmationsHaveOneDeterministicWinner(t *testing.T) {
	service, _, _, _ := newTestService(t)
	queue := createMondayQueue(t, service)
	previewOne, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	previewTwo, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !previewOne.Slot.StartsAtUTC.Equal(previewTwo.Slot.StartsAtUTC) {
		t.Fatal("previews should intentionally race for the same first slot")
	}
	errorsChannel := make(chan error, 2)
	var wait sync.WaitGroup
	for index, preview := range []Preview{previewOne, previewTwo} {
		wait.Add(1)
		go func(index int, preview Preview) {
			defer wait.Done()
			_, confirmErr := service.Confirm(context.Background(), ConfirmCommand{
				WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
				PreviewToken: preview.Token, DraftID: "draft",
				ChannelIDs: []string{"channel"}, IdempotencyKey: string(rune('a' + index)),
			})
			errorsChannel <- confirmErr
		}(index, preview)
	}
	wait.Wait()
	close(errorsChannel)
	successes, conflicts := 0, 0
	for confirmErr := range errorsChannel {
		switch {
		case confirmErr == nil:
			successes++
		case errors.Is(confirmErr, ErrSlotUnavailable):
			conflicts++
		default:
			t.Fatalf("unexpected error = %v", confirmErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestConfirmRejectsStaleAndExpiredPreviews(t *testing.T) {
	service, _, _, now := newTestService(t)
	queue := createMondayQueue(t, service)
	stale, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateQueue(context.Background(), UpdateQueueCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
		ExpectedRevision: queue.Revision, Name: queue.Name, TimeZone: queue.TimeZone,
		IntervalMinutes: queue.IntervalMinutes, HorizonDays: queue.HorizonDays,
		Windows: queue.Windows,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Confirm(context.Background(), ConfirmCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
		PreviewToken: stale.Token, DraftID: "draft", ChannelIDs: []string{"channel"},
		IdempotencyKey: "stale",
	})
	if !errors.Is(err, ErrQueueChanged) {
		t.Fatalf("stale error = %v", err)
	}

	fresh, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(previewLifetime)
	_, err = service.Confirm(context.Background(), ConfirmCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
		PreviewToken: fresh.Token, DraftID: "draft", ChannelIDs: []string{"channel"},
		IdempotencyKey: "expired",
	})
	if !errors.Is(err, ErrPreviewExpired) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestPlanLimitsConstrainQueuesHorizonAndPendingReservations(t *testing.T) {
	service, _, entitlements, _ := newTestService(t)
	entitlements.set(PlanLimits{
		Enabled: true, MaxQueues: 1, MaxPendingReservations: 1, MaxHorizonDays: 14,
	})
	queue := createMondayQueue(t, service)
	_, err := service.CreateQueue(context.Background(), CreateQueueCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", Name: "Second",
		TimeZone: "UTC", IntervalMinutes: 30, HorizonDays: 14,
		Windows: []RecurringWindow{{Weekday: Tuesday, StartTime: "10:00", EndTime: "11:00"}},
	})
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("queue capacity error = %v", err)
	}
	previewOne, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	previewTwo, err := service.Preview(context.Background(), PreviewCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Confirm(context.Background(), ConfirmCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
		PreviewToken: previewOne.Token, DraftID: "draft-1", ChannelIDs: []string{"channel"},
		IdempotencyKey: "one",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Confirm(context.Background(), ConfirmCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1", QueueID: queue.ID,
		PreviewToken: previewTwo.Token, DraftID: "draft-2", ChannelIDs: []string{"channel"},
		IdempotencyKey: "two",
	})
	// Capacity is checked after queue revision and before the unique slot insert.
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("pending capacity error = %v", err)
	}
}
