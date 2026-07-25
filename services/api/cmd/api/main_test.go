package main

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultFeatureRootsIncludeFoundationAndAutonomousSlices(t *testing.T) {
	t.Setenv("POSTQRON_FEATURE_ROOTS", "")
	got := featureRoots("POSTQRON_FEATURE_ROOTS", defaultFeatureRoots())
	want := []string{"services/api/features", "features"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("featureRoots() = %v, want %v", got, want)
	}
	if os.PathListSeparator == 0 {
		t.Fatal("path list separator is unavailable")
	}
}

func TestOpenDatabaseRequiresPostgresConfiguration(t *testing.T) {
	if _, err := openDatabase("  "); err == nil ||
		!strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("openDatabase() error = %v, want required DATABASE_URL", err)
	}

	database, err := openDatabase(
		"postgres://postqron:test@127.0.0.1:1/postqron?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if database == nil {
		t.Fatal("openDatabase() returned a nil typed connection")
	}
	if database.Stats().MaxOpenConnections != 20 {
		t.Fatalf(
			"MaxOpenConnections = %d, want 20",
			database.Stats().MaxOpenConnections,
		)
	}
}

func TestPostgresDatabaseDependency(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not configured")
	}
	database, err := openDatabase(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("PostgreSQL dependency is not reachable: %v", err)
	}
}
