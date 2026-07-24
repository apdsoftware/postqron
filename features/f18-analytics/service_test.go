package analytics

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSyncUsesOnlyD2MetricsAndAdvancesCursor(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	adapter := &fakeAdapter{}
	resolver := &fakeResolver{adapter: adapter}
	service := newTestService(
		t,
		repository,
		resolver,
		&fakePermissions{granted: map[string]bool{"instagram_business_manage_insights": true}},
		&fakeLimiter{},
		&allowViewer{},
		func() time.Time { return now },
	)
	result, err := service.RegisterPublished(context.Background(), PublishedContent{
		WorkspaceID:  "workspace-1",
		ContentID:    "post-1",
		ChannelID:    "instagram-1",
		ChannelType:  ChannelInstagramProfessional,
		Provider:     "meta",
		ConnectionID: "connection-1",
		RemoteID:     "1789000001",
		PublishedAt:  now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created {
		t.Fatal("first registration should create the target")
	}

	adapter.fetch = func(request FetchRequest) (FetchResult, error) {
		if request.WorkspaceID != "workspace-1" ||
			request.ContentID != "post-1" ||
			request.ChannelID != "instagram-1" ||
			request.ConnectionID != "connection-1" ||
			request.RemoteID != "1789000001" {
			t.Fatalf("provider request lost target associations: %#v", request)
		}
		if request.Cursor != "" {
			t.Fatalf("first request should have no cursor, got %q", request.Cursor)
		}
		assertMetrics(t, request.Metrics, []MetricName{
			MetricReach,
			MetricLikes,
			MetricComments,
			MetricShares,
			MetricSaved,
			MetricViews,
			MetricPlays,
		})
		return availableResult(request.Metrics, now, "cursor-1"), nil
	}
	processed, err := service.SyncOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("sync one: processed=%v err=%v", processed, err)
	}

	now = now.Add(time.Hour)
	adapter.fetch = func(request FetchRequest) (FetchResult, error) {
		if request.Cursor != "cursor-1" {
			t.Fatalf("incremental cursor = %q, want cursor-1", request.Cursor)
		}
		return availableResult(request.Metrics, now, "cursor-2"), nil
	}
	processed, err = service.SyncOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("second sync: processed=%v err=%v", processed, err)
	}
	if adapter.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", adapter.calls)
	}
}

func TestMissingPermissionDoesNotCallProviderAndIsVisible(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	adapter := &fakeAdapter{}
	service := newTestService(
		t,
		repository,
		&fakeResolver{adapter: adapter},
		&fakePermissions{granted: map[string]bool{}},
		&fakeLimiter{},
		&allowViewer{},
		func() time.Time { return now },
	)
	_, err := service.RegisterPublished(context.Background(), PublishedContent{
		WorkspaceID:  "workspace-1",
		ContentID:    "post-1",
		ChannelID:    "facebook-1",
		ChannelType:  ChannelFacebookPage,
		Provider:     "meta",
		ConnectionID: "connection-1",
		RemoteID:     "remote-1",
		PublishedAt:  now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SyncOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 0 {
		t.Fatalf("provider called %d times without analytics permission", adapter.calls)
	}
	overview, err := service.ChannelOverview(context.Background(), OverviewQuery{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		From:        now.Add(-2 * time.Hour),
		To:          now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, metric := range overview.Channels[0].Metrics {
		if metric.State != MetricPermissionMissing || metric.Value != nil {
			t.Fatalf("missing permission metric not explicit: %#v", metric)
		}
	}
}

func TestRateLimitDefersLowPriorityWithoutProviderCall(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	adapter := &fakeAdapter{}
	limiter := &fakeLimiter{delay: 17 * time.Minute}
	service := newTestService(
		t,
		repository,
		&fakeResolver{adapter: adapter},
		&fakePermissions{granted: map[string]bool{"read_insights": true}},
		limiter,
		&allowViewer{},
		func() time.Time { return now },
	)
	registerFacebook(t, service, now, "post-1", "remote-1")
	if _, err := service.SyncOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 0 {
		t.Fatal("rate-limited analytics consumed provider capacity")
	}
	if limiter.priority != PriorityAnalytics {
		t.Fatalf("priority = %q, want %q", limiter.priority, PriorityAnalytics)
	}
	target := repository.targets[repository.order[0]]
	if !target.NextSyncAt.Equal(now.Add(17 * time.Minute)) {
		t.Fatalf("next sync = %v", target.NextSyncAt)
	}
	if target.ConsecutiveFailures != 0 {
		t.Fatal("capacity deferral must not consume retry attempts")
	}
}

func TestRetryHonorsProviderRetryAfterThenRecordsFailure(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	adapter := &fakeAdapter{
		fetch: func(FetchRequest) (FetchResult, error) {
			return FetchResult{}, &ProviderError{
				Code:       "rate_limited",
				Retryable:  true,
				RetryAfter: 3 * time.Hour,
			}
		},
	}
	service := newTestServiceWithPolicy(
		t,
		repository,
		&fakeResolver{adapter: adapter},
		&fakePermissions{granted: map[string]bool{"read_insights": true}},
		&fakeLimiter{},
		&allowViewer{},
		func() time.Time { return now },
		RetryPolicy{
			BaseDelay:       time.Minute,
			MaxDelay:        time.Hour,
			Lease:           time.Minute,
			RefreshInterval: time.Hour,
			MaxAttempts:     2,
		},
	)
	registerFacebook(t, service, now, "post-1", "remote-1")
	if _, err := service.SyncOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	target := repository.targets[repository.order[0]]
	if !target.NextSyncAt.Equal(now.Add(3 * time.Hour)) {
		t.Fatalf("Retry-After was capped: %v", target.NextSyncAt)
	}

	now = now.Add(3 * time.Hour)
	if _, err := service.SyncOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	target = repository.targets[repository.order[0]]
	if target.State != TargetFailed {
		t.Fatalf("state = %q, want failed", target.State)
	}
	for _, observation := range repository.observations[target.ID] {
		if observation.State != MetricFailed || observation.Value != nil {
			t.Fatalf("terminal failure observation = %#v", observation)
		}
	}
}

func TestOverviewDistinguishesZeroUnavailableAndMixed(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	targetOne := SyncTarget{
		ID:          "target-1",
		WorkspaceID: "workspace-1",
		ContentID:   "post-1",
		ChannelID:   "instagram-1",
		ChannelType: ChannelInstagramProfessional,
		PublishedAt: from.Add(time.Hour),
	}
	targetTwo := targetOne
	targetTwo.ID = "target-2"
	targetTwo.ContentID = "post-2"
	targetTwo.PublishedAt = from.Add(2 * time.Hour)
	zero := int64(0)
	query := OverviewQuery{
		WorkspaceID: "workspace-1",
		From:        from,
		To:          from.Add(24 * time.Hour),
	}
	overview := summarize(
		query,
		[]SyncTarget{targetOne, targetTwo},
		map[string][]Observation{
			"target-1": {
				{
					TargetID:   "target-1",
					Metric:     MetricReach,
					ObservedAt: from.Add(3 * time.Hour),
					Value:      &zero,
					State:      MetricAvailable,
				},
			},
			"target-2": {
				{
					TargetID:   "target-2",
					Metric:     MetricReach,
					ObservedAt: from.Add(3 * time.Hour),
					State:      MetricUnavailable,
				},
			},
		},
	)
	reach := overview.Channels[0].Metrics[0]
	if reach.State != MetricMixed || reach.Value == nil || *reach.Value != 0 {
		t.Fatalf("zero and unavailable collapsed: %#v", reach)
	}
	if reach.Targets.Available != 1 || reach.Targets.Unavailable != 1 {
		t.Fatalf("state counts = %#v", reach.Targets)
	}
}

func TestRejectsProviderAndChannelOutsideD2(t *testing.T) {
	now := time.Now().UTC()
	service := newTestService(
		t,
		NewMemoryRepository(),
		&fakeResolver{adapter: &fakeAdapter{}},
		&fakePermissions{},
		&fakeLimiter{},
		&allowViewer{},
		func() time.Time { return now },
	)
	_, err := service.RegisterPublished(context.Background(), PublishedContent{
		WorkspaceID:  "workspace-1",
		ContentID:    "post-1",
		ChannelID:    "linkedin-1",
		ChannelType:  "linkedin_page",
		Provider:     "linkedin",
		ConnectionID: "connection-1",
		RemoteID:     "remote-1",
		PublishedAt:  now,
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("error = %v, want invalid argument", err)
	}
}

type fakeAdapter struct {
	fetch func(FetchRequest) (FetchResult, error)
	calls int
}

func (adapter *fakeAdapter) Fetch(
	_ context.Context,
	request FetchRequest,
) (FetchResult, error) {
	adapter.calls++
	if adapter.fetch == nil {
		return availableResult(request.Metrics, time.Now().UTC(), "cursor"), nil
	}
	return adapter.fetch(request)
}

type fakeResolver struct {
	adapter ProviderAdapter
	err     error
}

func (resolver *fakeResolver) ResolveAnalyticsProvider(
	context.Context,
	string,
) (ProviderAdapter, error) {
	return resolver.adapter, resolver.err
}

type fakePermissions struct {
	granted map[string]bool
	err     error
}

func (permissions *fakePermissions) GrantedPermissions(
	context.Context,
	string,
	string,
) (map[string]bool, error) {
	return permissions.granted, permissions.err
}

type fakeLimiter struct {
	delay    time.Duration
	err      error
	priority WorkPriority
}

func (limiter *fakeLimiter) Reserve(
	_ context.Context,
	_, _ string,
	priority WorkPriority,
) (time.Duration, error) {
	limiter.priority = priority
	return limiter.delay, limiter.err
}

type allowViewer struct{}

func (*allowViewer) CanViewAnalytics(context.Context, string, string) error {
	return nil
}

func newTestService(
	t *testing.T,
	repository Repository,
	resolver ProviderResolver,
	permissions PermissionReader,
	limiter RateLimiter,
	authorizer ViewerAuthorizer,
	clock func() time.Time,
) *Service {
	t.Helper()
	return newTestServiceWithPolicy(
		t,
		repository,
		resolver,
		permissions,
		limiter,
		authorizer,
		clock,
		RetryPolicy{
			BaseDelay:       time.Minute,
			MaxDelay:        time.Hour,
			Lease:           time.Minute,
			RefreshInterval: time.Hour,
			MaxAttempts:     3,
		},
	)
}

func newTestServiceWithPolicy(
	t *testing.T,
	repository Repository,
	resolver ProviderResolver,
	permissions PermissionReader,
	limiter RateLimiter,
	authorizer ViewerAuthorizer,
	clock func() time.Time,
	policy RetryPolicy,
) *Service {
	t.Helper()
	service, err := NewService(
		repository,
		resolver,
		permissions,
		limiter,
		authorizer,
		policy,
		WithClock(clock),
		WithRandom(func(destination []byte) error {
			for index := range destination {
				destination[index] = byte(index + 1)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func availableResult(
	metrics []MetricName,
	observedAt time.Time,
	cursor string,
) FetchResult {
	result := FetchResult{NextCursor: cursor}
	for index, metric := range metrics {
		value := int64(index)
		result.Metrics = append(result.Metrics, ProviderMetric{
			Metric:       metric,
			OriginalName: string(metric),
			Period:       "lifetime",
			ObservedAt:   observedAt,
			Value:        &value,
			State:        MetricAvailable,
			APIVersion:   "v24.0",
		})
	}
	return result
}

func assertMetrics(t *testing.T, actual, expected []MetricName) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("metrics = %v, want %v", actual, expected)
	}
	for index := range actual {
		if actual[index] != expected[index] {
			t.Fatalf("metrics = %v, want %v", actual, expected)
		}
	}
}

func registerFacebook(
	t *testing.T,
	service *Service,
	now time.Time,
	contentID, remoteID string,
) {
	t.Helper()
	if _, err := service.RegisterPublished(context.Background(), PublishedContent{
		WorkspaceID:  "workspace-1",
		ContentID:    contentID,
		ChannelID:    "facebook-1",
		ChannelType:  ChannelFacebookPage,
		Provider:     "meta",
		ConnectionID: "connection-1",
		RemoteID:     remoteID,
		PublishedAt:  now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}
