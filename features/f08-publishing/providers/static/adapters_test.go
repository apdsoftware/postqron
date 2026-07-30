package staticproviders

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

type recordedCall struct {
	request socialconnections.PublishingRequest
}

type fixtureExecutor struct {
	mu        sync.Mutex
	calls     []recordedCall
	responses []socialconnections.PublishingResponse
	errs      []error
}

func (executor *fixtureExecutor) Execute(
	_ context.Context,
	request socialconnections.PublishingRequest,
) (socialconnections.PublishingResponse, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	request.Body = append([]byte(nil), request.Body...)
	request.Header = request.Header.Clone()
	executor.calls = append(executor.calls, recordedCall{request: request})
	index := len(executor.calls) - 1
	if index < len(executor.errs) && executor.errs[index] != nil {
		return socialconnections.PublishingResponse{}, executor.errs[index]
	}
	if index >= len(executor.responses) {
		return socialconnections.PublishingResponse{}, errors.New("fixture exhausted")
	}
	return executor.responses[index], nil
}

func readyGates() map[string]Gate {
	return map[string]Gate{
		ProviderX:                     {Enabled: true, ReviewApproved: true, AuditVerified: true, QuotaConfigured: true},
		ProviderLinkedIn:              {Enabled: true, ReviewApproved: true, AuditVerified: true, QuotaConfigured: true},
		ProviderPinterest:             {Enabled: true, ReviewApproved: true, AuditVerified: true, QuotaConfigured: true},
		ProviderGoogleBusinessProfile: {Enabled: true, ReviewApproved: true, AuditVerified: true, QuotaConfigured: true},
	}
}

func TestRegistrationFailsClosedForEveryMissingGate(t *testing.T) {
	fields := []func(*Gate){
		func(gate *Gate) { gate.Enabled = false },
		func(gate *Gate) { gate.ReviewApproved = false },
		func(gate *Gate) { gate.AuditVerified = false },
		func(gate *Gate) { gate.QuotaConfigured = false },
	}
	for _, mutate := range fields {
		registry := publishing.NewAdapterRegistry()
		gate := readyGates()[ProviderX]
		mutate(&gate)
		if err := Register(registry, Config{
			Executor: &fixtureExecutor{},
			Gates:    map[string]Gate{ProviderX: gate},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.ResolvePublisher(context.Background(), ProviderX); !errors.Is(err, publishing.ErrProviderUnavailable) {
			t.Fatalf("resolution error=%v gate=%+v", err, gate)
		}
	}
}

func TestRegistersExactStaticProviderSetAndCapabilities(t *testing.T) {
	registry := publishing.NewAdapterRegistry()
	if err := Register(registry, Config{
		Executor:        &fixtureExecutor{},
		LinkedInVersion: "202606",
		Gates:           readyGates(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{
		ProviderX, ProviderLinkedIn, ProviderPinterest,
		ProviderGoogleBusinessProfile,
	} {
		publisher, err := registry.ResolvePublisher(context.Background(), provider)
		if err != nil {
			t.Fatalf("%s: %v", provider, err)
		}
		capabilities := publisher.Capabilities()
		if !capabilities.Reconciliation || !capabilities.MultiStep ||
			capabilities.NativeIdempotency ||
			!capabilities.RemotePermalink {
			t.Fatalf("%s capabilities=%+v", provider, capabilities)
		}
	}
	for _, excluded := range []string{
		"facebook_pages", "instagram_professional", "tiktok", "youtube",
		"mastodon", "bluesky",
	} {
		if _, err := registry.ResolvePublisher(context.Background(), excluded); !errors.Is(err, publishing.ErrProviderUnavailable) {
			t.Fatalf("excluded provider %s error=%v", excluded, err)
		}
	}
}

func TestOfflineOfficialFixturesCoverEveryStaticProvider(t *testing.T) {
	source, err := os.ReadFile("testdata/official-publishing-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Provider       string          `json:"provider"`
		Payload        json.RawMessage `json:"payload"`
		ListResponse   json.RawMessage `json:"list_response"`
		CreateResponse json.RawMessage `json:"create_response"`
	}
	if err = json.Unmarshal(source, &fixtures); err != nil {
		t.Fatal(err)
	}
	expected := map[string]socialconnections.Provider{
		ProviderX:                     socialconnections.ProviderX,
		ProviderLinkedIn:              socialconnections.ProviderLinkedIn,
		ProviderPinterest:             socialconnections.ProviderPinterest,
		ProviderGoogleBusinessProfile: socialconnections.ProviderGoogleBusinessProfile,
	}
	if len(fixtures) != len(expected) {
		t.Fatalf("fixture count=%d", len(fixtures))
	}
	for _, fixture := range fixtures {
		provider, ok := expected[fixture.Provider]
		if !ok {
			t.Fatalf("unexpected provider %q", fixture.Provider)
		}
		adapter := &Adapter{
			provider: fixture.Provider, expected: provider,
			executor:        &fixtureExecutor{},
			linkedinVersion: "202606",
		}
		if _, err = adapter.decodePayload(fixture.Payload); err != nil {
			t.Fatalf("%s payload: %v", fixture.Provider, err)
		}
		if _, err = adapter.decodeList(fixture.ListResponse); err != nil {
			t.Fatalf("%s list fixture: %v", fixture.Provider, err)
		}
		if !json.Valid(fixture.CreateResponse) {
			t.Fatalf("%s create fixture is invalid", fixture.Provider)
		}
		delete(expected, fixture.Provider)
	}
	if len(expected) != 0 {
		t.Fatalf("missing fixtures=%v", expected)
	}
}

func TestXUsesExpectedProviderCheckpointRemoteIDAndPermalink(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"data":[{"id":"100","text":"hello","attachments":{"media_keys":[]}}]}`)},
		{StatusCode: 201, Body: []byte(`{"data":{"id":"200"}}`)},
	}}
	adapter := &Adapter{
		provider: ProviderX, expected: socialconnections.ProviderX,
		executor: executor,
	}
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload:        json.RawMessage(`{"format":"text","text":"hello","author":"postqron"}`),
		IdempotencyKey: "immutable-key",
	}
	progress, err := adapter.Publish(context.Background(), request)
	if err != nil || progress.Complete || len(progress.Checkpoint) == 0 {
		t.Fatalf("progress=%+v err=%v", progress, err)
	}
	request.Checkpoint = progress.Checkpoint
	result, err := adapter.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.RemoteID != "200" ||
		result.Permalink != "https://x.com/i/web/status/200" {
		t.Fatalf("result=%+v", result)
	}
	for _, call := range executor.calls {
		if call.request.ExpectedProvider != socialconnections.ProviderX ||
			call.request.WorkspaceID != "workspace" ||
			call.request.ConnectionID != "connection" {
			t.Fatalf("executor request=%+v", call.request)
		}
		if call.request.Header.Get("Authorization") != "" {
			t.Fatal("adapter passed an authorization header")
		}
	}
}

func TestLinkedInProfileAndPageUseVersionedOfficialHeaders(t *testing.T) {
	for _, author := range []string{"urn:li:person:123", "urn:li:organization:456"} {
		executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
			{StatusCode: 200, Body: []byte(`{"elements":[]}`)},
			{StatusCode: 201, Header: http.Header{"X-Restli-Id": {"urn:li:share:789"}}, Body: []byte(`{}`)},
		}}
		adapter := &Adapter{
			provider: ProviderLinkedIn, expected: socialconnections.ProviderLinkedIn,
			executor: executor, linkedinVersion: "202606",
		}
		request := publishing.PublishRequest{
			WorkspaceID: "workspace", ConnectionID: "connection",
			Payload: json.RawMessage(`{"format":"text","text":"hello","author":"` + author + `"}`),
		}
		first, err := adapter.Publish(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		request.Checkpoint = first.Checkpoint
		result, err := adapter.Publish(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.RemoteID != "urn:li:share:789" {
			t.Fatalf("result=%+v", result)
		}
		create := executor.calls[1].request
		if create.ExpectedProvider != socialconnections.ProviderLinkedIn ||
			create.Header.Get("Linkedin-Version") != "202606" ||
			create.Header.Get("X-Restli-Protocol-Version") != "2.0.0" {
			t.Fatalf("headers=%v provider=%s", create.Header, create.ExpectedProvider)
		}
	}
}

func TestPinterestAndGoogleBusinessProfileOfficialPayloads(t *testing.T) {
	tests := []struct {
		name      string
		adapter   *Adapter
		payload   string
		responses []socialconnections.PublishingResponse
		wantPath  string
		wantID    string
		wantLink  string
	}{
		{
			name:    "pinterest",
			adapter: &Adapter{provider: ProviderPinterest, expected: socialconnections.ProviderPinterest},
			payload: `{"format":"image","title":"title","description":"body","link":"https://example.com/article","board_id":"42","media":[{"source_url":"https://cdn.example.com/image.jpg"}]}`,
			responses: []socialconnections.PublishingResponse{
				{StatusCode: 200, Body: []byte(`{"items":[]}`)},
				{StatusCode: 201, Body: []byte(`{"id":"99"}`)},
			},
			wantPath: "/v5/pins", wantID: "99",
			wantLink: "https://www.pinterest.com/pin/99/",
		},
		{
			name:    "google business profile",
			adapter: &Adapter{provider: ProviderGoogleBusinessProfile, expected: socialconnections.ProviderGoogleBusinessProfile},
			payload: `{"format":"image","text":"news","location":"accounts/1/locations/2","language_code":"en","topic_type":"STANDARD","media":[{"source_url":"https://cdn.example.com/image.jpg"}]}`,
			responses: []socialconnections.PublishingResponse{
				{StatusCode: 200, Body: []byte(`{"localPosts":[]}`)},
				{StatusCode: 200, Body: []byte(`{"name":"accounts/1/locations/2/localPosts/3","searchUrl":"https://business.google.com/posts/3"}`)},
			},
			wantPath: "/v4/accounts/1/locations/2/localPosts",
			wantID:   "accounts/1/locations/2/localPosts/3",
			wantLink: "https://business.google.com/posts/3",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &fixtureExecutor{responses: test.responses}
			test.adapter.executor = executor
			request := publishing.PublishRequest{
				WorkspaceID: "workspace", ConnectionID: "connection",
				Payload: json.RawMessage(test.payload),
			}
			first, err := test.adapter.Publish(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			request.Checkpoint = first.Checkpoint
			result, err := test.adapter.Publish(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.RemoteID != test.wantID || result.Permalink != test.wantLink ||
				executor.calls[1].request.Path != test.wantPath {
				t.Fatalf("result=%+v path=%s", result, executor.calls[1].request.Path)
			}
		})
	}
}

func TestRetryAfterAndAmbiguityAreMappedWithoutDiagnostics(t *testing.T) {
	for _, fixture := range []struct {
		failure       *socialconnections.ExecutorFailure
		wantAmbiguous bool
	}{
		{
			failure: &socialconnections.ExecutorFailure{
				Kind: socialconnections.ExecutorFailureRateLimit,
				Code: "provider_rate_limited", RetryAfter: 37 * time.Second,
			},
		},
		{
			failure: &socialconnections.ExecutorFailure{
				Kind: socialconnections.ExecutorFailureAmbiguous,
				Code: "provider_outcome_ambiguous",
			},
			wantAmbiguous: true,
		},
	} {
		mapped := mapExecutorError(fixture.failure)
		var providerError *publishing.ProviderError
		if !errors.As(mapped, &providerError) || !providerError.Retryable ||
			providerError.Ambiguous != fixture.wantAmbiguous ||
			providerError.RetryAfter != fixture.failure.RetryAfter ||
			providerError.Detail != "" {
			t.Fatalf("mapped error=%+v", providerError)
		}
	}
}

func TestDeterministicReconciliationExcludesBaselineAndFailsClosedOnDuplicates(t *testing.T) {
	content := json.RawMessage(`{"format":"text","text":"same","author":"postqron"}`)
	state := mustJSON(checkpoint{Stage: "create", BaselineIDs: []string{"100"}})
	for _, test := range []struct {
		name string
		body string
		want publishing.ReconciliationState
	}{
		{
			name: "unique new match",
			body: `{"data":[{"id":"100","text":"same","attachments":{"media_keys":[]}},{"id":"200","text":"same","attachments":{"media_keys":[]}}]}`,
			want: publishing.ReconciliationFound,
		},
		{
			name: "duplicate new match",
			body: `{"data":[{"id":"201","text":"same","attachments":{"media_keys":[]}},{"id":"202","text":"same","attachments":{"media_keys":[]}}]}`,
			want: publishing.ReconciliationUnknown,
		},
		{
			name: "not found",
			body: `{"data":[{"id":"300","text":"different","attachments":{"media_keys":[]}}]}`,
			want: publishing.ReconciliationNotFound,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
				{StatusCode: 200, Body: []byte(test.body)},
			}}
			adapter := &Adapter{provider: ProviderX, expected: socialconnections.ProviderX, executor: executor}
			result, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
				WorkspaceID: "workspace", ConnectionID: "connection",
				Payload: content, Checkpoint: state,
			})
			if err != nil || result.State != test.want {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestAmbiguousCreateReconcilesWithoutDuplicatePost(t *testing.T) {
	executor := &fixtureExecutor{
		responses: []socialconnections.PublishingResponse{
			{StatusCode: 200, Body: []byte(`{"data":[{"id":"100","text":"older","attachments":{"media_keys":[]}}]}`)},
			{},
			{StatusCode: 200, Body: []byte(`{"data":[{"id":"100","text":"older","attachments":{"media_keys":[]}},{"id":"200","text":"publish once","attachments":{"media_keys":[]}}]}`)},
		},
		errs: []error{
			nil,
			&socialconnections.ExecutorFailure{
				Kind: socialconnections.ExecutorFailureAmbiguous,
				Code: "provider_outcome_ambiguous",
			},
			nil,
		},
	}
	adapter := &Adapter{
		provider: ProviderX, expected: socialconnections.ProviderX,
		executor: executor,
	}
	payload := json.RawMessage(
		`{"format":"text","text":"publish once","author":"postqron"}`,
	)
	progress, err := adapter.Publish(context.Background(), publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Publish(context.Background(), publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: payload, Checkpoint: progress.Checkpoint,
	})
	var providerError *publishing.ProviderError
	if !errors.As(err, &providerError) || !providerError.Ambiguous {
		t.Fatalf("create error=%v", err)
	}
	reconciled, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: payload, Checkpoint: progress.Checkpoint,
	})
	if err != nil || reconciled.State != publishing.ReconciliationFound ||
		reconciled.RemoteID != "200" {
		t.Fatalf("reconciled=%+v err=%v", reconciled, err)
	}
	posts := 0
	for _, call := range executor.calls {
		if call.request.Method == http.MethodPost {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("remote create calls=%d", posts)
	}
}

func TestMediaCheckpointTamperingFailsBeforeCreate(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"items":[]}`)},
	}}
	adapter := &Adapter{
		provider: ProviderPinterest, expected: socialconnections.ProviderPinterest,
		executor: executor,
	}
	payload := json.RawMessage(`{
		"format":"image",
		"board_id":"42",
		"media":[{"source_url":"https://cdn.example.com/one.jpg"}]
	}`)
	progress, err := adapter.Publish(context.Background(), publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	tampered := json.RawMessage(`{
		"format":"image",
		"board_id":"42",
		"media":[{"source_url":"https://cdn.example.com/two.jpg"}]
	}`)
	_, err = adapter.Publish(context.Background(), publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: tampered, Checkpoint: progress.Checkpoint,
	})
	var providerError *publishing.ProviderError
	if !errors.As(err, &providerError) ||
		providerError.Code != "checkpoint_invalid" {
		t.Fatalf("tamper error=%v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("executor calls=%d", len(executor.calls))
	}
}

func TestAdapterIsRaceSafe(t *testing.T) {
	executor := &fixtureExecutor{
		responses: make([]socialconnections.PublishingResponse, 64),
	}
	for index := range executor.responses {
		executor.responses[index] = socialconnections.PublishingResponse{
			StatusCode: 200, Body: []byte(`{"data":[]}`),
		}
	}
	adapter := &Adapter{provider: ProviderX, expected: socialconnections.ProviderX, executor: executor}
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := adapter.Publish(context.Background(), publishing.PublishRequest{
				WorkspaceID: "workspace", ConnectionID: "connection",
				Payload: json.RawMessage(`{"format":"text","text":"parallel","author":"postqron"}`),
			})
			if err != nil || result.Complete {
				t.Errorf("result=%+v err=%v", result, err)
			}
		}()
	}
	wait.Wait()
}
