package workspaces

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

const invitationLifetime = 7 * 24 * time.Hour

type Service struct {
	repository Repository
	limits     MemberLimitProvider
	emailKey   []byte
	now        func() time.Time
	random     func([]byte) error
}

type Option func(*Service)

func WithClock(clock func() time.Time) Option {
	return func(service *Service) {
		service.now = clock
	}
}

func WithRandom(random func([]byte) error) Option {
	return func(service *Service) {
		service.random = random
	}
}

func NewService(
	repository Repository,
	limits MemberLimitProvider,
	emailDigestKey []byte,
	options ...Option,
) (*Service, error) {
	if repository == nil || limits == nil {
		return nil, fmt.Errorf("%w: repository and member limits are required", ErrInvalidArgument)
	}
	if len(emailDigestKey) < 32 {
		return nil, fmt.Errorf("%w: email digest key must contain at least 32 bytes", ErrInvalidArgument)
	}
	service := &Service{
		repository: repository,
		limits:     limits,
		emailKey:   append([]byte(nil), emailDigestKey...),
		now:        time.Now,
		random: func(destination []byte) error {
			_, err := rand.Read(destination)
			return err
		},
	}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

func (service *Service) EnsurePersonalWorkspace(
	ctx context.Context,
	accountID, name string,
) (Workspace, bool, error) {
	if strings.TrimSpace(accountID) == "" {
		return Workspace{}, false, ErrUnauthenticated
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, false, fmt.Errorf("%w: workspace name is required", ErrInvalidArgument)
	}
	id, err := service.randomID(18)
	if err != nil {
		return Workspace{}, false, err
	}
	return service.repository.EnsurePersonal(ctx, accountID, name, id, service.now().UTC())
}

func (service *Service) Authorize(
	ctx context.Context,
	workspaceID, accountID string,
	permission Permission,
) error {
	if strings.TrimSpace(accountID) == "" {
		return ErrUnauthenticated
	}
	role, err := service.repository.Role(ctx, workspaceID, accountID)
	if err != nil {
		return err
	}
	if !Allowed(role, permission) {
		return ErrForbidden
	}
	status, err := service.repository.WorkspaceStatus(ctx, workspaceID)
	if err != nil {
		return err
	}
	if status != WorkspaceActive && permissionMutatesWorkspace(permission) {
		return ErrWorkspaceInactive
	}
	return nil
}

func (service *Service) Invite(
	ctx context.Context,
	workspaceID, actorID, email string,
) (InvitationResult, error) {
	if err := service.Authorize(
		ctx,
		workspaceID,
		actorID,
		PermissionManageMembers,
	); err != nil {
		return InvitationResult{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return InvitationResult{}, err
	}
	limit, err := service.memberLimit(ctx, workspaceID)
	if err != nil {
		return InvitationResult{}, err
	}
	token, tokenDigest, err := service.newToken()
	if err != nil {
		return InvitationResult{}, err
	}
	invitationID, err := service.randomID(18)
	if err != nil {
		return InvitationResult{}, err
	}
	now := service.now().UTC()
	invitation, reissued, err := service.repository.CreateInvitation(ctx, CreateInvitationCommand{
		ID:          invitationID,
		WorkspaceID: workspaceID,
		ActorID:     actorID,
		EmailDigest: service.emailDigest(normalized),
		TokenDigest: tokenDigest,
		ExpiresAt:   now.Add(invitationLifetime),
		Now:         now,
		MemberLimit: limit,
	})
	if err != nil {
		return InvitationResult{}, err
	}
	return InvitationResult{Invitation: invitation, Token: token, Reissued: reissued}, nil
}

func (service *Service) Workspace(
	ctx context.Context,
	workspaceID, accountID string,
) (Workspace, error) {
	if err := service.Authorize(
		ctx,
		workspaceID,
		accountID,
		PermissionViewWorkspace,
	); err != nil {
		return Workspace{}, err
	}
	return service.repository.GetWorkspace(ctx, workspaceID, accountID)
}

func (service *Service) Memberships(
	ctx context.Context,
	workspaceID, accountID string,
) ([]Membership, error) {
	if err := service.Authorize(
		ctx,
		workspaceID,
		accountID,
		PermissionViewWorkspace,
	); err != nil {
		return nil, err
	}
	return service.repository.ListMemberships(ctx, workspaceID, accountID)
}

func (service *Service) PendingInvitations(
	ctx context.Context,
	workspaceID, accountID string,
) ([]Invitation, error) {
	if err := service.Authorize(
		ctx,
		workspaceID,
		accountID,
		PermissionManageMembers,
	); err != nil {
		return nil, err
	}
	return service.repository.ListPendingInvitations(
		ctx,
		workspaceID,
		accountID,
		service.now().UTC(),
	)
}

func (service *Service) RenameWorkspace(
	ctx context.Context,
	workspaceID, actorID, name string,
) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, fmt.Errorf("%w: workspace name is required", ErrInvalidArgument)
	}
	return service.repository.RenameWorkspace(
		ctx,
		workspaceID,
		actorID,
		name,
		service.now().UTC(),
	)
}

func (service *Service) AcceptInvitation(
	ctx context.Context,
	token, accountID, verifiedEmail string,
	emailVerified bool,
) (Membership, error) {
	if strings.TrimSpace(accountID) == "" {
		return Membership{}, ErrUnauthenticated
	}
	if !emailVerified {
		return Membership{}, ErrEmailUnverified
	}
	normalized, err := normalizeEmail(verifiedEmail)
	if err != nil {
		return Membership{}, err
	}
	tokenDigest, err := digestToken(token)
	if err != nil {
		return Membership{}, err
	}
	workspaceID, err := service.repository.InvitationWorkspace(ctx, tokenDigest)
	if err != nil {
		return Membership{}, err
	}
	limit, err := service.memberLimit(ctx, workspaceID)
	if err != nil {
		return Membership{}, err
	}
	return service.repository.AcceptInvitation(ctx, AcceptInvitationCommand{
		AccountID:   accountID,
		EmailDigest: service.emailDigest(normalized),
		TokenDigest: tokenDigest,
		Now:         service.now().UTC(),
		MemberLimit: limit,
	})
}

func (service *Service) RevokeInvitation(
	ctx context.Context,
	workspaceID, actorID, invitationID string,
) error {
	return service.repository.RevokeInvitation(
		ctx,
		workspaceID,
		actorID,
		invitationID,
		service.now().UTC(),
	)
}

func (service *Service) ChangeRole(
	ctx context.Context,
	workspaceID, actorID, accountID string,
	role Role,
) error {
	if !role.Valid() {
		return fmt.Errorf("%w: unsupported role", ErrInvalidArgument)
	}
	return service.repository.ChangeRole(
		ctx,
		workspaceID,
		actorID,
		accountID,
		role,
		service.now().UTC(),
	)
}

func (service *Service) TransferOwnership(
	ctx context.Context,
	workspaceID, actorID, targetAccountID string,
) error {
	if actorID == targetAccountID {
		return fmt.Errorf("%w: ownership target must differ from actor", ErrInvalidArgument)
	}
	return service.repository.TransferOwnership(
		ctx,
		workspaceID,
		actorID,
		targetAccountID,
		service.now().UTC(),
	)
}

func (service *Service) RemoveMember(
	ctx context.Context,
	workspaceID, actorID, targetAccountID string,
) error {
	return service.repository.RemoveMember(
		ctx,
		workspaceID,
		actorID,
		targetAccountID,
		service.now().UTC(),
	)
}

func (service *Service) LeaveWorkspace(
	ctx context.Context,
	workspaceID, accountID string,
) error {
	return service.repository.LeaveWorkspace(ctx, workspaceID, accountID, service.now().UTC())
}

func (service *Service) RequestDeletion(
	ctx context.Context,
	workspaceID, actorID string,
	confirmation DeletionConfirmation,
) error {
	if !confirmation.IdentityReconfirmed || !confirmation.ExplicitlyConfirmed {
		return ErrConfirmationRequired
	}
	return service.repository.RequestDeletion(ctx, workspaceID, actorID, service.now().UTC())
}

func permissionMutatesWorkspace(permission Permission) bool {
	switch permission {
	case PermissionManageContent,
		PermissionManageChannels,
		PermissionManageMembers,
		PermissionManageWorkspace,
		PermissionManageBilling,
		PermissionTransferOwnership:
		return true
	default:
		return false
	}
}

func (service *Service) memberLimit(ctx context.Context, workspaceID string) (int, error) {
	limit, available, err := service.limits.MemberLimit(ctx, workspaceID)
	if err != nil {
		return 0, err
	}
	if !available || limit == 0 || limit < -1 {
		return 0, ErrEntitlementUnavailable
	}
	return limit, nil
}

func (service *Service) newToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if err := service.random(raw); err != nil {
		return "", nil, fmt.Errorf("generate invitation token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	return token, digest[:], nil
}

func digestToken(token string) ([]byte, error) {
	if len(token) < 32 {
		return nil, fmt.Errorf("%w: malformed invitation token", ErrInvalidArgument)
	}
	digest := sha256.Sum256([]byte(token))
	return digest[:], nil
}

func (service *Service) emailDigest(normalizedEmail string) []byte {
	digest := hmac.New(sha256.New, service.emailKey)
	_, _ = digest.Write([]byte(normalizedEmail))
	return digest.Sum(nil)
}

func (service *Service) randomID(size int) (string, error) {
	raw := make([]byte, size)
	if err := service.random(raw); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func normalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return "", fmt.Errorf("%w: valid bare email address required", ErrInvalidArgument)
	}
	local, domain, found := strings.Cut(address.Address, "@")
	if !found || local == "" || domain == "" {
		return "", fmt.Errorf("%w: valid email address required", ErrInvalidArgument)
	}
	return strings.ToLower(local) + "@" + strings.ToLower(domain), nil
}
