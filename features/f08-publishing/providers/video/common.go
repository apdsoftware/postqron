package video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const capabilityVersion = "2026-07-30-review-1"

var (
	remoteIDPattern       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	tikTokPostIDPattern   = regexp.MustCompile(`^[0-9]{1,32}$`)
	tikTokUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

type authenticatedExecutor interface {
	Execute(
		context.Context,
		socialconnections.PublishingRequest,
	) (socialconnections.PublishingResponse, error)
}

// MediaSource opens immutable media by its F6 storage key. Implementations
// must return the exact bytes described by the destination snapshot.
type MediaSource interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

type media struct {
	StorageKey      string  `json:"storage_key"`
	SourceURL       string  `json:"source_url,omitempty"`
	ContentType     string  `json:"content_type"`
	SizeBytes       int64   `json:"size_bytes"`
	SHA256          string  `json:"sha256"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	DurationSeconds float64 `json:"duration_seconds"`
}

func capabilities(reconciliation bool) publishing.AdapterCapabilities {
	return publishing.AdapterCapabilities{
		Version:             capabilityVersion,
		Mode:                publishing.PublishingModeAuto,
		Reconciliation:      reconciliation,
		AmbiguousFailClosed: true,
		MultiStep:           true,
		RemotePermalink:     true,
		NativeIdempotency:   false,
	}
}

func execute(
	ctx context.Context,
	executor authenticatedExecutor,
	provider socialconnections.Provider,
	request publishing.PublishRequest,
	method, path string,
	header http.Header,
	body []byte,
	upload *socialconnections.PublishingMedia,
) (socialconnections.PublishingResponse, error) {
	response, err := executor.Execute(ctx, socialconnections.PublishingRequest{
		WorkspaceID:      request.WorkspaceID,
		ConnectionID:     request.ConnectionID,
		ExpectedProvider: provider,
		Method:           method,
		Path:             path,
		Header:           header,
		Body:             body,
		Media:            upload,
	})
	if err != nil {
		return socialconnections.PublishingResponse{}, providerError(err)
	}
	return response, nil
}

func providerError(err error) error {
	var failure *socialconnections.ExecutorFailure
	if !errors.As(err, &failure) {
		return &publishing.ProviderError{
			Code:      "authenticated_provider_failure",
			Detail:    "The authenticated provider request failed.",
			Retryable: false,
		}
	}
	result := &publishing.ProviderError{
		Code:       failure.Code,
		Detail:     "The provider rejected or could not complete the request.",
		RetryAfter: failure.RetryAfter,
	}
	switch failure.Kind {
	case socialconnections.ExecutorFailureRateLimit,
		socialconnections.ExecutorFailureTemporary:
		result.Retryable = true
	case socialconnections.ExecutorFailureAmbiguous:
		result.Retryable = true
		result.Ambiguous = true
	case socialconnections.ExecutorFailureReconnect,
		socialconnections.ExecutorFailurePermanent:
		result.Retryable = false
	default:
		result.Code = "authenticated_provider_failure"
	}
	return result
}

func decodePayload[T any](raw json.RawMessage, target *T) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return permanent("invalid_video_payload", "Video publishing metadata is invalid.")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return permanent("invalid_video_payload", "Video publishing metadata is invalid.")
	}
	return nil
}

func decodeCheckpoint[T any](raw json.RawMessage, target *T) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return permanent("invalid_video_checkpoint", "Video publishing checkpoint is invalid.")
	}
	return nil
}

func checkpoint(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, permanent("invalid_video_checkpoint", "Video checkpoint could not be encoded.")
	}
	return encoded, nil
}

func jsonBody(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, permanent("invalid_video_payload", "Video metadata could not be encoded.")
	}
	return encoded, nil
}

func permanent(code, detail string) error {
	return &publishing.ProviderError{Code: code, Detail: detail}
}

func ambiguous(code, detail string) error {
	return &publishing.ProviderError{
		Code: code, Detail: detail, Retryable: true, Ambiguous: true,
	}
}

func retryable(code, detail string) error {
	return &publishing.ProviderError{
		Code: code, Detail: detail, Retryable: true,
	}
}

func mediaSourceError(err error) error {
	var providerError *publishing.ProviderError
	if errors.As(err, &providerError) {
		return err
	}
	return &publishing.ProviderError{
		Code:      "video_media_unavailable",
		Detail:    "The immutable video media is temporarily unavailable.",
		Retryable: true,
	}
}

func validHTTPSMediaURL(raw string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validMedia(value media) bool {
	switch value.ContentType {
	case "video/mp4", "video/quicktime", "video/webm":
	default:
		return false
	}
	digest, err := hex.DecodeString(value.SHA256)
	return err == nil && len(digest) == sha256.Size &&
		value.SHA256 == strings.ToLower(value.SHA256) &&
		strings.TrimSpace(value.StorageKey) != "" &&
		value.SizeBytes > 0 &&
		value.Width > 0 &&
		value.Height > 0 &&
		value.DurationSeconds > 0
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
