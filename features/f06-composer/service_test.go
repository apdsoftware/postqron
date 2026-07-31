package composer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type authorizerStub struct {
	allowed bool
	err     error
}

func (stub authorizerStub) CanManageContent(
	context.Context,
	string,
	string,
) (bool, error) {
	return stub.allowed, stub.err
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	service, err := NewService(
		NewMemoryRepository(),
		authorizerStub{allowed: true},
		WithCapabilityCatalog(fixtureCatalog(t)),
		WithClock(func() time.Time {
			return time.Date(2026, 7, 24, 16, 30, 0, 0, time.FixedZone("local", -4*60*60))
		}),
		WithRandom(func(destination []byte) error {
			for index := range destination {
				destination[index] = byte(index + 1)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestServiceCRUDSavesIncompleteDraftAndUsesOptimisticRevision(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content:     DraftContent{Text: "Work in progress"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Validation.Valid {
		t.Fatal("incomplete draft should be saved with invalid validation result")
	}
	if created.Draft.Revision != 1 || !created.Draft.CreatedAt.Equal(created.Draft.CreatedAt.UTC()) {
		t.Fatalf("created draft = %#v", created.Draft)
	}

	image := validImage("image", "image/jpeg", 1080, 1080)
	updated, err := service.UpdateDraft(ctx, UpdateDraftCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		DraftID:          created.Draft.ID,
		ExpectedRevision: 1,
		Content: DraftContent{
			Text:  "Ready",
			Media: []Media{image},
			Destinations: []Destination{{
				ID:           "image",
				ChannelID:    "image-1",
				ChannelType:  "fixture_image_channel",
				CapabilityID: "fixture:image",
				Format:       FormatImage,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Draft.Revision != 2 || !updated.Validation.Valid {
		t.Fatalf("updated = %#v", updated)
	}

	_, err = service.UpdateDraft(ctx, UpdateDraftCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		DraftID:          created.Draft.ID,
		ExpectedRevision: 1,
		Content:          updated.Draft.Content,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v", err)
	}

	list, err := service.ListDrafts(ctx, "workspace-1", "account-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %#v, err = %v", list, err)
	}
	if err := service.DeleteDraft(
		ctx,
		"workspace-1",
		"account-1",
		created.Draft.ID,
		2,
	); err != nil {
		t.Fatal(err)
	}
	_, err = service.GetDraft(ctx, "workspace-1", "account-1", created.Draft.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete error = %v", err)
	}
}

func TestServiceAutosaveIsIdempotentAndKeepsRevisionHistory(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content:     DraftContent{Text: "first"},
	})
	if err != nil {
		t.Fatal(err)
	}
	command := UpdateDraftCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		DraftID:          created.Draft.ID,
		ExpectedRevision: 1,
		AutosaveKey:      "session-1:save-1",
		Content:          DraftContent{Text: "second"},
	}
	first, err := service.UpdateDraft(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.UpdateDraft(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.Draft.Revision != 2 || replayed.Draft.Revision != 2 {
		t.Fatalf("autosave revisions = %d and %d", first.Draft.Revision, replayed.Draft.Revision)
	}
	revisions, err := service.ListDraftRevisions(
		ctx, "workspace-1", "account-1", created.Draft.ID,
	)
	if err != nil || len(revisions) != 2 ||
		revisions[0].AutosaveKey != command.AutosaveKey {
		t.Fatalf("revisions = %#v, err = %v", revisions, err)
	}
}

func TestServiceNormalizesTextAndProtectsWorkspaceBoundary(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content: DraftContent{
			Text: strings.Repeat("e\u0301", 2),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Draft.Content.Text != "éé" {
		t.Fatalf("normalized text = %q", created.Draft.Content.Text)
	}
	_, err = service.GetDraft(ctx, "workspace-2", "account-1", created.Draft.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace get error = %v", err)
	}
}

func TestServiceValidateForSchedulingReturnsTypedFailure(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content:     DraftContent{},
	})
	if err != nil {
		t.Fatal(err)
	}

	report, err := service.ValidateForScheduling(
		ctx,
		"workspace-1",
		"account-1",
		created.Draft.ID,
	)
	if !errors.Is(err, ErrValidation) || report.Valid {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
	var failure *ValidationFailure
	if !errors.As(err, &failure) || len(failure.Report.Errors) == 0 {
		t.Fatalf("typed failure = %#v", failure)
	}
}

func TestServiceAuthorizationFailsClosed(t *testing.T) {
	service, err := NewService(
		NewMemoryRepository(),
		authorizerStub{allowed: false},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateDraft(context.Background(), CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("authorization error = %v", err)
	}
}

func TestServiceRejectsAmbiguousContentIdentifiers(t *testing.T) {
	service := newTestService(t)
	_, err := service.CreateDraft(context.Background(), CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content: DraftContent{
			Media: []Media{
				validImage("duplicate", "image/jpeg", 1080, 1080),
				validImage("duplicate", "image/jpeg", 1080, 1080),
			},
		},
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate media error = %v", err)
	}
}
