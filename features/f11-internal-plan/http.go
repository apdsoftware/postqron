package internalplan

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxInternalRequestBytes = 4 << 10

type RequestAuthenticator interface {
	Principal(*http.Request) (Principal, bool)
}

// NewInternalHTTPHandler returns routes for a private administration listener.
// It must never be mounted on the public API mux.
func NewInternalHTTPHandler(
	service *Service,
	authenticator RequestAuthenticator,
) http.Handler {
	handler := &internalHTTPHandler{
		service:       service,
		authenticator: authenticator,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(
		"PUT /internal/v1/workspaces/{workspace_id}/entitlement-override",
		handler.assign,
	)
	mux.HandleFunc(
		"DELETE /internal/v1/workspaces/{workspace_id}/entitlement-override",
		handler.revoke,
	)
	return mux
}

type internalHTTPHandler struct {
	service       *Service
	authenticator RequestAuthenticator
}

func (handler *internalHTTPHandler) assign(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, authenticated := handler.authenticator.Principal(request)
	if !authenticated {
		writeInternalError(writer, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var payload struct {
		TargetAccountID string `json:"target_account_id"`
	}
	if err := decodeInternalPayload(writer, request, &payload); err != nil {
		if auditErr := handler.service.auditRejected(
			request.Context(),
			principal,
			ActionAssign,
			request.PathValue("workspace_id"),
			request.Header.Get("X-Correlation-ID"),
		); auditErr != nil {
			writeInternalError(writer, http.StatusServiceUnavailable, "service_unavailable")
			return
		}
		writeInternalError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := handler.service.Assign(request.Context(), principal, AssignmentRequest{
		WorkspaceID:     request.PathValue("workspace_id"),
		TargetAccountID: payload.TargetAccountID,
		CorrelationID:   request.Header.Get("X-Correlation-ID"),
	})
	handler.writeResult(writer, result, err)
}

func (handler *internalHTTPHandler) revoke(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, authenticated := handler.authenticator.Principal(request)
	if !authenticated {
		writeInternalError(writer, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if request.Body != nil && request.ContentLength != 0 {
		var payload struct{}
		if err := decodeInternalPayload(writer, request, &payload); err != nil {
			if auditErr := handler.service.auditRejected(
				request.Context(),
				principal,
				ActionRevoke,
				request.PathValue("workspace_id"),
				request.Header.Get("X-Correlation-ID"),
			); auditErr != nil {
				writeInternalError(
					writer,
					http.StatusServiceUnavailable,
					"service_unavailable",
				)
				return
			}
			writeInternalError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
	}
	result, err := handler.service.Revoke(request.Context(), principal, RevocationRequest{
		WorkspaceID:   request.PathValue("workspace_id"),
		CorrelationID: request.Header.Get("X-Correlation-ID"),
	})
	handler.writeResult(writer, result, err)
}

func (handler *internalHTTPHandler) writeResult(
	writer http.ResponseWriter,
	result ChangeResult,
	err error,
) {
	if err == nil {
		writeInternalJSON(writer, http.StatusOK, result)
		return
	}
	switch {
	case errors.Is(err, ErrInvalidRequest):
		writeInternalError(writer, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, ErrStrongAuthenticationRequired),
		errors.Is(err, ErrAdminRequired),
		errors.Is(err, ErrTargetNotAllowlisted),
		errors.Is(err, ErrActiveBindingConflict):
		// Keep admin, allowlist, and binding denials indistinguishable.
		writeInternalError(writer, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrAuthorizationUnavailable),
		errors.Is(err, ErrAuditUnavailable),
		errors.Is(err, ErrInternalPlanUnavailable):
		writeInternalError(writer, http.StatusServiceUnavailable, "service_unavailable")
	default:
		writeInternalError(writer, http.StatusInternalServerError, "internal_error")
	}
}

func decodeInternalPayload(
	writer http.ResponseWriter,
	request *http.Request,
	destination any,
) error {
	decoder := json.NewDecoder(http.MaxBytesReader(
		writer,
		request.Body,
		maxInternalRequestBytes,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeInternalJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeInternalError(writer http.ResponseWriter, status int, code string) {
	writeInternalJSON(writer, status, map[string]string{"error": code})
}
