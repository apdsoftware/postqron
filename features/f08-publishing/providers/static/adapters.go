// Package staticproviders implements the static-origin F8 publishing adapters
// owned by issue #330. Credentials remain exclusively inside the F5
// AuthenticatedExecutor boundary.
package staticproviders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const (
	ProviderX                     = "x"
	ProviderLinkedIn              = "linkedin"
	ProviderPinterest             = "pinterest"
	ProviderGoogleBusinessProfile = "google_business_profile"

	capabilityVersion = "static-publishing-v1"
	defaultPollLimit  = 6
)

// Executor is deliberately the exact credential-free portion of the F5
// AuthenticatedExecutor. No adapter accepts a token, secret, origin, or client.
type Executor interface {
	Execute(context.Context, socialconnections.PublishingRequest) (socialconnections.PublishingResponse, error)
}

// TargetResolver returns the immutable server-side F5 connection binding.
// Provider targets supplied by publication payloads are never authoritative.
type TargetResolver interface {
	ResolveTarget(context.Context, string, string) (ConnectionTarget, error)
}

type ConnectionTarget struct {
	Provider socialconnections.Provider
	RemoteID string
}

// MediaResolver opens an immutable, workspace-bound media object. The
// returned stream is passed to F5 only through PublishingRequest.Media.
type MediaResolver interface {
	OpenMedia(context.Context, string, string) (ResolvedMedia, error)
}

type ResolvedMedia struct {
	Body        io.ReadCloser
	Size        int64
	SHA256      string
	ContentType string
}

// Gate is supplied by worker configuration. Registration is fail-closed unless
// every operational approval is explicit.
type Gate struct {
	Enabled         bool
	ReviewApproved  bool
	AuditVerified   bool
	QuotaConfigured bool
}

func (gate Gate) ready() bool {
	return gate.Enabled && gate.ReviewApproved &&
		gate.AuditVerified && gate.QuotaConfigured
}

type Config struct {
	Executor        Executor
	Targets         TargetResolver
	Media           MediaResolver
	LinkedInVersion string
	ReconcilePolls  int
	CreatePollLimit int
	Gates           map[string]Gate
}

// Capability describes formats validated by an adapter in addition to the F8
// replay-safety snapshot.
type Capability struct {
	ID        string
	Formats   []string
	MultiStep bool
}

type Adapter struct {
	provider        string
	expected        socialconnections.Provider
	executor        Executor
	targets         TargetResolver
	media           MediaResolver
	linkedinVersion string
	reconcilePolls  int
	createPollLimit int
}

// Register mounts only fully gated adapters. Missing configuration is not an
// error and leaves resolution unavailable.
func Register(registry *publishing.AdapterRegistry, config Config) error {
	if registry == nil {
		return publishing.ErrInvalidArgument
	}
	if config.Executor == nil || config.Targets == nil {
		for _, gate := range config.Gates {
			if gate.ready() {
				return publishing.ErrInvalidArgument
			}
		}
		return nil
	}
	definitions := []struct {
		name     string
		expected socialconnections.Provider
	}{
		{ProviderX, socialconnections.ProviderX},
		{ProviderLinkedIn, socialconnections.ProviderLinkedIn},
		{ProviderPinterest, socialconnections.ProviderPinterest},
		{ProviderGoogleBusinessProfile, socialconnections.ProviderGoogleBusinessProfile},
	}
	for _, definition := range definitions {
		if !config.Gates[definition.name].ready() {
			continue
		}
		if definition.name == ProviderLinkedIn &&
			!validLinkedInVersion(config.LinkedInVersion) {
			continue
		}
		if definition.name == ProviderX && config.Media == nil {
			return publishing.ErrInvalidArgument
		}
		polls := config.ReconcilePolls
		if polls <= 0 {
			polls = 3
		}
		createPollLimit := config.CreatePollLimit
		if createPollLimit <= 0 {
			createPollLimit = defaultPollLimit
		}
		adapter := &Adapter{
			provider:        definition.name,
			expected:        definition.expected,
			executor:        config.Executor,
			targets:         config.Targets,
			media:           config.Media,
			linkedinVersion: strings.TrimSpace(config.LinkedInVersion),
			reconcilePolls:  polls,
			createPollLimit: createPollLimit,
		}
		if err := registry.RegisterPublisher(definition.name, adapter); err != nil {
			return err
		}
	}
	return nil
}

func (adapter *Adapter) Capabilities() publishing.AdapterCapabilities {
	return publishing.AdapterCapabilities{
		Version:         capabilityVersion,
		Mode:            publishing.PublishingModeAuto,
		Reconciliation:  true,
		MultiStep:       true,
		RemotePermalink: true,
	}
}

func (adapter *Adapter) ProviderCapabilities() []Capability {
	switch adapter.provider {
	case ProviderX:
		return []Capability{{ID: "x.post", Formats: []string{"text", "image", "video"}, MultiStep: true}}
	case ProviderLinkedIn:
		return []Capability{
			{ID: "linkedin.profile.post", Formats: []string{"text"}, MultiStep: true},
			{ID: "linkedin.page.post", Formats: []string{"text"}, MultiStep: true},
		}
	case ProviderPinterest:
		return []Capability{{ID: "pinterest.pin", Formats: []string{"image"}, MultiStep: true}}
	case ProviderGoogleBusinessProfile:
		return []Capability{{ID: "google_business_profile.local_post", Formats: []string{"text", "image"}, MultiStep: true}}
	default:
		return nil
	}
}

type payload struct {
	Format       string  `json:"format"`
	Text         string  `json:"text,omitempty"`
	Title        string  `json:"title,omitempty"`
	Description  string  `json:"description,omitempty"`
	Link         string  `json:"link,omitempty"`
	Author       string  `json:"author,omitempty"`
	BoardID      string  `json:"board_id,omitempty"`
	Location     string  `json:"location,omitempty"`
	LanguageCode string  `json:"language_code,omitempty"`
	TopicType    string  `json:"topic_type,omitempty"`
	Media        []media `json:"media,omitempty"`
}

type media struct {
	ID          string `json:"id,omitempty"`
	Ref         string `json:"ref,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	AltText     string `json:"alt_text,omitempty"`
}

type checkpoint struct {
	Version     int      `json:"version"`
	Stage       string   `json:"stage"`
	Target      string   `json:"target"`
	BaselineIDs []string `json:"baseline_ids"`
	MediaRefs   []string `json:"media_refs,omitempty"`
	MediaIDs    []string `json:"media_ids,omitempty"`
	MediaIndex  int      `json:"media_index,omitempty"`
	UploadID    string   `json:"upload_id,omitempty"`
	UploadPath  string   `json:"upload_path,omitempty"`
	PollCount   int      `json:"poll_count,omitempty"`
}

func (adapter *Adapter) Publish(
	ctx context.Context,
	request publishing.PublishRequest,
) (publishing.PublishResult, error) {
	if adapter == nil || adapter.executor == nil {
		return publishing.PublishResult{}, permanent("provider_unavailable")
	}
	content, err := adapter.decodePayload(request.Payload)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	content, target, err := adapter.bindTarget(ctx, request.WorkspaceID, request.ConnectionID, content)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	if adapter.provider == ProviderLinkedIn && len(content.Media) != 0 {
		// LinkedIn DMS uses a signed www.linkedin.com upload origin distinct
		// from the API resource server. The current public F5 boundary accepts
		// relative paths only, so #345 must land before this can be safe.
		return publishing.PublishResult{}, permanent(
			"linkedin_media_upload_boundary_unavailable",
		)
	}
	state, err := decodeCheckpoint(request.Checkpoint)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	if state.Stage == "" {
		items, listErr := adapter.list(ctx, request, content)
		if listErr != nil {
			return publishing.PublishResult{}, listErr
		}
		state.Version = 1
		state.Stage = "create"
		state.Target = target
		state.BaselineIDs = itemIDs(items)
		state.MediaRefs = mediaRefs(content)
		encoded, encodeErr := json.Marshal(state)
		if encodeErr != nil {
			return publishing.PublishResult{}, permanent("checkpoint_invalid")
		}
		return publishing.PublishResult{Complete: false, Checkpoint: encoded}, nil
	}
	if state.Version != 1 || state.Target != target {
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
	if !equalStrings(state.MediaRefs, mediaRefs(content)) {
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
	if adapter.provider == ProviderX && len(content.Media) != 0 {
		return adapter.publishXMedia(ctx, request, content, state)
	}
	if state.Stage == "create_poll" {
		return adapter.pollCreated(ctx, request, content, state)
	}
	if state.Stage != "create" {
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
	return adapter.create(ctx, request, content, state)
}

func (adapter *Adapter) create(
	ctx context.Context,
	request publishing.PublishRequest,
	content payload,
	state checkpoint,
) (publishing.PublishResult, error) {
	if len(state.MediaIDs) != 0 {
		if len(state.MediaIDs) != len(content.Media) {
			return publishing.PublishResult{}, permanent("checkpoint_invalid")
		}
		for index := range content.Media {
			content.Media[index].ID = state.MediaIDs[index]
		}
	}
	response, err := adapter.execute(
		ctx,
		request,
		http.MethodPost,
		adapter.createPath(content),
		adapter.createHeaders(),
		adapter.createBody(content),
	)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	remoteID, permalink, err := adapter.createdResult(response, content)
	if err != nil {
		return publishing.PublishResult{}, ambiguous(
			"provider_create_response_ambiguous",
		)
	}
	state.Stage = "complete"
	return publishing.PublishResult{
		Complete:   true,
		RemoteID:   remoteID,
		Permalink:  permalink,
		Checkpoint: mustJSON(state),
	}, nil
}

func (adapter *Adapter) pollCreated(
	ctx context.Context,
	request publishing.PublishRequest,
	content payload,
	state checkpoint,
) (publishing.PublishResult, error) {
	limit := adapter.createPollLimit
	if limit <= 0 {
		limit = defaultPollLimit
	}
	if state.PollCount >= limit {
		return publishing.PublishResult{}, permanent(
			"linkedin_create_poll_exhausted",
		)
	}
	items, err := adapter.list(ctx, request, content)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	match, unique := adapter.uniqueNewMatch(content, state.BaselineIDs, items)
	if unique {
		state.Stage = "complete"
		return publishing.PublishResult{
			Complete: true, RemoteID: match.ID, Permalink: match.Permalink,
			Checkpoint: mustJSON(state),
		}, nil
	}
	state.PollCount++
	if state.PollCount >= limit {
		return publishing.PublishResult{}, permanent(
			"linkedin_create_poll_exhausted",
		)
	}
	return publishing.PublishResult{
		Complete: false, Checkpoint: mustJSON(state), RetryAfter: time.Second,
	}, nil
}

func (adapter *Adapter) Reconcile(
	ctx context.Context,
	request publishing.ReconcileRequest,
) (publishing.ReconcileResult, error) {
	if adapter == nil || adapter.executor == nil {
		return publishing.ReconcileResult{}, permanent("provider_unavailable")
	}
	content, err := adapter.decodePayload(request.Payload)
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	content, target, err := adapter.bindTarget(ctx, request.WorkspaceID, request.ConnectionID, content)
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	state, err := decodeCheckpoint(request.Checkpoint)
	if err != nil || state.Stage == "" {
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "A durable pre-create checkpoint is unavailable.",
		}, nil
	}
	if state.Version != 1 || state.Target != target ||
		!equalStrings(state.MediaRefs, mediaRefs(content)) {
		return publishing.ReconcileResult{}, permanent("checkpoint_invalid")
	}
	if adapter.provider == ProviderLinkedIn && len(content.Media) != 0 {
		return publishing.ReconcileResult{}, permanent(
			"linkedin_media_upload_boundary_unavailable",
		)
	}
	if mediaResult, handled, mediaErr := adapter.reconcileMedia(
		ctx, request, content, state,
	); handled {
		return mediaResult, mediaErr
	}
	if state.Stage != "create" && state.Stage != "x_create" &&
		state.Stage != "linkedin_create" && state.Stage != "create_poll" {
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "The durable checkpoint cannot prove a safe replay.",
		}, nil
	}
	polls := adapter.reconcilePolls
	if polls <= 0 {
		polls = 3
	}
	for index := 0; index < polls; index++ {
		items, listErr := adapter.list(ctx, publishing.PublishRequest{
			WorkspaceID: request.WorkspaceID, PostID: request.PostID,
			ChannelID: request.ChannelID, ConnectionID: request.ConnectionID,
			Payload: request.Payload, Checkpoint: request.Checkpoint,
		}, content)
		if listErr != nil {
			return publishing.ReconcileResult{}, listErr
		}
		match, unique := adapter.uniqueNewMatch(content, state.BaselineIDs, items)
		if unique {
			state.Stage = "complete"
			return publishing.ReconcileResult{
				State: publishing.ReconciliationFound, RemoteID: match.ID,
				Permalink: match.Permalink, Checkpoint: mustJSON(state),
			}, nil
		}
	}
	return publishing.ReconcileResult{
		State:      publishing.ReconciliationUnknown,
		Diagnostic: "No unique remote publication is visible after deterministic paginated polling.",
	}, nil
}

func (adapter *Adapter) uniqueNewMatch(
	content payload,
	baselineIDs []string,
	items []remoteItem,
) (remoteItem, bool) {
	baseline := make(map[string]struct{}, len(baselineIDs))
	for _, id := range baselineIDs {
		baseline[id] = struct{}{}
	}
	matches := make([]remoteItem, 0, 1)
	for _, item := range items {
		if _, existed := baseline[item.ID]; !existed && adapter.matches(content, item) {
			matches = append(matches, item)
		}
	}
	return func() (remoteItem, bool) {
		if len(matches) != 1 {
			return remoteItem{}, false
		}
		return matches[0], true
	}()
}

func (adapter *Adapter) bindTarget(
	ctx context.Context,
	workspaceID, connectionID string,
	content payload,
) (payload, string, error) {
	if adapter == nil || adapter.targets == nil {
		return payload{}, "", permanent("provider_unavailable")
	}
	target, err := adapter.targets.ResolveTarget(ctx, workspaceID, connectionID)
	if err != nil || target.Provider != adapter.expected {
		return payload{}, "", permanent("connection_target_invalid")
	}
	target.RemoteID = strings.TrimSpace(target.RemoteID)
	if target.RemoteID == "" {
		return payload{}, "", permanent("connection_target_invalid")
	}
	switch adapter.provider {
	case ProviderX:
		if _, ok := cleanSegment(target.RemoteID); !ok ||
			(content.Author != "" && content.Author != target.RemoteID) {
			return payload{}, "", permanent("connection_target_mismatch")
		}
		content.Author = target.RemoteID
	case ProviderLinkedIn:
		if (!strings.HasPrefix(target.RemoteID, "urn:li:person:") &&
			!strings.HasPrefix(target.RemoteID, "urn:li:organization:")) ||
			(content.Author != "" && content.Author != target.RemoteID) {
			return payload{}, "", permanent("connection_target_mismatch")
		}
		content.Author = target.RemoteID
	case ProviderPinterest:
		if _, ok := cleanSegment(target.RemoteID); !ok ||
			(content.BoardID != "" && content.BoardID != target.RemoteID) {
			return payload{}, "", permanent("connection_target_mismatch")
		}
		content.BoardID = target.RemoteID
	case ProviderGoogleBusinessProfile:
		location, ok := canonicalLocation(target.RemoteID)
		if !ok || (content.Location != "" && content.Location != location) {
			return payload{}, "", permanent("connection_target_mismatch")
		}
		content.Location = location
	default:
		return payload{}, "", permanent("provider_unavailable")
	}
	return content, target.RemoteID, nil
}

func (adapter *Adapter) execute(
	ctx context.Context,
	request publishing.PublishRequest,
	method, requestPath string,
	header http.Header,
	body []byte,
) (socialconnections.PublishingResponse, error) {
	response, err := adapter.executor.Execute(ctx, socialconnections.PublishingRequest{
		WorkspaceID:      request.WorkspaceID,
		ConnectionID:     request.ConnectionID,
		ExpectedProvider: adapter.expected,
		Method:           method,
		Path:             requestPath,
		Header:           header,
		Body:             body,
	})
	if err != nil {
		return socialconnections.PublishingResponse{}, mapExecutorError(err)
	}
	return response, nil
}

func (adapter *Adapter) executeMedia(
	ctx context.Context,
	request publishing.PublishRequest,
	method, requestPath string,
	header http.Header,
	media socialconnections.PublishingMedia,
) (socialconnections.PublishingResponse, error) {
	response, err := adapter.executor.Execute(ctx, socialconnections.PublishingRequest{
		WorkspaceID: request.WorkspaceID, ConnectionID: request.ConnectionID,
		ExpectedProvider: adapter.expected, Method: method, Path: requestPath,
		Header: header, Media: &media,
	})
	if err != nil {
		return socialconnections.PublishingResponse{}, mapExecutorError(err)
	}
	return response, nil
}

func mapExecutorError(err error) error {
	var failure *socialconnections.ExecutorFailure
	if !errors.As(err, &failure) {
		return permanent("authenticated_executor_rejected")
	}
	providerError := &publishing.ProviderError{
		Code:       failure.Code,
		RetryAfter: failure.RetryAfter,
	}
	switch failure.Kind {
	case socialconnections.ExecutorFailureRateLimit,
		socialconnections.ExecutorFailureTemporary:
		providerError.Retryable = true
	case socialconnections.ExecutorFailureAmbiguous:
		providerError.Retryable = true
		providerError.Ambiguous = true
	case socialconnections.ExecutorFailureReconnect,
		socialconnections.ExecutorFailurePermanent:
	default:
		providerError.Code = "authenticated_executor_rejected"
	}
	return providerError
}

func permanent(code string) *publishing.ProviderError {
	return &publishing.ProviderError{Code: code, Retryable: false}
}

func ambiguous(code string) *publishing.ProviderError {
	return &publishing.ProviderError{
		Code: code, Retryable: true, Ambiguous: true,
	}
}

func decodeCheckpoint(raw json.RawMessage) (checkpoint, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("{}")) {
		return checkpoint{}, nil
	}
	var value checkpoint
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) == nil {
		return checkpoint{}, permanent("checkpoint_invalid")
	}
	sort.Strings(value.BaselineIDs)
	value.BaselineIDs = compact(value.BaselineIDs)
	if value.Version < 0 || value.MediaIndex < 0 || value.PollCount < 0 {
		return checkpoint{}, permanent("checkpoint_invalid")
	}
	if !validCheckpointShape(value) {
		return checkpoint{}, permanent("checkpoint_invalid")
	}
	return value, nil
}

func validCheckpointShape(value checkpoint) bool {
	if value.Stage == "" {
		return value.Version == 0 && value.Target == "" &&
			len(value.BaselineIDs) == 0 && len(value.MediaRefs) == 0 &&
			len(value.MediaIDs) == 0 && value.MediaIndex == 0 &&
			value.UploadID == "" && value.UploadPath == "" &&
			value.PollCount == 0
	}
	if value.Version != 1 || strings.TrimSpace(value.Target) == "" {
		return false
	}
	switch value.Stage {
	case "create":
		return value.UploadID == "" && value.UploadPath == "" &&
			len(value.MediaIDs) == 0 && value.MediaIndex == 0
	case "x_initialize":
		return value.UploadID == "" && value.UploadPath == "" &&
			len(value.MediaIDs) == value.MediaIndex
	case "x_append", "x_finalize", "x_status":
		return validRemoteID(ProviderX, value.UploadID) &&
			value.UploadPath == "" &&
			len(value.MediaIDs) == value.MediaIndex
	case "x_create":
		return value.UploadID == "" && value.UploadPath == "" &&
			value.MediaIndex > 0 && len(value.MediaIDs) == value.MediaIndex
	case "linkedin_upload":
		return strings.HasPrefix(value.UploadID, "urn:li:image:") &&
			validLinkedInUploadURL(value.UploadPath) &&
			len(value.MediaIDs) == 0 && value.MediaIndex == 0
	case "linkedin_create":
		return strings.HasPrefix(value.UploadID, "urn:li:image:") &&
			validLinkedInUploadURL(value.UploadPath) &&
			len(value.MediaIDs) == 1 &&
			value.MediaIDs[0] == value.UploadID
	case "create_poll", "complete":
		return value.UploadPath == "" ||
			validLinkedInUploadURL(value.UploadPath)
	default:
		return false
	}
}

func validLinkedInVersion(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 6 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func compact(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if value == "" || (len(result) != 0 && result[len(result)-1] == value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func itemIDs(items []remoteItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	sort.Strings(ids)
	return compact(ids)
}

func mediaRefs(content payload) []string {
	refs := make([]string, 0, len(content.Media))
	for _, item := range content.Media {
		if item.Ref != "" {
			refs = append(refs, strings.Join([]string{
				"ref", item.Ref, item.ContentType,
				fmtInt64(item.Size), item.SHA256, item.AltText,
			}, "\x00"))
		} else {
			refs = append(refs, "url:"+item.SourceURL)
		}
	}
	return refs
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cleanSegment(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "/?#\\") || value == "." || value == ".." {
		return "", false
	}
	return url.PathEscape(value), true
}

func validHTTPS(raw string) bool {
	target, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && target.Scheme == "https" && target.Host != "" &&
		target.User == nil && target.Fragment == ""
}

func canonicalJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func queryPath(base string, values url.Values) string {
	encoded := values.Encode()
	if encoded == "" {
		return base
	}
	return base + "?" + encoded
}

func permalinkFor(provider, id string) string {
	switch provider {
	case ProviderX:
		return "https://x.com/i/web/status/" + id
	case ProviderLinkedIn:
		return "https://www.linkedin.com/feed/update/" + id
	case ProviderPinterest:
		return "https://www.pinterest.com/pin/" + id + "/"
	default:
		return ""
	}
}

func validRemoteID(provider, id string) bool {
	id = strings.TrimSpace(id)
	switch provider {
	case ProviderX, ProviderPinterest:
		if id == "" || len(id) > 32 {
			return false
		}
		for _, character := range id {
			if character < '0' || character > '9' {
				return false
			}
		}
		return true
	case ProviderLinkedIn:
		return strings.HasPrefix(id, "urn:li:share:") ||
			strings.HasPrefix(id, "urn:li:ugcPost:")
	case ProviderGoogleBusinessProfile:
		parts := strings.Split(id, "/")
		if len(parts) != 6 || parts[0] != "accounts" ||
			parts[2] != "locations" || parts[4] != "localPosts" {
			return false
		}
		for _, index := range []int{1, 3, 5} {
			if _, ok := cleanSegment(parts[index]); !ok {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func canonicalLocation(value string) (string, bool) {
	cleaned := path.Clean("/" + strings.TrimSpace(value))
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	if len(parts) != 4 || parts[0] != "accounts" || parts[2] != "locations" {
		return "", false
	}
	for _, index := range []int{1, 3} {
		if _, ok := cleanSegment(parts[index]); !ok {
			return "", false
		}
	}
	return strings.TrimPrefix(cleaned, "/"), true
}

var _ publishing.Publisher = (*Adapter)(nil)
