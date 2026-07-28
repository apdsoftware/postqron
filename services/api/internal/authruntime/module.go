package authruntime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	auth "github.com/apdsoftware/postqron/features/f03-auth"
	"github.com/apdsoftware/postqron/services/api/internal/emailruntime"
)

const (
	adminBootstrapEmailEnv = "POSTQRON_ADMIN_BOOTSTRAP_EMAIL"
	adminPasswordHashEnv   = "POSTQRON_ADMIN_PASSWORD_HASH_B64"
	authAllowedOriginsEnv  = "POSTQRON_AUTH_ALLOWED_ORIGINS"
)

type Module struct {
	database       *sql.DB
	handler        http.Handler
	clock          func() time.Time
	bootstrapEmail string
	bootstrapHash  string
}

func NewModule(
	database *sql.DB,
	appDomain string,
	clock func() time.Time,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("auth runtime database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	passwordStore, err := auth.NewPostgresPasswordStore(database)
	if err != nil {
		return nil, err
	}
	passwordService, err := auth.NewPasswordService(passwordStore, clock, 0)
	if err != nil {
		return nil, err
	}
	registrationStore, err := auth.NewPostgresPasswordRegistrationStore(database)
	if err != nil {
		return nil, err
	}
	registrationService, err := auth.NewPasswordRegistrationService(auth.PasswordRegistrationConfig{
		Store: registrationStore,
		Now:   clock,
	})
	if err != nil {
		return nil, err
	}
	authStore, err := auth.NewPostgresStore(database)
	if err != nil {
		return nil, err
	}
	authService, err := newRuntimeAuthService(authStore, clock)
	if err != nil {
		return nil, err
	}
	delegate, err := auth.NewRuntimeHandler(
		authService,
		passwordService,
		registrationService,
		os.Getenv(authAllowedOriginsEnv),
	)
	if err != nil {
		return nil, err
	}
	emailService, err := emailruntime.NewService(database, appDomain, clock)
	if err != nil {
		return nil, err
	}
	handler, err := newHandler(
		delegate,
		registrationService,
		emailService,
		os.Getenv(authAllowedOriginsEnv),
	)
	if err != nil {
		return nil, err
	}
	module := &Module{
		database:       database,
		handler:        handler,
		clock:          clock,
		bootstrapEmail: strings.TrimSpace(os.Getenv(adminBootstrapEmailEnv)),
	}
	if encoded := strings.TrimSpace(os.Getenv(adminPasswordHashEnv)); encoded != "" {
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return nil, errors.New("admin password hash secret is not valid base64")
		}
		module.bootstrapHash = string(decoded)
	}
	if (module.bootstrapEmail == "") != (module.bootstrapHash == "") {
		return nil, errors.New("admin bootstrap email and password hash must be configured together")
	}
	return module, nil
}

func (module *Module) Start(ctx context.Context) error {
	if module == nil || module.database == nil || module.handler == nil {
		return errors.New("auth runtime module is not configured")
	}
	if module.bootstrapEmail == "" {
		return nil
	}
	return auth.BootstrapPasswordAccount(
		ctx,
		module.database,
		module.bootstrapEmail,
		module.bootstrapHash,
		module.clock().UTC(),
	)
}

func (module *Module) Stop(context.Context) error { return nil }

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("auth runtime database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil || name != "Auth" {
		return nil, false
	}
	return module.handler, true
}

type runtimeAuthStore interface {
	auth.TransactionStore
}

func newRuntimeAuthService(
	store runtimeAuthStore,
	now func() time.Time,
) (*auth.Service, error) {
	sealer := authSealerFromEnv()
	var providers map[auth.Provider]auth.ProviderAdapter
	if sealer != nil {
		providers = authProviderAdapters()
	}
	return auth.NewService(auth.Config{
		Store:     store,
		Sealer:    sealer,
		Providers: providers,
		Now:       now,
	})
}

type handler struct {
	delegate     http.Handler
	registration registrationService
	email        verificationMailer
	origins      map[string]struct{}
}

type registrationService interface {
	Register(
		context.Context,
		string,
		string,
		string,
		string,
		[]auth.ConsentReceipt,
	) (auth.PasswordRegistrationResult, error)
	ResendVerification(context.Context, string) (*auth.VerificationDelivery, error)
}

type verificationMailer interface {
	EnqueueVerification(context.Context, *auth.VerificationDelivery, string) error
}

func newHandler(
	delegate http.Handler,
	registration registrationService,
	emailService verificationMailer,
	allowedOrigins string,
) (http.Handler, error) {
	origins, err := parseOrigins(allowedOrigins)
	if err != nil {
		return nil, err
	}
	return &handler{
		delegate:     delegate,
		registration: registration,
		email:        emailService,
		origins:      origins,
	}, nil
}

func (handler *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	allowed, rejected := handler.guardCustomOrigin(writer, request)
	if rejected {
		return
	}
	if allowed {
		handler.applyCORS(writer, request)
	}
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/auth/password/register":
		handler.register(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/auth/password/verify/resend":
		handler.resend(writer, request)
	default:
		handler.delegate.ServeHTTP(writer, request)
	}
}

func (handler *handler) guardCustomOrigin(
	writer http.ResponseWriter,
	request *http.Request,
) (allowed bool, rejected bool) {
	if !isCustomRoute(request) {
		return false, false
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return false, false
	}
	parsed, err := normalizeOrigin(origin)
	if err != nil {
		writeOriginForbidden(writer)
		return false, true
	}
	if _, ok := handler.origins[parsed]; !ok {
		writeOriginForbidden(writer)
		return false, true
	}
	return true, false
}

func (handler *handler) register(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Email           string                `json:"email"`
		Password        string                `json:"password"`
		Confirmation    string                `json:"confirmation"`
		ContractCountry string                `json:"contract_country"`
		Consents        []auth.ConsentReceipt `json:"consents"`
	}
	if err := decodeRequestJSON(writer, request, &input); err != nil {
		writeRegistrationError(writer, err)
		return
	}
	result, err := handler.registration.Register(
		request.Context(),
		input.Email,
		input.Password,
		input.Confirmation,
		input.ContractCountry,
		input.Consents,
	)
	if err != nil {
		writeRegistrationError(writer, err)
		return
	}
	if result.Delivery != nil {
		if err := handler.email.EnqueueVerification(
			request.Context(),
			result.Delivery,
			registrationLocale(input.Consents),
		); err != nil {
			writeRegistrationError(writer, err)
			return
		}
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"verification_requested": true,
	})
}

func (handler *handler) resend(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if err := decodeRequestJSON(writer, request, &input); err != nil {
		writeRegistrationError(writer, err)
		return
	}
	delivery, err := handler.registration.ResendVerification(request.Context(), input.Email)
	if err != nil {
		writeRegistrationError(writer, err)
		return
	}
	if delivery != nil {
		if err := handler.email.EnqueueVerification(request.Context(), delivery, "it"); err != nil {
			writeRegistrationError(writer, err)
			return
		}
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"verification_requested": true,
	})
}

func registrationLocale(consents []auth.ConsentReceipt) string {
	for _, receipt := range consents {
		if strings.TrimSpace(receipt.Locale) != "" {
			return receipt.Locale
		}
	}
	return "it-IT"
}

func decodeRequestJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func writeRegistrationError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrPasswordConfirmation), errors.Is(err, auth.ErrPasswordPolicy):
		writePasswordOperationError(writer, err)
	case errors.Is(err, auth.ErrVerificationInvalid):
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"code":      "AUTH_EMAIL_VERIFICATION_INVALID",
				"message":   "The verification token is invalid.",
				"retryable": false,
			},
		})
	case errors.Is(err, auth.ErrVerificationExpired):
		writeJSON(writer, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"code":      "AUTH_EMAIL_VERIFICATION_EXPIRED",
				"message":   "The verification token expired.",
				"retryable": false,
			},
		})
	default:
		if code, message, retryable := auth.ErrorDetails(err); code != auth.CodeInternal {
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

func writePasswordOperationError(writer http.ResponseWriter, err error) {
	code, message, retryable := auth.ErrorDetails(err)
	status := http.StatusBadRequest
	if code == auth.CodeInternal {
		status = http.StatusServiceUnavailable
	}
	writeJSON(writer, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": retryable,
		},
	})
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}

func parseOrigins(raw string) (map[string]struct{}, error) {
	origins := make(map[string]struct{})
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		origin, err := normalizeOrigin(value)
		if err != nil {
			return nil, errors.New("auth allowed origin must be a valid URL")
		}
		origins[origin] = struct{}{}
	}
	return origins, nil
}

func (handler *handler) applyCORS(writer http.ResponseWriter, request *http.Request) {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return
	}
	if _, ok := handler.origins[origin]; !ok {
		return
	}
	writer.Header().Set("Access-Control-Allow-Origin", origin)
	writer.Header().Set("Access-Control-Allow-Credentials", "true")
	writer.Header().Add("Vary", "Origin")
}

func normalizeOrigin(value string) (string, error) {
	request, err := http.NewRequest(http.MethodGet, value, nil)
	if err != nil || request.URL == nil || request.URL.Host == "" {
		return "", errors.New("origin is invalid")
	}
	if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
		return "", errors.New("origin is invalid")
	}
	if request.URL.User != nil || request.URL.RawQuery != "" || request.URL.Fragment != "" {
		return "", errors.New("origin is invalid")
	}
	if request.URL.Path != "" && request.URL.Path != "/" {
		return "", errors.New("origin is invalid")
	}
	return request.URL.Scheme + "://" + request.URL.Host, nil
}

func isCustomRoute(request *http.Request) bool {
	if request.Method != http.MethodPost {
		return false
	}
	return request.URL.Path == "/api/v1/auth/password/register" ||
		request.URL.Path == "/api/v1/auth/password/verify/resend"
}

func writeOriginForbidden(writer http.ResponseWriter) {
	writeJSON(writer, http.StatusForbidden, map[string]any{
		"error": map[string]any{
			"code":      "AUTH_ORIGIN_FORBIDDEN",
			"message":   "The request origin is not allowed.",
			"retryable": false,
		},
	})
}

func authSealerFromEnv() auth.Sealer {
	encoded := strings.TrimSpace(os.Getenv("POSTQRON_AUTH_ENCRYPTION_KEY_B64"))
	if encoded == "" {
		return nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil
	}
	sealer, err := auth.NewAESGCMSealer(key)
	if err != nil {
		return nil
	}
	return sealer
}

func RuntimeSealerFromEnv() auth.Sealer {
	return authSealerFromEnv()
}

func authProviderAdapters() map[auth.Provider]auth.ProviderAdapter {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	adapters := make(map[auth.Provider]auth.ProviderAdapter)
	registerOIDC := func(
		provider auth.Provider,
		clientIDEnv, clientSecretEnv, redirectEnv string,
		authorizationURL, issuerURL, revocationURL string,
		scopes []string,
		extra map[string]string,
	) {
		clientID := strings.TrimSpace(os.Getenv(clientIDEnv))
		clientSecret := strings.TrimSpace(os.Getenv(clientSecretEnv))
		redirectURL := strings.TrimSpace(os.Getenv(redirectEnv))
		if clientID == "" || clientSecret == "" || !validOAuthRedirectURL(redirectURL) {
			return
		}
		adapter, err := auth.NewOIDCAdapter(auth.OIDCAdapterConfig{
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
		auth.ProviderGoogle,
		"POSTQRON_AUTH_GOOGLE_CLIENT_ID",
		"POSTQRON_AUTH_GOOGLE_CLIENT_SECRET",
		"POSTQRON_AUTH_GOOGLE_REDIRECT_URL",
		"https://accounts.google.com/o/oauth2/v2/auth",
		"https://accounts.google.com",
		"https://oauth2.googleapis.com/revoke",
		[]string{"openid", "email", "profile"},
		nil,
	)
	registerOIDC(
		auth.ProviderApple,
		"POSTQRON_AUTH_APPLE_CLIENT_ID",
		"POSTQRON_AUTH_APPLE_CLIENT_SECRET",
		"POSTQRON_AUTH_APPLE_REDIRECT_URL",
		"https://appleid.apple.com/auth/authorize",
		"https://appleid.apple.com",
		"https://appleid.apple.com/auth/revoke",
		[]string{"openid", "email", "name"},
		map[string]string{"response_mode": "form_post"},
	)
	registerOIDC(
		auth.ProviderLinkedIn,
		"POSTQRON_AUTH_LINKEDIN_CLIENT_ID",
		"POSTQRON_AUTH_LINKEDIN_CLIENT_SECRET",
		"POSTQRON_AUTH_LINKEDIN_REDIRECT_URL",
		"https://www.linkedin.com/oauth/v2/authorization",
		"https://www.linkedin.com/oauth",
		"",
		[]string{"openid", "email", "profile"},
		nil,
	)
	clientID := strings.TrimSpace(os.Getenv("POSTQRON_AUTH_FACEBOOK_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("POSTQRON_AUTH_FACEBOOK_CLIENT_SECRET"))
	redirectURL := strings.TrimSpace(os.Getenv("POSTQRON_AUTH_FACEBOOK_REDIRECT_URL"))
	graphVersion := strings.TrimSpace(os.Getenv("POSTQRON_AUTH_FACEBOOK_GRAPH_VERSION"))
	if clientID != "" && clientSecret != "" && validOAuthRedirectURL(redirectURL) && graphVersion != "" {
		adapter, err := auth.NewMetaAdapter(auth.MetaAdapterConfig{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			GraphVersion: graphVersion,
			HTTPClient:   httpClient,
		})
		if err == nil {
			adapters[auth.ProviderFacebook] = adapter
		}
	}
	return adapters
}

func validOAuthRedirectURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" &&
		parsed.Host != "" &&
		parsed.User == nil &&
		parsed.Fragment == ""
}

func RuntimeProviderAdapters() map[auth.Provider]auth.ProviderAdapter {
	return authProviderAdapters()
}
