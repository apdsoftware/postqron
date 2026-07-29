package email

// EventDefinition is the published, versioned F14 command matrix. Feature
// producers emit these commands; they never invoke Mailronix or SMTP directly.
type EventDefinition struct {
	Event              string
	Producer           string
	Recipient          string
	Template           TemplateID
	Priority           string
	LocaleSource       string
	IdempotencyPattern string
	Responsibility     string
}

var TransactionalEventMatrix = []EventDefinition{
	{"f14.welcome.v1", "F3 identity", "new account", TemplateWelcome, "normal", "recipient preference", "welcome:{account_id}", "Mailronix"},
	{"f14.account_verification_requested.v1", "F3 identity", "account owner", TemplateAccountVerification, "high", "recipient preference", "account-verification:{account_id}:{verification_request_id}", "Mailronix"},
	{"f14.workspace_invitation.v1", "F4 workspace", "invited member", TemplateWorkspaceInvitation, "high", "recipient preference", "workspace-invite:{invitation_id}", "Mailronix"},
	{"f14.account_security.v1", "F3 identity", "account owner", TemplateAccountSecurity, "critical", "recipient preference", "security:{event_id}", "Mailronix"},
	{"f14.account_linked.v1", "F3 identity", "account owner", TemplateAccountLinked, "high", "recipient preference", "account-link:{event_id}", "Mailronix"},
	{"f14.sensitive_change.v1", "F3/F12 account", "account owner", TemplateAccountSecurity, "critical", "recipient preference", "sensitive-change:{event_id}", "Mailronix"},
	{"f14.social_reconnect.v1", "F5 social", "workspace owner", TemplateSocialReconnect, "high", "recipient preference", "social-reconnect:{connection_id}:{revision}", "Mailronix"},
	{"f14.approval_requested.v1", "F17 collaboration", "approver", TemplateCollaboration, "high", "recipient preference", "approval:{approval_id}:{revision}", "Mailronix"},
	{"f14.collaboration_update.v1", "F17 collaboration", "mentioned member", TemplateCollaboration, "normal", "recipient preference", "collaboration:{event_id}:{recipient_id}", "Mailronix"},
	{"f14.publication_succeeded.v1", "F8 publishing", "post owner", TemplatePublicationSuccess, "normal", "recipient preference", "publication:{destination_id}:succeeded", "Mailronix"},
	{"f14.publication_failed.v1", "F8 publishing", "post owner", TemplatePublicationFailed, "high", "recipient preference", "publication:{destination_id}:failed:{attempt_group}", "Mailronix"},
	{"f14.publication_retry_required.v1", "F8 publishing", "post owner", TemplatePublicationFailed, "high", "recipient preference", "publication:{destination_id}:retry-required", "Mailronix"},
	{"f14.payment_failed.v1", "F10 billing", "workspace owner", TemplateBilling, "critical", "recipient preference", "billing:{paddle_event_id}:payment-failed", "Mailronix notification; Paddle owns fiscal receipt"},
	{"f14.plan_changed.v1", "F10 billing", "workspace owner", TemplateBilling, "high", "recipient preference", "billing:{paddle_event_id}:plan-changed", "Mailronix notification; Paddle owns fiscal receipt"},
	{"f14.plan_cancelled.v1", "F10 billing", "workspace owner", TemplateBilling, "high", "recipient preference", "billing:{paddle_event_id}:plan-cancelled", "Mailronix notification; Paddle owns fiscal receipt"},
	{"f14.grace_period.v1", "F10 billing", "workspace owner", TemplateBilling, "critical", "recipient preference", "billing:{subscription_id}:grace:{revision}", "Mailronix; no Paddle receipt duplication"},
	{"f14.data_export_ready.v1", "F12 privacy", "requesting account", TemplateDataExportReady, "high", "recipient preference", "privacy-export:{export_id}:ready", "Mailronix"},
	{"f14.deletion_scheduled.v1", "F12 privacy", "account/workspace owner", TemplateDeletion, "critical", "recipient preference", "deletion:{request_id}:scheduled", "Mailronix"},
	{"f14.deletion_completed.v1", "F12 privacy", "former account address", TemplateDeletion, "critical", "locale captured with request", "deletion:{request_id}:completed", "Mailronix"},
	{"f14.privacy_request_received.v1", "F12 privacy", "requesting account", TemplatePrivacyRequest, "high", "recipient preference", "privacy-request:{request_id}:received", "Mailronix"},
	{"f14.prelaunch_access.v1", "F2 public site", "requesting address", TemplatePrelaunchAccess, "normal", "locale captured with request", "prelaunch:{request_id}:confirmed", "Mailronix"},
	{"f14.operational_alert.v1", "F15 operations", "affected user", TemplateOperationalAlert, "critical", "recipient preference", "operational:{alert_id}:{recipient_id}", "Mailronix"},
}
