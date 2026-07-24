package email

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWebhookRecordsHardBounceOnceAndSuppressesRecipient(t *testing.T) {
	store := NewMemoryStore()
	sender := &scriptedSender{}
	service, now := testService(t, store, sender)
	result, err := service.Enqueue(
		context.Background(),
		testMessage(ChannelTransactional, TemplateWelcome),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DispatchOne(context.Background()); err != nil {
		t.Fatal(err)
	}

	secret := strings.Repeat("w", 32)
	handler, err := NewWebhookHandler(
		store,
		mapSecrets{"MAILROX_WEBHOOK_SECRET": secret},
		"MAILROX_WEBHOOK_SECRET",
		5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	body := []byte(`{
		"id":"event_1",
		"type":"hard_bounce",
		"message_id":"mailrox_1",
		"recipient_id":"account_1",
		"code":"mailbox_missing",
		"diagnostic":"persona@example.test does not exist",
		"occurred_at":"2026-07-24T12:00:00Z"
	}`)
	for range 2 {
		request := httptest.NewRequest(
			http.MethodPost,
			"https://api.example.test/webhooks/mailrox",
			bytes.NewReader(body),
		)
		request.Header.Set("X-Mailrox-Signature", testWebhookSignature(
			now.Unix(),
			body,
			secret,
		))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("webhook status = %d, body = %q", response.Code, response.Body.String())
		}
	}
	delivery, _ := store.Delivery(result.ID)
	if delivery.State != StateBounced {
		t.Fatalf("delivery state = %s, want bounced", delivery.State)
	}
	if strings.Contains(delivery.LastDiagnostic.Detail, "persona@") {
		t.Fatalf("diagnostic leaked recipient: %q", delivery.LastDiagnostic.Detail)
	}
	for _, channel := range []Channel{ChannelTransactional, ChannelMarketing} {
		suppressed, _ := store.IsSuppressed(
			context.Background(),
			"account_1",
			channel,
		)
		if !suppressed {
			t.Fatalf("%s was not suppressed after hard bounce", channel)
		}
	}
}

func TestWebhookRejectsInvalidOrStaleSignature(t *testing.T) {
	handler, err := NewWebhookHandler(
		NewMemoryStore(),
		mapSecrets{"WEBHOOK": strings.Repeat("w", 32)},
		"WEBHOOK",
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	handler.now = func() time.Time { return now }
	body := []byte(`{}`)
	for name, signature := range map[string]string{
		"invalid": "t=" + strconv.FormatInt(now.Unix(), 10) + ",v1=00",
		"stale": testWebhookSignature(
			now.Add(-2*time.Minute).Unix(),
			body,
			strings.Repeat("w", 32),
		),
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"https://api.example.test/webhooks/mailrox",
				bytes.NewReader(body),
			)
			request.Header.Set("X-Mailrox-Signature", signature)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.Code)
			}
		})
	}
}

func testWebhookSignature(timestamp int64, body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "t=" + strconv.FormatInt(timestamp, 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
