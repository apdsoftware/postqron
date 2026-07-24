package operations

import (
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

const prometheusContentType = "text/plain; version=0.0.4; charset=utf-8"

// Metrics deliberately exposes a bounded, label-free set to prevent tenant or
// user identifiers from leaking into monitoring and to avoid high cardinality.
type Metrics struct {
	queueDepth            atomic.Int64
	oldestQueuedJobMillis atomic.Int64
	publicationFailures   atomic.Uint64
	rateLimitRejections   atomic.Uint64
	auditWriteFailures    atomic.Uint64
	readiness             atomic.Int64
	lastBackupUnix        atomic.Int64
	lastRestoreTestUnix   atomic.Int64
}

type MetricsSnapshot struct {
	QueueDepth            int64
	OldestQueuedJobAge    time.Duration
	PublicationFailures   uint64
	RateLimitRejections   uint64
	AuditWriteFailures    uint64
	Ready                 bool
	LastSuccessfulBackup  time.Time
	LastSuccessfulRestore time.Time
}

func (metrics *Metrics) SetQueue(depth int64, oldestAge time.Duration) {
	if depth < 0 {
		depth = 0
	}
	if oldestAge < 0 {
		oldestAge = 0
	}
	metrics.queueDepth.Store(depth)
	metrics.oldestQueuedJobMillis.Store(oldestAge.Milliseconds())
}

func (metrics *Metrics) RecordPublicationFailure() {
	metrics.publicationFailures.Add(1)
}

func (metrics *Metrics) RecordRateLimitRejection() {
	metrics.rateLimitRejections.Add(1)
}

func (metrics *Metrics) RecordAuditWriteFailure() {
	metrics.auditWriteFailures.Add(1)
}

func (metrics *Metrics) SetReady(ready bool) {
	if ready {
		metrics.readiness.Store(1)
		return
	}
	metrics.readiness.Store(0)
}

func (metrics *Metrics) RecordSuccessfulBackup(at time.Time) {
	if at.IsZero() {
		metrics.lastBackupUnix.Store(0)
		return
	}
	metrics.lastBackupUnix.Store(at.UTC().Unix())
}

func (metrics *Metrics) RecordSuccessfulRestoreTest(at time.Time) {
	if at.IsZero() {
		metrics.lastRestoreTestUnix.Store(0)
		return
	}
	metrics.lastRestoreTestUnix.Store(at.UTC().Unix())
}

func (metrics *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		QueueDepth:            metrics.queueDepth.Load(),
		OldestQueuedJobAge:    time.Duration(metrics.oldestQueuedJobMillis.Load()) * time.Millisecond,
		PublicationFailures:   metrics.publicationFailures.Load(),
		RateLimitRejections:   metrics.rateLimitRejections.Load(),
		AuditWriteFailures:    metrics.auditWriteFailures.Load(),
		Ready:                 metrics.readiness.Load() == 1,
		LastSuccessfulBackup:  unixTime(metrics.lastBackupUnix.Load()),
		LastSuccessfulRestore: unixTime(metrics.lastRestoreTestUnix.Load()),
	}
}

func unixTime(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func (metrics *Metrics) ServeHTTP(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", prometheusContentType)
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_ = metrics.WritePrometheus(writer)
}

func (metrics *Metrics) WritePrometheus(writer io.Writer) error {
	snapshot := metrics.Snapshot()
	ready := 0
	if snapshot.Ready {
		ready = 1
	}

	values := []struct {
		name        string
		kind        string
		help        string
		measurement any
	}{
		{"postqron_queue_depth", "gauge", "Number of publication jobs waiting in the durable queue.", snapshot.QueueDepth},
		{"postqron_oldest_queued_job_age_seconds", "gauge", "Age in seconds of the oldest waiting publication job.", snapshot.OldestQueuedJobAge.Seconds()},
		{"postqron_publication_failures_total", "counter", "Total terminal publication failures.", snapshot.PublicationFailures},
		{"postqron_rate_limit_rejections_total", "counter", "Total requests rejected by application rate limits.", snapshot.RateLimitRejections},
		{"postqron_audit_write_failures_total", "counter", "Total sensitive audit events that could not be persisted.", snapshot.AuditWriteFailures},
		{"postqron_readiness", "gauge", "Whether required service dependencies are ready.", ready},
		{"postqron_last_successful_backup_timestamp_seconds", "gauge", "Unix timestamp of the last verified database backup.", prometheusTimestamp(snapshot.LastSuccessfulBackup)},
		{"postqron_last_successful_restore_test_timestamp_seconds", "gauge", "Unix timestamp of the last successful restore test.", prometheusTimestamp(snapshot.LastSuccessfulRestore)},
	}
	for _, value := range values {
		if _, err := fmt.Fprintf(
			writer,
			"# HELP %s %s\n# TYPE %s %s\n%s %v\n",
			value.name,
			value.help,
			value.name,
			value.kind,
			value.name,
			value.measurement,
		); err != nil {
			return err
		}
	}
	return nil
}

func prometheusTimestamp(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}
