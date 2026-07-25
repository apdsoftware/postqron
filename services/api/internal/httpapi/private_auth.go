package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

const productSessionCookie = "__Host-postqron_session"

func NewPostgresSessionAuthentication(
	database *sql.DB,
	clock func() time.Time,
) (func(http.Handler) http.Handler, error) {
	if database == nil || clock == nil {
		return nil, errors.New("private channel session dependencies are required")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			cookie, err := request.Cookie(productSessionCookie)
			if err != nil || strings.TrimSpace(cookie.Value) == "" {
				writeJSON(writer, http.StatusUnauthorized, map[string]string{
					"error": "ADMIN_UNAUTHENTICATED",
				})
				return
			}
			digest := sha256.Sum256([]byte(cookie.Value))
			var authenticated bool
			err = database.QueryRowContext(request.Context(), `
				SELECT EXISTS (
					SELECT 1
					FROM auth_sessions
					WHERE token_hash = $1
					  AND revoked_at IS NULL
					  AND expires_at > $2
				)`,
				hex.EncodeToString(digest[:]),
				clock().UTC(),
			).Scan(&authenticated)
			if err != nil {
				writeJSON(writer, http.StatusServiceUnavailable, map[string]string{
					"error": "ADMIN_UNAVAILABLE",
				})
				return
			}
			if !authenticated {
				writeJSON(writer, http.StatusUnauthorized, map[string]string{
					"error": "ADMIN_UNAUTHENTICATED",
				})
				return
			}
			next.ServeHTTP(writer, request)
		})
	}, nil
}
