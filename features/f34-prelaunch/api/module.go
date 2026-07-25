package prelaunch

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"time"
)

type Module struct {
	database *sql.DB
	handler  *HTTPHandler
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	repository, err := NewPostgresRepository(database)
	if err != nil {
		return nil, err
	}
	service, err := NewService(repository, clock)
	if err != nil {
		return nil, err
	}
	handler, err := NewHTTPHandler(
		service,
		ResolveMode(os.Getenv("PRELAUNCH_MODE"), environment()),
		NewOriginPolicy(os.Getenv("PRELAUNCH_ALLOWED_ORIGINS"), environment()),
	)
	if err != nil {
		return nil, err
	}
	return &Module{database: database, handler: handler}, nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.database == nil || module.handler == nil {
		return errors.New("prelaunch access module is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("prelaunch access database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil {
		return nil, false
	}
	switch name {
	case "AccessRequests":
		return http.HandlerFunc(module.handler.AccessRequests), true
	case "PrelaunchStatus":
		return http.HandlerFunc(module.handler.Status), true
	default:
		return nil, false
	}
}
