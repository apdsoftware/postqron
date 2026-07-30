package accountprivacyruntime

import (
	"encoding/json"
	"net/http"
	"strings"
)

func credentialedCORS(
	next http.Handler,
	allowedOrigins map[string]struct{},
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		if origin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		if _, allowed := allowedOrigins[origin]; !allowed {
			writer.Header().Set("Content-Type", "application/json; charset=utf-8")
			writer.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"error": map[string]any{
					"code":      "ACCOUNT_ORIGIN_FORBIDDEN",
					"message":   "The request origin is not allowed.",
					"retryable": false,
				},
			})
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		writer.Header().Add("Vary", "Origin")
		if request.Method == http.MethodOptions {
			writer.Header().Set(
				"Access-Control-Allow-Methods",
				"GET, POST, PATCH, DELETE, OPTIONS",
			)
			writer.Header().Set(
				"Access-Control-Allow-Headers",
				"Content-Type, X-CSRF-Token",
			)
			writer.Header().Set("Access-Control-Max-Age", "600")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
