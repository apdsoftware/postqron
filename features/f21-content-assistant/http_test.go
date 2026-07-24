package contentassistant

import (
	"bytes"
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

func newHTTPHandlerForTest(t *testing.T, generator Generator) http.Handler {
	t.Helper()
	service, _, _ := newServiceForTest(t, generator)
	return NewHTTPHandler(
		service,
		authenticatorStub{accountID: "account-1", ok: true},
	)
}

func TestHTTPGeneratedProposalRequiresExplicitConfirmation(t *testing.T) {
	handler := newHTTPHandlerForTest(t, generatedStub())
	create := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts/draft-1/content-assistant/proposals",
		`{"alternatives_per_channel":1}`,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	var proposal Proposal
	if err := json.Unmarshal(create.Body.Bytes(), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.Status != StatusPending || len(proposal.Candidates) != 2 {
		t.Fatalf("proposal = %#v", proposal)
	}
	if !strings.Contains(create.Header().Get("Location"), proposal.ID) {
		t.Fatalf("Location = %q", create.Header().Get("Location"))
	}

	withoutConfirmation := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/content-assistant/proposals/"+
			proposal.ID+"/confirm",
		`{"expected_revision":1,"confirmation":false,"candidate_ids":["`+
			proposal.Candidates[0].ID+`"]}`,
	)
	if withoutConfirmation.Code != http.StatusBadRequest ||
		!strings.Contains(
			withoutConfirmation.Body.String(),
			`"code":"explicit_confirmation_required"`,
		) {
		t.Fatalf(
			"without confirmation = %d %s",
			withoutConfirmation.Code,
			withoutConfirmation.Body.String(),
		)
	}

	confirm := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/content-assistant/proposals/"+
			proposal.ID+"/confirm",
		`{"expected_revision":1,"confirmation":true,"candidate_ids":["`+
			proposal.Candidates[0].ID+`"]}`,
	)
	if confirm.Code != http.StatusOK ||
		!strings.Contains(confirm.Body.String(), `"status":"confirmed"`) ||
		!strings.Contains(confirm.Body.String(), `"draft_revision":7`) {
		t.Fatalf("confirm = %d %s", confirm.Code, confirm.Body.String())
	}
}

func TestHTTPAdvertisesAndAcceptsManualFallback(t *testing.T) {
	handler := newHTTPHandlerForTest(
		t,
		&generatorStub{err: ErrGeneratorUnavailable},
	)
	generated := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts/draft-1/content-assistant/proposals",
		`{}`,
	)
	if generated.Code != http.StatusServiceUnavailable ||
		!strings.Contains(
			generated.Body.String(),
			`"manual_fallback_available":true`,
		) {
		t.Fatalf("generated = %d %s", generated.Code, generated.Body.String())
	}

	manual := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts/draft-1/content-assistant/manual-proposals",
		`{"candidates":[{"destination_id":"facebook","proposed":"Manuale"}]}`,
	)
	if manual.Code != http.StatusCreated ||
		!strings.Contains(manual.Body.String(), `"source":"manual"`) ||
		!strings.Contains(manual.Body.String(), `"status":"pending"`) {
		t.Fatalf("manual = %d %s", manual.Code, manual.Body.String())
	}
}

func TestHTTPRejectsUnknownFieldsAndRequiresAuthentication(t *testing.T) {
	handler := newHTTPHandlerForTest(t, generatedStub())
	response := performRequest(
		handler,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts/draft-1/content-assistant/proposals",
		`{"unknown":true}`,
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), `"field":"body"`) {
		t.Fatalf("unknown field = %d %s", response.Code, response.Body.String())
	}

	service, _, _ := newServiceForTest(t, generatedStub())
	unauthenticated := NewHTTPHandler(service, authenticatorStub{})
	response = performRequest(
		unauthenticated,
		http.MethodPost,
		"/api/v1/workspaces/workspace-1/drafts/draft-1/content-assistant/proposals",
		`{}`,
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.Code)
	}
}

func performRequest(
	handler http.Handler,
	method, target, body string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "http-test-request")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
