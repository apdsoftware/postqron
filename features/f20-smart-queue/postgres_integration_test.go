package smartqueue

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresConfirmationIsAtomicUnderSlotContention(t *testing.T) {
	databaseURL := os.Getenv("F20_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F20_DATABASE_URL is not configured")
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
	workspaceID := "workspace-f20-" + suffix
	queue := Queue{
		ID: "queue_integration_" + suffix, WorkspaceID: workspaceID,
		Name: "Integration", TimeZone: "UTC", IntervalMinutes: 30, HorizonDays: 14,
		Windows: []RecurringWindow{{
			Weekday: Monday, StartTime: "10:00", EndTime: "11:00",
		}},
		Revision: 1, CreatedBy: "account-integration",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	queue.UpdatedAt = queue.CreatedAt
	if _, err := repository.CreateQueue(ctx, queue, 10); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM f20_scheduling_commands WHERE workspace_id = $1", workspaceID)
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM f20_slot_previews WHERE workspace_id = $1", workspaceID)
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM f20_slot_reservations WHERE workspace_id = $1", workspaceID)
		_, _ = pool.Exec(context.Background(),
			"DELETE FROM f20_smart_queues WHERE workspace_id = $1", workspaceID)
	})

	slot := Slot{
		StartsAtUTC:   time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second),
		LocalDateTime: "2026-07-27T10:00:00", TimeZone: "UTC",
	}
	now := time.Now().UTC()
	previews := []Preview{
		{Token: "preview_one_" + suffix, WorkspaceID: workspaceID, QueueID: queue.ID,
			QueueRevision: 1, Slot: slot, NotBeforeUTC: now,
			SearchUntilUTC: now.Add(48 * time.Hour), CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{Token: "preview_two_" + suffix, WorkspaceID: workspaceID, QueueID: queue.ID,
			QueueRevision: 1, Slot: slot, NotBeforeUTC: now,
			SearchUntilUTC: now.Add(48 * time.Hour), CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	for _, preview := range previews {
		if err := repository.CreatePreview(ctx, preview); err != nil {
			t.Fatal(err)
		}
	}

	var wait sync.WaitGroup
	results := make(chan error, len(previews))
	for index, preview := range previews {
		wait.Add(1)
		go func(index int, preview Preview) {
			defer wait.Done()
			reservation := Reservation{
				ID:          "reservation_integration_" + string(rune('a'+index)) + suffix,
				WorkspaceID: workspaceID, QueueID: queue.ID, DraftID: "draft",
				ChannelIDs: []string{"channel"}, IdempotencyKey: "request-" + string(rune('a'+index)),
				CreatedBy: "account", CreatedAt: now,
			}
			command := SchedulingCommand{
				ID:            "queuecmd_integration_" + string(rune('a'+index)) + suffix,
				ReservationID: reservation.ID, WorkspaceID: workspaceID,
				DraftID: "draft", ChannelIDs: []string{"channel"}, State: "pending",
				IdempotencyKey: "f20:" + reservation.ID, CreatedAt: now,
			}
			_, confirmErr := repository.Confirm(ctx, ConfirmRequest{
				Preview: preview, Reservation: reservation, SchedulingCommand: command,
				ConfirmationHash: confirmationHash(
					workspaceID, queue.ID, preview.Token, "draft", []string{"channel"},
				),
				MaxPendingReservations: 10,
			})
			results <- confirmErr
		}(index, preview)
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for confirmErr := range results {
		switch {
		case confirmErr == nil:
			successes++
		case errors.Is(confirmErr, ErrSlotUnavailable):
			conflicts++
		default:
			t.Fatalf("unexpected confirmation error: %v", confirmErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}
