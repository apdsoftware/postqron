package pwa

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type gatewayReply struct {
	result SendResult
	err    error
}

type fakeGateway struct {
	mu      sync.Mutex
	replies []gatewayReply
	sent    []Subscription
	events  []PushEvent
}

func (gateway *fakeGateway) Send(
	_ context.Context,
	subscription Subscription,
	event PushEvent,
) (SendResult, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.sent = append(gateway.sent, subscription)
	gateway.events = append(gateway.events, event)
	if len(gateway.replies) == 0 {
		return SendResult{StatusCode: 201}, nil
	}
	reply := gateway.replies[0]
	gateway.replies = gateway.replies[1:]
	return reply.result, reply.err
}

func TestSubscribeRequiresOptInDataAndRevokeIsIdempotent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := newTestService(t, repository, &fakeGateway{}, func() time.Time {
		return now
	})

	subscription, created, err := service.Subscribe(context.Background(), validInput(
		"account-1",
		"https://push.example.test/device-1",
	))
	if err != nil || !created {
		t.Fatalf("subscribe: created=%v err=%v", created, err)
	}
	if subscription.Endpoint != "https://push.example.test/device-1" {
		t.Fatalf("unexpected endpoint: %q", subscription.Endpoint)
	}
	_, created, err = service.Subscribe(context.Background(), validInput(
		"account-1",
		"https://push.example.test/device-1",
	))
	if err != nil || created {
		t.Fatalf("refresh subscribe: created=%v err=%v", created, err)
	}

	revoked, err := service.Revoke(
		context.Background(),
		"account-1",
		"https://push.example.test/device-1",
	)
	if err != nil || !revoked {
		t.Fatalf("revoke: revoked=%v err=%v", revoked, err)
	}
	revoked, err = service.Revoke(
		context.Background(),
		"account-1",
		"https://push.example.test/device-1",
	)
	if err != nil || revoked {
		t.Fatalf("idempotent revoke: revoked=%v err=%v", revoked, err)
	}
	active, err := repository.ActiveSubscriptions(
		context.Background(),
		[]string{"account-1"},
		now,
	)
	if err != nil || len(active) != 0 {
		t.Fatalf("active after revoke: count=%d err=%v", len(active), err)
	}
}

func TestConsumeEventFansOutToDevicesAndDeduplicates(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	gateway := &fakeGateway{}
	service := newTestService(t, repository, gateway, func() time.Time {
		return now
	})
	for _, endpoint := range []string{
		"https://push.example.test/device-a",
		"https://push.example.test/device-b",
	} {
		if _, _, err := service.Subscribe(
			context.Background(),
			validInput("account-1", endpoint),
		); err != nil {
			t.Fatal(err)
		}
	}
	event := validEvent()
	created, err := service.ConsumeEvent(context.Background(), event)
	if err != nil || created != 2 {
		t.Fatalf("first consume: created=%d err=%v", created, err)
	}
	created, err = service.ConsumeEvent(context.Background(), event)
	if err != nil || created != 0 {
		t.Fatalf("duplicate consume: created=%d err=%v", created, err)
	}
	for range 2 {
		found, err := service.Dispatch(context.Background())
		if err != nil || !found {
			t.Fatalf("dispatch: found=%v err=%v", found, err)
		}
	}
	if len(gateway.sent) != 2 {
		t.Fatalf("sent %d pushes, want 2", len(gateway.sent))
	}
	for _, delivery := range repository.DeliverySnapshot() {
		if delivery.State != DeliveryDelivered {
			t.Fatalf("delivery %s is %s", delivery.ID, delivery.State)
		}
	}
}

func TestDispatchRetriesAndExpiresGoneDevice(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	gateway := &fakeGateway{replies: []gatewayReply{
		{result: SendResult{StatusCode: 503}, err: errors.New("temporary")},
		{result: SendResult{StatusCode: 201}},
		{result: SendResult{StatusCode: 410}, err: errors.New("gone")},
	}}
	service := newTestService(t, repository, gateway, func() time.Time {
		return now
	})
	if _, _, err := service.Subscribe(
		context.Background(),
		validInput("account-1", "https://push.example.test/device-a"),
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Subscribe(
		context.Background(),
		validInput("account-2", "https://push.example.test/device-b"),
	); err != nil {
		t.Fatal(err)
	}
	first := validEvent()
	if _, err := service.ConsumeEvent(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	retrying := repository.DeliverySnapshot()[0]
	if retrying.State != DeliveryRetry || retrying.AttemptCount != 1 ||
		!retrying.NextAttemptAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("unexpected retry: %+v", retrying)
	}
	now = now.Add(5 * time.Second)
	if _, err := service.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}

	second := validEvent()
	second.EventID = "status-event-2"
	second.RecipientAccountIDs = []string{"account-2"}
	if _, err := service.ConsumeEvent(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	subscriptions := repository.SubscriptionSnapshot()
	var gone Subscription
	for _, subscription := range subscriptions {
		if subscription.AccountID == "account-2" {
			gone = subscription
		}
	}
	if gone.RevokedAt == nil {
		t.Fatal("410 response did not expire the device token")
	}
}

func TestCollaborationApprovalEventsAreMappedWithoutUserContent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	gateway := &fakeGateway{}
	service := newTestService(t, repository, gateway, func() time.Time {
		return now
	})
	if _, _, err := service.Subscribe(
		context.Background(),
		validInput("reviewer-1", "https://push.example.test/reviewer"),
	); err != nil {
		t.Fatal(err)
	}
	created, err := service.ConsumeCollaborationEvent(
		context.Background(),
		CollaborationEvent{
			ID:                  "event_review_1",
			Type:                "collaboration.review.requested.v1",
			RecipientAccountIDs: []string{"reviewer-1"},
			WorkspaceID:         "workspace 1",
			DraftID:             "draft/1",
			OccurredAt:          now,
		},
	)
	if err != nil || created != 1 {
		t.Fatalf("consume approval: created=%d err=%v", created, err)
	}
	if _, err := service.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := gateway.events[0]
	if event.Kind != EventReviewRequested ||
		event.ActionURL != "/app/workspaces/workspace%201/drafts/draft%2F1/review" ||
		event.Body != "È stata richiesta la tua revisione editoriale." {
		t.Fatalf("unexpected approval payload: %+v", event)
	}
}

func TestPublicationFailureEventUsesOnlySafeDiagnostic(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	gateway := &fakeGateway{}
	service := newTestService(t, repository, gateway, func() time.Time {
		return now
	})
	if _, _, err := service.Subscribe(
		context.Background(),
		validInput("owner-1", "https://push.example.test/owner"),
	); err != nil {
		t.Fatal(err)
	}
	detail := "Il provider non è disponibile. " +
		strings.Repeat("x", maxBodyLength)
	created, err := service.ConsumeStatusEvent(
		context.Background(),
		StatusEvent{
			EventID:             "status_failure_1",
			Kind:                "publication_failed",
			RecipientAccountIDs: []string{"owner-1"},
			WorkspaceID:         "workspace-1",
			PostID:              "post-1",
			SafeDetail:          detail,
			OccurredAt:          now,
		},
	)
	if err != nil || created != 1 {
		t.Fatalf("consume failure: created=%d err=%v", created, err)
	}
	if _, err := service.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	event := gateway.events[0]
	if event.Kind != EventPublicationFailed ||
		event.Title != "Pubblicazione non riuscita" ||
		len([]rune(event.Body)) != maxBodyLength {
		t.Fatalf("unexpected publication failure payload: %+v", event)
	}
}

func newTestService(
	t *testing.T,
	repository Repository,
	gateway Gateway,
	clock func() time.Time,
) *Service {
	t.Helper()
	service, err := NewService(
		repository,
		gateway,
		WithClock(clock),
		WithRandom(func(destination []byte) error {
			for index := range destination {
				destination[index] = byte(index + 1)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func validInput(accountID, endpoint string) SubscriptionInput {
	p256dh := make([]byte, 65)
	auth := make([]byte, 16)
	for index := range p256dh {
		p256dh[index] = byte(index + 1)
	}
	for index := range auth {
		auth[index] = byte(index + 1)
	}
	return SubscriptionInput{
		AccountID: accountID,
		Endpoint:  endpoint,
		P256DH:    base64.RawURLEncoding.EncodeToString(p256dh),
		Auth:      base64.RawURLEncoding.EncodeToString(auth),
	}
}

func validEvent() PushEvent {
	return PushEvent{
		EventID:             "status-event-1",
		Kind:                EventPublicationFailed,
		RecipientAccountIDs: []string{"account-1"},
		WorkspaceID:         "workspace-1",
		ResourceID:          "post-1",
		Title:               "Pubblicazione non riuscita",
		Body:                "Il provider non è temporaneamente disponibile.",
		ActionURL:           "/app/workspaces/workspace-1/posts/post-1",
		OccurredAt:          time.Date(2026, 7, 24, 11, 59, 0, 0, time.UTC),
	}
}
