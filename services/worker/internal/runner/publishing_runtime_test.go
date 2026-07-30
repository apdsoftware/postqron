package runner

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
)

type runnerF5Authorizer struct{}

func (runnerF5Authorizer) Authorize(
	context.Context, string, string, socialconnections.Permission,
) error {
	return nil
}

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

func TestRealRunnerCompositionAcceptsValidGatesOnlyWithF5Executor(
	t *testing.T,
) {
	t.Chdir("../../../..")
	t.Setenv("POSTQRON_ENV", "test")
	for _, key := range []string{
		"POSTQRON_F08_X_ENABLED",
		"POSTQRON_F08_X_REVIEW_APPROVED",
		"POSTQRON_F08_X_RUNTIME_AUDIT_VERIFIED",
		"POSTQRON_F08_X_QUOTA_CONFIGURED",
	} {
		t.Setenv(key, "true")
	}
	mediaRoot := t.TempDir()
	t.Setenv("POSTQRON_F08_MEDIA_ROOT", mediaRoot)
	database, _ := openRunnerTestDatabase(t)
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if _, err := NewRuntimeWithExecutor(
		nil, database, "postgres://worker:worker@127.0.0.1/postqron",
		"example.test", time.Second, time.Now, logger, nil,
	); err == nil {
		t.Fatal("valid gates without F5 executor must fail closed")
	}
	repository, err := socialconnections.NewPostgresRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	quota, err := socialconnections.NewPostgresChannelQuota(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	f5Service, err := socialconnections.NewService(socialconnections.Config{
		Repository: repository, Authorizer: runnerF5Authorizer{}, Quota: quota,
		Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := socialconnections.NewAuthenticatedExecutor(
		socialconnections.AuthenticatedExecutorConfig{Service: f5Service},
	)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := NewRuntimeWithExecutor(
		nil, database, "postgres://worker:worker@127.0.0.1/postqron",
		"example.test", time.Second, time.Now, logger,
		executor,
	)
	if err != nil {
		t.Fatalf("compose gated runner: %v", err)
	}
	t.Cleanup(worker.Close)
	if worker.publishing == nil || worker.closePublishing == nil {
		t.Fatal("real runner did not mount the gated F8 runtime")
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
