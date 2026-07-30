// Package socialconnections owns provider-agnostic authorization, explicit
// resource selection, encrypted credentials, refresh, reconnection, and
// revocation for social publishing channels.
package socialconnections

import (
	"context"
	"errors"
	"net/http"
	"time"
)

const FeatureID = "social-connections"

type Provider string

const (
	ProviderFacebookPages         Provider = "facebook_pages"
	ProviderFacebookGroups        Provider = "facebook_groups"
	ProviderInstagramProfessional Provider = "instagram_professional"
	ProviderInstagramPersonal     Provider = "instagram_personal"
	ProviderX                     Provider = "x"
	ProviderLinkedIn              Provider = "linkedin"
	ProviderPinterest             Provider = "pinterest"
	ProviderTikTok                Provider = "tiktok"
	ProviderGoogleBusinessProfile Provider = "google_business_profile"
	ProviderMastodon              Provider = "mastodon"
	ProviderYouTube               Provider = "youtube"
	ProviderThreads               Provider = "threads"
	ProviderBluesky               Provider = "bluesky"
)

var SupportedProviders = []Provider{
	ProviderFacebookPages,
	ProviderFacebookGroups,
	ProviderInstagramProfessional,
	ProviderInstagramPersonal,
	ProviderX,
	ProviderLinkedIn,
	ProviderPinterest,
	ProviderTikTok,
	ProviderGoogleBusinessProfile,
	ProviderMastodon,
	ProviderYouTube,
	ProviderThreads,
	ProviderBluesky,
}

// LegacyBootstrapProviders keeps the already shipped F30 client operational
// until #306 adopts the versioned provider catalog. New clients must consume
// ClientBootstrap.Catalog.
var LegacyBootstrapProviders = []Provider{
	ProviderFacebookPages,
	ProviderInstagramProfessional,
}

type ResourceType string

const (
	ResourceFacebookPage           ResourceType = "facebook_page"
	ResourceFacebookGroup          ResourceType = "facebook_group"
	ResourceInstagramProfessional  ResourceType = "instagram_professional"
	ResourceInstagramPersonal      ResourceType = "instagram_personal"
	ResourceXProfile               ResourceType = "x_profile"
	ResourceLinkedInProfile        ResourceType = "linkedin_profile"
	ResourceLinkedInPage           ResourceType = "linkedin_page"
	ResourcePinterestBoard         ResourceType = "pinterest_board"
	ResourceTikTokProfile          ResourceType = "tiktok_profile"
	ResourceGoogleBusinessLocation ResourceType = "google_business_profile_location"
	ResourceMastodonAccount        ResourceType = "mastodon_account"
	ResourceYouTubeChannel         ResourceType = "youtube_channel"
	ResourceThreadsProfile         ResourceType = "threads_profile"
	ResourceBlueskyAccount         ResourceType = "bluesky_account"
)

type AccountType string

const (
	AccountTypePage         AccountType = "page"
	AccountTypeGroup        AccountType = "group"
	AccountTypeBusiness     AccountType = "business"
	AccountTypeCreator      AccountType = "creator"
	AccountTypePersonal     AccountType = "personal"
	AccountTypeProfile      AccountType = "profile"
	AccountTypeOrganization AccountType = "organization"
	AccountTypeBoard        AccountType = "board"
	AccountTypeLocation     AccountType = "location"
	AccountTypeChannel      AccountType = "channel"
)

type PublishingMode string

const (
	PublishingModeAuto         PublishingMode = "auto"
	PublishingModeNotification PublishingMode = "notification"
)

type ResourceCapability struct {
	ResourceType    ResourceType     `json:"resource_type"`
	AccountTypes    []AccountType    `json:"account_types"`
	PublishingModes []PublishingMode `json:"publishing_modes"`
}

type AdapterCapabilities struct {
	Authorization     bool `json:"authorization"`
	PKCE              bool `json:"pkce"`
	ResourceSelection bool `json:"resource_selection"`
	TokenRefresh      bool `json:"token_refresh"`
	RemoteRevocation  bool `json:"remote_revocation"`
	DynamicDiscovery  bool `json:"dynamic_discovery"`
	PAR               bool `json:"par"`
	DPoP              bool `json:"dpop"`
	AccessTokenHash   bool `json:"access_token_hash"`
	AuthenticatedHTTP bool `json:"authenticated_http"`
}

// AdapterCapabilityReporter is optional so existing test doubles and future
// adapters cannot accidentally claim capabilities they do not implement.
type AdapterCapabilityReporter interface {
	AdapterCapabilities() AdapterCapabilities
}

type ConnectionStatus string

const (
	StatusConnected         ConnectionStatus = "connected"
	StatusReconnectRequired ConnectionStatus = "reconnect_required"
	StatusRevoked           ConnectionStatus = "revoked"
)

type Permission string

const (
	PermissionViewWorkspace  Permission = "workspace.view"
	PermissionManageChannels Permission = "channels.manage"
)

type Candidate struct {
	RemoteID     string       `json:"remote_id"`
	ResourceType ResourceType `json:"resource_type"`
	AccountType  AccountType  `json:"account_type"`
	DisplayName  string       `json:"display_name"`
	Handle       string       `json:"handle,omitempty"`
	PictureURL   string       `json:"picture_url,omitempty"`
	Scopes       []string     `json:"scopes"`
}

type Connection struct {
	ID                 string           `json:"id"`
	WorkspaceID        string           `json:"workspace_id"`
	Provider           Provider         `json:"provider"`
	RemoteID           string           `json:"remote_id"`
	ResourceType       ResourceType     `json:"resource_type"`
	AccountType        AccountType      `json:"account_type"`
	DisplayName        string           `json:"display_name"`
	Handle             string           `json:"handle,omitempty"`
	PictureURL         string           `json:"picture_url,omitempty"`
	Scopes             []string         `json:"scopes"`
	Status             ConnectionStatus `json:"status"`
	ReconnectReason    string           `json:"reconnect_reason,omitempty"`
	TokenExpiresAt     *time.Time       `json:"token_expires_at"`
	LastVerifiedAt     *time.Time       `json:"last_verified_at"`
	ConnectedByActorID string           `json:"-"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	RevokedAt          *time.Time       `json:"revoked_at,omitempty"`
}

type Credential struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    *time.Time
	Scopes       []string
}

type DiscoveredResource struct {
	Candidate  Candidate
	Credential Credential
}

type OAuthScopeSeparator string

const (
	// OAuthScopeSeparatorComma preserves the Meta-compatible serialization
	// used before OAuthConfig exposed a provider-neutral separator.
	OAuthScopeSeparatorComma OAuthScopeSeparator = ","
	OAuthScopeSeparatorSpace OAuthScopeSeparator = " "
)

type OAuthConfig struct {
	ClientID         string
	AuthorizationURL string
	RedirectURL      string
	Scopes           []string
	ScopeSeparator   OAuthScopeSeparator
	SupportsPKCE     bool
	ExtraParameters  map[string]string
}

type ExchangeRequest struct {
	Code         string
	RedirectURL  string
	PKCEVerifier string
}

type DiscoveryInputKind string

const (
	DiscoveryInstanceOrigin DiscoveryInputKind = "instance_origin"
	DiscoveryHandle         DiscoveryInputKind = "handle"
	DiscoveryDID            DiscoveryInputKind = "did"
	DiscoveryPDSOrigin      DiscoveryInputKind = "pds_origin"
)

// DiscoveryInput is deliberately typed and contains no password or
// app-password variant. Provider adapters must still perform DNS-pinned SSRF
// validation when dereferencing a syntactically valid input.
type DiscoveryInput struct {
	Kind  DiscoveryInputKind `json:"kind"`
	Value string             `json:"value"`
}

type OAuthBinding struct {
	Issuer         string
	ResourceServer string
	Subject        string
}

type RefreshTokenMode string

const (
	RefreshTokenReusable  RefreshTokenMode = "reusable"
	RefreshTokenSingleUse RefreshTokenMode = "single_use"
)

type RevocationPolicy string

const (
	RevocationBestEffort     RevocationPolicy = "best_effort"
	RevocationRemoteRequired RevocationPolicy = "remote_required"
)

type DynamicNetworkPolicy struct {
	RejectRedirects   bool
	ValidateAndPinDNS bool
	MaxResponseBytes  int64
}

type DynamicOAuthConfig struct {
	RedirectURL      string
	Scopes           []string
	RequiresPAR      bool
	RequiresDPoP     bool
	RequiresATH      bool
	RequiresIssuer   bool
	RequiresSubject  bool
	RefreshTokenMode RefreshTokenMode
	RevocationPolicy RevocationPolicy
	NetworkPolicy    DynamicNetworkPolicy
}

type DynamicBeginRequest struct {
	Discovery       DiscoveryInput
	PreviousBinding OAuthBinding
	State           string
	ExpiresAt       time.Time
}

type DynamicAuthorization struct {
	URL           string
	ProviderState []byte
	Binding       OAuthBinding
	PARRequestURI string
}

type DynamicCallbackRequest struct {
	Code          string
	Issuer        string
	RedirectURL   string
	ProviderState []byte
	Binding       OAuthBinding
}

type DynamicCompletion struct {
	Resources     []DiscoveredResource
	ProviderState []byte
	Binding       OAuthBinding
}

// AuthenticatedRequest intentionally accepts a relative resource-server path.
// The adapter owns the bound origin and transport, so callers cannot turn the
// F5 credential boundary into an arbitrary URL fetch.
type AuthenticatedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

type AuthenticatedResponse struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

type DynamicSession struct {
	Binding       OAuthBinding
	Credential    Credential
	ProviderState []byte
}

type DynamicRefreshResult struct {
	Session DynamicSession
}

type DynamicAuthenticatedResult struct {
	Response AuthenticatedResponse
	Session  DynamicSession
}

// DynamicAdapter is an optional provider-neutral boundary. Static adapters
// continue to implement Adapter unchanged. Dynamic providers return opaque
// attempt/session state; F5 encrypts it before persistence and never exposes it
// to HTTP or publishing callers.
type DynamicAdapter interface {
	DynamicConfig() DynamicOAuthConfig
	BeginDynamic(context.Context, DynamicBeginRequest) (DynamicAuthorization, error)
	CompleteDynamic(context.Context, DynamicCallbackRequest) (DynamicCompletion, error)
	RefreshDynamic(context.Context, DynamicSession) (DynamicRefreshResult, error)
	DoAuthenticated(context.Context, DynamicSession, AuthenticatedRequest) (DynamicAuthenticatedResult, error)
	RevokeDynamic(context.Context, DynamicSession) error
}

type Adapter interface {
	Config() OAuthConfig
	Exchange(context.Context, ExchangeRequest) (Credential, error)
	Discover(context.Context, Credential) ([]DiscoveredResource, error)
	Refresh(context.Context, Credential) (Credential, error)
	Verify(context.Context, string, Credential) error
	Revoke(context.Context, string, Credential) error
}

type AdapterRevocationPolicyReporter interface {
	RevocationPolicy() RevocationPolicy
}

type ProviderFailureKind string

const (
	FailureTemporary         ProviderFailureKind = "temporary"
	FailureAuthentication    ProviderFailureKind = "authentication_revoked"
	FailurePermissionMissing ProviderFailureKind = "permission_missing"
	FailureResourceGone      ProviderFailureKind = "resource_gone"
	FailureInvalidResponse   ProviderFailureKind = "invalid_response"
)

type ProviderFailure struct {
	Kind      ProviderFailureKind
	Code      string
	Retryable bool
	Cause     error
}

func (failure *ProviderFailure) Error() string {
	if failure.Cause != nil {
		return failure.Code + ": " + failure.Cause.Error()
	}
	return failure.Code
}

func (failure *ProviderFailure) Unwrap() error {
	return failure.Cause
}

type Authorization struct {
	URL       string    `json:"authorization_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Selection struct {
	ID        string      `json:"selection_id"`
	Provider  Provider    `json:"provider"`
	Resources []Candidate `json:"resources"`
	ExpiresAt time.Time   `json:"expires_at"`
}

type ProviderAvailabilityStatus string

const (
	ProviderAvailable   ProviderAvailabilityStatus = "available"
	ProviderUnavailable ProviderAvailabilityStatus = "unavailable"
)

type ProviderConfigurationState string

const (
	ProviderNotConfigured  ProviderConfigurationState = "not_configured"
	ProviderReviewRequired ProviderConfigurationState = "review_required"
	ProviderAuditRequired  ProviderConfigurationState = "audit_required"
	ProviderReady          ProviderConfigurationState = "ready"
)

type ProviderAvailability struct {
	Provider           Provider                   `json:"provider"`
	Status             ProviderAvailabilityStatus `json:"status"`
	ConfigurationState ProviderConfigurationState `json:"configuration_state"`
	Retryable          bool                       `json:"retryable"`
}

type ProviderCatalogEntry struct {
	Provider           Provider                   `json:"provider"`
	Status             ProviderAvailabilityStatus `json:"status"`
	ConfigurationState ProviderConfigurationState `json:"configuration_state"`
	Retryable          bool                       `json:"retryable"`
	Resources          []ResourceCapability       `json:"resources"`
	Capabilities       AdapterCapabilities        `json:"capabilities"`
}

type ClientBootstrap struct {
	CatalogVersion string                 `json:"catalog_version"`
	Providers      []ProviderAvailability `json:"providers"`
	Catalog        []ProviderCatalogEntry `json:"catalog"`
}

type BeginRequest struct {
	WorkspaceID     string
	ActorID         string
	Provider        Provider
	Discovery       DiscoveryInput
	previousBinding OAuthBinding
}

type CallbackRequest struct {
	State         string
	Code          string
	Issuer        string
	ProviderError string
}

type SelectRequest struct {
	WorkspaceID string
	ActorID     string
	SelectionID string
	RemoteID    string
}

type ReconnectRequest struct {
	WorkspaceID  string
	ActorID      string
	ConnectionID string
}

type RevocationResult struct {
	Connection      Connection
	ProviderRevoked bool
}

type Authorizer interface {
	Authorize(context.Context, string, string, Permission) error
}

type Ciphertext struct {
	KeyID string
	Data  []byte
}

type CredentialCipher interface {
	Seal(plaintext, additionalData []byte) (Ciphertext, error)
	Open(ciphertext Ciphertext, additionalData []byte) ([]byte, error)
}

type OAuthAttempt struct {
	ID                     string
	StateHash              string
	WorkspaceID            string
	ActorID                string
	Provider               Provider
	PKCEVerifierCiphertext Ciphertext
	OAuthStateCiphertext   Ciphertext
	Binding                OAuthBinding
	CreatedAt              time.Time
	ExpiresAt              time.Time
	ConsumedAt             *time.Time
}

type StoredResource struct {
	Candidate              Candidate
	AccessTokenCiphertext  Ciphertext
	RefreshTokenCiphertext Ciphertext
	OAuthSessionCiphertext Ciphertext
	Binding                OAuthBinding
	RefreshTokenMode       RefreshTokenMode
	TokenExpiresAt         *time.Time
}

type StoredSelection struct {
	ID          string
	WorkspaceID string
	ActorID     string
	Provider    Provider
	Resources   []StoredResource
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type StoredCredential struct {
	Connection
	AccessTokenCiphertext  Ciphertext
	RefreshTokenCiphertext Ciphertext
	RefreshLockedUntil     *time.Time
	OAuthSessionCiphertext Ciphertext
	Binding                OAuthBinding
	RefreshTokenMode       RefreshTokenMode
	SessionLockedUntil     *time.Time
	SessionLeaseID         string
	SessionRefreshing      bool
}

type Event struct {
	ID            string
	Type          string
	Version       int
	WorkspaceID   string
	ConnectionID  string
	Provider      Provider
	RemoteID      string
	ActorID       string
	Reason        string
	CorrelationID string
	OccurredAt    time.Time
}

const (
	EventConnected         = "social.connection.connected"
	EventReconnected       = "social.connection.reconnected"
	EventReconnectRequired = "social.connection.reconnect-required"
	EventTokenRefreshed    = "social.connection.token-refreshed"
	EventDisconnected      = "social.connection.disconnected"
)

type ConnectCommand struct {
	NewConnectionID string
	WorkspaceID     string
	ActorID         string
	SelectionID     string
	RemoteID        string
	Now             time.Time
	Event           Event
}

type SelectionTarget struct {
	Provider             Provider
	RemoteID             string
	ExistingConnectionID string
	ExistingStatus       ConnectionStatus
}

type ChannelQuotaDecision struct {
	Accepted  bool
	Code      string
	Retryable bool
}

// ChannelQuota is the trusted, server-only F10 boundary. Browser payloads
// never supply quota resource names, deltas, usage, or plan limits.
type ChannelQuota interface {
	ReserveChannel(context.Context, string, string) (ChannelQuotaDecision, error)
	ReleaseChannel(context.Context, string, string) (ChannelQuotaDecision, error)
}

type RefreshCommand struct {
	ConnectionID           string
	AccessTokenCiphertext  Ciphertext
	RefreshTokenCiphertext Ciphertext
	Scopes                 []string
	ExpiresAt              *time.Time
	VerifiedAt             time.Time
	Now                    time.Time
	Event                  Event
}

type SessionCommand struct {
	ConnectionID           string
	SessionLeaseID         string
	AccessTokenCiphertext  Ciphertext
	RefreshTokenCiphertext Ciphertext
	OAuthSessionCiphertext Ciphertext
	Scopes                 []string
	ExpiresAt              *time.Time
	UpdateCredential       bool
	VerifiedAt             time.Time
	Now                    time.Time
	Event                  *Event
}

type Repository interface {
	CreateAttempt(context.Context, OAuthAttempt) error
	ConsumeAttempt(context.Context, string, time.Time) (OAuthAttempt, error)
	SaveSelection(context.Context, StoredSelection) error
	InspectSelection(context.Context, string, string, string, string, time.Time) (SelectionTarget, error)
	Connect(context.Context, ConnectCommand) (Connection, bool, error)
	ListConnections(context.Context, string) ([]Connection, error)
	GetCredential(context.Context, string, string) (StoredCredential, error)
	ClaimRefresh(context.Context, string, string, time.Time, time.Time, time.Duration) (StoredCredential, bool, error)
	CompleteRefresh(context.Context, RefreshCommand) (Connection, error)
	ReleaseRefresh(context.Context, string, string) error
	ClaimSession(context.Context, string, string, time.Time, time.Time, time.Duration) (StoredCredential, bool, error)
	CompleteSession(context.Context, SessionCommand) (Connection, error)
	ReleaseSession(context.Context, string, string, string) error
	MarkReconnectRequired(context.Context, string, string, string, time.Time, Event) (Connection, bool, error)
	Revoke(context.Context, string, string, time.Time, Event) (Connection, bool, error)
}

var (
	ErrInvalidArgument                = errors.New("invalid argument")
	ErrUnsupportedProvider            = errors.New("unsupported social provider")
	ErrUnauthorized                   = errors.New("social channel operation not authorized")
	ErrInvalidState                   = errors.New("invalid or already used OAuth state")
	ErrFlowExpired                    = errors.New("social authorization flow expired")
	ErrProviderDenied                 = errors.New("social authorization denied by provider")
	ErrNoResources                    = errors.New("provider returned no publishable resources")
	ErrResourceNotFound               = errors.New("social resource not found")
	ErrResourceAlreadyUsed            = errors.New("social resource is already connected")
	ErrReconnectRequired              = errors.New("social connection must be reconnected")
	ErrRefreshInProgress              = errors.New("social credential refresh is already in progress")
	ErrAuthenticatedRequestInProgress = errors.New("social authenticated request is already in progress")
	ErrAuthenticatedRequestRequired   = errors.New("provider access requires the authenticated request boundary")
	ErrRefreshOutcomeUnknown          = errors.New("single-use refresh outcome is unknown")
	ErrNotRefreshable                 = errors.New("social credential cannot be refreshed")
	ErrExternalRevocationUnavailable  = errors.New("per-resource provider revocation is unavailable")
	ErrRemoteRevocationRequired       = errors.New("remote provider revocation is required")
	ErrProviderRequestFailed          = errors.New("authenticated provider request failed")
	ErrConnectionRevoked              = errors.New("social connection is revoked")
	ErrProviderUnavailable            = errors.New("social provider is unavailable")
	ErrProviderNotConfigured          = errors.New("social provider is not configured")
	ErrProviderReviewRequired         = errors.New("social provider review is required")
	ErrProviderAuditRequired          = errors.New("social provider verification is required")
	ErrChannelQuotaExceeded           = errors.New("social channel quota exceeded")
	ErrChannelQuotaUnavailable        = errors.New("social channel quota is unavailable")
)
