package cookieconsent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testResolver struct{}

func (testResolver) Resolve(
	_ context.Context,
	request *http.Request,
) (Resolution, error) {
	if accountID := request.Header.Get("X-Test-Account"); accountID != "" {
		return Resolution{Subject: Subject{Kind: SubjectAccount, ID: accountID}}, nil
	}
	if cookie, err := request.Cookie(AnonymousSubjectCookieName); err == nil {
		return Resolution{Subject: Subject{Kind: SubjectBrowser, ID: cookie.Value}}, nil
	}
	return Resolution{
		Subject:   testBrowser("http-first"),
		SetCookie: true,
	}, nil
}

func testHTTPHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	service, _, _, _ := testService(t)
	handler, err := NewHTTPHandler(service, testResolver{})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHTTPFirstVisitAcceptRejectGranularRevokeAndRetry(t *testing.T) {
	handler := testHTTPHandler(t)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(
		http.MethodGet, "/api/v1/cookie-preferences", nil,
	))
	if first.Code != http.StatusOK {
		t.Fatalf("first visit status = %d body=%s", first.Code, first.Body.String())
	}
	var initial PreferenceState
	if err := json.Unmarshal(first.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	if initial.HasRecordedChoice || initial.Analytics || !initial.Necessary {
		t.Fatalf("initial = %+v", initial)
	}
	cookies := first.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != AnonymousSubjectCookieName ||
		!cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("anonymous cookie = %#v", cookies)
	}

	cases := []struct {
		name string
		key  string
		body string
	}{
		{
			name: "accept",
			key:  "http-accept-0001",
			body: `{"policy_version":"1.0","source":"banner","preferences":true,"analytics":true,"marketing":true}`,
		},
		{
			name: "reject",
			key:  "http-reject-0002",
			body: `{"policy_version":"1.0","source":"banner","preferences":false,"analytics":false,"marketing":false}`,
		},
		{
			name: "granular",
			key:  "http-granular-0003",
			body: `{"policy_version":"1.0","source":"preferences_center","preferences":true,"analytics":false,"marketing":true}`,
		},
		{
			name: "revoke",
			key:  "http-revoke-0004",
			body: `{"policy_version":"1.0","source":"preferences_center","preferences":false,"analytics":false,"marketing":false}`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPut,
				"/api/v1/cookie-preferences",
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", test.key)
			request.AddCookie(cookies[0])
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
		})
	}
	retry := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/cookie-preferences",
		strings.NewReader(cases[3].body),
	)
	retry.Header.Set("Content-Type", "application/json")
	retry.Header.Set("Idempotency-Key", cases[3].key)
	retry.AddCookie(cookies[0])
	retryResponse := httptest.NewRecorder()
	handler.ServeHTTP(retryResponse, retry)
	if retryResponse.Code != http.StatusOK ||
		retryResponse.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("retry status=%d headers=%v", retryResponse.Code, retryResponse.Header())
	}
}

func TestHTTPRejectsUnknownCategoriesNecessaryAndAlteredPolicy(t *testing.T) {
	handler := testHTTPHandler(t)
	for _, test := range []struct {
		name   string
		body   string
		status int
	}{
		{
			name:   "unknown",
			body:   `{"policy_version":"1.0","source":"banner","preferences":false,"analytics":false,"marketing":false,"profiling":true}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "necessary cannot be supplied",
			body:   `{"policy_version":"1.0","source":"banner","necessary":false,"preferences":false,"analytics":false,"marketing":false}`,
			status: http.StatusBadRequest,
		},
		{
			name:   "altered version",
			body:   `{"policy_version":"999.0","source":"banner","preferences":false,"analytics":false,"marketing":false}`,
			status: http.StatusConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPut,
				"/api/v1/cookie-preferences",
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "invalid-input-0001")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestHTTPAccountIsolationExportAndErasure(t *testing.T) {
	handler := testHTTPHandler(t)
	put := func(account, key string) {
		t.Helper()
		request := httptest.NewRequest(
			http.MethodPut,
			"/api/v1/cookie-preferences",
			strings.NewReader(
				`{"policy_version":"1.0","source":"account","preferences":false,"analytics":true,"marketing":false}`,
			),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set("X-Test-Account", account)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("put status=%d body=%s", response.Code, response.Body.String())
		}
	}
	put("account-http-one", "account-one-0001")

	secondRequest := httptest.NewRequest(
		http.MethodGet, "/api/v1/cookie-preferences", nil,
	)
	secondRequest.Header.Set("X-Test-Account", "account-http-two")
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, secondRequest)
	var state PreferenceState
	_ = json.Unmarshal(second.Body.Bytes(), &state)
	if state.Analytics || state.HasRecordedChoice {
		t.Fatalf("second account leaked state: %+v", state)
	}

	exportRequest := httptest.NewRequest(
		http.MethodGet, "/api/v1/cookie-preferences/export", nil,
	)
	exportRequest.Header.Set("X-Test-Account", "account-http-one")
	exportResponse := httptest.NewRecorder()
	handler.ServeHTTP(exportResponse, exportRequest)
	var exported PortableExport
	_ = json.Unmarshal(exportResponse.Body.Bytes(), &exported)
	if exportResponse.Code != http.StatusOK || len(exported.Evidence) != 3 {
		t.Fatalf("export status=%d value=%+v", exportResponse.Code, exported)
	}

	deleteRequest := httptest.NewRequest(
		http.MethodDelete, "/api/v1/cookie-preferences", nil,
	)
	deleteRequest.Header.Set("X-Test-Account", "account-http-one")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestCalendarExpiryUsesSixMonths(t *testing.T) {
	value := time.Date(2026, 8, 31, 10, 15, 0, 0, time.UTC)
	got := addSixCalendarMonths(value)
	want := time.Date(2027, 2, 28, 10, 15, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("addSixCalendarMonths()=%s want %s", got, want)
	}
}
