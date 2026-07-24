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

func writeFeature(t *testing.T, root, id string, dependencies []string) {
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
		"kind: api\n" +
		"version: 0.1.0\n" +
		"entrypoint: ./entry.go\n" +
		"dependencies: [" + strings.Join(dependencies, ", ") + "]\n" +
		"migrations: []\n"
	if err := os.WriteFile(filepath.Join(directory, "feature.yaml"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}
