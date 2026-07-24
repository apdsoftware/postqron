// Package socialconnections owns OAuth connection, explicit resource
// selection, encrypted credentials, refresh, reconnection, and revocation for
// the launch social channels.
package socialconnections

import (
	"context"
	"errors"
	"time"
)

const FeatureID = "social-connections"

type Provider string

const (
	ProviderFacebookPages         Provider = "facebook_pages"
	ProviderInstagramProfessional Provider = "instagram_professional"
)

var SupportedProviders = []Provider{
	ProviderFacebookPages,
	ProviderInstagramProfessional,
}

type ResourceType string

const (
	ResourceFacebookPage          ResourceType = "facebook_page"
	ResourceInstagramProfessional ResourceType = "instagram_professional"
)

type AccountType string

const (
	AccountTypePage     AccountType = "page"
	AccountTypeBusiness AccountType = "business"
	AccountTypeCreator  AccountType = "creator"
)

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
	ID                 string
	WorkspaceID        string
	Provider           Provider
	RemoteID           string
	ResourceType       ResourceType
	AccountType        AccountType
	DisplayName        string
	Handle             string
	PictureURL         string
	Scopes             []string
	Status             ConnectionStatus
	ReconnectReason    string
	TokenExpiresAt     *time.Time
	LastVerifiedAt     *time.Time
	ConnectedByActorID string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	RevokedAt          *time.Time
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

type OAuthConfig struct {
	ClientID         string
	AuthorizationURL string
	RedirectURL      string
	Scopes           []string
	SupportsPKCE     bool
	ExtraParameters  map[string]string
}

type ExchangeRequest struct {
	Code         string
	RedirectURL  string
	PKCEVerifier string
}

type Adapter interface {
	Config() OAuthConfig
	Exchange(context.Context, ExchangeRequest) (Credential, error)
	Discover(context.Context, Credential) ([]DiscoveredResource, error)
	Refresh(context.Context, Credential) (Credential, error)
	Verify(context.Context, string, Credential) error
	Revoke(context.Context, string, Credential) error
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
	URL       string
	ExpiresAt time.Time
}

type Selection struct {
	ID        string
	Provider  Provider
	Resources []Candidate
	ExpiresAt time.Time
}

type BeginRequest struct {
	WorkspaceID string
	ActorID     string
	Provider    Provider
}

type CallbackRequest struct {
	State         string
	Code          string
	ProviderError string
}

type SelectRequest struct {
	WorkspaceID string
	ActorID     string
	SelectionID string
	RemoteID    string
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
	CreatedAt              time.Time
	ExpiresAt              time.Time
	ConsumedAt             *time.Time
}

type StoredResource struct {
	Candidate              Candidate
	AccessTokenCiphertext  Ciphertext
	RefreshTokenCiphertext Ciphertext
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

type Repository interface {
	CreateAttempt(context.Context, OAuthAttempt) error
	ConsumeAttempt(context.Context, string, time.Time) (OAuthAttempt, error)
	SaveSelection(context.Context, StoredSelection) error
	Connect(context.Context, ConnectCommand) (Connection, bool, error)
	ListConnections(context.Context, string) ([]Connection, error)
	GetCredential(context.Context, string, string) (StoredCredential, error)
	ClaimRefresh(context.Context, string, string, time.Time, time.Time, time.Duration) (StoredCredential, bool, error)
	CompleteRefresh(context.Context, RefreshCommand) (Connection, error)
	ReleaseRefresh(context.Context, string, string) error
	MarkReconnectRequired(context.Context, string, string, string, time.Time, Event) (Connection, bool, error)
	Revoke(context.Context, string, string, time.Time, Event) (Connection, bool, error)
}

var (
	ErrInvalidArgument               = errors.New("invalid argument")
	ErrUnsupportedProvider           = errors.New("unsupported social provider")
	ErrUnauthorized                  = errors.New("social channel operation not authorized")
	ErrInvalidState                  = errors.New("invalid or already used OAuth state")
	ErrFlowExpired                   = errors.New("social authorization flow expired")
	ErrProviderDenied                = errors.New("social authorization denied by provider")
	ErrNoResources                   = errors.New("provider returned no publishable resources")
	ErrResourceNotFound              = errors.New("social resource not found")
	ErrResourceAlreadyUsed           = errors.New("social resource is already connected")
	ErrReconnectRequired             = errors.New("social connection must be reconnected")
	ErrRefreshInProgress             = errors.New("social credential refresh is already in progress")
	ErrNotRefreshable                = errors.New("social credential cannot be refreshed")
	ErrExternalRevocationUnavailable = errors.New("per-resource provider revocation is unavailable")
	ErrConnectionRevoked             = errors.New("social connection is revoked")
)
