package runner

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTickDispatchesF8Runtime(t *testing.T) {
	var output bytes.Buffer
	dispatcher := &recordingPublishingDispatcher{processed: true}
	worker := &Runner{
		interval:   time.Second,
		clock:      time.Now,
		logger:     slog.New(slog.NewJSONHandler(&output, nil)),
		publishing: dispatcher,
	}
	worker.Tick(context.Background())
	if dispatcher.calls.Load() != 1 {
		t.Fatalf("F8 dispatch calls=%d", dispatcher.calls.Load())
	}
	if !strings.Contains(output.String(), "worker F8 publishing dispatch processed") {
		t.Fatalf("worker log=%q", output.String())
	}
}

func TestTickLogsF8FailureWithoutDisablingOtherFeatureDiscovery(t *testing.T) {
	var output bytes.Buffer
	dispatcher := &recordingPublishingDispatcher{err: context.DeadlineExceeded}
	worker := New(nil, time.Second, slog.New(slog.NewJSONHandler(&output, nil)))
	worker.publishing = dispatcher
	worker.Tick(context.Background())
	if dispatcher.calls.Load() != 1 {
		t.Fatalf("F8 dispatch calls=%d", dispatcher.calls.Load())
	}
	if !strings.Contains(output.String(), "worker F8 publishing dispatch failed") {
		t.Fatalf("worker log=%q", output.String())
	}
}

type recordingPublishingDispatcher struct {
	calls     atomic.Int64
	processed bool
	err       error
}

func (dispatcher *recordingPublishingDispatcher) DispatchOne(
	context.Context,
) (bool, error) {
	dispatcher.calls.Add(1)
	return dispatcher.processed, dispatcher.err
}
