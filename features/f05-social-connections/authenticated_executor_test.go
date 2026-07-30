package socialconnections

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type executorTransport struct {
	calls atomic.Int32
	do    func(*http.Request) (*http.Response, error)
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
		{WorkspaceID: "wrong", ConnectionID: "missing", Method: "POST", Path: "/posts"},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, ExpectedProvider: ProviderX, Method: "POST", Path: "/posts"},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, Method: "POST", Path: "https://evil.example/x"},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, Method: "POST", Path: "/../admin"},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, Method: "POST", Path: "/posts", Header: http.Header{"Authorization": {"stolen"}}},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, Method: "POST", Path: "/posts", Header: http.Header{"X-Api-Key": {"stolen"}}},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, Method: "POST", Path: "/posts", Header: http.Header{"Connection": {"keep-alive"}}},
		{WorkspaceID: "workspace-1", ConnectionID: connection.ID, Method: "POST", Path: "/posts", Header: http.Header{"X-Test": {"ok\r\nAuthorization: stolen"}}},
	} {
		_, _ = executor.Execute(context.Background(), request)
	}
	if transport.calls.Load() != 0 {
		t.Fatalf("transport calls = %d", transport.calls.Load())
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
		response.Header.Get("X-Safe") != "yes" {
		t.Fatalf("sanitized headers = %#v", response.Header)
	}
	if bytes.Contains(response.Body, []byte("instagram-token")) {
		t.Fatal("access token escaped through response")
	}

	badBody := &trackedReadCloser{Reader: bytes.NewReader(body)}
	bad := sha256.Sum256([]byte("other"))
	_, err = executor.Execute(context.Background(), PublishingRequest{
		WorkspaceID: "workspace-1", ConnectionID: connection.ID,
		Method: http.MethodPost, Path: "/media",
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
		Method: http.MethodPost, Path: "/media",
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
		Method: http.MethodPost, Path: "/posts",
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
				Method: http.MethodGet, Path: "/me",
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
	routes, err := os.ReadFile("http.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range [][]byte{openAPI, routes} {
		if bytes.Contains(bytes.ToLower(content), []byte("authenticated-executor")) ||
			bytes.Contains(bytes.ToLower(content), []byte("publishingrequest")) {
			t.Fatal("authenticated executor leaked into public HTTP surface")
		}
	}
}
