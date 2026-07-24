package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

var testNow = time.Date(2026, time.July, 24, 14, 30, 0, 0, time.UTC)

const (
	testWorkspace = "9f355e66-678d-4a27-a9b8-daeb352ceb89"
)

var testCredential = "pq_live_" + strings.Repeat("not-a-secret-", 3)

type fakeAuthenticator struct {
	credential string
	err        error
	mu         sync.Mutex
	principal  Principal
}

func (fake *fakeAuthenticator) Authenticate(_ context.Context, credential string) (Principal, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.credential = credential
	return fake.principal, fake.err
}

type fakeAuthorizer struct {
	calls int
	err   error
	mu    sync.Mutex
	scope Scope
}

func (fake *fakeAuthorizer) Authorize(
	_ context.Context,
	_ Principal,
	_ string,
	scope Scope,
) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.calls++
	fake.scope = scope
	return fake.err
}

type fakeLimiter struct {
	allowed    bool
	err        error
	key        string
	mu         sync.Mutex
	retryAfter time.Duration
}

func (fake *fakeLimiter) Allow(
	_ context.Context,
	key string,
	_ time.Time,
) (bool, time.Duration, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.key = key
	return fake.allowed, fake.retryAfter, fake.err
}

type fakePostGateway struct {
	createCount int
	created     Post
	listAfter   []string
	listLimit   []int
	listPosts   []Post
	mu          sync.Mutex
	nextAfter   string
}

func (fake *fakePostGateway) ListPosts(
	_ context.Context,
	_ string,
	page PageRequest,
) ([]Post, string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.listAfter = append(fake.listAfter, page.After)
	fake.listLimit = append(fake.listLimit, page.Limit)
	return fake.listPosts, fake.nextAfter, nil
}

func (fake *fakePostGateway) CreatePost(
	_ context.Context,
	workspaceID string,
	command CreatePostCommand,
) (Post, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.createCount++
	post := fake.created
	post.WorkspaceID = workspaceID
	post.Text = command.Text
	return post, nil
}

func newTestAPI(
	t *testing.T,
	scopes ...Scope,
) (http.Handler, *fakeAuthenticator, *fakeAuthorizer, *fakeLimiter, *fakePostGateway) {
	t.Helper()
	granted := make(map[Scope]struct{}, len(scopes))
	for _, scope := range scopes {
		granted[scope] = struct{}{}
	}
	authenticator := &fakeAuthenticator{principal: Principal{
		CredentialID: "cred_01",
		WorkspaceID:  testWorkspace,
		Scopes:       granted,
		ExpiresAt:    testNow.Add(time.Hour),
	}}
	authorizer := &fakeAuthorizer{}
	limiter := &fakeLimiter{allowed: true}
	posts := &fakePostGateway{created: Post{
		ID:        "post_01",
		Status:    "draft",
		CreatedAt: testNow,
	}}
	handler, err := NewAPIHandler(APIConfig{
		Authenticator: authenticator,
		Authorizer:    authorizer,
		Clock:         func() time.Time { return testNow },
		CursorKey:     []byte("0123456789abcdef0123456789abcdef"),
		Idempotency:   NewMemoryIdempotencyStore(func() time.Time { return testNow }),
		Limiter:       limiter,
		Posts:         posts,
	})
	if err != nil {
		t.Fatalf("NewAPIHandler() error = %v", err)
	}
	return handler, authenticator, authorizer, limiter, posts
}

func authorizedRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testCredential)
	return request
}

func TestPublicAPIAuthenticationScopeWorkspaceAndRateLimit(t *testing.T) {
	t.Run("missing bearer credential", func(t *testing.T) {
		handler, _, authorizer, _, _ := newTestAPI(t, ScopePostsRead)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(
			http.MethodGet,
			"/api/public/v1/workspaces/"+testWorkspace+"/posts",
			nil,
		))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", response.Code, response.Body)
		}
		if response.Header().Get("WWW-Authenticate") == "" {
			t.Fatal("WWW-Authenticate header is missing")
		}
		if authorizer.calls != 0 {
			t.Fatalf("authorizer calls = %d, want 0", authorizer.calls)
		}
	})

	t.Run("missing scope", func(t *testing.T) {
		handler, _, authorizer, _, _ := newTestAPI(t, ScopePostsWrite)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authorizedRequest(
			http.MethodGet,
			"/api/public/v1/workspaces/"+testWorkspace+"/posts",
			"",
		))
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", response.Code, response.Body)
		}
		if authorizer.calls != 0 {
			t.Fatalf("authorizer calls = %d, want 0", authorizer.calls)
		}
	})

	t.Run("workspace mismatch fails before RBAC", func(t *testing.T) {
		handler, _, authorizer, _, _ := newTestAPI(t, ScopePostsRead)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authorizedRequest(
			http.MethodGet,
			"/api/public/v1/workspaces/6ebf68d8-bb97-48bc-b067-0f191a408451/posts",
			"",
		))
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", response.Code, response.Body)
		}
		if authorizer.calls != 0 {
			t.Fatalf("authorizer calls = %d, want 0", authorizer.calls)
		}
	})

	t.Run("RBAC denial", func(t *testing.T) {
		handler, _, authorizer, _, _ := newTestAPI(t, ScopePostsRead)
		authorizer.err = ErrForbidden
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authorizedRequest(
			http.MethodGet,
			"/api/public/v1/workspaces/"+testWorkspace+"/posts",
			"",
		))
		if response.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", response.Code, response.Body)
		}
	})

	t.Run("rate limit uses authenticated identity", func(t *testing.T) {
		handler, _, _, limiter, _ := newTestAPI(t, ScopePostsRead)
		limiter.allowed = false
		limiter.retryAfter = 1500 * time.Millisecond
		request := authorizedRequest(
			http.MethodGet,
			"/api/public/v1/workspaces/"+testWorkspace+"/posts",
			"",
		)
		request.Header.Set("X-Credential-ID", "attacker-controlled")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429; body=%s", response.Code, response.Body)
		}
		if got := response.Header().Get("Retry-After"); got != "2" {
			t.Fatalf("Retry-After = %q, want 2", got)
		}
		if limiter.key != "cred_01:posts:read" {
			t.Fatalf("rate key = %q", limiter.key)
		}
	})
}

func TestPublicAPIUsesSignedPaginationCursor(t *testing.T) {
	handler, authenticator, authorizer, limiter, posts := newTestAPI(t, ScopePostsRead)
	posts.listPosts = []Post{{
		ID:          "post_01",
		WorkspaceID: testWorkspace,
		Text:        "Public content",
		Status:      "scheduled",
		CreatedAt:   testNow,
	}}
	posts.nextAfter = "post_01"

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, authorizedRequest(
		http.MethodGet,
		"/api/public/v1/workspaces/"+testWorkspace+"/posts?limit=10",
		"",
	))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d; body=%s", first.Code, first.Body)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.NextCursor == "" || strings.Contains(page.NextCursor, "post_01") {
		t.Fatalf("cursor is missing or transparent: %q", page.NextCursor)
	}
	if strings.Contains(first.Body.String(), "access_token") ||
		strings.Contains(first.Body.String(), "provider_token") {
		t.Fatalf("response contains a provider credential field: %s", first.Body)
	}

	posts.nextAfter = ""
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, authorizedRequest(
		http.MethodGet,
		"/api/public/v1/workspaces/"+testWorkspace+"/posts?cursor="+url.QueryEscape(page.NextCursor),
		"",
	))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d; body=%s", second.Code, second.Body)
	}
	if len(posts.listAfter) != 2 || posts.listAfter[1] != "post_01" {
		t.Fatalf("decoded after values = %#v", posts.listAfter)
	}
	if posts.listLimit[0] != 10 || posts.listLimit[1] != defaultPageSize {
		t.Fatalf("page limits = %#v", posts.listLimit)
	}
	if authenticator.credential != testCredential ||
		authorizer.scope != ScopePostsRead ||
		limiter.key != "cred_01:posts:read" {
		t.Fatal("authentication, authorization, or rate-limit boundary was bypassed")
	}
	if got := first.Header().Get("Postqron-API-Version"); got != "2026-07-01" {
		t.Fatalf("API version header = %q", got)
	}

	tampered := page.NextCursor[:len(page.NextCursor)-1] + "A"
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, authorizedRequest(
		http.MethodGet,
		"/api/public/v1/workspaces/"+testWorkspace+"/posts?cursor="+url.QueryEscape(tampered),
		"",
	))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("tampered cursor status = %d; body=%s", invalid.Code, invalid.Body)
	}
}

func TestPublicAPICreateIsIdempotent(t *testing.T) {
	handler, _, _, _, posts := newTestAPI(t, ScopePostsWrite)
	target := "/api/public/v1/workspaces/" + testWorkspace + "/posts"
	call := func(body string) *httptest.ResponseRecorder {
		request := authorizedRequest(http.MethodPost, target, body)
		request.Header.Set("Idempotency-Key", "cms-entry-123456789")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	first := call(`{"text":"Publish once"}`)
	second := call(`{"text":"Publish once"}`)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("statuses = %d, %d; bodies=%s / %s", first.Code, second.Code, first.Body, second.Body)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replayed body differs: %q / %q", first.Body, second.Body)
	}
	if got := second.Header().Get("Idempotency-Replayed"); got != "true" {
		t.Fatalf("Idempotency-Replayed = %q", got)
	}
	if posts.createCount != 1 {
		t.Fatalf("CreatePost calls = %d, want 1", posts.createCount)
	}

	conflict := call(`{"text":"Different request"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d; body=%s", conflict.Code, conflict.Body)
	}
	if posts.createCount != 1 {
		t.Fatalf("CreatePost calls after conflict = %d, want 1", posts.createCount)
	}
}

func TestPublicAPICreateSerializesConcurrentRetries(t *testing.T) {
	handler, _, _, _, posts := newTestAPI(t, ScopePostsWrite)
	target := "/api/public/v1/workspaces/" + testWorkspace + "/posts"
	const requests = 16
	statuses := make(chan int, requests)
	var group sync.WaitGroup
	for range requests {
		group.Add(1)
		go func() {
			defer group.Done()
			request := authorizedRequest(http.MethodPost, target, `{"text":"Only once"}`)
			request.Header.Set("Idempotency-Key", "concurrent-key-123456")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			statuses <- response.Code
		}()
	}
	group.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusCreated {
			t.Fatalf("concurrent status = %d", status)
		}
	}
	if posts.createCount != 1 {
		t.Fatalf("CreatePost calls = %d, want 1", posts.createCount)
	}
}

func TestCursorIsWorkspaceBoundAndExpires(t *testing.T) {
	now := testNow
	codec, err := NewCursorCodec(
		[]byte("0123456789abcdef0123456789abcdef"),
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := codec.Encode(testWorkspace, "post_01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Decode("another-workspace", cursor); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-workspace Decode() error = %v", err)
	}
	now = now.Add(cursorLifetime + time.Second)
	if _, err := codec.Decode(testWorkspace, cursor); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expired Decode() error = %v", err)
	}
}
