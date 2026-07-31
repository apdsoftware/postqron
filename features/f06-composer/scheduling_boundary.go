package composer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

type schedulingRevisionReader interface {
	GetRevision(context.Context, string, string, int64) (DraftRevision, error)
}

type schedulingMediaCloner interface {
	CloneForDraft(context.Context, string, []Media) ([]Media, error)
}

// SchedulingBoundary is the narrow F6 contract intended for F7.
type SchedulingBoundary struct {
	service *Service
}

func NewSchedulingBoundary(service *Service) (*SchedulingBoundary, error) {
	if service == nil {
		return nil, fmt.Errorf("%w: composer service is required", ErrInvalidArgument)
	}
	return &SchedulingBoundary{service: service}, nil
}

func (service *Service) SchedulingBoundary() (*SchedulingBoundary, error) {
	return NewSchedulingBoundary(service)
}

func (boundary *SchedulingBoundary) ValidateForScheduling(
	ctx context.Context,
	command SchedulingValidationCommand,
) (SchedulingDraftReference, error) {
	if boundary == nil || boundary.service == nil {
		return SchedulingDraftReference{}, ErrDependencyUnavailable
	}
	service := boundary.service
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return SchedulingDraftReference{}, err
	}
	draftID := strings.TrimSpace(command.DraftID)
	if draftID == "" {
		return SchedulingDraftReference{}, invalidField(
			"draft_id",
			"required",
			"draft_id_required",
			"Draft id is required.",
		)
	}
	channelIDs, err := normalizeRequestedChannelIDs(command.ChannelIDs)
	if err != nil {
		return SchedulingDraftReference{}, err
	}
	draft, err := service.repository.Get(ctx, command.WorkspaceID, draftID)
	if err != nil {
		return SchedulingDraftReference{}, err
	}
	content, report, err := service.liveSchedulingContent(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		draft.Content,
	)
	if err != nil {
		return SchedulingDraftReference{}, err
	}
	draftChannels, channelErr := schedulingDraftChannelSet(content)
	if channelErr != nil {
		report.Errors = append(report.Errors, *channelErr)
		report.Valid = false
	}
	if !slices.Equal(draftChannels, channelIDs) {
		report.Errors = append(report.Errors, validationError(
			"",
			"channel_ids",
			"exact_channel_set",
			"channel_set_mismatch",
			"The requested channels do not match the validated draft destinations.",
			"Refresh the draft destinations and schedule the exact same channel set.",
			map[string]any{
				"expected_channel_ids": draftChannels,
				"actual_channel_ids":   channelIDs,
			},
		))
		report.Valid = false
	}
	if !report.Valid {
		return SchedulingDraftReference{}, &ValidationFailure{Report: report}
	}
	return SchedulingDraftReference{
		DraftID:           draft.ID,
		DraftRevision:     draft.Revision,
		ChannelIDs:        draftChannels,
		CapabilityVersion: report.CapabilityVersion,
	}, nil
}

func (boundary *SchedulingBoundary) DuplicateDraft(
	ctx context.Context,
	command DuplicateDraftCommand,
) (DuplicatedDraft, error) {
	if boundary == nil || boundary.service == nil {
		return DuplicatedDraft{}, ErrDependencyUnavailable
	}
	service := boundary.service
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return DuplicatedDraft{}, err
	}
	sourceDraftID := strings.TrimSpace(command.SourceDraftID)
	if sourceDraftID == "" || command.SourceRevision < 1 {
		return DuplicatedDraft{}, fmt.Errorf(
			"%w: source draft id and positive source revision are required",
			ErrInvalidArgument,
		)
	}
	current, err := service.repository.Get(ctx, command.WorkspaceID, sourceDraftID)
	if err != nil {
		return DuplicatedDraft{}, err
	}
	if current.Revision != command.SourceRevision {
		return DuplicatedDraft{}, ErrConflict
	}
	revisionReader, ok := service.repository.(schedulingRevisionReader)
	if !ok {
		return DuplicatedDraft{}, ErrDependencyUnavailable
	}
	revision, err := revisionReader.GetRevision(
		ctx,
		command.WorkspaceID,
		sourceDraftID,
		command.SourceRevision,
	)
	if err != nil {
		return DuplicatedDraft{}, err
	}
	content := cloneContent(revision.Content)
	if err := service.requireLiveSchedulingDependencies(content); err != nil {
		return DuplicatedDraft{}, err
	}
	if len(content.Media) > 0 {
		cloner, ok := service.media.(schedulingMediaCloner)
		if !ok {
			return DuplicatedDraft{}, ErrDependencyUnavailable
		}
		content.Media, err = cloner.CloneForDraft(ctx, command.WorkspaceID, content.Media)
		if err != nil {
			return DuplicatedDraft{}, err
		}
		defer service.cleanupClonedMedia(ctx, command.WorkspaceID, content.Media, &err)
	}
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: command.WorkspaceID,
		ActorID:     command.ActorID,
		Content:     content,
	})
	if err != nil {
		return DuplicatedDraft{}, err
	}
	return DuplicatedDraft{
		DraftID:             created.Draft.ID,
		DraftRevision:       created.Draft.Revision,
		SourceDraftID:       sourceDraftID,
		SourceDraftRevision: command.SourceRevision,
	}, nil
}

func (service *Service) cleanupClonedMedia(
	ctx context.Context,
	workspaceID string,
	media []Media,
	operationErr *error,
) {
	if operationErr == nil || *operationErr == nil {
		return
	}
	deleter, ok := service.media.(*PostgresMediaStore)
	if !ok {
		return
	}
	for _, item := range media {
		_ = deleter.Delete(ctx, workspaceID, item.ID)
	}
}

func (service *Service) requireLiveSchedulingDependencies(
	content DraftContent,
) error {
	if len(content.Destinations) > 0 && service.destinations == nil {
		return ErrDependencyUnavailable
	}
	if len(content.Media) > 0 && service.media == nil {
		return ErrDependencyUnavailable
	}
	return nil
}

func (service *Service) liveSchedulingContent(
	ctx context.Context,
	workspaceID, actorID string,
	content DraftContent,
) (DraftContent, ValidationReport, error) {
	if err := service.requireLiveSchedulingDependencies(content); err != nil {
		return DraftContent{}, ValidationReport{}, err
	}
	live := cloneContent(content)
	if len(live.Media) > 0 {
		var err error
		live, err = service.canonicalizeMedia(ctx, workspaceID, actorID, live)
		if err != nil {
			return live, schedulingFailureReport(content, service.catalog, err), nil
		}
	}
	if len(live.Destinations) > 0 {
		resolved := cloneContent(live)
		var err error
		resolved, err = service.canonicalizeDestinations(ctx, workspaceID, resolved)
		if err != nil {
			return live, schedulingFailureReport(content, service.catalog, err), nil
		}
		report := Validate(resolved, service.catalog)
		for index := range resolved.Destinations {
			if destinationDrift(live.Destinations[index], resolved.Destinations[index]) {
				report.Destinations[index].Errors = append(
					report.Destinations[index].Errors,
					validationError(
						resolved.Destinations[index].ID,
						"capability_id",
						"immutable_revision",
						"destination_snapshot_stale",
						"The draft revision no longer matches the live channel capability.",
						"Refresh the draft destinations before scheduling.",
						map[string]any{
							"stored_capability_id":   live.Destinations[index].CapabilityID,
							"resolved_capability_id": resolved.Destinations[index].CapabilityID,
							"stored_channel_type":    live.Destinations[index].ChannelType,
							"resolved_channel_type":  resolved.Destinations[index].ChannelType,
						},
					),
				)
				report.Destinations[index].Valid = false
				report.Valid = false
			}
		}
		return resolved, report, nil
	}
	return live, Validate(live, service.catalog), nil
}

func schedulingFailureReport(
	content DraftContent,
	catalog CapabilityCatalog,
	cause error,
) ValidationReport {
	report := Validate(content, catalog)
	report.Valid = false
	var fieldError *FieldRuleError
	if errors.As(cause, &fieldError) {
		report.Errors = append(report.Errors, validationError(
			"",
			fieldError.Field,
			fieldError.Rule,
			fieldError.Code,
			fieldError.Message,
			"Refresh the draft dependencies and try again.",
			nil,
		))
	}
	return report
}

func schedulingDraftChannelSet(content DraftContent) ([]string, *ValidationError) {
	channelIDs := make([]string, 0, len(content.Destinations))
	seen := make(map[string]struct{}, len(content.Destinations))
	for _, destination := range content.Destinations {
		channelID := strings.TrimSpace(destination.ChannelID)
		if _, duplicate := seen[channelID]; duplicate {
			err := validationError(
				destination.ID,
				"destinations",
				"unique_channel_ids",
				"destination_channel_duplicate",
				"The draft contains duplicate channel ids and cannot be scheduled safely.",
				"Keep exactly one destination per channel for scheduling.",
				map[string]any{"channel_id": channelID},
			)
			return nil, &err
		}
		seen[channelID] = struct{}{}
		channelIDs = append(channelIDs, channelID)
	}
	sort.Strings(channelIDs)
	return channelIDs, nil
}

func normalizeRequestedChannelIDs(channelIDs []string) ([]string, error) {
	if len(channelIDs) == 0 {
		return nil, invalidField(
			"channel_ids",
			"required",
			"channel_ids_required",
			"At least one channel id is required.",
		)
	}
	normalized := make([]string, 0, len(channelIDs))
	seen := make(map[string]struct{}, len(channelIDs))
	for index, raw := range channelIDs {
		channelID := strings.TrimSpace(raw)
		if channelID == "" {
			return nil, invalidField(
				fmt.Sprintf("channel_ids[%d]", index),
				"required",
				"channel_id_required",
				"Channel id is required.",
			)
		}
		if _, duplicate := seen[channelID]; duplicate {
			return nil, invalidField(
				fmt.Sprintf("channel_ids[%d]", index),
				"unique",
				"channel_id_duplicate",
				"Channel ids must be unique.",
			)
		}
		seen[channelID] = struct{}{}
		normalized = append(normalized, channelID)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func destinationDrift(stored, resolved Destination) bool {
	return stored.ChannelType != resolved.ChannelType ||
		stored.CapabilityID != resolved.CapabilityID ||
		stored.Format != resolved.Format
}
