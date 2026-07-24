package analytics

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"
)

// MemoryRepository is intended for tests and local development.
type MemoryRepository struct {
	mutex        sync.Mutex
	targets      map[string]SyncTarget
	order        []string
	observations map[string][]Observation
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		targets:      make(map[string]SyncTarget),
		observations: make(map[string][]Observation),
	}
}

func (repository *MemoryRepository) Register(
	_ context.Context,
	target SyncTarget,
) (RegisterResult, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if err := validateTarget(target); err != nil {
		return RegisterResult{}, err
	}
	if existing, found := repository.targets[target.ID]; found {
		if !sameRemoteTarget(existing, target) {
			return RegisterResult{}, ErrConflict
		}
		return RegisterResult{TargetID: existing.ID, Created: false}, nil
	}
	repository.targets[target.ID] = cloneTarget(target)
	repository.order = append(repository.order, target.ID)
	return RegisterResult{TargetID: target.ID, Created: true}, nil
}

func (repository *MemoryRepository) ClaimDue(
	_ context.Context,
	now, lockedUntil time.Time,
	leaseToken string,
) (SyncTarget, bool, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	if leaseToken == "" || !lockedUntil.After(now) {
		return SyncTarget{}, false, ErrInvalidArgument
	}
	for _, id := range repository.order {
		target := repository.targets[id]
		claimable := (target.State != TargetFailed &&
			target.State != TargetSyncing) ||
			target.State == TargetSyncing &&
				target.LockedUntil != nil &&
				!target.LockedUntil.After(now)
		if !claimable || target.NextSyncAt.After(now) {
			continue
		}
		target.State = TargetSyncing
		target.AttemptCount++
		target.LeaseToken = leaseToken
		lock := lockedUntil
		target.LockedUntil = &lock
		target.UpdatedAt = now
		repository.targets[id] = cloneTarget(target)
		return cloneTarget(target), true, nil
	}
	return SyncTarget{}, false, nil
}

func (repository *MemoryRepository) SaveSuccess(
	_ context.Context,
	targetID, leaseToken string,
	observations []Observation,
	cursor string,
	state TargetState,
	nextSyncAt, now time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	target, err := repository.claimed(targetID, leaseToken)
	if err != nil {
		return err
	}
	if state != TargetCurrent && state != TargetUnavailable &&
		state != TargetPermissionMissing {
		return ErrInvalidArgument
	}
	if !nextSyncAt.After(now) {
		return ErrInvalidArgument
	}
	if err := validateObservations(targetID, observations); err != nil {
		return err
	}
	repository.storeObservations(targetID, observations)
	target.Cursor = cursor
	target.State = state
	target.ConsecutiveFailures = 0
	target.NextSyncAt = nextSyncAt
	target.LeaseToken = ""
	target.LockedUntil = nil
	target.LastErrorCode = ""
	target.LastErrorAt = nil
	target.UpdatedAt = now
	repository.targets[targetID] = target
	return nil
}

func (repository *MemoryRepository) SaveRetry(
	_ context.Context,
	targetID, leaseToken, code string,
	nextSyncAt, now time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	target, err := repository.claimed(targetID, leaseToken)
	if err != nil {
		return err
	}
	if code == "" || !nextSyncAt.After(now) {
		return ErrInvalidArgument
	}
	target.State = TargetRetryWait
	target.ConsecutiveFailures++
	target.NextSyncAt = nextSyncAt
	target.LeaseToken = ""
	target.LockedUntil = nil
	target.LastErrorCode = code
	errorAt := now
	target.LastErrorAt = &errorAt
	target.UpdatedAt = now
	repository.targets[targetID] = target
	return nil
}

func (repository *MemoryRepository) Defer(
	_ context.Context,
	targetID, leaseToken string,
	nextSyncAt, now time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	target, err := repository.claimed(targetID, leaseToken)
	if err != nil {
		return err
	}
	if !nextSyncAt.After(now) {
		return ErrInvalidArgument
	}
	target.State = TargetRetryWait
	target.NextSyncAt = nextSyncAt
	target.LeaseToken = ""
	target.LockedUntil = nil
	target.UpdatedAt = now
	repository.targets[targetID] = target
	return nil
}

func (repository *MemoryRepository) SaveFailure(
	_ context.Context,
	targetID, leaseToken string,
	observations []Observation,
	code string,
	now time.Time,
) error {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	target, err := repository.claimed(targetID, leaseToken)
	if err != nil {
		return err
	}
	if code == "" {
		return ErrInvalidArgument
	}
	if err := validateObservations(targetID, observations); err != nil {
		return err
	}
	repository.storeObservations(targetID, observations)
	target.State = TargetFailed
	target.ConsecutiveFailures++
	target.LeaseToken = ""
	target.LockedUntil = nil
	target.LastErrorCode = code
	errorAt := now
	target.LastErrorAt = &errorAt
	target.UpdatedAt = now
	repository.targets[targetID] = target
	return nil
}

func (repository *MemoryRepository) Overview(
	_ context.Context,
	query OverviewQuery,
) (Overview, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	targets := make([]SyncTarget, 0)
	observations := make(map[string][]Observation)
	for _, id := range repository.order {
		target := repository.targets[id]
		if target.WorkspaceID != query.WorkspaceID ||
			target.PublishedAt.Before(query.From) ||
			!target.PublishedAt.Before(query.To) ||
			!channelIncluded(query.ChannelIDs, target.ChannelID) {
			continue
		}
		targets = append(targets, cloneTarget(target))
		observations[target.ID] = cloneObservations(repository.observations[target.ID])
	}
	return summarize(query, targets, observations), nil
}

func summarize(
	query OverviewQuery,
	targets []SyncTarget,
	observations map[string][]Observation,
) Overview {
	type channelGroup struct {
		channelType ChannelType
		targets     []SyncTarget
	}
	groups := make(map[string]*channelGroup)
	for _, target := range targets {
		group, found := groups[target.ChannelID]
		if !found {
			group = &channelGroup{channelType: target.ChannelType}
			groups[target.ChannelID] = group
		}
		group.targets = append(group.targets, target)
	}
	channelIDs := make([]string, 0, len(groups))
	for channelID := range groups {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Strings(channelIDs)
	result := Overview{
		From:     query.From,
		To:       query.To,
		Channels: make([]ChannelOverview, 0, len(channelIDs)),
	}
	for _, channelID := range channelIDs {
		group := groups[channelID]
		metricNames, _ := metricsFor(group.channelType)
		channel := ChannelOverview{
			ChannelID:    channelID,
			ChannelType:  group.channelType,
			ContentCount: len(group.targets),
			Metrics:      make([]MetricSummary, 0, len(metricNames)),
		}
		for _, metricName := range metricNames {
			summary := MetricSummary{Metric: metricName}
			var total int64
			hasValue := false
			for _, target := range group.targets {
				observation, found := latestObservation(
					observations[target.ID],
					metricName,
				)
				state := MetricUnavailable
				if found {
					state = observation.State
				}
				switch state {
				case MetricAvailable:
					summary.Targets.Available++
					total += *observation.Value
					hasValue = true
				case MetricPermissionMissing:
					summary.Targets.PermissionMissing++
				case MetricFailed:
					summary.Targets.Failed++
				default:
					summary.Targets.Unavailable++
				}
			}
			if hasValue {
				summary.Value = cloneInt64(&total)
			}
			summary.State = aggregateState(summary.Targets)
			channel.Metrics = append(channel.Metrics, summary)
		}
		result.Channels = append(result.Channels, channel)
	}
	return result
}

func latestObservation(
	observations []Observation,
	metric MetricName,
) (Observation, bool) {
	var latest Observation
	found := false
	for _, observation := range observations {
		if observation.Metric != metric {
			continue
		}
		if !found || observation.ObservedAt.After(latest.ObservedAt) {
			latest = observation
			found = true
		}
	}
	return latest, found
}

func aggregateState(counts StateCounts) MetricState {
	nonZero := 0
	state := MetricUnavailable
	if counts.Available > 0 {
		nonZero++
		state = MetricAvailable
	}
	if counts.Unavailable > 0 {
		nonZero++
		state = MetricUnavailable
	}
	if counts.PermissionMissing > 0 {
		nonZero++
		state = MetricPermissionMissing
	}
	if counts.Failed > 0 {
		nonZero++
		state = MetricFailed
	}
	if nonZero > 1 {
		return MetricMixed
	}
	return state
}

func (repository *MemoryRepository) claimed(
	targetID, leaseToken string,
) (SyncTarget, error) {
	target, found := repository.targets[targetID]
	if !found {
		return SyncTarget{}, ErrNotFound
	}
	if target.State != TargetSyncing || target.LeaseToken != leaseToken ||
		leaseToken == "" {
		return SyncTarget{}, ErrConflict
	}
	return cloneTarget(target), nil
}

func (repository *MemoryRepository) storeObservations(
	targetID string,
	observations []Observation,
) {
	existing := repository.observations[targetID]
	for _, observation := range observations {
		replaced := false
		for index := range existing {
			if sameObservationKey(existing[index], observation) {
				existing[index] = cloneObservation(observation)
				replaced = true
				break
			}
		}
		if !replaced {
			existing = append(existing, cloneObservation(observation))
		}
	}
	repository.observations[targetID] = existing
}

func validateTarget(target SyncTarget) error {
	if target.ID == "" || target.WorkspaceID == "" || target.ContentID == "" ||
		target.ChannelID == "" || target.Provider == "" ||
		target.ConnectionID == "" || target.RemoteID == "" ||
		target.PublishedAt.IsZero() || target.NextSyncAt.IsZero() ||
		target.CreatedAt.IsZero() || target.UpdatedAt.IsZero() ||
		target.State != TargetPending {
		return ErrInvalidArgument
	}
	return nil
}

func validateObservations(targetID string, observations []Observation) error {
	if targetID == "" || len(observations) == 0 {
		return ErrInvalidArgument
	}
	for _, observation := range observations {
		if observation.TargetID != targetID ||
			observation.Metric == "" ||
			observation.OriginalName == "" ||
			observation.Period == "" ||
			observation.ObservedAt.IsZero() {
			return ErrInvalidArgument
		}
		switch observation.State {
		case MetricAvailable:
			if observation.Value == nil || *observation.Value < 0 ||
				observation.APIVersion == "" ||
				observation.ReasonCode != "" {
				return ErrInvalidArgument
			}
		case MetricUnavailable:
			if observation.Value != nil ||
				observation.APIVersion == "" ||
				observation.ReasonCode != "" {
				return ErrInvalidArgument
			}
		case MetricPermissionMissing, MetricFailed:
			if observation.Value != nil ||
				observation.APIVersion != "" ||
				observation.ReasonCode == "" {
				return ErrInvalidArgument
			}
		default:
			return ErrInvalidArgument
		}
	}
	return nil
}

func sameRemoteTarget(left, right SyncTarget) bool {
	return left.WorkspaceID == right.WorkspaceID &&
		left.ContentID == right.ContentID &&
		left.ChannelID == right.ChannelID &&
		left.ChannelType == right.ChannelType &&
		left.Provider == right.Provider &&
		left.ConnectionID == right.ConnectionID &&
		left.RemoteID == right.RemoteID
}

func sameObservationKey(left, right Observation) bool {
	return left.TargetID == right.TargetID &&
		left.Metric == right.Metric &&
		left.OriginalName == right.OriginalName &&
		left.Period == right.Period &&
		left.ObservedAt.Equal(right.ObservedAt)
}

func channelIncluded(filters []string, channelID string) bool {
	return len(filters) == 0 || slices.Contains(filters, channelID)
}

func cloneTarget(target SyncTarget) SyncTarget {
	if target.LockedUntil != nil {
		lockedUntil := *target.LockedUntil
		target.LockedUntil = &lockedUntil
	}
	if target.LastErrorAt != nil {
		errorAt := *target.LastErrorAt
		target.LastErrorAt = &errorAt
	}
	return target
}

func cloneObservation(observation Observation) Observation {
	observation.Value = cloneInt64(observation.Value)
	return observation
}

func cloneObservations(observations []Observation) []Observation {
	result := make([]Observation, len(observations))
	for index, observation := range observations {
		result[index] = cloneObservation(observation)
	}
	return result
}
