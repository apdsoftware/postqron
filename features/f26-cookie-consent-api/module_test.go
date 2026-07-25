package cookieconsent

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestModuleExposesOnlyDeclaredHandlers(t *testing.T) {
	handler := &HTTPHandler{}
	module := &Module{handler: handler}
	for _, name := range []string{"CookiePreferences", "CookiePreferencesExport"} {
		value, found := module.Handler(name)
		if !found || value != handler {
			t.Fatalf("Handler(%q)=%v, %v", name, value, found)
		}
	}
	if value, found := module.Handler("Unknown"); found || value != nil {
		t.Fatalf("unknown handler=%v, %v", value, found)
	}
	if err := module.Start(context.Background()); err == nil {
		t.Fatal("unconfigured module should fail startup")
	}
}

func TestManifestDeclaresRuntimeRoutesAndMigration(t *testing.T) {
	source := readTestFile(t, "feature.yaml")
	for _, expected := range []string{
		"id: cookie-consent-api",
		"entrypoint: ./module.go",
		"path: /cookie-preferences",
		"handler: CookiePreferences",
		"handler: CookiePreferencesExport",
		"methods: [GET, PUT, DELETE]",
		"./migrations/000001_create_cookie_consent_ledger.sql",
	} {
		if !strings.Contains(source, expected) {
			t.Fatalf("feature.yaml is missing %q", expected)
		}
	}
}

func TestOpenAPIKeepsNecessaryServerControlled(t *testing.T) {
	source := readTestFile(t, "contracts/openapi.yaml")
	inputStart := strings.Index(source, "CookiePreferenceInput:")
	outputStart := strings.Index(source, "CookiePreferences:")
	if inputStart < 0 || outputStart <= inputStart {
		t.Fatal("cookie schemas are missing")
	}
	input := source[inputStart:outputStart]
	if strings.Contains(input, "necessary:") {
		t.Fatal("write contract must not accept the necessary category")
	}
	for _, expected := range []string{
		"additionalProperties: false",
		"preferences",
		"analytics",
		"marketing",
	} {
		if !strings.Contains(input, expected) {
			t.Fatalf("write contract missing %q", expected)
		}
	}
}

func readTestFile(t *testing.T, name string) string {
	t.Helper()
	value, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

var _ http.Handler = (*HTTPHandler)(nil)
