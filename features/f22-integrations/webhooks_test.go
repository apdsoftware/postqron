package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

var testSigningSecret = []byte("0123456789abcdef0123456789abcdef")

type fakeSubscriptionRepository struct {
	subscriptions []WebhookSubscription
}

func (fake fakeSubscriptionRepository) ListActive(
	_ context.Context,
	_ string,
	_ string,
) ([]WebhookSubscription, error) {
	return fake.subscriptions, nil
}

type fakeWebhookQueue struct {
	claimed        []ClaimedWebhookDelivery
	deadCode       string
	deadStatus     int
	delivered      bool
	deliveredCode  int
	enqueued       WebhookEvent
	envelope       []byte
	rescheduleCode string
	rescheduleAt   time.Time
	rescheduleHTTP int
	subscriptions  []WebhookSubscription
}

func (fake *fakeWebhookQueue) Enqueue(
	_ context.Context,
	event WebhookEvent,
	envelope []byte,
	subscriptions []WebhookSubscription,
) error {
	fake.enqueued = event
	fake.envelope = append([]byte(nil), envelope...)
	fake.subscriptions = append([]WebhookSubscription(nil), subscriptions...)
	return nil
}

func (fake *fakeWebhookQueue) ClaimDue(
	_ context.Context,
	_ time.Time,
	_ int,
) ([]ClaimedWebhookDelivery, error) {
	return fake.claimed, nil
}

func (fake *fakeWebhookQueue) MarkDelivered(
	_ context.Context,
	_ string,
	_ time.Time,
	statusCode int,
) error {
	fake.delivered = true
	fake.deliveredCode = statusCode
	return nil
}

func (fake *fakeWebhookQueue) Reschedule(
	_ context.Context,
	_ string,
	nextAttemptAt time.Time,
	statusCode int,
	errorCode string,
) error {
	fake.rescheduleAt = nextAttemptAt
	fake.rescheduleHTTP = statusCode
	fake.rescheduleCode = errorCode
	return nil
}

func (fake *fakeWebhookQueue) MoveToDeadLetter(
	_ context.Context,
	_ string,
	_ time.Time,
	statusCode int,
	errorCode string,
) error {
	fake.deadStatus = statusCode
	fake.deadCode = errorCode
	return nil
}

type fakeWebhookSender struct {
	deadline time.Time
	endpoint string
	envelope []byte
	err      error
	headers  http.Header
	result   DeliveryResult
}

func (fake *fakeWebhookSender) Send(
	ctx context.Context,
	endpoint string,
	envelope []byte,
	headers http.Header,
) (DeliveryResult, error) {
	fake.endpoint = endpoint
	fake.envelope = append([]byte(nil), envelope...)
	fake.headers = headers.Clone()
	fake.deadline, _ = ctx.Deadline()
	return fake.result, fake.err
}

type fakeWebhookObserver struct {
	observation WebhookObservation
}

func (fake *fakeWebhookObserver) ObserveWebhook(
	_ context.Context,
	observation WebhookObservation,
) {
	fake.observation = observation
}

func validWebhookEvent() WebhookEvent {
	return WebhookEvent{
		ID:          "evt_01",
		Type:        "post.published",
		Version:     WebhookEventVersion,
		WorkspaceID: testWorkspace,
		OccurredAt:  testNow,
		Data:        json.RawMessage(`{"post_id":"post_01","status":"published"}`),
	}
}

func validClaim(attempt int) ClaimedWebhookDelivery {
	event := validWebhookEvent()
	envelope, _ := json.Marshal(event)
	return ClaimedWebhookDelivery{
		Delivery: WebhookDelivery{
			ID:             "del_01",
			EventID:        event.ID,
			SubscriptionID: "sub_01",
			WorkspaceID:    testWorkspace,
			Attempt:        attempt,
		},
		Subscription: WebhookSubscription{
			ID:            "sub_01",
			WorkspaceID:   testWorkspace,
			Endpoint:      "https://hooks.example.com/postqron",
			EventTypes:    map[string]struct{}{"post.published": {}},
			SigningSecret: append([]byte(nil), testSigningSecret...),
			Active:        true,
		},
		Event:    event,
		Envelope: envelope,
	}
}

func TestWebhookPublisherVersionsFiltersAndAtomicallyEnqueues(t *testing.T) {
	metrics := &WebhookMetrics{}
	queue := &fakeWebhookQueue{}
	repository := fakeSubscriptionRepository{subscriptions: []WebhookSubscription{
		{
			ID:          "sub_matching",
			WorkspaceID: testWorkspace,
			Endpoint:    "https://hooks.example.com/postqron",
			EventTypes:  map[string]struct{}{"post.published": {}},
			Active:      true,
		},
		{
			ID:          "sub_other_event",
			WorkspaceID: testWorkspace,
			Endpoint:    "https://hooks.example.com/postqron",
			EventTypes:  map[string]struct{}{"post.failed": {}},
			Active:      true,
		},
		{
			ID:          "sub_private_destination",
			WorkspaceID: testWorkspace,
			Endpoint:    "https://127.0.0.1/postqron",
			EventTypes:  map[string]struct{}{"post.published": {}},
			Active:      true,
		},
	}}
	publisher, err := NewWebhookPublisher(
		repository,
		queue,
		metrics,
		func() time.Time { return testNow },
	)
	if err != nil {
		t.Fatal(err)
	}
	event := validWebhookEvent()
	event.Version = ""
	event.OccurredAt = time.Time{}
	if err := publisher.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if queue.enqueued.Version != WebhookEventVersion || queue.enqueued.OccurredAt != testNow {
		t.Fatalf("enqueued event = %#v", queue.enqueued)
	}
	if len(queue.subscriptions) != 1 || queue.subscriptions[0].ID != "sub_matching" {
		t.Fatalf("enqueued subscriptions = %#v", queue.subscriptions)
	}
	if snapshot := metrics.Snapshot(); snapshot.Enqueued != 1 {
		t.Fatalf("enqueued metric = %d", snapshot.Enqueued)
	}
	var encoded WebhookEvent
	if err := json.Unmarshal(queue.envelope, &encoded); err != nil {
		t.Fatalf("envelope is invalid: %v", err)
	}
	if encoded.ID != event.ID || encoded.Version != WebhookEventVersion {
		t.Fatalf("encoded event = %#v", encoded)
	}
}

func TestWebhookEventRejectsCredentialFieldsRecursively(t *testing.T) {
	for _, payload := range []string{
		`{"access_token":"provider-secret"}`,
		`{"nested":{"refresh-token":"provider-secret"}}`,
		`{"items":[{"authorization":"Bearer provider-secret"}]}`,
		`{"safe":true} {"token":"second-value"}`,
	} {
		event := validWebhookEvent()
		event.Data = json.RawMessage(payload)
		if err := ValidateWebhookEvent(event); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("payload %s error = %v, want ErrInvalidArgument", payload, err)
		}
	}
	event := validWebhookEvent()
	event.Data = json.RawMessage(`{"post_id":"post_01","token_count":42}`)
	if err := ValidateWebhookEvent(event); err != nil {
		t.Fatalf("safe event rejected: %v", err)
	}
}

func TestWebhookSignatureVerification(t *testing.T) {
	envelope := []byte(`{"id":"evt_01"}`)
	signature, err := SignWebhook(testSigningSecret, testNow, envelope)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(signature, "t=1784903400,v1=") {
		t.Fatalf("signature = %q", signature)
	}
	if err := VerifyWebhook(
		testSigningSecret,
		signature,
		envelope,
		testNow.Add(time.Minute),
		5*time.Minute,
	); err != nil {
		t.Fatalf("VerifyWebhook() error = %v", err)
	}
	if err := VerifyWebhook(
		testSigningSecret,
		signature,
		append(envelope, ' '),
		testNow.Add(time.Minute),
		5*time.Minute,
	); err == nil {
		t.Fatal("tampered envelope verified")
	}
	if err := VerifyWebhook(
		testSigningSecret,
		signature,
		envelope,
		testNow.Add(6*time.Minute),
		5*time.Minute,
	); err == nil {
		t.Fatal("stale signature verified")
	}
}

func TestWebhookEndpointRejectsInternalAndUnsafeDestinations(t *testing.T) {
	if err := ValidateWebhookEndpoint("https://hooks.example.com/postqron?tenant=one"); err != nil {
		t.Fatalf("public HTTPS endpoint rejected: %v", err)
	}
	for _, endpoint := range []string{
		"http://hooks.example.com/postqron",
		"https://user:password@hooks.example.com/postqron",
		"https://localhost/postqron",
		"https://service.local/postqron",
		"https://127.0.0.1/postqron",
		"https://10.0.0.1/postqron",
		"https://100.64.0.1/postqron",
		"https://192.0.2.1/postqron",
		"https://[::1]/postqron",
		"https://[::ffff:127.0.0.1]/postqron",
		"https://hooks.example.com/postqron#fragment",
	} {
		if err := ValidateWebhookEndpoint(endpoint); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("endpoint %q error = %v, want ErrInvalidArgument", endpoint, err)
		}
	}
}

func TestWebhookProcessorSignsTimesOutAndMarksSuccess(t *testing.T) {
	claim := validClaim(1)
	queue := &fakeWebhookQueue{claimed: []ClaimedWebhookDelivery{claim}}
	sender := &fakeWebhookSender{result: DeliveryResult{StatusCode: http.StatusNoContent}}
	observer := &fakeWebhookObserver{}
	metrics := &WebhookMetrics{}
	processor, err := NewWebhookProcessor(WebhookProcessorConfig{
		Clock:       func() time.Time { return testNow },
		Metrics:     metrics,
		Observer:    observer,
		Queue:       queue,
		SendTimeout: 5 * time.Second,
		Sender:      sender,
	})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("ProcessDue() error = %v", err)
	}
	if processed != 1 || !queue.delivered || queue.deliveredCode != http.StatusNoContent {
		t.Fatalf("processed=%d delivered=%v code=%d", processed, queue.delivered, queue.deliveredCode)
	}
	if sender.endpoint != claim.Subscription.Endpoint ||
		!bytes.Equal(sender.envelope, claim.Envelope) {
		t.Fatal("sender received a changed destination or envelope")
	}
	if sender.deadline.IsZero() || sender.deadline.Sub(time.Now()) > 6*time.Second {
		t.Fatalf("sender deadline = %v", sender.deadline)
	}
	if got := sender.headers.Get("Postqron-Event-Version"); got != WebhookEventVersion {
		t.Fatalf("event version header = %q", got)
	}
	if err := VerifyWebhook(
		testSigningSecret,
		sender.headers.Get("Postqron-Signature"),
		sender.envelope,
		testNow,
		5*time.Minute,
	); err != nil {
		t.Fatalf("sent signature is invalid: %v", err)
	}
	if snapshot := metrics.Snapshot(); snapshot.Delivered != 1 {
		t.Fatalf("delivered metric = %d", snapshot.Delivered)
	}
	if observer.observation.Outcome != "delivered" ||
		observer.observation.EventType != "post.published" {
		t.Fatalf("observation = %#v", observer.observation)
	}
}

func TestWebhookProcessorRetriesTransientFailures(t *testing.T) {
	queue := &fakeWebhookQueue{claimed: []ClaimedWebhookDelivery{validClaim(1)}}
	sender := &fakeWebhookSender{result: DeliveryResult{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: 2 * time.Minute,
	}}
	metrics := &WebhookMetrics{}
	processor, err := NewWebhookProcessor(WebhookProcessorConfig{
		Clock:   func() time.Time { return testNow },
		Metrics: metrics,
		Queue:   queue,
		Sender:  sender,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessDue(context.Background(), 1); err != nil {
		t.Fatalf("ProcessDue() error = %v", err)
	}
	if queue.rescheduleCode != "http_429" ||
		queue.rescheduleHTTP != http.StatusTooManyRequests ||
		queue.rescheduleAt != testNow.Add(2*time.Minute) {
		t.Fatalf(
			"reschedule code=%q status=%d at=%v",
			queue.rescheduleCode,
			queue.rescheduleHTTP,
			queue.rescheduleAt,
		)
	}
	if snapshot := metrics.Snapshot(); snapshot.Retried != 1 || snapshot.DeadLettered != 0 {
		t.Fatalf("metrics = %#v", snapshot)
	}
}

func TestWebhookProcessorDeadLettersPermanentAndExhaustedFailures(t *testing.T) {
	tests := []struct {
		name     string
		attempt  int
		status   int
		wantCode string
	}{
		{"permanent consumer error", 1, http.StatusBadRequest, "http_4xx"},
		{"retry budget exhausted", 8, http.StatusServiceUnavailable, "http_5xx"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queue := &fakeWebhookQueue{claimed: []ClaimedWebhookDelivery{validClaim(test.attempt)}}
			sender := &fakeWebhookSender{result: DeliveryResult{StatusCode: test.status}}
			metrics := &WebhookMetrics{}
			processor, err := NewWebhookProcessor(WebhookProcessorConfig{
				Clock:   func() time.Time { return testNow },
				Metrics: metrics,
				Queue:   queue,
				Sender:  sender,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := processor.ProcessDue(context.Background(), 1); err != nil {
				t.Fatalf("ProcessDue() error = %v", err)
			}
			if queue.deadCode != test.wantCode || queue.deadStatus != test.status {
				t.Fatalf("dead letter code=%q status=%d", queue.deadCode, queue.deadStatus)
			}
			if snapshot := metrics.Snapshot(); snapshot.DeadLettered != 1 {
				t.Fatalf("dead letter metric = %d", snapshot.DeadLettered)
			}
		})
	}
}

func TestWebhookMetricsHaveNoSensitiveOrHighCardinalityLabels(t *testing.T) {
	metrics := &WebhookMetrics{}
	metrics.RecordEnqueued(2)
	metrics.RecordDelivered(120 * time.Millisecond)
	metrics.RecordRetried()
	metrics.RecordDeadLetter()
	var output strings.Builder
	if err := metrics.WritePrometheus(&output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, forbidden := range []string{
		testWorkspace,
		"del_01",
		"hooks.example.com",
		string(testSigningSecret),
		"post.published",
		"{",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("metrics expose %q:\n%s", forbidden, text)
		}
	}
	if !strings.Contains(text, "# TYPE postqron_webhook_last_delivery_duration_seconds gauge") {
		t.Fatalf("duration metric is not a gauge:\n%s", text)
	}
}
