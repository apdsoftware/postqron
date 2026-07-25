package featurehost

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

func TestFixtureFeatureIsDiscoveredStartedAndReachedOverHTTP(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "fixture")
	if err := os.MkdirAll(filepath.Join(directory, "migrations"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"server.go":                     "package fixture\n",
		"migrations/000001_fixture.sql": "-- fixture\n",
	} {
		if err := os.WriteFile(filepath.Join(directory, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `schema_version: 1
id: fixture
kind: api
version: 0.1.0
entrypoints:
  server: ./server.go
dependencies: []
migrations:
  - ./migrations/000001_fixture.sql
server:
  routes:
    - path: /fixture
      handler: fixture
      methods: [GET]
      visibility: public
`
	if err := os.WriteFile(
		filepath.Join(directory, "feature.yaml"),
		[]byte(manifest),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	features, err := featureruntime.Discover(root)
	if err != nil {
		t.Fatal(err)
	}

	var received Dependencies
	module := &fakeModule{
		handlers: map[string]http.Handler{
			"fixture": http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte("fixture active"))
			}),
		},
	}
	registry := NewRegistry()
	if err := registry.Register(
		"./server.go",
		func(
			_ context.Context,
			_ featureruntime.Feature,
			dependencies Dependencies,
		) (Module, error) {
			received = dependencies
			return module, nil
		},
	); err != nil {
		t.Fatal(err)
	}
	host, err := New(features, registry, testDependencies(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := host.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/fixture", nil)
	response := httptest.NewRecorder()
	host.PublicHandler().ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "fixture active" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if module.starts != 1 {
		t.Fatalf("module starts = %d, want 1", module.starts)
	}
	if received.Database == nil || received.Logger == nil || received.Clock == nil {
		t.Fatalf("factory did not receive explicit dependencies: %+v", received)
	}
	if statuses := host.Statuses(); len(statuses) != 1 || statuses[0].State != StateActive {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestRouteCollisionBlocksHostCreation(t *testing.T) {
	features := []featureruntime.Feature{
		testFeature("first", "/collision", "public", true),
		testFeature("second", "/collision", "private", true),
	}

	_, err := New(features, NewRegistry(), testDependencies(), nil)

	if err == nil ||
		!strings.Contains(err.Error(), `route collision "GET /api/v1/collision"`) ||
		!strings.Contains(err.Error(), `"first" and "second"`) {
		t.Fatalf("New() error = %v, want readable route collision", err)
	}
}

func TestPrivateRouteRequiresExplicitAuthenticatedChannel(t *testing.T) {
	feature := testFeature("admin", "/admin", "private", true)
	registry := NewRegistry()
	if err := registry.Register("./server.go", func(
		context.Context,
		featureruntime.Feature,
		Dependencies,
	) (Module, error) {
		return &fakeModule{handlers: map[string]http.Handler{
			"handler": http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			}),
		}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	host, err := New([]featureruntime.Feature{feature}, registry, testDependencies(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	publicResponse := httptest.NewRecorder()
	host.PublicHandler().ServeHTTP(
		publicResponse,
		httptest.NewRequest(http.MethodGet, "/api/v1/admin", nil),
	)
	if publicResponse.Code != http.StatusNotFound {
		t.Fatalf("private route on public mux returned %d", publicResponse.Code)
	}
	if _, err := host.AuthenticatedHandler(nil); err == nil {
		t.Fatal("AuthenticatedHandler(nil) did not reject an unauthenticated channel")
	}
	private, err := host.AuthenticatedHandler(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "Bearer test" {
				http.Error(writer, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(writer, request)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	unauthorized := httptest.NewRecorder()
	private.ServeHTTP(
		unauthorized,
		httptest.NewRequest(http.MethodGet, "/api/v1/admin", nil),
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized response = %d", unauthorized.Code)
	}
	authorizedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/admin", nil)
	authorizedRequest.Header.Set("Authorization", "Bearer test")
	authorized := httptest.NewRecorder()
	private.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("authorized response = %d", authorized.Code)
	}
}

func TestReadinessFailsForRequiredModuleOrMigration(t *testing.T) {
	required := testFeature("required", "/required", "public", true)
	optional := testFeature("optional", "/optional", "public", false)
	registry := NewRegistry()
	if err := registry.Register("./server.go", func(
		_ context.Context,
		feature featureruntime.Feature,
		_ Dependencies,
	) (Module, error) {
		module := &fakeModule{handlers: map[string]http.Handler{
			"handler": http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		}}
		if feature.Manifest.ID == "optional" {
			module.readyError = errors.New("optional unavailable")
		}
		return module, nil
	}); err != nil {
		t.Fatal(err)
	}
	migrations := &fakeMigrations{
		readyErrors: map[string]error{"required": errors.New("migration pending")},
	}
	host, err := New(
		[]featureruntime.Feature{required, optional},
		registry,
		testDependencies(),
		migrations,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	err = host.Ready(context.Background())

	if err == nil || !strings.Contains(err.Error(), `required feature "required" migrations`) {
		t.Fatalf("Ready() error = %v, want required migration failure", err)
	}
	if strings.Contains(err.Error(), "optional unavailable") {
		t.Fatalf("Ready() included optional module failure: %v", err)
	}
}

func TestLifecycleStopsActiveModulesInReverseOrder(t *testing.T) {
	var events []string
	registry := NewRegistry()
	if err := registry.Register("./first.go", lifecycleFactory("first", &events)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("./second.go", lifecycleFactory("second", &events)); err != nil {
		t.Fatal(err)
	}
	first := testFeature("first", "", "public", true)
	first.Manifest.Entrypoints.Server = "./first.go"
	second := testFeature("second", "", "public", true)
	second.Manifest.Entrypoints.Server = "./second.go"
	host, err := New(
		[]featureruntime.Feature{first, second},
		registry,
		testDependencies(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(events, ","); got != "start:first,start:second,stop:second,stop:first" {
		t.Fatalf("lifecycle = %s", got)
	}
}

func lifecycleFactory(id string, events *[]string) Factory {
	return func(
		context.Context,
		featureruntime.Feature,
		Dependencies,
	) (Module, error) {
		return &fakeModule{
			start: func() { *events = append(*events, "start:"+id) },
			stop:  func() { *events = append(*events, "stop:"+id) },
		}, nil
	}
}

func testFeature(
	id, routePath, visibility string,
	required bool,
) featureruntime.Feature {
	feature := featureruntime.Feature{
		Manifest: featureruntime.Manifest{
			SchemaVersion: 1,
			ID:            id,
			Kind:          "api",
			Version:       "0.1.0",
			Entrypoints:   featureruntime.Entrypoints{Server: "./server.go"},
			Required:      &required,
		},
	}
	if routePath != "" {
		feature.Manifest.Server.Routes = []featureruntime.ServerRoute{{
			Path:       routePath,
			Handler:    "handler",
			Methods:    []string{http.MethodGet},
			Visibility: visibility,
		}}
	}
	return feature
}

func testDependencies() Dependencies {
	return Dependencies{
		Database: struct{}{},
		Config:   map[string]string{"environment": "test"},
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:    func() time.Time { return time.Unix(1, 0).UTC() },
	}
}

type fakeModule struct {
	handlers   map[string]http.Handler
	readyError error
	start      func()
	stop       func()
	starts     int
	stops      int
}

func (module *fakeModule) Start(context.Context) error {
	module.starts++
	if module.start != nil {
		module.start()
	}
	return nil
}

func (module *fakeModule) Stop(context.Context) error {
	module.stops++
	if module.stop != nil {
		module.stop()
	}
	return nil
}

func (module *fakeModule) Ready(context.Context) error {
	return module.readyError
}

func (module *fakeModule) Handler(name string) (http.Handler, bool) {
	handler, ok := module.handlers[name]
	return handler, ok
}

type fakeMigrations struct {
	applyErrors map[string]error
	readyErrors map[string]error
}

func (migrations *fakeMigrations) Apply(
	_ context.Context,
	feature featureruntime.Feature,
	_ any,
) error {
	return migrations.applyErrors[feature.Manifest.ID]
}

func (migrations *fakeMigrations) Ready(
	_ context.Context,
	feature featureruntime.Feature,
	_ any,
) error {
	return migrations.readyErrors[feature.Manifest.ID]
}
