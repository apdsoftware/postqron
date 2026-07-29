package email

import (
	"context"
	"strings"
	"testing"
	"time"
)

type scriptedSender struct {
	errors   []error
	messages []RenderedMessage
}

func (sender *scriptedSender) Send(
	_ context.Context,
	message RenderedMessage,
) (ProviderReceipt, error) {
	sender.messages = append(sender.messages, message)
	if len(sender.errors) > 0 {
		err := sender.errors[0]
		sender.errors = sender.errors[1:]
		if err != nil {
			return ProviderReceipt{}, err
		}
	}
	return ProviderReceipt{MessageID: "mailronix_1"}, nil
}

func testService(
	t *testing.T,
	store *MemoryStore,
	sender Sender,
) (*Service, time.Time) {
	t.Helper()
	service, err := NewService(
		store, testRenderer(t), sender,
		RetryPolicy{BaseDelay: time.Minute, MaxDelay: time.Hour},
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	service.newID = func() (string, error) { return "email_generated", nil }
	return service, now
}

func TestServiceEnqueueIsIdempotentAndPinsRenderedLocale(t *testing.T) {
	store := NewMemoryStore()
	sender := &scriptedSender{}
	service, _ := testService(t, store, sender)
	message := testMessage(TemplateWelcome)
	message.ID = ""
	message.CreatedAt = time.Time{}
	message.Recipient.Locale = "fr-FR"

	first, err := service.Enqueue(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	message.Recipient.Locale = "de-DE"
	second, err := service.Enqueue(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.ID != second.ID {
		t.Fatalf("idempotency results = %#v, %#v", first, second)
	}
	if _, err := service.DispatchOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0].Locale != LocaleFrench {
		t.Fatalf("send did not preserve first locale: %#v", sender.messages)
	}
	delivery, _ := store.Delivery(first.ID)
	if delivery.State != StateAccepted || delivery.ProviderMessageID != "mailronix_1" {
		t.Fatalf("delivery = %#v", delivery)
	}
}

func TestServiceRetriesTransientFailureThenAccepts(t *testing.T) {
	store := NewMemoryStore()
	sender := &scriptedSender{errors: []error{
		&MailronixError{
			Code: "rate_limited", Retryable: true, RetryAfter: 10 * time.Minute,
			Detail: "wait for persona@example.test Bearer secret-value",
		},
		nil,
	}}
	service, now := testService(t, store, sender)
	result, err := service.Enqueue(context.Background(), testMessage(TemplateAccountSecurity))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DispatchOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	delivery, _ := store.Delivery(result.ID)
	if delivery.State != StateRetry ||
		!delivery.NextAttemptAt.Equal(now.Add(10*time.Minute)) ||
		delivery.LastDiagnostic.Detail != "wait for [redacted] [redacted]" {
		t.Fatalf("retry delivery = %#v", delivery)
	}
	service.now = func() time.Time { return now.Add(10 * time.Minute) }
	if _, err := service.DispatchOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	delivery, _ = store.Delivery(result.ID)
	if delivery.State != StateAccepted || delivery.Attempt != 2 {
		t.Fatalf("accepted delivery = %#v", delivery)
	}
}

func TestServiceStopsAfterPermanentFailure(t *testing.T) {
	store := NewMemoryStore()
	sender := &scriptedSender{errors: []error{
		&MailronixError{Code: "domain_not_verified", Detail: "sender rejected"},
	}}
	service, _ := testService(t, store, sender)
	result, err := service.Enqueue(context.Background(), testMessage(TemplateBilling))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DispatchOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	delivery, _ := store.Delivery(result.ID)
	if delivery.State != StateFailed || delivery.LastDiagnostic.Retryable {
		t.Fatalf("failed delivery = %#v", delivery)
	}
}

func TestServiceRedactsVerificationTokenFromDiagnostics(t *testing.T) {
	for name, detail := range map[string]string{
		"query_token": "retry https://app.example.test/verify-email?verification_token=abc123&email=persona@example.test",
		"path_token":  "retry https://app.example.test/verify/abc123?email=persona@example.test",
	} {
		t.Run(name, func(t *testing.T) {
			store := NewMemoryStore()
			sender := &scriptedSender{errors: []error{
				&MailronixError{
					Code: "rate_limited", Retryable: true,
					Detail: detail,
				},
			}}
			service, _ := testService(t, store, sender)
			result, err := service.Enqueue(context.Background(), testMessage(TemplateAccountVerification))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.DispatchOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			delivery, _ := store.Delivery(result.ID)
			if delivery.State != StateRetry {
				t.Fatalf("delivery state = %s", delivery.State)
			}
			if got := delivery.LastDiagnostic.Detail; strings.Contains(got, "abc123") ||
				strings.Contains(got, "persona@") ||
				strings.Contains(got, "verify-email") ||
				strings.Contains(got, "/verify/") ||
				!strings.Contains(got, "[redacted-url]") {
				t.Fatalf("diagnostic leaked verification material: %q", got)
			}
		})
	}
}
