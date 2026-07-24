package operations

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestMetricsExposeBoundedOperationalMeasurements(t *testing.T) {
	metrics := &Metrics{}
	now := time.Unix(1_750_000_000, 0).UTC()
	metrics.SetQueue(12, 45*time.Second)
	metrics.RecordPublicationFailure()
	metrics.RecordRateLimitRejection()
	metrics.RecordAuditWriteFailure()
	metrics.SetReady(true)
	metrics.RecordSuccessfulBackup(now)
	metrics.RecordSuccessfulRestoreTest(now.Add(-time.Hour))

	var output bytes.Buffer
	if err := metrics.WritePrometheus(&output); err != nil {
		t.Fatalf("WritePrometheus() error = %v", err)
	}
	got := output.String()
	for _, wanted := range []string{
		"postqron_queue_depth 12",
		"postqron_oldest_queued_job_age_seconds 45",
		"postqron_publication_failures_total 1",
		"postqron_rate_limit_rejections_total 1",
		"postqron_audit_write_failures_total 1",
		"postqron_readiness 1",
		"postqron_last_successful_backup_timestamp_seconds 1750000000",
	} {
		if !strings.Contains(got, wanted) {
			t.Fatalf("metrics missing %q:\n%s", wanted, got)
		}
	}
	if strings.Contains(got, "{") {
		t.Fatalf("metrics unexpectedly contain labels: %s", got)
	}
}

func TestMetricsClampNegativeQueueValues(t *testing.T) {
	metrics := &Metrics{}
	metrics.SetQueue(-1, -time.Second)

	snapshot := metrics.Snapshot()
	if snapshot.QueueDepth != 0 || snapshot.OldestQueuedJobAge != 0 {
		t.Fatalf("snapshot = %#v, want non-negative queue values", snapshot)
	}
}
