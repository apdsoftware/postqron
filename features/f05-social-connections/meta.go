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
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxMetaResponseBytes = 2 << 20

var graphVersionPattern = regexp.MustCompile(`^v[1-9][0-9]*\.0$`)

type MetaAdapterConfig struct {
	Provider              Provider
	ClientID              string
	ClientSecret          string
	RedirectURL           string
	GraphVersion          string
	FacebookLoginConfigID string
	SupportsPKCE          bool
	HTTPClient            *http.Client

	// Endpoint overrides exist for contract tests. Production should leave
	// these empty so the official Meta endpoints below are used.
	AuthorizationURL  string
	FacebookGraphURL  string
	InstagramGraphURL string
	InstagramTokenURL string
}

type MetaAdapter struct {
	provider              Provider
	clientID              string
	clientSecret          string
	redirectURL           string
	graphVersion          string
	facebookLoginConfigID string
	supportsPKCE          bool
	http                  *http.Client
	authorizationURL      string
	facebookGraphURL      string
	instagramGraphURL     string
	instagramTokenURL     string
}

func NewMetaAdapter(config MetaAdapterConfig) (*MetaAdapter, error) {
	if config.Provider != ProviderFacebookPages &&
		config.Provider != ProviderInstagramProfessional {
		return nil, ErrUnsupportedProvider
	}
	if strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" {
		return nil, fmt.Errorf("%w: Meta client ID and secret are required", ErrInvalidArgument)
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" {
		return nil, fmt.Errorf("%w: Meta redirect URL must use HTTPS", ErrInvalidArgument)
	}
	if !graphVersionPattern.MatchString(config.GraphVersion) {
		return nil, fmt.Errorf(
			"%w: an explicit Meta Graph version such as v25.0 is required",
			ErrInvalidArgument,
		)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	facebookGraphURL := strings.TrimRight(config.FacebookGraphURL, "/")
	if facebookGraphURL == "" {
		facebookGraphURL = "https://graph.facebook.com"
	}
	instagramGraphURL := strings.TrimRight(config.InstagramGraphURL, "/")
	if instagramGraphURL == "" {
		instagramGraphURL = "https://graph.instagram.com"
	}
	instagramTokenURL := strings.TrimRight(config.InstagramTokenURL, "/")
	if instagramTokenURL == "" {
		instagramTokenURL = "https://api.instagram.com"
	}
	authorizationURL := config.AuthorizationURL
	if authorizationURL == "" {
		if config.Provider == ProviderFacebookPages {
			authorizationURL = "https://www.facebook.com/" +
				config.GraphVersion + "/dialog/oauth"
		} else {
			authorizationURL = "https://www.instagram.com/oauth/authorize"
		}
	}
	return &MetaAdapter{
		provider:              config.Provider,
		clientID:              config.ClientID,
		clientSecret:          config.ClientSecret,
		redirectURL:           config.RedirectURL,
		graphVersion:          config.GraphVersion,
		facebookLoginConfigID: config.FacebookLoginConfigID,
		supportsPKCE:          config.SupportsPKCE,
		http:                  config.HTTPClient,
		authorizationURL:      authorizationURL,
		facebookGraphURL:      facebookGraphURL,
		instagramGraphURL:     instagramGraphURL,
		instagramTokenURL:     instagramTokenURL,
	}, nil
}

func (adapter *MetaAdapter) Config() OAuthConfig {
	extra := make(map[string]string)
	if adapter.provider == ProviderFacebookPages &&
		adapter.facebookLoginConfigID != "" {
		extra["config_id"] = adapter.facebookLoginConfigID
	}
	if adapter.provider == ProviderInstagramProfessional {
		extra["enable_fb_login"] = "0"
		extra["force_authentication"] = "1"
	}
	return OAuthConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), requiredScopes[adapter.provider]...),
		SupportsPKCE:     adapter.supportsPKCE,
		ExtraParameters:  extra,
	}
}

func (adapter *MetaAdapter) AdapterCapabilities() AdapterCapabilities {
	capabilities := AdapterCapabilities{
		Authorization:     true,
		PKCE:              adapter.supportsPKCE,
		ResourceSelection: true,
	}
	if adapter.provider == ProviderInstagramProfessional {
		capabilities.TokenRefresh = true
		capabilities.RemoteRevocation = true
	}
	return capabilities
}

func (adapter *MetaAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	if adapter.provider == ProviderFacebookPages {
		return adapter.exchangeFacebook(ctx, request)
	}
	return adapter.exchangeInstagram(ctx, request)
}

func (adapter *MetaAdapter) Discover(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	if adapter.provider == ProviderFacebookPages {
		return adapter.discoverFacebookPages(ctx, grant)
	}
	return adapter.discoverInstagramProfessional(ctx, grant)
}

func (adapter *MetaAdapter) Refresh(
	ctx context.Context,
	credential Credential,
) (Credential, error) {
	if adapter.provider == ProviderFacebookPages {
		// A Page token is derived from the selected Page and is not refreshed
		// independently. An invalid Page token requires an explicit new grant.
		return Credential{}, ErrNotRefreshable
	}
	values := url.Values{
		"grant_type":   {"ig_refresh_token"},
		"access_token": {credential.AccessToken},
	}
	var response metaTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.instagramGraphURL+"/refresh_access_token?"+values.Encode(),
		nil,
		"",
		&response,
	); err != nil {
		return Credential{}, err
	}
	if response.AccessToken == "" || response.ExpiresIn <= 0 {
		return Credential{}, invalidMetaResponse("Instagram refresh response is incomplete")
	}
	expiresAt := time.Now().UTC().Add(time.Duration(response.ExpiresIn) * time.Second)
	return Credential{
		AccessToken: response.AccessToken,
		ExpiresAt:   &expiresAt,
		Scopes:      append([]string(nil), credential.Scopes...),
	}, nil
}

func (adapter *MetaAdapter) Verify(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	if adapter.provider == ProviderFacebookPages {
		var response struct {
			ID    string   `json:"id"`
			Tasks []string `json:"tasks"`
		}
		endpoint := adapter.facebookGraphURL + "/" + adapter.graphVersion +
			"/" + url.PathEscape(remoteID) + "?fields=id,tasks"
		if err := adapter.requestJSON(
			ctx,
			http.MethodGet,
			endpoint,
			nil,
			credential.AccessToken,
			&response,
		); err != nil {
			return err
		}
		if response.ID != remoteID {
			return &ProviderFailure{
				Kind: FailureResourceGone,
				Code: "meta_resource_gone",
			}
		}
		if !slices.Contains(response.Tasks, "CREATE_CONTENT") {
			return &ProviderFailure{
				Kind: FailurePermissionMissing,
				Code: "meta_create_content_missing",
			}
		}
		return nil
	}
	var response struct {
		ID          string `json:"id"`
		UserID      string `json:"user_id"`
		AccountType string `json:"account_type"`
	}
	endpoint := adapter.instagramGraphURL + "/" + adapter.graphVersion +
		"/" + url.PathEscape(remoteID) + "?fields=id,user_id,account_type"
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		endpoint,
		nil,
		credential.AccessToken,
		&response,
	); err != nil {
		return err
	}
	if firstNonEmpty(response.UserID, response.ID) != remoteID {
		return &ProviderFailure{Kind: FailureResourceGone, Code: "meta_resource_gone"}
	}
	accountType := strings.ToUpper(response.AccountType)
	if accountType != "BUSINESS" && accountType != "CREATOR" {
		return &ProviderFailure{
			Kind: FailurePermissionMissing,
			Code: "meta_professional_account_required",
		}
	}
	return nil
}

func (adapter *MetaAdapter) Revoke(
	ctx context.Context,
	remoteID string,
	credential Credential,
) error {
	if adapter.provider == ProviderFacebookPages {
		// Facebook Login grants may back multiple selected Pages. Revoking the
		// user grant for one Page would disconnect the others, so F5 performs
		// guaranteed local credential deletion instead.
		return ErrExternalRevocationUnavailable
	}
	endpoint := adapter.instagramGraphURL + "/" + adapter.graphVersion +
		"/" + url.PathEscape(remoteID) + "/permissions"
	var response struct {
		Success bool `json:"success"`
	}
	if err := adapter.requestJSON(
		ctx,
		http.MethodDelete,
		endpoint,
		nil,
		credential.AccessToken,
		&response,
	); err != nil {
		return err
	}
	if !response.Success {
		return invalidMetaResponse("Instagram revocation was not confirmed")
	}
	return nil
}

type metaTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	UserID      int64  `json:"user_id"`
}

func (adapter *MetaAdapter) exchangeFacebook(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	values := url.Values{
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"redirect_uri":  {request.RedirectURL},
		"code":          {request.Code},
	}
	if request.PKCEVerifier != "" {
		values.Set("code_verifier", request.PKCEVerifier)
	}
	var short metaTokenResponse
	endpoint := adapter.facebookGraphURL + "/" + adapter.graphVersion +
		"/oauth/access_token?" + values.Encode()
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		endpoint,
		nil,
		"",
		&short,
	); err != nil {
		return Credential{}, err
	}
	if short.AccessToken == "" {
		return Credential{}, invalidMetaResponse("Facebook token response is incomplete")
	}
	longValues := url.Values{
		"grant_type":        {"fb_exchange_token"},
		"client_id":         {adapter.clientID},
		"client_secret":     {adapter.clientSecret},
		"fb_exchange_token": {short.AccessToken},
	}
	var long metaTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.facebookGraphURL+"/"+adapter.graphVersion+
			"/oauth/access_token?"+longValues.Encode(),
		nil,
		"",
		&long,
	); err != nil {
		return Credential{}, err
	}
	if long.AccessToken == "" {
		return Credential{}, invalidMetaResponse("Facebook long-lived token is missing")
	}
	var expiresAt *time.Time
	if long.ExpiresIn > 0 {
		value := time.Now().UTC().Add(time.Duration(long.ExpiresIn) * time.Second)
		expiresAt = &value
	}
	return Credential{
		AccessToken: long.AccessToken,
		ExpiresAt:   expiresAt,
		Scopes:      append([]string(nil), requiredScopes[ProviderFacebookPages]...),
	}, nil
}

func (adapter *MetaAdapter) exchangeInstagram(
	ctx context.Context,
	request ExchangeRequest,
) (Credential, error) {
	values := url.Values{
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {request.RedirectURL},
		"code":          {request.Code},
	}
	if request.PKCEVerifier != "" {
		values.Set("code_verifier", request.PKCEVerifier)
	}
	var short metaTokenResponse
	body := []byte(values.Encode())
	if err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.instagramTokenURL+"/oauth/access_token",
		body,
		"",
		&short,
	); err != nil {
		return Credential{}, err
	}
	if short.AccessToken == "" {
		return Credential{}, invalidMetaResponse("Instagram token response is incomplete")
	}
	longValues := url.Values{
		"grant_type":    {"ig_exchange_token"},
		"client_secret": {adapter.clientSecret},
		"access_token":  {short.AccessToken},
	}
	var long metaTokenResponse
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.instagramGraphURL+"/access_token?"+longValues.Encode(),
		nil,
		"",
		&long,
	); err != nil {
		return Credential{}, err
	}
	if long.AccessToken == "" || long.ExpiresIn <= 0 {
		return Credential{}, invalidMetaResponse("Instagram long-lived token is incomplete")
	}
	expiresAt := time.Now().UTC().Add(time.Duration(long.ExpiresIn) * time.Second)
	return Credential{
		AccessToken: long.AccessToken,
		ExpiresAt:   &expiresAt,
		Scopes: append(
			[]string(nil),
			requiredScopes[ProviderInstagramProfessional]...,
		),
	}, nil
}

func (adapter *MetaAdapter) discoverFacebookPages(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	type page struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		AccessToken string   `json:"access_token"`
		Tasks       []string `json:"tasks"`
		Picture     struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"picture"`
	}
	var response struct {
		Data   []page `json:"data"`
		Paging struct {
			Next string `json:"next"`
		} `json:"paging"`
	}
	endpoint := adapter.facebookGraphURL + "/" + adapter.graphVersion +
		"/me/accounts?fields=id,name,picture,tasks,access_token"
	var resources []DiscoveredResource
	for pageNumber := 0; endpoint != "" && pageNumber < 20; pageNumber++ {
		response = struct {
			Data   []page `json:"data"`
			Paging struct {
				Next string `json:"next"`
			} `json:"paging"`
		}{}
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
		for _, page := range response.Data {
			if page.ID == "" || page.Name == "" || page.AccessToken == "" ||
				!slices.Contains(page.Tasks, "CREATE_CONTENT") {
				continue
			}
			scopes := append([]string(nil), requiredScopes[ProviderFacebookPages]...)
			resources = append(resources, DiscoveredResource{
				Candidate: Candidate{
					RemoteID:     page.ID,
					ResourceType: ResourceFacebookPage,
					AccountType:  AccountTypePage,
					DisplayName:  page.Name,
					PictureURL:   page.Picture.Data.URL,
					Scopes:       scopes,
				},
				Credential: Credential{
					AccessToken: page.AccessToken,
					Scopes:      scopes,
				},
			})
		}
		next, err := adapter.validNextPage(response.Paging.Next)
		if err != nil {
			return nil, err
		}
		endpoint = next
	}
	return resources, nil
}

func (adapter *MetaAdapter) discoverInstagramProfessional(
	ctx context.Context,
	grant Credential,
) ([]DiscoveredResource, error) {
	var response struct {
		ID                string `json:"id"`
		UserID            string `json:"user_id"`
		Username          string `json:"username"`
		Name              string `json:"name"`
		AccountType       string `json:"account_type"`
		ProfilePictureURL string `json:"profile_picture_url"`
	}
	endpoint := adapter.instagramGraphURL + "/" + adapter.graphVersion +
		"/me?fields=id,user_id,username,name,account_type,profile_picture_url"
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
	remoteID := firstNonEmpty(response.UserID, response.ID)
	accountType := strings.ToUpper(response.AccountType)
	if remoteID == "" || response.Username == "" ||
		(accountType != "BUSINESS" && accountType != "CREATOR") {
		return nil, nil
	}
	normalizedType := AccountTypeBusiness
	if accountType == "CREATOR" {
		normalizedType = AccountTypeCreator
	}
	scopes := append(
		[]string(nil),
		requiredScopes[ProviderInstagramProfessional]...,
	)
	return []DiscoveredResource{{
		Candidate: Candidate{
			RemoteID:     remoteID,
			ResourceType: ResourceInstagramProfessional,
			AccountType:  normalizedType,
			DisplayName:  firstNonEmpty(response.Name, response.Username),
			Handle:       response.Username,
			PictureURL:   response.ProfilePictureURL,
			Scopes:       scopes,
		},
		Credential: Credential{
			AccessToken: grant.AccessToken,
			ExpiresAt:   cloneTimePointer(grant.ExpiresAt),
			Scopes:      scopes,
		},
	}}, nil
}

func (adapter *MetaAdapter) validNextPage(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	next, err := url.Parse(raw)
	if err != nil {
		return "", invalidMetaResponse("invalid Meta pagination URL")
	}
	base, _ := url.Parse(adapter.facebookGraphURL)
	if next.Scheme != "https" && base.Scheme == "https" {
		return "", invalidMetaResponse("Meta pagination URL must use HTTPS")
	}
	if !strings.EqualFold(next.Host, base.Host) {
		return "", invalidMetaResponse("Meta pagination URL changed host")
	}
	return next.String(), nil
}

func (adapter *MetaAdapter) requestJSON(
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
		return fmt.Errorf("create Meta request: %w", err)
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
			Code:      "meta_transport_error",
			Retryable: true,
			Cause:     err,
		}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxMetaResponseBytes))
	if err != nil {
		return &ProviderFailure{
			Kind:      FailureTemporary,
			Code:      "meta_response_read_error",
			Retryable: true,
			Cause:     err,
		}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyMetaError(response.StatusCode, payload)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return invalidMetaResponse("Meta returned malformed JSON")
	}
	return nil
}

func classifyMetaError(status int, payload []byte) error {
	var envelope struct {
		Error struct {
			Code    int `json:"code"`
			Subcode int `json:"error_subcode"`
		} `json:"error"`
	}
	_ = json.Unmarshal(payload, &envelope)
	code := envelope.Error.Code
	stableCode := "meta_http_" + strconv.Itoa(status)
	if code != 0 {
		stableCode = "meta_error_" + strconv.Itoa(code)
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

func invalidMetaResponse(message string) error {
	return &ProviderFailure{
		Kind:  FailureInvalidResponse,
		Code:  "meta_invalid_response",
		Cause: errors.New(message),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
