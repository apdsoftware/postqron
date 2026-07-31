package runner

import (
	"bytes"
	"context"
	"database/sql"
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

func TestRealPublishingBootstrapRegistersGatedVideoAdapters(t *testing.T) {
	previous := newPublishingRuntimeService
	t.Cleanup(func() { newPublishingRuntimeService = previous })

	var verified atomic.Bool
	newPublishingRuntimeService = func(
		_ context.Context,
		_ *sql.DB,
		_ string,
		_ func() time.Time,
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
