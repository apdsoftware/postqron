package scheduling

import (
	"os"
	"strings"
	"testing"
)

func TestBrowserContractsExcludeInternalCommandAndAccountFields(t *testing.T) {
	for _, path := range []string{
		"contracts/scheduling.openapi.yaml",
		"client/contracts.ts",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"active_command_id",
			"invalidation_key",
			"created_by",
		} {
			if strings.Contains(string(contents), forbidden) {
				t.Fatalf("%s exposes internal field %q", path, forbidden)
			}
		}
	}
}

func TestOpenAPIRequiresIdempotencyForCreateAndDuplicate(t *testing.T) {
	contents, err := os.ReadFile("contracts/scheduling.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(contents)
	if strings.Count(contract, "$ref: '#/components/parameters/IdempotencyKey'") != 2 {
		t.Fatal("schedule and duplicate must both require Idempotency-Key")
	}
	for _, required := range []string{
		"idempotency_payload_mismatch",
		"idempotency_in_progress",
		"Idempotency-Replayed",
	} {
		if !strings.Contains(contract, required) {
			t.Fatalf("OpenAPI is missing %q semantics", required)
		}
	}
}
