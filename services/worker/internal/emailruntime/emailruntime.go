package emailruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	email "github.com/apdsoftware/postqron/features/f14-email"
)

const (
	mailronixEndpointEnv         = "POSTQRON_MAILRONIX_ENDPOINT"
	mailronixAPIKeySecretEnv     = "POSTQRON_MAILRONIX_API_KEY_SECRET_NAME"
	mailronixSenderEmailEnv      = "POSTQRON_MAILRONIX_SENDER_EMAIL"
	mailronixDomainVerifiedEnv   = "POSTQRON_MAILRONIX_DOMAIN_VERIFIED"
	mailronixFailureThresholdEnv = "POSTQRON_MAILRONIX_FAILURE_THRESHOLD"
	mailronixCircuitOpenForEnv   = "POSTQRON_MAILRONIX_CIRCUIT_OPEN_FOR"
)

type Service struct {
	emailService *email.Service
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
	sender, err := runtimeSender()
	if err != nil {
		return nil, err
	}
	service, err := email.NewService(
		&sqlStore{database: database},
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
	return &Service{emailService: service}, nil
}

func (service *Service) DispatchOne(ctx context.Context) (bool, error) {
	if service == nil || service.emailService == nil {
		return false, errors.New("email runtime service is not configured")
	}
	return service.emailService.DispatchOne(ctx)
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

func runtimeSender() (email.Sender, error) {
	endpoint := strings.TrimSpace(os.Getenv(mailronixEndpointEnv))
	secretName := strings.TrimSpace(os.Getenv(mailronixAPIKeySecretEnv))
	senderEmail := strings.TrimSpace(os.Getenv(mailronixSenderEmailEnv))
	domainVerified := strings.EqualFold(strings.TrimSpace(os.Getenv(mailronixDomainVerifiedEnv)), "true")
	configured := 0
	for _, value := range []string{endpoint, secretName, senderEmail} {
		if value != "" {
			configured++
		}
	}
	if domainVerified {
		configured++
	}
	if configured == 0 &&
		!strings.EqualFold(strings.TrimSpace(os.Getenv("PADDLE_ENVIRONMENT")), "production") {
		return &email.FakeSender{}, nil
	}
	if configured != 4 {
		return nil, errors.New("Mailronix configuration is required and must be complete")
	}
	failureThreshold := 3
	if value := strings.TrimSpace(os.Getenv(mailronixFailureThresholdEnv)); value != "" {
		if _, err := fmt.Sscanf(value, "%d", &failureThreshold); err != nil || failureThreshold < 1 {
			return nil, errors.New("POSTQRON_MAILRONIX_FAILURE_THRESHOLD is invalid")
		}
	}
	circuitOpenFor := 2 * time.Minute
	if value := strings.TrimSpace(os.Getenv(mailronixCircuitOpenForEnv)); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			return nil, errors.New("POSTQRON_MAILRONIX_CIRCUIT_OPEN_FOR is invalid")
		}
		circuitOpenFor = duration
	}
	return email.NewMailronixClient(
		email.MailronixConfig{
			Endpoint:         endpoint,
			ContractVersion:  email.MailronixContractVersion,
			APIKeySecret:     secretName,
			From:             email.SenderIdentity{Email: senderEmail},
			DomainVerified:   true,
			FailureThreshold: failureThreshold,
			CircuitOpenFor:   circuitOpenFor,
		},
		&http.Client{Timeout: 10 * time.Second},
		envSecretProvider{},
	)
}

type envSecretProvider struct{}

func (envSecretProvider) Secret(_ context.Context, name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", errors.New("secret is unavailable")
	}
	return value, nil
}
