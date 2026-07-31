package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	mastodonRuntimeRedirectURLKey = "social.mastodon.redirect_url"
	blueskyRuntimeClientIDKey     = "social.bluesky.client_id"
	blueskyRuntimeRedirectURLKey  = "social.bluesky.redirect_url"
	blueskyRuntimePLCDirectoryKey = "social.bluesky.plc_directory_origin"
	mastodonRuntimeRedirectURLEnv = "POSTQRON_F05_MASTODON_REDIRECT_URL"
	blueskyRuntimeClientIDEnv     = "POSTQRON_F05_BLUESKY_CLIENT_ID"
	blueskyRuntimeRedirectURLEnv  = "POSTQRON_F05_BLUESKY_REDIRECT_URL"
	blueskyRuntimePLCDirectoryEnv = "POSTQRON_F05_BLUESKY_PLC_DIRECTORY_ORIGIN"
)

func init() {
	decentralizedNetworksRuntimeDynamicHook = configureDecentralizedDynamicProviders
}

// configureDecentralizedNetworksRuntime intentionally leaves Mastodon and
// Bluesky registration to the typed dynamic hook added in #351. This static
// extension point must remain empty so centralized ownership, versioning, and
// fail-closed dynamic registration all flow through the provider-neutral root.
func configureDecentralizedNetworksRuntime(
	_ map[string]string,
	_ CredentialCipher,
	_ map[Provider]Adapter,
	_ map[Provider]ProviderAvailability,
) {
}

func configureDecentralizedDynamicProviders(
	input runtimeProviderFamilyInput,
) ([]RuntimeDynamicProviderRegistration, error) {
	registrations := make([]RuntimeDynamicProviderRegistration, 0, 2)

	mastodon, err := newMastodonRuntimeDynamicAdapter(input.Values)
	if err != nil {
		return nil, err
	}
	registrations = append(registrations, RuntimeDynamicProviderRegistration{
		Provider:         ProviderMastodon,
		Adapter:          mastodon,
		Configured:       mastodon != nil,
		SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
	})

	bluesky, err := newBlueskyRuntimeDynamicAdapter(input.Values, input.Cipher)
	if err != nil {
		return nil, err
	}
	registrations = append(registrations, RuntimeDynamicProviderRegistration{
		Provider:         ProviderBluesky,
		Adapter:          bluesky,
		Configured:       bluesky != nil,
		SupportedVersion: RuntimeDynamicProviderCompatibilityVersion,
	})

	return registrations, nil
}

type mastodonRuntimeState struct {
	Instance MastodonInstance   `json:"instance"`
	App      mastodonRuntimeApp `json:"app"`
}

type mastodonRuntimeAttemptState struct {
	Version int `json:"version"`
	mastodonRuntimeState
	PKCEVerifier string `json:"pkce_verifier,omitempty"`
}

type mastodonRuntimeDynamicAdapter struct {
	redirectURL string
	discovery   *MastodonDiscovery
	http        *mastodonSafeHTTP
}

type mastodonRuntimeApp struct {
	Origin       string `json:"origin"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

func newMastodonRuntimeDynamicAdapter(
	values map[string]string,
) (DynamicAdapter, error) {
	redirectURL := runtimeProviderValue(
		values,
		mastodonRuntimeRedirectURLKey,
		mastodonRuntimeRedirectURLEnv,
	)
	if strings.TrimSpace(redirectURL) == "" {
		return nil, nil
	}
	return &mastodonRuntimeDynamicAdapter{
		redirectURL: redirectURL,
		discovery:   NewMastodonDiscovery(),
		http:        newMastodonSafeHTTP(),
	}, nil
}

func (adapter *mastodonRuntimeDynamicAdapter) DynamicConfig() DynamicOAuthConfig {
	return DynamicOAuthConfig{
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), mastodonScopes...),
		RefreshTokenMode: RefreshTokenReusable,
		RevocationPolicy: RevocationRemoteRequired,
		NetworkPolicy: DynamicNetworkPolicy{
			RejectRedirects:   true,
			ValidateAndPinDNS: true,
			MaxResponseBytes:  maximumAuthenticatedBody,
		},
	}
}

func (adapter *mastodonRuntimeDynamicAdapter) BeginDynamic(
	ctx context.Context,
	request DynamicBeginRequest,
) (DynamicAuthorization, error) {
	origin := request.Discovery.Value
	if request.PreviousBinding != (OAuthBinding{}) {
		origin = request.PreviousBinding.ResourceServer
	}
	instance, err := adapter.discovery.Discover(ctx, origin)
	if err != nil {
		return DynamicAuthorization{}, err
	}
	app, err := adapter.registerApp(ctx, instance)
	if err != nil {
		return DynamicAuthorization{}, err
	}
	authorizationURL, err := url.Parse(instance.AuthorizationURL)
	if err != nil {
		return DynamicAuthorization{}, err
	}
	query := authorizationURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", app.ClientID)
	query.Set("redirect_uri", adapter.redirectURL)
	query.Set("scope", strings.Join(mastodonScopes, " "))
	query.Set("state", request.State)
	query.Set("force_login", "true")
	attemptState := mastodonRuntimeAttemptState{
		Version: 1,
		mastodonRuntimeState: mastodonRuntimeState{
			Instance: instance,
			App:      app,
		},
	}
	if instance.SupportsPKCE {
		verifier, verifierErr := randomOpaqueID(64)
		if verifierErr != nil {
			return DynamicAuthorization{}, verifierErr
		}
		attemptState.PKCEVerifier = verifier
		query.Set("code_challenge", pkceChallenge(verifier))
		query.Set("code_challenge_method", "S256")
	}
	authorizationURL.RawQuery = query.Encode()
	providerState, err := json.Marshal(attemptState)
	if err != nil {
		return DynamicAuthorization{}, err
	}
	return DynamicAuthorization{
		URL:           authorizationURL.String(),
		ProviderState: providerState,
		Binding: OAuthBinding{
			Issuer:         instance.Origin,
			ResourceServer: instance.Origin,
		},
	}, nil
}

func (adapter *mastodonRuntimeDynamicAdapter) CompleteDynamic(
	ctx context.Context,
	request DynamicCallbackRequest,
) (DynamicCompletion, error) {
	state, err := openMastodonRuntimeAttemptState(request.ProviderState)
	if err != nil {
		return DynamicCompletion{}, err
	}
	staticAdapter, err := adapter.runtimeAdapter(state.Instance, state.App)
	if err != nil {
		return DynamicCompletion{}, err
	}
	credential, err := staticAdapter.Exchange(ctx, ExchangeRequest{
		Code:         request.Code,
		RedirectURL:  request.RedirectURL,
		PKCEVerifier: state.PKCEVerifier,
	})
	if err != nil {
		return DynamicCompletion{}, err
	}
	resources, err := staticAdapter.Discover(ctx, credential)
	if err != nil {
		return DynamicCompletion{}, err
	}
	providerState, err := json.Marshal(state.mastodonRuntimeState)
	if err != nil {
		return DynamicCompletion{}, err
	}
	return DynamicCompletion{
		Resources:     resources,
		ProviderState: providerState,
		Binding: OAuthBinding{
			Issuer:         state.Instance.Origin,
			ResourceServer: state.Instance.Origin,
		},
	}, nil
}

func (adapter *mastodonRuntimeDynamicAdapter) RefreshDynamic(
	ctx context.Context,
	session DynamicSession,
) (DynamicRefreshResult, error) {
	state, err := openMastodonRuntimeState(session.ProviderState)
	if err != nil {
		return DynamicRefreshResult{}, err
	}
	staticAdapter, err := adapter.runtimeAdapter(state.Instance, state.App)
	if err != nil {
		return DynamicRefreshResult{}, err
	}
	credential, err := staticAdapter.Refresh(ctx, session.Credential)
	if err != nil {
		return DynamicRefreshResult{}, err
	}
	return DynamicRefreshResult{
		Session: DynamicSession{
			Binding:       session.Binding,
			Credential:    credential,
			ProviderState: append([]byte(nil), session.ProviderState...),
		},
	}, nil
}

func (adapter *mastodonRuntimeDynamicAdapter) DoAuthenticated(
	ctx context.Context,
	session DynamicSession,
	request AuthenticatedRequest,
) (DynamicAuthenticatedResult, error) {
	state, err := openMastodonRuntimeState(session.ProviderState)
	if err != nil {
		return DynamicAuthenticatedResult{}, err
	}
	origin, err := mastodonOrigin(state.Instance.Origin)
	if err != nil {
		return DynamicAuthenticatedResult{}, err
	}
	target, err := origin.Parse(request.Path)
	if err != nil {
		return DynamicAuthenticatedResult{}, ErrInvalidArgument
	}
	httpRequest, _ := http.NewRequestWithContext(
		ctx,
		request.Method,
		target.String(),
		bytes.NewReader(request.Body),
	)
	httpRequest.Header = request.Header.Clone()
	httpRequest.Header.Set("Authorization", "Bearer "+session.Credential.AccessToken)
	response, err := adapter.http.do(ctx, httpRequest)
	if err != nil {
		return DynamicAuthenticatedResult{}, mastodonFailure(
			"mastodon_authenticated_request",
			err,
		)
	}
	body, readErr := mastodonReadLimited(response)
	if readErr != nil {
		return DynamicAuthenticatedResult{}, mastodonFailure(
			"mastodon_authenticated_request",
			readErr,
		)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return DynamicAuthenticatedResult{}, mastodonStatusFailure(
			"mastodon_authenticated_request",
			response.StatusCode,
		)
	}
	return DynamicAuthenticatedResult{
		Response: AuthenticatedResponse{
			StatusCode: response.StatusCode,
			Header:     response.Header.Clone(),
			Body:       body,
		},
		Session: session,
	}, nil
}

func (adapter *mastodonRuntimeDynamicAdapter) RevokeDynamic(
	ctx context.Context,
	session DynamicSession,
) error {
	state, err := openMastodonRuntimeState(session.ProviderState)
	if err != nil {
		return err
	}
	staticAdapter, err := adapter.runtimeAdapter(state.Instance, state.App)
	if err != nil {
		return err
	}
	return staticAdapter.Revoke(ctx, "", session.Credential)
}

func (adapter *mastodonRuntimeDynamicAdapter) runtimeAdapter(
	instance MastodonInstance,
	app mastodonRuntimeApp,
) (*MastodonAdapter, error) {
	if app.Origin != instance.Origin ||
		strings.TrimSpace(app.ClientID) == "" ||
		strings.TrimSpace(app.ClientSecret) == "" {
		return nil, ErrInvalidArgument
	}
	return NewMastodonAdapter(MastodonAdapterConfig{
		Instance:     instance,
		ClientID:     app.ClientID,
		ClientSecret: app.ClientSecret,
		RedirectURL:  adapter.redirectURL,
		HTTP:         adapter.http,
	})
}

func (adapter *mastodonRuntimeDynamicAdapter) registerApp(
	ctx context.Context,
	instance MastodonInstance,
) (mastodonRuntimeApp, error) {
	origin, err := mastodonOrigin(instance.Origin)
	if err != nil {
		return mastodonRuntimeApp{}, err
	}
	target, err := url.Parse(instance.AppRegistrationURL)
	if err != nil || !mastodonSameOriginEndpoint(origin, target.String()) {
		return mastodonRuntimeApp{}, ErrInvalidArgument
	}
	form := url.Values{
		"client_name":   {"Postqron"},
		"redirect_uris": {adapter.redirectURL},
		"scopes":        {strings.Join(mastodonScopes, " ")},
	}
	request, _ := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		target.String(),
		bytes.NewBufferString(form.Encode()),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := adapter.http.do(ctx, request)
	if err != nil {
		return mastodonRuntimeApp{}, mastodonFailure("mastodon_app_registration", err)
	}
	body, readErr := mastodonReadLimited(response)
	if readErr != nil {
		return mastodonRuntimeApp{}, mastodonFailure("mastodon_app_registration", readErr)
	}
	if response.StatusCode != http.StatusOK ||
		!mastodonJSON(response.Header.Get("Content-Type")) {
		return mastodonRuntimeApp{}, mastodonStatusFailure(
			"mastodon_app_registration",
			response.StatusCode,
		)
	}
	var registered struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if json.Unmarshal(body, &registered) != nil ||
		strings.TrimSpace(registered.ClientID) == "" ||
		strings.TrimSpace(registered.ClientSecret) == "" {
		return mastodonRuntimeApp{}, mastodonFailure(
			"mastodon_app_registration_malformed",
			fmt.Errorf("invalid app registration response"),
		)
	}
	return mastodonRuntimeApp{
		Origin:       instance.Origin,
		ClientID:     registered.ClientID,
		ClientSecret: registered.ClientSecret,
	}, nil
}

type blueskyRuntimeDynamicAdapter struct {
	client *BlueskyOAuthClient
}

func newBlueskyRuntimeDynamicAdapter(
	values map[string]string,
	cipher CredentialCipher,
) (DynamicAdapter, error) {
	clientID := runtimeProviderValue(
		values,
		blueskyRuntimeClientIDKey,
		blueskyRuntimeClientIDEnv,
	)
	redirectURL := runtimeProviderValue(
		values,
		blueskyRuntimeRedirectURLKey,
		blueskyRuntimeRedirectURLEnv,
	)
	if strings.TrimSpace(clientID) == "" ||
		strings.TrimSpace(redirectURL) == "" ||
		cipher == nil {
		return nil, nil
	}
	client, err := NewBlueskyOAuthClient(BlueskyOAuthConfig{
		ClientID:    clientID,
		RedirectURL: redirectURL,
		Cipher:      cipher,
		HTTP:        newMastodonSafeHTTP(),
		PLCDirectoryOrigin: runtimeProviderValue(
			values,
			blueskyRuntimePLCDirectoryKey,
			blueskyRuntimePLCDirectoryEnv,
		),
	})
	if err != nil {
		return nil, err
	}
	return &blueskyRuntimeDynamicAdapter{client: client}, nil
}

func (adapter *blueskyRuntimeDynamicAdapter) DynamicConfig() DynamicOAuthConfig {
	return DynamicOAuthConfig{
		RedirectURL:      adapter.client.redirectURL,
		Scopes:           append([]string(nil), blueskyScopes...),
		RequiresPAR:      true,
		RequiresDPoP:     true,
		RequiresATH:      true,
		RequiresIssuer:   true,
		RequiresSubject:  true,
		RefreshTokenMode: RefreshTokenSingleUse,
		RevocationPolicy: RevocationBestEffort,
		NetworkPolicy: DynamicNetworkPolicy{
			RejectRedirects:   true,
			ValidateAndPinDNS: true,
			MaxResponseBytes:  maximumAuthenticatedBody,
		},
	}
}

func (adapter *blueskyRuntimeDynamicAdapter) BeginDynamic(
	ctx context.Context,
	request DynamicBeginRequest,
) (DynamicAuthorization, error) {
	pdsOrigin := request.Discovery.Value
	if request.PreviousBinding != (OAuthBinding{}) {
		pdsOrigin = request.PreviousBinding.ResourceServer
	}
	authorization, err := adapter.client.beginWithState(
		ctx,
		pdsOrigin,
		"",
		request.State,
		request.ExpiresAt,
	)
	if err != nil {
		return DynamicAuthorization{}, err
	}
	return DynamicAuthorization{
		URL:           authorization.URL,
		ProviderState: authorization.ProviderState,
		PARRequestURI: authorization.PARRequestURI,
		Binding:       authorization.Binding,
	}, nil
}

func (adapter *blueskyRuntimeDynamicAdapter) CompleteDynamic(
	ctx context.Context,
	request DynamicCallbackRequest,
) (DynamicCompletion, error) {
	session, err := adapter.client.completeBoundAttempt(
		ctx,
		request.ProviderState,
		request.Code,
		request.Issuer,
	)
	if err != nil {
		return DynamicCompletion{}, err
	}
	resource, err := adapter.client.DiscoverProfile(ctx, &session)
	if err != nil {
		return DynamicCompletion{}, err
	}
	providerState, err := json.Marshal(blueskySessionEnvelope{
		Credential: session.Credential,
		SubjectDID: session.SubjectDID,
		PDSOrigin:  session.PDSOrigin,
		Issuer:     session.Issuer,
		ASNonce:    session.ASNonce,
		RSNonce:    session.RSNonce,
		DPoPKey:    session.DPoPKey,
		TokenURL:   session.TokenURL,
	})
	if err != nil {
		return DynamicCompletion{}, err
	}
	return DynamicCompletion{
		Resources:     []DiscoveredResource{resource},
		ProviderState: providerState,
		Binding: OAuthBinding{
			Issuer:         session.Issuer,
			ResourceServer: session.PDSOrigin,
			Subject:        session.SubjectDID,
		},
	}, nil
}

func (adapter *blueskyRuntimeDynamicAdapter) RefreshDynamic(
	ctx context.Context,
	session DynamicSession,
) (DynamicRefreshResult, error) {
	blueskySession, err := openBlueskyRuntimeSession(
		session.ProviderState,
		session.Credential,
	)
	if err != nil {
		return DynamicRefreshResult{}, err
	}
	blueskySession, err = adapter.client.Refresh(ctx, blueskySession)
	if err != nil {
		return DynamicRefreshResult{}, err
	}
	providerState, err := marshalBlueskyRuntimeSession(blueskySession)
	if err != nil {
		return DynamicRefreshResult{}, err
	}
	return DynamicRefreshResult{
		Session: DynamicSession{
			Binding:       session.Binding,
			Credential:    blueskySession.Credential,
			ProviderState: providerState,
		},
	}, nil
}

func (adapter *blueskyRuntimeDynamicAdapter) DoAuthenticated(
	ctx context.Context,
	session DynamicSession,
	request AuthenticatedRequest,
) (DynamicAuthenticatedResult, error) {
	blueskySession, err := openBlueskyRuntimeSession(
		session.ProviderState,
		session.Credential,
	)
	if err != nil {
		return DynamicAuthenticatedResult{}, err
	}
	pds, err := mastodonOrigin(blueskySession.PDSOrigin)
	if err != nil {
		return DynamicAuthenticatedResult{}, err
	}
	target, err := pds.Parse(request.Path)
	if err != nil {
		return DynamicAuthenticatedResult{}, ErrInvalidArgument
	}
	response, body, nonce, err := blueskyRuntimeRequest(
		ctx,
		adapter.client,
		request,
		target,
		blueskySession.RSNonce,
		blueskySession.DPoPKey,
		blueskySession.Credential.AccessToken,
		"",
	)
	if err != nil {
		return DynamicAuthenticatedResult{}, err
	}
	blueskySession.RSNonce = nonce
	providerState, err := marshalBlueskyRuntimeSession(blueskySession)
	if err != nil {
		return DynamicAuthenticatedResult{}, err
	}
	return DynamicAuthenticatedResult{
		Response: AuthenticatedResponse{
			StatusCode: response.StatusCode,
			Header:     response.Header.Clone(),
			Body:       body,
		},
		Session: DynamicSession{
			Binding:       session.Binding,
			Credential:    blueskySession.Credential,
			ProviderState: providerState,
		},
	}, nil
}

func (adapter *blueskyRuntimeDynamicAdapter) RevokeDynamic(
	ctx context.Context,
	session DynamicSession,
) error {
	blueskySession, err := openBlueskyRuntimeSession(
		session.ProviderState,
		session.Credential,
	)
	if err != nil {
		return err
	}
	return adapter.client.Revoke(blueskySession)
}

func openMastodonRuntimeAttemptState(
	data []byte,
) (mastodonRuntimeAttemptState, error) {
	var state mastodonRuntimeAttemptState
	if len(data) == 0 ||
		json.Unmarshal(data, &state) != nil ||
		state.Version != 1 ||
		strings.TrimSpace(state.Instance.Origin) == "" ||
		strings.TrimSpace(state.App.Origin) == "" ||
		strings.TrimSpace(state.App.ClientID) == "" ||
		strings.TrimSpace(state.App.ClientSecret) == "" {
		return mastodonRuntimeAttemptState{}, ErrInvalidArgument
	}
	return state, nil
}

func openMastodonRuntimeState(data []byte) (mastodonRuntimeState, error) {
	var state mastodonRuntimeState
	if len(data) == 0 ||
		json.Unmarshal(data, &state) != nil ||
		strings.TrimSpace(state.Instance.Origin) == "" ||
		strings.TrimSpace(state.App.Origin) == "" ||
		strings.TrimSpace(state.App.ClientID) == "" ||
		strings.TrimSpace(state.App.ClientSecret) == "" {
		return mastodonRuntimeState{}, ErrInvalidArgument
	}
	return state, nil
}

func openBlueskyRuntimeSession(
	data []byte,
	credential Credential,
) (BlueskySession, error) {
	var envelope blueskySessionEnvelope
	if len(data) == 0 || json.Unmarshal(data, &envelope) != nil {
		return BlueskySession{}, ErrInvalidArgument
	}
	envelope.Credential = credential
	return BlueskySession{
		Credential: envelope.Credential,
		SubjectDID: envelope.SubjectDID,
		PDSOrigin:  envelope.PDSOrigin,
		Issuer:     envelope.Issuer,
		ASNonce:    envelope.ASNonce,
		RSNonce:    envelope.RSNonce,
		DPoPKey:    envelope.DPoPKey,
		TokenURL:   envelope.TokenURL,
	}, nil
}

func marshalBlueskyRuntimeSession(session BlueskySession) ([]byte, error) {
	return json.Marshal(blueskySessionEnvelope{
		SubjectDID: session.SubjectDID,
		PDSOrigin:  session.PDSOrigin,
		Issuer:     session.Issuer,
		ASNonce:    session.ASNonce,
		RSNonce:    session.RSNonce,
		DPoPKey:    session.DPoPKey,
		TokenURL:   session.TokenURL,
	})
}

func blueskyRuntimeRequest(
	ctx context.Context,
	client *BlueskyOAuthClient,
	request AuthenticatedRequest,
	target *url.URL,
	nonce string,
	key blueskyDPoPKey,
	accessToken string,
	proxy string,
) (*http.Response, []byte, string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		httpRequest, _ := http.NewRequestWithContext(
			ctx,
			request.Method,
			target.String(),
			bytes.NewReader(request.Body),
		)
		httpRequest.Header = request.Header.Clone()
		httpRequest.Header.Set("Accept", "application/json")
		proof, err := key.proof(
			request.Method,
			target,
			nonce,
			accessToken,
			client.now().UTC(),
		)
		if err != nil {
			return nil, nil, "", blueskyFailure("bluesky_dpop", err)
		}
		httpRequest.Header.Set("DPoP", proof)
		httpRequest.Header.Set("Authorization", "DPoP "+accessToken)
		if proxy != "" {
			httpRequest.Header.Set("atproto-proxy", proxy)
		}
		response, err := client.http.do(ctx, httpRequest)
		if err != nil {
			return nil, nil, "", blueskyFailure("bluesky_request", err)
		}
		body, readErr := mastodonReadLimited(response)
		if readErr != nil {
			return nil, nil, "", blueskyFailure("bluesky_request", readErr)
		}
		if response.StatusCode == http.StatusBadRequest &&
			strings.EqualFold(
				strings.TrimSpace(response.Header.Get("DPoP-Nonce")),
				"",
			) &&
			attempt == 0 {
			return nil, nil, "", blueskyFailure(
				"bluesky_request",
				fmt.Errorf("missing DPoP nonce"),
			)
		}
		rotatedNonce := strings.TrimSpace(response.Header.Get("DPoP-Nonce"))
		if response.StatusCode == http.StatusUnauthorized &&
			rotatedNonce != "" &&
			strings.Contains(string(body), "use_dpop_nonce") &&
			attempt == 0 {
			nonce = rotatedNonce
			continue
		}
		if response.StatusCode >= http.StatusBadRequest {
			return nil, nil, "", blueskyStatusFailure(
				"bluesky_request",
				response.StatusCode,
			)
		}
		if rotatedNonce == "" {
			return nil, nil, "", blueskyFailure(
				"bluesky_request",
				fmt.Errorf("missing DPoP nonce"),
			)
		}
		return response, body, rotatedNonce, nil
	}
	return nil, nil, "", blueskyFailure(
		"bluesky_request",
		fmt.Errorf("DPoP nonce retry exhausted"),
	)
}
