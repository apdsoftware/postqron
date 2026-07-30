package socialconnections

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const defaultMaximumPublishingMediaBytes int64 = 256 << 20

// AuthenticatedExecutor is the internal F5 boundary used by publishing
// workers. Callers identify a connection, never a provider, origin, binding,
// access token, refresh token, DPoP nonce, or key.
type AuthenticatedExecutor struct {
	service        *Service
	client         *http.Client
	transport      pinnedRoundTripper
	resourceServer map[Provider]string
	classifiers    map[Provider]ProviderResponseClassifier
	maxMediaBytes  int64
}

// pinnedRoundTripper is an F5-only transport contract. PinOrigin must resolve,
// validate, and bind the exact origin addresses subsequently used by
// RoundTrip; a plain http.RoundTripper is intentionally insufficient.
type pinnedRoundTripper interface {
	http.RoundTripper
	PinOrigin(context.Context, string) error
}

// ProviderResponseEvidence is safe classification input for F8 provider
// adapters. Header is allowlisted and Body has already passed credential,
// OAuth-session, and DPoP-state screening.
type ProviderResponseEvidence struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Method     string
}

// ProviderResponseClassification contains scheduling semantics only. Provider
// error strings and diagnostics are deliberately absent.
type ProviderResponseClassification struct {
	Kind       ExecutorFailureKind
	RetryAfter time.Duration
	Reconnect  bool
}

// ProviderResponseClassifier lets provider-neutral F8 adapters classify
// sanitized 4xx/5xx evidence without receiving credentials or raw headers.
type ProviderResponseClassifier interface {
	ClassifyProviderResponse(
		ProviderResponseEvidence,
	) (ProviderResponseClassification, bool)
}

type AuthenticatedExecutorConfig struct {
	Service         *Service
	Transport       pinnedRoundTripper
	ResourceServers map[Provider]string
	Classifiers     map[Provider]ProviderResponseClassifier
	MaxMediaBytes   int64
}

type PublishingRequest struct {
	WorkspaceID  string
	ConnectionID string
	// ExpectedProvider is a required confused-deputy guard. It is compared with
	// the server-side connection and is never used for routing.
	ExpectedProvider Provider
	Method           string
	Path             string
	Header           http.Header
	Body             []byte
	Media            *PublishingMedia
}

// PublishingMedia is single-use. Execute always calls Close, including on
// validation, transport, size, and digest failures.
type PublishingMedia struct {
	Body   io.ReadCloser
	Size   int64
	SHA256 string
}

// streamingAuthenticatedRequest is passed only to trusted dynamic adapters.
// Its path remains relative and its origin, credential, DPoP state, and
// provider binding remain in the DynamicSession supplied by F5.
type streamingAuthenticatedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   io.Reader
	Size   int64
	SHA256 string
}

// dynamicStreamingAdapter is deliberately package-private: F8 and the worker
// can consume Execute but cannot receive a DynamicSession or credential.
type dynamicStreamingAdapter interface {
	doAuthenticatedStream(
		context.Context,
		DynamicSession,
		streamingAuthenticatedRequest,
	) (DynamicAuthenticatedResult, error)
}

type PublishingResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type ExecutorFailureKind string

const (
	ExecutorFailureRateLimit ExecutorFailureKind = "rate_limit"
	ExecutorFailureTemporary ExecutorFailureKind = "temporary"
	ExecutorFailurePermanent ExecutorFailureKind = "permanent"
	ExecutorFailureReconnect ExecutorFailureKind = "reconnect"
	ExecutorFailureAmbiguous ExecutorFailureKind = "ambiguous"
)

// ExecutorFailure contains only scheduling-safe metadata. Cause, response
// bodies, credentials, provider diagnostics, and request URLs are never
// returned to the publishing worker.
type ExecutorFailure struct {
	Kind       ExecutorFailureKind
	Code       string
	RetryAfter time.Duration
	Reconnect  bool
}

func (failure *ExecutorFailure) Error() string {
	if failure == nil || failure.Code == "" {
		return "authenticated publishing request failed"
	}
	return failure.Code
}

func NewAuthenticatedExecutor(
	config AuthenticatedExecutorConfig,
) (*AuthenticatedExecutor, error) {
	if config.Service == nil {
		return nil, fmt.Errorf("%w: social connection service is required", ErrInvalidArgument)
	}
	if config.MaxMediaBytes == 0 {
		config.MaxMediaBytes = defaultMaximumPublishingMediaBytes
	}
	if config.MaxMediaBytes < 0 {
		return nil, fmt.Errorf("%w: maximum media size is invalid", ErrInvalidArgument)
	}
	servers := make(map[Provider]string, len(config.ResourceServers))
	for provider, raw := range config.ResourceServers {
		if !slicesContainsProvider(provider) {
			return nil, ErrUnsupportedProvider
		}
		origin, err := canonicalPublicOrigin(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: resource server is invalid", ErrInvalidArgument)
		}
		servers[provider] = origin
	}
	if len(servers) != 0 && config.Transport == nil {
		return nil, fmt.Errorf(
			"%w: a DNS-pinning transport is required",
			ErrInvalidArgument,
		)
	}
	classifiers := make(
		map[Provider]ProviderResponseClassifier,
		len(config.Classifiers),
	)
	for provider, classifier := range config.Classifiers {
		if !slicesContainsProvider(provider) || classifier == nil {
			return nil, fmt.Errorf(
				"%w: provider response classifier is invalid",
				ErrInvalidArgument,
			)
		}
		classifiers[provider] = classifier
	}
	return &AuthenticatedExecutor{
		service: config.Service,
		client: &http.Client{
			Transport: config.Transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		resourceServer: servers,
		transport:      config.Transport,
		classifiers:    classifiers,
		maxMediaBytes:  config.MaxMediaBytes,
	}, nil
}

func (executor *AuthenticatedExecutor) Execute(
	ctx context.Context,
	request PublishingRequest,
) (PublishingResponse, error) {
	var err error
	request, err = snapshotPublishingRequest(request)
	if err != nil {
		return PublishingResponse{}, err
	}
	if request.Media != nil {
		defer request.Media.close()
	}
	if executor == nil || executor.service == nil ||
		strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.ConnectionID) == "" {
		return PublishingResponse{}, ErrInvalidArgument
	}
	authenticated, body, verifier, err := executor.prepareRequest(request)
	if err != nil {
		return PublishingResponse{}, err
	}

	stored, err := executor.service.repository.GetCredential(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
	)
	if err != nil {
		return PublishingResponse{}, err
	}
	if stored.Status == StatusReconnectRequired {
		return PublishingResponse{}, &ExecutorFailure{
			Kind:      ExecutorFailureReconnect,
			Code:      "reconnect_required",
			Reconnect: true,
		}
	}
	if stored.Status == StatusRevoked {
		return PublishingResponse{}, executorFailure(ExecutorFailurePermanent, "connection_revoked", 0)
	}
	if request.ExpectedProvider != stored.Provider {
		return PublishingResponse{}, ErrInvalidState
	}

	// Availability and the exact provider/binding are checked before any
	// transport can observe the request.
	if dynamic := executor.service.dynamicAdapters[stored.Provider]; dynamic != nil {
		if _, err = executor.service.availableDynamicAdapter(stored.Provider); err != nil {
			return PublishingResponse{}, redactExecutorError(err, request.Method, 0)
		}
		if request.Media != nil {
			streaming, ok := dynamic.(dynamicStreamingAdapter)
			if !ok {
				return PublishingResponse{}, executorFailure(
					ExecutorFailurePermanent,
					"provider_streaming_not_supported",
					0,
				)
			}
			response, streamErr := executor.executeDynamicStream(
				ctx,
				stored,
				streaming,
				request,
				body,
				verifier,
			)
			if streamErr != nil {
				return PublishingResponse{}, redactExecutorError(
					streamErr,
					request.Method,
					0,
				)
			}
			return executor.finishDynamicResponse(
				ctx,
				request.WorkspaceID,
				request.ConnectionID,
				request.Method,
				response,
			)
		}
		response, dynamicErr := executor.service.AuthenticatedRequest(
			ctx,
			request.WorkspaceID,
			request.ConnectionID,
			authenticated,
		)
		if dynamicErr != nil {
			return PublishingResponse{}, redactExecutorError(dynamicErr, request.Method, 0)
		}
		return executor.finishDynamicResponse(
			ctx,
			request.WorkspaceID,
			request.ConnectionID,
			request.Method,
			response,
		)
	}

	origin, configured := executor.resourceServer[stored.Provider]
	if !configured {
		return PublishingResponse{}, executorFailure(
			ExecutorFailurePermanent,
			"provider_resource_server_not_configured",
			0,
		)
	}
	if stored.Binding.ResourceServer != "" && stored.Binding.ResourceServer != origin {
		return PublishingResponse{}, ErrInvalidState
	}
	if executor.transport == nil {
		return PublishingResponse{}, executorFailure(
			ExecutorFailurePermanent,
			"provider_transport_not_configured",
			0,
		)
	}
	if err = executor.transport.PinOrigin(ctx, origin); err != nil {
		return PublishingResponse{}, executorFailure(
			ExecutorFailureTemporary,
			"provider_dns_pin_failed",
			0,
		)
	}
	if _, err = executor.service.availableAdapter(stored.Provider); err != nil {
		return PublishingResponse{}, redactExecutorError(err, request.Method, 0)
	}
	token, err := executor.service.AccessToken(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
	)
	if err != nil {
		return PublishingResponse{}, redactExecutorError(err, request.Method, 0)
	}
	target := origin + request.Path
	httpRequest, err := http.NewRequestWithContext(ctx, request.Method, target, body)
	if err != nil {
		return PublishingResponse{}, ErrInvalidArgument
	}
	httpRequest.Header = request.Header.Clone()
	if httpRequest.Header == nil {
		httpRequest.Header = make(http.Header)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	if request.Media != nil {
		httpRequest.ContentLength = request.Media.Size
	}
	response, transportErr := executor.client.Do(httpRequest)
	if finalizeMedia(verifier) != nil {
		closeHTTPResponse(response)
		return PublishingResponse{}, redactExecutorError(
			ErrProviderRequestFailed,
			request.Method,
			0,
		)
	}
	if transportErr != nil {
		return PublishingResponse{}, redactExecutorError(
			ErrProviderRequestFailed,
			request.Method,
			0,
		)
	}
	return executor.readHTTPResponse(
		ctx,
		response,
		stored,
		request.Method,
		token,
	)
}

func snapshotPublishingRequest(
	request PublishingRequest,
) (PublishingRequest, error) {
	if request.ExpectedProvider == "" {
		return PublishingRequest{}, fmt.Errorf(
			"%w: expected provider is required",
			ErrInvalidArgument,
		)
	}
	snapshot := PublishingRequest{
		WorkspaceID:      request.WorkspaceID,
		ConnectionID:     request.ConnectionID,
		ExpectedProvider: request.ExpectedProvider,
		Method:           request.Method,
		Path:             request.Path,
		Header:           request.Header.Clone(),
		Body:             append([]byte(nil), request.Body...),
	}
	if request.Media != nil {
		snapshot.Media = &PublishingMedia{
			Body:   request.Media.Body,
			Size:   request.Media.Size,
			SHA256: request.Media.SHA256,
		}
	}
	return snapshot, nil
}

func (executor *AuthenticatedExecutor) executeDynamicStream(
	ctx context.Context,
	beforeClaim StoredCredential,
	adapter dynamicStreamingAdapter,
	request PublishingRequest,
	body io.Reader,
	verifier *verifiedMediaReader,
) (AuthenticatedResponse, error) {
	dynamic := executor.service.dynamicAdapters[beforeClaim.Provider]
	config := dynamic.DynamicConfig()
	now := executor.service.now().UTC()
	stored, needsRefresh, err := executor.service.repository.ClaimSession(
		ctx,
		request.WorkspaceID,
		request.ConnectionID,
		now,
		now.Add(executor.service.refreshBefore),
		executor.service.refreshLockTTL,
	)
	if errors.Is(err, ErrRefreshOutcomeUnknown) {
		return AuthenticatedResponse{}, executor.service.markReconnect(
			ctx, stored, "refresh_outcome_unknown", now,
		)
	}
	if err != nil {
		return AuthenticatedResponse{}, err
	}
	release := func() {
		_ = executor.service.repository.ReleaseSession(
			ctx, request.WorkspaceID, request.ConnectionID, stored.SessionLeaseID,
		)
	}
	if stored.Provider != beforeClaim.Provider ||
		stored.Binding != beforeClaim.Binding {
		release()
		return AuthenticatedResponse{}, ErrInvalidState
	}
	session, err := executor.service.openDynamicSession(stored)
	if err != nil {
		release()
		return AuthenticatedResponse{}, err
	}
	if needsRefresh {
		previousRefresh := session.Credential.RefreshToken
		refreshed, refreshErr := dynamic.RefreshDynamic(ctx, session)
		if refreshErr != nil {
			if stored.RefreshTokenMode == RefreshTokenSingleUse {
				return AuthenticatedResponse{}, executor.service.markReconnect(
					ctx, stored, "refresh_outcome_unknown", now,
				)
			}
			release()
			return AuthenticatedResponse{}, refreshErr
		}
		if validateDynamicSession(config, stored.Binding, refreshed.Session) != nil ||
			(stored.RefreshTokenMode == RefreshTokenSingleUse &&
				refreshed.Session.Credential.RefreshToken == previousRefresh) ||
			refreshed.Session.Credential.ExpiresAt == nil ||
			!refreshed.Session.Credential.ExpiresAt.After(
				now.Add(executor.service.refreshBefore),
			) {
			return AuthenticatedResponse{}, executor.service.markReconnect(
				ctx, stored, "invalid_refresh_rotation", now,
			)
		}
		event, eventErr := executor.service.newEvent(
			EventTokenRefreshed,
			stored.WorkspaceID,
			stored.ID,
			stored.Provider,
			stored.RemoteID,
			"",
			"",
		)
		if eventErr != nil {
			return AuthenticatedResponse{}, executor.service.markReconnect(
				ctx, stored, "refresh_outcome_unknown", now,
			)
		}
		command, commandErr := executor.service.dynamicSessionCommand(
			stored, refreshed.Session, true, now, &event,
		)
		if commandErr != nil {
			return AuthenticatedResponse{}, executor.service.markReconnect(
				ctx, stored, "refresh_outcome_unknown", now,
			)
		}
		if _, commandErr = executor.service.repository.CompleteSession(
			ctx,
			command,
		); commandErr != nil {
			_ = executor.service.markReconnect(
				ctx,
				stored,
				"refresh_outcome_unknown",
				now,
			)
			return AuthenticatedResponse{}, ErrReconnectRequired
		}
		stored, needsRefresh, err = executor.service.repository.ClaimSession(
			ctx,
			request.WorkspaceID,
			request.ConnectionID,
			now,
			now.Add(executor.service.refreshBefore),
			executor.service.refreshLockTTL,
		)
		if err != nil {
			return AuthenticatedResponse{}, err
		}
		if needsRefresh {
			release()
			return AuthenticatedResponse{}, ErrReconnectRequired
		}
		session, err = executor.service.openDynamicSession(stored)
		if err != nil {
			release()
			return AuthenticatedResponse{}, err
		}
	}
	result, requestErr := adapter.doAuthenticatedStream(
		ctx,
		session,
		streamingAuthenticatedRequest{
			Method: request.Method,
			Path:   request.Path,
			Header: request.Header.Clone(),
			Body:   body,
			Size:   request.Media.Size,
			SHA256: request.Media.SHA256,
		},
	)
	if finalizeMedia(verifier) != nil {
		requestErr = ErrProviderRequestFailed
	}
	if result.Session.Binding == (OAuthBinding{}) {
		result.Session.Binding = session.Binding
	}
	if len(result.Session.ProviderState) == 0 {
		result.Session.ProviderState = session.ProviderState
	}
	if result.Session.Credential.AccessToken == "" {
		result.Session.Credential = session.Credential
	}
	if err = validateDynamicSession(config, stored.Binding, result.Session); err != nil {
		return AuthenticatedResponse{}, executor.failClosedDynamicOutcome(
			ctx, stored, request.Method, now, "dynamic_session_invalid",
		)
	}
	responsePresent := authenticatedResponsePresent(result.Response)
	if requestErr == nil && !responsePresent {
		return AuthenticatedResponse{}, executor.failClosedDynamicOutcome(
			ctx, stored, request.Method, now, "provider_response_missing",
		)
	}
	if responsePresent {
		if err = validateAuthenticatedResponse(config, result.Response); err != nil {
			return AuthenticatedResponse{}, executor.failClosedDynamicOutcome(
				ctx, stored, request.Method, now, "provider_response_invalid",
			)
		}
	}
	command, err := executor.service.dynamicSessionCommand(
		stored,
		result.Session,
		false,
		now,
		nil,
	)
	if err != nil {
		return AuthenticatedResponse{}, executor.failClosedDynamicOutcome(
			ctx, stored, request.Method, now, "dynamic_session_persistence_failed",
		)
	}
	if _, err = executor.service.repository.CompleteSession(ctx, command); err != nil {
		return AuthenticatedResponse{}, executor.failClosedDynamicOutcome(
			ctx, stored, request.Method, now, "dynamic_session_persistence_failed",
		)
	}
	if requestErr != nil {
		var providerFailure *ProviderFailure
		if errors.As(requestErr, &providerFailure) &&
			isReconnectFailure(providerFailure.Kind) {
			return AuthenticatedResponse{}, executor.service.markReconnect(
				ctx, stored, string(providerFailure.Kind), now,
			)
		}
		return AuthenticatedResponse{}, ErrProviderRequestFailed
	}
	return sanitizeAuthenticatedResponse(result.Response), nil
}

func (executor *AuthenticatedExecutor) failClosedDynamicOutcome(
	ctx context.Context,
	stored StoredCredential,
	method string,
	now time.Time,
	reason string,
) error {
	if method == http.MethodGet || method == http.MethodHead {
		_ = executor.service.repository.ReleaseSession(
			ctx,
			stored.WorkspaceID,
			stored.ID,
			stored.SessionLeaseID,
		)
		return ErrProviderRequestFailed
	}
	_ = executor.service.markReconnect(ctx, stored, reason, now)
	return &ExecutorFailure{
		Kind:      ExecutorFailureAmbiguous,
		Code:      "provider_outcome_ambiguous",
		Reconnect: true,
	}
}

func (executor *AuthenticatedExecutor) prepareRequest(
	request PublishingRequest,
) (AuthenticatedRequest, io.Reader, *verifiedMediaReader, error) {
	if request.Media != nil && len(request.Body) != 0 {
		return AuthenticatedRequest{}, nil, nil, fmt.Errorf(
			"%w: body and media are mutually exclusive",
			ErrInvalidArgument,
		)
	}
	authenticated := AuthenticatedRequest{
		Method: request.Method,
		Path:   request.Path,
		Header: request.Header.Clone(),
		Body:   append([]byte(nil), request.Body...),
	}
	if request.Path != "/" {
		target, parseErr := url.ParseRequestURI(request.Path)
		if parseErr != nil || path.Clean(target.Path) != target.Path {
			return AuthenticatedRequest{}, nil, nil, fmt.Errorf(
				"%w: authenticated request path is not canonical",
				ErrInvalidArgument,
			)
		}
	}
	for key := range request.Header {
		if !validPublishingHeaderName(key) ||
			forbiddenPublishingHeader(key) {
			return AuthenticatedRequest{}, nil, nil, fmt.Errorf(
				"%w: authenticated request header is forbidden",
				ErrInvalidArgument,
			)
		}
	}
	safePolicy := DynamicOAuthConfig{NetworkPolicy: DynamicNetworkPolicy{
		RejectRedirects:   true,
		ValidateAndPinDNS: true,
		MaxResponseBytes:  maximumAuthenticatedBody,
	}}
	if err := validateAuthenticatedRequest(safePolicy, authenticated); err != nil {
		return AuthenticatedRequest{}, nil, nil, err
	}
	if request.Media == nil {
		return authenticated, bytes.NewReader(request.Body), nil, nil
	}
	if request.Media.Body == nil ||
		request.Media.Size < 0 ||
		request.Media.Size > executor.maxMediaBytes {
		return AuthenticatedRequest{}, nil, nil, fmt.Errorf("%w: media size is invalid", ErrInvalidArgument)
	}
	digest, err := hex.DecodeString(request.Media.SHA256)
	if err != nil || len(digest) != sha256.Size ||
		request.Media.SHA256 != strings.ToLower(request.Media.SHA256) {
		return AuthenticatedRequest{}, nil, nil, fmt.Errorf("%w: media digest is invalid", ErrInvalidArgument)
	}
	verifier := newVerifiedMediaReader(request.Media, digest)
	return authenticated, verifier, verifier, nil
}

func validPublishingHeaderName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character > 127 ||
			!strings.ContainsRune(
				"!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
				character,
			) {
			return false
		}
	}
	return true
}

func forbiddenPublishingHeader(value string) bool {
	switch strings.ToLower(value) {
	case "authorization",
		"connection",
		"content-length",
		"cookie",
		"dpop",
		"dpop-nonce",
		"forwarded",
		"host",
		"keep-alive",
		"proxy-authorization",
		"proxy-connection",
		"set-cookie",
		"te",
		"trailer",
		"transfer-encoding",
		"upgrade",
		"x-api-key",
		"x-auth-token",
		"x-forwarded-for",
		"x-forwarded-host",
		"x-forwarded-proto",
		"x-real-ip":
		return true
	default:
		return false
	}
}

func (executor *AuthenticatedExecutor) readHTTPResponse(
	ctx context.Context,
	response *http.Response,
	stored StoredCredential,
	method string,
	secrets ...string,
) (PublishingResponse, error) {
	if response == nil || response.Body == nil {
		return PublishingResponse{}, postSendFailure(
			method,
			"provider_response_missing",
		)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maximumAuthenticatedBody+1)
	body, err := io.ReadAll(limited)
	if err != nil || len(body) > maximumAuthenticatedBody {
		return PublishingResponse{}, postSendFailure(
			method,
			"provider_response_invalid",
		)
	}
	authenticated := AuthenticatedResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       body,
	}
	if err = validateAuthenticatedResponse(
		DynamicOAuthConfig{NetworkPolicy: DynamicNetworkPolicy{
			MaxResponseBytes: maximumAuthenticatedBody,
		}},
		authenticated,
	); err != nil {
		return PublishingResponse{}, postSendFailure(
			method,
			"provider_response_invalid",
		)
	}
	return executor.finishResponse(ctx, stored, method, authenticated, secrets...)
}

func postSendFailure(method, code string) *ExecutorFailure {
	if method == http.MethodGet || method == http.MethodHead {
		return executorFailure(ExecutorFailureTemporary, code, 0)
	}
	return &ExecutorFailure{
		Kind: ExecutorFailureAmbiguous,
		Code: "provider_outcome_ambiguous",
	}
}

func (executor *AuthenticatedExecutor) finishResponse(
	ctx context.Context,
	stored StoredCredential,
	method string,
	response AuthenticatedResponse,
	secrets ...string,
) (PublishingResponse, error) {
	response = sanitizePublishingResponse(response)
	if responseContainsSecret(response, secrets...) {
		return PublishingResponse{}, executorFailure(
			ExecutorFailurePermanent,
			"provider_response_redacted",
			0,
		)
	}
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"), executor.service.now())
	if response.StatusCode >= 400 {
		if classifier := executor.classifiers[stored.Provider]; classifier != nil {
			classification, classified := classifier.ClassifyProviderResponse(
				ProviderResponseEvidence{
					StatusCode: response.StatusCode,
					Header:     response.Header.Clone(),
					Body:       append([]byte(nil), response.Body...),
					Method:     method,
				},
			)
			if classified {
				return PublishingResponse{}, executor.persistResponseFailure(
					ctx,
					stored,
					classifiedExecutorFailure(classification),
				)
			}
		}
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return PublishingResponse{}, executorFailure(
			ExecutorFailureRateLimit,
			"provider_rate_limited",
			retryAfter,
		)
	}
	if response.StatusCode == http.StatusUnauthorized ||
		response.StatusCode == http.StatusForbidden {
		return PublishingResponse{}, executor.persistResponseFailure(
			ctx,
			stored,
			&ExecutorFailure{
				Kind:      ExecutorFailureReconnect,
				Code:      "provider_reconnect_required",
				Reconnect: true,
			},
		)
	}
	if response.StatusCode >= 500 {
		if method != http.MethodGet && method != http.MethodHead {
			return PublishingResponse{}, executorFailure(
				ExecutorFailureAmbiguous,
				"provider_outcome_ambiguous",
				retryAfter,
			)
		}
		return PublishingResponse{}, executorFailure(
			ExecutorFailureTemporary,
			"provider_temporary_failure",
			retryAfter,
		)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return PublishingResponse{}, executorFailure(
			ExecutorFailurePermanent,
			"provider_redirect_rejected",
			0,
		)
	}
	if response.StatusCode >= 400 {
		return PublishingResponse{}, executorFailure(
			ExecutorFailurePermanent,
			"provider_request_rejected",
			0,
		)
	}
	return PublishingResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Body:       append([]byte(nil), response.Body...),
	}, nil
}

func (executor *AuthenticatedExecutor) finishDynamicResponse(
	ctx context.Context,
	workspaceID, connectionID string,
	method string,
	response AuthenticatedResponse,
) (PublishingResponse, error) {
	stored, err := executor.service.repository.GetCredential(
		ctx,
		workspaceID,
		connectionID,
	)
	if err != nil {
		return PublishingResponse{}, redactExecutorError(err, http.MethodPost, 0)
	}
	credential, err := executor.service.openCredential(stored)
	if err != nil {
		return PublishingResponse{}, redactExecutorError(err, http.MethodPost, 0)
	}
	session, err := executor.service.openDynamicSession(stored)
	if err != nil {
		return PublishingResponse{}, redactExecutorError(err, method, 0)
	}
	secrets := []string{
		credential.AccessToken,
		credential.RefreshToken,
	}
	secrets = append(secrets, dynamicSecretCandidates(session.ProviderState)...)
	return executor.finishResponse(
		ctx,
		stored,
		method,
		response,
		secrets...,
	)
}

func (executor *AuthenticatedExecutor) persistResponseFailure(
	ctx context.Context,
	stored StoredCredential,
	failure *ExecutorFailure,
) error {
	if failure == nil {
		return executorFailure(
			ExecutorFailurePermanent,
			"provider_request_failed",
			0,
		)
	}
	if failure.Kind != ExecutorFailureReconnect && !failure.Reconnect {
		return failure
	}
	failure.Reconnect = true
	if stored.ID == "" {
		return failure
	}
	_ = executor.service.markReconnect(
		ctx,
		stored,
		string(failure.Kind),
		executor.service.now().UTC(),
	)
	return failure
}

func responseContainsSecret(
	response AuthenticatedResponse,
	secrets ...string,
) bool {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if bytes.Contains(response.Body, []byte(secret)) {
			return true
		}
		for _, values := range response.Header {
			for _, value := range values {
				if strings.Contains(value, secret) {
					return true
				}
			}
		}
	}
	return false
}

func closeHTTPResponse(response *http.Response) {
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func executorFailure(
	kind ExecutorFailureKind,
	code string,
	retryAfter time.Duration,
) *ExecutorFailure {
	return &ExecutorFailure{Kind: kind, Code: code, RetryAfter: retryAfter}
}

func classifiedExecutorFailure(
	classification ProviderResponseClassification,
) *ExecutorFailure {
	code := "provider_request_failed"
	switch classification.Kind {
	case ExecutorFailureRateLimit:
		code = "provider_rate_limited"
	case ExecutorFailureTemporary:
		code = "provider_temporary_failure"
	case ExecutorFailurePermanent:
		code = "provider_request_rejected"
	case ExecutorFailureReconnect:
		code = "provider_reconnect_required"
		classification.Reconnect = true
	case ExecutorFailureAmbiguous:
		code = "provider_outcome_ambiguous"
	default:
		classification.Kind = ExecutorFailurePermanent
	}
	return &ExecutorFailure{
		Kind:       classification.Kind,
		Code:       code,
		RetryAfter: classification.RetryAfter,
		Reconnect:  classification.Reconnect,
	}
}

func redactExecutorError(err error, method string, retryAfter time.Duration) error {
	if err == nil {
		return nil
	}
	var failure *ExecutorFailure
	if errors.As(err, &failure) {
		return &ExecutorFailure{
			Kind:       failure.Kind,
			Code:       failure.Code,
			RetryAfter: failure.RetryAfter,
			Reconnect:  failure.Reconnect,
		}
	}
	if errors.Is(err, ErrReconnectRequired) ||
		errors.Is(err, ErrRefreshOutcomeUnknown) {
		return executorFailure(ExecutorFailureReconnect, "reconnect_required", 0)
	}
	if errors.Is(err, ErrRefreshInProgress) ||
		errors.Is(err, ErrAuthenticatedRequestInProgress) ||
		errors.Is(err, ErrProviderUnavailable) {
		return executorFailure(ExecutorFailureTemporary, "provider_temporarily_unavailable", retryAfter)
	}
	var provider *ProviderFailure
	if errors.As(err, &provider) {
		switch provider.Kind {
		case FailureAuthentication, FailurePermissionMissing, FailureResourceGone:
			return executorFailure(ExecutorFailureReconnect, "provider_reconnect_required", 0)
		case FailureTemporary:
			return executorFailure(ExecutorFailureTemporary, "provider_temporary_failure", retryAfter)
		default:
			return executorFailure(ExecutorFailurePermanent, "provider_request_failed", 0)
		}
	}
	if errors.Is(err, ErrProviderRequestFailed) {
		if method == http.MethodGet || method == http.MethodHead {
			return executorFailure(ExecutorFailureTemporary, "provider_transport_failed", retryAfter)
		}
		return executorFailure(ExecutorFailureAmbiguous, "provider_outcome_ambiguous", retryAfter)
	}
	if errors.Is(err, ErrProviderReviewRequired) ||
		errors.Is(err, ErrProviderAuditRequired) ||
		errors.Is(err, ErrProviderNotConfigured) {
		return executorFailure(ExecutorFailurePermanent, "provider_not_available", 0)
	}
	return executorFailure(ExecutorFailurePermanent, "authenticated_request_failed", 0)
}

func sanitizePublishingResponse(
	response AuthenticatedResponse,
) AuthenticatedResponse {
	safe := AuthenticatedResponse{
		StatusCode: response.StatusCode,
		Header:     make(http.Header),
		Body:       append([]byte(nil), response.Body...),
	}
	for _, header := range []string{
		"Content-Type",
		"ETag",
		"Last-Modified",
		"RateLimit-Limit",
		"RateLimit-Policy",
		"RateLimit-Remaining",
		"RateLimit-Reset",
		"Retry-After",
		"X-RateLimit-Limit",
		"X-RateLimit-Remaining",
		"X-RateLimit-Reset",
		"X-RateLimit-Reset-After",
	} {
		for _, value := range response.Header.Values(header) {
			if !containsUnsafeText(value) {
				safe.Header.Add(header, value)
			}
		}
	}
	return safe
}

func dynamicSecretCandidates(state []byte) []string {
	if len(state) == 0 {
		return nil
	}
	candidates := []string{string(state)}
	for _, candidate := range strings.FieldsFunc(string(state), func(character rune) bool {
		return !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' ||
			character == '.' || character == '~')
	}) {
		if len(candidate) >= 8 {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}

type verifiedMediaReader struct {
	source   *PublishingMedia
	hash     hashWriter
	expected []byte
	read     int64
	done     bool
	finalErr error
}

type hashWriter interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}

func newVerifiedMediaReader(
	media *PublishingMedia,
	expected []byte,
) *verifiedMediaReader {
	return &verifiedMediaReader{
		source:   media,
		hash:     sha256.New(),
		expected: append([]byte(nil), expected...),
	}
}

func finalizeMedia(reader *verifiedMediaReader) error {
	if reader == nil {
		return nil
	}
	wasPartial := reader.read < reader.source.Size
	if !reader.done {
		if _, err := io.Copy(io.Discard, reader); err != nil {
			return err
		}
	}
	if wasPartial {
		return ErrProviderRequestFailed
	}
	return reader.finalErr
}

func (reader *verifiedMediaReader) Read(buffer []byte) (int, error) {
	if reader.done {
		if reader.finalErr != nil {
			return 0, reader.finalErr
		}
		return 0, io.EOF
	}
	remaining := reader.source.Size - reader.read
	if remaining < 0 {
		reader.finalErr = fmt.Errorf("%w: media exceeds declared size", ErrInvalidArgument)
		return 0, reader.finalErr
	}
	maximum := int64(len(buffer))
	if maximum > remaining+1 {
		maximum = remaining + 1
	}
	n, err := reader.source.Body.Read(buffer[:maximum])
	if n > 0 {
		reader.read += int64(n)
		_, _ = reader.hash.Write(buffer[:n])
	}
	if reader.read > reader.source.Size {
		reader.done = true
		reader.finalErr = fmt.Errorf("%w: media exceeds declared size", ErrInvalidArgument)
		return n, reader.finalErr
	}
	if err == io.EOF {
		reader.done = true
		if reader.read != reader.source.Size {
			reader.finalErr = fmt.Errorf("%w: media size mismatch", ErrInvalidArgument)
			return n, reader.finalErr
		}
		if !equalBytes(reader.hash.Sum(nil), reader.expected) {
			reader.finalErr = fmt.Errorf("%w: media digest mismatch", ErrInvalidArgument)
			return n, reader.finalErr
		}
	}
	return n, err
}

func (media *PublishingMedia) close() {
	if media != nil && media.Body != nil {
		_ = media.Body.Close()
	}
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
