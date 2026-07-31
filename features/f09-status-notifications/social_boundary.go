package statusnotifications

import (
	"context"
	"fmt"
	"strings"
)

type SocialNotificationCommand struct {
	WorkspaceID    string
	PostID         string
	ChannelID      string
	Provider       string
	RecipientID    string
	Locale         string
	TemplateID     string
	IdempotencyKey string
}

type SocialDeliveryState string

const (
	SocialDeliveryPending          SocialDeliveryState = "pending"
	SocialDeliveryDelivered        SocialDeliveryState = "delivered"
	SocialDeliveryPermanentFailure SocialDeliveryState = "permanent_failure"
)

type SocialDeliveryReceipt struct {
	EmailDeliveryID string
	State           SocialDeliveryState
}

type SocialEmailGateway interface {
	DeliverSocialNotification(
		context.Context,
		SocialNotificationCommand,
	) (SocialDeliveryReceipt, error)
}

// SocialNotificationBoundary is the public F9 boundary between an F8 manual
// social destination and F14. The command contains only server-resolved
// identifiers and routing metadata; addresses, rendered copy, social content,
// and credentials are deliberately impossible to provide.
type SocialNotificationBoundary struct {
	email SocialEmailGateway
}

func NewSocialNotificationBoundary(
	email SocialEmailGateway,
) (*SocialNotificationBoundary, error) {
	if email == nil {
		return nil, fmt.Errorf("%w: social email gateway", ErrInvalidArgument)
	}
	return &SocialNotificationBoundary{email: email}, nil
}

func (boundary *SocialNotificationBoundary) Deliver(
	ctx context.Context,
	command SocialNotificationCommand,
) (SocialDeliveryReceipt, error) {
	command.WorkspaceID = strings.TrimSpace(command.WorkspaceID)
	command.PostID = strings.TrimSpace(command.PostID)
	command.ChannelID = strings.TrimSpace(command.ChannelID)
	command.Provider = strings.TrimSpace(command.Provider)
	command.RecipientID = strings.TrimSpace(command.RecipientID)
	command.Locale = strings.TrimSpace(command.Locale)
	command.TemplateID = strings.TrimSpace(command.TemplateID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if command.WorkspaceID == "" || command.PostID == "" ||
		command.ChannelID == "" || command.RecipientID == "" ||
		command.IdempotencyKey == "" || !validSocialLocale(command.Locale) ||
		!validSocialTemplate(command.Provider, command.TemplateID) {
		return SocialDeliveryReceipt{}, fmt.Errorf(
			"%w: social notification command",
			ErrInvalidArgument,
		)
	}
	receipt, err := boundary.email.DeliverSocialNotification(ctx, command)
	if err != nil {
		return SocialDeliveryReceipt{}, err
	}
	if strings.TrimSpace(receipt.EmailDeliveryID) == "" ||
		(receipt.State != SocialDeliveryPending &&
			receipt.State != SocialDeliveryDelivered &&
			receipt.State != SocialDeliveryPermanentFailure) {
		return SocialDeliveryReceipt{}, fmt.Errorf(
			"%w: social delivery receipt",
			ErrInvalidArgument,
		)
	}
	return receipt, nil
}

func validSocialLocale(locale string) bool {
	switch locale {
	case "en", "it", "es", "fr", "de":
		return true
	default:
		return false
	}
}

func validSocialTemplate(provider, templateID string) bool {
	switch provider {
	case "facebook_groups":
		return templateID == "facebook_group_manual_publish"
	case "instagram_personal":
		return templateID == "instagram_personal_manual_publish"
	default:
		return false
	}
}
