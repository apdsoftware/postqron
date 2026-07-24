package internalplan

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	adminID     = "11111111-1111-4111-8111-111111111111"
	memberID    = "22222222-2222-4222-8222-222222222222"
	targetID    = "33333333-3333-4333-8333-333333333333"
	otherTarget = "44444444-4444-4444-8444-444444444444"
	workspaceID = "55555555-5555-4555-8555-555555555555"
)

type adminAuthorizerStub struct {
	admins map[string]bool
	err    error
}

func (authorizer *adminAuthorizerStub) IsAdmin(
	_ context.Context,
	accountID string,
) (bool, error) {
	return authorizer.admins[accountID], authorizer.err
}

type memoryRepository struct {
	allowlist map[string]bool
	binding   map[string]string
	active    map[string]bool
	audits    []AuditEvent
	auditErr  error
	applyErr  error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		allowlist: make(map[string]bool),
		binding:   make(map[string]string),
		active:    make(map[string]bool),
	}
}

func (repository *memoryRepository) Apply(
	_ context.Context,
	command ChangeCommand,
) (ChangeResult, error) {
	if repository.applyErr != nil {
		return ChangeResult{}, repository.applyErr
	}
	if command.Action == ActionAssign {
		key := command.WorkspaceID + ":" + command.TargetAccountID
		if !repository.allowlist[key] {
			repository.audits = append(
				repository.audits,
				command.audit(OutcomeDenied),
			)
			return ChangeResult{}, ErrTargetNotAllowlisted
		}
		if repository.active[command.WorkspaceID] &&
			repository.binding[command.WorkspaceID] != command.TargetAccountID {
			repository.audits = append(
				repository.audits,
				command.audit(OutcomeDenied),
			)
			return ChangeResult{}, ErrActiveBindingConflict
		}
		changed := !repository.active[command.WorkspaceID]
		repository.binding[command.WorkspaceID] = command.TargetAccountID
		repository.active[command.WorkspaceID] = true
		repository.audits = append(
			repository.audits,
			command.audit(OutcomeSucceeded),
		)
		return ChangeResult{
			AuditEventID: command.EventID,
			Changed:      changed,
			Active:       true,
			OccurredAt:   command.OccurredAt,
		}, nil
	}

	target := repository.binding[command.WorkspaceID]
	changed := repository.active[command.WorkspaceID]
	repository.active[command.WorkspaceID] = false
	command.TargetAccountID = target
	repository.audits = append(
		repository.audits,
		command.audit(OutcomeSucceeded),
	)
	return ChangeResult{
		AuditEventID: command.EventID,
		Changed:      changed,
		Active:       false,
		OccurredAt:   command.OccurredAt,
	}, nil
}

func (repository *memoryRepository) AppendAudit(
	_ context.Context,
	event AuditEvent,
) error {
	if repository.auditErr != nil {
		return repository.auditErr
	}
	repository.audits = append(repository.audits, event)
	return nil
}

func newTestService(
	t *testing.T,
	repository *memoryRepository,
	authorizer *adminAuthorizerStub,
) *Service {
	t.Helper()
	service, err := NewService(repository, authorizer)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time {
		return time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	}
	service.newID = func() (string, error) {
		return "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", nil
	}
	return service
}

func TestAssignmentRequiresStrongAdminAndServerSideAllowlist(t *testing.T) {
	tests := []struct {
		name        string
		principal   Principal
		allowlisted bool
		wantError   error
		wantOutcome Outcome
	}{
		{
			name: "weak admin session",
			principal: Principal{
				AccountID:             adminID,
				StronglyAuthenticated: false,
			},
			allowlisted: true,
			wantError:   ErrStrongAuthenticationRequired,
			wantOutcome: OutcomeDenied,
		},
		{
			name: "non admin escalation",
			principal: Principal{
				AccountID:             memberID,
				StronglyAuthenticated: true,
			},
			allowlisted: true,
			wantError:   ErrAdminRequired,
			wantOutcome: OutcomeDenied,
		},
		{
			name: "admin targets non allowlisted account",
			principal: Principal{
				AccountID:             adminID,
				StronglyAuthenticated: true,
			},
			allowlisted: false,
			wantError:   ErrTargetNotAllowlisted,
			wantOutcome: OutcomeDenied,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newMemoryRepository()
			repository.allowlist[workspaceID+":"+targetID] = test.allowlisted
			service := newTestService(t, repository, &adminAuthorizerStub{
				admins: map[string]bool{adminID: true},
			})

			_, err := service.Assign(context.Background(), test.principal, AssignmentRequest{
				WorkspaceID:     workspaceID,
				TargetAccountID: targetID,
				CorrelationID:   "case-17-escalation",
			})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("Assign error = %v, want %v", err, test.wantError)
			}
			if repository.active[workspaceID] {
				t.Fatal("denied request activated the override")
			}
			if len(repository.audits) != 1 {
				t.Fatalf("audit count = %d, want 1", len(repository.audits))
			}
			audit := repository.audits[0]
			if audit.Outcome != test.wantOutcome ||
				audit.ActorAccountID != test.principal.AccountID ||
				audit.WorkspaceID != workspaceID ||
				audit.Action != ActionAssign {
				t.Fatalf("unexpected audit: %#v", audit)
			}
		})
	}
}

func TestAssignmentAndRevocationAreAuditedAndIdempotent(t *testing.T) {
	repository := newMemoryRepository()
	repository.allowlist[workspaceID+":"+targetID] = true
	service := newTestService(t, repository, &adminAuthorizerStub{
		admins: map[string]bool{adminID: true},
	})
	principal := Principal{AccountID: adminID, StronglyAuthenticated: true}

	first, err := service.Assign(context.Background(), principal, AssignmentRequest{
		WorkspaceID:     workspaceID,
		TargetAccountID: targetID,
		CorrelationID:   "case-17-assign",
	})
	if err != nil || !first.Changed || !first.Active {
		t.Fatalf("first assignment = %#v, %v", first, err)
	}
	second, err := service.Assign(context.Background(), principal, AssignmentRequest{
		WorkspaceID:     workspaceID,
		TargetAccountID: targetID,
		CorrelationID:   "case-17-assign-retry",
	})
	if err != nil || second.Changed || !second.Active {
		t.Fatalf("replayed assignment = %#v, %v", second, err)
	}
	revoked, err := service.Revoke(context.Background(), principal, RevocationRequest{
		WorkspaceID:   workspaceID,
		CorrelationID: "case-17-revoke",
	})
	if err != nil || !revoked.Changed || revoked.Active {
		t.Fatalf("revocation = %#v, %v", revoked, err)
	}

	if len(repository.audits) != 3 {
		t.Fatalf("audit count = %d, want 3", len(repository.audits))
	}
	for index, event := range repository.audits {
		if event.Outcome != OutcomeSucceeded || event.ActorAccountID != adminID {
			t.Fatalf("audit[%d] = %#v", index, event)
		}
	}
	if repository.audits[2].Action != ActionRevoke ||
		repository.audits[2].TargetAccountID != targetID {
		t.Fatalf("revocation audit = %#v", repository.audits[2])
	}
}

func TestAuditFailureIsFailClosed(t *testing.T) {
	repository := newMemoryRepository()
	repository.allowlist[workspaceID+":"+targetID] = true
	repository.auditErr = errors.New("audit offline")
	service := newTestService(t, repository, &adminAuthorizerStub{
		admins: map[string]bool{adminID: true},
	})

	_, err := service.Assign(context.Background(), Principal{
		AccountID:             memberID,
		StronglyAuthenticated: true,
	}, AssignmentRequest{
		WorkspaceID:     workspaceID,
		TargetAccountID: targetID,
		CorrelationID:   "case-17-audit-failure",
	})
	if !errors.Is(err, ErrAuditUnavailable) {
		t.Fatalf("Assign error = %v, want audit unavailable", err)
	}
	if repository.active[workspaceID] {
		t.Fatal("audit failure activated the override")
	}
}

func TestActiveBindingCannotBeRetargetedByAlteredPayload(t *testing.T) {
	repository := newMemoryRepository()
	repository.allowlist[workspaceID+":"+targetID] = true
	repository.allowlist[workspaceID+":"+otherTarget] = true
	service := newTestService(t, repository, &adminAuthorizerStub{
		admins: map[string]bool{adminID: true},
	})
	principal := Principal{AccountID: adminID, StronglyAuthenticated: true}

	_, err := service.Assign(context.Background(), principal, AssignmentRequest{
		WorkspaceID:     workspaceID,
		TargetAccountID: targetID,
		CorrelationID:   "case-17-original",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Assign(context.Background(), principal, AssignmentRequest{
		WorkspaceID:     workspaceID,
		TargetAccountID: otherTarget,
		CorrelationID:   "case-17-altered-target",
	})
	if !errors.Is(err, ErrActiveBindingConflict) {
		t.Fatalf("retarget error = %v", err)
	}
	if repository.binding[workspaceID] != targetID {
		t.Fatalf("binding changed to %s", repository.binding[workspaceID])
	}
	if got := repository.audits[len(repository.audits)-1].Outcome; got != OutcomeDenied {
		t.Fatalf("retarget audit outcome = %s", got)
	}
}

func TestInvalidSemanticPayloadIsAuditedWithoutPayloadData(t *testing.T) {
	repository := newMemoryRepository()
	service := newTestService(t, repository, &adminAuthorizerStub{
		admins: map[string]bool{adminID: true},
	})
	_, err := service.Assign(context.Background(), Principal{
		AccountID:             adminID,
		StronglyAuthenticated: true,
	}, AssignmentRequest{
		WorkspaceID:     workspaceID,
		TargetAccountID: "not-a-uuid",
		CorrelationID:   "case-17-invalid-target",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid target error = %v", err)
	}
	if len(repository.audits) != 1 {
		t.Fatalf("audit count = %d, want 1", len(repository.audits))
	}
	audit := repository.audits[0]
	if audit.Outcome != OutcomeDenied ||
		audit.TargetAccountID != "" ||
		audit.WorkspaceID != workspaceID {
		t.Fatalf("invalid payload audit = %#v", audit)
	}
}
