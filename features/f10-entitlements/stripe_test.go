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
	completions int
	events      []BillingEvent
	binding     BillingBinding
	eventResult BillingEventResult
	err         error
}

func (store *billingStoreStub) CompleteCheckout(
	context.Context,
	string,
	time.Time,
	string,
	string,
	string,
) (bool, error) {
	store.completions++
	return true, store.err
}

func (store *billingStoreStub) ResolveSubscription(
	context.Context,
	string,
) (BillingBinding, error) {
	if store.err != nil {
		return BillingBinding{}, store.err
	}
	return store.binding, nil
}

func (store *billingStoreStub) ApplyBillingEvent(
	_ context.Context,
	event BillingEvent,
) (BillingEventResult, error) {
	store.events = append(store.events, event)
	return store.eventResult, store.err
}

func TestVerifyStripeSignature(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	body := []byte(`{"id":"evt_test"}`)
	secret := "whsec_test"
	header := stripeSignature(secret, now, body)

	if err := verifyStripeSignature(secret, header, body, now, 5*time.Minute); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := verifyStripeSignature(
		secret,
		header,
		[]byte(`{"id":"changed"}`),
		now,
		5*time.Minute,
	); err == nil {
		t.Fatal("modified body signature accepted")
	}
	if err := verifyStripeSignature(
		secret,
		header,
		body,
		now.Add(6*time.Minute),
		5*time.Minute,
	); err == nil {
		t.Fatal("expired signature accepted")
	}
}

func TestStripeWebhookActivatesOnlyFromPaidInvoice(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := &billingStoreStub{binding: BillingBinding{
		WorkspaceID:    "46c847c5-621f-4c2a-a672-bdfeb2f9aa29",
		Plan:           PlanStart,
		Interval:       IntervalMonthly,
		CustomerID:     "cus_test",
		SubscriptionID: "sub_test",
	}}
	handler, err := NewStripeWebhookHandler("whsec_test", testPrices(), store)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	body := stripeEventBody(t, "evt_paid", "invoice.paid", now, map[string]any{
		"subscription": "sub_test",
		"lines": map[string]any{
			"data": []any{
				map[string]any{
					"price": map[string]any{"id": "price_team_annual"},
					"period": map[string]any{
						"start": now.Unix(),
						"end":   now.AddDate(1, 0, 0).Unix(),
					},
				},
			},
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	request.Header.Set("Stripe-Signature", stripeSignature("whsec_test", now, body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(store.events) != 1 {
		t.Fatalf("events = %d, want 1", len(store.events))
	}
	event := store.events[0]
	if event.State != StateActive ||
		event.Plan != PlanTeam ||
		event.Interval != IntervalAnnual {
		t.Fatalf("event = %#v", event)
	}
}

func TestStripePaymentFailureKeepsCurrentPlan(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := &billingStoreStub{binding: BillingBinding{
		WorkspaceID:    "46c847c5-621f-4c2a-a672-bdfeb2f9aa29",
		Plan:           PlanStart,
		Interval:       IntervalMonthly,
		CustomerID:     "cus_test",
		SubscriptionID: "sub_test",
	}}
	handler, err := NewStripeWebhookHandler("whsec_test", testPrices(), store)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	body := stripeEventBody(
		t,
		"evt_failed_upgrade",
		"invoice.payment_failed",
		now,
		map[string]any{
			"subscription": "sub_test",
			"lines": map[string]any{
				"data": []any{
					map[string]any{
						"price": map[string]any{"id": "price_team_monthly"},
						"period": map[string]any{
							"start": now.Unix(),
							"end":   now.AddDate(0, 1, 0).Unix(),
						},
					},
				},
			},
		},
	)
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	request.Header.Set("Stripe-Signature", stripeSignature("whsec_test", now, body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	event := store.events[0]
	if event.State != StatePastDue ||
		event.Plan != PlanStart ||
		event.Interval != IntervalMonthly {
		t.Fatalf("failed upgrade changed entitlement: %#v", event)
	}
}

func TestStripeWebhookRejectsUnknownPrice(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := &billingStoreStub{binding: BillingBinding{
		WorkspaceID:    "workspace",
		Plan:           PlanStart,
		Interval:       IntervalMonthly,
		CustomerID:     "cus_test",
		SubscriptionID: "sub_test",
	}}
	handler, err := NewStripeWebhookHandler("whsec_test", testPrices(), store)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	body := stripeEventBody(t, "evt_unknown", "invoice.paid", now, map[string]any{
		"subscription": "sub_test",
		"lines": map[string]any{
			"data": []any{
				map[string]any{
					"price": map[string]any{"id": "price_not_configured"},
					"period": map[string]any{
						"start": now.Unix(),
						"end":   now.AddDate(0, 1, 0).Unix(),
					},
				},
			},
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	request.Header.Set("Stripe-Signature", stripeSignature("whsec_test", now, body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 so Stripe retries after configuration", response.Code)
	}
	if len(store.events) != 0 {
		t.Fatal("unknown Stripe price changed billing state")
	}
}

func TestStripePaidInvoiceIgnoresProrationCreditLine(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := &billingStoreStub{binding: BillingBinding{
		WorkspaceID:    "46c847c5-621f-4c2a-a672-bdfeb2f9aa29",
		Plan:           PlanStart,
		Interval:       IntervalMonthly,
		CustomerID:     "cus_test",
		SubscriptionID: "sub_test",
	}}
	handler, err := NewStripeWebhookHandler("whsec_test", testPrices(), store)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	body := stripeEventBody(t, "evt_upgrade", "invoice.paid", now, map[string]any{
		"subscription": "sub_test",
		"lines": map[string]any{
			"data": []any{
				map[string]any{
					"price":     map[string]any{"id": "price_start_monthly"},
					"proration": true,
					"period": map[string]any{
						"start": now.Unix(),
						"end":   now.AddDate(0, 1, 0).Unix(),
					},
				},
				map[string]any{
					"price": map[string]any{"id": "price_team_monthly"},
					"period": map[string]any{
						"start": now.Unix(),
						"end":   now.AddDate(0, 1, 0).Unix(),
					},
				},
			},
		},
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	request.Header.Set("Stripe-Signature", stripeSignature("whsec_test", now, body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if event := store.events[0]; event.Plan != PlanTeam {
		t.Fatalf("paid upgrade selected proration credit plan: %#v", event)
	}
}

func stripeSignature(secret string, timestamp time.Time, body []byte) string {
	payload := fmt.Sprintf("%d.%s", timestamp.Unix(), body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return fmt.Sprintf("t=%d,v1=%s", timestamp.Unix(), hex.EncodeToString(mac.Sum(nil)))
}

func stripeEventBody(
	t *testing.T,
	id string,
	eventType string,
	created time.Time,
	object any,
) []byte {
	t.Helper()
	payload := map[string]any{
		"id":      id,
		"type":    eventType,
		"created": created.Unix(),
		"data": map[string]any{
			"object": object,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestStripeWebhookReturnsServerErrorForRetryableStoreFailure(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	store := &billingStoreStub{err: errors.New("database unavailable")}
	handler, err := NewStripeWebhookHandler("whsec_test", testPrices(), store)
	if err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return now }

	body := stripeEventBody(t, "evt_checkout", "checkout.session.completed", now, map[string]any{
		"id":           "cs_test",
		"customer":     "cus_test",
		"subscription": "sub_test",
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	request.Header.Set("Stripe-Signature", stripeSignature("whsec_test", now, body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
}
