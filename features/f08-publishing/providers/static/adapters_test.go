package staticproviders

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

type recordedCall struct {
	request   socialconnections.PublishingRequest
	mediaBody []byte
}

type fixtureTargetResolver struct {
	provider socialconnections.Provider
	remoteID string
}

func (resolver fixtureTargetResolver) ResolveTarget(
	context.Context, string, string,
) (ConnectionTarget, error) {
	return ConnectionTarget{
		Provider: resolver.provider, RemoteID: resolver.remoteID,
	}, nil
}

type fixtureMediaResolver struct {
	assets map[string][]byte
	types  map[string]string
}

func (resolver fixtureMediaResolver) OpenMedia(
	_ context.Context, _, ref string,
) (ResolvedMedia, error) {
	body, ok := resolver.assets[ref]
	if !ok {
		return ResolvedMedia{}, errors.New("media not found")
	}
	digest := sha256.Sum256(body)
	return ResolvedMedia{
		Body: io.NopCloser(bytes.NewReader(body)), Size: int64(len(body)),
		SHA256: fmt.Sprintf("%x", digest[:]), ContentType: resolver.types[ref],
	}, nil
}

func target(provider socialconnections.Provider, remoteID string) TargetResolver {
	return fixtureTargetResolver{provider: provider, remoteID: remoteID}
}

func registrationMedia() MediaResolver {
	return fixtureMediaResolver{
		assets: map[string][]byte{"asset": []byte("asset")},
		types:  map[string]string{"asset": "image/jpeg"},
	}
}

func fixtureRemoteID(provider string) string {
	switch provider {
	case ProviderX:
		return "123"
	case ProviderLinkedIn:
		return "urn:li:person:123"
	case ProviderPinterest:
		return "42"
	case ProviderGoogleBusinessProfile:
		return "accounts/1/locations/2"
	default:
		return ""
	}
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
	var mediaBody []byte
	if request.Media != nil {
		mediaBody, _ = io.ReadAll(request.Media.Body)
		_ = request.Media.Body.Close()
	}
	executor.calls = append(executor.calls, recordedCall{
		request: request, mediaBody: mediaBody,
	})
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
			Targets:  target(socialconnections.ProviderX, "postqron"),
			Media:    registrationMedia(),
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
		Targets:         target(socialconnections.ProviderX, "postqron"),
		Media:           registrationMedia(),
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
		if provider == ProviderLinkedIn {
			for _, capability := range publisher.(*Adapter).ProviderCapabilities() {
				if len(capability.Formats) != 1 ||
					capability.Formats[0] != "text" {
					t.Fatalf("LinkedIn capability=%+v", capability)
				}
			}
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

func TestLinkedInTextRegistersWithoutMediaResolver(t *testing.T) {
	registry := publishing.NewAdapterRegistry()
	if err := Register(registry, Config{
		Executor:        &fixtureExecutor{},
		Targets:         target(socialconnections.ProviderLinkedIn, "urn:li:person:123"),
		LinkedInVersion: "202606",
		Gates: map[string]Gate{
			ProviderLinkedIn: readyGates()[ProviderLinkedIn],
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ResolvePublisher(
		context.Background(),
		ProviderLinkedIn,
	); err != nil {
		t.Fatal(err)
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
			targets:         target(provider, fixtureRemoteID(fixture.Provider)),
			media:           registrationMedia(),
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
		executor: executor, targets: target(socialconnections.ProviderX, "postqron"),
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
			{StatusCode: 201, Header: http.Header{"X-Restli-Id": {"urn:li:share:ignored"}}, Body: []byte(`{"id":"urn:li:share:789"}`)},
		}}
		adapter := &Adapter{
			provider: ProviderLinkedIn, expected: socialconnections.ProviderLinkedIn,
			executor: executor, linkedinVersion: "202606",
			targets: target(socialconnections.ProviderLinkedIn, author),
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
		if !result.Complete || result.RemoteID != "urn:li:share:789" {
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
			adapter: &Adapter{provider: ProviderPinterest, expected: socialconnections.ProviderPinterest, targets: target(socialconnections.ProviderPinterest, "42")},
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
			adapter: &Adapter{provider: ProviderGoogleBusinessProfile, expected: socialconnections.ProviderGoogleBusinessProfile, targets: target(socialconnections.ProviderGoogleBusinessProfile, "accounts/1/locations/2")},
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

func TestPinterestImageVariantSelectionIsDeterministic(t *testing.T) {
	adapter := &Adapter{provider: ProviderPinterest}
	body := []byte(`{"items":[{
		"id":"99",
		"title":"title",
		"description":"body",
		"link":"https://example.com",
		"media":{"images":{
			"z_variant":{"url":"https://cdn.example.com/z.jpg"},
			"a_variant":{"url":"https://cdn.example.com/a.jpg"}
		}}
	}]}`)
	for attempt := 0; attempt < 100; attempt++ {
		items, err := adapter.decodeList(body)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || len(items[0].Media) != 1 ||
			items[0].Media[0].SourceURL != "https://cdn.example.com/a.jpg" {
			t.Fatalf("items=%+v", items)
		}
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
	state := mustJSON(checkpoint{
		Version: 1, Stage: "create", Target: "postqron",
		BaselineIDs: []string{"100"},
	})
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
			want: publishing.ReconciliationUnknown,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
				{StatusCode: 200, Body: []byte(test.body)},
			}}
			adapter := &Adapter{provider: ProviderX, expected: socialconnections.ProviderX, executor: executor, targets: target(socialconnections.ProviderX, "postqron"), reconcilePolls: 1}
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
		executor: executor, targets: target(socialconnections.ProviderX, "postqron"),
		reconcilePolls: 1,
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
		executor: executor, targets: target(socialconnections.ProviderPinterest, "42"),
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

func TestXOfficialMediaFlowUsesInitializeAppendFinalizeStatusAndMediaBoundary(
	t *testing.T,
) {
	raw := []byte("official-x-image")
	digest := sha256.Sum256(raw)
	payload := json.RawMessage(fmt.Sprintf(`{
		"format":"image",
		"text":"with media",
		"media":[{
			"ref":"asset",
			"content_type":"image/jpeg",
			"size":%d,
			"sha256":"%x"
		}]
	}`, len(raw), digest))
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"data":[]}`)},
		{StatusCode: 200, Body: []byte(`{"data":{"id":"10"}}`)},
		{StatusCode: 200, Body: []byte(`{"data":{"expires_at":1}}`)},
		{StatusCode: 200, Body: []byte(`{"data":{"id":"10","processing_info":{"state":"pending","check_after_secs":1}}}`)},
		{StatusCode: 200, Body: []byte(`{"data":{"id":"10","processing_info":{"state":"succeeded"}}}`)},
		{StatusCode: 201, Body: []byte(`{"data":{"id":"20"}}`)},
	}}
	adapter := &Adapter{
		provider: ProviderX, expected: socialconnections.ProviderX,
		executor: executor, targets: target(socialconnections.ProviderX, "123"),
		media: fixtureMediaResolver{
			assets: map[string][]byte{"asset": raw},
			types:  map[string]string{"asset": "image/jpeg"},
		},
	}
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection", Payload: payload,
	}
	var result publishing.PublishResult
	var err error
	for attempt := 0; attempt < 7; attempt++ {
		result, err = adapter.Publish(context.Background(), request)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		request.Checkpoint = result.Checkpoint
	}
	if !result.Complete || result.RemoteID != "20" {
		t.Fatalf("result=%+v", result)
	}
	wantPaths := []string{
		"/2/users/123/tweets?max_results=100&tweet.fields=attachments",
		"/2/media/upload/initialize",
		"/2/media/upload/10/append",
		"/2/media/upload/10/finalize",
		"/2/media/upload?command=STATUS&media_id=10",
		"/2/tweets",
	}
	if len(executor.calls) != len(wantPaths) {
		t.Fatalf("calls=%d", len(executor.calls))
	}
	for index, want := range wantPaths {
		if executor.calls[index].request.Path != want {
			t.Fatalf("call %d path=%q want=%q", index, executor.calls[index].request.Path, want)
		}
	}
	appendCall := executor.calls[2]
	if appendCall.request.Media == nil || len(appendCall.mediaBody) == 0 ||
		len(appendCall.request.Body) != 0 {
		t.Fatalf("append request=%+v media bytes=%d", appendCall.request, len(appendCall.mediaBody))
	}
	if !bytes.Contains(appendCall.mediaBody, raw) ||
		!bytes.Contains(appendCall.mediaBody, []byte(`name="segment_index"`)) {
		t.Fatal("X append did not carry deterministic multipart media")
	}
}

func TestAmbiguousXMediaCreateReconcilesWithoutSecondCreate(t *testing.T) {
	raw := []byte("x")
	digest := sha256.Sum256(raw)
	payload := json.RawMessage(fmt.Sprintf(`{
		"format":"image","text":"once",
		"media":[{"ref":"asset","content_type":"image/jpeg",
			"size":%d,"sha256":"%x"}]
	}`, len(raw), digest))
	executor := &fixtureExecutor{
		responses: []socialconnections.PublishingResponse{
			{StatusCode: 200, Body: []byte(`{"data":[]}`)},
			{StatusCode: 200, Body: []byte(`{"data":{"id":"10"}}`)},
			{StatusCode: 200, Body: []byte(`{"data":{"expires_at":1}}`)},
			{StatusCode: 200, Body: []byte(`{"data":{"id":"10"}}`)},
			{StatusCode: 200, Body: []byte(`{"data":{"id":"10","processing_info":{"state":"succeeded"}}}`)},
			{},
			{StatusCode: 200, Body: []byte(`{"data":[{"id":"20","text":"once"}]}`)},
		},
		errs: []error{
			nil, nil, nil, nil, nil,
			&socialconnections.ExecutorFailure{
				Kind: socialconnections.ExecutorFailureAmbiguous,
				Code: "provider_outcome_ambiguous",
			},
			nil,
		},
	}
	adapter := &Adapter{
		provider: ProviderX, expected: socialconnections.ProviderX,
		executor: executor, targets: target(socialconnections.ProviderX, "123"),
		media: fixtureMediaResolver{
			assets: map[string][]byte{"asset": raw},
			types:  map[string]string{"asset": "image/jpeg"},
		},
		reconcilePolls: 1,
	}
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection", Payload: payload,
	}
	for attempt := 0; attempt < 6; attempt++ {
		result, err := adapter.Publish(context.Background(), request)
		if err != nil {
			t.Fatalf("progress %d: %v", attempt, err)
		}
		request.Checkpoint = result.Checkpoint
	}
	_, err := adapter.Publish(context.Background(), request)
	var providerError *publishing.ProviderError
	if !errors.As(err, &providerError) || !providerError.Ambiguous {
		t.Fatalf("create error=%v", err)
	}
	reconciled, err := adapter.Reconcile(
		context.Background(),
		publishing.ReconcileRequest{
			WorkspaceID: "workspace", ConnectionID: "connection",
			Payload: payload, Checkpoint: request.Checkpoint,
		},
	)
	if err != nil || reconciled.State != publishing.ReconciliationFound ||
		reconciled.RemoteID != "20" {
		t.Fatalf("reconciled=%+v err=%v", reconciled, err)
	}
	creates := 0
	for _, call := range executor.calls {
		if call.request.Path == "/2/tweets" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("X create calls=%d", creates)
	}
}

func TestLinkedInSignedUploadURLIsPreservedAndMediaPathFailsClosed(
	t *testing.T,
) {
	raw := []byte("official-linkedin-image")
	digest := sha256.Sum256(raw)
	author := "urn:li:organization:456"
	payload := json.RawMessage(fmt.Sprintf(`{
		"format":"image",
		"text":"with media",
		"media":[{
			"ref":"asset",
			"content_type":"image/png",
			"size":%d,
			"sha256":"%x",
			"alt_text":"alt"
		}]
	}`, len(raw), digest))
	signedURL := "https://www.linkedin.com/dms-uploads/image/upload" +
		"?ca=123&cn=opaque%2Bsignature"
	uploadID, parsedURL, err := decodeLinkedInInitialize([]byte(`{"value":{
		"uploadUrl":"` + signedURL + `",
		"image":"urn:li:image:abc"
	}}`))
	if err != nil || uploadID != "urn:li:image:abc" || parsedURL != signedURL {
		t.Fatalf("upload id=%q url=%q err=%v", uploadID, parsedURL, err)
	}
	if _, _, err = decodeLinkedInInitialize([]byte(`{"value":{
		"uploadUrl":"https://api.linkedin.com/dms-uploads/image/upload?sig=opaque",
		"image":"urn:li:image:abc"
	}}`)); err == nil {
		t.Fatal("API resource-server origin must not substitute the signed DMS origin")
	}
	executor := &fixtureExecutor{}
	adapter := &Adapter{
		provider: ProviderLinkedIn, expected: socialconnections.ProviderLinkedIn,
		executor: executor, targets: target(socialconnections.ProviderLinkedIn, author),
		media: fixtureMediaResolver{
			assets: map[string][]byte{"asset": raw},
			types:  map[string]string{"asset": "image/png"},
		},
		linkedinVersion: "202606",
	}
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection", Payload: payload,
	}
	_, err = adapter.Publish(context.Background(), request)
	var providerError *publishing.ProviderError
	if !errors.As(err, &providerError) ||
		providerError.Code != "linkedin_media_upload_boundary_unavailable" ||
		providerError.Retryable {
		t.Fatalf("error=%v", err)
	}
	if len(executor.calls) != 0 {
		t.Fatalf("media path made %d requests before a safe F5 DMS boundary", len(executor.calls))
	}
	request.Checkpoint = mustJSON(checkpoint{
		Version: 1, Stage: "linkedin_create", Target: author,
		MediaRefs: []string{
			"ref\x00asset\x00image/png\x00" + fmt.Sprint(len(raw)) +
				"\x00" + fmt.Sprintf("%x", digest) + "\x00alt",
		},
		MediaIDs:   []string{"urn:li:image:abc"},
		UploadID:   "urn:li:image:abc",
		UploadPath: signedURL,
	})
	if _, err = adapter.Publish(context.Background(), request); err == nil {
		t.Fatal("forged media checkpoint reached LinkedIn create")
	}
	if len(executor.calls) != 0 {
		t.Fatalf("media checkpoint made %d requests without AVAILABLE proof", len(executor.calls))
	}
}

func TestCreate2xxWithoutIDIsAmbiguousAndNeverPostsTwice(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"data":[]}`)},
		{StatusCode: 201, Body: []byte(`{}`)},
		{StatusCode: 200, Body: []byte(`{"data":[{
			"id":"200","text":"publish once"
		}]}`)},
	}}
	adapter := &Adapter{
		provider: ProviderX, expected: socialconnections.ProviderX,
		executor: executor, targets: target(socialconnections.ProviderX, "123"),
		reconcilePolls: 1,
	}
	payload := json.RawMessage(`{"format":"text","text":"publish once"}`)
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection", Payload: payload,
	}
	progress, err := adapter.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Checkpoint = progress.Checkpoint
	_, err = adapter.Publish(context.Background(), request)
	var providerError *publishing.ProviderError
	if !errors.As(err, &providerError) || !providerError.Ambiguous ||
		!providerError.Retryable {
		t.Fatalf("create error=%v", err)
	}
	reconciled, err := adapter.Reconcile(
		context.Background(),
		publishing.ReconcileRequest{
			WorkspaceID: "workspace", ConnectionID: "connection",
			Payload: payload, Checkpoint: progress.Checkpoint,
		},
	)
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
		t.Fatalf("create POST calls=%d", posts)
	}
}

func TestEveryProviderCreate2xxWithoutIDIsAmbiguous(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		expected socialconnections.Provider
		target   string
		payload  string
		listBody string
	}{
		{
			name: "x", provider: ProviderX,
			expected: socialconnections.ProviderX, target: "123",
			payload:  `{"format":"text","text":"once"}`,
			listBody: `{"data":[]}`,
		},
		{
			name: "linkedin", provider: ProviderLinkedIn,
			expected: socialconnections.ProviderLinkedIn,
			target:   "urn:li:person:123",
			payload:  `{"format":"text","text":"once"}`,
			listBody: `{"elements":[]}`,
		},
		{
			name: "pinterest", provider: ProviderPinterest,
			expected: socialconnections.ProviderPinterest, target: "42",
			payload: `{"format":"image","title":"once","media":[{
				"source_url":"https://cdn.example.com/image.jpg"
			}]}`,
			listBody: `{"items":[]}`,
		},
		{
			name:     "google business profile",
			provider: ProviderGoogleBusinessProfile,
			expected: socialconnections.ProviderGoogleBusinessProfile,
			target:   "accounts/1/locations/2",
			payload:  `{"format":"text","text":"once"}`,
			listBody: `{"localPosts":[]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &fixtureExecutor{
				responses: []socialconnections.PublishingResponse{
					{StatusCode: 200, Body: []byte(test.listBody)},
					{StatusCode: 201, Body: []byte(`{}`)},
				},
			}
			adapter := &Adapter{
				provider: test.provider, expected: test.expected,
				executor: executor, targets: target(test.expected, test.target),
				linkedinVersion: "202606",
			}
			request := publishing.PublishRequest{
				WorkspaceID: "workspace", ConnectionID: "connection",
				Payload: json.RawMessage(test.payload),
			}
			progress, err := adapter.Publish(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			request.Checkpoint = progress.Checkpoint
			_, err = adapter.Publish(context.Background(), request)
			var providerError *publishing.ProviderError
			if !errors.As(err, &providerError) ||
				!providerError.Ambiguous || !providerError.Retryable {
				t.Fatalf("error=%v", err)
			}
			posts := 0
			for _, call := range executor.calls {
				if call.request.Method == http.MethodPost {
					posts++
				}
			}
			if posts != 1 {
				t.Fatalf("create POST calls=%d", posts)
			}
		})
	}
}

func TestLinkedInAmbiguousCreateReconcilesThroughFinderBoundary(t *testing.T) {
	author := "urn:li:organization:456"
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"elements":[]}`)},
		{StatusCode: 201, Header: http.Header{
			"X-Restli-Id": {"urn:li:share:ignored"},
		}, Body: []byte(`{}`)},
		{StatusCode: 200, Body: []byte(`{"elements":[{
			"id":"urn:li:share:789",
			"author":"urn:li:organization:456",
			"commentary":"once"
		}]}`)},
	}}
	adapter := &Adapter{
		provider: ProviderLinkedIn, expected: socialconnections.ProviderLinkedIn,
		executor: executor, targets: target(socialconnections.ProviderLinkedIn, author),
		linkedinVersion: "202606", reconcilePolls: 1,
	}
	payload := json.RawMessage(`{"format":"text","text":"once"}`)
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection", Payload: payload,
	}
	progress, err := adapter.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Checkpoint = progress.Checkpoint
	if _, err = adapter.Publish(context.Background(), request); err == nil {
		t.Fatal("body-less create must be ambiguous")
	}
	result, err := adapter.Reconcile(
		context.Background(),
		publishing.ReconcileRequest{
			WorkspaceID: "workspace", ConnectionID: "connection",
			Payload: payload, Checkpoint: progress.Checkpoint,
		},
	)
	if err != nil || result.State != publishing.ReconciliationFound ||
		result.RemoteID != "urn:li:share:789" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	finder := executor.calls[2].request
	if finder.Method != http.MethodGet ||
		finder.Header.Get("X-RestLi-Method") != "FINDER" ||
		finder.Header.Get("X-Restli-Id") != "" {
		t.Fatalf("finder=%+v", finder)
	}
	posts := 0
	for _, call := range executor.calls {
		if call.request.Method == http.MethodPost {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("create POST calls=%d", posts)
	}
}

func TestLinkedInLegacyCreatePollHasFiniteLimit(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{"elements":[]}`)},
		{StatusCode: 200, Body: []byte(`{"elements":[]}`)},
	}}
	author := "urn:li:person:123"
	adapter := &Adapter{
		provider: ProviderLinkedIn, expected: socialconnections.ProviderLinkedIn,
		executor: executor, targets: target(socialconnections.ProviderLinkedIn, author),
		linkedinVersion: "202606", createPollLimit: 2,
	}
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: json.RawMessage(
			`{"format":"text","text":"eventually visible"}`,
		),
		Checkpoint: mustJSON(checkpoint{
			Version: 1, Stage: "create_poll", Target: author,
		}),
	}
	first, err := adapter.Publish(context.Background(), request)
	if err != nil || first.Complete {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	request.Checkpoint = first.Checkpoint
	_, err = adapter.Publish(context.Background(), request)
	var providerError *publishing.ProviderError
	if !errors.As(err, &providerError) ||
		providerError.Code != "linkedin_create_poll_exhausted" ||
		providerError.Retryable {
		t.Fatalf("error=%v", err)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("poll calls=%d", len(executor.calls))
	}
}

func TestPayloadTargetsCannotOverrideF5ConnectionBinding(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		expected socialconnections.Provider
		target   string
		payload  string
	}{
		{
			name: "x author", provider: ProviderX,
			expected: socialconnections.ProviderX, target: "123",
			payload: `{"format":"text","text":"x","author":"999"}`,
		},
		{
			name: "linkedin author", provider: ProviderLinkedIn,
			expected: socialconnections.ProviderLinkedIn,
			target:   "urn:li:person:123",
			payload:  `{"format":"text","text":"x","author":"urn:li:organization:999"}`,
		},
		{
			name: "pinterest board", provider: ProviderPinterest,
			expected: socialconnections.ProviderPinterest, target: "42",
			payload: `{"format":"image","board_id":"99","media":[
				{"source_url":"https://cdn.example.com/image.jpg"}
			]}`,
		},
		{
			name: "GBP location", provider: ProviderGoogleBusinessProfile,
			expected: socialconnections.ProviderGoogleBusinessProfile,
			target:   "accounts/1/locations/2",
			payload: `{"format":"text","text":"x",
				"location":"accounts/1/locations/999"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &fixtureExecutor{}
			adapter := &Adapter{
				provider: test.provider, expected: test.expected,
				executor: executor, targets: target(test.expected, test.target),
			}
			_, err := adapter.Publish(context.Background(), publishing.PublishRequest{
				WorkspaceID: "workspace", ConnectionID: "connection",
				Payload: json.RawMessage(test.payload),
			})
			var providerError *publishing.ProviderError
			if !errors.As(err, &providerError) ||
				providerError.Code != "connection_target_mismatch" {
				t.Fatalf("error=%v", err)
			}
			if len(executor.calls) != 0 {
				t.Fatalf("executor calls=%d", len(executor.calls))
			}
		})
	}
}

func TestXAndLinkedInRejectPreloadedRemoteMediaIdentifiers(t *testing.T) {
	tests := []struct {
		provider string
		expected socialconnections.Provider
		target   string
		payload  string
	}{
		{
			provider: ProviderX, expected: socialconnections.ProviderX,
			target:  "123",
			payload: `{"format":"image","text":"x","media":[{"id":"456"}]}`,
		},
		{
			provider: ProviderLinkedIn,
			expected: socialconnections.ProviderLinkedIn,
			target:   "urn:li:person:123",
			payload: `{"format":"image","text":"x","media":[
				{"id":"urn:li:image:preloaded"}
			]}`,
		},
	}
	for _, test := range tests {
		executor := &fixtureExecutor{}
		adapter := &Adapter{
			provider: test.provider, expected: test.expected,
			executor: executor, targets: target(test.expected, test.target),
		}
		_, err := adapter.Publish(context.Background(), publishing.PublishRequest{
			WorkspaceID: "workspace", ConnectionID: "connection",
			Payload: json.RawMessage(test.payload),
		})
		var providerError *publishing.ProviderError
		if !errors.As(err, &providerError) ||
			providerError.Code != "payload_unsupported" {
			t.Fatalf("%s error=%v", test.provider, err)
		}
		if len(executor.calls) != 0 {
			t.Fatalf("%s executor calls=%d", test.provider, len(executor.calls))
		}
	}
}

func TestReconcilePaginatesAndNeverRepostsWhenVisibilityIsDelayed(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		{StatusCode: 200, Body: []byte(`{
			"data":[{"id":"100","text":"older"}],
			"meta":{"next_token":"page-2"}
		}`)},
		{StatusCode: 200, Body: []byte(`{
			"data":[{"id":"101","text":"older"}]
		}`)},
		{StatusCode: 200, Body: []byte(`{"data":[{"id":"100","text":"older"}]}`)},
		{StatusCode: 200, Body: []byte(`{"data":[{"id":"100","text":"older"}]}`)},
	}}
	adapter := &Adapter{
		provider: ProviderX, expected: socialconnections.ProviderX,
		executor: executor, targets: target(socialconnections.ProviderX, "123"),
		reconcilePolls: 2,
	}
	payload := json.RawMessage(`{"format":"text","text":"new"}`)
	progress, err := adapter.Publish(context.Background(), publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection", Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.calls[1].request.Path !=
		"/2/users/123/tweets?max_results=100&pagination_token=page-2&tweet.fields=attachments" {
		t.Fatalf("second page=%q", executor.calls[1].request.Path)
	}
	result, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: payload, Checkpoint: progress.Checkpoint,
	})
	if err != nil || result.State != publishing.ReconciliationUnknown {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, call := range executor.calls {
		if call.request.Method == http.MethodPost {
			t.Fatalf("unexpected create path=%s", call.request.Path)
		}
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
	adapter := &Adapter{provider: ProviderX, expected: socialconnections.ProviderX, executor: executor, targets: target(socialconnections.ProviderX, "postqron")}
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
