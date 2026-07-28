package auth

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"
)

func TestPostgresPasswordRegistrationIsAtomic(t *testing.T) {
	database, state := openPasswordTestDatabase(
		t,
		passwordSQLStep{
			kind:     "exec",
			contains: "INSERT INTO auth_accounts",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "INSERT INTO auth_password_credentials",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "INSERT INTO auth_consent_events",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "INSERT INTO auth_consent_events",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "INSERT INTO auth_outbox_events",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "INSERT INTO auth_password_tokens",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "'email.verification_requested'",
			affected: 1,
		},
	)
	store, err := NewPostgresPasswordRegistrationStore(database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RegisterPasswordAccount(context.Background(), RegisterPasswordAccountCommand{
		Account: Account{
			ID:              "account-1",
			Email:           "user@example.test",
			NormalizedEmail: "user@example.test",
			ContractCountry: "IT",
			CreatedAt:       testNow,
		},
		PasswordHash:        "$argon2id$v=19$m=65536,t=3,p=1$abcdefghijklmno$abcdefghijklmnopqrstuv",
		Consents:            validConsents(),
		CorrelationID:       "corr-1",
		OnboardingEventID:   "event-1",
		VerificationTokenID: "token-1",
		VerificationHash:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		VerificationExpiry:  testNow.Add(defaultVerificationTTL),
		SecurityEventID:     "security-1",
		Now:                 testNow,
	})
	if err != nil {
		t.Fatalf("RegisterPasswordAccount() error = %v", err)
	}
	assertPasswordSQLComplete(t, state)
}

func TestPostgresPasswordVerificationConsumesTokenAndMarksAccountVerified(t *testing.T) {
	database, state := openPasswordTestDatabase(
		t,
		passwordSQLStep{
			kind:     "query",
			contains: "FROM auth_password_tokens",
			columns:  []string{"id", "account_id", "expires_at", "consumed_at"},
			rows:     [][]driver.Value{{"token-1", "account-1", testNow.Add(time.Hour), nil}},
		},
		passwordSQLStep{
			kind:     "query",
			contains: "FROM auth_accounts",
			columns:  []string{"?column?"},
			rows:     [][]driver.Value{{false}},
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "UPDATE auth_password_tokens",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "UPDATE auth_accounts",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "'email.verified'",
			affected: 1,
		},
	)
	store, err := NewPostgresPasswordRegistrationStore(database)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.VerifyPasswordEmail(context.Background(), VerifyPasswordEmailCommand{
		TokenHash:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SecurityEventID: "security-verified",
		Now:             testNow,
	})
	if err != nil {
		t.Fatalf("VerifyPasswordEmail() error = %v", err)
	}
	if !result.Verified || result.Expired {
		t.Fatalf("unexpected verification result: %+v", result)
	}
	assertPasswordSQLComplete(t, state)
}

func TestPostgresPasswordResendVerificationRateLimit(t *testing.T) {
	database, state := openPasswordTestDatabase(
		t,
		passwordSQLStep{
			kind:     "query",
			contains: "FROM auth_accounts account",
			columns:  []string{"id", "?column?", "?column?"},
			rows:     [][]driver.Value{{"account-1", false, true}},
		},
		passwordSQLStep{
			kind:     "query",
			contains: "FROM auth_password_tokens",
			columns:  []string{"created_at"},
			rows:     [][]driver.Value{{testNow}},
		},
	)
	store, err := NewPostgresPasswordRegistrationStore(database)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ResendPasswordVerification(
		context.Background(),
		ResendPasswordVerificationCommand{
			NormalizedEmail:     "user@example.test",
			VerificationTokenID: "token-2",
			VerificationHash:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			VerificationExpiry:  testNow.Add(defaultVerificationTTL),
			SecurityEventID:     "security-2",
			Now:                 testNow,
			NotBefore:           testNow.Add(-time.Minute),
		},
	)
	if err != nil {
		t.Fatalf("ResendPasswordVerification() error = %v", err)
	}
	if !result.RateLimited || result.Issued {
		t.Fatalf("unexpected resend result: %+v", result)
	}
	assertPasswordSQLComplete(t, state)
}
