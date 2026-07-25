package prelaunch

import "time"

const (
	AccessConsentPolicyVersion  = "prelaunch-access-v1"
	ConfirmationEvent           = "f14.prelaunch_access.v1"
	ConfirmationTemplate        = "prelaunch_access"
	ConfirmationTemplateVersion = "1.0.0"
)

type AccessRequest struct {
	Email                string `json:"email"`
	Locale               string `json:"locale"`
	AccessConsent        bool   `json:"access_consent"`
	MarketingConsent     bool   `json:"marketing_consent"`
	ConsentPolicyVersion string `json:"consent_policy_version"`
}

type ConsentProof struct {
	PolicyVersion    string    `json:"policy_version"`
	ConsentedAt      time.Time `json:"consented_at"`
	CollectionPoint  string    `json:"collection_point"`
	AccessConsent    bool      `json:"access_consent"`
	MarketingConsent bool      `json:"marketing_consent"`
}

type EmailRecipient struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Locale string `json:"locale"`
}

type EmailData struct {
	OccurredAt string `json:"occurred_at"`
}

// TransactionalEmailCommand mirrors the published F14 producer contract. F34
// records this command in its durable outbox and never calls Mailronix or a
// marketing channel directly.
type TransactionalEmailCommand struct {
	Event           string         `json:"event"`
	IdempotencyKey  string         `json:"idempotency_key"`
	Channel         string         `json:"channel"`
	TemplateID      string         `json:"template_id"`
	TemplateVersion string         `json:"template_version"`
	Recipient       EmailRecipient `json:"recipient"`
	Data            EmailData      `json:"data"`
	OccurredAt      string         `json:"occurred_at"`
}

type Submission struct {
	ID          string
	Email       string
	EmailHash   string
	Locale      string
	Consent     ConsentProof
	Command     TransactionalEmailCommand
	RequestedAt time.Time
}

type SubmitResult struct {
	RequestID string
	Created   bool
}
