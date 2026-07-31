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

func TestExpiredLeaseReplayDoesNotCreateDuplicate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	engine := newTestEngine(t, store, provider, &now, &fakeGate{current: true}, &fakeAuthorizer{allowed: true})
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-one", "meta", 3),
	})

	claimed, found, err := store.ClaimDue(
		ctx,
		now,
		now.Add(30*time.Second),
		"lease_crashed_worker",
	)
	if err != nil || !found {
		t.Fatalf("ClaimDue() destination=%#v found=%v error=%v", claimed, found, err)
	}
	first, err := provider.Publish(ctx, PublishRequest{
		WorkspaceID:    claimed.WorkspaceID,
		PostID:         claimed.PostID,
		ChannelID:      claimed.ChannelID,
		ConnectionID:   claimed.ConnectionID,
		Payload:        claimed.Payload,
		IdempotencyKey: claimed.IdempotencyKey,
	})
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(31 * time.Second)
	processed, err := engine.DispatchOne(ctx)
	if err != nil || !processed {
		t.Fatalf("DispatchOne() processed=%v error=%v", processed, err)
	}
	job, err := engine.GetJob(ctx, "workspace-1", result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != JobPublished ||
		len(job.Destinations) != 1 ||
		job.Destinations[0].RemoteID != first.RemoteID {
		t.Fatalf("job after replay = %#v", job)
	}
	if provider.Calls() != 2 || provider.Creates() != 1 {
		t.Fatalf(
			"provider calls=%d creates=%d, want two calls and one remote creation",
			provider.Calls(),
			provider.Creates(),
		)
	}
	if provider.ReconcileCalls() != 0 {
		t.Fatalf("native-idempotent reclaim reconciled %d times", provider.ReconcileCalls())
	}
}

func TestConcurrentWorkersClaimDestinationOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	engine := newTestEngine(t, store, provider, &now, &fakeGate{current: true}, &fakeAuthorizer{allowed: true})
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-one", "meta", 3),
	})

	var wait sync.WaitGroup
	var processed atomic.Int64
	errorsFound := make(chan error, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			ok, err := engine.DispatchOne(ctx)
			if err != nil {
				errorsFound <- err
				return
			}
			if ok {
				processed.Add(1)
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if processed.Load() != 1 || provider.Creates() != 1 {
		t.Fatalf("processed=%d creates=%d", processed.Load(), provider.Creates())
	}
	if !provider.ReceivedTIDAllocator() {
		t.Fatal("engine did not inject the durable TID allocator")
	}
	job, err := engine.GetJob(ctx, "workspace-1", result.JobID)
	if err != nil || job.Status != JobPublished {
		t.Fatalf("job=%#v error=%v", job, err)
	}
}

func TestPerDestinationBackoffDeadLetterAndManualRetry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	provider.Fail(
		"channel-bad",
		&ProviderError{
			Code:      "rate_limited",
			Detail:    "Bearer secret-token user@example.com",
			Retryable: true,
		},
		&ProviderError{
			Code:      "upstream_unavailable",
			Detail:    "token=do-not-store",
			Retryable: true,
		},
	)
	authorizer := &fakeAuthorizer{allowed: true}
	engine := newTestEngine(t, store, provider, &now, &fakeGate{current: true}, authorizer)
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-good", "meta", 2),
		testDestination("channel-bad", "meta", 2),
	})

	if processed, err := engine.DispatchOne(ctx); err != nil || !processed {
		t.Fatalf("publish good destination: processed=%v error=%v", processed, err)
	}
	if processed, err := engine.DispatchOne(ctx); err != nil || !processed {
		t.Fatalf("first bad attempt: processed=%v error=%v", processed, err)
	}
	job, err := engine.GetJob(ctx, "workspace-1", result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	good := destinationByChannel(t, job, "channel-good")
	bad := destinationByChannel(t, job, "channel-bad")
	if good.Status != DestinationPublished || good.RemoteID == "" {
		t.Fatalf("good destination = %#v", good)
	}
	if bad.Status != DestinationRetryWait ||
		bad.NextAttemptAt != now.Add(10*time.Second) ||
		bad.LastDiagnostic.Detail != "[redacted] [redacted]" {
		t.Fatalf("bad destination after retry = %#v", bad)
	}
	if processed, err := engine.DispatchOne(ctx); err != nil || processed {
		t.Fatalf("retry ran before backoff: processed=%v error=%v", processed, err)
	}

	now = now.Add(10 * time.Second)
	if processed, err := engine.DispatchOne(ctx); err != nil || !processed {
		t.Fatalf("second bad attempt: processed=%v error=%v", processed, err)
	}
	job, err = engine.GetJob(ctx, "workspace-1", result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	bad = destinationByChannel(t, job, "channel-bad")
	if job.Status != JobPartiallyFailed ||
		bad.Status != DestinationDeadLetter ||
		bad.LastDiagnostic.Retryable ||
		bad.LastDiagnostic.Detail != "token=[redacted]" {
		t.Fatalf("job after exhausted retry = %#v", job)
	}

	authorizer.allowed = false
	_, err = engine.RetryDestination(ctx, RetryDestinationCommand{
		WorkspaceID:   "workspace-1",
		ActorID:       "member-1",
		DestinationID: bad.ID,
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("unauthorized RetryDestination() error = %v", err)
	}

	authorizer.allowed = true
	retried, err := engine.RetryDestination(ctx, RetryDestinationCommand{
		WorkspaceID:   "workspace-1",
		ActorID:       "owner-1",
		DestinationID: bad.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != DestinationRetryWait ||
		retried.CycleAttemptCount != 0 ||
		retried.ManualRetryCount != 1 ||
		retried.IdempotencyKey != bad.IdempotencyKey {
		t.Fatalf("manually retried destination = %#v", retried)
	}
	if processed, err := engine.DispatchOne(ctx); err != nil || !processed {
		t.Fatalf("manual retry dispatch: processed=%v error=%v", processed, err)
	}
	job, err = engine.GetJob(ctx, "workspace-1", result.JobID)
	if err != nil || job.Status != JobPublished {
		t.Fatalf("job after manual retry = %#v error=%v", job, err)
	}
	bad = destinationByChannel(t, job, "channel-bad")
	if bad.RemoteID == "" || bad.ManualRetryCount != 1 {
		t.Fatalf("published retried destination = %#v", bad)
	}
}

func TestInvalidatedCommandIsCancelledBeforeProviderCall(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	provider := newFakeProvider()
	gate := &fakeGate{current: true}
	engine := newTestEngine(t, store, provider, &now, gate, &fakeAuthorizer{allowed: true})
	result := enqueueTestJob(t, ctx, engine, now, []DestinationInput{
		testDestination("channel-one", "meta", 3),
	})

	gate.current = false
	if processed, err := engine.DispatchOne(ctx); err != nil || !processed {
		t.Fatalf("DispatchOne() processed=%v error=%v", processed, err)
	}
	job, err := engine.GetJob(ctx, "workspace-1", result.JobID)
	if err != nil ||
		job.Status != JobCancelled ||
		job.Destinations[0].Status != DestinationCancelled {
		t.Fatalf("cancelled job=%#v error=%v", job, err)
	}
	if provider.Calls() != 0 {
		t.Fatalf("provider was called %d times", provider.Calls())
	}
}

func TestEnqueueIsIdempotentPerCommand(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	engine := newTestEngine(
		t,
		store,
		newFakeProvider(),
		&now,
		&fakeGate{current: true},
		&fakeAuthorizer{allowed: true},
	)
	request := testEnqueueRequest(now, []DestinationInput{
		testDestination("channel-one", "meta", 3),
	})
	first, err := engine.Enqueue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Enqueue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || second.Created || first.JobID != second.JobID {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	job, err := engine.GetJob(ctx, "workspace-1", first.JobID)
	if err != nil || len(job.Destinations) != 1 {
		t.Fatalf("job=%#v error=%v", job, err)
	}
}

func TestRetryPolicyUsesProviderHintWithinCap(t *testing.T) {
	policy := RetryPolicy{
		BaseDelay:   10 * time.Second,
		MaxDelay:    2 * time.Minute,
		Lease:       30 * time.Second,
		MaxAttempts: 4,
	}
	if got := policy.Delay(3, 90*time.Second); got != 90*time.Second {
		t.Fatalf("Delay()=%s, want provider hint", got)
	}
	if got := policy.Delay(9, 10*time.Minute); got != 2*time.Minute {
		t.Fatalf("Delay()=%s, want cap", got)
	}
}

func newTestEngine(
	t *testing.T,
	store Store,
	provider *fakeProvider,
	now *time.Time,
	gate *fakeGate,
	authorizer *fakeAuthorizer,
) *Engine {
	t.Helper()
	var sequence atomic.Uint64
	engine, err := NewEngine(
		store,
		gate,
		&fakeResolver{publisher: provider},
		authorizer,
		RetryPolicy{
			BaseDelay:   10 * time.Second,
			MaxDelay:    time.Minute,
			Lease:       30 * time.Second,
			MaxAttempts: 3,
		},
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

func enqueueTestJob(
	t *testing.T,
	ctx context.Context,
	engine *Engine,
	now time.Time,
	destinations []DestinationInput,
) EnqueueResult {
	t.Helper()
	result, err := engine.Enqueue(ctx, testEnqueueRequest(now, destinations))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func testEnqueueRequest(
	now time.Time,
	destinations []DestinationInput,
) EnqueueRequest {
	return EnqueueRequest{
		Command: PublicationCommand{
			ID:              "pubcmd-1",
			WorkspaceID:     "workspace-1",
			PostID:          "post-1",
			DraftID:         "draft-1",
			Generation:      1,
			ExecuteAtUTC:    now,
			State:           CommandPending,
			InvalidationKey: "post-1:1",
		},
		Destinations: destinations,
	}
}

func testDestination(channelID, provider string, attempts int) DestinationInput {
	return DestinationInput{
		ChannelID:         channelID,
		Provider:          provider,
		ConnectionID:      "connection-" + channelID,
		Mode:              PublishingModeAuto,
		DraftRevision:     1,
		CapabilityID:      provider + ".text",
		CapabilityVersion: "test-v1",
		Payload:           []byte(`{"text":"hello"}`),
		MaxAttempts:       attempts,
	}
}

func destinationByChannel(
	t *testing.T,
	job Job,
	channelID string,
) Destination {
	t.Helper()
	for _, destination := range job.Destinations {
		if destination.ChannelID == channelID {
			return destination
		}
	}
	t.Fatalf("destination %q not found in %#v", channelID, job)
	return Destination{}
}

type fakeGate struct {
	mutex   sync.Mutex
	current bool
	err     error
}

func (gate *fakeGate) IsCurrent(
	_ context.Context,
	_, _ string,
	_ int64,
) (bool, error) {
	gate.mutex.Lock()
	defer gate.mutex.Unlock()
	return gate.current, gate.err
}

type fakeAuthorizer struct {
	mutex   sync.Mutex
	allowed bool
	err     error
}

func (authorizer *fakeAuthorizer) CanRetryPublication(
	_ context.Context,
	_, _ string,
) (bool, error) {
	authorizer.mutex.Lock()
	defer authorizer.mutex.Unlock()
	return authorizer.allowed, authorizer.err
}

type fakeResolver struct {
	mutex        sync.Mutex
	publisher    Publisher
	err          error
	lastProvider string
}

func (resolver *fakeResolver) ResolvePublisher(
	_ context.Context,
	provider string,
) (Publisher, error) {
	resolver.mutex.Lock()
	defer resolver.mutex.Unlock()
	resolver.lastProvider = provider
	return resolver.publisher, resolver.err
}

func (resolver *fakeResolver) LastProvider() string {
	resolver.mutex.Lock()
	defer resolver.mutex.Unlock()
	return resolver.lastProvider
}

type fakeProvider struct {
	mutex           sync.Mutex
	calls           int
	creates         int
	remote          map[string]string
	failures        map[string][]error
	capabilities    AdapterCapabilities
	reconciliations map[string]ReconcileResult
	reconcileCalls  int
	tidAllocator    bool
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{
		remote:   make(map[string]string),
		failures: make(map[string][]error),
		capabilities: AdapterCapabilities{
			Version:           "test-v1",
			Mode:              PublishingModeAuto,
			NativeIdempotency: true,
		},
		reconciliations: make(map[string]ReconcileResult),
	}
}

func (provider *fakeProvider) Capabilities() AdapterCapabilities {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.capabilities
}

func (provider *fakeProvider) Fail(channelID string, failures ...error) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.failures[channelID] = append([]error(nil), failures...)
}

func (provider *fakeProvider) Publish(
	_ context.Context,
	request PublishRequest,
) (PublishResult, error) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.calls++
	provider.tidAllocator = provider.tidAllocator ||
		request.TIDAllocator != nil
	if remoteID, exists := provider.remote[request.IdempotencyKey]; exists {
		return PublishResult{Complete: true, RemoteID: remoteID}, nil
	}
	if failures := provider.failures[request.ChannelID]; len(failures) > 0 {
		failure := failures[0]
		provider.failures[request.ChannelID] = failures[1:]
		return PublishResult{}, failure
	}
	provider.creates++
	remoteID := fmt.Sprintf("remote-%d", provider.creates)
	provider.remote[request.IdempotencyKey] = remoteID
	return PublishResult{Complete: true, RemoteID: remoteID}, nil
}

func (provider *fakeProvider) Reconcile(
	_ context.Context,
	request ReconcileRequest,
) (ReconcileResult, error) {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	provider.reconcileCalls++
	if result, exists := provider.reconciliations[request.IdempotencyKey]; exists {
		return result, nil
	}
	if remoteID, exists := provider.remote[request.IdempotencyKey]; exists {
		return ReconcileResult{
			State:    ReconciliationFound,
			RemoteID: remoteID,
		}, nil
	}
	return ReconcileResult{State: ReconciliationNotFound}, nil
}

func (provider *fakeProvider) Calls() int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.calls
}

func (provider *fakeProvider) Creates() int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.creates
}

func (provider *fakeProvider) ReconcileCalls() int {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.reconcileCalls
}

func (provider *fakeProvider) ReceivedTIDAllocator() bool {
	provider.mutex.Lock()
	defer provider.mutex.Unlock()
	return provider.tidAllocator
}
