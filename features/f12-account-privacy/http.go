package accountprivacy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type RequestAuthenticator interface {
	Principal(*http.Request) (Principal, bool)
}

type MutationRateLimiter interface {
	Allow(string, time.Time) (bool, time.Duration)
}

type HTTPHandler struct {
	service       *Service
	authenticator RequestAuthenticator
	limiter       MutationRateLimiter
	now           func() time.Time
}

type HTTPOption func(*HTTPHandler)

func WithHTTPRateLimiter(limiter MutationRateLimiter) HTTPOption {
	return func(handler *HTTPHandler) {
		handler.limiter = limiter
	}
}

func WithHTTPClock(clock func() time.Time) HTTPOption {
	return func(handler *HTTPHandler) {
		handler.now = clock
	}
}

func NewHTTPHandler(
	service *Service,
	authenticator RequestAuthenticator,
	options ...HTTPOption,
) (http.Handler, error) {
	if service == nil || authenticator == nil {
		return nil, fmtInvalidDependencies()
	}
	handler := &HTTPHandler{
		service:       service,
		authenticator: authenticator,
		limiter:       newDefaultAccountRateLimiter(),
		now:           time.Now,
	}
	for _, option := range options {
		option(handler)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/account", handler.accountArea)
	mux.HandleFunc("PATCH /api/v1/account/profile", handler.updateProfile)
	mux.HandleFunc("DELETE /api/v1/account/providers/{provider_id}", handler.disconnectProvider)
	mux.HandleFunc("POST /api/v1/account/exports", handler.requestExport)
	mux.HandleFunc("GET /api/v1/account/exports/{export_id}/download", handler.downloadExport)
	mux.HandleFunc("POST /api/v1/account/deletions", handler.requestDeletion)
	mux.HandleFunc("DELETE /api/v1/account/deletions/{request_id}", handler.cancelDeletion)
	return mux, nil
}

func (handler *HTTPHandler) accountArea(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	result, err := handler.service.AccountArea(request.Context(), principal)
	if err != nil {
		writeAccountError(writer, err)
		return
	}
	writeAccountJSON(writer, http.StatusOK, result)
}

func (handler *HTTPHandler) updateProfile(writer http.ResponseWriter, request *http.Request) {
	if !validMutationOrigin(writer, request) {
		return
	}
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input ProfileUpdate
	if err := decodeAccountJSON(writer, request, &input); err != nil {
		writeAccountError(writer, err)
		return
	}
	if !handler.allowRate(writer, principal, "profile.update") {
		return
	}
	profile, err := handler.service.UpdateProfile(request.Context(), principal, input)
	if err != nil {
		writeAccountError(writer, err)
		return
	}
	writeAccountJSON(writer, http.StatusOK, profile)
}

func (handler *HTTPHandler) disconnectProvider(writer http.ResponseWriter, request *http.Request) {
	if !validMutationOrigin(writer, request) {
		return
	}
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	providerID := request.PathValue("provider_id")
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeAccountJSON(writer, request, &input); err != nil {
		writeAccountError(writer, err)
		return
	}
	if input.Confirmation != providerID {
		writeAccountError(writer, fmtExplicitConfirmation())
		return
	}
	if !handler.allowRate(writer, principal, "provider.disconnect") {
		return
	}
	if err := handler.service.DisconnectProvider(request.Context(), principal, providerID); err != nil {
		writeAccountError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) requestExport(writer http.ResponseWriter, request *http.Request) {
	if !validMutationOrigin(writer, request) {
		return
	}
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input struct {
		Scope        ExportScope `json:"scope"`
		WorkspaceID  string      `json:"workspace_id"`
		Confirmation string      `json:"confirmation"`
	}
	if err := decodeAccountJSON(writer, request, &input); err != nil {
		writeAccountError(writer, err)
		return
	}
	if input.Confirmation != "EXPORT" {
		writeAccountError(writer, fmtExplicitConfirmation())
		return
	}
	if !handler.allowRate(writer, principal, "export.request") {
		return
	}
	result, err := handler.service.RequestExport(
		request.Context(),
		principal,
		input.Scope,
		input.WorkspaceID,
	)
	if err != nil {
		writeAccountError(writer, err)
		return
	}
	writeAccountJSON(writer, http.StatusAccepted, result)
}

func (handler *HTTPHandler) downloadExport(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	if !handler.allowRate(writer, principal, "export.download") {
		return
	}
	result, err := handler.service.DownloadExport(
		request.Context(),
		principal,
		request.PathValue("export_id"),
	)
	if err != nil {
		writeAccountError(writer, err)
		return
	}
	writeAccountJSON(writer, http.StatusOK, result)
}

func (handler *HTTPHandler) requestDeletion(writer http.ResponseWriter, request *http.Request) {
	if !validMutationOrigin(writer, request) {
		return
	}
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	var input struct {
		Scope        DeletionScope     `json:"scope"`
		WorkspaceID  string            `json:"workspace_id"`
		Actions      []OwnershipAction `json:"ownership_actions"`
		Confirmation string            `json:"confirmation"`
	}
	if err := decodeAccountJSON(writer, request, &input); err != nil {
		writeAccountError(writer, err)
		return
	}
	if input.Confirmation != "DELETE" {
		writeAccountError(writer, fmtExplicitConfirmation())
		return
	}
	if !handler.allowRate(writer, principal, "deletion.request") {
		return
	}
	result, err := handler.service.RequestDeletion(
		request.Context(),
		principal,
		input.Scope,
		input.WorkspaceID,
		input.Actions,
	)
	if err != nil {
		writeAccountError(writer, err)
		return
	}
	writeAccountJSON(writer, http.StatusAccepted, result)
}

func (handler *HTTPHandler) cancelDeletion(writer http.ResponseWriter, request *http.Request) {
	if !validMutationOrigin(writer, request) {
		return
	}
	principal, ok := handler.authenticate(writer, request)
	if !ok {
		return
	}
	if !handler.allowRate(writer, principal, "deletion.cancel") {
		return
	}
	if err := handler.service.CancelDeletion(
		request.Context(),
		principal,
		request.PathValue("request_id"),
	); err != nil {
		writeAccountError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) authenticate(
	writer http.ResponseWriter,
	request *http.Request,
) (Principal, bool) {
	principal, ok := handler.authenticator.Principal(request)
	if !ok || strings.TrimSpace(principal.AccountID) == "" {
		writeAccountError(writer, ErrUnauthenticated)
		return Principal{}, false
	}
	return principal, true
}

func (handler *HTTPHandler) allowRate(
	writer http.ResponseWriter,
	principal Principal,
	action string,
) bool {
	if handler.limiter == nil {
		return true
	}
	allowed, retryAfter := handler.limiter.Allow(
		principal.AccountID+":"+action,
		handler.now().UTC(),
	)
	if allowed {
		return true
	}
	writeRateLimited(writer, retryAfter)
	return false
}

func decodeAccountJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmtInvalidJSON(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmtInvalidJSON(err)
	}
	return nil
}

func validMutationOrigin(writer http.ResponseWriter, request *http.Request) bool {
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeAccountError(writer, fmtInvalidOrigin())
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") ||
		!strings.EqualFold(parsed.Host, request.Host) {
		writeAccountError(writer, fmtInvalidOrigin())
		return false
	}
	return true
}

func writeAccountError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, ErrReauthenticationRequired):
		status, code = http.StatusUnauthorized, "reauthentication_required"
	case errors.Is(err, ErrInvalidArgument):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrLastLoginProvider):
		status, code = http.StatusConflict, "last_login_provider"
	case errors.Is(err, ErrConflict), errors.Is(err, ErrDeletionInactive),
		errors.Is(err, ErrGracePeriodElapsed):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, ErrExportNotReady):
		status, code = http.StatusConflict, "export_not_ready"
	case errors.Is(err, ErrExportExpired):
		status, code = http.StatusGone, "export_expired"
	case errors.Is(err, ErrDeactivationIncomplete), errors.Is(err, ErrFinalizationIncomplete):
		status, code = http.StatusServiceUnavailable, "privacy_operation_incomplete"
	}
	writeAccountJSON(writer, status, map[string]string{"error": code})
}

func writeAccountJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}

func fmtInvalidDependencies() error {
	return errors.Join(ErrInvalidArgument, errors.New("service and authenticator are required"))
}

func fmtExplicitConfirmation() error {
	return errors.Join(ErrInvalidArgument, errors.New("explicit confirmation is required"))
}

func fmtInvalidJSON(err error) error {
	return errors.Join(ErrInvalidArgument, err)
}

func fmtInvalidOrigin() error {
	return errors.Join(ErrInvalidArgument, errors.New("invalid request origin"))
}

func writeRateLimited(writer http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds())
	if retryAfter <= 0 || time.Duration(seconds)*time.Second < retryAfter {
		seconds++
	}
	if seconds < 1 {
		seconds = 1
	}
	writer.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeAccountJSON(writer, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
}
