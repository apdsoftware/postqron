package operations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
)

type AuditAction string

const (
	AuditLoginFailed          AuditAction = "auth.login_failed"
	AuditSocialConnected      AuditAction = "social.connected"
	AuditSocialDisconnected   AuditAction = "social.disconnected"
	AuditWorkspaceRoleChanged AuditAction = "workspace.role_changed"
	AuditInternalPlanAssigned AuditAction = "plan.internal_assigned"
	AuditInternalPlanRevoked  AuditAction = "plan.internal_revoked"
	AuditExportRequested      AuditAction = "privacy.export_requested"
	AuditExportDownloaded     AuditAction = "privacy.export_downloaded"
	AuditDeletionRequested    AuditAction = "privacy.deletion_requested"
	AuditDeletionCancelled    AuditAction = "privacy.deletion_cancelled"
	AuditDeletionCompleted    AuditAction = "privacy.deletion_completed"
	AuditSecretRotated        AuditAction = "security.secret_rotated"
	AuditRateLimitTriggered   AuditAction = "security.rate_limit_triggered"
	AuditRestoreStarted       AuditAction = "recovery.restore_started"
	AuditRestoreCompleted     AuditAction = "recovery.restore_completed"
	AuditRestoreFailed        AuditAction = "recovery.restore_failed"
)

var (
	opaqueIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	sourceIPHashPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	allowedAuditActions     = []AuditAction{
		AuditLoginFailed,
		AuditSocialConnected,
		AuditSocialDisconnected,
		AuditWorkspaceRoleChanged,
		AuditInternalPlanAssigned,
		AuditInternalPlanRevoked,
		AuditExportRequested,
		AuditExportDownloaded,
		AuditDeletionRequested,
		AuditDeletionCancelled,
		AuditDeletionCompleted,
		AuditSecretRotated,
		AuditRateLimitTriggered,
		AuditRestoreStarted,
		AuditRestoreCompleted,
		AuditRestoreFailed,
	}
	allowedAuditOutcomes = []string{"attempted", "denied", "failed", "succeeded"}
)

// AuditEvent intentionally has no arbitrary payload. Opaque internal IDs give
// investigators correlation without copying content, credentials, or raw PII.
type AuditEvent struct {
	ID            string
	OccurredAt    time.Time
	ActorType     string
	ActorID       string
	WorkspaceID   string
	Action        AuditAction
	TargetType    string
	TargetID      string
	Outcome       string
	CorrelationID string
	SourceIPHash  string
}

func (event AuditEvent) Validate() error {
	required := map[string]string{
		"id":             event.ID,
		"actor_type":     event.ActorType,
		"actor_id":       event.ActorID,
		"action":         string(event.Action),
		"target_type":    event.TargetType,
		"target_id":      event.TargetID,
		"outcome":        event.Outcome,
		"correlation_id": event.CorrelationID,
	}
	for field, value := range required {
		if !opaqueIdentifierPattern.MatchString(value) {
			return fmt.Errorf("%s must be a non-personal opaque identifier", field)
		}
	}
	if event.OccurredAt.IsZero() {
		return errors.New("occurred_at is required")
	}
	if !slices.Contains(allowedAuditActions, event.Action) {
		return errors.New("action is not an allowlisted sensitive event")
	}
	if !slices.Contains(allowedAuditOutcomes, event.Outcome) {
		return errors.New("outcome must be attempted, denied, failed, or succeeded")
	}
	if event.WorkspaceID != "" && !opaqueIdentifierPattern.MatchString(event.WorkspaceID) {
		return errors.New("workspace_id must be an opaque identifier")
	}
	if event.SourceIPHash != "" {
		if !sourceIPHashPattern.MatchString(event.SourceIPHash) {
			return errors.New("source_ip_hash must be a salted SHA-256 digest")
		}
	}
	return nil
}

type AuditSink interface {
	Append(context.Context, AuditEvent) error
}

type AuditRecorder struct {
	metrics *Metrics
	sink    AuditSink
}

func NewAuditRecorder(sink AuditSink, metrics *Metrics) (*AuditRecorder, error) {
	if sink == nil {
		return nil, errors.New("audit sink is required")
	}
	if metrics == nil {
		metrics = &Metrics{}
	}
	return &AuditRecorder{sink: sink, metrics: metrics}, nil
}

// Record is fail-closed: callers receive validation and persistence errors and
// must not report a sensitive operation as successful when its audit write
// failed.
func (recorder *AuditRecorder) Record(ctx context.Context, event AuditEvent) error {
	if err := event.Validate(); err != nil {
		recorder.metrics.RecordAuditWriteFailure()
		return fmt.Errorf("validate sensitive audit event: %w", err)
	}
	if err := recorder.sink.Append(ctx, event); err != nil {
		recorder.metrics.RecordAuditWriteFailure()
		return fmt.Errorf("append sensitive audit event: %w", err)
	}
	return nil
}
