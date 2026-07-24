package smartqueue

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

const previewLifetime = 10 * time.Minute

type Authorizer interface {
	CanManageSmartQueue(context.Context, string, string) (bool, error)
}

// Entitlements is the trusted F10 boundary. Limits are never accepted from
// browser payloads.
type Entitlements interface {
	SmartQueueLimits(context.Context, string) (PlanLimits, error)
}

// ScheduledAvailability is the read-only F7 boundary used to avoid instants
// already present in the calendar during preview.
type ScheduledAvailability interface {
	OccupiedInstants(context.Context, string, time.Time, time.Time) ([]time.Time, error)
}

type Repository interface {
	CreateQueue(context.Context, Queue, int) (Queue, error)
	GetQueue(context.Context, string, string) (Queue, error)
	UpdateQueue(context.Context, Queue, int64) (Queue, error)
	ListReservedInstants(context.Context, string, string, time.Time, time.Time) ([]time.Time, error)
	CreatePreview(context.Context, Preview) error
	Confirm(context.Context, ConfirmRequest) (Confirmation, error)
}

type Service struct {
	repository   Repository
	authorizer   Authorizer
	entitlements Entitlements
	scheduling   ScheduledAvailability
	now          func() time.Time
	random       func([]byte) error
}

type ServiceOption func(*Service)

func WithClock(clock func() time.Time) ServiceOption {
	return func(service *Service) { service.now = clock }
}

func WithRandom(random func([]byte) error) ServiceOption {
	return func(service *Service) { service.random = random }
}

func NewService(
	repository Repository,
	authorizer Authorizer,
	entitlements Entitlements,
	scheduling ScheduledAvailability,
	options ...ServiceOption,
) (*Service, error) {
	if repository == nil || authorizer == nil || entitlements == nil || scheduling == nil {
		return nil, fmt.Errorf("%w: all smart queue dependencies are required", ErrInvalidArgument)
	}
	service := &Service{
		repository: repository, authorizer: authorizer,
		entitlements: entitlements, scheduling: scheduling,
		now: time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

func (service *Service) CreateQueue(
	ctx context.Context,
	command CreateQueueCommand,
) (Queue, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Queue{}, err
	}
	limits, err := service.limits(ctx, command.WorkspaceID)
	if err != nil {
		return Queue{}, err
	}
	name, zone, windows, err := normalizeQueueDefinition(
		command.Name, command.TimeZone, command.IntervalMinutes,
		command.HorizonDays, command.Windows,
	)
	if err != nil {
		return Queue{}, err
	}
	if command.HorizonDays > limits.MaxHorizonDays {
		return Queue{}, invalidField(
			"horizon_days", "plan_limit", "horizon_plan_limit",
			fmt.Sprintf("Horizon exceeds the active plan limit of %d days.", limits.MaxHorizonDays),
		)
	}
	id, err := service.randomID("queue_")
	if err != nil {
		return Queue{}, err
	}
	now := service.now().UTC()
	queue := Queue{
		ID: id, WorkspaceID: strings.TrimSpace(command.WorkspaceID),
		Name: name, TimeZone: zone, IntervalMinutes: command.IntervalMinutes,
		HorizonDays: command.HorizonDays, Windows: windows, Revision: 1,
		CreatedBy: strings.TrimSpace(command.ActorID), CreatedAt: now, UpdatedAt: now,
	}
	return service.repository.CreateQueue(ctx, queue, limits.MaxQueues)
}

func (service *Service) UpdateQueue(
	ctx context.Context,
	command UpdateQueueCommand,
) (Queue, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Queue{}, err
	}
	if strings.TrimSpace(command.QueueID) == "" || command.ExpectedRevision < 1 {
		return Queue{}, ErrInvalidArgument
	}
	limits, err := service.limits(ctx, command.WorkspaceID)
	if err != nil {
		return Queue{}, err
	}
	name, zone, windows, err := normalizeQueueDefinition(
		command.Name, command.TimeZone, command.IntervalMinutes,
		command.HorizonDays, command.Windows,
	)
	if err != nil {
		return Queue{}, err
	}
	if command.HorizonDays > limits.MaxHorizonDays {
		return Queue{}, invalidField(
			"horizon_days", "plan_limit", "horizon_plan_limit",
			fmt.Sprintf("Horizon exceeds the active plan limit of %d days.", limits.MaxHorizonDays),
		)
	}
	current, err := service.repository.GetQueue(ctx, command.WorkspaceID, command.QueueID)
	if err != nil {
		return Queue{}, err
	}
	if current.Revision != command.ExpectedRevision {
		return Queue{}, ErrConflict
	}
	current.Name, current.TimeZone, current.Windows = name, zone, windows
	current.IntervalMinutes, current.HorizonDays = command.IntervalMinutes, command.HorizonDays
	current.Revision++
	current.UpdatedAt = service.now().UTC()
	return service.repository.UpdateQueue(ctx, current, command.ExpectedRevision)
}

func (service *Service) Preview(
	ctx context.Context,
	command PreviewCommand,
) (Preview, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Preview{}, err
	}
	limits, err := service.limits(ctx, command.WorkspaceID)
	if err != nil {
		return Preview{}, err
	}
	queue, err := service.repository.GetQueue(ctx, command.WorkspaceID, command.QueueID)
	if err != nil {
		return Preview{}, err
	}
	now := service.now().UTC()
	notBefore := command.NotBeforeUTC.UTC()
	if command.NotBeforeUTC.IsZero() || notBefore.Before(now) {
		notBefore = now
	}
	horizonDays := min(queue.HorizonDays, limits.MaxHorizonDays)
	until := now.AddDate(0, 0, horizonDays)
	if command.UntilUTC != nil && command.UntilUTC.UTC().Before(until) {
		until = command.UntilUTC.UTC()
	}
	if until.Before(notBefore) {
		return Preview{}, invalidField(
			"until_utc", "after_not_before", "search_limit_invalid",
			"Search limit must be at or after not_before_utc.",
		)
	}
	reserved, err := service.repository.ListReservedInstants(
		ctx, queue.WorkspaceID, queue.ID, notBefore, until,
	)
	if err != nil {
		return Preview{}, fmt.Errorf("list reserved smart queue instants: %w", err)
	}
	scheduled, err := service.scheduling.OccupiedInstants(
		ctx, queue.WorkspaceID, notBefore, until,
	)
	if err != nil {
		return Preview{}, fmt.Errorf("list F7 occupied instants: %w", err)
	}
	occupied := make(map[int64]struct{}, len(reserved)+len(scheduled))
	for _, instant := range append(reserved, scheduled...) {
		occupied[instant.UTC().UnixNano()] = struct{}{}
	}
	slot, err := firstAvailableSlot(queue, notBefore, until, occupied)
	if err != nil {
		return Preview{}, err
	}
	token, err := service.randomID("preview_")
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{
		Token: token, WorkspaceID: queue.WorkspaceID, QueueID: queue.ID,
		QueueRevision: queue.Revision, Slot: slot, NotBeforeUTC: notBefore,
		SearchUntilUTC: until, CreatedAt: now, ExpiresAt: now.Add(previewLifetime),
	}
	if err := service.repository.CreatePreview(ctx, preview); err != nil {
		return Preview{}, err
	}
	return preview, nil
}

func (service *Service) Confirm(
	ctx context.Context,
	command ConfirmCommand,
) (Confirmation, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Confirmation{}, err
	}
	limits, err := service.limits(ctx, command.WorkspaceID)
	if err != nil {
		return Confirmation{}, err
	}
	draftID := strings.TrimSpace(command.DraftID)
	token := strings.TrimSpace(command.PreviewToken)
	idempotencyKey := strings.TrimSpace(command.IdempotencyKey)
	channels := normalizeStrings(command.ChannelIDs)
	if strings.TrimSpace(command.QueueID) == "" || token == "" || draftID == "" ||
		idempotencyKey == "" || len(idempotencyKey) > 200 || len(channels) == 0 {
		return Confirmation{}, ErrInvalidArgument
	}
	reservationID, err := service.randomID("reservation_")
	if err != nil {
		return Confirmation{}, err
	}
	commandID, err := service.randomID("queuecmd_")
	if err != nil {
		return Confirmation{}, err
	}
	now := service.now().UTC()
	// The repository owns the preview slot. Slot values are intentionally not
	// accepted from the client.
	preview := Preview{
		Token: token, WorkspaceID: strings.TrimSpace(command.WorkspaceID),
		QueueID: strings.TrimSpace(command.QueueID),
	}
	hash := confirmationHash(preview.WorkspaceID, preview.QueueID, token, draftID, channels)
	reservation := Reservation{
		ID: reservationID, WorkspaceID: preview.WorkspaceID, QueueID: preview.QueueID,
		DraftID: draftID, ChannelIDs: channels, IdempotencyKey: idempotencyKey,
		CreatedBy: strings.TrimSpace(command.ActorID), CreatedAt: now,
	}
	schedulingCommand := SchedulingCommand{
		ID: commandID, ReservationID: reservationID, WorkspaceID: preview.WorkspaceID,
		DraftID: draftID, ChannelIDs: channels, State: "pending",
		IdempotencyKey: "f20:" + reservationID, CreatedAt: now,
	}
	return service.repository.Confirm(ctx, ConfirmRequest{
		Preview: preview, Reservation: reservation,
		SchedulingCommand: schedulingCommand, ConfirmationHash: hash,
		MaxPendingReservations: limits.MaxPendingReservations,
	})
}

func (service *Service) authorize(ctx context.Context, workspaceID, actorID string) error {
	workspaceID, actorID = strings.TrimSpace(workspaceID), strings.TrimSpace(actorID)
	if workspaceID == "" || actorID == "" {
		return ErrInvalidArgument
	}
	allowed, err := service.authorizer.CanManageSmartQueue(ctx, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("authorize smart queue: %w", err)
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (service *Service) limits(ctx context.Context, workspaceID string) (PlanLimits, error) {
	limits, err := service.entitlements.SmartQueueLimits(ctx, strings.TrimSpace(workspaceID))
	if err != nil {
		return PlanLimits{}, fmt.Errorf("resolve F10 smart queue limits: %w", err)
	}
	if !limits.Enabled {
		return PlanLimits{}, ErrFeatureDisabled
	}
	if limits.MaxQueues < 1 || limits.MaxPendingReservations < 1 ||
		limits.MaxHorizonDays < 1 || limits.MaxHorizonDays > 366 {
		return PlanLimits{}, fmt.Errorf("%w: invalid F10 smart queue limits", ErrInvalidArgument)
	}
	return limits, nil
}

func (service *Service) randomID(prefix string) (string, error) {
	bytes := make([]byte, 18)
	if err := service.random(bytes); err != nil {
		return "", fmt.Errorf("generate smart queue id: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(bytes), nil
}

func normalizeStrings(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := strings.TrimSpace(value); normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func confirmationHash(workspaceID, queueID, token, draftID string, channels []string) string {
	sum := sha256.Sum256([]byte(strings.Join(
		[]string{workspaceID, queueID, token, draftID, strings.Join(channels, "\x1f")},
		"\x00",
	)))
	return hex.EncodeToString(sum[:])
}
