package pwa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	maxTitleLength = 80
	maxBodyLength  = 240
)

type Service struct {
	repository  Repository
	gateway     Gateway
	now         func() time.Time
	random      func([]byte) error
	lease       time.Duration
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

type ServiceOption func(*Service)

func WithClock(clock func() time.Time) ServiceOption {
	return func(service *Service) { service.now = clock }
}

func WithRandom(random func([]byte) error) ServiceOption {
	return func(service *Service) { service.random = random }
}

func WithRetryPolicy(maxAttempts int, base, maximum time.Duration) ServiceOption {
	return func(service *Service) {
		service.maxAttempts = maxAttempts
		service.baseBackoff = base
		service.maxBackoff = maximum
	}
}

func WithLease(lease time.Duration) ServiceOption {
	return func(service *Service) { service.lease = lease }
}

func NewService(repository Repository, gateway Gateway, options ...ServiceOption) (*Service, error) {
	if repository == nil || gateway == nil {
		return nil, fmt.Errorf("%w: repository and gateway are required", ErrInvalidArgument)
	}
	service := &Service{
		repository: repository,
		gateway:    gateway,
		now:        time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
		lease:       30 * time.Second,
		maxAttempts: 6,
		baseBackoff: 5 * time.Second,
		maxBackoff:  15 * time.Minute,
	}
	for _, option := range options {
		option(service)
	}
	if service.now == nil || service.random == nil || service.lease <= 0 ||
		service.maxAttempts < 1 || service.baseBackoff <= 0 ||
		service.maxBackoff < service.baseBackoff {
		return nil, fmt.Errorf("%w: invalid service options", ErrInvalidArgument)
	}
	return service, nil
}

func (service *Service) Subscribe(
	ctx context.Context,
	input SubscriptionInput,
) (Subscription, bool, error) {
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.Endpoint = strings.TrimSpace(input.Endpoint)
	input.P256DH = strings.TrimSpace(input.P256DH)
	input.Auth = strings.TrimSpace(input.Auth)
	if err := validateSubscription(input); err != nil {
		return Subscription{}, false, err
	}
	now := service.now().UTC()
	if input.ExpirationTime != nil {
		expiration := input.ExpirationTime.UTC()
		if !expiration.After(now) {
			return Subscription{}, false, fmt.Errorf(
				"%w: subscription is already expired",
				ErrInvalidArgument,
			)
		}
		input.ExpirationTime = &expiration
	}
	subscription := Subscription{
		ID:             stableID("subscription", input.AccountID, input.Endpoint),
		AccountID:      input.AccountID,
		Endpoint:       input.Endpoint,
		P256DH:         input.P256DH,
		Auth:           input.Auth,
		ExpirationTime: input.ExpirationTime,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return service.repository.UpsertSubscription(ctx, subscription)
}

func (service *Service) Revoke(
	ctx context.Context,
	accountID, endpoint string,
) (bool, error) {
	accountID = strings.TrimSpace(accountID)
	endpoint = strings.TrimSpace(endpoint)
	if accountID == "" || !validEndpoint(endpoint) {
		return false, fmt.Errorf("%w: account and endpoint are required", ErrInvalidArgument)
	}
	return service.repository.RevokeSubscription(
		ctx,
		accountID,
		endpoint,
		service.now().UTC(),
	)
}

// ConsumeEvent fans one durable upstream event out to every active device.
// The repository's event ledger and (event, subscription) uniqueness make
// re-delivery harmless.
func (service *Service) ConsumeEvent(ctx context.Context, event PushEvent) (int, error) {
	event = normalizeEvent(event)
	if err := validateEvent(event); err != nil {
		return 0, err
	}
	now := service.now().UTC()
	subscriptions, err := service.repository.ActiveSubscriptions(
		ctx,
		event.RecipientAccountIDs,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf("list active push subscriptions: %w", err)
	}
	return service.repository.EnqueueDeliveries(ctx, event, subscriptions, now)
}

func (service *Service) Dispatch(ctx context.Context) (bool, error) {
	now := service.now().UTC()
	leaseToken, err := service.leaseToken()
	if err != nil {
		return false, err
	}
	delivery, subscription, found, err := service.repository.ClaimDelivery(
		ctx,
		now,
		now.Add(service.lease),
		leaseToken,
	)
	if err != nil || !found {
		return found, err
	}
	if subscription.ExpirationTime != nil && !subscription.ExpirationTime.After(now) {
		if err := service.repository.ExpireSubscription(ctx, subscription.ID, now); err != nil {
			return true, fmt.Errorf("expire push subscription: %w", err)
		}
		if err := service.repository.MarkFailed(ctx, delivery.ID, leaseToken, now); err != nil {
			return true, fmt.Errorf("fail expired push delivery: %w", err)
		}
		return true, nil
	}

	result, sendErr := service.gateway.Send(ctx, subscription, delivery.Event)
	switch {
	case sendErr == nil && result.StatusCode >= 200 && result.StatusCode < 300:
		err = service.repository.MarkDelivered(ctx, delivery.ID, leaseToken, now)
	case result.StatusCode == 404 || result.StatusCode == 410:
		if expireErr := service.repository.ExpireSubscription(
			ctx,
			subscription.ID,
			now,
		); expireErr != nil {
			return true, fmt.Errorf("expire rejected push subscription: %w", expireErr)
		}
		err = service.repository.MarkFailed(ctx, delivery.ID, leaseToken, now)
	case retryablePush(result.StatusCode, sendErr):
		attempt := delivery.AttemptCount + 1
		if attempt >= service.maxAttempts {
			err = service.repository.MarkFailed(ctx, delivery.ID, leaseToken, now)
		} else {
			err = service.repository.MarkRetry(
				ctx,
				delivery.ID,
				leaseToken,
				attempt,
				now.Add(service.backoff(attempt)),
			)
		}
	default:
		err = service.repository.MarkFailed(ctx, delivery.ID, leaseToken, now)
	}
	if err != nil {
		return true, fmt.Errorf("complete push delivery: %w", err)
	}
	return true, nil
}

func validateSubscription(input SubscriptionInput) error {
	if input.AccountID == "" || len(input.AccountID) > 160 ||
		!validEndpoint(input.Endpoint) || len(input.Endpoint) > 2048 ||
		!validWebPushKey(input.P256DH, 65) ||
		!validWebPushKey(input.Auth, 16) {
		return fmt.Errorf("%w: invalid web push subscription", ErrInvalidArgument)
	}
	return nil
}

func validEndpoint(endpoint string) bool {
	parsed, err := url.ParseRequestURI(endpoint)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.Fragment == ""
}

func validWebPushKey(value string, decodedLength int) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(decoded) == decodedLength
}

func normalizeEvent(event PushEvent) PushEvent {
	event.EventID = strings.TrimSpace(event.EventID)
	event.WorkspaceID = strings.TrimSpace(event.WorkspaceID)
	event.ResourceID = strings.TrimSpace(event.ResourceID)
	event.Title = strings.TrimSpace(event.Title)
	event.Body = strings.TrimSpace(event.Body)
	event.ActionURL = strings.TrimSpace(event.ActionURL)
	event.OccurredAt = event.OccurredAt.UTC()
	recipients := make([]string, 0, len(event.RecipientAccountIDs))
	for _, accountID := range event.RecipientAccountIDs {
		if accountID = strings.TrimSpace(accountID); accountID != "" {
			recipients = append(recipients, accountID)
		}
	}
	slices.Sort(recipients)
	event.RecipientAccountIDs = slices.Compact(recipients)
	return event
}

func validateEvent(event PushEvent) error {
	if event.EventID == "" || len(event.EventID) > 160 || !event.Kind.Valid() ||
		len(event.RecipientAccountIDs) == 0 || event.WorkspaceID == "" ||
		event.ResourceID == "" || event.Title == "" ||
		len([]rune(event.Title)) > maxTitleLength ||
		len([]rune(event.Body)) > maxBodyLength || event.OccurredAt.IsZero() {
		return fmt.Errorf("%w: invalid push event", ErrInvalidArgument)
	}
	action, err := url.Parse(event.ActionURL)
	if err != nil || event.ActionURL == "" || action.IsAbs() ||
		action.Host != "" || !strings.HasPrefix(action.Path, "/") ||
		strings.HasPrefix(action.Path, "//") {
		return fmt.Errorf("%w: action URL must be same-origin relative", ErrInvalidArgument)
	}
	return nil
}

func retryablePush(statusCode int, err error) bool {
	return (err != nil && statusCode == 0) || statusCode == 408 ||
		statusCode == 425 || statusCode == 429 || statusCode >= 500
}

func (service *Service) backoff(attempt int) time.Duration {
	delay := service.baseBackoff
	for index := 1; index < attempt && delay < service.maxBackoff; index++ {
		if delay > service.maxBackoff/2 {
			return service.maxBackoff
		}
		delay *= 2
	}
	return min(delay, service.maxBackoff)
}

func (service *Service) leaseToken() (string, error) {
	value := make([]byte, 24)
	if err := service.random(value); err != nil {
		return "", fmt.Errorf("generate lease token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func stableID(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(hash.Sum(nil)[:24])
}
