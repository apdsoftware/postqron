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
