// Package workspaces implements the workspace, membership, invitation, and
// Owner/Member authorization boundary.
package workspaces

import (
	"context"
	"errors"
	"time"
)

const FeatureID = "workspaces"

type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

func (role Role) Valid() bool {
	return role == RoleOwner || role == RoleMember
}

type MembershipStatus string

const (
	MembershipActive  MembershipStatus = "active"
	MembershipRemoved MembershipStatus = "removed"
)

type WorkspaceStatus string

const (
	WorkspaceActive          WorkspaceStatus = "active"
	WorkspaceDeletionPending WorkspaceStatus = "deletion_pending"
)

type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationRevoked  InvitationStatus = "revoked"
	InvitationExpired  InvitationStatus = "expired"
)

type Permission string

const (
	PermissionViewWorkspace     Permission = "workspace.view"
	PermissionManageContent     Permission = "content.manage"
	PermissionManageChannels    Permission = "channels.manage"
	PermissionManageMembers     Permission = "members.manage"
	PermissionManageWorkspace   Permission = "workspace.manage"
	PermissionManageBilling     Permission = "billing.manage"
	PermissionTransferOwnership Permission = "ownership.transfer"
	PermissionLeaveWorkspace    Permission = "workspace.leave"
	PermissionDeleteWorkspace   Permission = "workspace.delete"
)

var permissions = map[Role]map[Permission]bool{
	RoleOwner: {
		PermissionViewWorkspace:     true,
		PermissionManageContent:     true,
		PermissionManageChannels:    true,
		PermissionManageMembers:     true,
		PermissionManageWorkspace:   true,
		PermissionManageBilling:     true,
		PermissionTransferOwnership: true,
		PermissionLeaveWorkspace:    true,
		PermissionDeleteWorkspace:   true,
	},
	RoleMember: {
		PermissionViewWorkspace:  true,
		PermissionManageContent:  true,
		PermissionLeaveWorkspace: true,
	},
}

func Allowed(role Role, permission Permission) bool {
	return permissions[role][permission]
}

type Workspace struct {
	ID                string
	PersonalAccountID string
	Name              string
	Status            WorkspaceStatus
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Membership struct {
	WorkspaceID string
	AccountID   string
	Role        Role
	Status      MembershipStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Invitation struct {
	ID                  string
	WorkspaceID         string
	Status              InvitationStatus
	ExpiresAt           time.Time
	AcceptedByAccountID string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type InvitationResult struct {
	Invitation Invitation
	Token      string
	Reissued   bool
}

type AuditEvent struct {
	ID          string
	WorkspaceID string
	ActorID     string
	SubjectID   string
	Type        string
	Outcome     string
	OccurredAt  time.Time
}

type DeletionConfirmation struct {
	IdentityReconfirmed bool
	ExplicitlyConfirmed bool
}

var (
	ErrInvalidArgument        = errors.New("invalid argument")
	ErrUnauthenticated        = errors.New("authentication required")
	ErrForbidden              = errors.New("operation forbidden")
	ErrNotFound               = errors.New("resource not found")
	ErrConflict               = errors.New("resource conflict")
	ErrLastOwner              = errors.New("workspace must retain at least one owner")
	ErrWorkspaceInactive      = errors.New("workspace is not active")
	ErrInvitationExpired      = errors.New("invitation expired")
	ErrInvitationRevoked      = errors.New("invitation revoked")
	ErrInvitationAccepted     = errors.New("invitation already accepted by another account")
	ErrEmailUnverified        = errors.New("a verified email is required")
	ErrEmailMismatch          = errors.New("verified email does not match invitation")
	ErrMemberLimitReached     = errors.New("workspace member limit reached")
	ErrEntitlementUnavailable = errors.New("workspace member entitlement unavailable")
	ErrConfirmationRequired   = errors.New("identity and explicit confirmation are required")
)

type Repository interface {
	EnsurePersonal(
		ctx context.Context,
		accountID string,
		name string,
		workspaceID string,
		now time.Time,
	) (workspace Workspace, created bool, err error)
	Role(ctx context.Context, workspaceID, accountID string) (Role, error)
	WorkspaceStatus(ctx context.Context, workspaceID string) (WorkspaceStatus, error)
	GetWorkspace(ctx context.Context, workspaceID, actorID string) (Workspace, error)
	ListMemberships(ctx context.Context, workspaceID, actorID string) ([]Membership, error)
	ListPendingInvitations(
		ctx context.Context,
		workspaceID, actorID string,
		now time.Time,
	) ([]Invitation, error)
	RenameWorkspace(
		ctx context.Context,
		workspaceID, actorID, name string,
		now time.Time,
	) (Workspace, error)
	CreateInvitation(
		ctx context.Context,
		command CreateInvitationCommand,
	) (invitation Invitation, reissued bool, err error)
	InvitationWorkspace(ctx context.Context, tokenDigest []byte) (string, error)
	AcceptInvitation(
		ctx context.Context,
		command AcceptInvitationCommand,
	) (Membership, error)
	RevokeInvitation(
		ctx context.Context,
		workspaceID, actorID, invitationID string,
		now time.Time,
	) error
	ChangeRole(
		ctx context.Context,
		workspaceID, actorID, accountID string,
		role Role,
		now time.Time,
	) error
	TransferOwnership(
		ctx context.Context,
		workspaceID, actorID, targetAccountID string,
		now time.Time,
	) error
	RemoveMember(
		ctx context.Context,
		workspaceID, actorID, targetAccountID string,
		now time.Time,
	) error
	LeaveWorkspace(
		ctx context.Context,
		workspaceID, accountID string,
		now time.Time,
	) error
	RequestDeletion(
		ctx context.Context,
		workspaceID, actorID string,
		now time.Time,
	) error
}

type CreateInvitationCommand struct {
	ID          string
	WorkspaceID string
	ActorID     string
	EmailDigest []byte
	TokenDigest []byte
	ExpiresAt   time.Time
	Now         time.Time
	MemberLimit int
}

type AcceptInvitationCommand struct {
	AccountID   string
	EmailDigest []byte
	TokenDigest []byte
	Now         time.Time
	MemberLimit int
}

type MemberLimitProvider interface {
	MemberLimit(ctx context.Context, workspaceID string) (limit int, available bool, err error)
}
