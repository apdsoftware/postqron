package adminconsole

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"time"
)

type Module struct {
	database *sql.DB
	service  *Service
	handler  http.Handler
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("admin database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	allowlist, err := AllowlistFromEnvironment(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	store := NewPostgresStore(database, clock, newSecureID)
	service, err := NewService(Config{
		Allowlist:    allowlist,
		Directory:    store,
		Reader:       store,
		InternalPlan: store,
		Audit:        store,
		Idempotency:  store,
		Now:          clock,
		NewID:        newSecureID,
	})
	if err != nil {
		return nil, err
	}
	handler, err := NewHandler(service, store)
	if err != nil {
		return nil, err
	}
	return &Module{
		database: database,
		service:  service,
		handler:  handler,
	}, nil
}

func (module *Module) Start(ctx context.Context) error {
	if module == nil || module.database == nil ||
		module.service == nil || module.handler == nil {
		return errors.New("admin module is not configured")
	}
	return module.service.BootstrapAdmins(ctx)
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("admin database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil || name != "Admin" {
		return nil, false
	}
	return module.handler, true
}

func newSecureID() string {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(value)
}
