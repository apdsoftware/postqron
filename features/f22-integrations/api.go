package integrations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPageSize = 25
	maxPageSize     = 100
	maxRequestBody  = 256 << 10
)

type Post struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspace_id"`
	Text        string     `json:"text"`
	Status      string     `json:"status"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type CreatePostCommand struct {
	Text        string
	ScheduledAt *time.Time
}

type PageRequest struct {
	After string
	Limit int
}

// PostGateway is implemented by the content slice. It deliberately exposes no
// social-provider credential or provider response.
type PostGateway interface {
	ListPosts(ctx context.Context, workspaceID string, page PageRequest) ([]Post, string, error)
	CreatePost(ctx context.Context, workspaceID string, command CreatePostCommand) (Post, error)
}

type StoredResponse struct {
	Status int
	Header http.Header
	Body   []byte
}

type IdempotencyRequest struct {
	WorkspaceID  string
	CredentialID string
	Operation    string
	Key          string
	Fingerprint  [sha256.Size]byte
	ExpiresAt    time.Time
}

// IdempotencyStore must serialize Execute calls for the same identity,
// operation, and key. It replays the first successful response and returns
// ErrIdempotencyConflict if a key is reused with another fingerprint.
type IdempotencyStore interface {
	Execute(
		ctx context.Context,
		request IdempotencyRequest,
		operation func(context.Context) (StoredResponse, error),
	) (response StoredResponse, replayed bool, err error)
}

type APIConfig struct {
	Authenticator Authenticator
	Authorizer    Authorizer
	Clock         Clock
	CursorKey     []byte
	Idempotency   IdempotencyStore
	Limiter       RateLimiter
	Posts         PostGateway
}

type APIHandler struct {
	authenticator Authenticator
	authorizer    Authorizer
	clock         Clock
	cursors       *CursorCodec
	idempotency   IdempotencyStore
	limiter       RateLimiter
	posts         PostGateway
}

func NewAPIHandler(config APIConfig) (http.Handler, error) {
	switch {
	case config.Authenticator == nil:
		return nil, errors.New("authenticator is required")
	case config.Authorizer == nil:
		return nil, errors.New("authorizer is required")
	case config.Idempotency == nil:
		return nil, errors.New("idempotency store is required")
	case config.Limiter == nil:
		return nil, errors.New("rate limiter is required")
	case config.Posts == nil:
		return nil, errors.New("post gateway is required")
	}
	if config.Clock == nil {
		config.Clock = systemClock
	}
	cursors, err := NewCursorCodec(config.CursorKey, config.Clock)
	if err != nil {
		return nil, err
	}
	handler := &APIHandler{
		authenticator: config.Authenticator,
		authorizer:    config.Authorizer,
		clock:         config.Clock,
		cursors:       cursors,
		idempotency:   config.Idempotency,
		limiter:       config.Limiter,
		posts:         config.Posts,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/public/v1/workspaces/{workspaceID}/posts", handler.listPosts)
	mux.HandleFunc("POST /api/public/v1/workspaces/{workspaceID}/posts", handler.createPost)
	return securityHeaders(mux), nil
}

func (handler *APIHandler) listPosts(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authorize(writer, request, ScopePostsRead)
	if !ok {
		return
	}
	workspaceID := request.PathValue("workspaceID")
	limit, err := pageLimit(request.URL.Query().Get("limit"))
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	var after string
	if encoded := request.URL.Query().Get("cursor"); encoded != "" {
		after, err = handler.cursors.Decode(workspaceID, encoded)
		if err != nil {
			writeAPIError(writer, err)
			return
		}
	}
	posts, nextAfter, err := handler.posts.ListPosts(
		request.Context(),
		principal.WorkspaceID,
		PageRequest{After: after, Limit: limit},
	)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	response := struct {
		Data       []Post `json:"data"`
		NextCursor string `json:"next_cursor,omitempty"`
	}{Data: posts}
	if response.Data == nil {
		response.Data = []Post{}
	}
	if nextAfter != "" {
		response.NextCursor, err = handler.cursors.Encode(workspaceID, nextAfter)
		if err != nil {
			writeAPIError(writer, ErrUnavailable)
			return
		}
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *APIHandler) createPost(writer http.ResponseWriter, request *http.Request) {
	principal, ok := handler.authorize(writer, request, ScopePostsWrite)
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if !validIdempotencyKey(idempotencyKey) {
		writeAPIError(writer, apiError{
			status:  http.StatusBadRequest,
			code:    "invalid_idempotency_key",
			message: "Idempotency-Key must contain 16 to 128 printable ASCII characters.",
		})
		return
	}
	body, err := readBody(writer, request)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	fingerprint := sha256.Sum256(append(
		[]byte(request.Method+"\n"+request.URL.Path+"\n"),
		body...,
	))
	idempotencyRequest := IdempotencyRequest{
		WorkspaceID:  principal.WorkspaceID,
		CredentialID: principal.CredentialID,
		Operation:    "posts.create",
		Key:          idempotencyKey,
		Fingerprint:  fingerprint,
		ExpiresAt:    handler.clock().Add(24 * time.Hour),
	}
	response, replayed, err := handler.idempotency.Execute(
		request.Context(),
		idempotencyRequest,
		func(ctx context.Context) (StoredResponse, error) {
			var input struct {
				Text        string     `json:"text"`
				ScheduledAt *time.Time `json:"scheduled_at"`
			}
			if decodeErr := decodeBytes(body, &input); decodeErr != nil {
				return StoredResponse{}, decodeErr
			}
			input.Text = strings.TrimSpace(input.Text)
			if input.Text == "" || len([]rune(input.Text)) > 5000 {
				return StoredResponse{}, apiError{
					status:  http.StatusBadRequest,
					code:    "invalid_post",
					message: "text is required and may contain at most 5000 characters.",
				}
			}
			post, createErr := handler.posts.CreatePost(ctx, principal.WorkspaceID, CreatePostCommand{
				Text:        input.Text,
				ScheduledAt: input.ScheduledAt,
			})
			if createErr != nil {
				return StoredResponse{}, createErr
			}
			encoded, marshalErr := json.Marshal(post)
			if marshalErr != nil {
				return StoredResponse{}, marshalErr
			}
			return StoredResponse{
				Status: http.StatusCreated,
				Header: http.Header{"Content-Type": []string{"application/json"}},
				Body:   append(encoded, '\n'),
			}, nil
		},
	)
	if err != nil {
		writeAPIError(writer, err)
		return
	}
	if replayed {
		writer.Header().Set("Idempotency-Replayed", "true")
	}
	copyStoredResponse(writer, response)
}

func (handler *APIHandler) authorize(
	writer http.ResponseWriter,
	request *http.Request,
	required Scope,
) (Principal, bool) {
	credential, err := bearerCredential(request.Header.Get("Authorization"))
	if err != nil {
		writeAPIError(writer, err)
		return Principal{}, false
	}
	principal, err := handler.authenticator.Authenticate(request.Context(), credential)
	if err != nil || !principal.HasScope(required, handler.clock()) {
		if err == nil {
			err = ErrForbidden
		}
		writeAPIError(writer, err)
		return Principal{}, false
	}
	workspaceID := request.PathValue("workspaceID")
	if workspaceID == "" || principal.WorkspaceID != workspaceID {
		writeAPIError(writer, ErrForbidden)
		return Principal{}, false
	}
	if err = handler.authorizer.Authorize(request.Context(), principal, workspaceID, required); err != nil {
		writeAPIError(writer, err)
		return Principal{}, false
	}
	allowed, retryAfter, err := handler.limiter.Allow(
		request.Context(),
		principal.CredentialID+":"+string(required),
		handler.clock(),
	)
	if err != nil {
		writeAPIError(writer, ErrUnavailable)
		return Principal{}, false
	}
	if !allowed {
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		writer.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		writeAPIError(writer, ErrRateLimited)
		return Principal{}, false
	}
	return principal, true
}

func bearerCredential(header string) (string, error) {
	parts := strings.Fields(header)
	if len(parts) != 2 ||
		!strings.EqualFold(parts[0], "Bearer") ||
		len(parts[1]) < 32 ||
		len(parts[1]) > 512 {
		return "", ErrUnauthenticated
	}
	return parts[1], nil
}

func pageLimit(value string) (int, error) {
	if value == "" {
		return defaultPageSize, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > maxPageSize {
		return 0, apiError{
			status:  http.StatusBadRequest,
			code:    "invalid_page_size",
			message: "limit must be an integer between 1 and 100.",
		}
	}
	return limit, nil
}

func validIdempotencyKey(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	for _, character := range key {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func readBody(writer http.ResponseWriter, request *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxRequestBody))
	if err != nil {
		return nil, apiError{
			status:  http.StatusRequestEntityTooLarge,
			code:    "request_too_large",
			message: "Request body exceeds 256 KiB.",
		}
	}
	return body, nil
}

func decodeBytes(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return apiError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "Request body is not valid JSON.",
		}
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return apiError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "Request body must contain exactly one JSON object.",
		}
	}
	return nil
}

func copyStoredResponse(writer http.ResponseWriter, response StoredResponse) {
	for name, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	writer.WriteHeader(response.Status)
	_, _ = writer.Write(response.Body)
}

type apiError struct {
	status  int
	code    string
	message string
}

func (err apiError) Error() string {
	return err.code
}

func writeAPIError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"
	message := "The request could not be completed."

	var described apiError
	switch {
	case errors.As(err, &described):
		status, code, message = described.status, described.code, described.message
	case errors.Is(err, ErrUnauthenticated):
		status, code, message = http.StatusUnauthorized, "unauthenticated", "A valid bearer credential is required."
		writer.Header().Set("WWW-Authenticate", `Bearer realm="postqron-public-api"`)
	case errors.Is(err, ErrForbidden):
		status, code, message = http.StatusForbidden, "forbidden", "The credential cannot perform this operation."
	case errors.Is(err, ErrRateLimited):
		status, code, message = http.StatusTooManyRequests, "rate_limited", "Request limit exceeded; retry later."
	case errors.Is(err, ErrInvalidCursor):
		status, code, message = http.StatusBadRequest, "invalid_cursor", "The pagination cursor is invalid or expired."
	case errors.Is(err, ErrIdempotencyConflict):
		status, code, message = http.StatusConflict, "idempotency_conflict", "The idempotency key was already used for a different request."
	case errors.Is(err, ErrNotFound):
		status, code, message = http.StatusNotFound, "not_found", "The requested resource does not exist."
	}
	writeJSON(writer, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Postqron-API-Version", "2026-07-01")
		next.ServeHTTP(writer, request)
	})
}
