package integrations

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// WebhookMetrics uses a fixed label-free series set. Workspace, subscription,
// delivery, endpoint, and event payload values are deliberately excluded.
type WebhookMetrics struct {
	deadLettered   atomic.Uint64
	delivered      atomic.Uint64
	deliveryMillis atomic.Uint64
	enqueued       atomic.Uint64
	retried        atomic.Uint64
}

type WebhookMetricsSnapshot struct {
	DeadLettered         uint64
	Delivered            uint64
	LastDeliveryDuration time.Duration
	Enqueued             uint64
	Retried              uint64
}

func (metrics *WebhookMetrics) RecordEnqueued(count int) {
	if count > 0 {
		metrics.enqueued.Add(uint64(count))
	}
}

func (metrics *WebhookMetrics) RecordDelivered(duration time.Duration) {
	if duration < 0 {
		duration = 0
	}
	metrics.delivered.Add(1)
	metrics.deliveryMillis.Store(uint64(duration.Milliseconds()))
}

func (metrics *WebhookMetrics) RecordRetried() {
	metrics.retried.Add(1)
}

func (metrics *WebhookMetrics) RecordDeadLetter() {
	metrics.deadLettered.Add(1)
}

func (metrics *WebhookMetrics) Snapshot() WebhookMetricsSnapshot {
	return WebhookMetricsSnapshot{
		DeadLettered:         metrics.deadLettered.Load(),
		Delivered:            metrics.delivered.Load(),
		LastDeliveryDuration: time.Duration(metrics.deliveryMillis.Load()) * time.Millisecond,
		Enqueued:             metrics.enqueued.Load(),
		Retried:              metrics.retried.Load(),
	}
}

func (metrics *WebhookMetrics) WritePrometheus(writer io.Writer) error {
	snapshot := metrics.Snapshot()
	values := []struct {
		name  string
		help  string
		kind  string
		value any
	}{
		{"postqron_webhook_enqueued_total", "Webhook deliveries durably enqueued.", "counter", snapshot.Enqueued},
		{"postqron_webhook_delivered_total", "Webhook deliveries acknowledged with a 2xx response.", "counter", snapshot.Delivered},
		{"postqron_webhook_retried_total", "Webhook deliveries scheduled for another attempt.", "counter", snapshot.Retried},
		{"postqron_webhook_dead_lettered_total", "Webhook deliveries moved to the dead letter queue.", "counter", snapshot.DeadLettered},
		{"postqron_webhook_last_delivery_duration_seconds", "Duration of the last successful webhook delivery.", "gauge", snapshot.LastDeliveryDuration.Seconds()},
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
			value.value,
		); err != nil {
			return err
		}
	}
	return nil
}
