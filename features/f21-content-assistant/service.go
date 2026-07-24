package contentassistant

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maximumAlternatives = 3
	maximumDestinations = 10
	maximumTextRunes    = 5_000
	maximumTraceValue   = 128
)

type ContentAuthorizer interface {
	CanManageContent(context.Context, string, string) (bool, error)
}

type DraftReader interface {
	GetDraftSnapshot(context.Context, string, string) (DraftSnapshot, error)
}

type Generator interface {
	Generate(context.Context, GenerationInput) (GenerationOutput, error)
}

type Repository interface {
	Create(context.Context, Proposal) (Proposal, error)
	Get(context.Context, string, string) (Proposal, error)
	Decide(context.Context, string, string, int64, ProposalStatus, TraceEvent) (Proposal, error)
}

type Service struct {
	repository Repository
	authorizer ContentAuthorizer
	drafts     DraftReader
	generator  Generator
	now        func() time.Time
	newID      func(string) (string, error)
}

type ServiceOption func(*Service)

func WithClock(clock func() time.Time) ServiceOption {
	return func(service *Service) {
		service.now = clock
	}
}

func WithIDGenerator(generator func(string) (string, error)) ServiceOption {
	return func(service *Service) {
		service.newID = generator
	}
}

func NewService(
	repository Repository,
	authorizer ContentAuthorizer,
	drafts DraftReader,
	generator Generator,
	options ...ServiceOption,
) (*Service, error) {
	if repository == nil || authorizer == nil || drafts == nil {
		return nil, fmt.Errorf(
			"%w: repository, authorizer, and draft reader are required",
			ErrInvalidArgument,
		)
	}
	service := &Service{
		repository: repository,
		authorizer: authorizer,
		drafts:     drafts,
		generator:  generator,
		now:        time.Now,
		newID: func(prefix string) (string, error) {
			randomBytes := make([]byte, 18)
			if _, err := rand.Read(randomBytes); err != nil {
				return "", err
			}
			return prefix + base64.RawURLEncoding.EncodeToString(randomBytes), nil
		},
	}
	for _, option := range options {
		option(service)
	}
	if service.now == nil || service.newID == nil {
		return nil, fmt.Errorf("%w: invalid service option", ErrInvalidArgument)
	}
	return service, nil
}

func (service *Service) Suggest(
	ctx context.Context,
	command SuggestCommand,
) (Proposal, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Proposal{}, err
	}
	alternatives := command.AlternativesPerChannel
	if alternatives == 0 {
		alternatives = 1
	}
	if alternatives < 1 || alternatives > maximumAlternatives {
		return Proposal{}, invalidField(
			"alternatives_per_channel",
			"alternatives_invalid",
			"Alternatives per channel must be between 1 and 3.",
		)
	}
	destinations, snapshot, err := service.loadDestinations(
		ctx,
		command.WorkspaceID,
		command.DraftID,
		command.DestinationIDs,
	)
	if err != nil {
		return Proposal{}, err
	}
	if service.generator == nil {
		return Proposal{}, &GeneratorError{}
	}

	candidates := make([]Candidate, 0, len(destinations)*alternatives)
	for _, destination := range destinations {
		output, generateErr := service.generator.Generate(ctx, GenerationInput{
			WorkspaceID:      command.WorkspaceID,
			DraftID:          snapshot.ID,
			DraftRevision:    snapshot.Revision,
			DestinationID:    destination.ID,
			Channel:          destination.Channel,
			OriginalText:     destination.Text,
			AlternativeCount: alternatives,
		})
		if generateErr != nil {
			return Proposal{}, &GeneratorError{Cause: generateErr}
		}
		generated, normalizeErr := service.generatedCandidates(destination, output, alternatives)
		if normalizeErr != nil {
			return Proposal{}, normalizeErr
		}
		candidates = append(candidates, generated...)
	}

	return service.createProposal(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		snapshot,
		candidates,
		TraceGenerated,
		command.CorrelationID,
	)
}

func (service *Service) CreateManual(
	ctx context.Context,
	command CreateManualCommand,
) (Proposal, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Proposal{}, err
	}
	if len(command.Candidates) == 0 ||
		len(command.Candidates) > maximumDestinations*maximumAlternatives {
		return Proposal{}, invalidField(
			"candidates",
			"manual_candidates_invalid",
			"Provide between 1 and 30 manual candidates.",
		)
	}
	requestedIDs := make([]string, 0, len(command.Candidates))
	for _, candidate := range command.Candidates {
		requestedIDs = append(requestedIDs, candidate.DestinationID)
	}
	destinations, snapshot, err := service.loadDestinations(
		ctx,
		command.WorkspaceID,
		command.DraftID,
		requestedIDs,
	)
	if err != nil {
		return Proposal{}, err
	}
	destinationByID := make(map[string]Destination, len(destinations))
	for _, destination := range destinations {
		destinationByID[destination.ID] = destination
	}
	countByDestination := make(map[string]int, len(destinations))
	candidates := make([]Candidate, 0, len(command.Candidates))
	for index, manual := range command.Candidates {
		destinationID := strings.TrimSpace(manual.DestinationID)
		destination := destinationByID[destinationID]
		countByDestination[destinationID]++
		if countByDestination[destinationID] > maximumAlternatives {
			return Proposal{}, invalidField(
				fmt.Sprintf("candidates[%d].destination_id", index),
				"too_many_alternatives",
				"A destination can have at most 3 alternatives.",
			)
		}
		proposed, normalizeErr := normalizeText(
			fmt.Sprintf("candidates[%d].proposed", index),
			manual.Proposed,
		)
		if normalizeErr != nil {
			return Proposal{}, normalizeErr
		}
		candidateID, idErr := service.newID("candidate_")
		if idErr != nil {
			return Proposal{}, fmt.Errorf("generate candidate id: %w", idErr)
		}
		candidates = append(candidates, Candidate{
			ID:            candidateID,
			DestinationID: destination.ID,
			Channel:       destination.Channel,
			Original:      destination.Text,
			Proposed:      proposed,
			Diff:          textDiff(destination.Text, proposed),
			Source:        SourceManual,
		})
	}

	return service.createProposal(
		ctx,
		command.WorkspaceID,
		command.ActorID,
		snapshot,
		candidates,
		TraceManualSubmitted,
		command.CorrelationID,
	)
}

func (service *Service) GetProposal(
	ctx context.Context,
	workspaceID, actorID, proposalID string,
) (Proposal, error) {
	if err := service.authorize(ctx, workspaceID, actorID); err != nil {
		return Proposal{}, err
	}
	if strings.TrimSpace(proposalID) == "" {
		return Proposal{}, invalidField(
			"proposal_id",
			"proposal_id_required",
			"Proposal id is required.",
		)
	}
	return service.repository.Get(ctx, workspaceID, proposalID)
}

func (service *Service) Confirm(
	ctx context.Context,
	command ConfirmCommand,
) (Proposal, ConfirmedChangeSet, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Proposal{}, ConfirmedChangeSet{}, err
	}
	if !command.Confirmed {
		return Proposal{}, ConfirmedChangeSet{}, invalidField(
			"confirmation",
			"explicit_confirmation_required",
			"Explicit human confirmation is required.",
		)
	}
	if command.ExpectedRevision < 1 {
		return Proposal{}, ConfirmedChangeSet{}, invalidField(
			"expected_revision",
			"expected_revision_invalid",
			"A positive proposal revision is required.",
		)
	}
	if len(command.CandidateIDs) == 0 {
		return Proposal{}, ConfirmedChangeSet{}, invalidField(
			"candidate_ids",
			"candidate_selection_required",
			"Select at least one candidate to confirm.",
		)
	}
	proposal, err := service.repository.Get(
		ctx,
		command.WorkspaceID,
		command.ProposalID,
	)
	if err != nil {
		return Proposal{}, ConfirmedChangeSet{}, err
	}
	selected, err := selectCandidates(proposal, command.CandidateIDs)
	if err != nil {
		return Proposal{}, ConfirmedChangeSet{}, err
	}
	event, err := service.traceEvent(
		TraceConfirmed,
		command.ActorID,
		command.CorrelationID,
		command.CandidateIDs,
	)
	if err != nil {
		return Proposal{}, ConfirmedChangeSet{}, err
	}
	decided, err := service.repository.Decide(
		ctx,
		command.WorkspaceID,
		command.ProposalID,
		command.ExpectedRevision,
		StatusConfirmed,
		event,
	)
	if err != nil {
		return Proposal{}, ConfirmedChangeSet{}, err
	}
	changes := make([]SuggestedChange, 0, len(selected))
	for _, candidate := range selected {
		changes = append(changes, SuggestedChange{
			CandidateID:   candidate.ID,
			DestinationID: candidate.DestinationID,
			Channel:       candidate.Channel,
			Original:      candidate.Original,
			Proposed:      candidate.Proposed,
		})
	}
	return decided, ConfirmedChangeSet{
		ProposalID:    proposal.ID,
		WorkspaceID:   proposal.WorkspaceID,
		DraftID:       proposal.DraftID,
		DraftRevision: proposal.DraftRevision,
		ConfirmedBy:   command.ActorID,
		ConfirmedAt:   event.OccurredAt,
		Changes:       changes,
	}, nil
}

func (service *Service) Reject(
	ctx context.Context,
	command RejectCommand,
) (Proposal, error) {
	if err := service.authorize(ctx, command.WorkspaceID, command.ActorID); err != nil {
		return Proposal{}, err
	}
	if command.ExpectedRevision < 1 {
		return Proposal{}, invalidField(
			"expected_revision",
			"expected_revision_invalid",
			"A positive proposal revision is required.",
		)
	}
	event, err := service.traceEvent(
		TraceRejected,
		command.ActorID,
		command.CorrelationID,
		nil,
	)
	if err != nil {
		return Proposal{}, err
	}
	return service.repository.Decide(
		ctx,
		command.WorkspaceID,
		command.ProposalID,
		command.ExpectedRevision,
		StatusRejected,
		event,
	)
}

func (service *Service) authorize(
	ctx context.Context,
	workspaceID, actorID string,
) error {
	if strings.TrimSpace(actorID) == "" {
		return ErrUnauthenticated
	}
	if strings.TrimSpace(workspaceID) == "" {
		return invalidField(
			"workspace_id",
			"workspace_id_required",
			"Workspace id is required.",
		)
	}
	allowed, err := service.authorizer.CanManageContent(ctx, workspaceID, actorID)
	if err != nil {
		return fmt.Errorf("authorize content assistance: %w", err)
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

func (service *Service) loadDestinations(
	ctx context.Context,
	workspaceID, draftID string,
	requestedIDs []string,
) ([]Destination, DraftSnapshot, error) {
	draftID = strings.TrimSpace(draftID)
	if draftID == "" {
		return nil, DraftSnapshot{}, invalidField(
			"draft_id",
			"draft_id_required",
			"Draft id is required.",
		)
	}
	snapshot, err := service.drafts.GetDraftSnapshot(ctx, workspaceID, draftID)
	if err != nil {
		return nil, DraftSnapshot{}, fmt.Errorf("read composer draft: %w", err)
	}
	if err := validateSnapshot(snapshot, draftID); err != nil {
		return nil, DraftSnapshot{}, err
	}
	if len(requestedIDs) == 0 {
		return cloneDestinations(snapshot.Destinations), cloneSnapshot(snapshot), nil
	}

	requested := make(map[string]struct{}, len(requestedIDs))
	for index, rawID := range requestedIDs {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return nil, DraftSnapshot{}, invalidField(
				fmt.Sprintf("destination_ids[%d]", index),
				"destination_id_required",
				"Destination id is required.",
			)
		}
		requested[id] = struct{}{}
	}
	destinations := make([]Destination, 0, len(requested))
	for _, destination := range snapshot.Destinations {
		if _, wanted := requested[destination.ID]; wanted {
			destinations = append(destinations, destination)
			delete(requested, destination.ID)
		}
	}
	if len(requested) != 0 {
		return nil, DraftSnapshot{}, invalidField(
			"destination_ids",
			"destination_not_found",
			"A selected destination does not belong to the draft.",
		)
	}
	return cloneDestinations(destinations), cloneSnapshot(snapshot), nil
}

func validateSnapshot(snapshot DraftSnapshot, expectedDraftID string) error {
	if snapshot.ID != expectedDraftID || snapshot.Revision < 1 {
		return fmt.Errorf("%w: composer returned an invalid draft snapshot", ErrInvalidArgument)
	}
	if len(snapshot.Destinations) == 0 ||
		len(snapshot.Destinations) > maximumDestinations {
		return invalidField(
			"destinations",
			"destinations_invalid",
			"The draft must contain between 1 and 10 destinations.",
		)
	}
	seen := make(map[string]struct{}, len(snapshot.Destinations))
	for index, destination := range snapshot.Destinations {
		if strings.TrimSpace(destination.ID) == "" ||
			strings.TrimSpace(string(destination.Channel)) == "" {
			return fmt.Errorf(
				"%w: composer destination %d is incomplete",
				ErrInvalidArgument,
				index,
			)
		}
		if _, duplicate := seen[destination.ID]; duplicate {
			return fmt.Errorf(
				"%w: composer returned duplicate destination ids",
				ErrInvalidArgument,
			)
		}
		seen[destination.ID] = struct{}{}
		if _, err := normalizeText("original", destination.Text); err != nil {
			return fmt.Errorf("%w: invalid composer destination text", ErrInvalidArgument)
		}
	}
	return nil
}

func (service *Service) generatedCandidates(
	destination Destination,
	output GenerationOutput,
	requested int,
) ([]Candidate, error) {
	if len(output.Candidates) == 0 || len(output.Candidates) > requested {
		return nil, fmt.Errorf(
			"%w: generator returned an invalid candidate count",
			ErrGeneratorUnavailable,
		)
	}
	provider, err := normalizeTraceValue("provider", output.Provider)
	if err != nil {
		return nil, err
	}
	model, err := normalizeTraceValue("model", output.Model)
	if err != nil {
		return nil, err
	}
	requestID, err := normalizeTraceValue("provider_request_id", output.RequestID)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, len(output.Candidates))
	seen := make(map[string]struct{}, len(output.Candidates))
	for index, candidateText := range output.Candidates {
		proposed, normalizeErr := normalizeText(
			fmt.Sprintf("candidates[%d]", index),
			candidateText,
		)
		if normalizeErr != nil {
			return nil, fmt.Errorf("%w: invalid generator candidate", ErrGeneratorUnavailable)
		}
		if _, duplicate := seen[proposed]; duplicate {
			return nil, fmt.Errorf(
				"%w: generator returned duplicate candidates",
				ErrGeneratorUnavailable,
			)
		}
		seen[proposed] = struct{}{}
		candidateID, idErr := service.newID("candidate_")
		if idErr != nil {
			return nil, fmt.Errorf("generate candidate id: %w", idErr)
		}
		candidates = append(candidates, Candidate{
			ID:                candidateID,
			DestinationID:     destination.ID,
			Channel:           destination.Channel,
			Original:          destination.Text,
			Proposed:          proposed,
			Diff:              textDiff(destination.Text, proposed),
			Source:            SourceGenerated,
			Provider:          provider,
			Model:             model,
			ProviderRequestID: requestID,
		})
	}
	return candidates, nil
}

func (service *Service) createProposal(
	ctx context.Context,
	workspaceID, actorID string,
	snapshot DraftSnapshot,
	candidates []Candidate,
	action TraceAction,
	correlationID string,
) (Proposal, error) {
	proposalID, err := service.newID("proposal_")
	if err != nil {
		return Proposal{}, fmt.Errorf("generate proposal id: %w", err)
	}
	event, err := service.traceEvent(action, actorID, correlationID, nil)
	if err != nil {
		return Proposal{}, err
	}
	proposal := Proposal{
		ID:            proposalID,
		WorkspaceID:   workspaceID,
		DraftID:       snapshot.ID,
		DraftRevision: snapshot.Revision,
		Status:        StatusPending,
		Revision:      1,
		Candidates:    cloneCandidates(candidates),
		Trace:         []TraceEvent{event},
		CreatedAt:     event.OccurredAt,
	}
	return service.repository.Create(ctx, proposal)
}

func (service *Service) traceEvent(
	action TraceAction,
	actorID, correlationID string,
	candidateIDs []string,
) (TraceEvent, error) {
	eventID, err := service.newID("trace_")
	if err != nil {
		return TraceEvent{}, fmt.Errorf("generate trace id: %w", err)
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		correlationID, err = service.newID("correlation_")
		if err != nil {
			return TraceEvent{}, fmt.Errorf("generate correlation id: %w", err)
		}
	}
	if len(correlationID) > maximumTraceValue {
		return TraceEvent{}, invalidField(
			"correlation_id",
			"correlation_id_too_long",
			"Correlation id must not exceed 128 characters.",
		)
	}
	return TraceEvent{
		ID:            eventID,
		Action:        action,
		ActorID:       actorID,
		CorrelationID: correlationID,
		OccurredAt:    service.now().UTC(),
		CandidateIDs:  append([]string(nil), candidateIDs...),
	}, nil
}

func selectCandidates(proposal Proposal, requestedIDs []string) ([]Candidate, error) {
	byID := make(map[string]Candidate, len(proposal.Candidates))
	for _, candidate := range proposal.Candidates {
		byID[candidate.ID] = candidate
	}
	selected := make([]Candidate, 0, len(requestedIDs))
	seenCandidates := make(map[string]struct{}, len(requestedIDs))
	seenDestinations := make(map[string]struct{}, len(requestedIDs))
	for index, rawID := range requestedIDs {
		id := strings.TrimSpace(rawID)
		candidate, exists := byID[id]
		if !exists {
			return nil, invalidField(
				fmt.Sprintf("candidate_ids[%d]", index),
				"candidate_not_found",
				"A selected candidate does not belong to the proposal.",
			)
		}
		if _, duplicate := seenCandidates[id]; duplicate {
			return nil, invalidField(
				fmt.Sprintf("candidate_ids[%d]", index),
				"candidate_duplicate",
				"Candidate ids must be unique.",
			)
		}
		if _, duplicate := seenDestinations[candidate.DestinationID]; duplicate {
			return nil, invalidField(
				fmt.Sprintf("candidate_ids[%d]", index),
				"destination_selection_ambiguous",
				"Select at most one candidate for each destination.",
			)
		}
		seenCandidates[id] = struct{}{}
		seenDestinations[candidate.DestinationID] = struct{}{}
		selected = append(selected, candidate)
	}
	return selected, nil
}

func normalizeText(field, text string) (string, error) {
	if !utf8.ValidString(text) {
		return "", invalidField(field, "text_invalid", "Text must be valid UTF-8.")
	}
	if strings.TrimSpace(text) == "" {
		return "", invalidField(field, "text_required", "Text is required.")
	}
	if utf8.RuneCountInString(text) > maximumTextRunes {
		return "", invalidField(
			field,
			"text_too_long",
			"Text must not exceed 5000 characters.",
		)
	}
	return text, nil
}

func normalizeTraceValue(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) > maximumTraceValue {
		return "", fmt.Errorf(
			"%w: generator %s exceeds 128 characters",
			ErrGeneratorUnavailable,
			field,
		)
	}
	return value, nil
}

func invalidField(field, code, message string) error {
	return &FieldError{Field: field, Code: code, Message: message}
}
