package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	tikTokScopeVideoPublish = "video.publish"
	maxTikTokResponseBytes  = 1 << 20
)

var tikTokRequiredScopes = []string{tikTokScopeVideoPublish}

type TikTokAdapterConfig struct {
	ClientKey    string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
	Now          func() time.Time

	// Endpoint overrides are for offline contract tests only.
	AuthorizationURL string
	TokenURL         string
	CreatorInfoURL   string
	RevokeURL        string
}

type TikTokAdapter struct {
	clientKey        string
	clientSecret     string
	redirectURL      string
	http             *http.Client
	now              func() time.Time
	authorizationURL string
	tokenURL         string
	creatorInfoURL   string
	revokeURL        string
}

func NewTikTokAdapter(config TikTokAdapterConfig) (*TikTokAdapter, error) {
	if strings.TrimSpace(config.ClientKey) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf(
			"%w: TikTok client key and secret are required",
			ErrInvalidArgument,
		)
	}
	if err := validateVideoNetworkRedirect("TikTok", config.RedirectURL); err != nil {
		return nil, err
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &TikTokAdapter{
		clientKey:    strings.TrimSpace(config.ClientKey),
		clientSecret: strings.TrimSpace(config.ClientSecret),
		redirectURL:  config.RedirectURL,
		http:         config.HTTPClient,
		now:          config.Now,
		authorizationURL: endpointOrDefault(
			config.AuthorizationURL,
			"https://www.tiktok.com/v2/auth/authorize/",
		),
		tokenURL: endpointOrDefault(
			config.TokenURL,
			"https://open.tiktokapis.com/v2/oauth/token/",
		),
		creatorInfoURL: endpointOrDefault(
			config.CreatorInfoURL,
			"https://open.tiktokapis.com/v2/post/publish/creator_info/query/",
		),
		revokeURL: endpointOrDefault(
			config.RevokeURL,
			"https://open.tiktokapis.com/v2/oauth/revoke/",
		),
	}, nil
}

func (adapter *TikTokAdapter) Config() OAuthConfig {
	return OAuthConfig{
		ClientID:         adapter.clientKey,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), tikTokRequiredScopes...),
		ScopeSeparator:   OAuthScopeSeparatorComma,
		SupportsPKCE:     false,
		ExtraParameters: map[string]string{
			"client_key": adapter.clientKey,
		},
	}
}

func (adapter *TikTokAdapter) AdapterCapabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		PKCE:              false,
		ResourceSelection: true,
		TokenRefresh:      true,
		RemoteRevocation:  true,
	}
}

type tikTokTokenResponse struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int64  `json:"expires_in"`
	OpenID           string `json:"open_id"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	RefreshToken     string `json:"refresh_token"`
	Scope            string `json:"scope"`
	TokenType        string `json:"token_type"`
}

func (adapter *TikTokAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if strings.TrimSpace(request.Code) == "" ||
		request.RedirectURL != adapter.redirectURL ||
		request.PKCEVerifier != "" {
		return Credential{}, fmt.Errorf(
			"%w: invalid TikTok authorization exchange",
			ErrInvalidArgument,
		)
	}
	values := url.Values{
		"client_key":    {adapter.clientKey},
		"client_secret": {adapter.clientSecret},
		"code":          {request.Code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {request.RedirectURL},
	}
	var response tikTokTokenResponse
	if err := adapter.requestFormJSON(
		ctx,
		adapter.tokenURL,
		values,
		"",
		&response,
	); err != nil {
		return Credential{}, err
	}
	return adapter.credentialFromToken(response, true)
}

func (adapter *TikTokAdapter) Discover(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	profile, err := adapter.creatorInfo(ctx, grant.AccessToken)
	if err != nil {
		return nil, err
	}
	scopes := append([]string(nil), grant.Scopes...)
	return []DiscoveredResource{{
		Candidate: Candidate{
			RemoteID:     profile.Username,
			ResourceType: ResourceTikTokProfile,
			AccountType:  AccountTypeProfile,
			DisplayName:  firstNonEmpty(profile.Nickname, profile.Username),
			Handle:       profile.Username,
			Scopes:       scopes,
		},
		Credential: Credential{
			AccessToken:  grant.AccessToken,
			RefreshToken: grant.RefreshToken,
			ExpiresAt:    cloneTimePointer(grant.ExpiresAt),
			Scopes:       scopes,
		},
	}}, nil
}

func (adapter *TikTokAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return Credential{}, ErrNotRefreshable
	}
	values := url.Values{
		"client_key":    {adapter.clientKey},
		"client_secret": {adapter.clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
	}
	var response tikTokTokenResponse
	if err := adapter.requestFormJSON(
		ctx,
		adapter.tokenURL,
		values,
		"",
		&response,
	); err != nil {
		return Credential{}, err
	}
	return adapter.credentialFromToken(response, true)
}

func (adapter *TikTokAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	profile, err := adapter.creatorInfo(ctx, credential.AccessToken)
	if err != nil {
		return err
	}
	if profile.Username != remoteID {
		return &ProviderFailure{
			Kind: FailureResourceGone,
			Code: "tiktok_profile_gone",
		}
	}
	return nil
}

func (adapter *TikTokAdapter) Revoke(
	ctx context.Context,
	_ string,
	credential Credential,
) error {
	if strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("%w: TikTok access token is required", ErrInvalidArgument)
	}
	values := url.Values{
		"client_key":    {adapter.clientKey},
		"client_secret": {adapter.clientSecret},
		"token":         {credential.AccessToken},
	}
	return adapter.requestFormJSON(ctx, adapter.revokeURL, values, "", nil)
}

type tikTokCreatorInfo struct {
	Username string
	Nickname string
}

func (adapter *TikTokAdapter) creatorInfo(
	ctx context.Context,
	accessToken string,
) (tikTokCreatorInfo, error) {
	var response struct {
		Data struct {
			Username string `json:"creator_username"`
			Nickname string `json:"creator_nickname"`
		} `json:"data"`
		Error tikTokError `json:"error"`
	}
	if err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.creatorInfoURL,
		nil,
		accessToken,
		&response,
	); err != nil {
		return tikTokCreatorInfo{}, err
	}
	if response.Error.Code != "" && response.Error.Code != "ok" {
		return tikTokCreatorInfo{}, classifyTikTokFailure(
			http.StatusOK,
			response.Error.Code,
		)
	}
	if strings.TrimSpace(response.Data.Username) == "" ||
		strings.TrimSpace(response.Data.Nickname) == "" {
		return tikTokCreatorInfo{}, invalidTikTokResponse(
			"TikTok Creator Info response is incomplete",
		)
	}
	return tikTokCreatorInfo{
		Username: response.Data.Username,
		Nickname: response.Data.Nickname,
	}, nil
}

func (adapter *TikTokAdapter) credentialFromToken(
	response tikTokTokenResponse,
	requireRefresh bool,
) (Credential, error) {
	scopes := splitOAuthScopes(response.Scope, ",")
	if response.AccessToken == "" ||
		response.ExpiresIn <= 0 ||
		response.OpenID == "" ||
		!strings.EqualFold(response.TokenType, "Bearer") ||
		(requireRefresh && response.RefreshToken == "") ||
		validateScopes(tikTokRequiredScopes, scopes) != nil {
		return Credential{}, invalidTikTokResponse(
			"TikTok token response is incomplete",
		)
	}
	expiresAt := adapter.now().UTC().Add(
		time.Duration(response.ExpiresIn) * time.Second,
	)
	return Credential{
		AccessToken:  response.AccessToken,
		RefreshToken: response.RefreshToken,
		ExpiresAt:    &expiresAt,
		Scopes:       scopes,
	}, nil
}

func (adapter *TikTokAdapter) requestFormJSON(
	ctx context.Context,
	endpoint string,
	values url.Values,
	bearer string,
	target any,
) error {
	return adapter.requestJSON(
		ctx,
		http.MethodPost,
		endpoint,
		[]byte(values.Encode()),
		bearer,
		target,
	)
}

func (adapter *TikTokAdapter) requestJSON(
	ctx context.Context,
	method, endpoint string,
	body []byte,
	bearer string,
	target any,
) error {
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create TikTok request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Postqron/0.1")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := adapter.http.Do(request)
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "tiktok_transport_error",
			Retryable: true,
			Cause:     err,
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxTikTokResponseBytes,
	))
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "tiktok_response_read_error",
			Retryable: true,
			Cause:     err,
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyTikTokError(response.StatusCode, payload)
	}
	if target == nil && len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if target == nil {
		var envelope struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil {
			return invalidTikTokResponse("TikTok returned malformed JSON")
		}
		if envelope.Error != "" {
			return classifyTikTokFailure(response.StatusCode, envelope.Error)
		}
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return invalidTikTokResponse("TikTok returned malformed JSON")
	}
	return nil
}

type tikTokError struct {
	Code string `json:"code"`
}

func classifyTikTokError(status int, payload []byte) error {
	var envelope struct {
		Error       json.RawMessage `json:"error"`
		ErrorObject tikTokError     `json:"-"`
	}
	code := ""
	if json.Unmarshal(payload, &envelope) == nil && len(envelope.Error) > 0 {
		var text string
		if json.Unmarshal(envelope.Error, &text) == nil {
			code = text
		} else {
			_ = json.Unmarshal(envelope.Error, &envelope.ErrorObject)
			code = envelope.ErrorObject.Code
		}
	}
	return classifyTikTokFailure(status, code)
}

func classifyTikTokFailure(status int, code string) error {
	stableCode := "tiktok_http_" + strconv.Itoa(status)
	if sanitized := stableProviderCode(code); sanitized != "" {
		stableCode = "tiktok_" + sanitized
	}
	failure := &ProviderFailure{Code: stableCode}
	switch {
	case status == http.StatusTooManyRequests,
		status >= 500,
		isTikTokTemporaryCode(code):
		failure.Kind = FailureTemporary
		failure.Retryable = true
	case status == http.StatusUnauthorized,
		code == "access_token_invalid",
		code == "invalid_grant":
		failure.Kind = FailureAuthentication
	case status == http.StatusForbidden,
		code == "scope_not_authorized",
		code == "access_denied",
		code == "spam_risk_user_banned_from_posting":
		failure.Kind = FailurePermissionMissing
	case status == http.StatusNotFound:
		failure.Kind = FailureResourceGone
	default:
		failure.Kind = FailureInvalidResponse
	}
	return failure
}

func isTikTokTemporaryCode(code string) bool {
	switch code {
	case "rate_limit_exceeded",
		"reached_active_user_cap",
		"spam_risk_too_many_posts",
		"internal_error",
		"processing_error":
		return true
	default:
		return false
	}
}

func invalidTikTokResponse(message string) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  "tiktok_invalid_response",
		Cause: errors.New(message),
	}
}

func validateVideoNetworkRedirect(provider, rawURL string) error {
	redirect, err := url.Parse(rawURL)
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" ||
		redirect.User != nil || redirect.Fragment != "" {
		return fmt.Errorf(
			"%w: %s redirect URL must be an absolute HTTPS URL",
			ErrInvalidArgument,
			provider,
		)
	}
	return nil
}

func endpointOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func splitOAuthScopes(value, separator string) []string {
	var raw []string
	if separator == " " {
		raw = strings.Fields(value)
	} else {
		raw = strings.Split(value, separator)
	}
	scopes := make([]string, 0, len(raw))
	for _, scope := range raw {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return scopes
}

func stableProviderCode(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	var result strings.Builder
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
			result.WriteRune(character)
		case character >= '0' && character <= '9':
			result.WriteRune(character)
		case character == '_', character == '-':
			result.WriteRune('_')
		}
	}
	return strings.Trim(result.String(), "_")
}
