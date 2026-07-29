package main

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/api/internal/featurehost"
)

func TestApiFeatureSetAvoidsRouteCollisions(t *testing.T) {
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
	features, err := featureruntime.FilterKind(discovered, "api")
	if err != nil {
		t.Fatal(err)
	}
	registry := featurehost.NewRegistry()
	if err := registerFeatureFactories(registry); err != nil {
		t.Fatal(err)
	}
	host, err := featurehost.New(features, registry, runtimeTestDependencies(t), nil)
	if err != nil {
		t.Fatalf("featurehost.New() error = %v", err)
	}
	if host == nil {
		t.Fatal("featurehost.New() returned a nil host")
	}
}

func runtimeTestDependencies(t *testing.T) featurehost.Dependencies {
	t.Helper()
	database, err := openDatabase(
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return featurehost.Dependencies{
		PostgreSQL: database,
		Config: map[string]string{
			"billing.app_domain": "app.postqron.example",
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	}
}
