package cookieconsent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

const evidenceRetention = 365 * 24 * time.Hour

var (
	versionPattern        = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	digestPattern         = regexp.MustCompile(`^[a-f0-9]{64}$`)
	idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,200}$`)
	validSources          = map[string]struct{}{
		"banner":             {},
		"preferences_center": {},
		"account":            {},
	}
)

type Service struct {
	repository Repository
	policies   PolicySource
	clock      func() time.Time
}

func NewService(
	repository Repository,
	policies PolicySource,
	clock func() time.Time,
) (*Service, error) {
	if repository == nil || policies == nil || clock == nil {
		return nil, ErrInvalidRequest
	}
	return &Service{repository: repository, policies: policies, clock: clock}, nil
}

func (service *Service) Get(
	ctx context.Context,
	subject Subject,
) (PreferenceState, error) {
	if err := validateSubject(subject); err != nil {
		return PreferenceState{}, err
	}
	now := service.clock().UTC()
	policy, err := service.currentPolicy(ctx, now)
	if err != nil {
		return PreferenceState{}, err
	}
	return service.repository.Read(ctx, subject, policy, now)
}

func (service *Service) Put(
	ctx context.Context,
	subject Subject,
	policyVersion string,
	selection Selection,
	source, idempotencyKey string,
) (PreferenceState, bool, error) {
	if err := validateSubject(subject); err != nil {
		return PreferenceState{}, false, err
	}
	source = strings.TrimSpace(source)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if _, valid := validSources[source]; !valid ||
		!idempotencyKeyPattern.MatchString(idempotencyKey) {
		return PreferenceState{}, false, ErrInvalidRequest
	}
	now := service.clock().UTC()
	policy, err := service.currentPolicy(ctx, now)
	if err != nil {
		return PreferenceState{}, false, err
	}
	if policyVersion != policy.Version {
		return PreferenceState{}, false, ErrPolicyMismatch
	}
	input := struct {
		PolicyVersion string    `json:"policy_version"`
		Selection     Selection `json:"selection"`
		Source        string    `json:"source"`
	}{policyVersion, selection, source}
	encoded, _ := json.Marshal(input)
	digest := sha256.Sum256(encoded)
	return service.repository.Put(ctx, Mutation{
		Subject:        subject,
		Policy:         policy,
		Selection:      selection,
		Source:         source,
		IdempotencyKey: idempotencyKey,
		Fingerprint:    hex.EncodeToString(digest[:]),
		SelectedAt:     now,
		ExpiresAt:      addSixCalendarMonths(now),
		RetentionUntil: now.Add(evidenceRetention),
	})
}

func (service *Service) Export(
	ctx context.Context,
	subject Subject,
) (PortableExport, error) {
	if err := validateSubject(subject); err != nil {
		return PortableExport{}, err
	}
	now := service.clock().UTC()
	policy, err := service.currentPolicy(ctx, now)
	if err != nil {
		return PortableExport{}, err
	}
	return service.repository.Export(ctx, subject, policy, now)
}

func (service *Service) Erase(ctx context.Context, subject Subject) error {
	if err := validateSubject(subject); err != nil {
		return err
	}
	return service.repository.Erase(ctx, subject)
}

func (service *Service) PurgeExpiredEvidence(ctx context.Context) (int64, error) {
	return service.repository.PurgeEvidence(ctx, service.clock().UTC())
}

func (service *Service) currentPolicy(
	ctx context.Context,
	now time.Time,
) (PolicyRelease, error) {
	policy, err := service.policies.Current(ctx, now)
	if err != nil {
		return PolicyRelease{}, err
	}
	if !versionPattern.MatchString(policy.Version) ||
		!digestPattern.MatchString(policy.DigestSHA256) ||
		policy.EffectiveAt.IsZero() ||
		policy.EffectiveAt.After(now) {
		return PolicyRelease{}, ErrPolicyUnavailable
	}
	policy.EffectiveAt = policy.EffectiveAt.UTC()
	return policy, nil
}

func validateSubject(subject Subject) error {
	id := strings.TrimSpace(subject.ID)
	if (subject.Kind != SubjectBrowser && subject.Kind != SubjectAccount) ||
		len(id) < 8 || len(id) > 200 {
		return ErrInvalidRequest
	}
	return nil
}

func addSixCalendarMonths(value time.Time) time.Time {
	value = value.UTC()
	year, month, day := value.Date()
	targetMonth := month + 6
	targetYear := year
	for targetMonth > 12 {
		targetMonth -= 12
		targetYear++
	}
	lastDay := time.Date(targetYear, targetMonth+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(
		targetYear, targetMonth, day,
		value.Hour(), value.Minute(), value.Second(), value.Nanosecond(), time.UTC,
	)
}

func defaultState(policy PolicyRelease) PreferenceState {
	return PreferenceState{
		Necessary:     true,
		PolicyVersion: policy.Version,
		PolicyDigest:  policy.DigestSHA256,
	}
}

func evidenceAction(previous, current bool, hadChoice bool) EvidenceAction {
	if current {
		return ActionGranted
	}
	if hadChoice && previous {
		return ActionWithdrawn
	}
	return ActionRejected
}
