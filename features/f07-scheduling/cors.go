package scheduling

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func parseSchedulingAllowedOrigins(raw string) (map[string]struct{}, error) {
	origins := make(map[string]struct{})
	for _, candidate := range strings.Split(raw, ",") {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		origin, err := normalizeSchedulingOrigin(candidate)
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

func normalizeSchedulingOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", ErrInvalidArgument
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func schedulingRequestOrigin(request *http.Request) (string, bool) {
	values := request.Header.Values("Origin")
	if len(values) != 1 {
		return "", false
	}
	origin, err := normalizeSchedulingOrigin(values[0])
	return origin, err == nil
}

func credentialedSchedulingCORS(
	next http.Handler,
	allowedOrigins map[string]struct{},
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(request.Header.Values("Origin")) == 0 {
			next.ServeHTTP(writer, request)
			return
		}
		origin, ok := schedulingRequestOrigin(request)
		if !ok {
			writeSchedulingError(
				writer,
				http.StatusForbidden,
				"origin_forbidden",
				nil,
			)
			return
		}
		if _, allowed := allowedOrigins[origin]; !allowed {
			writeSchedulingError(
				writer,
				http.StatusForbidden,
				"origin_forbidden",
				nil,
			)
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		writer.Header().Set(
			"Access-Control-Expose-Headers",
			"Location, Idempotency-Replayed",
		)
		addSchedulingVaryOrigin(writer.Header())
		next.ServeHTTP(writer, request)
	})
}

func addSchedulingVaryOrigin(header http.Header) {
	for _, value := range header.Values("Vary") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "Origin") {
				return
			}
		}
	}
	header.Add("Vary", "Origin")
}

func (handler *HTTPHandler) validMutationOrigin(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeSchedulingError(writer, http.StatusForbidden, "origin_forbidden", nil)
		return false
	}
	if len(request.Header.Values("Origin")) == 0 {
		return true
	}
	origin, ok := schedulingRequestOrigin(request)
	if !ok {
		writeSchedulingError(writer, http.StatusForbidden, "origin_forbidden", nil)
		return false
	}
	if _, allowed := handler.origins[origin]; !allowed {
		writeSchedulingError(writer, http.StatusForbidden, "origin_forbidden", nil)
		return false
	}
	return true
}
