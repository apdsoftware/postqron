package composer

import (
	"context"
	"strings"
	"time"
)

const duplicateOperationLease = 2 * time.Minute

type duplicateOperationState string

const (
	duplicateOperationPending   duplicateOperationState = "pending"
	duplicateOperationCompleted duplicateOperationState = "completed"
)

type duplicateOperation struct {
	WorkspaceID        string
	IdempotencyKey     string
	SourceDraftID      string
	SourceRevision     int64
	CreatedByAccount   string
	Status             duplicateOperationState
	CloneDraftID       string
	CloneDraftRevision int64
	LeaseGeneration    int64
	LockedUntil        time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type duplicateOperationStore interface {
	ReserveDuplicateOperation(
		context.Context,
		duplicateOperation,
		time.Time,
	) (duplicateOperation, bool, error)
	CompleteDuplicateOperation(
		context.Context,
		duplicateOperation,
		string,
		int64,
		time.Time,
	) error
	AbandonDuplicateOperation(
		context.Context,
		duplicateOperation,
	) (bool, error)
	ResetDanglingCompletedDuplicateOperation(
		context.Context,
		duplicateOperation,
		time.Time,
	) (duplicateOperation, bool, error)
}

func normalizeIdempotencyKey(key string) string {
	return strings.TrimSpace(key)
}
