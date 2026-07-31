package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

func TestYouTubeMultipartCheckpointMachine(t *testing.T) {
	videoBytes := []byte("offline-video-fixture")
	digest := sha256.Sum256(videoBytes)
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		jsonResponse(`{
			"items":[{"id":"channel-1","status":{"privacyStatus":"public"}}]
		}`),
		jsonResponse(`{"id":"video-1"}`),
		jsonResponse(`{
			"items":[{
				"id":"video-1",
				"processingDetails":{"processingStatus":"processing"}
			}]
		}`),
		jsonResponse(`{
			"items":[{
				"id":"video-1",
				"processingDetails":{"processingStatus":"succeeded"}
			}]
		}`),
	}}
	source := &fixtureMediaSource{data: videoBytes}
	adapter := newYouTubeForTest(executor, source)
	request := youtubeRequest(t, videoBytes, digest)

	for step := 0; step < 3; step++ {
		result, err := adapter.Publish(context.Background(), request)
		if err != nil || result.Complete || len(result.Checkpoint) == 0 {
			t.Fatalf("step %d result=%#v error=%v", step, result, err)
		}
		request.Checkpoint = result.Checkpoint
	}
	final, err := adapter.Publish(context.Background(), request)
	if err != nil || !final.Complete || final.RemoteID != "video-1" ||
		final.Permalink != "https://www.youtube.com/shorts/video-1" {
		t.Fatalf("final result=%#v error=%v", final, err)
	}

	calls := executor.Calls()
	if len(calls) != 4 {
		t.Fatalf("authenticated calls=%d", len(calls))
	}
	for index, call := range calls {
		if call.ExpectedProvider != socialconnections.ProviderYouTube {
			t.Fatalf("call %d ExpectedProvider=%q", index, call.ExpectedProvider)
		}
		if call.Header.Get("Authorization") != "" {
			t.Fatalf("call %d leaked authorization", index)
		}
	}
	if calls[1].Method != http.MethodPost ||
		calls[1].Path != youTubeUploadPath ||
		calls[1].Media == nil ||
		!strings.HasPrefix(
			calls[1].Header.Get("Content-Type"),
			"multipart/related; boundary=",
		) {
		t.Fatalf("multipart upload=%#v", calls[1])
	}
	if source.opens != 1 {
		t.Fatalf("media source opens=%d", source.opens)
	}
}

func TestYouTubeCrashDuringUploadFailsClosedWithoutDuplicate(t *testing.T) {
	videoBytes := []byte("crash-safe-video")
	digest := sha256.Sum256(videoBytes)
	executor := &fixtureExecutor{}
	adapter := newYouTubeForTest(executor, &fixtureMediaSource{data: videoBytes})
	request := youtubeRequest(t, videoBytes, digest)
	request.Checkpoint = mustJSON(t, youTubeCheckpoint{
		Step: "creator_ready", ChannelID: "channel-1",
	})
	result, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		WorkspaceID: request.WorkspaceID, ConnectionID: request.ConnectionID,
		Payload: request.Payload, Checkpoint: request.Checkpoint,
	})
	if err != nil || result.State != publishing.ReconciliationUnknown {
		t.Fatalf("reconcile=%#v error=%v", result, err)
	}
	if len(executor.Calls()) != 0 {
		t.Fatal("ambiguous upload was repeated")
	}
}

func TestYouTubeReconcilesProcessingVideo(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		jsonResponse(`{
			"items":[{
				"id":"video-1",
				"processingDetails":{"processingStatus":"succeeded"}
			}]
		}`),
	}}
	adapter := newYouTubeForTest(executor, &fixtureMediaSource{})
	madeForKids := false
	synthetic := false
	result, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		WorkspaceID: "workspace-1", ConnectionID: "connection-1",
		Payload: mustJSON(t, youTubePayload{
			Video: media{
				StorageKey: "video", ContentType: "video/mp4", SizeBytes: 1,
				SHA256: strings.Repeat("a", 64), Width: 1080, Height: 1920,
				DurationSeconds: 1,
			},
			Metadata: youTubeMetadata{
				ChannelID: "channel-1", Title: "Short", CategoryID: "22",
				PrivacyStatus: "private", MadeForKids: &madeForKids,
				ContainsSyntheticMedia: &synthetic,
			},
		}),
		Checkpoint: mustJSON(t, youTubeCheckpoint{
			Step: "processing", ChannelID: "channel-1", VideoID: "video-1",
		}),
	})
	if err != nil || result.State != publishing.ReconciliationFound ||
		result.RemoteID != "video-1" {
		t.Fatalf("reconcile=%#v error=%v", result, err)
	}
}

type fixtureMediaSource struct {
	data   []byte
	err    error
	reader io.ReadCloser
	opens  int
}

func (source *fixtureMediaSource) Open(
	_ context.Context,
	_ string,
) (io.ReadCloser, error) {
	source.opens++
	if source.err != nil {
		return nil, source.err
	}
	if source.reader != nil {
		return source.reader, nil
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), source.data...))), nil
}

func youtubeRequest(
	t *testing.T,
	videoBytes []byte,
	digest [sha256.Size]byte,
) publishing.PublishRequest {
	t.Helper()
	madeForKids := false
	synthetic := false
	return publishing.PublishRequest{
		WorkspaceID: "workspace-1", ConnectionID: "connection-1",
		Payload: mustJSON(t, youTubePayload{
			Video: media{
				StorageKey: "videos/short.mp4", ContentType: "video/mp4",
				SizeBytes: int64(len(videoBytes)),
				SHA256:    hex.EncodeToString(digest[:]),
				Width:     1080, Height: 1920, DurationSeconds: 42,
			},
			Metadata: youTubeMetadata{
				ChannelID: "channel-1", Title: "Offline short",
				Description: "Fixture", CategoryID: "22",
				PrivacyStatus: "private", MadeForKids: &madeForKids,
				ContainsSyntheticMedia: &synthetic,
			},
		}),
	}
}

func TestVideoAdaptersExposeSafeCapabilities(t *testing.T) {
	for name, test := range map[string]struct {
		adapter        publishing.Publisher
		reconciliation bool
	}{
		"tiktok": {
			adapter: newTikTokForTest(&fixtureExecutor{}), reconciliation: true,
		},
		"youtube": {
			adapter:        newYouTubeForTest(&fixtureExecutor{}, &fixtureMediaSource{}),
			reconciliation: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := test.adapter.Capabilities()
			if got.Version != capabilityVersion ||
				got.Mode != publishing.PublishingModeAuto ||
				got.NativeIdempotency ||
				got.Reconciliation != test.reconciliation ||
				!got.AmbiguousFailClosed ||
				!got.MultiStep || !got.RemotePermalink {
				t.Fatalf("capabilities=%#v", got)
			}
		})
	}
}

func TestYouTubeSuccessfulUploadWithoutVideoIDIsAmbiguous(t *testing.T) {
	videoBytes := []byte("ambiguous-video")
	digest := sha256.Sum256(videoBytes)
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		jsonResponse(`{}`),
	}}
	adapter := newYouTubeForTest(executor, &fixtureMediaSource{data: videoBytes})
	request := youtubeRequest(t, videoBytes, digest)
	request.Checkpoint = mustJSON(t, youTubeCheckpoint{
		Step: "creator_ready", ChannelID: "channel-1",
	})

	_, err := adapter.Publish(context.Background(), request)
	var failure *publishing.ProviderError
	if !errors.As(err, &failure) || !failure.Ambiguous || !failure.Retryable {
		t.Fatalf("publish error=%#v", err)
	}
	reconciled, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		Payload: request.Payload, Checkpoint: request.Checkpoint,
	})
	if err != nil || reconciled.State != publishing.ReconciliationUnknown {
		t.Fatalf("reconcile=%#v error=%v", reconciled, err)
	}
	if calls := executor.Calls(); len(calls) != 1 {
		t.Fatalf("upload calls=%d, want exactly one", len(calls))
	}
}

func TestYouTubeShortsRejectsLandscapeAndMissingDimensions(t *testing.T) {
	videoBytes := []byte("dimension-video")
	digest := sha256.Sum256(videoBytes)
	for _, dimensions := range [][2]int{{1920, 1080}, {0, 1920}, {1080, 0}} {
		t.Run(
			fmt.Sprintf("%dx%d", dimensions[0], dimensions[1]),
			func(t *testing.T) {
				request := youtubeRequest(t, videoBytes, digest)
				var payload youTubePayload
				if err := json.Unmarshal(request.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				payload.Video.Width = dimensions[0]
				payload.Video.Height = dimensions[1]
				request.Payload = mustJSON(t, payload)
				adapter := newYouTubeForTest(
					&fixtureExecutor{}, &fixtureMediaSource{data: videoBytes},
				)
				if _, err := adapter.Publish(context.Background(), request); err == nil {
					t.Fatal("invalid Shorts dimensions were accepted")
				}
			},
		)
	}
}

func TestYouTubeShortsAcceptsSquareSnapshot(t *testing.T) {
	videoBytes := []byte("square-video")
	digest := sha256.Sum256(videoBytes)
	request := youtubeRequest(t, videoBytes, digest)
	var payload youTubePayload
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload.Video.Width = 1080
	payload.Video.Height = 1080
	request.Payload = mustJSON(t, payload)
	adapter := newYouTubeForTest(
		&fixtureExecutor{}, &fixtureMediaSource{data: videoBytes},
	)
	if _, _, err := adapter.input(request); err != nil {
		t.Fatalf("square Shorts snapshot rejected: %v", err)
	}
}

func TestYouTubeTemporaryMediaSourceFailureRemainsRetryable(t *testing.T) {
	videoBytes := []byte("retryable-video")
	digest := sha256.Sum256(videoBytes)
	adapter := newYouTubeForTest(
		&fixtureExecutor{},
		&fixtureMediaSource{err: context.DeadlineExceeded},
	)
	request := youtubeRequest(t, videoBytes, digest)
	request.Checkpoint = mustJSON(t, youTubeCheckpoint{
		Step: "creator_ready", ChannelID: "channel-1",
	})

	_, err := adapter.Publish(context.Background(), request)
	var failure *publishing.ProviderError
	if !errors.As(err, &failure) || !failure.Retryable || failure.Ambiguous {
		t.Fatalf("media source error=%#v", err)
	}
}

func TestYouTubeTemporaryMediaReadFailureRemainsRetryable(t *testing.T) {
	videoBytes := []byte("retryable-read-video")
	digest := sha256.Sum256(videoBytes)
	adapter := newYouTubeForTest(
		&fixtureExecutor{},
		&fixtureMediaSource{reader: &failingMediaReader{
			data: videoBytes[:4], err: context.DeadlineExceeded,
		}},
	)
	request := youtubeRequest(t, videoBytes, digest)
	request.Checkpoint = mustJSON(t, youTubeCheckpoint{
		Step: "creator_ready", ChannelID: "channel-1",
	})

	_, err := adapter.Publish(context.Background(), request)
	var failure *publishing.ProviderError
	if !errors.As(err, &failure) || !failure.Retryable || failure.Ambiguous {
		t.Fatalf("media read error=%#v", err)
	}
}

type failingMediaReader struct {
	data []byte
	err  error
	read bool
}

func (reader *failingMediaReader) Read(destination []byte) (int, error) {
	if reader.read {
		return 0, reader.err
	}
	reader.read = true
	return copy(destination, reader.data), nil
}

func (*failingMediaReader) Close() error { return nil }

var _ MediaSource = (*fixtureMediaSource)(nil)
