package email

import (
	"context"
	"errors"
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
	return ProviderReceipt{MessageID: "mailrox_1"}, nil
}

func testService(
	t *testing.T,
	store *MemoryStore,
	sender Sender,
) (*Service, time.Time) {
	t.Helper()
	service, err := NewService(
		store,
		testRenderer(t),
		sender,
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

func TestServiceEnqueueIsIdempotentAndDispatchesOnce(t *testing.T) {
	store := NewMemoryStore()
	sender := &scriptedSender{}
	service, _ := testService(t, store, sender)
	message := testMessage(ChannelTransactional, TemplateWelcome)
	message.ID = ""
	message.CreatedAt = time.Time{}

	first, err := service.Enqueue(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Enqueue(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.ID != second.ID {
		t.Fatalf("idempotency results = %#v, %#v", first, second)
	}
	processed, err := service.DispatchOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("DispatchOne() = %v, %v", processed, err)
	}
	processed, err = service.DispatchOne(context.Background())
	if err != nil || processed {
		t.Fatalf("second DispatchOne() = %v, %v", processed, err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("send count = %d, want 1", len(sender.messages))
	}
	delivery, _ := store.Delivery(first.ID)
	if delivery.State != StateAccepted || delivery.ProviderMessageID != "mailrox_1" {
		t.Fatalf("delivery = %#v", delivery)
	}
}

func TestServiceRetriesTransientFailureThenAccepts(t *testing.T) {
	store := NewMemoryStore()
	sender := &scriptedSender{errors: []error{
		&MailroxError{
			Code:       "rate_limited",
			Retryable:  true,
			RetryAfter: 10 * time.Minute,
			Detail:     "wait for persona@example.test Bearer secret-value",
		},
		nil,
	}}
	service, now := testService(t, store, sender)
	message := testMessage(ChannelTransactional, TemplateSecurityAlert)

	result, err := service.Enqueue(context.Background(), message)
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
		&MailroxError{Code: "invalid_recipient", Detail: "address rejected"},
	}}
	service, _ := testService(t, store, sender)
	message := testMessage(ChannelTransactional, TemplatePlanChanged)

	result, err := service.Enqueue(context.Background(), message)
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

func TestSuppressionSeparatesMarketingUntilHardBounce(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Suppress(context.Background(), Suppression{
		RecipientID: "account_1",
		Scope:       SuppressMarketing,
		Reason:      "unsubscribe",
		OccurredAt:  time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	service, _ := testService(t, store, &scriptedSender{})

	marketing := testMessage(ChannelMarketing, TemplateMarketingUpdate)
	marketing.Data.UnsubscribeURL = "https://app.example.test/unsubscribe?token=opaque"
	if _, err := service.Enqueue(context.Background(), marketing); !errors.Is(err, ErrSuppressed) {
		t.Fatalf("marketing Enqueue() error = %v, want ErrSuppressed", err)
	}
	transactional := testMessage(ChannelTransactional, TemplateWelcome)
	if _, err := service.Enqueue(context.Background(), transactional); err != nil {
		t.Fatalf("transactional Enqueue() error = %v", err)
	}
}
