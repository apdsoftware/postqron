package scheduling

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

const maximumSchedulingRequestBytes = 1 << 20

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
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/calendar",
		handler.calendar,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/scheduled-posts",
		handler.schedule,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/scheduled-posts/{post_id}",
		handler.getPost,
	)
	mux.HandleFunc(
		"PUT /api/v1/workspaces/{workspace_id}/scheduled-posts/{post_id}",
		handler.edit,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/scheduled-posts/{post_id}/reschedule",
		handler.reschedule,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/scheduled-posts/{post_id}/duplicate",
		handler.duplicate,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/scheduled-posts/{post_id}/cancel",
		handler.cancel,
	)
	return mux
}

func (handler *HTTPHandler) schedule(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		DraftID    string        `json:"draft_id"`
		ChannelIDs []string      `json:"channel_ids"`
		Scheduled  ScheduleInput `json:"scheduled_at"`
	}
	if !decodeSchedulingJSON(writer, request, &payload) {
		return
	}
	post, err := handler.service.SchedulePost(request.Context(), SchedulePostCommand{
		WorkspaceID: request.PathValue("workspace_id"),
		ActorID:     accountID,
		DraftID:     payload.DraftID,
		ChannelIDs:  payload.ChannelIDs,
		Schedule:    payload.Scheduled,
	})
	if err != nil {
		writeSchedulingServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+post.WorkspaceID+"/scheduled-posts/"+post.ID,
	)
	writeSchedulingJSON(writer, http.StatusCreated, post)
}

func (handler *HTTPHandler) getPost(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	post, err := handler.service.GetPost(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("post_id"),
	)
	if err != nil {
		writeSchedulingServiceError(writer, err)
		return
	}
	writeSchedulingJSON(writer, http.StatusOK, post)
}

func (handler *HTTPHandler) calendar(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	filter, err := calendarFilterFromRequest(request)
	if err != nil {
		writeSchedulingServiceError(writer, err)
		return
	}
	entries, err := handler.service.Calendar(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		filter,
	)
	if err != nil {
		writeSchedulingServiceError(writer, err)
		return
	}
	writeSchedulingJSON(
		writer,
		http.StatusOK,
		map[string][]CalendarEntry{"entries": entries},
	)
}

func (handler *HTTPHandler) edit(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ExpectedRevision int64    `json:"expected_revision"`
		DraftID          string   `json:"draft_id"`
		ChannelIDs       []string `json:"channel_ids"`
	}
	if !decodeSchedulingJSON(writer, request, &payload) {
		return
	}
	post, err := handler.service.EditPost(request.Context(), EditPostCommand{
		WorkspaceID:      request.PathValue("workspace_id"),
		ActorID:          accountID,
		PostID:           request.PathValue("post_id"),
		ExpectedRevision: payload.ExpectedRevision,
		DraftID:          payload.DraftID,
		ChannelIDs:       payload.ChannelIDs,
	})
	if err != nil {
		writeSchedulingServiceError(writer, err)
		return
	}
	writeSchedulingJSON(writer, http.StatusOK, post)
}

func (handler *HTTPHandler) reschedule(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ExpectedRevision int64         `json:"expected_revision"`
		Scheduled        ScheduleInput `json:"scheduled_at"`
	}
	if !decodeSchedulingJSON(writer, request, &payload) {
		return
	}
	post, err := handler.service.ReschedulePost(
		request.Context(),
		ReschedulePostCommand{
			WorkspaceID:      request.PathValue("workspace_id"),
			ActorID:          accountID,
			PostID:           request.PathValue("post_id"),
			ExpectedRevision: payload.ExpectedRevision,
			Schedule:         payload.Scheduled,
		},
	)
	if err != nil {
		writeSchedulingServiceError(writer, err)
		return
	}
	writeSchedulingJSON(writer, http.StatusOK, post)
}

func (handler *HTTPHandler) duplicate(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ExpectedRevision int64          `json:"expected_revision"`
		Scheduled        *ScheduleInput `json:"scheduled_at,omitempty"`
	}
	if !decodeSchedulingJSON(writer, request, &payload) {
		return
	}
	post, err := handler.service.DuplicatePost(
		request.Context(),
		DuplicatePostCommand{
			WorkspaceID:      request.PathValue("workspace_id"),
			ActorID:          accountID,
			PostID:           request.PathValue("post_id"),
			ExpectedRevision: payload.ExpectedRevision,
			Schedule:         payload.Scheduled,
		},
	)
	if err != nil {
		writeSchedulingServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+post.WorkspaceID+"/scheduled-posts/"+post.ID,
	)
	writeSchedulingJSON(writer, http.StatusCreated, post)
}

func (handler *HTTPHandler) cancel(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeSchedulingJSON(writer, request, &payload) {
		return
	}
	post, err := handler.service.CancelPost(request.Context(), CancelPostCommand{
		WorkspaceID:      request.PathValue("workspace_id"),
		ActorID:          accountID,
		PostID:           request.PathValue("post_id"),
		ExpectedRevision: payload.ExpectedRevision,
	})
	if err != nil {
		writeSchedulingServiceError(writer, err)
		return
	}
	writeSchedulingJSON(writer, http.StatusOK, post)
}

func (handler *HTTPHandler) accountID(
	writer http.ResponseWriter,
	request *http.Request,
) (string, bool) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeSchedulingError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return "", false
	}
	return accountID, true
}

func calendarFilterFromRequest(request *http.Request) (CalendarFilter, error) {
	query := request.URL.Query()
	from, err := time.Parse(time.RFC3339, query.Get("from"))
	if err != nil {
		return CalendarFilter{}, invalidField(
			"from",
			"rfc3339",
			"from_invalid",
			"Calendar from must be an RFC3339 instant.",
		)
	}
	untilValue := query.Get("until")
	if untilValue == "" {
		untilValue = query.Get("to")
	}
	until, err := time.Parse(time.RFC3339, untilValue)
	if err != nil {
		return CalendarFilter{}, invalidField(
			"until",
			"rfc3339",
			"until_invalid",
			"Calendar until must be an RFC3339 instant.",
		)
	}
	return CalendarFilter{
		FromUTC:   from,
		UntilUTC:  until,
		ChannelID: query.Get("channel_id"),
		Status:    PostStatus(query.Get("status")),
	}, nil
}

func decodeSchedulingJSON(
	writer http.ResponseWriter,
	request *http.Request,
	target any,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(
		writer,
		request.Body,
		maximumSchedulingRequestBytes,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeSchedulingError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			&FieldError{
				Field:   "body",
				Rule:    "valid_json",
				Code:    "request_body_invalid",
				Message: "Request body must match the scheduling contract.",
			},
		)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeSchedulingError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			&FieldError{
				Field:   "body",
				Rule:    "single_json_value",
				Code:    "request_body_invalid",
				Message: "Request body must contain one JSON value.",
			},
		)
		return false
	}
	return true
}

func writeSchedulingServiceError(writer http.ResponseWriter, err error) {
	var fieldError *FieldError
	if errors.As(err, &fieldError) {
		writeSchedulingError(writer, http.StatusBadRequest, "invalid_request", fieldError)
		return
	}
	switch {
	case errors.Is(err, ErrUnauthenticated):
		writeSchedulingError(writer, http.StatusUnauthorized, "unauthenticated", nil)
	case errors.Is(err, ErrForbidden):
		writeSchedulingError(writer, http.StatusForbidden, "forbidden", nil)
	case errors.Is(err, ErrNotFound):
		writeSchedulingError(writer, http.StatusNotFound, "scheduled_post_not_found", nil)
	case errors.Is(err, ErrConflict):
		writeSchedulingError(writer, http.StatusConflict, "revision_conflict", nil)
	case errors.Is(err, ErrImmutable):
		writeSchedulingError(writer, http.StatusConflict, "scheduled_post_immutable", nil)
	case errors.Is(err, ErrInvalidArgument):
		writeSchedulingError(writer, http.StatusBadRequest, "invalid_request", nil)
	default:
		writeSchedulingError(writer, http.StatusInternalServerError, "internal_error", nil)
	}
}

func writeSchedulingError(
	writer http.ResponseWriter,
	status int,
	code string,
	fieldError *FieldError,
) {
	body := map[string]any{
		"code":    code,
		"message": http.StatusText(status),
	}
	if fieldError != nil {
		body["field"] = fieldError.Field
		body["rule"] = fieldError.Rule
		body["field_code"] = fieldError.Code
		body["field_message"] = fieldError.Message
	}
	writeSchedulingJSON(writer, status, map[string]any{"error": body})
}

func writeSchedulingJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
