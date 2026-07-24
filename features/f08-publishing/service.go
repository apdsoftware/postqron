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
	"strings"
	"time"
)

type Store interface {
	Enqueue(context.Context, Job) (EnqueueResult, error)
	ClaimDue(context.Context, time.Time, time.Time, string) (Destination, bool, error)
	MarkCancelled(context.Context, string, string, Diagnostic, time.Time) error
	MarkPublished(context.Context, string, string, string, time.Time) error
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

// Publisher must make IdempotencyKey durable at the provider boundary and
// return the same RemoteID when a completed request is replayed.
type Publisher interface {
	Publish(context.Context, PublishRequest) (PublishResult, error)
}

// PublisherResolver is supplied by runtime discovery; publishing keeps no
// central provider registry.
type PublisherResolver interface {
	ResolvePublisher(context.Context, string) (Publisher, error)
}

type RetryAuthorizer interface {
	CanRetryPublication(context.Context, string, string) (bool, error)
}

type Engine struct {
	store      Store
	gate       CommandGate
	resolver   PublisherResolver
	authorizer RetryAuthorizer
	policy     RetryPolicy
	now        func() time.Time
	random     func([]byte) error
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
		store:      store,
		gate:       gate,
		resolver:   resolver,
		authorizer: authorizer,
		policy:     policy,
		now:        time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
	}
	for _, option := range options {
		option(engine)
	}
	if engine.now == nil || engine.random == nil {
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
		destinationID, idErr := engine.randomID("pubdst")
		if idErr != nil {
			return EnqueueResult{}, idErr
		}
		maxAttempts := input.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = engine.policy.MaxAttempts
		}
		job.Destinations = append(job.Destinations, Destination{
			ID:           destinationID,
			JobID:        job.ID,
			CommandID:    job.CommandID,
			WorkspaceID:  job.WorkspaceID,
			PostID:       job.PostID,
			Generation:   job.Generation,
			ChannelID:    strings.TrimSpace(input.ChannelID),
			Provider:     strings.TrimSpace(input.Provider),
			ConnectionID: strings.TrimSpace(input.ConnectionID),
			Payload:      append([]byte(nil), input.Payload...),
			IdempotencyKey: destinationIdempotencyKey(
				request.Command.InvalidationKey,
				input.ChannelID,
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

	publisher, err := engine.resolver.ResolvePublisher(ctx, destination.Provider)
	if err != nil {
		return true, engine.handleFailure(
			ctx,
			destination,
			fmt.Errorf("resolve provider adapter: %w", err),
			now,
		)
	}
	result, publishErr := publisher.Publish(ctx, PublishRequest{
		WorkspaceID:    destination.WorkspaceID,
		PostID:         destination.PostID,
		ChannelID:      destination.ChannelID,
		ConnectionID:   destination.ConnectionID,
		Payload:        append([]byte(nil), destination.Payload...),
		IdempotencyKey: destination.IdempotencyKey,
	})
	if publishErr != nil {
		return true, engine.handleFailure(ctx, destination, publishErr, now)
	}
	remoteID := strings.TrimSpace(result.RemoteID)
	if remoteID == "" {
		return true, engine.handleFailure(
			ctx,
			destination,
			&ProviderError{
				Code:   "missing_remote_id",
				Detail: "Provider returned success without a remote publication id.",
			},
			now,
		)
	}
	if err := engine.store.MarkPublished(
		ctx,
		destination.ID,
		destination.LeaseToken,
		remoteID,
		now,
	); err != nil {
		return true, fmt.Errorf("record published destination: %w", err)
	}
	return true, nil
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
			strings.TrimSpace(destination.ConnectionID) == "" ||
			!providerPattern.MatchString(strings.TrimSpace(destination.Provider)) ||
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

func destinationIdempotencyKey(commandKey, channelID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(commandKey) + "\x00" + strings.TrimSpace(channelID)))
	return "publish_" + hex.EncodeToString(sum[:])
}

func (engine *Engine) randomID(prefix string) (string, error) {
	var value [18]byte
	if err := engine.random(value[:]); err != nil {
		return "", fmt.Errorf("create %s id: %w", prefix, err)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(value[:]), nil
}
