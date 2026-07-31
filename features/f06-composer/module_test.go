package composer

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModuleUsesBlockedCatalogUntilD02MatrixIsConfigured(t *testing.T) {
	database, err := sql.Open(
		"pgx",
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	module, err := NewPostgresModule(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Configure(map[string]string{
		configAllowedOrigins: "https://postqron.com",
	}); err != nil {
		t.Fatal(err)
	}
	if module.service.catalog.Status != "blocked" ||
		module.service.catalog.Version != "pending-d02-301" ||
		len(module.service.catalog.Capabilities) != 0 {
		t.Fatalf("default catalog = %#v", module.service.catalog)
	}
	handler, ok := module.Handler(composerRuntimeHandlerName)
	if !ok {
		t.Fatal("composer runtime handler is not available")
	}
	preflight := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/workspaces/workspace-1/drafts",
		nil,
	)
	preflight.Header.Set("Origin", "https://postqron.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, preflight)
	if response.Code != http.StatusNoContent ||
		response.Header().Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(response.Header().Get("Vary"), "Origin") {
		t.Fatalf("preflight = %d headers=%v", response.Code, response.Header())
	}
}

func TestModuleRejectsInvalidCatalogAndHostileOrigin(t *testing.T) {
	database, err := sql.Open(
		"pgx",
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	module, err := NewPostgresModule(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := module.Configure(map[string]string{
		configCapabilitiesJSON: `{"version":"v1","status":"active","capabilities":[
			{"id":"duplicate","provider":"fixture","channel_type":"one","format":"text","available":true,
			 "text":{"allowed":true,"required":false},"link":{"allowed":false,"required":false},
			 "media":{"allowed":false},"thread":{"allowed":false,"required":false}},
			{"id":"duplicate","provider":"fixture","channel_type":"two","format":"text","available":true,
			 "text":{"allowed":true,"required":false},"link":{"allowed":false,"required":false},
			 "media":{"allowed":false},"thread":{"allowed":false,"required":false}}
		]}`,
	}); err == nil {
		t.Fatal("duplicate capability catalog was accepted")
	}
	if err := module.Configure(map[string]string{
		configAllowedOrigins: "https://postqron.com",
	}); err != nil {
		t.Fatal(err)
	}
	handler, _ := module.Handler(composerRuntimeHandlerName)
	request := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/workspaces/workspace-1/drafts",
		nil,
	)
	request.Header.Set("Origin", "https://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden ||
		response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("hostile origin = %d headers=%v", response.Code, response.Header())
	}
}

func TestRuntimeObjectStoreInsecureEndpointFlagDefaultsFailClosed(t *testing.T) {
	t.Setenv("POSTQRON_F06_S3_ALLOW_INSECURE_ENDPOINT", "")
	values := map[string]string{
		configS3Endpoint:        "http://objects.example",
		configS3Region:          "test-region",
		configS3Bucket:          "test-bucket",
		configS3AccessKeyID:     "test-access-key",
		configS3SecretAccessKey: "test-secret-key",
	}
	if _, _, err := runtimeObjectStore(values); err == nil {
		t.Fatal("runtime accepted insecure non-loopback storage by default")
	}
	values[configS3AllowInsecure] = "false"
	if _, _, err := runtimeObjectStore(values); err == nil {
		t.Fatal("runtime accepted insecure storage with explicit false flag")
	}
	values[configS3AllowInsecure] = "true"
	if _, _, err := runtimeObjectStore(values); err != nil {
		t.Fatalf("explicit insecure endpoint flag rejected: %v", err)
	}
	values[configS3AllowInsecure] = "not-a-boolean"
	if _, _, err := runtimeObjectStore(values); err == nil {
		t.Fatal("invalid insecure endpoint flag was accepted")
	}
}
