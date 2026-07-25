package email

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MailronixContractVersion = "1.0.0"
	MailronixProductionURL   = "https://api.mailronix.com/email/send"
)

var mailronixUUID = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`,
)

type SecretProvider interface {
	Secret(context.Context, string) (string, error)
}

type SenderIdentity struct {
	Email string
}

type MailronixConfig struct {
	Endpoint         string
	ContractVersion  string
	APIKeySecret     string
	From             SenderIdentity
	DomainVerified   bool
	FailureThreshold int
	CircuitOpenFor   time.Duration
}

type MailronixClient struct {
	endpoint *url.URL
	http     *http.Client
	secrets  SecretProvider
	config   MailronixConfig
	now      func() time.Time

	mu            sync.Mutex
	failures      int
	circuitOpenTo time.Time
}

type MailronixError struct {
	Code       string
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
	Detail     string
}

func (failure *MailronixError) Error() string {
	if failure.Detail == "" {
		return failure.Code
	}
	return failure.Code + ": " + failure.Detail
}

func NewMailronixClient(
	config MailronixConfig,
	httpClient *http.Client,
	secrets SecretProvider,
) (*MailronixClient, error) {
	endpoint, err := url.ParseRequestURI(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" ||
		endpoint.Path != "/email/send" || endpoint.RawQuery != "" ||
		endpoint.Fragment != "" || endpoint.User != nil {
		return nil, errors.New("Mailronix endpoint must be an absolute HTTPS /email/send URL")
	}
	if config.ContractVersion != MailronixContractVersion {
		return nil, errors.New("unsupported Mailronix contract version")
	}
	if httpClient == nil || httpClient.Timeout <= 0 {
		return nil, errors.New("bounded HTTP client is required")
	}
	if secrets == nil || !validSecretName(config.APIKeySecret) {
		return nil, errors.New("external API-key secret is required")
	}
	if _, err := mailAddress(config.From.Email); err != nil {
		return nil, errors.New("valid sender identity is required")
	}
	if !config.DomainVerified {
		return nil, errors.New("Mailronix sender domain must be verified before live delivery")
	}
	if config.FailureThreshold < 1 || config.CircuitOpenFor <= 0 {
		return nil, errors.New("valid Mailronix circuit-breaker policy is required")
	}
	return &MailronixClient{
		endpoint: endpoint,
		http:     httpClient, secrets: secrets, config: config, now: time.Now,
	}, nil
}

func (client *MailronixClient) Send(
	ctx context.Context,
	message RenderedMessage,
) (ProviderReceipt, error) {
	if err := validateRenderedMessage(message); err != nil {
		return ProviderReceipt{}, err
	}
	if err := client.allowRequest(); err != nil {
		return ProviderReceipt{}, err
	}
	apiKey, err := client.secrets.Secret(ctx, client.config.APIKeySecret)
	if err != nil || !validMailronixAPIKey(apiKey) {
		failure := &MailronixError{
			Code: "secret_unavailable", Retryable: err != nil,
			Detail: "Mailronix credentials are unavailable or invalid",
		}
		client.recordFailure(failure)
		return ProviderReceipt{}, failure
	}

	payload := struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
	}{
		From: client.config.From.Email, To: message.Recipient.Email,
		Subject: message.Subject, HTMLBody: message.HTML, TextBody: message.Text,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProviderReceipt{}, fmt.Errorf("encode Mailronix request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, client.endpoint.String(), bytes.NewReader(body),
	)
	if err != nil {
		return ProviderReceipt{}, fmt.Errorf("create Mailronix request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := client.http.Do(request)
	if err != nil {
		failure := &MailronixError{
			Code: "transport_error", Retryable: true,
			Detail: "Mailronix did not return a response",
		}
		client.recordFailure(failure)
		return ProviderReceipt{}, failure
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		failure := &MailronixError{
			Code: "response_read_error", StatusCode: response.StatusCode,
			Retryable: true, Detail: "Mailronix returned an unreadable response",
		}
		client.recordFailure(failure)
		return ProviderReceipt{}, failure
	}
	if response.StatusCode != http.StatusAccepted {
		failure := classifyMailronixResponse(response, responseBody, client.now())
		client.recordFailure(failure)
		return ProviderReceipt{}, failure
	}
	var accepted struct {
		Status     string `json:"status"`
		EmailLogID string `json:"email_log_id"`
	}
	if err := json.Unmarshal(responseBody, &accepted); err != nil ||
		accepted.Status != "queued" || !mailronixUUID.MatchString(accepted.EmailLogID) {
		failure := &MailronixError{
			Code: "invalid_response", StatusCode: response.StatusCode,
			Retryable: true, Detail: "Mailronix returned an invalid queued receipt",
		}
		client.recordFailure(failure)
		return ProviderReceipt{}, failure
	}
	client.recordSuccess()
	return ProviderReceipt{MessageID: accepted.EmailLogID}, nil
}

func (client *MailronixClient) allowRequest() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	now := client.now()
	if client.circuitOpenTo.After(now) {
		return &MailronixError{
			Code: "circuit_open", Retryable: true,
			RetryAfter: client.circuitOpenTo.Sub(now),
			Detail:     "Mailronix delivery circuit is temporarily open",
		}
	}
	return nil
}

func (client *MailronixClient) recordFailure(failure *MailronixError) {
	if !failure.Retryable {
		return
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	client.failures++
	if client.failures >= client.config.FailureThreshold {
		client.circuitOpenTo = client.now().Add(client.config.CircuitOpenFor)
		client.failures = 0
	}
}

func (client *MailronixClient) recordSuccess() {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.failures = 0
	client.circuitOpenTo = time.Time{}
}

func classifyMailronixResponse(
	response *http.Response,
	body []byte,
	now time.Time,
) *MailronixError {
	var provider struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &provider)
	code := provider.Error.Code
	if code == "" {
		code = "mailronix_http_" + strconv.Itoa(response.StatusCode)
	}
	retryable := response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode == http.StatusInternalServerError ||
		response.StatusCode == http.StatusServiceUnavailable
	return &MailronixError{
		Code: sanitizeCode(code), StatusCode: response.StatusCode,
		Retryable:  retryable,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), now),
		Detail:     SanitizeDiagnostic(firstNonEmpty(provider.Error.Message, http.StatusText(response.StatusCode))),
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

func sanitizeCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var output strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '_' || character == '-' {
			output.WriteRune(character)
		}
	}
	if output.Len() == 0 {
		return "provider_error"
	}
	if output.Len() > 64 {
		return output.String()[:64]
	}
	return output.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func mailAddress(value string) (string, error) {
	parsed, err := mail.ParseAddress(value)
	if err != nil || !strings.EqualFold(parsed.Address, strings.TrimSpace(value)) {
		return "", errors.New("invalid email address")
	}
	return parsed.Address, nil
}

func validateRenderedMessage(message RenderedMessage) error {
	if message.Channel != ChannelTransactional {
		return ErrInvalidChannel
	}
	if _, err := mailAddress(message.Recipient.Email); err != nil {
		return ErrInvalidRecipient
	}
	if strings.TrimSpace(message.Subject) == "" ||
		strings.TrimSpace(message.HTML) == "" && strings.TrimSpace(message.Text) == "" {
		return ErrInvalidMessage
	}
	return nil
}

func validMailronixAPIKey(value string) bool {
	if !strings.HasPrefix(value, "mrx_live_") || len(value) == len("mrx_live_") {
		return false
	}
	// The official contract does not specify a narrower token alphabet. Reject
	// whitespace, control bytes, and non-ASCII bytes that are unsafe in an HTTP
	// Authorization header without inventing additional provider constraints.
	for index := range len(value) {
		if value[index] <= 0x20 || value[index] >= 0x7f {
			return false
		}
	}
	return true
}

// FakeSender is the only supported sender for tests and local development. It
// refuses non-reserved recipient domains so it cannot deliver real email.
type FakeSender struct {
	mu       sync.Mutex
	messages []RenderedMessage
}

func (sender *FakeSender) Send(
	_ context.Context,
	message RenderedMessage,
) (ProviderReceipt, error) {
	address, err := mailAddress(message.Recipient.Email)
	if err != nil {
		return ProviderReceipt{}, ErrInvalidRecipient
	}
	domain := strings.ToLower(strings.SplitN(address, "@", 2)[1])
	if domain != "example.test" && !strings.HasSuffix(domain, ".example.test") &&
		domain != "example.invalid" && !strings.HasSuffix(domain, ".example.invalid") {
		return ProviderReceipt{}, errors.New("fake sender refuses real recipient domains")
	}
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.messages = append(sender.messages, message)
	return ProviderReceipt{MessageID: fmt.Sprintf("fake_%d", len(sender.messages))}, nil
}

func (sender *FakeSender) Messages() []RenderedMessage {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return append([]RenderedMessage(nil), sender.messages...)
}
