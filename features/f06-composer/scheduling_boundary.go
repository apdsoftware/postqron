package composer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

type schedulingRevisionReader interface {
	GetRevision(context.Context, string, string, int64) (DraftRevision, error)
}

type schedulingMediaCloner interface {
	CloneForDraft(context.Context, string, []Media) ([]Media, error)
}

type schedulingMediaPreflighter interface {
	PreflightScheduling(
		context.Context,
		string,
		string,
		[]Media,
	) ([]Media, error)
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
		return SchedulingDraftReference{}, mapSchedulingDependencyError(err)
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
		return SchedulingDraftReference{}, mapSchedulingDependencyError(err)
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
	idempotencyKey := normalizeIdempotencyKey(command.IdempotencyKey)
	if sourceDraftID == "" || command.SourceRevision < 1 || idempotencyKey == "" {
		return DuplicatedDraft{}, fmt.Errorf(
			"%w: source draft id, positive source revision, and idempotency key are required",
			ErrInvalidArgument,
		)
	}
	revisionReader, ok := service.repository.(schedulingRevisionReader)
	if !ok {
		return DuplicatedDraft{}, ErrDependencyUnavailable
	}
	duplicates, ok := service.repository.(duplicateOperationStore)
	if !ok {
		return DuplicatedDraft{}, ErrDependencyUnavailable
	}
	for attempt := 0; attempt < 3; attempt++ {
		now := service.now().UTC()
		operation, replayed, err := duplicates.ReserveDuplicateOperation(ctx, duplicateOperation{
			WorkspaceID:      command.WorkspaceID,
			IdempotencyKey:   idempotencyKey,
			SourceDraftID:    sourceDraftID,
			SourceRevision:   command.SourceRevision,
			CreatedByAccount: command.ActorID,
		}, now)
		if err != nil {
			return DuplicatedDraft{}, mapSchedulingDependencyError(err)
		}
		if replayed {
			_, err := service.repository.Get(
				ctx,
				command.WorkspaceID,
				operation.CloneDraftID,
			)
			if err == nil {
				return DuplicatedDraft{
					DraftID:             operation.CloneDraftID,
					DraftRevision:       operation.CloneDraftRevision,
					SourceDraftID:       sourceDraftID,
					SourceDraftRevision: command.SourceRevision,
					Replayed:            true,
				}, nil
			}
			if !errors.Is(err, ErrNotFound) {
				return DuplicatedDraft{}, mapSchedulingDependencyError(err)
			}
			operation, reset, resetErr := duplicates.ResetDanglingCompletedDuplicateOperation(
				ctx,
				operation,
				now,
			)
			if resetErr != nil {
				return DuplicatedDraft{}, mapSchedulingDependencyError(resetErr)
			}
			if !reset {
				continue
			}
			result, duplicateErr := boundary.executeDuplicateDraft(
				ctx,
				command,
				sourceDraftID,
				idempotencyKey,
				operation,
				revisionReader,
				duplicates,
				now,
			)
			if duplicateErr == nil {
				return result, nil
			}
			return DuplicatedDraft{}, duplicateErr
		}
		result, duplicateErr := boundary.executeDuplicateDraft(
			ctx,
			command,
			sourceDraftID,
			idempotencyKey,
			operation,
			revisionReader,
			duplicates,
			now,
		)
		if duplicateErr == nil {
			return result, nil
		}
		if errors.Is(duplicateErr, ErrConflict) {
			continue
		}
		return DuplicatedDraft{}, duplicateErr
	}
	return DuplicatedDraft{}, ErrConflict
}

func (boundary *SchedulingBoundary) executeDuplicateDraft(
	ctx context.Context,
	command DuplicateDraftCommand,
	sourceDraftID string,
	idempotencyKey string,
	operation duplicateOperation,
	revisionReader schedulingRevisionReader,
	duplicates duplicateOperationStore,
	now time.Time,
) (_ DuplicatedDraft, err error) {
	service := boundary.service
	var (
		content     DraftContent
		clonedMedia []Media
		created     Draft
	)
	defer func() {
		if err == nil {
			return
		}
		if created.ID == "" && len(clonedMedia) > 0 {
			service.cleanupClonedMedia(
				context.Background(),
				command.WorkspaceID,
				clonedMedia,
				&err,
			)
		}
		abandoned, abandonErr := duplicates.AbandonDuplicateOperation(
			context.Background(),
			operation,
		)
		if abandonErr != nil {
			err = ErrDependencyUnavailable
			return
		}
		if !abandoned {
			return
		}
		if created.ID != "" {
			_ = service.DeleteDraft(
				context.Background(),
				command.WorkspaceID,
				command.ActorID,
				created.ID,
				created.Revision,
			)
		}
		if created.ID != "" && len(clonedMedia) > 0 {
			service.cleanupClonedMedia(
				context.Background(),
				command.WorkspaceID,
				clonedMedia,
				&err,
			)
		}
	}()
	revision, err := revisionReader.GetRevision(
		ctx,
		command.WorkspaceID,
		sourceDraftID,
		command.SourceRevision,
	)
	if err != nil {
		return DuplicatedDraft{}, mapSchedulingDependencyError(err)
	}
	content = cloneContent(revision.Content)
	if err := service.requireLiveSchedulingDependencies(content); err != nil {
		return DuplicatedDraft{}, err
	}
	cloneDraftID := duplicatedDraftID(
		command.WorkspaceID,
		sourceDraftID,
		command.SourceRevision,
		idempotencyKey,
	)
	if existing, getErr := service.repository.Get(
		ctx,
		command.WorkspaceID,
		cloneDraftID,
	); getErr == nil {
		if completeErr := duplicates.CompleteDuplicateOperation(
			ctx,
			operation,
			existing.ID,
			existing.Revision,
			now,
		); completeErr != nil {
			return DuplicatedDraft{}, mapSchedulingDependencyError(completeErr)
		}
		return DuplicatedDraft{
			DraftID:             existing.ID,
			DraftRevision:       existing.Revision,
			SourceDraftID:       sourceDraftID,
			SourceDraftRevision: command.SourceRevision,
			Replayed:            true,
		}, nil
	} else if !errors.Is(getErr, ErrNotFound) {
		return DuplicatedDraft{}, mapSchedulingDependencyError(getErr)
	}
	if len(content.Media) > 0 {
		cloner, ok := service.media.(schedulingMediaCloner)
		if !ok {
			return DuplicatedDraft{}, ErrDependencyUnavailable
		}
		clonedMedia, err = cloner.CloneForDraft(ctx, command.WorkspaceID, content.Media)
		if err != nil {
			return DuplicatedDraft{}, mapSchedulingDependencyError(err)
		}
		content, err = remapDraftMediaReferences(content, clonedMedia)
		if err != nil {
			return DuplicatedDraft{}, mapSchedulingDependencyError(err)
		}
	}
	created, err = service.repository.Create(ctx, Draft{
		ID:          cloneDraftID,
		WorkspaceID: command.WorkspaceID,
		CreatedBy:   command.ActorID,
		Content:     content,
		Revision:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			existing, getErr := service.repository.Get(
				ctx,
				command.WorkspaceID,
				cloneDraftID,
			)
			if getErr == nil {
				if completeErr := duplicates.CompleteDuplicateOperation(
					ctx,
					operation,
					existing.ID,
					existing.Revision,
					now,
				); completeErr != nil {
					return DuplicatedDraft{}, mapSchedulingDependencyError(completeErr)
				}
				return DuplicatedDraft{
					DraftID:             existing.ID,
					DraftRevision:       existing.Revision,
					SourceDraftID:       sourceDraftID,
					SourceDraftRevision: command.SourceRevision,
					Replayed:            true,
				}, nil
			}
			if !errors.Is(getErr, ErrNotFound) {
				return DuplicatedDraft{}, mapSchedulingDependencyError(getErr)
			}
		}
		return DuplicatedDraft{}, mapSchedulingDependencyError(err)
	}
	if err = duplicates.CompleteDuplicateOperation(
		ctx,
		operation,
		created.ID,
		created.Revision,
		now,
	); err != nil {
		return DuplicatedDraft{}, mapSchedulingDependencyError(err)
	}
	return DuplicatedDraft{
		DraftID:             created.ID,
		DraftRevision:       created.Revision,
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
		if preflighter, ok := service.media.(schedulingMediaPreflighter); ok {
			live.Media, err = preflighter.PreflightScheduling(
				ctx,
				workspaceID,
				actorID,
				live.Media,
			)
		} else {
			live, err = service.canonicalizeMedia(ctx, workspaceID, actorID, live)
		}
		if err != nil {
			return classifySchedulingValidationError(content, service.catalog, err)
		}
	}
	if len(live.Destinations) > 0 {
		resolved := cloneContent(live)
		var err error
		resolved, err = service.canonicalizeDestinations(ctx, workspaceID, resolved)
		if err != nil {
			return classifySchedulingValidationError(content, service.catalog, err)
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

func classifySchedulingValidationError(
	content DraftContent,
	catalog CapabilityCatalog,
	cause error,
) (DraftContent, ValidationReport, error) {
	var fieldError *FieldRuleError
	if errors.As(cause, &fieldError) {
		report := Validate(content, catalog)
		report.Valid = false
		report.Errors = append(report.Errors, validationError(
			"",
			fieldError.Field,
			fieldError.Rule,
			fieldError.Code,
			fieldError.Message,
			"Refresh the draft dependencies and try again.",
			nil,
		))
		return DraftContent{}, report, &ValidationFailure{Report: report}
	}
	return DraftContent{}, ValidationReport{}, mapSchedulingDependencyError(cause)
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

func remapDraftMediaReferences(
	content DraftContent,
	clonedMedia []Media,
) (DraftContent, error) {
	if len(content.Media) != len(clonedMedia) {
		return DraftContent{}, ErrConflict
	}
	mapping := make(map[string]string, len(content.Media))
	for index, original := range content.Media {
		mapping[original.ID] = clonedMedia[index].ID
	}
	remapped := cloneContent(content)
	remapped.Media = append([]Media(nil), clonedMedia...)
	var err error
	for index := range remapped.Thread {
		remapped.Thread[index].MediaIDs, err = remapMediaIDList(
			remapped.Thread[index].MediaIDs,
			mapping,
		)
		if err != nil {
			return DraftContent{}, err
		}
	}
	for index := range remapped.Destinations {
		destination := &remapped.Destinations[index]
		if destination.MediaIDs != nil {
			mediaIDs, remapErr := remapMediaIDList(*destination.MediaIDs, mapping)
			if remapErr != nil {
				return DraftContent{}, remapErr
			}
			destination.MediaIDs = &mediaIDs
		}
		if destination.ThreadOverride != nil {
			thread := make([]ThreadItem, len(*destination.ThreadOverride))
			for itemIndex, item := range *destination.ThreadOverride {
				thread[itemIndex] = item
				thread[itemIndex].MediaIDs, err = remapMediaIDList(item.MediaIDs, mapping)
				if err != nil {
					return DraftContent{}, err
				}
			}
			destination.ThreadOverride = &thread
		}
	}
	return remapped, nil
}

func remapMediaIDList(ids []string, mapping map[string]string) ([]string, error) {
	result := append([]string(nil), ids...)
	for index, id := range result {
		remapped, ok := mapping[id]
		if !ok {
			return nil, ErrConflict
		}
		result[index] = remapped
	}
	return result, nil
}

func mapSchedulingDependencyError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrInvalidArgument),
		errors.Is(err, ErrUnauthenticated),
		errors.Is(err, ErrForbidden),
		errors.Is(err, ErrNotFound),
		errors.Is(err, ErrConflict),
		errors.Is(err, ErrValidation),
		errors.Is(err, ErrDependencyUnavailable):
		return err
	case errors.Is(err, ErrStorageUnavailable):
		return ErrDependencyUnavailable
	default:
		return ErrDependencyUnavailable
	}
}

func duplicatedDraftID(
	workspaceID, sourceDraftID string,
	sourceRevision int64,
	idempotencyKey string,
) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%d\x00%s",
		workspaceID,
		sourceDraftID,
		sourceRevision,
		idempotencyKey,
	)))
	return "draft_dup_" + base64.RawURLEncoding.EncodeToString(sum[:18])
}
