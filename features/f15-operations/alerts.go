package operations

import "time"

type AlertSeverity string

const (
	SeverityWarning  AlertSeverity = "warning"
	SeverityCritical AlertSeverity = "critical"
)

type Alert struct {
	Code     string
	Severity AlertSeverity
	Runbook  string
}

type AlertThresholds struct {
	MaxQueueDepth         int64
	MaxOldestQueuedJobAge time.Duration
	MaxBackupAge          time.Duration
	MaxRestoreTestAge     time.Duration
}

func DefaultAlertThresholds() AlertThresholds {
	policy := DefaultOperationalPolicy()
	return AlertThresholds{
		MaxQueueDepth:         100,
		MaxOldestQueuedJobAge: 60 * time.Second,
		MaxBackupAge:          policy.BackupMaxAge,
		MaxRestoreTestAge:     policy.RestoreTestMaxAge,
	}
}

// EvaluateAlerts returns stable machine-readable alert codes. It never embeds
// tenant, user, post, or provider payload data in an alert.
func EvaluateAlerts(snapshot MetricsSnapshot, thresholds AlertThresholds, now time.Time) []Alert {
	alerts := make([]Alert, 0, 6)
	add := func(condition bool, code string, severity AlertSeverity, runbook string) {
		if condition {
			alerts = append(alerts, Alert{Code: code, Severity: severity, Runbook: runbook})
		}
	}

	add(
		snapshot.QueueDepth > thresholds.MaxQueueDepth ||
			snapshot.OldestQueuedJobAge > thresholds.MaxOldestQueuedJobAge,
		"publication_queue_delayed",
		SeverityCritical,
		"runbooks/incident-response.md#publication-queue",
	)
	add(
		snapshot.PublicationFailures > 0,
		"publication_failure_detected",
		SeverityWarning,
		"runbooks/incident-response.md#publication-failures",
	)
	add(
		snapshot.AuditWriteFailures > 0,
		"sensitive_audit_write_failed",
		SeverityCritical,
		"runbooks/incident-response.md#audit-write-failures",
	)
	add(
		!snapshot.Ready,
		"service_not_ready",
		SeverityCritical,
		"runbooks/incident-response.md#readiness",
	)
	add(
		snapshot.LastSuccessfulBackup.IsZero() ||
			now.Sub(snapshot.LastSuccessfulBackup) > thresholds.MaxBackupAge,
		"database_backup_stale",
		SeverityCritical,
		"runbooks/backup-restore.md#backup-failure",
	)
	add(
		snapshot.LastSuccessfulRestore.IsZero() ||
			now.Sub(snapshot.LastSuccessfulRestore) > thresholds.MaxRestoreTestAge,
		"restore_test_overdue",
		SeverityWarning,
		"runbooks/backup-restore.md#restore-drills",
	)
	return alerts
}
