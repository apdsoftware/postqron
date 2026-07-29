package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type registrationMemoryStore struct {
	mu            sync.Mutex
	accounts      map[string]registrationAccount
	tokens        map[string]registrationToken
	consents      []ConsentReceipt
	consentCount  int
	outboxCount   int
	securityCount int
}

type registrationAccount struct {
	ID           string
	Email        string
	PasswordHash string
	Verified     bool
}

type registrationToken struct {
	AccountID  string
	Hash       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

func newRegistrationMemoryStore() *registrationMemoryStore {
	return &registrationMemoryStore{
		accounts: make(map[string]registrationAccount),
		tokens:   make(map[string]registrationToken),
	}
}

func (store *registrationMemoryStore) RegisterPasswordAccount(
	_ context.Context,
	command RegisterPasswordAccountCommand,
) (RegisterPasswordAccountResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.accounts[command.Account.NormalizedEmail]; exists {
		return RegisterPasswordAccountResult{}, nil
	}
	store.accounts[command.Account.NormalizedEmail] = registrationAccount{
		ID:           command.Account.ID,
		Email:        command.Account.Email,
		PasswordHash: command.PasswordHash,
	}
	store.tokens[command.VerificationHash] = registrationToken{
		AccountID: command.Account.ID,
		Hash:      command.VerificationHash,
		CreatedAt: command.Now,
		ExpiresAt: command.VerificationExpiry,
	}
	store.consents = append([]ConsentReceipt(nil), command.Consents...)
	store.consentCount += len(command.Consents)
	store.outboxCount++
	store.securityCount++
	return RegisterPasswordAccountResult{Created: true}, nil
}

func (store *registrationMemoryStore) VerifyPasswordEmail(
	_ context.Context,
	command VerifyPasswordEmailCommand,
) (VerifyPasswordEmailResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	token, exists := store.tokens[command.TokenHash]
	if !exists || token.ConsumedAt != nil {
		return VerifyPasswordEmailResult{}, nil
	}
	if !command.Now.Before(token.ExpiresAt) {
		token.ConsumedAt = timePointer(command.Now)
		store.tokens[command.TokenHash] = token
		return VerifyPasswordEmailResult{Expired: true}, nil
	}
	for hash, current := range store.tokens {
		if current.AccountID == token.AccountID && current.ConsumedAt == nil {
			current.ConsumedAt = timePointer(command.Now)
			store.tokens[hash] = current
		}
	}
	for email, account := range store.accounts {
		if account.ID == token.AccountID {
			account.Verified = true
			store.accounts[email] = account
			break
		}
	}
	store.securityCount++
	return VerifyPasswordEmailResult{Verified: true}, nil
}

func (store *registrationMemoryStore) ResendPasswordVerification(
	_ context.Context,
	command ResendPasswordVerificationCommand,
) (ResendPasswordVerificationResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	account, exists := store.accounts[command.NormalizedEmail]
	if !exists || account.Verified {
		return ResendPasswordVerificationResult{}, nil
	}
	for _, token := range store.tokens {
		if token.AccountID == account.ID &&
			token.ConsumedAt == nil &&
			token.ExpiresAt.After(command.Now) &&
			!token.CreatedAt.Before(command.NotBefore) {
			return ResendPasswordVerificationResult{
				RateLimited: true,
				AccountID:   account.ID,
			}, nil
		}
	}
	for hash, token := range store.tokens {
		if token.AccountID == account.ID && token.ConsumedAt == nil {
			token.ConsumedAt = timePointer(command.Now)
			store.tokens[hash] = token
		}
	}
	store.tokens[command.VerificationHash] = registrationToken{
		AccountID: account.ID,
		Hash:      command.VerificationHash,
		CreatedAt: command.Now,
		ExpiresAt: command.VerificationExpiry,
	}
	store.securityCount++
	return ResendPasswordVerificationResult{
		Issued:    true,
		AccountID: account.ID,
	}, nil
}

func TestPasswordRegistrationCreatesDigestOnlyVerificationState(t *testing.T) {
	store := newRegistrationMemoryStore()
	service, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: store,
		Now:   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Register(
		context.Background(),
		"  user@example.test ",
		"correct horse battery staple",
		"correct horse battery staple",
		"IT",
		validConsents(),
	)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if !result.Created || result.Delivery == nil || result.Delivery.Token == "" {
		t.Fatalf("unexpected registration result: %+v", result)
	}
	account := store.accounts["user@example.test"]
	if account.Email != "user@example.test" || account.Verified {
		t.Fatalf("stored account = %+v", account)
	}
	if _, exists := store.tokens[tokenDigest(result.Delivery.Token)]; !exists {
		t.Fatal("verification token digest was not persisted")
	}
	if _, exists := store.tokens[result.Delivery.Token]; exists {
		t.Fatal("raw verification token was persisted")
	}
	if store.consentCount != 2 || store.outboxCount != 1 || store.securityCount != 1 {
		t.Fatalf(
			"ledger counts = consents:%d outbox:%d security:%d",
			store.consentCount,
			store.outboxCount,
			store.securityCount,
		)
	}
}

func TestPasswordRegistrationAcceptsCurrentProductionLegalVersions(t *testing.T) {
	store := newRegistrationMemoryStore()
	service, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: store,
		Now:   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	consents := []ConsentReceipt{
		{
			DocumentKey:   "terms_it",
			Version:       "0.2",
			DigestSHA256:  "2630d35d50853a781453dcad5b067725df2bcd0469e8bf37e9e109c660533f9b",
			Action:        ConsentAccepted,
			Purpose:       "contract",
			Locale:        "it-IT",
			Surface:       "signup",
			ControlTextID: "signup-terms-v1",
		},
		{
			DocumentKey:   "privacy_it",
			Version:       "0.1",
			DigestSHA256:  "e9bd3260ec45259f92e84592f988be9886423d69f3c8ddb84ab8f03a39b1a660",
			Action:        ConsentAcknowledged,
			Purpose:       "privacy_notice",
			Locale:        "it-IT",
			Surface:       "signup",
			ControlTextID: "signup-privacy-v1",
		},
	}

	result, err := service.Register(
		context.Background(),
		"production-legal@example.test",
		"correct horse battery staple",
		"correct horse battery staple",
		"IT",
		consents,
	)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if !result.Created || result.Delivery == nil || result.Delivery.Token == "" {
		t.Fatalf("unexpected registration result: %+v", result)
	}
	if _, exists := store.accounts["production-legal@example.test"]; !exists {
		t.Fatal("registration did not create the account")
	}
	if len(store.consents) != len(consents) {
		t.Fatalf("stored consents = %d, want %d", len(store.consents), len(consents))
	}
	for index, receipt := range consents {
		stored := store.consents[index]
		if stored.Version != receipt.Version || stored.DigestSHA256 != receipt.DigestSHA256 {
			t.Fatalf(
				"stored consent %d version/digest = %s/%s, want %s/%s",
				index,
				stored.Version,
				stored.DigestSHA256,
				receipt.Version,
				receipt.DigestSHA256,
			)
		}
	}
}

func TestPasswordRegistrationIsConcurrentAndIdempotent(t *testing.T) {
	store := newRegistrationMemoryStore()
	service, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: store,
		Now:   func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 8
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	created := make(chan bool, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.Register(
				context.Background(),
				"race@example.test",
				"correct horse battery staple",
				"correct horse battery staple",
				"IT",
				validConsents(),
			)
			if err != nil {
				errs <- err
				return
			}
			created <- result.Created
		}()
	}
	wg.Wait()
	close(errs)
	close(created)
	for err := range errs {
		t.Fatalf("Register() error = %v", err)
	}
	createdCount := 0
	for wasCreated := range created {
		if wasCreated {
			createdCount++
		}
	}
	if createdCount != 1 || len(store.accounts) != 1 || len(store.tokens) != 1 {
		t.Fatalf(
			"created=%d accounts=%d tokens=%d",
			createdCount,
			len(store.accounts),
			len(store.tokens),
		)
	}
}

func TestPasswordRegistrationVerificationAndResendRateLimit(t *testing.T) {
	store := newRegistrationMemoryStore()
	now := testNow
	service, err := NewPasswordRegistrationService(PasswordRegistrationConfig{
		Store: store,
		Now:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Register(
		context.Background(),
		"verify@example.test",
		"correct horse battery staple",
		"correct horse battery staple",
		"IT",
		validConsents(),
	)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := service.ResendVerification(context.Background(), "verify@example.test")
	if err != nil {
		t.Fatalf("ResendVerification() error = %v", err)
	}
	if delivery != nil {
		t.Fatal("resend should have been rate-limited without issuing a token")
	}
	if err := service.VerifyEmail(context.Background(), "wrong-token"); !errors.Is(err, ErrVerificationInvalid) {
		t.Fatalf("VerifyEmail(wrong) error = %v", err)
	}
	if err := service.VerifyEmail(context.Background(), result.Delivery.Token); err != nil {
		t.Fatalf("VerifyEmail() error = %v", err)
	}
	if err := service.VerifyEmail(context.Background(), result.Delivery.Token); !errors.Is(err, ErrVerificationInvalid) {
		t.Fatalf("VerifyEmail(replay) error = %v", err)
	}
	account := store.accounts["verify@example.test"]
	if !account.Verified {
		t.Fatal("account was not marked verified")
	}
	now = now.Add(2 * time.Minute)
	delivery, err = service.ResendVerification(context.Background(), "verify@example.test")
	if err != nil {
		t.Fatalf("ResendVerification(verified) error = %v", err)
	}
	if delivery != nil {
		t.Fatal("verified account should not receive another verification token")
	}
}

func TestMissingOAuthProviderDoesNotBlockConfiguredProvider(t *testing.T) {
	store := NewMemoryStore()
	sealer, err := NewAESGCMSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	providers := map[Provider]ProviderAdapter{
		ProviderGoogle: &fakeProvider{config: ProviderConfig{
			ClientID:         "google-client",
			AuthorizationURL: "https://google.example.test/oauth/authorize",
			RedirectURL:      "https://app.example.test/api/v1/auth/callback",
			Scopes:           []string{"openid", "email", "profile"},
		}},
	}
	service, err := NewService(Config{
		Store:     store,
		Sealer:    sealer,
		Providers: providers,
		Now:       func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Begin(context.Background(), BeginRequest{
		Provider:        ProviderGoogle,
		ContractCountry: "IT",
		Consents:        validConsents(),
	}); err != nil {
		t.Fatalf("Begin(google) error = %v", err)
	}
	_, err = service.Begin(context.Background(), BeginRequest{
		Provider: ProviderFacebook,
	})
	assertErrorCode(t, err, CodeUnsupportedProvider)
}
