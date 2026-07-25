package entitlements

import (
	"bytes"
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

type PaddleClient struct {
	apiKey  string
	apiBase string
	client  *http.Client
	now     func() time.Time
}

func NewPaddleClient(config PaddleConfig, client *http.Client) (*PaddleClient, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &PaddleClient{
		apiKey:  config.APIKey,
		apiBase: config.APIBaseURL(),
		client:  client,
		now:     time.Now,
	}, nil
}

func (client *PaddleClient) CreateCheckout(
	ctx context.Context,
	request ProviderCheckoutRequest,
) (CheckoutSession, error) {
	payload := struct {
		Items          []PaddleItem `json:"items"`
		CollectionMode string       `json:"collection_mode"`
		Checkout       struct {
			URL string `json:"url"`
		} `json:"checkout"`
		CustomData map[string]any `json:"custom_data"`
	}{
		Items:          request.Items,
		CollectionMode: "automatic",
		CustomData: map[string]any{
			"postqron_workspace_id": request.WorkspaceID,
			"catalog_version":       request.CatalogVersion,
			"plan":                  request.Plan,
			"interval":              request.Interval,
			"channels":              request.Channels,
			"idempotency_key":       request.IdempotencyKey,
		},
	}
	payload.Checkout.URL = request.CheckoutURL
	var response struct {
		Data struct {
			ID       string `json:"id"`
			Checkout struct {
				URL string `json:"url"`
			} `json:"checkout"`
		} `json:"data"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/transactions", payload, &response); err != nil {
		return CheckoutSession{}, err
	}
	return CheckoutSession{
		ID:        response.Data.ID,
		URL:       response.Data.Checkout.URL,
		ExpiresAt: client.now().UTC().Add(24 * time.Hour),
	}, nil
}

func (client *PaddleClient) CreatePortal(
	ctx context.Context,
	request ProviderPortalRequest,
) (PortalSession, error) {
	path := "/customers/" + url.PathEscape(request.CustomerID) + "/portal-sessions"
	payload := struct {
		SubscriptionIDs []string `json:"subscription_ids,omitempty"`
	}{}
	if request.SubscriptionID != "" {
		payload.SubscriptionIDs = []string{request.SubscriptionID}
	}
	var response struct {
		Data struct {
			URLs struct {
				General struct {
					Overview string `json:"overview"`
				} `json:"general"`
			} `json:"urls"`
		} `json:"data"`
	}
	if err := client.doJSON(ctx, http.MethodPost, path, payload, &response); err != nil {
		return PortalSession{}, err
	}
	return PortalSession{URL: response.Data.URLs.General.Overview}, nil
}

func (client *PaddleClient) PreviewSubscriptionChange(
	ctx context.Context,
	change ProviderSubscriptionChange,
) (json.RawMessage, error) {
	payload := paddleSubscriptionChangePayload(change)
	var response json.RawMessage
	if err := client.doJSON(
		ctx,
		http.MethodPatch,
		"/subscriptions/"+url.PathEscape(change.SubscriptionID)+"/preview",
		payload,
		&response,
	); err != nil {
		return nil, err
	}
	return response, nil
}

func (client *PaddleClient) UpdateSubscription(
	ctx context.Context,
	change ProviderSubscriptionChange,
) error {
	var response json.RawMessage
	return client.doJSON(
		ctx,
		http.MethodPatch,
		"/subscriptions/"+url.PathEscape(change.SubscriptionID),
		paddleSubscriptionChangePayload(change),
		&response,
	)
}

func (client *PaddleClient) CancelSubscription(
	ctx context.Context,
	subscriptionID string,
	_ string,
) error {
	var response json.RawMessage
	return client.doJSON(
		ctx,
		http.MethodPost,
		"/subscriptions/"+url.PathEscape(subscriptionID)+"/cancel",
		map[string]string{"effective_from": "next_billing_period"},
		&response,
	)
}

func paddleSubscriptionChangePayload(change ProviderSubscriptionChange) any {
	return struct {
		Items            []PaddleItem `json:"items"`
		ProrationMode    string       `json:"proration_billing_mode"`
		OnPaymentFailure string       `json:"on_payment_failure"`
	}{
		Items:            change.Items,
		ProrationMode:    change.ProrationMode,
		OnPaymentFailure: change.OnPaymentFailure,
	}
}

func (client *PaddleClient) doJSON(
	ctx context.Context,
	method string,
	path string,
	payload any,
	result any,
) error {
	var requestBody io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode Paddle request: %w", err)
		}
		requestBody = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		client.apiBase+path,
		requestBody,
	)
	if err != nil {
		return fmt.Errorf("build Paddle request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Paddle-Version", "1")
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("send Paddle request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("read Paddle response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var providerError struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &providerError)
		if providerError.Error.Code == "" {
			return fmt.Errorf("Paddle returned HTTP %d", response.StatusCode)
		}
		return fmt.Errorf(
			"Paddle returned HTTP %d (%s)",
			response.StatusCode,
			providerError.Error.Code,
		)
	}
	if err := json.Unmarshal(responseBody, result); err != nil {
		return fmt.Errorf("decode Paddle response: %w", err)
	}
	return nil
}

type PaddleWebhookHandler struct {
	secret    string
	catalog   PaddleCatalog
	store     BillingStore
	now       func() time.Time
	tolerance time.Duration
}

func NewPaddleWebhookHandler(
	webhookSecret string,
	catalog PaddleCatalog,
	store BillingStore,
) (*PaddleWebhookHandler, error) {
	if strings.TrimSpace(webhookSecret) == "" {
		return nil, fmt.Errorf("%w: webhook secret is required", ErrInvalidPaddleConfig)
	}
	if err := catalog.Validate(); err != nil {
		return nil, err
	}
	return &PaddleWebhookHandler{
		secret:    webhookSecret,
		catalog:   catalog,
		store:     store,
		now:       time.Now,
		tolerance: 5 * time.Minute,
	}, nil
}

func (handler *PaddleWebhookHandler) ServeHTTP(
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
	if err := verifyPaddleSignature(
		handler.secret,
		request.Header.Get("Paddle-Signature"),
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
			errors.Is(err, ErrEventConflict) ||
			errors.Is(err, ErrCatalogMismatch) {
			status = http.StatusBadRequest
		}
		http.Error(writer, "webhook was not applied", status)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

type paddleEnvelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

type paddleTransaction struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	CustomerID     string `json:"customer_id"`
	SubscriptionID string `json:"subscription_id"`
	Items          []struct {
		Price struct {
			ID string `json:"id"`
		} `json:"price"`
		Quantity int64 `json:"quantity"`
	} `json:"items"`
	BillingPeriod *struct {
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
	} `json:"billing_period"`
}

type paddleSubscription struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	CustomerID    string `json:"customer_id"`
	TransactionID string `json:"transaction_id"`
	Items         []struct {
		Price struct {
			ID string `json:"id"`
		} `json:"price"`
		Quantity int64 `json:"quantity"`
	} `json:"items"`
	CurrentBillingPeriod *struct {
		StartsAt time.Time `json:"starts_at"`
		EndsAt   time.Time `json:"ends_at"`
	} `json:"current_billing_period"`
}

func (handler *PaddleWebhookHandler) process(ctx context.Context, body []byte) error {
	var envelope paddleEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil ||
		envelope.EventID == "" ||
		envelope.EventType == "" ||
		envelope.OccurredAt.IsZero() {
		return ErrEventConflict
	}
	envelope.OccurredAt = envelope.OccurredAt.UTC()
	switch envelope.EventType {
	case "transaction.completed", "transaction.payment_failed":
		return handler.processTransaction(ctx, envelope)
	case "subscription.created",
		"subscription.updated",
		"subscription.activated",
		"subscription.past_due",
		"subscription.paused",
		"subscription.resumed",
		"subscription.canceled":
		return handler.processSubscription(ctx, envelope)
	default:
		return nil
	}
}

func (handler *PaddleWebhookHandler) processTransaction(
	ctx context.Context,
	envelope paddleEnvelope,
) error {
	var transaction paddleTransaction
	if err := json.Unmarshal(envelope.Data, &transaction); err != nil ||
		transaction.ID == "" {
		return ErrEventConflict
	}
	if envelope.EventType == "transaction.completed" &&
		transaction.CustomerID == "" {
		return ErrEventConflict
	}
	items := transactionItems(transaction)
	var binding BillingBinding
	var err error
	resolvedSubscription := false
	if transaction.SubscriptionID != "" {
		binding, err = handler.store.ResolveSubscription(ctx, transaction.SubscriptionID)
		resolvedSubscription = err == nil
	}
	if err != nil || transaction.SubscriptionID == "" {
		binding, err = handler.store.ResolveTransaction(ctx, transaction.ID)
	}
	if err != nil {
		return err
	}
	period := binding.Period
	if transaction.BillingPeriod != nil {
		period = Period{
			Start: transaction.BillingPeriod.StartsAt.UTC(),
			End:   transaction.BillingPeriod.EndsAt.UTC(),
		}
	}
	if !validPeriod(period) {
		return ErrEventConflict
	}
	event := BillingEvent{
		ID:             envelope.EventID,
		Type:           envelope.EventType,
		OccurredAt:     envelope.OccurredAt,
		WorkspaceID:    binding.WorkspaceID,
		Plan:           binding.Plan,
		Interval:       binding.Interval,
		Channels:       binding.Channels,
		CustomerID:     transaction.CustomerID,
		SubscriptionID: transaction.SubscriptionID,
		TransactionID:  transaction.ID,
		Period:         period,
	}
	if envelope.EventType == "transaction.completed" {
		if transaction.Status != "completed" || transaction.SubscriptionID == "" {
			return ErrCatalogMismatch
		}
		if resolvedSubscription {
			plan, interval, channels, ok := handler.catalog.ResolveItems(items)
			if !ok {
				return ErrCatalogMismatch
			}
			event.Plan = plan
			event.Interval = interval
			event.Channels = channels
		} else if !SamePaddleItems(binding.ExpectedItems, items) {
			return ErrCatalogMismatch
		}
		event.State = StateActive
		event.ApplyState = true
	} else {
		event.State = StatePastDue
		failedAt := envelope.OccurredAt
		event.PaymentFailedAt = &failedAt
		// A failed initial checkout never replaces a reliable entitlement.
		event.ApplyState = binding.SubscriptionID != "" &&
			binding.SubscriptionID == transaction.SubscriptionID
	}
	_, err = handler.store.ApplyBillingEvent(ctx, event)
	return err
}

func (handler *PaddleWebhookHandler) processSubscription(
	ctx context.Context,
	envelope paddleEnvelope,
) error {
	var subscription paddleSubscription
	if err := json.Unmarshal(envelope.Data, &subscription); err != nil ||
		subscription.ID == "" ||
		subscription.CustomerID == "" {
		return ErrEventConflict
	}
	binding, err := handler.store.ResolveSubscription(ctx, subscription.ID)
	if err != nil {
		// A subscription may be announced before transaction.completed. It is
		// intentionally not allowed to provision access.
		if envelope.EventType != "subscription.created" ||
			subscription.TransactionID == "" {
			return err
		}
		binding, err = handler.store.ResolveTransaction(ctx, subscription.TransactionID)
		if err != nil {
			return err
		}
		_, err = handler.store.ApplyBillingEvent(ctx, BillingEvent{
			ID:             envelope.EventID,
			Type:           envelope.EventType,
			OccurredAt:     envelope.OccurredAt,
			WorkspaceID:    binding.WorkspaceID,
			Plan:           binding.Plan,
			Interval:       binding.Interval,
			Channels:       binding.Channels,
			CustomerID:     subscription.CustomerID,
			SubscriptionID: subscription.ID,
			TransactionID:  subscription.TransactionID,
			Period:         binding.Period,
			ApplyState:     false,
		})
		return err
	}
	period := binding.Period
	if subscription.CurrentBillingPeriod != nil {
		period = Period{
			Start: subscription.CurrentBillingPeriod.StartsAt.UTC(),
			End:   subscription.CurrentBillingPeriod.EndsAt.UTC(),
		}
	}
	if !validPeriod(period) {
		return ErrEventConflict
	}
	event := BillingEvent{
		ID:             envelope.EventID,
		Type:           envelope.EventType,
		OccurredAt:     envelope.OccurredAt,
		WorkspaceID:    binding.WorkspaceID,
		Plan:           binding.Plan,
		Interval:       binding.Interval,
		Channels:       binding.Channels,
		CustomerID:     binding.CustomerID,
		SubscriptionID: binding.SubscriptionID,
		Period:         period,
	}
	switch envelope.EventType {
	case "subscription.created":
		event.ApplyState = false
	case "subscription.activated", "subscription.resumed":
		event.State = StateActive
		event.ApplyState = true
	case "subscription.past_due":
		event.State = StatePastDue
		event.ApplyState = true
		failedAt := envelope.OccurredAt
		event.PaymentFailedAt = &failedAt
	case "subscription.paused":
		event.State = StatePaymentRestricted
		event.ApplyState = true
	case "subscription.canceled":
		event.Plan = PlanStart
		event.Interval = IntervalMonthly
		event.Channels = 3
		event.State = StateCanceled
		event.ApplyState = true
	case "subscription.updated":
		switch subscription.Status {
		case "past_due":
			event.State = StatePastDue
			event.ApplyState = true
			failedAt := envelope.OccurredAt
			event.PaymentFailedAt = &failedAt
		case "paused":
			event.State = StatePaymentRestricted
			event.ApplyState = true
		case "canceled":
			event.Plan = PlanStart
			event.Interval = IntervalMonthly
			event.Channels = 3
			event.State = StateCanceled
			event.ApplyState = true
		default:
			// Pricing changes are committed only by transaction.completed.
			event.ApplyState = false
		}
	}
	_, err = handler.store.ApplyBillingEvent(ctx, event)
	return err
}

func transactionItems(transaction paddleTransaction) []PaddleItem {
	items := make([]PaddleItem, 0, len(transaction.Items))
	for _, item := range transaction.Items {
		items = append(items, PaddleItem{
			PriceID:  item.Price.ID,
			Quantity: item.Quantity,
		})
	}
	return items
}

func validPeriod(period Period) bool {
	return !period.Start.IsZero() &&
		!period.End.IsZero() &&
		period.End.After(period.Start)
}

func verifyPaddleSignature(
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
	for _, component := range strings.Split(header, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(component), "=")
		if !ok {
			continue
		}
		switch key {
		case "ts":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return errors.New("invalid Paddle signature timestamp")
			}
			timestamp = parsed
		case "h1":
			signatures = append(signatures, value)
		}
	}
	if timestamp <= 0 || len(signatures) == 0 {
		return errors.New("incomplete Paddle signature")
	}
	signedAt := time.Unix(timestamp, 0)
	if now.Sub(signedAt) > tolerance || signedAt.Sub(now) > tolerance {
		return errors.New("expired Paddle signature")
	}
	payload := strconv.FormatInt(timestamp, 10) + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	expected := mac.Sum(nil)
	for _, signature := range signatures {
		decoded, err := hex.DecodeString(signature)
		if err == nil && hmac.Equal(expected, decoded) {
			return nil
		}
	}
	return errors.New("Paddle signature mismatch")
}
