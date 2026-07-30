package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRepositoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("F04_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F04_DATABASE_URL is not set")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	repository, err := NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(
		repository,
		fixedLimits{limit: 5, available: true},
		testEmailKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	ownerID := "integration-owner-" + suffix

	const concurrentEnsures = 8
	workspaces := make(chan Workspace, concurrentEnsures)
	failures := make(chan error, concurrentEnsures)
	var wait sync.WaitGroup
	for range concurrentEnsures {
		wait.Add(1)
		go func() {
			defer wait.Done()
			workspace, _, ensureErr := service.EnsurePersonalWorkspace(
				context.Background(),
				ownerID,
				"Integration workspace",
			)
			if ensureErr != nil {
				failures <- ensureErr
				return
			}
			workspaces <- workspace
		}()
	}
	wait.Wait()
	close(workspaces)
	close(failures)
	for failure := range failures {
		t.Fatalf("concurrent EnsurePersonalWorkspace() error = %v", failure)
	}
	var workspace Workspace
	for result := range workspaces {
		if workspace.ID == "" {
			workspace = result
		}
		if result.ID != workspace.ID {
			t.Fatalf("workspace ID = %q, want %q", result.ID, workspace.ID)
		}
	}

	memberID := "integration-member-" + suffix
	email := "integration-" + suffix + "@example.com"
	invitation, err := service.Invite(
		context.Background(),
		workspace.ID,
		ownerID,
		email,
	)
	if err != nil {
		t.Fatal(err)
	}
	membership, err := service.AcceptInvitation(
		context.Background(),
		invitation.Token,
		memberID,
		email,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if membership.Role != RoleMember || membership.Status != MembershipActive {
		t.Fatalf("accepted membership = %#v", membership)
	}
	read, err := service.Workspace(context.Background(), workspace.ID, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if read.ID != workspace.ID {
		t.Fatalf("Workspace() ID = %q, want %q", read.ID, workspace.ID)
	}
	memberships, err := service.Memberships(context.Background(), workspace.ID, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if len(memberships) != 2 {
		t.Fatalf("Memberships() count = %d, want 2", len(memberships))
	}
	if _, err = service.RenameWorkspace(
		context.Background(),
		workspace.ID,
		memberID,
		"Forbidden rename",
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Member RenameWorkspace() error = %v, want forbidden", err)
	}
	if _, err = service.RenameWorkspace(
		context.Background(),
		workspace.ID,
		ownerID,
		"Renamed integration workspace",
	); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Invite(
		context.Background(),
		workspace.ID,
		memberID,
		"forbidden-"+suffix+"@example.com",
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("Member Invite() error = %v, want forbidden", err)
	}
	if err = service.TransferOwnership(
		context.Background(),
		workspace.ID,
		ownerID,
		memberID,
	); err != nil {
		t.Fatal(err)
	}
	if err = service.ChangeRole(
		context.Background(),
		workspace.ID,
		memberID,
		memberID,
		RoleMember,
	); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last Owner demotion error = %v, want last owner", err)
	}
	if err = service.RemoveMember(
		context.Background(),
		workspace.ID,
		memberID,
		ownerID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.Role(
		context.Background(),
		workspace.ID,
		ownerID,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("removed Member Role() error = %v, want forbidden", err)
	}

	raceOwnerA := "integration-race-owner-a-" + suffix
	raceOwnerB := "integration-race-owner-b-" + suffix
	raceWorkspace := createPersonal(t, service, raceOwnerA)
	inviteAndAccept(
		t,
		service,
		raceWorkspace.ID,
		raceOwnerA,
		raceOwnerB,
		"race-"+suffix+"@example.com",
	)
	if err = service.ChangeRole(
		context.Background(),
		raceWorkspace.ID,
		raceOwnerA,
		raceOwnerB,
		RoleOwner,
	); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	roleResults := make(chan error, 2)
	wait = sync.WaitGroup{}
	for _, change := range [][2]string{
		{raceOwnerA, raceOwnerB},
		{raceOwnerB, raceOwnerA},
	} {
		wait.Add(1)
		go func(actorID, targetID string) {
			defer wait.Done()
			<-start
			roleResults <- service.ChangeRole(
				context.Background(),
				raceWorkspace.ID,
				actorID,
				targetID,
				RoleMember,
			)
		}(change[0], change[1])
	}
	close(start)
	wait.Wait()
	close(roleResults)
	successes := 0
	for result := range roleResults {
		if result == nil {
			successes++
			continue
		}
		var stateError interface{ SQLState() string }
		if !errors.Is(result, ErrForbidden) &&
			!errors.Is(result, ErrLastOwner) &&
			!(errors.As(result, &stateError) && stateError.SQLState() == "40001") {
			t.Fatalf("concurrent role change error = %v", result)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent role changes = %d, want 1", successes)
	}
	var ownerCount int
	if err = database.QueryRowContext(
		context.Background(),
		`SELECT count(*)
		 FROM f04_memberships
		 WHERE workspace_id = $1
		   AND status = 'active'
		   AND role = 'owner'`,
		raceWorkspace.ID,
	).Scan(&ownerCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 1 {
		t.Fatalf("concurrent active Owner count = %d, want 1", ownerCount)
	}

	var emailDigestLength, tokenDigestLength int
	if err = database.QueryRowContext(
		context.Background(),
		`SELECT octet_length(email_digest), octet_length(token_digest)
		 FROM f04_invitations
		 WHERE id = $1`,
		invitation.Invitation.ID,
	).Scan(&emailDigestLength, &tokenDigestLength); err != nil {
		t.Fatal(err)
	}
	if emailDigestLength != sha256DigestSize || tokenDigestLength != sha256DigestSize {
		t.Fatalf(
			"digest lengths = %d/%d, want %d/%d",
			emailDigestLength,
			tokenDigestLength,
			sha256DigestSize,
			sha256DigestSize,
		)
	}
}

const sha256DigestSize = 32
