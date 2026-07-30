package publishing

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCrashReclaimUsesReconciliationWhenDeclared(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	provider.capabilities.NativeIdempotency = false
	provider.capabilities.Reconciliation = true
	engine := newTestEngine(
		t, store, provider, &now,
		&fakeGate{current: true},
		&fakeAuthorizer{allowed: true},
	)
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-reconcile", "meta", 3),
	})
	claimed, found, err := store.ClaimDue(
		ctx, now, now.Add(30*time.Second), "lease_crash_reconcile",
	)
	if err != nil || !found {
		t.Fatalf("claim found=%v error=%v", found, err)
	}
	remote, err := provider.Publish(ctx, publishRequestFromDestination(claimed))
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(31 * time.Second)
	processed, err := engine.DispatchOne(ctx)
	if err != nil || !processed {
		t.Fatalf("reclaim processed=%v error=%v", processed, err)
	}
	job, err := engine.GetJob(ctx, "workspace-1", result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Destinations[0].RemoteID != remote.RemoteID ||
		provider.Calls() != 1 ||
		provider.ReconcileCalls() != 1 {
		t.Fatalf(
			"destination=%#v publish_calls=%d reconcile_calls=%d",
			job.Destinations[0],
			provider.Calls(),
			provider.ReconcileCalls(),
		)
	}
}

func TestCrashReclaimFailClosedAdapterNeverBlindlyRepublishes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	provider.capabilities.NativeIdempotency = false
	provider.capabilities.Reconciliation = false
	provider.capabilities.FailClosedOnAmbiguous = true
	engine := newTestEngine(
		t, store, provider, &now,
		&fakeGate{current: true},
		&fakeAuthorizer{allowed: true},
	)
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-fail-closed", "meta", 3),
	})
	claimed, found, err := store.ClaimDue(
		ctx, now, now.Add(30*time.Second), "lease_crash_fail_closed",
	)
	if err != nil || !found {
		t.Fatalf("claim found=%v error=%v", found, err)
	}
	if _, err = provider.Publish(ctx, publishRequestFromDestination(claimed)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	processed, err := engine.DispatchOne(ctx)
	if err != nil || !processed {
		t.Fatalf("reclaim processed=%v error=%v", processed, err)
	}
	job, err := engine.GetJob(ctx, "workspace-1", result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	destination := job.Destinations[0]
	if destination.Status != DestinationDeadLetter ||
		!destination.LastDiagnostic.Ambiguous ||
		destination.LastDiagnostic.Retryable ||
		provider.Calls() != 1 || provider.ReconcileCalls() != 0 {
		t.Fatalf(
			"destination=%#v publish_calls=%d reconcile_calls=%d",
			destination,
			provider.Calls(),
			provider.ReconcileCalls(),
		)
	}
}

func TestNativeIdempotencyTimeoutRetriesWithoutReconciliation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	provider.Fail("channel-native", context.DeadlineExceeded)
	engine := newTestEngine(
		t, store, provider, &now,
		&fakeGate{current: true},
		&fakeAuthorizer{allowed: true},
	)
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-native", "meta", 2),
	})
	if processed, err := engine.DispatchOne(ctx); err != nil || !processed {
		t.Fatalf("first dispatch processed=%v error=%v", processed, err)
	}
	job, _ := engine.GetJob(ctx, "workspace-1", result.JobID)
	destination := job.Destinations[0]
	if destination.Status != DestinationRetryWait ||
		destination.NeedsReconciliation ||
		!destination.LastDiagnostic.Retryable ||
		destination.LastDiagnostic.Ambiguous {
		t.Fatalf("native timeout destination=%#v", destination)
	}
	now = destination.NextAttemptAt
	if processed, err := engine.DispatchOne(ctx); err != nil || !processed {
		t.Fatalf("retry dispatch processed=%v error=%v", processed, err)
	}
	if provider.ReconcileCalls() != 0 || provider.Creates() != 1 {
		t.Fatalf(
			"reconcile_calls=%d creates=%d",
			provider.ReconcileCalls(),
			provider.Creates(),
		)
	}
}

func TestReconciliationTimeoutReconcilesBeforeRetryPublish(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	provider.capabilities.NativeIdempotency = false
	provider.capabilities.Reconciliation = true
	provider.Fail("channel-reconcile", context.DeadlineExceeded)
	engine := newTestEngine(
		t, store, provider, &now,
		&fakeGate{current: true},
		&fakeAuthorizer{allowed: true},
	)
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-reconcile", "meta", 2),
	})
	if _, err := engine.DispatchOne(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ := engine.GetJob(ctx, "workspace-1", result.JobID)
	destination := job.Destinations[0]
	if !destination.NeedsReconciliation ||
		!destination.LastDiagnostic.Ambiguous {
		t.Fatalf("reconciliation timeout destination=%#v", destination)
	}
	now = destination.NextAttemptAt
	if _, err := engine.DispatchOne(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.ReconcileCalls() != 1 || provider.Creates() != 1 {
		t.Fatalf(
			"reconcile_calls=%d creates=%d",
			provider.ReconcileCalls(),
			provider.Creates(),
		)
	}
}

func TestHealthyMultiStepProgressDoesNotConsumeFailureBudget(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := &multiStepProvider{steps: 6}
	engine := newEngineForPublisher(t, store, provider, &now)
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-video", "video", 1),
	})

	for step := 0; step < provider.steps; step++ {
		if processed, err := engine.DispatchOne(ctx); err != nil || !processed {
			t.Fatalf("step %d processed=%v error=%v", step, processed, err)
		}
		job, err := engine.GetJob(ctx, "workspace-1", result.JobID)
		if err != nil {
			t.Fatal(err)
		}
		destination := job.Destinations[0]
		if step < provider.steps-1 {
			if destination.Status != DestinationRetryWait ||
				destination.CycleAttemptCount != 0 ||
				destination.Status == DestinationDeadLetter {
				t.Fatalf("step %d destination=%#v", step, destination)
			}
		} else if destination.Status != DestinationPublished {
			t.Fatalf("final destination=%#v", destination)
		}
	}
}

func TestManualRetryOfAmbiguousDeadLetterReconcilesFirst(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := &sideEffectTimeoutProvider{}
	engine := newEngineForPublisher(t, store, provider, &now)
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-ambiguous", "reconcile", 1),
	})
	if _, err := engine.DispatchOne(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ := engine.GetJob(ctx, "workspace-1", result.JobID)
	dead := job.Destinations[0]
	if dead.Status != DestinationDeadLetter || !dead.NeedsReconciliation {
		t.Fatalf("dead letter=%#v", dead)
	}
	retried, err := engine.RetryDestination(ctx, RetryDestinationCommand{
		WorkspaceID:   "workspace-1",
		ActorID:       "owner-1",
		DestinationID: dead.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !retried.NeedsReconciliation {
		t.Fatal("manual retry cleared ambiguous outcome")
	}
	if _, err := engine.DispatchOne(ctx); err != nil {
		t.Fatal(err)
	}
	if provider.PublishCalls() != 1 || provider.ReconcileCalls() != 1 {
		t.Fatalf(
			"publish_calls=%d reconcile_calls=%d",
			provider.PublishCalls(),
			provider.ReconcileCalls(),
		)
	}
}

func TestProviderIsNormalizedBeforeResolution(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	resolver := &fakeResolver{publisher: provider}
	engine, err := NewEngine(
		store,
		&fakeGate{current: true},
		resolver,
		&fakeAuthorizer{allowed: true},
		testPolicy(),
		WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	input := testDestination("channel-normalized", " meta ", 1)
	if _, err := engine.Enqueue(ctx, testEnqueueRequest(now, []DestinationInput{input})); err != nil {
		t.Fatal(err)
	}
	if resolver.LastProvider() != "meta" {
		t.Fatalf("resolved provider %q", resolver.LastProvider())
	}
}

func TestNotificationDeliveryIsIdempotentAndPersisted(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	registry := NewAdapterRegistry()
	notifier := newFakeNotifier()
	if err := registry.RegisterNotificationPublisher("instagram_personal", notifier); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(
		store,
		&fakeGate{current: true},
		registry,
		&fakeAuthorizer{allowed: true},
		testPolicy(),
		WithNotificationResolver(registry),
		WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	input := DestinationInput{
		ChannelID:         "logical-instagram",
		Provider:          "instagram_personal",
		Mode:              PublishingModeNotification,
		DraftRevision:     1,
		CapabilityID:      "instagram_personal.notification",
		CapabilityVersion: "notification-v1",
		Payload:           []byte(`{"text":"ready"}`),
		MaxAttempts:       2,
	}
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{input})
	claimed, found, err := store.ClaimDue(
		ctx, now, now.Add(30*time.Second), "lease_notification_crash",
	)
	if err != nil || !found {
		t.Fatalf("claim found=%v error=%v", found, err)
	}
	first, err := notifier.Notify(ctx, NotificationRequest{
		WorkspaceID:    claimed.WorkspaceID,
		PostID:         claimed.PostID,
		ChannelID:      claimed.ChannelID,
		Payload:        claimed.Payload,
		IdempotencyKey: claimed.IdempotencyKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	if _, err := engine.DispatchOne(ctx); err != nil {
		t.Fatal(err)
	}
	job, _ := engine.GetJob(ctx, "workspace-1", result.JobID)
	destination := job.Destinations[0]
	if destination.Status != DestinationNotified ||
		destination.NotificationID != first.DeliveryID ||
		destination.RemoteID != "" ||
		notifier.Deliveries() != 1 {
		t.Fatalf(
			"destination=%#v deliveries=%d",
			destination,
			notifier.Deliveries(),
		)
	}
}

func TestImmutableSnapshotRejectsChangedReplay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	engine := newEngineForPublisher(t, store, newFakeProvider(), &now)
	input := testDestination("channel-snapshot", "meta", 2)
	request := testEnqueueRequest(now, []DestinationInput{input})
	if _, err := engine.Enqueue(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.Destinations[0].Payload = []byte(`{"text":"mutated"}`)
	if _, err := engine.Enqueue(ctx, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed snapshot replay error=%v", err)
	}
}

func publishRequestFromDestination(destination Destination) PublishRequest {
	return PublishRequest{
		WorkspaceID:    destination.WorkspaceID,
		PostID:         destination.PostID,
		ChannelID:      destination.ChannelID,
		ConnectionID:   destination.ConnectionID,
		Payload:        destination.Payload,
		Checkpoint:     destination.Checkpoint,
		IdempotencyKey: destination.IdempotencyKey,
	}
}

func testPolicy() RetryPolicy {
	return RetryPolicy{
		BaseDelay:   10 * time.Second,
		MaxDelay:    time.Minute,
		Lease:       30 * time.Second,
		MaxAttempts: 3,
	}
}

func newEngineForPublisher(
	t *testing.T,
	store Store,
	publisher Publisher,
	now *time.Time,
) *Engine {
	t.Helper()
	var sequence atomic.Uint64
	engine, err := NewEngine(
		store,
		&fakeGate{current: true},
		&fakeResolver{publisher: publisher},
		&fakeAuthorizer{allowed: true},
		testPolicy(),
		WithClock(func() time.Time { return *now }),
		WithRandom(func(destination []byte) error {
			value := sequence.Add(1)
			for offset := 0; offset < len(destination); offset += 8 {
				var encoded [8]byte
				binary.LittleEndian.PutUint64(encoded[:], value)
				copy(destination[offset:], encoded[:])
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

type multiStepProvider struct {
	mu    sync.Mutex
	steps int
	calls int
}

func (provider *multiStepProvider) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Version:           "test-v1",
		Mode:              PublishingModeAuto,
		NativeIdempotency: true,
		MultiStep:         true,
	}
}

func (provider *multiStepProvider) Publish(
	_ context.Context,
	_ PublishRequest,
) (PublishResult, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	if provider.calls < provider.steps {
		return PublishResult{
			Checkpoint: []byte(fmt.Sprintf(`{"step":%d}`, provider.calls)),
		}, nil
	}
	return PublishResult{Complete: true, RemoteID: "video-remote"}, nil
}

func (*multiStepProvider) Reconcile(
	context.Context,
	ReconcileRequest,
) (ReconcileResult, error) {
	return ReconcileResult{State: ReconciliationNotFound}, nil
}

type sideEffectTimeoutProvider struct {
	mu             sync.Mutex
	publishCalls   int
	reconcileCalls int
	remoteID       string
}

func (*sideEffectTimeoutProvider) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Version:        "test-v1",
		Mode:           PublishingModeAuto,
		Reconciliation: true,
	}
}

func (provider *sideEffectTimeoutProvider) Publish(
	_ context.Context,
	_ PublishRequest,
) (PublishResult, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.publishCalls++
	provider.remoteID = "created-before-timeout"
	return PublishResult{}, context.DeadlineExceeded
}

func (provider *sideEffectTimeoutProvider) Reconcile(
	_ context.Context,
	_ ReconcileRequest,
) (ReconcileResult, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.reconcileCalls++
	return ReconcileResult{
		State:    ReconciliationFound,
		RemoteID: provider.remoteID,
	}, nil
}

func (provider *sideEffectTimeoutProvider) PublishCalls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.publishCalls
}

func (provider *sideEffectTimeoutProvider) ReconcileCalls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.reconcileCalls
}

type fakeNotifier struct {
	mu         sync.Mutex
	calls      int
	deliveries map[string]string
}

func newFakeNotifier() *fakeNotifier {
	return &fakeNotifier{deliveries: make(map[string]string)}
}

func (*fakeNotifier) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{
		Version:                 "notification-v1",
		Mode:                    PublishingModeNotification,
		NotificationIdempotency: true,
	}
}

func (notifier *fakeNotifier) Notify(
	_ context.Context,
	request NotificationRequest,
) (NotificationResult, error) {
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	notifier.calls++
	if deliveryID, exists := notifier.deliveries[request.IdempotencyKey]; exists {
		return NotificationResult{DeliveryID: deliveryID}, nil
	}
	deliveryID := fmt.Sprintf("delivery-%d", len(notifier.deliveries)+1)
	notifier.deliveries[request.IdempotencyKey] = deliveryID
	return NotificationResult{DeliveryID: deliveryID}, nil
}

func (notifier *fakeNotifier) Deliveries() int {
	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	return len(notifier.deliveries)
}
