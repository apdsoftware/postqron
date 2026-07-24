package medialibrary

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maximumRequestBytes = 32 << 10

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
		"POST /api/v1/workspaces/{workspace_id}/media/uploads",
		handler.createUpload,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/media/uploads/{upload_id}/complete",
		handler.completeUpload,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/media/assets",
		handler.searchAssets,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/media/assets/{asset_id}",
		handler.getAsset,
	)
	mux.HandleFunc(
		"PATCH /api/v1/workspaces/{workspace_id}/media/assets/{asset_id}",
		handler.updateMetadata,
	)
	mux.HandleFunc(
		"DELETE /api/v1/workspaces/{workspace_id}/media/assets/{asset_id}",
		handler.archiveAsset,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/media/assets/{asset_id}/composer-reference",
		handler.composerReference,
	)
	return mux
}

func (handler *HTTPHandler) createUpload(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		OriginalName   string `json:"original_name"`
		ContentType    string `json:"content_type"`
		SizeBytes      int64  `json:"size_bytes"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	ticket, err := handler.service.CreateUpload(request.Context(), CreateUploadCommand{
		WorkspaceID:    request.PathValue("workspace_id"),
		ActorID:        accountID,
		OriginalName:   payload.OriginalName,
		ContentType:    payload.ContentType,
		SizeBytes:      payload.SizeBytes,
		IdempotencyKey: payload.IdempotencyKey,
	})
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+ticket.Upload.WorkspaceID+"/media/uploads/"+ticket.Upload.ID,
	)
	writeJSON(writer, http.StatusCreated, ticket)
}

func (handler *HTTPHandler) completeUpload(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	asset, err := handler.service.CompleteUpload(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("upload_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set(
		"Location",
		"/api/v1/workspaces/"+asset.WorkspaceID+"/media/assets/"+asset.ID,
	)
	writeJSON(writer, http.StatusCreated, asset)
}

func (handler *HTTPHandler) searchAssets(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	limit := 0
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		limit = parsed
	}
	result, err := handler.service.Search(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		SearchQuery{
			Text:  request.URL.Query().Get("q"),
			Kind:  MediaKind(request.URL.Query().Get("kind")),
			Tags:  splitTags(request.URL.Query().Get("tags")),
			Limit: limit,
		},
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (handler *HTTPHandler) getAsset(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	asset, err := handler.service.GetAsset(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("asset_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, asset)
}

func (handler *HTTPHandler) updateMetadata(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	var payload struct {
		ExpectedRevision int64    `json:"expected_revision"`
		OriginalName     string   `json:"original_name"`
		AltText          string   `json:"alt_text"`
		Tags             []string `json:"tags"`
	}
	if !decodeJSON(writer, request, &payload) {
		return
	}
	asset, err := handler.service.UpdateMetadata(request.Context(), UpdateMetadataCommand{
		WorkspaceID:      request.PathValue("workspace_id"),
		ActorID:          accountID,
		AssetID:          request.PathValue("asset_id"),
		ExpectedRevision: payload.ExpectedRevision,
		OriginalName:     payload.OriginalName,
		AltText:          payload.AltText,
		Tags:             payload.Tags,
	})
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, asset)
}

func (handler *HTTPHandler) archiveAsset(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	revision, err := strconv.ParseInt(request.URL.Query().Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	asset, err := handler.service.Archive(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("asset_id"),
		revision,
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, asset)
}

func (handler *HTTPHandler) composerReference(writer http.ResponseWriter, request *http.Request) {
	accountID, ok := handler.accountID(writer, request)
	if !ok {
		return
	}
	reference, err := handler.service.ResolveForComposer(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		request.PathValue("asset_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]ComposerMedia{"media": reference})
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
	decoder := json.NewDecoder(http.MaxBytesReader(
		writer, request.Body, maximumRequestBytes,
	))
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
	switch {
	case errors.Is(err, ErrUnauthenticated):
		writeError(writer, http.StatusUnauthorized, "unauthenticated")
	case errors.Is(err, ErrForbidden):
		writeError(writer, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrNotFound):
		writeError(writer, http.StatusNotFound, "media_not_found")
	case errors.Is(err, ErrQuotaExceeded):
		writeError(writer, http.StatusConflict, "media_quota_exceeded")
	case errors.Is(err, ErrConflict):
		writeError(writer, http.StatusConflict, "revision_conflict")
	case errors.Is(err, ErrAssetArchived):
		writeError(writer, http.StatusConflict, "media_archived")
	case errors.Is(err, ErrAssetInUse):
		writeError(writer, http.StatusConflict, "media_in_use")
	case errors.Is(err, ErrUploadExpired):
		writeError(writer, http.StatusGone, "upload_expired")
	case errors.Is(err, ErrUploadMismatch), errors.Is(err, ErrInvalidArgument):
		writeError(writer, http.StatusBadRequest, "invalid_request")
	default:
		writeError(writer, http.StatusInternalServerError, "internal_error")
	}
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": http.StatusText(status),
		},
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}
