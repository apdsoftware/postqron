package entitlements

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type requestAuthenticatorStub struct{}

func (requestAuthenticatorStub) AccountID(*http.Request) (string, bool) {
	return "", false
}

type workspaceViewerStub struct{}

func (workspaceViewerStub) CanViewBilling(
	context.Context,
	string,
	string,
) (bool, error) {
	return false, nil
}

func TestPublicPlansEndpointOnlyContainsD03Catalog(t *testing.T) {
	handler := NewHTTPHandler(
		NewService(&serviceStoreStub{}),
		nil,
		nil,
		http.NotFoundHandler(),
		requestAuthenticatorStub{},
		workspaceViewerStub{},
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/billing/plans", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	payload := strings.ToLower(string(body))
	for _, required := range []string{`"provider":"stripe"`, `"start"`, `"pro"`, `"team"`} {
		if !strings.Contains(payload, required) {
			t.Fatalf("public catalog is missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{"internal", "unlimited", "override"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("public catalog contains %q: %s", forbidden, body)
		}
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("Cache-Control = %q", cacheControl)
	}
}
