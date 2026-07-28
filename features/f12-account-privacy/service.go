package accountprivacy

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type Service struct {
	repository  Repository
	plans       PlanReader
	providers   ProviderDisconnecter
	exports     ExportAuthorizer
	exportQueue ExportQueue
	signer      DownloadSigner
	artifacts   ExportArtifactStore
	ownership   OwnershipResolver
	safety      DeletionSafety
	eraser      Eraser
	now         func() time.Time
	random      func([]byte) error
}

type Dependencies struct {
	Repository       Repository
	Plans            PlanReader
	Providers        ProviderDisconnecter
	ExportAuthorizer ExportAuthorizer
	ExportQueue      ExportQueue
	DownloadSigner   DownloadSigner
	ExportArtifacts  ExportArtifactStore
	Ownership        OwnershipResolver
	DeletionSafety   DeletionSafety
	Eraser           Eraser
}

type Option func(*Service)

func WithClock(clock func() time.Time) Option {
	return func(service *Service) { service.now = clock }
}

func WithRandom(random func([]byte) error) Option {
	return func(service *Service) { service.random = random }
}

func NewService(dependencies Dependencies, options ...Option) (*Service, error) {
	if dependencies.Repository == nil || dependencies.Plans == nil ||
		dependencies.Providers == nil || dependencies.ExportAuthorizer == nil ||
		dependencies.ExportQueue == nil || dependencies.DownloadSigner == nil ||
		dependencies.ExportArtifacts == nil ||
		dependencies.Ownership == nil || dependencies.DeletionSafety == nil ||
		dependencies.Eraser == nil {
		return nil, fmt.Errorf("%w: all account privacy dependencies are required", ErrInvalidArgument)
	}
	service := &Service{
		repository:  dependencies.Repository,
		plans:       dependencies.Plans,
		providers:   dependencies.Providers,
		exports:     dependencies.ExportAuthorizer,
		exportQueue: dependencies.ExportQueue,
		signer:      dependencies.DownloadSigner,
		artifacts:   dependencies.ExportArtifacts,
		ownership:   dependencies.Ownership,
		safety:      dependencies.DeletionSafety,
		eraser:      dependencies.Eraser,
		now:         time.Now,
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

func (service *Service) AccountArea(ctx context.Context, principal Principal) (AccountArea, error) {
	if err := authenticate(principal); err != nil {
		return AccountArea{}, err
	}
	profile, err := service.repository.Profile(ctx, principal.AccountID)
	if err != nil {
		return AccountArea{}, err
	}
	providers, err := service.repository.Providers(ctx, principal.AccountID)
	if err != nil {
		return AccountArea{}, err
	}
	workspaces, err := service.repository.Workspaces(ctx, principal.AccountID)
	if err != nil {
		return AccountArea{}, err
	}
	result := AccountArea{
		Profile:    profile,
		Providers:  nonNilProviders(providers),
		Workspaces: make([]WorkspacePlan, 0, len(workspaces)),
	}
	for _, workspace := range workspaces {
		plan, planErr := service.plans.Plan(ctx, workspace.ID, principal.AccountID)
		if planErr != nil {
			return AccountArea{}, fmt.Errorf("read plan for workspace %s: %w", workspace.ID, planErr)
		}
		result.Workspaces = append(result.Workspaces, WorkspacePlan{Workspace: workspace, Plan: plan})
	}
	return result, nil
}

func (service *Service) UpdateProfile(
	ctx context.Context,
	principal Principal,
	update ProfileUpdate,
) (Profile, error) {
	if err := authenticate(principal); err != nil {
		return Profile{}, err
	}
	displayName := strings.TrimSpace(update.DisplayName)
	locale := strings.TrimSpace(update.Locale)
	timezone := strings.TrimSpace(update.Timezone)
	if displayName == "" || utf8.RuneCountInString(displayName) > 100 {
		return Profile{}, fmt.Errorf("%w: display name must contain 1 to 100 characters", ErrInvalidArgument)
	}
	if len(locale) < 2 || len(locale) > 35 {
		return Profile{}, fmt.Errorf("%w: locale is invalid", ErrInvalidArgument)
	}
	if timezone == "" {
		return Profile{}, fmt.Errorf("%w: timezone is required", ErrInvalidArgument)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return Profile{}, fmt.Errorf("%w: timezone must be an IANA identifier", ErrInvalidArgument)
	}
	return service.repository.UpdateProfile(ctx, ProfileUpdateCommand{
		AccountID:   principal.AccountID,
		DisplayName: displayName,
		Locale:      locale,
		Timezone:    timezone,
		Now:         service.now().UTC(),
	})
}

func (service *Service) DisconnectProvider(
	ctx context.Context,
	principal Principal,
	providerID string,
) error {
	now := service.now().UTC()
	if err := requireRecent(principal, now); err != nil {
		return err
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return fmt.Errorf("%w: provider id is required", ErrInvalidArgument)
	}
	provider, err := service.repository.Provider(ctx, principal.AccountID, providerID)
	if err != nil {
		return err
	}
	if provider.Kind == ProviderIdentity && provider.OnlyLoginMethod {
		return ErrLastLoginProvider
	}
	if provider.Kind != ProviderIdentity && provider.Kind != ProviderSocial {
		return fmt.Errorf("%w: unsupported provider kind", ErrInvalidArgument)
	}
	return service.providers.Disconnect(ctx, principal.AccountID, provider)
}

func (service *Service) RequestExport(
	ctx context.Context,
	principal Principal,
	scope ExportScope,
	workspaceID string,
) (ExportRequest, error) {
	now := service.now().UTC()
	if err := requireRecent(principal, now); err != nil {
		return ExportRequest{}, err
	}
	if scope != ExportAccount && scope != ExportWorkspace {
		return ExportRequest{}, fmt.Errorf("%w: unsupported export scope", ErrInvalidArgument)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if scope == ExportWorkspace && workspaceID == "" {
		return ExportRequest{}, fmt.Errorf("%w: workspace id is required", ErrInvalidArgument)
	}
	if scope == ExportAccount {
		workspaceID = ""
	}
	if err := service.exports.AuthorizeExport(
		ctx,
		principal.AccountID,
		scope,
		workspaceID,
	); err != nil {
		return ExportRequest{}, err
	}
	active, found, err := service.repository.ActiveExport(
		ctx,
		principal.AccountID,
		scope,
		workspaceID,
	)
	if err != nil {
		return ExportRequest{}, err
	}
	if found {
		if now.Before(active.ExpiresAt) {
			return active, nil
		}
		if active.ObjectKey != "" {
			if err := service.artifacts.DeleteExport(ctx, active.ObjectKey); err != nil {
				return ExportRequest{}, fmt.Errorf("delete expired export artifact: %w", err)
			}
		}
		if err := service.repository.MarkExportExpired(ctx, active.ID, now); err != nil {
			return ExportRequest{}, err
		}
	}
	id, err := service.newID()
	if err != nil {
		return ExportRequest{}, err
	}
	request := ExportRequest{
		ID:          id,
		AccountID:   principal.AccountID,
		Scope:       scope,
		WorkspaceID: workspaceID,
		Status:      ExportQueued,
		RequestedAt: now,
		ExpiresAt:   now.Add(ExportRetention),
	}
	if err := service.repository.CreateExport(ctx, request); err != nil {
		if errors.Is(err, ErrConflict) {
			existing, reusable, lookupErr := service.repository.ActiveExport(
				ctx,
				principal.AccountID,
				scope,
				workspaceID,
			)
			if lookupErr == nil && reusable && now.Before(existing.ExpiresAt) {
				return existing, nil
			}
		}
		return ExportRequest{}, err
	}
	if err := service.exportQueue.EnqueueExport(ctx, ExportJob{
		RequestID:   request.ID,
		AccountID:   request.AccountID,
		Scope:       request.Scope,
		WorkspaceID: request.WorkspaceID,
	}); err != nil {
		_ = service.repository.MarkExportFailed(ctx, request.ID, now)
		return ExportRequest{}, fmt.Errorf("enqueue privacy export: %w", err)
	}
	return request, nil
}

func (service *Service) CompleteExport(
	ctx context.Context,
	requestID, objectKey, checksum string,
	sizeBytes int64,
) error {
	requestID = strings.TrimSpace(requestID)
	objectKey = strings.TrimSpace(objectKey)
	checksum = strings.ToLower(strings.TrimSpace(checksum))
	digest, err := hex.DecodeString(checksum)
	if requestID == "" || objectKey == "" || err != nil || len(digest) != 32 || sizeBytes <= 0 {
		return fmt.Errorf("%w: export artifact metadata is invalid", ErrInvalidArgument)
	}
	now := service.now().UTC()
	request, err := service.repository.Export(ctx, requestID)
	if err != nil {
		return err
	}
	if !now.Before(request.ExpiresAt) {
		return ErrExportExpired
	}
	return service.repository.MarkExportReady(ctx, ExportReadyCommand{
		RequestID: requestID,
		ObjectKey: objectKey,
		SHA256:    checksum,
		SizeBytes: sizeBytes,
		ReadyAt:   now,
	})
}

func (service *Service) DownloadExport(
	ctx context.Context,
	principal Principal,
	requestID string,
) (Download, error) {
	now := service.now().UTC()
	if err := requireRecent(principal, now); err != nil {
		return Download{}, err
	}
	request, err := service.repository.Export(ctx, strings.TrimSpace(requestID))
	if err != nil {
		return Download{}, err
	}
	if request.AccountID != principal.AccountID {
		return Download{}, ErrNotFound
	}
	if !now.Before(request.ExpiresAt) {
		return Download{}, ErrExportExpired
	}
	if request.Status != ExportReady || request.ObjectKey == "" {
		return Download{}, ErrExportNotReady
	}
	expiresAt := now.Add(DownloadLinkLifetime)
	if request.ExpiresAt.Before(expiresAt) {
		expiresAt = request.ExpiresAt
	}
	url, err := service.signer.SignedDownloadURL(ctx, request.ObjectKey, expiresAt)
	if err != nil {
		return Download{}, fmt.Errorf("sign privacy export download: %w", err)
	}
	return Download{
		URL:       url,
		ExpiresAt: expiresAt,
		SHA256:    request.SHA256,
		SizeBytes: request.SizeBytes,
	}, nil
}

func (service *Service) PurgeExpiredExports(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidArgument)
	}
	now := service.now().UTC()
	requests, err := service.repository.ExpiredExports(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, request := range requests {
		if err := service.artifacts.DeleteExport(ctx, request.ObjectKey); err != nil {
			return purged, fmt.Errorf("delete expired export %s: %w", request.ID, err)
		}
		if err := service.repository.MarkExportExpired(ctx, request.ID, now); err != nil {
			return purged, err
		}
		purged++
	}
	return purged, nil
}

func (service *Service) RequestDeletion(
	ctx context.Context,
	principal Principal,
	scope DeletionScope,
	workspaceID string,
	actions []OwnershipAction,
) (DeletionRequest, error) {
	now := service.now().UTC()
	if err := requireRecent(principal, now); err != nil {
		return DeletionRequest{}, err
	}
	if scope != DeleteAccount && scope != DeleteWorkspace {
		return DeletionRequest{}, fmt.Errorf("%w: unsupported deletion scope", ErrInvalidArgument)
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if scope == DeleteWorkspace && workspaceID == "" {
		return DeletionRequest{}, fmt.Errorf("%w: workspace id is required", ErrInvalidArgument)
	}
	if scope == DeleteAccount {
		workspaceID = ""
	}
	ownership, err := service.ownership.Resolve(
		ctx,
		principal.AccountID,
		scope,
		workspaceID,
		actions,
	)
	if err != nil {
		return DeletionRequest{}, err
	}
	id, err := service.newID()
	if err != nil {
		return DeletionRequest{}, err
	}
	request := DeletionRequest{
		ID:          id,
		AccountID:   principal.AccountID,
		Scope:       scope,
		WorkspaceID: workspaceID,
		Status:      DeletionDeactivating,
		RequestedAt: now,
		GraceEndsAt: now.Add(GracePeriod),
		Ownership:   ownership,
	}
	if err := service.repository.CreateDeletion(ctx, request); err != nil {
		return DeletionRequest{}, err
	}
	receipt, deactivateErr := service.safety.Deactivate(ctx, request)
	if deactivateErr != nil || !validDeactivation(receipt) {
		_ = service.repository.MarkDeactivationFailed(
			ctx,
			request.ID,
			"deactivation_incomplete",
			now,
		)
		if deactivateErr != nil {
			return DeletionRequest{}, fmt.Errorf("%w: %v", ErrDeactivationIncomplete, deactivateErr)
		}
		return DeletionRequest{}, ErrDeactivationIncomplete
	}
	if err := service.repository.MarkGracePeriod(ctx, request.ID, now); err != nil {
		return DeletionRequest{}, err
	}
	request.Status = DeletionGracePeriod
	return request, nil
}

func (service *Service) CancelDeletion(
	ctx context.Context,
	principal Principal,
	requestID string,
) error {
	now := service.now().UTC()
	if err := requireRecent(principal, now); err != nil {
		return err
	}
	request, err := service.repository.Deletion(ctx, strings.TrimSpace(requestID))
	if err != nil {
		return err
	}
	if request.AccountID != principal.AccountID {
		return ErrNotFound
	}
	if request.Status != DeletionGracePeriod {
		return ErrDeletionInactive
	}
	if !now.Before(request.GraceEndsAt) {
		return ErrGracePeriodElapsed
	}
	// RestoreAccess must not restore sessions, provider tokens, or cancelled jobs.
	if err := service.safety.RestoreAccess(ctx, request); err != nil {
		return fmt.Errorf("restore account access: %w", err)
	}
	return service.repository.CancelDeletion(ctx, request.ID, now)
}

func (service *Service) FinalizeDue(
	ctx context.Context,
	limit int,
) ([]DeletionRequest, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: limit must be between 1 and 100", ErrInvalidArgument)
	}
	now := service.now().UTC()
	requests, err := service.repository.ClaimDueDeletions(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	completed := make([]DeletionRequest, 0, len(requests))
	incomplete := false
	for _, request := range requests {
		receipt, eraseErr := service.eraser.Erase(ctx, request, now)
		if eraseErr != nil || !validErasure(request.Scope, receipt, now) {
			incomplete = true
			_ = service.repository.MarkFinalizationFailed(
				ctx,
				request.ID,
				"erasure_incomplete",
				now,
			)
			continue
		}
		if err := service.repository.CompleteDeletion(ctx, DeletionCompleteCommand{
			RequestID:          request.ID,
			CompletedAt:        now,
			TombstoneID:        receipt.TombstoneID,
			TombstoneExpiresAt: receipt.TombstoneExpiresAt,
		}); err != nil {
			incomplete = true
			_ = service.repository.MarkFinalizationFailed(
				ctx,
				request.ID,
				"completion_persistence_failed",
				now,
			)
			continue
		}
		request.Status = DeletionCompleted
		request.CompletedAt = timePointer(now)
		request.TombstoneID = receipt.TombstoneID
		request.TombstoneExpiresAt = timePointer(receipt.TombstoneExpiresAt)
		completed = append(completed, request)
	}
	if incomplete {
		return completed, ErrFinalizationIncomplete
	}
	return completed, nil
}

func authenticate(principal Principal) error {
	if strings.TrimSpace(principal.AccountID) == "" {
		return ErrUnauthenticated
	}
	return nil
}

func requireRecent(principal Principal, now time.Time) error {
	if err := authenticate(principal); err != nil {
		return err
	}
	authenticatedAt := principal.AuthenticatedAt.UTC()
	if authenticatedAt.IsZero() || authenticatedAt.After(now.Add(time.Minute)) ||
		now.Sub(authenticatedAt) > ReauthenticationWindow {
		return ErrReauthenticationRequired
	}
	return nil
}

func validDeactivation(receipt DeactivationReceipt) bool {
	return receipt.AccessFrozen &&
		receipt.SessionsRevoked &&
		receipt.ProviderRevocationAttempted &&
		receipt.LocalTokensDeleted &&
		receipt.FutureJobsCancelled
}

func validErasure(scope DeletionScope, receipt ErasureReceipt, now time.Time) bool {
	if !receipt.IdentifyingDataDeleted ||
		!receipt.SharedAttributionAnonymized ||
		!receipt.OwnershipApplied ||
		strings.TrimSpace(receipt.TombstoneID) == "" {
		return false
	}
	if scope == DeleteWorkspace && !receipt.WorkspaceDataDeleted {
		return false
	}
	tombstoneMin := now.Add(TombstoneRetention - time.Minute)
	tombstoneMax := now.Add(TombstoneRetention + time.Minute)
	if receipt.TombstoneExpiresAt.Before(tombstoneMin) ||
		receipt.TombstoneExpiresAt.After(tombstoneMax) {
		return false
	}
	if receipt.DatabaseCompletedAt.IsZero() ||
		receipt.DatabaseCompletedAt.Before(now.Add(-time.Minute)) ||
		receipt.DatabaseCompletedAt.After(now.Add(48*time.Hour)) ||
		receipt.MediaDeletionDueAt.IsZero() ||
		receipt.MediaDeletionDueAt.Before(now.Add(-time.Minute)) ||
		receipt.MediaDeletionDueAt.After(now.Add(7*24*time.Hour)) {
		return false
	}
	return true
}

func (service *Service) newID() (string, error) {
	random := make([]byte, 18)
	if err := service.random(random); err != nil {
		return "", fmt.Errorf("generate opaque id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func nonNilProviders(providers []Provider) []Provider {
	if providers == nil {
		return []Provider{}
	}
	return providers
}

func timePointer(value time.Time) *time.Time {
	return &value
}
