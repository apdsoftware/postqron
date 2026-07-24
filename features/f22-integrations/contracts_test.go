package integrations

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestOwnedContractsCoverAcceptanceControls(t *testing.T) {
	openAPIBytes, err := os.ReadFile("contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	openAPI := string(openAPIBytes)
	for _, required := range []string{
		"openapi: 3.1.0",
		"/workspaces/{workspaceId}/posts:",
		"x-postqron-required-scopes:",
		"- posts:read",
		"- posts:write",
		"Idempotency-Key",
		"next_cursor",
		"maximum: 100",
		"\"429\":",
		"Postqron-Signature",
		"Postqron-Event-Version",
		"const: \"2026-07-01\"",
	} {
		if !strings.Contains(openAPI, required) {
			t.Errorf("OpenAPI contract is missing %q", required)
		}
	}
	for _, forbiddenProperty := range []string{
		"\n        access_token:",
		"\n        refresh_token:",
		"\n        provider_token:",
	} {
		if strings.Contains(openAPI, forbiddenProperty) {
			t.Errorf("OpenAPI exposes forbidden property %q", forbiddenProperty)
		}
	}

	schemaBytes, err := os.ReadFile("contracts/webhook-event.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		t.Fatalf("webhook schema is invalid JSON: %v", err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("unexpected webhook JSON Schema version: %v", schema["$schema"])
	}
}

func TestMigrationStoresOnlyCredentialDigestsAndEncryptedSigningSecrets(t *testing.T) {
	migrationBytes, err := os.ReadFile("migrations/000001_create_integrations.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration := string(migrationBytes)
	for _, required := range []string{
		"token_digest bytea NOT NULL UNIQUE",
		"octet_length(token_digest) = 32",
		"signing_secret_ciphertext bytea NOT NULL",
		"signing_secret_key_id text NOT NULL",
		"CREATE TABLE integrations.idempotency_responses",
		"CREATE TABLE integrations.webhook_dead_letters",
		"REVOKE ALL ON SCHEMA integrations FROM PUBLIC",
	} {
		if !strings.Contains(migration, required) {
			t.Errorf("migration is missing %q", required)
		}
	}
	for _, forbiddenColumn := range []string{
		"access_token text",
		"refresh_token text",
		"provider_token text",
		"signing_secret text",
	} {
		if strings.Contains(migration, forbiddenColumn) {
			t.Errorf("migration persists forbidden plaintext column %q", forbiddenColumn)
		}
	}
}

func TestFeatureManifestDeclaresDiscoveryDependencies(t *testing.T) {
	manifestBytes, err := os.ReadFile("feature.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(manifestBytes)
	for _, required := range []string{
		"id: integrations",
		"entrypoint: ./integrations.go",
		"  - auth",
		"  - workspaces",
		"  - operations",
		"  - ./migrations/000001_create_integrations.sql",
	} {
		if !strings.Contains(manifest, required) {
			t.Errorf("feature manifest is missing %q", required)
		}
	}
}
