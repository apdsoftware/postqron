package composer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"
)

type schedulingDestinationResolverStub struct {
	resolved map[string]ResolvedDestination
	err      error
}

func (stub schedulingDestinationResolverStub) Resolve(
	_ context.Context,
	_, channelID string,
	_ Format,
) (ResolvedDestination, error) {
	if stub.err != nil {
		return ResolvedDestination{}, stub.err
	}
	resolved, ok := stub.resolved[channelID]
	if !ok {
		return ResolvedDestination{}, &destinationResolutionError{
			Rule:    "active_workspace_channel",
			Code:    "channel_unknown",
			Message: "The selected channel does not exist.",
		}
	}
	return resolved, nil
}

type schedulingMediaResolverStub struct {
	canonical map[string]Media
}

func (stub *schedulingMediaResolverStub) Canonicalize(
	_ context.Context,
	_, _ string,
	media []Media,
) ([]Media, error) {
	result := make([]Media, len(media))
	for index, item := range media {
		canonical, ok := stub.canonical[item.ID]
		if !ok {
			return nil, ErrNotFound
		}
		result[index] = canonical
	}
	return result, nil
}

func (stub *schedulingMediaResolverStub) CloneForDraft(
	_ context.Context,
	_ string,
	media []Media,
) ([]Media, error) {
	cloned := make([]Media, len(media))
	for index, item := range media {
		cloned[index] = item
		cloned[index].ID = item.ID + "-clone"
		if stub.canonical != nil {
			canonical := item
			canonical.ID = cloned[index].ID
			stub.canonical[canonical.ID] = canonical
		}
	}
	return cloned, nil
}

func newSchedulingBoundaryService(t *testing.T) (*Service, *SchedulingBoundary) {
	t.Helper()
	media := &schedulingMediaResolverStub{
		canonical: map[string]Media{
			"image-1": validImage("image-1", "image/jpeg", 1080, 1080),
		},
	}
	sequence := uint64(0)
	service, err := NewService(
		NewMemoryRepository(),
		authorizerStub{allowed: true},
		WithCapabilityCatalog(fixtureCatalog(t)),
		WithDestinationResolver(schedulingDestinationResolverStub{
			resolved: map[string]ResolvedDestination{
				"channel-1": {
					ChannelType:  "fixture_image_channel",
					CapabilityID: "fixture:image",
					Format:       FormatImage,
				},
				"channel-2": {
					ChannelType:  "fixture_text_channel",
					CapabilityID: "fixture:text",
					Format:       FormatText,
				},
			},
		}),
		WithMediaResolver(media),
		WithClock(func() time.Time {
			return time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
		}),
		WithRandom(func(destination []byte) error {
			sequence++
			for index := range destination {
				destination[index] = byte(index + 1)
			}
			binary.BigEndian.PutUint64(destination[len(destination)-8:], sequence)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	boundary, err := service.SchedulingBoundary()
	if err != nil {
		t.Fatal(err)
	}
	return service, boundary
}

func TestSchedulingBoundaryValidateForSchedulingReturnsImmutableReference(t *testing.T) {
	ctx := context.Background()
	service, boundary := newSchedulingBoundaryService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content: DraftContent{
			Text:  "",
			Media: []Media{{ID: "image-1"}},
			Destinations: []Destination{
				{
					ID:           "text",
					ChannelID:    "channel-2",
					ChannelType:  "fixture_text_channel",
					CapabilityID: "fixture:text",
					Format:       FormatText,
					TextOverride: stringPointer("Ready"),
					MediaIDs:     &[]string{},
				},
				{
					ID:           "image",
					ChannelID:    "channel-1",
					ChannelType:  "fixture_image_channel",
					CapabilityID: "fixture:image",
					Format:       FormatImage,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	reference, err := boundary.ValidateForScheduling(ctx, SchedulingValidationCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     created.Draft.ID,
		ChannelIDs:  []string{"channel-2", "channel-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reference.DraftID != created.Draft.ID ||
		reference.DraftRevision != 1 ||
		reference.CapabilityVersion != fixtureCatalog(t).Version ||
		fmt.Sprint(reference.ChannelIDs) != "[channel-1 channel-2]" {
		t.Fatalf("scheduling reference = %#v", reference)
	}
}

func TestSchedulingBoundaryValidateForSchedulingRejectsSubsetAndDuplicates(t *testing.T) {
	ctx := context.Background()
	service, boundary := newSchedulingBoundaryService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content: DraftContent{
			Text:  "",
			Media: []Media{{ID: "image-1"}},
			Destinations: []Destination{{
				ID:           "text",
				ChannelID:    "channel-2",
				ChannelType:  "fixture_text_channel",
				CapabilityID: "fixture:text",
				Format:       FormatText,
				TextOverride: stringPointer("Ready"),
				MediaIDs:     &[]string{},
			}, {
				ID:           "image",
				ChannelID:    "channel-1",
				ChannelType:  "fixture_image_channel",
				CapabilityID: "fixture:image",
				Format:       FormatImage,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = boundary.ValidateForScheduling(ctx, SchedulingValidationCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     created.Draft.ID,
		ChannelIDs:  []string{"channel-1"},
	})
	var failure *ValidationFailure
	if !errors.As(err, &failure) || failure.Report.Valid {
		t.Fatalf("subset validation error = %#v", err)
	}

	_, err = boundary.ValidateForScheduling(ctx, SchedulingValidationCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     created.Draft.ID,
		ChannelIDs:  []string{"channel-1", "channel-1"},
	})
	var fieldError *FieldRuleError
	if !errors.As(err, &fieldError) || fieldError.Code != "channel_id_duplicate" {
		t.Fatalf("duplicate channel error = %#v", err)
	}
}

func TestSchedulingBoundaryValidateForSchedulingDetectsCapabilityDrift(t *testing.T) {
	ctx := context.Background()
	service, boundary := newSchedulingBoundaryService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content: DraftContent{
			Text: "Ready",
			Destinations: []Destination{{
				ID:           "text",
				ChannelID:    "channel-2",
				ChannelType:  "fixture_text_channel",
				CapabilityID: "fixture:text",
				Format:       FormatText,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.destinations = schedulingDestinationResolverStub{
		resolved: map[string]ResolvedDestination{
			"channel-2": {
				ChannelType:  "fixture_text_channel",
				CapabilityID: "fixture:link",
				Format:       FormatText,
			},
		},
	}

	_, err = boundary.ValidateForScheduling(ctx, SchedulingValidationCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     created.Draft.ID,
		ChannelIDs:  []string{"channel-2"},
	})
	var failure *ValidationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("capability drift error = %#v", err)
	}
	found := false
	for _, destination := range failure.Report.Destinations {
		for _, item := range destination.Errors {
			if item.Code == "destination_snapshot_stale" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("capability drift report = %#v", failure.Report)
	}
}

func TestSchedulingBoundaryDuplicateDraftRequiresExactRevision(t *testing.T) {
	ctx := context.Background()
	service, boundary := newSchedulingBoundaryService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content: DraftContent{
			Text:         "v1",
			Media:        []Media{{ID: "image-1"}},
			Destinations: []Destination{{ID: "image", ChannelID: "channel-1", ChannelType: "fixture_image_channel", CapabilityID: "fixture:image", Format: FormatImage}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateDraft(ctx, UpdateDraftCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		DraftID:          created.Draft.ID,
		ExpectedRevision: 1,
		Content: DraftContent{
			Text:         "v2",
			Media:        []Media{{ID: "image-1"}},
			Destinations: created.Draft.Content.Destinations,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 1,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale duplicate error = %v", err)
	}
}

func TestSchedulingBoundaryDuplicateDraftClonesIndependentMedia(t *testing.T) {
	ctx := context.Background()
	service, boundary := newSchedulingBoundaryService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content: DraftContent{
			Text:         "Ready",
			Media:        []Media{{ID: "image-1"}},
			Destinations: []Destination{{ID: "image", ChannelID: "channel-1", ChannelType: "fixture_image_channel", CapabilityID: "fixture:image", Format: FormatImage}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	duplicated, err := boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := service.GetDraft(ctx, "workspace-1", "account-1", duplicated.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	if cloned.Draft.Content.Media[0].ID == created.Draft.Content.Media[0].ID {
		t.Fatalf("clone reused source media id: %#v", cloned.Draft.Content.Media)
	}
	if err := service.DeleteDraft(ctx, "workspace-1", "account-1", created.Draft.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetDraft(ctx, "workspace-1", "account-1", duplicated.DraftID); err != nil {
		t.Fatalf("clone became unavailable after source delete: %v", err)
	}
}

func stringPointer(value string) *string {
	return &value
}
