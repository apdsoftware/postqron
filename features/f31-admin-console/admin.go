// Package adminconsole implements the server-side boundary for Postqron's
// protected administration console.
package adminconsole

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	FeatureID             = "admin-console"
	AdminAllowlistEnvName = "POSTQRON_ADMIN_ALLOWLIST"
	defaultReauthLimit    = 5 * time.Minute
)

var (
	ErrUnauthenticated           = errors.New("admin authentication required")
	ErrForbidden                 = errors.New("admin access forbidden")
	ErrInvalidRequest            = errors.New("invalid admin request")
	ErrCSRF                      = errors.New("csrf validation failed")
	ErrRecentReauthRequired      = errors.New("recent re-authentication required")
	ErrIdempotencyKeyRequired    = errors.New("idempotency key required")
	ErrAuditUnavailable          = errors.New("admin audit unavailable")
	ErrAdministrationUnavailable = errors.New("administration unavailable")
)

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{7,127}$`)

type Session struct {
	AccountID       string
	Email           string
	EmailVerified   bool
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
	CSRFToken       string
}

type Principal struct {
	AccountID       string    `json:"account_id"`
	Email           string    `json:"email"`
	AuthenticatedAt time.Time `json:"authenticated_at"`
}

type ServiceHealth struct {
	Code      string    `json:"code"`
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
}

type UserSummary struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	DisplayName   string `json:"display_name"`
	EmailVerified bool   `json:"email_verified"`
}

type WorkspaceSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OwnerEmail  string `json:"owner_email"`
	MemberCount int    `json:"member_count"`
}

type EntitlementSummary struct {
	WorkspaceID string `json:"workspace_id"`
	PlanCode    string `json:"plan_code"`
	Internal    bool   `json:"internal"`
}

type AuditEvent struct {
	ID            string    `json:"id"`
	Code          string    `json:"code"`
	ActorID       string    `json:"actor_id"`
	SubjectID     string    `json:"subject_id"`
	Reason        string    `json:"reason"`
	Outcome       string    `json:"outcome"`
	CorrelationID string    `json:"correlation_id"`
	OccurredAt    time.Time `json:"occurred_at"`
}

type Dashboard struct {
	Services     []ServiceHealth      `json:"services"`
	Entitlements []EntitlementSummary `json:"entitlements"`
	RecentAudit  []AuditEvent         `json:"recent_audit"`
}

type SearchResults struct {
	Users      []UserSummary      `json:"users"`
	Workspaces []WorkspaceSummary `json:"workspaces"`
}

type AdminRecord struct {
	AccountID string
	Email     string
	Active    bool
}

type Authenticator interface {
	Session(context.Context, string) (Session, error)
}

type AdminDirectory interface {
	AccountIDByEmail(context.Context, string) (string, bool, error)
	Admin(context.Context, string) (AdminRecord, bool, error)
	ListAdmins(context.Context) ([]AdminRecord, error)
	SetAdmin(context.Context, string, bool) error
}

type Reader interface {
	Dashboard(context.Context) (Dashboard, error)
	Search(context.Context, string) (SearchResults, error)
	ListUsers(context.Context, UserDirectoryQuery) (UserDirectoryPage, error)
	User(context.Context, string) (UserDirectoryItem, bool, error)
	ListWorkspaces(context.Context, WorkspaceDirectoryQuery) (WorkspaceDirectoryPage, error)
}

type InternalPlanChange struct {
	Action         string
	ActorAccountID string
	WorkspaceID    string
	Reason         string
	CorrelationID  string
}

type InternalPlan interface {
	Change(context.Context, InternalPlanChange) error
}

type AuditWriter interface {
	Append(context.Context, AuditEvent) error
}

type MutationResult struct {
	Code          string `json:"code"`
	CorrelationID string `json:"correlation_id"`
}

type IdempotencyStore interface {
	Do(
		context.Context,
		string,
		func() (MutationResult, error),
	) (MutationResult, error)
}

type Config struct {
	Allowlist    []string
	Directory    AdminDirectory
	Reader       Reader
	InternalPlan InternalPlan
	Audit        AuditWriter
	Idempotency  IdempotencyStore
	Now          func() time.Time
	ReauthWindow time.Duration
	NewID        func() string
}

type Service struct {
	allowlist    map[string]struct{}
	directory    AdminDirectory
	reader       Reader
	internalPlan InternalPlan
	audit        AuditWriter
	idempotency  IdempotencyStore
	now          func() time.Time
	reauthWindow time.Duration
	newID        func() string
}

func ParseAllowlist(raw string) ([]string, error) {
	seen := make(map[string]struct{})
	var values []string
	for _, candidate := range strings.Split(raw, ",") {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		normalized, err := NormalizeEmail(candidate)
		if err != nil {
			return nil, fmt.Errorf("invalid admin allowlist: %w", err)
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		values = append(values, normalized)
	}
	if len(values) == 0 {
		return nil, errors.New("admin allowlist must not be empty")
	}
	slices.Sort(values)
	return values, nil
}

func AllowlistFromEnvironment(
	lookup func(string) (string, bool),
) ([]string, error) {
	if lookup == nil {
		return nil, errors.New("environment lookup is required")
	}
	raw, exists := lookup(AdminAllowlistEnvName)
	if !exists {
		return nil, fmt.Errorf("%s is required", AdminAllowlistEnvName)
	}
	return ParseAllowlist(raw)
}

func NormalizeEmail(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(normalized)
	if err != nil || address.Address != normalized || strings.ContainsAny(normalized, "\r\n") {
		return "", errors.New("email is invalid")
	}
	parts := strings.Split(normalized, "@")
	if len(parts) != 2 || parts[0] == "" || !strings.Contains(parts[1], ".") {
		return "", errors.New("email is invalid")
	}
	return normalized, nil
}

func NewService(config Config) (*Service, error) {
	if config.Directory == nil || config.Reader == nil || config.InternalPlan == nil ||
		config.Audit == nil || config.Idempotency == nil {
		return nil, errors.New("all admin service dependencies are required")
	}
	allowlist := make(map[string]struct{}, len(config.Allowlist))
	for _, email := range config.Allowlist {
		normalized, err := NormalizeEmail(email)
		if err != nil {
			return nil, err
		}
		allowlist[normalized] = struct{}{}
	}
	if len(allowlist) == 0 {
		return nil, errors.New("admin allowlist must not be empty")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.ReauthWindow == 0 {
		config.ReauthWindow = defaultReauthLimit
	}
	if config.ReauthWindow <= 0 {
		return nil, errors.New("admin re-authentication window must be positive")
	}
	if config.NewID == nil {
		return nil, errors.New("admin id generator is required")
	}
	return &Service{
		allowlist:    allowlist,
		directory:    config.Directory,
		reader:       config.Reader,
		internalPlan: config.InternalPlan,
		audit:        config.Audit,
		idempotency:  config.Idempotency,
		now:          config.Now,
		reauthWindow: config.ReauthWindow,
		newID:        config.NewID,
	}, nil
}

// BootstrapAdmins reconciles active administrators with server configuration.
// Every change is represented by a new immutable audit event.
func (service *Service) BootstrapAdmins(ctx context.Context) error {
	current, err := service.directory.ListAdmins(ctx)
	if err != nil {
		return errors.Join(ErrAdministrationUnavailable, err)
	}
	activeByEmail := make(map[string]AdminRecord)
	for _, record := range current {
		email, normalizeErr := NormalizeEmail(record.Email)
		if normalizeErr == nil && record.Active {
			activeByEmail[email] = record
		}
	}
	for email := range service.allowlist {
		if _, active := activeByEmail[email]; active {
			continue
		}
		accountID, exists, lookupErr := service.directory.AccountIDByEmail(ctx, email)
		if lookupErr != nil {
			return errors.Join(ErrAdministrationUnavailable, lookupErr)
		}
		if !exists {
			continue
		}
		if err := service.setAdmin(ctx, "server-config", accountID, email, true, "server allowlist reconciliation", service.newID()); err != nil {
			return err
		}
	}
	for email, record := range activeByEmail {
		if _, configured := service.allowlist[email]; configured {
			continue
		}
		if err := service.setAdmin(ctx, "server-config", record.AccountID, email, false, "server allowlist reconciliation", service.newID()); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) Authorize(ctx context.Context, session Session) (Principal, error) {
	now := service.now().UTC()
	if session.AccountID == "" || session.ExpiresAt.IsZero() || !session.ExpiresAt.After(now) {
		return Principal{}, ErrUnauthenticated
	}
	email, err := NormalizeEmail(session.Email)
	if err != nil || !session.EmailVerified {
		return Principal{}, ErrForbidden
	}
	if _, allowed := service.allowlist[email]; !allowed {
		return Principal{}, ErrForbidden
	}
	record, exists, err := service.directory.Admin(ctx, session.AccountID)
	if err != nil {
		return Principal{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	recordEmail, normalizeErr := NormalizeEmail(record.Email)
	if !exists || !record.Active || normalizeErr != nil || recordEmail != email {
		return Principal{}, ErrForbidden
	}
	return Principal{
		AccountID:       session.AccountID,
		Email:           email,
		AuthenticatedAt: session.AuthenticatedAt.UTC(),
	}, nil
}

func (service *Service) Dashboard(ctx context.Context) (Dashboard, error) {
	value, err := service.reader.Dashboard(ctx)
	if err != nil {
		return Dashboard{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	return value, nil
}

func (service *Service) Search(ctx context.Context, query string) (SearchResults, error) {
	query = strings.TrimSpace(query)
	if len(query) < 2 || len(query) > 120 {
		return SearchResults{}, ErrInvalidRequest
	}
	value, err := service.reader.Search(ctx, query)
	if err != nil {
		return SearchResults{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	return value, nil
}

type MutationRequest struct {
	Action         string
	SubjectID      string
	SubjectEmail   string
	Reason         string
	Confirmed      bool
	CSRFToken      string
	IdempotencyKey string
}

func (service *Service) Mutate(
	ctx context.Context,
	principal Principal,
	session Session,
	request MutationRequest,
) (MutationResult, error) {
	current, err := service.Authorize(ctx, session)
	if err != nil {
		return MutationResult{}, err
	}
	if current.AccountID != principal.AccountID || current.Email != principal.Email {
		return MutationResult{}, ErrForbidden
	}
	principal = current
	if err := service.validateMutation(principal, session, request); err != nil {
		return MutationResult{}, err
	}
	key := strings.Join([]string{
		principal.AccountID,
		request.Action,
		request.SubjectID,
		request.IdempotencyKey,
	}, ":")
	return service.idempotency.Do(ctx, key, func() (MutationResult, error) {
		correlationID := service.newID()
		switch request.Action {
		case "internal_plan.assign", "internal_plan.revoke":
			if err := service.appendAudit(ctx, AuditEvent{
				ID:            service.newID(),
				Code:          request.Action,
				ActorID:       principal.AccountID,
				SubjectID:     request.SubjectID,
				Reason:        strings.TrimSpace(request.Reason),
				Outcome:       "requested",
				CorrelationID: correlationID,
				OccurredAt:    service.now().UTC(),
			}); err != nil {
				return MutationResult{}, err
			}
			err := service.internalPlan.Change(ctx, InternalPlanChange{
				Action:         request.Action,
				ActorAccountID: principal.AccountID,
				WorkspaceID:    request.SubjectID,
				Reason:         strings.TrimSpace(request.Reason),
				CorrelationID:  correlationID,
			})
			if err != nil {
				return MutationResult{}, errors.Join(ErrAdministrationUnavailable, err)
			}
		case "admin.add", "admin.remove":
			email, err := NormalizeEmail(request.SubjectEmail)
			if err != nil {
				return MutationResult{}, ErrInvalidRequest
			}
			if _, configured := service.allowlist[email]; !configured {
				return MutationResult{}, ErrForbidden
			}
			accountEmail, exists, err := service.directory.AccountIDByEmail(ctx, email)
			if err != nil {
				return MutationResult{}, errors.Join(ErrAdministrationUnavailable, err)
			}
			if !exists || accountEmail != request.SubjectID {
				return MutationResult{}, ErrForbidden
			}
			if err := service.setAdmin(
				ctx,
				principal.AccountID,
				request.SubjectID,
				email,
				request.Action == "admin.add",
				request.Reason,
				correlationID,
			); err != nil {
				return MutationResult{}, err
			}
		default:
			return MutationResult{}, ErrInvalidRequest
		}
		return MutationResult{
			Code:          "ADMIN_MUTATION_ACCEPTED",
			CorrelationID: correlationID,
		}, nil
	})
}

func (service *Service) validateMutation(
	principal Principal,
	session Session,
	request MutationRequest,
) error {
	if principal.AccountID == "" || principal.AccountID != session.AccountID {
		return ErrForbidden
	}
	if !request.Confirmed || len(strings.TrimSpace(request.Reason)) < 8 ||
		len(strings.TrimSpace(request.Reason)) > 500 || request.SubjectID == "" {
		return ErrInvalidRequest
	}
	if !idempotencyKeyPattern.MatchString(request.IdempotencyKey) {
		return ErrIdempotencyKeyRequired
	}
	if session.CSRFToken == "" || request.CSRFToken == "" ||
		subtle.ConstantTimeCompare([]byte(session.CSRFToken), []byte(request.CSRFToken)) != 1 {
		return ErrCSRF
	}
	now := service.now().UTC()
	if session.AuthenticatedAt.IsZero() || session.AuthenticatedAt.After(now) ||
		now.Sub(session.AuthenticatedAt) > service.reauthWindow {
		return ErrRecentReauthRequired
	}
	return nil
}

func (service *Service) setAdmin(
	ctx context.Context,
	actorID, accountID, email string,
	enabled bool,
	reason, correlationID string,
) error {
	action := "admin.remove"
	if enabled {
		action = "admin.add"
	}
	event := AuditEvent{
		ID:            service.newID(),
		Code:          action,
		ActorID:       actorID,
		SubjectID:     accountID,
		Reason:        strings.TrimSpace(reason),
		Outcome:       "requested",
		CorrelationID: correlationID,
		OccurredAt:    service.now().UTC(),
	}
	if err := service.appendAudit(ctx, event); err != nil {
		return err
	}
	if err := service.directory.SetAdmin(ctx, accountID, enabled); err != nil {
		return errors.Join(ErrAdministrationUnavailable, err)
	}
	return nil
}

func (service *Service) appendAudit(ctx context.Context, event AuditEvent) error {
	if err := service.audit.Append(ctx, event); err != nil {
		return errors.Join(ErrAuditUnavailable, err)
	}
	return nil
}
