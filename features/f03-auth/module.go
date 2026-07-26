package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	adminBootstrapEmailEnv = "POSTQRON_ADMIN_BOOTSTRAP_EMAIL"
	adminPasswordHashEnv   = "POSTQRON_ADMIN_PASSWORD_HASH_B64"
	authAllowedOriginsEnv  = "POSTQRON_AUTH_ALLOWED_ORIGINS"
)

type Module struct {
	database       *sql.DB
	handler        http.Handler
	clock          func() time.Time
	bootstrapEmail string
	bootstrapHash  string
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("auth database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	store, err := NewPostgresPasswordStore(database)
	if err != nil {
		return nil, err
	}
	service, err := NewPasswordService(store, clock, 0)
	if err != nil {
		return nil, err
	}
	handler, err := NewPasswordHandler(
		service,
		os.Getenv(authAllowedOriginsEnv),
	)
	if err != nil {
		return nil, err
	}
	module := &Module{
		database:       database,
		handler:        handler,
		clock:          clock,
		bootstrapEmail: strings.TrimSpace(os.Getenv(adminBootstrapEmailEnv)),
	}
	if encoded := strings.TrimSpace(os.Getenv(adminPasswordHashEnv)); encoded != "" {
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return nil, errors.New("admin password hash secret is not valid base64")
		}
		module.bootstrapHash = string(decoded)
	}
	if (module.bootstrapEmail == "") != (module.bootstrapHash == "") {
		return nil, errors.New("admin bootstrap email and password hash must be configured together")
	}
	return module, nil
}

func (module *Module) Start(ctx context.Context) error {
	if module == nil || module.database == nil || module.handler == nil {
		return errors.New("auth module is not configured")
	}
	if module.bootstrapEmail == "" {
		return nil
	}
	return BootstrapPasswordAccount(
		ctx,
		module.database,
		module.bootstrapEmail,
		module.bootstrapHash,
		module.clock().UTC(),
	)
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("auth database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil || name != "Password" {
		return nil, false
	}
	return module.handler, true
}
