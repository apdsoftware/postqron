package prelaunch

import (
	"os"
	"strings"
	"testing"
)

func TestMigrationEnforcesConsentDeduplicationAndTransactionalSeparation(
	t *testing.T,
) {
	source, err := os.ReadFile("migrations/000001_create_prelaunch_access.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(source)
	for _, required := range []string{
		"email_hash CHAR(64) NOT NULL UNIQUE",
		"marketing_consent = FALSE",
		"access_consent')::BOOLEAN = TRUE",
		"f14.prelaunch_access.v1",
		"channel = 'transactional'",
		"NOT (command ? 'marketing_consent')",
		"PRIMARY KEY (key_hash, window_started_at)",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("migration is missing %q", required)
		}
	}
}
