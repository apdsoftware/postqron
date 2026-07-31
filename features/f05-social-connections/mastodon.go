package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var mastodonScopes = []string{
	"read:accounts",
	"write:media",
	"write:statuses",
}

type MastodonInstance struct {
	Origin             string
	Domain             string
	Version            string
	APIVersion         int
	SupportsPKCE       bool
	SupportsRefresh    bool
	AuthorizationURL   string
	TokenURL           string
	RevocationURL      string
	AppRegistrationURL string
}

type MastodonDiscovery struct {
	http *mastodonSafeHTTP
}

func NewMastodonDiscovery() *MastodonDiscovery {
	return &MastodonDiscovery{http: newMastodonSafeHTTP()}
}

func (discovery *MastodonDiscovery) Discover(
	ctx context.Context,
	rawOrigin string,
) (MastodonInstance, error) {
	origin, err := mastodonOrigin(rawOrigin)
	if err != nil {
		return MastodonInstance{}, err
	}
	instanceURL := mastodonEndpoint(origin, "/api/v2/instance")
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, instanceURL.String(), nil)
	request.Header.Set("Accept", "application/json")
	response, err := discovery.http.do(ctx, request)
	if err != nil {
		return MastodonInstance{}, mastodonFailure("mastodon_instance_discovery", err)
	}
	body, readErr := mastodonReadLimited(response)
	if readErr != nil {
		return MastodonInstance{}, mastodonFailure("mastodon_instance_discovery", readErr)
	}
	if response.StatusCode != http.StatusOK {
		return MastodonInstance{}, mastodonStatusFailure(
			"mastodon_instance_discovery",
			response.StatusCode,
		)
	}
	if !mastodonJSON(response.Header.Get("Content-Type")) {
		return MastodonInstance{}, mastodonFailure(
			"mastodon_instance_content_type",
			errors.New("expected application/json"),
		)
	}
	var document struct {
		Domain      string         `json:"domain"`
		Version     string         `json:"version"`
		APIVersions map[string]int `json:"api_versions"`
	}
	if json.Unmarshal(body, &document) != nil ||
		strings.TrimSpace(document.Domain) == "" ||
		strings.TrimSpace(document.Version) == "" {
		return MastodonInstance{}, mastodonFailure(
			"mastodon_instance_malformed",
			errors.New("required instance fields are missing"),
		)
	}
	major, minor, validVersion := mastodonVersion(document.Version)
	if !validVersion || major < 4 {
		return MastodonInstance{}, mastodonFailure(
			"mastodon_instance_incompatible",
			errors.New("Mastodon 4.0 or newer is required"),
		)
	}
	metadata, metadataFound, metadataErr := discovery.oauthMetadata(ctx, origin)
	if metadataErr != nil {
		return MastodonInstance{}, metadataErr
	}
	supportsPKCE := major > 4 || major == 4 && minor >= 3
	if !metadataFound {
		if supportsPKCE {
			return MastodonInstance{}, mastodonFailure(
				"mastodon_oauth_metadata_required",
				errors.New("Mastodon 4.3 or newer must publish OAuth metadata"),
			)
		}
		metadata = mastodonFallbackMetadata(origin)
	}
	return MastodonInstance{
		Origin:             origin.String(),
		Domain:             document.Domain,
		Version:            document.Version,
		APIVersion:         document.APIVersions["mastodon"],
		SupportsPKCE:       supportsPKCE,
		SupportsRefresh:    metadata.SupportsRefresh,
		AuthorizationURL:   metadata.AuthorizationURL,
		TokenURL:           metadata.TokenURL,
		RevocationURL:      metadata.RevocationURL,
		AppRegistrationURL: metadata.AppRegistrationURL,
	}, nil
}

type mastodonOAuthMetadata struct {
	AuthorizationURL   string
	TokenURL           string
	RevocationURL      string
	AppRegistrationURL string
	SupportsRefresh    bool
}

func (discovery *MastodonDiscovery) oauthMetadata(
	ctx context.Context,
	origin *url.URL,
) (mastodonOAuthMetadata, bool, error) {
	target := mastodonEndpoint(origin, "/.well-known/oauth-authorization-server")
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	request.Header.Set("Accept", "application/json")
	response, err := discovery.http.do(ctx, request)
	if err != nil {
		return mastodonOAuthMetadata{}, false, mastodonFailure("mastodon_oauth_metadata", err)
	}
	body, readErr := mastodonReadLimited(response)
	if readErr != nil {
		return mastodonOAuthMetadata{}, false, mastodonFailure("mastodon_oauth_metadata", readErr)
	}
	if response.StatusCode == http.StatusNotFound {
		return mastodonOAuthMetadata{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return mastodonOAuthMetadata{}, false, mastodonStatusFailure(
			"mastodon_oauth_metadata",
			response.StatusCode,
		)
	}
	if !mastodonJSON(response.Header.Get("Content-Type")) {
		return mastodonOAuthMetadata{}, false, nil
	}
	var document struct {
		Issuer                  string   `json:"issuer"`
		AuthorizationEndpoint   string   `json:"authorization_endpoint"`
		TokenEndpoint           string   `json:"token_endpoint"`
		RevocationEndpoint      string   `json:"revocation_endpoint"`
		AppRegistrationEndpoint string   `json:"app_registration_endpoint"`
		GrantTypes              []string `json:"grant_types_supported"`
	}
	if json.Unmarshal(body, &document) != nil ||
		!mastodonIssuerMatches(origin, document.Issuer) ||
		!mastodonSameOriginEndpoint(origin, document.AuthorizationEndpoint) ||
		!mastodonSameOriginEndpoint(origin, document.TokenEndpoint) {
		return mastodonOAuthMetadata{}, true, mastodonFailure(
			"mastodon_oauth_metadata_malformed",
			errors.New("OAuth metadata does not match instance origin"),
		)
	}
	revocation := document.RevocationEndpoint
	if revocation == "" {
		revocation = mastodonEndpoint(origin, "/oauth/revoke").String()
	}
	if !mastodonSameOriginEndpoint(origin, revocation) {
		return mastodonOAuthMetadata{}, true, mastodonFailure(
			"mastodon_oauth_metadata_malformed",
			errors.New("revocation endpoint is unsafe"),
		)
	}
	appRegistration := document.AppRegistrationEndpoint
	if appRegistration == "" {
		appRegistration = mastodonEndpoint(origin, "/api/v1/apps").String()
	}
	if !mastodonSameOriginEndpoint(origin, appRegistration) {
		return mastodonOAuthMetadata{}, true, mastodonFailure(
			"mastodon_oauth_metadata_malformed",
			errors.New("application registration endpoint is unsafe"),
		)
	}
	return mastodonOAuthMetadata{
		AuthorizationURL:   document.AuthorizationEndpoint,
		TokenURL:           document.TokenEndpoint,
		RevocationURL:      revocation,
		AppRegistrationURL: appRegistration,
		SupportsRefresh:    mastodonContains(document.GrantTypes, "refresh_token"),
	}, true, nil
}

func mastodonFallbackMetadata(origin *url.URL) mastodonOAuthMetadata {
	return mastodonOAuthMetadata{
		AuthorizationURL:   mastodonEndpoint(origin, "/oauth/authorize").String(),
		TokenURL:           mastodonEndpoint(origin, "/oauth/token").String(),
		RevocationURL:      mastodonEndpoint(origin, "/oauth/revoke").String(),
		AppRegistrationURL: mastodonEndpoint(origin, "/api/v1/apps").String(),
		SupportsRefresh:    false,
	}
}

type MastodonAdapterConfig struct {
	Instance     MastodonInstance
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTP         *mastodonSafeHTTP
}

type MastodonAdapter struct {
	instance     MastodonInstance
	clientID     string
	clientSecret string
	redirectURL  string
	http         *mastodonSafeHTTP
}

func NewMastodonAdapter(config MastodonAdapterConfig) (*MastodonAdapter, error) {
	origin, err := mastodonOrigin(config.Instance.Origin)
	if err != nil ||
		strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf("%w: complete Mastodon credentials are required", ErrInvalidArgument)
	}
	redirect, redirectErr := url.Parse(config.RedirectURL)
	if redirectErr != nil || redirect.Scheme != "https" || redirect.Host == "" {
		return nil, fmt.Errorf("%w: Mastodon redirect must use HTTPS", ErrInvalidArgument)
	}
	for _, endpoint := range []string{
		config.Instance.AuthorizationURL,
		config.Instance.TokenURL,
		config.Instance.RevocationURL,
	} {
		if !mastodonSameOriginEndpoint(origin, endpoint) {
			return nil, fmt.Errorf("%w: Mastodon endpoints must match the instance", ErrInvalidArgument)
		}
	}
	if config.HTTP == nil {
		config.HTTP = newMastodonSafeHTTP()
	}
	return &MastodonAdapter{
		instance:     config.Instance,
		clientID:     config.ClientID,
		clientSecret: config.ClientSecret,
		redirectURL:  config.RedirectURL,
		http:         config.HTTP,
	}, nil
}

func (adapter *MastodonAdapter) Config() OAuthConfig {
	return OAuthConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.instance.AuthorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), mastodonScopes...),
		ScopeSeparator:   OAuthScopeSeparatorSpace,
		SupportsPKCE:     adapter.instance.SupportsPKCE,
		ExtraParameters:  map[string]string{"force_login": "true"},
	}
}

func (adapter *MastodonAdapter) AdapterCapabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		PKCE:              adapter.instance.SupportsPKCE,
		ResourceSelection: true,
		TokenRefresh:      adapter.instance.SupportsRefresh,
		RemoteRevocation:  true,
	}
}

func (adapter *MastodonAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if request.RedirectURL != adapter.redirectURL ||
		strings.TrimSpace(request.Code) == "" ||
		(adapter.instance.SupportsPKCE && strings.TrimSpace(request.PKCEVerifier) == "") {
		return Credential{}, fmt.Errorf("%w: invalid Mastodon token exchange", ErrInvalidArgument)
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {request.Code},
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"redirect_uri":  {request.RedirectURL},
	}
	if adapter.instance.SupportsPKCE {
		form.Set("code_verifier", request.PKCEVerifier)
	}
	return adapter.token(ctx, form)
}

func (adapter *MastodonAdapter) Discover(
	ctx context.Context,
	credential Credential,
) ([]DiscoveredResource, error) {
	account, err := adapter.profile(ctx, credential)
	if err != nil {
		return nil, err
	}
	displayName := strings.TrimSpace(account.DisplayName)
	if displayName == "" {
		displayName = account.Username
	}
	return []DiscoveredResource{{
		Candidate: Candidate{
			RemoteID:     adapter.remoteID(account.ID),
			ResourceType: ResourceMastodonAccount,
			AccountType:  AccountTypeProfile,
			DisplayName:  displayName,
			Handle:       "@" + account.Username + "@" + adapter.instance.Domain,
			PictureURL:   account.Avatar,
			Scopes:       append([]string(nil), credential.Scopes...),
		},
		Credential: credential,
	}}, nil
}

func (adapter *MastodonAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if !adapter.instance.SupportsRefresh ||
		strings.TrimSpace(credential.RefreshToken) == "" {
		return Credential{}, ErrNotRefreshable
	}
	return adapter.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
	})
}

func (adapter *MastodonAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	account, err := adapter.profile(ctx, credential)
	if err != nil {
		return err
	}
	if adapter.remoteID(account.ID) != remoteID {
		return &ProviderFailure{
			Kind: FailureResourceGone,
			Code: "mastodon_resource_gone",
		}
	}
	return nil
}

func (adapter *MastodonAdapter) Revoke(
	ctx context.Context,
	_ string,
	credential Credential,
) error {
	form := url.Values{
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"token":         {credential.AccessToken},
	}
	request, _ := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		adapter.instance.RevocationURL,
		strings.NewReader(form.Encode()),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := adapter.http.do(ctx, request)
	if err != nil {
		return mastodonFailure("mastodon_revoke", err)
	}
	_, readErr := mastodonReadLimited(response)
	if readErr != nil {
		return mastodonFailure("mastodon_revoke", readErr)
	}
	if response.StatusCode != http.StatusOK {
		return mastodonStatusFailure("mastodon_revoke", response.StatusCode)
	}
	return nil
}

type mastodonAccount struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	URL         string `json:"url"`
	Avatar      string `json:"avatar"`
}

func (adapter *MastodonAdapter) profile(
	ctx context.Context,
	credential Credential,
) (mastodonAccount, error) {
	origin, _ := mastodonOrigin(adapter.instance.Origin)
	target := mastodonEndpoint(origin, "/api/v1/accounts/verify_credentials")
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	request.Header.Set("Authorization", "Bearer "+credential.AccessToken)
	request.Header.Set("Accept", "application/json")
	response, err := adapter.http.do(ctx, request)
	if err != nil {
		return mastodonAccount{}, mastodonFailure("mastodon_profile", err)
	}
	body, readErr := mastodonReadLimited(response)
	if readErr != nil {
		return mastodonAccount{}, mastodonFailure("mastodon_profile", readErr)
	}
	if response.StatusCode != http.StatusOK {
		return mastodonAccount{}, mastodonStatusFailure("mastodon_profile", response.StatusCode)
	}
	var account mastodonAccount
	if !mastodonJSON(response.Header.Get("Content-Type")) ||
		json.Unmarshal(body, &account) != nil ||
		account.ID == "" ||
		account.Username == "" ||
		account.URL == "" {
		return mastodonAccount{}, mastodonFailure(
			"mastodon_profile_malformed",
			errors.New("invalid account response"),
		)
	}
	if account.Avatar != "" {
		avatar, parseErr := url.Parse(account.Avatar)
		if parseErr != nil || avatar.Scheme != "https" || avatar.Host == "" {
			account.Avatar = ""
		}
	}
	return account, nil
}

func (adapter *MastodonAdapter) remoteID(accountID string) string {
	origin, _ := mastodonOrigin(adapter.instance.Origin)
	return mastodonEndpoint(
		origin,
		"/api/v1/accounts/"+url.PathEscape(accountID),
	).String()
}

func (adapter *MastodonAdapter) token(
	ctx context.Context,
	form url.Values,
) (Credential, error) {
	request, _ := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		adapter.instance.TokenURL,
		bytes.NewBufferString(form.Encode()),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := adapter.http.do(ctx, request)
	if err != nil {
		return Credential{}, mastodonFailure("mastodon_token", err)
	}
	body, readErr := mastodonReadLimited(response)
	if readErr != nil {
		return Credential{}, mastodonFailure("mastodon_token", readErr)
	}
	if response.StatusCode != http.StatusOK {
		return Credential{}, mastodonStatusFailure("mastodon_token", response.StatusCode)
	}
	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if !mastodonJSON(response.Header.Get("Content-Type")) ||
		json.Unmarshal(body, &token) != nil ||
		token.AccessToken == "" ||
		!strings.EqualFold(token.TokenType, "Bearer") {
		return Credential{}, mastodonFailure(
			"mastodon_token_malformed",
			errors.New("invalid token response"),
		)
	}
	scopes := strings.Fields(token.Scope)
	if validateScopes(mastodonScopes, scopes) != nil {
		return Credential{}, &ProviderFailure{
			Kind: FailurePermissionMissing,
			Code: "mastodon_scope_missing",
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
	}, nil
}

func mastodonVersion(value string) (int, int, bool) {
	value = strings.SplitN(value, "+", 2)[0]
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	return major, minor, majorErr == nil && minorErr == nil
}

func mastodonSameOriginEndpoint(origin *url.URL, raw string) bool {
	target, err := url.Parse(raw)
	return err == nil &&
		target.Scheme == "https" &&
		target.Host == origin.Host &&
		target.User == nil &&
		target.Fragment == ""
}

func mastodonIssuerMatches(origin *url.URL, raw string) bool {
	target, err := url.Parse(raw)
	if err != nil ||
		target.Scheme != "https" ||
		target.Host != origin.Host ||
		target.User != nil ||
		target.RawQuery != "" ||
		target.Fragment != "" ||
		(target.Path != "" && target.Path != "/") {
		return false
	}
	target.Path = "/"
	target.RawPath = ""
	canonical := *origin
	canonical.Path = "/"
	canonical.RawPath = ""
	return target.String() == canonical.String()
}

func mastodonContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func mastodonJSON(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return contentType == "application/json" || strings.HasSuffix(contentType, "+json")
}

func mastodonFailure(code string, cause error) error {
	return &ProviderFailure{
		Kind:      FailureInvalidResponse,
		Code:      code,
		Retryable: false,
		Cause:     cause,
	}
}

func mastodonStatusFailure(code string, status int) error {
	failure := &ProviderFailure{
		Kind: FailureInvalidResponse,
		Code: code,
	}
	switch {
	case status == http.StatusUnauthorized:
		failure.Kind = FailureAuthentication
	case status == http.StatusForbidden:
		failure.Kind = FailurePermissionMissing
	case status == http.StatusNotFound:
		failure.Kind = FailureResourceGone
	case status == http.StatusTooManyRequests || status >= 500:
		failure.Kind = FailureTemporary
		failure.Retryable = true
	}
	return failure
}
