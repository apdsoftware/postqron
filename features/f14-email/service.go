package email

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

type Service struct {
	store    Store
	renderer *Renderer
	sender   Sender
	retry    RetryPolicy
	now      func() time.Time
	newID    func() (string, error)
}

func NewService(
	store Store,
	renderer *Renderer,
	sender Sender,
	retry RetryPolicy,
) (*Service, error) {
	if store == nil || renderer == nil || sender == nil {
		return nil, errors.New("email store, renderer, and sender are required")
	}
	if err := retry.Validate(); err != nil {
		return nil, err
	}
	return &Service{
		store:    store,
		renderer: renderer,
		sender:   sender,
		retry:    retry,
		now:      time.Now,
		newID:    randomID,
	}, nil
}

func (service *Service) Enqueue(
	ctx context.Context,
	message Message,
) (EnqueueResult, error) {
	if message.ID == "" {
		id, err := service.newID()
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("create message ID: %w", err)
		}
		message.ID = id
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = service.now().UTC()
	}
	if message.MaxAttempts == 0 {
		message.MaxAttempts = 5
	}
	rendered, err := service.renderer.Render(message)
	if err != nil {
		return EnqueueResult{}, err
	}
	result, err := service.store.Enqueue(ctx, Delivery{
		Message:       message,
		Rendered:      rendered,
		State:         StatePending,
		NextAttemptAt: message.CreatedAt,
	})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("enqueue email: %w", err)
	}
	return result, nil
}

// DispatchOne claims and processes at most one due message. Stores must claim
// atomically so concurrent workers cannot send the same delivery.
func (service *Service) DispatchOne(ctx context.Context) (bool, error) {
	now := service.now().UTC()
	delivery, found, err := service.store.ClaimDue(ctx, now)
	if err != nil {
		return false, fmt.Errorf("claim email delivery: %w", err)
	}
	if !found {
		return false, nil
	}

	if err := service.store.MarkProviderCallStarted(
		ctx,
		delivery.Message.ID,
		delivery.LeaseToken,
		now,
	); err != nil {
		return true, fmt.Errorf("record provider call start: %w", err)
	}
	receipt, sendErr := service.sender.Send(ctx, delivery.Rendered)
	if sendErr == nil {
		if err := service.store.MarkAccepted(
			ctx,
			delivery.Message.ID,
			delivery.LeaseToken,
			receipt.MessageID,
			now,
		); err != nil {
			return true, fmt.Errorf("record accepted email: %w", err)
		}
		return true, nil
	}

	diagnostic, retryAfter := diagnosticFromError(sendErr, now)
	if ambiguousProviderOutcome(sendErr) {
		diagnostic.Retryable = false
	}
	if diagnostic.Retryable && delivery.Attempt < delivery.Message.MaxAttempts {
		next := now.Add(service.retry.Delay(delivery.Attempt, retryAfter))
		if err := service.store.MarkRetry(
			ctx,
			delivery.Message.ID,
			delivery.LeaseToken,
			diagnostic,
			next,
		); err != nil {
			return true, fmt.Errorf("schedule email retry: %w", err)
		}
		return true, nil
	}
	diagnostic.Retryable = false
	if err := service.store.MarkFailed(
		ctx,
		delivery.Message.ID,
		delivery.LeaseToken,
		diagnostic,
	); err != nil {
		return true, fmt.Errorf("record failed email: %w", err)
	}
	return true, nil
}

func ambiguousProviderOutcome(err error) bool {
	var providerError *MailronixError
	if !errors.As(err, &providerError) {
		return false
	}
	switch providerError.Code {
	case "transport_error", "response_read_error", "invalid_response":
		return true
	default:
		return false
	}
}

func diagnosticFromError(err error, now time.Time) (Diagnostic, time.Duration) {
	diagnostic := Diagnostic{
		Code:   "delivery_failed",
		Detail: SanitizeDiagnostic(err.Error()),
		At:     now,
	}
	var providerError *MailronixError
	if errors.As(err, &providerError) {
		diagnostic.Code = sanitizeCode(providerError.Code)
		diagnostic.Detail = SanitizeDiagnostic(providerError.Detail)
		diagnostic.Retryable = providerError.Retryable
		return diagnostic, providerError.RetryAfter
	}
	return diagnostic, 0
}

func randomID() (string, error) {
	var value [18]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "email_" + base64.RawURLEncoding.EncodeToString(value[:]), nil
}
