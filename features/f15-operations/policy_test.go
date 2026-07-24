package operations

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestOperationalPolicyMatchesAdoptedD05Targets(t *testing.T) {
	policy := DefaultOperationalPolicy()
	const day = 24 * time.Hour
	if policy.OperationalLogRetention != 30*day {
		t.Fatalf("log retention = %v, want 30 days", policy.OperationalLogRetention)
	}
	if policy.AuditRetentionMonths != 12 {
		t.Fatalf("audit retention = %d, want 12 months", policy.AuditRetentionMonths)
	}
	if policy.MetricRetentionMonths != 13 {
		t.Fatalf("metric retention = %d, want 13 months", policy.MetricRetentionMonths)
	}
	if policy.BackupRetention != 35*day {
		t.Fatalf("backup retention = %v, want 35 days", policy.BackupRetention)
	}
	if policy.DatabaseRPO != 15*time.Minute || policy.DatabaseRTO != 4*time.Hour {
		t.Fatalf("database RPO/RTO = %v/%v", policy.DatabaseRPO, policy.DatabaseRTO)
	}
	if policy.ObjectRPO != day || policy.ObjectRTO != 8*time.Hour {
		t.Fatalf("object RPO/RTO = %v/%v", policy.ObjectRPO, policy.ObjectRTO)
	}
	if policy.EndToEndRTO != 8*time.Hour {
		t.Fatalf("end-to-end RTO = %v, want 8 hours", policy.EndToEndRTO)
	}
}

func TestOperationalArtifactsContainMandatoryControls(t *testing.T) {
	cases := map[string][]string{
		"config/alerts.yaml": {
			"PostqronPublicationQueueDelayed",
			"PostqronPublicationFailures",
			"PostqronDatabaseBackupStale",
			"PostqronSensitiveAuditWriteFailed",
		},
		"config/backup-policy.yaml": {
			"rpo_minutes: 15",
			"rto_hours: 4",
			"retention_days: 35",
			"isolated_database_restore: monthly",
		},
		"migrations/000001_create_sensitive_audit_events.sql": {
			"ENABLE ALWAYS TRIGGER",
			"BEFORE UPDATE OR DELETE",
			"purge_expired_sensitive_audit_events",
			"INTERVAL '12 months'",
			"source_ip_hash",
		},
	}
	for path, required := range cases {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, value := range required {
			if !strings.Contains(string(source), value) {
				t.Errorf("%s does not contain %q", path, value)
			}
		}
	}
}
