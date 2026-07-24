// Package accountprivacy owns the account profile and the orchestration needed
// to exercise export and erasure rights. Cross-feature side effects are exposed
// as ports so credentials and data never have to cross this slice boundary.
package accountprivacy

import (
	"context"
	"errors"
	"time"
)

const (
	FeatureID              = "account-privacy"
	ReauthenticationWindow = 5 * time.Minute
	GracePeriod            = 28 * 24 * time.Hour
	ExportRetention        = 7 * 24 * time.Hour
	DownloadLinkLifetime   = 24 * time.Hour
	TombstoneRetention     = 45 * 24 * time.Hour
)

var (
	ErrUnauthenticated          = errors.New("authentication required")
	ErrReauthenticationRequired = errors.New("recent authentication required")
	ErrInvalidArgument          = errors.New("invalid argument")
	ErrNotFound                 = errors.New("resource not found")
	ErrForbidden                = errors.New("operation forbidden")
	ErrConflict                 = errors.New("resource conflict")
	ErrLastLoginProvider        = errors.New("the last login provider cannot be disconnected")
	ErrExportNotReady           = errors.New("export is not ready")
	ErrExportExpired            = errors.New("export has expired")
	ErrDeletionInactive         = errors.New("deletion request is not in its grace period")
	ErrGracePeriodElapsed       = errors.New("the deletion grace period has elapsed")
	ErrDeactivationIncomplete   = errors.New("account deactivation is incomplete")
	ErrFinalizationIncomplete   = errors.New("deletion finalization is incomplete")
)

type Principal struct {
	AccountID       string
	AuthenticatedAt time.Time
}

type Profile struct {
	AccountID   string    `json:"account_id"`
	DisplayName string    `json:"display_name"`
	Locale      string    `json:"locale"`
	Timezone    string    `json:"timezone"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type ProviderKind string

const (
	ProviderIdentity ProviderKind = "identity"
	ProviderSocial   ProviderKind = "social"
)

type Provider struct {
	ID              string       `json:"id"`
	Kind            ProviderKind `json:"kind"`
	Name            string       `json:"name"`
	ExternalLabel   string       `json:"external_label"`
	ConnectedAt     time.Time    `json:"connected_at"`
	OnlyLoginMethod bool         `json:"only_login_method"`
}

type WorkspaceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type Plan struct {
	Code       string           `json:"code"`
	Name       string           `json:"name"`
	State      string           `json:"state"`
	Usage      map[string]int64 `json:"usage"`
	Limits     map[string]int64 `json:"limits"`
	RenewsAt   *time.Time       `json:"renews_at,omitempty"`
	Manageable bool             `json:"manageable"`
}

type WorkspacePlan struct {
	Workspace WorkspaceRef `json:"workspace"`
	Plan      Plan         `json:"plan"`
}

type AccountArea struct {
	Profile    Profile         `json:"profile"`
	Providers  []Provider      `json:"providers"`
	Workspaces []WorkspacePlan `json:"workspaces"`
}

type ProfileUpdate struct {
	DisplayName string `json:"display_name"`
	Locale      string `json:"locale"`
	Timezone    string `json:"timezone"`
}

type ExportScope string

const (
	ExportAccount   ExportScope = "account"
	ExportWorkspace ExportScope = "workspace"
)

type ExportStatus string

const (
	ExportQueued  ExportStatus = "queued"
	ExportReady   ExportStatus = "ready"
	ExportFailed  ExportStatus = "failed"
	ExportExpired ExportStatus = "expired"
)

type ExportRequest struct {
	ID          string       `json:"id"`
	AccountID   string       `json:"account_id"`
	Scope       ExportScope  `json:"scope"`
	WorkspaceID string       `json:"workspace_id,omitempty"`
	Status      ExportStatus `json:"status"`
	RequestedAt time.Time    `json:"requested_at"`
	ReadyAt     *time.Time   `json:"ready_at,omitempty"`
	ExpiresAt   time.Time    `json:"expires_at"`
	ObjectKey   string       `json:"-"`
	SHA256      string       `json:"sha256,omitempty"`
	SizeBytes   int64        `json:"size_bytes,omitempty"`
}

type ExportJob struct {
	RequestID   string
	AccountID   string
	Scope       ExportScope
	WorkspaceID string
}

type Download struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
	SHA256    string    `json:"sha256"`
	SizeBytes int64     `json:"size_bytes"`
}

type DeletionScope string

const (
	DeleteAccount   DeletionScope = "account"
	DeleteWorkspace DeletionScope = "workspace"
)

type OwnershipActionKind string

const (
	TransferWorkspace OwnershipActionKind = "transfer"
	DeleteOwnedSpace  OwnershipActionKind = "delete"
)

type OwnershipAction struct {
	WorkspaceID       string              `json:"workspace_id"`
	Action            OwnershipActionKind `json:"action"`
	TransferAccountID string              `json:"transfer_account_id,omitempty"`
}

type OwnershipPlan struct {
	Actions []OwnershipAction `json:"actions"`
}

type DeletionStatus string

const (
	DeletionDeactivating       DeletionStatus = "deactivating"
	DeletionGracePeriod        DeletionStatus = "grace_period"
	DeletionFinalizing         DeletionStatus = "finalizing"
	DeletionCompleted          DeletionStatus = "completed"
	DeletionCancelled          DeletionStatus = "cancelled"
	DeletionDeactivationFailed DeletionStatus = "deactivation_failed"
	DeletionFinalizationFailed DeletionStatus = "finalization_failed"
)

type DeletionRequest struct {
	ID                 string         `json:"id"`
	AccountID          string         `json:"account_id"`
	Scope              DeletionScope  `json:"scope"`
	WorkspaceID        string         `json:"workspace_id,omitempty"`
	Status             DeletionStatus `json:"status"`
	RequestedAt        time.Time      `json:"requested_at"`
	GraceEndsAt        time.Time      `json:"grace_ends_at"`
	Immediate          bool           `json:"immediate"`
	Ownership          OwnershipPlan  `json:"ownership"`
	FailureCode        string         `json:"failure_code,omitempty"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	TombstoneID        string         `json:"tombstone_id,omitempty"`
	TombstoneExpiresAt *time.Time     `json:"tombstone_expires_at,omitempty"`
}

type DeactivationReceipt struct {
	AccessFrozen                bool
	SessionsRevoked             bool
	ProviderRevocationAttempted bool
	LocalTokensDeleted          bool
	FutureJobsCancelled         bool
}

type ErasureReceipt struct {
	IdentifyingDataDeleted      bool
	SharedAttributionAnonymized bool
	WorkspaceDataDeleted        bool
	OwnershipApplied            bool
	TombstoneID                 string
	TombstoneExpiresAt          time.Time
	DatabaseCompletedAt         time.Time
	MediaDeletionDueAt          time.Time
}

type AuditEvent struct {
	ID         string         `json:"id"`
	AccountID  string         `json:"account_id"`
	TargetID   string         `json:"target_id"`
	Type       string         `json:"type"`
	Outcome    string         `json:"outcome"`
	OccurredAt time.Time      `json:"occurred_at"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// Repository persists each state transition together with its minimal audit
// event. Implementations must reject concurrent active requests for one target.
type Repository interface {
	Profile(context.Context, string) (Profile, error)
	UpdateProfile(context.Context, ProfileUpdateCommand) (Profile, error)
	Providers(context.Context, string) ([]Provider, error)
	Provider(context.Context, string, string) (Provider, error)
	Workspaces(context.Context, string) ([]WorkspaceRef, error)

	CreateExport(context.Context, ExportRequest) error
	Export(context.Context, string) (ExportRequest, error)
	MarkExportReady(context.Context, ExportReadyCommand) error
	MarkExportFailed(context.Context, string, time.Time) error
	ExpiredExports(context.Context, time.Time, int) ([]ExportRequest, error)
	MarkExportExpired(context.Context, string, time.Time) error

	CreateDeletion(context.Context, DeletionRequest) error
	MarkGracePeriod(context.Context, string, time.Time) error
	MarkDeactivationFailed(context.Context, string, string, time.Time) error
	Deletion(context.Context, string) (DeletionRequest, error)
	CancelDeletion(context.Context, string, time.Time) error
	ClaimDueDeletions(context.Context, time.Time, int) ([]DeletionRequest, error)
	CompleteDeletion(context.Context, DeletionCompleteCommand) error
	MarkFinalizationFailed(context.Context, string, string, time.Time) error
}

type ProfileUpdateCommand struct {
	AccountID   string
	DisplayName string
	Locale      string
	Timezone    string
	Now         time.Time
}

type ExportReadyCommand struct {
	RequestID string
	ObjectKey string
	SHA256    string
	SizeBytes int64
	ReadyAt   time.Time
}

type DeletionCompleteCommand struct {
	RequestID          string
	CompletedAt        time.Time
	TombstoneID        string
	TombstoneExpiresAt time.Time
}

type PlanReader interface {
	Plan(context.Context, string, string) (Plan, error)
}

type ProviderDisconnecter interface {
	// Disconnect must re-check the last-login-method invariant atomically with
	// identity removal, revoke remotely when supported, and always remove local
	// token ciphertext.
	Disconnect(context.Context, string, Provider) error
}

type ExportAuthorizer interface {
	AuthorizeExport(context.Context, string, ExportScope, string) error
}

type ExportQueue interface {
	EnqueueExport(context.Context, ExportJob) error
}

type DownloadSigner interface {
	SignedDownloadURL(context.Context, string, time.Time) (string, error)
}

type ExportArtifactStore interface {
	// DeleteExport is idempotent so concurrent expiry workers are safe.
	DeleteExport(context.Context, string) error
}

// OwnershipResolver validates Owner permissions and makes every sole-owned
// workspace disposition explicit before a deletion can start.
type OwnershipResolver interface {
	Resolve(context.Context, string, DeletionScope, string, []OwnershipAction) (OwnershipPlan, error)
}

// DeletionSafety owns the time-bounded immediate effects: sessions and queued
// jobs within five minutes, and provider token removal within fifteen minutes.
type DeletionSafety interface {
	Deactivate(context.Context, DeletionRequest) (DeactivationReceipt, error)
	RestoreAccess(context.Context, DeletionRequest) error
}

type Eraser interface {
	Erase(context.Context, DeletionRequest, time.Time) (ErasureReceipt, error)
}
