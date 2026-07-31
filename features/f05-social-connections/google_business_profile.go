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
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	googleBusinessScope       = "https://www.googleapis.com/auth/business.manage"
	maxGoogleBusinessPages    = 100
	maxGoogleBusinessResponse = 2 << 20
)

var (
	googleAccountPattern  = regexp.MustCompile(`^accounts/([^/]+)$`)
	googleLocationPattern = regexp.MustCompile(`^locations/([^/]+)$`)
	googleRemotePattern   = regexp.MustCompile(`^accounts/([^/]+)/locations/([^/]+)$`)
)

type GoogleBusinessProfileAdapterConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client

	// Endpoint overrides are only for offline contract fixtures.
	AuthorizationURL   string
	TokenURL           string
	AccountAPIBaseURL  string
	BusinessAPIBaseURL string
}

type GoogleBusinessProfileAdapter struct {
	clientID           string
	clientSecret       string
	redirectURL        string
	http               *http.Client
	authorizationURL   string
	tokenURL           string
	accountAPIBaseURL  string
	businessAPIBaseURL string
}

func NewGoogleBusinessProfileAdapter(
	config GoogleBusinessProfileAdapterConfig,
) (*GoogleBusinessProfileAdapter, error) {
	if strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf(
			"%w: Google Business Profile client ID and secret are required",
			ErrInvalidArgument,
		)
	}
	if err := validateHTTPSRedirect(
		config.RedirectURL,
		"Google Business Profile",
	); err != nil {
		return nil, err
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(
				*http.Request,
				[]*http.Request,
			) error {
				return http.ErrUseLastResponse
			},
		}
	}
	authorizationURL := linkedInEndpointOrDefault(
		config.AuthorizationURL,
		"https://accounts.google.com/o/oauth2/v2/auth",
	)
	tokenURL := linkedInEndpointOrDefault(
		config.TokenURL,
		"https://oauth2.googleapis.com/token",
	)
	accountAPIBaseURL := strings.TrimRight(linkedInEndpointOrDefault(
		config.AccountAPIBaseURL,
		"https://mybusinessaccountmanagement.googleapis.com",
	), "/")
	businessAPIBaseURL := strings.TrimRight(linkedInEndpointOrDefault(
		config.BusinessAPIBaseURL,
		"https://mybusinessbusinessinformation.googleapis.com",
	), "/")
	if err := validateHTTPSEndpoints(
		"Google Business Profile",
		authorizationURL,
		tokenURL,
		accountAPIBaseURL,
		businessAPIBaseURL,
	); err != nil {
		return nil, err
	}
	return &GoogleBusinessProfileAdapter{
		clientID:           strings.TrimSpace(config.ClientID),
		clientSecret:       strings.TrimSpace(config.ClientSecret),
		redirectURL:        config.RedirectURL,
		http:               config.HTTPClient,
		authorizationURL:   authorizationURL,
		tokenURL:           tokenURL,
		accountAPIBaseURL:  accountAPIBaseURL,
		businessAPIBaseURL: businessAPIBaseURL,
	}, nil
}

func (adapter *GoogleBusinessProfileAdapter) Config() OAuthConfig {
	return OAuthConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           []string{googleBusinessScope},
		SupportsPKCE:     false,
		ExtraParameters: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
	}
}

func (adapter *GoogleBusinessProfileAdapter) AdapterCapabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		ResourceSelection: true,
		TokenRefresh:      true,
		RemoteRevocation:  false,
	}
}

func (adapter *GoogleBusinessProfileAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if strings.TrimSpace(request.Code) == "" ||
		request.RedirectURL != adapter.redirectURL ||
		request.PKCEVerifier != "" {
		return Credential{}, fmt.Errorf(
			"%w: invalid Google Business Profile authorization exchange",
			ErrInvalidArgument,
		)
	}
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {request.Code},
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"redirect_uri":  {request.RedirectURL},
	}
	token, err := adapter.exchangeToken(ctx, values)
	if err != nil {
		return Credential{}, err
	}
	if token.RefreshToken == "" {
		return Credential{}, invalidGoogleBusinessResponse(
			"Google offline token response lacks a refresh token",
		)
	}
	return token.credential(""), nil
}

func (adapter *GoogleBusinessProfileAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return Credential{}, ErrNotRefreshable
	}
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
	}
	token, err := adapter.exchangeToken(ctx, values)
	if err != nil {
		return Credential{}, err
	}
	return token.credential(credential.RefreshToken), nil
}

func (adapter *GoogleBusinessProfileAdapter) Discover(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	accounts, err := adapter.accounts(ctx, grant.AccessToken)
	if err != nil {
		return nil, err
	}
	var resources []DiscoveredResource
	for _, account := range accounts {
		locations, locationErr := adapter.locations(
			ctx,
			account,
			grant,
		)
		if locationErr != nil {
			return nil, locationErr
		}
		resources = append(resources, locations...)
	}
	return resources, nil
}

func (adapter *GoogleBusinessProfileAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	match := googleRemotePattern.FindStringSubmatch(remoteID)
	if len(match) != 3 {
		return invalidGoogleBusinessResponse(
			"invalid Google Business Profile resource name",
		)
	}
	account := googleBusinessAccount{Name: "accounts/" + match[1]}
	locations, err := adapter.locations(ctx, account, credential)
	if err != nil {
		return err
	}
	for _, location := range locations {
		if location.Candidate.RemoteID == remoteID {
			return nil
		}
	}
	return &ProviderFailure{
		Kind: FailureResourceGone,
		Code: "google_business_location_gone",
	}
}

func (adapter *GoogleBusinessProfileAdapter) Revoke(
	context.Context,
	string,
	Credential,
) error {
	// Google's endpoint revokes every OAuth scope granted to the project and
	// invalidates sibling locations. F5 revocation is per connection, so only
	// guaranteed local deletion is safe here.
	return ErrExternalRevocationUnavailable
}

type googleBusinessTokenResponse struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

func (token googleBusinessTokenResponse) credential(
	existingRefreshToken string,
) Credential {
	refreshToken := token.RefreshToken
	if refreshToken == "" {
		refreshToken = existingRefreshToken
	}
	expiresAt := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	return Credential{
		AccessToken:  token.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    &expiresAt,
		Scopes:       []string{googleBusinessScope},
	}
}

func (adapter *GoogleBusinessProfileAdapter) exchangeToken(
	ctx context.Context,
	values url.Values,
) (googleBusinessTokenResponse, error) {
	var token googleBusinessTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.tokenURL,
		[]byte(values.Encode()),
		"",
		&token,
	); err != nil {
		return googleBusinessTokenResponse{}, err
	}
	if token.AccessToken == "" || token.ExpiresIn <= 0 ||
		(token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer")) {
		return googleBusinessTokenResponse{}, invalidGoogleBusinessResponse(
			"Google token response is incomplete",
		)
	}
	scope := token.Scope
	if scope == "" {
		scope = googleBusinessScope
	}
	if err := validateAtomicScopeGrant(scope, []string{googleBusinessScope}); err != nil {
		return googleBusinessTokenResponse{}, &ProviderFailure{
			Kind:  FailurePermissionMissing,
			Code:  "google_business_scope_missing",
			Cause: err,
		}
	}
	return token, nil
}

type googleBusinessAccount struct {
	Name            string `json:"name"`
	AccountName     string `json:"accountName"`
	Type            string `json:"type"`
	Role            string `json:"role"`
	PermissionLevel string `json:"permissionLevel"`
}

func (adapter *GoogleBusinessProfileAdapter) accounts(
	ctx context.Context,
	accessToken string,
) ([]googleBusinessAccount, error) {
	var accounts []googleBusinessAccount
	pageToken := ""
	for pageNumber := 0; pageNumber < maxGoogleBusinessPages; pageNumber++ {
		query := url.Values{"pageSize": {"20"}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var response struct {
			Accounts      []googleBusinessAccount `json:"accounts"`
			NextPageToken string                  `json:"nextPageToken"`
		}
		if err := adapter.requestJSON(
			ctx,
			http.MethodGet,
			adapter.accountAPIBaseURL+"/v1/accounts?"+query.Encode(),
			nil,
			accessToken,
			&response,
		); err != nil {
			return nil, err
		}
		for _, account := range response.Accounts {
			if !googleAccountPattern.MatchString(account.Name) ||
				strings.TrimSpace(account.AccountName) == "" ||
				!googleBusinessManageRole(account.Role) {
				return nil, invalidGoogleBusinessResponse(
					"Google returned an invalid account",
				)
			}
			accounts = append(accounts, account)
		}
		if response.NextPageToken == "" {
			return accounts, nil
		}
		if response.NextPageToken == pageToken {
			return nil, invalidGoogleBusinessResponse(
				"Google account pagination did not advance",
			)
		}
		pageToken = response.NextPageToken
	}
	return nil, invalidGoogleBusinessResponse(
		"Google account pagination limit exceeded",
	)
}

func googleBusinessManageRole(role string) bool {
	switch role {
	case "PRIMARY_OWNER", "OWNER", "MANAGER", "SITE_MANAGER":
		return true
	default:
		return false
	}
}

func (adapter *GoogleBusinessProfileAdapter) locations(
	ctx context.Context,
	account googleBusinessAccount,
	grant Credential,
) ([]DiscoveredResource, error) {
	accountMatch := googleAccountPattern.FindStringSubmatch(account.Name)
	if len(accountMatch) != 2 {
		return nil, invalidGoogleBusinessResponse("invalid Google account name")
	}
	pageToken := ""
	var resources []DiscoveredResource
	for pageNumber := 0; pageNumber < maxGoogleBusinessPages; pageNumber++ {
		query := url.Values{
			"readMask": {"name,title,storefrontAddress"},
			"pageSize": {"100"},
		}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var response struct {
			Locations []struct {
				Name              string `json:"name"`
				Title             string `json:"title"`
				StorefrontAddress struct {
					Locality   string `json:"locality"`
					RegionCode string `json:"regionCode"`
				} `json:"storefrontAddress"`
			} `json:"locations"`
			NextPageToken string `json:"nextPageToken"`
		}
		endpoint := adapter.businessAPIBaseURL + "/v1/accounts/" +
			url.PathEscape(accountMatch[1]) + "/locations?" + query.Encode()
		if err := adapter.requestJSON(
			ctx,
			http.MethodGet,
			endpoint,
			nil,
			grant.AccessToken,
			&response,
		); err != nil {
			return nil, err
		}
		for _, location := range response.Locations {
			locationMatch := googleLocationPattern.FindStringSubmatch(location.Name)
			if len(locationMatch) != 2 || strings.TrimSpace(location.Title) == "" {
				return nil, invalidGoogleBusinessResponse(
					"Google returned an invalid location",
				)
			}
			addressParts := make([]string, 0, 2)
			for _, part := range []string{
				location.StorefrontAddress.Locality,
				location.StorefrontAddress.RegionCode,
			} {
				if strings.TrimSpace(part) != "" {
					addressParts = append(addressParts, strings.TrimSpace(part))
				}
			}
			handle := strings.Join(addressParts, ", ")
			scopes := []string{googleBusinessScope}
			resources = append(resources, DiscoveredResource{
				Candidate: Candidate{
					RemoteID:     account.Name + "/" + location.Name,
					ResourceType: ResourceGoogleBusinessLocation,
					AccountType:  AccountTypeLocation,
					DisplayName:  location.Title,
					Handle:       handle,
					Scopes:       append([]string(nil), scopes...),
				},
				Credential: Credential{
					AccessToken:  grant.AccessToken,
					RefreshToken: grant.RefreshToken,
					ExpiresAt:    cloneTimePointer(grant.ExpiresAt),
					Scopes:       scopes,
				},
			})
		}
		if response.NextPageToken == "" {
			return resources, nil
		}
		if response.NextPageToken == pageToken {
			return nil, invalidGoogleBusinessResponse(
				"Google location pagination did not advance",
			)
		}
		pageToken = response.NextPageToken
	}
	return nil, invalidGoogleBusinessResponse(
		"Google location pagination limit exceeded",
	)
}

func (adapter *GoogleBusinessProfileAdapter) requestJSON(
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
		return fmt.Errorf("create Google Business Profile request: %w", err)
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
			Code:      "google_business_transport_error",
			Retryable: true,
			Cause:     err,
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxGoogleBusinessResponse,
	))
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "google_business_response_read_error",
			Retryable: true,
			Cause:     err,
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyGoogleBusinessError(response.StatusCode, payload)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return invalidGoogleBusinessResponse("Google returned malformed JSON")
	}
	return nil
}

func classifyGoogleBusinessError(status int, payload []byte) error {
	raw := string(payload)
	failure := &ProviderFailure{
		Code: "google_business_http_" + strconv.Itoa(status),
	}
	switch {
	case status == http.StatusTooManyRequests || status >= 500 ||
		(status == http.StatusForbidden &&
			(strings.Contains(raw, "rateLimitExceeded") ||
				strings.Contains(raw, "userRateLimitExceeded") ||
				strings.Contains(raw, "quotaExceeded"))):
		failure.Kind = FailureTemporary
		failure.Retryable = true
	case status == http.StatusUnauthorized ||
		(status == http.StatusBadRequest &&
			strings.Contains(raw, "invalid_grant")):
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

func invalidGoogleBusinessResponse(message string) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  "google_business_invalid_response",
		Cause: errors.New(message),
	}
}
