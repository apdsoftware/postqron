// Package internalplan owns the private administrative boundary for Postqron's
// unlimited entitlement override. It intentionally exposes no public plan
// code, catalog entry, checkout path, or user-facing entitlement type.
package internalplan

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"time"
)

const FeatureID = "f11-internal-plan"

const redactedWorkspaceID = "00000000-0000-4000-8000-000000000000"

type Action string

const (
	ActionAssign Action = "plan.internal_assigned"
	ActionRevoke Action = "plan.internal_revoked"
)

type Outcome string

const (
	OutcomeDenied    Outcome = "denied"
	OutcomeFailed    Outcome = "failed"
	OutcomeSucceeded Outcome = "succeeded"
)

type Principal struct {
	AccountID             string
	StronglyAuthenticated bool
}

type AssignmentRequest struct {
	WorkspaceID     string
	TargetAccountID string
	CorrelationID   string
}

type RevocationRequest struct {
	WorkspaceID   string
	CorrelationID string
}

type ChangeResult struct {
	AuditEventID string    `json:"audit_event_id"`
	Changed      bool      `json:"changed"`
	Active       bool      `json:"active"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// ChangeCommand contains only identities established by trusted server-side
// authentication plus validated opaque identifiers. Repository implementations
// must enforce the assignment allowlist and append the final audit event in the
// same transaction as the entitlement override.
type ChangeCommand struct {
	EventID         string
	Action          Action
	ActorAccountID  string
	WorkspaceID     string
	TargetAccountID string
	CorrelationID   string
	OccurredAt      time.Time
}

type AuditEvent struct {
	EventID         string
	Action          Action
	Outcome         Outcome
	ActorAccountID  string
	WorkspaceID     string
	TargetAccountID string
	CorrelationID   string
	OccurredAt      time.Time
}

type Repository interface {
	Apply(context.Context, ChangeCommand) (ChangeResult, error)
	AppendAudit(context.Context, AuditEvent) error
}

type AdminAuthorizer interface {
	IsAdmin(context.Context, string) (bool, error)
}

type Service struct {
	repository Repository
	admins     AdminAuthorizer
	now        func() time.Time
	newID      func() (string, error)
}

var (
	ErrInvalidRequest               = errors.New("invalid internal plan request")
	ErrStrongAuthenticationRequired = errors.New("strong authentication required")
	ErrAdminRequired                = errors.New("administrator permission required")
	ErrAuthorizationUnavailable     = errors.New("administrator authorization unavailable")
	ErrTargetNotAllowlisted         = errors.New("target account is not allowlisted")
	ErrActiveBindingConflict        = errors.New("workspace has a different active internal binding")
	ErrAuditUnavailable             = errors.New("sensitive audit unavailable")
	ErrInternalPlanUnavailable      = errors.New("internal plan administration unavailable")
)

var (
	uuidPattern = regexp.MustCompile(
		`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`,
	)
	correlationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

func NewService(repository Repository, admins AdminAuthorizer) (*Service, error) {
	if repository == nil {
		return nil, errors.New("internal plan repository is required")
	}
	if admins == nil {
		return nil, errors.New("admin authorizer is required")
	}
	return &Service{
		repository: repository,
		admins:     admins,
		now:        time.Now,
		newID:      randomUUID,
	}, nil
}

func (service *Service) Assign(
	ctx context.Context,
	principal Principal,
	request AssignmentRequest,
) (ChangeResult, error) {
	if !validUUID(request.TargetAccountID) {
		if err := service.auditRejected(
			ctx,
			principal,
			ActionAssign,
			request.WorkspaceID,
			request.CorrelationID,
		); err != nil {
			return ChangeResult{}, err
		}
		return ChangeResult{}, ErrInvalidRequest
	}
	return service.change(ctx, principal, ChangeCommand{
		Action:          ActionAssign,
		WorkspaceID:     request.WorkspaceID,
		TargetAccountID: request.TargetAccountID,
		CorrelationID:   request.CorrelationID,
	})
}

func (service *Service) Revoke(
	ctx context.Context,
	principal Principal,
	request RevocationRequest,
) (ChangeResult, error) {
	return service.change(ctx, principal, ChangeCommand{
		Action:        ActionRevoke,
		WorkspaceID:   request.WorkspaceID,
		CorrelationID: request.CorrelationID,
	})
}

func (service *Service) change(
	ctx context.Context,
	principal Principal,
	command ChangeCommand,
) (ChangeResult, error) {
	if !validUUID(principal.AccountID) {
		return ChangeResult{}, ErrAuditUnavailable
	}
	if !validUUID(command.WorkspaceID) ||
		!correlationPattern.MatchString(command.CorrelationID) {
		if err := service.auditRejected(
			ctx,
			principal,
			command.Action,
			command.WorkspaceID,
			command.CorrelationID,
		); err != nil {
			return ChangeResult{}, err
		}
		return ChangeResult{}, ErrInvalidRequest
	}

	eventID, err := service.newID()
	if err != nil {
		return ChangeResult{}, fmt.Errorf("%w: create audit id", ErrAuditUnavailable)
	}
	command.EventID = eventID
	command.ActorAccountID = principal.AccountID
	command.OccurredAt = service.now().UTC()

	if !principal.StronglyAuthenticated {
		if err := service.appendDecision(ctx, command, OutcomeDenied); err != nil {
			return ChangeResult{}, err
		}
		return ChangeResult{}, ErrStrongAuthenticationRequired
	}

	allowed, err := service.admins.IsAdmin(ctx, principal.AccountID)
	if err != nil {
		if auditErr := service.appendDecision(ctx, command, OutcomeFailed); auditErr != nil {
			return ChangeResult{}, auditErr
		}
		return ChangeResult{}, ErrAuthorizationUnavailable
	}
	if !allowed {
		if err := service.appendDecision(ctx, command, OutcomeDenied); err != nil {
			return ChangeResult{}, err
		}
		return ChangeResult{}, ErrAdminRequired
	}

	result, err := service.repository.Apply(ctx, command)
	if err != nil {
		if errors.Is(err, ErrTargetNotAllowlisted) ||
			errors.Is(err, ErrActiveBindingConflict) {
			// Security denials are audited transactionally by Apply.
			return ChangeResult{}, err
		}
		return ChangeResult{}, fmt.Errorf("%w: %v", ErrInternalPlanUnavailable, err)
	}
	return result, nil
}

func (service *Service) appendDecision(
	ctx context.Context,
	command ChangeCommand,
	outcome Outcome,
) error {
	err := service.repository.AppendAudit(ctx, AuditEvent{
		EventID:         command.EventID,
		Action:          command.Action,
		Outcome:         outcome,
		ActorAccountID:  command.ActorAccountID,
		WorkspaceID:     command.WorkspaceID,
		TargetAccountID: command.TargetAccountID,
		CorrelationID:   command.CorrelationID,
		OccurredAt:      command.OccurredAt,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuditUnavailable, err)
	}
	return nil
}

// auditRejected records an authenticated request that the HTTP boundary could
// not decode. Payload values are deliberately excluded because they are
// untrusted and may contain personal or secret data.
func (service *Service) auditRejected(
	ctx context.Context,
	principal Principal,
	action Action,
	workspaceID string,
	correlationID string,
) error {
	if !validUUID(principal.AccountID) {
		return ErrAuditUnavailable
	}
	eventID, err := service.newID()
	if err != nil {
		return fmt.Errorf("%w: create rejected-request audit id", ErrAuditUnavailable)
	}
	if !validUUID(workspaceID) {
		workspaceID = redactedWorkspaceID
	}
	if !correlationPattern.MatchString(correlationID) {
		correlationID = eventID
	}
	err = service.repository.AppendAudit(ctx, AuditEvent{
		EventID:        eventID,
		Action:         action,
		Outcome:        OutcomeDenied,
		ActorAccountID: principal.AccountID,
		WorkspaceID:    workspaceID,
		CorrelationID:  correlationID,
		OccurredAt:     service.now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuditUnavailable, err)
	}
	return nil
}

func validUUID(value string) bool {
	return uuidPattern.MatchString(value)
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}
