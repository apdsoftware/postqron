package operations

import (
	"context"
	"errors"
	"testing"
	"time"
)

type auditSinkStub struct {
	appended []AuditEvent
	err      error
}

func (sink *auditSinkStub) Append(_ context.Context, event AuditEvent) error {
	if sink.err != nil {
		return sink.err
	}
	sink.appended = append(sink.appended, event)
	return nil
}

func validAuditEvent() AuditEvent {
	return AuditEvent{
		ID:            "evt_01HXYZ",
		OccurredAt:    time.Unix(1_750_000_000, 0).UTC(),
		ActorType:     "user",
		ActorID:       "usr_01HXYZ",
		WorkspaceID:   "wrk_01HXYZ",
		Action:        AuditDeletionRequested,
		TargetType:    "workspace",
		TargetID:      "wrk_01HXYZ",
		Outcome:       "succeeded",
		CorrelationID: "req_01HXYZ",
		SourceIPHash:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func TestAuditRecorderPersistsAllowlistedMinimalEvent(t *testing.T) {
	sink := &auditSinkStub{}
	recorder, err := NewAuditRecorder(sink, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := recorder.Record(context.Background(), validAuditEvent()); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if len(sink.appended) != 1 {
		t.Fatalf("appended = %d, want 1", len(sink.appended))
	}
}

func TestAuditRecorderFailsClosedAndIncrementsMetric(t *testing.T) {
	metrics := &Metrics{}
	sink := &auditSinkStub{err: errors.New("database unavailable")}
	recorder, err := NewAuditRecorder(sink, metrics)
	if err != nil {
		t.Fatal(err)
	}

	err = recorder.Record(context.Background(), validAuditEvent())
	if err == nil {
		t.Fatal("Record() succeeded with unavailable audit sink")
	}
	if metrics.Snapshot().AuditWriteFailures != 1 {
		t.Fatalf("audit failures = %d, want 1", metrics.Snapshot().AuditWriteFailures)
	}
}

func TestAuditEventRejectsArbitraryOrPersonalFields(t *testing.T) {
	event := validAuditEvent()
	event.Action = AuditAction("custom.payload_copied")
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() accepted an arbitrary action")
	}

	event = validAuditEvent()
	event.ActorID = "person@example.test"
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() accepted an email as actor ID")
	}

	event = validAuditEvent()
	event.SourceIPHash = "192.0.2.1"
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() accepted a raw IP address")
	}

	event = validAuditEvent()
	event.SourceIPHash = "sha256:zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"
	if err := event.Validate(); err == nil {
		t.Fatal("Validate() accepted a non-hex source IP digest")
	}
}
