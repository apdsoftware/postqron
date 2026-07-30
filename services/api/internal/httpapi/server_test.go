package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
)

func TestHealth(t *testing.T) {
	handler := New(nil, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if body := response.Body.String(); !strings.Contains(body, `"version":"test"`) {
		t.Fatalf("body = %q, want version", body)
	}
}

func TestFeatureCatalogDoesNotExposePaths(t *testing.T) {
	features := []featureruntime.Feature{{
		Directory:    "/secret/workspace/path",
		ManifestPath: "/secret/workspace/path/feature.yaml",
		Manifest: featureruntime.Manifest{
			ID:      "platform",
			Kind:    "api",
			Version: "0.1.0",
		},
	}}
	handler := New(features, "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/features", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	body := response.Body.String()
	if strings.Contains(body, "/secret/") {
		t.Fatalf("body exposes a local path: %q", body)
	}
	if !strings.Contains(body, `"id":"platform"`) {
		t.Fatalf("body = %q, want platform feature", body)
	}
	if !strings.Contains(body, `"status":"active"`) {
		t.Fatalf("body = %q, want active feature status", body)
	}
}

func TestHostedFeatureErrorAppearsInCatalogAndReadiness(t *testing.T) {
	required := true
	features := []featureruntime.Feature{{
		Manifest: featureruntime.Manifest{
			ID:          "broken",
			Kind:        "api",
			Version:     "0.1.0",
			Entrypoints: featureruntime.Entrypoints{Server: "./broken.go"},
			Required:    &required,
			Server: featureruntime.ServerModule{
				Routes: []featureruntime.ServerRoute{{
					Path:       "/broken",
					Handler:    "broken",
					Methods:    []string{http.MethodGet},
					Visibility: "public",
				}},
			},
		},
	}}
	host, err := featurehost.New(
		features,
		featurehost.NewRegistry(),
		featurehost.Dependencies{
			Database: struct{}{},
			Config:   map[string]string{},
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			Clock:    time.Now,
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler, err := NewWithHost(
		host,
		func(next http.Handler) http.Handler { return next },
		"test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}

	catalog := httptest.NewRecorder()
	handler.ServeHTTP(
		catalog,
		httptest.NewRequest(http.MethodGet, "/api/v1/features", nil),
	)
	if body := catalog.Body.String(); !strings.Contains(body, `"status":"error"`) ||
		!strings.Contains(body, `no factory registered`) {
		t.Fatalf("catalog = %q, want readable feature error", body)
	}

	readiness := httptest.NewRecorder()
	handler.ServeHTTP(
		readiness,
		httptest.NewRequest(http.MethodGet, "/readyz", nil),
	)
	if readiness.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", readiness.Code, http.StatusServiceUnavailable)
	}
}

func TestPrivateRoutesUseOnlyTheExplicitAuthenticatedOverlay(t *testing.T) {
	required := true
	feature := featureruntime.Feature{
		Manifest: featureruntime.Manifest{
			ID:          "admin",
			Kind:        "web",
			Version:     "0.1.0",
			Entrypoints: featureruntime.Entrypoints{Server: "./module.go"},
			Required:    &required,
			Server: featureruntime.ServerModule{
				Routes: []featureruntime.ServerRoute{{
					Path:       "/admin/session",
					Handler:    "admin",
					Methods:    []string{http.MethodGet},
					Visibility: "private",
				}},
			},
		},
	}
	registry := featurehost.NewRegistry()
	if err := registry.Register("admin", func(
		context.Context,
		featureruntime.Feature,
		featurehost.Dependencies,
	) (featurehost.Module, error) {
		return privateTestModule{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	host, err := featurehost.New(
		[]featureruntime.Feature{feature},
		registry,
		featurehost.Dependencies{
			Database: struct{}{},
			Config:   map[string]string{},
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			Clock:    time.Now,
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithHost(
		host,
		nil,
		"test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	); err == nil {
		t.Fatal("NewWithHost accepted private routes without authentication")
	}
	handler, err := NewWithHost(
		host,
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				if request.Header.Get("Authorization") != "Bearer session" {
					writer.WriteHeader(http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(writer, request)
			})
		},
		"test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}

	anonymous := httptest.NewRecorder()
	handler.ServeHTTP(
		anonymous,
		httptest.NewRequest(http.MethodGet, "/api/v1/admin/session", nil),
	)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous private response = %d", anonymous.Code)
	}

	authenticatedRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/session",
		nil,
	)
	authenticatedRequest.Header.Set("Authorization", "Bearer session")
	authenticated := httptest.NewRecorder()
	handler.ServeHTTP(authenticated, authenticatedRequest)
	if authenticated.Code != http.StatusNoContent {
		t.Fatalf("authenticated private response = %d", authenticated.Code)
	}

	catalog := httptest.NewRecorder()
	handler.ServeHTTP(
		catalog,
		httptest.NewRequest(http.MethodGet, "/api/v1/features", nil),
	)
	if !strings.Contains(catalog.Body.String(), `"id":"admin"`) {
		t.Fatalf("feature inventory omitted private feature: %s", catalog.Body.String())
	}

	publicFallback := httptest.NewRecorder()
	handler.ServeHTTP(
		publicFallback,
		httptest.NewRequest(http.MethodPost, "/api/v1/admin/session", nil),
	)
	if publicFallback.Code != http.StatusNotFound {
		t.Fatalf("private path leaked through public fallback: %d", publicFallback.Code)
	}
}

func TestAccountPreflightBypassesAuthenticationAndUnauthorizedHasCORS(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	discovered, err := featureruntime.Discover(
		filepath.Join(root, "services", "api", "features"),
		filepath.Join(root, "features"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var feature featureruntime.Feature
	for _, candidate := range discovered {
		if candidate.Manifest.ID == "account-privacy-runtime" {
			feature = candidate
			break
		}
	}
	if feature.Manifest.ID == "" {
		t.Fatal("account-privacy-runtime feature was not discovered")
	}
	registry := featurehost.NewRegistry()
	if err := registry.Register("account-privacy-runtime", func(
		context.Context,
		featureruntime.Feature,
		featurehost.Dependencies,
	) (featurehost.Module, error) {
		return accountTransportTestModule{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	host, err := featurehost.New(
		[]featureruntime.Feature{feature},
		registry,
		featurehost.Dependencies{
			Database: struct{}{},
			Config:   map[string]string{},
			Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			Clock:    time.Now,
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	authCalls := 0
	handler, err := NewWithHost(
		host,
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(
				writer http.ResponseWriter,
				request *http.Request,
			) {
				authCalls++
				if request.Header.Get("Authorization") == "" {
					writer.WriteHeader(http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(writer, request)
			})
		},
		"test",
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/account", nil)
	preflight.Header.Set("Origin", "https://postqron.com")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", preflightResponse.Code)
	}
	if authCalls != 0 {
		t.Fatalf("preflight authentication calls = %d, want 0", authCalls)
	}
	assertAccountTransportCORS(t, preflightResponse.Header())

	get := httptest.NewRequest(http.MethodGet, "/api/v1/account", nil)
	get.Header.Set("Origin", "https://postqron.com")
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous GET status = %d, want 401", getResponse.Code)
	}
	if authCalls != 1 {
		t.Fatalf("GET authentication calls = %d, want 1", authCalls)
	}
	assertAccountTransportCORS(t, getResponse.Header())

	originless := httptest.NewRequest(http.MethodGet, "/api/v1/account", nil)
	originlessResponse := httptest.NewRecorder()
	handler.ServeHTTP(originlessResponse, originless)
	if originlessResponse.Code != http.StatusUnauthorized {
		t.Fatalf("originless GET status = %d, want 401", originlessResponse.Code)
	}
	if authCalls != 2 {
		t.Fatalf("originless GET authentication calls = %d, want 2", authCalls)
	}
	if originlessResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("originless GET received CORS headers: %v", originlessResponse.Header())
	}

	disallowedPreflight := httptest.NewRequest(
		http.MethodOptions,
		"/api/v1/account",
		nil,
	)
	disallowedPreflight.Header.Set("Origin", "https://attacker.example")
	disallowedPreflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(disallowedPreflightResponse, disallowedPreflight)
	if disallowedPreflightResponse.Code != http.StatusForbidden {
		t.Fatalf(
			"disallowed preflight status = %d, want 403",
			disallowedPreflightResponse.Code,
		)
	}
	if authCalls != 2 {
		t.Fatalf("disallowed preflight reached authentication: calls = %d", authCalls)
	}
	if disallowedPreflightResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf(
			"disallowed preflight received CORS headers: %v",
			disallowedPreflightResponse.Header(),
		)
	}

	disallowed := httptest.NewRequest(http.MethodGet, "/api/v1/account", nil)
	disallowed.Header.Set("Origin", "https://attacker.example")
	disallowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(disallowedResponse, disallowed)
	if disallowedResponse.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin status = %d, want 403", disallowedResponse.Code)
	}
	if authCalls != 2 {
		t.Fatalf("disallowed origin reached authentication: calls = %d", authCalls)
	}
}

func assertAccountTransportCORS(t *testing.T, header http.Header) {
	t.Helper()
	if header.Get("Access-Control-Allow-Origin") != "https://postqron.com" ||
		header.Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(header.Get("Vary"), "Origin") {
		t.Fatalf("credentialed CORS headers = %v", header)
	}
}

type accountTransportTestModule struct{}

func (accountTransportTestModule) Start(context.Context) error { return nil }
func (accountTransportTestModule) Stop(context.Context) error  { return nil }
func (accountTransportTestModule) Ready(context.Context) error { return nil }
func (accountTransportTestModule) Handler(name string) (http.Handler, bool) {
	switch name {
	case "AccountPrivacy":
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}), true
	case "AccountPrivacyPreflight":
		return accountTransportCORS(http.NotFoundHandler()), true
	default:
		return nil, false
	}
}
func (accountTransportTestModule) WrapAuthenticatedRoute(
	_ string,
	next http.Handler,
) http.Handler {
	return accountTransportCORS(next)
}

func accountTransportCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		if origin != "https://postqron.com" {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		writer.Header().Set("Access-Control-Allow-Origin", origin)
		writer.Header().Set("Access-Control-Allow-Credentials", "true")
		writer.Header().Set("Vary", "Origin")
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

type privateTestModule struct{}

func (privateTestModule) Start(context.Context) error { return nil }
func (privateTestModule) Stop(context.Context) error  { return nil }
func (privateTestModule) Ready(context.Context) error { return nil }
func (privateTestModule) Handler(name string) (http.Handler, bool) {
	if name != "admin" {
		return nil, false
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}), true
}
