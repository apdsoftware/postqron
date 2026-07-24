package collaboration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type headerAuthenticator struct{}

func (headerAuthenticator) AccountID(request *http.Request) (string, bool) {
	accountID := request.Header.Get("X-Test-Account")
	return accountID, accountID != ""
}

func TestHTTPCommentAndReviewFlow(t *testing.T) {
	service, _, _ := testService(t)
	handler, err := NewHTTPHandler(service, headerAuthenticator{})
	if err != nil {
		t.Fatal(err)
	}

	unauthenticated := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/workspace-1/drafts/draft-1/comments",
		nil,
	)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, unauthenticated)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", recorder.Code)
	}

	payload := bytes.NewBufferString(`{"body":"Controllare il copy."}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts/draft-1/comments",
		payload,
	)
	request.Header.Set("X-Test-Account", "member")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("comment status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var comment Comment
	if err = json.NewDecoder(recorder.Body).Decode(&comment); err != nil {
		t.Fatal(err)
	}
	if comment.AuthorID != "member" {
		t.Fatalf("author = %q, want authenticated member", comment.AuthorID)
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts/draft-1/review",
		bytes.NewBufferString(`{"expected_revision":3,"role":"owner"}`),
	)
	request.Header.Set("X-Test-Account", "member")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown trusted field status = %d", recorder.Code)
	}
}
