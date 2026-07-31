package socialconnections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maximumDiscoveryInputBytes  = 2048
	maximumProviderStateBytes   = 256 * 1024
	maximumAuthenticatedBody    = 1024 * 1024
	maximumAuthenticatedHeaders = 32 * 1024
)

type dynamicAttemptEnvelope struct {
	ProviderState []byte `json:"provider_state"`
	PARRequestURI string `json:"par_request_uri,omitempty"`
}

func (service *Service) beginDynamic(
	ctx context.Context,
	request BeginRequest,
	adapter DynamicAdapter,
) (Authorization, error) {
	config := adapter.DynamicConfig()
	binding, err := canonicalOAuthBinding(request.previousBinding)
	if err != nil {
		return Authorization{}, err
	}
	if binding == (OAuthBinding{}) {
		if err = validateDiscoveryInput(request.Provider, request.Discovery); err != nil {
			return Authorization{}, err
		}
	} else if request.Discovery != (DiscoveryInput{}) {
		return Authorization{}, fmt.Errorf(
			"%w: reconnect discovery is derived from the stored binding",
			ErrInvalidArgument,
		)
	}
	attemptID, err := randomOpaqueID(18)
	if err != nil {
		return Authorization{}, fmt.Errorf("generate social OAuth attempt ID: %w", err)
	}
	state, err := randomOpaqueID(32)
	if err != nil {
		return Authorization{}, fmt.Errorf("generate social OAuth state: %w", err)
	}
	now := service.now().UTC()
	expiresAt := now.Add(service.authorizationTTL)
	authorization, err := adapter.BeginDynamic(ctx, DynamicBeginRequest{
		Discovery:       request.Discovery,
		PreviousBinding: binding,
		State:           state,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		return Authorization{}, err
	}
	authorization.Binding, err = canonicalOAuthBinding(authorization.Binding)
	if err != nil {
		return Authorization{}, err
	}
	if binding != (OAuthBinding{}) {
		if err = requireExactBinding(binding, authorization.Binding, false); err != nil {
			return Authorization{}, err
		}
	}
	if err = validateDynamicAuthorization(config, authorization); err != nil {
		return Authorization{}, err
	}
	envelope, err := json.Marshal(dynamicAttemptEnvelope{
		ProviderState: append([]byte(nil), authorization.ProviderState...),
		PARRequestURI: authorization.PARRequestURI,
	})
	if err != nil {
		return Authorization{}, fmt.Errorf("encode dynamic OAuth attempt: %w", err)
	}
	ciphertext, err := service.cipher.Seal(
		envelope,
		dynamicAttemptAdditionalData(
			attemptID,
			request.Provider,
			authorization.Binding,
		),
	)
	if err != nil {
		return Authorization{}, fmt.Errorf("seal dynamic OAuth attempt: %w", err)
	}
	attempt := OAuthAttempt{
		ID:                   attemptID,
		StateHash:            digest(state),
		WorkspaceID:          request.WorkspaceID,
		ActorID:              request.ActorID,
		Provider:             request.Provider,
		OAuthStateCiphertext: ciphertext,
		Binding:              authorization.Binding,
		CreatedAt:            now,
		ExpiresAt:            expiresAt,
	}
	if err = service.repository.CreateAttempt(ctx, attempt); err != nil {
		return Authorization{}, fmt.Errorf("save dynamic OAuth attempt: %w", err)
	}
	return Authorization{
		URL:       authorization.URL,
		ExpiresAt: expiresAt,
	}, nil
}

func (service *Service) callbackDynamic(
	ctx context.Context,
	request CallbackRequest,
	attempt OAuthAttempt,
	adapter DynamicAdapter,
	now time.Time,
) (Selection, error) {
	config := adapter.DynamicConfig()
	callbackIssuer := ""
	var err error
	if strings.TrimSpace(request.Issuer) != "" {
		callbackIssuer, err = canonicalOrigin(request.Issuer)
		if err != nil {
			return Selection{}, ErrInvalidState
		}
	}
	if config.RequiresIssuer &&
		(callbackIssuer == "" || callbackIssuer != attempt.Binding.Issuer) {
		return Selection{}, ErrInvalidState
	}
	plaintext, err := service.cipher.Open(
		attempt.OAuthStateCiphertext,
		dynamicAttemptAdditionalData(attempt.ID, attempt.Provider, attempt.Binding),
	)
	if err != nil {
		return Selection{}, fmt.Errorf("open dynamic OAuth attempt: %w", err)
	}
	var envelope dynamicAttemptEnvelope
	if err = json.Unmarshal(plaintext, &envelope); err != nil ||
		len(envelope.ProviderState) == 0 ||
		len(envelope.ProviderState) > maximumProviderStateBytes {
		return Selection{}, ErrInvalidState
	}
	if config.RequiresPAR && strings.TrimSpace(envelope.PARRequestURI) == "" {
		return Selection{}, ErrInvalidState
	}
	completion, err := adapter.CompleteDynamic(ctx, DynamicCallbackRequest{
		Code:          request.Code,
		Issuer:        callbackIssuer,
		RedirectURL:   config.RedirectURL,
		ProviderState: append([]byte(nil), envelope.ProviderState...),
		Binding:       attempt.Binding,
	})
	if err != nil {
		return Selection{}, err
	}
	completion.Binding, err = canonicalOAuthBinding(completion.Binding)
	if err != nil {
		return Selection{}, err
	}
	if err = requireExactBinding(
		attempt.Binding,
		completion.Binding,
		config.RequiresSubject,
	); err != nil {
		return Selection{}, err
	}
	if config.RequiresDPoP &&
		(len(completion.ProviderState) == 0 ||
			len(completion.ProviderState) > maximumProviderStateBytes) {
		return Selection{}, fmt.Errorf(
			"%w: DPoP session state is required",
			ErrInvalidArgument,
		)
	}
	valid := make([]DiscoveredResource, 0, len(completion.Resources))
	for _, resource := range completion.Resources {
		if validateDiscoveredResource(
			attempt.Provider,
			config.Scopes,
			resource,
		) == nil {
			valid = append(valid, resource)
		}
	}
	if len(valid) == 0 {
		return Selection{}, ErrNoResources
	}
	selectionID, err := randomOpaqueID(18)
	if err != nil {
		return Selection{}, fmt.Errorf("generate social selection ID: %w", err)
	}
	stored := StoredSelection{
		ID:          selectionID,
		WorkspaceID: attempt.WorkspaceID,
		ActorID:     attempt.ActorID,
		Provider:    attempt.Provider,
		CreatedAt:   now,
		ExpiresAt:   now.Add(service.selectionTTL),
	}
	safe := Selection{
		ID:        selectionID,
		Provider:  attempt.Provider,
		ExpiresAt: stored.ExpiresAt,
	}
	for _, resource := range valid {
		resource.Candidate.Scopes = append(
			[]string(nil),
			resource.Credential.Scopes...,
		)
		credentialAAD := credentialAdditionalData(
			attempt.WorkspaceID,
			attempt.Provider,
			resource.Candidate.RemoteID,
		)
		accessCiphertext, sealErr := service.cipher.Seal(
			[]byte(resource.Credential.AccessToken),
			credentialAAD,
		)
		if sealErr != nil {
			return Selection{}, fmt.Errorf("seal social access token: %w", sealErr)
		}
		refreshCiphertext, sealErr := service.cipher.Seal(
			[]byte(resource.Credential.RefreshToken),
			credentialAAD,
		)
		if sealErr != nil {
			return Selection{}, fmt.Errorf("seal social refresh token: %w", sealErr)
		}
		sessionCiphertext, sealErr := service.cipher.Seal(
			completion.ProviderState,
			dynamicSessionAdditionalData(
				attempt.WorkspaceID,
				attempt.Provider,
				resource.Candidate.RemoteID,
				completion.Binding,
			),
		)
		if sealErr != nil {
			return Selection{}, fmt.Errorf("seal dynamic OAuth session: %w", sealErr)
		}
		candidate := cloneCandidate(resource.Candidate)
		stored.Resources = append(stored.Resources, StoredResource{
			Candidate:              candidate,
			AccessTokenCiphertext:  accessCiphertext,
			RefreshTokenCiphertext: refreshCiphertext,
			OAuthSessionCiphertext: sessionCiphertext,
			Binding:                completion.Binding,
			RefreshTokenMode:       config.RefreshTokenMode,
			TokenExpiresAt:         cloneTimePointer(resource.Credential.ExpiresAt),
		})
		safe.Resources = append(safe.Resources, candidate)
	}
	if err = service.repository.SaveSelection(ctx, stored); err != nil {
		return Selection{}, fmt.Errorf("save dynamic social selection: %w", err)
	}
	return safe, nil
}

// AuthenticatedRequest is the only F5 boundary that dynamic DPoP providers
// expose to publishing callers. Tokens, nonces, and private keys remain inside
// the encrypted connection session.
func (service *Service) AuthenticatedRequest(
	ctx context.Context,
	workspaceID, connectionID string,
	request AuthenticatedRequest,
) (AuthenticatedResponse, error) {
	if strings.TrimSpace(workspaceID) == "" ||
		strings.TrimSpace(connectionID) == "" {
		return AuthenticatedResponse{}, ErrInvalidArgument
	}
	beforeClaim, err := service.repository.GetCredential(
		ctx,
		workspaceID,
		connectionID,
	)
	if err != nil {
		return AuthenticatedResponse{}, err
	}
	adapter, err := service.availableDynamicAdapter(beforeClaim.Provider)
	if err != nil {
		return AuthenticatedResponse{}, err
	}
	config := adapter.DynamicConfig()
	if err = validateAuthenticatedRequest(config, request); err != nil {
		return AuthenticatedResponse{}, err
	}
	now := service.now().UTC()
	stored, needsRefresh, err := service.repository.ClaimSession(
		ctx,
		workspaceID,
		connectionID,
		now,
		now.Add(service.refreshBefore),
		service.refreshLockTTL,
	)
	if errors.Is(err, ErrRefreshOutcomeUnknown) {
		return AuthenticatedResponse{}, service.markReconnect(
			ctx,
			stored,
			"refresh_outcome_unknown",
			now,
		)
	}
	if err != nil {
		return AuthenticatedResponse{}, err
	}
	if stored.Provider != beforeClaim.Provider {
		_ = service.repository.ReleaseSession(
			ctx, workspaceID, connectionID, stored.SessionLeaseID,
		)
		return AuthenticatedResponse{}, ErrInvalidState
	}
	session, err := service.openDynamicSession(stored)
	if err != nil {
		_ = service.repository.ReleaseSession(
			ctx, workspaceID, connectionID, stored.SessionLeaseID,
		)
		return AuthenticatedResponse{}, err
	}
	if needsRefresh {
		previousRefresh := session.Credential.RefreshToken
		refreshed, refreshErr := adapter.RefreshDynamic(ctx, session)
		if refreshErr != nil {
			if stored.RefreshTokenMode == RefreshTokenSingleUse {
				return AuthenticatedResponse{}, service.markReconnect(
					ctx,
					stored,
					"refresh_outcome_unknown",
					now,
				)
			}
			_ = service.repository.ReleaseSession(
				ctx, workspaceID, connectionID, stored.SessionLeaseID,
			)
			return AuthenticatedResponse{}, refreshErr
		}
		if err = validateDynamicSession(config, stored.Binding, refreshed.Session); err != nil ||
			(stored.RefreshTokenMode == RefreshTokenSingleUse &&
				refreshed.Session.Credential.RefreshToken == previousRefresh) ||
			refreshed.Session.Credential.ExpiresAt == nil ||
			!refreshed.Session.Credential.ExpiresAt.After(
				now.Add(service.refreshBefore),
			) {
			return AuthenticatedResponse{}, service.markReconnect(
				ctx,
				stored,
				"invalid_refresh_rotation",
				now,
			)
		}
		event, eventErr := service.newEvent(
			EventTokenRefreshed,
			stored.WorkspaceID,
			stored.ID,
			stored.Provider,
			stored.RemoteID,
			"",
			"",
		)
		if eventErr != nil {
			return AuthenticatedResponse{}, service.markReconnect(
				ctx,
				stored,
				"refresh_outcome_unknown",
				now,
			)
		}
		refreshCommand, commandErr := service.dynamicSessionCommand(
			stored,
			refreshed.Session,
			true,
			now,
			&event,
		)
		if commandErr != nil {
			return AuthenticatedResponse{}, service.markReconnect(
				ctx,
				stored,
				"refresh_outcome_unknown",
				now,
			)
		}
		if _, commandErr = service.repository.CompleteSession(
			ctx,
			refreshCommand,
		); commandErr != nil {
			// The provider may already have consumed the single-use token.
			// Keeping session_refreshing and its lease intact makes every
			// post-expiry claim fail closed as outcome_unknown.
			if stored.RefreshTokenMode != RefreshTokenSingleUse {
				_ = service.repository.ReleaseSession(
					ctx, workspaceID, connectionID, stored.SessionLeaseID,
				)
			}
			return AuthenticatedResponse{}, commandErr
		}
		// Refresh credentials, the rotated AS nonce, and the new single-use
		// token are durable before any resource request starts. A crash here
		// cannot cause reuse of the old refresh token.
		stored, needsRefresh, err = service.repository.ClaimSession(
			ctx,
			workspaceID,
			connectionID,
			now,
			now.Add(service.refreshBefore),
			service.refreshLockTTL,
		)
		if err != nil {
			return AuthenticatedResponse{}, err
		}
		if needsRefresh {
			_ = service.repository.ReleaseSession(
				ctx, workspaceID, connectionID, stored.SessionLeaseID,
			)
			return AuthenticatedResponse{}, ErrReconnectRequired
		}
		session, err = service.openDynamicSession(stored)
		if err != nil {
			_ = service.repository.ReleaseSession(
				ctx, workspaceID, connectionID, stored.SessionLeaseID,
			)
			return AuthenticatedResponse{}, err
		}
	}
	result, requestErr := adapter.DoAuthenticated(ctx, session, request)
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
		_ = service.repository.ReleaseSession(
			ctx, workspaceID, connectionID, stored.SessionLeaseID,
		)
		return AuthenticatedResponse{}, err
	}
	responsePresent := authenticatedResponsePresent(result.Response)
	if requestErr == nil && !responsePresent {
		err = fmt.Errorf("%w: provider response is missing", ErrInvalidArgument)
	} else if responsePresent {
		err = validateAuthenticatedResponse(config, result.Response)
	} else {
		err = nil
	}
	if err != nil {
		_ = service.repository.ReleaseSession(
			ctx, workspaceID, connectionID, stored.SessionLeaseID,
		)
		return AuthenticatedResponse{}, err
	}
	command, err := service.dynamicSessionCommand(
		stored,
		result.Session,
		false,
		now,
		nil,
	)
	if err != nil {
		_ = service.repository.ReleaseSession(
			ctx, workspaceID, connectionID, stored.SessionLeaseID,
		)
		return AuthenticatedResponse{}, err
	}
	if _, err = service.repository.CompleteSession(ctx, command); err != nil {
		_ = service.repository.ReleaseSession(
			ctx, workspaceID, connectionID, stored.SessionLeaseID,
		)
		return AuthenticatedResponse{}, err
	}
	if requestErr != nil {
		var failure *ProviderFailure
		if errors.As(requestErr, &failure) && isReconnectFailure(failure.Kind) {
			return AuthenticatedResponse{}, service.markReconnect(
				ctx,
				stored,
				string(failure.Kind),
				now,
			)
		}
		return AuthenticatedResponse{}, redactDynamicRequestError(requestErr)
	}
	return sanitizeAuthenticatedResponse(result.Response), nil
}

func authenticatedResponsePresent(response AuthenticatedResponse) bool {
	return response.StatusCode != 0 ||
		len(response.Header) != 0 ||
		len(response.Body) != 0
}

func redactDynamicRequestError(err error) error {
	var failure *ProviderFailure
	if errors.As(err, &failure) {
		return &ProviderFailure{
			Kind:      failure.Kind,
			Code:      "authenticated_provider_request_failed",
			Retryable: failure.Retryable,
		}
	}
	return ErrProviderRequestFailed
}

func (service *Service) dynamicSessionCommand(
	stored StoredCredential,
	session DynamicSession,
	updateCredential bool,
	now time.Time,
	event *Event,
) (SessionCommand, error) {
	sessionCiphertext, err := service.cipher.Seal(
		session.ProviderState,
		dynamicSessionAdditionalData(
			stored.WorkspaceID,
			stored.Provider,
			stored.RemoteID,
			stored.Binding,
		),
	)
	if err != nil {
		return SessionCommand{}, fmt.Errorf("seal dynamic OAuth session: %w", err)
	}
	command := SessionCommand{
		ConnectionID:           stored.ID,
		SessionLeaseID:         stored.SessionLeaseID,
		OAuthSessionCiphertext: sessionCiphertext,
		UpdateCredential:       updateCredential,
		VerifiedAt:             now,
		Now:                    now,
		Event:                  event,
	}
	if !updateCredential {
		return command, nil
	}
	credentialAAD := credentialAdditionalData(
		stored.WorkspaceID,
		stored.Provider,
		stored.RemoteID,
	)
	command.AccessTokenCiphertext, err = service.cipher.Seal(
		[]byte(session.Credential.AccessToken),
		credentialAAD,
	)
	if err != nil {
		return SessionCommand{}, err
	}
	command.RefreshTokenCiphertext, err = service.cipher.Seal(
		[]byte(session.Credential.RefreshToken),
		credentialAAD,
	)
	if err != nil {
		return SessionCommand{}, err
	}
	command.Scopes = append([]string(nil), session.Credential.Scopes...)
	command.ExpiresAt = cloneTimePointer(session.Credential.ExpiresAt)
	return command, nil
}

func (service *Service) openDynamicSession(
	stored StoredCredential,
) (DynamicSession, error) {
	credential, err := service.openCredential(stored)
	if err != nil {
		return DynamicSession{}, err
	}
	state, err := service.cipher.Open(
		stored.OAuthSessionCiphertext,
		dynamicSessionAdditionalData(
			stored.WorkspaceID,
			stored.Provider,
			stored.RemoteID,
			stored.Binding,
		),
	)
	if err != nil {
		return DynamicSession{}, fmt.Errorf("open dynamic OAuth session: %w", err)
	}
	if len(state) == 0 || len(state) > maximumProviderStateBytes {
		return DynamicSession{}, ErrReconnectRequired
	}
	return DynamicSession{
		Binding:       stored.Binding,
		Credential:    credential,
		ProviderState: state,
	}, nil
}

func (service *Service) availableDynamicAdapter(
	provider Provider,
) (DynamicAdapter, error) {
	if !slicesContainsProvider(provider) {
		return nil, ErrUnsupportedProvider
	}
	availability := service.availability[provider]
	adapter := service.dynamicAdapters[provider]
	if adapter != nil &&
		availability.Status == ProviderAvailable &&
		availability.ConfigurationState == ProviderReady {
		return adapter, nil
	}
	return nil, availabilityError(availability)
}

func providerAvailabilityError(staticErr, dynamicErr error) error {
	if !errors.Is(dynamicErr, ErrProviderNotConfigured) {
		return dynamicErr
	}
	return staticErr
}

func availabilityError(availability ProviderAvailability) error {
	switch availability.ConfigurationState {
	case ProviderReviewRequired:
		return ErrProviderReviewRequired
	case ProviderAuditRequired:
		return ErrProviderAuditRequired
	case ProviderNotConfigured, "":
		return ErrProviderNotConfigured
	default:
		return ErrProviderUnavailable
	}
}

func slicesContainsProvider(provider Provider) bool {
	for _, supported := range SupportedProviders {
		if supported == provider {
			return true
		}
	}
	return false
}

func dynamicAdapterCapabilities(config DynamicOAuthConfig) AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		PKCE:              true,
		ResourceSelection: true,
		TokenRefresh:      true,
		RemoteRevocation:  config.RevocationPolicy == RevocationRemoteRequired,
		DynamicDiscovery:  true,
		PAR:               config.RequiresPAR,
		DPoP:              config.RequiresDPoP,
		AccessTokenHash:   config.RequiresATH,
		AuthenticatedHTTP: true,
	}
}

func validateDynamicOAuthConfig(
	provider Provider,
	config DynamicOAuthConfig,
) error {
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" ||
		redirect.User != nil || redirect.Fragment != "" {
		return fmt.Errorf("%w: %s dynamic redirect URL is invalid", ErrInvalidArgument, provider)
	}
	if err = validateRequestedScopes(provider, config.Scopes); err != nil {
		return err
	}
	switch config.RefreshTokenMode {
	case RefreshTokenReusable, RefreshTokenSingleUse:
	default:
		return fmt.Errorf("%w: invalid refresh token mode", ErrInvalidArgument)
	}
	switch config.RevocationPolicy {
	case RevocationBestEffort, RevocationRemoteRequired:
	default:
		return fmt.Errorf("%w: invalid revocation policy", ErrInvalidArgument)
	}
	if !config.NetworkPolicy.RejectRedirects ||
		!config.NetworkPolicy.ValidateAndPinDNS ||
		config.NetworkPolicy.MaxResponseBytes <= 0 ||
		config.NetworkPolicy.MaxResponseBytes > maximumAuthenticatedBody {
		return fmt.Errorf(
			"%w: dynamic adapters require redirect rejection, per-request DNS validation/pinning, and a bounded response",
			ErrInvalidArgument,
		)
	}
	if provider == ProviderBluesky &&
		(!config.RequiresPAR ||
			!config.RequiresDPoP ||
			!config.RequiresATH ||
			!config.RequiresIssuer ||
			!config.RequiresSubject ||
			config.RefreshTokenMode != RefreshTokenSingleUse) {
		return fmt.Errorf(
			"%w: Bluesky requires PAR, DPoP with ath, issuer/subject binding, and single-use refresh",
			ErrInvalidArgument,
		)
	}
	return nil
}

func validateDynamicAuthorization(
	config DynamicOAuthConfig,
	authorization DynamicAuthorization,
) error {
	if len(authorization.ProviderState) == 0 ||
		len(authorization.ProviderState) > maximumProviderStateBytes {
		return fmt.Errorf("%w: dynamic provider state is invalid", ErrInvalidArgument)
	}
	if authorization.Binding.Issuer == "" ||
		authorization.Binding.ResourceServer == "" {
		return fmt.Errorf("%w: issuer and resource server are required", ErrInvalidArgument)
	}
	target, err := url.Parse(authorization.URL)
	if err != nil || target.Scheme != "https" || target.Host == "" ||
		target.User != nil || target.Fragment != "" {
		return fmt.Errorf("%w: dynamic authorization URL is invalid", ErrInvalidArgument)
	}
	targetOrigin, err := canonicalOrigin(target.Scheme + "://" + target.Host)
	if err != nil || targetOrigin != authorization.Binding.Issuer {
		return fmt.Errorf("%w: authorization issuer binding mismatch", ErrInvalidArgument)
	}
	requestURI := strings.TrimSpace(authorization.PARRequestURI)
	if config.RequiresPAR &&
		(requestURI == "" ||
			len(requestURI) > maximumDiscoveryInputBytes ||
			containsUnsafeText(requestURI)) {
		return fmt.Errorf("%w: PAR request_uri is invalid", ErrInvalidArgument)
	}
	if !config.RequiresPAR && requestURI != "" {
		return fmt.Errorf("%w: unexpected PAR request_uri", ErrInvalidArgument)
	}
	return nil
}

func validateDynamicSession(
	config DynamicOAuthConfig,
	expected OAuthBinding,
	session DynamicSession,
) error {
	actual, err := canonicalOAuthBinding(session.Binding)
	if err != nil {
		return err
	}
	if err = requireExactBinding(expected, actual, config.RequiresSubject); err != nil {
		return err
	}
	if strings.TrimSpace(session.Credential.AccessToken) == "" ||
		validateScopes(config.Scopes, session.Credential.Scopes) != nil {
		return ErrReconnectRequired
	}
	if config.RefreshTokenMode == RefreshTokenSingleUse &&
		strings.TrimSpace(session.Credential.RefreshToken) == "" {
		return ErrReconnectRequired
	}
	if config.RequiresDPoP &&
		(len(session.ProviderState) == 0 ||
			len(session.ProviderState) > maximumProviderStateBytes) {
		return ErrReconnectRequired
	}
	return nil
}

func validateAuthenticatedRequest(
	config DynamicOAuthConfig,
	request AuthenticatedRequest,
) error {
	switch request.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		return fmt.Errorf("%w: authenticated HTTP method is invalid", ErrInvalidArgument)
	}
	if len(request.Body) > maximumAuthenticatedBody {
		return fmt.Errorf("%w: authenticated request body is too large", ErrInvalidArgument)
	}
	if len(request.Path) == 0 ||
		!strings.HasPrefix(request.Path, "/") ||
		strings.HasPrefix(request.Path, "//") ||
		strings.Contains(request.Path, "\\") ||
		containsUnsafeText(request.Path) {
		return fmt.Errorf("%w: authenticated request path is invalid", ErrInvalidArgument)
	}
	target, err := url.ParseRequestURI(request.Path)
	if err != nil || target.IsAbs() || target.Host != "" || target.Fragment != "" {
		return fmt.Errorf("%w: authenticated request path is invalid", ErrInvalidArgument)
	}
	decodedPath, err := url.PathUnescape(target.EscapedPath())
	if err != nil ||
		strings.Contains(decodedPath, "\\") ||
		containsUnsafeText(decodedPath) {
		return fmt.Errorf("%w: authenticated request path is invalid", ErrInvalidArgument)
	}
	lowerEscapedPath := strings.ToLower(target.EscapedPath())
	if strings.Contains(lowerEscapedPath, "%2f") ||
		strings.Contains(lowerEscapedPath, "%5c") ||
		strings.Contains(lowerEscapedPath, "%25") {
		return fmt.Errorf("%w: encoded path separators are forbidden", ErrInvalidArgument)
	}
	for _, segment := range strings.Split(decodedPath, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("%w: path traversal is forbidden", ErrInvalidArgument)
		}
	}
	headerBytes := 0
	for key, values := range request.Header {
		canonical := strings.ToLower(strings.TrimSpace(key))
		switch canonical {
		case "authorization", "dpop", "host", "cookie", "proxy-authorization":
			return fmt.Errorf("%w: caller-controlled authentication headers are forbidden", ErrInvalidArgument)
		}
		headerBytes += len(key)
		for _, value := range values {
			if containsUnsafeText(value) {
				return fmt.Errorf("%w: authenticated request header is invalid", ErrInvalidArgument)
			}
			headerBytes += len(value)
		}
	}
	if headerBytes > maximumAuthenticatedHeaders {
		return fmt.Errorf("%w: authenticated request headers are too large", ErrInvalidArgument)
	}
	if !config.NetworkPolicy.RejectRedirects ||
		!config.NetworkPolicy.ValidateAndPinDNS {
		return fmt.Errorf("%w: adapter network policy is unsafe", ErrInvalidArgument)
	}
	return nil
}

func validateAuthenticatedResponse(
	config DynamicOAuthConfig,
	response AuthenticatedResponse,
) error {
	if response.StatusCode < 100 || response.StatusCode > 599 {
		return fmt.Errorf("%w: provider response status is invalid", ErrInvalidArgument)
	}
	limit := config.NetworkPolicy.MaxResponseBytes
	if limit <= 0 || limit > maximumAuthenticatedBody {
		limit = maximumAuthenticatedBody
	}
	if int64(len(response.Body)) > limit {
		return fmt.Errorf("%w: provider response is too large", ErrInvalidArgument)
	}
	headerBytes := 0
	for key, values := range response.Header {
		headerBytes += len(key)
		for _, value := range values {
			headerBytes += len(value)
		}
	}
	if headerBytes > maximumAuthenticatedHeaders {
		return fmt.Errorf("%w: provider response headers are too large", ErrInvalidArgument)
	}
	return nil
}

func sanitizeAuthenticatedResponse(
	response AuthenticatedResponse,
) AuthenticatedResponse {
	response.Body = append([]byte(nil), response.Body...)
	response.Header = response.Header.Clone()
	for _, header := range []string{
		"Authorization",
		"Cookie",
		"DPoP",
		"DPoP-Nonce",
		"Proxy-Authorization",
		"Set-Cookie",
	} {
		response.Header.Del(header)
	}
	return response
}

func validateDiscoveryInput(provider Provider, input DiscoveryInput) error {
	if len(input.Value) == 0 ||
		len(input.Value) > maximumDiscoveryInputBytes ||
		strings.TrimSpace(input.Value) != input.Value ||
		containsUnsafeText(input.Value) {
		return fmt.Errorf("%w: discovery input is invalid", ErrInvalidArgument)
	}
	switch provider {
	case ProviderMastodon:
		if input.Kind != DiscoveryInstanceOrigin {
			return fmt.Errorf("%w: Mastodon requires an instance origin", ErrInvalidArgument)
		}
		_, err := canonicalPublicOrigin(input.Value)
		return err
	case ProviderBluesky:
		switch input.Kind {
		case DiscoveryHandle:
			if !validPublicDNSName(strings.ToLower(input.Value)) ||
				strings.ToLower(input.Value) != input.Value {
				return fmt.Errorf("%w: AT Protocol handle is invalid", ErrInvalidArgument)
			}
			return nil
		case DiscoveryDID:
			return validateDID(input.Value)
		case DiscoveryPDSOrigin:
			_, err := canonicalPublicOrigin(input.Value)
			return err
		default:
			return fmt.Errorf("%w: unsupported AT Protocol discovery input", ErrInvalidArgument)
		}
	default:
		return fmt.Errorf("%w: provider does not accept dynamic discovery", ErrInvalidArgument)
	}
}

func canonicalOAuthBinding(binding OAuthBinding) (OAuthBinding, error) {
	if binding == (OAuthBinding{}) {
		return binding, nil
	}
	issuer, err := canonicalPublicOrigin(binding.Issuer)
	if err != nil {
		return OAuthBinding{}, fmt.Errorf("%w: invalid OAuth issuer", ErrInvalidArgument)
	}
	resource, err := canonicalPublicOrigin(binding.ResourceServer)
	if err != nil {
		return OAuthBinding{}, fmt.Errorf("%w: invalid OAuth resource server", ErrInvalidArgument)
	}
	subject := binding.Subject
	if subject != "" {
		if err = validateDID(subject); err != nil {
			return OAuthBinding{}, fmt.Errorf("%w: invalid OAuth subject", ErrInvalidArgument)
		}
	}
	return OAuthBinding{
		Issuer:         issuer,
		ResourceServer: resource,
		Subject:        subject,
	}, nil
}

func requireExactBinding(
	expected, actual OAuthBinding,
	requireSubject bool,
) error {
	expected, err := canonicalOAuthBinding(expected)
	if err != nil {
		return err
	}
	actual, err = canonicalOAuthBinding(actual)
	if err != nil {
		return err
	}
	if expected.Issuer != actual.Issuer ||
		expected.ResourceServer != actual.ResourceServer ||
		(expected.Subject != "" && expected.Subject != actual.Subject) ||
		(requireSubject && actual.Subject == "") {
		return fmt.Errorf("%w: OAuth issuer/resource/subject binding mismatch", ErrInvalidState)
	}
	return nil
}

func canonicalOrigin(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || containsUnsafeText(raw) {
		return "", ErrInvalidArgument
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
		parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrInvalidArgument
	}
	hostname := strings.ToLower(parsed.Hostname())
	if !validDNSName(hostname) {
		return "", ErrInvalidArgument
	}
	port := parsed.Port()
	if port == "443" {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return "https://" + host, nil
}

func canonicalPublicOrigin(raw string) (string, error) {
	origin, err := canonicalOrigin(raw)
	if err != nil {
		return "", fmt.Errorf("%w: discovery origin is invalid", ErrInvalidArgument)
	}
	parsed, _ := url.Parse(origin)
	hostname := parsed.Hostname()
	if address, parseErr := netip.ParseAddr(hostname); parseErr == nil {
		if !address.IsGlobalUnicast() ||
			address.IsPrivate() ||
			address.IsLoopback() ||
			address.IsLinkLocalUnicast() {
			return "", fmt.Errorf("%w: discovery IP literal is forbidden", ErrInvalidArgument)
		}
		// DNS pinning is mandatory even for public targets; rejecting all
		// literals keeps discovery policy uniform and avoids alternate forms.
		return "", fmt.Errorf("%w: discovery IP literals are forbidden", ErrInvalidArgument)
	}
	if !validPublicDNSName(hostname) {
		return "", fmt.Errorf("%w: discovery host is forbidden", ErrInvalidArgument)
	}
	return origin, nil
}

func validateDID(value string) error {
	if strings.TrimSpace(value) != value || containsUnsafeText(value) ||
		!strings.HasPrefix(value, "did:") {
		return ErrInvalidArgument
	}
	parts := strings.Split(value, ":")
	if len(parts) < 3 || parts[1] == "" {
		return ErrInvalidArgument
	}
	switch parts[1] {
	case "plc":
		if len(parts) != 3 || len(parts[2]) != 24 {
			return ErrInvalidArgument
		}
		for _, character := range parts[2] {
			if !(character >= 'a' && character <= 'z') &&
				!(character >= '2' && character <= '7') {
				return ErrInvalidArgument
			}
		}
	case "web":
		if len(parts) < 3 || !validCanonicalDIDWebHost(parts[2]) {
			return ErrInvalidArgument
		}
		for _, rawSegment := range parts[3:] {
			if rawSegment == "" {
				return ErrInvalidArgument
			}
			segment, decodeErr := url.PathUnescape(rawSegment)
			if decodeErr != nil ||
				segment == "" ||
				segment == "." ||
				segment == ".." ||
				containsUnsafeText(segment) ||
				strings.ContainsAny(segment, `/\`) ||
				url.PathEscape(segment) != rawSegment {
				return ErrInvalidArgument
			}
		}
	default:
		return ErrInvalidArgument
	}
	return nil
}

func validCanonicalDIDWebHost(raw string) bool {
	if raw == "" || strings.Contains(raw, "%") && !strings.Contains(raw, "%3A") {
		return false
	}
	if strings.Contains(raw, "%3a") || strings.Count(raw, "%3A") > 1 {
		return false
	}
	decoded := strings.Replace(raw, "%3A", ":", 1)
	host := decoded
	if strings.Contains(decoded, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(decoded)
		if err != nil || host == "" || port == "" {
			return false
		}
		number, conversionErr := strconv.Atoi(port)
		if conversionErr != nil || number < 1 || number > 65535 {
			return false
		}
		if raw != host+"%3A"+port {
			return false
		}
	}
	return host == strings.ToLower(host) && validPublicDNSName(host)
}

func validPublicDNSName(host string) bool {
	if !validDNSName(host) ||
		!strings.Contains(host, ".") ||
		host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") {
		return false
	}
	_, parseErr := netip.ParseAddr(host)
	return parseErr != nil
}

func validDNSName(host string) bool {
	if host == "" || len(host) > 253 || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 ||
			label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z') &&
				!(character >= 'A' && character <= 'Z') &&
				!(character >= '0' && character <= '9') &&
				character != '-' {
				return false
			}
		}
	}
	return true
}

func containsUnsafeText(value string) bool {
	if !utf8.ValidString(value) {
		return true
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func dynamicAttemptAdditionalData(
	attemptID string,
	provider Provider,
	binding OAuthBinding,
) []byte {
	return []byte(
		"f05|dynamic-attempt|" + attemptID + "|" + string(provider) + "|" +
			binding.Issuer + "|" + binding.ResourceServer + "|" + binding.Subject,
	)
}

func dynamicSessionAdditionalData(
	workspaceID string,
	provider Provider,
	remoteID string,
	binding OAuthBinding,
) []byte {
	return []byte(
		"f05|dynamic-session|" + workspaceID + "|" + string(provider) + "|" +
			remoteID + "|" + binding.Issuer + "|" +
			binding.ResourceServer + "|" + binding.Subject,
	)
}
