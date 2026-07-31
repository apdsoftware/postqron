package dynamic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

type fixtureFile struct {
	data []byte
}

type executorFunc func(
	context.Context,
	socialconnections.PublishingRequest,
) (socialconnections.PublishingResponse, error)

func (execute executorFunc) Execute(
	ctx context.Context,
	request socialconnections.PublishingRequest,
) (socialconnections.PublishingResponse, error) {
	return execute(ctx, request)
}

type failingMediaSource struct{}

func (failingMediaSource) Open(
	context.Context, string,
) (io.ReadCloser, error) {
	return nil, errors.New("temporary storage outage")
}

func (source fixtureFile) Open(
	_ context.Context,
	key string,
) (io.ReadCloser, error) {
	if key != "fixture/image.png" {
		return nil, errors.New("unknown fixture")
	}
	return io.NopCloser(bytes.NewReader(source.data)), nil
}

type tlsFixtureExecutor struct {
	server         *httptest.Server
	provider       socialconnections.Provider
	ambiguousPath  string
	ambiguousOnce  atomic.Bool
	observedSecret atomic.Bool
}

func (executor *tlsFixtureExecutor) Execute(
	ctx context.Context,
	request socialconnections.PublishingRequest,
) (socialconnections.PublishingResponse, error) {
	if request.ExpectedProvider != executor.provider ||
		request.WorkspaceID == "" || request.ConnectionID == "" ||
		request.Header.Get("Authorization") != "" ||
		request.Header.Get("DPoP") != "" ||
		request.Header.Get("DPoP-Nonce") != "" {
		executor.observedSecret.Store(true)
		return socialconnections.PublishingResponse{}, errors.New("unsafe request")
	}
	var body io.Reader = bytes.NewReader(request.Body)
	if request.Media != nil {
		body = request.Media.Body
		defer request.Media.Body.Close()
	}
	outbound, err := http.NewRequestWithContext(
		ctx,
		request.Method,
		executor.server.URL+request.Path,
		body,
	)
	if err != nil {
		return socialconnections.PublishingResponse{}, err
	}
	outbound.Header = request.Header.Clone()
	if outbound.Header == nil {
		outbound.Header = make(http.Header)
	}
	if request.Media != nil {
		outbound.ContentLength = request.Media.Size
	}
	// This fixture-only value simulates credential injection inside F5. It is
	// never visible to the adapter request or returned response.
	outbound.Header.Set("Authorization", "DPoP fixture-boundary-secret")
	response, err := executor.server.Client().Do(outbound)
	if err != nil {
		return socialconnections.PublishingResponse{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return socialconnections.PublishingResponse{}, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return socialconnections.PublishingResponse{},
			&socialconnections.ExecutorFailure{
				Kind: socialconnections.ExecutorFailurePermanent,
				Code: "provider_request_rejected",
			}
	}
	if request.Method == http.MethodPost &&
		request.Path == executor.ambiguousPath &&
		executor.ambiguousOnce.CompareAndSwap(true, false) {
		return socialconnections.PublishingResponse{},
			&socialconnections.ExecutorFailure{
				Kind: socialconnections.ExecutorFailureAmbiguous,
				Code: "provider_outcome_ambiguous",
			}
	}
	return socialconnections.PublishingResponse{
		StatusCode: response.StatusCode,
		Header:     response.Header.Clone(),
		Body:       responseBody,
	}, nil
}

type officialFixtures struct {
	Mastodon struct {
		Instance json.RawMessage `json:"instance"`
		Media    json.RawMessage `json:"media"`
		Status   json.RawMessage `json:"status"`
	} `json:"mastodon"`
	Bluesky struct {
		Blob   json.RawMessage `json:"blob"`
		Record struct {
			CID string `json:"cid"`
		} `json:"record"`
	} `json:"bluesky"`
}

func loadOfficialFixtures(t *testing.T) officialFixtures {
	t.Helper()
	raw, err := os.ReadFile("testdata/official-dynamic-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture officialFixtures
	if json.Unmarshal(raw, &fixture) != nil {
		t.Fatal("invalid official fixture")
	}
	return fixture
}

func TestMastodonOfficialTLSMediaCheckpointAndIdempotentReconciliation(
	t *testing.T,
) {
	fixture := loadOfficialFixtures(t)
	image := []byte("fixture-png-15")
	digest := sha256.Sum256(image)
	var (
		mu                sync.Mutex
		statusByKey       = make(map[string]json.RawMessage)
		statusSideEffects int
		mediaUploads      int
	)
	server := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.TLS == nil ||
				request.Header.Get("Authorization") != "DPoP fixture-boundary-secret" {
				http.Error(writer, "tls boundary required", http.StatusUnauthorized)
				return
			}
			switch request.URL.Path {
			case mastodonInstancePath:
				_, _ = writer.Write(fixture.Mastodon.Instance)
			case mastodonMediaPath:
				if err := request.ParseMultipartForm(1 << 20); err != nil {
					http.Error(writer, "multipart required", http.StatusBadRequest)
					return
				}
				file, _, err := request.FormFile("file")
				if err != nil {
					http.Error(writer, "file required", http.StatusBadRequest)
					return
				}
				uploaded, _ := io.ReadAll(file)
				_ = file.Close()
				if !bytes.Equal(uploaded, image) {
					http.Error(writer, "wrong media", http.StatusBadRequest)
					return
				}
				mu.Lock()
				mediaUploads++
				mu.Unlock()
				writer.Header().Set("Retry-After", "0")
				_, _ = writer.Write(fixture.Mastodon.Media)
			case mastodonStatusesPath:
				key := request.Header.Get("Idempotency-Key")
				if !strings.HasPrefix(key, "publish_") {
					http.Error(writer, "idempotency required", http.StatusBadRequest)
					return
				}
				mu.Lock()
				body, exists := statusByKey[key]
				if !exists {
					body = fixture.Mastodon.Status
					statusByKey[key] = body
					statusSideEffects++
				}
				mu.Unlock()
				_, _ = writer.Write(body)
			default:
				http.NotFound(writer, request)
			}
		},
	))
	defer server.Close()
	executor := &tlsFixtureExecutor{
		server: server, provider: socialconnections.ProviderMastodon,
		ambiguousPath: mastodonStatusesPath,
	}
	adapter := newMastodonForTest(executor, fixtureFile{data: image})
	payload := mustJSON(t, mastodonPayload{
		Text: "offline TLS fixture", Visibility: "public",
		Media: []media{{
			StorageKey: "fixture/image.png", ContentType: "image/png",
			SizeBytes: int64(len(image)),
			SHA256:    hex.EncodeToString(digest[:]), Alt: "fixture",
			Width: 10, Height: 10,
		}},
	})
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: payload, IdempotencyKey: "publish_" + strings.Repeat("a", 64),
	}
	var result publishing.PublishResult
	for step := 0; step < 8; step++ {
		var err error
		result, err = adapter.Publish(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.Complete {
			break
		}
		request.Checkpoint = result.Checkpoint
	}
	if !result.Complete || result.RemoteID != "92001" ||
		result.Permalink != "https://social.example/@fixture/92001" {
		t.Fatalf("publish result=%+v", result)
	}
	mu.Lock()
	if mediaUploads != 1 || statusSideEffects != 1 {
		t.Fatalf(
			"media uploads=%d status side effects=%d",
			mediaUploads,
			statusSideEffects,
		)
	}
	mu.Unlock()

	// Simulate a crash after Mastodon committed the status but before F8
	// durably recorded the response. Reconciliation replays the official
	// idempotency key and receives the same status, never a duplicate.
	executor.ambiguousOnce.Store(true)
	_, err := adapter.Publish(context.Background(), request)
	var providerErr *publishing.ProviderError
	if !errors.As(err, &providerErr) || !providerErr.Ambiguous {
		t.Fatalf("ambiguous error=%v", err)
	}
	reconciled, err := adapter.Reconcile(
		context.Background(),
		publishing.ReconcileRequest{
			WorkspaceID:  request.WorkspaceID,
			ConnectionID: request.ConnectionID,
			Payload:      request.Payload, Checkpoint: request.Checkpoint,
			IdempotencyKey: request.IdempotencyKey,
		},
	)
	if err != nil || reconciled.State != publishing.ReconciliationFound {
		t.Fatalf("reconciliation=%+v error=%v", reconciled, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if statusSideEffects != 1 {
		t.Fatalf("status side effects=%d, want 1", statusSideEffects)
	}
	if executor.observedSecret.Load() {
		t.Fatal("adapter exposed an authentication or DPoP secret")
	}
}

func TestBlueskyOfficialTLSMediaAndDeterministicRace(t *testing.T) {
	fixture := loadOfficialFixtures(t)
	image := []byte("fixture-png-15")
	digest := sha256.Sum256(image)
	var (
		mu          sync.Mutex
		records     = make(map[string]blueskyRecordEnvelope)
		sideEffects int
		blobUploads int
	)
	server := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			if request.TLS == nil ||
				request.Header.Get("Authorization") != "DPoP fixture-boundary-secret" {
				http.Error(writer, "tls boundary required", http.StatusUnauthorized)
				return
			}
			switch request.URL.Path {
			case blueskyUploadBlobPath:
				body, _ := io.ReadAll(request.Body)
				if !bytes.Equal(body, image) ||
					request.Header.Get("Content-Type") != "image/png" {
					http.Error(writer, "wrong blob", http.StatusBadRequest)
					return
				}
				mu.Lock()
				blobUploads++
				mu.Unlock()
				_, _ = writer.Write(fixture.Bluesky.Blob)
			case blueskyCreatePath:
				var body struct {
					Repository string          `json:"repo"`
					Collection string          `json:"collection"`
					RKey       string          `json:"rkey"`
					Record     json.RawMessage `json:"record"`
				}
				if json.NewDecoder(request.Body).Decode(&body) != nil ||
					body.Collection != blueskyCollection {
					http.Error(writer, "wrong record", http.StatusBadRequest)
					return
				}
				uri := blueskyURI(body.Repository, body.RKey)
				mu.Lock()
				record, exists := records[uri]
				if !exists {
					record = blueskyRecordEnvelope{
						URI: uri, CID: fixture.Bluesky.Record.CID,
						Value: body.Record,
					}
					records[uri] = record
					sideEffects++
				}
				mu.Unlock()
				_ = json.NewEncoder(writer).Encode(record)
			case blueskyGetPath:
				uri := blueskyURI(
					request.URL.Query().Get("repo"),
					request.URL.Query().Get("rkey"),
				)
				mu.Lock()
				record, exists := records[uri]
				mu.Unlock()
				if !exists {
					http.Error(writer, "record not found", http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(writer).Encode(record)
			default:
				http.NotFound(writer, request)
			}
		},
	))
	defer server.Close()
	executor := &tlsFixtureExecutor{
		server: server, provider: socialconnections.ProviderBluesky,
	}
	adapter := newBlueskyForTest(executor, fixtureFile{data: image})
	payload := mustJSON(t, blueskyPayload{
		Repository: "did:plc:fixture123",
		Text:       "offline TLS fixture",
		CreatedAt:  "2026-07-30T12:00:00Z",
		Languages:  []string{"en"},
		Media: []media{{
			StorageKey: "fixture/image.png", ContentType: "image/png",
			SizeBytes: int64(len(image)),
			SHA256:    hex.EncodeToString(digest[:]), Alt: "fixture",
			Width: 10, Height: 10,
		}},
	})
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: payload, IdempotencyKey: "publish_" + strings.Repeat("b", 64),
	}
	first, err := adapter.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Checkpoint = first.Checkpoint
	second, err := adapter.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Checkpoint = second.Checkpoint
	if blobUploads != 1 {
		t.Fatalf("blob uploads=%d", blobUploads)
	}

	const racers = 12
	results := make(chan publishing.PublishResult, racers)
	failures := make(chan error, racers)
	var group sync.WaitGroup
	for index := 0; index < racers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, publishErr := adapter.Publish(context.Background(), request)
			results <- result
			failures <- publishErr
		}()
	}
	group.Wait()
	close(results)
	close(failures)
	for publishErr := range failures {
		if publishErr != nil {
			t.Fatal(publishErr)
		}
	}
	var remoteID string
	for result := range results {
		if !result.Complete {
			t.Fatalf("incomplete race result=%+v", result)
		}
		if remoteID == "" {
			remoteID = result.RemoteID
		} else if result.RemoteID != remoteID {
			t.Fatalf("remote ids differ: %q != %q", result.RemoteID, remoteID)
		}
	}
	mu.Lock()
	if sideEffects != 1 {
		t.Fatalf("record side effects=%d, want 1", sideEffects)
	}
	mu.Unlock()

	reconciled, err := adapter.Reconcile(
		context.Background(),
		publishing.ReconcileRequest{
			WorkspaceID:  request.WorkspaceID,
			ConnectionID: request.ConnectionID,
			Payload:      request.Payload, Checkpoint: request.Checkpoint,
			IdempotencyKey: request.IdempotencyKey,
		},
	)
	if err != nil || reconciled.State != publishing.ReconciliationFound ||
		reconciled.RemoteID != remoteID {
		t.Fatalf("reconciliation=%+v error=%v", reconciled, err)
	}
	if executor.observedSecret.Load() {
		t.Fatal("adapter exposed an authentication or DPoP secret")
	}
}

func TestBlueskyAmbiguousCreateReconcilesWithoutDuplicate(t *testing.T) {
	var (
		mu          sync.Mutex
		records     = make(map[string]blueskyRecordEnvelope)
		sideEffects int
	)
	server := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case blueskyCreatePath:
				var input struct {
					Repository string          `json:"repo"`
					RKey       string          `json:"rkey"`
					Record     json.RawMessage `json:"record"`
				}
				_ = json.NewDecoder(request.Body).Decode(&input)
				uri := blueskyURI(input.Repository, input.RKey)
				mu.Lock()
				record, exists := records[uri]
				if !exists {
					record = blueskyRecordEnvelope{
						URI:   uri,
						CID:   "bafyreifixturerecord",
						Value: input.Record,
					}
					records[uri] = record
					sideEffects++
				}
				current := record
				mu.Unlock()
				_ = json.NewEncoder(writer).Encode(current)
			case blueskyGetPath:
				expected := blueskyURI(
					request.URL.Query().Get("repo"),
					request.URL.Query().Get("rkey"),
				)
				mu.Lock()
				current, exists := records[expected]
				mu.Unlock()
				if !exists {
					http.Error(writer, "record not found", http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(writer).Encode(current)
			}
		},
	))
	defer server.Close()
	executor := &tlsFixtureExecutor{
		server: server, provider: socialconnections.ProviderBluesky,
		ambiguousPath: blueskyCreatePath,
	}
	executor.ambiguousOnce.Store(true)
	adapter := newBlueskyForTest(executor, nil)
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: mustJSON(t, blueskyPayload{
			Repository: "did:plc:fixture123", Text: "crash fixture",
			CreatedAt: "2026-07-30T12:00:00Z",
		}),
		IdempotencyKey: "publish_" + strings.Repeat("c", 64),
	}
	progress, err := adapter.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.Checkpoint = progress.Checkpoint
	_, err = adapter.Publish(context.Background(), request)
	var providerErr *publishing.ProviderError
	if !errors.As(err, &providerErr) || !providerErr.Ambiguous {
		t.Fatalf("ambiguous error=%v", err)
	}
	reconciled, err := adapter.Reconcile(
		context.Background(),
		publishing.ReconcileRequest{
			WorkspaceID:  request.WorkspaceID,
			ConnectionID: request.ConnectionID,
			Payload:      request.Payload, Checkpoint: request.Checkpoint,
			IdempotencyKey: request.IdempotencyKey,
		},
	)
	if err != nil || reconciled.State != publishing.ReconciliationFound {
		t.Fatalf("reconciliation=%+v error=%v", reconciled, err)
	}
	missingRequest := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: mustJSON(t, blueskyPayload{
			Repository: "did:plc:fixture123", Text: "pre-send crash",
			CreatedAt: "2026-07-30T12:00:01Z",
		}),
		IdempotencyKey: "publish_" + strings.Repeat("9", 64),
	}
	missingProgress, err := adapter.Publish(
		context.Background(),
		missingRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	missingReconciled, err := adapter.Reconcile(
		context.Background(),
		publishing.ReconcileRequest{
			WorkspaceID:    missingRequest.WorkspaceID,
			ConnectionID:   missingRequest.ConnectionID,
			Payload:        missingRequest.Payload,
			Checkpoint:     missingProgress.Checkpoint,
			IdempotencyKey: missingRequest.IdempotencyKey,
		},
	)
	if err != nil ||
		missingReconciled.State != publishing.ReconciliationFound {
		t.Fatalf(
			"missing reconciliation=%+v error=%v",
			missingReconciled,
			err,
		)
	}
	mu.Lock()
	defer mu.Unlock()
	if sideEffects != 2 {
		t.Fatalf("side effects=%d, want 2 distinct deterministic records", sideEffects)
	}
}

func TestExecutorFailureMapsRetryAfterAndRedacts(t *testing.T) {
	retryAfter := 37 * time.Second
	err := providerError(&socialconnections.ExecutorFailure{
		Kind: socialconnections.ExecutorFailureRateLimit,
		Code: "provider_rate_limited", RetryAfter: retryAfter,
	})
	var providerErr *publishing.ProviderError
	if !errors.As(err, &providerErr) || !providerErr.Retryable ||
		providerErr.RetryAfter != retryAfter ||
		strings.Contains(providerErr.Detail, "token") {
		t.Fatalf("provider error=%+v", providerErr)
	}
}

func TestOfficialClassifiersPreserveRetryAfterAndReconnect(t *testing.T) {
	classification, ok := (MastodonResponseClassifier{}).
		ClassifyProviderResponse(socialconnections.ProviderResponseEvidence{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": {"41"}},
			Method:     http.MethodGet,
		})
	if !ok ||
		classification.Kind != socialconnections.ExecutorFailureRateLimit ||
		classification.RetryAfter != 41*time.Second {
		t.Fatalf("Mastodon classification=%+v ok=%v", classification, ok)
	}
	classification, ok = (BlueskyResponseClassifier{}).
		ClassifyProviderResponse(socialconnections.ProviderResponseEvidence{
			StatusCode: http.StatusBadRequest,
			Body:       []byte(`{"error":"ExpiredToken"}`),
			Method:     http.MethodGet,
		})
	if !ok ||
		classification.Kind != socialconnections.ExecutorFailureReconnect ||
		!classification.Reconnect {
		t.Fatalf("Bluesky classification=%+v ok=%v", classification, ok)
	}
}

func TestAmbiguousMediaUploadsFailClosed(t *testing.T) {
	for name, reconcile := range map[string]func() (publishing.ReconcileResult, error){
		"mastodon": func() (publishing.ReconcileResult, error) {
			adapter := newMastodonForTest(nil, nil)
			payload := mustJSON(t, mastodonPayload{
				Text: "fixture", Visibility: "public",
				Media: []media{validFixtureMedia()},
			})
			state, _ := encodeCheckpoint(mastodonCheckpoint{
				Step: "media_upload_pending",
				Capabilities: mastodonCapabilities{
					MaxCharacters: 500, CharactersReservedPerURL: 23,
					MaxAttachments: 4, DescriptionLimit: 1500,
					MIMETypes:  []string{"image/png"},
					ImageBytes: 1 << 20, ImageMatrixLimit: 1 << 20,
					VideoBytes: 1 << 20, VideoMatrixLimit: 1 << 20,
					VideoFrameRateLimit: 60,
				},
			}, "fixture")
			return adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
				Payload: payload, Checkpoint: state,
				IdempotencyKey: "publish_" + strings.Repeat("d", 64),
			})
		},
		"bluesky": func() (publishing.ReconcileResult, error) {
			adapter := newBlueskyForTest(nil, nil)
			payload := mustJSON(t, blueskyPayload{
				Repository: "did:plc:fixture123", Text: "fixture",
				CreatedAt: "2026-07-30T12:00:00Z",
				Media:     []media{validFixtureMedia()},
			})
			state, _ := encodeCheckpoint(blueskyCheckpoint{
				Step:         "media_upload_pending",
				Capabilities: officialBlueskyCapabilities(),
				RKey: mustBlueskyRKey(
					t,
					"publish_"+strings.Repeat("d", 64),
					"2026-07-30T12:00:00Z",
				),
			}, "fixture")
			return adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
				Payload: payload, Checkpoint: state,
				IdempotencyKey: "publish_" + strings.Repeat("d", 64),
			})
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := reconcile()
			if err != nil || result.State != publishing.ReconciliationUnknown {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
	}
}

func validFixtureMedia() media {
	data := []byte("fixture-png-15")
	digest := sha256.Sum256(data)
	return media{
		StorageKey: "fixture/image.png", ContentType: "image/png",
		SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
		Width: 10, Height: 10,
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustBlueskyRKey(
	t *testing.T,
	idempotencyKey, createdAt string,
) string {
	t.Helper()
	allocator := publishing.NewMemoryStore()
	value, err := blueskyRKey(
		context.Background(),
		allocator,
		"did:plc:fixture123",
		createdAt,
		idempotencyKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCanonicalReconciliationQueryIsRelativeAndEncoded(t *testing.T) {
	path := canonicalQueryPath(blueskyGetPath, url.Values{
		"repo": {"did:plc:fixture"},
	})
	if path != blueskyGetPath+"?repo=did%3Aplc%3Afixture" {
		t.Fatalf("path=%q", path)
	}
}

func TestBlueskyDeterministicRKeyIsOfficialTID(t *testing.T) {
	allocator := publishing.NewMemoryStore()
	key := "publish_" + strings.Repeat("e", 64)
	first, err := blueskyRKey(
		context.Background(), allocator, "did:plc:fixture123",
		"2026-07-30T12:00:00Z", key,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := blueskyRKey(
		context.Background(), allocator, "did:plc:fixture123",
		"2026-07-30T12:00:00Z", key,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !atTIDPattern.MatchString(first) ||
		len(first) != 13 {
		t.Fatalf("deterministic TID first=%q second=%q", first, second)
	}
	different, err := blueskyRKey(
		context.Background(), allocator, "did:plc:fixture123",
		"2026-07-30T12:00:00Z",
		"publish_"+strings.Repeat("f", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if different == first {
		t.Fatalf("different idempotency keys produced TID %q", first)
	}
	later, err := blueskyRKey(
		context.Background(), allocator, "did:plc:fixture123",
		"2026-07-30T12:00:00.000001Z",
		"publish_"+strings.Repeat("d", 64),
	)
	if err != nil {
		t.Fatal(err)
	}
	if later <= first {
		t.Fatalf("TID is not monotonic: later=%q first=%q", later, first)
	}
	createdAtMicros := time.Date(
		2026, 7, 30, 12, 0, 0, 0, time.UTC,
	).UnixMicro()
	logicalMicros := tidTimestampMicros(t, first)
	if logicalMicros != createdAtMicros {
		t.Fatalf(
			"TID %q logical timestamp=%d createdAt=%d",
			first, logicalMicros, createdAtMicros,
		)
	}
}

func TestMastodonExpiredIdempotencyWindowNeverReplaysCreate(t *testing.T) {
	now := time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	adapter := newMastodonForTest(executorFunc(func(
		context.Context, socialconnections.PublishingRequest,
	) (socialconnections.PublishingResponse, error) {
		calls.Add(1)
		return socialconnections.PublishingResponse{}, nil
	}), nil)
	adapter.clock = func() time.Time { return now }
	payload := mustJSON(t, mastodonPayload{
		Text: "already attempted", Visibility: "public",
	})
	state := mastodonCheckpoint{
		Step:             "status_pending",
		Capabilities:     validMastodonCapabilities(),
		StatusPreparedAt: now.Add(-mastodonReplayWindow),
	}
	checkpoint := mustJSON(t, state)
	_, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: payload, Checkpoint: checkpoint,
		IdempotencyKey: "publish_" + strings.Repeat("1", 64),
	})
	var providerErr *publishing.ProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "mastodon_idempotency_window_expired" ||
		providerErr.Retryable || calls.Load() != 0 {
		t.Fatalf("error=%v calls=%d", err, calls.Load())
	}
}

func TestMastodonAllowsMediaOnlyAndEnforcesOfficialCapabilities(t *testing.T) {
	payload := mastodonPayload{
		Visibility: "public", Media: []media{validFixtureMedia()},
	}
	if _, _, err := newMastodonForTest(nil, nil).input(
		publishing.PublishRequest{Payload: mustJSON(t, payload)},
	); err != nil {
		t.Fatalf("media-only input=%v", err)
	}
	if err := validateMastodonPayload(
		payload, validMastodonCapabilities(),
	); err != nil {
		t.Fatalf("media-only validation=%v", err)
	}
	capability := validMastodonCapabilities()
	capability.MaxCharacters = 24
	if err := validateMastodonPayload(mastodonPayload{
		Text: "https://example.test/a", Visibility: "public",
	}, capability); err != nil {
		t.Fatalf("reserved URL validation=%v", err)
	}
	video := validFixtureMedia()
	video.ContentType = "video/mp4"
	video.FrameRate = capability.VideoFrameRateLimit + 1
	if err := validateMastodonPayload(mastodonPayload{
		Visibility: "public", Media: []media{video},
	}, capability); err == nil {
		t.Fatal("video above frame-rate limit accepted")
	}
	image := validFixtureMedia()
	image.Alt = strings.Repeat("x", capability.DescriptionLimit+1)
	if err := validateMastodonPayload(mastodonPayload{
		Visibility: "public", Media: []media{image},
	}, capability); err == nil {
		t.Fatal("description above instance limit accepted")
	}
	image = validFixtureMedia()
	image.Width = int(capability.ImageMatrixLimit)
	image.Height = 2
	if err := validateMastodonPayload(mastodonPayload{
		Visibility: "public", Media: []media{image},
	}, capability); err == nil {
		t.Fatal("image above matrix limit accepted")
	}
}

func TestMastodonOfficialAudioCapabilitiesAreFilteredNotRejected(t *testing.T) {
	fixture := loadOfficialFixtures(t)
	capability, err := decodeMastodonCapabilities(fixture.Mastodon.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(capability.MIMETypes, "image/png") ||
		!containsString(capability.MIMETypes, "video/mp4") {
		t.Fatalf("publishable MIME types=%v", capability.MIMETypes)
	}
	for _, contentType := range capability.MIMETypes {
		if strings.HasPrefix(contentType, "audio/") {
			t.Fatalf("audio type %q was not filtered", contentType)
		}
	}
	audioOnly := validMastodonCapabilities()
	audioOnly.MIMETypes = filterMastodonMIMETypes(
		[]string{"audio/mpeg", "audio/ogg"},
	)
	if err = validateMastodonPayload(mastodonPayload{
		Text: "text remains publishable", Visibility: "public",
	}, audioOnly); err != nil {
		t.Fatalf("text-only payload with audio-only instance=%v", err)
	}
	if err = validateMastodonPayload(mastodonPayload{
		Visibility: "public", Media: []media{validFixtureMedia()},
	}, audioOnly); err == nil {
		t.Fatal("image accepted after required image format was filtered out")
	}
}

func TestMediaSourceFailuresAreRetryable(t *testing.T) {
	mastodon := newMastodonForTest(nil, failingMediaSource{})
	_, _, err := mastodon.multipartMedia(
		context.Background(), validFixtureMedia(),
	)
	assertRetryableProviderError(t, err)

	bluesky := newBlueskyForTest(nil, failingMediaSource{})
	payload := blueskyPayload{
		Repository: "did:plc:fixture123", Text: "fixture",
		CreatedAt: "2026-07-30T12:00:00Z",
		Media:     []media{validFixtureMedia()},
	}
	state := blueskyCheckpoint{
		Step:         "media_upload_pending",
		Capabilities: officialBlueskyCapabilities(),
		RKey: mustBlueskyRKey(
			t, "publish_"+strings.Repeat("2", 64), payload.CreatedAt,
		),
	}
	_, err = bluesky.Publish(context.Background(), publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: mustJSON(t, payload), Checkpoint: mustJSON(t, state),
		IdempotencyKey: "publish_" + strings.Repeat("2", 64),
	})
	assertRetryableProviderError(t, err)
}

func TestBlueskyRejectsPayloadRepositoryMismatchBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	adapter := newBlueskyForTest(executorFunc(func(
		context.Context, socialconnections.PublishingRequest,
	) (socialconnections.PublishingResponse, error) {
		calls.Add(1)
		return socialconnections.PublishingResponse{}, nil
	}), nil)
	adapter.identity = connectionIdentityResolverFunc(func(
		context.Context, string, string, socialconnections.Provider,
	) (string, error) {
		return "did:plc:trusted", nil
	})
	_, err := adapter.Publish(context.Background(), publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: mustJSON(t, blueskyPayload{
			Repository: "did:plc:attacker", Text: "fixture",
			CreatedAt: "2026-07-30T12:00:00Z",
		}),
		IdempotencyKey: "publish_" + strings.Repeat("3", 64),
	})
	var providerErr *publishing.ProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "bluesky_repository_mismatch" ||
		calls.Load() != 0 {
		t.Fatalf("error=%v calls=%d", err, calls.Load())
	}
}

func TestBlueskyReconciliationRejectsSemanticRecordMismatch(t *testing.T) {
	payload := blueskyPayload{
		Repository: "did:plc:fixture123", Text: "expected",
		CreatedAt: "2026-07-30T12:00:00Z", Languages: []string{"en"},
		Media: []media{validFixtureMedia()},
	}
	key := "publish_" + strings.Repeat("4", 64)
	state := blueskyCheckpoint{
		Step:         "record_pending",
		Capabilities: officialBlueskyCapabilities(),
		RKey:         mustBlueskyRKey(t, key, payload.CreatedAt),
		MediaIndex:   1,
		Blobs: []json.RawMessage{mustJSON(t, map[string]any{
			"$type":    "blob",
			"ref":      map[string]string{"$link": "bafkreiexpectedblob"},
			"mimeType": "image/png",
			"size":     int64(len([]byte("fixture-png-15"))),
		})},
	}
	adapter := newBlueskyForTest(executorFunc(func(
		_ context.Context,
		request socialconnections.PublishingRequest,
	) (socialconnections.PublishingResponse, error) {
		if request.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", request.Method)
		}
		return socialconnections.PublishingResponse{
			StatusCode: http.StatusOK,
			Body: mustJSON(t, blueskyRecordEnvelope{
				URI: blueskyURI(payload.Repository, state.RKey),
				CID: "bafyreivalidcid",
				Value: mustJSON(t, map[string]any{
					"$type":     blueskyCollection,
					"text":      payload.Text,
					"createdAt": payload.CreatedAt,
					"langs":     payload.Languages,
					"embed": map[string]any{
						"$type": "app.bsky.embed.images",
						"images": []any{map[string]any{
							"alt": "",
							"image": map[string]any{
								"$type": "blob",
								"ref": map[string]string{
									"$link": "bafkreiattackerblob",
								},
								"mimeType": "image/png",
								"size":     int64(len([]byte("fixture-png-15"))),
							},
							"aspectRatio": map[string]int{
								"width": 10, "height": 10,
							},
						}},
					},
				}),
			}),
		}, nil
	}), nil)
	_, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: mustJSON(t, payload), Checkpoint: mustJSON(t, state),
		IdempotencyKey: key,
	})
	var providerErr *publishing.ProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "invalid_bluesky_reconciliation" {
		t.Fatalf("error=%v", err)
	}
}

func validMastodonCapabilities() mastodonCapabilities {
	return mastodonCapabilities{
		MaxCharacters: 500, CharactersReservedPerURL: 23,
		MaxAttachments: 4, DescriptionLimit: 1500,
		MIMETypes:  []string{"image/png", "video/mp4"},
		ImageBytes: 1 << 20, ImageMatrixLimit: 1 << 20,
		VideoBytes: 1 << 20, VideoMatrixLimit: 1 << 20,
		VideoFrameRateLimit: 60,
	}
}

func assertRetryableProviderError(t *testing.T, err error) {
	t.Helper()
	var providerErr *publishing.ProviderError
	if !errors.As(err, &providerErr) || !providerErr.Retryable ||
		providerErr.Ambiguous {
		t.Fatalf("error=%v", err)
	}
}

func tidTimestampMicros(t *testing.T, value string) int64 {
	t.Helper()
	const alphabet = "234567abcdefghijklmnopqrstuvwxyz"
	var decoded uint64
	for _, character := range value {
		index := strings.IndexRune(alphabet, character)
		if index < 0 {
			t.Fatalf("invalid TID %q", value)
		}
		decoded = decoded<<5 | uint64(index)
	}
	return int64(decoded >> 10)
}

func TestBlueskyFormerTIDCollisionPublishesDistinctRecordsOnce(t *testing.T) {
	var (
		mu      sync.Mutex
		creates = make(map[string]int)
	)
	executor := executorFunc(func(
		_ context.Context,
		request socialconnections.PublishingRequest,
	) (socialconnections.PublishingResponse, error) {
		if request.Method != http.MethodPost ||
			request.Path != blueskyCreatePath {
			t.Fatalf("unexpected provider request %s %s",
				request.Method, request.Path)
		}
		var input struct {
			Repository string `json:"repo"`
			RKey       string `json:"rkey"`
		}
		if json.Unmarshal(request.Body, &input) != nil {
			t.Fatal("invalid createRecord body")
		}
		uri := blueskyURI(input.Repository, input.RKey)
		mu.Lock()
		creates[uri]++
		mu.Unlock()
		return socialconnections.PublishingResponse{
			StatusCode: http.StatusOK,
			Body: mustJSON(t, blueskyRecordEnvelope{
				URI: uri, CID: "bafyreicollisionfixture",
			}),
		}, nil
	})
	adapter := newBlueskyForTest(executor, nil)
	keys := []string{
		"publish_c89c98f88e44e5bbd531ed13d967f34619c5fc0e6fe4711c7c517f3b2473308f",
		"publish_8292a09311bfe2e4ca21ac76822f570371c77db6c8cb034fe53189d0e473308f",
	}
	requests := make([]publishing.PublishRequest, len(keys))
	for index, key := range keys {
		requests[index] = publishing.PublishRequest{
			WorkspaceID: "workspace", ConnectionID: "connection",
			Payload: mustJSON(t, blueskyPayload{
				Repository: "did:plc:fixture123",
				Text:       fmt.Sprintf("collision fixture %d", index),
				CreatedAt:  "2026-07-30T12:00:00Z",
			}),
			IdempotencyKey: key,
		}
		progress, err := adapter.Publish(context.Background(), requests[index])
		if err != nil {
			t.Fatal(err)
		}
		requests[index].Checkpoint = progress.Checkpoint
	}
	var group sync.WaitGroup
	results := make(chan publishing.PublishResult, len(requests))
	failures := make(chan error, len(requests))
	for _, request := range requests {
		group.Add(1)
		go func(request publishing.PublishRequest) {
			defer group.Done()
			result, err := adapter.Publish(context.Background(), request)
			results <- result
			failures <- err
		}(request)
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	remoteIDs := make(map[string]struct{}, len(requests))
	for result := range results {
		if !result.Complete {
			t.Fatalf("incomplete result=%+v", result)
		}
		remoteIDs[result.RemoteID] = struct{}{}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(remoteIDs) != 2 || len(creates) != 2 {
		t.Fatalf("remote IDs=%v creates=%v", remoteIDs, creates)
	}
	for uri, count := range creates {
		if count != 1 {
			t.Fatalf("URI %q created %d times", uri, count)
		}
	}
}

func TestBlueskyTIDAllocationSurvivesCrashBeforeCheckpoint(t *testing.T) {
	allocator := publishing.NewMemoryStore()
	var creates atomic.Int32
	executor := executorFunc(func(
		_ context.Context,
		request socialconnections.PublishingRequest,
	) (socialconnections.PublishingResponse, error) {
		creates.Add(1)
		var input struct {
			Repository string `json:"repo"`
			RKey       string `json:"rkey"`
		}
		if json.Unmarshal(request.Body, &input) != nil {
			return socialconnections.PublishingResponse{},
				errors.New("invalid createRecord body")
		}
		return socialconnections.PublishingResponse{
			StatusCode: http.StatusOK,
			Body: mustJSON(t, blueskyRecordEnvelope{
				URI: blueskyURI(input.Repository, input.RKey),
				CID: "bafyreicrashallocation",
			}),
		}, nil
	})
	request := publishing.PublishRequest{
		WorkspaceID: "workspace", ConnectionID: "connection",
		Payload: mustJSON(t, blueskyPayload{
			Repository: "did:plc:fixture123",
			Text:       "crash after allocation",
			CreatedAt:  "2026-07-30T12:00:00Z",
		}),
		IdempotencyKey: "publish_" + strings.Repeat("7", 64),
	}
	firstProcess := newBlueskyForTest(executor, nil)
	firstProcess.allocator = allocator
	beforeCrash, err := firstProcess.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 0 {
		t.Fatal("createRecord ran before the allocated TID checkpoint")
	}

	secondProcess := newBlueskyForTest(executor, nil)
	secondProcess.allocator = allocator
	afterRestart, err := secondProcess.Publish(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeCrash.Checkpoint, afterRestart.Checkpoint) {
		t.Fatalf("checkpoint changed after restart: %s != %s",
			beforeCrash.Checkpoint, afterRestart.Checkpoint)
	}
	request.Checkpoint = afterRestart.Checkpoint
	complete, err := secondProcess.Publish(context.Background(), request)
	if err != nil || !complete.Complete || creates.Load() != 1 {
		t.Fatalf("complete=%+v creates=%d error=%v",
			complete, creates.Load(), err)
	}
}
