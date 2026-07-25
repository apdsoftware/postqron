package cookieconsent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sync"
	"time"
)

type StaticPolicySource struct {
	Policy PolicyRelease
	Err    error
}

func (source *StaticPolicySource) Current(
	context.Context,
	time.Time,
) (PolicyRelease, error) {
	if source.Err != nil {
		return PolicyRelease{}, source.Err
	}
	return source.Policy, nil
}

type memorySubject struct {
	key      string
	current  PreferenceState
	hasState bool
	replays  map[string]memoryReplay
}

type memoryReplay struct {
	fingerprint string
	state       PreferenceState
}

type memoryEvent struct {
	subjectKey string
	evidence   Evidence
}

type MemoryRepository struct {
	mu       sync.Mutex
	subjects map[string]*memorySubject
	events   []memoryEvent
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{subjects: make(map[string]*memorySubject)}
}

func (repository *MemoryRepository) Read(
	_ context.Context,
	subject Subject,
	policy PolicyRelease,
	now time.Time,
) (PreferenceState, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	stored := repository.subjects[subjectMapKey(subject)]
	if stored == nil || !stored.hasState ||
		!stored.current.HasRecordedChoice ||
		stored.current.PolicyVersion != policy.Version ||
		stored.current.PolicyDigest != policy.DigestSHA256 ||
		stored.current.ExpiresAt == nil ||
		!now.Before(*stored.current.ExpiresAt) {
		return defaultState(policy), nil
	}
	return cloneState(stored.current), nil
}

func (repository *MemoryRepository) Put(
	_ context.Context,
	mutation Mutation,
) (PreferenceState, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	mapKey := subjectMapKey(mutation.Subject)
	stored := repository.subjects[mapKey]
	if stored == nil {
		keyDigest := sha256.Sum256([]byte(mapKey))
		stored = &memorySubject{
			key:     hex.EncodeToString(keyDigest[:16]),
			replays: make(map[string]memoryReplay),
		}
		repository.subjects[mapKey] = stored
	}
	if replay, found := stored.replays[mutation.IdempotencyKey]; found {
		if replay.fingerprint != mutation.Fingerprint {
			return PreferenceState{}, false, ErrIdempotencyConflict
		}
		return cloneState(replay.state), true, nil
	}

	previous := defaultState(mutation.Policy)
	if stored.hasState {
		previous = stored.current
	}
	revision := previous.Revision + 1
	selectedAt := mutation.SelectedAt.UTC()
	expiresAt := mutation.ExpiresAt.UTC()
	next := PreferenceState{
		Necessary:         true,
		Preferences:       mutation.Selection.Preferences,
		Analytics:         mutation.Selection.Analytics,
		Marketing:         mutation.Selection.Marketing,
		HasRecordedChoice: true,
		PolicyVersion:     mutation.Policy.Version,
		PolicyDigest:      mutation.Policy.DigestSHA256,
		SelectedAt:        &selectedAt,
		ExpiresAt:         &expiresAt,
		Source:            mutation.Source,
		Revision:          revision,
	}
	for _, category := range []string{"preferences", "analytics", "marketing"} {
		oldValue := categoryValue(previous.Selection(), category)
		newValue := categoryValue(mutation.Selection, category)
		digest := sha256.Sum256([]byte(stored.key + "\x00" +
			mutation.IdempotencyKey + "\x00" + category))
		repository.events = append(repository.events, memoryEvent{
			subjectKey: stored.key,
			evidence: Evidence{
				EventID:         hex.EncodeToString(digest[:]),
				Category:        category,
				Action:          evidenceAction(oldValue, newValue, previous.HasRecordedChoice),
				Enabled:         newValue,
				PolicyVersion:   mutation.Policy.Version,
				PolicyDigest:    mutation.Policy.DigestSHA256,
				OccurredAt:      selectedAt,
				Source:          mutation.Source,
				IdempotencyKey:  mutation.IdempotencyKey,
				RetentionUntil:  mutation.RetentionUntil.UTC(),
				PreferenceState: revision,
			},
		})
	}
	stored.current = next
	stored.hasState = true
	stored.replays[mutation.IdempotencyKey] = memoryReplay{
		fingerprint: mutation.Fingerprint,
		state:       next,
	}
	return cloneState(next), false, nil
}

func (repository *MemoryRepository) Export(
	ctx context.Context,
	subject Subject,
	policy PolicyRelease,
	now time.Time,
) (PortableExport, error) {
	repository.mu.Lock()
	stored := repository.subjects[subjectMapKey(subject)]
	var subjectKey string
	if stored != nil {
		subjectKey = stored.key
	}
	var evidence []Evidence
	for _, event := range repository.events {
		if event.subjectKey == subjectKey && event.evidence.RetentionUntil.After(now) {
			evidence = append(evidence, event.evidence)
		}
	}
	repository.mu.Unlock()
	current, err := repository.Read(ctx, subject, policy, now)
	if err != nil {
		return PortableExport{}, err
	}
	slices.SortFunc(evidence, func(left, right Evidence) int {
		if value := left.OccurredAt.Compare(right.OccurredAt); value != 0 {
			return value
		}
		if left.Category < right.Category {
			return -1
		}
		if left.Category > right.Category {
			return 1
		}
		return 0
	})
	return PortableExport{
		GeneratedAt: now.UTC(),
		SubjectKind: subject.Kind,
		Current:     current,
		Evidence:    evidence,
	}, nil
}

func (repository *MemoryRepository) Erase(_ context.Context, subject Subject) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	delete(repository.subjects, subjectMapKey(subject))
	return nil
}

func (repository *MemoryRepository) PurgeEvidence(
	_ context.Context,
	now time.Time,
) (int64, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	kept := repository.events[:0]
	var purged int64
	for _, event := range repository.events {
		if !event.evidence.RetentionUntil.After(now) {
			purged++
			continue
		}
		kept = append(kept, event)
	}
	repository.events = kept
	return purged, nil
}

func subjectMapKey(subject Subject) string {
	return string(subject.Kind) + "\x00" + subject.ID
}

func categoryValue(selection Selection, category string) bool {
	switch category {
	case "preferences":
		return selection.Preferences
	case "analytics":
		return selection.Analytics
	default:
		return selection.Marketing
	}
}

func cloneState(state PreferenceState) PreferenceState {
	cloned := state
	if state.SelectedAt != nil {
		value := *state.SelectedAt
		cloned.SelectedAt = &value
	}
	if state.ExpiresAt != nil {
		value := *state.ExpiresAt
		cloned.ExpiresAt = &value
	}
	return cloned
}
