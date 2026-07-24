// Package email implements the autonomous F14 email delivery slice.
package email

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Channel string

const (
	ChannelTransactional Channel = "transactional"
	ChannelMarketing     Channel = "marketing"
)

type TemplateID string

const (
	TemplateWelcome           TemplateID = "welcome"
	TemplatePlanChanged       TemplateID = "plan_changed"
	TemplatePublicationFailed TemplateID = "publication_failed"
	TemplateSecurityAlert     TemplateID = "security_alert"
	TemplateMarketingUpdate   TemplateID = "marketing_update"
)

type Recipient struct {
	ID    string
	Email string
	Name  string
}

type TemplateData struct {
	Heading        string
	Intro          string
	Body           string
	Detail         string
	ActionLabel    string
	ActionURL      string
	UnsubscribeURL string
}

type Message struct {
	ID              string
	IdempotencyKey  string
	Channel         Channel
	Template        TemplateID
	TemplateVersion string
	Recipient       Recipient
	Data            TemplateData
	CreatedAt       time.Time
	MaxAttempts     int
}

type RenderedMessage struct {
	MessageID       string
	IdempotencyKey  string
	Channel         Channel
	Template        TemplateID
	TemplateVersion string
	Recipient       Recipient
	Subject         string
	HTML            string
	Text            string
	Headers         map[string]string
}

type DeliveryState string

const (
	StatePending    DeliveryState = "pending"
	StateSending    DeliveryState = "sending"
	StateRetry      DeliveryState = "retry"
	StateAccepted   DeliveryState = "accepted"
	StateDelivered  DeliveryState = "delivered"
	StateBounced    DeliveryState = "bounced"
	StateComplained DeliveryState = "complained"
	StateFailed     DeliveryState = "failed"
	StateSuppressed DeliveryState = "suppressed"
)

type Delivery struct {
	Message           Message
	Rendered          RenderedMessage
	State             DeliveryState
	Attempt           int
	NextAttemptAt     time.Time
	ProviderMessageID string
	LastDiagnostic    Diagnostic
}

type Diagnostic struct {
	Code      string
	Detail    string
	Retryable bool
	At        time.Time
}

type EnqueueResult struct {
	ID      string
	Created bool
	State   DeliveryState
}

type ProviderReceipt struct {
	MessageID string
}

type Sender interface {
	Send(context.Context, RenderedMessage) (ProviderReceipt, error)
}

type Store interface {
	Enqueue(context.Context, Delivery) (EnqueueResult, error)
	ClaimDue(context.Context, time.Time) (Delivery, bool, error)
	MarkAccepted(context.Context, string, string, time.Time) error
	MarkRetry(context.Context, string, Diagnostic, time.Time) error
	MarkFailed(context.Context, string, Diagnostic) error
	RecordProviderEvent(context.Context, ProviderEvent) (bool, error)
	Suppress(context.Context, Suppression) error
	IsSuppressed(context.Context, string, Channel) (bool, error)
}

var (
	ErrInvalidMessage        = errors.New("invalid email message")
	ErrInvalidRecipient      = errors.New("invalid email recipient")
	ErrInvalidChannel        = errors.New("invalid email channel")
	ErrTemplateChannel       = errors.New("template is not allowed on this channel")
	ErrUnsubscribeRequired   = errors.New("marketing email requires unsubscribe")
	ErrUnexpectedUnsubscribe = errors.New("transactional email cannot include marketing unsubscribe")
	ErrSuppressed            = errors.New("recipient is suppressed")
	semverPattern            = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)
)

func validateMessage(message Message) error {
	if strings.TrimSpace(message.ID) == "" ||
		strings.TrimSpace(message.IdempotencyKey) == "" ||
		len(message.IdempotencyKey) > 255 ||
		strings.TrimSpace(string(message.Template)) == "" ||
		!semverPattern.MatchString(message.TemplateVersion) ||
		strings.TrimSpace(message.Data.Heading) == "" ||
		strings.TrimSpace(message.Data.Intro) == "" ||
		message.MaxAttempts < 1 {
		return ErrInvalidMessage
	}
	if message.Recipient.ID == "" {
		return ErrInvalidRecipient
	}
	address, err := mail.ParseAddress(message.Recipient.Email)
	if err != nil || !strings.EqualFold(address.Address, strings.TrimSpace(message.Recipient.Email)) {
		return ErrInvalidRecipient
	}
	if message.Channel != ChannelTransactional && message.Channel != ChannelMarketing {
		return ErrInvalidChannel
	}
	expected, ok := templateChannels[message.Template]
	if !ok || expected != message.Channel {
		return ErrTemplateChannel
	}
	if message.Data.ActionURL != "" {
		if err := validateHTTPSURL(message.Data.ActionURL); err != nil {
			return fmt.Errorf("%w: action URL", ErrInvalidMessage)
		}
		if strings.TrimSpace(message.Data.ActionLabel) == "" {
			return fmt.Errorf("%w: action label", ErrInvalidMessage)
		}
	} else if message.Data.ActionLabel != "" {
		return fmt.Errorf("%w: action URL", ErrInvalidMessage)
	}
	switch message.Channel {
	case ChannelMarketing:
		if err := validateHTTPSURL(message.Data.UnsubscribeURL); err != nil {
			return ErrUnsubscribeRequired
		}
	case ChannelTransactional:
		if message.Data.UnsubscribeURL != "" {
			return ErrUnexpectedUnsubscribe
		}
	}
	return nil
}

func validateHTTPSURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("absolute HTTPS URL is required")
	}
	return nil
}

var templateChannels = map[TemplateID]Channel{
	TemplateWelcome:           ChannelTransactional,
	TemplatePlanChanged:       ChannelTransactional,
	TemplatePublicationFailed: ChannelTransactional,
	TemplateSecurityAlert:     ChannelTransactional,
	TemplateMarketingUpdate:   ChannelMarketing,
}
