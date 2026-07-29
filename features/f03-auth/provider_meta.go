package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type MetaAdapterConfig struct {
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	GraphVersion     string
	AuthorizationURL string
	GraphURL         string
	HTTPClient       *http.Client
}

type MetaAdapter struct {
	clientID         string
	clientSecret     string
	redirectURL      string
	graphVersion     string
	authorizationURL string
	graphURL         string
	httpClient       *http.Client
}

func NewMetaAdapter(config MetaAdapterConfig) (*MetaAdapter, error) {
	if strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" ||
		strings.TrimSpace(config.RedirectURL) == "" ||
		strings.TrimSpace(config.GraphVersion) == "" {
		return nil, errors.New("Meta provider configuration is incomplete")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	authorizationURL := strings.TrimSpace(config.AuthorizationURL)
	if authorizationURL == "" {
		authorizationURL = "https://www.facebook.com/" +
			config.GraphVersion + "/dialog/oauth"
	}
	graphURL := strings.TrimRight(strings.TrimSpace(config.GraphURL), "/")
	if graphURL == "" {
		graphURL = "https://graph.facebook.com"
	}
	return &MetaAdapter{
		clientID:         config.ClientID,
		clientSecret:     config.ClientSecret,
		redirectURL:      config.RedirectURL,
		graphVersion:     config.GraphVersion,
		authorizationURL: authorizationURL,
		graphURL:         graphURL,
		httpClient:       config.HTTPClient,
	}, nil
}

func (adapter *MetaAdapter) Config() ProviderConfig {
	return ProviderConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           []string{"email", "public_profile"},
	}
}

func (adapter *MetaAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (ExternalIdentity, error) {
	values := url.Values{
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
		"code":          {request.Code},
		"redirect_uri":  {request.RedirectURL},
	}
	if strings.TrimSpace(request.PKCEVerifier) != "" {
		values.Set("code_verifier", request.PKCEVerifier)
	}
	var tokenResponse struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := adapter.requestJSON(
		ctx,
		http.MethodPost,
		adapter.graphURL+"/"+adapter.graphVersion+"/oauth/access_token",
		strings.NewReader(values.Encode()),
		"",
		&tokenResponse,
	); err != nil {
		return ExternalIdentity{}, err
	}
	if strings.TrimSpace(tokenResponse.AccessToken) == "" {
		return ExternalIdentity{}, &ProviderError{
			Code:      "meta_missing_access_token",
			Retryable: false,
		}
	}
	if err := adapter.validateToken(ctx, tokenResponse.AccessToken); err != nil {
		return ExternalIdentity{}, err
	}
	var profile struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		adapter.graphURL+"/"+adapter.graphVersion+"/me?fields=id,name,email",
		nil,
		tokenResponse.AccessToken,
		&profile,
	); err != nil {
		return ExternalIdentity{}, err
	}
	if strings.TrimSpace(profile.ID) == "" {
		return ExternalIdentity{}, &ProviderError{
			Code:      "meta_profile_incomplete",
			Retryable: false,
		}
	}
	return ExternalIdentity{
		Subject:       strings.TrimSpace(profile.ID),
		Email:         strings.TrimSpace(profile.Email),
		DisplayName:   strings.TrimSpace(profile.Name),
		EmailVerified: strings.TrimSpace(profile.Email) != "",
		// Meta returns a bearer access token that can be invalidated on unlink.
		RevocationToken: tokenResponse.AccessToken,
	}, nil
}

func (adapter *MetaAdapter) Revoke(ctx context.Context, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		adapter.graphURL+"/"+adapter.graphVersion+"/me/permissions?access_token="+url.QueryEscape(token),
		nil,
	)
	if err != nil {
		return &ProviderError{Code: "meta_revoke_request_invalid", Retryable: false, Cause: err}
	}
	response, err := adapter.httpClient.Do(request)
	if err != nil {
		return &ProviderError{Code: "meta_revoke_failed", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return &ProviderError{
			Code:      "meta_revoke_rejected",
			Retryable: response.StatusCode >= http.StatusInternalServerError,
		}
	}
	return nil
}

func (adapter *MetaAdapter) validateToken(
	ctx context.Context,
	accessToken string,
) error {
	debugURL := adapter.graphURL + "/" + adapter.graphVersion + "/debug_token?" + url.Values{
		"input_token":  {accessToken},
		"access_token": {adapter.clientID + "|" + adapter.clientSecret},
	}.Encode()
	var response struct {
		Data struct {
			AppID   string   `json:"app_id"`
			IsValid bool     `json:"is_valid"`
			Scopes  []string `json:"scopes"`
			UserID  string   `json:"user_id"`
		} `json:"data"`
	}
	if err := adapter.requestJSON(
		ctx,
		http.MethodGet,
		debugURL,
		nil,
		"",
		&response,
	); err != nil {
		return err
	}
	if !response.Data.IsValid || response.Data.AppID != adapter.clientID {
		return &ProviderError{
			Code:      "meta_token_invalid",
			Retryable: false,
		}
	}
	if !contains(response.Data.Scopes, "email") {
		return &ProviderError{
			Code:      "meta_email_scope_missing",
			Retryable: false,
		}
	}
	return nil
}

func (adapter *MetaAdapter) requestJSON(
	ctx context.Context,
	method, endpoint string,
	body io.Reader,
	bearerToken string,
	target any,
) error {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return &ProviderError{Code: "meta_request_invalid", Retryable: false, Cause: err}
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if strings.TrimSpace(bearerToken) != "" {
		request.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	response, err := adapter.httpClient.Do(request)
	if err != nil {
		return &ProviderError{Code: "meta_request_failed", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return &ProviderError{Code: "meta_response_unreadable", Retryable: true, Cause: err}
	}
	if response.StatusCode >= http.StatusBadRequest {
		var providerMessage struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(payload, &providerMessage)
		return &ProviderError{
			Code:      "meta_http_" + fmt.Sprint(response.StatusCode),
			Retryable: response.StatusCode >= http.StatusInternalServerError,
			Cause:     errors.New(strings.TrimSpace(providerMessage.Error.Message)),
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(target); err != nil {
		return &ProviderError{Code: "meta_response_invalid", Retryable: false, Cause: err}
	}
	return nil
}
