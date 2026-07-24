package entitlements

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type PriceKey struct {
	Plan     PlanCode
	Interval BillingInterval
}

type StripePrices map[PriceKey]string

func (prices StripePrices) Validate() error {
	if len(prices) != len(publicCatalog)*2 {
		return errors.New("exactly six public Stripe prices must be configured")
	}
	seen := make(map[string]struct{}, 6)
	for key := range prices {
		if _, err := PublicPlanByCode(key.Plan); err != nil || !validInterval(key.Interval) {
			return errors.New("Stripe prices may only map public plans and intervals")
		}
	}
	for _, plan := range PublicPlans() {
		for _, interval := range []BillingInterval{IntervalMonthly, IntervalAnnual} {
			priceID, ok := prices.PriceID(plan.Code, interval)
			if !ok || !strings.HasPrefix(priceID, "price_") {
				return fmt.Errorf("%w for %s/%s", ErrMissingStripePrice, plan.Code, interval)
			}
			if _, duplicate := seen[priceID]; duplicate {
				return fmt.Errorf("Stripe price %q is configured more than once", priceID)
			}
			seen[priceID] = struct{}{}
		}
	}
	return nil
}

func (prices StripePrices) PriceID(plan PlanCode, interval BillingInterval) (string, bool) {
	priceID, ok := prices[PriceKey{Plan: plan, Interval: interval}]
	return priceID, ok && priceID != ""
}

func (prices StripePrices) PlanForPrice(priceID string) (PlanCode, BillingInterval, bool) {
	for key, configuredID := range prices {
		if hmac.Equal([]byte(configuredID), []byte(priceID)) {
			return key.Plan, key.Interval, true
		}
	}
	return "", "", false
}

type StripeClient struct {
	secretKey string
	apiBase   string
	client    *http.Client
}

func NewStripeClient(secretKey string, client *http.Client) (*StripeClient, error) {
	if !strings.HasPrefix(secretKey, "sk_") {
		return nil, errors.New("Stripe secret key is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &StripeClient{
		secretKey: secretKey,
		apiBase:   "https://api.stripe.com/v1",
		client:    client,
	}, nil
}

func (client *StripeClient) CreateCheckout(
	ctx context.Context,
	request ProviderCheckoutRequest,
) (CheckoutSession, error) {
	form := url.Values{
		"mode":                    {"subscription"},
		"line_items[0][price]":    {request.PriceID},
		"line_items[0][quantity]": {"1"},
		"success_url":             {request.SuccessURL},
		"cancel_url":              {request.CancelURL},
		"client_reference_id":     {request.WorkspaceID},
		"automatic_tax[enabled]":  {"true"},
		"allow_promotion_codes":   {"false"},
		"subscription_data[metadata][postqron_workspace_id]": {request.WorkspaceID},
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.apiBase+"/checkout/sessions",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return CheckoutSession{}, fmt.Errorf("build Stripe request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.secretKey)
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)

	response, err := client.client.Do(httpRequest)
	if err != nil {
		return CheckoutSession{}, fmt.Errorf("send Stripe request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return CheckoutSession{}, fmt.Errorf("read Stripe response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CheckoutSession{}, fmt.Errorf("Stripe returned HTTP %d", response.StatusCode)
	}

	var payload struct {
		ID        string `json:"id"`
		URL       string `json:"url"`
		ExpiresAt int64  `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return CheckoutSession{}, fmt.Errorf("decode Stripe response: %w", err)
	}
	return CheckoutSession{
		ID:        payload.ID,
		URL:       payload.URL,
		ExpiresAt: time.Unix(payload.ExpiresAt, 0).UTC(),
	}, nil
}

func (client *StripeClient) CreatePortal(
	ctx context.Context,
	request ProviderPortalRequest,
) (PortalSession, error) {
	form := url.Values{
		"customer":   {request.CustomerID},
		"return_url": {request.ReturnURL},
	}
	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		client.apiBase+"/billing_portal/sessions",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return PortalSession{}, fmt.Errorf("build Stripe request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+client.secretKey)
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey)

	response, err := client.client.Do(httpRequest)
	if err != nil {
		return PortalSession{}, fmt.Errorf("send Stripe request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return PortalSession{}, fmt.Errorf("read Stripe response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return PortalSession{}, fmt.Errorf("Stripe returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return PortalSession{}, fmt.Errorf("decode Stripe response: %w", err)
	}
	return PortalSession{URL: payload.URL}, nil
}

type StripeWebhookHandler struct {
	secret    string
	prices    StripePrices
	store     BillingStore
	now       func() time.Time
	tolerance time.Duration
}

func NewStripeWebhookHandler(
	webhookSecret string,
	prices StripePrices,
	store BillingStore,
) (*StripeWebhookHandler, error) {
	if !strings.HasPrefix(webhookSecret, "whsec_") {
		return nil, errors.New("Stripe webhook secret is required")
	}
	if err := prices.Validate(); err != nil {
		return nil, err
	}
	return &StripeWebhookHandler{
		secret:    webhookSecret,
		prices:    prices,
		store:     store,
		now:       time.Now,
		tolerance: 5 * time.Minute,
	}, nil
}

func (handler *StripeWebhookHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		http.Error(writer, "invalid webhook body", http.StatusBadRequest)
		return
	}
	if err := verifyStripeSignature(
		handler.secret,
		request.Header.Get("Stripe-Signature"),
		body,
		handler.now().UTC(),
		handler.tolerance,
	); err != nil {
		http.Error(writer, "invalid webhook signature", http.StatusBadRequest)
		return
	}
	if err := handler.process(request.Context(), body); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrInvalidCheckout) ||
			errors.Is(err, ErrEventConflict) {
			status = http.StatusBadRequest
		}
		http.Error(writer, "webhook was not applied", status)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

type stripeEnvelope struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Created int64  `json:"created"`
	Data    struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

func (handler *StripeWebhookHandler) process(ctx context.Context, body []byte) error {
	var envelope stripeEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return ErrEventConflict
	}
	if envelope.ID == "" || envelope.Type == "" || envelope.Created <= 0 {
		return ErrEventConflict
	}
	createdAt := time.Unix(envelope.Created, 0).UTC()

	switch envelope.Type {
	case "checkout.session.completed":
		var session struct {
			ID           string `json:"id"`
			Customer     string `json:"customer"`
			Subscription string `json:"subscription"`
		}
		if err := json.Unmarshal(envelope.Data.Object, &session); err != nil {
			return ErrInvalidCheckout
		}
		if session.ID == "" || session.Customer == "" || session.Subscription == "" {
			return ErrInvalidCheckout
		}
		_, err := handler.store.CompleteCheckout(
			ctx,
			envelope.ID,
			createdAt,
			session.ID,
			session.Customer,
			session.Subscription,
		)
		return err

	case "invoice.paid", "invoice.payment_failed":
		invoice, err := decodeStripeInvoice(envelope.Data.Object)
		if err != nil {
			return err
		}
		binding, err := handler.store.ResolveSubscription(ctx, invoice.SubscriptionID)
		if err != nil {
			return err
		}
		plan := binding.Plan
		interval := binding.Interval
		period := binding.Period
		state := StateActive
		if envelope.Type == "invoice.payment_failed" {
			state = StatePastDue
			if !validPeriod(period) {
				period = invoice.Lines[0].Period
			}
		} else {
			var ok bool
			plan, interval, period, ok = handler.paidInvoiceEntitlement(invoice)
			if !ok {
				return ErrMissingStripePrice
			}
		}
		if !validPeriod(period) {
			return ErrEventConflict
		}
		_, err = handler.store.ApplyBillingEvent(ctx, BillingEvent{
			ID:             envelope.ID,
			Type:           envelope.Type,
			CreatedAt:      createdAt,
			WorkspaceID:    binding.WorkspaceID,
			Plan:           plan,
			Interval:       interval,
			State:          state,
			CustomerID:     binding.CustomerID,
			SubscriptionID: binding.SubscriptionID,
			Period:         period,
		})
		return err

	case "customer.subscription.updated", "customer.subscription.deleted":
		subscription, err := decodeStripeSubscription(envelope.Data.Object)
		if err != nil {
			return err
		}
		state, actionable := restrictedSubscriptionState(
			envelope.Type,
			subscription.Status,
		)
		if !actionable {
			return nil
		}
		binding, err := handler.store.ResolveSubscription(ctx, subscription.ID)
		if err != nil {
			return err
		}
		period := subscription.Period
		if !validPeriod(period) {
			period = binding.Period
		}
		if !validPeriod(period) {
			return ErrEventConflict
		}
		_, err = handler.store.ApplyBillingEvent(ctx, BillingEvent{
			ID:             envelope.ID,
			Type:           envelope.Type,
			CreatedAt:      createdAt,
			WorkspaceID:    binding.WorkspaceID,
			Plan:           binding.Plan,
			Interval:       binding.Interval,
			State:          state,
			CustomerID:     binding.CustomerID,
			SubscriptionID: binding.SubscriptionID,
			Period:         period,
		})
		return err
	default:
		return nil
	}
}

type stripeInvoice struct {
	SubscriptionID string
	Lines          []stripeInvoiceLine
}

type stripeInvoiceLine struct {
	PriceID   string
	Period    Period
	Proration bool
}

func decodeStripeInvoice(source []byte) (stripeInvoice, error) {
	var payload struct {
		Subscription string `json:"subscription"`
		Parent       struct {
			SubscriptionDetails struct {
				Subscription string `json:"subscription"`
			} `json:"subscription_details"`
		} `json:"parent"`
		Lines struct {
			Data []struct {
				Proration bool `json:"proration"`
				Price     struct {
					ID string `json:"id"`
				} `json:"price"`
				Pricing struct {
					PriceDetails struct {
						Price string `json:"price"`
					} `json:"price_details"`
				} `json:"pricing"`
				Parent struct {
					SubscriptionItemDetails struct {
						Proration bool `json:"proration"`
					} `json:"subscription_item_details"`
				} `json:"parent"`
				Period struct {
					Start int64 `json:"start"`
					End   int64 `json:"end"`
				} `json:"period"`
			} `json:"data"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(source, &payload); err != nil || len(payload.Lines.Data) == 0 {
		return stripeInvoice{}, ErrEventConflict
	}
	subscriptionID := payload.Subscription
	if subscriptionID == "" {
		subscriptionID = payload.Parent.SubscriptionDetails.Subscription
	}
	if subscriptionID == "" {
		return stripeInvoice{}, ErrEventConflict
	}
	invoice := stripeInvoice{SubscriptionID: subscriptionID}
	for _, line := range payload.Lines.Data {
		priceID := line.Price.ID
		if priceID == "" {
			priceID = line.Pricing.PriceDetails.Price
		}
		invoice.Lines = append(invoice.Lines, stripeInvoiceLine{
			PriceID: priceID,
			Period: Period{
				Start: time.Unix(line.Period.Start, 0).UTC(),
				End:   time.Unix(line.Period.End, 0).UTC(),
			},
			Proration: line.Proration ||
				line.Parent.SubscriptionItemDetails.Proration,
		})
	}
	return invoice, nil
}

func (handler *StripeWebhookHandler) paidInvoiceEntitlement(
	invoice stripeInvoice,
) (PlanCode, BillingInterval, Period, bool) {
	for _, includeProrations := range []bool{false, true} {
		for _, line := range invoice.Lines {
			if line.Proration != includeProrations || !validPeriod(line.Period) {
				continue
			}
			plan, interval, ok := handler.prices.PlanForPrice(line.PriceID)
			if ok {
				return plan, interval, line.Period, true
			}
		}
	}
	return "", "", Period{}, false
}

type stripeSubscription struct {
	ID     string
	Status string
	Period Period
}

func decodeStripeSubscription(source []byte) (stripeSubscription, error) {
	var payload struct {
		ID                 string `json:"id"`
		Status             string `json:"status"`
		CurrentPeriodStart int64  `json:"current_period_start"`
		CurrentPeriodEnd   int64  `json:"current_period_end"`
	}
	if err := json.Unmarshal(source, &payload); err != nil ||
		payload.ID == "" ||
		payload.Status == "" {
		return stripeSubscription{}, ErrEventConflict
	}
	return stripeSubscription{
		ID:     payload.ID,
		Status: payload.Status,
		Period: Period{
			Start: time.Unix(payload.CurrentPeriodStart, 0).UTC(),
			End:   time.Unix(payload.CurrentPeriodEnd, 0).UTC(),
		},
	}, nil
}

func restrictedSubscriptionState(eventType, providerState string) (BillingState, bool) {
	if eventType == "customer.subscription.deleted" || providerState == "canceled" {
		return StateCanceled, true
	}
	switch providerState {
	case "past_due":
		return StatePastDue, true
	case "unpaid", "paused":
		return StatePaymentRestricted, true
	default:
		return "", false
	}
}

func validPeriod(period Period) bool {
	return !period.Start.IsZero() &&
		!period.End.IsZero() &&
		period.End.After(period.Start)
}

func verifyStripeSignature(
	secret string,
	header string,
	body []byte,
	now time.Time,
	tolerance time.Duration,
) error {
	var (
		timestamp  int64
		signatures []string
	)
	for _, component := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(component), "=")
		if !ok {
			continue
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return errors.New("invalid Stripe signature timestamp")
			}
			timestamp = parsed
		case "v1":
			signatures = append(signatures, value)
		}
	}
	if timestamp <= 0 || len(signatures) == 0 {
		return errors.New("incomplete Stripe signature")
	}
	signedAt := time.Unix(timestamp, 0)
	if now.Sub(signedAt) > tolerance || signedAt.Sub(now) > tolerance {
		return errors.New("expired Stripe signature")
	}

	payload := strconv.FormatInt(timestamp, 10) + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	expected := mac.Sum(nil)
	for _, signature := range signatures {
		decoded, err := hex.DecodeString(signature)
		if err == nil && hmac.Equal(expected, decoded) {
			return nil
		}
	}
	return errors.New("Stripe signature mismatch")
}
