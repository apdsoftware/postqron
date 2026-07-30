package video

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const (
	youTubeChannelPath = "/youtube/v3/channels?mine=true&part=id%2Cstatus"
	youTubeUploadPath  = "/upload/youtube/v3/videos?part=snippet%2Cstatus&uploadType=multipart"
)

type YouTube struct {
	executor authenticatedExecutor
	media    MediaSource
}

func NewYouTube(
	executor *socialconnections.AuthenticatedExecutor,
	source MediaSource,
) (*YouTube, error) {
	if executor == nil || source == nil {
		return nil, publishing.ErrInvalidArgument
	}
	return &YouTube{executor: executor, media: source}, nil
}

func newYouTubeForTest(
	executor authenticatedExecutor,
	source MediaSource,
) *YouTube {
	return &YouTube{executor: executor, media: source}
}

func (*YouTube) Capabilities() publishing.AdapterCapabilities { return capabilities() }

type youTubePayload struct {
	Video    media           `json:"video"`
	Metadata youTubeMetadata `json:"metadata"`
}

type youTubeMetadata struct {
	ChannelID              string   `json:"channel_id"`
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	Tags                   []string `json:"tags,omitempty"`
	CategoryID             string   `json:"category_id"`
	PrivacyStatus          string   `json:"privacy_status"`
	MadeForKids            *bool    `json:"made_for_kids"`
	ContainsSyntheticMedia *bool    `json:"contains_synthetic_media"`
}

type youTubeCheckpoint struct {
	Step      string `json:"step"`
	ChannelID string `json:"channel_id,omitempty"`
	VideoID   string `json:"video_id,omitempty"`
}

type youTubeChannelEnvelope struct {
	Items []struct {
		ID     string `json:"id"`
		Status struct {
			PrivacyStatus string `json:"privacyStatus"`
		} `json:"status"`
	} `json:"items"`
}

type youTubeVideoEnvelope struct {
	ID    string `json:"id"`
	Items []struct {
		ID                string `json:"id"`
		ProcessingDetails struct {
			Status string `json:"processingStatus"`
		} `json:"processingDetails"`
	} `json:"items"`
}

func (adapter *YouTube) Publish(
	ctx context.Context,
	request publishing.PublishRequest,
) (publishing.PublishResult, error) {
	payload, state, err := adapter.input(request)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	switch state.Step {
	case "":
		response, callErr := execute(
			ctx, adapter.executor, socialconnections.ProviderYouTube, request,
			http.MethodGet, youTubeChannelPath, nil, nil, nil,
		)
		if callErr != nil {
			return publishing.PublishResult{}, callErr
		}
		var envelope youTubeChannelEnvelope
		if json.Unmarshal(response.Body, &envelope) != nil ||
			len(envelope.Items) != 1 ||
			envelope.Items[0].ID != payload.Metadata.ChannelID ||
			envelope.Items[0].Status.PrivacyStatus == "" {
			return publishing.PublishResult{}, permanent(
				"youtube_channel_unavailable",
				"The authenticated YouTube channel cannot accept this upload.",
			)
		}
		state = youTubeCheckpoint{
			Step: "creator_ready", ChannelID: envelope.Items[0].ID,
		}
		encoded, _ := checkpoint(state)
		return publishing.PublishResult{Checkpoint: encoded}, nil
	case "creator_ready":
		metadata, _ := jsonBody(map[string]any{
			"snippet": map[string]any{
				"title": payload.Metadata.Title, "description": payload.Metadata.Description,
				"tags": payload.Metadata.Tags, "categoryId": payload.Metadata.CategoryID,
			},
			"status": map[string]any{
				"privacyStatus":           payload.Metadata.PrivacyStatus,
				"selfDeclaredMadeForKids": *payload.Metadata.MadeForKids,
				"containsSyntheticMedia":  *payload.Metadata.ContainsSyntheticMedia,
			},
		})
		upload, contentType, uploadErr := adapter.multipartUpload(
			ctx, payload.Video, metadata,
		)
		if uploadErr != nil {
			return publishing.PublishResult{}, uploadErr
		}
		response, callErr := execute(
			ctx, adapter.executor, socialconnections.ProviderYouTube, request,
			http.MethodPost, youTubeUploadPath,
			http.Header{"Content-Type": {contentType}}, nil, upload,
		)
		if callErr != nil {
			return publishing.PublishResult{}, callErr
		}
		return adapter.uploadComplete(response, state)
	case "processing":
		return adapter.poll(ctx, request, state)
	default:
		return publishing.PublishResult{}, permanent(
			"invalid_video_checkpoint", "YouTube checkpoint step is invalid.",
		)
	}
}

func (adapter *YouTube) Reconcile(
	ctx context.Context,
	request publishing.ReconcileRequest,
) (publishing.ReconcileResult, error) {
	var state youTubeCheckpoint
	if err := decodeCheckpoint(request.Checkpoint, &state); err != nil {
		return publishing.ReconcileResult{}, err
	}
	if state.VideoID == "" {
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "YouTube upload outcome cannot be reconciled without a returned video id.",
		}, nil
	}
	result, err := adapter.poll(ctx, publishing.PublishRequest{
		WorkspaceID: request.WorkspaceID, ConnectionID: request.ConnectionID,
		Payload: request.Payload, Checkpoint: request.Checkpoint,
	}, state)
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	if result.Complete {
		return publishing.ReconcileResult{
			State:    publishing.ReconciliationFound,
			RemoteID: result.RemoteID, Permalink: result.Permalink,
			Checkpoint: result.Checkpoint,
		}, nil
	}
	return publishing.ReconcileResult{State: publishing.ReconciliationUnknown}, nil
}

func (adapter *YouTube) uploadComplete(
	response socialconnections.PublishingResponse,
	state youTubeCheckpoint,
) (publishing.PublishResult, error) {
	var envelope youTubeVideoEnvelope
	if json.Unmarshal(response.Body, &envelope) != nil ||
		!remoteIDPattern.MatchString(envelope.ID) {
		return publishing.PublishResult{}, permanent(
			"youtube_upload_result_invalid", "YouTube did not return the uploaded video id.",
		)
	}
	state.Step = "processing"
	state.VideoID = envelope.ID
	encoded, _ := checkpoint(state)
	return publishing.PublishResult{
		Checkpoint: encoded, RetryAfter: 5 * time.Second,
	}, nil
}

func (adapter *YouTube) poll(
	ctx context.Context,
	request publishing.PublishRequest,
	state youTubeCheckpoint,
) (publishing.PublishResult, error) {
	path := "/youtube/v3/videos?id=" + state.VideoID +
		"&part=processingDetails%2Cstatus"
	if !remoteIDPattern.MatchString(state.VideoID) {
		return publishing.PublishResult{}, permanent(
			"invalid_video_checkpoint", "YouTube video id is invalid.",
		)
	}
	response, err := execute(
		ctx, adapter.executor, socialconnections.ProviderYouTube, request,
		http.MethodGet, path, nil, nil, nil,
	)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	var envelope youTubeVideoEnvelope
	if json.Unmarshal(response.Body, &envelope) != nil ||
		len(envelope.Items) != 1 || envelope.Items[0].ID != state.VideoID {
		return publishing.PublishResult{}, permanent(
			"youtube_video_missing", "YouTube could not find the uploaded video.",
		)
	}
	switch envelope.Items[0].ProcessingDetails.Status {
	case "processing":
		encoded, _ := checkpoint(state)
		return publishing.PublishResult{
			Checkpoint: encoded,
			RetryAfter: responseRetryAfter(response, 10*time.Second),
		}, nil
	case "succeeded":
		state.Step = "complete"
		encoded, _ := checkpoint(state)
		return publishing.PublishResult{
			Complete: true, RemoteID: state.VideoID,
			Permalink:  "https://www.youtube.com/shorts/" + state.VideoID,
			Checkpoint: encoded,
		}, nil
	case "failed", "terminated":
		return publishing.PublishResult{}, permanent(
			"youtube_processing_failed", "YouTube could not process the video.",
		)
	default:
		return publishing.PublishResult{}, permanent(
			"invalid_processing_status", "YouTube returned an unknown processing status.",
		)
	}
}

func (adapter *YouTube) input(
	request publishing.PublishRequest,
) (youTubePayload, youTubeCheckpoint, error) {
	var payload youTubePayload
	var state youTubeCheckpoint
	if err := decodePayload(request.Payload, &payload); err != nil {
		return payload, state, err
	}
	if err := decodeCheckpoint(request.Checkpoint, &state); err != nil {
		return payload, state, err
	}
	privacy := payload.Metadata.PrivacyStatus
	if !validMedia(payload.Video) || payload.Video.DurationSeconds > 180 ||
		payload.Metadata.ChannelID == "" ||
		strings.TrimSpace(payload.Metadata.Title) == "" ||
		len([]rune(payload.Metadata.Title)) > 100 ||
		payload.Metadata.CategoryID == "" ||
		(privacy != "private" && privacy != "unlisted" && privacy != "public") ||
		payload.Metadata.MadeForKids == nil ||
		payload.Metadata.ContainsSyntheticMedia == nil {
		return payload, state, permanent(
			"missing_required_metadata",
			"YouTube Shorts metadata and audience disclosures are incomplete.",
		)
	}
	return payload, state, nil
}

func (adapter *YouTube) multipartUpload(
	ctx context.Context,
	video media,
	metadata []byte,
) (*socialconnections.PublishingMedia, string, error) {
	source, err := adapter.media.Open(ctx, video.StorageKey)
	if err != nil || source == nil {
		return nil, "", permanent(
			"video_media_unavailable", "The immutable video media is unavailable.",
		)
	}
	defer source.Close()
	file, err := os.CreateTemp("", "postqron-youtube-upload-*")
	if err != nil {
		return nil, "", permanent(
			"video_media_unavailable", "The video upload could not be prepared.",
		)
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(file.Name())
	}
	hash := sha256.New()
	sourceHash := sha256.New()
	writer := multipart.NewWriter(io.MultiWriter(file, hash))
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metadataPart, err := writer.CreatePart(metadataHeader)
	if err == nil {
		_, err = metadataPart.Write(metadata)
	}
	videoHeader := make(textproto.MIMEHeader)
	videoHeader.Set("Content-Type", video.ContentType)
	var videoPart io.Writer
	if err == nil {
		videoPart, err = writer.CreatePart(videoHeader)
	}
	var copied int64
	if err == nil {
		copied, err = io.Copy(
			io.MultiWriter(videoPart, sourceHash),
			io.LimitReader(source, video.SizeBytes+1),
		)
	}
	if closeErr := writer.Close(); err == nil {
		err = closeErr
	}
	if err != nil || copied != video.SizeBytes ||
		fmt.Sprintf("%x", sourceHash.Sum(nil)) != video.SHA256 {
		cleanup()
		return nil, "", permanent(
			"video_media_changed", "The immutable video no longer matches its snapshot.",
		)
	}
	info, err := file.Stat()
	if err != nil {
		cleanup()
		return nil, "", permanent(
			"video_media_unavailable", "The video upload could not be prepared.",
		)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, "", permanent(
			"video_media_unavailable", "The video upload could not be prepared.",
		)
	}
	return &socialconnections.PublishingMedia{
			Body: &temporaryUpload{File: file, path: file.Name()},
			Size: info.Size(), SHA256: fmt.Sprintf("%x", hash.Sum(nil)),
		},
		"multipart/related; boundary=" + writer.Boundary(),
		nil
}

var _ publishing.Publisher = (*YouTube)(nil)
