package smartqueue

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryRepository struct {
	mutex        sync.RWMutex
	queues       map[string]Queue
	previews     map[string]Preview
	reservations map[string]Reservation
	commands     map[string]SchedulingCommand
	requests     map[string]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		queues: make(map[string]Queue), previews: make(map[string]Preview),
		reservations: make(map[string]Reservation), commands: make(map[string]SchedulingCommand),
		requests: make(map[string]string),
	}
}

func smartKey(workspaceID, id string) string { return workspaceID + "\x00" + id }

func (repository *MemoryRepository) CreateQueue(
	_ context.Context, queue Queue, maxQueues int,
) (Queue, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	count := 0
	for _, existing := range repository.queues {
		if existing.WorkspaceID == queue.WorkspaceID {
			count++
		}
	}
	if count >= maxQueues {
		return Queue{}, ErrCapacityExceeded
	}
	key := smartKey(queue.WorkspaceID, queue.ID)
	if _, exists := repository.queues[key]; exists {
		return Queue{}, ErrConflict
	}
	repository.queues[key] = cloneQueue(queue)
	return cloneQueue(queue), nil
}

func (repository *MemoryRepository) GetQueue(
	_ context.Context, workspaceID, queueID string,
) (Queue, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	queue, exists := repository.queues[smartKey(workspaceID, queueID)]
	if !exists {
		return Queue{}, ErrNotFound
	}
	return cloneQueue(queue), nil
}

func (repository *MemoryRepository) UpdateQueue(
	_ context.Context, queue Queue, expectedRevision int64,
) (Queue, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := smartKey(queue.WorkspaceID, queue.ID)
	current, exists := repository.queues[key]
	if !exists {
		return Queue{}, ErrNotFound
	}
	if current.Revision != expectedRevision || queue.Revision != expectedRevision+1 {
		return Queue{}, ErrConflict
	}
	queue.CreatedAt, queue.CreatedBy = current.CreatedAt, current.CreatedBy
	repository.queues[key] = cloneQueue(queue)
	return cloneQueue(queue), nil
}

func (repository *MemoryRepository) ListReservedInstants(
	_ context.Context, workspaceID, queueID string, from, until time.Time,
) ([]time.Time, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	result := make([]time.Time, 0)
	for _, reservation := range repository.reservations {
		if reservation.WorkspaceID == workspaceID && reservation.QueueID == queueID &&
			!reservation.Slot.StartsAtUTC.Before(from) && !reservation.Slot.StartsAtUTC.After(until) {
			result = append(result, reservation.Slot.StartsAtUTC)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Before(result[right]) })
	return result, nil
}

func (repository *MemoryRepository) CreatePreview(_ context.Context, preview Preview) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := smartKey(preview.WorkspaceID, preview.Token)
	if _, exists := repository.previews[key]; exists {
		return ErrConflict
	}
	repository.previews[key] = preview
	return nil
}

func (repository *MemoryRepository) Confirm(
	_ context.Context, request ConfirmRequest,
) (Confirmation, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	requestKey := smartKey(request.Reservation.WorkspaceID, request.Reservation.IdempotencyKey)
	if reservationID, exists := repository.requests[requestKey]; exists {
		reservation := repository.reservations[smartKey(request.Reservation.WorkspaceID, reservationID)]
		preview := repository.previews[smartKey(request.Preview.WorkspaceID, request.Preview.Token)]
		if preview.ConfirmationHash != request.ConfirmationHash {
			return Confirmation{}, ErrIdempotencyReplay
		}
		return Confirmation{
			Reservation: cloneReservation(reservation),
			SchedulingCommand: cloneSchedulingCommand(
				repository.commands[smartKey(reservation.WorkspaceID, reservation.ID)],
			),
		}, nil
	}
	previewKey := smartKey(request.Preview.WorkspaceID, request.Preview.Token)
	preview, exists := repository.previews[previewKey]
	if !exists || preview.QueueID != request.Preview.QueueID {
		return Confirmation{}, ErrNotFound
	}
	if !request.Reservation.CreatedAt.Before(preview.ExpiresAt) {
		return Confirmation{}, ErrPreviewExpired
	}
	if preview.ConfirmedAt != nil {
		return Confirmation{}, ErrPreviewConsumed
	}
	queue, exists := repository.queues[smartKey(preview.WorkspaceID, preview.QueueID)]
	if !exists {
		return Confirmation{}, ErrNotFound
	}
	if queue.Revision != preview.QueueRevision {
		return Confirmation{}, ErrQueueChanged
	}
	pending := 0
	for _, reservation := range repository.reservations {
		if reservation.WorkspaceID == preview.WorkspaceID {
			pending++
		}
	}
	if pending >= request.MaxPendingReservations {
		return Confirmation{}, ErrCapacityExceeded
	}
	for _, reservation := range repository.reservations {
		if reservation.QueueID == preview.QueueID &&
			reservation.Slot.StartsAtUTC.Equal(preview.Slot.StartsAtUTC) {
			return Confirmation{}, ErrSlotUnavailable
		}
	}
	reservation := cloneReservation(request.Reservation)
	reservation.Slot = preview.Slot
	command := cloneSchedulingCommand(request.SchedulingCommand)
	command.ScheduledAt = preview.Slot
	repository.reservations[smartKey(reservation.WorkspaceID, reservation.ID)] = reservation
	repository.commands[smartKey(command.WorkspaceID, reservation.ID)] = command
	repository.requests[requestKey] = reservation.ID
	confirmedAt := request.Reservation.CreatedAt
	preview.ConfirmedAt = &confirmedAt
	preview.ReservationID = reservation.ID
	preview.IdempotencyKey = reservation.IdempotencyKey
	preview.ConfirmationHash = request.ConfirmationHash
	repository.previews[previewKey] = preview
	return Confirmation{
		Reservation: cloneReservation(reservation), SchedulingCommand: cloneSchedulingCommand(command),
	}, nil
}

func (repository *MemoryRepository) MarkSchedulingCommandSent(
	_ context.Context, workspaceID, commandID string,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	for key, command := range repository.commands {
		if command.WorkspaceID == workspaceID && command.ID == commandID {
			if command.State != "pending" {
				return ErrConflict
			}
			command.State = "sent"
			repository.commands[key] = command
			return nil
		}
	}
	return ErrNotFound
}

func cloneQueue(queue Queue) Queue {
	queue.Windows = append([]RecurringWindow(nil), queue.Windows...)
	return queue
}

func cloneReservation(reservation Reservation) Reservation {
	reservation.ChannelIDs = append([]string(nil), reservation.ChannelIDs...)
	return reservation
}

func cloneSchedulingCommand(command SchedulingCommand) SchedulingCommand {
	command.ChannelIDs = append([]string(nil), command.ChannelIDs...)
	return command
}
