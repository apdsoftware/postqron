package dynamic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const (
	mastodonInstancePath = "/api/v2/instance"
	mastodonMediaPath    = "/api/v2/media"
	mastodonStatusesPath = "/api/v1/statuses"
	mastodonReplayWindow = time.Hour
)

type Mastodon struct {
	executor authenticatedExecutor
	media    MediaSource
	clock    func() time.Time
}

func NewMastodon(
	executor *socialconnections.AuthenticatedExecutor,
	mediaSource MediaSource,
	clock func() time.Time,
) (*Mastodon, error) {
	if executor == nil || clock == nil {
		return nil, publishing.ErrInvalidArgument
	}
	return &Mastodon{executor: executor, media: mediaSource, clock: clock}, nil
}

func newMastodonForTest(
	executor authenticatedExecutor,
	mediaSource MediaSource,
) *Mastodon {
	return &Mastodon{executor: executor, media: mediaSource, clock: time.Now}
}

func (*Mastodon) Capabilities() publishing.AdapterCapabilities {
	// Mastodon officially accepts Idempotency-Key on status creation. Media
	// upload checkpoints are separately fail-closed after ambiguous outcomes.
	return capabilities(true)
}

type mastodonPayload struct {
	Text        string  `json:"text"`
	SpoilerText string  `json:"spoiler_text,omitempty"`
	Visibility  string  `json:"visibility"`
	Language    string  `json:"language,omitempty"`
	Sensitive   bool    `json:"sensitive"`
	Media       []media `json:"media,omitempty"`
}

type mastodonCapabilities struct {
	MaxCharacters            int      `json:"max_characters"`
	CharactersReservedPerURL int      `json:"characters_reserved_per_url"`
	MaxAttachments           int      `json:"max_attachments"`
	DescriptionLimit         int      `json:"description_limit"`
	MIMETypes                []string `json:"mime_types"`
	ImageBytes               int64    `json:"image_bytes"`
	ImageMatrixLimit         int64    `json:"image_matrix_limit"`
	VideoBytes               int64    `json:"video_bytes"`
	VideoMatrixLimit         int64    `json:"video_matrix_limit"`
	VideoFrameRateLimit      float64  `json:"video_frame_rate_limit"`
}

type mastodonCheckpoint struct {
	Step             string               `json:"step"`
	Capabilities     mastodonCapabilities `json:"capabilities,omitempty"`
	MediaIndex       int                  `json:"media_index,omitempty"`
	MediaIDs         []string             `json:"media_ids,omitempty"`
	PendingID        string               `json:"pending_media_id,omitempty"`
	StatusPreparedAt time.Time            `json:"status_prepared_at,omitempty"`
}

type mastodonInstanceEnvelope struct {
	Configuration struct {
		Statuses struct {
			MaxCharacters            int `json:"max_characters"`
			CharactersReservedPerURL int `json:"characters_reserved_per_url"`
			MaxMediaAttachments      int `json:"max_media_attachments"`
		} `json:"statuses"`
		MediaAttachments struct {
			SupportedMIMETypes  []string `json:"supported_mime_types"`
			DescriptionLimit    int      `json:"description_limit"`
			ImageSizeLimit      int64    `json:"image_size_limit"`
			ImageMatrixLimit    int64    `json:"image_matrix_limit"`
			VideoSizeLimit      int64    `json:"video_size_limit"`
			VideoMatrixLimit    int64    `json:"video_matrix_limit"`
			VideoFrameRateLimit float64  `json:"video_frame_rate_limit"`
		} `json:"media_attachments"`
	} `json:"configuration"`
}

type mastodonMediaEnvelope struct {
	ID  string  `json:"id"`
	URL *string `json:"url"`
}

type mastodonStatusEnvelope struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	URI string `json:"uri"`
}

func (adapter *Mastodon) Publish(
	ctx context.Context,
	request publishing.PublishRequest,
) (publishing.PublishResult, error) {
	payload, state, err := adapter.input(request)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	switch state.Step {
	case "":
		state.Step = "capability_pending"
		return mastodonProgress(state, 0)
	case "capability_pending":
		response, callErr := execute(
			ctx, adapter.executor, socialconnections.ProviderMastodon, request,
			http.MethodGet, mastodonInstancePath, nil, nil, nil,
		)
		if callErr != nil {
			return publishing.PublishResult{}, callErr
		}
		capability, parseErr := decodeMastodonCapabilities(response.Body)
		if parseErr != nil {
			return publishing.PublishResult{}, parseErr
		}
		if validationErr := validateMastodonPayload(
			payload,
			capability,
		); validationErr != nil {
			return publishing.PublishResult{}, validationErr
		}
		state.Capabilities = capability
		if len(payload.Media) == 0 {
			state.Step = "status_pending"
			state.StatusPreparedAt = adapter.clock().UTC()
		} else {
			state.Step = "media_upload_pending"
		}
		return mastodonProgress(state, 0)
	case "media_upload_pending":
		if state.MediaIndex < 0 || state.MediaIndex >= len(payload.Media) ||
			adapter.media == nil {
			return publishing.PublishResult{}, permanent(
				"mastodon_media_unavailable",
				"Mastodon media source or checkpoint is unavailable.",
			)
		}
		upload, contentType, buildErr := adapter.multipartMedia(
			ctx, payload.Media[state.MediaIndex],
		)
		if buildErr != nil {
			return publishing.PublishResult{}, buildErr
		}
		response, callErr := execute(
			ctx, adapter.executor, socialconnections.ProviderMastodon, request,
			http.MethodPost, mastodonMediaPath,
			http.Header{"Content-Type": {contentType}},
			nil, upload,
		)
		if callErr != nil {
			return publishing.PublishResult{}, callErr
		}
		var envelope mastodonMediaEnvelope
		if json.Unmarshal(response.Body, &envelope) != nil ||
			!mastodonIDPattern.MatchString(envelope.ID) {
			return publishing.PublishResult{}, permanent(
				"invalid_mastodon_media",
				"Mastodon did not return a valid media identifier.",
			)
		}
		state.PendingID = envelope.ID
		state.Step = "media_processing"
		if envelope.URL != nil && strings.TrimSpace(*envelope.URL) != "" {
			state.MediaIDs = append(state.MediaIDs, envelope.ID)
			state.MediaIndex++
			state.PendingID = ""
			state.Step = mastodonNextStep(state.MediaIndex, len(payload.Media))
			adapter.stampStatusPending(&state)
		}
		return mastodonProgress(
			state,
			responseRetryAfter(response, 2*time.Second),
		)
	case "media_processing":
		if !mastodonIDPattern.MatchString(state.PendingID) {
			return publishing.PublishResult{}, permanent(
				"invalid_mastodon_checkpoint",
				"Mastodon media checkpoint is invalid.",
			)
		}
		response, callErr := execute(
			ctx, adapter.executor, socialconnections.ProviderMastodon, request,
			http.MethodGet, "/api/v1/media/"+state.PendingID, nil, nil, nil,
		)
		if callErr != nil {
			return publishing.PublishResult{}, callErr
		}
		var envelope mastodonMediaEnvelope
		if json.Unmarshal(response.Body, &envelope) != nil ||
			envelope.ID != state.PendingID {
			return publishing.PublishResult{}, permanent(
				"invalid_mastodon_media",
				"Mastodon returned invalid media processing state.",
			)
		}
		if envelope.URL == nil || strings.TrimSpace(*envelope.URL) == "" {
			return mastodonProgress(
				state,
				responseRetryAfter(response, 2*time.Second),
			)
		}
		state.MediaIDs = append(state.MediaIDs, state.PendingID)
		state.MediaIndex++
		state.PendingID = ""
		state.Step = mastodonNextStep(state.MediaIndex, len(payload.Media))
		adapter.stampStatusPending(&state)
		return mastodonProgress(state, 0)
	case "status_pending":
		return adapter.publishStatus(ctx, request, payload, state)
	default:
		return publishing.PublishResult{}, permanent(
			"invalid_mastodon_checkpoint",
			"Mastodon checkpoint step is invalid.",
		)
	}
}

func (adapter *Mastodon) Reconcile(
	ctx context.Context,
	request publishing.ReconcileRequest,
) (publishing.ReconcileResult, error) {
	payload, state, err := adapter.input(publishing.PublishRequest{
		Payload: request.Payload, Checkpoint: request.Checkpoint,
	})
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	if state.Step == "media_upload_pending" {
		// Mastodon has no official lookup by media upload request. Re-uploading
		// after an ambiguous write would be blind, so the destination remains
		// fail-closed for operator reconciliation.
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "Mastodon media upload outcome cannot be reconciled safely.",
		}, nil
	}
	if state.Step != "status_pending" {
		return publishing.ReconcileResult{
			State: publishing.ReconciliationNotFound,
		}, nil
	}
	result, err := adapter.publishStatus(ctx, publishing.PublishRequest{
		WorkspaceID: request.WorkspaceID, PostID: request.PostID,
		ChannelID: request.ChannelID, ConnectionID: request.ConnectionID,
		Payload: request.Payload, Checkpoint: request.Checkpoint,
		IdempotencyKey: request.IdempotencyKey,
	}, payload, state)
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	if !result.Complete {
		return publishing.ReconcileResult{State: publishing.ReconciliationUnknown}, nil
	}
	return publishing.ReconcileResult{
		State:    publishing.ReconciliationFound,
		RemoteID: result.RemoteID, Permalink: result.Permalink,
		Checkpoint: result.Checkpoint,
	}, nil
}

func (adapter *Mastodon) publishStatus(
	ctx context.Context,
	request publishing.PublishRequest,
	payload mastodonPayload,
	state mastodonCheckpoint,
) (publishing.PublishResult, error) {
	if err := adapter.validateReplayWindow(state); err != nil {
		return publishing.PublishResult{}, err
	}
	if !publishingKeyPattern.MatchString(request.IdempotencyKey) {
		return publishing.PublishResult{}, permanent(
			"invalid_mastodon_idempotency",
			"Mastodon idempotency key is invalid.",
		)
	}
	body, err := jsonBody(map[string]any{
		"status":       payload.Text,
		"spoiler_text": payload.SpoilerText,
		"visibility":   payload.Visibility,
		"language":     payload.Language,
		"sensitive":    payload.Sensitive,
		"media_ids":    state.MediaIDs,
	}, "invalid_mastodon_payload")
	if err != nil {
		return publishing.PublishResult{}, err
	}
	response, callErr := execute(
		ctx, adapter.executor, socialconnections.ProviderMastodon, request,
		http.MethodPost, mastodonStatusesPath,
		http.Header{
			"Content-Type":    {"application/json"},
			"Idempotency-Key": {request.IdempotencyKey},
		},
		body, nil,
	)
	if callErr != nil {
		return publishing.PublishResult{}, callErr
	}
	var envelope mastodonStatusEnvelope
	if json.Unmarshal(response.Body, &envelope) != nil ||
		!mastodonIDPattern.MatchString(envelope.ID) {
		return publishing.PublishResult{}, permanent(
			"invalid_mastodon_status",
			"Mastodon did not return a valid status identifier.",
		)
	}
	permalink := strings.TrimSpace(envelope.URL)
	if permalink == "" {
		permalink = strings.TrimSpace(envelope.URI)
	}
	if !validHTTPSPermalink(permalink) {
		return publishing.PublishResult{}, permanent(
			"invalid_mastodon_permalink",
			"Mastodon did not return a valid status permalink.",
		)
	}
	state.Step = "complete"
	finalCheckpoint, err := encodeCheckpoint(
		state,
		"invalid_mastodon_checkpoint",
	)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	return publishing.PublishResult{
		Complete: true, RemoteID: envelope.ID, Permalink: permalink,
		Checkpoint: finalCheckpoint,
	}, nil
}

func (adapter *Mastodon) input(
	request publishing.PublishRequest,
) (mastodonPayload, mastodonCheckpoint, error) {
	var payload mastodonPayload
	var state mastodonCheckpoint
	if err := decodePayload(
		request.Payload, &payload, "invalid_mastodon_payload",
	); err != nil {
		return payload, state, err
	}
	if err := decodeCheckpoint(
		request.Checkpoint, &state, "invalid_mastodon_checkpoint",
	); err != nil {
		return payload, state, err
	}
	if !utf8.ValidString(payload.Text) ||
		(strings.TrimSpace(payload.Text) == "" && len(payload.Media) == 0) ||
		len(payload.Media) > 16 || state.MediaIndex < 0 ||
		state.MediaIndex > len(payload.Media) ||
		len(state.MediaIDs) != state.MediaIndex {
		return payload, state, permanent(
			"invalid_mastodon_payload",
			"Mastodon post metadata is invalid.",
		)
	}
	for _, item := range payload.Media {
		if !validMedia(item) || len([]rune(item.Alt)) > 1500 {
			return payload, state, permanent(
				"invalid_mastodon_media",
				"Mastodon media metadata is invalid.",
			)
		}
	}
	if state.Step == "capability_pending" {
		if state.MediaIndex != 0 || len(state.MediaIDs) != 0 ||
			state.PendingID != "" {
			return payload, state, permanent(
				"invalid_mastodon_checkpoint",
				"Mastodon capability checkpoint is invalid.",
			)
		}
		return payload, state, nil
	}
	if state.Step != "" {
		if err := validateMastodonPayload(
			payload,
			state.Capabilities,
		); err != nil {
			return payload, state, err
		}
		seen := make(map[string]struct{}, len(state.MediaIDs))
		for _, mediaID := range state.MediaIDs {
			if !mastodonIDPattern.MatchString(mediaID) {
				return payload, state, permanent(
					"invalid_mastodon_checkpoint",
					"Mastodon media checkpoint is invalid.",
				)
			}
			if _, duplicate := seen[mediaID]; duplicate {
				return payload, state, permanent(
					"invalid_mastodon_checkpoint",
					"Mastodon media checkpoint contains a duplicate.",
				)
			}
			seen[mediaID] = struct{}{}
		}
		switch state.Step {
		case "media_upload_pending":
			if state.MediaIndex >= len(payload.Media) || state.PendingID != "" {
				return payload, state, permanent(
					"invalid_mastodon_checkpoint",
					"Mastodon upload checkpoint is invalid.",
				)
			}
		case "media_processing":
			if state.MediaIndex >= len(payload.Media) ||
				!mastodonIDPattern.MatchString(state.PendingID) {
				return payload, state, permanent(
					"invalid_mastodon_checkpoint",
					"Mastodon processing checkpoint is invalid.",
				)
			}
		case "status_pending":
			if state.MediaIndex != len(payload.Media) || state.PendingID != "" ||
				state.StatusPreparedAt.IsZero() {
				return payload, state, permanent(
					"invalid_mastodon_checkpoint",
					"Mastodon status checkpoint is invalid.",
				)
			}
		default:
			return payload, state, permanent(
				"invalid_mastodon_checkpoint",
				"Mastodon checkpoint step is invalid.",
			)
		}
	}
	return payload, state, nil
}

func decodeMastodonCapabilities(
	body []byte,
) (mastodonCapabilities, error) {
	var envelope mastodonInstanceEnvelope
	if json.Unmarshal(body, &envelope) != nil {
		return mastodonCapabilities{}, permanent(
			"invalid_mastodon_capabilities",
			"Mastodon instance capabilities are invalid.",
		)
	}
	value := mastodonCapabilities{
		MaxCharacters:            envelope.Configuration.Statuses.MaxCharacters,
		CharactersReservedPerURL: envelope.Configuration.Statuses.CharactersReservedPerURL,
		MaxAttachments:           envelope.Configuration.Statuses.MaxMediaAttachments,
		DescriptionLimit:         envelope.Configuration.MediaAttachments.DescriptionLimit,
		MIMETypes: append(
			[]string(nil),
			envelope.Configuration.MediaAttachments.SupportedMIMETypes...,
		),
		ImageBytes:          envelope.Configuration.MediaAttachments.ImageSizeLimit,
		ImageMatrixLimit:    envelope.Configuration.MediaAttachments.ImageMatrixLimit,
		VideoBytes:          envelope.Configuration.MediaAttachments.VideoSizeLimit,
		VideoMatrixLimit:    envelope.Configuration.MediaAttachments.VideoMatrixLimit,
		VideoFrameRateLimit: envelope.Configuration.MediaAttachments.VideoFrameRateLimit,
	}
	if !validMastodonCapabilityDocument(value) {
		return mastodonCapabilities{}, permanent(
			"invalid_mastodon_capabilities",
			"Mastodon instance capabilities are incomplete.",
		)
	}
	return value, nil
}

func validateMastodonPayload(
	payload mastodonPayload,
	capability mastodonCapabilities,
) error {
	if !validMastodonCapabilityDocument(capability) {
		return permanent(
			"invalid_mastodon_capabilities",
			"Mastodon instance capabilities are incomplete.",
		)
	}
	textLength := mastodonTextLength(
		payload.Text, capability.CharactersReservedPerURL,
	)
	if textLength < 0 ||
		textLength+
			len([]rune(payload.SpoilerText)) >
			capability.MaxCharacters ||
		len(payload.Media) > capability.MaxAttachments {
		return permanent(
			"mastodon_capability_mismatch",
			"Mastodon instance capabilities reject this post.",
		)
	}
	switch payload.Visibility {
	case "public", "unlisted", "private", "direct":
	default:
		return permanent(
			"mastodon_capability_mismatch",
			"Mastodon visibility is unsupported.",
		)
	}
	for _, item := range payload.Media {
		if len([]rune(item.Alt)) > capability.DescriptionLimit {
			return permanent("mastodon_capability_mismatch",
				"Mastodon media description limit is exceeded.")
		}
		if !containsString(capability.MIMETypes, item.ContentType) {
			return permanent(
				"mastodon_capability_mismatch",
				"Mastodon instance does not support this media type.",
			)
		}
		limit := capability.ImageBytes
		matrixLimit := capability.ImageMatrixLimit
		if strings.HasPrefix(item.ContentType, "video/") ||
			item.ContentType == "image/gif" {
			limit = capability.VideoBytes
			matrixLimit = capability.VideoMatrixLimit
			if item.FrameRate <= 0 ||
				item.FrameRate > capability.VideoFrameRateLimit {
				return permanent("mastodon_capability_mismatch",
					"Mastodon video frame rate cannot be validated.")
			}
		} else if !strings.HasPrefix(item.ContentType, "image/") {
			return permanent("mastodon_capability_mismatch",
				"Mastodon media format cannot be validated.")
		}
		if limit <= 0 || item.SizeBytes > limit || item.Width <= 0 ||
			item.Height <= 0 || int64(item.Width) > matrixLimit/int64(item.Height) {
			return permanent(
				"mastodon_capability_mismatch",
				"Mastodon instance media size limit is exceeded.",
			)
		}
	}
	return nil
}

func validMastodonCapabilityDocument(value mastodonCapabilities) bool {
	if value.MaxCharacters <= 0 || value.CharactersReservedPerURL <= 0 ||
		value.MaxAttachments < 0 || value.MaxAttachments > 16 ||
		value.DescriptionLimit <= 0 || value.ImageBytes <= 0 ||
		value.ImageMatrixLimit <= 0 || value.VideoBytes <= 0 ||
		value.VideoMatrixLimit <= 0 || value.VideoFrameRateLimit <= 0 ||
		len(value.MIMETypes) == 0 {
		return false
	}
	for _, contentType := range value.MIMETypes {
		if !strings.HasPrefix(contentType, "image/") &&
			!strings.HasPrefix(contentType, "video/") {
			return false
		}
	}
	return true
}

func (adapter *Mastodon) multipartMedia(
	ctx context.Context,
	item media,
) (*socialconnections.PublishingMedia, string, error) {
	source, err := adapter.media.Open(ctx, item.StorageKey)
	if err != nil {
		return nil, "", temporary(
			"mastodon_media_unavailable",
			"Mastodon media could not be opened.",
		)
	}
	defer source.Close()
	file, err := os.CreateTemp("", "postqron-mastodon-media-*")
	if err != nil {
		return nil, "", temporary(
			"mastodon_media_unavailable",
			"Mastodon media staging failed.",
		)
	}
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()
	writer := multipart.NewWriter(file)
	if item.Alt != "" {
		if err = writer.WriteField("description", item.Alt); err != nil {
			return nil, "", temporary("mastodon_media_unavailable", "Mastodon media staging failed.")
		}
	}
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(
		`form-data; name="file"; filename=%q`,
		"upload"+mediaExtension(item.ContentType),
	))
	partHeader.Set("Content-Type", item.ContentType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return nil, "", temporary("mastodon_media_unavailable", "Mastodon media staging failed.")
	}
	sourceHash := sha256.New()
	written, err := io.Copy(part, io.TeeReader(source, sourceHash))
	if err != nil {
		return nil, "", temporary(
			"mastodon_media_unavailable",
			"Mastodon media could not be read.",
		)
	}
	if written != item.SizeBytes ||
		hex.EncodeToString(sourceHash.Sum(nil)) != item.SHA256 {
		return nil, "", permanent(
			"mastodon_media_changed",
			"Mastodon media no longer matches its immutable snapshot.",
		)
	}
	if err = writer.Close(); err != nil {
		return nil, "", temporary("mastodon_media_unavailable", "Mastodon media staging failed.")
	}
	size, digest, err := rewindAndDigest(file)
	if err != nil {
		return nil, "", temporary("mastodon_media_unavailable", "Mastodon media staging failed.")
	}
	keep = true
	return &socialconnections.PublishingMedia{
		Body: &temporaryUpload{File: file, path: file.Name()},
		Size: size, SHA256: digest,
	}, writer.FormDataContentType(), nil
}

func mastodonProgress(
	state mastodonCheckpoint,
	delay time.Duration,
) (publishing.PublishResult, error) {
	encoded, err := encodeCheckpoint(state, "invalid_mastodon_checkpoint")
	return publishing.PublishResult{Checkpoint: encoded, RetryAfter: delay}, err
}

func mastodonNextStep(index, total int) string {
	if index < total {
		return "media_upload_pending"
	}
	return "status_pending"
}

func (adapter *Mastodon) stampStatusPending(state *mastodonCheckpoint) {
	if state.Step == "status_pending" && state.StatusPreparedAt.IsZero() {
		state.StatusPreparedAt = adapter.clock().UTC()
	}
}

func (adapter *Mastodon) validateReplayWindow(state mastodonCheckpoint) error {
	now := adapter.clock().UTC()
	if state.StatusPreparedAt.IsZero() || state.StatusPreparedAt.After(now) ||
		now.Sub(state.StatusPreparedAt) >= mastodonReplayWindow {
		return permanent(
			"mastodon_idempotency_window_expired",
			"Mastodon status creation cannot be replayed safely.",
		)
	}
	return nil
}

var mastodonURLPattern = regexp.MustCompile(`https?://[^\s<]+`)

func mastodonTextLength(text string, reserved int) int {
	total := len([]rune(text))
	for _, match := range mastodonURLPattern.FindAllString(text, -1) {
		candidate := strings.TrimRight(match, ".,!?;:)]}")
		parsed, err := url.ParseRequestURI(candidate)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return -1
		}
		total -= len([]rune(candidate))
		total += reserved
	}
	if strings.Contains(text, "http://") || strings.Contains(text, "https://") {
		if len(mastodonURLPattern.FindAllString(text, -1)) == 0 {
			return -1
		}
	}
	return total
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func validHTTPSPermalink(raw string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil
}

func mediaExtension(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	default:
		return filepath.Ext("upload")
	}
}

type temporaryUpload struct {
	*os.File
	path string
}

func (upload *temporaryUpload) Close() error {
	closeErr := upload.File.Close()
	removeErr := os.Remove(upload.path)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func rewindAndDigest(file *os.File) (int64, string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, "", err
	}
	var digest hash.Hash = sha256.New()
	size, err := io.Copy(digest, file)
	if err != nil {
		return 0, "", err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(digest.Sum(nil)), nil
}

var _ publishing.Publisher = (*Mastodon)(nil)
