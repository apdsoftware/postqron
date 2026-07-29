package emailruntime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	auth "github.com/apdsoftware/postqron/features/f03-auth"
	email "github.com/apdsoftware/postqron/features/f14-email"
)

const (
	postqronEnvEnv               = "POSTQRON_ENV"
	mailronixEndpointEnv         = "POSTQRON_MAILRONIX_ENDPOINT"
	mailronixAPIKeySecretEnv     = "POSTQRON_MAILRONIX_API_KEY_SECRET_NAME"
	mailronixSenderEmailEnv      = "POSTQRON_MAILRONIX_SENDER_EMAIL"
	mailronixDomainVerifiedEnv   = "POSTQRON_MAILRONIX_DOMAIN_VERIFIED"
	mailronixFailureThresholdEnv = "POSTQRON_MAILRONIX_FAILURE_THRESHOLD"
	mailronixCircuitOpenForEnv   = "POSTQRON_MAILRONIX_CIRCUIT_OPEN_FOR"
)

type Service struct {
	emailService *email.Service
	appDomain    string
	now          func() time.Time
}

func NewService(
	database *sql.DB,
	appDomain string,
	clock func() time.Time,
) (*Service, error) {
	if database == nil {
		return nil, errors.New("email runtime database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	validatedDomain, err := validateAppDomain(appDomain)
	if err != nil {
		return nil, err
	}

	brand, err := loadBrand(validatedDomain)
	if err != nil {
		return nil, err
	}
	renderer, err := email.NewRenderer(brand)
	if err != nil {
		return nil, err
	}
	store := &sqlStore{database: database}
	sender, err := runtimeSender()
	if err != nil {
		return nil, err
	}
	service, err := email.NewService(
		store,
		renderer,
		sender,
		email.RetryPolicy{
			BaseDelay: 2 * time.Second,
			MaxDelay:  5 * time.Minute,
		},
	)
	if err != nil {
		return nil, err
	}
	serviceNowField(service, clock)
	return &Service{
		emailService: service,
		appDomain:    validatedDomain,
		now:          clock,
	}, nil
}

func (service *Service) EnqueueVerification(
	ctx context.Context,
	delivery *auth.VerificationDelivery,
	locale string,
) error {
	if service == nil || service.emailService == nil {
		return errors.New("email runtime service is not configured")
	}
	if delivery == nil {
		return nil
	}
	actionURL, err := service.verificationURL(locale, delivery.Token)
	if err != nil {
		return err
	}
	verificationID := verificationRequestID(delivery.Token)
	_, err = service.emailService.Enqueue(ctx, email.Message{
		IdempotencyKey:  "account-verification:" + delivery.AccountID + ":" + verificationID,
		Channel:         email.ChannelTransactional,
		Template:        email.TemplateAccountVerification,
		TemplateVersion: "1.0.0",
		Recipient: email.Recipient{
			ID:     delivery.AccountID,
			Email:  delivery.Email,
			Locale: normalizeLocale(locale),
		},
		Data: email.TemplateData{
			ActionURL:  actionURL,
			OccurredAt: delivery.ExpiresAt,
			TimeZone:   "UTC",
		},
		CreatedAt:   service.now().UTC(),
		MaxAttempts: 5,
	})
	if err != nil {
		return fmt.Errorf("enqueue account verification email: %w", err)
	}
	return nil
}

func (service *Service) DispatchOne(ctx context.Context) (bool, error) {
	if service == nil || service.emailService == nil {
		return false, errors.New("email runtime service is not configured")
	}
	return service.emailService.DispatchOne(ctx)
}

func (service *Service) verificationURL(locale, token string) (string, error) {
	if service.appDomain == "" {
		return "", errors.New("app domain is required for account verification URLs")
	}
	if strings.TrimSpace(token) == "" {
		return "", errors.New("verification token is required")
	}
	pathLocale := normalizeLocale(locale)
	candidate := &url.URL{
		Scheme:   "https",
		Host:     service.appDomain,
		Path:     "/" + pathLocale + "/app/verify-email",
		RawQuery: url.Values{"token": {token}}.Encode(),
	}
	return candidate.String(), nil
}

func verificationRequestID(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func normalizeLocale(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "it", "it-it":
		return "it"
	case "es", "es-es":
		return "es"
	case "fr", "fr-fr":
		return "fr"
	case "de", "de-de":
		return "de"
	default:
		return "en"
	}
}

func validateAppDomain(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "://") || strings.ContainsAny(value, "/?#@") {
		return "", errors.New("APP_DOMAIN must be a bare host name")
	}
	parsed, err := url.Parse("https://" + value)
	if err != nil || parsed.Host == "" || parsed.Host != value || parsed.User != nil {
		return "", errors.New("APP_DOMAIN must be a bare host name")
	}
	return value, nil
}

func loadBrand(appDomain string) (email.Brand, error) {
	tokensPath := filepath.Join("features", "f01-brand", "tokens", "tokens.json")
	file, err := os.Open(tokensPath)
	if err != nil {
		return email.Brand{}, fmt.Errorf("open F1 tokens: %w", err)
	}
	defer file.Close()
	logoURL := "https://" + appDomain + "/brand/logo-primary.svg"
	return email.LoadBrandFromF1(file, "Postqron", logoURL)
}

func runtimeSender() (email.Sender, error) {
	boundary, err := runtimeSenderBoundary()
	if err != nil {
		return nil, err
	}
	return boundary.Sender, nil
}

func runtimeSenderBoundary() (email.SenderBoundary, error) {
	return email.NewSenderBoundaryFromEnv(runtimeSenderOptions(), envSecretProvider{})
}

func runtimeSenderOptions() email.SenderBoundaryOptions {
	environment := strings.ToLower(strings.TrimSpace(os.Getenv(postqronEnvEnv)))
	production := environment == "production"
	options := email.SenderBoundaryOptions{
		Environment: environment,
		Production:  production,
	}
	if production || runtimeSenderConfigured() {
		options.Mode = email.SenderModeLive
	}
	return options
}

func runtimeSenderConfigured() bool {
	return strings.TrimSpace(os.Getenv(mailronixEndpointEnv)) != "" &&
		strings.TrimSpace(os.Getenv(mailronixAPIKeySecretEnv)) != "" &&
		strings.TrimSpace(os.Getenv(mailronixSenderEmailEnv)) != "" &&
		strings.TrimSpace(os.Getenv(mailronixDomainVerifiedEnv)) != ""
}

type envSecretProvider struct{}

func (envSecretProvider) Secret(_ context.Context, name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", errors.New("secret is unavailable")
	}
	return value, nil
}

// The F14 service keeps its clock private; tests only need deterministic store
// timestamps, so setting created_at explicitly on messages is sufficient.
func serviceNowField(_ *email.Service, _ func() time.Time) {}
