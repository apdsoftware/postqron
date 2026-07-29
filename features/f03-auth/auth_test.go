package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

type fakeProvider struct {
	config       ProviderConfig
	identity     ExternalIdentity
	exchangeErrs []error
	exchanges    []ExchangeRequest
	revoked      []string
	revokeErr    error
}

func (provider *fakeProvider) Config() ProviderConfig {
	return provider.config
}

func (provider *fakeProvider) Exchange(
	_ context.Context,
	request ExchangeRequest,
) (ExternalIdentity, error) {
	provider.exchanges = append(provider.exchanges, request)
	if len(provider.exchangeErrs) > 0 {
		err := provider.exchangeErrs[0]
		provider.exchangeErrs = provider.exchangeErrs[1:]
		if err != nil {
			return ExternalIdentity{}, err
		}
	}
	return provider.identity, nil
}

func (provider *fakeProvider) Revoke(_ context.Context, token string) error {
	provider.revoked = append(provider.revoked, token)
	return provider.revokeErr
}

func TestEveryProviderUsesStatePKCEAndCreatesAtomicOnboarding(t *testing.T) {
	for _, selectedProvider := range SupportedProviders {
		t.Run(string(selectedProvider), func(t *testing.T) {
			service, store, providers := newTestService(t, nil)
			providers[selectedProvider].identity = ExternalIdentity{
				Subject:         "subject-" + string(selectedProvider),
				Email:           string(selectedProvider) + "@example.test",
				EmailVerified:   true,
				DisplayName:     "Ada Lovelace",
				RevocationToken: "provider-secret",
			}

			authorization, state := beginRegistration(t, service, selectedProvider)
			query := mustParseURL(t, authorization.URL).Query()
			if query.Get("state") == "" {
				t.Fatal("authorization URL does not contain state")
			}
			if query.Get("code_challenge_method") != "S256" {
				t.Fatalf("code_challenge_method = %q", query.Get("code_challenge_method"))
			}
			if len(query.Get("code_challenge")) != 43 {
				t.Fatalf("unexpected PKCE challenge length: %d", len(query.Get("code_challenge")))
			}
			if query.Get("nonce") == "" {
				t.Fatal("authorization URL does not contain OIDC nonce")
			}

			result, err := service.Callback(context.Background(), CallbackRequest{
				State: state,
				Code:  "one-time-code",
			})
			if err != nil {
				t.Fatalf("Callback() error = %v", err)
			}
			if !result.Onboarding || result.Linked || result.SessionToken == "" {
				t.Fatalf("unexpected callback result: %+v", result)
			}

			exchange := providers[selectedProvider].exchanges[0]
			if pkceChallenge(exchange.PKCEVerifier) != query.Get("code_challenge") {
				t.Fatal("provider exchange did not receive the verifier matching the challenge")
			}
			if exchange.ExpectedNonce != query.Get("nonce") {
				t.Fatal("provider exchange did not receive the expected OIDC nonce")
			}

			snapshot := store.Snapshot()
			if len(snapshot.Accounts) != 1 ||
				len(snapshot.Identities) != 1 ||
				len(snapshot.Sessions) != 1 ||
				len(snapshot.Consents) != 2 ||
				len(snapshot.Outbox) != 1 {
				t.Fatalf("authentication was not finalized atomically: %+v", snapshot)
			}
			if snapshot.Attempts[0].StateHash == state {
				t.Fatal("raw OAuth state was persisted")
			}
			if snapshot.Sessions[0].TokenHash == result.SessionToken {
				t.Fatal("raw session token was persisted")
			}
			if string(snapshot.Identities[0].RevocationTokenCiphertext) == "provider-secret" {
				t.Fatal("raw provider revocation token was persisted")
			}
			if snapshot.Outbox[0].Type != OnboardingEventType ||
				snapshot.Outbox[0].Version != OnboardingEventVersion {
				t.Fatalf("unexpected onboarding event: %+v", snapshot.Outbox[0])
			}
			var payload map[string]any
			if err := json.Unmarshal(snapshot.Outbox[0].Payload, &payload); err != nil {
				t.Fatalf("decode onboarding payload: %v", err)
			}
			if payload["requested_role"] != "owner" ||
				payload["contract_country"] != "IT" ||
				payload["idempotency_key"] != "auth-account:"+result.AccountID {
				t.Fatalf("unexpected onboarding payload: %v", payload)
			}

			_, err = service.Callback(context.Background(), CallbackRequest{
				State: state,
				Code:  "replayed-code",
			})
			assertErrorCode(t, err, CodeInvalidState)
			if len(store.Snapshot().Sessions) != 1 {
				t.Fatal("replayed callback created another session")
			}
		})
	}
}

func TestVerifiedEmailCollisionRequiresExplicitLinking(t *testing.T) {
	service, store, providers := newTestService(t, nil)
	providers[ProviderGoogle].identity = ExternalIdentity{
		Subject:       "google-1",
		Email:         "User@Example.test",
		EmailVerified: true,
		DisplayName:   "Existing User",
	}
	first := register(t, service, ProviderGoogle)

	providers[ProviderFacebook].identity = ExternalIdentity{
		Subject:       "facebook-1",
		Email:         "user@example.test",
		EmailVerified: true,
		DisplayName:   "Existing User",
	}
	_, state := beginRegistration(t, service, ProviderFacebook)
	_, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "facebook-code",
	})
	assertErrorCode(t, err, CodeLinkingRequired)
	snapshot := store.Snapshot()
	if len(snapshot.Accounts) != 1 ||
		len(snapshot.Identities) != 1 ||
		len(snapshot.Sessions) != 1 {
		t.Fatalf("email collision created partial state: %+v", snapshot)
	}

	linkAuthorization, err := service.BeginLink(context.Background(), BeginLinkRequest{
		Provider:     ProviderFacebook,
		ReturnTo:     "/app/account",
		SessionToken: first.SessionToken,
	})
	if err != nil {
		t.Fatalf("BeginLink() error = %v", err)
	}
	linkState := mustParseURL(t, linkAuthorization.URL).Query().Get("state")
	linked, err := service.Callback(context.Background(), CallbackRequest{
		State: linkState,
		Code:  "facebook-link-code",
	})
	if err != nil {
		t.Fatalf("link Callback() error = %v", err)
	}
	if !linked.Linked || linked.AccountID != first.AccountID || linked.SessionToken != "" {
		t.Fatalf("unexpected link result: %+v", linked)
	}
	snapshot = store.Snapshot()
	if len(snapshot.Accounts) != 1 ||
		len(snapshot.Identities) != 2 ||
		len(snapshot.Sessions) != 1 {
		t.Fatalf("explicit link duplicated account or session: %+v", snapshot)
	}
}

func TestLinkingCannotTakeOverIdentityOwnedByAnotherAccount(t *testing.T) {
	service, store, providers := newTestService(t, nil)
	providers[ProviderGoogle].identity = ExternalIdentity{
		Subject:       "google-a",
		Email:         "a@example.test",
		EmailVerified: true,
	}
	accountA := register(t, service, ProviderGoogle)
	providers[ProviderApple].identity = ExternalIdentity{
		Subject:       "apple-b",
		Email:         "b@example.test",
		EmailVerified: true,
	}
	accountB := register(t, service, ProviderApple)

	authorization, err := service.BeginLink(context.Background(), BeginLinkRequest{
		Provider:     ProviderApple,
		SessionToken: accountA.SessionToken,
	})
	if err != nil {
		t.Fatalf("BeginLink() error = %v", err)
	}
	_, err = service.Callback(context.Background(), CallbackRequest{
		State: mustParseURL(t, authorization.URL).Query().Get("state"),
		Code:  "takeover-attempt",
	})
	assertErrorCode(t, err, CodeIdentityConflict)
	snapshot := store.Snapshot()
	if len(snapshot.Accounts) != 2 || len(snapshot.Identities) != 2 {
		t.Fatalf("takeover attempt changed identities: %+v", snapshot)
	}
	if _, err := service.Authenticate(context.Background(), accountB.SessionToken); err != nil {
		t.Fatalf("other account session was affected: %v", err)
	}
}

func TestRetryableProviderFailureCreatesNoPartialSession(t *testing.T) {
	service, store, providers := newTestService(t, nil)
	providers[ProviderLinkedIn].identity = ExternalIdentity{
		Subject:       "linkedin-1",
		Email:         "retry@example.test",
		EmailVerified: true,
	}
	providers[ProviderLinkedIn].exchangeErrs = []error{
		&ProviderError{Code: "timeout", Retryable: true, Cause: errors.New("timeout")},
		nil,
	}
	_, state := beginRegistration(t, service, ProviderLinkedIn)
	_, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "retryable-code",
	})
	assertErrorCode(t, err, CodeProviderUnavailable)
	snapshot := store.Snapshot()
	if len(snapshot.Accounts) != 0 || len(snapshot.Sessions) != 0 ||
		snapshot.Attempts[0].Status != AttemptPending {
		t.Fatalf("retryable error left partial state: %+v", snapshot)
	}

	result, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "retryable-code",
	})
	if err != nil {
		t.Fatalf("retry Callback() error = %v", err)
	}
	if result.SessionToken == "" || len(store.Snapshot().Accounts) != 1 {
		t.Fatal("retry did not complete authentication")
	}
}

func TestTransactionFailureRollsBackAndAllowsRetry(t *testing.T) {
	baseStore := NewMemoryStore()
	failingStore := &failOnceStore{TransactionStore: baseStore, fail: true}
	service, _, providers := newTestService(t, failingStore)
	providers[ProviderGoogle].identity = ExternalIdentity{
		Subject:       "google-atomic",
		Email:         "atomic@example.test",
		EmailVerified: true,
	}
	_, state := beginRegistration(t, service, ProviderGoogle)
	_, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "atomic-code",
	})
	assertErrorCode(t, err, CodeInternal)
	snapshot := baseStore.Snapshot()
	if len(snapshot.Accounts) != 0 || len(snapshot.Sessions) != 0 ||
		snapshot.Attempts[0].Status != AttemptPending {
		t.Fatalf("transaction failure left partial state: %+v", snapshot)
	}
	if _, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "atomic-code",
	}); err != nil {
		t.Fatalf("retry after transaction failure: %v", err)
	}
}

func TestLogoutGlobalRevocationAndProviderUnlink(t *testing.T) {
	service, store, providers := newTestService(t, nil)
	providers[ProviderGoogle].identity = ExternalIdentity{
		Subject:       "google-session",
		Email:         "sessions@example.test",
		EmailVerified: true,
	}
	first := register(t, service, ProviderGoogle)

	_, secondState := beginLogin(t, service, ProviderGoogle)
	second, err := service.Callback(context.Background(), CallbackRequest{
		State: secondState,
		Code:  "second-login",
	})
	if err != nil {
		t.Fatalf("second login: %v", err)
	}
	if err := service.Logout(context.Background(), first.SessionToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	_, err = service.Authenticate(context.Background(), first.SessionToken)
	assertErrorCode(t, err, CodeUnauthenticated)
	if _, err := service.Authenticate(context.Background(), second.SessionToken); err != nil {
		t.Fatalf("logout revoked another session: %v", err)
	}

	providers[ProviderFacebook].identity = ExternalIdentity{
		Subject:         "facebook-session",
		Email:           "sessions@example.test",
		EmailVerified:   true,
		RevocationToken: "facebook-revoke-token",
	}
	linkAuthorization, err := service.BeginLink(context.Background(), BeginLinkRequest{
		Provider:     ProviderFacebook,
		SessionToken: second.SessionToken,
	})
	if err != nil {
		t.Fatalf("BeginLink() error = %v", err)
	}
	if _, err := service.Callback(context.Background(), CallbackRequest{
		State: mustParseURL(t, linkAuthorization.URL).Query().Get("state"),
		Code:  "link-facebook",
	}); err != nil {
		t.Fatalf("link callback: %v", err)
	}
	if err := service.UnlinkProvider(
		context.Background(),
		second.SessionToken,
		ProviderFacebook,
	); err != nil {
		t.Fatalf("UnlinkProvider() error = %v", err)
	}
	if len(providers[ProviderFacebook].revoked) != 1 ||
		providers[ProviderFacebook].revoked[0] != "facebook-revoke-token" {
		t.Fatalf("provider token was not revoked: %v", providers[ProviderFacebook].revoked)
	}
	if len(store.Snapshot().Identities) != 1 {
		t.Fatal("provider identity was not removed")
	}
	err = service.UnlinkProvider(context.Background(), second.SessionToken, ProviderGoogle)
	assertErrorCode(t, err, CodeLastProvider)

	if err := service.RevokeAllSessions(context.Background(), second.SessionToken); err != nil {
		t.Fatalf("RevokeAllSessions() error = %v", err)
	}
	_, err = service.Authenticate(context.Background(), second.SessionToken)
	assertErrorCode(t, err, CodeUnauthenticated)
	for _, session := range store.Snapshot().Sessions {
		if session.RevokedAt == nil {
			t.Fatal("global revocation left an active session")
		}
	}
}

func TestRegistrationRequiresVersionedLegalReceiptsAndItalianCountry(t *testing.T) {
	service, store, providers := newTestService(t, nil)
	providers[ProviderGoogle].identity = ExternalIdentity{
		Subject:       "google-consent",
		Email:         "consent@example.test",
		EmailVerified: true,
	}
	authorization, err := service.Begin(context.Background(), BeginRequest{
		Provider: ProviderGoogle,
		ReturnTo: "/app",
	})
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	_, err = service.Callback(context.Background(), CallbackRequest{
		State: mustParseURL(t, authorization.URL).Query().Get("state"),
		Code:  "missing-consent",
	})
	assertErrorCode(t, err, CodeCountryNotSupported)
	if len(store.Snapshot().Accounts) != 0 {
		t.Fatal("invalid registration created an account")
	}

	invalid := validConsents()
	invalid[0].DigestSHA256 = "not-a-digest"
	_, err = service.Begin(context.Background(), BeginRequest{
		Provider:        ProviderGoogle,
		ContractCountry: "IT",
		Consents:        invalid,
	})
	assertErrorCode(t, err, CodeInvalidConsent)
}

func TestLegalVersionRequiresCanonicalMajorMinor(t *testing.T) {
	tests := []struct {
		name    string
		version string
		valid   bool
	}{
		{name: "zero major", version: "0.1", valid: true},
		{name: "positive major", version: "1.0", valid: true},
		{name: "multi digit components", version: "12.34", valid: true},
		{name: "minor leading zero remains valid", version: "1.01", valid: true},
		{name: "empty", version: "", valid: false},
		{name: "missing minor", version: "1", valid: false},
		{name: "missing major", version: ".1", valid: false},
		{name: "empty minor", version: "1.", valid: false},
		{name: "major leading zero", version: "01.2", valid: false},
		{name: "extra component", version: "1.2.3", valid: false},
		{name: "prefix", version: "v1.2", valid: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipts := validConsents()
			receipts[0].Version = test.version
			err := validateConsentShape(receipts)
			if test.valid && err != nil {
				t.Fatalf("validateConsentShape() error = %v", err)
			}
			if !test.valid {
				assertErrorCode(t, err, CodeInvalidConsent)
			}
		})
	}
}

func newTestService(
	t *testing.T,
	store TransactionStore,
) (*Service, *MemoryStore, map[Provider]*fakeProvider) {
	t.Helper()
	memoryStore, ok := store.(*MemoryStore)
	if store == nil {
		memoryStore = NewMemoryStore()
		store = memoryStore
	} else if !ok {
		if wrapper, wrapperOK := store.(*failOnceStore); wrapperOK {
			memoryStore, _ = wrapper.TransactionStore.(*MemoryStore)
		}
	}
	sealer, err := NewAESGCMSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewAESGCMSealer() error = %v", err)
	}
	providers := make(map[Provider]*fakeProvider)
	adapters := make(map[Provider]ProviderAdapter)
	for _, provider := range SupportedProviders {
		scopes := []string{"email"}
		switch provider {
		case ProviderGoogle, ProviderLinkedIn:
			scopes = []string{"openid", "email", "profile"}
		case ProviderFacebook:
			scopes = []string{"email", "public_profile"}
		case ProviderApple:
			scopes = []string{"email", "name"}
		}
		fake := &fakeProvider{config: ProviderConfig{
			ClientID:         "client-" + string(provider),
			AuthorizationURL: "https://" + string(provider) + ".example.test/oauth/authorize",
			RedirectURL:      "https://app.example.test/api/v1/auth/callback",
			Scopes:           scopes,
		}}
		providers[provider] = fake
		adapters[provider] = fake
	}
	service, err := NewService(Config{
		Store:     store,
		Sealer:    sealer,
		Providers: adapters,
		Now:       func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service, memoryStore, providers
}

func beginRegistration(
	t *testing.T,
	service *Service,
	provider Provider,
) (Authorization, string) {
	t.Helper()
	authorization, err := service.Begin(context.Background(), BeginRequest{
		Provider:        provider,
		ReturnTo:        "/app/onboarding",
		ContractCountry: "IT",
		Consents:        validConsents(),
	})
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return authorization, mustParseURL(t, authorization.URL).Query().Get("state")
}

func beginLogin(
	t *testing.T,
	service *Service,
	provider Provider,
) (Authorization, string) {
	t.Helper()
	authorization, err := service.Begin(context.Background(), BeginRequest{
		Provider: provider,
		ReturnTo: "/app",
	})
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	return authorization, mustParseURL(t, authorization.URL).Query().Get("state")
}

func register(t *testing.T, service *Service, provider Provider) CallbackResult {
	t.Helper()
	_, state := beginRegistration(t, service, provider)
	result, err := service.Callback(context.Background(), CallbackRequest{
		State: state,
		Code:  "registration-code",
	})
	if err != nil {
		t.Fatalf("registration Callback() error = %v", err)
	}
	return result
}

func validConsents() []ConsentReceipt {
	return []ConsentReceipt{
		{
			DocumentKey:   "terms_it",
			Version:       "1.0",
			DigestSHA256:  strings.Repeat("a", 64),
			Action:        ConsentAccepted,
			Purpose:       "contract",
			Locale:        "it-IT",
			Surface:       "signup",
			ControlTextID: "signup-terms-v1",
		},
		{
			DocumentKey:   "privacy_it",
			Version:       "1.0",
			DigestSHA256:  strings.Repeat("b", 64),
			Action:        ConsentAcknowledged,
			Purpose:       "privacy_notice",
			Locale:        "it-IT",
			Surface:       "signup",
			ControlTextID: "signup-privacy-v1",
		},
	}
}

func mustParseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", value, err)
	}
	return parsed
}

func assertErrorCode(t *testing.T, err error, expected string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error, got nil", expected)
	}
	code, _, _ := ErrorDetails(err)
	if code != expected {
		t.Fatalf("error code = %s, want %s (error: %v)", code, expected, err)
	}
}

type failOnceStore struct {
	TransactionStore
	fail bool
}

func (store *failOnceStore) Transaction(
	ctx context.Context,
	operation func(Transaction) error,
) error {
	if store.fail {
		store.fail = false
		return errors.New("injected transaction failure")
	}
	return store.TransactionStore.Transaction(ctx, operation)
}
