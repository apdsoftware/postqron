package publishingruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
	metapublishing "github.com/apdsoftware/postqron/features/f08-publishing/providers/meta"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service is intentionally only F8 wiring. Meta adapters can be registered
// only by injecting an already configured F5 AuthenticatedExecutor. The
// default worker registry remains empty and fails closed.
type Service struct {
	engine *publishing.Engine
	pool   *pgxpool.Pool
}

type runtimeConfig struct {
	meta metapublishing.RegistrationConfig
}

type Option func(*runtimeConfig)

func WithMetaAdapters(config metapublishing.RegistrationConfig) Option {
	return func(runtime *runtimeConfig) {
		runtime.meta = config
	}
}

func New(
	ctx context.Context,
	database *sql.DB,
	databaseURL string,
	clock func() time.Time,
	options ...Option,
) (*Service, error) {
	if database == nil || strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("publishing runtime database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	pool, err := pgxpool.New(ctx, strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("configure publishing postgres pool: %w", err)
	}
	store, err := publishing.NewPostgresStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	var config runtimeConfig
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	registry, err := newRuntimeAdapterRegistry(config.meta)
	if err != nil {
		pool.Close()
		return nil, err
	}
	engine, err := publishing.NewEngine(
		store,
		postgresCommandGate{database: database},
		registry,
		postgresRetryAuthorizer{database: database},
		publishing.RetryPolicy{
			BaseDelay:   15 * time.Second,
			MaxDelay:    6 * time.Hour,
			Lease:       2 * time.Minute,
			MaxAttempts: 6,
		},
		publishing.WithNotificationResolver(registry),
		publishing.WithClock(clock),
	)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return &Service{engine: engine, pool: pool}, nil
}

func newRuntimeAdapterRegistry(
	config metapublishing.RegistrationConfig,
) (*publishing.AdapterRegistry, error) {
	registry := publishing.NewAdapterRegistry()
	if err := metapublishing.Register(registry, config); err != nil {
		return nil, err
	}
	return registry, nil
}

func (service *Service) DispatchOne(ctx context.Context) (bool, error) {
	if service == nil || service.engine == nil {
		return false, errors.New("publishing runtime is not configured")
	}
	return service.engine.DispatchOne(ctx)
}

func (service *Service) Close() {
	if service != nil && service.pool != nil {
		service.pool.Close()
	}
}

type postgresCommandGate struct {
	database *sql.DB
}

func (gate postgresCommandGate) IsCurrent(
	ctx context.Context,
	workspaceID, commandID string,
	generation int64,
) (bool, error) {
	var current bool
	err := gate.database.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM f07_publication_commands command
			  JOIN f07_scheduled_posts post
			    ON post.id = command.post_id
			   AND post.workspace_id = command.workspace_id
			 WHERE command.workspace_id = $1
			   AND command.id = $2
			   AND command.generation = $3
			   AND command.state = 'pending'
			   AND post.active_command_id = command.id
			   AND post.revision = command.generation
			   AND post.status IN ('scheduled', 'publishing')
		)`,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(commandID),
		generation,
	).Scan(&current)
	if err != nil {
		return false, fmt.Errorf("verify F7 publication command: %w", err)
	}
	return current, nil
}

type postgresRetryAuthorizer struct {
	database *sql.DB
}

func (authorizer postgresRetryAuthorizer) CanRetryPublication(
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
		return false, fmt.Errorf("authorize F8 manual retry: %w", err)
	}
	return allowed, nil
}
