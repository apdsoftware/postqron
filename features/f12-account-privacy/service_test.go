package accountprivacy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAccountAreaIncludesProfileProvidersAndWorkspacePlans(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	repository.PutProfile(Profile{
		AccountID:   "account-1",
		DisplayName: "Carlo",
		Locale:      "it-IT",
		Timezone:    "Europe/Rome",
		UpdatedAt:   now,
	})
	repository.PutProviders("account-1", []Provider{{
		ID:   "google-1",
		Kind: ProviderIdentity,
		Name: "google",
	}})
	repository.PutWorkspaces("account-1", []WorkspaceRef{{
		ID:   "workspace-1",
		Name: "Editoriale",
		Role: "owner",
	}})
	adapters := defaultAdapters(now)
	adapters.plans["workspace-1"] = Plan{
		Code:       "pro",
		Name:       "Pro",
		State:      "active",
		Usage:      map[string]int64{"members": 2},
		Limits:     map[string]int64{"members": 5},
		Manageable: true,
	}
	service := newTestService(t, repository, adapters, func() time.Time { return now })

	area, err := service.AccountArea(context.Background(), recentPrincipal(now))
	if err != nil {
		t.Fatalf("read account area: %v", err)
	}
	if area.Profile.DisplayName != "Carlo" || len(area.Providers) != 1 ||
		len(area.Workspaces) != 1 || area.Workspaces[0].Plan.Code != "pro" {
		t.Fatalf("unexpected account area: %#v", area)
	}

	updated, err := service.UpdateProfile(
		context.Background(),
		recentPrincipal(now),
		ProfileUpdate{DisplayName: "  Carlo Z.  ", Locale: "it-IT", Timezone: "America/Santo_Domingo"},
	)
	if err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if updated.DisplayName != "Carlo Z." || !updated.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected profile: %#v", updated)
	}
	if _, err := service.UpdateProfile(
		context.Background(),
		recentPrincipal(now),
		ProfileUpdate{DisplayName: "Carlo", Locale: "it-IT", Timezone: "Mars/Olympus"},
	); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid timezone, got %v", err)
	}
}

func TestDisconnectProviderRequiresRecentAuthenticationAndAnotherLoginMethod(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	repository.PutProviders("account-1", []Provider{
		{
			ID:              "google-1",
			Kind:            ProviderIdentity,
			Name:            "google",
			OnlyLoginMethod: true,
		},
		{
			ID:   "linkedin-page",
			Kind: ProviderSocial,
			Name: "linkedin",
		},
	})
	adapters := defaultAdapters(now)
	service := newTestService(t, repository, adapters, func() time.Time { return now })

	stale := Principal{AccountID: "account-1", AuthenticatedAt: now.Add(-6 * time.Minute)}
	if err := service.DisconnectProvider(context.Background(), stale, "linkedin-page"); !errors.Is(
		err,
		ErrReauthenticationRequired,
	) {
		t.Fatalf("expected reauthentication error, got %v", err)
	}
	if err := service.DisconnectProvider(
		context.Background(),
		recentPrincipal(now),
		"google-1",
	); !errors.Is(err, ErrLastLoginProvider) {
		t.Fatalf("expected last provider protection, got %v", err)
	}
	if err := service.DisconnectProvider(
		context.Background(),
		recentPrincipal(now),
		"linkedin-page",
	); err != nil {
		t.Fatalf("disconnect social provider: %v", err)
	}
	if len(adapters.disconnected) != 1 || adapters.disconnected[0].ID != "linkedin-page" {
		t.Fatalf("unexpected disconnects: %#v", adapters.disconnected)
	}
}

func TestExportIsAuthorizedQueuedSignedAndTemporary(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	clock := now
	repository := NewMemoryRepository()
	adapters := defaultAdapters(now)
	service := newTestService(t, repository, adapters, func() time.Time { return clock })

	request, err := service.RequestExport(
		context.Background(),
		recentPrincipal(clock),
		ExportWorkspace,
		"workspace-1",
	)
	if err != nil {
		t.Fatalf("request export: %v", err)
	}
	if request.Status != ExportQueued || !request.ExpiresAt.Equal(now.Add(ExportRetention)) {
		t.Fatalf("unexpected export request: %#v", request)
	}
	if len(adapters.exportJobs) != 1 || adapters.exportJobs[0].WorkspaceID != "workspace-1" {
		t.Fatalf("unexpected export jobs: %#v", adapters.exportJobs)
	}

	checksum := strings.Repeat("a", 64)
	if err := service.CompleteExport(
		context.Background(),
		request.ID,
		"private/account-1/export.zip",
		checksum,
		4096,
	); err != nil {
		t.Fatalf("complete export: %v", err)
	}
	download, err := service.DownloadExport(
		context.Background(),
		recentPrincipal(clock),
		request.ID,
	)
	if err != nil {
		t.Fatalf("download export: %v", err)
	}
	if download.URL != "https://objects.invalid/signed" ||
		!download.ExpiresAt.Equal(now.Add(DownloadLinkLifetime)) ||
		adapters.signedObject != "private/account-1/export.zip" {
		t.Fatalf("unexpected download: %#v", download)
	}

	clock = now.Add(ExportRetention)
	if _, err := service.DownloadExport(
		context.Background(),
		recentPrincipal(clock),
		request.ID,
	); !errors.Is(err, ErrExportExpired) {
		t.Fatalf("expected expired export, got %v", err)
	}
	purged, err := service.PurgeExpiredExports(context.Background(), 10)
	if err != nil {
		t.Fatalf("purge expired export: %v", err)
	}
	if purged != 1 || len(adapters.deletedObjects) != 1 ||
		adapters.deletedObjects[0] != "private/account-1/export.zip" {
		t.Fatalf("expired artifact was not deleted: %#v", adapters.deletedObjects)
	}
	stored, err := repository.Export(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("read expired export: %v", err)
	}
	if stored.Status != ExportExpired || stored.ObjectKey != "" {
		t.Fatalf("expired export retained object metadata: %#v", stored)
	}
}

func TestDeletionDeactivatesImmediatelyAndCanBeCancelledDuringGracePeriod(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	adapters := defaultAdapters(now)
	actions := []OwnershipAction{{
		WorkspaceID:       "workspace-1",
		Action:            TransferWorkspace,
		TransferAccountID: "account-2",
	}}
	service := newTestService(t, repository, adapters, func() time.Time { return now })

	request, err := service.RequestDeletion(
		context.Background(),
		recentPrincipal(now),
		DeleteAccount,
		"",
		actions,
		false,
	)
	if err != nil {
		t.Fatalf("request deletion: %v", err)
	}
	if request.Status != DeletionGracePeriod ||
		!request.GraceEndsAt.Equal(now.Add(GracePeriod)) ||
		len(adapters.deactivated) != 1 {
		t.Fatalf("unexpected deletion request: %#v", request)
	}
	if err := service.CancelDeletion(
		context.Background(),
		recentPrincipal(now),
		request.ID,
	); err != nil {
		t.Fatalf("cancel deletion: %v", err)
	}
	stored, err := repository.Deletion(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("read deletion: %v", err)
	}
	if stored.Status != DeletionCancelled || len(adapters.restored) != 1 {
		t.Fatalf("unexpected cancelled deletion: %#v", stored)
	}
	events := repository.AuditEvents()
	if len(events) != 3 || events[0].Type != "deletion.requested" ||
		events[2].Type != "deletion.cancelled" {
		t.Fatalf("unexpected audit events: %#v", events)
	}
}

func TestImmediateDeletionFinalizesErasureAnonymizationAndOwnership(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	adapters := defaultAdapters(now)
	service := newTestService(t, repository, adapters, func() time.Time { return now })

	request, err := service.RequestDeletion(
		context.Background(),
		recentPrincipal(now),
		DeleteWorkspace,
		"workspace-1",
		[]OwnershipAction{{WorkspaceID: "workspace-1", Action: DeleteOwnedSpace}},
		true,
	)
	if err != nil {
		t.Fatalf("request immediate deletion: %v", err)
	}
	if !request.Immediate || !request.GraceEndsAt.Equal(now) {
		t.Fatalf("immediate deletion did not waive grace period: %#v", request)
	}
	completed, err := service.FinalizeDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("finalize deletion: %v", err)
	}
	if len(completed) != 1 || completed[0].Status != DeletionCompleted ||
		completed[0].TombstoneID != "tombstone-1" {
		t.Fatalf("unexpected completed requests: %#v", completed)
	}
	stored, err := repository.Deletion(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("read completed deletion: %v", err)
	}
	if stored.TombstoneExpiresAt == nil ||
		!stored.TombstoneExpiresAt.Equal(now.Add(TombstoneRetention)) {
		t.Fatalf("unexpected tombstone retention: %#v", stored)
	}
}

func TestIncompleteDeactivationAndErasureAreRecordedAsFailures(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	adapters := defaultAdapters(now)
	adapters.deactivation.FutureJobsCancelled = false
	service := newTestService(t, repository, adapters, func() time.Time { return now })

	_, err := service.RequestDeletion(
		context.Background(),
		recentPrincipal(now),
		DeleteAccount,
		"",
		nil,
		false,
	)
	if !errors.Is(err, ErrDeactivationIncomplete) {
		t.Fatalf("expected deactivation failure, got %v", err)
	}
	var failed DeletionRequest
	for _, event := range repository.AuditEvents() {
		if event.Type == "deletion.deactivated" && event.Outcome == "failed" {
			failed, err = repository.Deletion(context.Background(), event.TargetID)
			if err != nil {
				t.Fatalf("read failed deletion: %v", err)
			}
		}
	}
	if failed.Status != DeletionDeactivationFailed {
		t.Fatalf("deactivation failure was not persisted: %#v", failed)
	}

	secondRepository := NewMemoryRepository()
	secondAdapters := defaultAdapters(now)
	secondAdapters.erasure.SharedAttributionAnonymized = false
	secondService := newTestService(
		t,
		secondRepository,
		secondAdapters,
		func() time.Time { return now },
	)
	request, err := secondService.RequestDeletion(
		context.Background(),
		recentPrincipal(now),
		DeleteAccount,
		"",
		nil,
		true,
	)
	if err != nil {
		t.Fatalf("request deletion for erasure test: %v", err)
	}
	completed, err := secondService.FinalizeDue(context.Background(), 10)
	if !errors.Is(err, ErrFinalizationIncomplete) || len(completed) != 0 {
		t.Fatalf("expected incomplete erasure, completed=%#v err=%v", completed, err)
	}
	stored, err := secondRepository.Deletion(context.Background(), request.ID)
	if err != nil {
		t.Fatalf("read failed finalization: %v", err)
	}
	if stored.Status != DeletionFinalizationFailed {
		t.Fatalf("finalization failure was not persisted: %#v", stored)
	}
}

type testAdapters struct {
	plans          map[string]Plan
	disconnected   []Provider
	exportJobs     []ExportJob
	signedObject   string
	signedExpiry   time.Time
	deletedObjects []string
	ownership      OwnershipPlan
	deactivation   DeactivationReceipt
	deactivated    []DeletionRequest
	restored       []DeletionRequest
	erasure        ErasureReceipt
	erased         []DeletionRequest
}

func defaultAdapters(now time.Time) *testAdapters {
	return &testAdapters{
		plans: make(map[string]Plan),
		deactivation: DeactivationReceipt{
			AccessFrozen:                true,
			SessionsRevoked:             true,
			ProviderRevocationAttempted: true,
			LocalTokensDeleted:          true,
			FutureJobsCancelled:         true,
		},
		erasure: ErasureReceipt{
			IdentifyingDataDeleted:      true,
			SharedAttributionAnonymized: true,
			WorkspaceDataDeleted:        true,
			OwnershipApplied:            true,
			TombstoneID:                 "tombstone-1",
			TombstoneExpiresAt:          now.Add(TombstoneRetention),
			DatabaseCompletedAt:         now,
			MediaDeletionDueAt:          now.Add(7 * 24 * time.Hour),
		},
	}
}

func newTestService(
	t *testing.T,
	repository Repository,
	adapters *testAdapters,
	clock func() time.Time,
) *Service {
	t.Helper()
	service, err := NewService(Dependencies{
		Repository:       repository,
		Plans:            adapters,
		Providers:        adapters,
		ExportAuthorizer: adapters,
		ExportQueue:      adapters,
		DownloadSigner:   adapters,
		ExportArtifacts:  adapters,
		Ownership:        adapters,
		DeletionSafety:   adapters,
		Eraser:           adapters,
	}, WithClock(clock), WithRandom(func(destination []byte) error {
		for index := range destination {
			destination[index] = byte(index + 1)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("create service: %v", err)
	}
	return service
}

func recentPrincipal(now time.Time) Principal {
	return Principal{AccountID: "account-1", AuthenticatedAt: now.Add(-time.Minute)}
}

func (adapters *testAdapters) Plan(_ context.Context, workspaceID, _ string) (Plan, error) {
	plan, found := adapters.plans[workspaceID]
	if !found {
		return Plan{}, ErrNotFound
	}
	return plan, nil
}

func (adapters *testAdapters) Disconnect(
	_ context.Context,
	_ string,
	provider Provider,
) error {
	adapters.disconnected = append(adapters.disconnected, provider)
	return nil
}

func (adapters *testAdapters) AuthorizeExport(
	_ context.Context,
	_ string,
	_ ExportScope,
	_ string,
) error {
	return nil
}

func (adapters *testAdapters) EnqueueExport(_ context.Context, job ExportJob) error {
	adapters.exportJobs = append(adapters.exportJobs, job)
	return nil
}

func (adapters *testAdapters) SignedDownloadURL(
	_ context.Context,
	objectKey string,
	expiresAt time.Time,
) (string, error) {
	adapters.signedObject = objectKey
	adapters.signedExpiry = expiresAt
	return "https://objects.invalid/signed", nil
}

func (adapters *testAdapters) DeleteExport(_ context.Context, objectKey string) error {
	adapters.deletedObjects = append(adapters.deletedObjects, objectKey)
	return nil
}

func (adapters *testAdapters) Resolve(
	_ context.Context,
	_ string,
	_ DeletionScope,
	_ string,
	actions []OwnershipAction,
) (OwnershipPlan, error) {
	if adapters.ownership.Actions != nil {
		return adapters.ownership, nil
	}
	return OwnershipPlan{Actions: append([]OwnershipAction(nil), actions...)}, nil
}

func (adapters *testAdapters) Deactivate(
	_ context.Context,
	request DeletionRequest,
) (DeactivationReceipt, error) {
	adapters.deactivated = append(adapters.deactivated, request)
	return adapters.deactivation, nil
}

func (adapters *testAdapters) RestoreAccess(
	_ context.Context,
	request DeletionRequest,
) error {
	adapters.restored = append(adapters.restored, request)
	return nil
}

func (adapters *testAdapters) Erase(
	_ context.Context,
	request DeletionRequest,
	_ time.Time,
) (ErasureReceipt, error) {
	adapters.erased = append(adapters.erased, request)
	return adapters.erasure, nil
}
