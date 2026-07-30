package dynamic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
					Repository string `json:"repo"`
					Collection string `json:"collection"`
					RKey       string `json:"rkey"`
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
					Repository string `json:"repo"`
					RKey       string `json:"rkey"`
				}
				_ = json.NewDecoder(request.Body).Decode(&input)
				uri := blueskyURI(input.Repository, input.RKey)
				mu.Lock()
				record, exists := records[uri]
				if !exists {
					record = blueskyRecordEnvelope{
						URI: uri,
						CID: "bafyreifixturerecord",
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
					MaxCharacters: 500, MaxAttachments: 4,
					MIMETypes:  []string{"image/png"},
					ImageBytes: 1 << 20, VideoBytes: 1 << 20,
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
	value, err := blueskyRKey(idempotencyKey, createdAt)
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
	key := "publish_" + strings.Repeat("e", 64)
	first := mustBlueskyRKey(t, key, "2026-07-30T12:00:00Z")
	second := mustBlueskyRKey(t, key, "2026-07-30T12:00:00Z")
	if first != second || !atTIDPattern.MatchString(first) ||
		len(first) != 13 {
		t.Fatalf("deterministic TID first=%q second=%q", first, second)
	}
	different := mustBlueskyRKey(
		t,
		"publish_"+strings.Repeat("f", 64),
		"2026-07-30T12:00:00Z",
	)
	if different == first {
		t.Fatalf("different idempotency keys produced TID %q", first)
	}
}
