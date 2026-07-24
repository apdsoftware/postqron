package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrSecretNotAllowed = errors.New("secret is not allowlisted")
	ErrSecretNotFound   = errors.New("secret was not found")
)

type Secret struct {
	value string
}

func NewSecret(value string) (Secret, error) {
	if strings.TrimSpace(value) == "" {
		return Secret{}, ErrSecretNotFound
	}
	return Secret{value: value}, nil
}

func (secret Secret) Reveal() string {
	return secret.value
}

func (Secret) String() string {
	return RedactedValue
}

func (Secret) GoString() string {
	return RedactedValue
}

type SecretProvider interface {
	ReadSecret(context.Context, string) (Secret, error)
}

type secretCacheEntry struct {
	expiresAt time.Time
	secret    Secret
}

// SecretManager is the single application entry point for runtime secrets. It
// allowlists names and delegates retrieval to an external provider.
type SecretManager struct {
	allowed  map[string]struct{}
	cache    map[string]secretCacheEntry
	cacheTTL time.Duration
	now      func() time.Time
	provider SecretProvider
	mu       sync.Mutex
}

func NewSecretManager(
	provider SecretProvider,
	allowedNames []string,
	cacheTTL time.Duration,
) (*SecretManager, error) {
	if provider == nil {
		return nil, errors.New("secret provider is required")
	}
	if cacheTTL < 0 {
		return nil, errors.New("secret cache TTL cannot be negative")
	}
	allowed := make(map[string]struct{}, len(allowedNames))
	for _, name := range allowedNames {
		if err := validateSecretName(name); err != nil {
			return nil, err
		}
		allowed[name] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, errors.New("at least one secret name is required")
	}
	return &SecretManager{
		allowed:  allowed,
		cache:    make(map[string]secretCacheEntry),
		cacheTTL: cacheTTL,
		now:      time.Now,
		provider: provider,
	}, nil
}

func (manager *SecretManager) Get(ctx context.Context, name string) (Secret, error) {
	if _, allowed := manager.allowed[name]; !allowed {
		return Secret{}, ErrSecretNotAllowed
	}
	now := manager.now()
	manager.mu.Lock()
	if cached, exists := manager.cache[name]; exists && now.Before(cached.expiresAt) {
		manager.mu.Unlock()
		return cached.secret, nil
	}
	manager.mu.Unlock()

	secret, err := manager.provider.ReadSecret(ctx, name)
	if err != nil {
		return Secret{}, fmt.Errorf("read allowlisted secret %q: %w", name, err)
	}
	if strings.TrimSpace(secret.Reveal()) == "" {
		return Secret{}, ErrSecretNotFound
	}
	if manager.cacheTTL > 0 {
		manager.mu.Lock()
		manager.cache[name] = secretCacheEntry{
			expiresAt: now.Add(manager.cacheTTL),
			secret:    secret,
		}
		manager.mu.Unlock()
	}
	return secret, nil
}

func (manager *SecretManager) Invalidate(name string) {
	manager.mu.Lock()
	delete(manager.cache, name)
	manager.mu.Unlock()
}

func validateSecretName(name string) error {
	if name == "" {
		return errors.New("secret name is empty")
	}
	for _, character := range name {
		if character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '_' {
			continue
		}
		return fmt.Errorf("secret name %q must use uppercase ASCII, digits, and underscores", name)
	}
	return nil
}

// MapSecretProvider exists only for local development and tests. Production
// must inject a provider backed by the deployment secret manager.
type MapSecretProvider map[string]string

func (provider MapSecretProvider) ReadSecret(_ context.Context, name string) (Secret, error) {
	value, exists := provider[name]
	if !exists {
		return Secret{}, ErrSecretNotFound
	}
	return NewSecret(value)
}
