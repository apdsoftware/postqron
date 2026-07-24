package email

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

type mapSecrets map[string]string

func (secrets mapSecrets) Secret(_ context.Context, name string) (string, error) {
	value, exists := secrets[name]
	if !exists {
		return "", errors.New("secret not found")
	}
	return value, nil
}

func TestUnsubscribeUsesOpaqueSignedTokenAndIsIdempotent(t *testing.T) {
	store := NewMemoryStore()
	service, err := NewUnsubscribe(
		"https://app.example.test/email/unsubscribe",
		mapSecrets{"F14_UNSUBSCRIBE_SECRET": strings.Repeat("s", 32)},
		"F14_UNSUBSCRIBE_SECRET",
		store,
	)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.URL(context.Background(), "account_123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(target, "account_123") {
		t.Fatalf("unsubscribe URL exposes recipient ID: %s", target)
	}
	parsed, _ := url.Parse(target)
	token := parsed.Query().Get("token")
	for range 2 {
		recipientID, err := service.Apply(context.Background(), token)
		if err != nil || recipientID != "account_123" {
			t.Fatalf("Apply() = %q, %v", recipientID, err)
		}
	}
	suppressed, _ := store.IsSuppressed(
		context.Background(),
		"account_123",
		ChannelMarketing,
	)
	if !suppressed {
		t.Fatal("unsubscribe did not suppress marketing")
	}
	suppressed, _ = store.IsSuppressed(
		context.Background(),
		"account_123",
		ChannelTransactional,
	)
	if suppressed {
		t.Fatal("unsubscribe suppressed required transactional email")
	}

	token = token[:len(token)-1] + "x"
	if _, err := service.Apply(context.Background(), token); err == nil {
		t.Fatal("tampered unsubscribe token was accepted")
	}
}
