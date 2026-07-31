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
	youTubeScopeReadOnly = "https://www.googleapis.com/auth/youtube.readonly"
	youTubeScopeUpload   = "https://www.googleapis.com/auth/youtube.upload"
	maxYouTubeResponse   = 1 << 20
)

var youTubeRequiredScopes = []string{
	youTubeScopeReadOnly,
	youTubeScopeUpload,
}

type YouTubeAdapterConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
	Now          func() time.Time

	// Endpoint overrides are for offline contract tests only.
	AuthorizationURL string
	TokenURL         string
	APIBaseURL       string
	RevokeURL        string
}

type YouTubeAdapter struct {
	clientID         string
	clientSecret     string
	redirectURL      string
	http             *http.Client
	now              func() time.Time
	authorizationURL string
	tokenURL         string
	apiBaseURL       string
	revokeURL        string
}

func NewYouTubeAdapter(config YouTubeAdapterConfig) (*YouTubeAdapter, error) {
	if strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf(
			"%w: YouTube client ID and secret are required",
			ErrInvalidArgument,
		)
	}
	if err := validateVideoNetworkRedirect("YouTube", config.RedirectURL); err != nil {
		return nil, err
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &YouTubeAdapter{
		clientID:     strings.TrimSpace(config.ClientID),
		clientSecret: strings.TrimSpace(config.ClientSecret),
		redirectURL:  config.RedirectURL,
		http:         config.HTTPClient,
		now:          config.Now,
		authorizationURL: endpointOrDefault(
			config.AuthorizationURL,
			"https://accounts.google.com/o/oauth2/v2/auth",
		),
		tokenURL: endpointOrDefault(
			config.TokenURL,
			"https://oauth2.googleapis.com/token",
		),
		apiBaseURL: strings.TrimRight(endpointOrDefault(
			config.APIBaseURL,
			"https://www.googleapis.com/youtube/v3",
		), "/"),
		revokeURL: endpointOrDefault(
			config.RevokeURL,
			"https://oauth2.googleapis.com/revoke",
		),
	}, nil
}

func (adapter *YouTubeAdapter) Config() OAuthConfig {
	return OAuthConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), youTubeRequiredScopes...),
		ScopeSeparator:   OAuthScopeSeparatorSpace,
		SupportsPKCE:     false,
		ExtraParameters: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
	}
}

func (adapter *YouTubeAdapter) AdapterCapabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		PKCE:              false,
		ResourceSelection: true,
		TokenRefresh:      true,
		RemoteRevocation:  true,
	}
}

type youTubeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

func (adapter *YouTubeAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if strings.TrimSpace(request.Code) == "" ||
		request.RedirectURL != adapter.redirectURL ||
		request.PKCEVerifier != "" {
		return Credential{}, fmt.Errorf(
			"%w: invalid YouTube authorization exchange",
			ErrInvalidArgument,
		)
	}
	values := url.Values{
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"code":          {request.Code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {request.RedirectURL},
	}
	var response youTubeTokenResponse
	if err := adapter.requestFormJSON(
		ctx,
		adapter.tokenURL,
		values,
		&response,
	); err != nil {
		return Credential{}, err
	}
	return adapter.credentialFromToken(response, "", nil, true)
}

func (adapter *YouTubeAdapter) Discover(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	channels, err := adapter.channels(ctx, grant.AccessToken)
	if err != nil {
		return nil, err
	}
	resources := make([]DiscoveredResource, 0, len(channels))
	for _, channel := range channels {
		if channel.ID == "" || channel.Snippet.Title == "" {
			return nil, invalidYouTubeResponse(
				"YouTube channel response is incomplete",
			)
		}
		scopes := append([]string(nil), grant.Scopes...)
		resources = append(resources, DiscoveredResource{
			Candidate: Candidate{
				RemoteID:     channel.ID,
				ResourceType: ResourceYouTubeChannel,
				AccountType:  AccountTypeChannel,
				DisplayName:  channel.Snippet.Title,
				Handle:       channel.Snippet.CustomURL,
				PictureURL:   channel.Snippet.Thumbnails.Default.URL,
				Scopes:       scopes,
			},
			Credential: Credential{
				AccessToken:  grant.AccessToken,
				RefreshToken: grant.RefreshToken,
				ExpiresAt:    cloneTimePointer(grant.ExpiresAt),
				Scopes:       scopes,
			},
		})
	}
	return resources, nil
}

func (adapter *YouTubeAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return Credential{}, ErrNotRefreshable
	}
	values := url.Values{
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
	}
	var response youTubeTokenResponse
	if err := adapter.requestFormJSON(
		ctx,
		adapter.tokenURL,
		values,
		&response,
	); err != nil {
		return Credential{}, err
	}
	return adapter.credentialFromToken(
		response,
		credential.RefreshToken,
		credential.Scopes,
		false,
	)
}

func (adapter *YouTubeAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	channels, err := adapter.channels(ctx, credential.AccessToken)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if channel.ID == remoteID {
			return nil
		}
	}
	return &ProviderFailure{
		Kind: FailureResourceGone,
		Code: "youtube_channel_gone",
	}
}

func (adapter *YouTubeAdapter) Revoke(
	ctx context.Context,
	_ string,
	credential Credential,
) error {
	token := firstNonEmpty(credential.RefreshToken, credential.AccessToken)
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%w: YouTube token is required", ErrInvalidArgument)
	}
	return adapter.requestFormJSON(
		ctx,
		adapter.revokeURL,
		url.Values{"token": {token}},
		nil,
	)
}

type youTubeChannel struct {
	ID      string `json:"id"`
	Snippet struct {
		Title      string `json:"title"`
		CustomURL  string `json:"customUrl"`
		Thumbnails struct {
			Default struct {
				URL string `json:"url"`
			} `json:"default"`
		} `json:"thumbnails"`
	} `json:"snippet"`
}

func (adapter *YouTubeAdapter) channels(
	ctx context.Context,
	accessToken string,
) ([]youTubeChannel, error) {
	values := url.Values{
		"part":       {"id,snippet"},
		"mine":       {"true"},
		"maxResults": {"50"},
	}
	var response struct {
		Items []youTubeChannel `json:"items"`
	}
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.apiBaseURL+"/channels?"+values.Encode(),
		nil,
		accessToken,
		&response,
	); err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (adapter *YouTubeAdapter) credentialFromToken(
	response youTubeTokenResponse,
	previousRefresh string,
	previousScopes []string,
	requireRefresh bool,
) (Credential, error) {
	refreshToken := firstNonEmpty(response.RefreshToken, previousRefresh)
	scopes := splitOAuthScopes(response.Scope, " ")
	if len(scopes) == 0 {
		scopes = append([]string(nil), previousScopes...)
	}
	if response.AccessToken == "" ||
		response.ExpiresIn <= 0 ||
		!strings.EqualFold(response.TokenType, "Bearer") ||
		(requireRefresh && refreshToken == "") ||
		validateScopes(youTubeRequiredScopes, scopes) != nil {
		return Credential{}, invalidYouTubeResponse(
			"YouTube token response is incomplete",
		)
	}
	expiresAt := adapter.now().UTC().Add(
		time.Duration(response.ExpiresIn) * time.Second,
	)
	return Credential{
		AccessToken:  response.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    &expiresAt,
		Scopes:       scopes,
	}, nil
}

func (adapter *YouTubeAdapter) requestFormJSON(
	ctx context.Context,
	endpoint string,
	values url.Values,
	target any,
) error {
	return adapter.requestJSON(
		ctx,
		http.MethodPost,
		endpoint,
		[]byte(values.Encode()),
		"",
		target,
	)
}

func (adapter *YouTubeAdapter) requestJSON(
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
		return fmt.Errorf("create YouTube request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Postqron/0.1")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := adapter.http.Do(request)
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "youtube_transport_error",
			Retryable: true,
			Cause:     err,
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxYouTubeResponse,
	))
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "youtube_response_read_error",
			Retryable: true,
			Cause:     err,
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyYouTubeError(response.StatusCode, payload)
	}
	if target == nil && len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if target == nil {
		var unused any
		if err := json.Unmarshal(payload, &unused); err != nil {
			return invalidYouTubeResponse("YouTube returned malformed JSON")
		}
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return invalidYouTubeResponse("YouTube returned malformed JSON")
	}
	return nil
}

func classifyYouTubeError(status int, payload []byte) error {
	var envelope struct {
		OAuthError string `json:"error_description"`
		Error      any    `json:"error"`
	}
	code := ""
	reason := ""
	if json.Unmarshal(payload, &envelope) == nil {
		switch value := envelope.Error.(type) {
		case string:
			code = value
		case map[string]any:
			if rawCode, ok := value["code"].(float64); ok {
				code = strconv.Itoa(int(rawCode))
			}
			if errorsList, ok := value["errors"].([]any); ok &&
				len(errorsList) > 0 {
				if first, ok := errorsList[0].(map[string]any); ok {
					reason, _ = first["reason"].(string)
				}
			}
			if reason == "" {
				reason, _ = value["status"].(string)
			}
		}
	}
	stable := firstNonEmpty(reason, code)
	stableCode := "youtube_http_" + strconv.Itoa(status)
	if sanitized := stableProviderCode(stable); sanitized != "" {
		stableCode = "youtube_" + sanitized
	}
	failure := &ProviderFailure{Code: stableCode}
	switch {
	case status == http.StatusTooManyRequests,
		status >= 500,
		isYouTubeTemporaryReason(reason):
		failure.Kind = FailureTemporary
		failure.Retryable = true
	case status == http.StatusUnauthorized,
		code == "invalid_grant",
		code == "invalid_token":
		failure.Kind = FailureAuthentication
	case status == http.StatusForbidden,
		code == "access_denied",
		reason == "insufficientPermissions",
		reason == "forbidden",
		reason == "channelForbidden":
		failure.Kind = FailurePermissionMissing
	case status == http.StatusNotFound:
		failure.Kind = FailureResourceGone
	default:
		failure.Kind = FailureInvalidResponse
	}
	return failure
}

func isYouTubeTemporaryReason(reason string) bool {
	switch reason {
	case "quotaExceeded",
		"dailyLimitExceeded",
		"userRateLimitExceeded",
		"rateLimitExceeded",
		"backendError",
		"processingFailure":
		return true
	default:
		return false
	}
}

func invalidYouTubeResponse(message string) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  "youtube_invalid_response",
		Cause: errors.New(message),
	}
}
