package composer

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

const (
	composerRuntimeHandlerName = "ComposerRuntime"
	configAllowedOrigins       = "composer.allowed_origins"
	configCapabilitiesJSON     = "composer.capabilities_json"
	configS3Endpoint           = "composer.s3.endpoint"
	configS3Region             = "composer.s3.region"
	configS3Bucket             = "composer.s3.bucket"
	configS3AccessKeyID        = "composer.s3.access_key_id"
	configS3SecretAccessKey    = "composer.s3.secret_access_key"
	configS3PathStyle          = "composer.s3.path_style"
	configS3AllowInsecure      = "composer.s3.allow_insecure_endpoint"
	configMaximumUploadBytes   = "composer.max_upload_bytes"
)

var composerRuntimeEnvironment = map[string]string{
	configS3Endpoint:         "POSTQRON_F06_S3_ENDPOINT",
	configS3Region:           "POSTQRON_F06_S3_REGION",
	configS3Bucket:           "POSTQRON_F06_S3_BUCKET",
	configS3AccessKeyID:      "POSTQRON_F06_S3_ACCESS_KEY_ID",
	configS3SecretAccessKey:  "POSTQRON_F06_S3_SECRET_ACCESS_KEY",
	configS3PathStyle:        "POSTQRON_F06_S3_PATH_STYLE",
	configS3AllowInsecure:    "POSTQRON_F06_S3_ALLOW_INSECURE_ENDPOINT",
	configMaximumUploadBytes: "POSTQRON_F06_MAX_UPLOAD_BYTES",
}

type Module struct {
	database   *sql.DB
	clock      func() time.Time
	repository *PostgresRepository
	authorizer ContentAuthorizer
	media      *PostgresMediaStore
	service    *Service
	handler    http.Handler
	origins    map[string]struct{}
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("composer database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	repository, err := NewPostgresRepository(database)
	if err != nil {
		return nil, err
	}
	return &Module{
		database:   database,
		clock:      clock,
		repository: repository,
		authorizer: postgresContentAuthorizer{database: database},
	}, nil
}

func (module *Module) Configure(values map[string]string) error {
	if module == nil || module.repository == nil ||
		module.authorizer == nil {
		return errors.New("composer module is not configured")
	}
	allowedOrigins := strings.TrimSpace(values[configAllowedOrigins])
	if allowedOrigins == "" {
		allowedOrigins = strings.TrimSpace(os.Getenv("POSTQRON_AUTH_ALLOWED_ORIGINS"))
	}
	origins, err := parseComposerAllowedOrigins(allowedOrigins)
	if err != nil {
		return err
	}
	capabilitiesJSON := strings.TrimSpace(values[configCapabilitiesJSON])
	if capabilitiesJSON == "" {
		capabilitiesJSON = strings.TrimSpace(os.Getenv("POSTQRON_F06_CAPABILITIES_JSON"))
	}
	catalog, err := ParseCapabilityCatalog(capabilitiesJSON)
	if err != nil {
		return err
	}
	objects, maxUploadBytes, err := runtimeObjectStore(values)
	if err != nil {
		return err
	}
	media, err := NewPostgresMediaStore(
		module.database,
		objects,
		StreamMediaInspector{},
		module.clock,
		maxUploadBytes,
	)
	if err != nil {
		return err
	}
	destinations, err := NewPostgresDestinationResolver(module.database, catalog)
	if err != nil {
		return err
	}
	service, err := NewService(
		module.repository,
		module.authorizer,
		WithClock(module.clock),
		WithCapabilityCatalog(catalog),
		WithMediaResolver(media),
		WithDestinationResolver(destinations),
	)
	if err != nil {
		return err
	}
	authenticator := runtimeComposerAuthenticator{}
	draftHandler := NewHTTPHandler(service, authenticator)
	mediaHandler := NewMediaHTTPHandler(media, module.authorizer, authenticator)
	mux := http.NewServeMux()
	mux.Handle(
		"/api/v1/workspaces/{workspace_id}/composer/media",
		mediaHandler,
	)
	mux.Handle(
		"/api/v1/workspaces/{workspace_id}/composer/media/{media_id}",
		mediaHandler,
	)
	mux.Handle(
		"/api/v1/workspaces/{workspace_id}/composer/media/{media_id}/complete",
		mediaHandler,
	)
	mux.Handle(
		"/api/v1/workspaces/{workspace_id}/composer/media/{media_id}/download",
		mediaHandler,
	)
	mux.Handle("/", draftHandler)
	module.service = service
	module.media = media
	module.handler = securityHeaders(credentialedComposerCORS(mux, origins))
	module.origins = origins
	return nil
}

func runtimeObjectStore(values map[string]string) (ObjectStore, int64, error) {
	configured := make(map[string]string, len(composerRuntimeEnvironment))
	for key, environmentKey := range composerRuntimeEnvironment {
		value := strings.TrimSpace(values[key])
		if value == "" {
			value = strings.TrimSpace(os.Getenv(environmentKey))
		}
		configured[key] = value
	}
	maxUploadBytes := defaultMaximumUploadBytes
	if configured[configMaximumUploadBytes] != "" {
		parsed, err := strconv.ParseInt(configured[configMaximumUploadBytes], 10, 64)
		if err != nil || parsed < 1 {
			return nil, 0, errors.New("POSTQRON_F06_MAX_UPLOAD_BYTES must be a positive integer")
		}
		maxUploadBytes = parsed
	}
	required := []string{
		configS3Endpoint,
		configS3Region,
		configS3Bucket,
		configS3AccessKeyID,
		configS3SecretAccessKey,
	}
	configuredCount := 0
	for _, key := range required {
		if configured[key] != "" {
			configuredCount++
		}
	}
	if configuredCount == 0 {
		return unavailableObjectStore{}, maxUploadBytes, nil
	}
	if configuredCount != len(required) {
		return nil, 0, errors.New("composer S3 configuration is incomplete")
	}
	pathStyle := true
	if configured[configS3PathStyle] != "" {
		parsed, err := strconv.ParseBool(configured[configS3PathStyle])
		if err != nil {
			return nil, 0, errors.New("POSTQRON_F06_S3_PATH_STYLE must be a boolean")
		}
		pathStyle = parsed
	}
	allowInsecure := false
	if configured[configS3AllowInsecure] != "" {
		parsed, err := strconv.ParseBool(configured[configS3AllowInsecure])
		if err != nil {
			return nil, 0, errors.New(
				"POSTQRON_F06_S3_ALLOW_INSECURE_ENDPOINT must be a boolean",
			)
		}
		allowInsecure = parsed
	}
	store, err := NewS3ObjectStore(S3ObjectStoreConfig{
		Endpoint:              configured[configS3Endpoint],
		Region:                configured[configS3Region],
		Bucket:                configured[configS3Bucket],
		AccessKeyID:           configured[configS3AccessKeyID],
		SecretAccessKey:       configured[configS3SecretAccessKey],
		PathStyle:             pathStyle,
		AllowInsecureEndpoint: allowInsecure,
	})
	if err != nil {
		return nil, 0, err
	}
	return store, maxUploadBytes, nil
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.database == nil ||
		module.service == nil || module.handler == nil {
		return errors.New("composer runtime is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("composer database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil || name != composerRuntimeHandlerName {
		return nil, false
	}
	return module.handler, true
}

func (module *Module) WrapAuthenticatedRoute(
	handlerName string,
	next http.Handler,
) http.Handler {
	if module == nil || handlerName != composerRuntimeHandlerName {
		return next
	}
	return securityHeaders(credentialedComposerCORS(next, module.origins))
}

func (module *Module) SchedulingBoundary() (*SchedulingBoundary, bool) {
	if module == nil || module.service == nil {
		return nil, false
	}
	boundary, err := module.service.SchedulingBoundary()
	if err != nil {
		return nil, false
	}
	return boundary, true
}

type runtimeComposerAuthenticator struct{}

func (runtimeComposerAuthenticator) AccountID(
	request *http.Request,
) (string, bool) {
	return featureruntime.AuthenticatedAccount(request.Context())
}

type postgresContentAuthorizer struct {
	database *sql.DB
}

func (authorizer postgresContentAuthorizer) CanManageContent(
	ctx context.Context,
	workspaceID, actorID string,
) (bool, error) {
	var allowed bool
	err := authorizer.database.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM f04_memberships
			 WHERE workspace_id = $1
			   AND account_id = $2
			   AND status = 'active'
			   AND role::text IN ('owner', 'member')
		)`,
		workspaceID,
		actorID,
	).Scan(&allowed)
	if err != nil {
		return false, err
	}
	return allowed, nil
}
