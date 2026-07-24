package contentassistant

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maximumRequestBytes = 1 << 20

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
		"POST /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/content-assistant/proposals",
		handler.suggest,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/content-assistant/manual-proposals",
		handler.createManual,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/content-assistant/proposals/{proposal_id}",
		handler.getProposal,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/content-assistant/proposals/{proposal_id}/confirm",
		handler.confirm,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/content-assistant/proposals/{proposal_id}/reject",
		handler.reject,
	)
	return mux
}

func (handler *HTTPHandler) suggest(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		DestinationIDs         []string `json:"destination_ids"`
		AlternativesPerChannel int      `json:"alternatives_per_channel"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	proposal, err := handler.service.Suggest(request.Context(), SuggestCommand{
		WorkspaceID:            request.PathValue("workspace_id"),
		ActorID:                accountID,
		DraftID:                request.PathValue("draft_id"),
		DestinationIDs:         payload.DestinationIDs,
		AlternativesPerChannel: payload.AlternativesPerChannel,
		CorrelationID:          request.Header.Get("X-Request-ID"),
	})
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+proposal.WorkspaceID+
			"/content-assistant/proposals/"+proposal.ID,
	)
	writeJSON(writer, http.StatusCreated, proposal)
}

func (handler *HTTPHandler) createManual(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Candidates []ManualCandidate `json:"candidates"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	proposal, err := handler.service.CreateManual(
		request.Context(),
		CreateManualCommand{
			WorkspaceID:   request.PathValue("workspace_id"),
			ActorID:       accountID,
			DraftID:       request.PathValue("draft_id"),
			Candidates:    payload.Candidates,
			CorrelationID: request.Header.Get("X-Request-ID"),
		},
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+proposal.WorkspaceID+
			"/content-assistant/proposals/"+proposal.ID,
	)
	writeJSON(writer, http.StatusCreated, proposal)
}

func (handler *HTTPHandler) getProposal(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	proposal, err := handler.service.GetProposal(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("proposal_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, proposal)
}

func (handler *HTTPHandler) confirm(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ExpectedRevision int64    `json:"expected_revision"`
		Confirmation     bool     `json:"confirmation"`
		CandidateIDs     []string `json:"candidate_ids"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	proposal, changeSet, err := handler.service.Confirm(
		request.Context(),
		ConfirmCommand{
			WorkspaceID:      request.PathValue("workspace_id"),
			ActorID:          accountID,
			ProposalID:       request.PathValue("proposal_id"),
			ExpectedRevision: payload.ExpectedRevision,
			Confirmed:        payload.Confirmation,
			CandidateIDs:     payload.CandidateIDs,
			CorrelationID:    request.Header.Get("X-Request-ID"),
		},
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"proposal":   proposal,
		"change_set": changeSet,
	})
}

func (handler *HTTPHandler) reject(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ExpectedRevision int64 `json:"expected_revision"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	proposal, err := handler.service.Reject(request.Context(), RejectCommand{
		WorkspaceID:      request.PathValue("workspace_id"),
		ActorID:          accountID,
		ProposalID:       request.PathValue("proposal_id"),
		ExpectedRevision: payload.ExpectedRevision,
		CorrelationID:    request.Header.Get("X-Request-ID"),
	})
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, proposal)
}

func (handler *HTTPHandler) accountID(
	writer http.ResponseWriter,
	request *http.Request,
) (string, bool) {
	if handler.authenticator == nil {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", nil, false)
		return "", false
	}
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", nil, false)
		return "", false
	}
	return accountID, true
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(
		writer,
		request.Body,
		maximumRequestBytes,
	))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			&FieldError{
				Field:   "body",
				Code:    "request_body_invalid",
				Message: "The request body must match the content assistant contract.",
			},
			false,
		)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_request",
			&FieldError{
				Field:   "body",
				Code:    "request_body_invalid",
				Message: "The request body must contain exactly one JSON value.",
			},
			false,
		)
		return false
	}
	return true
}

func writeServiceError(writer http.ResponseWriter, err error) {
	var fieldError *FieldError
	switch {
	case errors.As(err, &fieldError):
		writeError(writer, http.StatusBadRequest, "invalid_request", fieldError, false)
	case errors.Is(err, ErrUnauthenticated):
		writeError(writer, http.StatusUnauthorized, "unauthenticated", nil, false)
	case errors.Is(err, ErrForbidden):
		writeError(writer, http.StatusForbidden, "forbidden", nil, false)
	case errors.Is(err, ErrNotFound):
		writeError(writer, http.StatusNotFound, "proposal_not_found", nil, false)
	case errors.Is(err, ErrConflict):
		writeError(writer, http.StatusConflict, "proposal_conflict", nil, false)
	case errors.Is(err, ErrGeneratorUnavailable):
		writeError(
			writer,
			http.StatusServiceUnavailable,
			"generator_unavailable",
			nil,
			true,
		)
	default:
		writeError(writer, http.StatusInternalServerError, "internal_error", nil, false)
	}
}

func writeError(
	writer http.ResponseWriter,
	status int,
	code string,
	fieldError *FieldError,
	manualFallback bool,
) {
	body := map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": http.StatusText(status),
		},
	}
	errorBody := body["error"].(map[string]any)
	if fieldError != nil {
		errorBody["field_error"] = fieldError
	}
	if manualFallback {
		errorBody["manual_fallback_available"] = true
	}
	writeJSON(writer, status, body)
}

func writeJSON(writer http.ResponseWriter, status int, body any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(body)
}
