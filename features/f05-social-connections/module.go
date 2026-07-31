package socialconnections

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

const (
	socialRuntimeHandlerName = "SocialRuntime"

	configEnabled           = "social.meta.enabled"
	configAllowedOrigins    = "social.allowed_origins"
	configGraphVersion      = "social.meta.graph_version"
	configCipherKeyID       = "social.meta.cipher_key_id"
	configCipherKey         = "social.meta.cipher_key_base64"
	configFacebookID        = "social.meta.facebook.client_id"
	configFacebookSecret    = "social.meta.facebook.client_secret"
	configFacebookRedirect  = "social.meta.facebook.redirect_url"
	configFacebookConfigID  = "social.meta.facebook.login_config_id"
	configFacebookReviewed  = "social.meta.facebook.app_review_approved"
	configFacebookAudited   = "social.meta.facebook.runtime_audit_verified"
	configInstagramID       = "social.meta.instagram.client_id"
	configInstagramSecret   = "social.meta.instagram.client_secret"
	configInstagramRedirect = "social.meta.instagram.redirect_url"
	configInstagramReviewed = "social.meta.instagram.app_review_approved"
	configInstagramAudited  = "social.meta.instagram.runtime_audit_verified"
)

var runtimeEnvironmentKeys = map[string]string{
	configEnabled:           "POSTQRON_F05_META_ENABLED",
	configAllowedOrigins:    "POSTQRON_AUTH_ALLOWED_ORIGINS",
	configGraphVersion:      "POSTQRON_F05_META_GRAPH_VERSION",
	configCipherKeyID:       "POSTQRON_F05_CIPHER_KEY_ID",
	configCipherKey:         "POSTQRON_F05_CIPHER_KEY_BASE64",
	configFacebookID:        "POSTQRON_F05_FACEBOOK_CLIENT_ID",
	configFacebookSecret:    "POSTQRON_F05_FACEBOOK_CLIENT_SECRET",
	configFacebookRedirect:  "POSTQRON_F05_FACEBOOK_REDIRECT_URL",
	configFacebookConfigID:  "POSTQRON_F05_FACEBOOK_LOGIN_CONFIG_ID",
	configFacebookReviewed:  "POSTQRON_F05_FACEBOOK_APP_REVIEW_APPROVED",
	configFacebookAudited:   "POSTQRON_F05_FACEBOOK_RUNTIME_AUDIT_VERIFIED",
	configInstagramID:       "POSTQRON_F05_INSTAGRAM_CLIENT_ID",
	configInstagramSecret:   "POSTQRON_F05_INSTAGRAM_CLIENT_SECRET",
	configInstagramRedirect: "POSTQRON_F05_INSTAGRAM_REDIRECT_URL",
	configInstagramReviewed: "POSTQRON_F05_INSTAGRAM_APP_REVIEW_APPROVED",
	configInstagramAudited:  "POSTQRON_F05_INSTAGRAM_RUNTIME_AUDIT_VERIFIED",
}

type Module struct {
	database   *sql.DB
	clock      func() time.Time
	repository Repository
	authorizer Authorizer
	quota      ChannelQuota
	service    *Service
	handler    http.Handler
	origins    map[string]struct{}
}

func NewPostgresModule(
	database *sql.DB,
	clock func() time.Time,
) (*Module, error) {
	if database == nil {
		return nil, errors.New("social connections database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	repository, err := NewPostgresRepository(database)
	if err != nil {
		return nil, err
	}
	quota, err := NewPostgresChannelQuota(database, clock)
	if err != nil {
		return nil, err
	}
	return &Module{
		database:   database,
		clock:      clock,
		repository: repository,
		authorizer: postgresSocialAuthorizer{database: database},
		quota:      quota,
	}, nil
}

func (module *Module) Configure(values map[string]string) error {
	if module == nil || module.repository == nil ||
		module.authorizer == nil || module.quota == nil {
		return errors.New("social connections module is not configured")
	}
	configured := runtimeValues(values)
	origins, err := parseSocialAllowedOrigins(configured[configAllowedOrigins])
	if err != nil {
		return err
	}
	originPolicy, err := newSocialOriginPolicy(origins)
	if err != nil {
		return err
	}
	adapters := make(map[Provider]Adapter)
	availability := make(map[Provider]ProviderAvailability)
	for _, provider := range SupportedProviders {
		availability[provider] = ProviderAvailability{
			Provider:           provider,
			Status:             ProviderUnavailable,
			ConfigurationState: ProviderNotConfigured,
			Retryable:          false,
		}
	}

	var cipher CredentialCipher
	featureEnabled := configured[configEnabled] == "true"
	key, keyOK := decodeRuntimeCipherKey(configured[configCipherKey])
	keyID := strings.TrimSpace(configured[configCipherKeyID])
	if featureEnabled && keyOK && keyID != "" {
		var err error
		cipher, err = NewAESGCMCipher(keyID, key)
		if err != nil {
			cipher = nil
		}
	}
	if featureEnabled && cipher != nil {
		module.configureFacebook(configured, adapters, availability)
		module.configureInstagram(configured, adapters, availability)
	}
	configureRuntimeProviderFamilies(
		configured,
		cipher,
		adapters,
		availability,
	)
	service, err := NewService(Config{
		Repository:   module.repository,
		Authorizer:   module.authorizer,
		Cipher:       cipher,
		Quota:        module.quota,
		Adapters:     adapters,
		Availability: availability,
		Now:          module.clock,
	})
	if err != nil {
		return err
	}
	handler, err := NewHTTPHandler(
		service,
		runtimeRequestAuthenticator{},
		origins...,
	)
	if err != nil {
		return err
	}
	module.service = service
	module.handler = handler
	module.origins = originPolicy
	return nil
}

func (module *Module) configureFacebook(
	values map[string]string,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	if !allPresent(
		values,
		configGraphVersion,
		configFacebookID,
		configFacebookSecret,
		configFacebookRedirect,
		configFacebookConfigID,
	) {
		return
	}
	if values[configFacebookReviewed] != "true" {
		availability[ProviderFacebookPages] = unavailableProvider(
			ProviderFacebookPages,
			ProviderReviewRequired,
		)
		return
	}
	if values[configFacebookAudited] != "true" {
		availability[ProviderFacebookPages] = unavailableProvider(
			ProviderFacebookPages,
			ProviderAuditRequired,
		)
		return
	}
	adapter, err := NewMetaAdapter(MetaAdapterConfig{
		Provider:              ProviderFacebookPages,
		ClientID:              values[configFacebookID],
		ClientSecret:          values[configFacebookSecret],
		RedirectURL:           values[configFacebookRedirect],
		GraphVersion:          values[configGraphVersion],
		FacebookLoginConfigID: values[configFacebookConfigID],
		SupportsPKCE:          true,
	})
	if err != nil {
		return
	}
	adapters[ProviderFacebookPages] = adapter
	availability[ProviderFacebookPages] = ProviderAvailability{
		Provider:           ProviderFacebookPages,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}

func (module *Module) configureInstagram(
	values map[string]string,
	adapters map[Provider]Adapter,
	availability map[Provider]ProviderAvailability,
) {
	if !allPresent(
		values,
		configGraphVersion,
		configInstagramID,
		configInstagramSecret,
		configInstagramRedirect,
	) {
		return
	}
	if values[configInstagramReviewed] != "true" {
		availability[ProviderInstagramProfessional] = unavailableProvider(
			ProviderInstagramProfessional,
			ProviderReviewRequired,
		)
		return
	}
	if values[configInstagramAudited] != "true" {
		availability[ProviderInstagramProfessional] = unavailableProvider(
			ProviderInstagramProfessional,
			ProviderAuditRequired,
		)
		return
	}
	adapter, err := NewMetaAdapter(MetaAdapterConfig{
		Provider:     ProviderInstagramProfessional,
		ClientID:     values[configInstagramID],
		ClientSecret: values[configInstagramSecret],
		RedirectURL:  values[configInstagramRedirect],
		GraphVersion: values[configGraphVersion],
		SupportsPKCE: true,
	})
	if err != nil {
		return
	}
	adapters[ProviderInstagramProfessional] = adapter
	availability[ProviderInstagramProfessional] = ProviderAvailability{
		Provider:           ProviderInstagramProfessional,
		Status:             ProviderAvailable,
		ConfigurationState: ProviderReady,
	}
}

func (module *Module) Start(context.Context) error {
	if module == nil || module.database == nil ||
		module.service == nil || module.handler == nil {
		return errors.New("social connections runtime is not configured")
	}
	return nil
}

func (module *Module) Stop(context.Context) error {
	return nil
}

func (module *Module) Ready(ctx context.Context) error {
	if module == nil || module.database == nil {
		return errors.New("social connections database is unavailable")
	}
	return module.database.PingContext(ctx)
}

func (module *Module) Handler(name string) (http.Handler, bool) {
	if module == nil || module.handler == nil || name != socialRuntimeHandlerName {
		return nil, false
	}
	return module.handler, true
}

func (module *Module) WrapAuthenticatedRoute(
	handlerName string,
	next http.Handler,
) http.Handler {
	if module == nil || handlerName != socialRuntimeHandlerName {
		return next
	}
	return credentialedSocialCORS(next, module.origins)
}

type runtimeRequestAuthenticator struct{}

func (runtimeRequestAuthenticator) AccountID(
	request *http.Request,
) (string, bool) {
	return featureruntime.AuthenticatedAccount(request.Context())
}

type postgresSocialAuthorizer struct {
	database *sql.DB
}

func (authorizer postgresSocialAuthorizer) Authorize(
	ctx context.Context,
	workspaceID, actorID string,
	permission Permission,
) error {
	role := ""
	switch permission {
	case PermissionViewWorkspace:
	case PermissionManageChannels:
		role = "owner"
	default:
		return ErrUnauthorized
	}
	var allowed bool
	err := authorizer.database.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM f04_memberships
			 WHERE workspace_id = $1
			   AND account_id = $2
			   AND status = 'active'
			   AND ($3 = '' OR role::text = $3)
		)`,
		workspaceID,
		actorID,
		role,
	).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrUnauthorized
	}
	return nil
}

func runtimeValues(values map[string]string) map[string]string {
	result := make(
		map[string]string,
		len(values)+len(runtimeEnvironmentKeys),
	)
	for key, value := range values {
		result[key] = strings.TrimSpace(value)
	}
	for key, environmentKey := range runtimeEnvironmentKeys {
		value := strings.TrimSpace(values[key])
		if value == "" {
			value = strings.TrimSpace(os.Getenv(environmentKey))
		}
		result[key] = value
	}
	return result
}

func decodeRuntimeCipherKey(value string) ([]byte, bool) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	return key, err == nil && len(key) == 32
}

func allPresent(values map[string]string, keys ...string) bool {
	for _, key := range keys {
		if strings.TrimSpace(values[key]) == "" {
			return false
		}
	}
	return true
}

func unavailableProvider(
	provider Provider,
	state ProviderConfigurationState,
) ProviderAvailability {
	return ProviderAvailability{
		Provider:           provider,
		Status:             ProviderUnavailable,
		ConfigurationState: state,
		Retryable:          false,
	}
}
