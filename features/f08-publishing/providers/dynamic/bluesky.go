package dynamic

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const (
	blueskyUploadBlobPath = "/xrpc/com.atproto.repo.uploadBlob"
	blueskyCreatePath     = "/xrpc/com.atproto.repo.createRecord"
	blueskyGetPath        = "/xrpc/com.atproto.repo.getRecord"
	blueskyCollection     = "app.bsky.feed.post"
	blueskyMaxTextRunes   = 300
	blueskyMaxImages      = 4
	blueskyMaxImageBytes  = 2_000_000
)

type Bluesky struct {
	executor authenticatedExecutor
	media    MediaSource
}

func NewBluesky(
	executor *socialconnections.AuthenticatedExecutor,
	mediaSource MediaSource,
) (*Bluesky, error) {
	if executor == nil {
		return nil, publishing.ErrInvalidArgument
	}
	return &Bluesky{executor: executor, media: mediaSource}, nil
}

func newBlueskyForTest(
	executor authenticatedExecutor,
	mediaSource MediaSource,
) *Bluesky {
	return &Bluesky{executor: executor, media: mediaSource}
}

func (*Bluesky) Capabilities() publishing.AdapterCapabilities {
	// AT Protocol has no idempotency header for createRecord. A deterministic
	// TID plus exact getRecord reconciliation provides safe replay instead.
	return capabilities(false)
}

type blueskyPayload struct {
	Repository string   `json:"repository"`
	Text       string   `json:"text"`
	CreatedAt  string   `json:"created_at"`
	Languages  []string `json:"languages,omitempty"`
	Media      []media  `json:"media,omitempty"`
}

type blueskyCapabilities struct {
	MaxTextRunes  int      `json:"max_text_runes"`
	MaxImages     int      `json:"max_images"`
	MaxImageBytes int64    `json:"max_image_bytes"`
	MIMETypes     []string `json:"mime_types"`
}

type blueskyCheckpoint struct {
	Step         string              `json:"step"`
	Capabilities blueskyCapabilities `json:"capabilities,omitempty"`
	MediaIndex   int                 `json:"media_index,omitempty"`
	Blobs        []json.RawMessage   `json:"blobs,omitempty"`
	RKey         string              `json:"rkey,omitempty"`
}

type blueskyBlobEnvelope struct {
	Blob json.RawMessage `json:"blob"`
}

type blueskyBlob struct {
	Type string `json:"$type"`
	Ref  struct {
		Link string `json:"$link"`
	} `json:"ref"`
	MIMEType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

type blueskyRecordEnvelope struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

func (adapter *Bluesky) Publish(
	ctx context.Context,
	request publishing.PublishRequest,
) (publishing.PublishResult, error) {
	payload, state, err := adapter.input(request)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	switch state.Step {
	case "":
		if !publishingKeyPattern.MatchString(request.IdempotencyKey) {
			return publishing.PublishResult{}, permanent(
				"invalid_bluesky_idempotency",
				"Bluesky idempotency key is invalid.",
			)
		}
		state.Capabilities = officialBlueskyCapabilities()
		if validationErr := validateBlueskyPayload(
			payload,
			state.Capabilities,
		); validationErr != nil {
			return publishing.PublishResult{}, validationErr
		}
		state.RKey, err = blueskyRKey(
			request.IdempotencyKey,
			payload.CreatedAt,
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		if len(payload.Media) == 0 {
			state.Step = "record_pending"
		} else {
			state.Step = "media_upload_pending"
		}
		return blueskyProgress(state, 0)
	case "media_upload_pending":
		if state.MediaIndex < 0 || state.MediaIndex >= len(payload.Media) ||
			adapter.media == nil {
			return publishing.PublishResult{}, permanent(
				"bluesky_media_unavailable",
				"Bluesky media source or checkpoint is unavailable.",
			)
		}
		item := payload.Media[state.MediaIndex]
		source, openErr := adapter.media.Open(ctx, item.StorageKey)
		if openErr != nil {
			return publishing.PublishResult{}, permanent(
				"bluesky_media_unavailable",
				"Bluesky media could not be opened.",
			)
		}
		response, callErr := execute(
			ctx, adapter.executor, socialconnections.ProviderBluesky, request,
			http.MethodPost, blueskyUploadBlobPath,
			http.Header{"Content-Type": {item.ContentType}},
			nil,
			&socialconnections.PublishingMedia{
				Body: source, Size: item.SizeBytes, SHA256: item.SHA256,
			},
		)
		if callErr != nil {
			return publishing.PublishResult{}, callErr
		}
		blob, parseErr := decodeBlueskyBlob(response.Body, item)
		if parseErr != nil {
			return publishing.PublishResult{}, parseErr
		}
		state.Blobs = append(state.Blobs, blob)
		state.MediaIndex++
		if state.MediaIndex == len(payload.Media) {
			state.Step = "record_pending"
		}
		return blueskyProgress(
			state,
			responseRetryAfter(response, 0),
		)
	case "record_pending":
		return adapter.createRecord(ctx, request, payload, state)
	default:
		return publishing.PublishResult{}, permanent(
			"invalid_bluesky_checkpoint",
			"Bluesky checkpoint step is invalid.",
		)
	}
}

func (adapter *Bluesky) Reconcile(
	ctx context.Context,
	request publishing.ReconcileRequest,
) (publishing.ReconcileResult, error) {
	payload, state, err := adapter.input(publishing.PublishRequest{
		Payload: request.Payload, Checkpoint: request.Checkpoint,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	if state.Step == "media_upload_pending" {
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "Bluesky blob upload outcome cannot be reconciled safely.",
		}, nil
	}
	if state.Step != "record_pending" {
		return publishing.ReconcileResult{
			State: publishing.ReconciliationNotFound,
		}, nil
	}
	values := url.Values{
		"repo":       {payload.Repository},
		"collection": {blueskyCollection},
		"rkey":       {state.RKey},
	}
	response, callErr := execute(
		ctx, adapter.executor, socialconnections.ProviderBluesky,
		publishing.PublishRequest{
			WorkspaceID:  request.WorkspaceID,
			ConnectionID: request.ConnectionID,
		},
		http.MethodGet, canonicalQueryPath(blueskyGetPath, values),
		nil, nil, nil,
	)
	if callErr != nil {
		var providerErr *publishing.ProviderError
		if !errors.As(callErr, &providerErr) ||
			providerErr.Code != "provider_request_rejected" ||
			providerErr.Retryable || providerErr.Ambiguous {
			return publishing.ReconcileResult{}, callErr
		}
		// F5 deliberately redacts RecordNotFound like other permanent provider
		// diagnostics. Reissuing createRecord with the same repository TID is
		// still duplicate-safe: an occupied key cannot create a second record.
		result, createErr := adapter.createRecord(
			ctx,
			publishing.PublishRequest{
				WorkspaceID:    request.WorkspaceID,
				ConnectionID:   request.ConnectionID,
				Payload:        request.Payload,
				Checkpoint:     request.Checkpoint,
				IdempotencyKey: request.IdempotencyKey,
			},
			payload,
			state,
		)
		if createErr != nil {
			return publishing.ReconcileResult{}, createErr
		}
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationFound,
			RemoteID:   result.RemoteID,
			Permalink:  result.Permalink,
			Checkpoint: result.Checkpoint,
		}, nil
	}
	var envelope blueskyRecordEnvelope
	if json.Unmarshal(response.Body, &envelope) != nil {
		return publishing.ReconcileResult{}, permanent(
			"invalid_bluesky_reconciliation",
			"Bluesky returned invalid reconciliation evidence.",
		)
	}
	expectedURI := blueskyURI(payload.Repository, state.RKey)
	if envelope.URI != expectedURI || !atCIDPattern.MatchString(envelope.CID) {
		return publishing.ReconcileResult{}, permanent(
			"invalid_bluesky_reconciliation",
			"Bluesky reconciliation did not match the deterministic record.",
		)
	}
	state.Step = "complete"
	finalCheckpoint, checkpointErr := encodeCheckpoint(
		state,
		"invalid_bluesky_checkpoint",
	)
	if checkpointErr != nil {
		return publishing.ReconcileResult{}, checkpointErr
	}
	return publishing.ReconcileResult{
		State:      publishing.ReconciliationFound,
		RemoteID:   envelope.URI,
		Permalink:  blueskyPermalink(payload.Repository, state.RKey),
		Checkpoint: finalCheckpoint,
	}, nil
}

func (adapter *Bluesky) createRecord(
	ctx context.Context,
	request publishing.PublishRequest,
	payload blueskyPayload,
	state blueskyCheckpoint,
) (publishing.PublishResult, error) {
	record := map[string]any{
		"$type":     blueskyCollection,
		"text":      payload.Text,
		"createdAt": payload.CreatedAt,
	}
	if len(payload.Languages) != 0 {
		record["langs"] = payload.Languages
	}
	if len(payload.Media) != 0 {
		images := make([]map[string]any, 0, len(payload.Media))
		for index, item := range payload.Media {
			var blob any
			if json.Unmarshal(state.Blobs[index], &blob) != nil {
				return publishing.PublishResult{}, permanent(
					"invalid_bluesky_checkpoint",
					"Bluesky blob checkpoint is invalid.",
				)
			}
			image := map[string]any{"alt": item.Alt, "image": blob}
			if item.Width > 0 && item.Height > 0 {
				image["aspectRatio"] = map[string]int{
					"width": item.Width, "height": item.Height,
				}
			}
			images = append(images, image)
		}
		record["embed"] = map[string]any{
			"$type":  "app.bsky.embed.images",
			"images": images,
		}
	}
	body, err := jsonBody(map[string]any{
		"repo":       payload.Repository,
		"collection": blueskyCollection,
		"rkey":       state.RKey,
		"validate":   true,
		"record":     record,
	}, "invalid_bluesky_payload")
	if err != nil {
		return publishing.PublishResult{}, err
	}
	response, callErr := execute(
		ctx, adapter.executor, socialconnections.ProviderBluesky, request,
		http.MethodPost, blueskyCreatePath,
		http.Header{"Content-Type": {"application/json"}},
		body, nil,
	)
	if callErr != nil {
		return publishing.PublishResult{}, callErr
	}
	var envelope blueskyRecordEnvelope
	expectedURI := blueskyURI(payload.Repository, state.RKey)
	if json.Unmarshal(response.Body, &envelope) != nil ||
		envelope.URI != expectedURI ||
		!atCIDPattern.MatchString(envelope.CID) {
		return publishing.PublishResult{}, permanent(
			"invalid_bluesky_record",
			"Bluesky did not return the deterministic record.",
		)
	}
	state.Step = "complete"
	finalCheckpoint, err := encodeCheckpoint(
		state,
		"invalid_bluesky_checkpoint",
	)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	return publishing.PublishResult{
		Complete: true, RemoteID: envelope.URI,
		Permalink:  blueskyPermalink(payload.Repository, state.RKey),
		Checkpoint: finalCheckpoint,
	}, nil
}

func (adapter *Bluesky) input(
	request publishing.PublishRequest,
) (blueskyPayload, blueskyCheckpoint, error) {
	var payload blueskyPayload
	var state blueskyCheckpoint
	if err := decodePayload(
		request.Payload, &payload, "invalid_bluesky_payload",
	); err != nil {
		return payload, state, err
	}
	if err := decodeCheckpoint(
		request.Checkpoint, &state, "invalid_bluesky_checkpoint",
	); err != nil {
		return payload, state, err
	}
	if !atDIDPattern.MatchString(payload.Repository) ||
		!utf8.ValidString(payload.Text) ||
		(strings.TrimSpace(payload.Text) == "" && len(payload.Media) == 0) ||
		len(payload.Media) > blueskyMaxImages ||
		state.MediaIndex < 0 || state.MediaIndex > len(payload.Media) ||
		len(state.Blobs) != state.MediaIndex {
		return payload, state, permanent(
			"invalid_bluesky_payload",
			"Bluesky post metadata is invalid.",
		)
	}
	createdAt, timeErr := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if timeErr != nil || createdAt.Format(time.RFC3339Nano) != payload.CreatedAt {
		return payload, state, permanent(
			"invalid_bluesky_payload",
			"Bluesky created_at must be a canonical RFC3339 timestamp.",
		)
	}
	if state.Step != "" {
		expectedRKey, keyErr := blueskyRKey(
			request.IdempotencyKey,
			payload.CreatedAt,
		)
		if keyErr != nil {
			return payload, state, keyErr
		}
		if state.RKey != expectedRKey ||
			!atTIDPattern.MatchString(state.RKey) ||
			!sameBlueskyCapabilities(
				state.Capabilities,
				officialBlueskyCapabilities(),
			) {
			return payload, state, permanent(
				"invalid_bluesky_checkpoint",
				"Bluesky capability or idempotency checkpoint is invalid.",
			)
		}
		if err := validateBlueskyPayload(
			payload,
			state.Capabilities,
		); err != nil {
			return payload, state, err
		}
		switch state.Step {
		case "media_upload_pending":
			if state.MediaIndex >= len(payload.Media) {
				return payload, state, permanent(
					"invalid_bluesky_checkpoint",
					"Bluesky upload checkpoint is invalid.",
				)
			}
		case "record_pending":
			if state.MediaIndex != len(payload.Media) {
				return payload, state, permanent(
					"invalid_bluesky_checkpoint",
					"Bluesky record checkpoint is incomplete.",
				)
			}
		default:
			return payload, state, permanent(
				"invalid_bluesky_checkpoint",
				"Bluesky checkpoint step is invalid.",
			)
		}
	}
	for _, item := range payload.Media {
		if !validMedia(item) || len([]rune(item.Alt)) > 2000 {
			return payload, state, permanent(
				"invalid_bluesky_media",
				"Bluesky image metadata is invalid.",
			)
		}
	}
	return payload, state, nil
}

func officialBlueskyCapabilities() blueskyCapabilities {
	return blueskyCapabilities{
		MaxTextRunes:  blueskyMaxTextRunes,
		MaxImages:     blueskyMaxImages,
		MaxImageBytes: blueskyMaxImageBytes,
		MIMETypes:     []string{"image/*"},
	}
}

func sameBlueskyCapabilities(
	left, right blueskyCapabilities,
) bool {
	return left.MaxTextRunes == right.MaxTextRunes &&
		left.MaxImages == right.MaxImages &&
		left.MaxImageBytes == right.MaxImageBytes &&
		slices.Equal(left.MIMETypes, right.MIMETypes)
}

func validateBlueskyPayload(
	payload blueskyPayload,
	capability blueskyCapabilities,
) error {
	if len([]rune(payload.Text)) > capability.MaxTextRunes ||
		len(payload.Media) > capability.MaxImages ||
		len(payload.Languages) > 3 {
		return permanent(
			"bluesky_capability_mismatch",
			"Bluesky capabilities reject this post.",
		)
	}
	for _, language := range payload.Languages {
		if len(language) < 2 || len(language) > 35 ||
			strings.ContainsAny(language, " \t\r\n") {
			return permanent(
				"bluesky_capability_mismatch",
				"Bluesky language metadata is invalid.",
			)
		}
	}
	for _, item := range payload.Media {
		if !validBlueskyImageType(item.ContentType) ||
			item.SizeBytes > capability.MaxImageBytes {
			return permanent(
				"bluesky_capability_mismatch",
				"Bluesky image capabilities reject this media.",
			)
		}
	}
	return nil
}

func validBlueskyImageType(value string) bool {
	if !strings.HasPrefix(value, "image/") || len(value) > 127 {
		return false
	}
	subtype := strings.TrimPrefix(value, "image/")
	if subtype == "" {
		return false
	}
	for _, character := range subtype {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			!strings.ContainsRune(".+-", character) {
			return false
		}
	}
	return true
}

func decodeBlueskyBlob(
	body []byte,
	expected media,
) (json.RawMessage, error) {
	var envelope blueskyBlobEnvelope
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Blob) == 0 {
		return nil, permanent(
			"invalid_bluesky_blob",
			"Bluesky did not return a valid blob.",
		)
	}
	var blob blueskyBlob
	if json.Unmarshal(envelope.Blob, &blob) != nil ||
		blob.Type != "blob" || !atCIDPattern.MatchString(blob.Ref.Link) ||
		blob.MIMEType != expected.ContentType || blob.Size != expected.SizeBytes {
		return nil, permanent(
			"invalid_bluesky_blob",
			"Bluesky blob does not match the immutable media snapshot.",
		)
	}
	return append(json.RawMessage(nil), envelope.Blob...), nil
}

func blueskyProgress(
	state blueskyCheckpoint,
	delay time.Duration,
) (publishing.PublishResult, error) {
	encoded, err := encodeCheckpoint(state, "invalid_bluesky_checkpoint")
	return publishing.PublishResult{Checkpoint: encoded, RetryAfter: delay}, err
}

func blueskyRKey(
	idempotencyKey, createdAt string,
) (string, error) {
	if !publishingKeyPattern.MatchString(idempotencyKey) {
		return "", permanent(
			"invalid_bluesky_idempotency",
			"Bluesky idempotency key is invalid.",
		)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return "", permanent(
			"invalid_bluesky_payload",
			"Bluesky created_at is invalid.",
		)
	}
	const microsecondsPerDay = int64(24 * time.Hour / time.Microsecond)
	dayStart := timestamp.UTC().Truncate(24 * time.Hour).UnixMicro()
	digest := sha256.Sum256([]byte(idempotencyKey))
	// Preserve the post's UTC day while spreading deterministic keys over the
	// TID timestamp and clock fields. This avoids relying on process-local
	// clocks and makes the same F8 key converge after any crash or race.
	offset := int64(binary.BigEndian.Uint64(digest[:8]) %
		uint64(microsecondsPerDay))
	microseconds := dayStart + offset
	if microseconds < 0 || uint64(microseconds) >= uint64(1)<<53 {
		return "", permanent(
			"invalid_bluesky_payload",
			"Bluesky created_at cannot be represented as an AT Protocol TID.",
		)
	}
	clockID := uint64(digest[8])<<2 | uint64(digest[9]>>6)
	value := uint64(microseconds)<<10 | clockID
	const alphabet = "234567abcdefghijklmnopqrstuvwxyz"
	encoded := [13]byte{}
	for index := len(encoded) - 1; index >= 0; index-- {
		encoded[index] = alphabet[value&31]
		value >>= 5
	}
	result := string(encoded[:])
	if !atTIDPattern.MatchString(result) {
		return "", permanent(
			"invalid_bluesky_idempotency",
			"Bluesky deterministic TID is invalid.",
		)
	}
	return result, nil
}

func blueskyURI(repository, rkey string) string {
	return "at://" + repository + "/" + blueskyCollection + "/" + rkey
}

func blueskyPermalink(repository, rkey string) string {
	return "https://bsky.app/profile/" + repository + "/post/" + rkey
}

var _ publishing.Publisher = (*Bluesky)(nil)
