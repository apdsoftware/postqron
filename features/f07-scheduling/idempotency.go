package scheduling

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type OperationKind string

const (
	OperationSchedule  OperationKind = "schedule"
	OperationDuplicate OperationKind = "duplicate"
)

type OperationState string

const (
	OperationReserved     OperationState = "reserved"
	OperationPrepared     OperationState = "prepared"
	OperationCloneCreated OperationState = "clone_created"
	OperationCompleted    OperationState = "completed"
)

type ResponseSnapshotStatus string

const (
	ResponseSnapshotPending           ResponseSnapshotStatus = "pending"
	ResponseSnapshotAvailable         ResponseSnapshotStatus = "available"
	ResponseSnapshotLegacyUnavailable ResponseSnapshotStatus = "legacy_unavailable"
)

const operationLease = 5 * time.Minute

// IdempotencyOperation is the durable F7 saga record. LeaseGeneration is a
// fencing token: only its current owner may advance or complete the operation.
type IdempotencyOperation struct {
	WorkspaceID              string
	Kind                     OperationKind
	IdempotencyKey           string
	DownstreamIdempotencyKey string
	PayloadFingerprint       string
	State                    OperationState
	PostID                   string
	PublicationCommandID     string
	SourcePostID             string
	SourcePostRevision       int64
	SourceDraftID            string
	SourceDraftRevision      int64
	ChannelIDs               []string
	Schedule                 resolvedSchedule
	CloneDraftID             string
	CloneDraftRevision       int64
	LeaseGeneration          int64
	LockedUntil              time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
	CompletedAt              *time.Time
	ResponseSnapshotStatus   ResponseSnapshotStatus
	ResponseSnapshot         *ScheduledPostView
}

func normalizeIdempotencyKey(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 200 {
		return "", invalidField(
			"Idempotency-Key",
			"required_bounded_header",
			"idempotency_key_invalid",
			"Idempotency-Key must contain between 1 and 200 non-whitespace characters.",
		)
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return "", invalidField(
				"Idempotency-Key",
				"visible_ascii",
				"idempotency_key_invalid",
				"Idempotency-Key must contain visible ASCII characters only.",
			)
		}
	}
	return value, nil
}

func idempotencyKeyDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func schedulePayloadFingerprint(
	draftID string,
	channelIDs []string,
	schedule resolvedSchedule,
) (string, error) {
	channels := append([]string(nil), channelIDs...)
	sort.Strings(channels)
	return payloadFingerprint(struct {
		DraftID        string   `json:"draft_id"`
		ChannelIDs     []string `json:"channel_ids"`
		ScheduledUTC   string   `json:"scheduled_utc"`
		ScheduledLocal string   `json:"scheduled_local"`
		TimeZone       string   `json:"time_zone"`
		OffsetMinutes  int      `json:"offset_minutes"`
	}{draftID, channels, schedule.utc.Format(time.RFC3339Nano), schedule.local, schedule.timeZone, schedule.offsetMinutes})
}

func duplicatePayloadFingerprint(
	postID string,
	expectedRevision int64,
	schedule *resolvedSchedule,
) (string, error) {
	type duplicateFingerprint struct {
		PostID           string `json:"post_id"`
		ExpectedRevision int64  `json:"expected_revision"`
		ScheduleProvided bool   `json:"schedule_provided"`
		ScheduledUTC     string `json:"scheduled_utc,omitempty"`
		ScheduledLocal   string `json:"scheduled_local,omitempty"`
		TimeZone         string `json:"time_zone,omitempty"`
		OffsetMinutes    int    `json:"offset_minutes,omitempty"`
	}
	value := duplicateFingerprint{PostID: strings.TrimSpace(postID), ExpectedRevision: expectedRevision}
	if schedule != nil {
		value.ScheduleProvided = true
		value.ScheduledUTC = schedule.utc.Format(time.RFC3339Nano)
		value.ScheduledLocal = schedule.local
		value.TimeZone = schedule.timeZone
		value.OffsetMinutes = schedule.offsetMinutes
	}
	return payloadFingerprint(value)
}

func payloadFingerprint(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("fingerprint idempotent payload: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func composerDuplicateIdempotencyKey(operation IdempotencyOperation) string {
	if operation.DownstreamIdempotencyKey != "" {
		return operation.DownstreamIdempotencyKey
	}
	return deriveComposerDuplicateIdempotencyKey(operation)
}

func deriveComposerDuplicateIdempotencyKey(operation IdempotencyOperation) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		operation.WorkspaceID,
		string(operation.Kind),
		operation.IdempotencyKey,
		operation.PayloadFingerprint,
	}, "\x00")))
	return "f07_duplicate_" + hex.EncodeToString(digest[:])
}
