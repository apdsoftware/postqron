package accountprivacyruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	accountprivacy "github.com/apdsoftware/postqron/features/f12-account-privacy"
)

const (
	cancelCapabilityIssuePath  = "/api/v1/account/deletion-cancel-capabilities"
	cancelCapabilityCookieName = "postqron_deletion_cancel"
	cancelCapabilityCookiePath = "/api/v1/account/deletions/"
	cancelCapabilityLifetime   = 29 * 24 * time.Hour
)

type cancelCapabilityHandler struct {
	store          cancelCapabilityStore
	service        deletionCancellationService
	authenticator  principalAuthenticator
	allowedOrigins map[string]struct{}
	secureCookies  bool
	now            func() time.Time
}

type deletionCancellationService interface {
	CancelDeletion(context.Context, accountprivacy.Principal, string) error
}

type principalAuthenticator interface {
	Principal(*http.Request) (accountprivacy.Principal, bool)
}

type cancelCapabilityStore interface {
	Issue(context.Context, string, string, time.Time, time.Time) error
	Claim(context.Context, string, string, string, time.Time) (string, error)
	Release(context.Context, string, string) error
	Consume(context.Context, string, string, time.Time) error
	AuditConsumeFailure(context.Context, time.Time) error
}

type sqlCancelCapabilityStore struct {
	database *sql.DB
}

func (store sqlCancelCapabilityStore) Issue(
	ctx context.Context,
	tokenHash, accountID string,
	expiresAt, now time.Time,
) error {
	_, err := store.database.ExecContext(ctx, `
		INSERT INTO account_privacy_cancel_capabilities
			(token_hash, account_id, expires_at, created_at)
		VALUES ($1, $2, $3, $4)`,
		tokenHash, accountID, expiresAt, now)
	return err
}

func (store sqlCancelCapabilityStore) Claim(
	ctx context.Context,
	tokenHash, requestID, claimToken string,
	now time.Time,
) (string, error) {
	var accountID string
	err := store.database.QueryRowContext(ctx, `
		UPDATE account_privacy_cancel_capabilities capability
		SET claimed_at = $4, claim_token = $3
		FROM account_privacy_deletion_requests deletion
		WHERE capability.token_hash = $1
		  AND capability.account_id = deletion.account_id
		  AND capability.consumed_at IS NULL
		  AND capability.expires_at > $4
		  AND capability.claimed_at IS NULL
		  AND deletion.id = $2
		  AND deletion.status = 'grace_period'
		  AND deletion.grace_ends_at > $4
		RETURNING capability.account_id`,
		tokenHash, requestID, claimToken, now,
	).Scan(&accountID)
	return accountID, err
}

func (store sqlCancelCapabilityStore) Release(
	ctx context.Context,
	tokenHash, claimToken string,
) error {
	_, err := store.database.ExecContext(ctx, `
		UPDATE account_privacy_cancel_capabilities
		SET claimed_at = NULL, claim_token = NULL
		WHERE token_hash = $1
		  AND claim_token = $2
		  AND consumed_at IS NULL`,
		tokenHash, claimToken)
	return err
}

func (store sqlCancelCapabilityStore) Consume(
	ctx context.Context,
	tokenHash, claimToken string,
	now time.Time,
) error {
	result, err := store.database.ExecContext(ctx, `
		UPDATE account_privacy_cancel_capabilities
		SET consumed_at = $3, claimed_at = NULL, claim_token = NULL
		WHERE token_hash = $1
		  AND claim_token = $2
		  AND consumed_at IS NULL`,
		tokenHash, claimToken, now)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (store sqlCancelCapabilityStore) AuditConsumeFailure(
	ctx context.Context,
	now time.Time,
) error {
	_, err := store.database.ExecContext(ctx, `
		INSERT INTO account_privacy_runtime_audit
			(target_id, event_type, outcome, error_code, occurred_at)
		VALUES ('cancel_capability', 'capability_consume', 'failed', 'consume_failed', $1)`,
		now)
	return err
}

func (handler cancelCapabilityHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	switch {
	case request.Method == http.MethodPost && request.URL.Path == cancelCapabilityIssuePath:
		handler.issue(response, request)
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/cancel"):
		handler.cancel(response, request)
	default:
		http.NotFound(response, request)
	}
}

func (handler cancelCapabilityHandler) issue(response http.ResponseWriter, request *http.Request) {
	if !handler.originAllowed(request) {
		http.Error(response, "origin not allowed", http.StatusForbidden)
		return
	}
	principal, ok := handler.authenticator.Principal(request)
	now := handler.now().UTC()
	if !ok || principal.AuthenticatedAt.IsZero() ||
		now.Sub(principal.AuthenticatedAt.UTC()) > accountprivacy.ReauthenticationWindow {
		http.Error(response, "recent authentication required", http.StatusUnauthorized)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		http.Error(response, "capability unavailable", http.StatusServiceUnavailable)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	expiresAt := now.Add(cancelCapabilityLifetime)
	if err := handler.store.Issue(
		request.Context(),
		hex.EncodeToString(digest[:]),
		principal.AccountID,
		expiresAt,
		now,
	); err != nil {
		http.Error(response, "capability unavailable", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     cancelCapabilityCookieName,
		Value:    token,
		Path:     cancelCapabilityCookiePath,
		Expires:  expiresAt,
		MaxAge:   int(cancelCapabilityLifetime.Seconds()),
		HttpOnly: true,
		Secure:   handler.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
	writeCapabilityJSON(response, http.StatusCreated, map[string]any{
		"expires_at": expiresAt,
	})
}

func (handler cancelCapabilityHandler) cancel(response http.ResponseWriter, request *http.Request) {
	if !handler.originAllowed(request) {
		http.Error(response, "origin not allowed", http.StatusForbidden)
		return
	}
	requestID := strings.TrimSuffix(strings.TrimPrefix(
		request.URL.Path, "/api/v1/account/deletions/",
	), "/cancel")
	if requestID == "" || strings.Contains(requestID, "/") {
		http.NotFound(response, request)
		return
	}
	cookie, err := request.Cookie(cancelCapabilityCookieName)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	token := strings.TrimSpace(cookie.Value)
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		http.NotFound(response, request)
		return
	}
	digest := sha256.Sum256([]byte(token))
	now := handler.now().UTC()
	tokenHash := hex.EncodeToString(digest[:])
	claimBytes := make([]byte, 18)
	if _, err := rand.Read(claimBytes); err != nil {
		http.Error(response, "cancellation unavailable", http.StatusServiceUnavailable)
		return
	}
	claimToken := hex.EncodeToString(claimBytes)
	accountID, err := handler.store.Claim(
		request.Context(), tokenHash, requestID, claimToken, now,
	)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	if err := handler.service.CancelDeletion(request.Context(), accountprivacy.Principal{
		AccountID: accountID, AuthenticatedAt: now,
	}, requestID); err != nil {
		if retryableCancellationError(err) {
			_ = handler.store.Release(
				context.WithoutCancel(request.Context()), tokenHash, claimToken,
			)
		}
		http.Error(response, "cancellation unavailable", http.StatusConflict)
		return
	}
	if err := handler.store.Consume(
		context.WithoutCancel(request.Context()), tokenHash, claimToken, now,
	); err != nil {
		_ = handler.store.AuditConsumeFailure(
			context.WithoutCancel(request.Context()), now,
		)
	}
	handler.clearCookie(response)
	response.WriteHeader(http.StatusNoContent)
}

func retryableCancellationError(err error) bool {
	return !errors.Is(err, accountprivacy.ErrNotFound) &&
		!errors.Is(err, accountprivacy.ErrDeletionInactive) &&
		!errors.Is(err, accountprivacy.ErrGracePeriodElapsed) &&
		!errors.Is(err, accountprivacy.ErrInvalidArgument) &&
		!errors.Is(err, accountprivacy.ErrForbidden)
}

func (handler cancelCapabilityHandler) originAllowed(request *http.Request) bool {
	normalized, err := normalizePrivacyOrigin(request.Header.Get("Origin"))
	if err != nil {
		return false
	}
	_, ok := handler.allowedOrigins[normalized]
	return ok
}

func (handler cancelCapabilityHandler) clearCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{
		Name:     cancelCapabilityCookieName,
		Value:    "",
		Path:     cancelCapabilityCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   handler.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func writeCapabilityJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
