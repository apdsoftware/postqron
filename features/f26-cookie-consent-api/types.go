package cookieconsent

import (
	"context"
	"time"
)

type SubjectKind string

const (
	SubjectBrowser SubjectKind = "pseudonymous_browser"
	SubjectAccount SubjectKind = "authenticated_account"
)

type Subject struct {
	Kind SubjectKind `json:"kind"`
	ID   string      `json:"-"`
}

type PolicyRelease struct {
	Version      string    `json:"version"`
	DigestSHA256 string    `json:"digest_sha256"`
	EffectiveAt  time.Time `json:"effective_at"`
}

type Selection struct {
	Preferences bool `json:"preferences"`
	Analytics   bool `json:"analytics"`
	Marketing   bool `json:"marketing"`
}

type PreferenceState struct {
	Necessary         bool       `json:"necessary"`
	Preferences       bool       `json:"preferences"`
	Analytics         bool       `json:"analytics"`
	Marketing         bool       `json:"marketing"`
	HasRecordedChoice bool       `json:"has_recorded_choice"`
	PolicyVersion     string     `json:"policy_version"`
	PolicyDigest      string     `json:"policy_digest_sha256"`
	SelectedAt        *time.Time `json:"selected_at"`
	ExpiresAt         *time.Time `json:"expires_at"`
	Source            string     `json:"source,omitempty"`
	Revision          int64      `json:"revision"`
}

func (state PreferenceState) Selection() Selection {
	return Selection{
		Preferences: state.Preferences,
		Analytics:   state.Analytics,
		Marketing:   state.Marketing,
	}
}

type EvidenceAction string

const (
	ActionGranted   EvidenceAction = "granted"
	ActionRejected  EvidenceAction = "rejected"
	ActionWithdrawn EvidenceAction = "withdrawn"
)

type Evidence struct {
	EventID         string         `json:"event_id"`
	Category        string         `json:"category"`
	Action          EvidenceAction `json:"action"`
	Enabled         bool           `json:"enabled"`
	PolicyVersion   string         `json:"policy_version"`
	PolicyDigest    string         `json:"policy_digest_sha256"`
	OccurredAt      time.Time      `json:"occurred_at"`
	Source          string         `json:"source"`
	IdempotencyKey  string         `json:"idempotency_key"`
	RetentionUntil  time.Time      `json:"retention_until"`
	PreferenceState int64          `json:"preference_revision"`
}

type PortableExport struct {
	GeneratedAt time.Time       `json:"generated_at"`
	SubjectKind SubjectKind     `json:"subject_kind"`
	Current     PreferenceState `json:"current"`
	Evidence    []Evidence      `json:"evidence"`
}

type Mutation struct {
	Subject        Subject
	Policy         PolicyRelease
	Selection      Selection
	Source         string
	IdempotencyKey string
	Fingerprint    string
	SelectedAt     time.Time
	ExpiresAt      time.Time
	RetentionUntil time.Time
}

type PolicySource interface {
	Current(context.Context, time.Time) (PolicyRelease, error)
}

type Repository interface {
	Read(context.Context, Subject, PolicyRelease, time.Time) (PreferenceState, error)
	Put(context.Context, Mutation) (PreferenceState, bool, error)
	Export(context.Context, Subject, PolicyRelease, time.Time) (PortableExport, error)
	Erase(context.Context, Subject) error
	PurgeEvidence(context.Context, time.Time) (int64, error)
}
