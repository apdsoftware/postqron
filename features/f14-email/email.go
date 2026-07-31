// Package email implements the autonomous F14 transactional-email slice.
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

const ChannelTransactional Channel = "transactional"

type Locale string

const (
	LocaleEnglish Locale = "en"
	LocaleItalian Locale = "it"
	LocaleSpanish Locale = "es"
	LocaleFrench  Locale = "fr"
	LocaleGerman  Locale = "de"
)

var SupportedLocales = []Locale{
	LocaleEnglish, LocaleItalian, LocaleSpanish, LocaleFrench, LocaleGerman,
}

func ResolveLocale(value string) Locale {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if separator := strings.IndexAny(normalized, "-_"); separator >= 0 {
		normalized = normalized[:separator]
	}
	switch Locale(normalized) {
	case LocaleItalian, LocaleSpanish, LocaleFrench, LocaleGerman:
		return Locale(normalized)
	default:
		return LocaleEnglish
	}
}

type TemplateID string

const (
	TemplateWelcome                 TemplateID = "welcome"
	TemplateAccountVerification     TemplateID = "account_verification"
	TemplateWorkspaceInvitation     TemplateID = "workspace_invitation"
	TemplateAccountSecurity         TemplateID = "account_security"
	TemplateAccountLinked           TemplateID = "account_linked"
	TemplateSocialReconnect         TemplateID = "social_reconnect"
	TemplateCollaboration           TemplateID = "collaboration"
	TemplatePublicationSuccess      TemplateID = "publication_succeeded"
	TemplatePublicationFailed       TemplateID = "publication_failed"
	TemplateBilling                 TemplateID = "billing_update"
	TemplateDataExportReady         TemplateID = "data_export_ready"
	TemplateDeletion                TemplateID = "deletion_update"
	TemplatePrivacyRequest          TemplateID = "privacy_request"
	TemplatePrelaunchAccess         TemplateID = "prelaunch_access"
	TemplateOperationalAlert        TemplateID = "operational_alert"
	TemplateFacebookGroupManual     TemplateID = "facebook_group_manual_publish"
	TemplateInstagramPersonalManual TemplateID = "instagram_personal_manual_publish"
)

type Recipient struct {
	ID     string
	Email  string
	Name   string
	Locale string
}

// TemplateData contains values, never translated prose. The renderer owns all
// user-facing copy so producers cannot accidentally bypass F14 localization.
type TemplateData struct {
	Detail      string
	ActionURL   string
	OccurredAt  time.Time
	TimeZone    string
	Count       *int64
	AmountMinor *int64
	Currency    string
}

type Message struct {
	ID                string
	IdempotencyKey    string
	Channel           Channel
	Template          TemplateID
	TemplateVersion   string
	SourceWorkspaceID string
	Recipient         Recipient
	Data              TemplateData
	CreatedAt         time.Time
	MaxAttempts       int
}

type RenderedMessage struct {
	MessageID       string
	IdempotencyKey  string
	Channel         Channel
	Template        TemplateID
	TemplateVersion string
	Locale          Locale
	Recipient       Recipient
	Subject         string
	Preheader       string
	HTML            string
	Text            string
}

type DeliveryState string

const (
	StatePending  DeliveryState = "pending"
	StateSending  DeliveryState = "sending"
	StateRetry    DeliveryState = "retry"
	StateAccepted DeliveryState = "accepted"
	StateFailed   DeliveryState = "failed"
)

type Delivery struct {
	Message           Message
	Rendered          RenderedMessage
	State             DeliveryState
	Attempt           int
	NextAttemptAt     time.Time
	ProviderMessageID string
	LastDiagnostic    Diagnostic
	LeaseToken        string
	LockedUntil       time.Time
	ProviderCallAt    time.Time
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
	ReconcileExpiredLeases(context.Context, time.Time) (int64, error)
	ClaimDue(context.Context, time.Time) (Delivery, bool, error)
	MarkProviderCallStarted(context.Context, string, string, time.Time) error
	MarkAccepted(context.Context, string, string, string, time.Time) error
	MarkRetry(context.Context, string, string, Diagnostic, time.Time) error
	MarkFailed(context.Context, string, string, Diagnostic) error
}

var (
	ErrInvalidMessage   = errors.New("invalid email message")
	ErrInvalidRecipient = errors.New("invalid email recipient")
	ErrInvalidChannel   = errors.New("only transactional email is allowed")
	ErrUnknownTemplate  = errors.New("unknown transactional template")
	semverPattern       = regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`)
)

func validateMessage(message Message) error {
	if strings.TrimSpace(message.ID) == "" ||
		strings.TrimSpace(message.IdempotencyKey) == "" ||
		len(message.IdempotencyKey) > 255 ||
		!semverPattern.MatchString(message.TemplateVersion) ||
		message.MaxAttempts < 1 {
		return ErrInvalidMessage
	}
	isSocial := message.Template == TemplateFacebookGroupManual ||
		message.Template == TemplateInstagramPersonalManual
	if isSocial != (strings.TrimSpace(message.SourceWorkspaceID) != "") {
		return ErrInvalidMessage
	}
	if message.Channel != ChannelTransactional {
		return ErrInvalidChannel
	}
	if _, ok := templateCatalog[message.Template]; !ok {
		return ErrUnknownTemplate
	}
	if strings.TrimSpace(message.Recipient.ID) == "" {
		return ErrInvalidRecipient
	}
	address, err := mail.ParseAddress(message.Recipient.Email)
	if err != nil || !strings.EqualFold(address.Address, strings.TrimSpace(message.Recipient.Email)) {
		return ErrInvalidRecipient
	}
	if message.Data.ActionURL != "" {
		if err := validateHTTPSURL(message.Data.ActionURL); err != nil {
			return fmt.Errorf("%w: action URL", ErrInvalidMessage)
		}
	}
	if templateRequiresActionURL(message.Template) &&
		strings.TrimSpace(message.Data.ActionURL) == "" {
		return fmt.Errorf("%w: action URL required", ErrInvalidMessage)
	}
	if message.Data.AmountMinor != nil && strings.TrimSpace(message.Data.Currency) == "" {
		return fmt.Errorf("%w: currency", ErrInvalidMessage)
	}
	return nil
}

func templateRequiresActionURL(templateID TemplateID) bool {
	switch templateID {
	case TemplateAccountVerification,
		TemplateFacebookGroupManual,
		TemplateInstagramPersonalManual:
		return true
	default:
		return false
	}
}

func validateHTTPSURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("absolute HTTPS URL is required")
	}
	return nil
}
