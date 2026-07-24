package operations

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type countingSecretProvider struct {
	calls int
	value string
}

func (provider *countingSecretProvider) ReadSecret(
	_ context.Context,
	_ string,
) (Secret, error) {
	provider.calls++
	return NewSecret(provider.value)
}

func TestSecretManagerAllowlistsCachesAndRedactsSecrets(t *testing.T) {
	provider := &countingSecretProvider{value: "super-secret-value"}
	manager, err := NewSecretManager(provider, []string{"DATABASE_URL"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	manager.now = func() time.Time { return time.Unix(1_750_000_000, 0) }

	first, err := manager.Get(context.Background(), "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Get(context.Background(), "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	if first.Reveal() != "super-secret-value" || second.Reveal() != first.Reveal() {
		t.Fatal("secret manager returned the wrong value")
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if got := fmt.Sprintf("%s %#v", first, first); got != RedactedValue+" "+RedactedValue {
		t.Fatalf("formatted secret = %q", got)
	}

	_, err = manager.Get(context.Background(), "UNAPPROVED_SECRET")
	if !errors.Is(err, ErrSecretNotAllowed) {
		t.Fatalf("unapproved secret error = %v", err)
	}
}

func TestSecretManagerInvalidationSupportsRotation(t *testing.T) {
	provider := &countingSecretProvider{value: "version-one"}
	manager, err := NewSecretManager(provider, []string{"SESSION_SIGNING_KEY"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get(context.Background(), "SESSION_SIGNING_KEY"); err != nil {
		t.Fatal(err)
	}
	provider.value = "version-two"
	manager.Invalidate("SESSION_SIGNING_KEY")

	rotated, err := manager.Get(context.Background(), "SESSION_SIGNING_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Reveal() != "version-two" || provider.calls != 2 {
		t.Fatalf("rotation = %q, calls %d", rotated.Reveal(), provider.calls)
	}
}

func TestSecretManagerRejectsInvalidNames(t *testing.T) {
	_, err := NewSecretManager(MapSecretProvider{}, []string{"database-url"}, 0)
	if err == nil {
		t.Fatal("NewSecretManager() accepted a noncanonical name")
	}
}
