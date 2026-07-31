package scheduling

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type authenticatorStub struct {
	accountID string
}

func (stub authenticatorStub) AccountID(*http.Request) (string, bool) {
	return stub.accountID, stub.accountID != ""
}

func newTestHTTPHandler(t *testing.T) http.Handler {
	t.Helper()
	service, _, _ := newTestService(t)
	return NewHTTPHandler(service, authenticatorStub{accountID: "account-1"})
}

func TestHTTPScheduleCalendarRescheduleAndCancel(t *testing.T) {
	handler := newTestHTTPHandler(t)
	createdResponse := performSchedulingRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		`{
			"draft_id":"draft-1",
			"channel_ids":["instagram-1"],
			"scheduled_at":{
				"local_date_time":"2026-07-25T10:00:00",
				"time_zone":"Europe/Rome"
			}
		}`,
	)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf(
			"schedule status=%d body=%s",
			createdResponse.Code,
			createdResponse.Body.String(),
		)
	}
	var created ScheduledPostView
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" ||
		!strings.HasSuffix(createdResponse.Header().Get("Location"), created.ID) {
		t.Fatalf("created = %#v headers=%v", created, createdResponse.Header())
	}
	if strings.Contains(createdResponse.Body.String(), "active_command_id") ||
		strings.Contains(createdResponse.Body.String(), "created_by") {
		t.Fatalf("browser response exposed internal fields: %s", createdResponse.Body.String())
	}

	calendarResponse := performSchedulingRequest(
		handler,
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/calendar"+
			"?from=2026-07-25T00%3A00%3A00Z"+
			"&until=2026-07-26T00%3A00%3A00Z"+
			"&channel_id=instagram-1&status=scheduled",
		"",
	)
	if calendarResponse.Code != http.StatusOK ||
		!strings.Contains(calendarResponse.Body.String(), created.ID) ||
		!strings.Contains(calendarResponse.Body.String(), `"time_zone":"Europe/Rome"`) {
		t.Fatalf(
			"calendar status=%d body=%s",
			calendarResponse.Code,
			calendarResponse.Body.String(),
		)
	}

	rescheduleResponse := performSchedulingRequest(
		handler,
		http.MethodPost,
		schedulingPath("workspace-1", created.ID)+"/reschedule",
		`{
			"expected_revision":1,
			"scheduled_at":{
				"local_date_time":"2026-07-26T12:00:00",
				"time_zone":"UTC"
			}
		}`,
	)
	if rescheduleResponse.Code != http.StatusOK {
		t.Fatalf(
			"reschedule status=%d body=%s",
			rescheduleResponse.Code,
			rescheduleResponse.Body.String(),
		)
	}

	cancelResponse := performSchedulingRequest(
		handler,
		http.MethodPost,
		schedulingPath("workspace-1", created.ID)+"/cancel",
		`{"expected_revision":2}`,
	)
	if cancelResponse.Code != http.StatusOK ||
		!strings.Contains(cancelResponse.Body.String(), `"status":"cancelled"`) {
		t.Fatalf(
			"cancel status=%d body=%s",
			cancelResponse.Code,
			cancelResponse.Body.String(),
		)
	}
}

func TestHTTPReturnsFieldErrorsAndRejectsUnknownFields(t *testing.T) {
	handler := newTestHTTPHandler(t)
	response := performSchedulingRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		`{
			"draft_id":"draft-1",
			"channel_ids":["instagram-1"],
			"scheduled_at":{
				"local_date_time":"2026-03-29T02:30:00",
				"time_zone":"Europe/Rome"
			}
		}`,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"field_code":"local_time_nonexistent"`) {
		t.Fatalf("DST response=%d %s", response.Code, response.Body.String())
	}

	response = performSchedulingRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		`{"draft_id":"draft-1","channel_ids":["channel-1"],"scheduled_at":{
			"local_date_time":"2026-07-25T10:00:00","time_zone":"UTC"
		},"unknown":true}`,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"field":"body"`) {
		t.Fatalf("unknown field response=%d %s", response.Code, response.Body.String())
	}
}

func TestHTTPRequiresAuthentication(t *testing.T) {
	service, _, _ := newTestService(t)
	handler := NewHTTPHandler(service, authenticatorStub{})
	response := performSchedulingRequest(
		handler,
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/calendar"+
			"?from=2026-07-25T00%3A00%3A00Z&until=2026-07-26T00%3A00%3A00Z",
		"",
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPReturnsClientSafeDependencyUnavailable(t *testing.T) {
	service, err := NewService(
		NewMemoryRepository(),
		authorizerStub{allowed: true},
		unavailableContentGateway{},
		WithClock(func() time.Time { return testNow }),
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(service, authenticatorStub{accountID: "account-1"})
	response := performSchedulingRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		`{
			"draft_id":"draft-1",
			"channel_ids":["channel-1"],
			"scheduled_at":{
				"local_date_time":"2026-07-25T10:00:00",
				"time_zone":"UTC"
			}
		}`,
	)
	if response.Code != http.StatusServiceUnavailable ||
		!strings.Contains(
			response.Body.String(),
			`"code":"scheduling_dependency_unavailable"`,
		) ||
		!strings.Contains(response.Body.String(), `"retryable":true`) ||
		strings.Contains(response.Body.String(), "validate draft") {
		t.Fatalf(
			"dependency response=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
}

func performSchedulingRequest(
	handler http.Handler,
	method, target, body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request.WithContext(context.Background()))
	return response
}

func schedulingPath(workspaceID, postID string) string {
	return "/api/v1/workspaces/" + workspaceID + "/scheduled-posts/" + postID
}
