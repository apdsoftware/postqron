package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type OIDCAdapterConfig struct {
	Provider         Provider
	ClientID         string
	ClientSecret     string
	RedirectURL      string
	AuthorizationURL string
	IssuerURL        string
	RevocationURL    string
	Scopes           []string
	ExtraParameters  map[string]string
	HTTPClient       *http.Client
}

type OIDCAdapter struct {
	provider         Provider
	clientID         string
	clientSecret     string
	redirectURL      string
	authorizationURL string
	issuerURL        string
	revocationURL    string
	scopes           []string
	extraParameters  map[string]string
	httpClient       *http.Client

	mu              sync.Mutex
	discovered      *oidc.Provider
	discoveredError error
}

func NewOIDCAdapter(config OIDCAdapterConfig) (*OIDCAdapter, error) {
	if !isSupportedProvider(config.Provider) {
		return nil, fmt.Errorf("%s provider is not supported", config.Provider)
	}
	if strings.TrimSpace(config.ClientID) == "" ||
		strings.TrimSpace(config.ClientSecret) == "" ||
		strings.TrimSpace(config.RedirectURL) == "" ||
		strings.TrimSpace(config.AuthorizationURL) == "" ||
		strings.TrimSpace(config.IssuerURL) == "" {
		return nil, errors.New("OIDC provider configuration is incomplete")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &OIDCAdapter{
		provider:         config.Provider,
		clientID:         config.ClientID,
		clientSecret:     config.ClientSecret,
		redirectURL:      config.RedirectURL,
		authorizationURL: config.AuthorizationURL,
		issuerURL:        config.IssuerURL,
		revocationURL:    strings.TrimSpace(config.RevocationURL),
		scopes:           append([]string(nil), config.Scopes...),
		extraParameters:  cloneStringMap(config.ExtraParameters),
		httpClient:       config.HTTPClient,
	}, nil
}

func (adapter *OIDCAdapter) Config() ProviderConfig {
	return ProviderConfig{
		ClientID:         adapter.clientID,
		AuthorizationURL: adapter.authorizationURL,
		RedirectURL:      adapter.redirectURL,
		Scopes:           append([]string(nil), adapter.scopes...),
		ExtraParameters:  cloneStringMap(adapter.extraParameters),
	}
}

func (adapter *OIDCAdapter) Exchange(
	ctx context.Context,
	request ExchangeRequest,
) (ExternalIdentity, error) {
	provider, err := adapter.providerMetadata(ctx)
	if err != nil {
		return ExternalIdentity{}, &ProviderError{
			Code:      "oidc_discovery_failed",
			Retryable: true,
			Cause:     err,
		}
	}
	oauthConfig := oauth2.Config{
		ClientID:     adapter.clientID,
		ClientSecret: adapter.clientSecret,
		RedirectURL:  adapter.redirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       append([]string(nil), adapter.scopes...),
	}
	token, err := oauthConfig.Exchange(
		oidc.ClientContext(ctx, adapter.httpClient),
		request.Code,
		oauth2.SetAuthURLParam("code_verifier", request.PKCEVerifier),
	)
	if err != nil {
		return ExternalIdentity{}, &ProviderError{
			Code:      "oidc_code_exchange_failed",
			Retryable: true,
			Cause:     err,
		}
	}
	rawIDToken, _ := token.Extra("id_token").(string)
	if rawIDToken == "" {
		return ExternalIdentity{}, &ProviderError{
			Code:      "oidc_missing_id_token",
			Retryable: false,
		}
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: adapter.clientID}).Verify(
		oidc.ClientContext(ctx, adapter.httpClient),
		rawIDToken,
	)
	if err != nil {
		return ExternalIdentity{}, &ProviderError{
			Code:      "oidc_invalid_id_token",
			Retryable: false,
			Cause:     err,
		}
	}
	var claims oidcIdentityClaims
	if err := idToken.Claims(&claims); err != nil {
		return ExternalIdentity{}, &ProviderError{
			Code:      "oidc_claims_invalid",
			Retryable: false,
			Cause:     err,
		}
	}
	if claims.Nonce != request.ExpectedNonce {
		return ExternalIdentity{}, &ProviderError{
			Code:      "oidc_nonce_mismatch",
			Retryable: false,
		}
	}
	identity := claims.externalIdentity()
	if identity.DisplayName == "" && token.AccessToken != "" {
		if userInfo, userInfoErr := adapter.userInfo(
			ctx,
			provider,
			token.AccessToken,
		); userInfoErr == nil {
			if identity.Email == "" {
				identity.Email = userInfo.Email
			}
			identity.EmailVerified = identity.EmailVerified || userInfo.EmailVerified
			if identity.DisplayName == "" {
				identity.DisplayName = userInfo.DisplayName
			}
		}
	}
	if refreshToken := strings.TrimSpace(token.RefreshToken); refreshToken != "" {
		identity.RevocationToken = refreshToken
	} else {
		identity.RevocationToken = token.AccessToken
	}
	return identity, nil
}

func (adapter *OIDCAdapter) Revoke(ctx context.Context, token string) error {
	if strings.TrimSpace(adapter.revocationURL) == "" || strings.TrimSpace(token) == "" {
		return nil
	}
	values := url.Values{
		"token":         {token},
		"client_id":     {adapter.clientID},
		"client_secret": {adapter.clientSecret},
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		adapter.revocationURL,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		return &ProviderError{Code: "oidc_revoke_request_invalid", Retryable: false, Cause: err}
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := adapter.httpClient.Do(request)
	if err != nil {
		return &ProviderError{Code: "oidc_revoke_failed", Retryable: true, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return &ProviderError{
			Code:      "oidc_revoke_rejected",
			Retryable: response.StatusCode >= http.StatusInternalServerError,
		}
	}
	return nil
}

func (adapter *OIDCAdapter) providerMetadata(
	ctx context.Context,
) (*oidc.Provider, error) {
	adapter.mu.Lock()
	cached := adapter.discovered
	adapter.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	provider, err := oidc.NewProvider(
		oidc.ClientContext(ctx, adapter.httpClient),
		adapter.issuerURL,
	)
	if err != nil {
		return nil, err
	}
	adapter.mu.Lock()
	adapter.discovered = provider
	adapter.mu.Unlock()
	return provider, nil
}

func (adapter *OIDCAdapter) userInfo(
	ctx context.Context,
	provider *oidc.Provider,
	accessToken string,
) (ExternalIdentity, error) {
	userInfo, err := provider.UserInfo(
		oidc.ClientContext(ctx, adapter.httpClient),
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: accessToken}),
	)
	if err != nil {
		return ExternalIdentity{}, err
	}
	var claims oidcIdentityClaims
	if err := userInfo.Claims(&claims); err != nil {
		return ExternalIdentity{}, err
	}
	return claims.externalIdentity(), nil
}

type oidcIdentityClaims struct {
	Subject       string          `json:"sub"`
	Email         string          `json:"email"`
	Name          string          `json:"name"`
	GivenName     string          `json:"given_name"`
	FamilyName    string          `json:"family_name"`
	Nonce         string          `json:"nonce"`
	EmailVerified json.RawMessage `json:"email_verified"`
}

func (claims oidcIdentityClaims) externalIdentity() ExternalIdentity {
	displayName := strings.TrimSpace(claims.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(strings.TrimSpace(claims.GivenName) + " " + strings.TrimSpace(claims.FamilyName))
	}
	return ExternalIdentity{
		Subject:         strings.TrimSpace(claims.Subject),
		Email:           strings.TrimSpace(claims.Email),
		EmailVerified:   parseFlexibleBool(claims.EmailVerified),
		DisplayName:     displayName,
		RevocationToken: "",
	}
}

func parseFlexibleBool(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var boolValue bool
	if err := json.Unmarshal(raw, &boolValue); err == nil {
		return boolValue
	}
	var stringValue string
	if err := json.Unmarshal(raw, &stringValue); err == nil {
		return strings.EqualFold(strings.TrimSpace(stringValue), "true")
	}
	return false
}

func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}
