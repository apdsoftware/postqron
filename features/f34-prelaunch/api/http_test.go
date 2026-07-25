package prelaunch

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	service, err := NewService(NewMemoryRepository(), func() time.Time {
		return time.Date(2026, 7, 25, 18, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(
		service,
		Mode{Enabled: true, Source: ModeExplicitTrue},
		NewOriginPolicy("https://www.postqron.com", "production"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestAccessRequestReturnsGenericAcceptedResponse(t *testing.T) {
	handler := testHandler(t)
	payload, _ := json.Marshal(validRequest())
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/prelaunch/access-requests",
		bytes.NewReader(payload),
	)
	request.RemoteAddr = "192.0.2.40:1234"
	request.Header.Set("Origin", "https://www.postqron.com")
	response := httptest.NewRecorder()
	handler.AccessRequests(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	if response.Header().Get("Access-Control-Allow-Origin") !=
		"https://www.postqron.com" {
		t.Fatalf("CORS header = %q",
			response.Header().Get("Access-Control-Allow-Origin"))
	}
	if response.Body.String() != "{\"status\":\"accepted\"}\n" {
		t.Fatalf("body = %q", response.Body.String())
	}
}

func TestBrowserFormRedirectsToAllowedLocalizedResult(t *testing.T) {
	handler := testHandler(t)
	form := "email=person%40example.test&locale=it&access_consent=true" +
		"&marketing_consent=false&consent_policy_version=prelaunch-access-v1" +
		"&return_path=%2Fit%2Fprelaunch%2Faccess"
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/prelaunch/access-requests",
		bytes.NewBufferString(form),
	)
	request.RemoteAddr = "192.0.2.41:1234"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://www.postqron.com")
	response := httptest.NewRecorder()
	handler.AccessRequests(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	if location := response.Header().Get("Location"); location !=
		"https://www.postqron.com/it/prelaunch/access?result=success" {
		t.Fatalf("location = %q", location)
	}
}

func TestBrowserFormCannotCreateOpenRedirect(t *testing.T) {
	handler := testHandler(t)
	form := "email=person%40example.test&locale=en&access_consent=true" +
		"&marketing_consent=false&consent_policy_version=prelaunch-access-v1" +
		"&return_path=https%3A%2F%2Fattacker.example"
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/prelaunch/access-requests",
		bytes.NewBufferString(form),
	)
	request.RemoteAddr = "192.0.2.42:1234"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://www.postqron.com")
	response := httptest.NewRecorder()
	handler.AccessRequests(response, request)
	if location := response.Header().Get("Location"); location !=
		"https://www.postqron.com/prelaunch/access?result=success" {
		t.Fatalf("location = %q", location)
	}
}

func TestAccessRequestRejectsUnknownFieldsAndOrigins(t *testing.T) {
	handler := testHandler(t)
	for _, test := range []struct {
		name   string
		origin string
		body   string
		status int
	}{
		{
			"unknown field",
			"https://www.postqron.com",
			`{"email":"a@example.test","unexpected":true}`,
			http.StatusBadRequest,
		},
		{
			"origin",
			"https://attacker.example",
			`{}`,
			http.StatusForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/prelaunch/access-requests",
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			handler.AccessRequests(response, request)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestStatusExposesResolutionWithoutRawConfiguration(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/prelaunch/status",
		nil,
	)
	response := httptest.NewRecorder()
	handler.Status(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["prelaunch_mode"] != true ||
		payload["configuration"] != string(ModeExplicitTrue) {
		t.Fatalf("payload = %#v", payload)
	}
	if _, exposed := payload["raw_value"]; exposed {
		t.Fatal("status exposed the raw environment value")
	}
}

func TestClientIdentityTrustsCloudflareHeaderButNotForwardedFor(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request.RemoteAddr = "192.0.2.1:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.4")
	if got := clientIdentity(request); got != "192.0.2.1" {
		t.Fatalf("identity = %q", got)
	}
	request.Header.Set("CF-Connecting-IP", "203.0.113.5")
	if got := clientIdentity(request); got != "203.0.113.5" {
		t.Fatalf("Cloudflare identity = %q", got)
	}
}
