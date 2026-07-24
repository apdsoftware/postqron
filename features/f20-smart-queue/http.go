package smartqueue

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const maximumSmartQueueRequestBytes = 1 << 20

type RequestAuthenticator interface {
	AccountID(*http.Request) (string, bool)
}

type HTTPHandler struct {
	service       *Service
	authenticator RequestAuthenticator
}

func NewHTTPHandler(service *Service, authenticator RequestAuthenticator) http.Handler {
	handler := &HTTPHandler{service: service, authenticator: authenticator}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/workspaces/{workspace_id}/smart-queues", handler.createQueue)
	mux.HandleFunc("PUT /api/v1/workspaces/{workspace_id}/smart-queues/{queue_id}", handler.updateQueue)
	mux.HandleFunc("POST /api/v1/workspaces/{workspace_id}/smart-queues/{queue_id}/preview", handler.preview)
	mux.HandleFunc("POST /api/v1/workspaces/{workspace_id}/smart-queues/{queue_id}/confirm", handler.confirm)
	return mux
}

type queuePayload struct {
	Name             string            `json:"name"`
	TimeZone         string            `json:"time_zone"`
	IntervalMinutes  int               `json:"interval_minutes"`
	HorizonDays      int               `json:"horizon_days"`
	Windows          []RecurringWindow `json:"windows"`
	ExpectedRevision int64             `json:"expected_revision,omitempty"`
}

func (handler *HTTPHandler) createQueue(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload queuePayload
	if !decodeSmartQueueJSON(writer, request, &payload) {
		return
	}
	queue, err := handler.service.CreateQueue(request.Context(), CreateQueueCommand{
		WorkspaceID: request.PathValue("workspace_id"), ActorID: accountID,
		Name: payload.Name, TimeZone: payload.TimeZone,
		IntervalMinutes: payload.IntervalMinutes, HorizonDays: payload.HorizonDays,
		Windows: payload.Windows,
	})
	if err != nil {
		writeSmartQueueServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+queue.WorkspaceID+"/smart-queues/"+queue.ID,
	)
	writeSmartQueueJSON(writer, http.StatusCreated, queue)
}

func (handler *HTTPHandler) updateQueue(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload queuePayload
	if !decodeSmartQueueJSON(writer, request, &payload) {
		return
	}
	queue, err := handler.service.UpdateQueue(request.Context(), UpdateQueueCommand{
		WorkspaceID: request.PathValue("workspace_id"), ActorID: accountID,
		QueueID: request.PathValue("queue_id"), ExpectedRevision: payload.ExpectedRevision,
		Name: payload.Name, TimeZone: payload.TimeZone,
		IntervalMinutes: payload.IntervalMinutes, HorizonDays: payload.HorizonDays,
		Windows: payload.Windows,
	})
	if err != nil {
		writeSmartQueueServiceError(writer, err)
		return
	}
	writeSmartQueueJSON(writer, http.StatusOK, queue)
}

func (handler *HTTPHandler) preview(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		NotBeforeUTC *time.Time `json:"not_before_utc,omitempty"`
		UntilUTC     *time.Time `json:"until_utc,omitempty"`
	}
	if !decodeSmartQueueJSON(writer, request, &payload) {
		return
	}
	var notBefore time.Time
	if payload.NotBeforeUTC != nil {
		notBefore = payload.NotBeforeUTC.UTC()
	}
	preview, err := handler.service.Preview(request.Context(), PreviewCommand{
		WorkspaceID: request.PathValue("workspace_id"), ActorID: accountID,
		QueueID: request.PathValue("queue_id"), NotBeforeUTC: notBefore,
		UntilUTC: payload.UntilUTC,
	})
	if err != nil {
		writeSmartQueueServiceError(writer, err)
		return
	}
	writeSmartQueueJSON(writer, http.StatusOK, preview)
}

func (handler *HTTPHandler) confirm(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		PreviewToken   string   `json:"preview_token"`
		DraftID        string   `json:"draft_id"`
		ChannelIDs     []string `json:"channel_ids"`
		IdempotencyKey string   `json:"idempotency_key"`
	}
	if !decodeSmartQueueJSON(writer, request, &payload) {
		return
	}
	confirmation, err := handler.service.Confirm(request.Context(), ConfirmCommand{
		WorkspaceID: request.PathValue("workspace_id"), ActorID: accountID,
		QueueID: request.PathValue("queue_id"), PreviewToken: payload.PreviewToken,
		DraftID: payload.DraftID, ChannelIDs: payload.ChannelIDs,
		IdempotencyKey: payload.IdempotencyKey,
	})
	if err != nil {
		writeSmartQueueServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+confirmation.Reservation.WorkspaceID+
			"/smart-queue-reservations/"+confirmation.Reservation.ID,
	)
	writeSmartQueueJSON(writer, http.StatusCreated, confirmation.Reservation)
}

func (handler *HTTPHandler) accountID(
	writer http.ResponseWriter, request *http.Request,
) (string, bool) {
	if handler.authenticator == nil {
		writeSmartQueueError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return "", false
	}
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated || strings.TrimSpace(accountID) == "" {
		writeSmartQueueError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return "", false
	}
	return accountID, true
}

func decodeSmartQueueJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(
		writer, request.Body, maximumSmartQueueRequestBytes,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeSmartQueueError(writer, http.StatusBadRequest, "invalid_body", &FieldError{
			Field: "body", Rule: "json", Code: "body_invalid",
			Message: "Request body must be one valid JSON object with known fields.",
		})
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeSmartQueueError(writer, http.StatusBadRequest, "invalid_body", &FieldError{
			Field: "body", Rule: "single_json_value", Code: "body_invalid",
			Message: "Request body must contain exactly one JSON object.",
		})
		return false
	}
	return true
}

func writeSmartQueueServiceError(writer http.ResponseWriter, err error) {
	var fieldError *FieldError
	switch {
	case errors.As(err, &fieldError):
		writeSmartQueueError(writer, http.StatusBadRequest, fieldError.Code, fieldError)
	case errors.Is(err, ErrInvalidArgument):
		writeSmartQueueError(writer, http.StatusBadRequest, "invalid_argument", nil)
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrFeatureDisabled):
		writeSmartQueueError(writer, http.StatusForbidden, errorCode(err), nil)
	case errors.Is(err, ErrNotFound):
		writeSmartQueueError(writer, http.StatusNotFound, "not_found", nil)
	case errors.Is(err, ErrNoSlotAvailable):
		writeSmartQueueError(writer, http.StatusConflict, "no_slot_available", nil)
	case errors.Is(err, ErrSlotUnavailable):
		writeSmartQueueError(writer, http.StatusConflict, "slot_unavailable", nil)
	case errors.Is(err, ErrPreviewExpired):
		writeSmartQueueError(writer, http.StatusConflict, "preview_expired", nil)
	case errors.Is(err, ErrPreviewConsumed):
		writeSmartQueueError(writer, http.StatusConflict, "preview_consumed", nil)
	case errors.Is(err, ErrQueueChanged):
		writeSmartQueueError(writer, http.StatusConflict, "queue_changed", nil)
	case errors.Is(err, ErrCapacityExceeded):
		writeSmartQueueError(writer, http.StatusConflict, "capacity_exceeded", nil)
	case errors.Is(err, ErrIdempotencyReplay):
		writeSmartQueueError(writer, http.StatusConflict, "idempotency_mismatch", nil)
	case errors.Is(err, ErrConflict):
		writeSmartQueueError(writer, http.StatusConflict, "revision_conflict", nil)
	default:
		writeSmartQueueError(writer, http.StatusInternalServerError, "internal_error", nil)
	}
}

func errorCode(err error) string {
	if errors.Is(err, ErrFeatureDisabled) {
		return "feature_disabled"
	}
	return "forbidden"
}

func writeSmartQueueError(
	writer http.ResponseWriter, status int, code string, field *FieldError,
) {
	body := map[string]any{"error": map[string]any{"code": code}}
	if field != nil {
		body["error"].(map[string]any)["field"] = field.Field
		body["error"].(map[string]any)["rule"] = field.Rule
		body["error"].(map[string]any)["field_code"] = field.Code
		body["error"].(map[string]any)["message"] = field.Message
	}
	writeSmartQueueJSON(writer, status, body)
}

func writeSmartQueueJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
