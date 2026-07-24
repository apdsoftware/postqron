package operations

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type bucket struct {
	lastSeen time.Time
	tokens   float64
}

// RateLimiter is an in-process token bucket suitable for service-local
// protection. Production deployments should share limits through the
// configured central store so that replicas enforce one budget.
type RateLimiter struct {
	buckets         map[string]bucket
	burst           float64
	idleTTL         time.Duration
	maxKeys         int
	metrics         *Metrics
	mu              sync.Mutex
	tokensPerSecond float64
}

func NewRateLimiter(
	tokensPerSecond float64,
	burst int,
	maxKeys int,
	idleTTL time.Duration,
	metrics *Metrics,
) (*RateLimiter, error) {
	if tokensPerSecond <= 0 {
		return nil, errors.New("tokens per second must be positive")
	}
	if burst <= 0 {
		return nil, errors.New("burst must be positive")
	}
	if maxKeys <= 0 {
		return nil, errors.New("max keys must be positive")
	}
	if idleTTL <= 0 {
		return nil, errors.New("idle TTL must be positive")
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	return &RateLimiter{
		buckets:         make(map[string]bucket),
		burst:           float64(burst),
		idleTTL:         idleTTL,
		maxKeys:         maxKeys,
		metrics:         metrics,
		tokensPerSecond: tokensPerSecond,
	}, nil
}

func (limiter *RateLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	if key == "" {
		limiter.metrics.RecordRateLimitRejection()
		return false, time.Second
	}

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.evictExpired(now)

	current, exists := limiter.buckets[key]
	if !exists {
		if len(limiter.buckets) >= limiter.maxKeys {
			limiter.evictOldest()
		}
		current = bucket{lastSeen: now, tokens: limiter.burst}
	}
	elapsed := now.Sub(current.lastSeen).Seconds()
	if elapsed > 0 {
		current.tokens = math.Min(limiter.burst, current.tokens+elapsed*limiter.tokensPerSecond)
	}
	current.lastSeen = now

	if current.tokens < 1 {
		limiter.buckets[key] = current
		limiter.metrics.RecordRateLimitRejection()
		missing := 1 - current.tokens
		return false, time.Duration(math.Ceil(missing/limiter.tokensPerSecond*1000)) * time.Millisecond
	}

	current.tokens--
	limiter.buckets[key] = current
	return true, 0
}

func (limiter *RateLimiter) Middleware(
	keyForRequest func(*http.Request) (string, error),
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if keyForRequest == nil {
			writeRateLimitError(writer, time.Second)
			limiter.metrics.RecordRateLimitRejection()
			return
		}
		key, err := keyForRequest(request)
		if err != nil || key == "" {
			writeRateLimitError(writer, time.Second)
			limiter.metrics.RecordRateLimitRejection()
			return
		}
		allowed, retryAfter := limiter.Allow(key, time.Now())
		if !allowed {
			writeRateLimitError(writer, retryAfter)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (limiter *RateLimiter) evictExpired(now time.Time) {
	for key, current := range limiter.buckets {
		if now.Sub(current.lastSeen) >= limiter.idleTTL {
			delete(limiter.buckets, key)
		}
	}
}

func (limiter *RateLimiter) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for key, current := range limiter.buckets {
		if oldestKey == "" || current.lastSeen.Before(oldestTime) {
			oldestKey = key
			oldestTime = current.lastSeen
		}
	}
	delete(limiter.buckets, oldestKey)
}

func writeRateLimitError(writer http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	writer.Header().Set("Cache-Control", "no-store")
	writeOperationalJSON(writer, http.StatusTooManyRequests, map[string]string{
		"code":    "rate_limited",
		"message": "request limit exceeded; retry later",
	})
}
