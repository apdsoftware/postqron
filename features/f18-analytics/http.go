package analytics

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

type RequestAuthenticator interface {
	AccountID(*http.Request) (string, bool)
}

type HTTPHandler struct {
	service       *Service
	authenticator RequestAuthenticator
}

func NewHTTPHandler(
	service *Service,
	authenticator RequestAuthenticator,
) (http.Handler, error) {
	if service == nil || authenticator == nil {
		return nil, errors.New("analytics service and authenticator are required")
	}
	handler := &HTTPHandler{service: service, authenticator: authenticator}
	mux := http.NewServeMux()
	mux.HandleFunc(
		"GET /api/v1/workspaces/{workspace_id}/analytics",
		handler.overview,
	)
	return mux, nil
}

func (handler *HTTPHandler) overview(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, authenticated := handler.authenticator.AccountID(request)
	if !authenticated {
		writeAnalyticsError(writer, http.StatusUnauthorized, "unauthenticated")
		return
	}
	from, fromErr := time.Parse(time.RFC3339, request.URL.Query().Get("from"))
	to, toErr := time.Parse(time.RFC3339, request.URL.Query().Get("to"))
	if fromErr != nil || toErr != nil {
		writeAnalyticsError(writer, http.StatusBadRequest, "invalid_interval")
		return
	}
	channelIDs := request.URL.Query()["channel_id"]
	for index := range channelIDs {
		channelIDs[index] = strings.TrimSpace(channelIDs[index])
	}
	overview, err := handler.service.ChannelOverview(
		request.Context(),
		OverviewQuery{
			WorkspaceID: request.PathValue("workspace_id"),
			ActorID:     accountID,
			ChannelIDs:  channelIDs,
			From:        from,
			To:          to,
		},
	)
	if err != nil {
		status := http.StatusInternalServerError
		code := "analytics_unavailable"
		switch {
		case errors.Is(err, ErrInvalidArgument):
			status = http.StatusBadRequest
			code = "invalid_request"
		case errors.Is(err, ErrForbidden):
			status = http.StatusForbidden
			code = "forbidden"
		case errors.Is(err, ErrNotFound):
			status = http.StatusNotFound
			code = "not_found"
		}
		writeAnalyticsError(writer, status, code)
		return
	}
	writeAnalyticsJSON(writer, http.StatusOK, overview)
}

func writeAnalyticsError(writer http.ResponseWriter, status int, code string) {
	writeAnalyticsJSON(writer, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": http.StatusText(status),
		},
	})
}

func writeAnalyticsJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
}
