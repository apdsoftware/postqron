package operations

import (
	"slices"
	"testing"
	"time"
)

func TestEvaluateAlertsCoversQueueFailuresBackupReadinessAndAudit(t *testing.T) {
	now := time.Unix(1_750_000_000, 0).UTC()
	alerts := EvaluateAlerts(
		MetricsSnapshot{
			QueueDepth:            101,
			OldestQueuedJobAge:    61 * time.Second,
			PublicationFailures:   1,
			AuditWriteFailures:    1,
			Ready:                 false,
			LastSuccessfulBackup:  now.Add(-25 * time.Hour),
			LastSuccessfulRestore: now.Add(-32 * 24 * time.Hour),
		},
		DefaultAlertThresholds(),
		now,
	)
	codes := make([]string, 0, len(alerts))
	for _, alert := range alerts {
		codes = append(codes, alert.Code)
		if alert.Runbook == "" {
			t.Fatalf("alert %q has no runbook", alert.Code)
		}
	}
	for _, wanted := range []string{
		"publication_queue_delayed",
		"publication_failure_detected",
		"sensitive_audit_write_failed",
		"service_not_ready",
		"database_backup_stale",
		"restore_test_overdue",
	} {
		if !slices.Contains(codes, wanted) {
			t.Fatalf("alerts = %v, missing %q", codes, wanted)
		}
	}
}

func TestEvaluateAlertsAcceptsHealthySnapshot(t *testing.T) {
	now := time.Unix(1_750_000_000, 0).UTC()
	alerts := EvaluateAlerts(
		MetricsSnapshot{
			Ready:                 true,
			LastSuccessfulBackup:  now.Add(-time.Hour),
			LastSuccessfulRestore: now.Add(-24 * time.Hour),
		},
		DefaultAlertThresholds(),
		now,
	)
	if len(alerts) != 0 {
		t.Fatalf("alerts = %#v, want none", alerts)
	}
}
