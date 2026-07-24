package smartqueue

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type authenticatorStub struct{ accountID string }

func (stub authenticatorStub) AccountID(*http.Request) (string, bool) {
	return stub.accountID, stub.accountID != ""
}

func TestHTTPCreatePreviewAndConfirm(t *testing.T) {
	service, _, _, _ := newTestService(t)
	handler := NewHTTPHandler(service, authenticatorStub{accountID: "account-1"})
	created := performSmartQueueRequest(
		handler, http.MethodPost,
		"/api/v1/workspaces/workspace-1/smart-queues",
		`{"name":"Editorial","time_zone":"UTC","interval_minutes":30,
		"horizon_days":14,"windows":[
			{"weekday":"monday","start_time":"10:00","end_time":"11:00"}
		]}`,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var queue Queue
	if err := json.Unmarshal(created.Body.Bytes(), &queue); err != nil {
		t.Fatal(err)
	}
	previewed := performSmartQueueRequest(
		handler, http.MethodPost,
		"/api/v1/workspaces/workspace-1/smart-queues/"+queue.ID+"/preview",
		`{}`,
	)
	if previewed.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", previewed.Code, previewed.Body.String())
	}
	var preview Preview
	if err := json.Unmarshal(previewed.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	confirmed := performSmartQueueRequest(
		handler, http.MethodPost,
		"/api/v1/workspaces/workspace-1/smart-queues/"+queue.ID+"/confirm",
		`{"preview_token":"`+preview.Token+`","draft_id":"draft-1",
		"channel_ids":["channel-1"],"idempotency_key":"request-1"}`,
	)
	if confirmed.Code != http.StatusCreated ||
		!strings.Contains(confirmed.Body.String(), `"local_date_time":"2026-07-27T10:00:00"`) ||
		!strings.Contains(confirmed.Header().Get("Location"), "reservation_") {
		t.Fatalf("confirm status=%d headers=%v body=%s",
			confirmed.Code, confirmed.Header(), confirmed.Body.String())
	}
}

func TestHTTPReturnsStableConflictCodeAndRejectsUnknownFields(t *testing.T) {
	service, _, _, _ := newTestService(t)
	handler := NewHTTPHandler(service, authenticatorStub{accountID: "account-1"})
	response := performSmartQueueRequest(
		handler, http.MethodPost,
		"/api/v1/workspaces/workspace-1/smart-queues",
		`{"name":"Editorial","time_zone":"UTC","interval_minutes":30,
		"horizon_days":14,"windows":[],"unexpected":true}`,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"field_code":"body_invalid"`) {
		t.Fatalf("unknown status=%d body=%s", response.Code, response.Body.String())
	}
	response = performSmartQueueRequest(
		handler, http.MethodPost,
		"/api/v1/workspaces/workspace-1/smart-queues/missing/confirm",
		`{"preview_token":"missing","draft_id":"draft",
		"channel_ids":["channel"],"idempotency_key":"request"}`,
	)
	if response.Code != http.StatusNotFound ||
		!strings.Contains(response.Body.String(), `"code":"not_found"`) {
		t.Fatalf("missing status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPRequiresAuthentication(t *testing.T) {
	service, _, _, _ := newTestService(t)
	handler := NewHTTPHandler(service, authenticatorStub{})
	response := performSmartQueueRequest(
		handler, http.MethodPost,
		"/api/v1/workspaces/workspace-1/smart-queues",
		`{}`,
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func performSmartQueueRequest(
	handler http.Handler, method, target, body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
