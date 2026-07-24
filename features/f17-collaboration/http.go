package collaboration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maximumRequestBytes = 32 << 10

type RequestAuthenticator interface {
	AccountID(*http.Request) (string, bool)
}

type HTTPHandler struct {
	service       *Service
	authenticator RequestAuthenticator
}

func NewHTTPHandler(service *Service, authenticator RequestAuthenticator) (http.Handler, error) {
	if service == nil || authenticator == nil {
		return nil, errors.New("collaboration service and authenticator are required")
	}
	handler := &HTTPHandler{service: service, authenticator: authenticator}
	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/comments",
		handler.listComments,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/comments",
		handler.addComment,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/comments/{comment_id}/resolve",
		handler.resolveComment,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/review",
		handler.getReview,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/review",
		handler.requestReview,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/drafts/{draft_id}/review/{review_id}/decision",
		handler.decideReview,
	)
	return mux, nil
}

func (handler *HTTPHandler) addComment(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Body string `json:"body"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	comment, err := handler.service.AddComment(request.Context(), CreateCommentCommand{
		WorkspaceID: request.PathValue("workspace_id"),
		DraftID:     request.PathValue("draft_id"),
		ActorID:     accountID,
		Body:        payload.Body,
	})
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+comment.WorkspaceID+"/drafts/"+comment.DraftID+
			"/comments/"+comment.ID,
	)
	writeJSON(writer, http.StatusCreated, comment)
}

func (handler *HTTPHandler) listComments(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	comments, err := handler.service.Comments(
		request.Context(),
		request.PathValue("workspace_id"),
		request.PathValue("draft_id"),
		accountID,
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"comments": comments})
}

func (handler *HTTPHandler) resolveComment(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	comment, err := handler.service.ResolveComment(
		request.Context(),
		ResolveCommentCommand{
			WorkspaceID: request.PathValue("workspace_id"),
			DraftID:     request.PathValue("draft_id"),
			CommentID:   request.PathValue("comment_id"),
			ActorID:     accountID,
		},
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, comment)
}

func (handler *HTTPHandler) requestReview(writer http.ResponseWriter, request *http.Request) {
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
	review, created, err := handler.service.RequestReview(
		request.Context(),
		RequestReviewCommand{
			WorkspaceID:      request.PathValue("workspace_id"),
			DraftID:          request.PathValue("draft_id"),
			ActorID:          accountID,
			ExpectedRevision: payload.ExpectedRevision,
		},
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		writer.Header().Set(
			"Location",
			"/api/v1/workspaces/"+review.WorkspaceID+"/drafts/"+review.DraftID+
				"/review",
		)
	}
	writeJSON(writer, status, review)
}

func (handler *HTTPHandler) getReview(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	review, err := handler.service.Review(
		request.Context(),
		request.PathValue("workspace_id"),
		request.PathValue("draft_id"),
		accountID,
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, review)
}

func (handler *HTTPHandler) decideReview(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		Decision ReviewDecision `json:"decision"`
		Note     string         `json:"note"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	review, err := handler.service.DecideReview(request.Context(), DecideReviewCommand{
		WorkspaceID: request.PathValue("workspace_id"),
		DraftID:     request.PathValue("draft_id"),
		ReviewID:    request.PathValue("review_id"),
		ActorID:     accountID,
		Decision:    payload.Decision,
		Note:        payload.Note,
	})
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, review)
}

func (handler *HTTPHandler) accountID(
	writer http.ResponseWriter,
	request *http.Request,
) (string, bool) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeError(writer, http.StatusUnauthorized, "unauthenticated")
		return "", false
	}
	return accountID, true
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maximumRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func writeServiceError(writer http.ResponseWriter, err error) {
	code, status := publicErrorCode(err)
	writeError(writer, status, code)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": http.StatusText(status),
		},
	})
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}
