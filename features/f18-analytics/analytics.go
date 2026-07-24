// Package analytics synchronizes authorized social insights and exposes
// channel summaries without coupling to provider adapters or a central
// registry.
package analytics

import (
	"context"
	"errors"
	"time"
)

const FeatureID = "f18-analytics"

type ChannelType string

const (
	ChannelFacebookPage          ChannelType = "facebook_page"
	ChannelInstagramProfessional ChannelType = "instagram_professional"
)

type MetricName string

const (
	MetricReach    MetricName = "reach"
	MetricLikes    MetricName = "likes"
	MetricComments MetricName = "comments"
	MetricShares   MetricName = "shares"
	MetricSaved    MetricName = "saved"
	MetricViews    MetricName = "views"
	MetricPlays    MetricName = "plays"
)

type MetricState string

const (
	MetricAvailable         MetricState = "available"
	MetricUnavailable       MetricState = "unavailable"
	MetricPermissionMissing MetricState = "permission_missing"
	MetricFailed            MetricState = "failed"
	MetricMixed             MetricState = "mixed"
)

type TargetState string

const (
	TargetPending           TargetState = "pending"
	TargetSyncing           TargetState = "syncing"
	TargetRetryWait         TargetState = "retry_wait"
	TargetCurrent           TargetState = "current"
	TargetUnavailable       TargetState = "unavailable"
	TargetPermissionMissing TargetState = "permission_missing"
	TargetFailed            TargetState = "failed"
)

// PublishedContent is the read-only F8 boundary. RemoteID is the provider ID
// persisted for a successfully published destination; no token is copied.
type PublishedContent struct {
	WorkspaceID  string
	ContentID    string
	ChannelID    string
	ChannelType  ChannelType
	Provider     string
	ConnectionID string
	RemoteID     string
	PublishedAt  time.Time
}

type SyncTarget struct {
	ID                  string      `json:"id"`
	WorkspaceID         string      `json:"workspace_id"`
	ContentID           string      `json:"content_id"`
	ChannelID           string      `json:"channel_id"`
	ChannelType         ChannelType `json:"channel_type"`
	Provider            string      `json:"provider"`
	ConnectionID        string      `json:"connection_id"`
	RemoteID            string      `json:"remote_id"`
	PublishedAt         time.Time   `json:"published_at"`
	Cursor              string      `json:"-"`
	State               TargetState `json:"state"`
	AttemptCount        int         `json:"attempt_count"`
	ConsecutiveFailures int         `json:"consecutive_failures"`
	NextSyncAt          time.Time   `json:"next_sync_at"`
	LeaseToken          string      `json:"-"`
	LockedUntil         *time.Time  `json:"-"`
	LastErrorCode       string      `json:"last_error_code,omitempty"`
	LastErrorAt         *time.Time  `json:"last_error_at,omitempty"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
}

// Observation always carries a state. Value is present only for available
// observations, so an integer zero cannot be confused with missing data.
type Observation struct {
	TargetID     string      `json:"target_id"`
	Metric       MetricName  `json:"metric"`
	OriginalName string      `json:"original_name"`
	Period       string      `json:"period"`
	ObservedAt   time.Time   `json:"observed_at"`
	Value        *int64      `json:"value"`
	State        MetricState `json:"state"`
	APIVersion   string      `json:"api_version,omitempty"`
	ReasonCode   string      `json:"reason_code,omitempty"`
}

type ProviderMetric struct {
	Metric       MetricName
	OriginalName string
	Period       string
	ObservedAt   time.Time
	Value        *int64
	State        MetricState
	APIVersion   string
}

type FetchRequest struct {
	WorkspaceID  string
	ContentID    string
	ChannelID    string
	ChannelType  ChannelType
	ConnectionID string
	RemoteID     string
	Cursor       string
	Metrics      []MetricName
}

type FetchResult struct {
	Metrics    []ProviderMetric
	NextCursor string
}

// ProviderAdapter is resolved through F16 discovery. Implementations translate
// normalized metric names to the versioned provider API and must return one
// state for every requested metric.
type ProviderAdapter interface {
	Fetch(context.Context, FetchRequest) (FetchResult, error)
}

type ProviderResolver interface {
	ResolveAnalyticsProvider(context.Context, string) (ProviderAdapter, error)
}

type PermissionReader interface {
	GrantedPermissions(context.Context, string, string) (map[string]bool, error)
}

type WorkPriority string

const PriorityAnalytics WorkPriority = "analytics_low"

// RateLimiter protects publication capacity. A positive delay means analytics
// must be deferred without calling the provider.
type RateLimiter interface {
	Reserve(context.Context, string, string, WorkPriority) (time.Duration, error)
}

type ViewerAuthorizer interface {
	CanViewAnalytics(context.Context, string, string) error
}

type RegisterResult struct {
	TargetID string `json:"target_id"`
	Created  bool   `json:"created"`
}

type OverviewQuery struct {
	WorkspaceID string
	ActorID     string
	ChannelIDs  []string
	From        time.Time
	To          time.Time
}

type StateCounts struct {
	Available         int `json:"available"`
	Unavailable       int `json:"unavailable"`
	PermissionMissing int `json:"permission_missing"`
	Failed            int `json:"failed"`
}

type MetricSummary struct {
	Metric  MetricName  `json:"metric"`
	State   MetricState `json:"state"`
	Value   *int64      `json:"value"`
	Targets StateCounts `json:"targets"`
}

type ChannelOverview struct {
	ChannelID    string          `json:"channel_id"`
	ChannelType  ChannelType     `json:"channel_type"`
	ContentCount int             `json:"content_count"`
	Metrics      []MetricSummary `json:"metrics"`
}

type Overview struct {
	From     time.Time         `json:"from"`
	To       time.Time         `json:"to"`
	Channels []ChannelOverview `json:"channels"`
}

type Repository interface {
	Register(context.Context, SyncTarget) (RegisterResult, error)
	ClaimDue(context.Context, time.Time, time.Time, string) (SyncTarget, bool, error)
	SaveSuccess(
		context.Context,
		string,
		string,
		[]Observation,
		string,
		TargetState,
		time.Time,
		time.Time,
	) error
	SaveRetry(
		context.Context,
		string,
		string,
		string,
		time.Time,
		time.Time,
	) error
	Defer(context.Context, string, string, time.Time, time.Time) error
	SaveFailure(
		context.Context,
		string,
		string,
		[]Observation,
		string,
		time.Time,
	) error
	Overview(context.Context, OverviewQuery) (Overview, error)
}

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrForbidden       = errors.New("operation forbidden")
	ErrNotFound        = errors.New("resource not found")
	ErrConflict        = errors.New("resource conflict")
)
