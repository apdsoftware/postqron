package workspaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const maxRuntimeRequestBytes = 64 << 10

type RuntimeHTTPHandler struct {
	service        *RuntimeService
	authenticator  SessionAuthenticator
	allowedOrigins map[string]struct{}
	handler        http.Handler
}

func NewRuntimeHTTPHandler(
	service *RuntimeService,
	authenticator SessionAuthenticator,
	allowedOrigins ...string,
) (http.Handler, error) {
	if service == nil || authenticator == nil {
		return nil, errors.New("workspace runtime HTTP dependencies are required")
	}
	policy, err := newAppOriginPolicy(allowedOrigins)
	if err != nil {
		return nil, err
	}
	handler := &RuntimeHTTPHandler{
		service:        service,
		authenticator:  authenticator,
		allowedOrigins: policy,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/app/onboarding", handler.completeOnboarding)
	mux.HandleFunc("OPTIONS /api/v1/app/onboarding", handler.preflight)
	mux.HandleFunc("POST /api/v1/app/workspaces/select", handler.selectWorkspace)
	mux.HandleFunc("OPTIONS /api/v1/app/workspaces/select", handler.preflight)
	mux.HandleFunc("GET /api/v1/app/workspaces/current", handler.currentWorkspace)
	mux.HandleFunc("PATCH /api/v1/app/workspaces/current", handler.renameCurrentWorkspace)
	mux.HandleFunc("OPTIONS /api/v1/app/workspaces/current", handler.preflight)
	mux.HandleFunc("GET /api/v1/app/workspaces/current/members", handler.currentMemberships)
	mux.HandleFunc("OPTIONS /api/v1/app/workspaces/current/members", handler.preflight)
	mux.HandleFunc("POST /api/v1/app/workspaces/current/invitations", handler.inviteCurrentMember)
	mux.HandleFunc("OPTIONS /api/v1/app/workspaces/current/invitations", handler.preflight)
	mux.HandleFunc(
		"PUT /api/v1/app/workspaces/current/members/{memberId}/role",
		handler.changeCurrentMemberRole,
	)
	mux.HandleFunc(
		"OPTIONS /api/v1/app/workspaces/current/members/{memberId}/role",
		handler.preflight,
	)
	mux.HandleFunc(
		"DELETE /api/v1/app/workspaces/current/members/{memberId}",
		handler.removeCurrentMember,
	)
	mux.HandleFunc(
		"OPTIONS /api/v1/app/workspaces/current/members/{memberId}",
		handler.preflight,
	)
	handler.handler = handler.cors(mux)
	return handler, nil
}

func (handler *RuntimeHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.handler.ServeHTTP(writer, request)
}

func (handler *RuntimeHTTPHandler) completeOnboarding(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	account, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input struct {
		Consents  []OnboardingConsentReceipt `json:"consents"`
		Workspace OnboardingWorkspaceInput   `json:"workspace"`
	}
	if err := decodeRuntimeJSON(writer, request, &input); err != nil {
		writeRuntimeError(writer, err)
		return
	}
	session, created, err := handler.service.CompleteOnboarding(
		request.Context(),
		CompleteOnboardingCommand{
			Account:   account,
			Consents:  input.Consents,
			Workspace: input.Workspace,
		},
	)
	if err != nil {
		writeRuntimeError(writer, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeRuntimeJSON(writer, status, session)
}

func (handler *RuntimeHTTPHandler) selectWorkspace(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	account, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := decodeRuntimeJSON(writer, request, &input); err != nil {
		writeRuntimeError(writer, err)
		return
	}
	if err := handler.service.SelectWorkspace(request.Context(), account, input.WorkspaceID); err != nil {
		writeRuntimeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *RuntimeHTTPHandler) currentWorkspace(
	writer http.ResponseWriter,
	request *http.Request,
) {
	account, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	workspace, err := handler.service.CurrentWorkspace(request.Context(), account.ID)
	if err != nil {
		writeRuntimeError(writer, err)
		return
	}
	writeRuntimeJSON(writer, http.StatusOK, workspace)
}

func (handler *RuntimeHTTPHandler) renameCurrentWorkspace(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	account, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeRuntimeJSON(writer, request, &input); err != nil {
		writeRuntimeError(writer, err)
		return
	}
	workspace, err := handler.service.RenameCurrentWorkspace(
		request.Context(),
		account.ID,
		input.Name,
	)
	if err != nil {
		writeRuntimeError(writer, err)
		return
	}
	writeRuntimeJSON(writer, http.StatusOK, workspace)
}

func (handler *RuntimeHTTPHandler) currentMemberships(
	writer http.ResponseWriter,
	request *http.Request,
) {
	account, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	memberships, err := handler.service.CurrentMembers(request.Context(), account.ID)
	if err != nil {
		writeRuntimeError(writer, err)
		return
	}
	writeRuntimeJSON(writer, http.StatusOK, memberships)
}

func (handler *RuntimeHTTPHandler) inviteCurrentMember(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	account, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if err := decodeRuntimeJSON(writer, request, &input); err != nil {
		writeRuntimeError(writer, err)
		return
	}
	invitation, err := handler.service.InviteCurrentMember(
		request.Context(),
		account.ID,
		input.Email,
	)
	if err != nil {
		writeRuntimeError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	status := http.StatusCreated
	if invitation.Reissued {
		status = http.StatusOK
	}
	writeRuntimeJSON(writer, status, invitation)
}

func (handler *RuntimeHTTPHandler) changeCurrentMemberRole(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	account, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input struct {
		Role Role `json:"role"`
	}
	if err := decodeRuntimeJSON(writer, request, &input); err != nil {
		writeRuntimeError(writer, err)
		return
	}
	if err := handler.service.ChangeCurrentMemberRole(
		request.Context(),
		account.ID,
		request.PathValue("memberId"),
		input.Role,
	); err != nil {
		writeRuntimeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *RuntimeHTTPHandler) removeCurrentMember(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !handler.validMutationOrigin(writer, request) {
		return
	}
	account, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	if err := handler.service.RemoveCurrentMember(
		request.Context(),
		account.ID,
		request.PathValue("memberId"),
	); err != nil {
		writeRuntimeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *RuntimeHTTPHandler) authenticate(
	writer http.ResponseWriter,
	request *http.Request,
) (AppSessionAccount, bool) {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		writeRuntimeAppError(writer, http.StatusUnauthorized, "APP_UNAUTHENTICATED", false)
		return AppSessionAccount{}, false
	}
	account, err := handler.authenticator.Session(request.Context(), cookie.Value)
	if err != nil {
		writeRuntimeError(writer, err)
		return AppSessionAccount{}, false
	}
	return account, true
}

func (handler *RuntimeHTTPHandler) preflight(
	writer http.ResponseWriter,
	_ *http.Request,
) {
	writer.Header().Set(
		"Access-Control-Allow-Methods",
		"GET, PATCH, POST, PUT, DELETE, OPTIONS",
	)
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
	writer.Header().Set("Access-Control-Max-Age", "600")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *RuntimeHTTPHandler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		rawOrigin := request.Header.Get("Origin")
		if rawOrigin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		origin, err := normalizeAppOrigin(rawOrigin)
		if err != nil || !handler.originAllowed(request, origin) {
			writeRuntimeAppError(writer, http.StatusForbidden, "APP_ORIGIN_FORBIDDEN", false)
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		writer.Header().Add("Vary", "Origin")
		if request.Method == http.MethodOptions {
			handler.preflight(writer, request)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func (handler *RuntimeHTTPHandler) validMutationOrigin(
	writer http.ResponseWriter,
	request *http.Request,
) bool {
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeRuntimeAppError(writer, http.StatusForbidden, "APP_ORIGIN_FORBIDDEN", false)
		return false
	}
	rawOrigin := request.Header.Get("Origin")
	if rawOrigin == "" {
		return true
	}
	origin, err := normalizeAppOrigin(rawOrigin)
	if err != nil || !handler.originAllowed(request, origin) {
		writeRuntimeAppError(writer, http.StatusForbidden, "APP_ORIGIN_FORBIDDEN", false)
		return false
	}
	return true
}

func (handler *RuntimeHTTPHandler) originAllowed(request *http.Request, origin string) bool {
	if _, allowed := handler.allowedOrigins[origin]; allowed {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, request.Host)
}

func parseAppAllowedOrigins(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var origins []string
	for _, candidate := range strings.Split(raw, ",") {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		origin, err := normalizeAppOrigin(candidate)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[origin]; duplicate {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	return origins, nil
}

func newAppOriginPolicy(origins []string) (map[string]struct{}, error) {
	policy := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		normalized, err := normalizeAppOrigin(origin)
		if err != nil {
			return nil, err
		}
		policy[normalized] = struct{}{}
	}
	return policy, nil
}

func normalizeAppOrigin(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("origin is invalid")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func decodeRuntimeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRuntimeRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: malformed JSON body: %v", ErrInvalidArgument, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: request body must contain one JSON value", ErrInvalidArgument)
	}
	return nil
}

func writeRuntimeError(writer http.ResponseWriter, err error) {
	var stateError interface{ SQLState() string }
	switch {
	case errors.Is(err, ErrUnauthenticated):
		writeRuntimeAppError(writer, http.StatusUnauthorized, "APP_UNAUTHENTICATED", false)
	case errors.Is(err, ErrForbidden):
		writeRuntimeAppError(writer, http.StatusForbidden, "APP_ACCESS_DENIED", false)
	case errors.Is(err, ErrNotFound):
		writeRuntimeAppError(writer, http.StatusNotFound, "APP_WORKSPACE_NOT_FOUND", false)
	case errors.Is(err, ErrInvalidConsentReceipt):
		writeRuntimeAppError(writer, http.StatusBadRequest, "APP_INVALID_CONSENT", false)
	case errors.Is(err, ErrConsentOutdated):
		writeRuntimeAppError(writer, http.StatusConflict, "APP_ONBOARDING_CONFLICT", false)
	case errors.Is(err, ErrLastOwner):
		writeRuntimeAppError(writer, http.StatusConflict, "APP_LAST_OWNER", false)
	case errors.Is(err, ErrMemberLimitReached):
		writeRuntimeAppError(writer, http.StatusConflict, "APP_MEMBER_LIMIT_REACHED", false)
	case errors.Is(err, ErrInvitationExpired),
		errors.Is(err, ErrInvitationRevoked),
		errors.Is(err, ErrInvitationAccepted),
		errors.Is(err, ErrEmailMismatch):
		writeRuntimeAppError(writer, http.StatusConflict, "APP_INVITATION_CONFLICT", false)
	case errors.Is(err, ErrWorkspaceInactive), errors.Is(err, ErrConflict):
		writeRuntimeAppError(writer, http.StatusConflict, "APP_WORKSPACE_CONFLICT", false)
	case errors.As(err, &stateError) &&
		(stateError.SQLState() == "40001" || stateError.SQLState() == "40P01"):
		writeRuntimeAppError(writer, http.StatusConflict, "APP_CONCURRENT_UPDATE", true)
	case errors.Is(err, ErrRuntimeUnavailable),
		errors.Is(err, ErrEntitlementUnavailable):
		writeRuntimeAppError(writer, http.StatusServiceUnavailable, "APP_CONFIGURATION_UNAVAILABLE", true)
	case errors.Is(err, ErrInvalidArgument):
		writeRuntimeAppError(writer, http.StatusBadRequest, "APP_INVALID_REQUEST", false)
	default:
		writeRuntimeAppError(writer, http.StatusServiceUnavailable, "APP_RUNTIME_UNAVAILABLE", true)
	}
}

func writeRuntimeAppError(
	writer http.ResponseWriter,
	status int,
	code string,
	retryable bool,
) {
	writeRuntimeJSON(writer, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   "The app service request failed.",
			"retryable": retryable,
		},
	})
}

func writeRuntimeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

type fixedSessionAuthenticator struct {
	account AppSessionAccount
	err     error
}

func (authenticator fixedSessionAuthenticator) Session(
	context.Context,
	string,
) (AppSessionAccount, error) {
	if authenticator.err != nil {
		return AppSessionAccount{}, authenticator.err
	}
	return authenticator.account, nil
}
