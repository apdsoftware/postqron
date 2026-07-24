package contentassistant

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type authorizerStub struct {
	allowed bool
	err     error
}

func (stub authorizerStub) CanManageContent(
	context.Context,
	string,
	string,
) (bool, error) {
	return stub.allowed, stub.err
}

type draftReaderStub struct {
	snapshot DraftSnapshot
	err      error
	reads    int
}

func (stub *draftReaderStub) GetDraftSnapshot(
	context.Context,
	string,
	string,
) (DraftSnapshot, error) {
	stub.reads++
	return cloneSnapshot(stub.snapshot), stub.err
}

type generatorStub struct {
	outputs map[Channel]GenerationOutput
	err     error
	inputs  []GenerationInput
}

func (stub *generatorStub) Generate(
	_ context.Context,
	input GenerationInput,
) (GenerationOutput, error) {
	stub.inputs = append(stub.inputs, input)
	output := stub.outputs[input.Channel]
	if len(output.Candidates) > input.AlternativeCount {
		output.Candidates = append(
			[]string(nil),
			output.Candidates[:input.AlternativeCount]...,
		)
	}
	return output, stub.err
}

func testSnapshot() DraftSnapshot {
	return DraftSnapshot{
		ID:       "draft-1",
		Revision: 7,
		Destinations: []Destination{
			{
				ID:      "facebook",
				Channel: Channel("facebook_page"),
				Text:    "Original Facebook",
			},
			{
				ID:      "instagram",
				Channel: Channel("instagram_professional"),
				Text:    "Original Instagram",
			},
		},
	}
}

func newServiceForTest(
	t *testing.T,
	generator Generator,
) (*Service, *MemoryRepository, *draftReaderStub) {
	t.Helper()
	repository := NewMemoryRepository()
	drafts := &draftReaderStub{snapshot: testSnapshot()}
	nextID := 0
	service, err := NewService(
		repository,
		authorizerStub{allowed: true},
		drafts,
		generator,
		WithClock(func() time.Time {
			return time.Date(2026, 7, 24, 15, 0, nextID, 0, time.FixedZone("local", -4*60*60))
		}),
		WithIDGenerator(func(prefix string) (string, error) {
			nextID++
			return fmt.Sprintf("%s%d", prefix, nextID), nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, drafts
}

func generatedStub() *generatorStub {
	return &generatorStub{outputs: map[Channel]GenerationOutput{
		Channel("facebook_page"): {
			Candidates: []string{"Facebook A", "Facebook B"},
			Provider:   "provider",
			Model:      "model-v1",
			RequestID:  "provider-request-facebook",
		},
		Channel("instagram_professional"): {
			Candidates: []string{"Instagram A", "Instagram B"},
			Provider:   "provider",
			Model:      "model-v1",
			RequestID:  "provider-request-instagram",
		},
	}}
}

func TestSuggestCreatesChannelVariantsWithOriginalProposalAndDiff(t *testing.T) {
	generator := generatedStub()
	service, _, drafts := newServiceForTest(t, generator)

	proposal, err := service.Suggest(context.Background(), SuggestCommand{
		WorkspaceID:            "workspace-1",
		ActorID:                "account-1",
		DraftID:                "draft-1",
		AlternativesPerChannel: 2,
		CorrelationID:          "request-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if drafts.reads != 1 || len(generator.inputs) != 2 {
		t.Fatalf("draft reads = %d, generator inputs = %d", drafts.reads, len(generator.inputs))
	}
	if proposal.Status != StatusPending ||
		proposal.DraftRevision != 7 ||
		proposal.Revision != 1 {
		t.Fatalf("proposal state = %#v", proposal)
	}
	if len(proposal.Candidates) != 4 {
		t.Fatalf("candidate count = %d", len(proposal.Candidates))
	}
	channels := map[Channel]int{}
	for _, candidate := range proposal.Candidates {
		channels[candidate.Channel]++
		if candidate.Original == "" ||
			candidate.Proposed == "" ||
			len(candidate.Diff) == 0 {
			t.Fatalf("candidate lacks comparison data: %#v", candidate)
		}
		if candidate.Source != SourceGenerated ||
			candidate.ProviderRequestID == "" {
			t.Fatalf("candidate lacks generation trace: %#v", candidate)
		}
	}
	if channels[Channel("facebook_page")] != 2 ||
		channels[Channel("instagram_professional")] != 2 {
		t.Fatalf("variants by channel = %#v", channels)
	}
	if len(proposal.Trace) != 1 ||
		proposal.Trace[0].Action != TraceGenerated ||
		proposal.Trace[0].CorrelationID != "request-123" {
		t.Fatalf("trace = %#v", proposal.Trace)
	}
}

func TestExplicitConfirmationIsRequiredAndProducesVersionedChangeSet(t *testing.T) {
	service, repository, _ := newServiceForTest(t, generatedStub())
	proposal, err := service.Suggest(context.Background(), SuggestCommand{
		WorkspaceID:            "workspace-1",
		ActorID:                "account-1",
		DraftID:                "draft-1",
		AlternativesPerChannel: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = service.Confirm(context.Background(), ConfirmCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		ProposalID:       proposal.ID,
		ExpectedRevision: 1,
		Confirmed:        false,
		CandidateIDs:     []string{proposal.Candidates[0].ID},
	})
	var fieldError *FieldError
	if !errors.As(err, &fieldError) ||
		fieldError.Code != "explicit_confirmation_required" {
		t.Fatalf("confirmation error = %#v", err)
	}
	unchanged, err := repository.Get(context.Background(), "workspace-1", proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != StatusPending ||
		unchanged.Revision != 1 ||
		len(unchanged.Trace) != 1 {
		t.Fatalf("proposal changed without confirmation: %#v", unchanged)
	}

	confirmed, changeSet, err := service.Confirm(
		context.Background(),
		ConfirmCommand{
			WorkspaceID:      "workspace-1",
			ActorID:          "account-1",
			ProposalID:       proposal.ID,
			ExpectedRevision: 1,
			Confirmed:        true,
			CandidateIDs: []string{
				proposal.Candidates[0].ID,
				proposal.Candidates[2].ID,
			},
			CorrelationID: "confirm-request",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != StatusConfirmed ||
		confirmed.Revision != 2 ||
		len(confirmed.Trace) != 2 ||
		confirmed.Trace[1].Action != TraceConfirmed {
		t.Fatalf("confirmed proposal = %#v", confirmed)
	}
	if changeSet.DraftRevision != 7 ||
		changeSet.ConfirmedBy != "account-1" ||
		len(changeSet.Changes) != 2 {
		t.Fatalf("change set = %#v", changeSet)
	}

	_, _, err = service.Confirm(context.Background(), ConfirmCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		ProposalID:       proposal.ID,
		ExpectedRevision: 1,
		Confirmed:        true,
		CandidateIDs:     []string{proposal.Candidates[0].ID},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second confirmation error = %v", err)
	}
}

func TestConfirmRejectsTwoAlternativesForSameDestination(t *testing.T) {
	service, _, _ := newServiceForTest(t, generatedStub())
	proposal, err := service.Suggest(context.Background(), SuggestCommand{
		WorkspaceID:            "workspace-1",
		ActorID:                "account-1",
		DraftID:                "draft-1",
		DestinationIDs:         []string{"facebook"},
		AlternativesPerChannel: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = service.Confirm(context.Background(), ConfirmCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		ProposalID:       proposal.ID,
		ExpectedRevision: 1,
		Confirmed:        true,
		CandidateIDs: []string{
			proposal.Candidates[0].ID,
			proposal.Candidates[1].ID,
		},
	})
	var fieldError *FieldError
	if !errors.As(err, &fieldError) ||
		fieldError.Code != "destination_selection_ambiguous" {
		t.Fatalf("selection error = %#v", err)
	}
}

func TestManualFallbackWorksWhenGeneratorIsUnavailable(t *testing.T) {
	service, _, _ := newServiceForTest(
		t,
		&generatorStub{err: errors.New("provider timeout")},
	)
	_, err := service.Suggest(context.Background(), SuggestCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
	})
	if !errors.Is(err, ErrGeneratorUnavailable) {
		t.Fatalf("suggest error = %v", err)
	}

	manual, err := service.CreateManual(context.Background(), CreateManualCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
		Candidates: []ManualCandidate{{
			DestinationID: "instagram",
			Proposed:      "Testo inserito manualmente",
		}},
		CorrelationID: "manual-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if manual.Status != StatusPending ||
		len(manual.Candidates) != 1 ||
		manual.Candidates[0].Source != SourceManual ||
		manual.Trace[0].Action != TraceManualSubmitted {
		t.Fatalf("manual proposal = %#v", manual)
	}

	rejected, err := service.Reject(context.Background(), RejectCommand{
		WorkspaceID:      "workspace-1",
		ActorID:          "account-1",
		ProposalID:       manual.ID,
		ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != StatusRejected ||
		rejected.Trace[1].Action != TraceRejected {
		t.Fatalf("rejected = %#v", rejected)
	}
}

func TestAuthorizationAndWorkspaceBoundaryFailClosed(t *testing.T) {
	drafts := &draftReaderStub{snapshot: testSnapshot()}
	service, err := NewService(
		NewMemoryRepository(),
		authorizerStub{allowed: false},
		drafts,
		generatedStub(),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Suggest(context.Background(), SuggestCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
	})
	if !errors.Is(err, ErrForbidden) || drafts.reads != 0 {
		t.Fatalf("forbidden error = %v, reads = %d", err, drafts.reads)
	}

	service, _, _ = newServiceForTest(t, generatedStub())
	proposal, err := service.Suggest(context.Background(), SuggestCommand{
		WorkspaceID: "workspace-1",
		ActorID:     "account-1",
		DraftID:     "draft-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.GetProposal(
		context.Background(),
		"workspace-2",
		"account-1",
		proposal.ID,
	)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace error = %v", err)
	}
}
