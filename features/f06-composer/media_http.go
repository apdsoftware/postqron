package composer

import (
	"net/http"
)

type MediaHTTPHandler struct {
	store         *PostgresMediaStore
	authorizer    ContentAuthorizer
	authenticator RequestAuthenticator
}

func NewMediaHTTPHandler(
	store *PostgresMediaStore,
	authorizer ContentAuthorizer,
	authenticator RequestAuthenticator,
) http.Handler {
	handler := &MediaHTTPHandler{
		store:         store,
		authorizer:    authorizer,
		authenticator: authenticator,
	}
	mux := http.NewServeMux()
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/composer/media",
		handler.createUpload,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/composer/media/{media_id}",
		handler.getMedia,
	)
	mux.HandleFunc(
		"DELETE /api/v1/workspaces/{workspace_id}/composer/media/{media_id}",
		handler.deleteMedia,
	)
	mux.HandleFunc(
		"POST /api/v1/workspaces/{workspace_id}/composer/media/{media_id}/complete",
		handler.completeUpload,
	)
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/composer/media/{media_id}/download",
		handler.getDownload,
	)
	return mux
}

func (handler *MediaHTTPHandler) createUpload(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, ok := handler.authorize(writer, request)
	if !ok {
		return
	}
	var payload MediaUploadRequest
	if !decodeComposerJSON(writer, request, &payload) {
		return
	}
	upload, err := handler.store.CreateUpload(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
		payload,
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set("Location", upload.UploadURL)
	writeComposerJSON(writer, http.StatusCreated, upload)
}

func (handler *MediaHTTPHandler) completeUpload(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := handler.authorize(writer, request); !ok {
		return
	}
	media, err := handler.store.CompleteUpload(
		request.Context(),
		request.PathValue("workspace_id"),
		request.PathValue("media_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(writer, http.StatusOK, media)
}

func (handler *MediaHTTPHandler) getMedia(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := handler.authorize(writer, request); !ok {
		return
	}
	media, err := handler.store.Get(
		request.Context(),
		request.PathValue("workspace_id"),
		request.PathValue("media_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(writer, http.StatusOK, media)
}

func (handler *MediaHTTPHandler) getDownload(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := handler.authorize(writer, request); !ok {
		return
	}
	download, err := handler.store.Download(
		request.Context(),
		request.PathValue("workspace_id"),
		request.PathValue("media_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeComposerJSON(writer, http.StatusOK, download)
}

func (handler *MediaHTTPHandler) deleteMedia(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if _, ok := handler.authorize(writer, request); !ok {
		return
	}
	err := handler.store.Delete(
		request.Context(),
		request.PathValue("workspace_id"),
		request.PathValue("media_id"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *MediaHTTPHandler) authorize(
	writer http.ResponseWriter,
	request *http.Request,
) (string, bool) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeComposerError(writer, http.StatusUnauthorized, "unauthenticated", nil)
		return "", false
	}
	allowed, err := handler.authorizer.CanManageContent(
		request.Context(),
		request.PathValue("workspace_id"),
		accountID,
	)
	if err != nil {
		writeServiceError(writer, err)
		return "", false
	}
	if !allowed {
		writeComposerError(writer, http.StatusForbidden, "forbidden", nil)
		return "", false
	}
	return accountID, true
}
