package statusnotifications

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type RequestAuthenticator interface {
	AccountID(*http.Request) (string, error)
}

type HTTPHandler struct {
	service       *Service
	authenticator RequestAuthenticator
}

func NewHTTPHandler(
	service *Service,
	authenticator RequestAuthenticator,
) (*HTTPHandler, error) {
	if service == nil || authenticator == nil {
		return nil, ErrInvalidArgument
	}
	return &HTTPHandler{service: service, authenticator: authenticator}, nil
}

func (handler *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/publication-status/{post_id}",
		handler.getStatus,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/publication-status/{post_id}/destinations/{destination_id}/retry",
		handler.retryDestination,
	)
}

func (handler *HTTPHandler) getStatus(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, err := handler.authenticator.AccountID(request)
	if err != nil {
		writeError(writer, ErrForbidden)
		return
	}
	view, err := handler.service.GetStatus(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("post_id"),
	)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, view)
}

func (handler *HTTPHandler) retryDestination(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, err := handler.authenticator.AccountID(request)
	if err != nil {
		writeError(writer, ErrForbidden)
		return
	}
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(writer, ErrInvalidArgument)
		return
	}
	result, err := handler.service.RequestManualRetry(
		request.Context(),
		ManualRetryRequest{
			WorkspaceID:    request.PathValue("workspace_id"),
			PostID:         request.PathValue("post_id"),
			DestinationID:  request.PathValue("destination_id"),
			ActorID:        accountID,
			IdempotencyKey: strings.TrimSpace(body.IdempotencyKey),
		},
	)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, result)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	switch {
	case errors.Is(err, ErrInvalidArgument):
		status, code = http.StatusBadRequest, "invalid_request"
	case errors.Is(err, ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrConflict):
		status, code = http.StatusConflict, "conflict"
	}
	writeJSON(writer, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"retryable": status >= 500,
		},
	})
}
