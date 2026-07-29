package accountprivacy

import (
	"math"
	"sync"
	"time"
)

type rateBucket struct {
	lastSeen time.Time
	tokens   float64
}

type InMemoryRateLimiter struct {
	mu              sync.Mutex
	buckets         map[string]rateBucket
	tokensPerSecond float64
	burst           float64
	idleTTL         time.Duration
	maxKeys         int
}

func NewInMemoryRateLimiter(
	tokensPerSecond float64,
	burst int,
	maxKeys int,
	idleTTL time.Duration,
) *InMemoryRateLimiter {
	return &InMemoryRateLimiter{
		buckets:         make(map[string]rateBucket),
		tokensPerSecond: tokensPerSecond,
		burst:           float64(burst),
		idleTTL:         idleTTL,
		maxKeys:         maxKeys,
	}
}

func newDefaultAccountRateLimiter() *InMemoryRateLimiter {
	return NewInMemoryRateLimiter(0.25, 3, 10_000, 15*time.Minute)
}

func (limiter *InMemoryRateLimiter) Allow(
	key string,
	now time.Time,
) (bool, time.Duration) {
	if key == "" {
		return false, time.Second
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.evictExpired(now)
	current, found := limiter.buckets[key]
	if !found {
		if len(limiter.buckets) >= limiter.maxKeys {
			limiter.evictOldest()
		}
		current = rateBucket{lastSeen: now, tokens: limiter.burst}
	}
	elapsed := now.Sub(current.lastSeen).Seconds()
	if elapsed > 0 {
		current.tokens = math.Min(
			limiter.burst,
			current.tokens+elapsed*limiter.tokensPerSecond,
		)
	}
	current.lastSeen = now
	if current.tokens < 1 {
		limiter.buckets[key] = current
		missing := 1 - current.tokens
		return false, time.Duration(math.Ceil(missing/limiter.tokensPerSecond*1000)) * time.Millisecond
	}
	current.tokens--
	limiter.buckets[key] = current
	return true, 0
}

func (limiter *InMemoryRateLimiter) evictExpired(now time.Time) {
	for key, current := range limiter.buckets {
		if now.Sub(current.lastSeen) >= limiter.idleTTL {
			delete(limiter.buckets, key)
		}
	}
}

func (limiter *InMemoryRateLimiter) evictOldest() {
	var oldestKey string
	var oldestSeen time.Time
	for key, current := range limiter.buckets {
		if oldestKey == "" || current.lastSeen.Before(oldestSeen) {
			oldestKey = key
			oldestSeen = current.lastSeen
		}
	}
	delete(limiter.buckets, oldestKey)
}
