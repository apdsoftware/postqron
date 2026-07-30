package video

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

func TestTikTokCheckpointMachineUsesAuthenticatedExecutor(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		jsonResponse(`{
			"data":{
				"creator_username":"creator",
				"privacy_level_options":["SELF_ONLY"],
				"comment_disabled":true,
				"duet_disabled":false,
				"stitch_disabled":false,
				"max_video_post_duration_sec":180
			},
			"error":{"code":"ok"}
		}`),
		jsonResponse(`{
			"data":{"publish_id":"publish-1"},
			"error":{"code":"ok"}
		}`),
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Retry-After": {"17"}},
			Body: []byte(`{
				"data":{"status":"PROCESSING_DOWNLOAD"},
				"error":{"code":"ok"}
			}`),
		},
		jsonResponse(`{
			"data":{
				"status":"PUBLISH_COMPLETE",
				"publicaly_available_post_id":["7400000000000000000"]
			},
			"error":{"code":"ok"}
		}`),
	}}
	adapter := newTikTokForTest(executor)
	request := publishing.PublishRequest{
		WorkspaceID: "workspace-1", ConnectionID: "connection-1",
		IdempotencyKey: "fixture",
		Payload: mustJSON(t, tikTokPayload{
			Video: media{
				StorageKey:      "video/object.mp4",
				SourceURL:       "https://media.example/video/object.mp4",
				ContentType:     "video/mp4",
				SizeBytes:       1024,
				SHA256:          strings.Repeat("a", 64),
				DurationSeconds: 30,
			},
			Metadata: tikTokMetadata{
				Title: "A short video", PrivacyLevel: "SELF_ONLY",
				DisableDuet: boolPointer(false), DisableStitch: boolPointer(false),
				DisableComment: boolPointer(true),
				BrandContent:   boolPointer(false), BrandOrganic: boolPointer(false),
				AIGenerated: boolPointer(false),
			},
			Consent: true,
		}),
	}

	pending, err := adapter.Publish(context.Background(), request)
	if err != nil || pending.Complete || len(pending.Checkpoint) == 0 {
		t.Fatalf("pending step result=%#v error=%v", pending, err)
	}
	request.Checkpoint = pending.Checkpoint
	first, err := adapter.Publish(context.Background(), request)
	if err != nil || first.Complete || len(first.Checkpoint) == 0 {
		t.Fatalf("creator step result=%#v error=%v", first, err)
	}
	request.Checkpoint = first.Checkpoint
	second, err := adapter.Publish(context.Background(), request)
	if err != nil || second.Complete || len(second.Checkpoint) == 0 {
		t.Fatalf("init step result=%#v error=%v", second, err)
	}
	request.Checkpoint = second.Checkpoint
	third, err := adapter.Publish(context.Background(), request)
	if err != nil || third.Complete || third.RetryAfter != 17*time.Second {
		t.Fatalf("processing step result=%#v error=%v", third, err)
	}
	request.Checkpoint = third.Checkpoint
	final, err := adapter.Publish(context.Background(), request)
	if err != nil || !final.Complete ||
		final.RemoteID != "7400000000000000000" ||
		final.Permalink !=
			"https://www.tiktok.com/@creator/video/7400000000000000000" {
		t.Fatalf("final result=%#v error=%v", final, err)
	}

	calls := executor.Calls()
	wantPaths := []string{tikTokCreatorPath, tikTokInitPath, tikTokStatusPath, tikTokStatusPath}
	if len(calls) != len(wantPaths) {
		t.Fatalf("authenticated calls=%d, want %d", len(calls), len(wantPaths))
	}
	for index, call := range calls {
		if call.ExpectedProvider != socialconnections.ProviderTikTok {
			t.Fatalf("call %d ExpectedProvider=%q", index, call.ExpectedProvider)
		}
		if call.WorkspaceID != request.WorkspaceID ||
			call.ConnectionID != request.ConnectionID ||
			call.Path != wantPaths[index] {
			t.Fatalf("call %d=%#v", index, call)
		}
		if call.Header.Get("Authorization") != "" {
			t.Fatalf("call %d leaked authorization", index)
		}
	}
}

func TestTikTokCrashAfterInitReconcilesWithoutDuplicate(t *testing.T) {
	executor := &fixtureExecutor{responses: []socialconnections.PublishingResponse{
		jsonResponse(`{
			"data":{"status":"PUBLISH_COMPLETE","publicaly_available_post_id":["99"]},
			"error":{"code":"ok"}
		}`),
	}}
	adapter := newTikTokForTest(executor)
	reconciled, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		WorkspaceID: "workspace-1", ConnectionID: "connection-1",
		Checkpoint: mustJSON(t, tikTokCheckpoint{
			Step: "processing", CreatorUsername: "creator", PublishID: "publish-1",
		}),
	})
	if err != nil || reconciled.State != publishing.ReconciliationFound ||
		reconciled.RemoteID != "99" {
		t.Fatalf("reconcile=%#v error=%v", reconciled, err)
	}
	calls := executor.Calls()
	if len(calls) != 1 || calls[0].Path != tikTokStatusPath {
		t.Fatalf("reconciliation calls=%#v", calls)
	}
}

func TestTikTokAmbiguousInitWithoutPublishIDFailsClosed(t *testing.T) {
	executor := &fixtureExecutor{}
	adapter := newTikTokForTest(executor)
	result, err := adapter.Reconcile(context.Background(), publishing.ReconcileRequest{
		Checkpoint: mustJSON(t, tikTokCheckpoint{
			Step: "creator_ready", CreatorUsername: "creator",
		}),
	})
	if err != nil || result.State != publishing.ReconciliationUnknown {
		t.Fatalf("reconcile=%#v error=%v", result, err)
	}
	if len(executor.Calls()) != 0 {
		t.Fatal("ambiguous TikTok init was repeated")
	}
}

type fixtureExecutor struct {
	mu        sync.Mutex
	responses []socialconnections.PublishingResponse
	errors    []error
	calls     []socialconnections.PublishingRequest
}

func (executor *fixtureExecutor) Execute(
	_ context.Context,
	request socialconnections.PublishingRequest,
) (socialconnections.PublishingResponse, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if request.Media != nil && request.Media.Body != nil {
		defer request.Media.Body.Close()
	}
	request.Body = append([]byte(nil), request.Body...)
	request.Header = request.Header.Clone()
	request.Media = clonePublishingMedia(request.Media)
	executor.calls = append(executor.calls, request)
	index := len(executor.calls) - 1
	if index < len(executor.errors) && executor.errors[index] != nil {
		return socialconnections.PublishingResponse{}, executor.errors[index]
	}
	if index >= len(executor.responses) {
		return socialconnections.PublishingResponse{}, io.EOF
	}
	return executor.responses[index], nil
}

func (executor *fixtureExecutor) Calls() []socialconnections.PublishingRequest {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	result := make([]socialconnections.PublishingRequest, len(executor.calls))
	copy(result, executor.calls)
	return result
}

func jsonResponse(body string) socialconnections.PublishingResponse {
	return socialconnections.PublishingResponse{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       []byte(body),
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func clonePublishingMedia(
	media *socialconnections.PublishingMedia,
) *socialconnections.PublishingMedia {
	if media == nil {
		return nil
	}
	return &socialconnections.PublishingMedia{
		Body: media.Body, Size: media.Size, SHA256: media.SHA256,
	}
}

func boolPointer(value bool) *bool { return &value }
