package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverAndResolveOrder(t *testing.T) {
	root := t.TempDir()
	writeFeature(t, root, "foundation", nil)
	writeFeature(t, root, "scheduler", []string{"foundation"})

	features, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	ordered, err := ResolveOrder(features)
	if err != nil {
		t.Fatalf("ResolveOrder() error = %v", err)
	}
	if got := []string{ordered[0].Manifest.ID, ordered[1].Manifest.ID}; strings.Join(got, ",") != "foundation,scheduler" {
		t.Fatalf("ResolveOrder() = %v", got)
	}
}

func TestDiscoverRejectsMissingDependency(t *testing.T) {
	root := t.TempDir()
	writeFeature(t, root, "scheduler", []string{"missing"})

	_, err := Discover(root)
	if err == nil || !strings.Contains(err.Error(), "depends on missing feature") {
		t.Fatalf("Discover() error = %v, want missing dependency", err)
	}
}

func TestDiscoverRejectsEscapingEntrypoint(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "unsafe")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := `schema_version: 1
id: unsafe
kind: api
version: 0.1.0
entrypoint: ../outside.go
dependencies: []
migrations: []
`
	if err := os.WriteFile(filepath.Join(directory, "feature.yaml"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Discover(root)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("Discover() error = %v, want path escape", err)
	}
}

func TestDiscoverReturnsDependencyOrderAndFilterKindPreservesIt(t *testing.T) {
	root := t.TempDir()
	writeFeatureWithKind(t, root, "api-child", "api", []string{"web-foundation"})
	writeFeatureWithKind(t, root, "web-foundation", "web", nil)
	writeFeatureWithKind(t, root, "worker-child", "worker", []string{"api-child"})

	features, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := featureIDs(features); strings.Join(got, ",") != "web-foundation,api-child,worker-child" {
		t.Fatalf("Discover() order = %v", got)
	}

	filtered, err := FilterKind(features, "api", "worker")
	if err != nil {
		t.Fatal(err)
	}
	if got := featureIDs(filtered); strings.Join(got, ",") != "api-child,worker-child" {
		t.Fatalf("FilterKind() = %v", got)
	}
}

func TestFilterKindRejectsUnknownKind(t *testing.T) {
	if _, err := FilterKind(nil, "cron"); err == nil {
		t.Fatal("FilterKind() accepted an unknown kind")
	}
}

func writeFeature(t *testing.T, root, id string, dependencies []string) {
	writeFeatureWithKind(t, root, id, "api", dependencies)
}

func writeFeatureWithKind(
	t *testing.T,
	root, id, kind string,
	dependencies []string,
) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "entry.go"), []byte("package feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "schema_version: 1\n" +
		"id: " + id + "\n" +
		"kind: " + kind + "\n" +
		"version: 0.1.0\n" +
		"entrypoint: ./entry.go\n" +
		"dependencies: [" + strings.Join(dependencies, ", ") + "]\n" +
		"migrations: []\n"
	if err := os.WriteFile(filepath.Join(directory, "feature.yaml"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func featureIDs(features []Feature) []string {
	ids := make([]string, 0, len(features))
	for _, feature := range features {
		ids = append(ids, feature.Manifest.ID)
	}
	return ids
}
