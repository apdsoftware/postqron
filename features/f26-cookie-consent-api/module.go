package cookieconsent

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

type Module struct {
	database *sql.DB
	policies PolicySource
	handler  *HTTPHandler
	clock    func() time.Time
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	repository, err := NewPostgresRepository(database)
	if err != nil {
		return nil, err
	}
	policies, err := NewPostgresPolicySource(database)
	if err != nil {
		return nil, err
	}
	resolver, err := NewRequestSubjectResolver(database, clock)
	if err != nil {
		return nil, err
	}
	service, err := NewService(repository, policies, clock)
	if err != nil {
		return nil, err
	}
	handler, err := NewHTTPHandler(service, resolver)
	if err != nil {
		return nil, err
	}
	return &Module{
		database: database,
		policies: policies,
		handler:  handler,
		clock:    clock,
	}, nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.database == nil || module.handler == nil {
		return errors.New("cookie consent module is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("cookie consent database is unavailable")
	}
	if err := module.database.PingContext(ctx); err != nil {
		return err
	}
	_, err := module.policies.Current(ctx, module.clock().UTC())
	return err
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil {
		return nil, false
	}
	switch name {
	case "CookiePreferences", "CookiePreferencesExport":
		return module.handler, true
	default:
		return nil, false
	}
}
