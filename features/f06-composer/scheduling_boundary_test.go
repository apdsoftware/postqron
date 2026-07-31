package composer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
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
	canonical    map[string]Media
	preflightErr error
	cloneErr     error
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

func (stub *schedulingMediaResolverStub) PreflightScheduling(
	_ context.Context,
	_, _ string,
	media []Media,
) ([]Media, error) {
	if stub.preflightErr != nil {
		return nil, stub.preflightErr
	}
	return stub.Canonicalize(context.Background(), "", "", media)
}

func (stub *schedulingMediaResolverStub) CloneForDraft(
	_ context.Context,
	_ string,
	media []Media,
) ([]Media, error) {
	if stub.cloneErr != nil {
		return nil, stub.cloneErr
	}
	cloned := make([]Media, len(media))
	for index, item := range media {
		cloned[index] = item
		cloned[index].ID = item.ID + "-clone"
		cloned[index].URL = "/api/v1/media/" + cloned[index].ID
		if stub.canonical != nil {
			canonical := item
			canonical.ID = cloned[index].ID
			canonical.URL = cloned[index].URL
			stub.canonical[canonical.ID] = canonical
		}
	}
	return cloned, nil
}

func newSchedulingBoundaryService(t *testing.T) (*Service, *SchedulingBoundary) {
	t.Helper()
	return newSchedulingBoundaryServiceWithMedia(t, &schedulingMediaResolverStub{
		canonical: map[string]Media{
			"image-1": validImage("image-1", "image/jpeg", 1080, 1080),
		},
	})
}

func newSchedulingBoundaryServiceWithMedia(
	t *testing.T,
	media *schedulingMediaResolverStub,
) (*Service, *SchedulingBoundary) {
	t.Helper()
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

	_, err = boundary.ValidateForScheduling(ctx, SchedulingValidationCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     created.Draft.ID,
		ChannelIDs:  []string{"channel-1", "channel-2", "channel-extra"},
	})
	if !errors.As(err, &failure) || failure.Report.Valid {
		t.Fatalf("superset validation error = %#v", err)
	}

	_, err = boundary.ValidateForScheduling(ctx, SchedulingValidationCommand{
		WorkspaceID: "workspace-2",
		ActorID:     "account-1",
		DraftID:     created.Draft.ID,
		ChannelIDs:  []string{"channel-1", "channel-2"},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace validation error = %v", err)
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

func TestSchedulingBoundaryDuplicateDraftAllowsHistoricalRevision(t *testing.T) {
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

	duplicated, err := boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 1,
		IdempotencyKey: "test-idempotency-historical-duplication",
	})
	if err != nil {
		t.Fatal(err)
	}
	cloned, err := service.GetDraft(ctx, "workspace-1", "account-1", duplicated.DraftID)
	if err != nil {
		t.Fatal(err)
	}
	if cloned.Draft.Content.Text != "v1" {
		t.Fatalf("historical duplicate = %#v", cloned.Draft.Content)
	}
}

func TestSchedulingBoundaryDuplicateDraftRejectsMissingHistoricalRevision(t *testing.T) {
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
	_, err = boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 99,
		IdempotencyKey: "test-idempotency-missing-revision",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("missing historical revision error = %v", err)
	}
}

func TestSchedulingBoundaryDuplicateDraftClonesIndependentMedia(t *testing.T) {
	ctx := context.Background()
	service, boundary := newSchedulingBoundaryService(t)
	created, err := service.CreateDraft(ctx, CreateDraftCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		Content: DraftContent{
			Text:   "Ready",
			Media:  []Media{{ID: "image-1"}},
			Thread: []ThreadItem{{Text: "part-1", MediaIDs: []string{"image-1"}}},
			Destinations: []Destination{{
				ID:           "image",
				ChannelID:    "channel-1",
				ChannelType:  "fixture_image_channel",
				CapabilityID: "fixture:image",
				Format:       FormatImage,
				MediaIDs:     &[]string{"image-1"},
				ThreadOverride: &[]ThreadItem{{
					Text:     "override",
					MediaIDs: []string{"image-1"},
				}},
			}},
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
		IdempotencyKey: "test-idempotency-clone-independent",
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
	if cloned.Draft.Content.Thread[0].MediaIDs[0] != cloned.Draft.Content.Media[0].ID {
		t.Fatalf("thread media ids not remapped: %#v", cloned.Draft.Content.Thread)
	}
	if cloned.Draft.Content.Destinations[0].MediaIDs == nil ||
		(*cloned.Draft.Content.Destinations[0].MediaIDs)[0] != cloned.Draft.Content.Media[0].ID {
		t.Fatalf("destination media ids not remapped: %#v", cloned.Draft.Content.Destinations[0])
	}
	if cloned.Draft.Content.Destinations[0].ThreadOverride == nil ||
		(*cloned.Draft.Content.Destinations[0].ThreadOverride)[0].MediaIDs[0] !=
			cloned.Draft.Content.Media[0].ID {
		t.Fatalf("thread override media ids not remapped: %#v", cloned.Draft.Content.Destinations[0])
	}
	if err := service.DeleteDraft(ctx, "workspace-1", "account-1", created.Draft.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetDraft(ctx, "workspace-1", "account-1", duplicated.DraftID); err != nil {
		t.Fatalf("clone became unavailable after source delete: %v", err)
	}
}

func TestSchedulingBoundaryDuplicateDraftReplaysIdempotencyKey(t *testing.T) {
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
	first, err := boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 1,
		IdempotencyKey: "test-idempotency-retry-one",
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 1,
		IdempotencyKey: "test-idempotency-retry-one",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.DraftID != first.DraftID {
		t.Fatalf("duplicate replay = %#v, want %#v", replayed, first)
	}
}

func TestSchedulingBoundaryDuplicateDraftNormalizesWhitespaceEquivalentRetry(t *testing.T) {
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
	first, err := boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 1,
		IdempotencyKey: " test-idempotency-whitespace-retry ",
	})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  " " + created.Draft.ID + " ",
		SourceRevision: 1,
		IdempotencyKey: strings.Repeat(" ", 2) + "test-idempotency-whitespace-retry" + strings.Repeat(" ", 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.DraftID != first.DraftID {
		t.Fatalf("whitespace-normalized retry = %#v, want %#v", replayed, first)
	}
}

func TestSchedulingBoundaryDuplicateDraftCompletesPendingExistingClone(t *testing.T) {
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
	repository, ok := service.repository.(*MemoryRepository)
	if !ok {
		t.Fatal("memory repository not available")
	}
	cloneID := duplicatedDraftID(
		"workspace-1",
		created.Draft.ID,
		1,
		"test-idempotency-retry-pending",
	)
	now := service.now().UTC()
	clone, err := repository.Create(ctx, Draft{
		ID:          cloneID,
		WorkspaceID: "workspace-1",
		CreatedBy:   "account-1",
		Content:     cloneContent(created.Draft.Content),
		Revision:    1,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.duplicates[duplicateKey("workspace-1", "test-idempotency-retry-pending")] = duplicateOperation{
		WorkspaceID:      "workspace-1",
		IdempotencyKey:   "test-idempotency-retry-pending",
		SourceDraftID:    created.Draft.ID,
		SourceRevision:   1,
		CreatedByAccount: "account-1",
		Status:           duplicateOperationPending,
		LockedUntil:      now.Add(-time.Minute),
		CreatedAt:        now.Add(-2 * time.Minute),
		UpdatedAt:        now.Add(-time.Minute),
	}

	replayed, err := boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 1,
		IdempotencyKey: "test-idempotency-retry-pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || replayed.DraftID != clone.ID || replayed.DraftRevision != 1 {
		t.Fatalf("pending clone replay = %#v", replayed)
	}
	stored := repository.duplicates[duplicateKey("workspace-1", "test-idempotency-retry-pending")]
	if stored.Status != duplicateOperationCompleted || stored.CloneDraftID != clone.ID {
		t.Fatalf("pending operation not completed: %#v", stored)
	}
}

func TestSchedulingBoundaryValidateForSchedulingMapsMissingLiveObjectToValidationFailure(t *testing.T) {
	ctx := context.Background()
	media := &schedulingMediaResolverStub{
		canonical: map[string]Media{
			"image-1": validImage("image-1", "image/jpeg", 1080, 1080),
		},
		preflightErr: &FieldRuleError{
			Field:   "media[0].id",
			Rule:    "live_object_exists",
			Code:    "media_not_ready",
			Message: "Media must still exist in object storage before scheduling.",
		},
	}
	service, boundary := newSchedulingBoundaryServiceWithMedia(t, media)
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
	_, err = boundary.ValidateForScheduling(ctx, SchedulingValidationCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     created.Draft.ID,
		ChannelIDs:  []string{"channel-1"},
	})
	var failure *ValidationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("missing live object error = %#v", err)
	}
	if len(failure.Report.Errors) == 0 || failure.Report.Errors[0].Code != "media_not_ready" {
		t.Fatalf("missing live object report = %#v", failure.Report)
	}
}

func TestSchedulingBoundaryMapsUnavailablePreflightToDependencyUnavailable(t *testing.T) {
	ctx := context.Background()
	media := &schedulingMediaResolverStub{
		canonical: map[string]Media{
			"image-1": validImage("image-1", "image/jpeg", 1080, 1080),
		},
		preflightErr: ErrStorageUnavailable,
	}
	service, boundary := newSchedulingBoundaryServiceWithMedia(t, media)
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
	_, err = boundary.ValidateForScheduling(ctx, SchedulingValidationCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     created.Draft.ID,
		ChannelIDs:  []string{"channel-1"},
	})
	if !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("preflight outage error = %v", err)
	}
}

func TestSchedulingBoundaryMapsUnavailableCloneToDependencyUnavailable(t *testing.T) {
	ctx := context.Background()
	media := &schedulingMediaResolverStub{
		canonical: map[string]Media{
			"image-1": validImage("image-1", "image/jpeg", 1080, 1080),
		},
		cloneErr: errors.New("backend unavailable"),
	}
	service, boundary := newSchedulingBoundaryServiceWithMedia(t, media)
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
	_, err = boundary.DuplicateDraft(ctx, DuplicateDraftCommand{
		WorkspaceID:    "workspace-1",
		ActorID:        "account-1",
		SourceDraftID:  created.Draft.ID,
		SourceRevision: 1,
		IdempotencyKey: "test-idempotency-clone-outage",
	})
	if !errors.Is(err, ErrDependencyUnavailable) {
		t.Fatalf("clone outage error = %v", err)
	}
}

func stringPointer(value string) *string {
	return &value
}
