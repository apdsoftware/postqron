package entitlements

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type billingStoreStub struct {
	transaction     BillingBinding
	subscription    BillingBinding
	subscriptionErr error
	events          []BillingEvent
	delivered       map[string]time.Time
	last            time.Time
	err             error
}

func (stub *billingStoreStub) ResolveTransaction(
	context.Context,
	string,
) (BillingBinding, error) {
	return stub.transaction, stub.err
}

func (stub *billingStoreStub) ResolveSubscription(
	context.Context,
	string,
) (BillingBinding, error) {
	if stub.subscriptionErr != nil {
		return BillingBinding{}, stub.subscriptionErr
	}
	return stub.subscription, stub.err
}

func (stub *billingStoreStub) ApplyBillingEvent(
	_ context.Context,
	event BillingEvent,
) (BillingEventResult, error) {
	if stub.err != nil {
		return BillingEventResult{}, stub.err
	}
	if stub.delivered == nil {
		stub.delivered = make(map[string]time.Time)
	}
	if _, exists := stub.delivered[event.ID]; exists {
		return BillingEventResult{}, nil
	}
	stub.delivered[event.ID] = event.OccurredAt
	stub.events = append(stub.events, event)
	if !event.ApplyState || event.OccurredAt.Before(stub.last) {
		return BillingEventResult{FirstDelivery: true}, nil
	}
	stub.last = event.OccurredAt
	return BillingEventResult{FirstDelivery: true, StateChanged: true}, nil
}

func TestVerifyPaddleSignatureUsesRawBodyAndRejectsReplay(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"event_id":"evt_test","value":"raw whitespace"}`)
	header := paddleSignature("endpoint_secret", now, body)
	if err := verifyPaddleSignature(
		"endpoint_secret", header, body, now, 5*time.Minute,
	); err != nil {
		t.Fatal(err)
	}
	if err := verifyPaddleSignature(
		"endpoint_secret", header, append(body, '\n'), now, 5*time.Minute,
	); err == nil {
		t.Fatal("modified body passed signature verification")
	}
	if err := verifyPaddleSignature(
		"endpoint_secret", header, body, now.Add(6*time.Minute), 5*time.Minute,
	); err == nil {
		t.Fatal("replayed signature passed timestamp verification")
	}
}

func TestPaddleTransactionCompletedIsVerifiedDeduplicatedAndOrdered(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	catalog := testPaddleCatalog()
	items, _ := catalog.ExpectedItems(PlanTeam, IntervalAnnual, limit(9))
	store := &billingStoreStub{
		transaction: BillingBinding{
			WorkspaceID: "workspace", Plan: PlanTeam,
			Interval: IntervalAnnual, Channels: limit(9), ExpectedItems: items,
			Period: Period{Start: now, End: now.AddDate(1, 0, 0)},
		},
		subscriptionErr: ErrUnknownSubscription,
	}
	handler, err := NewPaddleWebhookHandler("endpoint_secret", catalog, store)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }
	data := map[string]any{
		"id":              "txn_00000000000000000000000001",
		"status":          "completed",
		"customer_id":     "ctm_00000000000000000000000001",
		"subscription_id": "sub_00000000000000000000000001",
		"items":           paddleWebhookItems(items),
		"billing_period": map[string]any{
			"starts_at": now,
			"ends_at":   now.AddDate(1, 0, 0),
		},
	}
	body := paddleEventBody(t, "evt_paid", "transaction.completed", now, data)
	for delivery := 0; delivery < 2; delivery++ {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		request.Header.Set("Paddle-Signature", paddleSignature("endpoint_secret", now, body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("delivery %d status = %d", delivery, response.Code)
		}
	}
	if len(store.events) != 1 || !store.events[0].ApplyState ||
		store.events[0].State != StateActive {
		t.Fatalf("events = %#v", store.events)
	}

	older := paddleEventBody(
		t, "evt_older", "subscription.paused", now.Add(-time.Minute),
		map[string]any{
			"id":          "sub_00000000000000000000000001",
			"status":      "paused",
			"customer_id": "ctm_00000000000000000000000001",
			"current_billing_period": map[string]any{
				"starts_at": now,
				"ends_at":   now.AddDate(1, 0, 0),
			},
		},
	)
	store.subscription = BillingBinding{
		WorkspaceID: "workspace", Plan: PlanTeam, Interval: IntervalAnnual,
		Channels: limit(9), CustomerID: "ctm", SubscriptionID: "sub",
		Period: Period{Start: now, End: now.AddDate(1, 0, 0)},
	}
	store.subscriptionErr = nil
	handler.now = func() time.Time { return now.Add(-time.Minute) }
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(older)))
	request.Header.Set(
		"Paddle-Signature",
		paddleSignature("endpoint_secret", now.Add(-time.Minute), older),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || len(store.events) != 2 ||
		!store.events[1].ApplyState ||
		store.events[1].State != StatePaymentRestricted {
		t.Fatalf("older delivery status=%d events=%#v", response.Code, store.events)
	}
}

func TestPaddleRejectsWrongItemsAndClientCheckoutEventCannotGrant(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	catalog := testPaddleCatalog()
	expected, _ := catalog.ExpectedItems(PlanPro, IntervalMonthly, limit(1))
	store := &billingStoreStub{
		transaction: BillingBinding{
			WorkspaceID: "workspace", Plan: PlanPro, Interval: IntervalMonthly,
			Channels: limit(1), ExpectedItems: expected,
			Period: Period{Start: now, End: now.AddDate(0, 1, 0)},
		},
		subscriptionErr: ErrUnknownSubscription,
	}
	handler, _ := NewPaddleWebhookHandler("secret", catalog, store)
	handler.now = func() time.Time { return now }
	wrong := []PaddleItem{{PriceID: "pri_99999999999999999999999999", Quantity: 1}}
	body := paddleEventBody(t, "evt_wrong", "transaction.completed", now, map[string]any{
		"id": "txn_00000000000000000000000001", "status": "completed",
		"customer_id":     "ctm_00000000000000000000000001",
		"subscription_id": "sub_00000000000000000000000001",
		"items":           paddleWebhookItems(wrong),
		"billing_period":  map[string]any{"starts_at": now, "ends_at": now.AddDate(0, 1, 0)},
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	request.Header.Set("Paddle-Signature", paddleSignature("secret", now, body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || len(store.events) != 0 {
		t.Fatalf("wrong items status=%d events=%#v", response.Code, store.events)
	}
	clientBody := paddleEventBody(t, "evt_client", "checkout.completed", now, map[string]any{})
	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(clientBody)))
	request.Header.Set("Paddle-Signature", paddleSignature("secret", now, clientBody))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || len(store.events) != 0 {
		t.Fatalf("client event status=%d events=%#v", response.Code, store.events)
	}
}

func TestPaddleWebhookReturnsRetryableServerErrorWithoutLeakingSecret(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	catalog := testPaddleCatalog()
	store := &billingStoreStub{err: errors.New("database unavailable")}
	handler, _ := NewPaddleWebhookHandler("do-not-log-this", catalog, store)
	handler.now = func() time.Time { return now }
	body := paddleEventBody(t, "evt_failed", "transaction.payment_failed", now, map[string]any{
		"id":          "txn_00000000000000000000000001",
		"customer_id": "ctm_00000000000000000000000001",
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	request.Header.Set("Paddle-Signature", paddleSignature("do-not-log-this", now, body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "do-not-log-this") ||
		strings.Contains(response.Body.String(), "database unavailable") {
		t.Fatalf("response leaked sensitive detail: %q", response.Body.String())
	}
}

func paddleWebhookItems(items []PaddleItem) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"price":    map[string]any{"id": item.PriceID},
			"quantity": item.Quantity,
		})
	}
	return result
}

func paddleSignature(secret string, timestamp time.Time, body []byte) string {
	payload := fmt.Sprintf("%d:%s", timestamp.Unix(), body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return fmt.Sprintf(
		"ts=%d;h1=%s",
		timestamp.Unix(),
		hex.EncodeToString(mac.Sum(nil)),
	)
}

func paddleEventBody(
	t *testing.T,
	id string,
	eventType string,
	occurredAt time.Time,
	data any,
) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"event_id": id, "event_type": eventType,
		"occurred_at": occurredAt, "notification_id": "ntf_test",
		"data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
