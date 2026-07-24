package socialconnections

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	defaultAuthorizationTTL = 10 * time.Minute
	defaultSelectionTTL     = 15 * time.Minute
	defaultRefreshLockTTL   = 30 * time.Second
	defaultRefreshBefore    = 5 * time.Minute
)

var requiredScopes = map[Provider][]string{
	ProviderFacebookPages: {
		"pages_show_list",
		"pages_read_engagement",
		"pages_manage_posts",
	},
	ProviderInstagramProfessional: {
		"instagram_business_basic",
		"instagram_business_content_publish",
	},
}

type Config struct {
	Repository       Repository
	Authorizer       Authorizer
	Cipher           CredentialCipher
	Adapters         map[Provider]Adapter
	Now              func() time.Time
	AuthorizationTTL time.Duration
	SelectionTTL     time.Duration
	RefreshLockTTL   time.Duration
	RefreshBefore    time.Duration
}

type Service struct {
	repository       Repository
	authorizer       Authorizer
	cipher           CredentialCipher
	adapters         map[Provider]Adapter
	now              func() time.Time
	authorizationTTL time.Duration
	selectionTTL     time.Duration
	refreshLockTTL   time.Duration
	refreshBefore    time.Duration
}

func NewService(config Config) (*Service, error) {
	if config.Repository == nil || config.Authorizer == nil || config.Cipher == nil {
		return nil, fmt.Errorf(
			"%w: repository, authorizer, and credential cipher are required",
			ErrInvalidArgument,
		)
	}
	adapters := make(map[Provider]Adapter, len(SupportedProviders))
	for _, provider := range SupportedProviders {
		adapter := config.Adapters[provider]
		if adapter == nil {
			return nil, fmt.Errorf("%w: %s adapter is required", ErrInvalidArgument, provider)
		}
		if err := validateOAuthConfig(provider, adapter.Config()); err != nil {
			return nil, err
		}
		adapters[provider] = adapter
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.AuthorizationTTL == 0 {
		config.AuthorizationTTL = defaultAuthorizationTTL
	}
	if config.SelectionTTL == 0 {
		config.SelectionTTL = defaultSelectionTTL
	}
	if config.RefreshLockTTL == 0 {
		config.RefreshLockTTL = defaultRefreshLockTTL
	}
	if config.RefreshBefore == 0 {
		config.RefreshBefore = defaultRefreshBefore
	}
	if config.AuthorizationTTL <= 0 || config.SelectionTTL <= 0 ||
		config.RefreshLockTTL <= 0 || config.RefreshBefore < 0 {
		return nil, fmt.Errorf("%w: service durations are invalid", ErrInvalidArgument)
	}
	return &Service{
		repository:       config.Repository,
		authorizer:       config.Authorizer,
		cipher:           config.Cipher,
		adapters:         adapters,
		now:              config.Now,
		authorizationTTL: config.AuthorizationTTL,
		selectionTTL:     config.SelectionTTL,
		refreshLockTTL:   config.RefreshLockTTL,
		refreshBefore:    config.RefreshBefore,
	}, nil
}

func (service *Service) Begin(
	ctx context.Context,
	request BeginRequest,
) (Authorization, error) {
	if strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.ActorID) == "" {
		return Authorization{}, fmt.Errorf("%w: workspace and actor are required", ErrInvalidArgument)
	}
	adapter, ok := service.adapters[request.Provider]
	if !ok {
		return Authorization{}, ErrUnsupportedProvider
	}
	if err := service.authorize(
		ctx,
		request.WorkspaceID,
		request.ActorID,
		PermissionManageChannels,
	); err != nil {
		return Authorization{}, err
	}

	attemptID, err := randomOpaqueID(18)
	if err != nil {
		return Authorization{}, fmt.Errorf("generate social OAuth attempt ID: %w", err)
	}
	state, err := randomOpaqueID(32)
	if err != nil {
		return Authorization{}, fmt.Errorf("generate social OAuth state: %w", err)
	}
	oauthConfig := adapter.Config()
	verifier := ""
	var verifierCiphertext Ciphertext
	if oauthConfig.SupportsPKCE {
		verifier, err = randomOpaqueID(64)
		if err != nil {
			return Authorization{}, fmt.Errorf("generate social PKCE verifier: %w", err)
		}
		verifierCiphertext, err = service.cipher.Seal(
			[]byte(verifier),
			attemptAdditionalData(attemptID),
		)
		if err != nil {
			return Authorization{}, fmt.Errorf("seal social PKCE verifier: %w", err)
		}
	}
	now := service.now().UTC()
	attempt := OAuthAttempt{
		ID:                     attemptID,
		StateHash:              digest(state),
		WorkspaceID:            request.WorkspaceID,
		ActorID:                request.ActorID,
		Provider:               request.Provider,
		PKCEVerifierCiphertext: verifierCiphertext,
		CreatedAt:              now,
		ExpiresAt:              now.Add(service.authorizationTTL),
	}
	if err := service.repository.CreateAttempt(ctx, attempt); err != nil {
		return Authorization{}, fmt.Errorf("save social OAuth attempt: %w", err)
	}
	authorizationURL, err := buildAuthorizationURL(oauthConfig, state, verifier)
	if err != nil {
		return Authorization{}, err
	}
	return Authorization{URL: authorizationURL, ExpiresAt: attempt.ExpiresAt}, nil
}

func (service *Service) Callback(
	ctx context.Context,
	request CallbackRequest,
) (Selection, error) {
	if strings.TrimSpace(request.State) == "" {
		return Selection{}, ErrInvalidState
	}
	now := service.now().UTC()
	attempt, err := service.repository.ConsumeAttempt(ctx, digest(request.State), now)
	if err != nil {
		return Selection{}, err
	}
	if request.ProviderError != "" {
		return Selection{}, ErrProviderDenied
	}
	if strings.TrimSpace(request.Code) == "" {
		return Selection{}, fmt.Errorf("%w: authorization code is required", ErrInvalidArgument)
	}
	adapter := service.adapters[attempt.Provider]
	verifier := ""
	if len(attempt.PKCEVerifierCiphertext.Data) > 0 {
		plaintext, openErr := service.cipher.Open(
			attempt.PKCEVerifierCiphertext,
			attemptAdditionalData(attempt.ID),
		)
		if openErr != nil {
			return Selection{}, fmt.Errorf("open social PKCE verifier: %w", openErr)
		}
		verifier = string(plaintext)
	}
	grant, err := adapter.Exchange(ctx, ExchangeRequest{
		Code:         request.Code,
		RedirectURL:  adapter.Config().RedirectURL,
		PKCEVerifier: verifier,
	})
	if err != nil {
		return Selection{}, err
	}
	resources, err := adapter.Discover(ctx, grant)
	if err != nil {
		return Selection{}, err
	}
	valid := make([]DiscoveredResource, 0, len(resources))
	for _, resource := range resources {
		if validateDiscoveredResource(attempt.Provider, resource) == nil {
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
		additionalData := credentialAdditionalData(
			attempt.WorkspaceID,
			attempt.Provider,
			resource.Candidate.RemoteID,
		)
		accessCiphertext, sealErr := service.cipher.Seal(
			[]byte(resource.Credential.AccessToken),
			additionalData,
		)
		if sealErr != nil {
			return Selection{}, fmt.Errorf("seal social access token: %w", sealErr)
		}
		refreshCiphertext, sealErr := service.cipher.Seal(
			[]byte(resource.Credential.RefreshToken),
			additionalData,
		)
		if sealErr != nil {
			return Selection{}, fmt.Errorf("seal social refresh token: %w", sealErr)
		}
		candidate := cloneCandidate(resource.Candidate)
		stored.Resources = append(stored.Resources, StoredResource{
			Candidate:              candidate,
			AccessTokenCiphertext:  accessCiphertext,
			RefreshTokenCiphertext: refreshCiphertext,
			TokenExpiresAt:         cloneTimePointer(resource.Credential.ExpiresAt),
		})
		safe.Resources = append(safe.Resources, candidate)
	}
	if err := service.repository.SaveSelection(ctx, stored); err != nil {
		return Selection{}, fmt.Errorf("save social resource selection: %w", err)
	}
	return safe, nil
}

func (service *Service) Select(
	ctx context.Context,
	request SelectRequest,
) (Connection, error) {
	if strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.ActorID) == "" ||
		strings.TrimSpace(request.SelectionID) == "" ||
		strings.TrimSpace(request.RemoteID) == "" {
		return Connection{}, fmt.Errorf("%w: selection fields are required", ErrInvalidArgument)
	}
	if err := service.authorize(
		ctx,
		request.WorkspaceID,
		request.ActorID,
		PermissionManageChannels,
	); err != nil {
		return Connection{}, err
	}
	connectionID, err := randomOpaqueID(18)
	if err != nil {
		return Connection{}, fmt.Errorf("generate social connection ID: %w", err)
	}
	event, err := service.newEvent(
		EventConnected,
		request.WorkspaceID,
		connectionID,
		"",
		request.RemoteID,
		request.ActorID,
		"",
	)
	if err != nil {
		return Connection{}, err
	}
	connection, reconnected, err := service.repository.Connect(ctx, ConnectCommand{
		NewConnectionID: connectionID,
		WorkspaceID:     request.WorkspaceID,
		ActorID:         request.ActorID,
		SelectionID:     request.SelectionID,
		RemoteID:        request.RemoteID,
		Now:             service.now().UTC(),
		Event:           event,
	})
	if err != nil {
		return Connection{}, err
	}
	if reconnected {
		// The repository atomically rewrites this event to target the existing
		// connection and use EventReconnected.
		return connection, nil
	}
	return connection, nil
}

func (service *Service) List(
	ctx context.Context,
	workspaceID, actorID string,
) ([]Connection, error) {
	if err := service.authorize(
		ctx,
		workspaceID,
		actorID,
		PermissionViewWorkspace,
	); err != nil {
		return nil, err
	}
	return service.repository.ListConnections(ctx, workspaceID)
}

// AccessToken is an internal service-to-service boundary for F8. API handlers
// must never serialize its result. It refreshes at most once under the
// repository's distributed lock.
func (service *Service) AccessToken(
	ctx context.Context,
	workspaceID, connectionID string,
) (string, error) {
	now := service.now().UTC()
	stored, claimed, err := service.repository.ClaimRefresh(
		ctx,
		workspaceID,
		connectionID,
		now,
		now.Add(service.refreshBefore),
		service.refreshLockTTL,
	)
	if err != nil {
		return "", err
	}
	if !claimed {
		return service.openAccessToken(stored)
	}
	credential, err := service.openCredential(stored)
	if err != nil {
		_ = service.repository.ReleaseRefresh(ctx, workspaceID, connectionID)
		return "", err
	}
	refreshed, err := service.adapters[stored.Provider].Refresh(ctx, credential)
	if err != nil {
		return "", service.handleCredentialFailure(ctx, stored, err, now)
	}
	if validateScopes(stored.Provider, refreshed.Scopes) != nil ||
		strings.TrimSpace(refreshed.AccessToken) == "" {
		return "", service.markReconnect(ctx, stored, "permission_missing", now)
	}
	additionalData := credentialAdditionalData(
		stored.WorkspaceID,
		stored.Provider,
		stored.RemoteID,
	)
	accessCiphertext, err := service.cipher.Seal(
		[]byte(refreshed.AccessToken),
		additionalData,
	)
	if err != nil {
		_ = service.repository.ReleaseRefresh(ctx, workspaceID, connectionID)
		return "", err
	}
	refreshCiphertext, err := service.cipher.Seal(
		[]byte(refreshed.RefreshToken),
		additionalData,
	)
	if err != nil {
		_ = service.repository.ReleaseRefresh(ctx, workspaceID, connectionID)
		return "", err
	}
	event, err := service.newEvent(
		EventTokenRefreshed,
		stored.WorkspaceID,
		stored.ID,
		stored.Provider,
		stored.RemoteID,
		"",
		"",
	)
	if err != nil {
		_ = service.repository.ReleaseRefresh(ctx, workspaceID, connectionID)
		return "", err
	}
	_, err = service.repository.CompleteRefresh(ctx, RefreshCommand{
		ConnectionID:           stored.ID,
		AccessTokenCiphertext:  accessCiphertext,
		RefreshTokenCiphertext: refreshCiphertext,
		Scopes:                 append([]string(nil), refreshed.Scopes...),
		ExpiresAt:              cloneTimePointer(refreshed.ExpiresAt),
		VerifiedAt:             now,
		Now:                    now,
		Event:                  event,
	})
	if err != nil {
		return "", err
	}
	return refreshed.AccessToken, nil
}

func (service *Service) Verify(
	ctx context.Context,
	workspaceID, connectionID string,
) error {
	stored, err := service.repository.GetCredential(ctx, workspaceID, connectionID)
	if err != nil {
		return err
	}
	if stored.Status == StatusReconnectRequired {
		return ErrReconnectRequired
	}
	if stored.Status == StatusRevoked {
		return ErrConnectionRevoked
	}
	credential, err := service.openCredential(stored)
	if err != nil {
		return err
	}
	now := service.now().UTC()
	if err := service.adapters[stored.Provider].Verify(
		ctx,
		stored.RemoteID,
		credential,
	); err != nil {
		var failure *ProviderFailure
		if errors.As(err, &failure) && isReconnectFailure(failure.Kind) {
			return service.markReconnect(ctx, stored, string(failure.Kind), now)
		}
		return err
	}
	return nil
}

func (service *Service) Revoke(
	ctx context.Context,
	workspaceID, actorID, connectionID string,
) (RevocationResult, error) {
	if err := service.authorize(
		ctx,
		workspaceID,
		actorID,
		PermissionManageChannels,
	); err != nil {
		return RevocationResult{}, err
	}
	stored, err := service.repository.GetCredential(ctx, workspaceID, connectionID)
	if err != nil {
		return RevocationResult{}, err
	}
	providerRevoked := false
	if stored.Status != StatusRevoked && len(stored.AccessTokenCiphertext.Data) > 0 {
		credential, openErr := service.openCredential(stored)
		if openErr == nil {
			providerRevoked = service.adapters[stored.Provider].Revoke(
				ctx,
				stored.RemoteID,
				credential,
			) == nil
		}
	}
	event, err := service.newEvent(
		EventDisconnected,
		workspaceID,
		connectionID,
		stored.Provider,
		stored.RemoteID,
		actorID,
		"",
	)
	if err != nil {
		return RevocationResult{}, err
	}
	connection, _, err := service.repository.Revoke(
		ctx,
		workspaceID,
		connectionID,
		service.now().UTC(),
		event,
	)
	if err != nil {
		return RevocationResult{}, err
	}
	return RevocationResult{
		Connection:      connection,
		ProviderRevoked: providerRevoked,
	}, nil
}

func (service *Service) handleCredentialFailure(
	ctx context.Context,
	stored StoredCredential,
	err error,
	now time.Time,
) error {
	if errors.Is(err, ErrNotRefreshable) {
		return service.markReconnect(ctx, stored, "not_refreshable", now)
	}
	var failure *ProviderFailure
	if errors.As(err, &failure) && isReconnectFailure(failure.Kind) {
		return service.markReconnect(ctx, stored, string(failure.Kind), now)
	}
	_ = service.repository.ReleaseRefresh(ctx, stored.WorkspaceID, stored.ID)
	return err
}

func (service *Service) markReconnect(
	ctx context.Context,
	stored StoredCredential,
	reason string,
	now time.Time,
) error {
	event, err := service.newEvent(
		EventReconnectRequired,
		stored.WorkspaceID,
		stored.ID,
		stored.Provider,
		stored.RemoteID,
		"",
		reason,
	)
	if err != nil {
		_ = service.repository.ReleaseRefresh(ctx, stored.WorkspaceID, stored.ID)
		return err
	}
	if _, _, err = service.repository.MarkReconnectRequired(
		ctx,
		stored.WorkspaceID,
		stored.ID,
		reason,
		now,
		event,
	); err != nil {
		return err
	}
	return ErrReconnectRequired
}

func (service *Service) openAccessToken(stored StoredCredential) (string, error) {
	if stored.Status == StatusReconnectRequired {
		return "", ErrReconnectRequired
	}
	if stored.Status == StatusRevoked {
		return "", ErrConnectionRevoked
	}
	plaintext, err := service.cipher.Open(
		stored.AccessTokenCiphertext,
		credentialAdditionalData(stored.WorkspaceID, stored.Provider, stored.RemoteID),
	)
	if err != nil {
		return "", fmt.Errorf("open social access token: %w", err)
	}
	if len(plaintext) == 0 {
		return "", ErrReconnectRequired
	}
	return string(plaintext), nil
}

func (service *Service) openCredential(
	stored StoredCredential,
) (Credential, error) {
	access, err := service.openAccessToken(stored)
	if err != nil {
		return Credential{}, err
	}
	refresh, err := service.cipher.Open(
		stored.RefreshTokenCiphertext,
		credentialAdditionalData(stored.WorkspaceID, stored.Provider, stored.RemoteID),
	)
	if err != nil {
		return Credential{}, fmt.Errorf("open social refresh token: %w", err)
	}
	return Credential{
		AccessToken:  access,
		RefreshToken: string(refresh),
		ExpiresAt:    cloneTimePointer(stored.TokenExpiresAt),
		Scopes:       append([]string(nil), stored.Scopes...),
	}, nil
}

func (service *Service) authorize(
	ctx context.Context,
	workspaceID, actorID string,
	permission Permission,
) error {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(actorID) == "" {
		return fmt.Errorf("%w: workspace and actor are required", ErrInvalidArgument)
	}
	if err := service.authorizer.Authorize(ctx, workspaceID, actorID, permission); err != nil {
		return fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	return nil
}

func (service *Service) newEvent(
	eventType string,
	workspaceID, connectionID string,
	provider Provider,
	remoteID, actorID, reason string,
) (Event, error) {
	id, err := randomOpaqueID(18)
	if err != nil {
		return Event{}, fmt.Errorf("generate social event ID: %w", err)
	}
	correlationID, err := randomOpaqueID(18)
	if err != nil {
		return Event{}, fmt.Errorf("generate social correlation ID: %w", err)
	}
	return Event{
		ID:            id,
		Type:          eventType,
		Version:       1,
		WorkspaceID:   workspaceID,
		ConnectionID:  connectionID,
		Provider:      provider,
		RemoteID:      remoteID,
		ActorID:       actorID,
		Reason:        reason,
		CorrelationID: correlationID,
		OccurredAt:    service.now().UTC(),
	}, nil
}

func buildAuthorizationURL(
	config OAuthConfig,
	state, verifier string,
) (string, error) {
	authorizationURL, err := url.Parse(config.AuthorizationURL)
	if err != nil {
		return "", fmt.Errorf("%w: invalid provider authorization URL", ErrInvalidArgument)
	}
	query := authorizationURL.Query()
	query.Set("client_id", config.ClientID)
	query.Set("redirect_uri", config.RedirectURL)
	query.Set("response_type", "code")
	query.Set("scope", strings.Join(config.Scopes, ","))
	query.Set("state", state)
	if config.SupportsPKCE {
		query.Set("code_challenge", pkceChallenge(verifier))
		query.Set("code_challenge_method", "S256")
	}
	for key, value := range config.ExtraParameters {
		query.Set(key, value)
	}
	authorizationURL.RawQuery = query.Encode()
	return authorizationURL.String(), nil
}

func validateOAuthConfig(provider Provider, config OAuthConfig) error {
	if strings.TrimSpace(config.ClientID) == "" {
		return fmt.Errorf("%w: %s client ID is required", ErrInvalidArgument, provider)
	}
	for _, rawURL := range []string{config.AuthorizationURL, config.RedirectURL} {
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("%w: %s OAuth URLs must use HTTPS", ErrInvalidArgument, provider)
		}
	}
	if err := validateRequestedScopes(provider, config.Scopes); err != nil {
		return err
	}
	reserved := []string{
		"client_id",
		"redirect_uri",
		"response_type",
		"scope",
		"state",
		"code_challenge",
		"code_challenge_method",
	}
	for _, key := range reserved {
		if _, exists := config.ExtraParameters[key]; exists {
			return fmt.Errorf(
				"%w: provider extra parameters cannot override %s",
				ErrInvalidArgument,
				key,
			)
		}
	}
	return nil
}

func validateDiscoveredResource(
	provider Provider,
	resource DiscoveredResource,
) error {
	if strings.TrimSpace(resource.Candidate.RemoteID) == "" ||
		strings.TrimSpace(resource.Candidate.DisplayName) == "" ||
		strings.TrimSpace(resource.Credential.AccessToken) == "" {
		return ErrInvalidArgument
	}
	if err := validateScopes(provider, resource.Credential.Scopes); err != nil {
		return err
	}
	switch provider {
	case ProviderFacebookPages:
		if resource.Candidate.ResourceType != ResourceFacebookPage ||
			resource.Candidate.AccountType != AccountTypePage {
			return ErrInvalidArgument
		}
	case ProviderInstagramProfessional:
		if resource.Candidate.ResourceType != ResourceInstagramProfessional ||
			(resource.Candidate.AccountType != AccountTypeBusiness &&
				resource.Candidate.AccountType != AccountTypeCreator) {
			return ErrInvalidArgument
		}
	default:
		return ErrUnsupportedProvider
	}
	return nil
}

func validateScopes(provider Provider, scopes []string) error {
	required, ok := requiredScopes[provider]
	if !ok {
		return ErrUnsupportedProvider
	}
	for _, scope := range required {
		if !slices.Contains(scopes, scope) {
			return fmt.Errorf("%w: required scope %s is missing", ErrReconnectRequired, scope)
		}
	}
	return nil
}

func validateRequestedScopes(provider Provider, scopes []string) error {
	if err := validateScopes(provider, scopes); err != nil {
		return err
	}
	required := requiredScopes[provider]
	if len(scopes) != len(required) {
		return fmt.Errorf(
			"%w: %s adapter must request only launch scopes",
			ErrInvalidArgument,
			provider,
		)
	}
	for _, scope := range scopes {
		if !slices.Contains(required, scope) {
			return fmt.Errorf(
				"%w: %s adapter requests unsupported scope %s",
				ErrInvalidArgument,
				provider,
				scope,
			)
		}
	}
	return nil
}

func isReconnectFailure(kind ProviderFailureKind) bool {
	return kind == FailureAuthentication ||
		kind == FailurePermissionMissing ||
		kind == FailureResourceGone
}

func attemptAdditionalData(attemptID string) []byte {
	return []byte("f05|oauth-attempt|" + attemptID)
}

func credentialAdditionalData(
	workspaceID string,
	provider Provider,
	remoteID string,
) []byte {
	return []byte("f05|credential|" + workspaceID + "|" + string(provider) + "|" + remoteID)
}

func cloneCandidate(candidate Candidate) Candidate {
	candidate.Scopes = append([]string(nil), candidate.Scopes...)
	return candidate
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
