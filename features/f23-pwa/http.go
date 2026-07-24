package pwa

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
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
	mux.HandleFunc("POST /api/v1/push/subscriptions", handler.subscribe)
	mux.HandleFunc("DELETE /api/v1/push/subscriptions", handler.revoke)
}

type subscriptionBody struct {
	Endpoint       string  `json:"endpoint"`
	ExpirationTime *int64  `json:"expiration_time"`
	Keys           keyBody `json:"keys"`
}

type keyBody struct {
	P256DH string `json:"p256dh"`
	Auth   string `json:"auth"`
}

func (handler *HTTPHandler) subscribe(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, err := handler.authenticator.AccountID(request)
	if err != nil || strings.TrimSpace(accountID) == "" {
		writeError(writer, ErrUnauthenticated)
		return
	}
	var body subscriptionBody
	if err := decodeJSON(writer, request, &body); err != nil {
		writeError(writer, err)
		return
	}
	var expiration *time.Time
	if body.ExpirationTime != nil {
		value := time.UnixMilli(*body.ExpirationTime).UTC()
		expiration = &value
	}
	subscription, created, err := handler.service.Subscribe(
		request.Context(),
		SubscriptionInput{
			AccountID:      accountID,
			Endpoint:       body.Endpoint,
			P256DH:         body.Keys.P256DH,
			Auth:           body.Keys.Auth,
			ExpirationTime: expiration,
		},
	)
	if err != nil {
		writeError(writer, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, subscription)
}

func (handler *HTTPHandler) revoke(
	writer http.ResponseWriter,
	request *http.Request,
) {
	accountID, err := handler.authenticator.AccountID(request)
	if err != nil || strings.TrimSpace(accountID) == "" {
		writeError(writer, ErrUnauthenticated)
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		writeError(writer, err)
		return
	}
	if _, err := handler.service.Revoke(
		request.Context(),
		accountID,
		body.Endpoint,
	); err != nil {
		writeError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func decodeJSON(
	writer http.ResponseWriter,
	request *http.Request,
	target any,
) error {
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidArgument
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ErrInvalidArgument
	}
	return nil
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
	case errors.Is(err, ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
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
