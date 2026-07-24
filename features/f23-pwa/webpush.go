package pwa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

type WebPushGateway struct {
	subscriber      string
	vapidPublicKey  string
	vapidPrivateKey string
	ttl             int
}

func NewWebPushGateway(
	subscriber, vapidPublicKey, vapidPrivateKey string,
) (*WebPushGateway, error) {
	subscriber = strings.TrimSpace(subscriber)
	vapidPublicKey = strings.TrimSpace(vapidPublicKey)
	vapidPrivateKey = strings.TrimSpace(vapidPrivateKey)
	if subscriber == "" || vapidPublicKey == "" || vapidPrivateKey == "" {
		return nil, fmt.Errorf("%w: VAPID configuration is required", ErrInvalidArgument)
	}
	return &WebPushGateway{
		subscriber:      subscriber,
		vapidPublicKey:  vapidPublicKey,
		vapidPrivateKey: vapidPrivateKey,
		ttl:             int((6 * time.Hour).Seconds()),
	}, nil
}

func (gateway *WebPushGateway) Send(
	ctx context.Context,
	subscription Subscription,
	event PushEvent,
) (SendResult, error) {
	payload, err := json.Marshal(map[string]string{
		"title": event.Title,
		"body":  event.Body,
		"url":   event.ActionURL,
		"tag":   "postqron-" + string(event.Kind) + "-" + event.ResourceID,
	})
	if err != nil {
		return SendResult{}, fmt.Errorf("encode push payload: %w", err)
	}
	response, err := webpush.SendNotificationWithContext(
		ctx,
		payload,
		&webpush.Subscription{
			Endpoint: subscription.Endpoint,
			Keys: webpush.Keys{
				P256dh: subscription.P256DH,
				Auth:   subscription.Auth,
			},
		},
		&webpush.Options{
			Subscriber:      gateway.subscriber,
			VAPIDPublicKey:  gateway.vapidPublicKey,
			VAPIDPrivateKey: gateway.vapidPrivateKey,
			TTL:             gateway.ttl,
		},
	)
	if response == nil {
		return SendResult{}, err
	}
	defer response.Body.Close()
	result := SendResult{StatusCode: response.StatusCode}
	if err != nil {
		return result, err
	}
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return result, fmt.Errorf("web push endpoint returned status %d", response.StatusCode)
	}
	return result, nil
}
