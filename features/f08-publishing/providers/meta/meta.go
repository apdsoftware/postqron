// Package meta implements the F8 publishing adapters for the Meta provider
// family. Credentials and provider routing remain exclusively inside the F5
// AuthenticatedExecutor boundary.
package meta

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const capabilityVersion = "meta-graph-v1"

type authenticatedExecutor interface {
	Execute(
		context.Context,
		socialconnections.PublishingRequest,
	) (socialconnections.PublishingResponse, error)
}

type Publisher struct {
	executor     authenticatedExecutor
	provider     socialconnections.Provider
	graphVersion string
}

type Config struct {
	Executor     *socialconnections.AuthenticatedExecutor
	Provider     socialconnections.Provider
	GraphVersion string
}

func NewPublisher(config Config) (*Publisher, error) {
	return newPublisher(config.Executor, config.Provider, config.GraphVersion)
}

func newPublisher(
	executor authenticatedExecutor,
	provider socialconnections.Provider,
	graphVersion string,
) (*Publisher, error) {
	if executor == nil ||
		(provider != socialconnections.ProviderFacebookPages &&
			provider != socialconnections.ProviderInstagramProfessional &&
			provider != socialconnections.ProviderThreads) ||
		!validGraphVersion(graphVersion) {
		return nil, publishing.ErrInvalidArgument
	}
	return &Publisher{
		executor: executor, provider: provider, graphVersion: graphVersion,
	}, nil
}

func (publisher *Publisher) Capabilities() publishing.AdapterCapabilities {
	return publishing.AdapterCapabilities{
		Version:           capabilityVersion,
		Mode:              publishing.PublishingModeAuto,
		Reconciliation:    true,
		MultiStep:         true,
		RemotePermalink:   true,
		NativeIdempotency: false,
	}
}

type Payload struct {
	Format string  `json:"format"`
	Text   string  `json:"text,omitempty"`
	Link   string  `json:"link_url,omitempty"`
	Media  []Media `json:"media,omitempty"`
}

type Media struct {
	URL     string `json:"url"`
	AltText string `json:"alt_text,omitempty"`
}

type checkpoint struct {
	Step        string    `json:"step"`
	ContainerID string    `json:"container_id,omitempty"`
	ChildIDs    []string  `json:"child_ids,omitempty"`
	NextMedia   int       `json:"next_media,omitempty"`
	RemoteID    string    `json:"remote_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (publisher *Publisher) Publish(
	ctx context.Context,
	request publishing.PublishRequest,
) (publishing.PublishResult, error) {
	payload, state, err := publisher.validate(request)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	switch publisher.provider {
	case socialconnections.ProviderFacebookPages:
		return publisher.publishFacebook(ctx, request, payload, state)
	case socialconnections.ProviderInstagramProfessional:
		return publisher.publishContainer(ctx, request, payload, state, "media", "media_publish")
	case socialconnections.ProviderThreads:
		return publisher.publishContainer(ctx, request, payload, state, "threads", "threads_publish")
	default:
		return publishing.PublishResult{}, permanent("provider_not_supported")
	}
}

func (publisher *Publisher) publishFacebook(
	ctx context.Context,
	request publishing.PublishRequest,
	payload Payload,
	state checkpoint,
) (publishing.PublishResult, error) {
	if state.Step == "published" && strings.TrimSpace(state.RemoteID) != "" {
		permalink, err := publisher.lookupPermalink(
			ctx,
			request,
			state.RemoteID,
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		return publishing.PublishResult{
			Complete: true, RemoteID: state.RemoteID, Permalink: permalink,
		}, nil
	}
	if state.Step != "" {
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
	values := url.Values{}
	path := publisher.graphPath("me", "feed")
	switch payload.Format {
	case "text":
		values.Set("message", payload.Text)
	case "link":
		values.Set("message", payload.Text)
		values.Set("link", payload.Link)
	case "image":
		path = publisher.graphPath("me", "photos")
		values.Set("caption", payload.Text)
		values.Set("url", payload.Media[0].URL)
	default:
		return publishing.PublishResult{}, permanent("capability_not_supported")
	}
	response, err := publisher.execute(
		ctx, request, http.MethodPost, path, values,
	)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	remoteID, err := responseID(response.Body)
	if err != nil {
		return publishing.PublishResult{}, ambiguous("provider_response_invalid", 0)
	}
	return progress(checkpoint{
		Step: "published", RemoteID: remoteID, CreatedAt: time.Now().UTC(),
	}, 0)
}

func (publisher *Publisher) publishContainer(
	ctx context.Context,
	request publishing.PublishRequest,
	payload Payload,
	state checkpoint,
	createEndpoint, publishEndpoint string,
) (publishing.PublishResult, error) {
	if payload.Format == "carousel" &&
		(state.Step == "" || state.Step == "carousel_children") {
		if state.Step == "" {
			state = checkpoint{
				Step: "carousel_children", CreatedAt: time.Now().UTC(),
			}
		}
		if state.NextMedia < 0 || state.NextMedia > len(payload.Media) ||
			len(state.ChildIDs) != state.NextMedia {
			return publishing.PublishResult{}, permanent("checkpoint_invalid")
		}
		if state.NextMedia < len(payload.Media) {
			values := url.Values{
				"is_carousel_item": {"true"},
				"media_type":       {"IMAGE"},
				"image_url":        {payload.Media[state.NextMedia].URL},
				"alt_text":         {payload.Media[state.NextMedia].AltText},
			}
			response, executeErr := publisher.execute(
				ctx,
				request,
				http.MethodPost,
				publisher.graphPath("me", createEndpoint),
				values,
			)
			if executeErr != nil {
				return publishing.PublishResult{}, executeErr
			}
			childID, parseErr := responseID(response.Body)
			if parseErr != nil {
				return publishing.PublishResult{}, ambiguous(
					"provider_response_invalid",
					0,
				)
			}
			state.ChildIDs = append(state.ChildIDs, childID)
			state.NextMedia++
			return progress(state, 0)
		}
		values := url.Values{
			"media_type": {"CAROUSEL"},
			"children":   {strings.Join(state.ChildIDs, ",")},
		}
		if publisher.provider == socialconnections.ProviderInstagramProfessional {
			values.Set("caption", payload.Text)
		} else {
			values.Set("text", payload.Text)
		}
		response, executeErr := publisher.execute(
			ctx,
			request,
			http.MethodPost,
			publisher.graphPath("me", createEndpoint),
			values,
		)
		if executeErr != nil {
			return publishing.PublishResult{}, executeErr
		}
		containerID, parseErr := responseID(response.Body)
		if parseErr != nil {
			return publishing.PublishResult{}, ambiguous(
				"provider_response_invalid",
				0,
			)
		}
		state.Step, state.ContainerID = "container_created", containerID
		return progress(state, 0)
	}
	if state.Step == "" {
		values, err := publisher.containerValues(payload)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		response, executeErr := publisher.execute(
			ctx,
			request,
			http.MethodPost,
			publisher.graphPath("me", createEndpoint),
			values,
		)
		if executeErr != nil {
			return publishing.PublishResult{}, executeErr
		}
		containerID, parseErr := responseID(response.Body)
		if parseErr != nil {
			return publishing.PublishResult{}, ambiguous("provider_response_invalid", 0)
		}
		state = checkpoint{
			Step: "container_created", ContainerID: containerID,
			CreatedAt: time.Now().UTC(),
		}
		return progress(state, 0)
	}
	if state.Step == "container_created" {
		ready, retryAfter, err := publisher.containerReady(ctx, request, state.ContainerID)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		if !ready {
			return progress(state, retryAfter)
		}
		state.Step = "container_ready"
		return progress(state, 0)
	}
	if state.Step == "container_ready" {
		response, err := publisher.execute(
			ctx,
			request,
			http.MethodPost,
			publisher.graphPath("me", publishEndpoint),
			url.Values{"creation_id": {state.ContainerID}},
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		remoteID, parseErr := responseID(response.Body)
		if parseErr != nil {
			return publishing.PublishResult{}, ambiguous("provider_response_invalid", 0)
		}
		state.Step, state.RemoteID = "published", remoteID
		return progress(state, 0)
	}
	if state.Step != "published" || strings.TrimSpace(state.RemoteID) == "" {
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
	permalink, err := publisher.lookupPermalink(ctx, request, state.RemoteID)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	return publishing.PublishResult{
		Complete: true, RemoteID: state.RemoteID, Permalink: permalink,
	}, nil
}

func (publisher *Publisher) Reconcile(
	ctx context.Context,
	request publishing.ReconcileRequest,
) (publishing.ReconcileResult, error) {
	_, state, err := publisher.validate(publishing.PublishRequest{
		WorkspaceID: request.WorkspaceID, PostID: request.PostID,
		ChannelID: request.ChannelID, ConnectionID: request.ConnectionID,
		Payload: request.Payload, Checkpoint: request.Checkpoint,
		IdempotencyKey: request.IdempotencyKey,
	})
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	if strings.TrimSpace(state.RemoteID) == "" {
		// No provider-visible identifier means the outcome cannot be proven.
		// Returning unknown prevents F8 from issuing a blind duplicate.
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "provider outcome cannot be uniquely identified",
		}, nil
	}
	permalink, err := publisher.lookupPermalink(
		ctx,
		publishing.PublishRequest{
			WorkspaceID: request.WorkspaceID, ConnectionID: request.ConnectionID,
		},
		state.RemoteID,
	)
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	return publishing.ReconcileResult{
		State: publishing.ReconciliationFound, RemoteID: state.RemoteID,
		Permalink: permalink, Checkpoint: request.Checkpoint,
	}, nil
}

func (publisher *Publisher) validate(
	request publishing.PublishRequest,
) (Payload, checkpoint, error) {
	if publisher == nil || publisher.executor == nil ||
		strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.ConnectionID) == "" ||
		strings.TrimSpace(request.IdempotencyKey) == "" {
		return Payload{}, checkpoint{}, publishing.ErrInvalidArgument
	}
	var payload Payload
	if err := strictJSON(request.Payload, &payload); err != nil {
		return Payload{}, checkpoint{}, permanent("payload_invalid")
	}
	if err := validatePayload(publisher.provider, payload); err != nil {
		return Payload{}, checkpoint{}, err
	}
	var state checkpoint
	if len(request.Checkpoint) != 0 {
		if err := strictJSON(request.Checkpoint, &state); err != nil ||
			state.Step == "" || state.CreatedAt.IsZero() {
			return Payload{}, checkpoint{}, permanent("checkpoint_invalid")
		}
	}
	return payload, state, nil
}

func validatePayload(provider socialconnections.Provider, payload Payload) error {
	if !utf8.ValidString(payload.Text) {
		return permanent("payload_invalid")
	}
	textLimit := 5000
	if provider == socialconnections.ProviderInstagramProfessional {
		textLimit = 2200
	}
	if provider == socialconnections.ProviderThreads {
		textLimit = 500
	}
	if utf8.RuneCountInString(payload.Text) > textLimit {
		return permanent("payload_invalid")
	}
	switch payload.Format {
	case "text":
		if strings.TrimSpace(payload.Text) == "" || len(payload.Media) != 0 ||
			(provider != socialconnections.ProviderFacebookPages &&
				provider != socialconnections.ProviderThreads) {
			return permanent("payload_invalid")
		}
	case "link":
		if strings.TrimSpace(payload.Text) == "" ||
			!absoluteHTTPS(payload.Link) || len(payload.Media) != 0 ||
			provider != socialconnections.ProviderFacebookPages {
			return permanent("payload_invalid")
		}
	case "image", "reel":
		if len(payload.Media) != 1 || !absoluteHTTPS(payload.Media[0].URL) {
			return permanent("payload_invalid")
		}
	case "carousel":
		maximum := 10
		if provider == socialconnections.ProviderThreads {
			maximum = 20
		}
		if len(payload.Media) < 2 || len(payload.Media) > maximum ||
			(provider != socialconnections.ProviderInstagramProfessional &&
				provider != socialconnections.ProviderThreads) {
			return permanent("payload_invalid")
		}
		for _, media := range payload.Media {
			if !absoluteHTTPS(media.URL) {
				return permanent("payload_invalid")
			}
		}
	default:
		return permanent("capability_not_supported")
	}
	return nil
}

func (publisher *Publisher) containerValues(payload Payload) (url.Values, error) {
	values := url.Values{}
	if publisher.provider == socialconnections.ProviderInstagramProfessional {
		values.Set("caption", payload.Text)
	} else {
		values.Set("text", payload.Text)
	}
	switch payload.Format {
	case "text":
		values.Set("media_type", "TEXT")
	case "image":
		values.Set("media_type", "IMAGE")
		values.Set("image_url", payload.Media[0].URL)
		values.Set("alt_text", payload.Media[0].AltText)
	case "reel":
		if publisher.provider == socialconnections.ProviderInstagramProfessional {
			values.Set("media_type", "REELS")
		} else {
			values.Set("media_type", "VIDEO")
		}
		values.Set("video_url", payload.Media[0].URL)
	case "carousel":
		return nil, permanent("checkpoint_invalid")
	default:
		return nil, permanent("capability_not_supported")
	}
	return values, nil
}

func (publisher *Publisher) containerReady(
	ctx context.Context,
	request publishing.PublishRequest,
	containerID string,
) (bool, time.Duration, error) {
	response, err := publisher.execute(
		ctx,
		request,
		http.MethodGet,
		publisher.graphPath(containerID)+"?fields=id,status_code",
		nil,
	)
	if err != nil {
		return false, 0, err
	}
	var result struct {
		ID         string `json:"id"`
		StatusCode string `json:"status_code"`
		Status     string `json:"status"`
	}
	if err := strictJSON(response.Body, &result); err != nil ||
		strings.TrimSpace(result.ID) != containerID {
		return false, 0, temporary("provider_response_invalid", 0)
	}
	status := strings.TrimSpace(result.StatusCode)
	if status == "" {
		status = strings.TrimSpace(result.Status)
	}
	switch strings.ToUpper(status) {
	case "FINISHED", "PUBLISHED":
		return true, 0, nil
	case "ERROR", "EXPIRED":
		return false, 0, permanent("media_processing_failed")
	default:
		return false, 5 * time.Second, nil
	}
}

func (publisher *Publisher) lookupPermalink(
	ctx context.Context,
	request publishing.PublishRequest,
	remoteID string,
) (string, error) {
	response, err := publisher.execute(
		ctx,
		request,
		http.MethodGet,
		publisher.graphPath(remoteID)+"?fields=id,permalink,permalink_url",
		nil,
	)
	if err != nil {
		return "", err
	}
	var result struct {
		ID           string `json:"id"`
		Permalink    string `json:"permalink"`
		PermalinkURL string `json:"permalink_url"`
	}
	if err := strictJSON(response.Body, &result); err != nil ||
		strings.TrimSpace(result.ID) != remoteID {
		return "", temporary("provider_response_invalid", 0)
	}
	permalink := strings.TrimSpace(result.Permalink)
	if permalink == "" {
		permalink = strings.TrimSpace(result.PermalinkURL)
	}
	if !absoluteHTTPS(permalink) {
		return "", temporary("provider_permalink_unavailable", 0)
	}
	return permalink, nil
}

func (publisher *Publisher) execute(
	ctx context.Context,
	request publishing.PublishRequest,
	method, path string,
	values url.Values,
) (socialconnections.PublishingResponse, error) {
	var body []byte
	header := make(http.Header)
	if values != nil {
		body = []byte(values.Encode())
		header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := publisher.executor.Execute(
		ctx,
		socialconnections.PublishingRequest{
			WorkspaceID: request.WorkspaceID, ConnectionID: request.ConnectionID,
			ExpectedProvider: publisher.provider, Method: method, Path: path,
			Header: header, Body: body,
		},
	)
	if err != nil {
		return socialconnections.PublishingResponse{}, mapExecutorError(err)
	}
	return response, nil
}

func (publisher *Publisher) graphPath(parts ...string) string {
	escaped := make([]string, 0, len(parts)+1)
	escaped = append(escaped, publisher.graphVersion)
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(strings.TrimSpace(part)))
	}
	return "/" + strings.Join(escaped, "/")
}

func responseID(body []byte) (string, error) {
	var response struct {
		ID string `json:"id"`
	}
	if err := strictJSON(body, &response); err != nil ||
		strings.TrimSpace(response.ID) == "" {
		return "", errors.New("provider response id is missing")
	}
	return strings.TrimSpace(response.ID), nil
}

func progress(state checkpoint, retryAfter time.Duration) (publishing.PublishResult, error) {
	body, err := json.Marshal(state)
	if err != nil {
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
	return publishing.PublishResult{
		Complete: false, Checkpoint: body, RetryAfter: retryAfter,
	}, nil
}

func mapExecutorError(err error) error {
	var failure *socialconnections.ExecutorFailure
	if !errors.As(err, &failure) {
		return permanent("authenticated_executor_failed")
	}
	switch failure.Kind {
	case socialconnections.ExecutorFailureRateLimit,
		socialconnections.ExecutorFailureTemporary:
		return temporary(failure.Code, failure.RetryAfter)
	case socialconnections.ExecutorFailureAmbiguous:
		return ambiguous(failure.Code, failure.RetryAfter)
	default:
		return permanent(failure.Code)
	}
}

type ResponseClassifier struct{}

func (ResponseClassifier) ClassifyProviderResponse(
	evidence socialconnections.ProviderResponseEvidence,
) (socialconnections.ProviderResponseClassification, bool) {
	var envelope struct {
		Error struct {
			Code        int  `json:"code"`
			Subcode     int  `json:"error_subcode"`
			IsTransient bool `json:"is_transient"`
		} `json:"error"`
	}
	if json.Unmarshal(evidence.Body, &envelope) != nil {
		return socialconnections.ProviderResponseClassification{}, false
	}
	retryAfter := retryAfterHeader(evidence.Header.Get("Retry-After"))
	if evidence.StatusCode == http.StatusTooManyRequests {
		// Let F5's generic classifier preserve both delta-seconds and HTTP-date
		// Retry-After values using its trusted clock.
		return socialconnections.ProviderResponseClassification{}, false
	}
	if envelope.Error.Code == 4 || envelope.Error.Code == 17 ||
		envelope.Error.Code == 32 || envelope.Error.Code == 613 {
		return socialconnections.ProviderResponseClassification{
			Kind:       socialconnections.ExecutorFailureRateLimit,
			RetryAfter: retryAfter,
		}, true
	}
	if envelope.Error.IsTransient {
		return socialconnections.ProviderResponseClassification{
			Kind:       socialconnections.ExecutorFailureTemporary,
			RetryAfter: retryAfter,
		}, true
	}
	if evidence.StatusCode == http.StatusUnauthorized ||
		evidence.StatusCode == http.StatusForbidden ||
		envelope.Error.Code == 190 ||
		envelope.Error.Subcode == 458 || envelope.Error.Subcode == 460 ||
		envelope.Error.Subcode == 463 || envelope.Error.Subcode == 467 {
		return socialconnections.ProviderResponseClassification{
			Kind:      socialconnections.ExecutorFailureReconnect,
			Reconnect: true,
		}, true
	}
	return socialconnections.ProviderResponseClassification{
		Kind: socialconnections.ExecutorFailurePermanent,
	}, true
}

func ResponseClassifiers() map[socialconnections.Provider]socialconnections.ProviderResponseClassifier {
	classifier := ResponseClassifier{}
	return map[socialconnections.Provider]socialconnections.ProviderResponseClassifier{
		socialconnections.ProviderFacebookPages:         classifier,
		socialconnections.ProviderInstagramProfessional: classifier,
		socialconnections.ProviderThreads:               classifier,
	}
}

func retryAfterHeader(value string) time.Duration {
	seconds, err := time.ParseDuration(strings.TrimSpace(value) + "s")
	if err != nil || seconds < 0 {
		return 0
	}
	return seconds
}

func temporary(code string, retryAfter time.Duration) *publishing.ProviderError {
	return &publishing.ProviderError{
		Code: code, Detail: "Meta request can be retried",
		Retryable: true, RetryAfter: retryAfter,
	}
}

func ambiguous(code string, retryAfter time.Duration) *publishing.ProviderError {
	return &publishing.ProviderError{
		Code: code, Detail: "Meta outcome requires reconciliation",
		Ambiguous: true, RetryAfter: retryAfter,
	}
}

func permanent(code string) *publishing.ProviderError {
	return &publishing.ProviderError{
		Code: code, Detail: "Meta request was rejected",
	}
}

func strictJSON(source []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(source)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validGraphVersion(value string) bool {
	if len(value) < 4 || value[0] != 'v' || !strings.HasSuffix(value, ".0") {
		return false
	}
	if value[1] < '1' || value[1] > '9' {
		return false
	}
	for _, character := range value[1 : len(value)-2] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func absoluteHTTPS(value string) bool {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(value))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.Fragment == ""
}

// NotificationStore persists user-directed manual publishing notifications.
// PutIfAbsent must be atomic on idempotencyKey.
type NotificationStore interface {
	PutIfAbsent(context.Context, string, string, string, json.RawMessage) (string, error)
}

type NotificationPublisher struct {
	provider string
	store    NotificationStore
}

func NewNotificationPublisher(
	provider string,
	store NotificationStore,
) (*NotificationPublisher, error) {
	provider = strings.TrimSpace(provider)
	if store == nil ||
		(provider != string(socialconnections.ProviderFacebookGroups) &&
			provider != string(socialconnections.ProviderInstagramPersonal)) {
		return nil, publishing.ErrInvalidArgument
	}
	return &NotificationPublisher{provider: provider, store: store}, nil
}

func (*NotificationPublisher) Capabilities() publishing.AdapterCapabilities {
	return publishing.AdapterCapabilities{
		Version: capabilityVersion, Mode: publishing.PublishingModeNotification,
		NotificationIdempotency: true,
	}
}

func (publisher *NotificationPublisher) Notify(
	ctx context.Context,
	request publishing.NotificationRequest,
) (publishing.NotificationResult, error) {
	if publisher == nil || publisher.store == nil ||
		strings.TrimSpace(request.WorkspaceID) == "" ||
		strings.TrimSpace(request.PostID) == "" ||
		strings.TrimSpace(request.ChannelID) == "" ||
		strings.TrimSpace(request.IdempotencyKey) == "" ||
		!json.Valid(request.Payload) {
		return publishing.NotificationResult{}, publishing.ErrInvalidArgument
	}
	var payload Payload
	if strictJSON(request.Payload, &payload) != nil ||
		validateNotificationPayload(publisher.provider, payload) != nil {
		return publishing.NotificationResult{}, publishing.ErrInvalidArgument
	}
	deliveryID, err := publisher.store.PutIfAbsent(
		ctx, publisher.provider, request.WorkspaceID,
		request.IdempotencyKey, append(json.RawMessage(nil), request.Payload...),
	)
	if err != nil {
		return publishing.NotificationResult{}, err
	}
	if strings.TrimSpace(deliveryID) == "" {
		return publishing.NotificationResult{}, permanent("notification_delivery_invalid")
	}
	return publishing.NotificationResult{DeliveryID: deliveryID}, nil
}

func validateNotificationPayload(provider string, payload Payload) error {
	if !utf8.ValidString(payload.Text) ||
		utf8.RuneCountInString(payload.Text) > 5000 {
		return publishing.ErrInvalidArgument
	}
	switch payload.Format {
	case "text":
		if provider == string(socialconnections.ProviderInstagramPersonal) ||
			strings.TrimSpace(payload.Text) == "" || len(payload.Media) != 0 {
			return publishing.ErrInvalidArgument
		}
	case "link":
		if provider == string(socialconnections.ProviderInstagramPersonal) ||
			!absoluteHTTPS(payload.Link) || len(payload.Media) != 0 {
			return publishing.ErrInvalidArgument
		}
	case "image", "reel":
		if len(payload.Media) != 1 || !absoluteHTTPS(payload.Media[0].URL) {
			return publishing.ErrInvalidArgument
		}
	case "carousel":
		if len(payload.Media) < 2 || len(payload.Media) > 20 {
			return publishing.ErrInvalidArgument
		}
		for _, media := range payload.Media {
			if !absoluteHTTPS(media.URL) {
				return publishing.ErrInvalidArgument
			}
		}
	default:
		return publishing.ErrInvalidArgument
	}
	return nil
}

func StableNotificationID(provider, idempotencyKey string) string {
	sum := sha256.Sum256([]byte(provider + "\x00" + idempotencyKey))
	return "meta_notification_" + hex.EncodeToString(sum[:16])
}

var (
	_ publishing.Publisher                         = (*Publisher)(nil)
	_ publishing.NotificationPublisher             = (*NotificationPublisher)(nil)
	_ socialconnections.ProviderResponseClassifier = ResponseClassifier{}
)
