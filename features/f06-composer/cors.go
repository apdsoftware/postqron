package composer

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func credentialedComposerCORS(
	next http.Handler,
	allowedOrigins map[string]struct{},
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(request.Header.Values("Origin")) == 0 {
			next.ServeHTTP(writer, request)
			return
		}
		origin, ok := composerRequestOrigin(request)
		if !ok {
			writeComposerError(writer, http.StatusForbidden, "origin_forbidden", nil)
			return
		}
		if _, allowed := allowedOrigins[origin]; !allowed {
			writeComposerError(writer, http.StatusForbidden, "origin_forbidden", nil)
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		addComposerVaryOrigin(writer.Header())
		if request.Method == http.MethodOptions {
			writer.Header().Set(
				"Access-Control-Allow-Methods",
				"GET, POST, PUT, PATCH, DELETE, OPTIONS",
			)
			writer.Header().Set(
				"Access-Control-Allow-Headers",
				"Content-Type, If-Match",
			)
			writer.Header().Set("Access-Control-Max-Age", "600")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func parseComposerAllowedOrigins(raw string) (map[string]struct{}, error) {
	origins := make(map[string]struct{})
	for _, candidate := range strings.Split(raw, ",") {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		origin, err := normalizeComposerOrigin(candidate)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: POSTQRON_AUTH_ALLOWED_ORIGINS contains an invalid origin",
				ErrInvalidArgument,
			)
		}
		origins[origin] = struct{}{}
	}
	return origins, nil
}

func composerRequestOrigin(request *http.Request) (string, bool) {
	values := request.Header.Values("Origin")
	if len(values) != 1 {
		return "", false
	}
	origin, err := normalizeComposerOrigin(values[0])
	return origin, err == nil
}

func normalizeComposerOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		parsed.Opaque != "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" ||
		parsed.RawPath != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("composer origin must be an exact HTTP(S) origin")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func addComposerVaryOrigin(header http.Header) {
	for _, value := range header.Values("Vary") {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), "Origin") {
				return
			}
		}
	}
	header.Add("Vary", "Origin")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}
