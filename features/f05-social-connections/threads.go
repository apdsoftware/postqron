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

const maxThreadsResponseBytes = 2 << 20

var threadsRequiredScopes = []string{
	"threads_basic",
	"threads_content_publish",
}

type ThreadsAdapterConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
	Now          func() time.Time

	// Endpoint overrides exist only for contract tests. Production uses the
	// official endpoints below.
	AuthorizationURL string
	APIBaseURL       string
}

type ThreadsAdapter struct {
	clientID         string
	clientSecret     string
	redirectURL      string
	http             *http.Client
	now              func() time.Time
	authorizationURL string
	apiBaseURL       string
}

func NewThreadsAdapter(config ThreadsAdapterConfig) (*ThreadsAdapter, error) {
	clientID := strings.TrimSpace(config.ClientID)
	clientSecret := strings.TrimSpace(config.ClientSecret)
	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf(
			"%w: Threads client ID and secret are required",
			ErrInvalidArgument,
		)
	}
	redirect, err := url.Parse(strings.TrimSpace(config.RedirectURL))
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" {
		return nil, fmt.Errorf(
			"%w: Threads redirect URL must use HTTPS",
			ErrInvalidArgument,
		)
	}
	authorizationURL := strings.TrimSpace(config.AuthorizationURL)
	if authorizationURL == "" {
		authorizationURL = "https://www.threads.com/oauth/authorize"
	}
	authorization, err := url.Parse(authorizationURL)
	if err != nil || authorization.Scheme != "https" || authorization.Host == "" {
		return nil, fmt.Errorf(
			"%w: Threads authorization URL must use HTTPS",
			ErrInvalidArgument,
		)
	}
	apiBaseURL := strings.TrimRight(strings.TrimSpace(config.APIBaseURL), "/")
	if apiBaseURL == "" {
		apiBaseURL = "https://graph.threads.net"
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	httpClient := *config.HTTPClient
	// Official token endpoints place secrets in the query string. Never
	// propagate that query to a redirect target.
	httpClient.CheckRedirect = func(
		_ *http.Request,
		_ []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &ThreadsAdapter{
		clientID:         clientID,
		clientSecret:     clientSecret,
		redirectURL:      redirect.String(),
		http:             &httpClient,
		now:              config.Now,
		authorizationURL: authorization.String(),
		apiBaseURL:       apiBaseURL,
	}, nil
}

func (adapter *ThreadsAdapter) Config() OAuthConfig {
	return OAuthConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), threadsRequiredScopes...),
		SupportsPKCE:     false,
	}
}

func (adapter *ThreadsAdapter) AdapterCapabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		PKCE:              false,
		ResourceSelection: true,
		TokenRefresh:      true,
		RemoteRevocation:  false,
	}
}

type threadsTokenResponse struct {
	AccessToken string          `json:"access_token"`
	TokenType   string          `json:"token_type"`
	ExpiresIn   int64           `json:"expires_in"`
	UserID      json.RawMessage `json:"user_id"`
}

type threadsProfileResponse struct {
	ID                json.RawMessage `json:"id"`
	Username          string          `json:"username"`
	Name              string          `json:"name"`
	ProfilePictureURL string          `json:"threads_profile_picture_url"`
}

type threadsTokenDebugResponse struct {
	Data struct {
		AppID   json.RawMessage `json:"app_id"`
		IsValid bool            `json:"is_valid"`
		Scopes  []string        `json:"scopes"`
		UserID  json.RawMessage `json:"user_id"`
	} `json:"data"`
}

func (adapter *ThreadsAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if strings.TrimSpace(request.Code) == "" ||
		request.RedirectURL != adapter.redirectURL {
		return Credential{}, fmt.Errorf(
			"%w: Threads code and exact redirect URL are required",
			ErrInvalidArgument,
		)
	}
	if request.PKCEVerifier != "" {
		return Credential{}, fmt.Errorf(
			"%w: Threads PKCE is not documented by the provider",
			ErrInvalidArgument,
		)
	}
	shortValues := url.Values{
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"code":          {request.Code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {adapter.redirectURL},
	}
	var short threadsTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.apiBaseURL+"/oauth/access_token?"+shortValues.Encode(),
		"",
		&short,
	); err != nil {
		return Credential{}, err
	}
	if short.AccessToken == "" {
		return Credential{}, invalidThreadsResponse(
			"Threads short-lived token response is incomplete",
		)
	}
	if _, err := parseThreadsID(short.UserID); err != nil {
		return Credential{}, invalidThreadsResponse(
			"Threads token response has no valid user ID",
		)
	}

	longValues := url.Values{
		"grant_type":    {"th_exchange_token"},
		"client_secret": {adapter.clientSecret},
	}
	var long threadsTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.apiBaseURL+"/access_token?"+longValues.Encode(),
		short.AccessToken,
		&long,
	); err != nil {
		return Credential{}, err
	}
	if long.AccessToken == "" || long.ExpiresIn <= 0 {
		return Credential{}, invalidThreadsResponse(
			"Threads long-lived token response is incomplete",
		)
	}
	shortUserID, _ := parseThreadsID(short.UserID)
	if err := adapter.verifyGrantedToken(
		ctx,
		long.AccessToken,
		shortUserID,
		false,
	); err != nil {
		return Credential{}, err
	}
	expiresAt := adapter.now().UTC().Add(
		time.Duration(long.ExpiresIn) * time.Second,
	)
	return Credential{
		AccessToken: long.AccessToken,
		ExpiresAt:   &expiresAt,
		Scopes:      append([]string(nil), threadsRequiredScopes...),
	}, nil
}

func (adapter *ThreadsAdapter) Discover(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	var profile threadsProfileResponse
	endpoint := adapter.apiBaseURL +
		"/me?fields=id%2Cusername%2Cname%2Cthreads_profile_picture_url"
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		endpoint,
		grant.AccessToken,
		&profile,
	); err != nil {
		return nil, err
	}
	remoteID, err := parseThreadsID(profile.ID)
	if err != nil || strings.TrimSpace(profile.Username) == "" {
		return nil, invalidThreadsResponse(
			"Threads profile response is incomplete",
		)
	}
	scopes := append([]string(nil), threadsRequiredScopes...)
	return []DiscoveredResource{{
		Candidate: Candidate{
			RemoteID:     remoteID,
			ResourceType: ResourceThreadsProfile,
			AccountType:  AccountTypeProfile,
			DisplayName:  firstNonEmpty(profile.Name, profile.Username),
			Handle:       profile.Username,
			PictureURL:   profile.ProfilePictureURL,
			Scopes:       scopes,
		},
		Credential: Credential{
			AccessToken: grant.AccessToken,
			ExpiresAt:   cloneTimePointer(grant.ExpiresAt),
			Scopes:      scopes,
		},
	}}, nil
}

func (adapter *ThreadsAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	var response threadsTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.apiBaseURL+"/refresh_access_token?grant_type=th_refresh_token",
		credential.AccessToken,
		&response,
	); err != nil {
		return Credential{}, err
	}
	if response.AccessToken == "" || response.ExpiresIn <= 0 {
		return Credential{}, invalidThreadsResponse(
			"Threads refresh response is incomplete",
		)
	}
	if err := adapter.verifyGrantedToken(
		ctx,
		response.AccessToken,
		"",
		false,
	); err != nil {
		return Credential{}, err
	}
	expiresAt := adapter.now().UTC().Add(
		time.Duration(response.ExpiresIn) * time.Second,
	)
	return Credential{
		AccessToken: response.AccessToken,
		ExpiresAt:   &expiresAt,
		Scopes:      append([]string(nil), threadsRequiredScopes...),
	}, nil
}

func (adapter *ThreadsAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	if strings.TrimSpace(remoteID) == "" {
		return fmt.Errorf("%w: Threads remote ID is required", ErrInvalidArgument)
	}
	var profile threadsProfileResponse
	endpoint := adapter.apiBaseURL + "/" + url.PathEscape(remoteID) +
		"?fields=id%2Cusername"
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		endpoint,
		credential.AccessToken,
		&profile,
	); err != nil {
		return err
	}
	verifiedID, err := parseThreadsID(profile.ID)
	if err != nil || verifiedID != remoteID {
		return &ProviderFailure{
			Kind: FailureResourceGone,
			Code: "threads_resource_gone",
		}
	}
	if strings.TrimSpace(profile.Username) == "" {
		return invalidThreadsResponse("Threads verification response is incomplete")
	}
	return adapter.verifyGrantedToken(
		ctx,
		credential.AccessToken,
		remoteID,
		true,
	)
}

func (*ThreadsAdapter) Revoke(
	context.Context,
	string,
	Credential,
) error {
	// The official Threads lifecycle does not document remote grant
	// revocation. F5 still guarantees local encrypted credential deletion.
	return ErrExternalRevocationUnavailable
}

func (adapter *ThreadsAdapter) verifyGrantedToken(
	ctx context.Context,
	userAccessToken, expectedUserID string,
	exactGrant bool,
) error {
	appValues := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
	}
	var appToken threadsTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.apiBaseURL+"/oauth/access_token?"+appValues.Encode(),
		"",
		&appToken,
	); err != nil {
		return err
	}
	if appToken.AccessToken == "" {
		return invalidThreadsResponse(
			"Threads app token response is incomplete",
		)
	}

	debugValues := url.Values{"input_token": {userAccessToken}}
	var debug threadsTokenDebugResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.apiBaseURL+"/debug_token?"+debugValues.Encode(),
		appToken.AccessToken,
		&debug,
	); err != nil {
		return err
	}
	appID, appIDErr := parseThreadsID(debug.Data.AppID)
	userID, userIDErr := parseThreadsID(debug.Data.UserID)
	identityIDsValid := appIDErr == nil && userIDErr == nil
	if !debug.Data.IsValid ||
		!identityIDsValid ||
		appID != adapter.clientID ||
		(expectedUserID != "" && userID != expectedUserID) {
		return &ProviderFailure{
			Kind: FailureAuthentication,
			Code: "threads_token_invalid",
		}
	}
	if err := validateThreadsScopes(debug.Data.Scopes, exactGrant); err != nil {
		return err
	}
	return nil
}

func validateThreadsScopes(scopes []string, exactGrant bool) error {
	normalized := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		normalized[scope] = struct{}{}
	}
	for _, scope := range threadsRequiredScopes {
		if _, ok := normalized[scope]; !ok {
			return &ProviderFailure{
				Kind: FailurePermissionMissing,
				Code: "threads_required_scope_missing",
			}
		}
	}
	if exactGrant && len(normalized) != len(threadsRequiredScopes) {
		return &ProviderFailure{
			Kind: FailurePermissionMissing,
			Code: "threads_required_scope_mismatch",
		}
	}
	return nil
}

func (adapter *ThreadsAdapter) requestJSON(
	ctx context.Context,
	method, endpoint, bearer string,
	target any,
) error {
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		endpoint,
		bytes.NewReader(nil),
	)
	if err != nil {
		// The endpoint may contain an app secret, authorization code, or user
		// token. Do not retain the URL-bearing parse error.
		return &ProviderFailure{
			Kind:  FailureInvalidResponse,
			Code:  "threads_request_invalid",
			Cause: errors.New("Threads request could not be created"),
		}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Postqron/0.1")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := adapter.http.Do(request)
	if err != nil {
		// Do not retain url.Error: token exchange URLs contain the app secret.
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "threads_transport_error",
			Retryable: true,
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxThreadsResponseBytes+1,
	))
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "threads_response_read_error",
			Retryable: true,
		}
	}
	if len(payload) > maxThreadsResponseBytes {
		return invalidThreadsResponse("Threads response exceeded the size limit")
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return classifyThreadsError(response.StatusCode, payload)
	}
	if len(payload) == 0 {
		return invalidThreadsResponse("Threads returned an empty response")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return invalidThreadsResponse("Threads returned malformed JSON")
	}
	return nil
}

func classifyThreadsError(status int, payload []byte) error {
	var envelope struct {
		Error struct {
			Code    int `json:"code"`
			Subcode int `json:"error_subcode"`
		} `json:"error"`
	}
	_ = json.Unmarshal(payload, &envelope)
	code := envelope.Error.Code
	stableCode := "threads_http_" + strconv.Itoa(status)
	if code != 0 {
		stableCode = "threads_error_" + strconv.Itoa(code)
		if envelope.Error.Subcode != 0 {
			stableCode += "_" + strconv.Itoa(envelope.Error.Subcode)
		}
	}
	failure := &ProviderFailure{Code: stableCode}
	switch {
	case status == http.StatusTooManyRequests || status >= 500:
		failure.Kind = FailureTemporary
		failure.Retryable = true
	case status == http.StatusUnauthorized || code == 190:
		failure.Kind = FailureAuthentication
	case status == http.StatusForbidden || code == 10 || code == 200:
		failure.Kind = FailurePermissionMissing
	case status == http.StatusNotFound:
		failure.Kind = FailureResourceGone
	default:
		failure.Kind = FailureInvalidResponse
	}
	return failure
}

func invalidThreadsResponse(message string) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  "threads_invalid_response",
		Cause: errors.New(message),
	}
}

func parseThreadsID(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", errors.New("missing Threads ID")
	}
	var quoted string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &quoted); err != nil ||
			strings.TrimSpace(quoted) == "" {
			return "", errors.New("invalid Threads ID")
		}
		return quoted, nil
	}
	value := string(raw)
	if _, err := strconv.ParseUint(value, 10, 64); err != nil {
		return "", errors.New("invalid Threads ID")
	}
	return value, nil
}
