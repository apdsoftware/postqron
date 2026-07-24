package integrations

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
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	WebhookEventVersion = "2026-07-01"
	maxWebhookPayload   = 256 << 10
)

var sensitivePayloadKeys = map[string]struct{}{
	"access_token":   {},
	"authorization":  {},
	"cookie":         {},
	"password":       {},
	"provider_token": {},
	"refresh_token":  {},
	"secret":         {},
	"token":          {},
}

type WebhookEvent struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Version     string          `json:"version"`
	WorkspaceID string          `json:"workspace_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Data        json.RawMessage `json:"data"`
}

type WebhookSubscription struct {
	ID          string
	WorkspaceID string
	Endpoint    string
	EventTypes  map[string]struct{}
	// SigningSecret is decrypted only while claiming a delivery. It must never
	// be logged, emitted in metrics, or returned by a read API.
	SigningSecret []byte
	Active        bool
}

func (subscription WebhookSubscription) Accepts(eventType string) bool {
	if !subscription.Active {
		return false
	}
	_, accepted := subscription.EventTypes[eventType]
	return accepted
}

type WebhookDelivery struct {
	ID             string
	EventID        string
	SubscriptionID string
	WorkspaceID    string
	Attempt        int
	NextAttemptAt  time.Time
}

type ClaimedWebhookDelivery struct {
	Delivery     WebhookDelivery
	Subscription WebhookSubscription
	Event        WebhookEvent
	Envelope     []byte
}

type WebhookSubscriptionRepository interface {
	ListActive(
		ctx context.Context,
		workspaceID string,
		eventType string,
	) ([]WebhookSubscription, error)
}

// WebhookQueue persists the event and all deliveries atomically. ClaimDue must
// lease rows so concurrent workers cannot send the same attempt.
type WebhookQueue interface {
	Enqueue(
		ctx context.Context,
		event WebhookEvent,
		envelope []byte,
		subscriptions []WebhookSubscription,
	) error
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]ClaimedWebhookDelivery, error)
	MarkDelivered(
		ctx context.Context,
		deliveryID string,
		deliveredAt time.Time,
		statusCode int,
	) error
	Reschedule(
		ctx context.Context,
		deliveryID string,
		nextAttemptAt time.Time,
		statusCode int,
		errorCode string,
	) error
	MoveToDeadLetter(
		ctx context.Context,
		deliveryID string,
		failedAt time.Time,
		statusCode int,
		errorCode string,
	) error
}

type WebhookPublisher struct {
	clock         Clock
	metrics       *WebhookMetrics
	queue         WebhookQueue
	subscriptions WebhookSubscriptionRepository
}

func NewWebhookPublisher(
	subscriptions WebhookSubscriptionRepository,
	queue WebhookQueue,
	metrics *WebhookMetrics,
	clock Clock,
) (*WebhookPublisher, error) {
	if subscriptions == nil {
		return nil, errors.New("webhook subscription repository is required")
	}
	if queue == nil {
		return nil, errors.New("webhook queue is required")
	}
	if metrics == nil {
		metrics = &WebhookMetrics{}
	}
	if clock == nil {
		clock = systemClock
	}
	return &WebhookPublisher{
		clock:         clock,
		metrics:       metrics,
		queue:         queue,
		subscriptions: subscriptions,
	}, nil
}

func (publisher *WebhookPublisher) Publish(ctx context.Context, event WebhookEvent) error {
	if event.Version == "" {
		event.Version = WebhookEventVersion
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = publisher.clock()
	}
	if err := ValidateWebhookEvent(event); err != nil {
		return err
	}
	subscriptions, err := publisher.subscriptions.ListActive(
		ctx,
		event.WorkspaceID,
		event.Type,
	)
	if err != nil {
		return fmt.Errorf("list webhook subscriptions: %w", err)
	}
	filtered := make([]WebhookSubscription, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		if subscription.WorkspaceID != event.WorkspaceID ||
			!subscription.Accepts(event.Type) ||
			ValidateWebhookEndpoint(subscription.Endpoint) != nil {
			continue
		}
		filtered = append(filtered, subscription)
	}
	if len(filtered) == 0 {
		return nil
	}
	envelope, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode webhook event: %w", err)
	}
	if err := publisher.queue.Enqueue(ctx, event, envelope, filtered); err != nil {
		return fmt.Errorf("enqueue webhook event: %w", err)
	}
	publisher.metrics.RecordEnqueued(len(filtered))
	return nil
}

func ValidateWebhookEvent(event WebhookEvent) error {
	switch {
	case strings.TrimSpace(event.ID) == "":
		return fmt.Errorf("%w: event ID is required", ErrInvalidArgument)
	case strings.TrimSpace(event.WorkspaceID) == "":
		return fmt.Errorf("%w: workspace ID is required", ErrInvalidArgument)
	case !validEventType(event.Type):
		return fmt.Errorf("%w: event type is invalid", ErrInvalidArgument)
	case event.Version != WebhookEventVersion:
		return fmt.Errorf("%w: unsupported event version", ErrInvalidArgument)
	case event.OccurredAt.IsZero():
		return fmt.Errorf("%w: occurrence time is required", ErrInvalidArgument)
	case len(event.Data) == 0 || len(event.Data) > maxWebhookPayload:
		return fmt.Errorf("%w: event data must contain at most 256 KiB", ErrInvalidArgument)
	}
	var payload any
	decoder := json.NewDecoder(bytes.NewReader(event.Data))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("%w: event data must be valid JSON", ErrInvalidArgument)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: event data must contain exactly one JSON value", ErrInvalidArgument)
	}
	if _, object := payload.(map[string]any); !object {
		return fmt.Errorf("%w: event data must be a JSON object", ErrInvalidArgument)
	}
	if containsSensitivePayloadKey(payload) {
		return fmt.Errorf("%w: event data contains a forbidden credential field", ErrInvalidArgument)
	}
	return nil
}

func validEventType(eventType string) bool {
	if len(eventType) < 3 || len(eventType) > 100 ||
		eventType[0] == '.' || eventType[len(eventType)-1] == '.' {
		return false
	}
	for _, character := range eventType {
		if character != '.' && character != '_' &&
			(character < 'a' || character > 'z') &&
			(character < '0' || character > '9') {
			return false
		}
	}
	return strings.Contains(eventType, ".")
}

func containsSensitivePayloadKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			if _, forbidden := sensitivePayloadKeys[normalized]; forbidden {
				return true
			}
			if containsSensitivePayloadKey(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsSensitivePayloadKey(nested) {
				return true
			}
		}
	}
	return false
}

type DeliveryResult struct {
	StatusCode int
	RetryAfter time.Duration
}

type WebhookSender interface {
	Send(
		ctx context.Context,
		endpoint string,
		envelope []byte,
		headers http.Header,
	) (DeliveryResult, error)
}

type WebhookObserver interface {
	ObserveWebhook(ctx context.Context, observation WebhookObservation)
}

type WebhookObservation struct {
	DeliveryID string
	EventType  string
	Outcome    string
	Attempt    int
	StatusCode int
	Duration   time.Duration
}

type RetryPolicy struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	MaxAttempts  int
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		InitialDelay: 30 * time.Second,
		MaxDelay:     6 * time.Hour,
		MaxAttempts:  8,
	}
}

type WebhookProcessorConfig struct {
	Clock       Clock
	Metrics     *WebhookMetrics
	Observer    WebhookObserver
	Queue       WebhookQueue
	Retry       RetryPolicy
	SendTimeout time.Duration
	Sender      WebhookSender
}

type WebhookProcessor struct {
	clock       Clock
	metrics     *WebhookMetrics
	observer    WebhookObserver
	queue       WebhookQueue
	retry       RetryPolicy
	sendTimeout time.Duration
	sender      WebhookSender
}

func NewWebhookProcessor(config WebhookProcessorConfig) (*WebhookProcessor, error) {
	if config.Queue == nil {
		return nil, errors.New("webhook queue is required")
	}
	if config.Sender == nil {
		return nil, errors.New("webhook sender is required")
	}
	if config.Clock == nil {
		config.Clock = systemClock
	}
	if config.Metrics == nil {
		config.Metrics = &WebhookMetrics{}
	}
	if config.Retry.MaxAttempts == 0 {
		config.Retry = DefaultRetryPolicy()
	}
	if config.Retry.MaxAttempts < 1 ||
		config.Retry.MaxAttempts > 8 ||
		config.Retry.InitialDelay <= 0 ||
		config.Retry.MaxDelay < config.Retry.InitialDelay {
		return nil, errors.New("invalid webhook retry policy")
	}
	if config.SendTimeout == 0 {
		config.SendTimeout = 10 * time.Second
	}
	if config.SendTimeout < time.Second || config.SendTimeout > 30*time.Second {
		return nil, errors.New("webhook send timeout must be between 1 and 30 seconds")
	}
	return &WebhookProcessor{
		clock:       config.Clock,
		metrics:     config.Metrics,
		observer:    config.Observer,
		queue:       config.Queue,
		retry:       config.Retry,
		sendTimeout: config.SendTimeout,
		sender:      config.Sender,
	}, nil
}

func (processor *WebhookProcessor) ProcessDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 {
		return 0, fmt.Errorf("%w: batch limit must be between 1 and 100", ErrInvalidArgument)
	}
	deliveries, err := processor.queue.ClaimDue(ctx, processor.clock(), limit)
	if err != nil {
		return 0, fmt.Errorf("claim webhook deliveries: %w", err)
	}
	var processErrors []error
	for _, delivery := range deliveries {
		if err := processor.processOne(ctx, delivery); err != nil {
			processErrors = append(processErrors, err)
		}
	}
	return len(deliveries), errors.Join(processErrors...)
}

func (processor *WebhookProcessor) processOne(
	ctx context.Context,
	claimed ClaimedWebhookDelivery,
) error {
	startedAt := processor.clock()
	delivery := claimed.Delivery
	if delivery.Attempt < 1 {
		delivery.Attempt = 1
	}
	if claimed.Subscription.ID != delivery.SubscriptionID ||
		claimed.Subscription.WorkspaceID != delivery.WorkspaceID ||
		claimed.Event.ID != delivery.EventID ||
		claimed.Event.WorkspaceID != delivery.WorkspaceID ||
		len(claimed.Subscription.SigningSecret) < 32 {
		return processor.deadLetter(
			ctx,
			claimed,
			0,
			"invalid_delivery",
			startedAt,
		)
	}
	signature, err := SignWebhook(
		claimed.Subscription.SigningSecret,
		startedAt,
		claimed.Envelope,
	)
	if err != nil {
		return processor.deadLetter(ctx, claimed, 0, "signing_error", startedAt)
	}
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("User-Agent", "Postqron-Webhooks/1.0")
	headers.Set("Postqron-Delivery-ID", delivery.ID)
	headers.Set("Postqron-Event", claimed.Event.Type)
	headers.Set("Postqron-Event-Version", claimed.Event.Version)
	headers.Set("Postqron-Signature", signature)
	headers.Set("Postqron-Timestamp", strconv.FormatInt(startedAt.Unix(), 10))

	sendContext, cancel := context.WithTimeout(ctx, processor.sendTimeout)
	result, sendErr := processor.sender.Send(
		sendContext,
		claimed.Subscription.Endpoint,
		claimed.Envelope,
		headers,
	)
	cancel()
	duration := processor.clock().Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	if sendErr == nil && result.StatusCode >= 200 && result.StatusCode < 300 {
		if err := processor.queue.MarkDelivered(
			ctx,
			delivery.ID,
			processor.clock(),
			result.StatusCode,
		); err != nil {
			return fmt.Errorf("mark webhook delivery complete: %w", err)
		}
		processor.metrics.RecordDelivered(duration)
		processor.observe(ctx, claimed, "delivered", result.StatusCode, duration)
		return nil
	}

	errorCode, retryable := classifyDeliveryFailure(result.StatusCode, sendErr)
	if !retryable || delivery.Attempt >= processor.retry.MaxAttempts {
		return processor.deadLetter(ctx, claimed, result.StatusCode, errorCode, startedAt)
	}
	delay := processor.retryDelay(delivery.ID, delivery.Attempt)
	if result.RetryAfter > delay {
		delay = result.RetryAfter
	}
	if delay > processor.retry.MaxDelay {
		delay = processor.retry.MaxDelay
	}
	if err := processor.queue.Reschedule(
		ctx,
		delivery.ID,
		processor.clock().Add(delay),
		result.StatusCode,
		errorCode,
	); err != nil {
		return fmt.Errorf("reschedule webhook delivery: %w", err)
	}
	processor.metrics.RecordRetried()
	processor.observe(ctx, claimed, "retrying", result.StatusCode, duration)
	return nil
}

func (processor *WebhookProcessor) deadLetter(
	ctx context.Context,
	claimed ClaimedWebhookDelivery,
	statusCode int,
	errorCode string,
	startedAt time.Time,
) error {
	if err := processor.queue.MoveToDeadLetter(
		ctx,
		claimed.Delivery.ID,
		processor.clock(),
		statusCode,
		errorCode,
	); err != nil {
		return fmt.Errorf("move webhook delivery to dead letter queue: %w", err)
	}
	duration := processor.clock().Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	processor.metrics.RecordDeadLetter()
	processor.observe(ctx, claimed, "dead_lettered", statusCode, duration)
	return nil
}

func (processor *WebhookProcessor) observe(
	ctx context.Context,
	claimed ClaimedWebhookDelivery,
	outcome string,
	statusCode int,
	duration time.Duration,
) {
	if processor.observer == nil {
		return
	}
	processor.observer.ObserveWebhook(ctx, WebhookObservation{
		DeliveryID: claimed.Delivery.ID,
		EventType:  claimed.Event.Type,
		Outcome:    outcome,
		Attempt:    claimed.Delivery.Attempt,
		StatusCode: statusCode,
		Duration:   duration,
	})
}

func (processor *WebhookProcessor) retryDelay(deliveryID string, attempt int) time.Duration {
	delay := processor.retry.InitialDelay
	for currentAttempt := 1; currentAttempt < attempt; currentAttempt++ {
		if delay >= processor.retry.MaxDelay/2 {
			delay = processor.retry.MaxDelay
			break
		}
		delay *= 2
	}
	if delay > processor.retry.MaxDelay {
		delay = processor.retry.MaxDelay
	}
	digest := sha256.Sum256([]byte(deliveryID + ":" + strconv.Itoa(attempt)))
	// Stable 0–20% jitter prevents synchronized retries while keeping tests
	// deterministic.
	jitter := time.Duration(digest[0]%21) * delay / 100
	if delay > processor.retry.MaxDelay-jitter {
		return processor.retry.MaxDelay
	}
	return delay + jitter
}

func classifyDeliveryFailure(statusCode int, sendErr error) (string, bool) {
	if sendErr != nil {
		if errors.Is(sendErr, context.DeadlineExceeded) {
			return "timeout", true
		}
		return "network_error", true
	}
	switch {
	case statusCode == http.StatusRequestTimeout:
		return "http_408", true
	case statusCode == http.StatusTooEarly:
		return "http_425", true
	case statusCode == http.StatusTooManyRequests:
		return "http_429", true
	case statusCode >= 500 && statusCode <= 599:
		return "http_5xx", true
	case statusCode >= 400 && statusCode <= 499:
		return "http_4xx", false
	default:
		return "invalid_http_status", false
	}
}

func SignWebhook(secret []byte, timestamp time.Time, envelope []byte) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("webhook signing secret must contain at least 32 bytes")
	}
	if timestamp.IsZero() {
		return "", errors.New("webhook timestamp is required")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(envelope)
	return "t=" + strconv.FormatInt(timestamp.Unix(), 10) +
		",v1=" + hex.EncodeToString(mac.Sum(nil)), nil
}

func VerifyWebhook(
	secret []byte,
	signatureHeader string,
	envelope []byte,
	now time.Time,
	tolerance time.Duration,
) error {
	if len(secret) < 32 || tolerance <= 0 {
		return errors.New("invalid webhook verification configuration")
	}
	var timestampText, signatureText string
	for _, part := range strings.Split(signatureHeader, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch name {
		case "t":
			timestampText = value
		case "v1":
			signatureText = value
		}
	}
	timestampUnix, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return errors.New("invalid webhook signature timestamp")
	}
	timestamp := time.Unix(timestampUnix, 0)
	age := now.Sub(timestamp)
	if age < -tolerance || age > tolerance {
		return errors.New("webhook signature timestamp outside tolerance")
	}
	expected, err := SignWebhook(secret, timestamp, envelope)
	if err != nil {
		return err
	}
	_, expectedHex, _ := strings.Cut(expected, "v1=")
	provided, err := hex.DecodeString(signatureText)
	if err != nil {
		return errors.New("invalid webhook signature encoding")
	}
	expectedBytes, _ := hex.DecodeString(expectedHex)
	if !hmac.Equal(provided, expectedBytes) {
		return errors.New("webhook signature mismatch")
	}
	return nil
}

func ValidateWebhookEndpoint(rawEndpoint string) error {
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil {
		return fmt.Errorf("%w: invalid webhook endpoint", ErrInvalidArgument)
	}
	host := strings.ToLower(endpoint.Hostname())
	if endpoint.Scheme != "https" ||
		endpoint.User != nil ||
		endpoint.Fragment != "" ||
		host == "" ||
		strings.Contains(host, "%") ||
		host == "localhost" ||
		strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") {
		return fmt.Errorf("%w: webhook endpoint must be a public HTTPS URL", ErrInvalidArgument)
	}
	if address, parseErr := netip.ParseAddr(host); parseErr == nil && !publicWebhookAddress(address) {
		return fmt.Errorf("%w: webhook endpoint resolves to a non-public address", ErrInvalidArgument)
	}
	return nil
}

func publicWebhookAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() ||
		!address.IsGlobalUnicast() ||
		address.IsPrivate() ||
		address.IsLoopback() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicWebhookPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var nonPublicWebhookPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

type HTTPWebhookSender struct {
	client *http.Client
}

func NewHTTPWebhookSender(timeout time.Duration) (*HTTPWebhookSender, error) {
	if timeout < time.Second || timeout > 30*time.Second {
		return nil, errors.New("HTTP webhook timeout must be between 1 and 30 seconds")
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("webhook endpoint DNS lookup failed")
			}
			for _, resolved := range addresses {
				if !publicWebhookAddress(resolved.Unmap()) {
					return nil, errors.New("webhook endpoint resolved to a non-public address")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
		},
	}
	return &HTTPWebhookSender{
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("webhook redirects are not followed")
			},
		},
	}, nil
}

func (sender *HTTPWebhookSender) Send(
	ctx context.Context,
	endpoint string,
	envelope []byte,
	headers http.Header,
) (DeliveryResult, error) {
	if err := ValidateWebhookEndpoint(endpoint); err != nil {
		return DeliveryResult{}, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint,
		bytes.NewReader(envelope),
	)
	if err != nil {
		return DeliveryResult{}, err
	}
	request.Header = headers.Clone()
	response, err := sender.client.Do(request)
	if err != nil {
		return DeliveryResult{}, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	return DeliveryResult{
		StatusCode: response.StatusCode,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
	}, nil
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 &&
		seconds <= int((24*time.Hour)/time.Second) {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil && retryAt.After(now) {
		return retryAt.Sub(now)
	}
	return 0
}
