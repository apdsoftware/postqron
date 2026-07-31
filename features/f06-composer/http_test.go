package composer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type authenticatorStub struct {
	accountID string
	ok        bool
}

func (stub authenticatorStub) AccountID(*http.Request) (string, bool) {
	return stub.accountID, stub.ok
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	return NewHTTPHandler(
		newTestService(t),
		authenticatorStub{accountID: "account-1", ok: true},
	)
}

func TestHTTPDraftCRUDAndValidationContract(t *testing.T) {
	handler := newTestHandler(t)
	createBody := `{
		"content": {
			"text": "Initial draft",
			"media": [],
			"destinations": []
		}
	}`
	create := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts",
		createBody,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	var created DraftView
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Draft.ID == "" || created.Validation.Valid {
		t.Fatalf("created = %#v", created)
	}
	if location := create.Header().Get("Location"); !strings.HasSuffix(location, created.Draft.ID) {
		t.Fatalf("Location = %q", location)
	}

	validate := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts/"+created.Draft.ID+"/validate",
		"",
	)
	if validate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("validate status = %d, body = %s", validate.Code, validate.Body.String())
	}
	if !strings.Contains(validate.Body.String(), `"field":"destinations"`) ||
		!strings.Contains(validate.Body.String(), `"rule":"required"`) {
		t.Fatalf("validation body = %s", validate.Body.String())
	}

	updatePayload := map[string]any{
		"expected_revision": 1,
		"content": DraftContent{
			Text:  "Ready",
			Media: []Media{validImage("image", "image/jpeg", 1080, 1080)},
			Destinations: []Destination{{
				ID:           "image",
				ChannelID:    "image-1",
				ChannelType:  "fixture_image_channel",
				CapabilityID: "fixture:image",
				Format:       FormatImage,
			}},
		},
	}
	encodedUpdate, err := json.Marshal(updatePayload)
	if err != nil {
		t.Fatal(err)
	}
	update := performRequest(
		handler,
		http.MethodPut,
		"/api/v1/workspaces/workspace-1/drafts/"+created.Draft.ID,
		string(encodedUpdate),
	)
	if update.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", update.Code, update.Body.String())
	}

	list := performRequest(
		handler,
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/drafts",
		"",
	)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), created.Draft.ID) {
		t.Fatalf("list status = %d, body = %s", list.Code, list.Body.String())
	}

	deleteResponse := performRequest(
		handler,
		http.MethodDelete,
		"/api/v1/workspaces/workspace-1/drafts/"+created.Draft.ID+"?revision=2",
		"",
	)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf(
			"delete status = %d, body = %s",
			deleteResponse.Code,
			deleteResponse.Body.String(),
		)
	}
}

func TestHTTPRejectsUnknownFieldsAndStaleUpdates(t *testing.T) {
	handler := newTestHandler(t)
	response := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts",
		`{"content":{"text":"","media":[],"destinations":[]},"unknown":true}`,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"field":"body"`) ||
		!strings.Contains(response.Body.String(), `"rule":"valid_json"`) {
		t.Fatalf("error body = %s", response.Body.String())
	}

	response = performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts",
		`{"content":{"text":"","media":[
			{"id":"same","kind":"image","content_type":"image/jpeg","size_bytes":1,"width":1,"height":1,"inspection_status":"ready","url":"/one"},
			{"id":"same","kind":"image","content_type":"image/jpeg","size_bytes":1,"width":1,"height":1,"inspection_status":"ready","url":"/two"}
		],"thread":[],"destinations":[],"link":""}}`,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"field":"media[1].id"`) ||
		!strings.Contains(response.Body.String(), `"rule":"unique"`) {
		t.Fatalf("duplicate id response = %d %s", response.Code, response.Body.String())
	}
}

func TestHTTPRequiresAuthentication(t *testing.T) {
	service, err := NewService(
		NewMemoryRepository(),
		authorizerStub{allowed: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(service, authenticatorStub{})
	response := performRequest(
		handler,
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/drafts",
		"",
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHTTPMapsUnavailableStorageToClientSafeRetryable503(t *testing.T) {
	response := httptest.NewRecorder()
	writeServiceError(response, ErrStorageUnavailable)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("storage status = %d", response.Code)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
			Message   string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "media_storage_unavailable" ||
		!body.Error.Retryable ||
		strings.Contains(response.Body.String(), ErrStorageUnavailable.Error()) {
		t.Fatalf("storage error body = %s", response.Body.String())
	}
}

func performRequest(
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
