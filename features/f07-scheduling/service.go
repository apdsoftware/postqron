package scheduling

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

type Authorizer interface {
	CanManageScheduling(context.Context, string, string) (bool, error)
}

// ContentGateway is the explicit F6 boundary. Validation enforces composer
// channel/media rules; duplication creates an independent draft snapshot.
type ContentGateway interface {
	ValidateForScheduling(context.Context, string, string, string, []string) error
	DuplicateDraft(context.Context, string, string, string) (string, error)
}

type Repository interface {
	Create(context.Context, ScheduledPost, PublicationCommand) (ScheduledPost, error)
	Get(context.Context, string, string) (ScheduledPost, error)
	List(context.Context, string, CalendarFilter) ([]ScheduledPost, error)
	Replace(
		context.Context,
		ScheduledPost,
		int64,
		PublicationCommand,
	) (ScheduledPost, error)
	Cancel(context.Context, string, string, int64, time.Time) (ScheduledPost, error)
	Duplicate(
		context.Context,
		string,
		string,
		int64,
		ScheduledPost,
		PublicationCommand,
	) (ScheduledPost, error)
	GetPublicationCommand(context.Context, string, string) (PublicationCommand, error)
	ListPublicationCommands(context.Context, string, string) ([]PublicationCommand, error)
}

type Service struct {
	repository Repository
	authorizer Authorizer
	content    ContentGateway
	now        func() time.Time
	random     func([]byte) error
}

type ServiceOption func(*Service)

func WithClock(clock func() time.Time) ServiceOption {
	return func(service *Service) {
		service.now = clock
	}
}

func WithRandom(random func([]byte) error) ServiceOption {
	return func(service *Service) {
		service.random = random
	}
}

func NewService(
	repository Repository,
	authorizer Authorizer,
	content ContentGateway,
	options ...ServiceOption,
) (*Service, error) {
	if repository == nil || authorizer == nil || content == nil {
		return nil, fmt.Errorf(
			"%w: repository, authorizer and content gateway are required",
			ErrInvalidArgument,
		)
	}
	service := &Service{
		repository: repository,
		authorizer: authorizer,
		content:    content,
		now:        time.Now,
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

func (service *Service) SchedulePost(
	ctx context.Context,
	command SchedulePostCommand,
) (ScheduledPost, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return ScheduledPost{}, err
	}
	draftID, channels, err := normalizeContentSelection(command.DraftID, command.ChannelIDs)
	if err != nil {
		return ScheduledPost{}, err
	}
	schedule, err := resolveSchedule(command.Schedule)
	if err != nil {
		return ScheduledPost{}, err
	}
	now := service.now().UTC()
	if err := requireFuture(schedule, now); err != nil {
		return ScheduledPost{}, err
	}
	if err := service.content.ValidateForScheduling(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		draftID,
		channels,
	); err != nil {
		return ScheduledPost{}, fmt.Errorf("validate draft for scheduling: %w", err)
	}

	postID, err := service.randomID("post")
	if err != nil {
		return ScheduledPost{}, err
	}
	commandID, err := service.randomID("pubcmd")
	if err != nil {
		return ScheduledPost{}, err
	}
	post := newScheduledPost(
		postID,
		commandID,
		command.WorkspaceID,
		command.ActorID,
		draftID,
		channels,
		schedule,
		1,
		now,
	)
	publicationCommand := commandFor(post, commandID, now)
	return service.repository.Create(ctx, post, publicationCommand)
}

func (service *Service) GetPost(
	ctx context.Context,
	workspaceID, actorID, postID string,
) (ScheduledPost, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return ScheduledPost{}, err
	}
	if strings.TrimSpace(postID) == "" {
		return ScheduledPost{}, invalidField(
			"post_id",
			"required",
			"post_id_required",
			"Post id is required.",
		)
	}
	return service.repository.Get(ctx, workspaceID, postID)
}

func (service *Service) Calendar(
	ctx context.Context,
	workspaceID, actorID string,
	filter CalendarFilter,
) ([]CalendarEntry, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return nil, err
	}
	normalized, err := normalizeCalendarFilter(filter)
	if err != nil {
		return nil, err
	}
	posts, err := service.repository.List(ctx, workspaceID, normalized)
	if err != nil {
		return nil, err
	}
	entries := make([]CalendarEntry, len(posts))
	for index, post := range posts {
		entries[index] = calendarEntry(post)
	}
	return entries, nil
}

func (service *Service) EditPost(
	ctx context.Context,
	command EditPostCommand,
) (ScheduledPost, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return ScheduledPost{}, err
	}
	if err := validateMutation(command.PostID, command.ExpectedRevision); err != nil {
		return ScheduledPost{}, err
	}
	draftID, channels, err := normalizeContentSelection(command.DraftID, command.ChannelIDs)
	if err != nil {
		return ScheduledPost{}, err
	}
	if err := service.content.ValidateForScheduling(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		draftID,
		channels,
	); err != nil {
		return ScheduledPost{}, fmt.Errorf("validate edited draft for scheduling: %w", err)
	}
	current, err := service.repository.Get(ctx, command.WorkspaceID, command.PostID)
	if err != nil {
		return ScheduledPost{}, err
	}
	if current.Revision != command.ExpectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if current.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}

	now := service.now().UTC()
	commandID, err := service.randomID("pubcmd")
	if err != nil {
		return ScheduledPost{}, err
	}
	replacement := clonePost(current)
	replacement.DraftID = draftID
	replacement.ChannelIDs = channels
	replacement.Revision = current.Revision + 1
	replacement.ActiveCommandID = commandID
	replacement.UpdatedAt = now
	return service.repository.Replace(
		ctx,
		replacement,
		command.ExpectedRevision,
		commandFor(replacement, commandID, now),
	)
}

func (service *Service) ReschedulePost(
	ctx context.Context,
	command ReschedulePostCommand,
) (ScheduledPost, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return ScheduledPost{}, err
	}
	if err := validateMutation(command.PostID, command.ExpectedRevision); err != nil {
		return ScheduledPost{}, err
	}
	schedule, err := resolveSchedule(command.Schedule)
	if err != nil {
		return ScheduledPost{}, err
	}
	now := service.now().UTC()
	if err := requireFuture(schedule, now); err != nil {
		return ScheduledPost{}, err
	}
	current, err := service.repository.Get(ctx, command.WorkspaceID, command.PostID)
	if err != nil {
		return ScheduledPost{}, err
	}
	if current.Revision != command.ExpectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if current.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}

	commandID, err := service.randomID("pubcmd")
	if err != nil {
		return ScheduledPost{}, err
	}
	replacement := clonePost(current)
	applySchedule(&replacement, schedule)
	replacement.Revision = current.Revision + 1
	replacement.ActiveCommandID = commandID
	replacement.UpdatedAt = now
	return service.repository.Replace(
		ctx,
		replacement,
		command.ExpectedRevision,
		commandFor(replacement, commandID, now),
	)
}

func (service *Service) DuplicatePost(
	ctx context.Context,
	command DuplicatePostCommand,
) (ScheduledPost, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return ScheduledPost{}, err
	}
	if err := validateMutation(command.PostID, command.ExpectedRevision); err != nil {
		return ScheduledPost{}, err
	}
	source, err := service.repository.Get(ctx, command.WorkspaceID, command.PostID)
	if err != nil {
		return ScheduledPost{}, err
	}
	if source.Revision != command.ExpectedRevision {
		return ScheduledPost{}, ErrConflict
	}
	if source.Status != StatusScheduled {
		return ScheduledPost{}, ErrImmutable
	}

	schedule := resolvedSchedule{
		utc:           source.ScheduledForUTC,
		local:         source.ScheduledLocal,
		timeZone:      source.TimeZone,
		offsetMinutes: source.UTCOffsetMinutes,
	}
	if command.Schedule != nil {
		schedule, err = resolveSchedule(*command.Schedule)
		if err != nil {
			return ScheduledPost{}, err
		}
	}
	now := service.now().UTC()
	if err := requireFuture(schedule, now); err != nil {
		return ScheduledPost{}, err
	}
	duplicatedDraftID, err := service.content.DuplicateDraft(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		source.DraftID,
	)
	if err != nil {
		return ScheduledPost{}, fmt.Errorf("duplicate scheduled draft: %w", err)
	}
	duplicatedDraftID = strings.TrimSpace(duplicatedDraftID)
	if duplicatedDraftID == "" {
		return ScheduledPost{}, fmt.Errorf(
			"%w: content gateway returned an empty duplicated draft id",
			ErrInvalidArgument,
		)
	}
	if err := service.content.ValidateForScheduling(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		duplicatedDraftID,
		source.ChannelIDs,
	); err != nil {
		return ScheduledPost{}, fmt.Errorf("validate duplicated draft: %w", err)
	}

	postID, err := service.randomID("post")
	if err != nil {
		return ScheduledPost{}, err
	}
	commandID, err := service.randomID("pubcmd")
	if err != nil {
		return ScheduledPost{}, err
	}
	duplicate := newScheduledPost(
		postID,
		commandID,
		command.WorkspaceID,
		command.ActorID,
		duplicatedDraftID,
		source.ChannelIDs,
		schedule,
		1,
		now,
	)
	duplicate.DuplicatedFromPostID = source.ID
	return service.repository.Duplicate(
		ctx,
		command.WorkspaceID,
		command.PostID,
		command.ExpectedRevision,
		duplicate,
		commandFor(duplicate, commandID, now),
	)
}

func (service *Service) CancelPost(
	ctx context.Context,
	command CancelPostCommand,
) (ScheduledPost, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return ScheduledPost{}, err
	}
	if err := validateMutation(command.PostID, command.ExpectedRevision); err != nil {
		return ScheduledPost{}, err
	}
	return service.repository.Cancel(
		ctx,
		command.WorkspaceID,
		command.PostID,
		command.ExpectedRevision,
		service.now().UTC(),
	)
}

func (service *Service) authorize(
	ctx context.Context,
	workspaceID, actorID string,
) error {
	if strings.TrimSpace(actorID) == "" {
		return ErrUnauthenticated
	}
	if strings.TrimSpace(workspaceID) == "" {
		return invalidField(
			"workspace_id",
			"required",
			"workspace_id_required",
			"Workspace id is required.",
		)
	}
	allowed, err := service.authorizer.CanManageScheduling(ctx, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("authorize scheduling: %w", err)
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (service *Service) randomID(prefix string) (string, error) {
	randomBytes := make([]byte, 18)
	if err := service.random(randomBytes); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(randomBytes), nil
}

func normalizeContentSelection(draftID string, channelIDs []string) (string, []string, error) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return "", nil, invalidField(
			"draft_id",
			"required",
			"draft_id_required",
			"Draft id is required.",
		)
	}
	normalized := make([]string, 0, len(channelIDs))
	seen := make(map[string]struct{}, len(channelIDs))
	for index, channelID := range channelIDs {
		channelID = strings.TrimSpace(channelID)
		if channelID == "" {
			return "", nil, invalidField(
				fmt.Sprintf("channel_ids[%d]", index),
				"required",
				"channel_id_required",
				"Channel id is required.",
			)
		}
		if _, duplicate := seen[channelID]; duplicate {
			return "", nil, invalidField(
				fmt.Sprintf("channel_ids[%d]", index),
				"unique",
				"channel_id_duplicate",
				"Channel ids must be unique.",
			)
		}
		seen[channelID] = struct{}{}
		normalized = append(normalized, channelID)
	}
	if len(normalized) == 0 {
		return "", nil, invalidField(
			"channel_ids",
			"required",
			"channels_required",
			"At least one connected channel is required.",
		)
	}
	return draftID, normalized, nil
}

func validateMutation(postID string, expectedRevision int64) error {
	if strings.TrimSpace(postID) == "" {
		return invalidField("post_id", "required", "post_id_required", "Post id is required.")
	}
	if expectedRevision < 1 {
		return invalidField(
			"expected_revision",
			"positive_integer",
			"revision_invalid",
			"Expected revision must be a positive integer.",
		)
	}
	return nil
}

func normalizeCalendarFilter(filter CalendarFilter) (CalendarFilter, error) {
	filter.ChannelID = strings.TrimSpace(filter.ChannelID)
	if filter.FromUTC.IsZero() || filter.UntilUTC.IsZero() ||
		!filter.FromUTC.Before(filter.UntilUTC) {
		return CalendarFilter{}, invalidField(
			"date_range",
			"ordered_half_open_range",
			"date_range_invalid",
			"Calendar requires a from instant before the until instant.",
		)
	}
	filter.FromUTC = filter.FromUTC.UTC()
	filter.UntilUTC = filter.UntilUTC.UTC()
	if filter.Status != "" && !filter.Status.Valid() {
		return CalendarFilter{}, invalidField(
			"status",
			"known_status",
			"status_invalid",
			"Calendar status is not supported.",
		)
	}
	return filter, nil
}

func newScheduledPost(
	postID, commandID, workspaceID, actorID, draftID string,
	channelIDs []string,
	schedule resolvedSchedule,
	revision int64,
	now time.Time,
) ScheduledPost {
	post := ScheduledPost{
		ID:              postID,
		WorkspaceID:     workspaceID,
		DraftID:         draftID,
		ChannelIDs:      append([]string(nil), channelIDs...),
		Status:          StatusScheduled,
		Revision:        revision,
		ActiveCommandID: commandID,
		CreatedBy:       actorID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	applySchedule(&post, schedule)
	return post
}

func applySchedule(post *ScheduledPost, schedule resolvedSchedule) {
	post.ScheduledForUTC = schedule.utc
	post.ScheduledLocal = schedule.local
	post.TimeZone = schedule.timeZone
	post.UTCOffsetMinutes = schedule.offsetMinutes
}

func commandFor(post ScheduledPost, commandID string, now time.Time) PublicationCommand {
	return PublicationCommand{
		ID:              commandID,
		WorkspaceID:     post.WorkspaceID,
		PostID:          post.ID,
		DraftID:         post.DraftID,
		ChannelIDs:      append([]string(nil), post.ChannelIDs...),
		Generation:      post.Revision,
		ExecuteAtUTC:    post.ScheduledForUTC,
		State:           CommandPending,
		CreatedAt:       now,
		InvalidationKey: post.ID + ":" + fmt.Sprintf("%d", post.Revision),
	}
}

func calendarEntry(post ScheduledPost) CalendarEntry {
	return CalendarEntry{
		PostID:           post.ID,
		DraftID:          post.DraftID,
		ChannelIDs:       append([]string(nil), post.ChannelIDs...),
		Status:           post.Status,
		ScheduledForUTC:  post.ScheduledForUTC,
		ScheduledLocal:   post.ScheduledLocal,
		TimeZone:         post.TimeZone,
		UTCOffsetMinutes: post.UTCOffsetMinutes,
		Revision:         post.Revision,
	}
}

func clonePost(post ScheduledPost) ScheduledPost {
	post.ChannelIDs = append([]string(nil), post.ChannelIDs...)
	if post.CancelledAt != nil {
		cancelledAt := *post.CancelledAt
		post.CancelledAt = &cancelledAt
	}
	return post
}

func clonePublicationCommand(command PublicationCommand) PublicationCommand {
	command.ChannelIDs = append([]string(nil), command.ChannelIDs...)
	if command.InvalidatedAt != nil {
		invalidatedAt := *command.InvalidatedAt
		command.InvalidatedAt = &invalidatedAt
	}
	return command
}
