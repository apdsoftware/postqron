package statusnotifications

import (
	"context"
	"errors"
	"testing"
)

type socialEmailGatewayStub struct {
	command SocialNotificationCommand
	receipt SocialDeliveryReceipt
}

func (gateway *socialEmailGatewayStub) DeliverSocialNotification(
	_ context.Context,
	command SocialNotificationCommand,
) (SocialDeliveryReceipt, error) {
	gateway.command = command
	return gateway.receipt, nil
}

func TestSocialNotificationBoundaryValidatesTargetSpecificCommand(t *testing.T) {
	gateway := &socialEmailGatewayStub{receipt: SocialDeliveryReceipt{
		EmailDeliveryID: "email-1",
		State:           SocialDeliveryPending,
	}}
	boundary, err := NewSocialNotificationBoundary(gateway)
	if err != nil {
		t.Fatal(err)
	}
	command := SocialNotificationCommand{
		WorkspaceID: " workspace-1 ", PostID: " post-1 ",
		ChannelID: " channel-1 ", Provider: " facebook_groups ",
		RecipientID: " account-1 ", Locale: "it",
		TemplateID:     "facebook_group_manual_publish",
		IdempotencyKey: " notify-1 ",
	}
	receipt, err := boundary.Deliver(context.Background(), command)
	if err != nil || receipt.State != SocialDeliveryPending {
		t.Fatalf("Deliver() = %+v, %v", receipt, err)
	}
	if gateway.command.WorkspaceID != "workspace-1" ||
		gateway.command.RecipientID != "account-1" {
		t.Fatalf("gateway command = %+v", gateway.command)
	}

	command.TemplateID = "instagram_personal_manual_publish"
	if _, err = boundary.Deliver(context.Background(), command); !errors.Is(
		err,
		ErrInvalidArgument,
	) {
		t.Fatalf("mismatched provider/template error = %v", err)
	}
}
