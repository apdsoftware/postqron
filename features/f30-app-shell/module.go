package appshell

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

const sessionCookieName = "__Host-postqron_session"

type Module struct {
	database *sql.DB
	handler  http.Handler
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("app shell database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	handler, err := newHandler(
		database,
		clock,
		os.Getenv("POSTQRON_AUTH_ALLOWED_ORIGINS"),
	)
	if err != nil {
		return nil, err
	}
	return &Module{database: database, handler: handler}, nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.database == nil || module.handler == nil {
		return errors.New("app shell module is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("app shell database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil || name != "App" {
		return nil, false
	}
	return module.handler, true
}

type appHandler struct {
	database       *sql.DB
	clock          func() time.Time
	allowedOrigins []string
}

func newHandler(
	database *sql.DB,
	clock func() time.Time,
	allowedOrigins string,
) (http.Handler, error) {
	origins, err := normalizeOrigins(allowedOrigins)
	if err != nil {
		return nil, err
	}
	handler := &appHandler{
		database:       database,
		clock:          clock,
		allowedOrigins: origins,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/app/bootstrap", handler.bootstrap)
	mux.HandleFunc("OPTIONS /api/v1/app/bootstrap", handler.preflight)
	mux.HandleFunc("GET /api/v1/app/session", handler.session)
	mux.HandleFunc("OPTIONS /api/v1/app/session", handler.preflight)
	return handler.cors(mux), nil
}

type legalDocument struct {
	Key          string `json:"key"`
	Version      string `json:"version"`
	DigestSHA256 string `json:"digest_sha256"`
	Href         string `json:"href"`
}

type workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type appSession struct {
	Account struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Locale        string `json:"locale"`
	} `json:"account"`
	OnboardingRequired bool        `json:"onboarding_required"`
	CurrentWorkspace   *workspace  `json:"current_workspace,omitempty"`
	Workspaces         []workspace `json:"workspaces"`
	AuthenticatedAt    time.Time   `json:"authenticated_at"`
}

func (handler *appHandler) bootstrap(
	writer http.ResponseWriter,
	request *http.Request,
) {
	documents, err := handler.legalDocuments(request.Context())
	if err != nil {
		writeAppError(writer, http.StatusServiceUnavailable, "APP_CONFIGURATION_UNAVAILABLE")
		return
	}
	response := map[string]any{
		"auth_methods":    []string{"password"},
		"providers":       configuredProviders(),
		"legal_documents": documents,
	}
	if session, found, err := handler.readSession(request); err != nil {
		writeAppError(writer, http.StatusServiceUnavailable, "APP_SESSION_UNAVAILABLE")
		return
	} else if found {
		response["session"] = session
	}
	writeAppJSON(writer, http.StatusOK, response)
}

func (handler *appHandler) session(
	writer http.ResponseWriter,
	request *http.Request,
) {
	session, found, err := handler.readSession(request)
	if err != nil {
		writeAppError(writer, http.StatusServiceUnavailable, "APP_SESSION_UNAVAILABLE")
		return
	}
	if !found {
		writeAppError(writer, http.StatusUnauthorized, "APP_UNAUTHENTICATED")
		return
	}
	writeAppJSON(writer, http.StatusOK, session)
}

func (handler *appHandler) legalDocuments(
	ctx context.Context,
) ([]legalDocument, error) {
	rows, err := handler.database.QueryContext(ctx, `
		SELECT document_key, version, digest_sha256
		FROM compliance_legal_documents
		WHERE document_key IN ('terms_it', 'privacy_it')
		  AND jurisdiction = 'IT'
		  AND locale = 'it-IT'
		  AND content_status = 'approved'
		  AND published_at IS NOT NULL
		  AND effective_at <= $1
		  AND (superseded_at IS NULL OR superseded_at > $1)
		ORDER BY document_key`,
		handler.clock().UTC(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	documents := make([]legalDocument, 0, 2)
	for rows.Next() {
		var key string
		var document legalDocument
		if err := rows.Scan(&key, &document.Version, &document.DigestSHA256); err != nil {
			return nil, err
		}
		switch key {
		case "terms_it":
			document.Key = "terms"
			document.Href = "/it/legal/terms"
		case "privacy_it":
			document.Key = "privacy"
			document.Href = "/it/legal/privacy"
		default:
			continue
		}
		documents = append(documents, document)
	}
	return documents, rows.Err()
}

func (handler *appHandler) readSession(
	request *http.Request,
) (appSession, bool, error) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return appSession{}, false, nil
	}
	digest := sha256.Sum256([]byte(cookie.Value))
	var session appSession
	session.Account.Locale = "en"
	err = handler.database.QueryRowContext(request.Context(), `
		SELECT
			account.id,
			account.display_name,
			account.normalized_email,
			account.email_verified_at IS NOT NULL,
			session.authenticated_at
		FROM auth_sessions session
		JOIN auth_accounts account ON account.id = session.account_id
		WHERE session.token_hash = $1
		  AND session.revoked_at IS NULL
		  AND session.expires_at > $2
		  AND account.email_verified_at IS NOT NULL`,
		hex.EncodeToString(digest[:]),
		handler.clock().UTC(),
	).Scan(
		&session.Account.ID,
		&session.Account.DisplayName,
		&session.Account.Email,
		&session.Account.EmailVerified,
		&session.AuthenticatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return appSession{}, false, nil
	}
	if err != nil {
		return appSession{}, false, err
	}
	rows, err := handler.database.QueryContext(request.Context(), `
		SELECT workspace.id, workspace.name, membership.role
		FROM f04_memberships membership
		JOIN f04_workspaces workspace ON workspace.id = membership.workspace_id
		WHERE membership.account_id = $1
		  AND membership.status = 'active'
		  AND workspace.status = 'active'
		ORDER BY workspace.created_at, workspace.id`,
		session.Account.ID,
	)
	if err != nil {
		return appSession{}, false, err
	}
	defer rows.Close()
	session.Workspaces = []workspace{}
	for rows.Next() {
		var current workspace
		if err := rows.Scan(&current.ID, &current.Name, &current.Role); err != nil {
			return appSession{}, false, err
		}
		session.Workspaces = append(session.Workspaces, current)
	}
	if err := rows.Err(); err != nil {
		return appSession{}, false, err
	}
	session.OnboardingRequired = len(session.Workspaces) == 0
	if len(session.Workspaces) > 0 {
		selectedID, selectionErr := handler.selectedWorkspaceID(
			request.Context(),
			session.Account.ID,
		)
		if selectionErr != nil {
			return appSession{}, false, selectionErr
		}
		current := session.Workspaces[0]
		for _, candidate := range session.Workspaces {
			if candidate.ID == selectedID {
				current = candidate
				break
			}
		}
		session.CurrentWorkspace = &current
	}
	return session, true, nil
}

func (handler *appHandler) selectedWorkspaceID(
	ctx context.Context,
	accountID string,
) (string, error) {
	var selectedID string
	err := handler.database.QueryRowContext(ctx, `
		SELECT workspace_id
		FROM f04_workspace_selections
		WHERE account_id = $1
		ORDER BY updated_at DESC
		LIMIT 1`,
		accountID,
	).Scan(&selectedID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(selectedID), nil
}

func (handler *appHandler) preflight(
	writer http.ResponseWriter,
	_ *http.Request,
) {
	writer.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	writer.Header().Set("Access-Control-Max-Age", "600")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *appHandler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		normalized, err := normalizeOrigin(origin)
		if err != nil || !slices.Contains(handler.allowedOrigins, normalized) {
			writeAppError(writer, http.StatusForbidden, "APP_ORIGIN_FORBIDDEN")
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", normalized)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		writer.Header().Add("Vary", "Origin")
		next.ServeHTTP(writer, request)
	})
}

func normalizeOrigins(raw string) ([]string, error) {
	var origins []string
	for _, value := range strings.Split(raw, ",") {
		if strings.TrimSpace(value) == "" {
			continue
		}
		origin, err := normalizeOrigin(value)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(origins, origin) {
			origins = append(origins, origin)
		}
	}
	slices.Sort(origins)
	return origins, nil
}

func normalizeOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("app allowed origin must be an HTTP(S) origin")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func configuredProviders() []string {
	if !validAuthEncryptionKey(os.Getenv("POSTQRON_AUTH_ENCRYPTION_KEY_B64")) {
		return []string{}
	}
	providers := make([]string, 0, 4)
	if validProviderConfig(
		"POSTQRON_AUTH_GOOGLE_CLIENT_ID",
		"POSTQRON_AUTH_GOOGLE_CLIENT_SECRET",
		"POSTQRON_AUTH_GOOGLE_REDIRECT_URL",
	) {
		providers = append(providers, "google")
	}
	if validProviderConfig(
		"POSTQRON_AUTH_APPLE_CLIENT_ID",
		"POSTQRON_AUTH_APPLE_CLIENT_SECRET",
		"POSTQRON_AUTH_APPLE_REDIRECT_URL",
	) {
		providers = append(providers, "apple")
	}
	if validProviderConfig(
		"POSTQRON_AUTH_FACEBOOK_CLIENT_ID",
		"POSTQRON_AUTH_FACEBOOK_CLIENT_SECRET",
		"POSTQRON_AUTH_FACEBOOK_REDIRECT_URL",
		"POSTQRON_AUTH_FACEBOOK_GRAPH_VERSION",
	) {
		providers = append(providers, "facebook")
	}
	if validProviderConfig(
		"POSTQRON_AUTH_LINKEDIN_CLIENT_ID",
		"POSTQRON_AUTH_LINKEDIN_CLIENT_SECRET",
		"POSTQRON_AUTH_LINKEDIN_REDIRECT_URL",
	) {
		providers = append(providers, "linkedin")
	}
	return providers
}

func validAuthEncryptionKey(value string) bool {
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(value))
	return err == nil && len(decoded) == 32
}

func validProviderConfig(keys ...string) bool {
	for index, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value == "" {
			return false
		}
		if index == len(keys)-1 && strings.HasSuffix(key, "_GRAPH_VERSION") {
			continue
		}
		if strings.HasSuffix(key, "_REDIRECT_URL") {
			if !validHTTPSAbsoluteURL(value) {
				return false
			}
		}
	}
	return true
}

func validHTTPSAbsoluteURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	if parsed.Path == "" {
		return false
	}
	return true
}

func hasEnv(keys ...string) bool {
	for _, key := range keys {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			return false
		}
	}
	return true
}

func writeAppError(writer http.ResponseWriter, status int, code string) {
	writeAppJSON(writer, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   "The app service request failed.",
			"retryable": status >= 500,
		},
	})
}

func writeAppJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
