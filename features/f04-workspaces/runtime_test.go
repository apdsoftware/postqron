package workspaces

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newRuntimeService(
	t *testing.T,
	store RuntimeStore,
) *RuntimeService {
	t.Helper()
	service, err := NewRuntimeServiceWithClock(
		store,
		func() time.Time { return testNow },
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func seedRuntimeDocuments(repository *MemoryRepository) {
	repository.SeedLegalDocuments(
		legalDocumentRecord{
			documentKey: "terms_it",
			version:     "1.0",
			digestSHA:   strings.Repeat("a", 64),
			effectiveAt: testNow.Add(-time.Hour),
		},
		legalDocumentRecord{
			documentKey: "privacy_it",
			version:     "1.0",
			digestSHA:   strings.Repeat("b", 64),
			effectiveAt: testNow.Add(-time.Hour),
		},
	)
}

func runtimeAccount(id string) AppSessionAccount {
	return AppSessionAccount{
		ID:              id,
		DisplayName:     "Ada Lovelace",
		Email:           "ada@example.com",
		Locale:          LocaleIT,
		ContractCountry: "IT",
	}
}

func runtimeConsents() []OnboardingConsentReceipt {
	return []OnboardingConsentReceipt{
		{
			DocumentKey:   "terms",
			Version:       "1.0",
			DigestSHA256:  strings.Repeat("a", 64),
			Action:        "accepted",
			Locale:        LocaleIT,
			Purpose:       "contract",
			Surface:       "app_onboarding",
			ControlTextID: "app.consent.terms.v1",
		},
		{
			DocumentKey:   "privacy",
			Version:       "1.0",
			DigestSHA256:  strings.Repeat("b", 64),
			Action:        "accepted",
			Locale:        LocaleIT,
			Purpose:       "privacy_acknowledgement",
			Surface:       "app_onboarding",
			ControlTextID: "app.consent.privacy.v1",
		},
	}
}

func TestRuntimeCompleteOnboardingCreateIsIdempotentUnderConcurrency(t *testing.T) {
	repository := NewMemoryRepository()
	seedRuntimeDocuments(repository)
	service := newRuntimeService(t, repository)
	account := runtimeAccount("account-1")

	const attempts = 24
	results := make(chan AppSession, attempts)
	failures := make(chan error, attempts)
	var created atomic.Int32
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			session, wasCreated, err := service.CompleteOnboarding(
				context.Background(),
				CompleteOnboardingCommand{
					Account:  account,
					Consents: runtimeConsents(),
					Workspace: OnboardingWorkspaceInput{
						Mode: "create",
						Name: "Ada Studio",
					},
				},
			)
			if err != nil {
				failures <- err
				return
			}
			if wasCreated {
				created.Add(1)
			}
			results <- session
		}()
	}
	wait.Wait()
	close(results)
	close(failures)

	for err := range failures {
		t.Fatalf("CompleteOnboarding() error = %v", err)
	}
	var currentID string
	for result := range results {
		if result.OnboardingRequired {
			t.Fatal("session still requires onboarding")
		}
		if result.CurrentWorkspace == nil {
			t.Fatal("current workspace is nil")
		}
		if currentID == "" {
			currentID = result.CurrentWorkspace.ID
		}
		if result.CurrentWorkspace.ID != currentID {
			t.Fatalf("current workspace id = %q, want %q", result.CurrentWorkspace.ID, currentID)
		}
	}
	if created.Load() != 1 {
		t.Fatalf("created count = %d, want 1", created.Load())
	}
	if repository.ConsentEvidenceCount() != 2 {
		t.Fatalf("consent evidence count = %d, want 2", repository.ConsentEvidenceCount())
	}
	membership, ok := repository.Membership(currentID, account.ID)
	if !ok || membership.Role != RoleOwner || membership.Status != MembershipActive {
		t.Fatalf("owner membership = %#v, %v", membership, ok)
	}
}

func TestRuntimeSelectWorkspaceRequiresActiveMembership(t *testing.T) {
	repository := NewMemoryRepository()
	service := newRuntimeService(t, repository)
	domainService := newTestService(t, repository, 10)
	workspace := createPersonal(t, domainService, "owner")

	if err := service.SelectWorkspace(
		context.Background(),
		runtimeAccount("member"),
		workspace.ID,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("SelectWorkspace() error = %v, want forbidden", err)
	}

	inviteAndAccept(
		t,
		domainService,
		workspace.ID,
		"owner",
		"member",
		"member@example.com",
	)
	if err := service.SelectWorkspace(
		context.Background(),
		AppSessionAccount{
			ID:              "member",
			DisplayName:     "Member",
			Email:           "member@example.com",
			Locale:          LocaleEN,
			ContractCountry: "IT",
		},
		workspace.ID,
	); err != nil {
		t.Fatal(err)
	}
	if repository.selections["member"] != workspace.ID {
		t.Fatalf("selected workspace = %q, want %q", repository.selections["member"], workspace.ID)
	}
}

func TestConsumeOnboardingRequiredEventIsIdempotent(t *testing.T) {
	repository := NewMemoryRepository()
	service := newRuntimeService(t, repository)
	event := OnboardingRequiredEvent{
		AccountID:            "account-2",
		Email:                "event@example.com",
		DisplayName:          "Event User",
		ContractCountry:      "IT",
		PersonalWorkspaceKey: "personal:account-2",
		RequestedRole:        "owner",
		IdempotencyKey:       "auth-account:account-2",
		OccurredAt:           testNow,
	}

	const attempts = 20
	workspaces := make(chan Workspace, attempts)
	failures := make(chan error, attempts)
	var created atomic.Int32
	var wait sync.WaitGroup
	for range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			workspace, wasCreated, err := service.ConsumeOnboardingRequired(
				context.Background(),
				event,
			)
			if err != nil {
				failures <- err
				return
			}
			if wasCreated {
				created.Add(1)
			}
			workspaces <- workspace
		}()
	}
	wait.Wait()
	close(workspaces)
	close(failures)

	for err := range failures {
		t.Fatalf("ConsumeOnboardingRequired() error = %v", err)
	}
	var workspaceID string
	for workspace := range workspaces {
		if workspaceID == "" {
			workspaceID = workspace.ID
		}
		if workspace.ID != workspaceID {
			t.Fatalf("workspace id = %q, want %q", workspace.ID, workspaceID)
		}
	}
	if created.Load() != 1 {
		t.Fatalf("created count = %d, want 1", created.Load())
	}
	if repository.selections[event.AccountID] != workspaceID {
		t.Fatalf("selected workspace = %q, want %q", repository.selections[event.AccountID], workspaceID)
	}
}

func TestRuntimeCurrentWorkspaceFallsBackToAccessibleWorkspace(t *testing.T) {
	repository := NewMemoryRepository()
	service := newRuntimeService(t, repository)
	domainService := newTestService(t, repository, 10)

	workspace := createPersonal(t, domainService, "owner")
	repository.selections["owner"] = "missing-workspace"

	current, err := service.CurrentWorkspace(context.Background(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != workspace.ID || current.Role != RoleOwner {
		t.Fatalf("current workspace = %#v", current)
	}
}

func TestRuntimeConcurrentRoleChangesRetainAnOwner(t *testing.T) {
	repository := NewMemoryRepository()
	manager := newTestService(t, repository, 5)
	workspace := createPersonal(t, manager, "owner-a")
	inviteAndAccept(
		t,
		manager,
		workspace.ID,
		"owner-a",
		"owner-b",
		"owner-b@example.com",
	)
	if err := manager.ChangeRole(
		context.Background(),
		workspace.ID,
		"owner-a",
		"owner-b",
		RoleOwner,
	); err != nil {
		t.Fatal(err)
	}
	repository.selections["owner-a"] = workspace.ID
	repository.selections["owner-b"] = workspace.ID
	service, err := NewRuntimeServiceWithManager(
		repository,
		manager,
		func() time.Time { return testNow },
	)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, change := range [][2]string{
		{"owner-a", "owner-b"},
		{"owner-b", "owner-a"},
	} {
		wait.Add(1)
		go func(actorID, targetID string) {
			defer wait.Done()
			<-start
			results <- service.ChangeCurrentMemberRole(
				context.Background(),
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
	for result := range results {
		if result == nil {
			successes++
			continue
		}
		if !errors.Is(result, ErrForbidden) && !errors.Is(result, ErrLastOwner) {
			t.Fatalf("concurrent role change error = %v", result)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent role changes = %d, want 1", successes)
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
