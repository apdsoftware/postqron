package adminconsole

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	DefaultDirectoryPageSize = 25
	MaxDirectoryPageSize     = 100
	AdminExportLimit         = 10_000
)

var (
	ErrAdminNotFound       = errors.New("admin directory item not found")
	ErrAdminExportTooLarge = errors.New("admin export limit exceeded")
	adminSubjectIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

var directoryPageSizes = []int{10, 25, 50, 100}

type UserWorkspaceMembership struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	PlanCode   string `json:"plan_code"`
	PlanStatus string `json:"plan_status"`
}

type UserDirectoryItem struct {
	ID             string                    `json:"id"`
	Email          string                    `json:"email"`
	DisplayName    string                    `json:"display_name"`
	AccountStatus  string                    `json:"account_status"`
	EmailVerified  bool                      `json:"email_verified"`
	LoginMethods   []string                  `json:"login_methods"`
	RegisteredAt   time.Time                 `json:"registered_at"`
	LastLoginAt    *time.Time                `json:"last_login_at"`
	ActiveSessions int                       `json:"active_sessions"`
	Workspaces     []UserWorkspaceMembership `json:"workspaces"`
}

type UserDirectoryQuery struct {
	Search         string
	Status         string
	EmailVerified  *bool
	Plan           string
	LoginMethod    string
	RegisteredFrom *time.Time
	RegisteredTo   *time.Time
	LastLoginFrom  *time.Time
	LastLoginTo    *time.Time
	Page           int
	PageSize       int
	Sort           string
	Direction      string
}

type UserDirectoryPage struct {
	Items     []UserDirectoryItem `json:"items"`
	Page      int                 `json:"page"`
	PageSize  int                 `json:"page_size"`
	Total     int                 `json:"total"`
	Sort      string              `json:"sort"`
	Direction string              `json:"direction"`
}

type WorkspaceDirectoryItem struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	OwnerID          string    `json:"owner_id"`
	OwnerEmail       string    `json:"owner_email"`
	OwnerDisplayName string    `json:"owner_display_name"`
	Status           string    `json:"status"`
	PlanCode         string    `json:"plan_code"`
	PlanStatus       string    `json:"plan_status"`
	MemberCount      int       `json:"member_count"`
	ChannelCount     int       `json:"channel_count"`
	PostCount        int       `json:"post_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type WorkspaceDirectoryQuery struct {
	Search      string
	Status      string
	Plan        string
	Owner       string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	UpdatedFrom *time.Time
	UpdatedTo   *time.Time
	Page        int
	PageSize    int
	Sort        string
	Direction   string
}

type WorkspaceDirectoryPage struct {
	Items     []WorkspaceDirectoryItem `json:"items"`
	Page      int                      `json:"page"`
	PageSize  int                      `json:"page_size"`
	Total     int                      `json:"total"`
	Sort      string                   `json:"sort"`
	Direction string                   `json:"direction"`
}

func (service *Service) ListUsers(
	ctx context.Context,
	query UserDirectoryQuery,
) (UserDirectoryPage, error) {
	normalized, err := normalizeUserDirectoryQuery(query)
	if err != nil {
		return UserDirectoryPage{}, err
	}
	result, err := service.reader.ListUsers(ctx, normalized)
	if err != nil {
		return UserDirectoryPage{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	if result.Items == nil {
		result.Items = []UserDirectoryItem{}
	}
	result.Page = normalized.Page
	result.PageSize = normalized.PageSize
	result.Sort = normalized.Sort
	result.Direction = normalized.Direction
	return result, nil
}

func (service *Service) User(
	ctx context.Context,
	accountID string,
) (UserDirectoryItem, error) {
	if !adminSubjectIDPattern.MatchString(accountID) {
		return UserDirectoryItem{}, ErrInvalidRequest
	}
	result, exists, err := service.reader.User(ctx, accountID)
	if err != nil {
		return UserDirectoryItem{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	if !exists {
		return UserDirectoryItem{}, ErrAdminNotFound
	}
	if result.LoginMethods == nil {
		result.LoginMethods = []string{}
	}
	if result.Workspaces == nil {
		result.Workspaces = []UserWorkspaceMembership{}
	}
	return result, nil
}

func (service *Service) ListWorkspaces(
	ctx context.Context,
	query WorkspaceDirectoryQuery,
) (WorkspaceDirectoryPage, error) {
	normalized, err := normalizeWorkspaceDirectoryQuery(query)
	if err != nil {
		return WorkspaceDirectoryPage{}, err
	}
	result, err := service.reader.ListWorkspaces(ctx, normalized)
	if err != nil {
		return WorkspaceDirectoryPage{}, errors.Join(ErrAdministrationUnavailable, err)
	}
	if result.Items == nil {
		result.Items = []WorkspaceDirectoryItem{}
	}
	result.Page = normalized.Page
	result.PageSize = normalized.PageSize
	result.Sort = normalized.Sort
	result.Direction = normalized.Direction
	return result, nil
}

func normalizeUserDirectoryQuery(
	query UserDirectoryQuery,
) (UserDirectoryQuery, error) {
	if err := normalizeDirectoryBase(
		&query.Search,
		&query.Page,
		&query.PageSize,
		&query.Direction,
	); err != nil {
		return UserDirectoryQuery{}, err
	}
	if query.Sort == "" {
		query.Sort = "registered_at"
	}
	if !contains([]string{
		"email", "display_name", "status", "email_verified",
		"registered_at", "last_login_at", "active_sessions",
	}, query.Sort) ||
		!contains([]string{"", "active", "locked"}, query.Status) ||
		!contains([]string{"", "start", "pro", "team", "internal"}, query.Plan) ||
		!contains([]string{
			"", "password", "google", "apple", "facebook", "linkedin",
		}, query.LoginMethod) ||
		!validRange(query.RegisteredFrom, query.RegisteredTo) ||
		!validRange(query.LastLoginFrom, query.LastLoginTo) {
		return UserDirectoryQuery{}, ErrInvalidRequest
	}
	return query, nil
}

func normalizeWorkspaceDirectoryQuery(
	query WorkspaceDirectoryQuery,
) (WorkspaceDirectoryQuery, error) {
	if err := normalizeDirectoryBase(
		&query.Search,
		&query.Page,
		&query.PageSize,
		&query.Direction,
	); err != nil {
		return WorkspaceDirectoryQuery{}, err
	}
	query.Owner = strings.TrimSpace(query.Owner)
	if len(query.Owner) > 120 {
		return WorkspaceDirectoryQuery{}, ErrInvalidRequest
	}
	if query.Sort == "" {
		query.Sort = "updated_at"
	}
	if !contains([]string{
		"name", "owner_email", "status", "plan_code", "member_count",
		"channel_count", "post_count", "created_at", "updated_at",
	}, query.Sort) ||
		!contains([]string{"", "active", "deletion_pending"}, query.Status) ||
		!contains([]string{"", "start", "pro", "team", "internal"}, query.Plan) ||
		!validRange(query.CreatedFrom, query.CreatedTo) ||
		!validRange(query.UpdatedFrom, query.UpdatedTo) {
		return WorkspaceDirectoryQuery{}, ErrInvalidRequest
	}
	return query, nil
}

func normalizeDirectoryBase(
	search *string,
	page, pageSize *int,
	direction *string,
) error {
	*search = strings.TrimSpace(*search)
	if len(*search) == 1 || len(*search) > 120 {
		return ErrInvalidRequest
	}
	if *page == 0 {
		*page = 1
	}
	if *page < 1 || *page > 1_000_000 {
		return ErrInvalidRequest
	}
	if *pageSize == 0 {
		*pageSize = DefaultDirectoryPageSize
	}
	if !slices.Contains(directoryPageSizes, *pageSize) {
		return ErrInvalidRequest
	}
	if *direction == "" {
		*direction = "desc"
	}
	if *direction != "asc" && *direction != "desc" {
		return ErrInvalidRequest
	}
	return nil
}

func validRange(from, to *time.Time) bool {
	return from == nil || to == nil || from.Before(*to)
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}
