package publishing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

type Store interface {
	Enqueue(context.Context, Job) (EnqueueResult, error)
	ClaimDue(context.Context, time.Time, time.Time, string) (Destination, bool, error)
	MarkCancelled(context.Context, string, string, Diagnostic, time.Time) error
	MarkPublished(context.Context, string, string, PublishResult, time.Time) error
	MarkProgress(context.Context, string, string, json.RawMessage, time.Time, time.Time) error
	MarkNotified(context.Context, string, string, string, time.Time) error
	MarkRetry(context.Context, string, string, Diagnostic, time.Time) error
	MarkDeadLetter(context.Context, string, string, Diagnostic, time.Time) error
	RetryDeadLetter(context.Context, string, string, string, time.Time) (Destination, error)
	GetJob(context.Context, string, string) (Job, error)
}

// CommandGate is the F7 boundary. Implementations must verify that the command
// is still pending and is the scheduled post's active generation.
type CommandGate interface {
	IsCurrent(context.Context, string, string, int64) (bool, error)
}

// Publisher performs at most one remote side effect per Publish call. A
// multi-step adapter returns Complete=false with a durable checkpoint; F8
// persists it before the next remote step.
type Publisher interface {
	Capabilities() AdapterCapabilities
	Publish(context.Context, PublishRequest) (PublishResult, error)
	Reconcile(context.Context, ReconcileRequest) (ReconcileResult, error)
}

// PublisherResolver is supplied by runtime discovery; publishing keeps no
// central provider registry.
type PublisherResolver interface {
	ResolvePublisher(context.Context, string) (Publisher, error)
}

type NotificationPublisher interface {
	Capabilities() AdapterCapabilities
	Notify(context.Context, NotificationRequest) (NotificationResult, error)
}

type NotificationResolver interface {
	ResolveNotificationPublisher(context.Context, string) (NotificationPublisher, error)
}

type RetryAuthorizer interface {
	CanRetryPublication(context.Context, string, string) (bool, error)
}

type Engine struct {
	store         Store
	gate          CommandGate
	resolver      PublisherResolver
	notifications NotificationResolver
	authorizer    RetryAuthorizer
	policy        RetryPolicy
	now           func() time.Time
	random        func([]byte) error
}

type EngineOption func(*Engine)

func WithClock(clock func() time.Time) EngineOption {
	return func(engine *Engine) {
		engine.now = clock
	}
}

func WithRandom(random func([]byte) error) EngineOption {
	return func(engine *Engine) {
		engine.random = random
	}
}

func WithNotificationResolver(resolver NotificationResolver) EngineOption {
	return func(engine *Engine) {
		engine.notifications = resolver
	}
}

func NewEngine(
	store Store,
	gate CommandGate,
	resolver PublisherResolver,
	authorizer RetryAuthorizer,
	policy RetryPolicy,
	options ...EngineOption,
) (*Engine, error) {
	if store == nil || gate == nil || resolver == nil || authorizer == nil {
		return nil, fmt.Errorf(
			"%w: store, command gate, publisher resolver and retry authorizer are required",
			ErrInvalidArgument,
		)
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	engine := &Engine{
		store:         store,
		gate:          gate,
		resolver:      resolver,
		notifications: unavailableNotificationResolver{},
		authorizer:    authorizer,
		policy:        policy,
		now:           time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
	}
	for _, option := range options {
		option(engine)
	}
	if engine.now == nil || engine.random == nil || engine.notifications == nil {
		return nil, fmt.Errorf("%w: clock and random source are required", ErrInvalidArgument)
	}
	return engine, nil
}

func (engine *Engine) Enqueue(
	ctx context.Context,
	request EnqueueRequest,
) (EnqueueResult, error) {
	if err := validateEnqueue(request); err != nil {
		return EnqueueResult{}, err
	}
	current, err := engine.gate.IsCurrent(
		ctx,
		request.Command.WorkspaceID,
		request.Command.ID,
		request.Command.Generation,
	)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("verify publication command: %w", err)
	}
	if !current {
		return EnqueueResult{}, ErrConflict
	}

	now := engine.now().UTC()
	jobID, err := engine.randomID("pubjob")
	if err != nil {
		return EnqueueResult{}, err
	}
	job := Job{
		ID:              jobID,
		CommandID:       request.Command.ID,
		WorkspaceID:     request.Command.WorkspaceID,
		PostID:          request.Command.PostID,
		DraftID:         request.Command.DraftID,
		Generation:      request.Command.Generation,
		InvalidationKey: request.Command.InvalidationKey,
		Status:          JobQueued,
		ExecuteAtUTC:    request.Command.ExecuteAtUTC.UTC(),
		CreatedAt:       now,
		UpdatedAt:       now,
		Destinations:    make([]Destination, 0, len(request.Destinations)),
	}
	for _, input := range request.Destinations {
		capabilities, capabilityErr := engine.resolveCapabilities(ctx, input)
		if capabilityErr != nil {
			return EnqueueResult{}, capabilityErr
		}
		payload, payloadErr := canonicalJSON(input.Payload)
		if payloadErr != nil {
			return EnqueueResult{}, payloadErr
		}
		snapshotHash := destinationSnapshotHash(input, capabilities, payload)
		destinationID, idErr := engine.randomID("pubdst")
		if idErr != nil {
			return EnqueueResult{}, idErr
		}
		maxAttempts := input.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = engine.policy.MaxAttempts
		}
		job.Destinations = append(job.Destinations, Destination{
			ID:            destinationID,
			JobID:         job.ID,
			CommandID:     job.CommandID,
			WorkspaceID:   job.WorkspaceID,
			PostID:        job.PostID,
			Generation:    job.Generation,
			DraftRevision: input.DraftRevision,
			ChannelID:     strings.TrimSpace(input.ChannelID),
			Provider:      strings.TrimSpace(input.Provider),
			ConnectionID:  strings.TrimSpace(input.ConnectionID),
			Mode:          input.Mode,
			CapabilityID:  strings.TrimSpace(input.CapabilityID),
			Capabilities:  capabilities,
			Payload:       payload,
			SnapshotHash:  snapshotHash,
			IdempotencyKey: destinationIdempotencyKey(
				request.Command.InvalidationKey,
				input.ChannelID,
				input.DraftRevision,
				snapshotHash,
			),
			Status:        DestinationPending,
			MaxAttempts:   maxAttempts,
			NextAttemptAt: request.Command.ExecuteAtUTC.UTC(),
		})
	}
	result, err := engine.store.Enqueue(ctx, job)
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("persist publication job: %w", err)
	}
	return result, nil
}

// DispatchOne claims and processes at most one due destination. A lease token
// protects every transition from stale workers; an expired claim can safely be
// replayed because the provider receives the same durable idempotency key.
func (engine *Engine) DispatchOne(ctx context.Context) (bool, error) {
	now := engine.now().UTC()
	leaseToken, err := engine.randomID("lease")
	if err != nil {
		return false, err
	}
	destination, found, err := engine.store.ClaimDue(
		ctx,
		now,
		now.Add(engine.policy.Lease),
		leaseToken,
	)
	if err != nil {
		return false, fmt.Errorf("claim publication destination: %w", err)
	}
	if !found {
		return false, nil
	}

	current, err := engine.gate.IsCurrent(
		ctx,
		destination.WorkspaceID,
		destination.CommandID,
		destination.Generation,
	)
	if err != nil {
		return true, fmt.Errorf("revalidate publication command: %w", err)
	}
	if !current {
		diagnostic := Diagnostic{
			Code:   "command_invalidated",
			Detail: "The scheduled publication command is no longer active.",
			At:     now,
		}
		if err := engine.store.MarkCancelled(
			ctx,
			destination.ID,
			destination.LeaseToken,
			diagnostic,
			now,
		); err != nil {
			return true, fmt.Errorf("cancel invalidated publication: %w", err)
		}
		return true, nil
	}

	if destination.Mode == PublishingModeNotification {
		return true, engine.dispatchNotification(ctx, destination, now)
	}
	publisher, err := engine.resolver.ResolvePublisher(ctx, destination.Provider)
	if err != nil {
		return true, engine.handleFailure(
			ctx,
			destination,
			fmt.Errorf("resolve provider adapter: %w", err),
			now,
		)
	}
	capabilities := publisher.Capabilities()
	if err := validateRuntimeCapabilities(destination, capabilities); err != nil {
		return true, engine.handleFailure(ctx, destination, err, now)
	}
	if destination.NeedsReconciliation && !capabilities.NativeIdempotency &&
		!capabilities.Reconciliation {
		return true, engine.handleFailure(ctx, destination, &ProviderError{
			Code:      "ambiguous_outcome_fail_closed",
			Detail:    "The provider outcome cannot be reconciled safely.",
			Retryable: false,
			Ambiguous: true,
		}, now)
	}
	if destination.NeedsReconciliation && capabilities.Reconciliation {
		reconciled, reconcileErr := publisher.Reconcile(ctx, ReconcileRequest{
			WorkspaceID:    destination.WorkspaceID,
			PostID:         destination.PostID,
			ChannelID:      destination.ChannelID,
			ConnectionID:   destination.ConnectionID,
			Payload:        append([]byte(nil), destination.Payload...),
			Checkpoint:     append([]byte(nil), destination.Checkpoint...),
			IdempotencyKey: destination.IdempotencyKey,
		})
		if reconcileErr != nil {
			return true, engine.handleFailure(ctx, destination, reconcileErr, now)
		}
		switch reconciled.State {
		case ReconciliationFound:
			result := PublishResult{
				Complete:   true,
				RemoteID:   strings.TrimSpace(reconciled.RemoteID),
				Permalink:  strings.TrimSpace(reconciled.Permalink),
				Checkpoint: append([]byte(nil), reconciled.Checkpoint...),
			}
			if err := validateCompletedResult(result); err != nil {
				return true, engine.handleFailure(ctx, destination, err, now)
			}
			if err := engine.store.MarkPublished(
				ctx, destination.ID, destination.LeaseToken, result, now,
			); err != nil {
				return true, fmt.Errorf("record reconciled destination: %w", err)
			}
			return true, nil
		case ReconciliationNotFound:
			// A definitive not-found closes the ambiguous window. It is safe
			// to perform the next step under the same lease.
		case ReconciliationUnknown:
			return true, engine.handleFailure(ctx, destination, &ProviderError{
				Code:      "ambiguous_outcome",
				Detail:    reconciled.Diagnostic,
				Retryable: true,
				Ambiguous: true,
			}, now)
		default:
			return true, engine.handleFailure(ctx, destination, &ProviderError{
				Code:      "invalid_reconciliation",
				Detail:    "Adapter returned an invalid reconciliation state.",
				Retryable: false,
			}, now)
		}
	}
	result, publishErr := publisher.Publish(ctx, PublishRequest{
		WorkspaceID:    destination.WorkspaceID,
		PostID:         destination.PostID,
		ChannelID:      destination.ChannelID,
		ConnectionID:   destination.ConnectionID,
		Payload:        append([]byte(nil), destination.Payload...),
		Checkpoint:     append([]byte(nil), destination.Checkpoint...),
		IdempotencyKey: destination.IdempotencyKey,
	})
	if publishErr != nil {
		return true, engine.handleFailure(
			ctx,
			destination,
			classifyPublishError(publishErr, capabilities),
			now,
		)
	}
	if !result.Complete {
		if !capabilities.MultiStep || !jsonValidObject(result.Checkpoint) {
			return true, engine.handleFailure(ctx, destination, &ProviderError{
				Code:      "invalid_adapter_checkpoint",
				Detail:    "Adapter returned incomplete work without a valid durable checkpoint.",
				Retryable: false,
			}, now)
		}
		delay := result.RetryAfter
		if delay < 0 {
			delay = 0
		}
		if delay > engine.policy.MaxDelay {
			delay = engine.policy.MaxDelay
		}
		if err := engine.store.MarkProgress(
			ctx,
			destination.ID,
			destination.LeaseToken,
			result.Checkpoint,
			now.Add(delay),
			now,
		); err != nil {
			return true, fmt.Errorf("persist publication checkpoint: %w", err)
		}
		return true, nil
	}
	if err := validateCompletedResult(result); err != nil {
		return true, engine.handleFailure(ctx, destination, err, now)
	}
	if err := engine.store.MarkPublished(
		ctx,
		destination.ID,
		destination.LeaseToken,
		result,
		now,
	); err != nil {
		return true, fmt.Errorf("record published destination: %w", err)
	}
	return true, nil
}

func (engine *Engine) dispatchNotification(
	ctx context.Context,
	destination Destination,
	now time.Time,
) error {
	notifier, err := engine.notifications.ResolveNotificationPublisher(
		ctx,
		destination.Provider,
	)
	if err != nil {
		return engine.handleFailure(ctx, destination, fmt.Errorf(
			"resolve notification publisher: %w", err,
		), now)
	}
	if err := validateRuntimeCapabilities(destination, notifier.Capabilities()); err != nil {
		return engine.handleFailure(ctx, destination, err, now)
	}
	result, err := notifier.Notify(ctx, NotificationRequest{
		WorkspaceID:    destination.WorkspaceID,
		PostID:         destination.PostID,
		ChannelID:      destination.ChannelID,
		Payload:        append([]byte(nil), destination.Payload...),
		IdempotencyKey: destination.IdempotencyKey,
	})
	if err != nil {
		return engine.handleFailure(ctx, destination, err, now)
	}
	deliveryID := strings.TrimSpace(result.DeliveryID)
	if deliveryID == "" {
		return engine.handleFailure(ctx, destination, &ProviderError{
			Code:      "missing_notification_id",
			Detail:    "Notification publisher returned no durable delivery id.",
			Retryable: false,
		}, now)
	}
	if err := engine.store.MarkNotified(
		ctx, destination.ID, destination.LeaseToken, deliveryID, now,
	); err != nil {
		return fmt.Errorf("record notification delivery: %w", err)
	}
	return nil
}

func (engine *Engine) RetryDestination(
	ctx context.Context,
	command RetryDestinationCommand,
) (Destination, error) {
	workspaceID := strings.TrimSpace(command.WorkspaceID)
	actorID := strings.TrimSpace(command.ActorID)
	destinationID := strings.TrimSpace(command.DestinationID)
	if workspaceID == "" || actorID == "" || destinationID == "" {
		return Destination{}, fmt.Errorf("%w: retry identifiers are required", ErrInvalidArgument)
	}
	allowed, err := engine.authorizer.CanRetryPublication(ctx, workspaceID, actorID)
	if err != nil {
		return Destination{}, fmt.Errorf("authorize manual publication retry: %w", err)
	}
	if !allowed {
		return Destination{}, ErrForbidden
	}
	destination, err := engine.store.RetryDeadLetter(
		ctx,
		workspaceID,
		destinationID,
		actorID,
		engine.now().UTC(),
	)
	if err != nil {
		return Destination{}, fmt.Errorf("retry dead-letter destination: %w", err)
	}
	return destination, nil
}

func (engine *Engine) GetJob(
	ctx context.Context,
	workspaceID, jobID string,
) (Job, error) {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(jobID) == "" {
		return Job{}, fmt.Errorf("%w: workspace and job ids are required", ErrInvalidArgument)
	}
	return engine.store.GetJob(ctx, workspaceID, jobID)
}

func (engine *Engine) handleFailure(
	ctx context.Context,
	destination Destination,
	failure error,
	now time.Time,
) error {
	diagnostic, providerDelay := diagnosticFromError(failure, now)
	if diagnostic.Retryable && destination.CycleAttemptCount < destination.MaxAttempts {
		next := now.Add(engine.policy.Delay(destination.CycleAttemptCount, providerDelay))
		if err := engine.store.MarkRetry(
			ctx,
			destination.ID,
			destination.LeaseToken,
			diagnostic,
			next,
		); err != nil {
			return fmt.Errorf("schedule publication retry: %w", err)
		}
		return nil
	}
	diagnostic.Retryable = false
	if err := engine.store.MarkDeadLetter(
		ctx,
		destination.ID,
		destination.LeaseToken,
		diagnostic,
		now,
	); err != nil {
		return fmt.Errorf("dead-letter publication destination: %w", err)
	}
	return nil
}

func diagnosticFromError(err error, now time.Time) (Diagnostic, time.Duration) {
	diagnostic := Diagnostic{
		Code:   "publication_failed",
		Detail: sanitizeDiagnostic(err.Error()),
		At:     now,
	}
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		diagnostic.Code = sanitizeCode(providerError.Code)
		diagnostic.Detail = sanitizeDiagnostic(providerError.Detail)
		diagnostic.Retryable = providerError.Retryable
		diagnostic.Ambiguous = providerError.Ambiguous
		return diagnostic, providerError.RetryAfter
	}
	return diagnostic, 0
}

func validateEnqueue(request EnqueueRequest) error {
	command := request.Command
	if strings.TrimSpace(command.ID) == "" ||
		strings.TrimSpace(command.WorkspaceID) == "" ||
		strings.TrimSpace(command.PostID) == "" ||
		strings.TrimSpace(command.DraftID) == "" ||
		command.Generation < 1 ||
		command.ExecuteAtUTC.IsZero() ||
		command.State != CommandPending ||
		strings.TrimSpace(command.InvalidationKey) == "" {
		return fmt.Errorf("%w: invalid publication command", ErrInvalidArgument)
	}
	if len(request.Destinations) == 0 {
		return fmt.Errorf("%w: at least one destination is required", ErrInvalidArgument)
	}
	channels := make(map[string]struct{}, len(request.Destinations))
	for _, destination := range request.Destinations {
		channelID := strings.TrimSpace(destination.ChannelID)
		if channelID == "" ||
			strings.TrimSpace(destination.Provider) == "" ||
			!providerPattern.MatchString(strings.TrimSpace(destination.Provider)) ||
			destination.DraftRevision < 1 ||
			strings.TrimSpace(destination.CapabilityID) == "" ||
			strings.TrimSpace(destination.CapabilityVersion) == "" ||
			(destination.Mode != PublishingModeAuto &&
				destination.Mode != PublishingModeNotification) ||
			(destination.Mode == PublishingModeAuto &&
				strings.TrimSpace(destination.ConnectionID) == "") ||
			!jsonValidObject(destination.Payload) ||
			destination.MaxAttempts < 0 {
			return fmt.Errorf("%w: invalid publication destination", ErrInvalidArgument)
		}
		if _, duplicate := channels[channelID]; duplicate {
			return fmt.Errorf("%w: duplicate channel destination", ErrInvalidArgument)
		}
		channels[channelID] = struct{}{}
	}
	return nil
}

func jsonValidObject(value []byte) bool {
	if !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func destinationIdempotencyKey(
	commandKey, channelID string,
	draftRevision int64,
	snapshotHash string,
) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%d\x00%s",
		strings.TrimSpace(commandKey),
		strings.TrimSpace(channelID),
		draftRevision,
		snapshotHash,
	)))
	return "publish_" + hex.EncodeToString(sum[:])
}

func (engine *Engine) resolveCapabilities(
	ctx context.Context,
	input DestinationInput,
) (AdapterCapabilities, error) {
	provider := strings.TrimSpace(input.Provider)
	var capabilities AdapterCapabilities
	switch input.Mode {
	case PublishingModeAuto:
		publisher, err := engine.resolver.ResolvePublisher(ctx, provider)
		if err != nil {
			return AdapterCapabilities{}, fmt.Errorf(
				"%w: %s", ErrProviderUnavailable, sanitizeDiagnostic(err.Error()),
			)
		}
		capabilities = publisher.Capabilities()
		if !capabilities.NativeIdempotency && !capabilities.Reconciliation &&
			!capabilities.AmbiguousFailClosed {
			return AdapterCapabilities{}, ErrUnsafeAdapter
		}
	case PublishingModeNotification:
		notifier, err := engine.notifications.ResolveNotificationPublisher(
			ctx,
			provider,
		)
		if err != nil {
			return AdapterCapabilities{}, fmt.Errorf(
				"%w: %s", ErrProviderUnavailable, sanitizeDiagnostic(err.Error()),
			)
		}
		capabilities = notifier.Capabilities()
		if !capabilities.NotificationIdempotency {
			return AdapterCapabilities{}, ErrUnsafeAdapter
		}
	default:
		return AdapterCapabilities{}, ErrInvalidArgument
	}
	if capabilities.Mode != input.Mode ||
		strings.TrimSpace(capabilities.Version) == "" ||
		capabilities.Version != strings.TrimSpace(input.CapabilityVersion) {
		return AdapterCapabilities{}, fmt.Errorf(
			"%w: adapter capability version or mode mismatch",
			ErrProviderUnavailable,
		)
	}
	return capabilities, nil
}

func validateRuntimeCapabilities(
	destination Destination,
	current AdapterCapabilities,
) error {
	if current != destination.Capabilities {
		return &ProviderError{
			Code:      "adapter_capability_drift",
			Detail:    "The provider adapter no longer matches the immutable capability snapshot.",
			Retryable: false,
		}
	}
	if destination.Mode == PublishingModeAuto &&
		!current.NativeIdempotency && !current.Reconciliation &&
		!current.AmbiguousFailClosed {
		return &ProviderError{
			Code:      "unsafe_adapter",
			Detail:    "The provider adapter cannot safely handle an ambiguous request.",
			Retryable: false,
		}
	}
	if destination.Mode == PublishingModeNotification &&
		!current.NotificationIdempotency {
		return &ProviderError{
			Code:      "unsafe_notification_adapter",
			Detail:    "The notification adapter does not guarantee idempotent delivery.",
			Retryable: false,
		}
	}
	return nil
}

func classifyPublishError(
	err error,
	capabilities AdapterCapabilities,
) error {
	if err == nil {
		return err
	}
	transportFailure := errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF)
	var networkError net.Error
	transportFailure = transportFailure || errors.As(err, &networkError)
	var requestError *url.Error
	transportFailure = transportFailure || errors.As(err, &requestError)
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		copyOfError := *providerError
		if transportFailure {
			copyOfError.Retryable = true
			copyOfError.Ambiguous =
				copyOfError.Ambiguous || !capabilities.NativeIdempotency
		}
		return &copyOfError
	}
	if !transportFailure {
		return err
	}
	return &ProviderError{
		Code:      "transport_outcome_unknown",
		Detail:    err.Error(),
		Retryable: true,
		Ambiguous: !capabilities.NativeIdempotency,
	}
}

func validateCompletedResult(result PublishResult) error {
	if !result.Complete || strings.TrimSpace(result.RemoteID) == "" {
		return &ProviderError{
			Code:      "missing_remote_id",
			Detail:    "Provider returned success without a remote publication id.",
			Retryable: false,
		}
	}
	if result.Permalink != "" {
		parsed, err := url.ParseRequestURI(strings.TrimSpace(result.Permalink))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
			parsed.User != nil {
			return &ProviderError{
				Code:      "invalid_permalink",
				Detail:    "Provider returned an invalid publication permalink.",
				Retryable: false,
			}
		}
	}
	if len(result.Checkpoint) > 0 && !jsonValidObject(result.Checkpoint) {
		return &ProviderError{
			Code:      "invalid_adapter_checkpoint",
			Detail:    "Provider returned an invalid final checkpoint.",
			Retryable: false,
		}
	}
	return nil
}

func canonicalJSON(value []byte) (json.RawMessage, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, fmt.Errorf("%w: invalid destination payload", ErrInvalidArgument)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: canonicalize destination payload", ErrInvalidArgument)
	}
	return canonical, nil
}

func destinationSnapshotHash(
	input DestinationInput,
	capabilities AdapterCapabilities,
	payload json.RawMessage,
) string {
	envelope := struct {
		ChannelID         string
		Provider          string
		ConnectionID      string
		Mode              PublishingMode
		DraftRevision     int64
		CapabilityID      string
		CapabilityVersion string
		Capabilities      AdapterCapabilities
		Payload           json.RawMessage
	}{
		ChannelID:         strings.TrimSpace(input.ChannelID),
		Provider:          strings.TrimSpace(input.Provider),
		ConnectionID:      strings.TrimSpace(input.ConnectionID),
		Mode:              input.Mode,
		DraftRevision:     input.DraftRevision,
		CapabilityID:      strings.TrimSpace(input.CapabilityID),
		CapabilityVersion: strings.TrimSpace(input.CapabilityVersion),
		Capabilities:      capabilities,
		Payload:           payload,
	}
	encoded, _ := json.Marshal(envelope)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

type unavailableNotificationResolver struct{}

func (unavailableNotificationResolver) ResolveNotificationPublisher(
	context.Context,
	string,
) (NotificationPublisher, error) {
	return nil, ErrProviderUnavailable
}

func (engine *Engine) randomID(prefix string) (string, error) {
	var value [18]byte
	if err := engine.random(value[:]); err != nil {
		return "", fmt.Errorf("create %s id: %w", prefix, err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(value[:]), nil
}
