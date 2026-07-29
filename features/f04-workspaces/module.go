package workspaces

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	sessionCookieName       = "__Host-postqron_session"
	authAllowedOriginsEnv   = "POSTQRON_AUTH_ALLOWED_ORIGINS"
	appRuntimeHandlerName   = "AppRuntime"
	defaultAccountAppLocale = LocaleEN
)

type Module struct {
	database *sql.DB
	service  *RuntimeService
	handler  http.Handler
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("workspace database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	repository, err := NewPostgresRepository(database)
	if err != nil {
		return nil, err
	}
	service, err := NewRuntimeServiceWithClock(repository, clock)
	if err != nil {
		return nil, err
	}
	origins, err := parseAppAllowedOrigins(os.Getenv(authAllowedOriginsEnv))
	if err != nil {
		return nil, err
	}
	handler, err := NewRuntimeHTTPHandler(
		service,
		postgresSessionAuthenticator{database: database, clock: clock},
		origins...,
	)
	if err != nil {
		return nil, err
	}
	return &Module{
		database: database,
		service:  service,
		handler:  handler,
	}, nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.database == nil || module.service == nil || module.handler == nil {
		return errors.New("workspace runtime module is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("workspace runtime database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil || name != appRuntimeHandlerName {
		return nil, false
	}
	return module.handler, true
}

type SessionAuthenticator interface {
	Session(context.Context, string) (AppSessionAccount, error)
}

type postgresSessionAuthenticator struct {
	database *sql.DB
	clock    func() time.Time
}

func (authenticator postgresSessionAuthenticator) Session(
	ctx context.Context,
	token string,
) (AppSessionAccount, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return AppSessionAccount{}, ErrUnauthenticated
	}
	digest := sha256.Sum256([]byte(token))
	var account AppSessionAccount
	account.Locale = defaultAccountAppLocale
	err := authenticator.database.QueryRowContext(
		ctx,
		`SELECT account.id, account.display_name, account.normalized_email, account.contract_country
		 FROM auth_sessions session
		 JOIN auth_accounts account ON account.id = session.account_id
		 WHERE session.token_hash = $1
		   AND session.revoked_at IS NULL
		   AND session.expires_at > $2
		   AND account.email_verified_at IS NOT NULL`,
		hex.EncodeToString(digest[:]),
		authenticator.clock().UTC(),
	).Scan(
		&account.ID,
		&account.DisplayName,
		&account.Email,
		&account.ContractCountry,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AppSessionAccount{}, ErrUnauthenticated
	}
	if err != nil {
		return AppSessionAccount{}, err
	}
	return normalizeSessionAccount(account), nil
}
