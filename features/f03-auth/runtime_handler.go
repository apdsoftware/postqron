package auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	authEncryptionKeyEnv        = "POSTQRON_AUTH_ENCRYPTION_KEY_B64"
	authGoogleClientIDEnv       = "POSTQRON_AUTH_GOOGLE_CLIENT_ID"
	authGoogleClientSecretEnv   = "POSTQRON_AUTH_GOOGLE_CLIENT_SECRET"
	authGoogleRedirectEnv       = "POSTQRON_AUTH_GOOGLE_REDIRECT_URL"
	authAppleClientIDEnv        = "POSTQRON_AUTH_APPLE_CLIENT_ID"
	authAppleClientSecretEnv    = "POSTQRON_AUTH_APPLE_CLIENT_SECRET"
	authAppleRedirectEnv        = "POSTQRON_AUTH_APPLE_REDIRECT_URL"
	authFacebookClientIDEnv     = "POSTQRON_AUTH_FACEBOOK_CLIENT_ID"
	authFacebookClientSecretEnv = "POSTQRON_AUTH_FACEBOOK_CLIENT_SECRET"
	authFacebookRedirectEnv     = "POSTQRON_AUTH_FACEBOOK_REDIRECT_URL"
	authFacebookGraphEnv        = "POSTQRON_AUTH_FACEBOOK_GRAPH_VERSION"
	authLinkedInClientIDEnv     = "POSTQRON_AUTH_LINKEDIN_CLIENT_ID"
	authLinkedInClientSecretEnv = "POSTQRON_AUTH_LINKEDIN_CLIENT_SECRET"
	authLinkedInRedirectEnv     = "POSTQRON_AUTH_LINKEDIN_REDIRECT_URL"
)

type rejectingSealer struct{}

func (rejectingSealer) Seal([]byte) ([]byte, error) {
	return nil, errors.New("auth provider encryption is unavailable")
}
func (rejectingSealer) Open([]byte) ([]byte, error) {
	return nil, errors.New("auth provider encryption is unavailable")
}

func NewRuntimeHandler(
	authService *Service,
	passwordService *PasswordService,
	registrationService *PasswordRegistrationService,
	allowedOrigins ...string,
) (http.Handler, error) {
	if authService == nil || passwordService == nil || registrationService == nil {
		return nil, errors.New("auth runtime services are required")
	}
	origins, err := normalizePasswordOrigins(allowedOrigins)
	if err != nil {
		return nil, err
	}
	password := &PasswordHandler{
		service:        passwordService,
		allowedOrigins: origins,
	}
	oauth := &Handler{service: authService}
	registration := &passwordRegistrationHandler{service: registrationService}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/password/register", registration.register)
	mux.HandleFunc("OPTIONS /api/v1/auth/password/register", authPreflight)
	mux.HandleFunc("POST /api/v1/auth/password/verify", registration.verify)
	mux.HandleFunc("OPTIONS /api/v1/auth/password/verify", authPreflight)
	mux.HandleFunc("POST /api/v1/auth/password/verify/resend", registration.resend)
	mux.HandleFunc("OPTIONS /api/v1/auth/password/verify/resend", authPreflight)
	mux.HandleFunc("POST /api/v1/auth/password/login", password.login)
	mux.HandleFunc("OPTIONS /api/v1/auth/password/login", authPreflight)
	mux.HandleFunc("POST /api/v1/auth/password/change", password.changePassword)
	mux.HandleFunc("OPTIONS /api/v1/auth/password/change", authPreflight)
	mux.HandleFunc("POST /api/v1/auth/logout", password.logout)
	mux.HandleFunc("OPTIONS /api/v1/auth/logout", authPreflight)
	mux.HandleFunc("POST /api/v1/auth/authorize", oauth.authorize)
	mux.HandleFunc("OPTIONS /api/v1/auth/authorize", authPreflight)
	mux.HandleFunc("GET /api/v1/auth/callback", oauth.callback)
	mux.HandleFunc("POST /api/v1/auth/callback", oauth.callback)
	mux.HandleFunc("POST /api/v1/auth/link", oauth.link)
	mux.HandleFunc("OPTIONS /api/v1/auth/link", authPreflight)
	mux.HandleFunc("POST /api/v1/auth/sessions/revoke", oauth.revokeSessions)
	mux.HandleFunc("OPTIONS /api/v1/auth/sessions/revoke", authPreflight)
	mux.HandleFunc("DELETE /api/v1/auth/providers/{provider}", oauth.unlink)
	mux.HandleFunc("OPTIONS /api/v1/auth/providers/{provider}", authPreflight)
	return password.cors(mux), nil
}

type passwordRegistrationHandler struct {
	service *PasswordRegistrationService
}

func (handler *passwordRegistrationHandler) register(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input struct {
		Email           string           `json:"email"`
		Password        string           `json:"password"`
		Confirmation    string           `json:"confirmation"`
		ContractCountry string           `json:"contract_country"`
		Consents        []ConsentReceipt `json:"consents"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writePasswordRegistrationError(writer, err)
		return
	}
	if _, err := handler.service.Register(
		request.Context(),
		input.Email,
		input.Password,
		input.Confirmation,
		input.ContractCountry,
		input.Consents,
	); err != nil {
		writePasswordRegistrationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"verification_requested": true,
	})
}

func (handler *passwordRegistrationHandler) verify(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writePasswordRegistrationError(writer, err)
		return
	}
	if err := handler.service.VerifyEmail(request.Context(), input.Token); err != nil {
		writePasswordRegistrationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"verified": true})
}

func (handler *passwordRegistrationHandler) resend(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var input struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writePasswordRegistrationError(writer, err)
		return
	}
	if _, err := handler.service.ResendVerification(request.Context(), input.Email); err != nil {
		writePasswordRegistrationError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"verification_requested": true,
	})
}

func writePasswordRegistrationError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrPasswordConfirmation):
		writePasswordOperationError(writer, err)
	case errors.Is(err, ErrPasswordPolicy):
		writePasswordOperationError(writer, err)
	case errors.Is(err, ErrVerificationInvalid):
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"code":      "AUTH_EMAIL_VERIFICATION_INVALID",
				"message":   "The verification token is invalid.",
				"retryable": false,
			},
		})
	case errors.Is(err, ErrVerificationExpired):
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"code":      "AUTH_EMAIL_VERIFICATION_EXPIRED",
				"message":   "The verification token expired.",
				"retryable": false,
			},
		})
	default:
		if code, message, retryable := ErrorDetails(err); code != CodeInternal {
			writeJSON(writer, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"code":      code,
					"message":   message,
					"retryable": retryable,
				},
			})
			return
		}
		writeJSON(writer, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{
				"code":      "AUTH_REGISTRATION_UNAVAILABLE",
				"message":   "Registration is temporarily unavailable.",
				"retryable": true,
			},
		})
	}
}

func authPreflight(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Access-Control-Allow-Methods", "POST, DELETE, OPTIONS")
	writer.Header().Set(
		"Access-Control-Allow-Headers",
		"Content-Type, X-CSRF-Token",
	)
	writer.Header().Set("Access-Control-Max-Age", "600")
	writer.WriteHeader(http.StatusNoContent)
}

func runtimeSealerFromEnv() Sealer {
	encoded := strings.TrimSpace(os.Getenv(authEncryptionKeyEnv))
	if encoded == "" {
		return nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil
	}
	sealer, err := NewAESGCMSealer(key)
	if err != nil {
		return nil
	}
	return sealer
}

func runtimeProviderAdapters() map[Provider]ProviderAdapter {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	adapters := make(map[Provider]ProviderAdapter)
	registerOIDC := func(
		provider Provider,
		clientIDEnv, clientSecretEnv, redirectEnv string,
		authorizationURL, issuerURL, revocationURL string,
		scopes []string,
		extra map[string]string,
	) {
		clientID := strings.TrimSpace(os.Getenv(clientIDEnv))
		clientSecret := strings.TrimSpace(os.Getenv(clientSecretEnv))
		redirectURL := strings.TrimSpace(os.Getenv(redirectEnv))
		if clientID == "" || clientSecret == "" || redirectURL == "" {
			return
		}
		adapter, err := NewOIDCAdapter(OIDCAdapterConfig{
			Provider:         provider,
			ClientID:         clientID,
			ClientSecret:     clientSecret,
			RedirectURL:      redirectURL,
			AuthorizationURL: authorizationURL,
			IssuerURL:        issuerURL,
			RevocationURL:    revocationURL,
			Scopes:           scopes,
			ExtraParameters:  extra,
			HTTPClient:       httpClient,
		})
		if err == nil {
			adapters[provider] = adapter
		}
	}
	registerOIDC(
		ProviderGoogle,
		authGoogleClientIDEnv,
		authGoogleClientSecretEnv,
		authGoogleRedirectEnv,
		"https://accounts.google.com/o/oauth2/v2/auth",
		"https://accounts.google.com",
		"https://oauth2.googleapis.com/revoke",
		[]string{"openid", "email", "profile"},
		nil,
	)
	registerOIDC(
		ProviderApple,
		authAppleClientIDEnv,
		authAppleClientSecretEnv,
		authAppleRedirectEnv,
		"https://appleid.apple.com/auth/authorize",
		"https://appleid.apple.com",
		"https://appleid.apple.com/auth/revoke",
		[]string{"openid", "email", "name"},
		map[string]string{"response_mode": "form_post"},
	)
	registerOIDC(
		ProviderLinkedIn,
		authLinkedInClientIDEnv,
		authLinkedInClientSecretEnv,
		authLinkedInRedirectEnv,
		"https://www.linkedin.com/oauth/v2/authorization",
		"https://www.linkedin.com",
		"",
		[]string{"openid", "email", "profile"},
		nil,
	)
	clientID := strings.TrimSpace(os.Getenv(authFacebookClientIDEnv))
	clientSecret := strings.TrimSpace(os.Getenv(authFacebookClientSecretEnv))
	redirectURL := strings.TrimSpace(os.Getenv(authFacebookRedirectEnv))
	graphVersion := strings.TrimSpace(os.Getenv(authFacebookGraphEnv))
	if clientID != "" && clientSecret != "" && redirectURL != "" && graphVersion != "" {
		adapter, err := NewMetaAdapter(MetaAdapterConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			GraphVersion: graphVersion,
			HTTPClient:   httpClient,
		})
		if err == nil {
			adapters[ProviderFacebook] = adapter
		}
	}
	return adapters
}
