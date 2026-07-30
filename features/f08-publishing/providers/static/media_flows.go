package staticproviders

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

func (adapter *Adapter) publishXMedia(
	ctx context.Context,
	request publishing.PublishRequest,
	content payload,
	state checkpoint,
) (publishing.PublishResult, error) {
	if state.MediaIndex >= len(content.Media) {
		if state.Stage != "x_create" {
			state.Stage = "x_create"
			return progress(state, 0), nil
		}
		return adapter.create(ctx, request, content, state)
	}
	current := content.Media[state.MediaIndex]
	switch state.Stage {
	case "create", "x_initialize":
		response, err := adapter.execute(
			ctx, request, http.MethodPost, "/2/media/upload/initialize",
			http.Header{"Content-Type": {"application/json"}},
			canonicalJSON(map[string]any{
				"media_category": xMediaCategory(current.ContentType),
				"media_type":     current.ContentType,
				"shared":         false,
				"total_bytes":    current.Size,
			}),
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		uploadID, _, err := decodeXMediaState(response.Body)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		state.Stage = "x_append"
		state.UploadID = uploadID
		return progress(state, 0), nil
	case "x_append":
		mediaRequest, contentType, err := adapter.xMultipartMedia(
			ctx, request.WorkspaceID, current,
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		_, err = adapter.executeMedia(
			ctx, request, http.MethodPost,
			"/2/media/upload/"+state.UploadID+"/append",
			http.Header{"Content-Type": {contentType}},
			mediaRequest,
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		state.Stage = "x_finalize"
		return progress(state, 0), nil
	case "x_finalize":
		response, err := adapter.execute(
			ctx, request, http.MethodPost,
			"/2/media/upload/"+state.UploadID+"/finalize",
			http.Header{"Content-Type": {"application/json"}}, nil,
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		uploadID, delay, err := decodeXMediaState(response.Body)
		if err != nil || uploadID != state.UploadID {
			if err != nil {
				return publishing.PublishResult{}, err
			}
			return publishing.PublishResult{}, permanent("provider_response_invalid")
		}
		state.Stage = "x_status"
		return progress(state, delay), nil
	case "x_status":
		response, err := adapter.execute(
			ctx, request, http.MethodGet,
			queryPath("/2/media/upload", url.Values{
				"command": {"STATUS"}, "media_id": {state.UploadID},
			}),
			http.Header{}, nil,
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		uploadID, delay, processing, err := decodeXMediaStatus(response.Body)
		if err != nil || uploadID != state.UploadID {
			if err != nil {
				return publishing.PublishResult{}, err
			}
			return publishing.PublishResult{}, permanent("provider_response_invalid")
		}
		switch processing {
		case "", "succeeded":
			state.MediaIDs = append(state.MediaIDs, state.UploadID)
			state.MediaIndex++
			state.UploadID = ""
			state.Stage = "x_initialize"
			return progress(state, 0), nil
		case "pending", "in_progress":
			return progress(state, delay), nil
		default:
			return publishing.PublishResult{}, permanent("media_processing_failed")
		}
	default:
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
}

func (adapter *Adapter) publishLinkedInMedia(
	ctx context.Context,
	request publishing.PublishRequest,
	content payload,
	state checkpoint,
) (publishing.PublishResult, error) {
	current := content.Media[0]
	switch state.Stage {
	case "create":
		response, err := adapter.execute(
			ctx, request, http.MethodPost,
			"/rest/images?action=initializeUpload",
			adapter.createHeaders(),
			canonicalJSON(map[string]any{
				"initializeUploadRequest": map[string]any{"owner": content.Author},
			}),
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		uploadID, uploadPath, err := decodeLinkedInInitialize(response.Body)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		state.Stage = "linkedin_upload"
		state.UploadID = uploadID
		state.UploadPath = uploadPath
		return progress(state, 0), nil
	case "linkedin_upload":
		mediaRequest, err := adapter.openMedia(
			ctx, request.WorkspaceID, current,
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		_, err = adapter.executeMedia(
			ctx, request, http.MethodPut, state.UploadPath,
			http.Header{"Content-Type": {current.ContentType}},
			mediaRequest,
		)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		state.MediaIDs = []string{state.UploadID}
		state.Stage = "linkedin_create"
		return progress(state, 0), nil
	case "create_poll":
		return adapter.pollCreated(ctx, request, content, state)
	case "linkedin_create":
		return adapter.create(ctx, request, content, state)
	default:
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
}

func (adapter *Adapter) reconcileMedia(
	ctx context.Context,
	request publishing.ReconcileRequest,
	content payload,
	state checkpoint,
) (publishing.ReconcileResult, bool, error) {
	publishRequest := publishing.PublishRequest{
		WorkspaceID: request.WorkspaceID, PostID: request.PostID,
		ChannelID: request.ChannelID, ConnectionID: request.ConnectionID,
		Payload: request.Payload, Checkpoint: request.Checkpoint,
	}
	switch state.Stage {
	case "x_append":
		// APPEND is keyed by immutable upload id and segment index zero.
		// Replaying that exact segment cannot create a second publication.
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationNotFound,
			Diagnostic: "The immutable upload segment is safe to replay.",
		}, true, nil
	case "x_finalize", "x_status":
		response, err := adapter.execute(
			ctx, publishRequest, http.MethodGet,
			queryPath("/2/media/upload", url.Values{
				"command": {"STATUS"}, "media_id": {state.UploadID},
			}), http.Header{}, nil,
		)
		if err != nil {
			return publishing.ReconcileResult{}, true, err
		}
		_, _, processing, err := decodeXMediaStatus(response.Body)
		if err != nil {
			return publishing.ReconcileResult{}, true, err
		}
		if processing == "failed" {
			return publishing.ReconcileResult{}, true,
				permanent("media_processing_failed")
		}
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationNotFound,
			Diagnostic: "Provider media status proves the checkpoint can resume without creating a post.",
		}, true, nil
	case "linkedin_upload":
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationNotFound,
			Diagnostic: "The provider-issued upload URL and immutable media digest make PUT replay safe.",
		}, true, nil
	default:
		return publishing.ReconcileResult{}, false, nil
	}
}

func (adapter *Adapter) openMedia(
	ctx context.Context,
	workspaceID string,
	item media,
) (socialconnections.PublishingMedia, error) {
	if adapter.media == nil {
		return socialconnections.PublishingMedia{}, permanent("media_unavailable")
	}
	resolved, err := adapter.media.OpenMedia(ctx, workspaceID, item.Ref)
	if err != nil {
		return socialconnections.PublishingMedia{}, permanent("media_unavailable")
	}
	if resolved.Body == nil || resolved.Size != item.Size ||
		resolved.SHA256 != item.SHA256 ||
		resolved.ContentType != item.ContentType {
		if resolved.Body != nil {
			_ = resolved.Body.Close()
		}
		return socialconnections.PublishingMedia{}, permanent("media_snapshot_mismatch")
	}
	return socialconnections.PublishingMedia{
		Body: resolved.Body, Size: resolved.Size, SHA256: resolved.SHA256,
	}, nil
}

func (adapter *Adapter) xMultipartMedia(
	ctx context.Context,
	workspaceID string,
	item media,
) (socialconnections.PublishingMedia, string, error) {
	source, err := adapter.openMedia(ctx, workspaceID, item)
	if err != nil {
		return socialconnections.PublishingMedia{}, "", err
	}
	defer source.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(source.Body, source.Size+1))
	if err != nil || int64(len(raw)) != source.Size {
		return socialconnections.PublishingMedia{}, "", permanent("media_snapshot_mismatch")
	}
	rawDigest := sha256.Sum256(raw)
	if hex.EncodeToString(rawDigest[:]) != source.SHA256 {
		return socialconnections.PublishingMedia{}, "", permanent("media_snapshot_mismatch")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	boundary := "postqron-" + source.SHA256[:24]
	if err = writer.SetBoundary(boundary); err != nil {
		return socialconnections.PublishingMedia{}, "", permanent("media_snapshot_mismatch")
	}
	if err = writer.WriteField("segment_index", "0"); err != nil {
		return socialconnections.PublishingMedia{}, "", permanent("media_snapshot_mismatch")
	}
	part, err := writer.CreateFormFile("media", "media")
	if err != nil {
		return socialconnections.PublishingMedia{}, "", permanent("media_snapshot_mismatch")
	}
	if _, err = part.Write(raw); err != nil {
		return socialconnections.PublishingMedia{}, "", permanent("media_snapshot_mismatch")
	}
	if err = writer.Close(); err != nil {
		return socialconnections.PublishingMedia{}, "", permanent("media_snapshot_mismatch")
	}
	encoded := append([]byte(nil), body.Bytes()...)
	digest := sha256.Sum256(encoded)
	return socialconnections.PublishingMedia{
		Body: io.NopCloser(bytes.NewReader(encoded)), Size: int64(len(encoded)),
		SHA256: hex.EncodeToString(digest[:]),
	}, writer.FormDataContentType(), nil
}

func decodeXMediaState(body []byte) (string, time.Duration, error) {
	id, delay, _, err := decodeXMediaStatus(body)
	return id, delay, err
}

func decodeXMediaStatus(body []byte) (string, time.Duration, string, error) {
	var value struct {
		Data struct {
			ID             string `json:"id"`
			ProcessingInfo struct {
				State          string `json:"state"`
				CheckAfterSecs int64  `json:"check_after_secs"`
			} `json:"processing_info"`
		} `json:"data"`
	}
	if strictJSON(body, &value) != nil ||
		!validRemoteID(ProviderX, value.Data.ID) ||
		value.Data.ProcessingInfo.CheckAfterSecs < 0 {
		return "", 0, "", permanent("provider_response_invalid")
	}
	return value.Data.ID,
		time.Duration(value.Data.ProcessingInfo.CheckAfterSecs) * time.Second,
		strings.ToLower(value.Data.ProcessingInfo.State), nil
}

func decodeLinkedInInitialize(body []byte) (string, string, error) {
	var value struct {
		Value struct {
			UploadURL string `json:"uploadUrl"`
			Image     string `json:"image"`
		} `json:"value"`
	}
	if strictJSON(body, &value) != nil ||
		!strings.HasPrefix(value.Value.Image, "urn:li:image:") {
		return "", "", permanent("provider_response_invalid")
	}
	target, err := url.Parse(value.Value.UploadURL)
	if err != nil || target.Scheme != "https" ||
		(target.Host != "www.linkedin.com" &&
			target.Host != "api.linkedin.com") ||
		target.User != nil || target.Fragment != "" ||
		!strings.HasPrefix(target.EscapedPath(), "/dms-uploads/") {
		return "", "", permanent("provider_response_invalid")
	}
	return value.Value.Image, target.RequestURI(), nil
}

func xMediaCategory(contentType string) string {
	switch contentType {
	case "image/gif":
		return "tweet_gif"
	case "video/mp4", "video/webm", "video/quicktime":
		return "tweet_video"
	default:
		return "tweet_image"
	}
}

func progress(state checkpoint, delay time.Duration) publishing.PublishResult {
	return publishing.PublishResult{
		Complete: false, Checkpoint: mustJSON(state), RetryAfter: delay,
	}
}

func fmtInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
