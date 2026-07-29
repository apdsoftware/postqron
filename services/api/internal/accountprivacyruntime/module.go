package accountprivacyruntime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	auth "github.com/apdsoftware/postqron/features/f03-auth"
	accountprivacy "github.com/apdsoftware/postqron/features/f12-account-privacy"
	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	authruntime "github.com/apdsoftware/postqron/services/api/internal/authruntime"
)

const sessionCookieName = "__Host-postqron_session"

type Module struct {
	database *sql.DB
	handler  http.Handler
	artifact http.Handler
	cancel   http.Handler
}

func NewModule(database *sql.DB, clock func() time.Time) (*Module, error) {
	return NewModuleWithAccountAccess(database, clock, nil)
}

func NewModuleWithAccountAccess(
	database *sql.DB,
	clock func() time.Time,
	access AccountAccessBoundary,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("account privacy runtime database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	repository, err := accountprivacy.NewPostgresRepository(
		database,
		providerProjection{database: database},
		workspaceProjection{database: database},
	)
	if err != nil {
		return nil, err
	}
	artifactRoot := strings.TrimSpace(os.Getenv("POSTQRON_PRIVACY_ARTIFACT_DIR"))
	if artifactRoot == "" {
		artifactRoot = filepath.Join(os.TempDir(), "postqron-privacy-artifacts")
	}
	artifactKey, err := artifactKeyFromEnv()
	if err != nil {
		return nil, err
	}
	artifactStore, err := newPrivateArtifactStore(artifactRoot, artifactKey)
	if err != nil {
		return nil, err
	}
	publicBaseURL := strings.TrimSpace(os.Getenv("POSTQRON_PRIVACY_DOWNLOAD_BASE_URL"))
	if publicBaseURL == "" {
		publicBaseURL = "http://127.0.0.1:8080"
	}
	publicBaseURL, err = validateDownloadBaseURL(
		publicBaseURL,
		strings.EqualFold(strings.TrimSpace(os.Getenv("POSTQRON_ENV")), "production"),
	)
	if err != nil {
		return nil, err
	}
	if access == nil {
		authStore, err := auth.NewPostgresStore(database)
		if err != nil {
			return nil, fmt.Errorf("create F3 account access store: %w", err)
		}
		authBoundary, err := auth.NewAccountAccessBoundary(authStore, clock)
		if err != nil {
			return nil, fmt.Errorf("create F3 account access boundary: %w", err)
		}
		access = authBoundary
	}
	service, err := accountprivacy.NewService(
		accountprivacy.Dependencies{
			Repository:       repository,
			Plans:            planProjection{database: database},
			Providers:        providerDisconnecter{database: database, now: clock},
			ExportAuthorizer: exportAuthorizer{database: database},
			ExportQueue:      sqlExportQueue{database: database, now: clock},
			DownloadSigner: oneTimeDownloadSigner{
				database: database, baseURL: publicBaseURL, now: clock,
			},
			ExportArtifacts: artifactStore,
			Ownership:       ownershipResolver{database: database},
			DeletionSafety: deletionSafety{
				database:  database,
				access:    access,
				providers: runtimeProviderRevoker{database: database},
				now:       clock,
			},
			Eraser: eraser{database: database, access: access},
		},
		accountprivacy.WithClock(clock),
	)
	if err != nil {
		return nil, err
	}
	handler, err := accountprivacy.NewHTTPHandler(
		service,
		requestAuthenticator{database: database, clock: clock},
		accountprivacy.WithHTTPClock(clock),
	)
	if err != nil {
		return nil, err
	}
	return &Module{
		database: database,
		handler:  handler,
		artifact: artifactDownloadHandler{database: database, store: artifactStore, now: clock},
		cancel: cancelCapabilityHandler{
			database:      database,
			service:       service,
			authenticator: requestAuthenticator{database: database, clock: clock},
			now:           clock,
		},
	}, nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.database == nil || module.handler == nil {
		return errors.New("account privacy runtime module is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error { return nil }

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("account privacy runtime database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil || name != "AccountPrivacy" {
		return nil, false
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, artifactRoutePrefix) {
			module.artifact.ServeHTTP(response, request)
			return
		}
		if request.URL.Path == cancelCapabilityIssuePath ||
			strings.HasSuffix(request.URL.Path, "/cancel") {
			module.cancel.ServeHTTP(response, request)
			return
		}
		module.handler.ServeHTTP(response, request)
	}), true
}

type requestAuthenticator struct {
	database *sql.DB
	clock    func() time.Time
}

func (authenticator requestAuthenticator) Principal(
	request *http.Request,
) (accountprivacy.Principal, bool) {
	accountID, ok := featureruntime.AuthenticatedAccount(request.Context())
	if !ok || strings.TrimSpace(accountID) == "" {
		return accountprivacy.Principal{}, false
	}
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return accountprivacy.Principal{}, false
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	var authenticatedAt time.Time
	err = authenticator.database.QueryRowContext(
		request.Context(),
		`SELECT authenticated_at
		   FROM auth_sessions
		  WHERE account_id = $1
		    AND token_hash = $2
		    AND revoked_at IS NULL
		    AND expires_at > $3`,
		accountID,
		hex.EncodeToString(digest[:]),
		authenticator.clock().UTC(),
	).Scan(&authenticatedAt)
	if err != nil {
		return accountprivacy.Principal{}, false
	}
	return accountprivacy.Principal{
		AccountID:       accountID,
		AuthenticatedAt: authenticatedAt,
	}, true
}

type providerProjection struct {
	database *sql.DB
}

func (projection providerProjection) Providers(
	ctx context.Context,
	accountID string,
) ([]accountprivacy.Provider, error) {
	providers := make([]accountprivacy.Provider, 0, 5)
	var changedAt time.Time
	err := projection.database.QueryRowContext(
		ctx,
		`SELECT changed_at
		   FROM auth_password_credentials
		  WHERE account_id = $1`,
		accountID,
	).Scan(&changedAt)
	if err == nil {
		providers = append(providers, accountprivacy.Provider{
			ID:              "password",
			Kind:            accountprivacy.ProviderIdentity,
			Name:            "password",
			ExternalLabel:   "password",
			ConnectedAt:     changedAt,
			OnlyLoginMethod: false,
		})
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read password provider projection: %w", err)
	}
	rows, err := projection.database.QueryContext(
		ctx,
		`SELECT provider, provider_email, linked_at
		   FROM auth_provider_identities
		  WHERE account_id = $1
		  ORDER BY linked_at, provider`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("read provider projections: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var provider accountprivacy.Provider
		provider.Kind = accountprivacy.ProviderIdentity
		if err := rows.Scan(&provider.Name, &provider.ExternalLabel, &provider.ConnectedAt); err != nil {
			return nil, fmt.Errorf("scan provider projection: %w", err)
		}
		provider.ID = provider.Name
		if provider.ExternalLabel == "" {
			provider.ExternalLabel = provider.Name
		}
		providers = append(providers, provider)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate provider projections: %w", err)
	}
	loginMethods := 0
	for _, provider := range providers {
		if provider.Kind == accountprivacy.ProviderIdentity {
			loginMethods++
		}
	}
	for index := range providers {
		if providers[index].Kind == accountprivacy.ProviderIdentity && loginMethods == 1 {
			providers[index].OnlyLoginMethod = true
		}
	}
	return providers, nil
}

func (projection providerProjection) Provider(
	ctx context.Context,
	accountID, providerID string,
) (accountprivacy.Provider, error) {
	providers, err := projection.Providers(ctx, accountID)
	if err != nil {
		return accountprivacy.Provider{}, err
	}
	for _, provider := range providers {
		if provider.ID == strings.TrimSpace(providerID) {
			return provider, nil
		}
	}
	return accountprivacy.Provider{}, accountprivacy.ErrNotFound
}

type workspaceProjection struct {
	database *sql.DB
}

func (projection workspaceProjection) Workspaces(
	ctx context.Context,
	accountID string,
) ([]accountprivacy.WorkspaceRef, error) {
	rows, err := projection.database.QueryContext(
		ctx,
		`SELECT workspace.id, workspace.name, membership.role
		   FROM f04_memberships membership
		   JOIN f04_workspaces workspace ON workspace.id = membership.workspace_id
		  WHERE membership.account_id = $1
		    AND membership.status = 'active'
		    AND workspace.status = 'active'
		  ORDER BY workspace.created_at, workspace.id`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("read workspace projections: %w", err)
	}
	defer rows.Close()
	workspaces := []accountprivacy.WorkspaceRef{}
	for rows.Next() {
		var workspace accountprivacy.WorkspaceRef
		if err := rows.Scan(&workspace.ID, &workspace.Name, &workspace.Role); err != nil {
			return nil, fmt.Errorf("scan workspace projection: %w", err)
		}
		workspaces = append(workspaces, workspace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate workspace projections: %w", err)
	}
	return workspaces, nil
}

type planProjection struct {
	database *sql.DB
}

func (projection planProjection) Plan(
	ctx context.Context,
	workspaceID string,
	_ string,
) (accountprivacy.Plan, error) {
	rows, err := projection.database.QueryContext(
		ctx,
		`SELECT plan_code, plan_name, billing_state, period_end, resource, used, quota_limit
		   FROM f10_public_entitlement_usage
		  WHERE workspace_id = $1
		  ORDER BY resource`,
		workspaceID,
	)
	if err != nil {
		return accountprivacy.Plan{}, fmt.Errorf("read plan projection: %w", err)
	}
	defer rows.Close()
	plan := accountprivacy.Plan{
		Usage:  map[string]int64{},
		Limits: map[string]int64{},
	}
	found := false
	for rows.Next() {
		found = true
		var renewsAt time.Time
		var resource string
		var used int64
		var limit sql.NullInt64
		if err := rows.Scan(
			&plan.Code,
			&plan.Name,
			&plan.State,
			&renewsAt,
			&resource,
			&used,
			&limit,
		); err != nil {
			return accountprivacy.Plan{}, fmt.Errorf("scan plan projection: %w", err)
		}
		plan.Usage[resource] = used
		if limit.Valid {
			plan.Limits[resource] = limit.Int64
		}
		plan.RenewsAt = &renewsAt
	}
	if err := rows.Err(); err != nil {
		return accountprivacy.Plan{}, fmt.Errorf("iterate plan projection: %w", err)
	}
	if !found {
		return accountprivacy.Plan{}, accountprivacy.ErrNotFound
	}
	plan.Manageable = plan.Code != "start"
	return plan, nil
}

type providerDisconnecter struct {
	database *sql.DB
	now      func() time.Time
}

func (disconnecter providerDisconnecter) Disconnect(
	ctx context.Context,
	accountID string,
	provider accountprivacy.Provider,
) error {
	if provider.ID == "password" {
		return disconnecter.deletePassword(ctx, accountID)
	}
	return disconnecter.deleteIdentity(ctx, accountID, provider)
}

func (disconnecter providerDisconnecter) deletePassword(ctx context.Context, accountID string) error {
	transaction, err := disconnecter.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err := lockAccount(ctx, transaction, accountID); err != nil {
		return err
	}
	var loginMethods int
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT
			(SELECT COUNT(*) FROM auth_provider_identities WHERE account_id = $1) +
			CASE WHEN EXISTS (
				SELECT 1 FROM auth_password_credentials WHERE account_id = $1
			) THEN 1 ELSE 0 END`,
		accountID,
	).Scan(&loginMethods); err != nil {
		return err
	}
	if loginMethods <= 1 {
		return accountprivacy.ErrLastLoginProvider
	}
	result, err := transaction.ExecContext(
		ctx,
		`DELETE FROM auth_password_credentials WHERE account_id = $1`,
		accountID,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return accountprivacy.ErrNotFound
	}
	return transaction.Commit()
}

func (disconnecter providerDisconnecter) deleteIdentity(
	ctx context.Context,
	accountID string,
	provider accountprivacy.Provider,
) error {
	transaction, err := disconnecter.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err := lockAccount(ctx, transaction, accountID); err != nil {
		return err
	}
	var loginMethods int
	if err := transaction.QueryRowContext(
		ctx,
		`SELECT
			(SELECT COUNT(*) FROM auth_provider_identities WHERE account_id = $1) +
			CASE WHEN EXISTS (
				SELECT 1 FROM auth_password_credentials WHERE account_id = $1
			) THEN 1 ELSE 0 END`,
		accountID,
	).Scan(&loginMethods); err != nil {
		return err
	}
	if loginMethods <= 1 {
		return accountprivacy.ErrLastLoginProvider
	}
	var tokenCiphertext []byte
	err = transaction.QueryRowContext(
		ctx,
		`SELECT COALESCE(revocation_token_ciphertext, ''::bytea)
		   FROM auth_provider_identities
		  WHERE account_id = $1
		    AND provider = $2`,
		accountID,
		provider.Name,
	).Scan(&tokenCiphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return accountprivacy.ErrNotFound
	}
	if err != nil {
		return err
	}
	adapters := authruntime.RuntimeProviderAdapters()
	if len(tokenCiphertext) > 0 {
		adapter := adapters[auth.Provider(provider.Name)]
		sealer := authruntime.RuntimeSealerFromEnv()
		if adapter == nil || sealer == nil {
			return errors.New("provider disconnect is unavailable")
		}
		token, err := sealer.Open(tokenCiphertext)
		if err != nil {
			return fmt.Errorf("open provider token: %w", err)
		}
		if err := adapter.Revoke(ctx, string(token)); err != nil {
			return err
		}
	}
	result, err := transaction.ExecContext(
		ctx,
		`DELETE FROM auth_provider_identities
		  WHERE account_id = $1
		    AND provider = $2`,
		accountID,
		provider.Name,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows != 1 {
		return accountprivacy.ErrNotFound
	}
	return transaction.Commit()
}

func lockAccount(ctx context.Context, transaction *sql.Tx, accountID string) error {
	var lockedID string
	err := transaction.QueryRowContext(
		ctx,
		`SELECT id FROM auth_accounts WHERE id = $1 FOR UPDATE`,
		accountID,
	).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return accountprivacy.ErrNotFound
	}
	return err
}

type exportAuthorizer struct {
	database *sql.DB
}

func (authorizer exportAuthorizer) AuthorizeExport(
	ctx context.Context,
	accountID string,
	scope accountprivacy.ExportScope,
	workspaceID string,
) error {
	if scope == accountprivacy.ExportAccount {
		return nil
	}
	var exists bool
	err := authorizer.database.QueryRowContext(
		ctx,
		`SELECT EXISTS (
			SELECT 1
			  FROM f04_memberships
			 WHERE account_id = $1
			   AND workspace_id = $2
			   AND role = 'owner'
			   AND status = 'active'
		)`,
		accountID,
		workspaceID,
	).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return accountprivacy.ErrForbidden
	}
	return nil
}
