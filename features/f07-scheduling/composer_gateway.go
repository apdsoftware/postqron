package scheduling

import (
	"context"
	"errors"

	composer "github.com/apdsoftware/postqron/features/f06-composer"
)

type ValidatedDraft struct {
	DraftID       string
	DraftRevision int64
	ChannelIDs    []string
}

type DuplicatedDraft struct {
	DraftID       string
	DraftRevision int64
}

type composerSchedulingBoundary interface {
	ValidateForScheduling(
		context.Context,
		composer.SchedulingValidationCommand,
	) (composer.SchedulingDraftReference, error)
	DuplicateDraft(
		context.Context,
		composer.DuplicateDraftCommand,
	) (composer.DuplicatedDraft, error)
}

type composerSchedulingGateway struct {
	boundary composerSchedulingBoundary
}

func newComposerSchedulingGateway(
	boundary composerSchedulingBoundary,
) ContentGateway {
	if boundary == nil {
		return unavailableContentGateway{}
	}
	return composerSchedulingGateway{boundary: boundary}
}

func (gateway composerSchedulingGateway) ValidateForScheduling(
	ctx context.Context,
	workspaceID, actorID, draftID string,
	channelIDs []string,
) (ValidatedDraft, error) {
	reference, err := gateway.boundary.ValidateForScheduling(
		ctx,
		composer.SchedulingValidationCommand{
			WorkspaceID: workspaceID,
			ActorID:     actorID,
			DraftID:     draftID,
			ChannelIDs:  channelIDs,
		},
	)
	if err != nil {
		return ValidatedDraft{}, mapComposerSchedulingError(
			err,
			"draft_id",
			ErrDraftRevisionStale,
		)
	}
	return ValidatedDraft{
		DraftID:       reference.DraftID,
		DraftRevision: reference.DraftRevision,
		ChannelIDs:    append([]string(nil), reference.ChannelIDs...),
	}, nil
}

func (gateway composerSchedulingGateway) DuplicateDraft(
	ctx context.Context,
	workspaceID, actorID, sourceDraftID string,
	sourceRevision int64,
	idempotencyKey string,
) (DuplicatedDraft, error) {
	duplicated, err := gateway.boundary.DuplicateDraft(
		ctx,
		composer.DuplicateDraftCommand{
			WorkspaceID:    workspaceID,
			ActorID:        actorID,
			SourceDraftID:  sourceDraftID,
			SourceRevision: sourceRevision,
			IdempotencyKey: idempotencyKey,
		},
	)
	if err != nil {
		return DuplicatedDraft{}, mapComposerSchedulingError(
			err,
			"source_draft_id",
			ErrComposerContention,
		)
	}
	return DuplicatedDraft{
		DraftID:       duplicated.DraftID,
		DraftRevision: duplicated.DraftRevision,
	}, nil
}

func mapComposerSchedulingError(
	err error,
	fallbackField string,
	conflictError error,
) error {
	if err == nil {
		return nil
	}
	var validationFailure *composer.ValidationFailure
	if errors.As(err, &validationFailure) {
		return composerValidationFailure(validationFailure.Report, fallbackField)
	}
	var fieldError *composer.FieldRuleError
	if errors.As(err, &fieldError) {
		return invalidField(
			fieldError.Field,
			fieldError.Rule,
			fieldError.Code,
			fieldError.Message,
		)
	}
	switch {
	case errors.Is(err, composer.ErrInvalidArgument):
		return ErrInvalidArgument
	case errors.Is(err, composer.ErrUnauthenticated):
		return ErrUnauthenticated
	case errors.Is(err, composer.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, composer.ErrNotFound):
		return ErrDraftNotFound
	case errors.Is(err, composer.ErrConflict):
		return conflictError
	case errors.Is(err, composer.ErrDependencyUnavailable),
		errors.Is(err, composer.ErrStorageUnavailable):
		return ErrDependencyUnavailable
	default:
		return ErrDependencyUnavailable
	}
}

func composerValidationFailure(
	report composer.ValidationReport,
	fallbackField string,
) error {
	for _, validationError := range report.Errors {
		field := validationError.Field
		if field == "" {
			field = fallbackField
		}
		return invalidField(
			field,
			validationError.Rule,
			validationError.Code,
			validationError.Message,
		)
	}
	for _, destination := range report.Destinations {
		for _, validationError := range destination.Errors {
			field := validationError.Field
			if field == "" {
				field = fallbackField
			}
			return invalidField(
				field,
				validationError.Rule,
				validationError.Code,
				validationError.Message,
			)
		}
	}
	return invalidField(
		fallbackField,
		"ready_for_scheduling",
		"draft_invalid",
		"The draft is not ready for scheduling.",
	)
}
