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

func TestHTTPScheduleRequiresKeyAndReplaysOriginalResponse(t *testing.T) {
	handler := newTestHTTPHandler(t)
	body := `{
		"draft_id":"draft-1",
		"channel_ids":["instagram-1"],
		"scheduled_at":{"local_date_time":"2026-07-25T10:00:00","time_zone":"UTC"}
	}`
	missingRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		strings.NewReader(body),
	)
	missingRequest.Header.Set("Content-Type", "application/json")
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusBadRequest ||
		!strings.Contains(missingResponse.Body.String(), `"field_code":"idempotency_key_invalid"`) {
		t.Fatalf("missing key response=%d %s", missingResponse.Code, missingResponse.Body.String())
	}

	first := performSchedulingRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		body,
	)
	var created ScheduledPostView
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	mutated := performSchedulingRequest(
		handler,
		http.MethodPost,
		schedulingPath("workspace-1", created.ID)+"/reschedule",
		`{"expected_revision":1,"scheduled_at":{`+
			`"local_date_time":"2026-07-25T12:00:00","time_zone":"UTC"}}`,
	)
	if mutated.Code != http.StatusOK {
		t.Fatalf("mutate scheduled post=%d %s", mutated.Code, mutated.Body.String())
	}
	replay := performSchedulingRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		body,
	)
	if first.Code != http.StatusCreated || replay.Code != http.StatusCreated ||
		replay.Header().Get("Idempotency-Replayed") != "true" ||
		first.Body.String() != replay.Body.String() {
		t.Fatalf("first=%d %s replay=%d headers=%v %s", first.Code, first.Body.String(), replay.Code, replay.Header(), replay.Body.String())
	}
	mismatch := performSchedulingRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		strings.Replace(body, "10:00:00", "11:00:00", 1),
	)
	if mismatch.Code != http.StatusConflict ||
		!strings.Contains(mismatch.Body.String(), `"code":"idempotency_payload_mismatch"`) ||
		!strings.Contains(mismatch.Body.String(), `"retryable":false`) {
		t.Fatalf("mismatch=%d %s", mismatch.Code, mismatch.Body.String())
	}
}

func TestHTTPDuplicateReplaysOriginalResponseAfterMutationAndRejectsMismatch(t *testing.T) {
	handler := newTestHTTPHandler(t)
	sourceResponse := performSchedulingRequestWithKey(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/scheduled-posts",
		`{"draft_id":"draft-1","channel_ids":["instagram-1"],`+
			`"scheduled_at":{"local_date_time":"2026-07-25T10:00:00","time_zone":"UTC"}}`,
		"schedule-source",
	)
	if sourceResponse.Code != http.StatusCreated {
		t.Fatalf("source=%d %s", sourceResponse.Code, sourceResponse.Body.String())
	}
	var source ScheduledPostView
	if err := json.Unmarshal(sourceResponse.Body.Bytes(), &source); err != nil {
		t.Fatal(err)
	}
	duplicateBody := `{"expected_revision":1}`
	first := performSchedulingRequestWithKey(
		handler,
		http.MethodPost,
		schedulingPath("workspace-1", source.ID)+"/duplicate",
		duplicateBody,
		"duplicate-source",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf("duplicate=%d %s", first.Code, first.Body.String())
	}
	var duplicate ScheduledPostView
	if err := json.Unmarshal(first.Body.Bytes(), &duplicate); err != nil {
		t.Fatal(err)
	}
	mutated := performSchedulingRequestWithKey(
		handler,
		http.MethodPost,
		schedulingPath("workspace-1", duplicate.ID)+"/reschedule",
		`{"expected_revision":1,"scheduled_at":{`+
			`"local_date_time":"2026-07-25T13:00:00","time_zone":"UTC"}}`,
		"unused-mutation-key",
	)
	if mutated.Code != http.StatusOK {
		t.Fatalf("mutate duplicate=%d %s", mutated.Code, mutated.Body.String())
	}
	replay := performSchedulingRequestWithKey(
		handler,
		http.MethodPost,
		schedulingPath("workspace-1", source.ID)+"/duplicate",
		duplicateBody,
		"duplicate-source",
	)
	if replay.Code != http.StatusCreated ||
		replay.Header().Get("Idempotency-Replayed") != "true" ||
		first.Body.String() != replay.Body.String() {
		t.Fatalf("first=%d %s replay=%d headers=%v %s", first.Code, first.Body.String(), replay.Code, replay.Header(), replay.Body.String())
	}
	mismatch := performSchedulingRequestWithKey(
		handler,
		http.MethodPost,
		schedulingPath("workspace-1", source.ID)+"/duplicate",
		`{"expected_revision":1,"scheduled_at":{`+
			`"local_date_time":"2026-07-25T14:00:00","time_zone":"UTC"}}`,
		"duplicate-source",
	)
	if mismatch.Code != http.StatusConflict ||
		!strings.Contains(mismatch.Body.String(), `"code":"idempotency_payload_mismatch"`) {
		t.Fatalf("mismatch=%d %s", mismatch.Code, mismatch.Body.String())
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
	return performSchedulingRequestWithKey(
		handler,
		method,
		target,
		body,
		"http-test-request",
	)
}

func performSchedulingRequestWithKey(
	handler http.Handler,
	method, target, body, idempotencyKey string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request.WithContext(context.Background()))
	return response
}

func schedulingPath(workspaceID, postID string) string {
	return "/api/v1/workspaces/" + workspaceID + "/scheduled-posts/" + postID
}
