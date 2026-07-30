package video

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
)

func TestTikTokOfflineTLSFixtureThroughAuthenticatedExecutor(t *testing.T) {
	var calls atomic.Int32
	var sawBearer atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
			sawBearer.Store(true)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case tikTokCreatorPath:
			_, _ = io.WriteString(writer, `{
				"data":{
					"creator_username":"tls_creator",
					"privacy_level_options":["SELF_ONLY"],
					"max_video_post_duration_sec":180
				},
				"error":{"code":"ok"}
			}`)
		case tikTokInitPath:
			_, _ = io.WriteString(writer, `{
				"data":{"publish_id":"tls-publish"},
				"error":{"code":"ok"}
			}`)
		case tikTokStatusPath:
			_, _ = io.WriteString(writer, `{
				"data":{
					"status":"PUBLISH_COMPLETE",
					"publicaly_available_post_id":["12345"]
				},
				"error":{"code":"ok"}
			}`)
		default:
			http.NotFound(writer, request)
		}
		calls.Add(1)
	}))
	defer server.Close()

	transport := &tlsPinnedTransport{
		origin: "https://open.tiktokapis.com",
		target: server.URL,
		base:   server.Client().Transport,
	}
	executor, connection := connectedTikTokExecutor(t, transport)
	recording := &recordingAuthenticatedExecutor{inner: executor}
	adapter := newTikTokForTest(recording)
	request := publishing.PublishRequest{
		WorkspaceID: "workspace-1", ConnectionID: connection.ID,
		Payload: mustJSON(t, tikTokPayload{
			Video: media{
				StorageKey: "video", SourceURL: "https://media.example/video.mp4",
				ContentType: "video/mp4", SizeBytes: 10,
				SHA256: strings.Repeat("a", 64), DurationSeconds: 10,
			},
			Metadata: tikTokMetadata{
				Title: "TLS fixture", PrivacyLevel: "SELF_ONLY",
				DisableDuet: boolPointer(false), DisableStitch: boolPointer(false),
				DisableComment: boolPointer(false),
				BrandContent:   boolPointer(false), BrandOrganic: boolPointer(false),
				AIGenerated: boolPointer(false),
			},
			Consent: true,
		}),
	}
	for step := 0; step < 3; step++ {
		result, publishErr := adapter.Publish(context.Background(), request)
		if publishErr != nil {
			t.Fatalf("step %d: %v (executor=%v)", step, publishErr, recording.err)
		}
		request.Checkpoint = result.Checkpoint
	}
	result, err := adapter.Publish(context.Background(), request)
	if err != nil || !result.Complete || result.RemoteID != "12345" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if calls.Load() != 3 || !sawBearer.Load() {
		t.Fatalf("TLS calls=%d bearer=%v", calls.Load(), sawBearer.Load())
	}
	if transport.PinnedOrigin() != "https://open.tiktokapis.com" {
		t.Fatalf("pinned origin=%q", transport.PinnedOrigin())
	}
}

type recordingAuthenticatedExecutor struct {
	inner *socialconnections.AuthenticatedExecutor
	err   error
}

func (executor *recordingAuthenticatedExecutor) Execute(
	ctx context.Context,
	request socialconnections.PublishingRequest,
) (socialconnections.PublishingResponse, error) {
	response, err := executor.inner.Execute(ctx, request)
	executor.err = err
	return response, err
}

func connectedTikTokExecutor(
	t *testing.T,
	transport *tlsPinnedTransport,
) (*socialconnections.AuthenticatedExecutor, socialconnections.Connection) {
	t.Helper()
	repository := socialconnections.NewMemoryRepository()
	cipher, err := socialconnections.NewAESGCMCipher(
		"fixture-key", bytesOf(32, 7),
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := tlsTikTokConnectionAdapter{}
	service, err := socialconnections.NewService(socialconnections.Config{
		Repository: repository,
		Authorizer: allowSocialAuthorizer{},
		Cipher:     cipher,
		Quota:      allowChannelQuota{},
		Adapters: map[socialconnections.Provider]socialconnections.Adapter{
			socialconnections.ProviderTikTok: provider,
		},
		Availability: map[socialconnections.Provider]socialconnections.ProviderAvailability{
			socialconnections.ProviderTikTok: {
				Provider:           socialconnections.ProviderTikTok,
				Status:             socialconnections.ProviderAvailable,
				ConfigurationState: socialconnections.ProviderReady,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := service.Begin(context.Background(), socialconnections.BeginRequest{
		WorkspaceID: "workspace-1", ActorID: "actor-1",
		Provider: socialconnections.ProviderTikTok,
	})
	if err != nil {
		t.Fatal(err)
	}
	authorizationURL, err := url.Parse(authorization.URL)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := service.Callback(
		context.Background(),
		socialconnections.CallbackRequest{
			State: authorizationURL.Query().Get("state"), Code: "fixture-code",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := service.Select(
		context.Background(),
		socialconnections.SelectRequest{
			WorkspaceID: "workspace-1", ActorID: "actor-1",
			SelectionID: selection.ID, RemoteID: "creator-1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := socialconnections.NewAuthenticatedExecutor(
		socialconnections.AuthenticatedExecutorConfig{
			Service: service, Transport: transport,
			ResourceServers: map[socialconnections.Provider]string{
				socialconnections.ProviderTikTok: transport.origin,
			},
			Classifiers: map[socialconnections.Provider]socialconnections.ProviderResponseClassifier{
				socialconnections.ProviderTikTok: TikTokResponseClassifier{},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return executor, connection
}

type tlsPinnedTransport struct {
	mu     sync.Mutex
	origin string
	pinned string
	target string
	base   http.RoundTripper
}

func (transport *tlsPinnedTransport) PinOrigin(
	_ context.Context,
	origin string,
) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.pinned = origin
	return nil
}

func (transport *tlsPinnedTransport) PinnedOrigin() string {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.pinned
}

func (transport *tlsPinnedTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	target, _ := url.Parse(transport.target)
	clone := request.Clone(request.Context())
	clone.URL.Scheme = target.Scheme
	clone.URL.Host = target.Host
	return transport.base.RoundTrip(clone)
}

type tlsTikTokConnectionAdapter struct{}

func (tlsTikTokConnectionAdapter) Config() socialconnections.OAuthConfig {
	return socialconnections.OAuthConfig{
		ClientID:         "fixture-client",
		AuthorizationURL: "https://www.tiktok.com/v2/auth/authorize/",
		RedirectURL:      "https://app.example/oauth/tiktok",
		Scopes:           []string{"video.publish"},
		ScopeSeparator:   socialconnections.OAuthScopeSeparatorComma,
	}
}

func (tlsTikTokConnectionAdapter) Exchange(
	context.Context,
	socialconnections.ExchangeRequest,
) (socialconnections.Credential, error) {
	return socialconnections.Credential{
		AccessToken: "fixture-access-token", Scopes: []string{"video.publish"},
	}, nil
}

func (tlsTikTokConnectionAdapter) Discover(
	_ context.Context,
	credential socialconnections.Credential,
) ([]socialconnections.DiscoveredResource, error) {
	return []socialconnections.DiscoveredResource{{
		Candidate: socialconnections.Candidate{
			RemoteID:     "creator-1",
			ResourceType: socialconnections.ResourceTikTokProfile,
			AccountType:  socialconnections.AccountTypeProfile,
			DisplayName:  "TLS Creator",
			Scopes:       []string{"video.publish"},
		},
		Credential: credential,
	}}, nil
}

func (tlsTikTokConnectionAdapter) Refresh(
	_ context.Context,
	credential socialconnections.Credential,
) (socialconnections.Credential, error) {
	return credential, nil
}

func (tlsTikTokConnectionAdapter) Verify(
	context.Context,
	string,
	socialconnections.Credential,
) error {
	return nil
}

func (tlsTikTokConnectionAdapter) Revoke(
	context.Context,
	string,
	socialconnections.Credential,
) error {
	return nil
}

type allowSocialAuthorizer struct{}

func (allowSocialAuthorizer) Authorize(
	context.Context,
	string,
	string,
	socialconnections.Permission,
) error {
	return nil
}

type allowChannelQuota struct{}

func (allowChannelQuota) ReserveChannel(
	context.Context,
	string,
	string,
) (socialconnections.ChannelQuotaDecision, error) {
	return socialconnections.ChannelQuotaDecision{Accepted: true}, nil
}

func (allowChannelQuota) ReleaseChannel(
	context.Context,
	string,
	string,
) (socialconnections.ChannelQuotaDecision, error) {
	return socialconnections.ChannelQuotaDecision{Accepted: true}, nil
}

func bytesOf(size int, value byte) []byte {
	result := make([]byte, size)
	for index := range result {
		result[index] = value
	}
	return result
}
