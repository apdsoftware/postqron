package contentassistant

import (
	"context"
	"sync"
)

type MemoryRepository struct {
	mutex     sync.RWMutex
	proposals map[string]Proposal
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{proposals: make(map[string]Proposal)}
}

func proposalKey(workspaceID, proposalID string) string {
	return workspaceID + "\x00" + proposalID
}

func (repository *MemoryRepository) Create(
	_ context.Context,
	proposal Proposal,
) (Proposal, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := proposalKey(proposal.WorkspaceID, proposal.ID)
	if _, exists := repository.proposals[key]; exists {
		return Proposal{}, ErrConflict
	}
	repository.proposals[key] = cloneProposal(proposal)
	return cloneProposal(proposal), nil
}

func (repository *MemoryRepository) Get(
	_ context.Context,
	workspaceID, proposalID string,
) (Proposal, error) {
	repository.mutex.RLock()
	defer repository.mutex.RUnlock()
	proposal, exists := repository.proposals[proposalKey(workspaceID, proposalID)]
	if !exists {
		return Proposal{}, ErrNotFound
	}
	return cloneProposal(proposal), nil
}

func (repository *MemoryRepository) Decide(
	_ context.Context,
	workspaceID, proposalID string,
	expectedRevision int64,
	status ProposalStatus,
	event TraceEvent,
) (Proposal, error) {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := proposalKey(workspaceID, proposalID)
	proposal, exists := repository.proposals[key]
	if !exists {
		return Proposal{}, ErrNotFound
	}
	if proposal.Revision != expectedRevision || proposal.Status != StatusPending {
		return Proposal{}, ErrConflict
	}
	if status != StatusConfirmed && status != StatusRejected {
		return Proposal{}, ErrInvalidArgument
	}
	proposal.Status = status
	proposal.Revision++
	decidedAt := event.OccurredAt
	proposal.DecidedAt = &decidedAt
	proposal.Trace = append(proposal.Trace, cloneTraceEvent(event))
	repository.proposals[key] = cloneProposal(proposal)
	return cloneProposal(proposal), nil
}

func cloneSnapshot(snapshot DraftSnapshot) DraftSnapshot {
	snapshot.Destinations = cloneDestinations(snapshot.Destinations)
	return snapshot
}

func cloneDestinations(destinations []Destination) []Destination {
	return append([]Destination(nil), destinations...)
}

func cloneCandidates(candidates []Candidate) []Candidate {
	cloned := make([]Candidate, len(candidates))
	for index, candidate := range candidates {
		cloned[index] = candidate
		cloned[index].Diff = append([]DiffSegment(nil), candidate.Diff...)
	}
	return cloned
}

func cloneTraceEvent(event TraceEvent) TraceEvent {
	event.CandidateIDs = append([]string(nil), event.CandidateIDs...)
	return event
}

func cloneProposal(proposal Proposal) Proposal {
	proposal.Candidates = cloneCandidates(proposal.Candidates)
	trace := proposal.Trace
	proposal.Trace = make([]TraceEvent, len(trace))
	for index, event := range trace {
		proposal.Trace[index] = cloneTraceEvent(event)
	}
	if proposal.DecidedAt != nil {
		decidedAt := *proposal.DecidedAt
		proposal.DecidedAt = &decidedAt
	}
	return proposal
}
