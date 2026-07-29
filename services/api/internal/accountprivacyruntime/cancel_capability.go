package accountprivacyruntime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	accountprivacy "github.com/apdsoftware/postqron/features/f12-account-privacy"
)

const (
	cancelCapabilityIssuePath = "/api/v1/account/deletion-cancel-capabilities"
	cancelCapabilityLifetime  = 29 * 24 * time.Hour
)

type cancelCapabilityHandler struct {
	database      *sql.DB
	service       *accountprivacy.Service
	authenticator requestAuthenticator
	now           func() time.Time
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
	if _, err := handler.database.ExecContext(request.Context(), `
		INSERT INTO account_privacy_cancel_capabilities
			(token_hash, account_id, expires_at, created_at)
		VALUES ($1, $2, $3, $4)`,
		hex.EncodeToString(digest[:]), principal.AccountID, expiresAt, now); err != nil {
		http.Error(response, "capability unavailable", http.StatusServiceUnavailable)
		return
	}
	writeCapabilityJSON(response, http.StatusCreated, map[string]any{
		"token": token, "expires_at": expiresAt,
	})
}

func (handler cancelCapabilityHandler) cancel(response http.ResponseWriter, request *http.Request) {
	requestID := strings.TrimSuffix(strings.TrimPrefix(
		request.URL.Path, "/api/v1/account/deletions/",
	), "/cancel")
	if requestID == "" || strings.Contains(requestID, "/") {
		http.NotFound(response, request)
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(response, "invalid capability", http.StatusBadRequest)
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(input.Token))
	if err != nil || len(decoded) != 32 {
		http.NotFound(response, request)
		return
	}
	digest := sha256.Sum256([]byte(input.Token))
	now := handler.now().UTC()
	var accountID string
	err = handler.database.QueryRowContext(request.Context(), `
		SELECT capability.account_id
		FROM account_privacy_cancel_capabilities capability
		JOIN account_privacy_deletion_requests deletion
		  ON deletion.account_id = capability.account_id
		WHERE capability.token_hash = $1
		  AND capability.consumed_at IS NULL
		  AND capability.expires_at > $2
		  AND deletion.id = $3
		  AND deletion.status = 'grace_period'
		  AND deletion.grace_ends_at > $2`,
		hex.EncodeToString(digest[:]), now, requestID).Scan(&accountID)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	if err := handler.service.CancelDeletion(request.Context(), accountprivacy.Principal{
		AccountID: accountID, AuthenticatedAt: now,
	}, requestID); err != nil {
		http.Error(response, "cancellation unavailable", http.StatusConflict)
		return
	}
	_, _ = handler.database.ExecContext(context.WithoutCancel(request.Context()), `
		UPDATE account_privacy_cancel_capabilities
		SET consumed_at = $2
		WHERE token_hash = $1 AND consumed_at IS NULL`,
		hex.EncodeToString(digest[:]), now)
	response.WriteHeader(http.StatusNoContent)
}

func writeCapabilityJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
