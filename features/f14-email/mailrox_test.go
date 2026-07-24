package email

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMailroxClientUsesSeparatedExternalSecretsAndIdempotency(t *testing.T) {
	var authorizations []string
	var idempotencyKeys []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		idempotencyKeys = append(idempotencyKeys, request.Header.Get("Idempotency-Key"))
		var payload struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Tags) != 2 {
			t.Errorf("tags = %#v", payload.Tags)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"message_id":"provider_123"}`))
	}))
	defer server.Close()
	server.Client().Timeout = time.Second

	client, err := NewMailroxClient(
		MailroxConfig{
			Endpoint:            server.URL,
			TransactionalSecret: "MAILROX_TRANSACTIONAL_API_KEY",
			MarketingSecret:     "MAILROX_MARKETING_API_KEY",
			TransactionalFrom: SenderIdentity{
				Email: "notifications@example.test",
				Name:  "Postqron",
			},
			MarketingFrom: SenderIdentity{
				Email: "updates@example.test",
				Name:  "Postqron",
			},
		},
		server.Client(),
		mapSecrets{
			"MAILROX_TRANSACTIONAL_API_KEY": "transactional-secret",
			"MAILROX_MARKETING_API_KEY":     "marketing-secret",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range []Channel{ChannelTransactional, ChannelMarketing} {
		templateID := TemplateWelcome
		headers := map[string]string{}
		if channel == ChannelMarketing {
			templateID = TemplateMarketingUpdate
			headers["List-Unsubscribe"] = "<https://app.example.test/unsubscribe?token=opaque>"
			headers["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
		}
		receipt, err := client.Send(context.Background(), RenderedMessage{
			MessageID:      "email_1",
			IdempotencyKey: "event:1",
			Channel:        channel,
			Template:       templateID,
			Recipient: Recipient{
				ID:    "account_1",
				Email: "persona@example.test",
			},
			Subject: "Oggetto",
			HTML:    "<p>Corpo</p>",
			Text:    "Corpo",
			Headers: headers,
		})
		if err != nil || receipt.MessageID != "provider_123" {
			t.Fatalf("Send(%s) = %#v, %v", channel, receipt, err)
		}
	}
	if authorizations[0] != "Bearer transactional-secret" ||
		authorizations[1] != "Bearer marketing-secret" {
		t.Fatalf("authorization headers = %#v", authorizations)
	}
	if idempotencyKeys[0] != "event:1" || idempotencyKeys[1] != "event:1" {
		t.Fatalf("idempotency headers = %#v", idempotencyKeys)
	}
}

func TestMailroxClientClassifiesAndRedactsRetryableResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		response.Header().Set("Retry-After", "120")
		response.WriteHeader(http.StatusTooManyRequests)
		_, _ = response.Write([]byte(
			`{"code":"rate_limited","message":"persona@example.test token=private"}`,
		))
	}))
	defer server.Close()
	server.Client().Timeout = time.Second
	client, err := NewMailroxClient(
		MailroxConfig{
			Endpoint:            server.URL,
			TransactionalSecret: "TX",
			MarketingSecret:     "MK",
			TransactionalFrom:   SenderIdentity{Email: "tx@example.test"},
			MarketingFrom:       SenderIdentity{Email: "mk@example.test"},
		},
		server.Client(),
		mapSecrets{"TX": "tx-secret", "MK": "mk-secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Send(context.Background(), RenderedMessage{
		IdempotencyKey: "event:1",
		Channel:        ChannelTransactional,
		Template:       TemplateWelcome,
		Recipient:      Recipient{Email: "persona@example.test"},
		Subject:        "Oggetto",
		HTML:           "<p>Corpo</p>",
		Text:           "Corpo",
	})
	failure, ok := err.(*MailroxError)
	if !ok || !failure.Retryable || failure.RetryAfter != 2*time.Minute {
		t.Fatalf("Send() error = %#v", err)
	}
	if strings.Contains(failure.Detail, "persona@") ||
		strings.Contains(failure.Detail, "private") {
		t.Fatalf("diagnostic leaked sensitive data: %q", failure.Detail)
	}
}

func TestMailroxClientRequiresDistinctSecretNames(t *testing.T) {
	_, err := NewMailroxClient(
		MailroxConfig{
			Endpoint:            "https://mailrox.example.test/send",
			TransactionalSecret: "SAME",
			MarketingSecret:     "SAME",
			TransactionalFrom:   SenderIdentity{Email: "tx@example.test"},
			MarketingFrom:       SenderIdentity{Email: "mk@example.test"},
		},
		&http.Client{Timeout: time.Second},
		mapSecrets{"SAME": "secret"},
	)
	if err == nil {
		t.Fatal("NewMailroxClient() accepted a shared marketing/transactional secret")
	}
}
