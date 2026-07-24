package medialibrary

import (
	"context"
	"errors"
	"testing"
	"time"
)

type authorizerStub struct {
	allowed bool
	err     error
}

func (stub authorizerStub) CanManageMedia(context.Context, string, string) (bool, error) {
	return stub.allowed, stub.err
}

type quotaStub struct {
	accepted bool
	reserved int64
	released int64
}

func (stub *quotaStub) ReserveMediaBytes(
	_ context.Context, _ string, amount int64, _ string,
) (bool, error) {
	stub.reserved += amount
	return stub.accepted, nil
}

func (stub *quotaStub) ReleaseMediaBytes(
	_ context.Context, _ string, amount int64, _ string,
) error {
	stub.released += amount
	return nil
}

type objectStoreStub struct {
	authorized int
	deleted    []string
}

func (stub *objectStoreStub) AuthorizeUpload(
	_ context.Context, _ string, contentType string, size int64, expiresAt time.Time,
) (UploadAuthorization, error) {
	stub.authorized++
	return UploadAuthorization{
		Method: "PUT", URL: "https://objects.example/upload",
		Headers: map[string]string{
			"Content-Type":   contentType,
			"Content-Length": "1024",
		},
		ExpiresAt: expiresAt,
	}, nil
}

func (stub *objectStoreStub) DeleteObject(_ context.Context, key string) error {
	stub.deleted = append(stub.deleted, key)
	return nil
}

type inspectorStub struct {
	result InspectedMedia
	err    error
}

func (stub inspectorStub) Inspect(context.Context, string) (InspectedMedia, error) {
	return stub.result, stub.err
}

type referencesStub struct{ count int64 }

func (stub *referencesStub) CountDraftReferences(
	context.Context, string, string,
) (int64, error) {
	return stub.count, nil
}

type testDependencies struct {
	service    *Service
	repository *MemoryRepository
	quota      *quotaStub
	objects    *objectStoreStub
	references *referencesStub
}

func newTestDependencies(t *testing.T) testDependencies {
	t.Helper()
	repository := NewMemoryRepository()
	quota := &quotaStub{accepted: true}
	objects := &objectStoreStub{}
	references := &referencesStub{}
	nextByte := byte(0)
	service, err := NewService(
		repository,
		authorizerStub{allowed: true},
		quota,
		objects,
		inspectorStub{result: validInspection()},
		references,
		WithClock(func() time.Time {
			return time.Date(2026, 7, 24, 20, 30, 0, 0, time.UTC)
		}),
		WithRandom(func(destination []byte) error {
			nextByte++
			for index := range destination {
				destination[index] = nextByte
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return testDependencies{
		service: service, repository: repository, quota: quota,
		objects: objects, references: references,
	}
}

func validInspection() InspectedMedia {
	return InspectedMedia{
		Kind: MediaImage, ContentType: "image/jpeg", SizeBytes: 1024,
		Width: 1080, Height: 1080, ColorSpace: "sRGB",
		ChecksumSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func createReadyAsset(t *testing.T, dependencies testDependencies) Asset {
	t.Helper()
	ticket, err := dependencies.service.CreateUpload(
		context.Background(),
		CreateUploadCommand{
			WorkspaceID: "workspace-1", ActorID: "account-1",
			OriginalName: "launch.jpg", ContentType: "image/jpeg",
			SizeBytes: 1024, IdempotencyKey: "client-request-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	asset, err := dependencies.service.CompleteUpload(
		context.Background(), "workspace-1", "account-1", ticket.Upload.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func TestUploadReservesF10QuotaAndUsesInspectedMetadata(t *testing.T) {
	dependencies := newTestDependencies(t)
	asset := createReadyAsset(t, dependencies)

	if dependencies.quota.reserved != 1024 || dependencies.objects.authorized != 1 {
		t.Fatalf(
			"reserved = %d, upload authorizations = %d",
			dependencies.quota.reserved,
			dependencies.objects.authorized,
		)
	}
	if asset.Kind != MediaImage || asset.Width != 1080 ||
		asset.ChecksumSHA256 == "" || asset.Revision != 1 {
		t.Fatalf("asset = %#v", asset)
	}
}

func TestUploadFailsClosedWhenQuotaRejects(t *testing.T) {
	dependencies := newTestDependencies(t)
	dependencies.quota.accepted = false

	_, err := dependencies.service.CreateUpload(
		context.Background(),
		CreateUploadCommand{
			WorkspaceID: "workspace-1", ActorID: "account-1",
			OriginalName: "large.mp4", ContentType: "video/mp4",
			SizeBytes: 1024, IdempotencyKey: "client-request-2",
		},
	)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("error = %v", err)
	}
	if dependencies.objects.authorized != 0 {
		t.Fatal("object upload must not be authorized after quota rejection")
	}
}

func TestCreateUploadIsIdempotentAndRejectsChangedPayload(t *testing.T) {
	dependencies := newTestDependencies(t)
	command := CreateUploadCommand{
		WorkspaceID: "workspace-1", ActorID: "account-1",
		OriginalName: "launch.jpg", ContentType: "image/jpeg",
		SizeBytes: 1024, IdempotencyKey: "same-request",
	}
	first, err := dependencies.service.CreateUpload(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := dependencies.service.CreateUpload(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if second.Upload.ID != first.Upload.ID {
		t.Fatalf("first = %s, second = %s", first.Upload.ID, second.Upload.ID)
	}
	command.SizeBytes = 2048
	if _, err := dependencies.service.CreateUpload(
		context.Background(), command,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed idempotent request error = %v", err)
	}
}

func TestCompleteRejectsObjectThatDiffersFromReservation(t *testing.T) {
	dependencies := newTestDependencies(t)
	ticket, err := dependencies.service.CreateUpload(
		context.Background(),
		CreateUploadCommand{
			WorkspaceID: "workspace-1", ActorID: "account-1",
			OriginalName: "launch.jpg", ContentType: "image/jpeg",
			SizeBytes: 2048, IdempotencyKey: "client-request-3",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dependencies.service.CompleteUpload(
		context.Background(), "workspace-1", "account-1", ticket.Upload.ID,
	)
	if !errors.Is(err, ErrUploadMismatch) {
		t.Fatalf("error = %v", err)
	}
}

func TestSearchComposerReuseAndConservativeLifecycle(t *testing.T) {
	dependencies := newTestDependencies(t)
	asset := createReadyAsset(t, dependencies)
	asset, err := dependencies.service.UpdateMetadata(
		context.Background(),
		UpdateMetadataCommand{
			WorkspaceID: "workspace-1", ActorID: "account-1", AssetID: asset.ID,
			ExpectedRevision: 1, OriginalName: "Summer Launch.jpg",
			AltText: "Product on a beach", Tags: []string{"Campaign", "Summer"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := dependencies.service.Search(
		context.Background(), "workspace-1", "account-1",
		SearchQuery{Text: "beach", Tags: []string{"summer"}},
	)
	if err != nil || len(result.Assets) != 1 {
		t.Fatalf("search = %#v, error = %v", result, err)
	}
	reference, err := dependencies.service.ResolveForComposer(
		context.Background(), "workspace-1", "account-1", asset.ID,
	)
	if err != nil || reference.StorageKey != asset.StorageKey {
		t.Fatalf("composer reference = %#v, error = %v", reference, err)
	}

	archived, err := dependencies.service.Archive(
		context.Background(), "workspace-1", "account-1", asset.ID, asset.Revision,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err = dependencies.service.Search(
		context.Background(), "workspace-1", "account-1", SearchQuery{},
	)
	if err != nil || len(result.Assets) != 0 {
		t.Fatalf("archived search = %#v, error = %v", result, err)
	}
	if _, err := dependencies.service.ResolveForComposer(
		context.Background(), "workspace-1", "account-1", asset.ID,
	); !errors.Is(err, ErrAssetArchived) {
		t.Fatalf("new reuse error = %v", err)
	}
	if _, err := dependencies.service.ResolveExistingDraft(
		context.Background(), "workspace-1", asset.ID,
	); err != nil {
		t.Fatalf("existing draft resolution error = %v", err)
	}

	dependencies.references.count = 1
	if err := dependencies.service.PurgeArchived(
		context.Background(), "workspace-1", asset.ID,
	); !errors.Is(err, ErrAssetInUse) {
		t.Fatalf("referenced purge error = %v", err)
	}
	if dependencies.quota.released != 0 || len(dependencies.objects.deleted) != 0 {
		t.Fatal("referenced asset must retain quota and object")
	}

	dependencies.references.count = 0
	if err := dependencies.service.PurgeArchived(
		context.Background(), "workspace-1", asset.ID,
	); err != nil {
		t.Fatal(err)
	}
	if dependencies.quota.released != asset.SizeBytes ||
		len(dependencies.objects.deleted) != 1 {
		t.Fatalf(
			"released = %d, deleted = %#v",
			dependencies.quota.released,
			dependencies.objects.deleted,
		)
	}
	if _, err := dependencies.service.ResolveExistingDraft(
		context.Background(), "workspace-1", archived.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged draft resolution error = %v", err)
	}
}

func TestAuthorizationAndWorkspaceBoundariesFailClosed(t *testing.T) {
	dependencies := newTestDependencies(t)
	asset := createReadyAsset(t, dependencies)
	if _, err := dependencies.service.GetAsset(
		context.Background(), "workspace-2", "account-1", asset.ID,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace error = %v", err)
	}

	service, err := NewService(
		NewMemoryRepository(),
		authorizerStub{allowed: false},
		&quotaStub{accepted: true},
		&objectStoreStub{},
		inspectorStub{},
		&referencesStub{},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Search(context.Background(), "workspace-1", "account-1", SearchQuery{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("authorization error = %v", err)
	}
}
