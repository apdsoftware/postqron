package analytics

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

type Service struct {
	repository  Repository
	resolver    ProviderResolver
	permissions PermissionReader
	limiter     RateLimiter
	authorizer  ViewerAuthorizer
	policy      RetryPolicy
	now         func() time.Time
	random      func([]byte) error
}

type ServiceOption func(*Service)

func WithClock(clock func() time.Time) ServiceOption {
	return func(service *Service) {
		service.now = clock
	}
}

func WithRandom(random func([]byte) error) ServiceOption {
	return func(service *Service) {
		service.random = random
	}
}

func NewService(
	repository Repository,
	resolver ProviderResolver,
	permissions PermissionReader,
	limiter RateLimiter,
	authorizer ViewerAuthorizer,
	policy RetryPolicy,
	options ...ServiceOption,
) (*Service, error) {
	if repository == nil || resolver == nil || permissions == nil ||
		limiter == nil || authorizer == nil {
		return nil, fmt.Errorf(
			"%w: repository, provider resolver, permission reader, rate limiter and authorizer are required",
			ErrInvalidArgument,
		)
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	service := &Service{
		repository:  repository,
		resolver:    resolver,
		permissions: permissions,
		limiter:     limiter,
		authorizer:  authorizer,
		policy:      policy,
		now:         time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
	}
	for _, option := range options {
		option(service)
	}
	if service.now == nil || service.random == nil {
		return nil, fmt.Errorf("%w: clock and random source are required", ErrInvalidArgument)
	}
	return service, nil
}

func (service *Service) RegisterPublished(
	ctx context.Context,
	published PublishedContent,
) (RegisterResult, error) {
	published.WorkspaceID = strings.TrimSpace(published.WorkspaceID)
	published.ContentID = strings.TrimSpace(published.ContentID)
	published.ChannelID = strings.TrimSpace(published.ChannelID)
	published.Provider = strings.TrimSpace(published.Provider)
	published.ConnectionID = strings.TrimSpace(published.ConnectionID)
	published.RemoteID = strings.TrimSpace(published.RemoteID)
	if published.WorkspaceID == "" || published.ContentID == "" ||
		published.ChannelID == "" || published.Provider == "" ||
		published.ConnectionID == "" || published.RemoteID == "" ||
		published.PublishedAt.IsZero() {
		return RegisterResult{}, fmt.Errorf("%w: published content identifiers are required", ErrInvalidArgument)
	}
	if published.Provider != "meta" {
		return RegisterResult{}, fmt.Errorf("%w: provider is outside D2", ErrInvalidArgument)
	}
	if _, supported := metricsFor(published.ChannelType); !supported {
		return RegisterResult{}, fmt.Errorf("%w: channel is outside D2", ErrInvalidArgument)
	}
	now := service.now().UTC()
	target := SyncTarget{
		ID:           targetID(published),
		WorkspaceID:  published.WorkspaceID,
		ContentID:    published.ContentID,
		ChannelID:    published.ChannelID,
		ChannelType:  published.ChannelType,
		Provider:     published.Provider,
		ConnectionID: published.ConnectionID,
		RemoteID:     published.RemoteID,
		PublishedAt:  published.PublishedAt.UTC(),
		State:        TargetPending,
		NextSyncAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	result, err := service.repository.Register(ctx, target)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("register analytics target: %w", err)
	}
	return result, nil
}

// SyncOne claims and processes at most one target. Provider adapters are called
// only after D2 scope, effective permission and low-priority capacity checks.
func (service *Service) SyncOne(ctx context.Context) (bool, error) {
	now := service.now().UTC()
	leaseToken, err := service.randomID("anllease")
	if err != nil {
		return false, err
	}
	target, found, err := service.repository.ClaimDue(
		ctx,
		now,
		now.Add(service.policy.Lease),
		leaseToken,
	)
	if err != nil {
		return false, fmt.Errorf("claim analytics target: %w", err)
	}
	if !found {
		return false, nil
	}

	allowedMetrics, supported := metricsFor(target.ChannelType)
	permission, permissionSupported := requiredPermission(target.ChannelType)
	if !supported || !permissionSupported || target.Provider != "meta" {
		return true, service.terminalFailure(ctx, target, "unsupported_target", now)
	}

	granted, err := service.permissions.GrantedPermissions(
		ctx,
		target.WorkspaceID,
		target.ConnectionID,
	)
	if err != nil {
		return true, service.handleFailure(ctx, target, err, now)
	}
	if !granted[permission] {
		observations := stateObservations(
			target.ID,
			allowedMetrics,
			MetricPermissionMissing,
			permission,
			now,
		)
		err = service.repository.SaveSuccess(
			ctx,
			target.ID,
			target.LeaseToken,
			observations,
			target.Cursor,
			TargetPermissionMissing,
			now.Add(service.policy.RefreshInterval),
			now,
		)
		if err != nil {
			return true, fmt.Errorf("record missing analytics permission: %w", err)
		}
		return true, nil
	}

	delay, err := service.limiter.Reserve(
		ctx,
		target.Provider,
		target.ChannelID,
		PriorityAnalytics,
	)
	if err != nil {
		return true, service.handleFailure(ctx, target, err, now)
	}
	if delay > 0 {
		if err := service.repository.Defer(
			ctx,
			target.ID,
			target.LeaseToken,
			now.Add(delay),
			now,
		); err != nil {
			return true, fmt.Errorf("defer rate-limited analytics target: %w", err)
		}
		return true, nil
	}

	adapter, err := service.resolver.ResolveAnalyticsProvider(ctx, target.Provider)
	if err != nil {
		return true, service.handleFailure(ctx, target, err, now)
	}
	result, err := adapter.Fetch(ctx, FetchRequest{
		WorkspaceID:  target.WorkspaceID,
		ContentID:    target.ContentID,
		ChannelID:    target.ChannelID,
		ChannelType:  target.ChannelType,
		ConnectionID: target.ConnectionID,
		RemoteID:     target.RemoteID,
		Cursor:       target.Cursor,
		Metrics:      slices.Clone(allowedMetrics),
	})
	if err != nil {
		return true, service.handleFailure(ctx, target, err, now)
	}
	observations, state, err := validateProviderMetrics(
		target.ID,
		allowedMetrics,
		result.Metrics,
	)
	if err != nil {
		return true, service.handleFailure(ctx, target, err, now)
	}
	if err := service.repository.SaveSuccess(
		ctx,
		target.ID,
		target.LeaseToken,
		observations,
		result.NextCursor,
		state,
		now.Add(service.policy.RefreshInterval),
		now,
	); err != nil {
		return true, fmt.Errorf("save analytics observations: %w", err)
	}
	return true, nil
}

func (service *Service) ChannelOverview(
	ctx context.Context,
	query OverviewQuery,
) (Overview, error) {
	query.WorkspaceID = strings.TrimSpace(query.WorkspaceID)
	query.ActorID = strings.TrimSpace(query.ActorID)
	if query.WorkspaceID == "" || query.ActorID == "" ||
		query.From.IsZero() || query.To.IsZero() ||
		!query.To.After(query.From) {
		return Overview{}, fmt.Errorf("%w: workspace, actor and valid interval are required", ErrInvalidArgument)
	}
	query.From = query.From.UTC()
	query.To = query.To.UTC()
	for index := range query.ChannelIDs {
		query.ChannelIDs[index] = strings.TrimSpace(query.ChannelIDs[index])
		if query.ChannelIDs[index] == "" {
			return Overview{}, fmt.Errorf("%w: empty channel filter", ErrInvalidArgument)
		}
	}
	if err := service.authorizer.CanViewAnalytics(
		ctx,
		query.WorkspaceID,
		query.ActorID,
	); err != nil {
		return Overview{}, fmt.Errorf("authorize analytics overview: %w", err)
	}
	overview, err := service.repository.Overview(ctx, query)
	if err != nil {
		return Overview{}, fmt.Errorf("load analytics overview: %w", err)
	}
	return overview, nil
}

func (service *Service) handleFailure(
	ctx context.Context,
	target SyncTarget,
	cause error,
	now time.Time,
) error {
	code := "analytics_sync_failed"
	retryable := true
	retryAfter := time.Duration(0)
	var providerError *ProviderError
	if errors.As(cause, &providerError) {
		code = safeErrorCode(providerError.Code)
		retryable = providerError.Retryable
		retryAfter = providerError.RetryAfter
	}
	nextFailure := target.ConsecutiveFailures + 1
	if !retryable || nextFailure >= service.policy.MaxAttempts {
		return service.terminalFailure(ctx, target, code, now)
	}
	next := now.Add(service.policy.Delay(nextFailure, retryAfter))
	if err := service.repository.SaveRetry(
		ctx,
		target.ID,
		target.LeaseToken,
		code,
		next,
		now,
	); err != nil {
		return fmt.Errorf("schedule analytics retry: %w", err)
	}
	return nil
}

func (service *Service) terminalFailure(
	ctx context.Context,
	target SyncTarget,
	code string,
	now time.Time,
) error {
	metrics, _ := metricsFor(target.ChannelType)
	observations := stateObservations(
		target.ID,
		metrics,
		MetricFailed,
		safeErrorCode(code),
		now,
	)
	if err := service.repository.SaveFailure(
		ctx,
		target.ID,
		target.LeaseToken,
		observations,
		safeErrorCode(code),
		now,
	); err != nil {
		return fmt.Errorf("record analytics failure: %w", err)
	}
	return nil
}

func validateProviderMetrics(
	targetID string,
	allowed []MetricName,
	providerMetrics []ProviderMetric,
) ([]Observation, TargetState, error) {
	if len(providerMetrics) != len(allowed) {
		return nil, "", fmt.Errorf("%w: provider must return one state per requested metric", ErrInvalidArgument)
	}
	allowedSet := make(map[MetricName]struct{}, len(allowed))
	for _, metric := range allowed {
		allowedSet[metric] = struct{}{}
	}
	seen := make(map[MetricName]struct{}, len(providerMetrics))
	observations := make([]Observation, 0, len(providerMetrics))
	available := 0
	for _, metric := range providerMetrics {
		if _, ok := allowedSet[metric.Metric]; !ok {
			return nil, "", fmt.Errorf("%w: provider returned metric outside D2", ErrInvalidArgument)
		}
		if _, duplicate := seen[metric.Metric]; duplicate {
			return nil, "", fmt.Errorf("%w: duplicate provider metric", ErrInvalidArgument)
		}
		seen[metric.Metric] = struct{}{}
		if strings.TrimSpace(metric.OriginalName) == "" ||
			strings.TrimSpace(metric.Period) == "" ||
			strings.TrimSpace(metric.APIVersion) == "" ||
			metric.ObservedAt.IsZero() {
			return nil, "", fmt.Errorf("%w: incomplete provider metric provenance", ErrInvalidArgument)
		}
		switch metric.State {
		case MetricAvailable:
			if metric.Value == nil || *metric.Value < 0 {
				return nil, "", fmt.Errorf("%w: available metric requires a non-negative value", ErrInvalidArgument)
			}
			available++
		case MetricUnavailable:
			if metric.Value != nil {
				return nil, "", fmt.Errorf("%w: unavailable metric cannot have a value", ErrInvalidArgument)
			}
		default:
			return nil, "", fmt.Errorf("%w: provider may return only available or unavailable", ErrInvalidArgument)
		}
		observations = append(observations, Observation{
			TargetID:     targetID,
			Metric:       metric.Metric,
			OriginalName: strings.TrimSpace(metric.OriginalName),
			Period:       strings.TrimSpace(metric.Period),
			ObservedAt:   metric.ObservedAt.UTC(),
			Value:        cloneInt64(metric.Value),
			State:        metric.State,
			APIVersion:   strings.TrimSpace(metric.APIVersion),
		})
	}
	state := TargetUnavailable
	if available > 0 {
		state = TargetCurrent
	}
	return observations, state, nil
}

func stateObservations(
	targetID string,
	metrics []MetricName,
	state MetricState,
	reason string,
	now time.Time,
) []Observation {
	observations := make([]Observation, 0, len(metrics))
	for _, metric := range metrics {
		observations = append(observations, Observation{
			TargetID:     targetID,
			Metric:       metric,
			OriginalName: string(metric),
			Period:       "lifetime",
			ObservedAt:   now,
			State:        state,
			ReasonCode:   reason,
		})
	}
	return observations
}

func targetID(published PublishedContent) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		published.WorkspaceID,
		published.Provider,
		published.ConnectionID,
		published.RemoteID,
	}, "\x00")))
	return "anltgt_" + hex.EncodeToString(sum[:])
}

func (service *Service) randomID(prefix string) (string, error) {
	payload := make([]byte, 16)
	if err := service.random(payload); err != nil {
		return "", fmt.Errorf("generate analytics identifier: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(payload), nil
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
