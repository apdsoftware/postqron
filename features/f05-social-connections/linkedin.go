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
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	linkedInRestliVersion = "2.0.0"
	maxLinkedInPages      = 100
	maxLinkedInResponse   = 2 << 20
)

var (
	linkedInVersionPattern      = regexp.MustCompile(`^20[0-9]{4}$`)
	linkedInOrganizationPattern = regexp.MustCompile(`^urn:li:organization:([0-9]+)$`)
	linkedInPersonPattern       = regexp.MustCompile(`^urn:li:person:([^:]+)$`)
	linkedInAtomicScopes        = []string{
		"openid",
		"profile",
		"w_member_social",
		"rw_organization_admin",
		"w_organization_social",
	}
)

type LinkedInAdapterConfig struct {
	ClientID                    string
	ClientSecret                string
	RedirectURL                 string
	APIVersion                  string
	ProgrammaticRefreshApproved bool
	HTTPClient                  *http.Client

	// Endpoint overrides are only for offline contract fixtures.
	AuthorizationURL string
	TokenURL         string
	UserInfoURL      string
	APIBaseURL       string
}

type LinkedInAdapter struct {
	clientID                    string
	clientSecret                string
	redirectURL                 string
	apiVersion                  string
	programmaticRefreshApproved bool
	http                        *http.Client
	authorizationURL            string
	tokenURL                    string
	userInfoURL                 string
	apiBaseURL                  string
}

func NewLinkedInAdapter(config LinkedInAdapterConfig) (*LinkedInAdapter, error) {
	if strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf(
			"%w: LinkedIn client ID and secret are required",
			ErrInvalidArgument,
		)
	}
	if err := validateHTTPSRedirect(config.RedirectURL, "LinkedIn"); err != nil {
		return nil, err
	}
	if !linkedInVersionPattern.MatchString(config.APIVersion) {
		return nil, fmt.Errorf(
			"%w: LinkedIn API version must use YYYYMM",
			ErrInvalidArgument,
		)
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
	authorizationURL := endpointOrDefault(
		config.AuthorizationURL,
		"https://www.linkedin.com/oauth/v2/authorization",
	)
	tokenURL := endpointOrDefault(
		config.TokenURL,
		"https://www.linkedin.com/oauth/v2/accessToken",
	)
	userInfoURL := endpointOrDefault(
		config.UserInfoURL,
		"https://api.linkedin.com/v2/userinfo",
	)
	apiBaseURL := strings.TrimRight(endpointOrDefault(
		config.APIBaseURL,
		"https://api.linkedin.com",
	), "/")
	if err := validateHTTPSEndpoints(
		"LinkedIn",
		authorizationURL,
		tokenURL,
		userInfoURL,
		apiBaseURL,
	); err != nil {
		return nil, err
	}
	return &LinkedInAdapter{
		clientID:                    strings.TrimSpace(config.ClientID),
		clientSecret:                strings.TrimSpace(config.ClientSecret),
		redirectURL:                 config.RedirectURL,
		apiVersion:                  config.APIVersion,
		programmaticRefreshApproved: config.ProgrammaticRefreshApproved,
		http:                        config.HTTPClient,
		authorizationURL:            authorizationURL,
		tokenURL:                    tokenURL,
		userInfoURL:                 userInfoURL,
		apiBaseURL:                  apiBaseURL,
	}, nil
}

func (adapter *LinkedInAdapter) Config() OAuthConfig {
	return OAuthConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), linkedInAtomicScopes...),
		ScopeSeparator:   OAuthScopeSeparatorSpace,
		SupportsPKCE:     false,
	}
}

func (adapter *LinkedInAdapter) AdapterCapabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Authorization:     true,
		ResourceSelection: true,
		TokenRefresh:      adapter.programmaticRefreshApproved,
	}
}

func (adapter *LinkedInAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if strings.TrimSpace(request.Code) == "" ||
		request.RedirectURL != adapter.redirectURL ||
		request.PKCEVerifier != "" {
		return Credential{}, fmt.Errorf(
			"%w: invalid LinkedIn authorization exchange",
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
	if adapter.programmaticRefreshApproved && token.RefreshToken == "" {
		return Credential{}, invalidLinkedInResponse(
			"approved programmatic refresh token is missing",
		)
	}
	if !adapter.programmaticRefreshApproved {
		token.RefreshToken = ""
	}
	return token.credential(), nil
}

func (adapter *LinkedInAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if !adapter.programmaticRefreshApproved ||
		strings.TrimSpace(credential.RefreshToken) == "" {
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
	if token.RefreshToken == "" {
		token.RefreshToken = credential.RefreshToken
	}
	return token.credential(), nil
}

func (adapter *LinkedInAdapter) Discover(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	member, err := adapter.memberResource(ctx, grant)
	if err != nil {
		return nil, err
	}
	organizations, err := adapter.organizationResources(ctx, grant)
	if err != nil {
		return nil, err
	}
	return append([]DiscoveredResource{member}, organizations...), nil
}

func (adapter *LinkedInAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	if linkedInPersonPattern.MatchString(remoteID) {
		resource, err := adapter.memberResource(ctx, credential)
		if err != nil {
			return err
		}
		if resource.Candidate.RemoteID != remoteID {
			return &ProviderFailure{
				Kind: FailureResourceGone,
				Code: "linkedin_member_changed",
			}
		}
		return nil
	}
	if !linkedInOrganizationPattern.MatchString(remoteID) {
		return invalidLinkedInResponse("invalid LinkedIn resource URN")
	}
	organizations, err := adapter.organizationURNs(ctx, credential.AccessToken)
	if err != nil {
		return err
	}
	if _, ok := organizations[remoteID]; !ok {
		return &ProviderFailure{
			Kind: FailurePermissionMissing,
			Code: "linkedin_organization_role_missing",
		}
	}
	_, err = adapter.organizationResource(ctx, remoteID, credential)
	return err
}

func (adapter *LinkedInAdapter) Revoke(
	context.Context,
	string,
	Credential,
) error {
	// LinkedIn does not document a per-resource revocation endpoint. Local
	// credential deletion is guaranteed by F5.
	return ErrExternalRevocationUnavailable
}

type linkedInTokenResponse struct {
	AccessToken           string `json:"access_token"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
	Scope                 string `json:"scope"`
	TokenType             string `json:"token_type"`
}

func (token linkedInTokenResponse) credential() Credential {
	expiresAt := time.Now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	return Credential{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    &expiresAt,
		Scopes:       append([]string(nil), linkedInAtomicScopes...),
	}
}

func (adapter *LinkedInAdapter) exchangeToken(
	ctx context.Context,
	values url.Values,
) (linkedInTokenResponse, error) {
	var token linkedInTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.tokenURL,
		[]byte(values.Encode()),
		"",
		false,
		&token,
	); err != nil {
		return linkedInTokenResponse{}, err
	}
	if token.AccessToken == "" || token.ExpiresIn <= 0 ||
		(token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer")) {
		return linkedInTokenResponse{}, invalidLinkedInResponse(
			"LinkedIn token response is incomplete",
		)
	}
	// OAuth 2.0 allows `scope` to be omitted when it is identical to the
	// requested grant. If LinkedIn returns it, require the complete atomic set.
	if strings.TrimSpace(token.Scope) != "" {
		if err := validateAtomicScopeGrant(
			token.Scope,
			linkedInAtomicScopes,
		); err != nil {
			return linkedInTokenResponse{}, &ProviderFailure{
				Kind:  FailurePermissionMissing,
				Code:  "linkedin_scope_missing",
				Cause: err,
			}
		}
	}
	return token, nil
}

func (adapter *LinkedInAdapter) memberResource(
	ctx context.Context,
	grant Credential,
) (DiscoveredResource, error) {
	var profile struct {
		Subject string `json:"sub"`
		Name    string `json:"name"`
		Given   string `json:"given_name"`
		Family  string `json:"family_name"`
		Picture string `json:"picture"`
	}
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.userInfoURL,
		nil,
		grant.AccessToken,
		false,
		&profile,
	); err != nil {
		return DiscoveredResource{}, err
	}
	name := strings.TrimSpace(firstNonEmpty(
		profile.Name,
		strings.TrimSpace(profile.Given+" "+profile.Family),
	))
	if profile.Subject == "" || name == "" || strings.Contains(profile.Subject, ":") {
		return DiscoveredResource{}, invalidLinkedInResponse(
			"LinkedIn userinfo response is incomplete",
		)
	}
	scopes := append([]string(nil), linkedInAtomicScopes...)
	return DiscoveredResource{
		Candidate: Candidate{
			RemoteID:     "urn:li:person:" + profile.Subject,
			ResourceType: ResourceLinkedInProfile,
			AccountType:  AccountTypeProfile,
			DisplayName:  name,
			PictureURL:   profile.Picture,
			Scopes:       append([]string(nil), scopes...),
		},
		Credential: Credential{
			AccessToken:  grant.AccessToken,
			RefreshToken: grant.RefreshToken,
			ExpiresAt:    cloneTimePointer(grant.ExpiresAt),
			Scopes:       scopes,
		},
	}, nil
}

func (adapter *LinkedInAdapter) organizationResources(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	urns, err := adapter.organizationURNs(ctx, grant.AccessToken)
	if err != nil {
		return nil, err
	}
	resources := make([]DiscoveredResource, 0, len(urns))
	orderedURNs := make([]string, 0, len(urns))
	for urn := range urns {
		orderedURNs = append(orderedURNs, urn)
	}
	sort.Strings(orderedURNs)
	for _, urn := range orderedURNs {
		resource, resourceErr := adapter.organizationResource(ctx, urn, grant)
		if resourceErr != nil {
			return nil, resourceErr
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

func (adapter *LinkedInAdapter) organizationURNs(
	ctx context.Context,
	accessToken string,
) (map[string]struct{}, error) {
	type acl struct {
		Organization       string `json:"organization"`
		OrganizationTarget string `json:"organizationTarget"`
		Role               string `json:"role"`
		State              string `json:"state"`
	}
	type page struct {
		Elements []acl `json:"elements"`
		Paging   struct {
			Start int `json:"start"`
			Count int `json:"count"`
			Total int `json:"total"`
			Links []struct {
				Relation string `json:"rel"`
				Href     string `json:"href"`
			} `json:"links"`
		} `json:"paging"`
	}
	result := make(map[string]struct{})
	start := 0
	for pageNumber := 0; pageNumber < maxLinkedInPages; pageNumber++ {
		query := url.Values{
			"q":     {"roleAssignee"},
			"state": {"APPROVED"},
			"start": {strconv.Itoa(start)},
			"count": {"100"},
		}
		var response page
		if err := adapter.requestJSON(
			ctx,
			http.MethodGet,
			adapter.apiBaseURL+"/rest/organizationAcls?"+query.Encode(),
			nil,
			accessToken,
			true,
			&response,
		); err != nil {
			return nil, err
		}
		for _, entry := range response.Elements {
			if entry.State != "APPROVED" || !linkedInPublishingRole(entry.Role) {
				continue
			}
			organization := firstNonEmpty(
				entry.Organization,
				entry.OrganizationTarget,
			)
			if !linkedInOrganizationPattern.MatchString(organization) {
				return nil, invalidLinkedInResponse(
					"LinkedIn returned an invalid organization URN",
				)
			}
			result[organization] = struct{}{}
		}
		next, hasNext, nextErr := adapter.nextOrganizationACLStart(
			response.Paging.Links,
			start,
		)
		if nextErr != nil {
			return nil, nextErr
		}
		if hasNext {
			start = next
			continue
		}
		if len(response.Elements) == 0 ||
			response.Paging.Total <= 0 ||
			start+len(response.Elements) >= response.Paging.Total {
			return result, nil
		}
		next = start + len(response.Elements)
		if response.Paging.Count > len(response.Elements) {
			next = start + response.Paging.Count
		}
		if next <= start {
			return nil, invalidLinkedInResponse(
				"LinkedIn pagination did not advance",
			)
		}
		start = next
	}
	return nil, invalidLinkedInResponse("LinkedIn pagination limit exceeded")
}

func (adapter *LinkedInAdapter) nextOrganizationACLStart(
	links []struct {
		Relation string `json:"rel"`
		Href     string `json:"href"`
	},
	current int,
) (int, bool, error) {
	var nextHref string
	for _, link := range links {
		if !strings.EqualFold(strings.TrimSpace(link.Relation), "next") {
			continue
		}
		if nextHref != "" || strings.TrimSpace(link.Href) == "" {
			return 0, false, invalidLinkedInResponse(
				"LinkedIn returned invalid pagination links",
			)
		}
		nextHref = link.Href
	}
	if nextHref == "" {
		return 0, false, nil
	}
	base, err := url.Parse(adapter.apiBaseURL)
	if err != nil {
		return 0, false, invalidLinkedInResponse(
			"LinkedIn API base URL is invalid",
		)
	}
	reference, err := url.Parse(nextHref)
	if err != nil {
		return 0, false, invalidLinkedInResponse(
			"LinkedIn returned an invalid pagination URL",
		)
	}
	nextURL := base.ResolveReference(reference)
	if nextURL.Scheme != base.Scheme ||
		nextURL.Host != base.Host ||
		nextURL.User != nil ||
		nextURL.Path != "/rest/organizationAcls" {
		return 0, false, invalidLinkedInResponse(
			"LinkedIn returned an unsafe pagination URL",
		)
	}
	query := nextURL.Query()
	if query.Get("q") != "roleAssignee" ||
		query.Get("state") != "APPROVED" {
		return 0, false, invalidLinkedInResponse(
			"LinkedIn returned invalid pagination parameters",
		)
	}
	next, err := strconv.Atoi(query.Get("start"))
	if err != nil || next <= current {
		return 0, false, invalidLinkedInResponse(
			"LinkedIn pagination did not advance",
		)
	}
	return next, true, nil
}

func linkedInPublishingRole(role string) bool {
	switch role {
	case "ADMINISTRATOR",
		"DIRECT_SPONSORED_CONTENT_POSTER",
		"CONTENT_ADMIN",
		"CONTENT_ADMINISTRATOR":
		return true
	default:
		return false
	}
}

func (adapter *LinkedInAdapter) organizationResource(
	ctx context.Context,
	urn string,
	grant Credential,
) (DiscoveredResource, error) {
	match := linkedInOrganizationPattern.FindStringSubmatch(urn)
	if len(match) != 2 {
		return DiscoveredResource{}, invalidLinkedInResponse(
			"invalid LinkedIn organization URN",
		)
	}
	var organization struct {
		ID            json.RawMessage `json:"id"`
		LocalizedName string          `json:"localizedName"`
		VanityName    string          `json:"vanityName"`
	}
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.apiBaseURL+"/rest/organizations/"+url.PathEscape(match[1]),
		nil,
		grant.AccessToken,
		true,
		&organization,
	); err != nil {
		return DiscoveredResource{}, err
	}
	id := strings.Trim(string(organization.ID), `"`)
	if id != match[1] || strings.TrimSpace(organization.LocalizedName) == "" {
		return DiscoveredResource{}, invalidLinkedInResponse(
			"LinkedIn organization response is incomplete",
		)
	}
	scopes := append([]string(nil), linkedInAtomicScopes...)
	return DiscoveredResource{
		Candidate: Candidate{
			RemoteID:     urn,
			ResourceType: ResourceLinkedInPage,
			AccountType:  AccountTypeOrganization,
			DisplayName:  organization.LocalizedName,
			Handle:       organization.VanityName,
			Scopes:       append([]string(nil), scopes...),
		},
		Credential: Credential{
			AccessToken:  grant.AccessToken,
			RefreshToken: grant.RefreshToken,
			ExpiresAt:    cloneTimePointer(grant.ExpiresAt),
			Scopes:       scopes,
		},
	}, nil
}

func (adapter *LinkedInAdapter) requestJSON(
	ctx context.Context,
	method, endpoint string,
	body []byte,
	bearer string,
	marketing bool,
	target any,
) error {
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create LinkedIn request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Postqron/0.1")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if marketing {
		request.Header.Set("Linkedin-Version", adapter.apiVersion)
		request.Header.Set("X-Restli-Protocol-Version", linkedInRestliVersion)
	}
	response, err := adapter.http.Do(request)
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "linkedin_transport_error",
			Retryable: true,
			Cause:     err,
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxLinkedInResponse))
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "linkedin_response_read_error",
			Retryable: true,
			Cause:     err,
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyLinkedInError(response.StatusCode, payload)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return invalidLinkedInResponse("LinkedIn returned malformed JSON")
	}
	return nil
}

func classifyLinkedInError(status int, payload []byte) error {
	failure := &ProviderFailure{
		Code: "linkedin_http_" + strconv.Itoa(status),
	}
	switch {
	case status == http.StatusTooManyRequests || status >= 500:
		failure.Kind = FailureTemporary
		failure.Retryable = true
	case status == http.StatusBadRequest && linkedInOAuthAuthenticationFailure(payload):
		failure.Kind = FailureAuthentication
		failure.Code = "linkedin_authentication_failed"
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

func linkedInOAuthAuthenticationFailure(payload []byte) bool {
	var oauthFailure struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(payload, &oauthFailure)
	if linkedInOAuthAuthenticationText(oauthFailure.Error) {
		return true
	}
	return linkedInOAuthAuthenticationText(oauthFailure.Message) ||
		linkedInOAuthAuthenticationText(string(payload))
}

func linkedInOAuthAuthenticationText(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return false
	}
	if normalized == "invalid_grant" || normalized == "invalid_request" {
		return true
	}
	return strings.Contains(normalized, "token") &&
		(strings.Contains(normalized, "invalid") ||
			strings.Contains(normalized, "expired") ||
			strings.Contains(normalized, "revoked"))
}

func invalidLinkedInResponse(message string) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  "linkedin_invalid_response",
		Cause: errors.New(message),
	}
}

func validateAtomicScopeGrant(raw string, required []string) error {
	granted := make(map[string]struct{})
	for _, scope := range strings.Fields(raw) {
		granted[scope] = struct{}{}
	}
	for _, scope := range required {
		if _, ok := granted[scope]; !ok {
			return fmt.Errorf("required scope %s is missing", scope)
		}
	}
	return nil
}

func validateHTTPSRedirect(raw, provider string) error {
	redirect, err := url.Parse(raw)
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" {
		return fmt.Errorf(
			"%w: %s redirect URL must use HTTPS",
			ErrInvalidArgument,
			provider,
		)
	}
	return nil
}

func validateHTTPSEndpoints(provider string, endpoints ...string) error {
	for _, raw := range endpoints {
		endpoint, err := url.Parse(raw)
		if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
			return fmt.Errorf(
				"%w: %s endpoints must use HTTPS",
				ErrInvalidArgument,
				provider,
			)
		}
	}
	return nil
}

func endpointOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
