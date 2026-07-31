package publishingruntime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	publishing "github.com/apdsoftware/postqron/features/f08-publishing"
	dynamicpublishing "github.com/apdsoftware/postqron/features/f08-publishing/providers/dynamic"
	metapublishing "github.com/apdsoftware/postqron/features/f08-publishing/providers/meta"
	staticproviders "github.com/apdsoftware/postqron/features/f08-publishing/providers/static"
	videopublishing "github.com/apdsoftware/postqron/features/f08-publishing/providers/video"
	statusnotifications "github.com/apdsoftware/postqron/features/f09-status-notifications"
	"github.com/apdsoftware/postqron/services/worker/internal/emailruntime"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service owns only F8 wiring. Credentials remain behind the injected F5
// AuthenticatedExecutor boundary.
type Service struct {
	engine                 *publishing.Engine
	notificationDispatcher *metapublishing.NotificationDispatcher
	notificationAuditStore *metapublishing.PostgresNotificationStore
	clock                  func() time.Time
	pool                   *pgxpool.Pool
}

type ProviderGate struct {
	Configured     bool
	ReviewApproved bool
	AuditVerified  bool
	QuotaVerified  bool
}

func (gate ProviderGate) ready() bool {
	return gate.Configured && gate.ReviewApproved &&
		gate.AuditVerified && gate.QuotaVerified
}

type VideoAdapterDependencies struct {
	Executor                 *socialconnections.AuthenticatedExecutor
	Media                    videopublishing.MediaSource
	TikTokVerifiedPullPrefix string
	F5TrailingSlashPaths     bool
	TikTok                   ProviderGate
	YouTube                  ProviderGate
}

// DynamicAdapterDependencies are process-local trusted dependencies. The
// executor owns credentials, origins, dynamic discovery, PAR, DPoP keys and
// nonce rotation; F8 receives none of them.
type DynamicAdapterDependencies struct {
	Executor *socialconnections.AuthenticatedExecutor
	Media    dynamicpublishing.MediaSource
	Identity dynamicpublishing.ConnectionIdentityResolver
	Mastodon ProviderGate
	Bluesky  ProviderGate
}

func New(
	ctx context.Context,
	database *sql.DB,
	databaseURL string,
	clock func() time.Time,
	videoDependencies ...VideoAdapterDependencies,
) (*Service, error) {
	config := runtimeStaticProviderConfig(database)
	return newService(
		ctx, database, databaseURL, clock, nil, config,
		metapublishing.RegistrationConfig{},
		DynamicAdapterDependencies{},
		videoDependencies...,
	)
}

// NewWithExecutor is the credential-free F5→F8 worker composition boundary.
// A nil executor is valid and preserves the default fail-closed registry.
func NewWithExecutor(
	ctx context.Context,
	database *sql.DB,
	databaseURL string,
	clock func() time.Time,
	executor *socialconnections.AuthenticatedExecutor,
	videoDependencies ...VideoAdapterDependencies,
) (*Service, error) {
	metaConfig, err := NewMetaRegistrationConfig(database, clock)
	if err != nil {
		return nil, err
	}
	return NewWithExecutorAndMeta(
		ctx,
		database,
		databaseURL,
		clock,
		executor,
		metaConfig,
		videoDependencies...,
	)
}

func NewWithExecutorAndMeta(
	ctx context.Context,
	database *sql.DB,
	databaseURL string,
	clock func() time.Time,
	executor *socialconnections.AuthenticatedExecutor,
	metaConfig metapublishing.RegistrationConfig,
	videoDependencies ...VideoAdapterDependencies,
) (*Service, error) {
	return NewWithAllAdapters(
		ctx,
		database,
		databaseURL,
		clock,
		executor,
		metaConfig,
		DynamicAdapterDependencies{},
		videoDependencies...,
	)
}

func NewWithAllAdapters(
	ctx context.Context,
	database *sql.DB,
	databaseURL string,
	clock func() time.Time,
	executor *socialconnections.AuthenticatedExecutor,
	metaConfig metapublishing.RegistrationConfig,
	dynamicDependencies DynamicAdapterDependencies,
	videoDependencies ...VideoAdapterDependencies,
) (*Service, error) {
	var boundary staticproviders.Executor
	if executor != nil {
		boundary = executor
	}
	if len(videoDependencies) == 1 && videoDependencies[0].Executor == nil {
		videoDependencies[0].Executor = executor
	}
	return newService(
		ctx,
		database,
		databaseURL,
		clock,
		boundary,
		runtimeStaticProviderConfig(database),
		metaConfig,
		dynamicDependencies,
		videoDependencies...,
	)
}

func NewMetaRegistrationConfig(
	database *sql.DB,
	clock func() time.Time,
	emailServices ...*emailruntime.Service,
) (metapublishing.RegistrationConfig, error) {
	config, err := productionMetaAutoConfig(database, clock)
	if err != nil {
		return metapublishing.RegistrationConfig{}, err
	}
	if strings.TrimSpace(
		os.Getenv("POSTQRON_F08_META_NOTIFICATIONS_ENABLED"),
	) != "true" {
		return config, nil
	}
	if len(emailServices) != 1 || emailServices[0] == nil {
		return metapublishing.RegistrationConfig{}, fmt.Errorf(
			"%w: social notification email boundary is unavailable",
			publishing.ErrProviderUnavailable,
		)
	}
	store, err := metapublishing.NewPostgresNotificationStore(database, clock)
	if err != nil {
		return metapublishing.RegistrationConfig{}, err
	}
	boundary, err := statusnotifications.NewSocialNotificationBoundary(
		runtimeSocialEmailGateway{email: emailServices[0]},
	)
	if err != nil {
		return metapublishing.RegistrationConfig{}, err
	}
	config.NotificationStore = store
	config.NotificationSender = runtimeSocialNotificationSender{
		boundary: boundary,
	}
	return config, nil
}

type runtimeSocialNotificationSender struct {
	boundary *statusnotifications.SocialNotificationBoundary
}

type runtimeSocialEmailGateway struct {
	email *emailruntime.Service
}

func (gateway runtimeSocialEmailGateway) DeliverSocialNotification(
	ctx context.Context,
	command statusnotifications.SocialNotificationCommand,
) (statusnotifications.SocialDeliveryReceipt, error) {
	emailID, state, err := gateway.email.EnqueueSocialNotification(
		ctx,
		emailruntime.SocialNotificationCommand{
			WorkspaceID:    command.WorkspaceID,
			PostID:         command.PostID,
			ChannelID:      command.ChannelID,
			Provider:       command.Provider,
			RecipientID:    command.RecipientID,
			Locale:         command.Locale,
			TemplateID:     command.TemplateID,
			IdempotencyKey: command.IdempotencyKey,
		},
	)
	if err != nil {
		return statusnotifications.SocialDeliveryReceipt{}, err
	}
	return statusnotifications.SocialDeliveryReceipt{
		EmailDeliveryID: emailID,
		State:           statusnotifications.SocialDeliveryState(state),
	}, nil
}

func (sender runtimeSocialNotificationSender) DeliverMetaNotification(
	ctx context.Context,
	delivery metapublishing.NotificationDelivery,
) (string, error) {
	if sender.boundary == nil {
		return "", &publishing.ProviderError{
			Code:   "notification_boundary_unavailable",
			Detail: "social notification F9 boundary is unavailable",
		}
	}
	receipt, err := sender.boundary.Deliver(
		ctx,
		statusnotifications.SocialNotificationCommand{
			WorkspaceID:    delivery.WorkspaceID,
			PostID:         delivery.PostID,
			ChannelID:      delivery.ChannelID,
			Provider:       delivery.Provider,
			RecipientID:    delivery.RecipientID,
			Locale:         delivery.Locale,
			TemplateID:     delivery.TemplateID,
			IdempotencyKey: delivery.IdempotencyKey,
		},
	)
	if err != nil {
		return "", &publishing.ProviderError{
			Code:      "notification_email_enqueue_failed",
			Detail:    "social notification F9/F14 delivery failed",
			Retryable: true,
		}
	}
	switch receipt.State {
	case statusnotifications.SocialDeliveryDelivered:
		return receipt.EmailDeliveryID, nil
	case statusnotifications.SocialDeliveryPermanentFailure:
		return receipt.EmailDeliveryID, &publishing.ProviderError{
			Code:   "notification_not_delivered",
			Detail: "social notification delivery could not be confirmed",
		}
	default:
		return receipt.EmailDeliveryID, &publishing.ProviderError{
			Code:       "notification_email_pending",
			Detail:     "social notification email is awaiting confirmed delivery",
			Retryable:  true,
			RetryAfter: 5 * time.Second,
		}
	}
}

func newService(
	ctx context.Context,
	database *sql.DB,
	databaseURL string,
	clock func() time.Time,
	executor staticproviders.Executor,
	staticConfig staticproviders.Config,
	metaConfig metapublishing.RegistrationConfig,
	dynamicDependencies DynamicAdapterDependencies,
	videoDependencies ...VideoAdapterDependencies,
) (*Service, error) {
	if database == nil || strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("publishing runtime database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	pool, err := pgxpool.New(ctx, strings.TrimSpace(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("configure publishing postgres pool: %w", err)
	}
	store, err := publishing.NewPostgresStore(pool)
	if err != nil {
		pool.Close()
		return nil, err
	}
	notificationAuditStore, err :=
		metapublishing.NewPostgresNotificationStore(database, clock)
	if err != nil {
		pool.Close()
		return nil, err
	}
	registry, err := newRuntimeAdapterRegistryWithAll(
		executor,
		staticConfig,
		metaConfig,
		dynamicDependencies,
		clock,
		videoDependencies...,
	)
	if err != nil {
		pool.Close()
		return nil, err
	}
	engine, err := publishing.NewEngine(
		store,
		postgresCommandGate{database: database},
		registry,
		postgresRetryAuthorizer{database: database},
		publishing.RetryPolicy{
			BaseDelay:   15 * time.Second,
			MaxDelay:    6 * time.Hour,
			Lease:       2 * time.Minute,
			MaxAttempts: 6,
		},
		publishing.WithNotificationResolver(registry),
		publishing.WithClock(clock),
	)
	if err != nil {
		pool.Close()
		return nil, err
	}
	var notificationDispatcher *metapublishing.NotificationDispatcher
	if metaConfig.NotificationStore != nil ||
		metaConfig.NotificationSender != nil {
		store, ok := metaConfig.NotificationStore.(*metapublishing.PostgresNotificationStore)
		if !ok || metaConfig.NotificationSender == nil {
			pool.Close()
			return nil, fmt.Errorf(
				"%w: incomplete Meta notification dispatcher",
				publishing.ErrProviderUnavailable,
			)
		}
		notificationDispatcher, err = metapublishing.NewNotificationDispatcher(
			store,
			metaConfig.NotificationSender,
			2*time.Minute,
		)
		if err != nil {
			pool.Close()
			return nil, err
		}
	}
	return &Service{
		engine:                 engine,
		notificationDispatcher: notificationDispatcher,
		notificationAuditStore: notificationAuditStore,
		clock:                  clock,
		pool:                   pool,
	}, nil
}

func newRuntimeAdapterRegistry(
	executor staticproviders.Executor,
	config staticproviders.Config,
	videoDependencies ...VideoAdapterDependencies,
) (*publishing.AdapterRegistry, error) {
	return newRuntimeAdapterRegistryWithMeta(
		executor,
		config,
		metapublishing.RegistrationConfig{},
		videoDependencies...,
	)
}

func newRuntimeAdapterRegistryWithMeta(
	executor staticproviders.Executor,
	config staticproviders.Config,
	metaConfig metapublishing.RegistrationConfig,
	videoDependencies ...VideoAdapterDependencies,
) (*publishing.AdapterRegistry, error) {
	return newRuntimeAdapterRegistryWithAll(
		executor,
		config,
		metaConfig,
		DynamicAdapterDependencies{},
		time.Now,
		videoDependencies...,
	)
}

func newRuntimeAdapterRegistryWithAll(
	executor staticproviders.Executor,
	config staticproviders.Config,
	metaConfig metapublishing.RegistrationConfig,
	dynamicDependencies DynamicAdapterDependencies,
	clock func() time.Time,
	videoDependencies ...VideoAdapterDependencies,
) (*publishing.AdapterRegistry, error) {
	registry := publishing.NewAdapterRegistry()
	config.Executor = executor
	if err := staticproviders.Register(registry, config); err != nil {
		return nil, fmt.Errorf("register static publishing adapters: %w", err)
	}
	if err := registerVideoAdapters(registry, videoDependencies...); err != nil {
		return nil, err
	}
	if err := metapublishing.Register(registry, metaConfig); err != nil {
		return nil, fmt.Errorf("register Meta publishing adapters: %w", err)
	}
	if err := registerDynamicAdapters(
		registry,
		dynamicDependencies,
		clock,
	); err != nil {
		return nil, err
	}
	return registry, nil
}

func NewDynamicAdapterRegistry(
	dependencies DynamicAdapterDependencies,
	clock func() time.Time,
) (*publishing.AdapterRegistry, error) {
	registry := publishing.NewAdapterRegistry()
	if err := registerDynamicAdapters(registry, dependencies, clock); err != nil {
		return nil, err
	}
	return registry, nil
}

func registerDynamicAdapters(
	registry *publishing.AdapterRegistry,
	config DynamicAdapterDependencies,
	clock func() time.Time,
) error {
	if config.Mastodon.ready() {
		adapter, err := dynamicpublishing.NewMastodon(
			config.Executor,
			config.Media,
			clock,
		)
		if err != nil {
			return fmt.Errorf("configure Mastodon publisher: %w", err)
		}
		if err = registry.RegisterPublisher(
			string(socialconnections.ProviderMastodon),
			adapter,
		); err != nil {
			return err
		}
	}
	if config.Bluesky.ready() {
		adapter, err := dynamicpublishing.NewBluesky(
			config.Executor,
			config.Media,
			config.Identity,
		)
		if err != nil {
			return fmt.Errorf("configure Bluesky publisher: %w", err)
		}
		if err = registry.RegisterPublisher(
			string(socialconnections.ProviderBluesky),
			adapter,
		); err != nil {
			return err
		}
	}
	return nil
}

func runtimeStaticProviderConfig(database *sql.DB) staticproviders.Config {
	return staticproviders.Config{
		Targets: postgresConnectionTargets{database: database},
		Media:   runtimeMediaResolver(),
		LinkedInVersion: strings.TrimSpace(os.Getenv(
			"POSTQRON_F05_LINKEDIN_API_VERSION",
		)),
		Gates: map[string]staticproviders.Gate{
			staticproviders.ProviderX:         staticProviderGate("X"),
			staticproviders.ProviderLinkedIn:  staticProviderGate("LINKEDIN"),
			staticproviders.ProviderPinterest: staticProviderGate("PINTEREST"),
			staticproviders.ProviderGoogleBusinessProfile: staticProviderGate(
				"GOOGLE_BUSINESS_PROFILE",
			),
		},
	}
}

type postgresConnectionTargets struct {
	database *sql.DB
}

func (resolver postgresConnectionTargets) ResolveTarget(
	ctx context.Context,
	workspaceID, connectionID string,
) (staticproviders.ConnectionTarget, error) {
	if resolver.database == nil {
		return staticproviders.ConnectionTarget{}, errors.New(
			"social connection target store is unavailable",
		)
	}
	var (
		provider string
		remoteID string
	)
	err := resolver.database.QueryRowContext(ctx, `
		SELECT provider::text, remote_id
		  FROM f05_social_connections
		 WHERE workspace_id = $1
		   AND id = $2
		   AND status = 'connected'`,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(connectionID),
	).Scan(&provider, &remoteID)
	if err != nil {
		return staticproviders.ConnectionTarget{}, fmt.Errorf(
			"resolve F5 connection target: %w", err,
		)
	}
	return staticproviders.ConnectionTarget{
		Provider: socialProvider(provider),
		RemoteID: strings.TrimSpace(remoteID),
	}, nil
}

func socialProvider(value string) socialconnections.Provider {
	return socialconnections.Provider(strings.TrimSpace(value))
}

type filesystemMediaResolver struct {
	root string
}

func runtimeMediaResolver() staticproviders.MediaResolver {
	root := strings.TrimSpace(os.Getenv("POSTQRON_F08_MEDIA_ROOT"))
	if root == "" || !filepath.IsAbs(root) {
		return nil
	}
	return filesystemMediaResolver{root: filepath.Clean(root)}
}

func (resolver filesystemMediaResolver) OpenMedia(
	_ context.Context,
	workspaceID, ref string,
) (staticproviders.ResolvedMedia, error) {
	if !safeMediaSegment(workspaceID) || !safeMediaSegment(ref) {
		return staticproviders.ResolvedMedia{}, errors.New(
			"media reference is invalid",
		)
	}
	target := filepath.Join(resolver.root, workspaceID, ref)
	expectedParent := filepath.Join(resolver.root, workspaceID)
	if filepath.Dir(target) != expectedParent {
		return staticproviders.ResolvedMedia{}, errors.New(
			"media reference escapes workspace",
		)
	}
	file, err := os.Open(target)
	if err != nil {
		return staticproviders.ResolvedMedia{}, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return staticproviders.ResolvedMedia{}, errors.New(
			"media object is not a regular file",
		)
	}
	hasher := sha256.New()
	prefix := make([]byte, 512)
	read, readErr := io.ReadFull(file, prefix)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) &&
		!errors.Is(readErr, io.EOF) {
		_ = file.Close()
		return staticproviders.ResolvedMedia{}, readErr
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return staticproviders.ResolvedMedia{}, err
	}
	if _, err = io.Copy(hasher, file); err != nil {
		_ = file.Close()
		return staticproviders.ResolvedMedia{}, err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return staticproviders.ResolvedMedia{}, err
	}
	return staticproviders.ResolvedMedia{
		Body: file, Size: info.Size(),
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
		ContentType: http.DetectContentType(prefix[:read]),
	}, nil
}

func safeMediaSegment(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, `/\`+"\x00")
}

func staticProviderGate(provider string) staticproviders.Gate {
	prefix := "POSTQRON_F08_" + provider + "_"
	return staticproviders.Gate{
		Enabled:         os.Getenv(prefix+"ENABLED") == "true",
		ReviewApproved:  os.Getenv(prefix+"REVIEW_APPROVED") == "true",
		AuditVerified:   os.Getenv(prefix+"RUNTIME_AUDIT_VERIFIED") == "true",
		QuotaConfigured: os.Getenv(prefix+"QUOTA_CONFIGURED") == "true",
	}
}

func NewVideoAdapterRegistry(
	dependencies ...VideoAdapterDependencies,
) (*publishing.AdapterRegistry, error) {
	registry := publishing.NewAdapterRegistry()
	if err := registerVideoAdapters(registry, dependencies...); err != nil {
		return nil, err
	}
	return registry, nil
}

func registerVideoAdapters(
	registry *publishing.AdapterRegistry,
	dependencies ...VideoAdapterDependencies,
) error {
	if len(dependencies) == 0 {
		return nil
	}
	if len(dependencies) != 1 {
		return errors.New("video adapter dependencies must be supplied once")
	}
	config := dependencies[0]
	if config.TikTok.ready() {
		if !config.F5TrailingSlashPaths {
			return errors.New(
				"TikTok publisher requires F5 trailing-slash path support (issue #342)",
			)
		}
		adapter, err := videopublishing.NewTikTok(
			config.Executor,
			videopublishing.TikTokConfig{
				VerifiedPullURLPrefix: config.TikTokVerifiedPullPrefix,
			},
		)
		if err != nil {
			return fmt.Errorf("configure TikTok publisher: %w", err)
		}
		if err = registry.RegisterPublisher(
			string(socialconnections.ProviderTikTok), adapter,
		); err != nil {
			return err
		}
	}
	if config.YouTube.ready() {
		adapter, err := videopublishing.NewYouTube(config.Executor, config.Media)
		if err != nil {
			return fmt.Errorf("configure YouTube publisher: %w", err)
		}
		if err = registry.RegisterPublisher(
			string(socialconnections.ProviderYouTube), adapter,
		); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) DispatchOne(ctx context.Context) (bool, error) {
	if service == nil || service.engine == nil {
		return false, errors.New("publishing runtime is not configured")
	}
	if service.notificationAuditStore == nil || service.clock == nil {
		return false, errors.New(
			"publishing notification audit cleanup is not configured",
		)
	}
	if _, err := service.notificationAuditStore.PurgeExpired(
		ctx,
		service.clock().UTC(),
	); err != nil {
		return false, fmt.Errorf(
			"purge publishing notification audit: %w",
			err,
		)
	}
	if service.notificationDispatcher != nil {
		dispatched, err := service.notificationDispatcher.DispatchOne(ctx)
		if err != nil || dispatched {
			return dispatched, err
		}
	}
	return service.engine.DispatchOne(ctx)
}

func (service *Service) Close() {
	if service != nil && service.pool != nil {
		service.pool.Close()
	}
}

type postgresCommandGate struct {
	database *sql.DB
}

func (gate postgresCommandGate) IsCurrent(
	ctx context.Context,
	workspaceID, commandID string,
	generation int64,
) (bool, error) {
	var current bool
	err := gate.database.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM f07_publication_commands command
			  JOIN f07_scheduled_posts post
			    ON post.id = command.post_id
			   AND post.workspace_id = command.workspace_id
			 WHERE command.workspace_id = $1
			   AND command.id = $2
			   AND command.generation = $3
			   AND command.state = 'pending'
			   AND post.active_command_id = command.id
			   AND post.revision = command.generation
			   AND post.status IN ('scheduled', 'publishing')
		)`,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(commandID),
		generation,
	).Scan(&current)
	if err != nil {
		return false, fmt.Errorf("verify F7 publication command: %w", err)
	}
	return current, nil
}

type postgresRetryAuthorizer struct {
	database *sql.DB
}

func (authorizer postgresRetryAuthorizer) CanRetryPublication(
	ctx context.Context,
	workspaceID, actorID string,
) (bool, error) {
	var allowed bool
	err := authorizer.database.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM f04_memberships membership
			  JOIN f04_workspaces workspace
			    ON workspace.id = membership.workspace_id
			 WHERE membership.workspace_id = $1
			   AND membership.account_id = $2
			   AND membership.status = 'active'
			   AND membership.role::text IN ('owner', 'member')
			   AND workspace.status = 'active'
		)`,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(actorID),
	).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("authorize F8 manual retry: %w", err)
	}
	return allowed, nil
}
