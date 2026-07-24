package email

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type WebhookHandler struct {
	store      Store
	secrets    SecretProvider
	secretName string
	now        func() time.Time
	maxSkew    time.Duration
}

func NewWebhookHandler(
	store Store,
	secrets SecretProvider,
	secretName string,
	maxSkew time.Duration,
) (*WebhookHandler, error) {
	if store == nil || secrets == nil || !validSecretName(secretName) {
		return nil, errors.New("webhook store, secret provider, and secret name are required")
	}
	if maxSkew <= 0 {
		return nil, errors.New("webhook signature skew must be positive")
	}
	return &WebhookHandler{
		store:      store,
		secrets:    secrets,
		secretName: secretName,
		now:        time.Now,
		maxSkew:    maxSkew,
	}, nil
}

func (handler *WebhookHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		http.Error(response, "invalid request", http.StatusBadRequest)
		return
	}
	valid, err := handler.verify(
		request.Context(),
		request.Header.Get("X-Mailrox-Signature"),
		body,
	)
	if err != nil {
		http.Error(response, "webhook unavailable", http.StatusServiceUnavailable)
		return
	}
	if !valid {
		http.Error(response, "invalid signature", http.StatusUnauthorized)
		return
	}
	event, err := decodeProviderEvent(body)
	if err != nil {
		http.Error(response, "invalid event", http.StatusBadRequest)
		return
	}
	if _, err := ProcessProviderEvent(request.Context(), handler.store, event); err != nil {
		http.Error(response, "event unavailable", http.StatusServiceUnavailable)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *WebhookHandler) verify(
	ctx context.Context,
	header string,
	body []byte,
) (bool, error) {
	timestamp, signature, ok := parseSignatureHeader(header)
	if !ok {
		return false, nil
	}
	signedAt := time.Unix(timestamp, 0)
	difference := handler.now().Sub(signedAt)
	if difference < 0 {
		difference = -difference
	}
	if difference > handler.maxSkew {
		return false, nil
	}
	secret, err := handler.secrets.Secret(ctx, handler.secretName)
	if err != nil || len(secret) < 32 {
		return false, errors.New("webhook signing secret is unavailable")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	actual, err := hex.DecodeString(signature)
	if err != nil {
		return false, nil
	}
	return hmac.Equal(expected, actual), nil
}

func parseSignatureHeader(value string) (int64, string, bool) {
	var timestamp int64
	var signature string
	for _, part := range strings.Split(value, ",") {
		key, raw, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch key {
		case "t":
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return 0, "", false
			}
			timestamp = parsed
		case "v1":
			signature = raw
		}
	}
	return timestamp, signature, timestamp > 0 && signature != ""
}

func decodeProviderEvent(body []byte) (ProviderEvent, error) {
	var payload struct {
		ID                string            `json:"id"`
		Type              ProviderEventType `json:"type"`
		ProviderMessageID string            `json:"message_id"`
		RecipientID       string            `json:"recipient_id"`
		Code              string            `json:"code"`
		Diagnostic        string            `json:"diagnostic"`
		OccurredAt        time.Time         `json:"occurred_at"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return ProviderEvent{}, err
	}
	return ProviderEvent{
		ID:                payload.ID,
		ProviderMessageID: payload.ProviderMessageID,
		Type:              payload.Type,
		RecipientID:       payload.RecipientID,
		Diagnostic: Diagnostic{
			Code:   payload.Code,
			Detail: payload.Diagnostic,
		},
		OccurredAt: payload.OccurredAt,
	}, nil
}
