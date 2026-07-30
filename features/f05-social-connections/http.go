package socialconnections

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maximumSocialRequestBytes = 32 << 10

type RequestAuthenticator interface {
	AccountID(*http.Request) (string, bool)
}

type HTTPHandler struct {
	service       *Service
	authenticator RequestAuthenticator
	origins       map[string]struct{}
	handler       http.Handler
}

func NewHTTPHandler(
	service *Service,
	authenticator RequestAuthenticator,
	allowedOrigins ...string,
) (http.Handler, error) {
	if service == nil || authenticator == nil {
		return nil, fmt.Errorf("%w: HTTP dependencies are required", ErrInvalidArgument)
	}
	origins, err := newSocialOriginPolicy(allowedOrigins)
	if err != nil {
		return nil, err
	}
	handler := &HTTPHandler{
		service:       service,
		authenticator: authenticator,
		origins:       origins,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/social-connections/bootstrap",
		handler.bootstrap,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/social-authorizations",
		handler.beginAuthorization,
	)
	mux.HandleFunc(
		"GET /api/v1/social-authorizations/callback",
		handler.callback,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/social-connections",
		handler.listConnections,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/social-connections",
		handler.selectResource,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/social-connections/{connection_id}/reconnect",
		handler.reconnect,
	)
	mux.HandleFunc(
		"DELETE /api/v1/workspaces/{workspace_id}/social-connections/{connection_id}",
		handler.revoke,
	)
	handler.handler = securityHeaders(credentialedSocialCORS(mux, origins))
	return handler, nil
}

func (handler *HTTPHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	handler.handler.ServeHTTP(writer, request)
}

func (handler *HTTPHandler) bootstrap(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	bootstrap, err := handler.service.BootstrapForWorkspace(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
	)
	if err != nil {
		writeSocialServiceError(writer, err)
		return
	}
	writeSocialJSON(writer, http.StatusOK, bootstrap)
}

func (handler *HTTPHandler) beginAuthorization(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var input struct {
		Provider Provider `json:"provider"`
	}
	if !decodeSocialJSON(writer, request, &input) {
		return
	}
	authorization, err := handler.service.Begin(
		request.Context(),
		BeginRequest{
			WorkspaceID: request.PathValue("workspace_id"),
			ActorID:     accountID,
			Provider:    input.Provider,
		},
	)
	if err != nil {
		writeSocialServiceError(writer, err)
		return
	}
	writeSocialJSON(writer, http.StatusCreated, authorization)
}

func (handler *HTTPHandler) callback(
	writer http.ResponseWriter,
	request *http.Request,
) {
	selection, err := handler.service.Callback(
		request.Context(),
		CallbackRequest{
			State:         request.URL.Query().Get("state"),
			Code:          request.URL.Query().Get("code"),
			ProviderError: request.URL.Query().Get("error"),
		},
	)
	if err != nil {
		writeSocialServiceError(writer, err)
		return
	}
	writeSocialJSON(writer, http.StatusOK, selection)
}

func (handler *HTTPHandler) listConnections(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	connections, err := handler.service.List(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
	)
	if err != nil {
		writeSocialServiceError(writer, err)
		return
	}
	if connections == nil {
		connections = []Connection{}
	}
	writeSocialJSON(
		writer,
		http.StatusOK,
		map[string][]Connection{"connections": connections},
	)
}

func (handler *HTTPHandler) selectResource(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var input struct {
		SelectionID string `json:"selection_id"`
		RemoteID    string `json:"remote_id"`
	}
	if !decodeSocialJSON(writer, request, &input) {
		return
	}
	connection, err := handler.service.Select(
		request.Context(),
		SelectRequest{
			WorkspaceID: request.PathValue("workspace_id"),
			ActorID:     accountID,
			SelectionID: input.SelectionID,
			RemoteID:    input.RemoteID,
		},
	)
	if err != nil {
		writeSocialServiceError(writer, err)
		return
	}
	writeSocialJSON(writer, http.StatusCreated, connection)
}

func (handler *HTTPHandler) reconnect(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	authorization, err := handler.service.BeginReconnect(
		request.Context(),
		ReconnectRequest{
			WorkspaceID:  request.PathValue("workspace_id"),
			ActorID:      accountID,
			ConnectionID: request.PathValue("connection_id"),
		},
	)
	if err != nil {
		writeSocialServiceError(writer, err)
		return
	}
	writeSocialJSON(writer, http.StatusCreated, authorization)
}

func (handler *HTTPHandler) revoke(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	result, err := handler.service.Revoke(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("connection_id"),
	)
	if err != nil {
		writeSocialServiceError(writer, err)
		return
	}
	writeSocialJSON(writer, http.StatusOK, map[string]any{
		"connection":       result.Connection,
		"provider_revoked": result.ProviderRevoked,
	})
}

func (handler *HTTPHandler) accountID(
	writer http.ResponseWriter,
	request *http.Request,
) (string, bool) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated || strings.TrimSpace(accountID) == "" {
		writeSocialError(
			writer,
			http.StatusUnauthorized,
			"unauthenticated",
			false,
		)
		return "", false
	}
	return accountID, true
}

func decodeSocialJSON(
	writer http.ResponseWriter,
	request *http.Request,
	target any,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(
		writer,
		request.Body,
		maximumSocialRequestBytes,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeSocialError(writer, http.StatusBadRequest, "invalid_request", false)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeSocialError(writer, http.StatusBadRequest, "invalid_request", false)
		return false
	}
	return true
}

func writeSocialServiceError(writer http.ResponseWriter, err error) {
	var providerFailure *ProviderFailure
	switch {
	case errors.Is(err, ErrUnauthorized):
		writeSocialError(writer, http.StatusForbidden, "forbidden", false)
	case errors.Is(err, ErrProviderUnavailable):
		writeSocialError(
			writer,
			http.StatusServiceUnavailable,
			"provider_unavailable",
			false,
		)
	case errors.Is(err, ErrChannelQuotaExceeded):
		writeSocialError(
			writer,
			http.StatusConflict,
			"channel_quota_exceeded",
			false,
		)
	case errors.Is(err, ErrChannelQuotaUnavailable):
		writeSocialError(
			writer,
			http.StatusServiceUnavailable,
			"channel_quota_unavailable",
			true,
		)
	case errors.Is(err, ErrResourceNotFound):
		writeSocialError(writer, http.StatusNotFound, "resource_not_found", false)
	case errors.Is(err, ErrFlowExpired):
		writeSocialError(writer, http.StatusGone, "flow_expired", true)
	case errors.Is(err, ErrResourceAlreadyUsed):
		writeSocialError(writer, http.StatusConflict, "resource_already_connected", false)
	case errors.Is(err, ErrInvalidState):
		writeSocialError(writer, http.StatusConflict, "invalid_oauth_state", false)
	case errors.Is(err, ErrProviderDenied):
		writeSocialError(writer, http.StatusBadRequest, "provider_denied", true)
	case errors.Is(err, ErrNoResources):
		writeSocialError(writer, http.StatusUnprocessableEntity, "no_publishable_resources", true)
	case errors.Is(err, ErrUnsupportedProvider), errors.Is(err, ErrInvalidArgument):
		writeSocialError(writer, http.StatusBadRequest, "invalid_request", false)
	case errors.As(err, &providerFailure):
		status, code, retryable := providerFailureResponse(providerFailure)
		writeSocialError(
			writer,
			status,
			code,
			retryable,
		)
	default:
		writeSocialError(writer, http.StatusInternalServerError, "internal_error", true)
	}
}

func providerFailureResponse(
	failure *ProviderFailure,
) (status int, code string, retryable bool) {
	switch failure.Kind {
	case FailureTemporary:
		return http.StatusBadGateway, "provider_temporary", true
	case FailureAuthentication, FailurePermissionMissing:
		return http.StatusUnprocessableEntity, "provider_access_denied", false
	case FailureResourceGone:
		return http.StatusUnprocessableEntity, "provider_resource_unavailable", false
	default:
		return http.StatusBadGateway, "provider_invalid_response", false
	}
}

func writeSocialError(
	writer http.ResponseWriter,
	status int,
	code string,
	retryable bool,
) {
	writeSocialJSON(writer, status, map[string]any{
		"code":      code,
		"message":   http.StatusText(status),
		"retryable": retryable,
	})
}

func writeSocialJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func (handler *HTTPHandler) validMutationOrigin(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	origin, ok := socialRequestOrigin(request)
	if !ok {
		writeSocialError(writer, http.StatusForbidden, "origin_forbidden", false)
		return false
	}
	if _, allowed := handler.origins[origin]; !allowed {
		writeSocialError(writer, http.StatusForbidden, "origin_forbidden", false)
		return false
	}
	return true
}

func credentialedSocialCORS(
	next http.Handler,
	allowedOrigins map[string]struct{},
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if len(request.Header.Values("Origin")) == 0 {
			next.ServeHTTP(writer, request)
			return
		}
		origin, ok := socialRequestOrigin(request)
		if !ok {
			writeSocialError(writer, http.StatusForbidden, "origin_forbidden", false)
			return
		}
		if _, allowed := allowedOrigins[origin]; !allowed {
			writeSocialError(writer, http.StatusForbidden, "origin_forbidden", false)
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		addSocialVaryOrigin(writer.Header())
		if request.Method == http.MethodOptions {
			writer.Header().Set(
				"Access-Control-Allow-Methods",
				"GET, POST, DELETE, OPTIONS",
			)
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			writer.Header().Set("Access-Control-Max-Age", "600")
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func parseSocialAllowedOrigins(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	origins := make([]string, 0)
	seen := make(map[string]struct{})
	for _, candidate := range strings.Split(raw, ",") {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		origin, err := normalizeSocialOrigin(candidate)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: POSTQRON_AUTH_ALLOWED_ORIGINS contains an invalid origin",
				ErrInvalidArgument,
			)
		}
		if _, duplicate := seen[origin]; duplicate {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins, nil
}

func newSocialOriginPolicy(origins []string) (map[string]struct{}, error) {
	policy := make(map[string]struct{}, len(origins))
	for _, candidate := range origins {
		origin, err := normalizeSocialOrigin(candidate)
		if err != nil {
			return nil, fmt.Errorf("%w: social allowed origin is invalid", ErrInvalidArgument)
		}
		policy[origin] = struct{}{}
	}
	return policy, nil
}

func socialRequestOrigin(request *http.Request) (string, bool) {
	values := request.Header.Values("Origin")
	if len(values) != 1 {
		return "", false
	}
	origin, err := normalizeSocialOrigin(values[0])
	return origin, err == nil
}

func normalizeSocialOrigin(value string) (string, error) {
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
		return "", errors.New("social origin must be an exact HTTP(S) origin")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func addSocialVaryOrigin(header http.Header) {
	for _, value := range header.Values("Vary") {
		for _, field := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(field), "Origin") {
				return
			}
		}
	}
	header.Add("Vary", "Origin")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(writer, request)
	})
}
