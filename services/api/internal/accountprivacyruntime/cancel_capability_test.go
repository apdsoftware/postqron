package accountprivacyruntime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accountprivacy "github.com/apdsoftware/postqron/features/f12-account-privacy"
)

const testPrivacyOrigin = "https://app.example.test"

func TestCancelCapabilityAllowsOneConcurrentWinner(t *testing.T) {
	t.Parallel()

	const contenders = 12
	rawToken := make([]byte, 32)
	for index := range rawToken {
		rawToken[index] = byte(index + 1)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	digest := sha256.Sum256([]byte(token))
	store := &concurrentCancelStore{
		tokenHash: hex.EncodeToString(digest[:]),
		accountID: "account-1",
		expected:  contenders,
		allClaims: make(chan struct{}),
	}
	service := &countingCancellationService{}
	handler := cancelCapabilityHandler{
		store:          store,
		service:        service,
		allowedOrigins: map[string]struct{}{testPrivacyOrigin: {}},
		now:            func() time.Time { return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC) },
	}

	start := make(chan struct{})
	statuses := make(chan int, contenders)
	var requests sync.WaitGroup
	requests.Add(contenders)
	for range contenders {
		go func() {
			defer requests.Done()
			<-start
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/account/deletions/deletion-1/cancel",
				nil,
			)
			request.Header.Set("Origin", testPrivacyOrigin)
			request.AddCookie(&http.Cookie{
				Name:  cancelCapabilityCookieName,
				Value: token,
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			statuses <- response.Code
		}()
	}
	close(start)
	requests.Wait()
	close(statuses)

	winners := 0
	for status := range statuses {
		if status == http.StatusNoContent {
			winners++
		} else if status != http.StatusNotFound {
			t.Fatalf("unexpected cancellation status %d", status)
		}
	}
	if winners != 1 {
		t.Fatalf("got %d successful cancellations, want exactly one", winners)
	}
	if calls := service.calls.Load(); calls != 1 {
		t.Fatalf("RestoreAccess path called %d times, want exactly one", calls)
	}
	if !store.consumed {
		t.Fatal("winning capability was not consumed")
	}
}

func TestCancelCapabilityClearsHttpOnlyCookieOnlyOnSuccess(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	digest := sha256.Sum256([]byte(token))
	handler := cancelCapabilityHandler{
		store: &concurrentCancelStore{
			tokenHash: hex.EncodeToString(digest[:]),
			accountID: "account-1",
			expected:  1,
			allClaims: make(chan struct{}),
		},
		service:        &countingCancellationService{},
		allowedOrigins: map[string]struct{}{testPrivacyOrigin: {}},
		secureCookies:  true,
		now: func() time.Time {
			return time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/account/deletions/deletion-1/cancel",
		nil,
	)
	request.Header.Set("Origin", testPrivacyOrigin)
	request.AddCookie(&http.Cookie{Name: cancelCapabilityCookieName, Value: token})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d response cookies, want one", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != cancelCapabilityCookieName || cookie.MaxAge != -1 ||
		cookie.Path != cancelCapabilityCookiePath || !cookie.HttpOnly ||
		!cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected cleared capability cookie: %#v", cookie)
	}
}

func TestCancelCapabilityReleasesClaimAfterRetryableFailure(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	digest := sha256.Sum256([]byte(token))
	service := &countingCancellationService{}
	service.failures.Store(1)
	handler := cancelCapabilityHandler{
		store: &concurrentCancelStore{
			tokenHash: hex.EncodeToString(digest[:]),
			accountID: "account-1",
			expected:  1,
			allClaims: make(chan struct{}),
		},
		service:        service,
		allowedOrigins: map[string]struct{}{testPrivacyOrigin: {}},
		now:            time.Now,
	}

	statuses := make([]int, 0, 2)
	for range 2 {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/account/deletions/deletion-1/cancel",
			nil,
		)
		request.Header.Set("Origin", testPrivacyOrigin)
		request.AddCookie(&http.Cookie{Name: cancelCapabilityCookieName, Value: token})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		statuses = append(statuses, response.Code)
	}
	if statuses[0] != http.StatusConflict || statuses[1] != http.StatusNoContent {
		t.Fatalf("statuses = %v, want [409 204]", statuses)
	}
}

func TestCancelCapabilityReturnsSemanticSuccessWhenConsumeFails(t *testing.T) {
	t.Parallel()

	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	digest := sha256.Sum256([]byte(token))
	store := &concurrentCancelStore{
		tokenHash:  hex.EncodeToString(digest[:]),
		accountID:  "account-1",
		expected:   1,
		allClaims:  make(chan struct{}),
		consumeErr: errors.New("temporary consume failure"),
	}
	service := &countingCancellationService{}
	handler := cancelCapabilityHandler{
		store:          store,
		service:        service,
		allowedOrigins: map[string]struct{}{testPrivacyOrigin: {}},
		secureCookies:  true,
		now:            time.Now,
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/account/deletions/deletion-1/cancel",
		nil,
	)
	request.Header.Set("Origin", testPrivacyOrigin)
	request.AddCookie(&http.Cookie{Name: cancelCapabilityCookieName, Value: token})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want semantic success 204", response.Code)
	}
	if calls := service.calls.Load(); calls != 1 {
		t.Fatalf("CancelDeletion calls = %d, want one", calls)
	}
	if !store.auditConsumeFailure {
		t.Fatal("consume failure was not audited")
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge != -1 {
		t.Fatalf("capability cookie was not cleared: %#v", cookies)
	}
}

func TestCancelCapabilityIssueRejectsMissingOrUntrustedOrigin(t *testing.T) {
	t.Parallel()

	handler := cancelCapabilityHandler{
		allowedOrigins: map[string]struct{}{testPrivacyOrigin: {}},
		now:            time.Now,
	}
	for _, origin := range []string{"", "https://evil.example.test", "javascript:alert(1)"} {
		request := httptest.NewRequest(http.MethodPost, cancelCapabilityIssuePath, nil)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		if response.Code != http.StatusForbidden {
			t.Fatalf("origin %q: status = %d, want 403", origin, response.Code)
		}
	}
}

func TestCancelCapabilityRejectsMissingOrUntrustedOriginBeforeClaim(t *testing.T) {
	t.Parallel()

	handler := cancelCapabilityHandler{
		allowedOrigins: map[string]struct{}{testPrivacyOrigin: {}},
		now:            time.Now,
	}
	for _, origin := range []string{"", "https://evil.example.test"} {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/account/deletions/deletion-1/cancel",
			nil,
		)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		if response.Code != http.StatusForbidden {
			t.Fatalf("origin %q: status = %d, want 403", origin, response.Code)
		}
	}
}

func TestCancelCapabilityIssueUsesCookieAndReturnsOnlyExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	store := &concurrentCancelStore{}
	handler := cancelCapabilityHandler{
		store: store,
		authenticator: fixedPrincipalAuthenticator{principal: accountprivacy.Principal{
			AccountID: "account-1", AuthenticatedAt: now,
		}},
		allowedOrigins: map[string]struct{}{testPrivacyOrigin: {}},
		secureCookies:  true,
		now:            func() time.Time { return now },
	}
	request := httptest.NewRequest(http.MethodPost, cancelCapabilityIssuePath, nil)
	request.Header.Set("Origin", testPrivacyOrigin)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 1 || payload["expires_at"] == nil {
		t.Fatalf("response exposes fields other than expires_at: %#v", payload)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d response cookies, want one", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != cancelCapabilityCookieName ||
		cookie.Path != cancelCapabilityCookiePath || !cookie.HttpOnly ||
		!cookie.Secure || cookie.SameSite != http.SameSiteStrictMode ||
		cookie.Value == "" {
		t.Fatalf("unexpected capability cookie: %#v", cookie)
	}
}

type concurrentCancelStore struct {
	mu                  sync.Mutex
	tokenHash           string
	accountID           string
	expected            int
	attempts            int
	claimed             bool
	consumed            bool
	consumeErr          error
	auditConsumeFailure bool
	allClaims           chan struct{}
}

func (store *concurrentCancelStore) Issue(
	context.Context, string, string, time.Time, time.Time,
) error {
	return nil
}

func (store *concurrentCancelStore) Claim(
	_ context.Context,
	tokenHash, requestID, _ string,
	_ time.Time,
) (string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.attempts++
	if store.attempts == store.expected {
		close(store.allClaims)
	}
	if tokenHash != store.tokenHash || requestID != "deletion-1" ||
		store.claimed || store.consumed {
		return "", context.Canceled
	}
	store.claimed = true
	return store.accountID, nil
}

func (store *concurrentCancelStore) Release(context.Context, string, string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.claimed = false
	return nil
}

func (store *concurrentCancelStore) Consume(
	context.Context, string, string, time.Time,
) error {
	<-store.allClaims
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.consumeErr != nil {
		return store.consumeErr
	}
	store.claimed = false
	store.consumed = true
	return nil
}

func (store *concurrentCancelStore) AuditConsumeFailure(
	context.Context,
	time.Time,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.auditConsumeFailure = true
	return nil
}

type countingCancellationService struct {
	calls    atomic.Int32
	failures atomic.Int32
}

type fixedPrincipalAuthenticator struct {
	principal accountprivacy.Principal
}

func (authenticator fixedPrincipalAuthenticator) Principal(
	*http.Request,
) (accountprivacy.Principal, bool) {
	return authenticator.principal, true
}

func (service *countingCancellationService) CancelDeletion(
	context.Context,
	accountprivacy.Principal,
	string,
) error {
	service.calls.Add(1)
	if service.failures.CompareAndSwap(1, 0) {
		return errors.New("temporary restore failure")
	}
	return nil
}
