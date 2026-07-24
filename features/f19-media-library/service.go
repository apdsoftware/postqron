package medialibrary

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	maxUploadBytes  = int64(5 << 30)
	maxNameBytes    = 255
	maxAltTextRunes = 2_000
	maxTags         = 20
)

type Authorizer interface {
	CanManageMedia(context.Context, string, string) (bool, error)
}

// MediaQuota is implemented by the server-side F10 adapter. Browser requests
// never supply limits or usage decisions.
type MediaQuota interface {
	ReserveMediaBytes(context.Context, string, int64, string) (bool, error)
	ReleaseMediaBytes(context.Context, string, int64, string) error
}

type ObjectStore interface {
	AuthorizeUpload(context.Context, string, string, int64, time.Time) (UploadAuthorization, error)
	DeleteObject(context.Context, string) error
}

type Inspector interface {
	Inspect(context.Context, string) (InspectedMedia, error)
}

// DraftReferences is the F6 server-side lifecycle boundary.
type DraftReferences interface {
	CountDraftReferences(context.Context, string, string) (int64, error)
}

type Repository interface {
	CreateUpload(context.Context, Upload) (Upload, bool, error)
	GetUpload(context.Context, string, string) (Upload, error)
	CancelUpload(context.Context, string, string) error
	CompleteUpload(context.Context, Upload, Asset) (Asset, bool, error)
	GetAsset(context.Context, string, string) (Asset, error)
	Search(context.Context, string, SearchQuery) ([]Asset, error)
	UpdateMetadata(context.Context, Asset, int64) (Asset, error)
	Archive(context.Context, string, string, int64, time.Time) (Asset, error)
	MarkPurged(context.Context, string, string, int64, time.Time) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	quota      MediaQuota
	objects    ObjectStore
	inspector  Inspector
	references DraftReferences
	now        func() time.Time
	random     func([]byte) error
}

type ServiceOption func(*Service)

func WithClock(clock func() time.Time) ServiceOption {
	return func(service *Service) { service.now = clock }
}

func WithRandom(random func([]byte) error) ServiceOption {
	return func(service *Service) { service.random = random }
}

func NewService(
	repository Repository,
	authorizer Authorizer,
	quota MediaQuota,
	objects ObjectStore,
	inspector Inspector,
	references DraftReferences,
	options ...ServiceOption,
) (*Service, error) {
	if repository == nil || authorizer == nil || quota == nil || objects == nil ||
		inspector == nil || references == nil {
		return nil, fmt.Errorf("%w: all media service dependencies are required", ErrInvalidArgument)
	}
	service := &Service{
		repository: repository,
		authorizer: authorizer,
		quota:      quota,
		objects:    objects,
		inspector:  inspector,
		references: references,
		now:        time.Now,
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

func (service *Service) CreateUpload(
	ctx context.Context,
	command CreateUploadCommand,
) (UploadTicket, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return UploadTicket{}, err
	}
	name, contentType, err := normalizeUploadInput(
		command.OriginalName,
		command.ContentType,
		command.SizeBytes,
		command.IdempotencyKey,
	)
	if err != nil {
		return UploadTicket{}, err
	}
	uploadID, err := service.randomID("upload_")
	if err != nil {
		return UploadTicket{}, err
	}
	assetID, err := service.randomID("media_")
	if err != nil {
		return UploadTicket{}, err
	}
	now := service.now().UTC()
	upload := Upload{
		ID:                uploadID,
		AssetID:           assetID,
		WorkspaceID:       strings.TrimSpace(command.WorkspaceID),
		CreatedBy:         strings.TrimSpace(command.ActorID),
		StorageKey:        "workspaces/" + strings.TrimSpace(command.WorkspaceID) + "/media/" + assetID,
		OriginalName:      name,
		DeclaredType:      contentType,
		ReservedSizeBytes: command.SizeBytes,
		IdempotencyKey:    strings.TrimSpace(command.IdempotencyKey),
		Status:            UploadPending,
		ExpiresAt:         now.Add(15 * time.Minute),
		CreatedAt:         now,
	}
	accepted, err := service.quota.ReserveMediaBytes(
		ctx,
		upload.WorkspaceID,
		upload.ReservedSizeBytes,
		"f19:"+command.IdempotencyKey,
	)
	if err != nil {
		return UploadTicket{}, fmt.Errorf("reserve F10 media quota: %w", err)
	}
	if !accepted {
		return UploadTicket{}, ErrQuotaExceeded
	}
	stored, created, err := service.repository.CreateUpload(ctx, upload)
	if err != nil {
		_ = service.quota.ReleaseMediaBytes(
			ctx, upload.WorkspaceID, upload.ReservedSizeBytes, "f19:"+upload.ID+":rollback",
		)
		return UploadTicket{}, err
	}
	upload = stored
	if !created && (upload.OriginalName != name ||
		upload.DeclaredType != contentType ||
		upload.ReservedSizeBytes != command.SizeBytes) {
		return UploadTicket{}, ErrConflict
	}
	if !created && upload.Status == UploadCompleted {
		return UploadTicket{Upload: upload}, nil
	}
	if !created && upload.Status != UploadPending {
		return UploadTicket{}, ErrConflict
	}
	if !created && !now.Before(upload.ExpiresAt) {
		return UploadTicket{}, ErrUploadExpired
	}
	authorization, err := service.objects.AuthorizeUpload(
		ctx,
		upload.StorageKey,
		upload.DeclaredType,
		upload.ReservedSizeBytes,
		upload.ExpiresAt,
	)
	if err != nil {
		_ = service.repository.CancelUpload(ctx, upload.WorkspaceID, upload.ID)
		_ = service.quota.ReleaseMediaBytes(
			ctx, upload.WorkspaceID, upload.ReservedSizeBytes, "f19:"+upload.ID+":authorize-failed",
		)
		return UploadTicket{}, fmt.Errorf("authorize object upload: %w", err)
	}
	return UploadTicket{Upload: upload, Authorization: authorization}, nil
}

func (service *Service) CompleteUpload(
	ctx context.Context,
	workspaceID, actorID, uploadID string,
) (Asset, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return Asset{}, err
	}
	upload, err := service.repository.GetUpload(ctx, workspaceID, strings.TrimSpace(uploadID))
	if err != nil {
		return Asset{}, err
	}
	if upload.Status == UploadCompleted {
		return service.repository.GetAsset(ctx, workspaceID, upload.AssetID)
	}
	if upload.Status != UploadPending {
		return Asset{}, ErrConflict
	}
	now := service.now().UTC()
	if !now.Before(upload.ExpiresAt) {
		return Asset{}, ErrUploadExpired
	}
	inspected, err := service.inspector.Inspect(ctx, upload.StorageKey)
	if err != nil {
		return Asset{}, fmt.Errorf("inspect uploaded media: %w", err)
	}
	if err := validateInspected(upload, inspected); err != nil {
		return Asset{}, err
	}
	asset := Asset{
		ID:                  upload.AssetID,
		WorkspaceID:         upload.WorkspaceID,
		CreatedBy:           upload.CreatedBy,
		StorageKey:          upload.StorageKey,
		OriginalName:        upload.OriginalName,
		Kind:                inspected.Kind,
		ContentType:         inspected.ContentType,
		SizeBytes:           inspected.SizeBytes,
		Width:               inspected.Width,
		Height:              inspected.Height,
		ColorSpace:          inspected.ColorSpace,
		VideoCodec:          inspected.VideoCodec,
		AudioCodec:          inspected.AudioCodec,
		AudioSampleRate:     inspected.AudioSampleRate,
		FramesPerSecond:     inspected.FramesPerSecond,
		VideoBitrate:        inspected.VideoBitrate,
		AudioBitrate:        inspected.AudioBitrate,
		DurationSeconds:     inspected.DurationSeconds,
		HasAudio:            inspected.HasAudio,
		HasEditList:         inspected.HasEditList,
		MoovBeforeMediaData: inspected.MoovBeforeMediaData,
		ChecksumSHA256:      strings.ToLower(inspected.ChecksumSHA256),
		Tags:                []string{},
		Status:              StatusReady,
		Revision:            1,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	asset, _, err = service.repository.CompleteUpload(ctx, upload, asset)
	return asset, err
}

func (service *Service) GetAsset(
	ctx context.Context,
	workspaceID, actorID, assetID string,
) (Asset, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return Asset{}, err
	}
	asset, err := service.repository.GetAsset(ctx, workspaceID, strings.TrimSpace(assetID))
	if err != nil {
		return Asset{}, err
	}
	if asset.Status == StatusPurged {
		return Asset{}, ErrNotFound
	}
	return asset, nil
}

func (service *Service) Search(
	ctx context.Context,
	workspaceID, actorID string,
	query SearchQuery,
) (SearchResult, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return SearchResult{}, err
	}
	query.Text = strings.ToLower(strings.TrimSpace(query.Text))
	if query.Kind != "" && query.Kind != MediaImage && query.Kind != MediaVideo {
		return SearchResult{}, fmt.Errorf("%w: unknown media kind", ErrInvalidArgument)
	}
	tags, err := normalizeTags(query.Tags)
	if err != nil {
		return SearchResult{}, err
	}
	query.Tags = tags
	if query.Limit == 0 {
		query.Limit = 25
	}
	if query.Limit < 1 || query.Limit > 100 {
		return SearchResult{}, fmt.Errorf("%w: search limit must be between 1 and 100", ErrInvalidArgument)
	}
	assets, err := service.repository.Search(ctx, strings.TrimSpace(workspaceID), query)
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{Assets: assets}, nil
}

func (service *Service) UpdateMetadata(
	ctx context.Context,
	command UpdateMetadataCommand,
) (Asset, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Asset{}, err
	}
	if command.ExpectedRevision < 1 {
		return Asset{}, fmt.Errorf("%w: positive revision is required", ErrInvalidArgument)
	}
	name, err := normalizeName(command.OriginalName)
	if err != nil {
		return Asset{}, err
	}
	if len([]rune(command.AltText)) > maxAltTextRunes {
		return Asset{}, fmt.Errorf("%w: alt text is too long", ErrInvalidArgument)
	}
	tags, err := normalizeTags(command.Tags)
	if err != nil {
		return Asset{}, err
	}
	asset, err := service.repository.GetAsset(ctx, command.WorkspaceID, command.AssetID)
	if err != nil {
		return Asset{}, err
	}
	if asset.Status != StatusReady {
		return Asset{}, ErrAssetArchived
	}
	asset.OriginalName = name
	asset.AltText = strings.TrimSpace(command.AltText)
	asset.Tags = tags
	asset.UpdatedAt = service.now().UTC()
	return service.repository.UpdateMetadata(ctx, asset, command.ExpectedRevision)
}

func (service *Service) Archive(
	ctx context.Context,
	workspaceID, actorID, assetID string,
	expectedRevision int64,
) (Asset, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return Asset{}, err
	}
	if expectedRevision < 1 {
		return Asset{}, fmt.Errorf("%w: positive revision is required", ErrInvalidArgument)
	}
	return service.repository.Archive(
		ctx, workspaceID, strings.TrimSpace(assetID), expectedRevision, service.now().UTC(),
	)
}

func (service *Service) ResolveForComposer(
	ctx context.Context,
	workspaceID, actorID, assetID string,
) (ComposerMedia, error) {
	asset, err := service.GetAsset(ctx, workspaceID, actorID, assetID)
	if err != nil {
		return ComposerMedia{}, err
	}
	if asset.Status != StatusReady {
		return ComposerMedia{}, ErrAssetArchived
	}
	return composerMedia(asset), nil
}

// ResolveExistingDraft keeps archived assets readable for already-saved F6
// drafts. It is a server-only boundary and must not be used to add new media.
func (service *Service) ResolveExistingDraft(
	ctx context.Context,
	workspaceID, assetID string,
) (ComposerMedia, error) {
	asset, err := service.repository.GetAsset(ctx, workspaceID, strings.TrimSpace(assetID))
	if err != nil {
		return ComposerMedia{}, err
	}
	if asset.Status == StatusPurged {
		return ComposerMedia{}, ErrNotFound
	}
	return composerMedia(asset), nil
}

func (service *Service) PurgeArchived(
	ctx context.Context,
	workspaceID, assetID string,
) error {
	asset, err := service.repository.GetAsset(ctx, workspaceID, strings.TrimSpace(assetID))
	if err != nil {
		return err
	}
	if asset.Status == StatusPurged {
		return nil
	}
	if asset.Status != StatusArchived {
		return ErrConflict
	}
	count, err := service.references.CountDraftReferences(ctx, workspaceID, assetID)
	if err != nil {
		return fmt.Errorf("check F6 draft references: %w", err)
	}
	if count != 0 {
		return ErrAssetInUse
	}
	if err := service.quota.ReleaseMediaBytes(
		ctx, workspaceID, asset.SizeBytes, "f19:"+asset.ID+":purge",
	); err != nil {
		return fmt.Errorf("release F10 media quota: %w", err)
	}
	if err := service.objects.DeleteObject(ctx, asset.StorageKey); err != nil {
		return fmt.Errorf("delete media object: %w", err)
	}
	return service.repository.MarkPurged(
		ctx, workspaceID, assetID, asset.Revision, service.now().UTC(),
	)
}

func (service *Service) authorize(ctx context.Context, workspaceID, actorID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return ErrUnauthenticated
	}
	if workspaceID == "" {
		return fmt.Errorf("%w: workspace id is required", ErrInvalidArgument)
	}
	allowed, err := service.authorizer.CanManageMedia(ctx, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("authorize media management: %w", err)
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (service *Service) randomID(prefix string) (string, error) {
	buffer := make([]byte, 18)
	if err := service.random(buffer); err != nil {
		return "", fmt.Errorf("generate media id: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buffer), nil
}

func normalizeUploadInput(name, contentType string, size int64, key string) (string, string, error) {
	name, err := normalizeName(name)
	if err != nil {
		return "", "", err
	}
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if !strings.HasPrefix(contentType, "image/") && !strings.HasPrefix(contentType, "video/") {
		return "", "", fmt.Errorf("%w: only image and video uploads are supported", ErrInvalidArgument)
	}
	if size < 1 || size > maxUploadBytes {
		return "", "", fmt.Errorf("%w: upload size is outside the supported range", ErrInvalidArgument)
	}
	if strings.TrimSpace(key) == "" || len(key) > 200 {
		return "", "", fmt.Errorf("%w: idempotency key is required", ErrInvalidArgument)
	}
	return name, contentType, nil
}

func normalizeName(name string) (string, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "" || len([]byte(name)) > maxNameBytes {
		return "", fmt.Errorf("%w: original name is required and must be at most 255 bytes", ErrInvalidArgument)
	}
	return name, nil
}

func normalizeTags(tags []string) ([]string, error) {
	if len(tags) > maxTags {
		return nil, fmt.Errorf("%w: at most 20 tags are allowed", ErrInvalidArgument)
	}
	seen := make(map[string]struct{}, len(tags))
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag == "" || len([]rune(tag)) > 50 {
			return nil, fmt.Errorf("%w: tags must contain 1 to 50 characters", ErrInvalidArgument)
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func validateInspected(upload Upload, inspected InspectedMedia) error {
	inspected.ContentType = strings.ToLower(strings.TrimSpace(inspected.ContentType))
	if inspected.SizeBytes != upload.ReservedSizeBytes ||
		inspected.ContentType != upload.DeclaredType {
		return ErrUploadMismatch
	}
	if inspected.Kind != MediaImage && inspected.Kind != MediaVideo {
		return fmt.Errorf("%w: inspector returned an unsupported kind", ErrUploadMismatch)
	}
	if (inspected.Kind == MediaImage && !strings.HasPrefix(inspected.ContentType, "image/")) ||
		(inspected.Kind == MediaVideo && !strings.HasPrefix(inspected.ContentType, "video/")) {
		return fmt.Errorf("%w: media kind and content type differ", ErrUploadMismatch)
	}
	if inspected.Width < 1 || inspected.Height < 1 ||
		len(inspected.ChecksumSHA256) != 64 {
		return fmt.Errorf("%w: inspected metadata is incomplete", ErrUploadMismatch)
	}
	if _, err := hex.DecodeString(inspected.ChecksumSHA256); err != nil {
		return fmt.Errorf("%w: checksum is not hexadecimal", ErrUploadMismatch)
	}
	if inspected.Kind == MediaVideo && inspected.DurationSeconds <= 0 {
		return fmt.Errorf("%w: video duration is required", ErrUploadMismatch)
	}
	return nil
}

func composerMedia(asset Asset) ComposerMedia {
	return ComposerMedia{
		ID: asset.ID, StorageKey: asset.StorageKey, Kind: asset.Kind,
		ContentType: asset.ContentType, SizeBytes: asset.SizeBytes,
		Width: asset.Width, Height: asset.Height, ColorSpace: asset.ColorSpace,
		VideoCodec: asset.VideoCodec, AudioCodec: asset.AudioCodec,
		AudioSampleRate: asset.AudioSampleRate, FramesPerSecond: asset.FramesPerSecond,
		VideoBitrate: asset.VideoBitrate, AudioBitrate: asset.AudioBitrate,
		DurationSeconds: asset.DurationSeconds, HasAudio: asset.HasAudio,
		HasEditList: asset.HasEditList, MoovBeforeMediaData: asset.MoovBeforeMediaData,
	}
}
