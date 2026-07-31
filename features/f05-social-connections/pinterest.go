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
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	pinterestAuthorizationURL = "https://www.pinterest.com/oauth/"
	pinterestAPIBaseURL       = "https://api.pinterest.com/v5"
	pinterestPageSize         = 250
	pinterestMaximumPages     = 100
	maxPinterestResponseBytes = 2 << 20
)

var (
	pinterestIDPattern = regexp.MustCompile(`^\d+$`)

	// These are the exact scopes currently required by Pinterest API v5 to
	// discover the destination board and create an organic Pin. Secret-board
	// scopes, ads, analytics, catalogs, and user-account scopes are excluded.
	pinterestRequiredScopes = []string{
		"boards:read",
		"boards:write",
		"pins:read",
		"pins:write",
	}
)

type PinterestAdapterConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
	Now          func() time.Time

	// Endpoint overrides are only for deterministic contract tests. Runtime
	// wiring never exposes them as configuration.
	APIBaseURL string
	TokenURL   string
}

type PinterestAdapter struct {
	clientID     string
	clientSecret string
	redirectURL  string
	http         *http.Client
	now          func() time.Time
	apiBaseURL   string
	tokenURL     string
}

type pinterestTokenResponse struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	TokenType             string `json:"token_type"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	RefreshTokenExpiresAt int64  `json:"refresh_token_expires_at"`
	ResponseType          string `json:"response_type"`
	Scope                 string `json:"scope"`
}

type pinterestBoard struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Privacy   string `json:"privacy"`
	IsAdsOnly bool   `json:"is_ads_only"`
}

func NewPinterestAdapter(
	config PinterestAdapterConfig,
) (*PinterestAdapter, error) {
	if strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf(
			"%w: Pinterest client ID and secret are required",
			ErrInvalidArgument,
		)
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" ||
		redirect.User != nil || redirect.Fragment != "" {
		return nil, fmt.Errorf(
			"%w: Pinterest redirect URL must be an absolute HTTPS URL",
			ErrInvalidArgument,
		)
	}
	apiBaseURL := strings.TrimRight(config.APIBaseURL, "/")
	if apiBaseURL == "" {
		apiBaseURL = pinterestAPIBaseURL
	}
	if err := validatePinterestEndpoint(apiBaseURL); err != nil {
		return nil, err
	}
	tokenURL := strings.TrimSpace(config.TokenURL)
	if tokenURL == "" {
		tokenURL = apiBaseURL + "/oauth/token"
	}
	if err := validatePinterestEndpoint(tokenURL); err != nil {
		return nil, err
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	} else {
		cloned := *client
		client = &cloned
	}
	// OAuth credentials and bearer tokens must never be forwarded through an
	// unexpected redirect.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &PinterestAdapter{
		clientID:     config.ClientID,
		clientSecret: config.ClientSecret,
		redirectURL:  config.RedirectURL,
		http:         client,
		now:          config.Now,
		apiBaseURL:   apiBaseURL,
		tokenURL:     tokenURL,
	}, nil
}

func (adapter *PinterestAdapter) Config() OAuthConfig {
	return OAuthConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: pinterestAuthorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), pinterestRequiredScopes...),
		ScopeSeparator:   OAuthScopeSeparatorSpace,
		// Pinterest's current Authorization Code documentation does not
		// document PKCE parameters. The core still supplies one-time state.
		SupportsPKCE: false,
	}
}

func (adapter *PinterestAdapter) AdapterCapabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		ResourceSelection: true,
		TokenRefresh:      true,
		// API v5 currently documents token revocation only for system-user
		// tokens, not the Authorization Code user tokens used here.
		RemoteRevocation: false,
	}
}

func (adapter *PinterestAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if strings.TrimSpace(request.Code) == "" ||
		request.RedirectURL != adapter.redirectURL ||
		request.PKCEVerifier != "" {
		return Credential{}, fmt.Errorf(
			"%w: invalid Pinterest authorization code exchange",
			ErrInvalidArgument,
		)
	}
	values := url.Values{
		"grant_type":         {"authorization_code"},
		"code":               {request.Code},
		"redirect_uri":       {adapter.redirectURL},
		"continuous_refresh": {"true"},
	}
	var response pinterestTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.tokenURL,
		values,
		"",
		true,
		&response,
	); err != nil {
		return Credential{}, err
	}
	return adapter.parseToken(response)
}

func (adapter *PinterestAdapter) Discover(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	if err := validatePinterestCredential(grant, true); err != nil {
		return nil, err
	}
	bookmark := ""
	seenBookmarks := make(map[string]struct{})
	seenBoards := make(map[string]struct{})
	resources := make([]DiscoveredResource, 0)
	for pageNumber := 0; pageNumber < pinterestMaximumPages; pageNumber++ {
		query := url.Values{
			"page_size": {strconv.Itoa(pinterestPageSize)},
		}
		if bookmark != "" {
			query.Set("bookmark", bookmark)
		}
		var response struct {
			Items    *[]pinterestBoard `json:"items"`
			Bookmark *string           `json:"bookmark"`
		}
		if err := adapter.requestJSON(
			ctx,
			http.MethodGet,
			adapter.apiBaseURL+"/boards?"+query.Encode(),
			nil,
			grant.AccessToken,
			false,
			&response,
		); err != nil {
			return nil, err
		}
		if response.Items == nil {
			return nil, invalidPinterestResponse(
				"Pinterest board response omitted items",
			)
		}
		for _, board := range *response.Items {
			if !pinterestIDPattern.MatchString(board.ID) ||
				strings.TrimSpace(board.Name) == "" {
				return nil, invalidPinterestResponse(
					"Pinterest returned an incomplete board",
				)
			}
			if _, duplicate := seenBoards[board.ID]; duplicate {
				return nil, invalidPinterestResponse(
					"Pinterest returned a duplicate board",
				)
			}
			seenBoards[board.ID] = struct{}{}
			privacy := strings.ToUpper(strings.TrimSpace(board.Privacy))
			if board.IsAdsOnly || privacy == "SECRET" || privacy == "PROTECTED" {
				continue
			}
			scopes := append([]string(nil), pinterestRequiredScopes...)
			resources = append(resources, DiscoveredResource{
				Candidate: Candidate{
					RemoteID:     board.ID,
					ResourceType: ResourcePinterestBoard,
					AccountType:  AccountTypeBoard,
					DisplayName:  board.Name,
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
		next := ""
		if response.Bookmark != nil {
			next = strings.TrimSpace(*response.Bookmark)
		}
		if next == "" {
			return resources, nil
		}
		if _, duplicate := seenBookmarks[next]; duplicate {
			return nil, invalidPinterestResponse(
				"Pinterest repeated a pagination bookmark",
			)
		}
		seenBookmarks[next] = struct{}{}
		bookmark = next
	}
	return nil, invalidPinterestResponse(
		"Pinterest board pagination exceeded the safety limit",
	)
}

func (adapter *PinterestAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if err := validatePinterestCredential(credential, true); err != nil {
		return Credential{}, err
	}
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
	}
	var response pinterestTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.tokenURL,
		values,
		"",
		true,
		&response,
	); err != nil {
		return Credential{}, err
	}
	return adapter.parseToken(response)
}

func (adapter *PinterestAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	if !pinterestIDPattern.MatchString(remoteID) {
		return fmt.Errorf("%w: invalid Pinterest board ID", ErrInvalidArgument)
	}
	if err := validatePinterestCredential(credential, false); err != nil {
		return err
	}
	var board pinterestBoard
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.apiBaseURL+"/boards/"+url.PathEscape(remoteID),
		nil,
		credential.AccessToken,
		false,
		&board,
	); err != nil {
		return err
	}
	privacy := strings.ToUpper(strings.TrimSpace(board.Privacy))
	if board.ID != remoteID || strings.TrimSpace(board.Name) == "" ||
		board.IsAdsOnly || privacy == "SECRET" || privacy == "PROTECTED" {
		return &ProviderFailure{
			Kind: FailureResourceGone,
			Code: "pinterest_board_unavailable",
		}
	}
	return nil
}

func (adapter *PinterestAdapter) Revoke(
	context.Context,
	string,
	Credential,
) error {
	// POST /v5/oauth/token/revoke is documented as supporting only tokens
	// issued for system users. Calling it with this adapter's user token would
	// claim unsupported remote revocation and could disclose a credential.
	return ErrExternalRevocationUnavailable
}

func (adapter *PinterestAdapter) parseToken(
	response pinterestTokenResponse,
) (Credential, error) {
	if strings.TrimSpace(response.AccessToken) == "" ||
		strings.TrimSpace(response.RefreshToken) == "" ||
		!strings.EqualFold(strings.TrimSpace(response.TokenType), "bearer") ||
		response.ExpiresIn <= 0 ||
		response.ExpiresIn > int64((1<<63-1)/int64(time.Second)) {
		return Credential{}, invalidPinterestResponse(
			"Pinterest token response is incomplete",
		)
	}
	scopes, err := parsePinterestScopes(response.Scope)
	if err != nil {
		return Credential{}, err
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

func (adapter *PinterestAdapter) requestJSON(
	ctx context.Context,
	method, endpoint string,
	form url.Values,
	bearer string,
	basic bool,
	target any,
) error {
	var body io.Reader = http.NoBody
	if form != nil {
		body = bytes.NewBufferString(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("create Pinterest request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Postqron/0.1")
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if basic {
		request.SetBasicAuth(adapter.clientID, adapter.clientSecret)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := adapter.http.Do(request)
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "pinterest_transport_error",
			Retryable: true,
			Cause:     err,
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxPinterestResponseBytes+1,
	))
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "pinterest_response_read_error",
			Retryable: true,
			Cause:     err,
		}
	}
	if len(payload) > maxPinterestResponseBytes {
		return invalidPinterestResponse("Pinterest response exceeded size limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyPinterestError(response.StatusCode, payload)
	}
	if target == nil {
		return nil
	}
	if len(payload) == 0 || json.Unmarshal(payload, target) != nil {
		return invalidPinterestResponse("Pinterest returned malformed JSON")
	}
	return nil
}

func classifyPinterestError(status int, payload []byte) error {
	var envelope struct {
		Code *int `json:"code"`
	}
	_ = json.Unmarshal(payload, &envelope)
	code := "pinterest_http_" + strconv.Itoa(status)
	if envelope.Code != nil {
		code = "pinterest_error_" + strconv.Itoa(*envelope.Code)
	}
	failure := &ProviderFailure{Code: code}
	switch {
	case status == http.StatusTooManyRequests || status >= 500:
		failure.Kind = FailureTemporary
		failure.Retryable = true
	case status == http.StatusUnauthorized:
		failure.Kind = FailureAuthentication
	case status == http.StatusForbidden:
		failure.Kind = FailurePermissionMissing
	case status == http.StatusNotFound:
		failure.Kind = FailureResourceGone
	default:
		failure.Kind = FailureInvalidResponse
	}
	return failure
}

func parsePinterestScopes(raw string) ([]string, error) {
	parts := strings.FieldsFunc(raw, func(value rune) bool {
		return value == ',' || value == ' ' || value == '\t' ||
			value == '\n' || value == '\r'
	})
	if len(parts) != len(pinterestRequiredScopes) {
		return nil, &ProviderFailure{
			Kind: FailurePermissionMissing,
			Code: "pinterest_scope_missing",
		}
	}
	seen := make(map[string]struct{}, len(parts))
	for _, scope := range parts {
		if _, duplicate := seen[scope]; duplicate {
			return nil, invalidPinterestResponse(
				"Pinterest returned duplicate scopes",
			)
		}
		seen[scope] = struct{}{}
	}
	for _, required := range pinterestRequiredScopes {
		if _, granted := seen[required]; !granted {
			return nil, &ProviderFailure{
				Kind: FailurePermissionMissing,
				Code: "pinterest_scope_missing",
			}
		}
	}
	return append([]string(nil), pinterestRequiredScopes...), nil
}

func validatePinterestCredential(
	credential Credential,
	requireRefresh bool,
) error {
	if strings.TrimSpace(credential.AccessToken) == "" ||
		(requireRefresh && strings.TrimSpace(credential.RefreshToken) == "") {
		return invalidPinterestResponse("Pinterest credential is incomplete")
	}
	if len(credential.Scopes) != len(pinterestRequiredScopes) {
		return &ProviderFailure{
			Kind: FailurePermissionMissing,
			Code: "pinterest_scope_missing",
		}
	}
	granted := make(map[string]struct{}, len(credential.Scopes))
	for _, scope := range credential.Scopes {
		granted[scope] = struct{}{}
	}
	for _, required := range pinterestRequiredScopes {
		if _, ok := granted[required]; !ok {
			return &ProviderFailure{
				Kind: FailurePermissionMissing,
				Code: "pinterest_scope_missing",
			}
		}
	}
	return nil
}

func validatePinterestEndpoint(raw string) error {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf(
			"%w: Pinterest endpoint must be an absolute URL",
			ErrInvalidArgument,
		)
	}
	if endpoint.Scheme == "https" {
		return nil
	}
	host := endpoint.Hostname()
	if endpoint.Scheme == "http" &&
		(strings.EqualFold(host, "localhost") || net.ParseIP(host) != nil &&
			net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return fmt.Errorf(
		"%w: Pinterest endpoint must use HTTPS",
		ErrInvalidArgument,
	)
}

func invalidPinterestResponse(message string) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  "pinterest_invalid_response",
		Cause: errors.New(message),
	}
}
