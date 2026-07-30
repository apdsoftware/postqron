package composer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

const maximumRequestBytes = 2 << 20

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
		"GET /api/v1/workspaces/{workspace_id}/drafts",
		handler.listDrafts,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/drafts",
		handler.createDraft,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/composer/capabilities",
		handler.getCapabilities,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/drafts/{draft_id}",
		handler.getDraft,
	)
	mux.HandleFunc(
		"PUT /api/v1/workspaces/{workspace_id}/drafts/{draft_id}",
		handler.updateDraft,
	)
	mux.HandleFunc(
		"PATCH /api/v1/workspaces/{workspace_id}/drafts/{draft_id}",
		handler.updateDraft,
	)
	mux.HandleFunc(
		"DELETE /api/v1/workspaces/{workspace_id}/drafts/{draft_id}",
		handler.deleteDraft,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/validate",
		handler.validateDraft,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/revisions",
		handler.listDraftRevisions,
	)
	return mux
}

func (handler *HTTPHandler) getCapabilities(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	catalog, err := handler.service.CapabilityCatalog(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(writer, http.StatusOK, catalog)
}

func (handler *HTTPHandler) createDraft(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	var payload struct {
		Content DraftContent `json:"content"`
	}
	if !decodeComposerJSON(writer, request, &payload) {
		return
	}
	view, err := handler.service.CreateDraft(request.Context(), CreateDraftCommand{
		WorkspaceID: request.PathValue("workspace_id"),
		ActorID:     accountID,
		Content:     payload.Content,
	})
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+view.Draft.WorkspaceID+"/drafts/"+view.Draft.ID,
	)
	writeComposerJSON(writer, http.StatusCreated, view)
}

func (handler *HTTPHandler) listDrafts(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	views, err := handler.service.ListDrafts(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(writer, http.StatusOK, map[string]any{"drafts": views})
}

func (handler *HTTPHandler) getDraft(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	view, err := handler.service.GetDraft(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("draft_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(writer, http.StatusOK, view)
}

func (handler *HTTPHandler) updateDraft(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	var payload struct {
		ExpectedRevision int64        `json:"expected_revision"`
		AutosaveKey      string       `json:"autosave_key,omitempty"`
		Content          DraftContent `json:"content"`
	}
	if !decodeComposerJSON(writer, request, &payload) {
		return
	}
	view, err := handler.service.UpdateDraft(request.Context(), UpdateDraftCommand{
		WorkspaceID:      request.PathValue("workspace_id"),
		ActorID:          accountID,
		DraftID:          request.PathValue("draft_id"),
		ExpectedRevision: payload.ExpectedRevision,
		AutosaveKey:      payload.AutosaveKey,
		Content:          payload.Content,
	})
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(writer, http.StatusOK, view)
}

func (handler *HTTPHandler) listDraftRevisions(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	revisions, err := handler.service.ListDraftRevisions(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("draft_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(writer, http.StatusOK, map[string]any{"revisions": revisions})
}

func (handler *HTTPHandler) deleteDraft(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	revision, err := strconv.ParseInt(request.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		writeComposerError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			&ValidationError{
				Field:   "revision",
				Rule:    "positive_integer",
				Code:    "revision_invalid",
				Message: "A positive revision query parameter is required.",
			},
		)
		return
	}
	err = handler.service.DeleteDraft(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("draft_id"),
		revision,
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) validateDraft(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return
	}
	report, err := handler.service.ValidateForScheduling(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("draft_id"),
	)
	if err != nil {
		var failure *ValidationFailure
		if errors.As(err, &failure) {
			writeComposerJSON(writer, http.StatusUnprocessableEntity, map[string]any{
				"error": map[string]any{
					"code":    "draft_invalid",
					"message": ErrValidation.Error(),
				},
				"validation": failure.Report,
			})
			return
		}
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(
		writer,
		http.StatusOK,
		map[string]ValidationReport{"validation": report},
	)
}

func decodeComposerJSON(
	writer http.ResponseWriter,
	request *http.Request,
	target any,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(
		writer,
		request.Body,
		maximumRequestBytes,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeComposerError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			&ValidationError{
				Field:   "body",
				Rule:    "valid_json",
				Code:    "request_body_invalid",
				Message: "The request body must match the composer contract.",
			},
		)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeComposerError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			&ValidationError{
				Field:   "body",
				Rule:    "single_json_value",
				Code:    "request_body_invalid",
				Message: "The request body must contain one JSON value.",
			},
		)
		return false
	}
	return true
}

func writeServiceError(writer http.ResponseWriter, err error) {
	var fieldRuleError *FieldRuleError
	if errors.As(err, &fieldRuleError) {
		writeComposerError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			&ValidationError{
				Field:   fieldRuleError.Field,
				Rule:    fieldRuleError.Rule,
				Code:    fieldRuleError.Code,
				Message: fieldRuleError.Message,
			},
		)
		return
	}
	switch {
	case errors.Is(err, ErrUnauthenticated):
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
	case errors.Is(err, ErrForbidden):
		writeComposerError(writer, http.StatusForbidden, "forbidden", nil)
	case errors.Is(err, ErrNotFound):
		writeComposerError(writer, http.StatusNotFound, "draft_not_found", nil)
	case errors.Is(err, ErrConflict):
		writeComposerError(writer, http.StatusConflict, "revision_conflict", nil)
	case errors.Is(err, ErrStorageUnavailable):
		writeComposerErrorWithRetryable(
			writer,
			http.StatusServiceUnavailable,
			"media_storage_unavailable",
			true,
			nil,
		)
	case errors.Is(err, ErrInvalidArgument):
		writeComposerError(writer, http.StatusBadRequest, "invalid_request", nil)
	default:
		writeComposerError(writer, http.StatusInternalServerError, "internal_error", nil)
	}
}

func writeComposerError(
	writer http.ResponseWriter,
	status int,
	code string,
	fieldError *ValidationError,
) {
	writeComposerErrorWithRetryable(writer, status, code, false, fieldError)
}

func writeComposerErrorWithRetryable(
	writer http.ResponseWriter,
	status int,
	code string,
	retryable bool,
	fieldError *ValidationError,
) {
	errorBody := map[string]any{
		"code":      code,
		"message":   http.StatusText(status),
		"retryable": retryable,
	}
	if fieldError != nil {
		errorBody["field"] = fieldError.Field
		errorBody["rule"] = fieldError.Rule
		errorBody["field_code"] = fieldError.Code
		errorBody["field_message"] = fieldError.Message
	}
	writeComposerJSON(writer, status, map[string]any{"error": errorBody})
}

func writeComposerJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
