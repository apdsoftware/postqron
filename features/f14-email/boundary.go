package email

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSenderHTTPTimeout             = 10 * time.Second
	defaultFailureThreshold              = 3
	defaultCircuitOpenFor                = 2 * time.Minute
	postqronMailronixEndpointEnv         = "POSTQRON_MAILRONIX_ENDPOINT"
	postqronMailronixAPIKeySecretNameEnv = "POSTQRON_MAILRONIX_API_KEY_SECRET_NAME"
	postqronMailronixSenderEmailEnv      = "POSTQRON_MAILRONIX_SENDER_EMAIL"
	postqronMailronixDomainVerifiedEnv   = "POSTQRON_MAILRONIX_DOMAIN_VERIFIED"
	postqronMailronixFailureThresholdEnv = "POSTQRON_MAILRONIX_FAILURE_THRESHOLD"
	postqronMailronixCircuitOpenForEnv   = "POSTQRON_MAILRONIX_CIRCUIT_OPEN_FOR"
)

var ErrInvalidSenderBoundary = errors.New("invalid email sender boundary")

type SenderMode string

const (
	SenderModeLive SenderMode = "live"
	SenderModeFake SenderMode = "fake"
	SenderModeNoop SenderMode = "noop"
)

// SenderBoundaryOptions carries the explicit runtime signal that F14 needs in
// order to decide whether a safe non-production sender is allowed.
type SenderBoundaryOptions struct {
	Environment string
	Production  bool
	Mode        SenderMode
	HTTPTimeout time.Duration
}

// SenderBoundary exposes a provider-neutral public boundary for wiring services.
// Callers depend only on F14's Sender interface and never construct
// MailronixClient directly.
type SenderBoundary struct {
	Sender Sender
	Mode   SenderMode
	Config SenderBoundaryConfig
}

// SenderBoundaryConfig is safe to log. It contains only redacted operational
// metadata and never the resolved API-key secret value.
type SenderBoundaryConfig struct {
	Environment      string
	Production       bool
	Mode             SenderMode
	Provider         string
	Endpoint         string
	ContractVersion  string
	APIKeySecretName string
	SenderEmail      string
	DomainVerified   bool
	FailureThreshold int
	CircuitOpenFor   time.Duration
	HTTPTimeout      time.Duration
}

func (config SenderBoundaryConfig) Fields() map[string]string {
	fields := map[string]string{
		"email.environment": config.Environment,
		"email.production":  strconv.FormatBool(config.Production),
		"email.sender_mode": string(config.Mode),
		"email.provider":    config.Provider,
	}
	if config.Endpoint != "" {
		fields["email.endpoint"] = config.Endpoint
	}
	if config.ContractVersion != "" {
		fields["email.contract_version"] = config.ContractVersion
	}
	if config.APIKeySecretName != "" {
		fields["email.api_key_secret_name"] = config.APIKeySecretName
	}
	if config.SenderEmail != "" {
		fields["email.sender_email"] = config.SenderEmail
	}
	if config.DomainVerified {
		fields["email.domain_verified"] = "true"
	}
	if config.FailureThreshold > 0 {
		fields["email.failure_threshold"] = strconv.Itoa(config.FailureThreshold)
	}
	if config.CircuitOpenFor > 0 {
		fields["email.circuit_open_for"] = config.CircuitOpenFor.String()
	}
	if config.HTTPTimeout > 0 {
		fields["email.http_timeout"] = config.HTTPTimeout.String()
	}
	return fields
}

func NewSenderFromEnv(
	options SenderBoundaryOptions,
	secrets SecretProvider,
) (Sender, error) {
	boundary, err := NewSenderBoundaryFromEnv(options, secrets)
	if err != nil {
		return nil, err
	}
	return boundary.Sender, nil
}

func NewSenderBoundaryFromEnv(
	options SenderBoundaryOptions,
	secrets SecretProvider,
) (SenderBoundary, error) {
	return newSenderBoundaryFromEnv(os.LookupEnv, options, secrets)
}

type envLookup func(string) (string, bool)

func newSenderBoundaryFromEnv(
	lookup envLookup,
	options SenderBoundaryOptions,
	secrets SecretProvider,
) (SenderBoundary, error) {
	normalized := normalizeSenderBoundaryOptions(options)
	mode, err := resolveSenderMode(normalized)
	if err != nil {
		return SenderBoundary{}, err
	}
	switch mode {
	case SenderModeFake:
		return SenderBoundary{
			Sender: &FakeSender{},
			Mode:   mode,
			Config: SenderBoundaryConfig{
				Environment: normalized.Environment,
				Production:  normalized.Production,
				Mode:        mode,
				Provider:    "fake",
			},
		}, nil
	case SenderModeNoop:
		return SenderBoundary{
			Sender: &NoopSender{},
			Mode:   mode,
			Config: SenderBoundaryConfig{
				Environment: normalized.Environment,
				Production:  normalized.Production,
				Mode:        mode,
				Provider:    "noop",
			},
		}, nil
	case SenderModeLive:
		if secrets == nil {
			return SenderBoundary{}, fmt.Errorf(
				"%w: live sender requires a secret provider",
				ErrInvalidSenderBoundary,
			)
		}
		config, redacted, err := loadMailronixBoundaryConfig(lookup, normalized)
		if err != nil {
			return SenderBoundary{}, err
		}
		client, err := NewMailronixClient(config, &http.Client{
			Timeout: normalized.HTTPTimeout,
		}, secrets)
		if err != nil {
			return SenderBoundary{}, err
		}
		return SenderBoundary{
			Sender: client,
			Mode:   mode,
			Config: redacted,
		}, nil
	default:
		return SenderBoundary{}, fmt.Errorf(
			"%w: unsupported sender mode",
			ErrInvalidSenderBoundary,
		)
	}
}

func normalizeSenderBoundaryOptions(
	options SenderBoundaryOptions,
) SenderBoundaryOptions {
	options.Environment = strings.ToLower(strings.TrimSpace(options.Environment))
	if options.HTTPTimeout <= 0 {
		options.HTTPTimeout = defaultSenderHTTPTimeout
	}
	return options
}

func resolveSenderMode(options SenderBoundaryOptions) (SenderMode, error) {
	mode := SenderMode(strings.ToLower(strings.TrimSpace(string(options.Mode))))
	if options.Production {
		if mode == "" {
			return "", fmt.Errorf(
				"%w: production requires an explicit live sender mode",
				ErrInvalidSenderBoundary,
			)
		}
		if mode != SenderModeLive {
			return "", fmt.Errorf(
				"%w: non-production sender modes are forbidden in production",
				ErrInvalidSenderBoundary,
			)
		}
		return SenderModeLive, nil
	}
	if mode == "" {
		if allowsDefaultFakeMode(options.Environment) {
			return SenderModeFake, nil
		}
		return "", fmt.Errorf(
			"%w: an explicit sender mode is required for environment %q",
			ErrInvalidSenderBoundary,
			safeEnvironmentLabel(options.Environment),
		)
	}
	switch mode {
	case SenderModeLive:
		return mode, nil
	case SenderModeFake:
		if !allowsFakeMode(options.Environment) {
			return "", fmt.Errorf(
				"%w: fake sender is only supported in local, development, test, or ci",
				ErrInvalidSenderBoundary,
			)
		}
		return mode, nil
	case SenderModeNoop:
		return mode, nil
	default:
		return "", fmt.Errorf(
			"%w: unsupported sender mode",
			ErrInvalidSenderBoundary,
		)
	}
}

func safeEnvironmentLabel(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func allowsDefaultFakeMode(environment string) bool {
	return allowsFakeMode(environment)
}

func allowsFakeMode(environment string) bool {
	switch environment {
	case "local", "development", "dev", "test", "ci":
		return true
	default:
		return false
	}
}

func loadMailronixBoundaryConfig(
	lookup envLookup,
	options SenderBoundaryOptions,
) (MailronixConfig, SenderBoundaryConfig, error) {
	endpoint, err := requiredEnvString(lookup, postqronMailronixEndpointEnv)
	if err != nil {
		return MailronixConfig{}, SenderBoundaryConfig{}, err
	}
	secretName, err := requiredEnvString(lookup, postqronMailronixAPIKeySecretNameEnv)
	if err != nil {
		return MailronixConfig{}, SenderBoundaryConfig{}, err
	}
	senderEmail, err := requiredEnvString(lookup, postqronMailronixSenderEmailEnv)
	if err != nil {
		return MailronixConfig{}, SenderBoundaryConfig{}, err
	}
	domainVerified, err := requiredEnvBool(lookup, postqronMailronixDomainVerifiedEnv)
	if err != nil {
		return MailronixConfig{}, SenderBoundaryConfig{}, err
	}
	failureThreshold, err := requiredEnvInt(
		lookup,
		postqronMailronixFailureThresholdEnv,
		defaultFailureThreshold,
	)
	if err != nil {
		return MailronixConfig{}, SenderBoundaryConfig{}, err
	}
	circuitOpenFor, err := requiredEnvDuration(
		lookup,
		postqronMailronixCircuitOpenForEnv,
		defaultCircuitOpenFor,
	)
	if err != nil {
		return MailronixConfig{}, SenderBoundaryConfig{}, err
	}
	mailronixConfig := MailronixConfig{
		Endpoint:         endpoint,
		ContractVersion:  MailronixContractVersion,
		APIKeySecret:     secretName,
		From:             SenderIdentity{Email: senderEmail},
		DomainVerified:   domainVerified,
		FailureThreshold: failureThreshold,
		CircuitOpenFor:   circuitOpenFor,
	}
	redacted := SenderBoundaryConfig{
		Environment:      safeEnvironmentLabel(options.Environment),
		Production:       options.Production,
		Mode:             SenderModeLive,
		Provider:         "mailronix",
		Endpoint:         endpoint,
		ContractVersion:  MailronixContractVersion,
		APIKeySecretName: secretName,
		SenderEmail:      senderEmail,
		DomainVerified:   domainVerified,
		FailureThreshold: failureThreshold,
		CircuitOpenFor:   circuitOpenFor,
		HTTPTimeout:      options.HTTPTimeout,
	}
	return mailronixConfig, redacted, nil
}

func requiredEnvString(lookup envLookup, name string) (string, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: missing %s", ErrInvalidSenderBoundary, name)
	}
	return strings.TrimSpace(value), nil
}

func requiredEnvBool(lookup envLookup, name string) (bool, error) {
	value, err := requiredEnvString(lookup, name)
	if err != nil {
		return false, err
	}
	parsed, parseErr := strconv.ParseBool(value)
	if parseErr != nil {
		return false, fmt.Errorf("%w: invalid %s", ErrInvalidSenderBoundary, name)
	}
	return parsed, nil
}

func requiredEnvInt(lookup envLookup, name string, fallback int) (int, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	parsed, parseErr := strconv.Atoi(value)
	if parseErr != nil || parsed < 1 {
		return 0, fmt.Errorf("%w: invalid %s", ErrInvalidSenderBoundary, name)
	}
	return parsed, nil
}

func requiredEnvDuration(
	lookup envLookup,
	name string,
	fallback time.Duration,
) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	parsed, parseErr := time.ParseDuration(value)
	if parseErr != nil || parsed <= 0 {
		return 0, fmt.Errorf("%w: invalid %s", ErrInvalidSenderBoundary, name)
	}
	return parsed, nil
}

// NoopSender is a non-production-only sender that validates the rendered
// payload and then discards it without attempting network delivery.
type NoopSender struct {
	mu      sync.Mutex
	counter int
}

func (sender *NoopSender) Send(
	_ context.Context,
	message RenderedMessage,
) (ProviderReceipt, error) {
	if err := validateRenderedMessage(message); err != nil {
		return ProviderReceipt{}, err
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.counter++
	return ProviderReceipt{
		MessageID: fmt.Sprintf("noop_%d", sender.counter),
	}, nil
}
