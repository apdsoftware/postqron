package meta

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

type executorCall struct {
	request socialconnections.PublishingRequest
}

type fixtureExecutor struct {
	mu        sync.Mutex
	responses []socialconnections.PublishingResponse
	errors    []error
	calls     []executorCall
}

func (executor *fixtureExecutor) Execute(
	_ context.Context,
	request socialconnections.PublishingRequest,
) (socialconnections.PublishingResponse, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.calls = append(executor.calls, executorCall{request: request})
	index := len(executor.calls) - 1
	if index < len(executor.errors) && executor.errors[index] != nil {
		return socialconnections.PublishingResponse{}, executor.errors[index]
	}
	return executor.responses[index], nil
}

func TestFacebookPublisherUsesExpectedProviderAndReturnsRemoteIdentity(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"id":"page_123"}`)},
		{StatusCode: 200, Body: []byte(
			`{"id":"page_123","permalink_url":"https://facebook.example/p/page_123"}`,
		)},
	}}
	publisher, err := newPublisher(
		executor, socialconnections.ProviderFacebookPages, "v25.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := publisher.Publish(context.Background(), publishRequest(
		`{"format":"text","text":"hello"}`,
		nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || len(result.Checkpoint) == 0 {
		t.Fatalf("first result=%+v", result)
	}
	result, err = publisher.Publish(context.Background(), publishRequest(
		`{"format":"text","text":"hello"}`,
		result.Checkpoint,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.RemoteID != "page_123" ||
		result.Permalink != "https://facebook.example/p/page_123" {
		t.Fatalf("result=%+v", result)
	}
	for _, call := range executor.calls {
		if call.request.ExpectedProvider != socialconnections.ProviderFacebookPages {
			t.Fatalf("ExpectedProvider=%q", call.request.ExpectedProvider)
		}
		if call.request.WorkspaceID != "workspace-1" ||
			call.request.ConnectionID != "connection-1" {
			t.Fatalf("unsafe executor request=%+v", call.request)
		}
	}
}

func TestInstagramMultiStepCheckpointsBeforePublish(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"id":"container-1"}`)},
		{StatusCode: 200, Body: []byte(`{"id":"container-1","status_code":"FINISHED"}`)},
		{StatusCode: 200, Body: []byte(`{"id":"media-1"}`)},
		{StatusCode: 200, Body: []byte(
			`{"id":"media-1","permalink":"https://instagram.example/p/media-1"}`,
		)},
	}}
	publisher, err := newPublisher(
		executor, socialconnections.ProviderInstagramProfessional, "v25.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := publishRequest(
		`{"format":"image","text":"caption","media":[{"url":"https://media.example/image.jpg"}]}`,
		nil,
	)
	first, err := publisher.Publish(context.Background(), request)
	if err != nil || first.Complete || len(first.Checkpoint) == 0 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	request.Checkpoint = first.Checkpoint
	second, err := publisher.Publish(context.Background(), request)
	if err != nil || second.Complete {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	request.Checkpoint = second.Checkpoint
	third, err := publisher.Publish(context.Background(), request)
	if err != nil || third.Complete {
		t.Fatalf("third=%+v err=%v", third, err)
	}
	request.Checkpoint = third.Checkpoint
	final, err := publisher.Publish(context.Background(), request)
	if err != nil || !final.Complete || final.RemoteID != "media-1" {
		t.Fatalf("final=%+v err=%v", final, err)
	}
	if len(executor.calls) != 4 {
		t.Fatalf("calls=%d", len(executor.calls))
	}
}

func TestCarouselCheckpointsEveryChildBeforeParent(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"id":"child-1"}`)},
		{StatusCode: 200, Body: []byte(`{"id":"child-2"}`)},
		{StatusCode: 200, Body: []byte(`{"id":"carousel-1"}`)},
	}}
	publisher, err := newPublisher(
		executor, socialconnections.ProviderInstagramProfessional, "v25.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := publishRequest(
		`{"format":"carousel","text":"caption","media":[`+
			`{"url":"https://media.example/1.jpg"},`+
			`{"url":"https://media.example/2.jpg"}]}`,
		nil,
	)
	for index := range 3 {
		result, publishErr := publisher.Publish(context.Background(), request)
		if publishErr != nil || result.Complete || len(result.Checkpoint) == 0 {
			t.Fatalf("step %d result=%+v err=%v", index, result, publishErr)
		}
		request.Checkpoint = result.Checkpoint
	}
	if len(executor.calls) != 3 {
		t.Fatalf("calls=%d", len(executor.calls))
	}
	if executor.calls[2].request.Path != "/v25.0/me/media" {
		t.Fatalf("parent path=%q", executor.calls[2].request.Path)
	}
}

func TestAmbiguousExecutorFailureIsNeverBlindlyRetried(t *testing.T) {
	executor := &fixtureExecutor{
		responses: []socialconnections.PublishingResponse{{}},
		errors: []error{&socialconnections.ExecutorFailure{
			Kind: socialconnections.ExecutorFailureAmbiguous,
			Code: "provider_outcome_ambiguous",
		}},
	}
	publisher, err := newPublisher(
		executor, socialconnections.ProviderThreads, "v1.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	request := publishRequest(`{"format":"text","text":"hello Threads"}`, nil)
	_, err = publisher.Publish(context.Background(), request)
	var providerError *publishing.ProviderError
	if !errors.As(err, &providerError) || !providerError.Ambiguous {
		t.Fatalf("publish error=%#v", err)
	}
	reconciled, err := publisher.Reconcile(context.Background(), publishing.ReconcileRequest{
		WorkspaceID: request.WorkspaceID, PostID: request.PostID,
		ChannelID: request.ChannelID, ConnectionID: request.ConnectionID,
		Payload: request.Payload, IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil || reconciled.State != publishing.ReconciliationUnknown {
		t.Fatalf("reconcile=%+v err=%v", reconciled, err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("ambiguous outcome caused %d calls", len(executor.calls))
	}
}

func TestReconciliationUsesCheckpointRemoteIDDeterministically(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(
			`{"id":"thread-1","permalink":"https://threads.example/t/thread-1"}`,
		)},
	}}
	publisher, err := newPublisher(
		executor, socialconnections.ProviderThreads, "v1.0",
	)
	if err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(checkpoint{
		Step: "published", RemoteID: "thread-1",
		CreatedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := publisher.Reconcile(
		context.Background(),
		publishing.ReconcileRequest{
			WorkspaceID: "workspace-1", PostID: "post-1",
			ChannelID: "channel-1", ConnectionID: "connection-1",
			Payload:    json.RawMessage(`{"format":"text","text":"hello"}`),
			Checkpoint: state, IdempotencyKey: "destination-1:revision-1:publish",
		},
	)
	if err != nil || reconciled.State != publishing.ReconciliationFound ||
		reconciled.RemoteID != "thread-1" ||
		reconciled.Permalink != "https://threads.example/t/thread-1" {
		t.Fatalf("reconciled=%+v err=%v", reconciled, err)
	}
	if len(executor.calls) != 1 ||
		executor.calls[0].request.ExpectedProvider != socialconnections.ProviderThreads {
		t.Fatalf("calls=%+v", executor.calls)
	}
}

func TestRetryAfterIsMappedWithoutProviderDiagnostic(t *testing.T) {
	executor := &fixtureExecutor{
		responses: []socialconnections.PublishingResponse{{}},
		errors: []error{&socialconnections.ExecutorFailure{
			Kind: socialconnections.ExecutorFailureRateLimit,
			Code: "provider_rate_limited", RetryAfter: 37 * time.Second,
		}},
	}
	publisher, _ := newPublisher(
		executor, socialconnections.ProviderFacebookPages, "v25.0",
	)
	_, err := publisher.Publish(
		context.Background(),
		publishRequest(`{"format":"text","text":"hello"}`, nil),
	)
	var providerError *publishing.ProviderError
	if !errors.As(err, &providerError) || !providerError.Retryable ||
		providerError.RetryAfter != 37*time.Second ||
		providerError.Detail != "Meta request can be retried" {
		t.Fatalf("error=%#v", err)
	}
}

type memoryNotificationStore struct {
	mu      sync.Mutex
	records map[string]string
}

func (store *memoryNotificationStore) PutIfAbsent(
	_ context.Context,
	provider, _ string,
	idempotencyKey string,
	_ json.RawMessage,
) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.records == nil {
		store.records = make(map[string]string)
	}
	key := provider + ":" + idempotencyKey
	if id := store.records[key]; id != "" {
		return id, nil
	}
	id := StableNotificationID(provider, idempotencyKey)
	store.records[key] = id
	return id, nil
}

func TestNotificationPublishingIsIdempotentUnderRace(t *testing.T) {
	store := &memoryNotificationStore{}
	publisher, err := NewNotificationPublisher("facebook_groups", store)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	results := make(chan string, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, notifyErr := publisher.Notify(
				context.Background(),
				publishing.NotificationRequest{
					WorkspaceID: "workspace-1", PostID: "post-1",
					ChannelID: "group-1", Payload: json.RawMessage(
						`{"format":"text","text":"publish me"}`,
					),
					IdempotencyKey: "destination-1:revision-1:notify",
				},
			)
			if notifyErr != nil {
				t.Errorf("notify: %v", notifyErr)
				return
			}
			results <- result.DeliveryID
		}()
	}
	wait.Wait()
	close(results)
	var expected string
	for result := range results {
		if expected == "" {
			expected = result
		}
		if result != expected {
			t.Fatalf("delivery ids differ: %q != %q", result, expected)
		}
	}
	if len(store.records) != 1 {
		t.Fatalf("records=%d", len(store.records))
	}
}

func TestRegistrationIsFailClosedAndRegistersNotificationOnlyProviders(t *testing.T) {
	registry := publishing.NewAdapterRegistry()
	if err := Register(registry, RegistrationConfig{
		GraphVersion: "v25.0",
	}); !errors.Is(err, publishing.ErrProviderUnavailable) {
		t.Fatalf("partial registration error=%v", err)
	}

	registry = publishing.NewAdapterRegistry()
	if err := Register(registry, RegistrationConfig{
		NotificationStore: &memoryNotificationStore{},
	}); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"facebook_groups", "instagram_personal"} {
		if _, err := registry.ResolveNotificationPublisher(
			context.Background(),
			provider,
		); err != nil {
			t.Fatalf("resolve %s: %v", provider, err)
		}
	}
	if _, err := registry.ResolvePublisher(
		context.Background(),
		"facebook_pages",
	); !errors.Is(err, publishing.ErrProviderUnavailable) {
		t.Fatalf("auto publisher leaked through notification config: %v", err)
	}
}

func TestPayloadAndCheckpointFailClosed(t *testing.T) {
	publisher, _ := newPublisher(
		&fixtureExecutor{}, socialconnections.ProviderInstagramProfessional, "v25.0",
	)
	tests := []publishing.PublishRequest{
		publishRequest(`{"format":"text","text":"not supported"}`, nil),
		publishRequest(
			`{"format":"image","media":[{"url":"http://private.example/image.jpg"}]}`,
			nil,
		),
		publishRequest(
			`{"format":"image","media":[{"url":"https://media.example/image.jpg"}],"token":"secret"}`,
			nil,
		),
		publishRequest(
			`{"format":"image","media":[{"url":"https://media.example/image.jpg"}]}`,
			json.RawMessage(`{"step":"published","remote_id":"x"}`),
		),
	}
	for _, request := range tests {
		if _, err := publisher.Publish(context.Background(), request); err == nil {
			t.Fatalf("request unexpectedly accepted: %s", request.Payload)
		}
	}
}

func publishRequest(payload string, state json.RawMessage) publishing.PublishRequest {
	return publishing.PublishRequest{
		WorkspaceID: "workspace-1", PostID: "post-1", ChannelID: "channel-1",
		ConnectionID: "connection-1", Payload: json.RawMessage(payload),
		Checkpoint: state, IdempotencyKey: "destination-1:revision-1:publish",
	}
}

func TestNoAuthorizationHeaderCanBeInjected(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: http.StatusOK, Body: []byte(`{"id":"post-1"}`)},
		{StatusCode: http.StatusOK, Body: []byte(
			`{"id":"post-1","permalink_url":"https://facebook.example/p/post-1"}`,
		)},
	}}
	publisher, _ := newPublisher(
		executor, socialconnections.ProviderFacebookPages, "v25.0",
	)
	first, err := publisher.Publish(
		context.Background(),
		publishRequest(`{"format":"text","text":"safe"}`, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publisher.Publish(
		context.Background(),
		publishRequest(`{"format":"text","text":"safe"}`, first.Checkpoint),
	); err != nil {
		t.Fatal(err)
	}
	for _, call := range executor.calls {
		if call.request.Header.Get("Authorization") != "" {
			t.Fatal("adapter supplied an authorization header")
		}
	}
}

func TestMetaResponseClassifierMapsSanitizedSchedulingSemantics(t *testing.T) {
	classifier := ResponseClassifier{}
	classification, ok := classifier.ClassifyProviderResponse(
		socialconnections.ProviderResponseEvidence{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Retry-After": {"41"}},
			Body: []byte(
				`{"error":{"message":"redacted","type":"OAuthException","code":4,"fbtrace_id":"trace"}}`,
			),
			Method: http.MethodPost,
		},
	)
	if !ok ||
		classification.Kind != socialconnections.ExecutorFailureRateLimit ||
		classification.RetryAfter != 41*time.Second {
		t.Fatalf("classification=%+v ok=%v", classification, ok)
	}
}

func TestOfficialOfflineFixtureCoversMetaExecutionModes(t *testing.T) {
	source, err := os.ReadFile("testdata/official-meta-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Provider                string   `json:"provider"`
		Mode                    string   `json:"mode"`
		Formats                 []string `json:"formats"`
		Reconciliation          bool     `json:"reconciliation"`
		MultiStep               bool     `json:"multi_step"`
		NotificationIdempotency bool     `json:"notification_idempotency"`
	}
	if err = json.Unmarshal(source, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 5 {
		t.Fatalf("fixtures=%d", len(fixtures))
	}
	auto, notification := 0, 0
	for _, fixture := range fixtures {
		if fixture.Provider == "" || len(fixture.Formats) == 0 {
			t.Fatalf("invalid fixture=%+v", fixture)
		}
		switch fixture.Mode {
		case "auto":
			auto++
			if !fixture.Reconciliation || !fixture.MultiStep {
				t.Fatalf("unsafe auto fixture=%+v", fixture)
			}
		case "notification":
			notification++
			if !fixture.NotificationIdempotency {
				t.Fatalf("unsafe notification fixture=%+v", fixture)
			}
		default:
			t.Fatalf("invalid mode=%q", fixture.Mode)
		}
	}
	if auto != 3 || notification != 2 {
		t.Fatalf("auto=%d notification=%d", auto, notification)
	}
}
