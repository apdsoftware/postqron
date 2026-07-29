package email

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mapSecrets map[string]string

func (secrets mapSecrets) Secret(_ context.Context, name string) (string, error) {
	value, exists := secrets[name]
	if !exists {
		return "", errors.New("secret not found")
	}
	return value, nil
}

func mailronixTestConfig(endpoint string) MailronixConfig {
	return MailronixConfig{
		Endpoint: endpoint, ContractVersion: MailronixContractVersion,
		APIKeySecret:   "MAILRONIX_TRANSACTIONAL_API_KEY",
		From:           SenderIdentity{Email: "notifications@postqron.example"},
		DomainVerified: true, FailureThreshold: 3, CircuitOpenFor: time.Minute,
	}
}

func TestMailronixContractAuthenticationAndSerialization(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/email/send" || request.Method != http.MethodPost {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer mrx_live_test_secret" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("Idempotency-Key") != "" {
			t.Fatal("adapter invented an unsupported Mailronix idempotency header")
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"from", "to", "subject", "html_body", "text_body"} {
			if payload[key] == nil || payload[key] == "" {
				t.Fatalf("missing official request field %q: %#v", key, payload)
			}
		}
		if len(payload) != 5 {
			t.Fatalf("adapter sent undocumented fields: %#v", payload)
		}
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusAccepted)
		_, _ = response.Write([]byte(
			`{"status":"queued","email_log_id":"9c4e2f1a-7d3b-4a5e-8f6c-1b2a3d4e5f6a"}`,
		))
	}))
	defer server.Close()
	server.Client().Timeout = time.Second
	client, err := NewMailronixClient(
		mailronixTestConfig(server.URL+"/email/send"),
		server.Client(),
		mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_test_secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.Send(context.Background(), RenderedMessage{
		Channel: ChannelTransactional, Template: TemplateWelcome,
		Recipient: Recipient{ID: "account_1", Email: "person@example.test"},
		Subject:   "Welcome", HTML: "<p>Welcome</p>", Text: "Welcome",
	})
	if err != nil || receipt.MessageID != "9c4e2f1a-7d3b-4a5e-8f6c-1b2a3d4e5f6a" {
		t.Fatalf("Send() = %#v, %v", receipt, err)
	}
}

func TestMailronixMapsDocumentedErrorsAndRedactsDiagnostics(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				if status == 429 {
					response.Header().Set("Retry-After", "120")
				}
				response.WriteHeader(status)
				_, _ = response.Write([]byte(
					`{"error":{"code":"provider_code","message":"person@example.test token=private"}}`,
				))
			}))
			defer server.Close()
			server.Client().Timeout = time.Second
			client, err := NewMailronixClient(
				mailronixTestConfig(server.URL+"/email/send"),
				server.Client(),
				mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_secret"},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Send(context.Background(), RenderedMessage{
				Channel: ChannelTransactional, Recipient: Recipient{Email: "person@example.test"},
				Subject: "Subject", HTML: "<p>Body</p>", Text: "Body",
			})
			failure, ok := err.(*MailronixError)
			wantRetry := status == 429 || status == 500 || status == 503
			if !ok || failure.Retryable != wantRetry {
				t.Fatalf("error = %#v, want retryable %v", err, wantRetry)
			}
			if strings.Contains(failure.Detail, "person@") ||
				strings.Contains(failure.Detail, "private") {
				t.Fatalf("diagnostic leaked PII/secret: %q", failure.Detail)
			}
			if status == 429 && failure.RetryAfter != 2*time.Minute {
				t.Fatalf("RetryAfter = %s", failure.RetryAfter)
			}
		})
	}
}

func TestMailronixRedactsVerificationURLsInProviderDiagnostics(t *testing.T) {
	for name, message := range map[string]string{
		"query_token": `{"error":{"code":"provider_code","message":"https://app.example.test/verify-email?verification_token=abc123&email=person@example.test"}}`,
		"path_token":  `{"error":{"code":"provider_code","message":"https://app.example.test/verify-email/abc123?email=person@example.test"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				_ *http.Request,
			) {
				response.WriteHeader(http.StatusTooManyRequests)
				_, _ = response.Write([]byte(message))
			}))
			defer server.Close()
			server.Client().Timeout = time.Second
			client, err := NewMailronixClient(
				mailronixTestConfig(server.URL+"/email/send"),
				server.Client(),
				mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_secret"},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Send(context.Background(), RenderedMessage{
				Channel: ChannelTransactional, Recipient: Recipient{Email: "person@example.test"},
				Subject: "Subject", HTML: "<p>Body</p>", Text: "Body",
			})
			failure, ok := err.(*MailronixError)
			if !ok {
				t.Fatalf("error = %#v", err)
			}
			if strings.Contains(failure.Detail, "abc123") ||
				strings.Contains(failure.Detail, "person@") ||
				strings.Contains(failure.Detail, "verify-email") ||
				!strings.Contains(failure.Detail, "[redacted-url]") {
				t.Fatalf("diagnostic leaked verification URL: %q", failure.Detail)
			}
		})
	}
}

func TestMailronixCircuitBreakerAndConfigurationGates(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		requests++
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = response.Write([]byte(`{"error":{"code":"auth_unavailable","message":"wait"}}`))
	}))
	defer server.Close()
	server.Client().Timeout = time.Second
	config := mailronixTestConfig(server.URL + "/email/send")
	config.FailureThreshold = 2
	client, err := NewMailronixClient(
		config, server.Client(),
		mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	message := RenderedMessage{
		Channel: ChannelTransactional, Recipient: Recipient{Email: "person@example.test"},
		Subject: "Subject", HTML: "<p>Body</p>", Text: "Body",
	}
	for range 3 {
		_, _ = client.Send(context.Background(), message)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, circuit did not open", requests)
	}

	config.DomainVerified = false
	if _, err := NewMailronixClient(config, server.Client(), mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_secret"}); err == nil {
		t.Fatal("client accepted an unverified sender domain")
	}
	config.DomainVerified = true
	config.ContractVersion = "2.0.0"
	if _, err := NewMailronixClient(config, server.Client(), mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_secret"}); err == nil {
		t.Fatal("client accepted an unverified contract version")
	}
}

func TestMailronixTimeoutIsRetryable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		_ http.ResponseWriter,
		_ *http.Request,
	) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	clientHTTP := server.Client()
	clientHTTP.Timeout = 20 * time.Millisecond
	client, err := NewMailronixClient(
		mailronixTestConfig(server.URL+"/email/send"),
		clientHTTP,
		mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(context.Background(), RenderedMessage{
		Channel:   ChannelTransactional,
		Recipient: Recipient{Email: "person@example.test"},
		Subject:   "Subject", HTML: "<p>Body</p>", Text: "Body",
	})
	failure, ok := err.(*MailronixError)
	if !ok || failure.Code != "transport_error" || !failure.Retryable {
		t.Fatalf("timeout error = %#v", err)
	}
}

func TestMailronixRejectsInvalidPayloadAndUnsafeSecretBeforeNetwork(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		requests++
		response.WriteHeader(http.StatusAccepted)
		_, _ = response.Write([]byte(
			`{"status":"queued","email_log_id":"9c4e2f1a-7d3b-4a5e-8f6c-1b2a3d4e5f6a"}`,
		))
	}))
	defer server.Close()
	server.Client().Timeout = time.Second

	valid := RenderedMessage{
		Channel:   ChannelTransactional,
		Recipient: Recipient{Email: "person@example.test"},
		Subject:   "Subject", HTML: "<p>Body</p>", Text: "Body",
	}
	for name, mutate := range map[string]func(*RenderedMessage){
		"channel":   func(message *RenderedMessage) { message.Channel = "marketing" },
		"recipient": func(message *RenderedMessage) { message.Recipient.Email = "invalid" },
		"subject":   func(message *RenderedMessage) { message.Subject = " " },
		"body": func(message *RenderedMessage) {
			message.HTML = ""
			message.Text = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := NewMailronixClient(
				mailronixTestConfig(server.URL+"/email/send"),
				server.Client(),
				mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": "mrx_live_secret"},
			)
			if err != nil {
				t.Fatal(err)
			}
			message := valid
			mutate(&message)
			if _, err := client.Send(context.Background(), message); err == nil {
				t.Fatal("Send() accepted an invalid rendered message")
			}
		})
	}

	client, err := NewMailronixClient(
		mailronixTestConfig(server.URL+"/email/send"),
		server.Client(),
		mapSecrets{"MAILRONIX_TRANSACTIONAL_API_KEY": " mrx_live_secret\n"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(context.Background(), valid)
	failure, ok := err.(*MailronixError)
	if !ok || failure.Code != "secret_unavailable" || failure.Retryable {
		t.Fatalf("unsafe API key error = %#v", err)
	}
	if requests != 0 {
		t.Fatalf("invalid input reached Mailronix %d times", requests)
	}
}

func TestFakeSenderCannotReachRealRecipients(t *testing.T) {
	sender := &FakeSender{}
	message := RenderedMessage{
		Channel:   ChannelTransactional,
		Recipient: Recipient{Email: "person@example.test"},
	}
	if _, err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	message.Recipient.Email = "person@gmail.com"
	if _, err := sender.Send(context.Background(), message); err == nil {
		t.Fatal("fake sender accepted a real recipient")
	}
	message.Recipient.Email = "Person <person@example.test>"
	if _, err := sender.Send(context.Background(), message); err == nil {
		t.Fatal("fake sender accepted a display-name address")
	}
	if len(sender.Messages()) != 1 {
		t.Fatalf("captured messages = %d", len(sender.Messages()))
	}
}
