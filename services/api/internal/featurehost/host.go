package featurehost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"sync"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

type Clock func() time.Time

type Dependencies struct {
	// Database is retained for metadata-only hosts created by older callers.
	// Executable server factories must use the typed PostgreSQL dependency.
	Database   any
	PostgreSQL *sql.DB
	Config     map[string]string
	Logger     *slog.Logger
	Clock      Clock
}

func (dependencies Dependencies) validate() error {
	if dependencies.Database == nil && dependencies.PostgreSQL == nil {
		return errors.New("feature database dependency is required")
	}
	if dependencies.Config == nil {
		return errors.New("feature configuration dependency is required")
	}
	if dependencies.Logger == nil {
		return errors.New("feature logger dependency is required")
	}
	if dependencies.Clock == nil {
		return errors.New("feature clock dependency is required")
	}
	return nil
}

type Module interface {
	Start(context.Context) error
	Stop(context.Context) error
	Ready(context.Context) error
	Handler(string) (http.Handler, bool)
}

type Factory func(
	context.Context,
	featureruntime.Feature,
	Dependencies,
) (Module, error)

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

func (registry *Registry) Register(featureID string, factory Factory) error {
	if featureID == "" {
		return errors.New("feature factory id is required")
	}
	if factory == nil {
		return fmt.Errorf("feature factory for %q is nil", featureID)
	}
	if _, exists := registry.factories[featureID]; exists {
		return fmt.Errorf("duplicate feature factory for %q", featureID)
	}
	registry.factories[featureID] = factory
	return nil
}

func (registry *Registry) load(
	ctx context.Context,
	feature featureruntime.Feature,
	dependencies Dependencies,
) (Module, error) {
	entrypoint := feature.Manifest.ServerEntrypoint()
	if factory, ok := registry.factories[feature.Manifest.ID]; ok {
		module, err := factory(ctx, feature, dependencies)
		if err != nil {
			return nil, fmt.Errorf("instantiate feature %q: %w", feature.Manifest.ID, err)
		}
		if module == nil {
			return nil, fmt.Errorf("instantiate feature %q: factory returned a nil module", feature.Manifest.ID)
		}
		return module, nil
	}
	if feature.Manifest.Entrypoints.Server != "" ||
		len(feature.Manifest.Server.Routes) > 0 {
		return nil, fmt.Errorf(
			"instantiate feature %q: no factory registered for server entrypoint %q",
			feature.Manifest.ID,
			entrypoint,
		)
	}
	return metadataModule{}, nil
}

type MigrationManager interface {
	Apply(context.Context, featureruntime.Feature, *sql.DB) error
	Ready(context.Context, featureruntime.Feature, *sql.DB) error
}

type ValidatedMigrations struct{}

func (ValidatedMigrations) Apply(
	context.Context,
	featureruntime.Feature,
	*sql.DB,
) error {
	return nil
}

func (ValidatedMigrations) Ready(
	context.Context,
	featureruntime.Feature,
	*sql.DB,
) error {
	return nil
}

type State string

const (
	StateDiscovered State = "discovered"
	StateActive     State = "active"
	StateError      State = "error"
	StateStopped    State = "stopped"
)

type Status struct {
	ID      string
	Kind    string
	Version string
	State   State
	Error   string
}

type hostedFeature struct {
	feature featureruntime.Feature
	module  Module
	status  Status
}

type Host struct {
	dependencies  Dependencies
	features      []*hostedFeature
	migrations    MigrationManager
	privateMux    *http.ServeMux
	privateRoutes []string
	publicMux     *http.ServeMux

	mu      sync.RWMutex
	started bool
}

func New(
	features []featureruntime.Feature,
	registry *Registry,
	dependencies Dependencies,
	migrations MigrationManager,
) (*Host, error) {
	if err := dependencies.validate(); err != nil {
		return nil, err
	}
	if registry == nil {
		registry = NewRegistry()
	}
	if migrations == nil {
		migrations = ValidatedMigrations{}
	}
	if err := validateFeatures(features); err != nil {
		return nil, err
	}

	host := &Host{
		dependencies: dependencies,
		migrations:   migrations,
		privateMux:   http.NewServeMux(),
		publicMux:    http.NewServeMux(),
	}
	for _, feature := range features {
		hosted := &hostedFeature{
			feature: feature,
			status: Status{
				ID:      feature.Manifest.ID,
				Kind:    feature.Manifest.Kind,
				Version: feature.Manifest.Version,
				State:   StateDiscovered,
			},
		}
		host.features = append(host.features, hosted)
	}

	for _, hosted := range host.features {
		module, err := registry.load(context.Background(), hosted.feature, dependencies)
		if err != nil {
			host.setError(hosted, err)
			continue
		}
		hosted.module = module
	}
	return host, nil
}

func validateFeatures(features []featureruntime.Feature) error {
	ids := make(map[string]struct{}, len(features))
	routes := map[string]string{
		"GET /api/v1/features": "runtime feature catalog",
	}
	patterns := http.NewServeMux()
	patterns.Handle("GET /api/v1/features", http.NotFoundHandler())
	for _, feature := range features {
		id := feature.Manifest.ID
		if _, duplicate := ids[id]; duplicate {
			return fmt.Errorf("duplicate feature id %q blocks API startup", id)
		}
		ids[id] = struct{}{}
		for _, route := range feature.Manifest.Server.Routes {
			for _, method := range route.Methods {
				key := method + " " + path.Clean("/api/v1"+route.Path)
				if owner, collision := routes[key]; collision {
					return fmt.Errorf(
						"route collision %q between features %q and %q blocks API startup",
						key,
						owner,
						id,
					)
				}
				if err := validateServeMuxPattern(patterns, key, id); err != nil {
					return err
				}
				routes[key] = id
			}
		}
	}
	return nil
}

func validateServeMuxPattern(mux *http.ServeMux, pattern, featureID string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf(
				"route collision for %q in feature %q blocks API startup: %v",
				pattern,
				featureID,
				recovered,
			)
		}
	}()
	mux.Handle(pattern, http.NotFoundHandler())
	return nil
}

func (host *Host) Start(ctx context.Context) error {
	host.mu.Lock()
	if host.started {
		host.mu.Unlock()
		return errors.New("feature host is already started")
	}
	host.started = true
	host.mu.Unlock()

	for _, hosted := range host.features {
		if hosted.module == nil {
			continue
		}
		if err := host.migrations.Apply(
			ctx,
			hosted.feature,
			host.dependencies.PostgreSQL,
		); err != nil {
			host.setError(hosted, fmt.Errorf("apply migrations: %w", err))
			continue
		}
		if err := hosted.module.Start(ctx); err != nil {
			host.setError(hosted, fmt.Errorf("start module: %w", err))
			continue
		}
		if err := host.mount(hosted); err != nil {
			_ = hosted.module.Stop(ctx)
			host.setError(hosted, err)
			continue
		}
		host.mu.Lock()
		hosted.status.State = StateActive
		hosted.status.Error = ""
		host.mu.Unlock()
	}
	return nil
}

func (host *Host) mount(hosted *hostedFeature) error {
	for _, route := range hosted.feature.Manifest.Server.Routes {
		handler, ok := hosted.module.Handler(route.Handler)
		if !ok || handler == nil {
			return fmt.Errorf(
				"mount feature %q: handler %q is not provided",
				hosted.feature.Manifest.ID,
				route.Handler,
			)
		}
		target := host.publicMux
		if route.Visibility == "private" {
			target = host.privateMux
		}
		for _, method := range route.Methods {
			pattern := method + " " + path.Clean("/api/v1"+route.Path)
			target.Handle(pattern, handler)
			if route.Visibility == "private" {
				host.privateRoutes = append(host.privateRoutes, pattern)
			}
		}
	}
	return nil
}

func (host *Host) Stop(ctx context.Context) error {
	host.mu.RLock()
	started := host.started
	host.mu.RUnlock()
	if !started {
		return nil
	}

	var failures []error
	for _, hosted := range slices.Backward(host.features) {
		host.mu.RLock()
		active := hosted.status.State == StateActive
		host.mu.RUnlock()
		if !active || hosted.module == nil {
			continue
		}
		if err := hosted.module.Stop(ctx); err != nil {
			failures = append(
				failures,
				fmt.Errorf("stop feature %q: %w", hosted.feature.Manifest.ID, err),
			)
			continue
		}
		host.mu.Lock()
		hosted.status.State = StateStopped
		host.mu.Unlock()
	}
	return errors.Join(failures...)
}

func (host *Host) Ready(ctx context.Context) error {
	host.mu.RLock()
	if !host.started {
		host.mu.RUnlock()
		return errors.New("feature host has not started")
	}
	features := slices.Clone(host.features)
	host.mu.RUnlock()

	var failures []error
	for _, hosted := range features {
		if !hosted.feature.Manifest.IsRequired() {
			continue
		}
		host.mu.RLock()
		status := hosted.status
		host.mu.RUnlock()
		if status.State != StateActive {
			detail := status.Error
			if detail == "" {
				detail = string(status.State)
			}
			failures = append(failures, fmt.Errorf("required feature %q: %s", status.ID, detail))
			continue
		}
		if err := host.migrations.Ready(
			ctx,
			hosted.feature,
			host.dependencies.PostgreSQL,
		); err != nil {
			failures = append(
				failures,
				fmt.Errorf("required feature %q migrations: %w", status.ID, err),
			)
		}
		if err := hosted.module.Ready(ctx); err != nil {
			failures = append(
				failures,
				fmt.Errorf("required feature %q readiness: %w", status.ID, err),
			)
		}
	}
	return errors.Join(failures...)
}

func (host *Host) Statuses() []Status {
	host.mu.RLock()
	defer host.mu.RUnlock()
	statuses := make([]Status, 0, len(host.features))
	for _, hosted := range host.features {
		statuses = append(statuses, hosted.status)
	}
	return statuses
}

func (host *Host) PublicHandler() http.Handler {
	return host.publicMux
}

func (host *Host) AuthenticatedHandler(
	authenticate func(http.Handler) http.Handler,
) (http.Handler, error) {
	if authenticate == nil {
		return nil, errors.New("private feature routes require explicit authentication middleware")
	}
	return authenticate(host.privateMux), nil
}

func (host *Host) MountAuthenticatedRoutes(
	mux *http.ServeMux,
	authenticate func(http.Handler) http.Handler,
) error {
	if mux == nil {
		return errors.New("private feature routes require an explicit server mux")
	}
	handler, err := host.AuthenticatedHandler(authenticate)
	if err != nil {
		return err
	}
	host.mu.RLock()
	patterns := slices.Clone(host.privateRoutes)
	host.mu.RUnlock()
	for _, pattern := range patterns {
		if err := mountAuthenticatedPattern(mux, pattern, handler); err != nil {
			return err
		}
	}
	return nil
}

func mountAuthenticatedPattern(
	mux *http.ServeMux,
	pattern string,
	handler http.Handler,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf(
				"mount authenticated private route %q: %v",
				pattern,
				recovered,
			)
		}
	}()
	mux.Handle(pattern, handler)
	return nil
}

func (host *Host) setError(hosted *hostedFeature, err error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	hosted.status.State = StateError
	hosted.status.Error = err.Error()
}

type metadataModule struct{}

func (metadataModule) Start(context.Context) error { return nil }
func (metadataModule) Stop(context.Context) error  { return nil }
func (metadataModule) Ready(context.Context) error { return nil }
func (metadataModule) Handler(string) (http.Handler, bool) {
	return nil, false
}
