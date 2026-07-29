package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	defaultVerificationTTL            = 24 * time.Hour
	defaultVerificationResendInterval = 60 * time.Second
)

var (
	ErrVerificationInvalid = errors.New("email verification token is invalid")
	ErrVerificationExpired = errors.New("email verification token expired")
)

type PasswordRegistrationConfig struct {
	Store                  PasswordRegistrationStore
	Now                    func() time.Time
	VerificationTTL        time.Duration
	VerificationRetryAfter time.Duration
}

type PasswordRegistrationService struct {
	store                  PasswordRegistrationStore
	now                    func() time.Time
	verificationTTL        time.Duration
	verificationRetryAfter time.Duration
}

type PasswordRegistrationStore interface {
	RegisterPasswordAccount(
		context.Context,
		RegisterPasswordAccountCommand,
	) (RegisterPasswordAccountResult, error)
	VerifyPasswordEmail(
		context.Context,
		VerifyPasswordEmailCommand,
	) (VerifyPasswordEmailResult, error)
	ResendPasswordVerification(
		context.Context,
		ResendPasswordVerificationCommand,
	) (ResendPasswordVerificationResult, error)
}

type RegisterPasswordAccountCommand struct {
	Account             Account
	PasswordHash        string
	Consents            []ConsentReceipt
	CorrelationID       string
	OnboardingEventID   string
	VerificationTokenID string
	VerificationHash    string
	VerificationExpiry  time.Time
	SecurityEventID     string
	Now                 time.Time
}

type RegisterPasswordAccountResult struct {
	Created bool
}

type VerifyPasswordEmailCommand struct {
	TokenHash       string
	SecurityEventID string
	Now             time.Time
}

type VerifyPasswordEmailResult struct {
	Verified bool
	Expired  bool
}

type ResendPasswordVerificationCommand struct {
	Email               string
	NormalizedEmail     string
	VerificationTokenID string
	VerificationHash    string
	VerificationExpiry  time.Time
	SecurityEventID     string
	Now                 time.Time
	NotBefore           time.Time
}

type ResendPasswordVerificationResult struct {
	Issued      bool
	RateLimited bool
	AccountID   string
}

type PasswordRegistrationResult struct {
	Created  bool
	Delivery *VerificationDelivery
}

type VerificationDelivery struct {
	AccountID string
	Email     string
	Token     string
	ExpiresAt time.Time
}

func NewPasswordRegistrationService(
	config PasswordRegistrationConfig,
) (*PasswordRegistrationService, error) {
	if config.Store == nil {
		return nil, errors.New("password registration store is required")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.VerificationTTL == 0 {
		config.VerificationTTL = defaultVerificationTTL
	}
	if config.VerificationRetryAfter == 0 {
		config.VerificationRetryAfter = defaultVerificationResendInterval
	}
	if config.VerificationTTL <= 0 || config.VerificationRetryAfter <= 0 {
		return nil, errors.New("password registration durations must be positive")
	}
	return &PasswordRegistrationService{
		store:                  config.Store,
		now:                    config.Now,
		verificationTTL:        config.VerificationTTL,
		verificationRetryAfter: config.VerificationRetryAfter,
	}, nil
}

func (service *PasswordRegistrationService) Register(
	ctx context.Context,
	email, password, confirmation, contractCountry string,
	consents []ConsentReceipt,
) (PasswordRegistrationResult, error) {
	normalized, err := normalizePasswordEmail(email)
	if err != nil {
		return PasswordRegistrationResult{}, err
	}
	if password != confirmation {
		return PasswordRegistrationResult{}, ErrPasswordConfirmation
	}
	if err := validatePassword(password); err != nil {
		return PasswordRegistrationResult{}, ErrPasswordPolicy
	}
	if err := validateConsentShape(consents); err != nil {
		return PasswordRegistrationResult{}, err
	}
	if err := validateRegistrationRequirements(strings.ToUpper(strings.TrimSpace(contractCountry)), consents); err != nil {
		return PasswordRegistrationResult{}, err
	}
	passwordHash, err := HashPassword(password, DefaultPasswordParameters())
	if err != nil {
		return PasswordRegistrationResult{}, err
	}
	accountID, err := randomToken(18)
	if err != nil {
		return PasswordRegistrationResult{}, err
	}
	correlationID, err := randomToken(18)
	if err != nil {
		return PasswordRegistrationResult{}, err
	}
	token, err := randomToken(32)
	if err != nil {
		return PasswordRegistrationResult{}, err
	}
	tokenID, err := randomToken(18)
	if err != nil {
		return PasswordRegistrationResult{}, err
	}
	now := service.now().UTC()
	command := RegisterPasswordAccountCommand{
		Account: Account{
			ID:              accountID,
			Email:           normalized,
			NormalizedEmail: normalized,
			DisplayName:     "",
			ContractCountry: "IT",
			CreatedAt:       now,
		},
		PasswordHash:        passwordHash,
		Consents:            append([]ConsentReceipt(nil), consents...),
		CorrelationID:       correlationID,
		OnboardingEventID:   secureEventID(),
		VerificationTokenID: tokenID,
		VerificationHash:    tokenDigest(token),
		VerificationExpiry:  now.Add(service.verificationTTL),
		SecurityEventID:     secureEventID(),
		Now:                 now,
	}
	result, err := service.store.RegisterPasswordAccount(ctx, command)
	if err != nil {
		if errors.Is(err, errStoreConflict) {
			return PasswordRegistrationResult{}, nil
		}
		return PasswordRegistrationResult{}, err
	}
	if !result.Created {
		return PasswordRegistrationResult{}, nil
	}
	return PasswordRegistrationResult{
		Created: true,
		Delivery: &VerificationDelivery{
			AccountID: accountID,
			Email:     normalized,
			Token:     token,
			ExpiresAt: command.VerificationExpiry,
		},
	}, nil
}

func (service *PasswordRegistrationService) VerifyEmail(
	ctx context.Context,
	token string,
) error {
	if strings.TrimSpace(token) == "" {
		return ErrVerificationInvalid
	}
	result, err := service.store.VerifyPasswordEmail(ctx, VerifyPasswordEmailCommand{
		TokenHash:       tokenDigest(token),
		SecurityEventID: secureEventID(),
		Now:             service.now().UTC(),
	})
	if err != nil {
		return err
	}
	if result.Expired {
		return ErrVerificationExpired
	}
	if !result.Verified {
		return ErrVerificationInvalid
	}
	return nil
}

func (service *PasswordRegistrationService) ResendVerification(
	ctx context.Context,
	email string,
) (*VerificationDelivery, error) {
	normalized, err := normalizePasswordEmail(email)
	if err != nil {
		return nil, err
	}
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	result, err := service.store.ResendPasswordVerification(
		ctx,
		ResendPasswordVerificationCommand{
			Email:               normalized,
			NormalizedEmail:     normalized,
			VerificationTokenID: secureEventID(),
			VerificationHash:    tokenDigest(token),
			VerificationExpiry:  now.Add(service.verificationTTL),
			SecurityEventID:     secureEventID(),
			Now:                 now,
			NotBefore:           now.Add(-service.verificationRetryAfter),
		},
	)
	if err != nil {
		return nil, err
	}
	if !result.Issued {
		return nil, nil
	}
	return &VerificationDelivery{
		AccountID: result.AccountID,
		Email:     normalized,
		Token:     token,
		ExpiresAt: now.Add(service.verificationTTL),
	}, nil
}

func buildOnboardingPayload(account Account, occurredAt time.Time) ([]byte, error) {
	return json.Marshal(struct {
		AccountID            string    `json:"account_id"`
		Email                string    `json:"email"`
		DisplayName          string    `json:"display_name,omitempty"`
		ContractCountry      string    `json:"contract_country"`
		PersonalWorkspaceKey string    `json:"personal_workspace_key"`
		RequestedRole        string    `json:"requested_role"`
		IdempotencyKey       string    `json:"idempotency_key"`
		OccurredAt           time.Time `json:"occurred_at"`
	}{
		AccountID:            account.ID,
		Email:                account.Email,
		DisplayName:          account.DisplayName,
		ContractCountry:      account.ContractCountry,
		PersonalWorkspaceKey: "personal:" + account.ID,
		RequestedRole:        "owner",
		IdempotencyKey:       "auth-account:" + account.ID,
		OccurredAt:           occurredAt,
	})
}
