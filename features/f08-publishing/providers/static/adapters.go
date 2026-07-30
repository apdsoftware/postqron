// Package staticproviders implements the static-origin F8 publishing adapters
// owned by issue #330. Credentials remain exclusively inside the F5
// AuthenticatedExecutor boundary.
package staticproviders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

const (
	ProviderX                     = "x"
	ProviderLinkedIn              = "linkedin"
	ProviderPinterest             = "pinterest"
	ProviderGoogleBusinessProfile = "google_business_profile"

	capabilityVersion = "static-publishing-v1"
)

// Executor is deliberately the exact credential-free portion of the F5
// AuthenticatedExecutor. No adapter accepts a token, secret, origin, or client.
type Executor interface {
	Execute(context.Context, socialconnections.PublishingRequest) (socialconnections.PublishingResponse, error)
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
	LinkedInVersion string
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
	linkedinVersion string
}

// Register mounts only fully gated adapters. Missing configuration is not an
// error and leaves resolution unavailable.
func Register(registry *publishing.AdapterRegistry, config Config) error {
	if registry == nil {
		return publishing.ErrInvalidArgument
	}
	if config.Executor == nil {
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
		adapter := &Adapter{
			provider:        definition.name,
			expected:        definition.expected,
			executor:        config.Executor,
			linkedinVersion: strings.TrimSpace(config.LinkedInVersion),
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
			{ID: "linkedin.profile.post", Formats: []string{"text", "image"}, MultiStep: true},
			{ID: "linkedin.page.post", Formats: []string{"text", "image"}, MultiStep: true},
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
	ID        string `json:"id,omitempty"`
	SourceURL string `json:"source_url,omitempty"`
	AltText   string `json:"alt_text,omitempty"`
}

type checkpoint struct {
	Stage       string   `json:"stage"`
	BaselineIDs []string `json:"baseline_ids"`
	MediaRefs   []string `json:"media_refs,omitempty"`
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
	state, err := decodeCheckpoint(request.Checkpoint)
	if err != nil {
		return publishing.PublishResult{}, err
	}
	if state.Stage == "" {
		items, listErr := adapter.list(ctx, request, content)
		if listErr != nil {
			return publishing.PublishResult{}, listErr
		}
		state.Stage = "create"
		state.BaselineIDs = itemIDs(items)
		state.MediaRefs = mediaRefs(content)
		encoded, encodeErr := json.Marshal(state)
		if encodeErr != nil {
			return publishing.PublishResult{}, permanent("checkpoint_invalid")
		}
		return publishing.PublishResult{Complete: false, Checkpoint: encoded}, nil
	}
	if state.Stage != "create" {
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
	}
	if !equalStrings(state.MediaRefs, mediaRefs(content)) {
		return publishing.PublishResult{}, permanent("checkpoint_invalid")
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
		return publishing.PublishResult{}, err
	}
	return publishing.PublishResult{
		Complete:  true,
		RemoteID:  remoteID,
		Permalink: permalink,
		Checkpoint: mustJSON(checkpoint{
			Stage:       "complete",
			BaselineIDs: state.BaselineIDs,
			MediaRefs:   state.MediaRefs,
		}),
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
	state, err := decodeCheckpoint(request.Checkpoint)
	if err != nil || state.Stage == "" {
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "A durable pre-create checkpoint is unavailable.",
		}, nil
	}
	if !equalStrings(state.MediaRefs, mediaRefs(content)) {
		return publishing.ReconcileResult{}, permanent("checkpoint_invalid")
	}
	if state.Stage != "create" {
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "The durable checkpoint is not in the pre-create state.",
		}, nil
	}
	items, err := adapter.list(ctx, publishing.PublishRequest{
		WorkspaceID:  request.WorkspaceID,
		PostID:       request.PostID,
		ChannelID:    request.ChannelID,
		ConnectionID: request.ConnectionID,
		Payload:      request.Payload,
		Checkpoint:   request.Checkpoint,
	}, content)
	if err != nil {
		return publishing.ReconcileResult{}, err
	}
	baseline := make(map[string]struct{}, len(state.BaselineIDs))
	for _, id := range state.BaselineIDs {
		baseline[id] = struct{}{}
	}
	matches := make([]remoteItem, 0, 1)
	for _, item := range items {
		if _, existed := baseline[item.ID]; !existed && adapter.matches(content, item) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return publishing.ReconcileResult{State: publishing.ReconciliationNotFound}, nil
	}
	if len(matches) != 1 {
		return publishing.ReconcileResult{
			State:      publishing.ReconciliationUnknown,
			Diagnostic: "More than one new remote item matches the immutable publication.",
		}, nil
	}
	return publishing.ReconcileResult{
		State:     publishing.ReconciliationFound,
		RemoteID:  matches[0].ID,
		Permalink: matches[0].Permalink,
		Checkpoint: mustJSON(checkpoint{
			Stage:       "complete",
			BaselineIDs: state.BaselineIDs,
			MediaRefs:   state.MediaRefs,
		}),
	}, nil
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
	sort.Strings(value.MediaRefs)
	value.MediaRefs = compact(value.MediaRefs)
	return value, nil
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
		if item.ID != "" {
			refs = append(refs, "id:"+item.ID)
		} else {
			refs = append(refs, "url:"+item.SourceURL)
		}
	}
	sort.Strings(refs)
	return compact(refs)
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
