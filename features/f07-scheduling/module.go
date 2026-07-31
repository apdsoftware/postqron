package scheduling

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	composer "github.com/apdsoftware/postqron/features/f06-composer"
	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

const (
	schedulingRuntimeHandlerName   = "SchedulingRuntime"
	schedulingPreflightHandlerName = "SchedulingPreflight"
)

type Module struct {
	database   *sql.DB
	clock      func() time.Time
	repository *PostgresRepository
	service    *Service
	handler    http.Handler
	preflight  http.Handler
	origins    map[string]struct{}
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("scheduling database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	repository, err := NewPostgresRepository(database)
	if err != nil {
		return nil, err
	}
	return &Module{
		database:   database,
		clock:      clock,
		repository: repository,
	}, nil
}

func (module *Module) Configure(values map[string]string) error {
	if module == nil || module.database == nil || module.repository == nil {
		return errors.New("scheduling runtime is not configured")
	}
	origins, err := parseSchedulingAllowedOrigins(
		strings.TrimSpace(os.Getenv("POSTQRON_AUTH_ALLOWED_ORIGINS")),
	)
	if err != nil {
		return err
	}
	content := schedulingBoundaryGateway(module.database, module.clock, values)
	service, err := NewService(
		module.repository,
		postgresSchedulingAuthorizer{database: module.database},
		content,
		WithClock(module.clock),
	)
	if err != nil {
		return err
	}
	handler := newSchedulingHTTPHandler(
		service,
		runtimeSchedulingAuthenticator{},
		origins,
	)
	module.service = service
	module.handler = handler
	module.preflight = credentialedSchedulingCORS(handler, origins)
	module.origins = origins
	return nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.database == nil ||
		module.repository == nil || module.service == nil ||
		module.handler == nil || module.preflight == nil {
		return errors.New("scheduling runtime is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("scheduling database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil {
		return nil, false
	}
	switch name {
	case schedulingRuntimeHandlerName:
		return module.handler, module.handler != nil
	case schedulingPreflightHandlerName:
		return module.preflight, module.preflight != nil
	default:
		return nil, false
	}
}

func (module *Module) WrapAuthenticatedRoute(
	handlerName string,
	next http.Handler,
) http.Handler {
	if module == nil || handlerName != schedulingRuntimeHandlerName {
		return next
	}
	return credentialedSchedulingCORS(next, module.origins)
}

type runtimeSchedulingAuthenticator struct{}

func (runtimeSchedulingAuthenticator) AccountID(
	request *http.Request,
) (string, bool) {
	return featureruntime.AuthenticatedAccount(request.Context())
}

type postgresSchedulingAuthorizer struct {
	database *sql.DB
}

func (authorizer postgresSchedulingAuthorizer) CanManageScheduling(
	ctx context.Context,
	workspaceID, actorID string,
) (bool, error) {
	var allowed bool
	err := authorizer.database.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM f04_memberships membership
			  JOIN f04_workspaces workspace
			    ON workspace.id = membership.workspace_id
			  JOIN f04_workspace_selections selection
			    ON selection.account_id = membership.account_id
			   AND selection.workspace_id = membership.workspace_id
			 WHERE membership.workspace_id = $1
			   AND membership.account_id = $2
			   AND membership.status = 'active'
			   AND membership.role::text IN ('owner', 'member')
			   AND workspace.status = 'active'
		)`,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(actorID),
	).Scan(&allowed)
	if err != nil {
		return false, err
	}
	return allowed, nil
}

type unavailableContentGateway struct{}

func (unavailableContentGateway) ValidateForScheduling(
	context.Context,
	string,
	string,
	string,
	[]string,
) (ValidatedDraft, error) {
	return ValidatedDraft{}, ErrDependencyUnavailable
}

func (unavailableContentGateway) DuplicateDraft(
	context.Context,
	string,
	string,
	string,
	int64,
	string,
) (DuplicatedDraft, error) {
	return DuplicatedDraft{}, ErrDependencyUnavailable
}

func schedulingBoundaryGateway(
	database *sql.DB,
	clock func() time.Time,
	values map[string]string,
) ContentGateway {
	composerModule, err := composer.NewPostgresModule(database, clock)
	if err != nil {
		return unavailableContentGateway{}
	}
	if err := composerModule.Configure(values); err != nil {
		return unavailableContentGateway{}
	}
	boundary, ok := composerModule.SchedulingBoundary()
	if !ok {
		return unavailableContentGateway{}
	}
	return newComposerSchedulingGateway(boundary)
}
