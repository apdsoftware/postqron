package video

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const (
	tikTokCreatorPath = "/v2/post/publish/creator_info/query/"
	tikTokInitPath    = "/v2/post/publish/video/init/"
	tikTokStatusPath  = "/v2/post/publish/status/fetch/"
)

type TikTok struct {
	executor   authenticatedExecutor
	pullPrefix string
}

type TikTokConfig struct {
	// VerifiedPullURLPrefix is an HTTPS URL-prefix property whose ownership
	// has been verified for the configured TikTok application.
	VerifiedPullURLPrefix string
}

func NewTikTok(
	executor *socialconnections.AuthenticatedExecutor,
	config TikTokConfig,
) (*TikTok, error) {
	if executor == nil {
		return nil, publishing.ErrInvalidArgument
	}
	prefix, err := verifiedTikTokPullPrefix(config.VerifiedPullURLPrefix)
	if err != nil {
		return nil, publishing.ErrInvalidArgument
	}
	return &TikTok{executor: executor, pullPrefix: prefix}, nil
}

func newTikTokForTest(executor authenticatedExecutor, prefix ...string) *TikTok {
	configured := "https://media.example/tiktok/"
	if len(prefix) == 1 {
		configured = prefix[0]
	}
	verified, _ := verifiedTikTokPullPrefix(configured)
	return &TikTok{executor: executor, pullPrefix: verified}
}

func (*TikTok) Capabilities() publishing.AdapterCapabilities { return capabilities(true) }

type tikTokPayload struct {
	Video    media          `json:"video"`
	Metadata tikTokMetadata `json:"metadata"`
	Consent  bool           `json:"creator_consent"`
}

type tikTokMetadata struct {
	Title          string `json:"title"`
	PrivacyLevel   string `json:"privacy_level"`
	DisableDuet    *bool  `json:"disable_duet"`
	DisableStitch  *bool  `json:"disable_stitch"`
	DisableComment *bool  `json:"disable_comment"`
	BrandContent   *bool  `json:"brand_content"`
	BrandOrganic   *bool  `json:"brand_organic"`
	AIGenerated    *bool  `json:"ai_generated"`
}

type tikTokCheckpoint struct {
	Step               string   `json:"step"`
	CreatorUsername    string   `json:"creator_username,omitempty"`
	PrivacyLevels      []string `json:"privacy_levels,omitempty"`
	CommentDisabled    bool     `json:"comment_disabled,omitempty"`
	DuetDisabled       bool     `json:"duet_disabled,omitempty"`
	StitchDisabled     bool     `json:"stitch_disabled,omitempty"`
	MaxDurationSeconds int      `json:"max_duration_seconds,omitempty"`
	PublishID          string   `json:"publish_id,omitempty"`
}

type tikTokEnvelope struct {
	Data struct {
		CreatorUsername    string   `json:"creator_username"`
		PrivacyLevels      []string `json:"privacy_level_options"`
		CommentDisabled    bool     `json:"comment_disabled"`
		DuetDisabled       bool     `json:"duet_disabled"`
		StitchDisabled     bool     `json:"stitch_disabled"`
		MaxDurationSeconds int      `json:"max_video_post_duration_sec"`
		PublishID          string   `json:"publish_id"`
		Status             string   `json:"status"`
		PostIDs            []string `json:"publicaly_available_post_id"`
		FailReason         string   `json:"fail_reason"`
	} `json:"data"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (adapter *TikTok) Publish(
	ctx context.Context,
	request publishing.PublishRequest,
) (publishing.PublishResult, error) {
	payload, state, err := adapter.input(request)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	switch state.Step {
	case "":
		state.Step = "capability_pending"
		encoded, _ := checkpoint(state)
		return publishing.PublishResult{Checkpoint: encoded}, nil
	case "capability_pending":
		response, callErr := execute(
			ctx, adapter.executor, socialconnections.ProviderTikTok, request,
			http.MethodPost, tikTokCreatorPath,
			http.Header{"Content-Type": {"application/json; charset=UTF-8"}},
			[]byte("{}"), nil,
		)
		if callErr != nil {
			return publishing.PublishResult{}, callErr
		}
		var envelope tikTokEnvelope
		if json.Unmarshal(response.Body, &envelope) != nil ||
			envelope.Error.Code != "ok" ||
			envelope.Data.CreatorUsername == "" ||
			!tikTokUsernamePattern.MatchString(envelope.Data.CreatorUsername) ||
			envelope.Data.MaxDurationSeconds <= 0 ||
			len(envelope.Data.PrivacyLevels) == 0 {
			return publishing.PublishResult{}, permanent(
				"invalid_creator_info", "TikTok creator capabilities are incomplete.",
			)
		}
		state = tikTokCheckpoint{
			Step:               "creator_ready",
			CreatorUsername:    envelope.Data.CreatorUsername,
			PrivacyLevels:      append([]string(nil), envelope.Data.PrivacyLevels...),
			CommentDisabled:    envelope.Data.CommentDisabled,
			DuetDisabled:       envelope.Data.DuetDisabled,
			StitchDisabled:     envelope.Data.StitchDisabled,
			MaxDurationSeconds: envelope.Data.MaxDurationSeconds,
		}
		encoded, _ := checkpoint(state)
		return publishing.PublishResult{Checkpoint: encoded, RetryAfter: 0}, nil
	case "creator_ready":
		if err := validateTikTokCapabilities(payload, state); err != nil {
			return publishing.PublishResult{}, err
		}
		pullURL, err := adapter.pullURL(payload.Video)
		if err != nil {
			return publishing.PublishResult{}, err
		}
		body, _ := jsonBody(map[string]any{
			"post_info": map[string]any{
				"title":                payload.Metadata.Title,
				"privacy_level":        payload.Metadata.PrivacyLevel,
				"disable_duet":         *payload.Metadata.DisableDuet,
				"disable_stitch":       *payload.Metadata.DisableStitch,
				"disable_comment":      *payload.Metadata.DisableComment,
				"brand_content_toggle": *payload.Metadata.BrandContent,
				"brand_organic_toggle": *payload.Metadata.BrandOrganic,
				"is_aigc":              *payload.Metadata.AIGenerated,
			},
			"source_info": map[string]any{
				"source":    "PULL_FROM_URL",
				"video_url": pullURL,
			},
		})
		response, callErr := execute(
			ctx, adapter.executor, socialconnections.ProviderTikTok, request,
			http.MethodPost, tikTokInitPath,
			http.Header{"Content-Type": {"application/json; charset=UTF-8"}},
			body, nil,
		)
		if callErr != nil {
			return publishing.PublishResult{}, callErr
		}
		var envelope tikTokEnvelope
		if json.Unmarshal(response.Body, &envelope) != nil ||
			envelope.Error.Code != "ok" || envelope.Data.PublishID == "" {
			return publishing.PublishResult{}, ambiguous(
				"tiktok_init_outcome_ambiguous",
				"TikTok accepted the initialization without a publish id.",
			)
		}
		state.Step = "processing"
		state.PublishID = envelope.Data.PublishID
		encoded, _ := checkpoint(state)
		return publishing.PublishResult{
			Checkpoint: encoded, RetryAfter: 10 * time.Second,
		}, nil
	case "processing":
		return adapter.poll(ctx, request, state)
	case "complete":
		return publishing.PublishResult{}, permanent(
			"invalid_video_checkpoint", "TikTok publication is already complete.",
		)
	default:
		return publishing.PublishResult{}, permanent(
			"invalid_video_checkpoint", "TikTok checkpoint step is invalid.",
		)
	}
}

func (adapter *TikTok) Reconcile(
	ctx context.Context,
	request publishing.ReconcileRequest,
) (publishing.ReconcileResult, error) {
	var state tikTokCheckpoint
	if err := decodeCheckpoint(request.Checkpoint, &state); err != nil {
		return publishing.ReconcileResult{}, err
	}
	if state.PublishID == "" {
		if state.Step == "capability_pending" {
			return publishing.ReconcileResult{State: publishing.ReconciliationNotFound}, nil
		}
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "TikTok post initialization cannot be replayed without a publish id.",
		}, nil
	}
	result, err := adapter.poll(ctx, publishing.PublishRequest{
		WorkspaceID: request.WorkspaceID, PostID: request.PostID,
		ChannelID: request.ChannelID, ConnectionID: request.ConnectionID,
		Payload: request.Payload, Checkpoint: request.Checkpoint,
		IdempotencyKey: request.IdempotencyKey,
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

func (adapter *TikTok) poll(
	ctx context.Context,
	request publishing.PublishRequest,
	state tikTokCheckpoint,
) (publishing.PublishResult, error) {
	body, _ := jsonBody(map[string]string{"publish_id": state.PublishID})
	response, err := execute(
		ctx, adapter.executor, socialconnections.ProviderTikTok, request,
		http.MethodPost, tikTokStatusPath,
		http.Header{"Content-Type": {"application/json; charset=UTF-8"}},
		body, nil,
	)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	var envelope tikTokEnvelope
	if json.Unmarshal(response.Body, &envelope) != nil ||
		envelope.Error.Code != "ok" || envelope.Data.Status == "" {
		return publishing.PublishResult{}, permanent(
			"invalid_processing_status", "TikTok returned an invalid processing status.",
		)
	}
	switch envelope.Data.Status {
	case "PROCESSING_UPLOAD", "PROCESSING_DOWNLOAD":
		encoded, _ := checkpoint(state)
		return publishing.PublishResult{
			Checkpoint: encoded, RetryAfter: responseRetryAfter(response, 10*time.Second),
		}, nil
	case "PUBLISH_COMPLETE":
		remoteID := state.PublishID
		permalink := ""
		if len(envelope.Data.PostIDs) == 1 &&
			tikTokPostIDPattern.MatchString(envelope.Data.PostIDs[0]) {
			remoteID = envelope.Data.PostIDs[0]
			permalink = "https://www.tiktok.com/@" +
				state.CreatorUsername + "/video/" + remoteID
		}
		state.Step = "complete"
		encoded, _ := checkpoint(state)
		return publishing.PublishResult{
			Complete: true, RemoteID: remoteID, Permalink: permalink,
			Checkpoint: encoded,
		}, nil
	case "FAILED":
		return publishing.PublishResult{}, permanent(
			"tiktok_processing_failed", "TikTok could not process the video.",
		)
	default:
		return publishing.PublishResult{}, permanent(
			"invalid_processing_status", "TikTok returned an unknown processing status.",
		)
	}
}

func (adapter *TikTok) input(
	request publishing.PublishRequest,
) (tikTokPayload, tikTokCheckpoint, error) {
	var payload tikTokPayload
	var state tikTokCheckpoint
	if err := decodePayload(request.Payload, &payload); err != nil {
		return payload, state, err
	}
	if err := decodeCheckpoint(request.Checkpoint, &state); err != nil {
		return payload, state, err
	}
	if !validMedia(payload.Video) ||
		!payload.Consent || payload.Metadata.PrivacyLevel == "" ||
		len([]rune(payload.Metadata.Title)) > 2200 ||
		payload.Metadata.DisableDuet == nil ||
		payload.Metadata.DisableStitch == nil ||
		payload.Metadata.DisableComment == nil ||
		payload.Metadata.BrandContent == nil ||
		payload.Metadata.BrandOrganic == nil ||
		payload.Metadata.AIGenerated == nil {
		return payload, state, permanent(
			"missing_required_metadata", "TikTok video metadata or creator consent is incomplete.",
		)
	}
	return payload, state, nil
}

func verifiedTikTokPullPrefix(raw string) (string, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	canonicalPath := ""
	if err == nil {
		canonicalPath = path.Clean(parsed.Path)
		if canonicalPath != "/" {
			canonicalPath += "/"
		}
	}
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Port() != "" || net.ParseIP(parsed.Hostname()) != nil ||
		!strings.HasSuffix(parsed.Path, "/") ||
		canonicalPath != parsed.Path {
		return "", publishing.ErrInvalidArgument
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}

func (adapter *TikTok) pullURL(value media) (string, error) {
	if adapter.pullPrefix == "" {
		return "", permanent(
			"tiktok_pull_source_unverified",
			"TikTok media delivery is not bound to a verified URL prefix.",
		)
	}
	name := path.Base(strings.TrimSpace(value.StorageKey))
	if name == "." || name == ".." || name == "/" || name == "" {
		return "", permanent(
			"tiktok_pull_source_invalid",
			"TikTok media delivery cannot identify the immutable object.",
		)
	}
	expected := adapter.pullPrefix + value.SHA256 + "/" +
		strconv.FormatInt(value.SizeBytes, 10) + "/" + url.PathEscape(name)
	if strings.TrimSpace(value.SourceURL) != expected ||
		!validHTTPSMediaURL(value.SourceURL) {
		return "", permanent(
			"tiktok_pull_source_unverified",
			"TikTok media URL does not match the verified immutable object prefix.",
		)
	}
	return expected, nil
}

func validateTikTokCapabilities(
	payload tikTokPayload,
	state tikTokCheckpoint,
) error {
	found := false
	for _, option := range state.PrivacyLevels {
		found = found || option == payload.Metadata.PrivacyLevel
	}
	if !found ||
		payload.Video.DurationSeconds > float64(state.MaxDurationSeconds) ||
		(state.CommentDisabled && !*payload.Metadata.DisableComment) ||
		(state.DuetDisabled && !*payload.Metadata.DisableDuet) ||
		(state.StitchDisabled && !*payload.Metadata.DisableStitch) {
		return permanent(
			"creator_capability_mismatch",
			"TikTok creator capabilities no longer permit this video metadata.",
		)
	}
	return nil
}

func responseRetryAfter(
	response socialconnections.PublishingResponse,
	fallback time.Duration,
) time.Duration {
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil &&
		seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

var _ publishing.Publisher = (*TikTok)(nil)
