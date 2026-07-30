package dynamic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const capabilityVersion = "2026-07-30"

var (
	mastodonIDPattern    = regexp.MustCompile(`^[0-9]{1,64}$`)
	atDIDPattern         = regexp.MustCompile(`^did:[a-z0-9]+:[A-Za-z0-9._:%-]{1,512}$`)
	atTIDPattern         = regexp.MustCompile(`^[234567abcdefghij][234567abcdefghijklmnopqrstuvwxyz]{12}$`)
	atCIDPattern         = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	publishingKeyPattern = regexp.MustCompile(`^publish_[a-f0-9]{64}$`)
)

type authenticatedExecutor interface {
	Execute(
		context.Context,
		socialconnections.PublishingRequest,
	) (socialconnections.PublishingResponse, error)
}

// MediaSource opens immutable media from its F6 snapshot storage key. The
// adapter verifies size and SHA-256 again at the F5 streaming boundary.
type MediaSource interface {
	Open(context.Context, string) (io.ReadCloser, error)
}

type media struct {
	StorageKey  string `json:"storage_key"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Alt         string `json:"alt,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

func capabilities(nativeIdempotency bool) publishing.AdapterCapabilities {
	return publishing.AdapterCapabilities{
		Version:           capabilityVersion,
		Mode:              publishing.PublishingModeAuto,
		NativeIdempotency: nativeIdempotency,
		Reconciliation:    true,
		MultiStep:         true,
		RemotePermalink:   true,
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
			Code:   "authenticated_provider_failure",
			Detail: "The authenticated provider request failed.",
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
	default:
		result.Code = "authenticated_provider_failure"
	}
	return result
}

func decodePayload[T any](raw json.RawMessage, target *T, code string) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return permanent(code, "Dynamic provider publishing metadata is invalid.")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return permanent(code, "Dynamic provider publishing metadata is invalid.")
	}
	return nil
}

func decodeCheckpoint[T any](raw json.RawMessage, target *T, code string) error {
	if len(raw) == 0 {
		return nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return permanent(code, "Dynamic provider publishing checkpoint is invalid.")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return permanent(code, "Dynamic provider publishing checkpoint is invalid.")
	}
	return nil
}

func encodeCheckpoint(value any, code string) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, permanent(code, "Dynamic provider checkpoint could not be encoded.")
	}
	return encoded, nil
}

func jsonBody(value any, code string) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, permanent(code, "Dynamic provider request could not be encoded.")
	}
	return encoded, nil
}

func permanent(code, detail string) error {
	return &publishing.ProviderError{Code: code, Detail: detail}
}

func validMedia(value media) bool {
	digest, err := hex.DecodeString(value.SHA256)
	return err == nil && len(digest) == sha256.Size &&
		value.SHA256 == strings.ToLower(value.SHA256) &&
		strings.TrimSpace(value.StorageKey) != "" &&
		value.SizeBytes > 0 && value.Width >= 0 && value.Height >= 0 &&
		((value.Width == 0 && value.Height == 0) ||
			(value.Width > 0 && value.Height > 0))
}

func responseRetryAfter(
	response socialconnections.PublishingResponse,
	fallback time.Duration,
) time.Duration {
	raw := strings.TrimSpace(response.Header.Get("Retry-After"))
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		delay := time.Until(parsed)
		if delay > 0 {
			return delay
		}
	}
	return fallback
}

func canonicalQueryPath(endpoint string, values url.Values) string {
	if len(values) == 0 {
		return endpoint
	}
	return endpoint + "?" + values.Encode()
}
