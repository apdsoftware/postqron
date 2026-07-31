package socialconnections

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gopkg.in/yaml.v3"
)

type executorTransport struct {
	calls  atomic.Int32
	pins   atomic.Int32
	mu     sync.Mutex
	origin string
	pinErr error
	do     func(*http.Request) (*http.Response, error)
}

func (transport *executorTransport) PinOrigin(
	_ context.Context,
	origin string,
) error {
	transport.mu.Lock()
	transport.origin = origin
	transport.mu.Unlock()
	transport.pins.Add(1)
	return transport.pinErr
}

func (transport *executorTransport) pinnedOrigin() string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.origin
}

func (transport *executorTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	transport.calls.Add(1)
	return transport.do(request)
}

type trackedReadCloser struct {
	io.Reader
	closed atomic.Bool
}

type fixedResponseClassifier struct {
	classification ProviderResponseClassification
	status         int
}

type capturingResponseClassifier struct {
	evidence ProviderResponseEvidence
}

func (classifier *capturingResponseClassifier) ClassifyProviderResponse(
	evidence ProviderResponseEvidence,
) (ProviderResponseClassification, bool) {
	classifier.evidence = evidence
	return ProviderResponseClassification{
		Kind: ExecutorFailurePermanent,
	}, true
}

func (classifier fixedResponseClassifier) ClassifyProviderResponse(
	evidence ProviderResponseEvidence,
) (ProviderResponseClassification, bool) {
	return classifier.classification, evidence.StatusCode == classifier.status
}

type failingCompleteSessionRepository struct {
	Repository
	fail atomic.Bool
}

type failingMarkReconnectRepository struct {
	Repository
}

func (repository *failingMarkReconnectRepository) MarkReconnectRequired(
	context.Context,
	ReconnectCommand,
) (Connection, bool, error) {
	return Connection{}, false, errors.New("injected MarkReconnectRequired failure")
}

func (repository *failingCompleteSessionRepository) CompleteSession(
	ctx context.Context,
	command SessionCommand,
) (Connection, error) {
	if repository.fail.Load() {
		return Connection{}, errors.New("injected CompleteSession failure")
	}
	return repository.Repository.CompleteSession(ctx, command)
}

func (reader *trackedReadCloser) Close() error {
	reader.closed.Store(true)
	return nil
}

// This method exists only in F5 tests: the publishing-facing contract remains
// AuthenticatedExecutor.Execute and never receives DynamicSession.
func (adapter *fakeDynamicAdapter) doAuthenticatedStream(
	ctx context.Context,
	session DynamicSession,
	request streamingAuthenticatedRequest,
) (DynamicAuthenticatedResult, error) {
	if _, err := io.Copy(io.Discard, request.Body); err != nil {
		return DynamicAuthenticatedResult{}, err
	}
	return adapter.DoAuthenticated(ctx, session, AuthenticatedRequest{
		Method: request.Method,
		Path:   request.Path,
		Header: request.Header,
	})
}

func TestAuthenticatedExecutorRejectsUnsafeInputBeforeTransport(t *testing.T) {
	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	transport := &executorTransport{do: func(*http.Request) (*http.Response, error) {
		t.Fatal("transport must not be called")
		return nil, nil
	}}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service:   fixture.service,
		Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderInstagramProfessional: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []PublishingRequest{
		{WorkspaceID: "wrong", ConnectionID: "missing", ExpectedProvider: ProviderInstagramProfessional, Method: "POST", Path: "/posts"},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, ExpectedProvider: ProviderX, Method: "POST", Path: "/posts"},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, ExpectedProvider: ProviderInstagramProfessional, Method: "POST", Path: "https://evil.example/x"},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, ExpectedProvider: ProviderInstagramProfessional, Method: "POST", Path: "/../admin"},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, ExpectedProvider: ProviderInstagramProfessional, Method: "POST", Path: "/posts", Header: http.Header{"Authorization": {"stolen"}}},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, ExpectedProvider: ProviderInstagramProfessional, Method: "POST", Path: "/posts", Header: http.Header{"X-Api-Key": {"stolen"}}},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, ExpectedProvider: ProviderInstagramProfessional, Method: "POST", Path: "/posts", Header: http.Header{"Connection": {"keep-alive"}}},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, ExpectedProvider: ProviderInstagramProfessional, Method: "POST", Path: "/posts", Header: http.Header{"X-Test": {"ok\r\nAuthorization: stolen"}}},
	} {
		_, _ = executor.Execute(context.Background(), request)
	}
	if transport.calls.Load() != 0 {
		t.Fatalf("transport calls = %d", transport.calls.Load())
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID: "workspace-1", ConnectionID: connection.ID,
		Method: http.MethodGet, Path: "/me",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing ExpectedProvider error = %v", err)
	}
}

func TestAuthenticatedExecutorRejectsNonCanonicalProviderPaths(t *testing.T) {
	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	transport := &executorTransport{do: func(*http.Request) (*http.Response, error) {
		t.Fatal("transport must not be called")
		return nil, nil
	}}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service:   fixture.service,
		Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderInstagramProfessional: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, providerPath := range []string{
		"https://evil.example/path/",
		"https://user:password@evil.example/path/",
		"//evil.example/path/",
		"/creator_info//query/",
		"/creator_info/query//",
		"/creator_info/./query/",
		"/creator_info/../query/",
		"/creator_info/%2e%2e/query/",
		"/creator_info/%252e%252e/query/",
		"/creator_info/%2fquery/",
		"/creator_info/%5cquery/",
		"/creator_info/%00query/",
		`/creator_info\query/`,
	} {
		t.Run(providerPath, func(t *testing.T) {
			_, executeErr := executor.Execute(
				context.Background(),
				PublishingRequest{
					WorkspaceID:      "workspace-1",
					ConnectionID:     connection.ID,
					ExpectedProvider: ProviderInstagramProfessional,
					Method:           http.MethodGet,
					Path:             providerPath,
				},
			)
			if !errors.Is(executeErr, ErrInvalidArgument) {
				t.Fatalf("path %q error = %v", providerPath, executeErr)
			}
		})
	}
	if transport.calls.Load() != 0 {
		t.Fatalf("transport calls = %d", transport.calls.Load())
	}
}

func TestAuthenticatedExecutorPreservesTrailingSlashOverTLS(t *testing.T) {
	const officialPath = "/v2/post/publish/creator_info/query/"
	received := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			received <- request.URL.RequestURI()
			if request.Header.Get("Authorization") != "Bearer instagram-token" {
				t.Error("authorization was not derived inside F5")
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ok":true}`))
		},
	))
	defer server.Close()

	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		forward, err := http.NewRequestWithContext(
			request.Context(),
			request.Method,
			server.URL+request.URL.RequestURI(),
			request.Body,
		)
		if err != nil {
			return nil, err
		}
		forward.Header = request.Header.Clone()
		return server.Client().Do(forward)
	}}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service:   fixture.service,
		Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderInstagramProfessional: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     connection.ID,
		ExpectedProvider: ProviderInstagramProfessional,
		Method:           http.MethodGet,
		Path:             officialPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-received; got != officialPath {
		t.Fatalf("TLS fixture path = %q, want %q", got, officialPath)
	}
	if transport.pinnedOrigin() != "https://graph.example.com" {
		t.Fatalf("pinned origin = %q", transport.pinnedOrigin())
	}
}

func TestAuthenticatedExecutorPreservesTrailingSlashInDynamicSession(
	t *testing.T,
) {
	const officialPath = "/xrpc/app.example.creator_info/query/"
	fixture := newDynamicServiceFixture(t)
	executor, err := NewAuthenticatedExecutor(
		AuthenticatedExecutorConfig{Service: fixture.service},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     fixture.connectionID,
		ExpectedProvider: ProviderBluesky,
		Method:           http.MethodGet,
		Path:             officialPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.adapter.mu.Lock()
	defer fixture.adapter.mu.Unlock()
	if len(fixture.adapter.requestPaths) != 1 ||
		fixture.adapter.requestPaths[0] != officialPath {
		t.Fatalf("dynamic paths = %#v", fixture.adapter.requestPaths)
	}
	if len(fixture.adapter.requestStates) != 1 ||
		string(fixture.adapter.requestStates[0]) !=
			"nonce-as-1|nonce-rs-1|dpop-key" {
		t.Fatal("dynamic request did not use the real bound session")
	}
}

func TestAuthenticatedExecutorSnapshotsMutableRequestBeforeTransport(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	body := []byte("immutable-media")
	sum := sha256.Sum256(body)
	mediaBody := &trackedReadCloser{Reader: bytes.NewReader(body)}
	entered := make(chan struct{})
	release := make(chan struct{})
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		close(entered)
		<-release
		if request.Header.Get("Content-Type") != "video/mp4" ||
			request.ContentLength != int64(len(body)) {
			t.Errorf(
				"transport snapshot = header %q size %d",
				request.Header.Get("Content-Type"),
				request.ContentLength,
			)
		}
		if _, readErr := io.Copy(io.Discard, request.Body); readErr != nil {
			return nil, readErr
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	}}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service, Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderInstagramProfessional: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     connection.ID,
		ExpectedProvider: ProviderInstagramProfessional,
		Method:           http.MethodPost,
		Path:             "/media",
		Header:           http.Header{"Content-Type": {"video/mp4"}},
		Media: &PublishingMedia{
			Body: mediaBody, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		},
	}
	done := make(chan error, 1)
	go func() {
		_, executeErr := executor.Execute(context.Background(), request)
		done <- executeErr
	}()
	<-entered
	request.Header.Set("Content-Type", "text/plain")
	request.Media.Size = 1
	request.Media.SHA256 = strings.Repeat("0", sha256.Size*2)
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if !mediaBody.closed.Load() {
		t.Fatal("snapshotted media was not closed")
	}
}

func TestAuthenticatedExecutorRequiresAndAppliesDNSPinBeforeCredentialUse(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	if _, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service,
		ResourceServers: map[Provider]string{
			ProviderInstagramProfessional: "https://graph.example.com",
		},
	}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("missing pinning transport error = %v", err)
	}
	expired := serviceTestNow.Add(-time.Minute)
	fixture.instagram.resources = []DiscoveredResource{instagramResource(
		"ig-1",
		"postqron",
		"expired-token",
		&expired,
	)}
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	transport := &executorTransport{
		pinErr: errors.New("DNS pin rejected private address"),
		do: func(*http.Request) (*http.Response, error) {
			t.Fatal("transport must not run after pin failure")
			return nil, nil
		},
	}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service, Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderInstagramProfessional: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     connection.ID,
		ExpectedProvider: ProviderInstagramProfessional,
		Method:           http.MethodGet,
		Path:             "/me",
	})
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Code != "provider_dns_pin_failed" {
		t.Fatalf("pin failure = %#v", err)
	}
	refreshCalls, _, _ := fixture.instagram.counts()
	if transport.pins.Load() != 1 ||
		transport.calls.Load() != 0 ||
		refreshCalls != 0 {
		t.Fatalf(
			"pin ordering: pins=%d transports=%d refreshes=%d",
			transport.pins.Load(),
			transport.calls.Load(),
			refreshCalls,
		)
	}
}

func TestAuthenticatedExecutorStreamsVerifiesClosesAndSanitizes(t *testing.T) {
	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	body := []byte("streamed-media")
	sum := sha256.Sum256(body)
	mediaBody := &trackedReadCloser{Reader: bytes.NewReader(body)}
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://graph.example.com/media" {
			t.Fatalf("target = %q", request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer instagram-token" {
			t.Fatalf("authorization was not derived inside F5")
		}
		got, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			return nil, readErr
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("media body = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Authorization": {"Bearer response-secret"},
				"Set-Cookie":    {"secret=session"},
				"X-Safe":        {"yes"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"remote-1"}`)),
		}, nil
	}}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service:   fixture.service,
		Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderInstagramProfessional: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     connection.ID,
		ExpectedProvider: ProviderInstagramProfessional,
		Method:           http.MethodPost,
		Path:             "/media",
		Media: &PublishingMedia{
			Body:   mediaBody,
			Size:   int64(len(body)),
			SHA256: hex.EncodeToString(sum[:]),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mediaBody.closed.Load() {
		t.Fatal("media was not closed")
	}
	if response.Header.Get("Authorization") != "" ||
		response.Header.Get("Set-Cookie") != "" ||
		response.Header.Get("X-Safe") != "" {
		t.Fatalf("sanitized headers = %#v", response.Header)
	}
	if bytes.Contains(response.Body, []byte("instagram-token")) {
		t.Fatal("access token escaped through response")
	}
	if transport.pins.Load() != 1 ||
		transport.pinnedOrigin() != "https://graph.example.com" {
		t.Fatalf(
			"DNS pin calls = %d origin %q",
			transport.pins.Load(),
			transport.pinnedOrigin(),
		)
	}

	badBody := &trackedReadCloser{Reader: bytes.NewReader(body)}
	bad := sha256.Sum256([]byte("other"))
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID: "workspace-1", ConnectionID: connection.ID,
		ExpectedProvider: ProviderInstagramProfessional,
		Method:           http.MethodPost, Path: "/media",
		Media: &PublishingMedia{
			Body: badBody, Size: int64(len(body)), SHA256: hex.EncodeToString(bad[:]),
		},
	})
	var failure *ExecutorFailure
	if !errors.As(err, &failure) || failure.Kind != ExecutorFailureAmbiguous {
		t.Fatalf("digest failure = %#v", err)
	}
	if !badBody.closed.Load() {
		t.Fatal("invalid media was not closed")
	}
}

func TestAuthenticatedExecutorClassifiesResponsesByMethodAndF5Classifier(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderFacebookPages,
		"page-1",
	)
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service,
		Classifiers: map[Provider]ProviderResponseClassifier{
			ProviderFacebookPages: fixedResponseClassifier{
				status: http.StatusNotFound,
				classification: ProviderResponseClassification{
					Kind:      ExecutorFailureReconnect,
					Reconnect: true,
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method string
		status int
		kind   ExecutorFailureKind
	}{
		{method: http.MethodGet, status: http.StatusBadGateway, kind: ExecutorFailureTemporary},
		{method: http.MethodHead, status: http.StatusServiceUnavailable, kind: ExecutorFailureTemporary},
		{method: http.MethodPost, status: http.StatusBadGateway, kind: ExecutorFailureAmbiguous},
		{method: http.MethodPut, status: http.StatusServiceUnavailable, kind: ExecutorFailureAmbiguous},
		{method: http.MethodPatch, status: http.StatusInternalServerError, kind: ExecutorFailureAmbiguous},
		{method: http.MethodDelete, status: http.StatusBadGateway, kind: ExecutorFailureAmbiguous},
		{method: http.MethodGet, status: http.StatusNotFound, kind: ExecutorFailureReconnect},
	}
	for _, test := range tests {
		_, finishErr := executor.finishResponse(
			context.Background(),
			stored,
			test.method,
			AuthenticatedResponse{
				StatusCode: test.status,
				Header:     make(http.Header),
			},
		)
		var failure *ExecutorFailure
		if !errors.As(finishErr, &failure) || failure.Kind != test.kind {
			t.Fatalf(
				"%s status %d failure = %#v, want %s",
				test.method,
				test.status,
				finishErr,
				test.kind,
			)
		}
	}
}

func TestAuthenticatedExecutorMutative5xxCannotBeDowngradedByClassifier(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderFacebookPages,
		"page-1",
	)
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	classifier := &capturingResponseClassifier{}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service,
		Classifiers: map[Provider]ProviderResponseClassifier{
			ProviderFacebookPages: classifier,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.finishResponse(
		context.Background(),
		stored,
		http.MethodPost,
		AuthenticatedResponse{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Retry-After": {"3"}},
			Body:       []byte(`{"safe_code":"temporary"}`),
		},
	)
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Kind != ExecutorFailureAmbiguous ||
		failure.RetryAfter != 3*time.Second {
		t.Fatalf("POST 503 floor failure = %#v", err)
	}
	if classifier.evidence.StatusCode != 0 {
		t.Fatalf("classifier ran before mutative 5xx floor: %#v", classifier.evidence)
	}
}

func TestProviderResponseClassifierReceivesOnlySanitizedEvidence(t *testing.T) {
	fixture := newServiceFixture(t)
	connection := connectResource(
		t,
		fixture.service,
		ProviderFacebookPages,
		"page-1",
	)
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		connection.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	classifier := &capturingResponseClassifier{}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service,
		Classifiers: map[Provider]ProviderResponseClassifier{
			ProviderFacebookPages: classifier,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.finishResponse(
		context.Background(),
		stored,
		http.MethodGet,
		AuthenticatedResponse{
			StatusCode: http.StatusBadRequest,
			Header: http.Header{
				"Authorization": {"Bearer secret"},
				"DPoP-Nonce":    {"secret-nonce"},
				"Set-Cookie":    {"secret=session"},
				"Retry-After":   {"12"},
				"X-Diagnostic":  {"raw-provider-diagnostic"},
			},
			Body: []byte(`{"safe_code":"invalid_media"}`),
		},
		"access-token",
		"dpop-private-state",
	)
	if err == nil {
		t.Fatal("classified provider failure is required")
	}
	if classifier.evidence.StatusCode != http.StatusBadRequest ||
		classifier.evidence.Method != http.MethodGet ||
		classifier.evidence.Header.Get("Retry-After") != "12" ||
		len(classifier.evidence.Header) != 1 ||
		!bytes.Equal(
			classifier.evidence.Body,
			[]byte(`{"safe_code":"invalid_media"}`),
		) {
		t.Fatalf("classifier evidence = %#v", classifier.evidence)
	}
}

func TestAuthenticatedExecutorPersistsReconnectBeforeReturning(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		classifier ProviderResponseClassifier
	}{
		{name: "401 default", status: http.StatusUnauthorized},
		{
			name:   "404 provider classifier",
			status: http.StatusNotFound,
			classifier: fixedResponseClassifier{
				status: http.StatusNotFound,
				classification: ProviderResponseClassification{
					Kind:      ExecutorFailureReconnect,
					Reconnect: true,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			connection := connectResource(
				t,
				fixture.service,
				ProviderFacebookPages,
				"page-1",
			)
			transport := &executorTransport{do: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: test.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":"redacted"}`)),
				}, nil
			}}
			classifiers := map[Provider]ProviderResponseClassifier{}
			if test.classifier != nil {
				classifiers[ProviderFacebookPages] = test.classifier
			}
			executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
				Service: fixture.service, Transport: transport,
				ResourceServers: map[Provider]string{
					ProviderFacebookPages: "https://graph.example.com",
				},
				Classifiers: classifiers,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := PublishingRequest{
				WorkspaceID:      "workspace-1",
				ConnectionID:     connection.ID,
				ExpectedProvider: ProviderFacebookPages,
				Method:           http.MethodPost,
				Path:             "/posts",
			}
			_, err = executor.Execute(context.Background(), request)
			var failure *ExecutorFailure
			if !errors.As(err, &failure) ||
				failure.Kind != ExecutorFailureReconnect ||
				!failure.Reconnect {
				t.Fatalf("first reconnect failure = %#v", err)
			}
			pins := transport.pins.Load()
			_, err = executor.Execute(context.Background(), request)
			if !errors.As(err, &failure) ||
				failure.Kind != ExecutorFailureReconnect {
				t.Fatalf("second reconnect failure = %#v", err)
			}
			if transport.calls.Load() != 1 || transport.pins.Load() != pins {
				t.Fatalf(
					"reconnected request reached pin/transport: pins=%d calls=%d",
					transport.pins.Load(),
					transport.calls.Load(),
				)
			}
			stored, getErr := fixture.repository.GetCredential(
				context.Background(),
				"workspace-1",
				connection.ID,
			)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if stored.Status != StatusReconnectRequired {
				t.Fatalf("reconnect was not persisted: %#v", stored)
			}
		})
	}
}

func TestAuthenticatedExecutorDynamicResponseRejectsDPoPStateLeak(t *testing.T) {
	fixture := newDynamicServiceFixture(t)
	fixture.adapter.requestResult.Response.Body = []byte(`{"key":"dpop-key"}`)
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     fixture.connectionID,
		ExpectedProvider: ProviderBluesky,
		Method:           http.MethodGet,
		Path:             "/xrpc/state-leak",
	})
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Code != "provider_response_redacted" {
		t.Fatalf("DPoP state leak failure = %#v", err)
	}
}

func TestAuthenticatedExecutorFinalizesPartiallyConsumedMediaAsAmbiguous(
	t *testing.T,
) {
	fixture := newServiceFixture(t)
	connection := connectResource(t, fixture.service, ProviderFacebookPages, "page-1")
	body := []byte("partial-stream")
	sum := sha256.Sum256(body)
	mediaBody := &trackedReadCloser{Reader: bytes.NewReader(body)}
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		buffer := make([]byte, 3)
		_, _ = request.Body.Read(buffer)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	}}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service, Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderFacebookPages: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID: "workspace-1", ConnectionID: connection.ID,
		ExpectedProvider: ProviderFacebookPages,
		Method:           http.MethodPost, Path: "/media",
		Media: &PublishingMedia{
			Body: mediaBody, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		},
	})
	var failure *ExecutorFailure
	if !errors.As(err, &failure) || failure.Kind != ExecutorFailureAmbiguous {
		t.Fatalf("partial send failure = %#v", err)
	}
	if !mediaBody.closed.Load() {
		t.Fatal("partially consumed media was not closed")
	}
}

func TestAuthenticatedExecutorRejectsRedirectWithoutFollowingIt(t *testing.T) {
	fixture := newServiceFixture(t)
	connection := connectResource(t, fixture.service, ProviderFacebookPages, "page-1")
	transport := &executorTransport{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Location": {"https://evil.example/credential-capture"},
			},
			Body: io.NopCloser(strings.NewReader("redirect")),
		}, nil
	}}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service, Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderFacebookPages: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID: "workspace-1", ConnectionID: connection.ID,
		ExpectedProvider: ProviderFacebookPages,
		Method:           http.MethodPost, Path: "/posts",
	})
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Kind != ExecutorFailurePermanent ||
		failure.Code != "provider_redirect_rejected" {
		t.Fatalf("redirect failure = %#v", err)
	}
	if transport.calls.Load() != 1 {
		t.Fatalf("redirect transport calls = %d, want 1", transport.calls.Load())
	}
}

func TestAuthenticatedExecutorDynamicStreamingUsesSessionLease(t *testing.T) {
	fixture := newDynamicServiceFixture(t)
	fixture.adapter.requestEntered = make(chan struct{}, 1)
	fixture.adapter.requestRelease = make(chan struct{})
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service,
	})
	if err != nil {
		t.Fatal(err)
	}
	newRequest := func(path string) PublishingRequest {
		body := []byte("dynamic-media")
		sum := sha256.Sum256(body)
		return PublishingRequest{
			WorkspaceID:      "workspace-1",
			ConnectionID:     fixture.connectionID,
			ExpectedProvider: ProviderBluesky,
			Method:           http.MethodPost,
			Path:             path,
			Media: &PublishingMedia{
				Body:   &trackedReadCloser{Reader: bytes.NewReader(body)},
				Size:   int64(len(body)),
				SHA256: hex.EncodeToString(sum[:]),
			},
		}
	}
	firstDone := make(chan error, 1)
	go func() {
		_, executeErr := executor.Execute(
			context.Background(),
			newRequest("/xrpc/media.first"),
		)
		firstDone <- executeErr
	}()
	<-fixture.adapter.requestEntered
	_, concurrentErr := executor.Execute(
		context.Background(),
		newRequest("/xrpc/media.concurrent"),
	)
	var failure *ExecutorFailure
	if !errors.As(concurrentErr, &failure) ||
		failure.Kind != ExecutorFailureTemporary {
		t.Fatalf("concurrent stream failure = %#v", concurrentErr)
	}
	close(fixture.adapter.requestRelease)
	if err = <-firstDone; err != nil {
		t.Fatal(err)
	}
	_, calls := fixture.adapter.counts()
	if calls != 1 {
		t.Fatalf("dynamic streaming calls = %d, want 1", calls)
	}
}

func TestAuthenticatedExecutorDynamicPostTransportFailuresAreAmbiguousAndFailClosed(
	t *testing.T,
) {
	tests := []struct {
		name   string
		inject func(*dynamicServiceFixture)
	}{
		{
			name: "invalid updated session",
			inject: func(fixture *dynamicServiceFixture) {
				fixture.adapter.requestResult.Session.Binding = OAuthBinding{
					Issuer:         "https://other-auth.example.com",
					ResourceServer: "https://other-pds.example.com",
					Subject:        dynamicTestDID,
				}
			},
		},
		{
			name: "oversized provider response",
			inject: func(fixture *dynamicServiceFixture) {
				fixture.adapter.requestResult.Response.Body = make(
					[]byte,
					maximumAuthenticatedBody+1,
				)
			},
		},
		{
			name: "CompleteSession failure",
			inject: func(fixture *dynamicServiceFixture) {
				repository := &failingCompleteSessionRepository{
					Repository: fixture.repository,
				}
				repository.fail.Store(true)
				fixture.service.repository = repository
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDynamicServiceFixture(t)
			test.inject(&fixture)
			executor, err := NewAuthenticatedExecutor(
				AuthenticatedExecutorConfig{Service: fixture.service},
			)
			if err != nil {
				t.Fatal(err)
			}
			body := []byte("dynamic-failure-media")
			sum := sha256.Sum256(body)
			_, err = executor.Execute(context.Background(), PublishingRequest{
				WorkspaceID:      "workspace-1",
				ConnectionID:     fixture.connectionID,
				ExpectedProvider: ProviderBluesky,
				Method:           http.MethodPost,
				Path:             "/xrpc/media.failure",
				Media: &PublishingMedia{
					Body: io.NopCloser(bytes.NewReader(body)),
					Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
				},
			})
			var failure *ExecutorFailure
			if !errors.As(err, &failure) ||
				failure.Kind != ExecutorFailureAmbiguous ||
				!failure.Reconnect {
				t.Fatalf("post-transport failure = %#v", err)
			}
			stored, getErr := fixture.repository.GetCredential(
				context.Background(),
				"workspace-1",
				fixture.connectionID,
			)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if stored.Status != StatusReconnectRequired {
				t.Fatalf("connection was not failed closed: %#v", stored)
			}
			_, calls := fixture.adapter.counts()
			if calls != 1 {
				t.Fatalf("provider calls = %d, want 1", calls)
			}
		})
	}
}

func TestAuthenticatedExecutorDynamicJSONCompleteFailureIsAmbiguousAndFailClosed(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	repository := &failingCompleteSessionRepository{
		Repository: fixture.repository,
	}
	repository.fail.Store(true)
	fixture.service.repository = repository
	executor, err := NewAuthenticatedExecutor(
		AuthenticatedExecutorConfig{Service: fixture.service},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     fixture.connectionID,
		ExpectedProvider: ProviderBluesky,
		Method:           http.MethodPost,
		Path:             "/xrpc/json.complete-failure",
		Header:           http.Header{"Content-Type": {"application/json"}},
		Body:             []byte(`{"text":"hello"}`),
	})
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Kind != ExecutorFailureAmbiguous ||
		!failure.Reconnect {
		t.Fatalf("dynamic JSON failure = %#v", err)
	}
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusReconnectRequired {
		t.Fatalf("dynamic JSON failure was not failed closed: %#v", stored)
	}
	_, calls := fixture.adapter.counts()
	if calls != 1 {
		t.Fatalf("dynamic JSON provider calls = %d, want 1", calls)
	}
}

func TestAuthenticatedExecutorMarkReconnectFailureRetainsDynamicLease(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	completeFailure := &failingCompleteSessionRepository{
		Repository: fixture.repository,
	}
	completeFailure.fail.Store(true)
	fixture.service.repository = &failingMarkReconnectRepository{
		Repository: completeFailure,
	}
	executor, err := NewAuthenticatedExecutor(
		AuthenticatedExecutorConfig{Service: fixture.service},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     fixture.connectionID,
		ExpectedProvider: ProviderBluesky,
		Method:           http.MethodPost,
		Path:             "/xrpc/json.mark-failure",
		Header:           http.Header{"Content-Type": {"application/json"}},
		Body:             []byte(`{"text":"hello"}`),
	}
	_, err = executor.Execute(context.Background(), request)
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Kind != ExecutorFailurePermanent ||
		failure.Code != "reconnect_persistence_failed" ||
		!failure.Reconnect {
		t.Fatalf("mark reconnect failure = %#v", err)
	}
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusConnected ||
		stored.SessionLeaseID == "" ||
		stored.SessionLockedUntil == nil {
		t.Fatalf("failed reconnect did not retain lease: %#v", stored)
	}
	_, err = executor.Execute(context.Background(), request)
	if !errors.As(err, &failure) ||
		failure.Kind != ExecutorFailureTemporary {
		t.Fatalf("second fail-closed request = %#v", err)
	}
	_, calls := fixture.adapter.counts()
	if calls != 1 {
		t.Fatalf("provider replayed after MarkReconnect failure: %d calls", calls)
	}
}

func TestAuthenticatedExecutorDynamicRedactionIncludesPreRotationState(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	fixture.adapter.requestResult.Session.ProviderState = []byte(
		"nonce-as-1|nonce-rs-2|dpop-key-rotated",
	)
	fixture.adapter.requestResult.Response.Body = []byte(
		`{"echo":"nonce-rs-1"}`,
	)
	executor, err := NewAuthenticatedExecutor(
		AuthenticatedExecutorConfig{Service: fixture.service},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     fixture.connectionID,
		ExpectedProvider: ProviderBluesky,
		Method:           http.MethodGet,
		Path:             "/xrpc/state.rotate",
	})
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Code != "provider_response_redacted" {
		t.Fatalf("pre-rotation state echo failure = %#v", err)
	}
}

func TestAuthenticatedExecutorDynamicStreamDelegatesAndPersistsNonceState(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	rotatedState := []byte("nonce-as-1|nonce-rs-9|dpop-key-rotated")
	fixture.adapter.requestResult.Session.ProviderState = rotatedState
	executor, err := NewAuthenticatedExecutor(
		AuthenticatedExecutorConfig{Service: fixture.service},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("nonce-media")
	sum := sha256.Sum256(body)
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     fixture.connectionID,
		ExpectedProvider: ProviderBluesky,
		Method:           http.MethodPost,
		Path:             "/xrpc/media.nonce",
		Media: &PublishingMedia{
			Body: io.NopCloser(bytes.NewReader(body)),
			Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.adapter.mu.Lock()
	requestState := append([]byte(nil), fixture.adapter.requestStates[0]...)
	fixture.adapter.mu.Unlock()
	if string(requestState) != "nonce-as-1|nonce-rs-1|dpop-key" {
		t.Fatalf("adapter did not receive the bound DPoP state: %q", requestState)
	}
	stored, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	session, err := fixture.service.openDynamicSession(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(session.ProviderState, rotatedState) {
		t.Fatalf("persisted nonce state = %q", session.ProviderState)
	}
}

func TestAuthenticatedExecutorSingleUseRefreshCrashFailsClosedBeforeStreaming(
	t *testing.T,
) {
	fixture := newDynamicServiceFixture(t)
	expired := fixture.now.Add(-time.Minute)
	fixture.repository.mu.Lock()
	stored := fixture.repository.connections[fixture.connectionID]
	stored.TokenExpiresAt = &expired
	fixture.repository.connections[fixture.connectionID] = stored
	fixture.repository.mu.Unlock()
	refreshedExpiry := fixture.now.Add(time.Hour)
	fixture.adapter.refreshResult = DynamicRefreshResult{
		Session: DynamicSession{
			Binding: stored.Binding,
			Credential: Credential{
				AccessToken:  "access-2",
				RefreshToken: "refresh-2",
				ExpiresAt:    &refreshedExpiry,
				Scopes:       []string{"atproto"},
			},
			ProviderState: []byte("nonce-as-2|nonce-rs-2|dpop-key-2"),
		},
	}
	repository := &failingCompleteSessionRepository{
		Repository: fixture.repository,
	}
	repository.fail.Store(true)
	fixture.service.repository = repository
	executor, err := NewAuthenticatedExecutor(
		AuthenticatedExecutorConfig{Service: fixture.service},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("refresh-crash-media")
	sum := sha256.Sum256(body)
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID:      "workspace-1",
		ConnectionID:     fixture.connectionID,
		ExpectedProvider: ProviderBluesky,
		Method:           http.MethodPost,
		Path:             "/xrpc/media.refresh-crash",
		Media: &PublishingMedia{
			Body: io.NopCloser(bytes.NewReader(body)),
			Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]),
		},
	})
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Kind != ExecutorFailureReconnect {
		t.Fatalf("single-use refresh crash failure = %#v", err)
	}
	_, requestCalls := fixture.adapter.counts()
	if requestCalls != 0 {
		t.Fatalf("stream reached provider after refresh crash: %d", requestCalls)
	}
	persisted, err := fixture.repository.GetCredential(
		context.Background(),
		"workspace-1",
		fixture.connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusReconnectRequired {
		t.Fatalf("refresh crash was not failed closed: %#v", persisted)
	}
}

func TestAuthenticatedExecutorStaticRefreshLeaseRefreshesOnce(t *testing.T) {
	fixture := newServiceFixture(t)
	expired := serviceTestNow.Add(-time.Minute)
	fixture.instagram.resources = []DiscoveredResource{instagramResource(
		"ig-1",
		"postqron",
		"expired-token",
		&expired,
	)}
	refreshedExpiry := serviceTestNow.Add(time.Hour)
	fixture.instagram.refreshResult = Credential{
		AccessToken: "rotated-token",
		ExpiresAt:   &refreshedExpiry,
		Scopes: append(
			[]string(nil),
			requiredScopes[ProviderInstagramProfessional]...,
		),
	}
	connection := connectResource(
		t,
		fixture.service,
		ProviderInstagramProfessional,
		"ig-1",
	)
	transport := &executorTransport{do: func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer rotated-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	}}
	executor, err := NewAuthenticatedExecutor(AuthenticatedExecutorConfig{
		Service: fixture.service, Transport: transport,
		ResourceServers: map[Provider]string{
			ProviderInstagramProfessional: "https://graph.example.com",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer group.Done()
			<-start
			_, _ = executor.Execute(context.Background(), PublishingRequest{
				WorkspaceID: "workspace-1", ConnectionID: connection.ID,
				ExpectedProvider: ProviderInstagramProfessional,
				Method:           http.MethodGet, Path: "/me",
			})
		}()
	}
	close(start)
	group.Wait()
	refreshCalls, _, _ := fixture.instagram.counts()
	if refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", refreshCalls)
	}
}

func TestAuthenticatedExecutorRedactsErrorsAndRetryAfter(t *testing.T) {
	failure := redactExecutorError(&ProviderFailure{
		Kind:  FailureTemporary,
		Code:  "secret-provider-code",
		Cause: errors.New("Bearer secret-token"),
	}, http.MethodPost, 0)
	if strings.Contains(failure.Error(), "secret") ||
		strings.Contains(failure.Error(), "Bearer") {
		t.Fatalf("failure leaked diagnostics: %v", failure)
	}
	var typed *ExecutorFailure
	if !errors.As(failure, &typed) || typed.Kind != ExecutorFailureTemporary {
		t.Fatalf("failure = %#v", failure)
	}
	if got := parseRetryAfter("17", time.Now()); got.String() != "17s" {
		t.Fatalf("Retry-After = %v", got)
	}
	ambiguous := redactExecutorError(
		ErrProviderRequestFailed,
		http.MethodPost,
		0,
	)
	if !errors.As(ambiguous, &typed) || typed.Kind != ExecutorFailureAmbiguous {
		t.Fatalf("ambiguous failure = %#v", ambiguous)
	}
}

func TestAuthenticatedExecutorIsNotExposedOverHTTP(t *testing.T) {
	openAPI, err := os.ReadFile("contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err = yaml.Unmarshal(openAPI, &contract); err != nil {
		t.Fatal(err)
	}
	gotPaths := make([]string, 0, len(contract.Paths))
	for route := range contract.Paths {
		gotPaths = append(gotPaths, route)
	}
	slices.Sort(gotPaths)
	wantPaths := []string{
		"/api/v1/social-authorizations/callback",
		"/api/v1/workspaces/{workspace_id}/social-authorizations",
		"/api/v1/workspaces/{workspace_id}/social-connections",
		"/api/v1/workspaces/{workspace_id}/social-connections/bootstrap",
		"/api/v1/workspaces/{workspace_id}/social-connections/{connection_id}",
		"/api/v1/workspaces/{workspace_id}/social-connections/{connection_id}/reconnect",
	}
	slices.Sort(wantPaths)
	if !slices.Equal(gotPaths, wantPaths) {
		t.Fatalf("OpenAPI paths = %#v, want public lifecycle only", gotPaths)
	}

	fixture := newServiceFixture(t)
	handler, err := NewHTTPHandler(
		fixture.service,
		fixedRequestAuthenticator{accountID: "owner-1"},
		testSocialOrigin,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{
		"/api/v1/workspaces/workspace-1/social-connections/connection-1/execute",
		"/api/v1/workspaces/workspace-1/social-connections/connection-1/publishing",
		"/api/v1/workspaces/workspace-1/social-connections/connection-1/media",
		"/api/v1/workspaces/workspace-1/social-connections/connection-1/linkedin-dms-upload",
		"/api/v1/workspaces/workspace-1/social-connections/connection-1/linkedin-assets/urn:li:image:abc",
		"/api/v1/internal/authenticated-executor",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
			request := httptest.NewRequest(method, route, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf(
					"internal candidate %s %s exposed with status %d",
					method,
					route,
					response.Code,
				)
			}
		}
	}
}

func TestAuthenticatedExecutorPostgresCrashReconnectBoundary(t *testing.T) {
	databaseURL := os.Getenv("F05_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F05_DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	workspaceID := "executor-pg-workspace-" + suffix
	connectionID := "executor-pg-connection-" + suffix
	selectionID := "executor-pg-selection-" + suffix
	remoteID := dynamicTestDID + "-" + suffix
	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := NewAESGCMCipher(
		"executor-pg-key",
		[]byte("0123456789abcdef0123456789abcdef"),
	)
	if err != nil {
		t.Fatal(err)
	}
	now := serviceTestNow
	expiresAt := now.Add(time.Hour)
	binding := OAuthBinding{
		Issuer:         "https://auth.example.com",
		ResourceServer: "https://pds.example.com",
		Subject:        dynamicTestDID,
	}
	credentialAAD := credentialAdditionalData(
		workspaceID,
		ProviderBluesky,
		remoteID,
	)
	access, err := cipher.Seal([]byte("executor-pg-access"), credentialAAD)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := cipher.Seal([]byte("executor-pg-refresh"), credentialAAD)
	if err != nil {
		t.Fatal(err)
	}
	session, err := cipher.Seal(
		[]byte("executor-pg-as|executor-pg-rs|executor-pg-dpop-key"),
		dynamicSessionAdditionalData(
			workspaceID,
			ProviderBluesky,
			remoteID,
			binding,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = repository.SaveSelection(context.Background(), StoredSelection{
		ID: selectionID, WorkspaceID: workspaceID, ActorID: "owner-1",
		Provider: ProviderBluesky,
		Resources: []StoredResource{{
			Candidate: Candidate{
				RemoteID: remoteID, ResourceType: ResourceBlueskyAccount,
				AccountType: AccountTypeProfile, DisplayName: "Executor PostgreSQL",
				Scopes: []string{"atproto"},
			},
			AccessTokenCiphertext: access, RefreshTokenCiphertext: refresh,
			OAuthSessionCiphertext: session, Binding: binding,
			RefreshTokenMode: RefreshTokenSingleUse, TokenExpiresAt: &expiresAt,
		}},
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repository.Connect(context.Background(), ConnectCommand{
		NewConnectionID: connectionID, WorkspaceID: workspaceID,
		ActorID: "owner-1", SelectionID: selectionID, RemoteID: remoteID,
		Now: now,
		Event: Event{
			ID: "executor-pg-connect-" + suffix, Type: EventConnected, Version: 1,
			WorkspaceID: workspaceID, ConnectionID: connectionID,
			Provider: ProviderBluesky, RemoteID: remoteID,
			CorrelationID: "executor-pg-correlation-" + suffix, OccurredAt: now,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeDynamicAdapter{
		config: DynamicOAuthConfig{
			RedirectURL: "https://app.example.test/social/callback",
			Scopes:      []string{"atproto"}, RequiresPAR: true, RequiresDPoP: true,
			RequiresATH: true, RequiresIssuer: true, RequiresSubject: true,
			RefreshTokenMode: RefreshTokenSingleUse,
			RevocationPolicy: RevocationBestEffort,
			NetworkPolicy: DynamicNetworkPolicy{
				RejectRedirects: true, ValidateAndPinDNS: true,
				MaxResponseBytes: maximumAuthenticatedBody,
			},
		},
		requestResult: DynamicAuthenticatedResult{
			Response: AuthenticatedResponse{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: []byte(`{"ok":true}`),
			},
		},
	}
	service, err := NewService(Config{
		Repository: repository,
		Authorizer: &fakeAuthorizer{permissions: map[Permission]bool{
			PermissionViewWorkspace: true, PermissionManageChannels: true,
		}},
		Cipher: cipher, Quota: newFakeChannelQuota(),
		DynamicAdapters: map[Provider]DynamicAdapter{ProviderBluesky: adapter},
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingCompleteSessionRepository{Repository: repository}
	failing.fail.Store(true)
	service.repository = failing
	executor, err := NewAuthenticatedExecutor(
		AuthenticatedExecutorConfig{Service: service},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := PublishingRequest{
		WorkspaceID: workspaceID, ConnectionID: connectionID,
		ExpectedProvider: ProviderBluesky, Method: http.MethodPost,
		Path: "/xrpc/post", Header: http.Header{"Content-Type": {"application/json"}},
		Body: []byte(`{"text":"postgres"}`),
	}
	_, err = executor.Execute(context.Background(), request)
	var failure *ExecutorFailure
	if !errors.As(err, &failure) ||
		failure.Kind != ExecutorFailureAmbiguous ||
		!failure.Reconnect {
		t.Fatalf("PostgreSQL crash failure = %#v", err)
	}
	persisted, err := repository.GetCredential(
		context.Background(),
		workspaceID,
		connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != StatusReconnectRequired {
		t.Fatalf("PostgreSQL reconnect state = %#v", persisted)
	}
	_, err = executor.Execute(context.Background(), request)
	if !errors.As(err, &failure) ||
		failure.Kind != ExecutorFailureReconnect {
		t.Fatalf("PostgreSQL second boundary failure = %#v", err)
	}
	_, requestCalls := adapter.counts()
	if requestCalls != 1 {
		t.Fatalf("PostgreSQL provider replay calls = %d", requestCalls)
	}
}
