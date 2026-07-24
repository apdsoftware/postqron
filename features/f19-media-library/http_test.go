package medialibrary

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type authenticatorStub struct{ authenticated bool }

func (stub authenticatorStub) AccountID(*http.Request) (string, bool) {
	return "account-1", stub.authenticated
}

func TestHTTPUploadSearchAndComposerReference(t *testing.T) {
	dependencies := newTestDependencies(t)
	handler := NewHTTPHandler(
		dependencies.service,
		authenticatorStub{authenticated: true},
	)
	payload := []byte(`{
		"original_name":"launch.jpg",
		"content_type":"image/jpeg",
		"size_bytes":1024,
		"idempotency_key":"http-upload-1"
	}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/media/uploads",
		bytes.NewReader(payload),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	var ticket UploadTicket
	if err := json.Unmarshal(response.Body.Bytes(), &ticket); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/media/uploads/"+ticket.Upload.ID+"/complete",
		nil,
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("complete status = %d, body = %s", response.Code, response.Body.String())
	}
	var asset Asset
	if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/media/assets/"+asset.ID+"/composer-reference",
		nil,
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("composer status = %d, body = %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/media/assets?q=launch&kind=image",
		nil,
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("search status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestHTTPRejectsUnauthenticatedAndUnknownFields(t *testing.T) {
	dependencies := newTestDependencies(t)
	unauthenticated := NewHTTPHandler(
		dependencies.service,
		authenticatorStub{authenticated: false},
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/media/assets",
		nil,
	).WithContext(context.Background())
	response := httptest.NewRecorder()
	unauthenticated.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}

	handler := NewHTTPHandler(
		dependencies.service,
		authenticatorStub{authenticated: true},
	)
	request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/media/uploads",
		bytes.NewBufferString(`{"unexpected":true}`),
	)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid request status = %d", response.Code)
	}
}
