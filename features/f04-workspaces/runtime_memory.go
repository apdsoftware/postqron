package workspaces

import (
	"context"
	"slices"
	"strings"
	"time"
)

type consentEvidence struct {
	AccountID   string
	WorkspaceID string
	DocumentKey string
	Version     string
	Purpose     string
}

func (repository *MemoryRepository) AppSession(
	_ context.Context,
	account AppSessionAccount,
) (AppSession, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	workspaces := repository.appWorkspaces(account.ID)
	return buildAppSession(account, workspaces, repository.selections[account.ID]), nil
}

func (repository *MemoryRepository) CompleteOnboarding(
	_ context.Context,
	command CompleteOnboardingCommand,
	now time.Time,
) (AppSession, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	documents := repository.onboardingDocuments(now)
	if len(documents) != 2 {
		return AppSession{}, false, ErrRuntimeUnavailable
	}
	if err := verifyOnboardingConsents(command.Consents, documents); err != nil {
		return AppSession{}, false, err
	}

	var current AppWorkspace
	created := false
	switch command.Workspace.Mode {
	case "create":
		workspaceID, exists := repository.personal[command.Account.ID]
		if !exists {
			workspaceID = workspaceIDSeed(command.Account.ID)
			workspace := Workspace{
				ID:                workspaceID,
				PersonalAccountID: command.Account.ID,
				Name:              strings.TrimSpace(command.Workspace.Name),
				Status:            WorkspaceActive,
				CreatedAt:         now,
				UpdatedAt:         now,
			}
			repository.workspaces[workspaceID] = workspace
			repository.personal[command.Account.ID] = workspaceID
			repository.memberships[workspaceID] = map[string]Membership{
				command.Account.ID: {
					WorkspaceID: workspaceID,
					AccountID:   command.Account.ID,
					Role:        RoleOwner,
					Status:      MembershipActive,
					CreatedAt:   now,
					UpdatedAt:   now,
				},
			}
			repository.audit(workspaceID, command.Account.ID, command.Account.ID, "workspace.personal_created", now)
			created = true
		}
		workspace := repository.workspaces[workspaceID]
		current = AppWorkspace{ID: workspace.ID, Name: workspace.Name, Role: RoleOwner}
	case "select":
		membership, err := repository.activeMembership(command.Workspace.ID, command.Account.ID)
		if err != nil {
			return AppSession{}, false, err
		}
		workspace := repository.workspaces[command.Workspace.ID]
		if workspace.Status != WorkspaceActive {
			return AppSession{}, false, ErrForbidden
		}
		current = AppWorkspace{ID: workspace.ID, Name: workspace.Name, Role: membership.Role}
	default:
		return AppSession{}, false, ErrInvalidArgument
	}

	repository.selections[command.Account.ID] = current.ID
	for _, receipt := range command.Consents {
		document := documents[receipt.DocumentKey]
		key := strings.Join([]string{
			command.Account.ID,
			current.ID,
			document.DocumentKey,
			document.Version,
			receipt.Purpose,
		}, "\x00")
		repository.consentEvidence[key] = consentEvidence{
			AccountID:   command.Account.ID,
			WorkspaceID: current.ID,
			DocumentKey: document.DocumentKey,
			Version:     document.Version,
			Purpose:     receipt.Purpose,
		}
	}

	workspaces := repository.appWorkspaces(command.Account.ID)
	return buildAppSession(command.Account, workspaces, repository.selections[command.Account.ID]), created, nil
}

func (repository *MemoryRepository) SelectWorkspace(
	_ context.Context,
	account AppSessionAccount,
	workspaceID string,
	_ time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	membership, err := repository.activeMembership(workspaceID, account.ID)
	if err != nil {
		return err
	}
	workspace := repository.workspaces[workspaceID]
	if workspace.Status != WorkspaceActive || !membership.Role.Valid() {
		return ErrForbidden
	}
	repository.selections[account.ID] = workspaceID
	return nil
}

func (repository *MemoryRepository) CurrentWorkspace(
	_ context.Context,
	accountID string,
) (Workspace, Role, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	current, err := repository.currentAppWorkspace(accountID)
	if err != nil {
		return Workspace{}, "", err
	}
	workspace := repository.workspaces[current.ID]
	return workspace, current.Role, nil
}

func (repository *MemoryRepository) CurrentMemberships(
	_ context.Context,
	accountID string,
) ([]Membership, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	current, err := repository.currentAppWorkspace(accountID)
	if err != nil {
		return nil, err
	}
	var memberships []Membership
	for _, membership := range repository.memberships[current.ID] {
		if membership.Status == MembershipActive {
			memberships = append(memberships, membership)
		}
	}
	slices.SortFunc(memberships, func(left, right Membership) int {
		return compareStrings(left.AccountID, right.AccountID)
	})
	return memberships, nil
}

func (repository *MemoryRepository) ConsumeOnboardingRequired(
	_ context.Context,
	event OnboardingRequiredEvent,
	now time.Time,
) (Workspace, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	workspaceID, exists := repository.personal[event.AccountID]
	if !exists {
		workspaceID = workspaceIDSeed(event.AccountID)
		workspace := Workspace{
			ID:                workspaceID,
			PersonalAccountID: event.AccountID,
			Name:              defaultPersonalWorkspaceName(event.DisplayName, event.Email),
			Status:            WorkspaceActive,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		repository.workspaces[workspaceID] = workspace
		repository.personal[event.AccountID] = workspaceID
		repository.memberships[workspaceID] = map[string]Membership{
			event.AccountID: {
				WorkspaceID: workspaceID,
				AccountID:   event.AccountID,
				Role:        RoleOwner,
				Status:      MembershipActive,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		}
		repository.audit(workspaceID, event.AccountID, event.AccountID, "workspace.personal_created", now)
		if _, selected := repository.selections[event.AccountID]; !selected {
			repository.selections[event.AccountID] = workspaceID
		}
		return workspace, true, nil
	}
	if _, selected := repository.selections[event.AccountID]; !selected {
		repository.selections[event.AccountID] = workspaceID
	}
	return repository.workspaces[workspaceID], false, nil
}

func (repository *MemoryRepository) onboardingDocuments(now time.Time) map[string]onboardingDocument {
	result := make(map[string]onboardingDocument, 2)
	for _, document := range repository.legalDocuments {
		if document.effectiveAt.After(now) || (!document.supersededAt.IsZero() && !document.supersededAt.After(now)) {
			continue
		}
		switch document.documentKey {
		case "terms_it":
			result["terms"] = onboardingDocument{
				ClientKey:   "terms",
				DocumentKey: "terms_it",
				Version:     document.version,
				DigestSHA:   document.digestSHA,
			}
		case "privacy_it":
			result["privacy"] = onboardingDocument{
				ClientKey:   "privacy",
				DocumentKey: "privacy_it",
				Version:     document.version,
				DigestSHA:   document.digestSHA,
			}
		}
	}
	return result
}

func (repository *MemoryRepository) appWorkspaces(accountID string) []AppWorkspace {
	var workspaces []AppWorkspace
	for workspaceID, memberships := range repository.memberships {
		membership, ok := memberships[accountID]
		if !ok || membership.Status != MembershipActive {
			continue
		}
		workspace := repository.workspaces[workspaceID]
		if workspace.Status != WorkspaceActive {
			continue
		}
		workspaces = append(workspaces, AppWorkspace{
			ID:   workspace.ID,
			Name: workspace.Name,
			Role: membership.Role,
		})
	}
	slices.SortFunc(workspaces, func(left, right AppWorkspace) int {
		return compareStrings(left.ID, right.ID)
	})
	return workspaces
}

func (repository *MemoryRepository) currentAppWorkspace(accountID string) (AppWorkspace, error) {
	workspaces := repository.appWorkspaces(accountID)
	current, ok := resolveCurrentWorkspace(workspaces, repository.selections[accountID])
	if !ok {
		return AppWorkspace{}, ErrNotFound
	}
	return current, nil
}

func (repository *MemoryRepository) ConsentEvidenceCount() int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return len(repository.consentEvidence)
}

type legalDocumentRecord struct {
	documentKey  string
	version      string
	digestSHA    string
	effectiveAt  time.Time
	supersededAt time.Time
}

func (repository *MemoryRepository) SeedLegalDocuments(documents ...legalDocumentRecord) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.legalDocuments = append([]legalDocumentRecord(nil), documents...)
}
