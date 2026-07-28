package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type passwordStoreFixture struct {
	credential       PasswordCredential
	failures         int
	session          PasswordSession
	context          PasswordSessionContext
	changeFailures   int
	change           PasswordChange
	revokedTokenHash string
}

func (store *passwordStoreFixture) CredentialByEmail(
	context.Context,
	string,
) (PasswordCredential, bool, error) {
	return store.credential, store.credential.AccountID != "", nil
}

func (store *passwordStoreFixture) RecordPasswordFailure(
	context.Context,
	string,
	time.Time,
) error {
	store.failures++
	return nil
}

func (store *passwordStoreFixture) CompletePasswordLogin(
	_ context.Context,
	session PasswordSession,
	_ string,
	_ time.Time,
) error {
	store.session = session
	return nil
}

func (store *passwordStoreFixture) PasswordSession(
	context.Context,
	string,
	time.Time,
) (PasswordSessionContext, bool, error) {
	return store.context, store.context.AccountID != "", nil
}

func (store *passwordStoreFixture) RecordPasswordChangeFailure(
	_ context.Context,
	_ string,
	_ string,
	_ time.Time,
) error {
	store.changeFailures++
	return nil
}

func (store *passwordStoreFixture) CompletePasswordChange(
	_ context.Context,
	change PasswordChange,
	_ string,
	_ time.Time,
) error {
	store.change = change
	return nil
}

func (store *passwordStoreFixture) RevokePasswordSession(
	_ context.Context,
	tokenHash string,
	_ string,
	_ time.Time,
) error {
	store.revokedTokenHash = tokenHash
	return nil
}

func TestArgon2idPasswordHashAndLoginSession(t *testing.T) {
	hash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("unexpected password hash metadata: %q", hash)
	}
	valid, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !valid {
		t.Fatalf("VerifyPassword() = %v, %v", valid, err)
	}
	valid, err = VerifyPassword(hash, "incorrect password")
	if err != nil || valid {
		t.Fatalf("wrong VerifyPassword() = %v, %v", valid, err)
	}

	store := &passwordStoreFixture{credential: PasswordCredential{
		AccountID:    "account-admin",
		PasswordHash: hash,
	}}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatalf("NewPasswordService() error = %v", err)
	}
	token, expiry, err := service.Login(
		context.Background(),
		" ADMIN@example.test ",
		"correct horse battery staple",
	)
	if err != nil || token == "" {
		t.Fatalf("Login() = %q, %v, %v", token, expiry, err)
	}
	if store.session.AccountID != "account-admin" ||
		store.session.TokenHash == token ||
		store.session.ExpiresAt != testNow.Add(time.Hour) {
		t.Fatalf("stored session = %+v", store.session)
	}
}

func TestPasswordLoginIsGenericAndCORSIsRestricted(t *testing.T) {
	hash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &passwordStoreFixture{credential: PasswordCredential{
		AccountID:    "account-admin",
		PasswordHash: hash,
	}}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPasswordHandler(service, "https://postqron.com")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/login",
		strings.NewReader(`{"email":"admin@example.test","password":"wrong password value"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized ||
		!strings.Contains(response.Body.String(), "AUTH_INVALID_CREDENTIALS") ||
		strings.Contains(response.Body.String(), "admin@example.test") {
		t.Fatalf("invalid login = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://postqron.com" ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed CORS headers = %v", response.Header())
	}
	if store.failures != 1 {
		t.Fatalf("password failures = %d", store.failures)
	}

	allowed := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/login",
		strings.NewReader(`{"email":"admin@example.test","password":"correct horse battery staple"}`),
	)
	allowed.Header.Set("Content-Type", "application/json")
	allowed.Header.Set("Origin", "https://postqron.com")
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, allowed)
	if accepted.Code != http.StatusOK {
		t.Fatalf("successful login = %d %s", accepted.Code, accepted.Body.String())
	}
	cookies := accepted.Result().Cookies()
	sessionCookie := cookieByName(cookies, SessionCookieName)
	if sessionCookie == nil || !sessionCookie.HttpOnly || !sessionCookie.Secure {
		t.Fatalf("login session cookie = %+v", sessionCookie)
	}
	csrfCookie := cookieByName(cookies, CSRFCookieName)
	if csrfCookie == nil ||
		csrfCookie.HttpOnly ||
		!csrfCookie.Secure ||
		csrfCookie.Value != csrfTokenValue(sessionCookie.Value) {
		t.Fatalf("login csrf cookie = %+v session=%+v", csrfCookie, sessionCookie)
	}

	forbidden := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/login",
		strings.NewReader(`{"email":"admin@example.test","password":"correct horse battery staple"}`),
	)
	forbidden.Header.Set("Origin", "https://evil.example")
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, forbidden)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("forbidden origin = %d %s", rejected.Code, rejected.Body.String())
	}
}

func TestPasswordChangeRotatesCurrentAndRevokesOtherSessions(t *testing.T) {
	currentHash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &passwordStoreFixture{context: PasswordSessionContext{
		AccountID:       "account-admin",
		PasswordHash:    currentHash,
		AuthenticatedAt: testNow.Add(-time.Minute),
	}}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "opaque-current-session"
	newToken, expiry, err := service.ChangePassword(
		context.Background(),
		sessionToken,
		passwordCSRF(sessionToken),
		"correct horse battery staple",
		"a different secure password",
		"a different secure password",
	)
	if err != nil || newToken == "" {
		t.Fatalf("ChangePassword() = %q, %v, %v", newToken, expiry, err)
	}
	if newToken == sessionToken ||
		store.change.CurrentSessionTokenHash != tokenDigest(sessionToken) ||
		store.change.NewSession.TokenHash != tokenDigest(newToken) ||
		store.change.NewSession.TokenHash == newToken {
		t.Fatalf("password change session rotation = %+v", store.change)
	}
	if store.change.AccountID != "account-admin" ||
		store.change.NewSession.AccountID != "account-admin" ||
		store.change.NewSession.AuthenticatedAt != testNow ||
		expiry != testNow.Add(time.Hour) {
		t.Fatalf("password change metadata = %+v, %v", store.change, expiry)
	}
	valid, err := VerifyPassword(
		store.change.NewPasswordHash,
		"a different secure password",
	)
	if err != nil || !valid {
		t.Fatalf("new password hash is invalid: %v, %v", valid, err)
	}
	oldValid, err := VerifyPassword(
		store.change.NewPasswordHash,
		"correct horse battery staple",
	)
	if err != nil || oldValid {
		t.Fatalf("old password still validates: %v, %v", oldValid, err)
	}
}

func TestPasswordChangeRejectsUnsafeRequestsWithoutEchoingSecrets(t *testing.T) {
	hash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "opaque-current-session"
	tests := []struct {
		name         string
		csrf         string
		current      string
		next         string
		confirmation string
		context      PasswordSessionContext
		want         error
	}{
		{
			name:         "invalid csrf",
			csrf:         "forged",
			current:      "correct horse battery staple",
			next:         "a different secure password",
			confirmation: "a different secure password",
			context:      PasswordSessionContext{AccountID: "account-admin"},
			want:         ErrPasswordCSRFInvalid,
		},
		{
			name:         "confirmation mismatch",
			csrf:         passwordCSRF(sessionToken),
			current:      "correct horse battery staple",
			next:         "a different secure password",
			confirmation: "another secure password",
			context:      PasswordSessionContext{AccountID: "account-admin"},
			want:         ErrPasswordConfirmation,
		},
		{
			name:         "weak password",
			csrf:         passwordCSRF(sessionToken),
			current:      "correct horse battery staple",
			next:         "too-short",
			confirmation: "too-short",
			context:      PasswordSessionContext{AccountID: "account-admin"},
			want:         ErrPasswordPolicy,
		},
		{
			name:         "expired session",
			csrf:         passwordCSRF(sessionToken),
			current:      "correct horse battery staple",
			next:         "a different secure password",
			confirmation: "a different secure password",
			want:         ErrPasswordUnauthenticated,
		},
		{
			name:         "stale authentication",
			csrf:         passwordCSRF(sessionToken),
			current:      "correct horse battery staple",
			next:         "a different secure password",
			confirmation: "a different secure password",
			context: PasswordSessionContext{
				AccountID:       "account-admin",
				PasswordHash:    hash,
				AuthenticatedAt: testNow.Add(-6 * time.Minute),
			},
			want: ErrPasswordReauthRequired,
		},
		{
			name:         "rate limited",
			csrf:         passwordCSRF(sessionToken),
			current:      "correct horse battery staple",
			next:         "a different secure password",
			confirmation: "a different secure password",
			context: PasswordSessionContext{
				AccountID:         "account-admin",
				PasswordHash:      hash,
				AuthenticatedAt:   testNow,
				ChangeLockedUntil: timePointer(testNow.Add(5 * time.Minute)),
			},
			want: ErrPasswordChangeRateLimited,
		},
		{
			name:         "incorrect current password",
			csrf:         passwordCSRF(sessionToken),
			current:      "the wrong current password",
			next:         "a different secure password",
			confirmation: "a different secure password",
			context: PasswordSessionContext{
				AccountID:       "account-admin",
				PasswordHash:    hash,
				AuthenticatedAt: testNow,
			},
			want: ErrCurrentPasswordInvalid,
		},
		{
			name:         "same password",
			csrf:         passwordCSRF(sessionToken),
			current:      "correct horse battery staple",
			next:         "correct horse battery staple",
			confirmation: "correct horse battery staple",
			context: PasswordSessionContext{
				AccountID:       "account-admin",
				PasswordHash:    hash,
				AuthenticatedAt: testNow,
			},
			want: ErrPasswordPolicy,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &passwordStoreFixture{context: test.context}
			service, err := NewPasswordService(
				store,
				func() time.Time { return testNow },
				time.Hour,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = service.ChangePassword(
				context.Background(),
				sessionToken,
				test.csrf,
				test.current,
				test.next,
				test.confirmation,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("ChangePassword() error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), test.current) ||
				strings.Contains(err.Error(), test.next) {
				t.Fatalf("password echoed in error: %v", err)
			}
			wantFailures := 0
			if errors.Is(test.want, ErrCurrentPasswordInvalid) {
				wantFailures = 1
			}
			if store.changeFailures != wantFailures {
				t.Fatalf("change failures = %d, want %d", store.changeFailures, wantFailures)
			}
		})
	}
}

func TestPasswordLogoutRequiresCSRFAndRevokesServerSession(t *testing.T) {
	store := &passwordStoreFixture{}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "opaque-current-session"
	if err := service.Logout(
		context.Background(),
		sessionToken,
		"forged",
	); !errors.Is(err, ErrPasswordCSRFInvalid) {
		t.Fatalf("forged Logout() error = %v", err)
	}
	if store.revokedTokenHash != "" {
		t.Fatal("forged logout revoked a session")
	}
	if err := service.Logout(
		context.Background(),
		sessionToken,
		passwordCSRF(sessionToken),
	); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if store.revokedTokenHash != tokenDigest(sessionToken) {
		t.Fatalf("revoked token hash = %q", store.revokedTokenHash)
	}
}

func TestPasswordChangeAndLogoutHTTPContracts(t *testing.T) {
	hash, err := HashPassword(
		"correct horse battery staple",
		DefaultPasswordParameters(),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &passwordStoreFixture{context: PasswordSessionContext{
		AccountID:       "account-admin",
		PasswordHash:    hash,
		AuthenticatedAt: testNow,
	}}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPasswordHandler(service, "https://postqron.com")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "opaque-current-session"

	change := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/password/change",
		strings.NewReader(`{
			"current_password":"correct horse battery staple",
			"new_password":"a different secure password",
			"confirmation":"a different secure password"
		}`),
	)
	change.Header.Set("Content-Type", "application/json")
	change.Header.Set("Origin", "https://postqron.com")
	change.Header.Set("X-CSRF-Token", passwordCSRF(sessionToken))
	change.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
	changed := httptest.NewRecorder()
	handler.ServeHTTP(changed, change)
	if changed.Code != http.StatusOK ||
		!strings.Contains(changed.Body.String(), `"changed":true`) {
		t.Fatalf("password change = %d %s", changed.Code, changed.Body.String())
	}
	changedCookies := changed.Result().Cookies()
	changedSessionCookie := cookieByName(changedCookies, SessionCookieName)
	changedCSRFCookie := cookieByName(changedCookies, CSRFCookieName)
	if changedSessionCookie == nil ||
		changedCSRFCookie == nil ||
		!changedSessionCookie.HttpOnly ||
		!changedSessionCookie.Secure ||
		changedCSRFCookie.HttpOnly ||
		!changedCSRFCookie.Secure ||
		changedSessionCookie.Value == sessionToken ||
		changedCSRFCookie.Value != csrfTokenValue(changedSessionCookie.Value) ||
		changedCSRFCookie.Value == passwordCSRF(sessionToken) ||
		strings.Contains(changed.Body.String(), "a different secure password") {
		t.Fatalf(
			"unsafe password change response: session=%+v csrf=%+v body=%s",
			changedSessionCookie,
			changedCSRFCookie,
			changed.Body.String(),
		)
	}

	logout := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/logout",
		nil,
	)
	logout.Header.Set("Origin", "https://postqron.com")
	logout.Header.Set("X-CSRF-Token", passwordCSRF(sessionToken))
	logout.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionToken})
	loggedOut := httptest.NewRecorder()
	handler.ServeHTTP(loggedOut, logout)
	if loggedOut.Code != http.StatusNoContent {
		t.Fatalf("logout = %d %s", loggedOut.Code, loggedOut.Body.String())
	}
	if store.revokedTokenHash != tokenDigest(sessionToken) {
		t.Fatalf("logout token hash = %q", store.revokedTokenHash)
	}
	logoutCookies := loggedOut.Result().Cookies()
	clearedSessionCookie := cookieByName(logoutCookies, SessionCookieName)
	clearedCSRFCookie := cookieByName(logoutCookies, CSRFCookieName)
	if clearedSessionCookie == nil || clearedCSRFCookie == nil ||
		clearedSessionCookie.MaxAge != -1 || clearedCSRFCookie.MaxAge != -1 {
		t.Fatalf(
			"logout cookies were not cleared: session=%+v csrf=%+v",
			clearedSessionCookie,
			clearedCSRFCookie,
		)
	}
}

func TestPasswordPreflightAllowsCSRFCHeader(t *testing.T) {
	store := &passwordStoreFixture{}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPasswordHandler(service, "https://postqron.com")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/auth/password/change",
		nil,
	)
	request.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight = %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(
		response.Header().Get("Access-Control-Allow-Headers"),
		"X-CSRF-Token",
	) {
		t.Fatalf("allow headers = %q", response.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestPasswordChangeHTTPRejectsCSRFAndExpiredSessionSafely(t *testing.T) {
	store := &passwordStoreFixture{}
	service, err := NewPasswordService(
		store,
		func() time.Time { return testNow },
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPasswordHandler(service, "https://postqron.com")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken := "opaque-current-session"
	body := `{
		"current_password":"correct horse battery staple",
		"new_password":"a different secure password",
		"confirmation":"a different secure password"
	}`
	for _, test := range []struct {
		name       string
		csrf       string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "invalid csrf",
			csrf:       "forged",
			wantStatus: http.StatusForbidden,
			wantCode:   "AUTH_CSRF_INVALID",
		},
		{
			name:       "expired session",
			csrf:       passwordCSRF(sessionToken),
			wantStatus: http.StatusUnauthorized,
			wantCode:   "AUTH_UNAUTHENTICATED",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/auth/password/change",
				strings.NewReader(body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://postqron.com")
			request.Header.Set("X-CSRF-Token", test.csrf)
			request.AddCookie(&http.Cookie{
				Name:  SessionCookieName,
				Value: sessionToken,
			})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus ||
				!strings.Contains(response.Body.String(), test.wantCode) ||
				strings.Contains(response.Body.String(), "correct horse") ||
				strings.Contains(response.Body.String(), "different secure") {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func passwordCSRF(sessionToken string) string {
	digest := sha256.Sum256([]byte("postqron-auth-csrf\x00" + sessionToken))
	return hex.EncodeToString(digest[:])
}
