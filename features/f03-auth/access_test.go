package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccountAccessFreezeRestoreFinalizeForEveryProvider(t *testing.T) {
	for _, provider := range SupportedProviders {
		t.Run(string(provider), func(t *testing.T) {
			service, store, providers := newTestService(t, nil)
			providers[provider].identity = ExternalIdentity{
				Subject:       "subject-" + string(provider),
				Email:         string(provider) + "@example.test",
				EmailVerified: true,
			}
			registered := register(t, service, provider)
			passwordTokenHash := "password-token-" + string(provider)
			store.mu.Lock()
			store.state.passwordTokens[passwordTokenHash] = memoryPasswordToken{
				AccountID: registered.AccountID,
			}
			store.mu.Unlock()
			boundary, err := NewAccountAccessBoundary(
				store,
				func() time.Time { return testNow.Add(time.Minute) },
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := boundary.Freeze(context.Background(), registered.AccountID); err != nil {
				t.Fatalf("Freeze() error = %v", err)
			}
			store.mu.Lock()
			firstConsumedAt := store.state.passwordTokens[passwordTokenHash].ConsumedAt
			store.mu.Unlock()
			if firstConsumedAt == nil {
				t.Fatal("freeze left a password token reusable")
			}
			if err := boundary.Freeze(context.Background(), registered.AccountID); err != nil {
				t.Fatalf("idempotent Freeze() error = %v", err)
			}
			store.mu.Lock()
			secondConsumedAt := store.state.passwordTokens[passwordTokenHash].ConsumedAt
			store.mu.Unlock()
			if secondConsumedAt == nil || !secondConsumedAt.Equal(*firstConsumedAt) {
				t.Fatal("idempotent freeze changed the consumed password token")
			}
			if _, err := service.Authenticate(
				context.Background(),
				registered.SessionToken,
			); err == nil {
				t.Fatal("frozen account retained an active session")
			}
			_, state := beginLogin(t, service, provider)
			if _, err := service.Callback(context.Background(), CallbackRequest{
				State: state,
				Code:  "frozen-login",
			}); err == nil {
				t.Fatal("frozen account created an OAuth session")
			}
			if err := boundary.Restore(context.Background(), registered.AccountID); err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			store.mu.Lock()
			restoredConsumedAt := store.state.passwordTokens[passwordTokenHash].ConsumedAt
			store.mu.Unlock()
			if restoredConsumedAt == nil || !restoredConsumedAt.Equal(*firstConsumedAt) {
				t.Fatal("restore resurrected a consumed password token")
			}
			if err := boundary.Restore(context.Background(), registered.AccountID); err != nil {
				t.Fatalf("idempotent Restore() error = %v", err)
			}
			if _, err := service.Authenticate(
				context.Background(),
				registered.SessionToken,
			); err == nil {
				t.Fatal("restore resurrected a revoked session")
			}
			_, state = beginLogin(t, service, provider)
			restored, err := service.Callback(context.Background(), CallbackRequest{
				State: state,
				Code:  "restored-login",
			})
			if err != nil || restored.SessionToken == "" {
				t.Fatalf("restored Callback() = %+v, %v", restored, err)
			}
			if err := boundary.Finalize(context.Background(), registered.AccountID); err != nil {
				t.Fatalf("Finalize() error = %v", err)
			}
			if err := boundary.Finalize(context.Background(), registered.AccountID); err != nil {
				t.Fatalf("idempotent Finalize() error = %v", err)
			}
			if !errors.Is(
				boundary.Restore(context.Background(), registered.AccountID),
				ErrAccountAccessUnavailable,
			) {
				t.Fatal("finalized account was restorable")
			}
			snapshot := store.Snapshot()
			if len(snapshot.Identities) != 0 {
				t.Fatal("finalize retained provider identity PII")
			}
			if snapshot.Accounts[0].DisplayName != "" ||
				snapshot.Accounts[0].AccessState != AccountAccessFinalized {
				t.Fatalf("finalized account = %+v", snapshot.Accounts[0])
			}
			if !strings.HasSuffix(snapshot.Accounts[0].Email, "@account.invalid") {
				t.Fatalf("finalized email = %q", snapshot.Accounts[0].Email)
			}
		})
	}
}

func TestFreezeInvalidatesPendingLinkAttempt(t *testing.T) {
	service, store, providers := newTestService(t, nil)
	providers[ProviderGoogle].identity = ExternalIdentity{
		Subject: "google", Email: "user@example.test", EmailVerified: true,
	}
	registered := register(t, service, ProviderGoogle)
	if _, err := service.BeginLink(context.Background(), BeginLinkRequest{
		Provider: ProviderApple, SessionToken: registered.SessionToken,
	}); err != nil {
		t.Fatalf("BeginLink() error = %v", err)
	}
	boundary, _ := NewAccountAccessBoundary(store, func() time.Time {
		return testNow.Add(time.Minute)
	})
	if err := boundary.Freeze(context.Background(), registered.AccountID); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range store.Snapshot().Attempts {
		if attempt.Intent == IntentLink && attempt.Status != AttemptFailed {
			t.Fatalf("link attempt status = %s", attempt.Status)
		}
	}
}

func TestFreezeAndOAuthLoginRaceNeverLeavesFrozenSession(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		service, store, providers := newTestService(t, nil)
		providers[ProviderGoogle].identity = ExternalIdentity{
			Subject: "google", Email: "race@example.test", EmailVerified: true,
		}
		registered := register(t, service, ProviderGoogle)
		_, state := beginLogin(t, service, ProviderGoogle)
		boundary, _ := NewAccountAccessBoundary(store, nil)
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			_, _ = service.Callback(context.Background(), CallbackRequest{
				State: state, Code: "race",
			})
		}()
		go func() {
			defer wait.Done()
			_ = boundary.Freeze(context.Background(), registered.AccountID)
		}()
		wait.Wait()
		snapshot := store.Snapshot()
		for _, account := range snapshot.Accounts {
			if account.ID != registered.AccountID ||
				account.AccessState != AccountAccessFrozen {
				continue
			}
			for _, session := range snapshot.Sessions {
				if session.AccountID == account.ID && session.RevokedAt == nil {
					t.Fatal("race left an active session on a frozen account")
				}
			}
		}
	}
}
