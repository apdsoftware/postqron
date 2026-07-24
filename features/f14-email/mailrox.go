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
	"strconv"
	"strings"
	"time"
)

type SecretProvider interface {
	Secret(context.Context, string) (string, error)
}

type SenderIdentity struct {
	Email string
	Name  string
}

type MailroxConfig struct {
	Endpoint            string
	TransactionalSecret string
	MarketingSecret     string
	TransactionalFrom   SenderIdentity
	MarketingFrom       SenderIdentity
}

type MailroxClient struct {
	endpoint *url.URL
	http     *http.Client
	secrets  SecretProvider
	config   MailroxConfig
}

type MailroxError struct {
	Code       string
	StatusCode int
	Retryable  bool
	RetryAfter time.Duration
	Detail     string
}

func (failure *MailroxError) Error() string {
	if failure.Detail == "" {
		return failure.Code
	}
	return failure.Code + ": " + failure.Detail
}

func NewMailroxClient(
	config MailroxConfig,
	httpClient *http.Client,
	secrets SecretProvider,
) (*MailroxClient, error) {
	endpoint, err := url.ParseRequestURI(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return nil, errors.New("Mailrox endpoint must be an absolute HTTPS URL")
	}
	if httpClient == nil || httpClient.Timeout <= 0 {
		return nil, errors.New("bounded HTTP client is required")
	}
	if secrets == nil {
		return nil, errors.New("external secret provider is required")
	}
	if !validSecretName(config.TransactionalSecret) ||
		!validSecretName(config.MarketingSecret) ||
		config.TransactionalSecret == config.MarketingSecret {
		return nil, errors.New("distinct transactional and marketing secret names are required")
	}
	for _, identity := range []SenderIdentity{config.TransactionalFrom, config.MarketingFrom} {
		if _, err := mailAddress(identity.Email); err != nil {
			return nil, errors.New("valid sender identities are required")
		}
	}
	return &MailroxClient{
		endpoint: endpoint,
		http:     httpClient,
		secrets:  secrets,
		config:   config,
	}, nil
}

func (client *MailroxClient) Send(
	ctx context.Context,
	message RenderedMessage,
) (ProviderReceipt, error) {
	expectedChannel, exists := templateChannels[message.Template]
	if !exists || expectedChannel != message.Channel {
		return ProviderReceipt{}, ErrTemplateChannel
	}
	if message.Channel == ChannelMarketing {
		if message.Headers["List-Unsubscribe"] == "" ||
			message.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
			return ProviderReceipt{}, ErrUnsubscribeRequired
		}
	}
	secretName, sender, err := client.channelConfig(message.Channel)
	if err != nil {
		return ProviderReceipt{}, err
	}
	apiKey, err := client.secrets.Secret(ctx, secretName)
	if err != nil || strings.TrimSpace(apiKey) == "" {
		return ProviderReceipt{}, &MailroxError{
			Code:      "secret_unavailable",
			Retryable: true,
			Detail:    "Mailrox credentials are temporarily unavailable",
		}
	}

	payload := struct {
		From    mailroxAddress    `json:"from"`
		To      []mailroxAddress  `json:"to"`
		Subject string            `json:"subject"`
		HTML    string            `json:"html"`
		Text    string            `json:"text"`
		Headers map[string]string `json:"headers,omitempty"`
		Tags    []string          `json:"tags"`
	}{
		From: mailroxAddress{Email: sender.Email, Name: sender.Name},
		To: []mailroxAddress{{
			Email: message.Recipient.Email,
			Name:  message.Recipient.Name,
		}},
		Subject: message.Subject,
		HTML:    message.HTML,
		Text:    message.Text,
		Headers: message.Headers,
		Tags:    []string{string(message.Channel), string(message.Template)},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ProviderReceipt{}, fmt.Errorf("encode Mailrox request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.endpoint.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return ProviderReceipt{}, fmt.Errorf("create Mailrox request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Idempotency-Key", message.IdempotencyKey)

	response, err := client.http.Do(request)
	if err != nil {
		return ProviderReceipt{}, &MailroxError{
			Code:      "transport_error",
			Retryable: true,
			Detail:    "Mailrox did not return a response",
		}
	}
	defer response.Body.Close()
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		return ProviderReceipt{}, &MailroxError{
			Code:       "response_read_error",
			StatusCode: response.StatusCode,
			Retryable:  true,
			Detail:     "Mailrox returned an unreadable response",
		}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ProviderReceipt{}, classifyMailroxResponse(response, responseBody)
	}

	var accepted struct {
		ID        string `json:"id"`
		MessageID string `json:"message_id"`
		Data      struct {
			ID        string `json:"id"`
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &accepted); err != nil {
		return ProviderReceipt{}, &MailroxError{
			Code:       "invalid_response",
			StatusCode: response.StatusCode,
			Retryable:  true,
			Detail:     "Mailrox accepted the request but returned an invalid receipt",
		}
	}
	providerID := firstNonEmpty(
		accepted.MessageID,
		accepted.ID,
		accepted.Data.MessageID,
		accepted.Data.ID,
	)
	if providerID == "" {
		return ProviderReceipt{}, &MailroxError{
			Code:       "missing_provider_id",
			StatusCode: response.StatusCode,
			Retryable:  true,
			Detail:     "Mailrox accepted the request without a message identifier",
		}
	}
	return ProviderReceipt{MessageID: providerID}, nil
}

type mailroxAddress struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

func (client *MailroxClient) channelConfig(channel Channel) (string, SenderIdentity, error) {
	switch channel {
	case ChannelTransactional:
		return client.config.TransactionalSecret, client.config.TransactionalFrom, nil
	case ChannelMarketing:
		return client.config.MarketingSecret, client.config.MarketingFrom, nil
	default:
		return "", SenderIdentity{}, ErrInvalidChannel
	}
}

func classifyMailroxResponse(response *http.Response, body []byte) *MailroxError {
	var provider struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(body, &provider)
	code := provider.Code
	if code == "" {
		code = "mailrox_http_" + strconv.Itoa(response.StatusCode)
	}
	detail := firstNonEmpty(provider.Message, provider.Error, http.StatusText(response.StatusCode))
	retryable := response.StatusCode == http.StatusRequestTimeout ||
		response.StatusCode == http.StatusTooEarly ||
		response.StatusCode == http.StatusTooManyRequests ||
		response.StatusCode >= http.StatusInternalServerError
	return &MailroxError{
		Code:       sanitizeCode(code),
		StatusCode: response.StatusCode,
		Retryable:  retryable,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
		Detail:     SanitizeDiagnostic(detail),
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
