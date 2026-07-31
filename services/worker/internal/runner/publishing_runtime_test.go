package runner

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	videopublishing "github.com/apdsoftware/postqron/features/f08-publishing/providers/video"
	"github.com/apdsoftware/postqron/services/worker/internal/publishingruntime"
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
	t.Setenv("POSTQRON_F05_ENABLED", "true")
	t.Setenv("POSTQRON_F05_CIPHER_KEY_ID", "worker-test-key")
	t.Setenv(
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		base64.StdEncoding.EncodeToString(
			[]byte("0123456789abcdef0123456789abcdef"),
		),
	)
	t.Setenv("POSTQRON_F05_X_RESOURCE_SERVER", "https://api.x.com")
	worker, err := NewRuntime(
		nil, database, "postgres://worker:worker@127.0.0.1/postqron",
		"example.test", time.Second, time.Now, logger,
		publishingruntime.VideoAdapterDependencies{},
	)
	if err != nil {
		t.Fatalf("compose gated runner: %v", err)
	}
	t.Cleanup(worker.Close)
	if worker.publishing == nil || worker.closePublishing == nil {
		t.Fatal("real runner did not mount the gated F8 runtime")
	}
}

func TestRealPublishingBootstrapRegistersGatedVideoAdapters(t *testing.T) {
	previous := newPublishingRuntimeService
	t.Cleanup(func() { newPublishingRuntimeService = previous })

	var verified atomic.Bool
	newPublishingRuntimeService = func(
		_ context.Context,
		_ *sql.DB,
		_ string,
		_ func() time.Time,
		_ *socialconnections.AuthenticatedExecutor,
		dependencies ...publishingruntime.VideoAdapterDependencies,
	) (*publishingruntime.Service, error) {
		registry, err := publishingruntime.NewVideoAdapterRegistry(dependencies...)
		if err != nil {
			return nil, err
		}
		for _, provider := range []string{"tiktok", "youtube"} {
			if _, err := registry.ResolvePublisher(context.Background(), provider); err != nil {
				return nil, err
			}
		}
		verified.Store(true)
		return &publishingruntime.Service{}, nil
	}

	ready := publishingruntime.ProviderGate{
		Configured: true, ReviewApproved: true,
		AuditVerified: true, QuotaVerified: true,
	}
	_, err := configurePublishingRuntime(
		context.Background(),
		&sql.DB{},
		"postgres://bootstrap.invalid/postqron",
		time.Now,
		&socialconnections.AuthenticatedExecutor{},
		publishingruntime.VideoAdapterDependencies{
			Executor:                 &socialconnections.AuthenticatedExecutor{},
			Media:                    bootstrapMediaSource{},
			TikTokVerifiedPullPrefix: "https://media.example/tiktok/",
			F5TrailingSlashPaths:     true,
			TikTok:                   ready,
			YouTube:                  ready,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Load() {
		t.Fatal("real publishing bootstrap did not build the video registry")
	}
}

type recordingPublishingDispatcher struct {
	calls     atomic.Int64
	processed bool
	err       error
}

type bootstrapMediaSource struct{}

func (bootstrapMediaSource) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

var _ videopublishing.MediaSource = bootstrapMediaSource{}

func (dispatcher *recordingPublishingDispatcher) DispatchOne(
	context.Context,
) (bool, error) {
	dispatcher.calls.Add(1)
	return dispatcher.processed, dispatcher.err
}
