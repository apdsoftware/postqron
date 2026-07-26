package entitlements

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// Module exposes the public catalog through the API feature runtime.
//
// The catalog is static application configuration, so serving it does not
// depend on PostgreSQL or on the billing provider being available.
type Module struct {
	publicPlans http.Handler
}

func NewPostgresModule(
	_ *sql.DB,
	_ func() time.Time,
) (*Module, error) {
	handler := &HTTPHandler{}
	return &Module{
		publicPlans: http.HandlerFunc(handler.plans),
	}, nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.publicPlans == nil {
		return errors.New("entitlements module is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(context.Context) error {
	if module == nil || module.publicPlans == nil {
		return errors.New("entitlements catalog is unavailable")
	}
	return nil
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.publicPlans == nil || name != "PublicPlans" {
		return nil, false
	}
	return module.publicPlans, true
}
