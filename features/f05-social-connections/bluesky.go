package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var blueskyScopes = []string{
	"atproto",
	"repo:app.bsky.feed.post?action=create",
	"blob:*/*",
	"rpc:app.bsky.actor.getProfile?aud=did:web:api.bsky.app#bsky_appview",
}

var errBlueskyRemoteRevocationUnsupported = errors.New(
	"AT Protocol does not publish a token revocation endpoint",
)

type BlueskyAuthorizationServer struct {
	PDSOrigin             string   `json:"pds_origin"`
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	PAREndpoint           string   `json:"par_endpoint"`
	Scopes                []string `json:"scopes"`
}

type BlueskyOAuthConfig struct {
	ClientID           string
	RedirectURL        string
	Cipher             CredentialCipher
	HTTP               *mastodonSafeHTTP
	PLCDirectoryOrigin string
	Now                func() time.Time
}

type BlueskyOAuthClient struct {
	clientID    string
	redirectURL string
	cipher      CredentialCipher
	http        *mastodonSafeHTTP
	plcOrigin   string
	now         func() time.Time
	mu          sync.Mutex
	attempts    map[string]blueskyStoredAttempt
}

type blueskyStoredAttempt struct {
	Ciphertext Ciphertext
	ExpiresAt  time.Time
}

type blueskyAttempt struct {
	State     string                     `json:"state"`
	Verifier  string                     `json:"verifier"`
	DPoPKey   blueskyDPoPKey             `json:"dpop_key"`
	ASNonce   string                     `json:"as_nonce"`
	Server    BlueskyAuthorizationServer `json:"server"`
	LoginHint string                     `json:"login_hint,omitempty"`
}

type BlueskyAuthorization struct {
	URL       string
	ExpiresAt time.Time
}

type BlueskySession struct {
	Credential Credential     `json:"-"`
	SubjectDID string         `json:"-"`
	PDSOrigin  string         `json:"-"`
	Issuer     string         `json:"-"`
	ASNonce    string         `json:"-"`
	RSNonce    string         `json:"-"`
	DPoPKey    blueskyDPoPKey `json:"-"`
	TokenURL   string         `json:"-"`
}

type blueskySessionEnvelope struct {
	Credential Credential     `json:"credential"`
	SubjectDID string         `json:"subject_did"`
	PDSOrigin  string         `json:"pds_origin"`
	Issuer     string         `json:"issuer"`
	ASNonce    string         `json:"as_nonce"`
	RSNonce    string         `json:"rs_nonce"`
	DPoPKey    blueskyDPoPKey `json:"dpop_key"`
	TokenURL   string         `json:"token_url"`
}

func NewBlueskyOAuthClient(
	config BlueskyOAuthConfig,
) (*BlueskyOAuthClient, error) {
	clientID, clientErr := url.Parse(config.ClientID)
	redirect, redirectErr := url.Parse(config.RedirectURL)
	if clientErr != nil ||
		clientID.Scheme != "https" ||
		clientID.Host == "" ||
		clientID.User != nil ||
		clientID.Fragment != "" ||
		redirectErr != nil ||
		redirect.Scheme != "https" ||
		redirect.Host == "" ||
		config.Cipher == nil {
		return nil, fmt.Errorf("%w: invalid Bluesky OAuth client configuration", ErrInvalidArgument)
	}
	if config.HTTP == nil {
		config.HTTP = newMastodonSafeHTTP()
	}
	if config.PLCDirectoryOrigin == "" {
		config.PLCDirectoryOrigin = "https://plc.directory"
	}
	if _, err := mastodonOrigin(config.PLCDirectoryOrigin); err != nil {
		return nil, fmt.Errorf("%w: invalid PLC directory origin", ErrInvalidArgument)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &BlueskyOAuthClient{
		clientID:    config.ClientID,
		redirectURL: config.RedirectURL,
		cipher:      config.Cipher,
		http:        config.HTTP,
		plcOrigin:   config.PLCDirectoryOrigin,
		now:         config.Now,
		attempts:    make(map[string]blueskyStoredAttempt),
	}, nil
}

func (client *BlueskyOAuthClient) DiscoverAuthorizationServer(
	ctx context.Context,
	pdsOrigin string,
) (BlueskyAuthorizationServer, error) {
	pds, err := mastodonOrigin(pdsOrigin)
	if err != nil {
		return BlueskyAuthorizationServer{}, err
	}
	resourceURL := mastodonEndpoint(pds, "/.well-known/oauth-protected-resource")
	var resource struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}
	if err = client.getMetadata(ctx, resourceURL, &resource); err != nil {
		return BlueskyAuthorizationServer{}, err
	}
	if len(resource.AuthorizationServers) != 1 {
		return BlueskyAuthorizationServer{}, blueskyFailure(
			"bluesky_protected_resource_malformed",
			errors.New("exactly one authorization server is required"),
		)
	}
	issuer, err := mastodonOrigin(resource.AuthorizationServers[0])
	if err != nil {
		return BlueskyAuthorizationServer{}, blueskyFailure(
			"bluesky_protected_resource_malformed",
			err,
		)
	}
	metadataURL := mastodonEndpoint(issuer, "/.well-known/oauth-authorization-server")
	var metadata struct {
		Issuer                     string   `json:"issuer"`
		AuthorizationEndpoint      string   `json:"authorization_endpoint"`
		TokenEndpoint              string   `json:"token_endpoint"`
		PAREndpoint                string   `json:"pushed_authorization_request_endpoint"`
		ResponseTypes              []string `json:"response_types_supported"`
		GrantTypes                 []string `json:"grant_types_supported"`
		CodeChallengeMethods       []string `json:"code_challenge_methods_supported"`
		TokenAuthMethods           []string `json:"token_endpoint_auth_methods_supported"`
		TokenAuthSigningAlgorithms []string `json:"token_endpoint_auth_signing_alg_values_supported"`
		Scopes                     []string `json:"scopes_supported"`
		DPoPAlgorithms             []string `json:"dpop_signing_alg_values_supported"`
		ResponseIssuer             bool     `json:"authorization_response_iss_parameter_supported"`
		RequirePAR                 bool     `json:"require_pushed_authorization_requests"`
		ClientMetadata             bool     `json:"client_id_metadata_document_supported"`
		RequireRequestRegistration *bool    `json:"require_request_uri_registration"`
	}
	if err = client.getMetadata(ctx, metadataURL, &metadata); err != nil {
		return BlueskyAuthorizationServer{}, err
	}
	valid := metadata.Issuer == issuer.String() &&
		mastodonSameOriginEndpoint(issuer, metadata.AuthorizationEndpoint) &&
		mastodonSameOriginEndpoint(issuer, metadata.TokenEndpoint) &&
		mastodonSameOriginEndpoint(issuer, metadata.PAREndpoint) &&
		mastodonContains(metadata.ResponseTypes, "code") &&
		mastodonContains(metadata.GrantTypes, "authorization_code") &&
		mastodonContains(metadata.GrantTypes, "refresh_token") &&
		mastodonContains(metadata.CodeChallengeMethods, "S256") &&
		mastodonContains(metadata.TokenAuthMethods, "none") &&
		mastodonContains(metadata.TokenAuthMethods, "private_key_jwt") &&
		mastodonContains(metadata.TokenAuthSigningAlgorithms, "ES256") &&
		mastodonContains(metadata.Scopes, "atproto") &&
		mastodonContains(metadata.DPoPAlgorithms, "ES256") &&
		metadata.ResponseIssuer &&
		metadata.RequirePAR &&
		metadata.ClientMetadata &&
		(metadata.RequireRequestRegistration == nil ||
			*metadata.RequireRequestRegistration)
	if !valid {
		return BlueskyAuthorizationServer{}, blueskyFailure(
			"bluesky_authorization_metadata_incompatible",
			errors.New("authorization server does not satisfy AT Protocol OAuth"),
		)
	}
	return BlueskyAuthorizationServer{
		PDSOrigin:             pds.String(),
		Issuer:                issuer.String(),
		AuthorizationEndpoint: metadata.AuthorizationEndpoint,
		TokenEndpoint:         metadata.TokenEndpoint,
		PAREndpoint:           metadata.PAREndpoint,
		Scopes:                append([]string(nil), blueskyScopes...),
	}, nil
}

func (client *BlueskyOAuthClient) Begin(
	ctx context.Context,
	pdsOrigin, loginHint string,
) (BlueskyAuthorization, error) {
	server, err := client.DiscoverAuthorizationServer(ctx, pdsOrigin)
	if err != nil {
		return BlueskyAuthorization{}, err
	}
	state, err := randomOpaqueID(32)
	if err != nil {
		return BlueskyAuthorization{}, err
	}
	verifier, err := randomOpaqueID(64)
	if err != nil {
		return BlueskyAuthorization{}, err
	}
	key, err := newBlueskyDPoPKey()
	if err != nil {
		return BlueskyAuthorization{}, err
	}
	attempt := blueskyAttempt{
		State:     state,
		Verifier:  verifier,
		DPoPKey:   key,
		Server:    server,
		LoginHint: strings.TrimSpace(loginHint),
	}
	form := url.Values{
		"client_id":             {client.clientID},
		"response_type":         {"code"},
		"redirect_uri":          {client.redirectURL},
		"state":                 {state},
		"scope":                 {strings.Join(blueskyScopes, " ")},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	if attempt.LoginHint != "" {
		form.Set("login_hint", attempt.LoginHint)
	}
	target, _ := url.Parse(server.PAREndpoint)
	response, body, nonce, err := client.dpopRequest(
		ctx,
		http.MethodPost,
		target,
		form,
		"",
		key,
		"",
		"",
	)
	if err != nil {
		return BlueskyAuthorization{}, err
	}
	if response.StatusCode != http.StatusCreated {
		return BlueskyAuthorization{}, blueskyStatusFailure("bluesky_par", response.StatusCode)
	}
	var pushed struct {
		RequestURI string `json:"request_uri"`
		ExpiresIn  int64  `json:"expires_in"`
	}
	if json.Unmarshal(body, &pushed) != nil ||
		pushed.RequestURI == "" ||
		pushed.ExpiresIn <= 0 ||
		pushed.ExpiresIn > 600 {
		return BlueskyAuthorization{}, blueskyFailure(
			"bluesky_par_malformed",
			errors.New("invalid PAR response"),
		)
	}
	attempt.ASNonce = nonce
	plaintext, _ := json.Marshal(attempt)
	ciphertext, err := client.cipher.Seal(
		plaintext,
		[]byte("f05|bluesky-attempt|"+digest(state)),
	)
	if err != nil {
		return BlueskyAuthorization{}, fmt.Errorf("seal Bluesky OAuth attempt: %w", err)
	}
	now := client.now().UTC()
	expiresAt := now.Add(time.Duration(pushed.ExpiresIn) * time.Second)
	client.mu.Lock()
	client.attempts[digest(state)] = blueskyStoredAttempt{
		Ciphertext: ciphertext,
		ExpiresAt:  expiresAt,
	}
	client.mu.Unlock()
	authorizationURL, _ := url.Parse(server.AuthorizationEndpoint)
	query := authorizationURL.Query()
	query.Set("client_id", client.clientID)
	query.Set("request_uri", pushed.RequestURI)
	authorizationURL.RawQuery = query.Encode()
	return BlueskyAuthorization{URL: authorizationURL.String(), ExpiresAt: expiresAt}, nil
}

func (client *BlueskyOAuthClient) Callback(
	ctx context.Context,
	state, code, issuer string,
) (BlueskySession, error) {
	if state == "" || code == "" || issuer == "" {
		return BlueskySession{}, ErrInvalidState
	}
	key := digest(state)
	client.mu.Lock()
	stored, found := client.attempts[key]
	delete(client.attempts, key)
	client.mu.Unlock()
	if !found || !client.now().UTC().Before(stored.ExpiresAt) {
		return BlueskySession{}, ErrInvalidState
	}
	plaintext, err := client.cipher.Open(
		stored.Ciphertext,
		[]byte("f05|bluesky-attempt|"+key),
	)
	if err != nil {
		return BlueskySession{}, fmt.Errorf("open Bluesky OAuth attempt: %w", err)
	}
	var attempt blueskyAttempt
	if json.Unmarshal(plaintext, &attempt) != nil ||
		attempt.State != state ||
		attempt.Server.Issuer != issuer {
		return BlueskySession{}, ErrInvalidState
	}
	target, _ := url.Parse(attempt.Server.TokenEndpoint)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {client.clientID},
		"redirect_uri":  {client.redirectURL},
		"code_verifier": {attempt.Verifier},
	}
	response, body, nonce, err := client.dpopRequest(
		ctx,
		http.MethodPost,
		target,
		form,
		attempt.ASNonce,
		attempt.DPoPKey,
		"",
		"",
	)
	if err != nil {
		return BlueskySession{}, err
	}
	if response.StatusCode != http.StatusOK {
		return BlueskySession{}, blueskyStatusFailure("bluesky_token", response.StatusCode)
	}
	credential, subject, err := blueskyToken(body)
	if err != nil {
		return BlueskySession{}, err
	}
	pds, err := client.resolvePDS(ctx, subject)
	if err != nil || pds != attempt.Server.PDSOrigin {
		return BlueskySession{}, blueskyFailure(
			"bluesky_subject_pds_mismatch",
			errors.New("token subject is not authoritative for the discovered PDS"),
		)
	}
	verified, err := client.DiscoverAuthorizationServer(ctx, pds)
	if err != nil || verified.Issuer != attempt.Server.Issuer {
		return BlueskySession{}, blueskyFailure(
			"bluesky_subject_issuer_mismatch",
			errors.New("token subject does not resolve to the callback issuer"),
		)
	}
	return BlueskySession{
		Credential: credential,
		SubjectDID: subject,
		PDSOrigin:  pds,
		Issuer:     attempt.Server.Issuer,
		ASNonce:    nonce,
		DPoPKey:    attempt.DPoPKey,
		TokenURL:   attempt.Server.TokenEndpoint,
	}, nil
}

func (client *BlueskyOAuthClient) DiscoverProfile(
	ctx context.Context,
	session *BlueskySession,
) (DiscoveredResource, error) {
	pds, _ := mastodonOrigin(session.PDSOrigin)
	target := mastodonEndpoint(pds, "/xrpc/app.bsky.actor.getProfile")
	query := target.Query()
	query.Set("actor", session.SubjectDID)
	target.RawQuery = query.Encode()
	response, body, nonce, err := client.dpopRequest(
		ctx,
		http.MethodGet,
		target,
		nil,
		session.RSNonce,
		session.DPoPKey,
		session.Credential.AccessToken,
		"did:web:api.bsky.app#bsky_appview",
	)
	if err != nil {
		return DiscoveredResource{}, err
	}
	if response.StatusCode != http.StatusOK {
		return DiscoveredResource{}, blueskyStatusFailure("bluesky_profile", response.StatusCode)
	}
	var profile struct {
		DID         string `json:"did"`
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
		Avatar      string `json:"avatar"`
	}
	if json.Unmarshal(body, &profile) != nil ||
		profile.DID != session.SubjectDID ||
		profile.Handle == "" {
		return DiscoveredResource{}, blueskyFailure(
			"bluesky_profile_malformed",
			errors.New("invalid profile response"),
		)
	}
	session.RSNonce = nonce
	displayName := strings.TrimSpace(profile.DisplayName)
	if displayName == "" {
		displayName = profile.Handle
	}
	if profile.Avatar != "" {
		avatar, parseErr := url.Parse(profile.Avatar)
		if parseErr != nil || avatar.Scheme != "https" || avatar.Host == "" {
			profile.Avatar = ""
		}
	}
	return DiscoveredResource{
		Candidate: Candidate{
			RemoteID:     profile.DID,
			ResourceType: ResourceBlueskyAccount,
			AccountType:  AccountTypeProfile,
			DisplayName:  displayName,
			Handle:       "@" + profile.Handle,
			PictureURL:   profile.Avatar,
			Scopes:       append([]string(nil), session.Credential.Scopes...),
		},
		Credential: session.Credential,
	}, nil
}

func (client *BlueskyOAuthClient) Refresh(
	ctx context.Context,
	session BlueskySession,
) (BlueskySession, error) {
	if session.Credential.RefreshToken == "" {
		return BlueskySession{}, ErrNotRefreshable
	}
	target, err := url.Parse(session.TokenURL)
	if err != nil {
		return BlueskySession{}, blueskyFailure("bluesky_refresh", err)
	}
	response, body, nonce, err := client.dpopRequest(
		ctx,
		http.MethodPost,
		target,
		url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {session.Credential.RefreshToken},
			"client_id":     {client.clientID},
		},
		session.ASNonce,
		session.DPoPKey,
		"",
		"",
	)
	if err != nil {
		return BlueskySession{}, err
	}
	if response.StatusCode != http.StatusOK {
		return BlueskySession{}, blueskyStatusFailure("bluesky_refresh", response.StatusCode)
	}
	credential, subject, err := blueskyToken(body)
	if err != nil || subject != session.SubjectDID {
		return BlueskySession{}, blueskyFailure(
			"bluesky_refresh_malformed",
			errors.New("refresh changed or omitted token subject"),
		)
	}
	session.Credential = credential
	session.ASNonce = nonce
	return session, nil
}

func (client *BlueskyOAuthClient) Revoke(BlueskySession) error {
	// The authoritative AT Protocol OAuth metadata does not define a
	// revocation endpoint. Returning an error prevents callers from claiming
	// remote revocation or silently treating local deletion as remote success.
	return errBlueskyRemoteRevocationUnsupported
}

func (client *BlueskyOAuthClient) SealSession(
	sessionID string,
	session BlueskySession,
) (Ciphertext, error) {
	if strings.TrimSpace(sessionID) == "" ||
		session.Credential.AccessToken == "" ||
		session.Credential.RefreshToken == "" {
		return Ciphertext{}, fmt.Errorf("%w: incomplete Bluesky session", ErrInvalidArgument)
	}
	plaintext, err := json.Marshal(blueskySessionEnvelope{
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
		return Ciphertext{}, err
	}
	return client.cipher.Seal(
		plaintext,
		[]byte("f05|bluesky-session|"+sessionID),
	)
}

func (client *BlueskyOAuthClient) OpenSession(
	sessionID string,
	ciphertext Ciphertext,
) (BlueskySession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return BlueskySession{}, fmt.Errorf("%w: Bluesky session ID is required", ErrInvalidArgument)
	}
	plaintext, err := client.cipher.Open(
		ciphertext,
		[]byte("f05|bluesky-session|"+sessionID),
	)
	if err != nil {
		return BlueskySession{}, err
	}
	var envelope blueskySessionEnvelope
	if json.Unmarshal(plaintext, &envelope) != nil ||
		envelope.Credential.AccessToken == "" ||
		envelope.Credential.RefreshToken == "" {
		return BlueskySession{}, blueskyFailure(
			"bluesky_session_malformed",
			errors.New("invalid encrypted session"),
		)
	}
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

func (client *BlueskyOAuthClient) getMetadata(
	ctx context.Context,
	target *url.URL,
	output any,
) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	request.Header.Set("Accept", "application/json")
	response, err := client.http.do(ctx, request)
	if err != nil {
		return blueskyFailure("bluesky_metadata", err)
	}
	body, readErr := mastodonReadLimited(response)
	if readErr != nil {
		return blueskyFailure("bluesky_metadata", readErr)
	}
	if response.StatusCode != http.StatusOK ||
		!mastodonJSON(response.Header.Get("Content-Type")) ||
		json.Unmarshal(body, output) != nil {
		return blueskyStatusFailure("bluesky_metadata", response.StatusCode)
	}
	return nil
}

func (client *BlueskyOAuthClient) dpopRequest(
	ctx context.Context,
	method string,
	target *url.URL,
	form url.Values,
	nonce string,
	key blueskyDPoPKey,
	accessToken string,
	proxy string,
) (*http.Response, []byte, string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		var body *bytes.Reader
		if form != nil {
			body = bytes.NewReader([]byte(form.Encode()))
		} else {
			body = bytes.NewReader(nil)
		}
		request, _ := http.NewRequestWithContext(ctx, method, target.String(), body)
		if form != nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		request.Header.Set("Accept", "application/json")
		proof, err := key.proof(method, target, nonce, accessToken, client.now().UTC())
		if err != nil {
			return nil, nil, "", blueskyFailure("bluesky_dpop", err)
		}
		request.Header.Set("DPoP", proof)
		if accessToken != "" {
			request.Header.Set("Authorization", "DPoP "+accessToken)
		}
		if proxy != "" {
			request.Header.Set("Atproto-Proxy", proxy)
		}
		response, err := client.http.do(ctx, request)
		if err != nil {
			return nil, nil, "", blueskyFailure("bluesky_request", err)
		}
		responseBody, readErr := mastodonReadLimited(response)
		if readErr != nil {
			return nil, nil, "", blueskyFailure("bluesky_response", readErr)
		}
		rotated := strings.TrimSpace(response.Header.Get("DPoP-Nonce"))
		if rotated == "" {
			return nil, nil, "", blueskyFailure(
				"bluesky_dpop_nonce_missing",
				errors.New("DPoP response omitted mandatory nonce"),
			)
		}
		if attempt == 0 && blueskyDPoPError(response, responseBody) {
			nonce = rotated
			continue
		}
		return response, responseBody, rotated, nil
	}
	return nil, nil, "", blueskyFailure(
		"bluesky_dpop_nonce_rejected",
		errors.New("rotated DPoP nonce was rejected"),
	)
}

func (client *BlueskyOAuthClient) resolvePDS(
	ctx context.Context,
	did string,
) (string, error) {
	target, err := client.didDocumentURL(did)
	if err != nil {
		return "", err
	}
	var document struct {
		ID      string   `json:"id"`
		Aliases []string `json:"alsoKnownAs"`
		Service []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Endpoint string `json:"serviceEndpoint"`
		} `json:"service"`
	}
	if err = client.getMetadata(ctx, target, &document); err != nil {
		return "", err
	}
	if document.ID != did {
		return "", blueskyFailure("bluesky_did_mismatch", errors.New("DID document ID mismatch"))
	}
	for _, service := range document.Service {
		if (service.ID == "#atproto_pds" ||
			service.ID == did+"#atproto_pds") &&
			service.Type == "AtprotoPersonalDataServer" {
			origin, originErr := mastodonOrigin(service.Endpoint)
			if originErr != nil {
				return "", blueskyFailure("bluesky_pds_unsafe", originErr)
			}
			return origin.String(), nil
		}
	}
	return "", blueskyFailure("bluesky_pds_missing", errors.New("DID has no PDS service"))
}

func (client *BlueskyOAuthClient) didDocumentURL(did string) (*url.URL, error) {
	switch {
	case strings.HasPrefix(did, "did:plc:"):
		origin, _ := mastodonOrigin(client.plcOrigin)
		target := mastodonEndpoint(origin, "/"+url.PathEscape(did))
		return target, nil
	case strings.HasPrefix(did, "did:web:"):
		parts := strings.Split(strings.TrimPrefix(did, "did:web:"), ":")
		if len(parts) == 0 {
			break
		}
		host, err := url.PathUnescape(parts[0])
		if err != nil || host == "" {
			break
		}
		path := "/.well-known/did.json"
		if len(parts) > 1 {
			decoded := make([]string, 0, len(parts)-1)
			for _, part := range parts[1:] {
				value, decodeErr := url.PathUnescape(part)
				if decodeErr != nil || value == "" || strings.Contains(value, "/") {
					return nil, blueskyFailure("bluesky_did_invalid", errors.New("invalid did:web"))
				}
				decoded = append(decoded, url.PathEscape(value))
			}
			path = "/" + strings.Join(decoded, "/") + "/did.json"
		}
		return url.Parse("https://" + host + path)
	}
	return nil, blueskyFailure(
		"bluesky_did_unsupported",
		errors.New("only did:plc and did:web are supported"),
	)
}

func blueskyToken(body []byte) (Credential, string, error) {
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		Subject      string `json:"sub"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.Unmarshal(body, &token) != nil ||
		token.AccessToken == "" ||
		token.RefreshToken == "" ||
		!strings.EqualFold(token.TokenType, "DPoP") ||
		token.Subject == "" {
		return Credential{}, "", blueskyFailure(
			"bluesky_token_malformed",
			errors.New("invalid token response"),
		)
	}
	scopes := strings.Fields(token.Scope)
	if validateScopes(blueskyScopes, scopes) != nil {
		return Credential{}, "", &ProviderFailure{
			Kind: FailurePermissionMissing,
			Code: "bluesky_scope_missing",
		}
	}
	var expiresAt *time.Time
	if token.ExpiresIn > 0 {
		value := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
		expiresAt = &value
	}
	return Credential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    expiresAt,
		Scopes:       scopes,
	}, token.Subject, nil
}

func blueskyFailure(code string, cause error) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  code,
		Cause: cause,
	}
}

func blueskyStatusFailure(code string, status int) error {
	return mastodonStatusFailure(code, status)
}
