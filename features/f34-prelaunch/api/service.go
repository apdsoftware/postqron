package prelaunch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

var (
	ErrConsentRequired  = errors.New("explicit access consent is required")
	ErrInvalidEmail     = errors.New("invalid email address")
	ErrInvalidPolicy    = errors.New("invalid consent policy version")
	ErrMarketingConsent = errors.New("marketing consent is not accepted here")
	ErrRateLimited      = errors.New("prelaunch access rate limit exceeded")
)

const (
	rateLimitCount  = 5
	rateLimitWindow = 10 * time.Minute
)

type Repository interface {
	Allow(context.Context, string, time.Time, int) (bool, error)
	Submit(context.Context, Submission) (SubmitResult, error)
}

type Service struct {
	repository Repository
	now        func() time.Time
	newID      func() (string, error)
}

func NewService(
	repository Repository,
	now func() time.Time,
) (*Service, error) {
	if repository == nil || now == nil {
		return nil, errors.New("prelaunch repository and clock are required")
	}
	return &Service{
		repository: repository,
		now:        now,
		newID:      randomID,
	}, nil
}

func (service *Service) Submit(
	ctx context.Context,
	input AccessRequest,
	clientIdentity string,
) (SubmitResult, error) {
	if !input.AccessConsent {
		return SubmitResult{}, ErrConsentRequired
	}
	if input.MarketingConsent {
		return SubmitResult{}, ErrMarketingConsent
	}
	if input.ConsentPolicyVersion != AccessConsentPolicyVersion {
		return SubmitResult{}, ErrInvalidPolicy
	}
	email, err := normalizeEmail(input.Email)
	if err != nil {
		return SubmitResult{}, err
	}

	now := service.now().UTC()
	allowed, err := service.repository.Allow(
		ctx,
		hashValue("f34-rate-v1", clientIdentity),
		now.Truncate(rateLimitWindow),
		rateLimitCount,
	)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("apply access rate limit: %w", err)
	}
	if !allowed {
		return SubmitResult{}, ErrRateLimited
	}

	requestID, err := service.newID()
	if err != nil {
		return SubmitResult{}, fmt.Errorf("create access request ID: %w", err)
	}
	locale := resolveLocale(input.Locale)
	occurredAt := now.Format(time.RFC3339Nano)
	submission := Submission{
		ID:        requestID,
		Email:     email,
		EmailHash: hashValue("f34-email-v1", email),
		Locale:    locale,
		Consent: ConsentProof{
			PolicyVersion:    AccessConsentPolicyVersion,
			ConsentedAt:      now,
			CollectionPoint:  "prelaunch_access_form",
			AccessConsent:    true,
			MarketingConsent: false,
		},
		Command: TransactionalEmailCommand{
			Event:           ConfirmationEvent,
			IdempotencyKey:  "prelaunch:" + requestID + ":confirmed",
			Channel:         "transactional",
			TemplateID:      ConfirmationTemplate,
			TemplateVersion: ConfirmationTemplateVersion,
			Recipient: EmailRecipient{
				ID: requestID, Email: email, Locale: locale,
			},
			Data:       EmailData{OccurredAt: occurredAt},
			OccurredAt: occurredAt,
		},
		RequestedAt: now,
	}
	result, err := service.repository.Submit(ctx, submission)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("store access request: %w", err)
	}
	return result, nil
}

func normalizeEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) == 0 || len(value) > 254 {
		return "", ErrInvalidEmail
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return "", ErrInvalidEmail
	}
	return value, nil
}

func resolveLocale(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if separator := strings.IndexAny(value, "-_"); separator >= 0 {
		value = value[:separator]
	}
	switch value {
	case "it", "es", "fr", "de":
		return value
	default:
		return "en"
	}
}

func hashValue(domain, value string) string {
	digest := sha256.Sum256([]byte(domain + "\x00" + strings.TrimSpace(value)))
	return hex.EncodeToString(digest[:])
}

func randomID() (string, error) {
	var value [18]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "pre_" + base64.RawURLEncoding.EncodeToString(value[:]), nil
}
