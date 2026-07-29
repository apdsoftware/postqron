package main

import (
	"path/filepath"
	"testing"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

func TestRuntimeDiscoveryIncludesServicesSideAdapters(t *testing.T) {
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
	seen := map[string]bool{}
	for _, feature := range features {
		seen[feature.Manifest.ID] = true
	}
	for _, featureID := range []string{
		"auth",
		"workspaces",
		"f10-entitlements",
		"account-privacy-runtime",
		"app-shell",
	} {
		if !seen[featureID] {
			t.Fatalf("feature %q was not discovered in API runtime", featureID)
		}
	}
}
