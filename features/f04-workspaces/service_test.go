package workspaces

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	testNow      = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	testEmailKey = []byte("0123456789abcdef0123456789abcdef")
)

type fixedLimits struct {
	limit     int
	available bool
	err       error
}

type mutableLimits struct {
	limit int
}

func (limits *mutableLimits) MemberLimit(
	context.Context,
	string,
) (int, bool, error) {
	return limits.limit, true, nil
}

func (limits fixedLimits) MemberLimit(
	context.Context,
	string,
) (int, bool, error) {
	return limits.limit, limits.available, limits.err
}

func newTestService(t *testing.T, repository Repository, limit int) *Service {
	t.Helper()
	service, err := NewService(
		repository,
		fixedLimits{limit: limit, available: true},
		testEmailKey,
		WithClock(func() time.Time { return testNow }),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func createPersonal(
	t *testing.T,
	service *Service,
	accountID string,
) Workspace {
	t.Helper()
	workspace, _, err := service.EnsurePersonalWorkspace(
		context.Background(),
		accountID,
		"Personal workspace",
	)
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}

func inviteAndAccept(
	t *testing.T,
	service *Service,
	workspaceID, ownerID, memberID, email string,
) {
	t.Helper()
	invitation, err := service.Invite(context.Background(), workspaceID, ownerID, email)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptInvitation(
		context.Background(),
		invitation.Token,
		memberID,
		email,
		true,
	); err != nil {
		t.Fatal(err)
	}
}

func TestEnsurePersonalWorkspaceIsIdempotentUnderConcurrency(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 15)

	const attempts = 32
	results := make(chan Workspace, attempts)
	errorsFound := make(chan error, attempts)
	var created atomic.Int32
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			workspace, wasCreated, err := service.EnsurePersonalWorkspace(
				context.Background(),
				"account-1",
				"Carlo's workspace",
			)
			if err != nil {
				errorsFound <- err
				return
			}
			if wasCreated {
				created.Add(1)
			}
			results <- workspace
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)

	for err := range errorsFound {
		t.Fatalf("EnsurePersonalWorkspace() error = %v", err)
	}
	var workspaceID string
	for result := range results {
		if workspaceID == "" {
			workspaceID = result.ID
		}
		if result.ID != workspaceID {
			t.Fatalf("workspace ID = %q, want %q", result.ID, workspaceID)
		}
	}
	if created.Load() != 1 {
		t.Fatalf("created count = %d, want 1", created.Load())
	}
	membership, ok := repository.Membership(workspaceID, "account-1")
	if !ok || membership.Role != RoleOwner || membership.Status != MembershipActive {
		t.Fatalf("personal membership = %#v, %v", membership, ok)
	}
}

func TestOwnerMemberPermissionMatrix(t *testing.T) {
	all := []Permission{
		PermissionViewWorkspace,
		PermissionManageContent,
		PermissionManageChannels,
		PermissionManageMembers,
		PermissionManageWorkspace,
		PermissionManageBilling,
		PermissionTransferOwnership,
		PermissionLeaveWorkspace,
		PermissionDeleteWorkspace,
	}
	memberAllowed := map[Permission]bool{
		PermissionViewWorkspace:  true,
		PermissionManageContent:  true,
		PermissionLeaveWorkspace: true,
	}
	for _, permission := range all {
		if !Allowed(RoleOwner, permission) {
			t.Errorf("Owner denied %q", permission)
		}
		if got := Allowed(RoleMember, permission); got != memberAllowed[permission] {
			t.Errorf("Member permission %q = %v, want %v", permission, got, memberAllowed[permission])
		}
	}
}

func TestAuthorizationUsesStoredMembershipRole(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 15)
	workspace := createPersonal(t, service, "owner")
	inviteAndAccept(t, service, workspace.ID, "owner", "member", "member@example.com")

	if err := service.Authorize(
		context.Background(),
		workspace.ID,
		"member",
		PermissionManageContent,
	); err != nil {
		t.Fatalf("Member content authorization error = %v", err)
	}
	if err := service.Authorize(
		context.Background(),
		workspace.ID,
		"member",
		PermissionManageMembers,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Member member-management error = %v, want forbidden", err)
	}
	if _, err := service.Invite(
		context.Background(),
		workspace.ID,
		"member",
		"other@example.com",
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Member Invite() error = %v, want forbidden", err)
	}
}

func TestWorkspaceReadsAndSettingsFollowRoleMatrix(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 5)
	workspace := createPersonal(t, service, "owner")
	inviteAndAccept(t, service, workspace.ID, "owner", "member", "member@example.com")

	read, err := service.Workspace(context.Background(), workspace.ID, "member")
	if err != nil {
		t.Fatal(err)
	}
	if read.ID != workspace.ID {
		t.Fatalf("Workspace() ID = %q, want %q", read.ID, workspace.ID)
	}
	memberships, err := service.Memberships(context.Background(), workspace.ID, "member")
	if err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 2 {
		t.Fatalf("Memberships() count = %d, want 2", len(memberships))
	}
	if _, err = service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"pending@example.com",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = service.PendingInvitations(
		context.Background(),
		workspace.ID,
		"member",
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Member PendingInvitations() error = %v, want forbidden", err)
	}
	pending, err := service.PendingInvitations(
		context.Background(),
		workspace.ID,
		"owner",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("PendingInvitations() count = %d, want 1", len(pending))
	}
	if _, err = service.RenameWorkspace(
		context.Background(),
		workspace.ID,
		"member",
		"Forbidden rename",
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Member RenameWorkspace() error = %v, want forbidden", err)
	}
	renamed, err := service.RenameWorkspace(
		context.Background(),
		workspace.ID,
		"owner",
		"Team workspace",
	)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "Team workspace" {
		t.Fatalf("renamed workspace name = %q", renamed.Name)
	}
}

func TestInvitationLifecycleIsSecureAndIdempotent(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 5)
	workspace := createPersonal(t, service, "owner")

	first, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"Member@Example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	reissued, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"member@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reissued.Reissued || reissued.Invitation.ID != first.Invitation.ID {
		t.Fatalf("reissued invitation = %#v, want same invitation", reissued)
	}
	if first.Token == reissued.Token {
		t.Fatal("reissued token was not rotated")
	}
	if _, err := service.AcceptInvitation(
		context.Background(),
		first.Token,
		"member",
		"member@example.com",
		true,
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token acceptance error = %v, want not found", err)
	}
	if _, err := service.AcceptInvitation(
		context.Background(),
		reissued.Token,
		"member",
		"wrong@example.com",
		true,
	); !errors.Is(err, ErrEmailMismatch) {
		t.Fatalf("wrong email acceptance error = %v, want mismatch", err)
	}
	if _, err := service.AcceptInvitation(
		context.Background(),
		reissued.Token,
		"member",
		"member@example.com",
		false,
	); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unverified acceptance error = %v, want unverified", err)
	}
	firstMembership, err := service.AcceptInvitation(
		context.Background(),
		reissued.Token,
		"member",
		"member@example.com",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	retriedMembership, err := service.AcceptInvitation(
		context.Background(),
		reissued.Token,
		"member",
		"member@example.com",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstMembership != retriedMembership {
		t.Fatalf("retry membership = %#v, want %#v", retriedMembership, firstMembership)
	}

	stored := repository.invitations[reissued.Invitation.ID]
	if string(stored.tokenDigest) == reissued.Token {
		t.Fatal("repository retained plaintext invitation token")
	}
	if len(stored.emailDigest) == 0 || string(stored.emailDigest) == "member@example.com" {
		t.Fatal("repository retained plaintext invitation email")
	}
}

func TestPendingInvitationsReserveMemberCapacity(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 2)
	workspace := createPersonal(t, service, "owner")
	if _, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"first@example.com",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"second@example.com",
	); !errors.Is(err, ErrMemberLimitReached) {
		t.Fatalf("second Invite() error = %v, want member limit", err)
	}
}

func TestExistingMemberAcceptanceIsIdempotentDuringDowngrade(t *testing.T) {
	repository := NewMemoryRepository()
	limits := &mutableLimits{limit: 3}
	service, err := NewService(
		repository,
		limits,
		testEmailKey,
		WithClock(func() time.Time { return testNow }),
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace := createPersonal(t, service, "owner")
	inviteAndAccept(t, service, workspace.ID, "owner", "member", "member@example.com")
	invitation, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"member@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}

	limits.limit = 1
	membership, err := service.AcceptInvitation(
		context.Background(),
		invitation.Token,
		"member",
		"member@example.com",
		true,
	)
	if err != nil {
		t.Fatalf("existing Member acceptance error = %v", err)
	}
	if membership.Role != RoleMember || membership.Status != MembershipActive {
		t.Fatalf("existing membership = %#v", membership)
	}
}

func TestInvitationCanBeRevokedAndExpiresAfterSevenDays(t *testing.T) {
	repository := NewMemoryRepository()
	now := testNow
	service, err := NewService(
		repository,
		fixedLimits{limit: 5, available: true},
		testEmailKey,
		WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace := createPersonal(t, service, "owner")

	revoked, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"revoked@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeInvitation(
		context.Background(),
		workspace.ID,
		"owner",
		revoked.Invitation.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcceptInvitation(
		context.Background(),
		revoked.Token,
		"revoked-member",
		"revoked@example.com",
		true,
	); !errors.Is(err, ErrInvitationRevoked) {
		t.Fatalf("revoked acceptance error = %v, want revoked", err)
	}

	expiring, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"expired@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(7 * 24 * time.Hour)
	if _, err := service.AcceptInvitation(
		context.Background(),
		expiring.Token,
		"expired-member",
		"expired@example.com",
		true,
	); !errors.Is(err, ErrInvitationExpired) {
		t.Fatalf("expired acceptance error = %v, want expired", err)
	}
}

func TestOwnerProtectionAndAtomicTransfer(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 5)
	workspace := createPersonal(t, service, "owner")
	inviteAndAccept(t, service, workspace.ID, "owner", "member", "member@example.com")

	if err := service.LeaveWorkspace(
		context.Background(),
		workspace.ID,
		"owner",
	); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last Owner leave error = %v, want last owner", err)
	}
	if err := service.ChangeRole(
		context.Background(),
		workspace.ID,
		"owner",
		"owner",
		RoleMember,
	); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last Owner demotion error = %v, want last owner", err)
	}
	if err := service.TransferOwnership(
		context.Background(),
		workspace.ID,
		"owner",
		"member",
	); err != nil {
		t.Fatal(err)
	}
	oldOwner, _ := repository.Membership(workspace.ID, "owner")
	newOwner, _ := repository.Membership(workspace.ID, "member")
	if oldOwner.Role != RoleMember || newOwner.Role != RoleOwner {
		t.Fatalf("transfer roles = %s/%s, want member/owner", oldOwner.Role, newOwner.Role)
	}
}

func TestRemovalRevokesFutureWorkspaceAuthorization(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 5)
	workspace := createPersonal(t, service, "owner")
	inviteAndAccept(t, service, workspace.ID, "owner", "member", "member@example.com")

	if err := service.RemoveMember(
		context.Background(),
		workspace.ID,
		"owner",
		"member",
	); err != nil {
		t.Fatal(err)
	}
	if err := service.Authorize(
		context.Background(),
		workspace.ID,
		"member",
		PermissionViewWorkspace,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("removed Member authorization error = %v, want forbidden", err)
	}
	membership, _ := repository.Membership(workspace.ID, "member")
	if membership.Status != MembershipRemoved {
		t.Fatalf("membership status = %q, want removed", membership.Status)
	}
}

func TestConcurrentOwnerChangesCannotRemoveEveryOwner(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 5)
	workspace := createPersonal(t, service, "owner-a")
	inviteAndAccept(t, service, workspace.ID, "owner-a", "owner-b", "owner-b@example.com")
	if err := service.ChangeRole(
		context.Background(),
		workspace.ID,
		"owner-a",
		"owner-b",
		RoleOwner,
	); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, change := range [][2]string{{"owner-a", "owner-b"}, {"owner-b", "owner-a"}} {
		wait.Add(1)
		go func(actorID, targetID string) {
			defer wait.Done()
			<-start
			results <- service.ChangeRole(
				context.Background(),
				workspace.ID,
				actorID,
				targetID,
				RoleMember,
			)
		}(change[0], change[1])
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent demotions = %d, want 1", successes)
	}
	owners := 0
	for _, accountID := range []string{"owner-a", "owner-b"} {
		membership, _ := repository.Membership(workspace.ID, accountID)
		if membership.Status == MembershipActive && membership.Role == RoleOwner {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("active Owners = %d, want 1", owners)
	}
}

func TestConcurrentInvitationsCannotOverbookLimit(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 2)
	workspace := createPersonal(t, service, "owner")

	const attempts = 16
	start := make(chan struct{})
	results := make(chan error, attempts)
	var wait sync.WaitGroup
	for index := range attempts {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := service.Invite(
				context.Background(),
				workspace.ID,
				"owner",
				"member-"+string(rune('a'+index))+"@example.com",
			)
			results <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrMemberLimitReached) {
			t.Fatalf("concurrent Invite() error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful invitations = %d, want 1", successes)
	}
}

func TestWorkspaceDeletionRequiresOwnerAndDoubleConfirmation(t *testing.T) {
	repository := NewMemoryRepository()
	service := newTestService(t, repository, 5)
	workspace := createPersonal(t, service, "owner")
	inviteAndAccept(t, service, workspace.ID, "owner", "member", "member@example.com")

	if err := service.RequestDeletion(
		context.Background(),
		workspace.ID,
		"owner",
		DeletionConfirmation{IdentityReconfirmed: true},
	); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("partial confirmation error = %v, want confirmation required", err)
	}
	if err := service.RequestDeletion(
		context.Background(),
		workspace.ID,
		"member",
		DeletionConfirmation{IdentityReconfirmed: true, ExplicitlyConfirmed: true},
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Member deletion error = %v, want forbidden", err)
	}
	if err := service.RequestDeletion(
		context.Background(),
		workspace.ID,
		"owner",
		DeletionConfirmation{IdentityReconfirmed: true, ExplicitlyConfirmed: true},
	); err != nil {
		t.Fatal(err)
	}
	stored, _ := repository.StoredWorkspace(workspace.ID)
	if stored.Status != WorkspaceDeletionPending {
		t.Fatalf("workspace status = %q, want deletion pending", stored.Status)
	}
	if _, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"blocked@example.com",
	); !errors.Is(err, ErrWorkspaceInactive) {
		t.Fatalf("Invite during deletion error = %v, want inactive", err)
	}
	if err := service.Authorize(
		context.Background(),
		workspace.ID,
		"owner",
		PermissionManageContent,
	); !errors.Is(err, ErrWorkspaceInactive) {
		t.Fatalf("content authorization during deletion error = %v, want inactive", err)
	}
	if err := service.Authorize(
		context.Background(),
		workspace.ID,
		"owner",
		PermissionViewWorkspace,
	); err != nil {
		t.Fatalf("view authorization during deletion error = %v", err)
	}
}

func TestUnavailableEntitlementFailsClosed(t *testing.T) {
	repository := NewMemoryRepository()
	service, err := NewService(
		repository,
		fixedLimits{available: false},
		testEmailKey,
		WithClock(func() time.Time { return testNow }),
	)
	if err != nil {
		t.Fatal(err)
	}
	workspace := createPersonal(t, service, "owner")
	if _, err := service.Invite(
		context.Background(),
		workspace.ID,
		"owner",
		"member@example.com",
	); !errors.Is(err, ErrEntitlementUnavailable) {
		t.Fatalf("Invite() error = %v, want unavailable entitlement", err)
	}
}
