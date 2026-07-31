package socialconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	xOfficialAuthorizationURL = "https://x.com/i/oauth2/authorize"
	xOfficialAPIBaseURL       = "https://api.x.com"
	xOAuthTokenPath           = "/2/oauth2/token"
	xOAuthRevokePath          = "/2/oauth2/revoke"
	xAuthenticatedUserPath    = "/2/users/me"
	maxXResponseBytes         = 1 << 20
)

var xRequiredScopes = []string{
	"tweet.read",
	"tweet.write",
	"users.read",
	"offline.access",
}

type XAdapterConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
	Now          func() time.Time

	// Endpoint overrides exist only for offline contract tests. Production
	// must leave them empty to use the official X endpoints above.
	AuthorizationURL string
	APIBaseURL       string
}

type XAdapter struct {
	clientID         string
	clientSecret     string
	redirectURL      string
	http             *http.Client
	now              func() time.Time
	authorizationURL string
	apiBaseURL       string
}

type xTokenResponse struct {
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	AccessToken  string `json:"access_token"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

type xUserResponse struct {
	Data struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Username        string `json:"username"`
		ProfileImageURL string `json:"profile_image_url"`
	} `json:"data"`
	Errors []json.RawMessage `json:"errors"`
}

func NewXAdapter(config XAdapterConfig) (*XAdapter, error) {
	clientID := strings.TrimSpace(config.ClientID)
	if clientID == "" || strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf(
			"%w: X confidential client ID and secret are required",
			ErrInvalidArgument,
		)
	}
	redirectURL := strings.TrimSpace(config.RedirectURL)
	if err := validateXRedirectURL(redirectURL); err != nil {
		return nil, err
	}
	authorizationURL, err := validateXEndpoint(
		config.AuthorizationURL,
		xOfficialAuthorizationURL,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid X authorization endpoint", ErrInvalidArgument)
	}
	apiBaseURL, err := validateXEndpoint(
		config.APIBaseURL,
		xOfficialAPIBaseURL,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid X API base URL", ErrInvalidArgument)
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	secureClient := *client
	secureClient.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &XAdapter{
		clientID:         clientID,
		clientSecret:     config.ClientSecret,
		redirectURL:      redirectURL,
		http:             &secureClient,
		now:              config.Now,
		authorizationURL: authorizationURL,
		apiBaseURL:       strings.TrimRight(apiBaseURL, "/"),
	}, nil
}

func (adapter *XAdapter) Config() OAuthConfig {
	return OAuthConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), xRequiredScopes...),
		ScopeSeparator:   OAuthScopeSeparatorSpace,
		SupportsPKCE:     true,
	}
}

func (adapter *XAdapter) AdapterCapabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		PKCE:              true,
		ResourceSelection: true,
		TokenRefresh:      true,
		RemoteRevocation:  true,
	}
}

func (adapter *XAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if strings.TrimSpace(request.Code) == "" ||
		strings.TrimSpace(request.PKCEVerifier) == "" ||
		request.RedirectURL != adapter.redirectURL {
		return Credential{}, fmt.Errorf(
			"%w: X authorization code, exact redirect URL, and PKCE verifier are required",
			ErrInvalidArgument,
		)
	}
	form := url.Values{
		"code":          {request.Code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {adapter.redirectURL},
		"code_verifier": {request.PKCEVerifier},
	}
	response, err := adapter.requestToken(ctx, form)
	if err != nil {
		return Credential{}, err
	}
	return adapter.credentialFromToken(response, "", true)
}

func (adapter *XAdapter) Discover(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	profile, err := adapter.getAuthenticatedUser(ctx, grant.AccessToken)
	if err != nil {
		return nil, err
	}
	if err := validateXUser(profile); err != nil {
		return nil, err
	}
	scopes := xCredentialScopes()
	return []DiscoveredResource{{
		Candidate: Candidate{
			RemoteID:     profile.Data.ID,
			ResourceType: ResourceXProfile,
			AccountType:  AccountTypeProfile,
			DisplayName:  profile.Data.Name,
			Handle:       profile.Data.Username,
			PictureURL:   profile.Data.ProfileImageURL,
			Scopes:       append([]string(nil), scopes...),
		},
		Credential: Credential{
			AccessToken:  grant.AccessToken,
			RefreshToken: grant.RefreshToken,
			ExpiresAt:    cloneTimePointer(grant.ExpiresAt),
			Scopes:       scopes,
		},
	}}, nil
}

func (adapter *XAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return Credential{}, ErrNotRefreshable
	}
	form := url.Values{
		"refresh_token": {credential.RefreshToken},
		"grant_type":    {"refresh_token"},
	}
	response, err := adapter.requestToken(ctx, form)
	if err != nil {
		return Credential{}, err
	}
	return adapter.credentialFromToken(
		response,
		credential.RefreshToken,
		false,
	)
}

func (adapter *XAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	profile, err := adapter.getAuthenticatedUser(ctx, credential.AccessToken)
	if err != nil {
		return err
	}
	if err := validateXUser(profile); err != nil {
		return err
	}
	if profile.Data.ID != remoteID {
		return &ProviderFailure{
			Kind: FailureResourceGone,
			Code: "x_resource_gone",
		}
	}
	return nil
}

func (adapter *XAdapter) Revoke(
	ctx context.Context,
	_ string,
	credential Credential,
) error {
	if strings.TrimSpace(credential.AccessToken) == "" {
		return fmt.Errorf("%w: X access token is required", ErrInvalidArgument)
	}
	tokens := []string{credential.AccessToken}
	if credential.RefreshToken != "" &&
		credential.RefreshToken != credential.AccessToken {
		tokens = append(tokens, credential.RefreshToken)
	}
	var firstFailure error
	for _, token := range tokens {
		if err := adapter.revokeToken(ctx, token); err != nil {
			// Revocation is idempotent. X can invalidate the whole grant when
			// the first token is revoked, making the second token report
			// invalid_token. That is already the desired remote state. Other
			// failures remain visible, while both tokens are still attempted.
			if !isXAlreadyRevoked(err) && firstFailure == nil {
				firstFailure = err
			}
		}
	}
	return firstFailure
}

func (adapter *XAdapter) requestToken(
	ctx context.Context,
	form url.Values,
) (xTokenResponse, error) {
	var response xTokenResponse
	err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.apiBaseURL+xOAuthTokenPath,
		form,
		"",
		true,
		&response,
	)
	return response, err
}

func (adapter *XAdapter) revokeToken(
	ctx context.Context,
	token string,
) error {
	return adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.apiBaseURL+xOAuthRevokePath,
		url.Values{"token": {token}},
		"",
		true,
		nil,
	)
}

func (adapter *XAdapter) getAuthenticatedUser(
	ctx context.Context,
	accessToken string,
) (xUserResponse, error) {
	if strings.TrimSpace(accessToken) == "" {
		return xUserResponse{}, fmt.Errorf("%w: X access token is required", ErrInvalidArgument)
	}
	endpoint := adapter.apiBaseURL + xAuthenticatedUserPath +
		"?user.fields=profile_image_url"
	var response xUserResponse
	err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		endpoint,
		nil,
		accessToken,
		false,
		&response,
	)
	return response, err
}

func (adapter *XAdapter) credentialFromToken(
	response xTokenResponse,
	previousRefreshToken string,
	requireRefreshToken bool,
) (Credential, error) {
	if !strings.EqualFold(response.TokenType, "bearer") ||
		strings.TrimSpace(response.AccessToken) == "" ||
		response.ExpiresIn <= 0 {
		return Credential{}, invalidXResponse("X token response is incomplete")
	}
	if err := validateXGrantedScopes(response.Scope); err != nil {
		return Credential{}, err
	}
	refreshToken := response.RefreshToken
	if refreshToken == "" {
		refreshToken = previousRefreshToken
	}
	if requireRefreshToken && strings.TrimSpace(refreshToken) == "" {
		return Credential{}, invalidXResponse(
			"X did not issue the refresh token required by offline.access",
		)
	}
	expiresAt := adapter.now().UTC().Add(
		time.Duration(response.ExpiresIn) * time.Second,
	)
	return Credential{
		AccessToken:  response.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    &expiresAt,
		Scopes:       xCredentialScopes(),
	}, nil
}

func (adapter *XAdapter) requestJSON(
	ctx context.Context,
	method, endpoint string,
	form url.Values,
	bearer string,
	confidentialClient bool,
	target any,
) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create X request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Postqron/0.1")
	if form != nil {
		request.Header.Set(
			"Content-Type",
			"application/x-www-form-urlencoded",
		)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if confidentialClient {
		request.SetBasicAuth(adapter.clientID, adapter.clientSecret)
	}
	response, err := adapter.http.Do(request)
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "x_transport_error",
			Retryable: true,
			Cause:     err,
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxXResponseBytes+1,
	))
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "x_response_read_error",
			Retryable: true,
			Cause:     err,
		}
	}
	if len(payload) > maxXResponseBytes {
		return invalidXResponse("X response exceeded the size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyXError(response.StatusCode, payload)
	}
	if target == nil {
		return nil
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return invalidXResponse("X returned an empty response")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return invalidXResponse("X returned malformed JSON")
	}
	return nil
}

func classifyXError(status int, payload []byte) error {
	var envelope struct {
		Type   string `json:"type"`
		Error  string `json:"error"`
		Errors []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(payload, &envelope)
	code := "x_http_" + strconv.Itoa(status)
	if stable := stableXErrorCode(envelope.Error); stable != "" {
		code = "x_oauth_" + stable
	} else if stable := stableXErrorCode(path.Base(envelope.Type)); stable != "" &&
		stable != "." {
		code = "x_problem_" + stable
	} else if len(envelope.Errors) > 0 && envelope.Errors[0].Code != 0 {
		code = "x_error_" + strconv.Itoa(envelope.Errors[0].Code)
	}
	failure := &ProviderFailure{Code: code}
	switch {
	case status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status >= 500:
		failure.Kind = FailureTemporary
		failure.Retryable = true
	case status == http.StatusUnauthorized ||
		envelope.Error == "invalid_grant" ||
		envelope.Error == "invalid_token":
		failure.Kind = FailureAuthentication
	case status == http.StatusForbidden ||
		envelope.Error == "access_denied" ||
		envelope.Error == "invalid_scope":
		failure.Kind = FailurePermissionMissing
	case status == http.StatusNotFound:
		failure.Kind = FailureResourceGone
	default:
		failure.Kind = FailureInvalidResponse
	}
	return failure
}

func invalidXResponse(message string) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  "x_invalid_response",
		Cause: errors.New(message),
	}
}

func isXAlreadyRevoked(err error) bool {
	var failure *ProviderFailure
	return errors.As(err, &failure) &&
		failure.Kind == FailureAuthentication &&
		failure.Code == "x_oauth_invalid_token"
}

func validateXRedirectURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil ||
		parsed.Scheme != "https" ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return fmt.Errorf(
			"%w: X redirect URL must be an exact HTTPS URL",
			ErrInvalidArgument,
		)
	}
	return nil
}

func validateXEndpoint(
	raw, fallback string,
	baseOnly bool,
) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		value = fallback
	}
	parsed, err := url.Parse(value)
	if err != nil ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return "", errors.New("invalid X endpoint")
	}
	if parsed.Scheme != "https" &&
		!(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return "", errors.New("X endpoint must use HTTPS")
	}
	if baseOnly && parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("X API base URL must not contain a path")
	}
	return parsed.String(), nil
}

func validateXGrantedScopes(raw string) error {
	granted := strings.Fields(raw)
	if len(granted) != len(xRequiredScopes) {
		return invalidXResponse("X returned an unexpected OAuth scope set")
	}
	seen := make(map[string]struct{}, len(granted))
	for _, scope := range granted {
		if _, duplicate := seen[scope]; duplicate {
			return invalidXResponse("X returned a duplicate OAuth scope")
		}
		seen[scope] = struct{}{}
	}
	for _, required := range xRequiredScopes {
		if _, ok := seen[required]; !ok {
			return &ProviderFailure{
				Kind: FailurePermissionMissing,
				Code: "x_required_scope_missing",
			}
		}
	}
	return nil
}

func validateXUser(profile xUserResponse) error {
	if len(profile.Errors) != 0 ||
		!isNumericXID(profile.Data.ID) ||
		strings.TrimSpace(profile.Data.Name) == "" ||
		strings.TrimSpace(profile.Data.Username) == "" {
		return invalidXResponse("X user response is incomplete")
	}
	if profile.Data.ProfileImageURL != "" {
		picture, err := url.Parse(profile.Data.ProfileImageURL)
		if err != nil ||
			picture.Scheme != "https" ||
			picture.Host == "" ||
			picture.User != nil {
			return invalidXResponse("X user profile image URL is invalid")
		}
	}
	return nil
}

func xCredentialScopes() []string {
	return append([]string(nil), xRequiredScopes...)
}

func stableXErrorCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '_' &&
			character != '-' {
			return ""
		}
	}
	return value
}

func isNumericXID(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
