package workspaces

import (
	"bytes"
	"context"
	"encoding/hex"
	"slices"
	"strconv"
	"sync"
	"time"
)

// MemoryRepository is a concurrency-safe repository useful for tests and
// ephemeral runtimes. It applies the same authorization and invariant checks
// as the PostgreSQL adapter.
type MemoryRepository struct {
	mu              sync.Mutex
	workspaces      map[string]Workspace
	personal        map[string]string
	memberships     map[string]map[string]Membership
	invitations     map[string]memoryInvitation
	invitationToken map[string]string
	auditEvents     []AuditEvent
}

type memoryInvitation struct {
	Invitation
	emailDigest []byte
	tokenDigest []byte
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		workspaces:      make(map[string]Workspace),
		personal:        make(map[string]string),
		memberships:     make(map[string]map[string]Membership),
		invitations:     make(map[string]memoryInvitation),
		invitationToken: make(map[string]string),
	}
}

func (repository *MemoryRepository) EnsurePersonal(
	_ context.Context,
	accountID string,
	name string,
	workspaceID string,
	now time.Time,
) (Workspace, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if existingID, ok := repository.personal[accountID]; ok {
		return repository.workspaces[existingID], false, nil
	}
	workspace := Workspace{
		ID:                workspaceID,
		PersonalAccountID: accountID,
		Name:              name,
		Status:            WorkspaceActive,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	repository.workspaces[workspaceID] = workspace
	repository.personal[accountID] = workspaceID
	repository.memberships[workspaceID] = map[string]Membership{
		accountID: {
			WorkspaceID: workspaceID,
			AccountID:   accountID,
			Role:        RoleOwner,
			Status:      MembershipActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	repository.audit(workspaceID, accountID, accountID, "workspace.personal_created", now)
	return workspace, true, nil
}

func (repository *MemoryRepository) Role(
	_ context.Context,
	workspaceID, accountID string,
) (Role, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	membership, err := repository.activeMembership(workspaceID, accountID)
	if err != nil {
		return "", err
	}
	return membership.Role, nil
}

func (repository *MemoryRepository) WorkspaceStatus(
	_ context.Context,
	workspaceID string,
) (WorkspaceStatus, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	workspace, ok := repository.workspaces[workspaceID]
	if !ok {
		return "", ErrNotFound
	}
	return workspace.Status, nil
}

func (repository *MemoryRepository) GetWorkspace(
	_ context.Context,
	workspaceID, actorID string,
) (Workspace, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, err := repository.activeMembership(workspaceID, actorID); err != nil {
		return Workspace{}, err
	}
	workspace, ok := repository.workspaces[workspaceID]
	if !ok {
		return Workspace{}, ErrNotFound
	}
	return workspace, nil
}

func (repository *MemoryRepository) ListMemberships(
	_ context.Context,
	workspaceID, actorID string,
) ([]Membership, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if _, err := repository.activeMembership(workspaceID, actorID); err != nil {
		return nil, err
	}
	var memberships []Membership
	for _, membership := range repository.memberships[workspaceID] {
		if membership.Status == MembershipActive {
			memberships = append(memberships, membership)
		}
	}
	slices.SortFunc(memberships, func(left, right Membership) int {
		return compareStrings(left.AccountID, right.AccountID)
	})
	return memberships, nil
}

func (repository *MemoryRepository) ListPendingInvitations(
	_ context.Context,
	workspaceID, actorID string,
	now time.Time,
) ([]Invitation, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if err := repository.requireOwner(workspaceID, actorID, true); err != nil {
		return nil, err
	}
	repository.expireInvitations(workspaceID, now)
	var invitations []Invitation
	for _, invitation := range repository.invitations {
		if invitation.WorkspaceID == workspaceID && invitation.Status == InvitationPending {
			invitations = append(invitations, invitation.Invitation)
		}
	}
	slices.SortFunc(invitations, func(left, right Invitation) int {
		return compareStrings(left.ID, right.ID)
	})
	return invitations, nil
}

func (repository *MemoryRepository) RenameWorkspace(
	_ context.Context,
	workspaceID, actorID, name string,
	now time.Time,
) (Workspace, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if err := repository.requireOwner(workspaceID, actorID, true); err != nil {
		return Workspace{}, err
	}
	workspace := repository.workspaces[workspaceID]
	workspace.Name = name
	workspace.UpdatedAt = now
	repository.workspaces[workspaceID] = workspace
	repository.audit(workspaceID, actorID, workspaceID, "workspace.renamed", now)
	return workspace, nil
}

func (repository *MemoryRepository) CreateInvitation(
	_ context.Context,
	command CreateInvitationCommand,
) (Invitation, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if err := repository.requireOwner(command.WorkspaceID, command.ActorID, true); err != nil {
		return Invitation{}, false, err
	}
	repository.expireInvitations(command.WorkspaceID, command.Now)

	for id, stored := range repository.invitations {
		if stored.WorkspaceID != command.WorkspaceID ||
			stored.Status != InvitationPending ||
			!bytes.Equal(stored.emailDigest, command.EmailDigest) {
			continue
		}
		delete(repository.invitationToken, hex.EncodeToString(stored.tokenDigest))
		stored.tokenDigest = append([]byte(nil), command.TokenDigest...)
		stored.ExpiresAt = command.ExpiresAt
		stored.UpdatedAt = command.Now
		repository.invitations[id] = stored
		repository.invitationToken[hex.EncodeToString(command.TokenDigest)] = id
		repository.audit(
			command.WorkspaceID,
			command.ActorID,
			id,
			"invitation.reissued",
			command.Now,
		)
		return stored.Invitation, true, nil
	}

	if repository.memberUsage(command.WorkspaceID, command.Now) >= command.MemberLimit &&
		command.MemberLimit > 0 {
		return Invitation{}, false, ErrMemberLimitReached
	}
	invitation := memoryInvitation{
		Invitation: Invitation{
			ID:          command.ID,
			WorkspaceID: command.WorkspaceID,
			Status:      InvitationPending,
			ExpiresAt:   command.ExpiresAt,
			CreatedAt:   command.Now,
			UpdatedAt:   command.Now,
		},
		emailDigest: append([]byte(nil), command.EmailDigest...),
		tokenDigest: append([]byte(nil), command.TokenDigest...),
	}
	repository.invitations[command.ID] = invitation
	repository.invitationToken[hex.EncodeToString(command.TokenDigest)] = command.ID
	repository.audit(
		command.WorkspaceID,
		command.ActorID,
		command.ID,
		"invitation.created",
		command.Now,
	)
	return invitation.Invitation, false, nil
}

func (repository *MemoryRepository) InvitationWorkspace(
	_ context.Context,
	tokenDigest []byte,
) (string, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	id, ok := repository.invitationToken[hex.EncodeToString(tokenDigest)]
	if !ok {
		return "", ErrNotFound
	}
	return repository.invitations[id].WorkspaceID, nil
}

func (repository *MemoryRepository) AcceptInvitation(
	_ context.Context,
	command AcceptInvitationCommand,
) (Membership, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	id, ok := repository.invitationToken[hex.EncodeToString(command.TokenDigest)]
	if !ok {
		return Membership{}, ErrNotFound
	}
	invitation := repository.invitations[id]
	switch invitation.Status {
	case InvitationAccepted:
		if invitation.AcceptedByAccountID != command.AccountID {
			return Membership{}, ErrInvitationAccepted
		}
		membership := repository.memberships[invitation.WorkspaceID][command.AccountID]
		if membership.Status != MembershipActive {
			return Membership{}, ErrForbidden
		}
		return membership, nil
	case InvitationRevoked:
		return Membership{}, ErrInvitationRevoked
	case InvitationExpired:
		return Membership{}, ErrInvitationExpired
	case InvitationPending:
	default:
		return Membership{}, ErrConflict
	}
	if !invitation.ExpiresAt.After(command.Now) {
		invitation.Status = InvitationExpired
		invitation.UpdatedAt = command.Now
		repository.invitations[id] = invitation
		repository.audit(
			invitation.WorkspaceID,
			"",
			invitation.ID,
			"invitation.expired",
			command.Now,
		)
		return Membership{}, ErrInvitationExpired
	}
	if !bytes.Equal(invitation.emailDigest, command.EmailDigest) {
		return Membership{}, ErrEmailMismatch
	}
	workspace, ok := repository.workspaces[invitation.WorkspaceID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	if workspace.Status != WorkspaceActive {
		return Membership{}, ErrWorkspaceInactive
	}
	repository.expireInvitations(invitation.WorkspaceID, command.Now)
	members := repository.memberships[invitation.WorkspaceID]
	membership, exists := members[command.AccountID]
	alreadyActive := exists && membership.Status == MembershipActive
	if !alreadyActive &&
		repository.memberUsage(invitation.WorkspaceID, command.Now) > command.MemberLimit &&
		command.MemberLimit > 0 {
		return Membership{}, ErrMemberLimitReached
	}
	if !alreadyActive {
		membership = Membership{
			WorkspaceID: invitation.WorkspaceID,
			AccountID:   command.AccountID,
			Role:        RoleMember,
			Status:      MembershipActive,
			CreatedAt:   command.Now,
			UpdatedAt:   command.Now,
		}
		if exists {
			membership.CreatedAt = members[command.AccountID].CreatedAt
		}
		members[command.AccountID] = membership
	}
	invitation.Status = InvitationAccepted
	invitation.AcceptedByAccountID = command.AccountID
	invitation.UpdatedAt = command.Now
	repository.invitations[id] = invitation
	repository.audit(
		invitation.WorkspaceID,
		command.AccountID,
		command.AccountID,
		"invitation.accepted",
		command.Now,
	)
	return membership, nil
}

func (repository *MemoryRepository) RevokeInvitation(
	_ context.Context,
	workspaceID, actorID, invitationID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if err := repository.requireOwner(workspaceID, actorID, true); err != nil {
		return err
	}
	invitation, ok := repository.invitations[invitationID]
	if !ok || invitation.WorkspaceID != workspaceID {
		return ErrNotFound
	}
	if invitation.Status == InvitationRevoked {
		return nil
	}
	if invitation.Status != InvitationPending {
		return ErrConflict
	}
	if !invitation.ExpiresAt.After(now) {
		invitation.Status = InvitationExpired
		invitation.UpdatedAt = now
		repository.invitations[invitationID] = invitation
		repository.audit(workspaceID, actorID, invitationID, "invitation.expired", now)
		return ErrInvitationExpired
	}
	invitation.Status = InvitationRevoked
	invitation.UpdatedAt = now
	repository.invitations[invitationID] = invitation
	repository.audit(workspaceID, actorID, invitationID, "invitation.revoked", now)
	return nil
}

func (repository *MemoryRepository) ChangeRole(
	_ context.Context,
	workspaceID, actorID, accountID string,
	role Role,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if err := repository.requireOwner(workspaceID, actorID, true); err != nil {
		return err
	}
	membership, err := repository.activeMembership(workspaceID, accountID)
	if err != nil {
		return err
	}
	if membership.Role == role {
		return nil
	}
	if membership.Role == RoleOwner && role == RoleMember &&
		repository.ownerCount(workspaceID) <= 1 {
		return ErrLastOwner
	}
	membership.Role = role
	membership.UpdatedAt = now
	repository.memberships[workspaceID][accountID] = membership
	eventType := "membership.promoted"
	if role == RoleMember {
		eventType = "membership.demoted"
	}
	repository.audit(workspaceID, actorID, accountID, eventType, now)
	return nil
}

func (repository *MemoryRepository) TransferOwnership(
	_ context.Context,
	workspaceID, actorID, targetAccountID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if err := repository.requireOwner(workspaceID, actorID, true); err != nil {
		return err
	}
	target, err := repository.activeMembership(workspaceID, targetAccountID)
	if err != nil {
		return err
	}
	if target.Role != RoleMember {
		return ErrConflict
	}
	actor := repository.memberships[workspaceID][actorID]
	target.Role = RoleOwner
	target.UpdatedAt = now
	actor.Role = RoleMember
	actor.UpdatedAt = now
	repository.memberships[workspaceID][targetAccountID] = target
	repository.memberships[workspaceID][actorID] = actor
	repository.audit(workspaceID, actorID, targetAccountID, "ownership.transferred", now)
	return nil
}

func (repository *MemoryRepository) RemoveMember(
	_ context.Context,
	workspaceID, actorID, targetAccountID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if err := repository.requireOwner(workspaceID, actorID, true); err != nil {
		return err
	}
	target, err := repository.activeMembership(workspaceID, targetAccountID)
	if err != nil {
		return err
	}
	if target.Role == RoleOwner && repository.ownerCount(workspaceID) <= 1 {
		return ErrLastOwner
	}
	target.Status = MembershipRemoved
	target.UpdatedAt = now
	repository.memberships[workspaceID][targetAccountID] = target
	repository.audit(workspaceID, actorID, targetAccountID, "membership.removed", now)
	return nil
}

func (repository *MemoryRepository) LeaveWorkspace(
	_ context.Context,
	workspaceID, accountID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	membership, err := repository.activeMembership(workspaceID, accountID)
	if err != nil {
		return err
	}
	if membership.Role == RoleOwner && repository.ownerCount(workspaceID) <= 1 {
		return ErrLastOwner
	}
	membership.Status = MembershipRemoved
	membership.UpdatedAt = now
	repository.memberships[workspaceID][accountID] = membership
	repository.audit(workspaceID, accountID, accountID, "membership.left", now)
	return nil
}

func (repository *MemoryRepository) RequestDeletion(
	_ context.Context,
	workspaceID, actorID string,
	now time.Time,
) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	if err := repository.requireOwner(workspaceID, actorID, false); err != nil {
		return err
	}
	workspace := repository.workspaces[workspaceID]
	if workspace.Status == WorkspaceDeletionPending {
		return nil
	}
	workspace.Status = WorkspaceDeletionPending
	workspace.UpdatedAt = now
	repository.workspaces[workspaceID] = workspace
	repository.audit(workspaceID, actorID, workspaceID, "workspace.deletion_requested", now)
	return nil
}

func (repository *MemoryRepository) Membership(
	workspaceID, accountID string,
) (Membership, bool) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	membership, ok := repository.memberships[workspaceID][accountID]
	return membership, ok
}

func (repository *MemoryRepository) StoredWorkspace(workspaceID string) (Workspace, bool) {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	workspace, ok := repository.workspaces[workspaceID]
	return workspace, ok
}

func (repository *MemoryRepository) AuditEvents() []AuditEvent {
	repository.mu.Lock()
	defer repository.mu.Unlock()

	return append([]AuditEvent(nil), repository.auditEvents...)
}

func (repository *MemoryRepository) activeMembership(
	workspaceID, accountID string,
) (Membership, error) {
	members, ok := repository.memberships[workspaceID]
	if !ok {
		return Membership{}, ErrNotFound
	}
	membership, ok := members[accountID]
	if !ok || membership.Status != MembershipActive {
		return Membership{}, ErrForbidden
	}
	return membership, nil
}

func (repository *MemoryRepository) requireOwner(
	workspaceID, accountID string,
	requireActiveWorkspace bool,
) error {
	workspace, ok := repository.workspaces[workspaceID]
	if !ok {
		return ErrNotFound
	}
	if requireActiveWorkspace && workspace.Status != WorkspaceActive {
		return ErrWorkspaceInactive
	}
	membership, err := repository.activeMembership(workspaceID, accountID)
	if err != nil {
		return err
	}
	if membership.Role != RoleOwner {
		return ErrForbidden
	}
	return nil
}

func (repository *MemoryRepository) ownerCount(workspaceID string) int {
	count := 0
	for _, membership := range repository.memberships[workspaceID] {
		if membership.Status == MembershipActive && membership.Role == RoleOwner {
			count++
		}
	}
	return count
}

func (repository *MemoryRepository) memberUsage(workspaceID string, now time.Time) int {
	usage := 0
	for _, membership := range repository.memberships[workspaceID] {
		if membership.Status == MembershipActive {
			usage++
		}
	}
	for _, invitation := range repository.invitations {
		if invitation.WorkspaceID == workspaceID &&
			invitation.Status == InvitationPending &&
			invitation.ExpiresAt.After(now) {
			usage++
		}
	}
	return usage
}

func (repository *MemoryRepository) expireInvitations(workspaceID string, now time.Time) {
	for id, invitation := range repository.invitations {
		if invitation.WorkspaceID != workspaceID ||
			invitation.Status != InvitationPending ||
			invitation.ExpiresAt.After(now) {
			continue
		}
		invitation.Status = InvitationExpired
		invitation.UpdatedAt = now
		repository.invitations[id] = invitation
		repository.audit(workspaceID, "", id, "invitation.expired", now)
	}
}

func (repository *MemoryRepository) audit(
	workspaceID, actorID, subjectID, eventType string,
	now time.Time,
) {
	repository.auditEvents = append(repository.auditEvents, AuditEvent{
		ID:          "audit-" + timeKey(now, len(repository.auditEvents)),
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		SubjectID:   subjectID,
		Type:        eventType,
		Outcome:     "succeeded",
		OccurredAt:  now,
	})
}

func timeKey(now time.Time, sequence int) string {
	return now.UTC().Format("20060102150405.000000000") + "-" + strconv.Itoa(sequence+1)
}

func compareStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
