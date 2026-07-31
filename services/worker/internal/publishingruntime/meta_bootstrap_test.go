package publishingruntime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
	metapublishing "github.com/apdsoftware/postqron/features/f08-publishing/providers/meta"
	staticproviders "github.com/apdsoftware/postqron/features/f08-publishing/providers/static"
	statusnotifications "github.com/apdsoftware/postqron/features/f09-status-notifications"
	"github.com/apdsoftware/postqron/services/worker/internal/emailruntime"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type allowMetaChannelQuota struct{}

func (allowMetaChannelQuota) ReserveChannel(
	context.Context,
	string,
	string,
) (socialconnections.ChannelQuotaDecision, error) {
	return socialconnections.ChannelQuotaDecision{Accepted: true}, nil
}

func (allowMetaChannelQuota) ReleaseChannel(
	context.Context,
	string,
	string,
) (socialconnections.ChannelQuotaDecision, error) {
	return socialconnections.ChannelQuotaDecision{Accepted: true}, nil
}

type recordingMetaTransport struct {
	mu       sync.Mutex
	pinned   []string
	requests []http.Request
	bodies   []string
}

func (transport *recordingMetaTransport) PinOrigin(
	_ context.Context,
	origin string,
) error {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.pinned = append(transport.pinned, origin)
	return nil
}

func (transport *recordingMetaTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	body := ""
	if request.Body != nil {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		body = string(payload)
	}
	clone := request.Clone(request.Context())
	clone.Body = nil
	transport.mu.Lock()
	transport.requests = append(transport.requests, *clone)
	transport.bodies = append(transport.bodies, body)
	transport.mu.Unlock()

	responseBody := `{"id":"thread-1","permalink":"https://threads.example/t/thread-1"}`
	switch request.URL.RequestURI() {
	case "/me/threads":
		responseBody = `{"id":"threads-container-1"}`
	case "/threads-container-1?fields=id,status":
		polls := transport.countPath("/threads-container-1?fields=id,status")
		if polls == 1 {
			responseBody = `{"id":"threads-container-1","status":"IN_PROGRESS"}`
		} else {
			responseBody = `{"id":"threads-container-1","status":"FINISHED"}`
		}
	case "/me/threads_publish":
		responseBody = `{"id":"thread-1"}`
	case "/thread-1?fields=id,permalink,permalink_url":
	default:
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
			Request:    request,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body:    io.NopCloser(strings.NewReader(responseBody)),
		Request: request,
	}, nil
}

func (transport *recordingMetaTransport) countPath(path string) int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	count := 0
	for _, request := range transport.requests {
		if request.URL.RequestURI() == path {
			count++
		}
	}
	return count
}

func withMetaBootstrapF5Fixtures(
	t *testing.T,
	transport metaPinnedTransport,
) *socialconnections.MemoryRepository {
	t.Helper()
	repository := socialconnections.NewMemoryRepository()
	t.Cleanup(func() {
		newMetaRepository = func(
			database *sql.DB,
		) (socialconnections.Repository, error) {
			return socialconnections.NewPostgresRepository(database)
		}
		newMetaQuota = func(
			database *sql.DB,
			clock func() time.Time,
		) (socialconnections.ChannelQuota, error) {
			return socialconnections.NewPostgresChannelQuota(database, clock)
		}
		newMetaTransport = func() metaPinnedTransport {
			return newPinnedMetaTransport()
		}
		newMetaAuthenticatedExecutor = socialconnections.NewAuthenticatedExecutor
	})
	newMetaRepository = func(
		*sql.DB,
	) (socialconnections.Repository, error) {
		return repository, nil
	}
	newMetaQuota = func(
		*sql.DB,
		func() time.Time,
	) (socialconnections.ChannelQuota, error) {
		return allowMetaChannelQuota{}, nil
	}
	newMetaTransport = func() metaPinnedTransport {
		return transport
	}
	return repository
}

func connectThreadsFixture(
	t *testing.T,
	repository socialconnections.Repository,
	keyID string,
	key []byte,
) (workspaceID, connectionID string) {
	t.Helper()
	cipher, err := socialconnections.NewAESGCMCipher(keyID, key)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	workspaceID = "workspace-threads-bootstrap"
	connectionID = "connection-threads-bootstrap"
	remoteID := "123456789"
	selectionID := "selection-threads-bootstrap"
	credentialAAD := []byte(
		"f05|credential|" + workspaceID + "|" +
			string(socialconnections.ProviderThreads) + "|" + remoteID,
	)
	access, err := cipher.Seal([]byte("threads-access-token"), credentialAAD)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := cipher.Seal([]byte("threads-refresh-token"), credentialAAD)
	if err != nil {
		t.Fatal(err)
	}
	err = repository.SaveSelection(context.Background(), socialconnections.StoredSelection{
		ID:          selectionID,
		WorkspaceID: workspaceID,
		ActorID:     "owner-threads-bootstrap",
		Provider:    socialconnections.ProviderThreads,
		Resources: []socialconnections.StoredResource{{
			Candidate: socialconnections.Candidate{
				RemoteID:     remoteID,
				ResourceType: socialconnections.ResourceThreadsProfile,
				AccountType:  socialconnections.AccountTypeProfile,
				DisplayName:  "Threads Bootstrap",
				Handle:       "postqron",
				Scopes: []string{
					"threads_basic",
					"threads_content_publish",
				},
			},
			AccessTokenCiphertext:  access,
			RefreshTokenCiphertext: refresh,
			Binding: socialconnections.OAuthBinding{
				ResourceServer: metaThreadsOrigin,
				Subject:        remoteID,
			},
			RefreshTokenMode: socialconnections.RefreshTokenReusable,
			TokenExpiresAt:   func() *time.Time { expiry := now.Add(time.Hour); return &expiry }(),
		}},
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repository.Connect(context.Background(), socialconnections.ConnectCommand{
		NewConnectionID: connectionID,
		WorkspaceID:     workspaceID,
		ActorID:         "owner-threads-bootstrap",
		SelectionID:     selectionID,
		RemoteID:        remoteID,
		Now:             now,
		Event: socialconnections.Event{
			ID:            "event-threads-bootstrap",
			Type:          socialconnections.EventConnected,
			Version:       1,
			WorkspaceID:   workspaceID,
			ConnectionID:  connectionID,
			Provider:      socialconnections.ProviderThreads,
			RemoteID:      remoteID,
			CorrelationID: "corr-threads-bootstrap",
			OccurredAt:    now,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return workspaceID, connectionID
}

type socialEmailGatewayStub struct {
	receipt statusnotifications.SocialDeliveryReceipt
}

func (gateway socialEmailGatewayStub) DeliverSocialNotification(
	context.Context,
	statusnotifications.SocialNotificationCommand,
) (statusnotifications.SocialDeliveryReceipt, error) {
	return gateway.receipt, nil
}

func TestProductionMetaBootstrapRegistersReviewedFacebookAndInstagram(t *testing.T) {
	clearMetaBootstrapEnvironment(t)
	withMetaBootstrapF5Fixtures(t, &recordingMetaTransport{})
	t.Setenv("POSTQRON_F08_META_AUTO_ENABLED", "true")
	t.Setenv("POSTQRON_F08_FACEBOOK_PAGES_ENABLED", "true")
	t.Setenv("POSTQRON_F08_INSTAGRAM_PROFESSIONAL_ENABLED", "true")
	t.Setenv("POSTQRON_F05_ENABLED", "true")
	t.Setenv("POSTQRON_F05_META_ENABLED", "true")
	t.Setenv("POSTQRON_F05_META_GRAPH_VERSION", "v25.0")
	t.Setenv("POSTQRON_F05_CIPHER_KEY_ID", "worker-meta-test")
	t.Setenv(
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		base64.StdEncoding.EncodeToString(
			[]byte("0123456789abcdef0123456789abcdef"),
		),
	)
	t.Setenv("POSTQRON_F05_FACEBOOK_CLIENT_ID", "facebook-client")
	t.Setenv("POSTQRON_F05_FACEBOOK_CLIENT_SECRET", "facebook-secret")
	t.Setenv(
		"POSTQRON_F05_FACEBOOK_REDIRECT_URL",
		"https://api.example.test/api/v1/social-authorizations/callback",
	)
	t.Setenv("POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID", "login-config")
	t.Setenv("POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED", "true")
	t.Setenv("POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED", "true")
	t.Setenv("POSTQRON_F05_INSTAGRAM_CLIENT_ID", "instagram-client")
	t.Setenv("POSTQRON_F05_INSTAGRAM_CLIENT_SECRET", "instagram-secret")
	t.Setenv(
		"POSTQRON_F05_INSTAGRAM_REDIRECT_URL",
		"https://api.example.test/api/v1/social-authorizations/callback",
	)
	t.Setenv("POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED", "true")
	t.Setenv("POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED", "true")

	config, err := NewMetaRegistrationConfig(nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if config.Executor == nil || config.GraphVersion != "v25.0" ||
		len(config.AutoProviders) != 2 ||
		config.AutoProviders[0] != socialconnections.ProviderFacebookPages ||
		config.AutoProviders[1] !=
			socialconnections.ProviderInstagramProfessional {
		t.Fatalf("config=%+v", config)
	}
	registry, err := newRuntimeAdapterRegistryWithMeta(
		nil,
		staticproviders.Config{},
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := registry.ResolvePublisher(
		context.Background(),
		string(socialconnections.ProviderFacebookPages),
	)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := publisher.Capabilities()
	if capabilities.Reconciliation || !capabilities.AmbiguousFailClosed ||
		capabilities.MediaFormats == "" {
		t.Fatalf("capabilities=%+v", capabilities)
	}
	if _, err = registry.ResolvePublisher(
		context.Background(),
		string(socialconnections.ProviderInstagramProfessional),
	); err != nil {
		t.Fatalf("Instagram resolution=%v", err)
	}
	if _, err = registry.ResolvePublisher(
		context.Background(),
		string(socialconnections.ProviderThreads),
	); !errors.Is(err, publishing.ErrProviderUnavailable) {
		t.Fatalf("Threads must remain unavailable, resolution=%v", err)
	}
}

func TestProductionMetaBootstrapRegistersReviewedThreads(t *testing.T) {
	clearMetaBootstrapEnvironment(t)
	transport := &recordingMetaTransport{}
	repository := withMetaBootstrapF5Fixtures(t, transport)
	t.Setenv("POSTQRON_F08_META_AUTO_ENABLED", "true")
	t.Setenv("POSTQRON_F08_THREADS_ENABLED", "true")
	t.Setenv("POSTQRON_F05_ENABLED", "true")
	t.Setenv("POSTQRON_F05_META_ENABLED", "true")
	t.Setenv("POSTQRON_F05_META_GRAPH_VERSION", "v25.0")
	t.Setenv("POSTQRON_F05_CIPHER_KEY_ID", "worker-meta-test")
	t.Setenv(
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		base64.StdEncoding.EncodeToString(
			[]byte("0123456789abcdef0123456789abcdef"),
		),
	)
	t.Setenv("POSTQRON_F05_THREADS_ENABLED", "true")
	t.Setenv("POSTQRON_F05_THREADS_CLIENT_ID", "threads-client")
	t.Setenv("POSTQRON_F05_THREADS_CLIENT_SECRET", "threads-secret")
	t.Setenv(
		"POSTQRON_F05_THREADS_REDIRECT_URL",
		"https://api.example.test/api/v1/social-authorizations/callback",
	)
	t.Setenv("POSTQRON_F05_THREADS_APP_REVIEW_APPROVED", "true")
	t.Setenv("POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED", "true")

	key := []byte("0123456789abcdef0123456789abcdef")
	workspaceID, connectionID := connectThreadsFixture(
		t,
		repository,
		"worker-meta-test",
		key,
	)
	config, err := NewMetaRegistrationConfig(nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if config.Executor == nil || config.GraphVersion != "v25.0" ||
		config.ThreadsGraphVersion != "" ||
		len(config.AutoProviders) != 1 ||
		config.AutoProviders[0] != socialconnections.ProviderThreads {
		t.Fatalf("config=%+v", config)
	}
	registry, err := newRuntimeAdapterRegistryWithMeta(
		nil,
		staticproviders.Config{},
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.ResolvePublisher(
		context.Background(),
		string(socialconnections.ProviderThreads),
	); err != nil {
		t.Fatalf("Threads resolution=%v", err)
	}
	publisher, err := registry.ResolvePublisher(
		context.Background(),
		string(socialconnections.ProviderThreads),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := publishing.PublishRequest{
		WorkspaceID:  workspaceID,
		ConnectionID: connectionID,
		Payload:      []byte(`{"format":"text","text":"hello Threads"}`),
		IdempotencyKey: "threads-bootstrap-idempotency",
	}
	first, err := publisher.Publish(context.Background(), request)
	if err != nil || first.Complete {
		t.Fatalf("create result=%+v err=%v", first, err)
	}
	request.Checkpoint = first.Checkpoint
	waiting, err := publisher.Publish(context.Background(), request)
	if err != nil || waiting.Complete || waiting.RetryAfter != 5*time.Second {
		t.Fatalf("waiting result=%+v err=%v", waiting, err)
	}
	request.Checkpoint = waiting.Checkpoint
	ready, err := publisher.Publish(context.Background(), request)
	if err != nil || ready.Complete {
		t.Fatalf("ready result=%+v err=%v", ready, err)
	}
	request.Checkpoint = ready.Checkpoint
	published, err := publisher.Publish(context.Background(), request)
	if err != nil || published.Complete {
		t.Fatalf("publish result=%+v err=%v", published, err)
	}
	request.Checkpoint = published.Checkpoint
	final, err := publisher.Publish(context.Background(), request)
	if err != nil || !final.Complete || final.RemoteID != "thread-1" {
		t.Fatalf("final result=%+v err=%v", final, err)
	}
	expectedPaths := []string{
		"/me/threads",
		"/threads-container-1?fields=id,status",
		"/threads-container-1?fields=id,status",
		"/me/threads_publish",
		"/thread-1?fields=id,permalink,permalink_url",
	}
	if len(transport.requests) != len(expectedPaths) {
		t.Fatalf("requests=%d", len(transport.requests))
	}
	for index, expectedPath := range expectedPaths {
		request := transport.requests[index]
		if request.Method != []string{
			http.MethodPost,
			http.MethodGet,
			http.MethodGet,
			http.MethodPost,
			http.MethodGet,
		}[index] {
			t.Fatalf("request %d method=%q", index, request.Method)
		}
		if request.URL.Host != "graph.threads.net" ||
			request.URL.RequestURI() != expectedPath {
			t.Fatalf(
				"request %d host/path=%s%s",
				index,
				request.URL.Host,
				request.URL.RequestURI(),
			)
		}
	}
	if len(transport.pinned) == 0 || transport.pinned[0] != metaThreadsOrigin {
		t.Fatalf("pinned origins=%v", transport.pinned)
	}
	if transport.bodies[3] != "creation_id=threads-container-1" {
		t.Fatalf("threads publish body=%q", transport.bodies[3])
	}
	if transport.bodies[0] == transport.bodies[3] {
		t.Fatalf("request bodies replayed unexpectedly: %q", transport.bodies[0])
	}
}

func TestProductionMetaBootstrapGatesMissingDependenciesAndIssue343(
	t *testing.T,
) {
	clearMetaBootstrapEnvironment(t)
	withMetaBootstrapF5Fixtures(t, &recordingMetaTransport{})

	t.Setenv("POSTQRON_F08_META_AUTO_ENABLED", "true")
	if _, err := NewMetaRegistrationConfig(nil, time.Now); err == nil ||
		!strings.Contains(err.Error(), "F5 credential and Meta gates") {
		t.Fatalf("missing F5 gate error=%v", err)
	}

	clearMetaBootstrapEnvironment(t)
	t.Setenv("POSTQRON_F08_META_NOTIFICATIONS_ENABLED", "true")
	if _, err := NewMetaRegistrationConfig(nil, time.Now); err == nil ||
		!strings.Contains(err.Error(), "email boundary is unavailable") {
		t.Fatalf("notification gate error=%v", err)
	}
	database, err := sql.Open(
		"pgx",
		"postgres://meta:meta@localhost/postqron?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	config, err := NewMetaRegistrationConfig(
		database,
		time.Now,
		&emailruntime.Service{},
	)
	if err != nil {
		t.Fatalf("configured notification boundary error=%v", err)
	}
	if config.NotificationStore == nil || config.NotificationSender == nil {
		t.Fatalf("notification boundary was not registered: %+v", config)
	}
}

func TestMetaNotificationSenderDoesNotTreatQueuedEmailAsDelivered(t *testing.T) {
	boundary, err := statusnotifications.NewSocialNotificationBoundary(
		socialEmailGatewayStub{
			receipt: statusnotifications.SocialDeliveryReceipt{
				EmailDeliveryID: "email-queued",
				State:           statusnotifications.SocialDeliveryPending,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	emailID, err := (runtimeSocialNotificationSender{boundary: boundary}).
		DeliverMetaNotification(
			context.Background(),
			metapublishing.NotificationDelivery{
				WorkspaceID:    "workspace-1",
				PostID:         "post-1",
				ChannelID:      "channel-1",
				Provider:       "facebook_groups",
				RecipientID:    "account-1",
				Locale:         "en",
				TemplateID:     "facebook_group_manual_publish",
				IdempotencyKey: "notification-1",
			},
		)
	var providerError *publishing.ProviderError
	if emailID != "email-queued" || !errors.As(err, &providerError) ||
		!providerError.Retryable ||
		providerError.Code != "notification_email_pending" {
		t.Fatalf(
			"queued receipt email=%q error=%#v",
			emailID,
			err,
		)
	}
}

func TestNotificationAuditPurgesWhenDeliveryGateIsDisabled(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not configured")
	}
	clearMetaBootstrapEnvironment(t)
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	config, err := NewMetaRegistrationConfig(database, clock)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewWithExecutorAndMeta(
		context.Background(),
		database,
		databaseURL,
		clock,
		nil,
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	if service.notificationDispatcher != nil {
		t.Fatal("notification sender registered while gate is disabled")
	}
	expiredOutboxID := "meta_notification_cccccccccccccccccccccccccccccccc"
	expiredTombstoneID := "meta_notification_dddddddddddddddddddddddddddddddd"
	_, err = database.Exec(`
		INSERT INTO f08_meta_notification_outbox (
			id, provider, workspace_id, post_id, channel_id, recipient_id,
			locale, template_id, idempotency_key, payload_fingerprint, state,
			attempt_count, next_attempt_at, permanent_failed_at,
			retention_until, created_at
		) VALUES (
			$1, 'facebook_groups', 'workspace-cleanup', 'post-cleanup',
			'channel-cleanup', 'account-cleanup', 'en',
			'facebook_group_manual_publish', 'cleanup-disabled',
			$2, 'permanent_failure', 1, $3, $3, $4, $3
		)`,
		expiredOutboxID,
		strings.Repeat("c", 64),
		now.AddDate(-1, 0, 0),
		now.Add(-time.Second),
	)
	if err == nil {
		_, err = database.Exec(`
		INSERT INTO f08_meta_notification_tombstones (
			id, provider, payload_fingerprint, outcome, expires_at
		) VALUES (
			$1, 'instagram_personal', $2, 'permanent_failure', $3
		)`,
			expiredTombstoneID,
			strings.Repeat("d", 64),
			now.Add(-time.Second),
		)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(
			`DELETE FROM f08_meta_notification_outbox WHERE id = $1`,
			expiredOutboxID,
		)
		_, _ = database.Exec(
			`DELETE FROM f08_meta_notification_tombstones
			  WHERE id IN ($1, $2)`,
			expiredOutboxID,
			expiredTombstoneID,
		)
	})
	processed, err := service.DispatchOne(context.Background())
	if err != nil || processed {
		t.Fatalf("cleanup tick processed=%v error=%v", processed, err)
	}
	var (
		outboxRows       int
		expiredTombstone int
		activeTombstone  int
	)
	err = database.QueryRow(`
		SELECT
		    (SELECT count(*) FROM f08_meta_notification_outbox
		      WHERE id = $1),
		    (SELECT count(*) FROM f08_meta_notification_tombstones
		      WHERE id = $2),
		    (SELECT count(*) FROM f08_meta_notification_tombstones
		      WHERE id = $1 AND expires_at > $3)`,
		expiredOutboxID,
		expiredTombstoneID,
		now,
	).Scan(&outboxRows, &expiredTombstone, &activeTombstone)
	if err != nil || outboxRows != 0 || expiredTombstone != 0 ||
		activeTombstone != 1 {
		t.Fatalf(
			"cleanup outbox=%d expired_tombstone=%d active_tombstone=%d error=%v",
			outboxRows,
			expiredTombstone,
			activeTombstone,
			err,
		)
	}
}

func TestProductionMetaBootstrapRejectsThreadsWithoutVerifiedF5Adapter(t *testing.T) {
	clearMetaBootstrapEnvironment(t)
	withMetaBootstrapF5Fixtures(t, &recordingMetaTransport{})
	t.Setenv("POSTQRON_F08_META_AUTO_ENABLED", "true")
	t.Setenv("POSTQRON_F08_THREADS_ENABLED", "true")
	t.Setenv("POSTQRON_F05_ENABLED", "true")
	t.Setenv("POSTQRON_F05_META_ENABLED", "true")
	t.Setenv("POSTQRON_F05_META_GRAPH_VERSION", "v25.0")
	t.Setenv("POSTQRON_F05_CIPHER_KEY_ID", "worker-meta-test")
	t.Setenv(
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		base64.StdEncoding.EncodeToString(
			[]byte("0123456789abcdef0123456789abcdef"),
		),
	)
	config, err := NewMetaRegistrationConfig(nil, time.Now)
	if err == nil || !strings.Contains(
		err.Error(),
		"verified F5 adapter configuration",
	) {
		t.Fatalf("Threads dependency config=%+v error=%v", config, err)
	}
	if config.Executor != nil || len(config.AutoProviders) != 0 {
		t.Fatalf("Threads was registered despite missing F5 dependency: %+v", config)
	}
}

func clearMetaBootstrapEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"POSTQRON_F08_META_AUTO_ENABLED",
		"POSTQRON_F08_FACEBOOK_PAGES_ENABLED",
		"POSTQRON_F08_INSTAGRAM_PROFESSIONAL_ENABLED",
		"POSTQRON_F08_THREADS_ENABLED",
		"POSTQRON_F08_META_NOTIFICATIONS_ENABLED",
		"POSTQRON_F05_ENABLED",
		"POSTQRON_F05_META_ENABLED",
		"POSTQRON_F05_META_GRAPH_VERSION",
		"POSTQRON_F05_CIPHER_KEY_ID",
		"POSTQRON_F05_CIPHER_KEY_BASE64",
		"POSTQRON_F05_FACEBOOK_CLIENT_ID",
		"POSTQRON_F05_FACEBOOK_CLIENT_SECRET",
		"POSTQRON_F05_FACEBOOK_REDIRECT_URL",
		"POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID",
		"POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED",
		"POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED",
		"POSTQRON_F05_INSTAGRAM_CLIENT_ID",
		"POSTQRON_F05_INSTAGRAM_CLIENT_SECRET",
		"POSTQRON_F05_INSTAGRAM_REDIRECT_URL",
		"POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED",
		"POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED",
		"POSTQRON_F05_THREADS_ENABLED",
		"POSTQRON_F05_THREADS_CLIENT_ID",
		"POSTQRON_F05_THREADS_CLIENT_SECRET",
		"POSTQRON_F05_THREADS_REDIRECT_URL",
		"POSTQRON_F05_THREADS_APP_REVIEW_APPROVED",
		"POSTQRON_F05_THREADS_RUNTIME_AUDIT_VERIFIED",
	} {
		t.Setenv(key, "")
	}
}
