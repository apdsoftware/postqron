package cookieconsent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	SessionCookieName          = "__Host-postqron_session"
	AnonymousSubjectCookieName = "__Host-postqron_cookie_subject"
	maxRequestBytes            = 8 << 10
)

var anonymousIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type Resolution struct {
	Subject   Subject
	SetCookie bool
}

type SubjectResolver interface {
	Resolve(context.Context, *http.Request) (Resolution, error)
}

type RequestSubjectResolver struct {
	database *sql.DB
	clock    func() time.Time
}

func NewRequestSubjectResolver(
	database *sql.DB,
	clock func() time.Time,
) (*RequestSubjectResolver, error) {
	if database == nil || clock == nil {
		return nil, ErrInvalidRequest
	}
	return &RequestSubjectResolver{database: database, clock: clock}, nil
}

func (resolver *RequestSubjectResolver) Resolve(
	ctx context.Context,
	request *http.Request,
) (Resolution, error) {
	if session, err := request.Cookie(SessionCookieName); err == nil &&
		strings.TrimSpace(session.Value) != "" {
		digest := sha256.Sum256([]byte(session.Value))
		var accountID string
		err := resolver.database.QueryRowContext(ctx, `
			SELECT account_id
			FROM auth_sessions
			WHERE token_hash = $1
			  AND revoked_at IS NULL
			  AND expires_at > $2`,
			hex.EncodeToString(digest[:]),
			resolver.clock().UTC(),
		).Scan(&accountID)
		if err == nil {
			return Resolution{
				Subject: Subject{Kind: SubjectAccount, ID: accountID},
			}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return Resolution{}, err
		}
	}
	if cookie, err := request.Cookie(AnonymousSubjectCookieName); err == nil &&
		anonymousIDPattern.MatchString(cookie.Value) {
		return Resolution{
			Subject: Subject{Kind: SubjectBrowser, ID: cookie.Value},
		}, nil
	}
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return Resolution{}, err
	}
	return Resolution{
		Subject: Subject{
			Kind: SubjectBrowser,
			ID:   base64.RawURLEncoding.EncodeToString(value),
		},
		SetCookie: true,
	}, nil
}

type HTTPHandler struct {
	service  *Service
	resolver SubjectResolver
}

func NewHTTPHandler(service *Service, resolver SubjectResolver) (*HTTPHandler, error) {
	if service == nil || resolver == nil {
		return nil, ErrInvalidRequest
	}
	return &HTTPHandler{service: service, resolver: resolver}, nil
}

func (handler *HTTPHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if strings.HasSuffix(request.URL.Path, "/export") {
		if request.Method != http.MethodGet {
			writeAPIError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		handler.export(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.get(writer, request)
	case http.MethodPut:
		handler.put(writer, request)
	case http.MethodDelete:
		handler.erase(writer, request)
	default:
		writeAPIError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (handler *HTTPHandler) get(writer http.ResponseWriter, request *http.Request) {
	resolution, ok := handler.resolve(writer, request)
	if !ok {
		return
	}
	state, err := handler.service.Get(request.Context(), resolution.Subject)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, state)
}

func (handler *HTTPHandler) put(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		writeAPIError(writer, http.StatusForbidden, "origin_not_allowed")
		return
	}
	resolution, ok := handler.resolve(writer, request)
	if !ok {
		return
	}
	var input struct {
		PolicyVersion string `json:"policy_version"`
		Source        string `json:"source"`
		Preferences   bool   `json:"preferences"`
		Analytics     bool   `json:"analytics"`
		Marketing     bool   `json:"marketing"`
	}
	if err := decodeJSON(writer, request, &input); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	state, replay, err := handler.service.Put(
		request.Context(),
		resolution.Subject,
		input.PolicyVersion,
		Selection{
			Preferences: input.Preferences,
			Analytics:   input.Analytics,
			Marketing:   input.Marketing,
		},
		input.Source,
		request.Header.Get("Idempotency-Key"),
	)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	if replay {
		writer.Header().Set("Idempotent-Replay", "true")
	}
	writeJSON(writer, http.StatusOK, state)
}

func (handler *HTTPHandler) export(writer http.ResponseWriter, request *http.Request) {
	resolution, ok := handler.resolve(writer, request)
	if !ok {
		return
	}
	value, err := handler.service.Export(request.Context(), resolution.Subject)
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writer.Header().Set("Content-Disposition", `attachment; filename="cookie-preferences.json"`)
	writeJSON(writer, http.StatusOK, value)
}

func (handler *HTTPHandler) erase(writer http.ResponseWriter, request *http.Request) {
	if !sameOrigin(request) {
		writeAPIError(writer, http.StatusForbidden, "origin_not_allowed")
		return
	}
	resolution, ok := handler.resolve(writer, request)
	if !ok {
		return
	}
	if err := handler.service.Erase(request.Context(), resolution.Subject); err != nil {
		writeServiceError(writer, err)
		return
	}
	if resolution.Subject.Kind == SubjectBrowser {
		http.SetCookie(writer, &http.Cookie{
			Name:     AnonymousSubjectCookieName,
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

func (handler *HTTPHandler) resolve(
	writer http.ResponseWriter,
	request *http.Request,
) (Resolution, bool) {
	resolution, err := handler.resolver.Resolve(request.Context(), request)
	if err != nil {
		writeAPIError(writer, http.StatusServiceUnavailable, "subject_unavailable")
		return Resolution{}, false
	}
	if resolution.SetCookie {
		http.SetCookie(writer, &http.Cookie{
			Name:     AnonymousSubjectCookieName,
			Value:    resolution.Subject.ID,
			Path:     "/",
			Secure:   true,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   60 * 60 * 24 * 183,
		})
	}
	return resolution, true
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, target any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}
	return nil
}

func sameOrigin(request *http.Request) bool {
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil &&
		(parsed.Scheme == "https" || parsed.Scheme == "http") &&
		strings.EqualFold(parsed.Host, request.Host)
}

func writeServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		writeAPIError(writer, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, ErrPolicyMismatch):
		writeAPIError(writer, http.StatusConflict, "cookie_policy_changed")
	case errors.Is(err, ErrIdempotencyConflict):
		writeAPIError(writer, http.StatusConflict, "idempotency_conflict")
	case errors.Is(err, ErrPolicyUnavailable):
		writeAPIError(writer, http.StatusServiceUnavailable, "cookie_policy_unavailable")
	default:
		writeAPIError(writer, http.StatusServiceUnavailable, "cookie_preferences_unavailable")
	}
}

func writeAPIError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"error": code})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
