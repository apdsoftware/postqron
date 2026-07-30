package workspaces

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	sessionCookieName       = "__Host-postqron_session"
	authAllowedOriginsEnv   = "POSTQRON_AUTH_ALLOWED_ORIGINS"
	authEncryptionKeyEnv    = "POSTQRON_AUTH_ENCRYPTION_KEY_B64"
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
	manager := postgresRuntimeManager{
		repository: repository,
		clock:      clock,
	}
	if emailKey := workspaceEmailDigestKeyFromEnv(); len(emailKey) >= 32 {
		manager.inviter, err = NewService(
			repository,
			postgresMemberLimits{database: database},
			emailKey,
			WithClock(clock),
		)
		if err != nil {
			return nil, err
		}
	}
	service, err := NewRuntimeServiceWithManager(repository, manager, clock)
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

type postgresRuntimeManager struct {
	repository *PostgresRepository
	inviter    *Service
	clock      func() time.Time
}

func (manager postgresRuntimeManager) RenameWorkspace(
	ctx context.Context,
	workspaceID, actorID, name string,
) (Workspace, error) {
	return manager.repository.RenameWorkspace(
		ctx,
		workspaceID,
		actorID,
		name,
		manager.clock().UTC(),
	)
}

func (manager postgresRuntimeManager) Invite(
	ctx context.Context,
	workspaceID, actorID, email string,
) (InvitationResult, error) {
	if manager.inviter == nil {
		return InvitationResult{}, ErrRuntimeUnavailable
	}
	return manager.inviter.Invite(ctx, workspaceID, actorID, email)
}

func (manager postgresRuntimeManager) ChangeRole(
	ctx context.Context,
	workspaceID, actorID, accountID string,
	role Role,
) error {
	return manager.repository.ChangeRole(
		ctx,
		workspaceID,
		actorID,
		accountID,
		role,
		manager.clock().UTC(),
	)
}

func (manager postgresRuntimeManager) RemoveMember(
	ctx context.Context,
	workspaceID, actorID, accountID string,
) error {
	return manager.repository.RemoveMember(
		ctx,
		workspaceID,
		actorID,
		accountID,
		manager.clock().UTC(),
	)
}

type postgresMemberLimits struct {
	database *sql.DB
}

func (limits postgresMemberLimits) MemberLimit(
	ctx context.Context,
	workspaceID string,
) (int, bool, error) {
	var limit sql.NullInt64
	err := limits.database.QueryRowContext(
		ctx,
		`SELECT quota_limit
		 FROM f10_public_entitlement_usage
		 WHERE workspace_id = $1
		   AND resource = 'members'`,
		workspaceID,
	).Scan(&limit)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read workspace member entitlement: %w", err)
	}
	if !limit.Valid {
		return -1, true, nil
	}
	if limit.Int64 <= 0 || limit.Int64 > int64(math.MaxInt) {
		return 0, false, nil
	}
	return int(limit.Int64), true, nil
}

func workspaceEmailDigestKeyFromEnv() []byte {
	encoded := strings.TrimSpace(os.Getenv(authEncryptionKeyEnv))
	if encoded == "" {
		return nil
	}
	key, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil
	}
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write([]byte("postqron/f04/invitation-email-digest/v1"))
	return digest.Sum(nil)
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
