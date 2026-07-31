package publishingruntime

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"os"
	"strings"
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

	database, err := sql.Open(
		"pgx",
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	config, err := NewMetaRegistrationConfig(database, time.Now)
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

func TestProductionMetaBootstrapGatesMissingDependenciesAndIssue343(
	t *testing.T,
) {
	clearMetaBootstrapEnvironment(t)
	database, err := sql.Open(
		"pgx",
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	t.Setenv("POSTQRON_F08_META_AUTO_ENABLED", "true")
	if _, err = NewMetaRegistrationConfig(database, time.Now); err == nil ||
		!strings.Contains(err.Error(), "F5 credential and Meta gates") {
		t.Fatalf("missing F5 gate error=%v", err)
	}

	clearMetaBootstrapEnvironment(t)
	t.Setenv("POSTQRON_F08_META_NOTIFICATIONS_ENABLED", "true")
	if _, err = NewMetaRegistrationConfig(database, time.Now); err == nil ||
		!strings.Contains(err.Error(), "email boundary is unavailable") {
		t.Fatalf("notification gate error=%v", err)
	}
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

func TestProductionMetaBootstrapRejectsThreadsUntilPR316(t *testing.T) {
	clearMetaBootstrapEnvironment(t)
	t.Setenv("POSTQRON_F08_META_AUTO_ENABLED", "true")
	t.Setenv("POSTQRON_F08_THREADS_ENABLED", "true")
	database, err := sql.Open(
		"pgx",
		"postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	config, err := NewMetaRegistrationConfig(database, time.Now)
	if err == nil || !strings.Contains(err.Error(), "PR #316") {
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
	} {
		t.Setenv(key, "")
	}
}
