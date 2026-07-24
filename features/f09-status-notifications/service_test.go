package statusnotifications

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type allowAuthorizer struct {
	view  bool
	retry bool
}

func (authorizer allowAuthorizer) CanViewStatus(
	context.Context,
	string,
	string,
) (bool, error) {
	return authorizer.view, nil
}

func (authorizer allowAuthorizer) CanRetryPublication(
	context.Context,
	string,
	string,
) (bool, error) {
	return authorizer.retry, nil
}

type recipientStub struct{}

func (recipientStub) ResolveNotificationRecipient(
	_ context.Context,
	notification Notification,
) (Recipient, error) {
	id := notification.AccountID
	if id == "" {
		id = "owner-" + notification.WorkspaceID
	}
	return Recipient{
		ID:    id,
		Email: id + "@example.test",
		Name:  "Recipient",
	}, nil
}

type emailGatewayStub struct {
	mutex    sync.Mutex
	commands []EmailCommand
	err      error
}

func (gateway *emailGatewayStub) EnqueueEmail(
	_ context.Context,
	command EmailCommand,
) error {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.err != nil {
		return gateway.err
	}
	gateway.commands = append(gateway.commands, command)
	return nil
}

func (gateway *emailGatewayStub) Commands() []EmailCommand {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	return slices.Clone(gateway.commands)
}

type retryGatewayStub struct {
	mutex        sync.Mutex
	destinations []string
	err          error
}

func (gateway *retryGatewayStub) RetryDestination(
	_ context.Context,
	_, _, destinationID string,
) error {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	if gateway.err != nil {
		return gateway.err
	}
	gateway.destinations = append(gateway.destinations, destinationID)
	return nil
}

func (gateway *retryGatewayStub) Destinations() []string {
	gateway.mutex.Lock()
	defer gateway.mutex.Unlock()
	return slices.Clone(gateway.destinations)
}

func newTestService(
	t *testing.T,
	now time.Time,
) (*Service, *MemoryRepository, *emailGatewayStub, *retryGatewayStub) {
	t.Helper()
	repository := NewMemoryRepository()
	email := &emailGatewayStub{}
	retry := &retryGatewayStub{}
	service, err := NewService(
		repository,
		allowAuthorizer{view: true, retry: true},
		recipientStub{},
		email,
		retry,
		WithClock(func() time.Time { return now }),
		WithRandom(func(destination []byte) error {
			for index := range destination {
				destination[index] = byte(index + 1)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, repository, email, retry
}

func TestProjectionShowsEveryStatePerDestination(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service, _, _, _ := newTestService(t, now)
	ctx := context.Background()

	draft, err := service.ConsumeLifecycle(ctx, LifecycleEvent{
		EventID:     "lifecycle-draft",
		WorkspaceID: "workspace-1",
		PostID:      "post-1",
		DraftID:     "draft-1",
		Revision:    1,
		Status:      StatusDraft,
		Destinations: []DestinationRef{
			{ID: "destination-1", ChannelID: "instagram-1"},
			{ID: "destination-2", ChannelID: "linkedin-1"},
		},
		OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("ConsumeLifecycle(draft) error = %v", err)
	}
	assertStatuses(t, draft.View, StatusDraft, DestinationDraft, DestinationDraft)

	scheduled, err := service.ConsumeLifecycle(ctx, LifecycleEvent{
		EventID:     "lifecycle-scheduled",
		WorkspaceID: "workspace-1",
		PostID:      "post-1",
		DraftID:     "draft-1",
		Revision:    2,
		Status:      StatusScheduled,
		Destinations: []DestinationRef{
			{ID: "destination-1", ChannelID: "instagram-1"},
			{ID: "destination-2", ChannelID: "linkedin-1"},
		},
		OccurredAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("ConsumeLifecycle(scheduled) error = %v", err)
	}
	assertStatuses(
		t,
		scheduled.View,
		StatusScheduled,
		DestinationScheduled,
		DestinationScheduled,
	)

	publishing, err := service.ConsumePublication(ctx, publicationEvent(
		"publishing-1",
		"destination-1",
		"instagram-1",
		"publishing",
		now.Add(2*time.Minute),
	))
	if err != nil {
		t.Fatalf("ConsumePublication(publishing) error = %v", err)
	}
	assertStatuses(
		t,
		publishing.View,
		StatusPublishing,
		DestinationPublishing,
		DestinationScheduled,
	)

	publishedEvent := publicationEvent(
		"published-1",
		"destination-1",
		"instagram-1",
		"published",
		now.Add(3*time.Minute),
	)
	publishedEvent.RemoteID = "remote-1"
	published, err := service.ConsumePublication(ctx, publishedEvent)
	if err != nil {
		t.Fatalf("ConsumePublication(published) error = %v", err)
	}
	assertStatuses(
		t,
		published.View,
		StatusScheduled,
		DestinationPublished,
		DestinationScheduled,
	)
	if published.View.Destinations[0].RemoteID != "remote-1" {
		t.Fatalf("RemoteID = %q, want remote-1", published.View.Destinations[0].RemoteID)
	}

	failedEvent := publicationEvent(
		"failed-2",
		"destination-2",
		"linkedin-1",
		"dead_letter",
		now.Add(4*time.Minute),
	)
	failedEvent.Diagnostic = SourceDiagnostic{
		Code:      "permission_revoked",
		Detail:    "token=secret user@example.test",
		Retryable: false,
	}
	failed, err := service.ConsumePublication(ctx, failedEvent)
	if err != nil {
		t.Fatalf("ConsumePublication(failed) error = %v", err)
	}
	assertStatuses(
		t,
		failed.View,
		StatusFailed,
		DestinationPublished,
		DestinationFailed,
	)
	diagnostic := failed.View.Destinations[1].Diagnostic
	if diagnostic.Message != "Il canale deve essere riconnesso prima di riprovare." {
		t.Fatalf("Diagnostic.Message = %q", diagnostic.Message)
	}
	if strings.Contains(diagnostic.Message, "secret") ||
		strings.Contains(diagnostic.Message, "@") {
		t.Fatalf("diagnostic leaked sensitive data: %q", diagnostic.Message)
	}

	cancelled, err := service.ConsumeLifecycle(ctx, LifecycleEvent{
		EventID:     "lifecycle-cancelled",
		WorkspaceID: "workspace-1",
		PostID:      "post-2",
		DraftID:     "draft-2",
		Revision:    1,
		Status:      StatusCancelled,
		Destinations: []DestinationRef{
			{ID: "destination-3", ChannelID: "facebook-1"},
		},
		OccurredAt: now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ConsumeLifecycle(cancelled) error = %v", err)
	}
	assertStatuses(
		t,
		cancelled.View,
		StatusCancelled,
		DestinationCancelled,
	)
}

func TestDuplicateDelayedAndOutOfOrderEventsAreHarmless(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service, repository, _, _ := newTestService(t, now)
	ctx := context.Background()
	mustSchedule(t, service, now)

	publishing := publicationEvent(
		"publishing",
		"destination-1",
		"instagram-1",
		"publishing",
		now.Add(2*time.Minute),
	)
	first, err := service.ConsumePublication(ctx, publishing)
	if err != nil || !first.FirstDelivery || !first.StateChanged {
		t.Fatalf("first publication result = %+v, err = %v", first, err)
	}
	duplicate, err := service.ConsumePublication(ctx, publishing)
	if err != nil {
		t.Fatalf("duplicate publication error = %v", err)
	}
	if duplicate.FirstDelivery || duplicate.StateChanged {
		t.Fatalf("duplicate result = %+v, want no-op", duplicate)
	}

	delayed := publicationEvent(
		"pending-delayed",
		"destination-1",
		"instagram-1",
		"pending",
		now.Add(time.Minute),
	)
	result, err := service.ConsumePublication(ctx, delayed)
	if err != nil {
		t.Fatalf("delayed publication error = %v", err)
	}
	if result.StateChanged ||
		result.View.Destinations[0].Status != DestinationPublishing {
		t.Fatalf("delayed event rolled state back: %+v", result)
	}

	conflicting := publishing
	conflicting.Status = "dead_letter"
	if _, err := service.ConsumePublication(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting event ID error = %v, want ErrConflict", err)
	}

	published := publicationEvent(
		"published",
		"destination-1",
		"instagram-1",
		"published",
		now.Add(3*time.Minute),
	)
	published.RemoteID = "remote-1"
	if _, err := service.ConsumePublication(ctx, published); err != nil {
		t.Fatalf("published event error = %v", err)
	}
	lateLifecycle, err := service.ConsumeLifecycle(ctx, LifecycleEvent{
		EventID:     "late-scheduled",
		WorkspaceID: "workspace-1",
		PostID:      "post-1",
		DraftID:     "draft-1",
		Revision:    2,
		Status:      StatusScheduled,
		Destinations: []DestinationRef{
			{ID: "destination-1", ChannelID: "instagram-1"},
			{ID: "destination-2", ChannelID: "linkedin-1"},
		},
		OccurredAt: now.Add(2500 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("late lifecycle error = %v", err)
	}
	if lateLifecycle.StateChanged ||
		lateLifecycle.View.Destinations[0].Status != DestinationPublished {
		t.Fatalf("late lifecycle rolled state back: %+v", lateLifecycle)
	}
	lateFailure := publicationEvent(
		"late-failure",
		"destination-1",
		"instagram-1",
		"dead_letter",
		now.Add(4*time.Minute),
	)
	result, err = service.ConsumePublication(ctx, lateFailure)
	if err != nil {
		t.Fatalf("late failure error = %v", err)
	}
	if result.StateChanged ||
		result.View.Destinations[0].Status != DestinationPublished {
		t.Fatalf("terminal published state changed: %+v", result)
	}
	if len(repository.Notifications()) != 0 {
		t.Fatalf("late failure queued a false notification")
	}

	withoutID := publicationEvent(
		"",
		"destination-2",
		"linkedin-1",
		"publishing",
		now.Add(5*time.Minute),
	)
	first, err = service.ConsumePublication(ctx, withoutID)
	if err != nil {
		t.Fatalf("derived event ID error = %v", err)
	}
	duplicate, err = service.ConsumePublication(ctx, withoutID)
	if err != nil || duplicate.FirstDelivery {
		t.Fatalf("derived duplicate result = %+v, err = %v", duplicate, err)
	}
}

func TestAllTransactionalNotificationsUseF14Idempotently(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service, repository, email, _ := newTestService(t, now)
	ctx := context.Background()

	events := []NotificationEvent{
		{
			EventID:    "account-created-1",
			Kind:       NotificationWelcome,
			AccountID:  "account-1",
			OccurredAt: now,
		},
		{
			EventID:     "plan-change-1",
			Kind:        NotificationPlanChanged,
			WorkspaceID: "workspace-1",
			Detail:      "Piano Pro attivo",
			OccurredAt:  now,
		},
		{
			EventID:    "session-revoked-1",
			Kind:       NotificationSecurityAlert,
			AccountID:  "account-1",
			Detail:     "Tutte le sessioni sono state revocate.",
			OccurredAt: now,
		},
	}
	for _, event := range events {
		result, err := service.ConsumeNotification(ctx, event)
		if err != nil || !result.Created {
			t.Fatalf("ConsumeNotification(%s) = %+v, %v", event.Kind, result, err)
		}
		duplicate, err := service.ConsumeNotification(ctx, event)
		if err != nil || duplicate.Created {
			t.Fatalf("duplicate notification = %+v, %v", duplicate, err)
		}
	}

	mustSchedule(t, service, now)
	failure := publicationEvent(
		"publication-failed-1",
		"destination-1",
		"instagram-1",
		"dead_letter",
		now.Add(time.Minute),
	)
	failure.Diagnostic = SourceDiagnostic{
		Code:   "rate_limited",
		Detail: "contact=user@example.test token=top-secret",
	}
	if _, err := service.ConsumePublication(ctx, failure); err != nil {
		t.Fatalf("ConsumePublication(failure) error = %v", err)
	}
	if _, err := service.ConsumePublication(ctx, failure); err != nil {
		t.Fatalf("duplicate failure error = %v", err)
	}
	if len(repository.Notifications()) != 4 {
		t.Fatalf("notifications = %d, want 4", len(repository.Notifications()))
	}

	for range 4 {
		processed, err := service.DispatchNotification(ctx)
		if err != nil || !processed {
			t.Fatalf("DispatchNotification() = %v, %v", processed, err)
		}
	}
	processed, err := service.DispatchNotification(ctx)
	if err != nil || processed {
		t.Fatalf("empty DispatchNotification() = %v, %v", processed, err)
	}

	commands := email.Commands()
	if len(commands) != 4 {
		t.Fatalf("email commands = %d, want 4", len(commands))
	}
	templates := make([]string, 0, len(commands))
	keys := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		if command.Channel != "transactional" ||
			command.TemplateVersion != "1.0.0" {
			t.Fatalf("invalid F14 command: %+v", command)
		}
		if _, exists := keys[command.IdempotencyKey]; exists {
			t.Fatalf("duplicate F14 idempotency key %q", command.IdempotencyKey)
		}
		keys[command.IdempotencyKey] = struct{}{}
		templates = append(templates, command.TemplateID)
	}
	slices.Sort(templates)
	want := []string{
		"plan_changed",
		"publication_failed",
		"security_alert",
		"welcome",
	}
	if !slices.Equal(templates, want) {
		t.Fatalf("templates = %v, want %v", templates, want)
	}
}

func TestManualRetryIsUniquePerFailureCycle(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service, repository, _, publishing := newTestService(t, now)
	ctx := context.Background()
	mustSchedule(t, service, now)

	failure := publicationEvent(
		"failure-cycle-1",
		"destination-1",
		"instagram-1",
		"dead_letter",
		now.Add(time.Minute),
	)
	if _, err := service.ConsumePublication(ctx, failure); err != nil {
		t.Fatalf("failure event error = %v", err)
	}
	first, err := service.RequestManualRetry(ctx, ManualRetryRequest{
		WorkspaceID:    "workspace-1",
		PostID:         "post-1",
		DestinationID:  "destination-1",
		ActorID:        "account-1",
		IdempotencyKey: "retry-request-1",
	})
	if err != nil || !first.Created {
		t.Fatalf("first retry = %+v, %v", first, err)
	}
	duplicate, err := service.RequestManualRetry(ctx, ManualRetryRequest{
		WorkspaceID:    "workspace-1",
		PostID:         "post-1",
		DestinationID:  "destination-1",
		ActorID:        "account-1",
		IdempotencyKey: "retry-request-1",
	})
	if err != nil || duplicate.Created || duplicate.ID != first.ID {
		t.Fatalf("same-key retry = %+v, %v", duplicate, err)
	}
	otherKey, err := service.RequestManualRetry(ctx, ManualRetryRequest{
		WorkspaceID:    "workspace-1",
		PostID:         "post-1",
		DestinationID:  "destination-1",
		ActorID:        "account-1",
		IdempotencyKey: "retry-request-2",
	})
	if err != nil || otherKey.Created || otherKey.ID != first.ID {
		t.Fatalf("other-key retry = %+v, %v", otherKey, err)
	}
	if len(repository.ManualRetries()) != 1 {
		t.Fatalf("manual retries = %d, want 1", len(repository.ManualRetries()))
	}

	processed, err := service.DispatchManualRetry(ctx)
	if err != nil || !processed {
		t.Fatalf("DispatchManualRetry() = %v, %v", processed, err)
	}
	processed, err = service.DispatchManualRetry(ctx)
	if err != nil || processed {
		t.Fatalf("empty DispatchManualRetry() = %v, %v", processed, err)
	}
	if got := publishing.Destinations(); !slices.Equal(got, []string{"destination-1"}) {
		t.Fatalf("publishing retries = %v", got)
	}
}

func TestDispatchFailuresAreRetriedAfterLeaseSafeBackoff(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service, repository, email, _ := newTestService(t, now)
	ctx := context.Background()
	if _, err := service.ConsumeNotification(ctx, NotificationEvent{
		EventID:    "welcome-1",
		Kind:       NotificationWelcome,
		AccountID:  "account-1",
		OccurredAt: now,
	}); err != nil {
		t.Fatalf("ConsumeNotification() error = %v", err)
	}
	email.err = errors.New("F14 unavailable")
	processed, err := service.DispatchNotification(ctx)
	if !processed || err == nil {
		t.Fatalf("DispatchNotification() = %v, %v", processed, err)
	}
	item := repository.Notifications()[0]
	if item.State != QueueRetry ||
		!item.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("retried item = %+v", item)
	}
}

func TestRedactRemovesSensitiveValuesAndLimitsLength(t *testing.T) {
	long := strings.Repeat("x", 400)
	safe := Redact(
		"Bearer abc.def user@example.test token=secret " +
			"https://provider.test/cb?api_key=value " + long,
	)
	for _, forbidden := range []string{
		"abc.def",
		"user@example.test",
		"token=secret",
		"api_key=value",
	} {
		if strings.Contains(safe, forbidden) {
			t.Fatalf("Redact() leaked %q in %q", forbidden, safe)
		}
	}
	if len([]rune(safe)) > maxDiagnosticLength {
		t.Fatalf("Redact() length = %d", len([]rune(safe)))
	}
}

func mustSchedule(t *testing.T, service *Service, now time.Time) {
	t.Helper()
	_, err := service.ConsumeLifecycle(context.Background(), LifecycleEvent{
		EventID:     "scheduled-post-1",
		WorkspaceID: "workspace-1",
		PostID:      "post-1",
		DraftID:     "draft-1",
		Revision:    1,
		Status:      StatusScheduled,
		Destinations: []DestinationRef{
			{ID: "destination-1", ChannelID: "instagram-1"},
			{ID: "destination-2", ChannelID: "linkedin-1"},
		},
		OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("schedule fixture error = %v", err)
	}
}

func publicationEvent(
	eventID, destinationID, channelID, status string,
	occurredAt time.Time,
) PublicationEvent {
	return PublicationEvent{
		EventID:       eventID,
		WorkspaceID:   "workspace-1",
		JobID:         "job-1",
		PostID:        "post-1",
		DestinationID: destinationID,
		ChannelID:     channelID,
		Status:        status,
		OccurredAt:    occurredAt,
	}
}

func assertStatuses(
	t *testing.T,
	view PostView,
	post PostStatus,
	destinations ...DestinationStatus,
) {
	t.Helper()
	if view.Status != post {
		t.Fatalf("post status = %q, want %q", view.Status, post)
	}
	if len(view.Destinations) != len(destinations) {
		t.Fatalf(
			"destinations = %d, want %d",
			len(view.Destinations),
			len(destinations),
		)
	}
	for index, want := range destinations {
		if view.Destinations[index].Status != want {
			t.Fatalf(
				"destination[%d] status = %q, want %q",
				index,
				view.Destinations[index].Status,
				want,
			)
		}
	}
}
