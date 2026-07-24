package operations

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterTokenBucketRefillsAndBoundsKeys(t *testing.T) {
	metrics := &Metrics{}
	limiter, err := NewRateLimiter(1, 2, 2, time.Minute, metrics)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_750_000_000, 0)

	for attempt := 0; attempt < 2; attempt++ {
		allowed, _ := limiter.Allow("tenant-a:write", now)
		if !allowed {
			t.Fatalf("attempt %d unexpectedly rejected", attempt)
		}
	}
	allowed, retryAfter := limiter.Allow("tenant-a:write", now)
	if allowed || retryAfter != time.Second {
		t.Fatalf("third attempt = %v, %v; want rejected for one second", allowed, retryAfter)
	}
	allowed, _ = limiter.Allow("tenant-a:write", now.Add(time.Second))
	if !allowed {
		t.Fatal("refilled request was rejected")
	}

	limiter.Allow("tenant-b:write", now.Add(2*time.Second))
	limiter.Allow("tenant-c:write", now.Add(3*time.Second))
	if len(limiter.buckets) > 2 {
		t.Fatalf("bucket cardinality = %d, want at most 2", len(limiter.buckets))
	}
	if metrics.Snapshot().RateLimitRejections != 1 {
		t.Fatalf("rejections = %d, want 1", metrics.Snapshot().RateLimitRejections)
	}
}

func TestRateLimitMiddlewareReturnsSafe429(t *testing.T) {
	metrics := &Metrics{}
	limiter, err := NewRateLimiter(1, 1, 10, time.Minute, metrics)
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := limiter.Middleware(func(*http.Request) (string, error) {
		return "", errors.New("no authenticated principal")
	}, next)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/posts", nil))

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if response.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After = %q, want 1", response.Header().Get("Retry-After"))
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", response.Header().Get("Cache-Control"))
	}
}

func TestNewRateLimiterRejectsUnsafeConfiguration(t *testing.T) {
	if _, err := NewRateLimiter(0, 1, 1, time.Second, nil); err == nil {
		t.Fatal("NewRateLimiter() accepted zero rate")
	}
	if _, err := NewRateLimiter(1, 0, 1, time.Second, nil); err == nil {
		t.Fatal("NewRateLimiter() accepted zero burst")
	}
}
