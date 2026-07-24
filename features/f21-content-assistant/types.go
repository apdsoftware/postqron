package contentassistant

import "time"

type Channel string

type Destination struct {
	ID      string  `json:"id"`
	Channel Channel `json:"channel"`
	Text    string  `json:"text"`
}

// DraftSnapshot is the narrow F6 boundary used by this slice. Text must be the
// effective text for each destination after composer overrides are resolved.
type DraftSnapshot struct {
	ID           string        `json:"id"`
	Revision     int64         `json:"revision"`
	Destinations []Destination `json:"destinations"`
}

type GenerationInput struct {
	WorkspaceID      string
	DraftID          string
	DraftRevision    int64
	DestinationID    string
	Channel          Channel
	OriginalText     string
	AlternativeCount int
}

type GenerationOutput struct {
	Candidates []string
	Provider   string
	Model      string
	RequestID  string
}

type DiffOperation string

const (
	DiffEqual  DiffOperation = "equal"
	DiffDelete DiffOperation = "delete"
	DiffInsert DiffOperation = "insert"
)

type DiffSegment struct {
	Operation DiffOperation `json:"operation"`
	Text      string        `json:"text"`
}

type CandidateSource string

const (
	SourceGenerated CandidateSource = "generated"
	SourceManual    CandidateSource = "manual"
)

type Candidate struct {
	ID                string          `json:"id"`
	DestinationID     string          `json:"destination_id"`
	Channel           Channel         `json:"channel"`
	Original          string          `json:"original"`
	Proposed          string          `json:"proposed"`
	Diff              []DiffSegment   `json:"diff"`
	Source            CandidateSource `json:"source"`
	Provider          string          `json:"provider,omitempty"`
	Model             string          `json:"model,omitempty"`
	ProviderRequestID string          `json:"provider_request_id,omitempty"`
}

type ProposalStatus string

const (
	StatusPending   ProposalStatus = "pending"
	StatusConfirmed ProposalStatus = "confirmed"
	StatusRejected  ProposalStatus = "rejected"
)

type TraceAction string

const (
	TraceGenerated       TraceAction = "proposal.generated"
	TraceManualSubmitted TraceAction = "proposal.manual_submitted"
	TraceConfirmed       TraceAction = "proposal.confirmed"
	TraceRejected        TraceAction = "proposal.rejected"
)

// TraceEvent intentionally contains opaque identifiers only. Content remains
// in the proposal snapshot and is never copied into arbitrary audit metadata.
type TraceEvent struct {
	ID            string      `json:"id"`
	Action        TraceAction `json:"action"`
	ActorID       string      `json:"actor_id"`
	CorrelationID string      `json:"correlation_id"`
	OccurredAt    time.Time   `json:"occurred_at"`
	CandidateIDs  []string    `json:"candidate_ids,omitempty"`
}

type Proposal struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	DraftID       string         `json:"draft_id"`
	DraftRevision int64          `json:"draft_revision"`
	Status        ProposalStatus `json:"status"`
	Revision      int64          `json:"revision"`
	Candidates    []Candidate    `json:"candidates"`
	Trace         []TraceEvent   `json:"trace"`
	CreatedAt     time.Time      `json:"created_at"`
	DecidedAt     *time.Time     `json:"decided_at,omitempty"`
}

type SuggestedChange struct {
	CandidateID   string  `json:"candidate_id"`
	DestinationID string  `json:"destination_id"`
	Channel       Channel `json:"channel"`
	Original      string  `json:"original"`
	Proposed      string  `json:"proposed"`
}

// ConfirmedChangeSet is the only output that may be handed to an F6 adapter.
// DraftRevision is an optimistic-concurrency precondition, so a stale
// suggestion cannot silently overwrite newer composer work.
type ConfirmedChangeSet struct {
	ProposalID    string            `json:"proposal_id"`
	WorkspaceID   string            `json:"workspace_id"`
	DraftID       string            `json:"draft_id"`
	DraftRevision int64             `json:"draft_revision"`
	ConfirmedBy   string            `json:"confirmed_by"`
	ConfirmedAt   time.Time         `json:"confirmed_at"`
	Changes       []SuggestedChange `json:"changes"`
}

type SuggestCommand struct {
	WorkspaceID            string
	ActorID                string
	DraftID                string
	DestinationIDs         []string
	AlternativesPerChannel int
	CorrelationID          string
}

type ManualCandidate struct {
	DestinationID string `json:"destination_id"`
	Proposed      string `json:"proposed"`
}

type CreateManualCommand struct {
	WorkspaceID   string
	ActorID       string
	DraftID       string
	Candidates    []ManualCandidate
	CorrelationID string
}

type ConfirmCommand struct {
	WorkspaceID      string
	ActorID          string
	ProposalID       string
	ExpectedRevision int64
	Confirmed        bool
	CandidateIDs     []string
	CorrelationID    string
}

type RejectCommand struct {
	WorkspaceID      string
	ActorID          string
	ProposalID       string
	ExpectedRevision int64
	CorrelationID    string
}
