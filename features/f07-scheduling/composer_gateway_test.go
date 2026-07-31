package scheduling

import (
	"context"
	"errors"
	"testing"

	composer "github.com/apdsoftware/postqron/features/f06-composer"
)

type composerBoundaryStub struct {
	validateReference composer.SchedulingDraftReference
	validateErr       error
	duplicateDraft    composer.DuplicatedDraft
	duplicateErr      error
}

func (stub composerBoundaryStub) ValidateForScheduling(
	context.Context,
	composer.SchedulingValidationCommand,
) (composer.SchedulingDraftReference, error) {
	return stub.validateReference, stub.validateErr
}

func (stub composerBoundaryStub) DuplicateDraft(
	context.Context,
	composer.DuplicateDraftCommand,
) (composer.DuplicatedDraft, error) {
	return stub.duplicateDraft, stub.duplicateErr
}

func TestComposerSchedulingGatewayMapsValidationFailuresToFieldErrors(t *testing.T) {
	gateway := newComposerSchedulingGateway(composerBoundaryStub{
		validateErr: &composer.ValidationFailure{
			Report: composer.ValidationReport{
				Errors: []composer.ValidationError{{
					Field:   "channel_ids",
					Rule:    "exact_channel_set",
					Code:    "channel_set_mismatch",
					Message: "The requested channels do not match the validated draft destinations.",
				}},
			},
		},
	})

	_, err := gateway.ValidateForScheduling(
		context.Background(),
		"workspace-1",
		"account-1",
		"draft-1",
		[]string{"channel-1"},
	)
	var fieldError *FieldError
	if !errors.As(err, &fieldError) {
		t.Fatalf("ValidateForScheduling() error = %v, want field error", err)
	}
	if fieldError.Code != "channel_set_mismatch" || fieldError.Field != "channel_ids" {
		t.Fatalf("field error = %#v", fieldError)
	}
}

func TestComposerSchedulingGatewayMapsTypedComposerErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{"draft not found", composer.ErrNotFound, ErrDraftNotFound},
		{"stale revision", composer.ErrConflict, ErrDraftRevisionStale},
		{"forbidden", composer.ErrForbidden, ErrForbidden},
		{"dependency unavailable", composer.ErrDependencyUnavailable, ErrDependencyUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := newComposerSchedulingGateway(composerBoundaryStub{
				validateErr: test.err,
			})
			_, err := gateway.ValidateForScheduling(
				context.Background(),
				"workspace-1",
				"account-1",
				"draft-1",
				[]string{"channel-1"},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("ValidateForScheduling() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestComposerDuplicateContentionIsRetryableNotDraftStale(t *testing.T) {
	gateway := newComposerSchedulingGateway(composerBoundaryStub{
		duplicateErr: composer.ErrConflict,
	})
	_, err := gateway.DuplicateDraft(
		context.Background(),
		"workspace-1",
		"account-1",
		"draft-1",
		1,
		"request-1",
	)
	if !errors.Is(err, ErrComposerContention) {
		t.Fatalf("DuplicateDraft() error = %v, want retryable contention", err)
	}
	if errors.Is(err, ErrDraftRevisionStale) {
		t.Fatalf("DuplicateDraft() contention was misclassified as stale: %v", err)
	}
}
