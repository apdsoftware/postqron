package entitlements

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	configAppDomain           = "billing.app_domain"
	configPaddleEnvironment   = "billing.paddle_environment"
	configPaddleAPIKey        = "billing.paddle_api_key"
	configPaddleWebhookSecret = "billing.paddle_webhook_secret"
	configPaddleCatalogJSON   = "billing.paddle_catalog_json"
)

var ErrIncompletePaddleRuntimeConfig = errors.New(
	"incomplete Paddle runtime configuration",
)

// Module exposes F10 through the API feature runtime. With no Paddle values it
// remains catalog-only for local/test compatibility. Any partial configuration
// is rejected, while a complete configuration enables all billing handlers.
type Module struct {
	database *sql.DB
	clock    func() time.Time
	handlers map[string]http.Handler
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	handler := &HTTPHandler{}
	return &Module{
		database: database,
		clock:    clock,
		handlers: catalogOnlyHandlers(handler),
	}, nil
}

func (module *Module) Configure(values map[string]string) error {
	if module == nil {
		return errors.New("entitlements module is not configured")
	}
	paddleValues := []string{
		values[configPaddleEnvironment],
		values[configPaddleAPIKey],
		values[configPaddleWebhookSecret],
		values[configPaddleCatalogJSON],
	}
	configured := 0
	for _, value := range paddleValues {
		if strings.TrimSpace(value) != "" {
			configured++
		}
	}
	if configured == 0 {
		return nil
	}
	if configured != len(paddleValues) ||
		strings.TrimSpace(values[configAppDomain]) == "" ||
		module.database == nil ||
		module.clock == nil {
		return ErrIncompletePaddleRuntimeConfig
	}

	paddleConfig, err := NewPaddleConfig(
		values[configPaddleEnvironment],
		values[configPaddleAPIKey],
		values[configPaddleWebhookSecret],
		values[configPaddleCatalogJSON],
	)
	if err != nil {
		return err
	}
	checkoutURL, err := billingCheckoutURL(values[configAppDomain])
	if err != nil {
		return err
	}
	provider, err := NewPaddleClient(paddleConfig, nil)
	if err != nil {
		return err
	}
	provider.now = module.clock
	database := sqlDatabase{DB: module.database}
	store := NewSQLStore(database)
	access := postgresBillingAccess{db: database}
	authenticator := runtimeRequestAuthenticator{}
	checkout, err := NewCheckoutService(
		access,
		provider,
		store,
		paddleConfig.Catalog,
		checkoutURL,
	)
	if err != nil {
		return err
	}
	checkout.now = module.clock
	changes, err := NewSubscriptionChangeService(
		access,
		provider,
		store,
		paddleConfig.Catalog,
	)
	if err != nil {
		return err
	}
	webhook, err := NewPaddleWebhookHandler(
		paddleConfig.WebhookSecret,
		paddleConfig.Catalog,
		store,
	)
	if err != nil {
		return err
	}
	webhook.now = module.clock
	handler := &HTTPHandler{
		service:       NewService(store),
		checkout:      checkout,
		portal:        NewPortalService(access, provider, store),
		changes:       changes,
		webhook:       webhook,
		authenticator: authenticator,
		viewer:        access,
	}
	module.handlers = configuredHandlers(handler)
	return nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.handlers["PublicPlans"] == nil {
		return errors.New("entitlements module is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(context.Context) error {
	if module == nil || module.handlers["PublicPlans"] == nil {
		return errors.New("entitlements catalog is unavailable")
	}
	return nil
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil {
		return nil, false
	}
	handler, ok := module.handlers[name]
	return handler, ok && handler != nil
}

func catalogOnlyHandlers(handler *HTTPHandler) map[string]http.Handler {
	unavailable := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeEntitlementError(
			writer,
			http.StatusServiceUnavailable,
			"billing_unavailable",
		)
	})
	handlers := configuredHandlers(handler)
	for name := range handlers {
		if name != "PublicPlans" {
			handlers[name] = unavailable
		}
	}
	return handlers
}

func configuredHandlers(handler *HTTPHandler) map[string]http.Handler {
	return map[string]http.Handler{
		"PublicPlans":               http.HandlerFunc(handler.plans),
		"BillingOverview":           http.HandlerFunc(handler.overview),
		"BillingCheckout":           http.HandlerFunc(handler.createCheckout),
		"BillingPortal":             http.HandlerFunc(handler.createPortal),
		"PreviewSubscriptionChange": http.HandlerFunc(handler.previewSubscriptionChange),
		"ApplySubscriptionChange":   http.HandlerFunc(handler.applySubscriptionChange),
		"CancelSubscription":        http.HandlerFunc(handler.cancelSubscription),
		"PaddleWebhook":             handler.webhook,
	}
}

func billingCheckoutURL(domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	candidate, err := url.Parse("https://" + domain)
	if err != nil ||
		candidate.Host == "" ||
		candidate.User != nil ||
		candidate.Path != "" ||
		candidate.RawQuery != "" ||
		candidate.Fragment != "" {
		return "", errors.New("invalid billing application domain")
	}
	candidate.Path = "/app/billing/checkout"
	if !absoluteHTTPS(candidate.String()) {
		return "", errors.New("invalid billing checkout URL")
	}
	return candidate.String(), nil
}

type sqlDatabase struct {
	*sql.DB
}

func (database sqlDatabase) Query(
	ctx context.Context,
	query string,
	args ...any,
) (pgx.Rows, error) {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{Rows: rows}, nil
}

func (database sqlDatabase) QueryRow(
	ctx context.Context,
	query string,
	args ...any,
) pgx.Row {
	return sqlRow{Row: database.QueryRowContext(ctx, query, args...)}
}

type sqlRow struct {
	*sql.Row
}

func (row sqlRow) Scan(dest ...any) error {
	err := row.Row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return pgx.ErrNoRows
	}
	return err
}

type sqlRows struct {
	*sql.Rows
}

func (rows sqlRows) Close() {
	_ = rows.Rows.Close()
}

func (rows sqlRows) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}

func (rows sqlRows) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (rows sqlRows) Values() ([]any, error) {
	return nil, errors.New("row values are unavailable")
}

func (rows sqlRows) RawValues() [][]byte {
	return nil
}

func (rows sqlRows) Conn() *pgx.Conn {
	return nil
}

type runtimeRequestAuthenticator struct{}

func (runtimeRequestAuthenticator) AccountID(request *http.Request) (string, bool) {
	return featureruntime.AuthenticatedAccount(request.Context())
}

type postgresBillingAccess struct {
	db DB
}

func (access postgresBillingAccess) IsOwner(
	ctx context.Context,
	workspaceID string,
	accountID string,
) (bool, error) {
	return access.membershipMatches(ctx, workspaceID, accountID, "owner")
}

func (access postgresBillingAccess) CanViewBilling(
	ctx context.Context,
	workspaceID string,
	accountID string,
) (bool, error) {
	return access.membershipMatches(ctx, workspaceID, accountID, "")
}

func (access postgresBillingAccess) membershipMatches(
	ctx context.Context,
	workspaceID string,
	accountID string,
	role string,
) (bool, error) {
	var allowed bool
	err := access.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM f04_memberships
			 WHERE workspace_id = $1
			   AND account_id = $2
			   AND status = 'active'
			   AND ($3 = '' OR role::text = $3)
		)`,
		workspaceID,
		accountID,
		role,
	).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("authorize workspace billing: %w", err)
	}
	return allowed, nil
}
