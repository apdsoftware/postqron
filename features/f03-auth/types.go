package auth

import (
	"context"
	"time"
)

type Provider string

const (
	ProviderGoogle   Provider = "google"
	ProviderApple    Provider = "apple"
	ProviderFacebook Provider = "facebook"
	ProviderLinkedIn Provider = "linkedin"
)

var SupportedProviders = []Provider{
	ProviderGoogle,
	ProviderApple,
	ProviderFacebook,
	ProviderLinkedIn,
}

type FlowIntent string

const (
	IntentLogin FlowIntent = "login"
	IntentLink  FlowIntent = "link"
)

type AttemptStatus string

const (
	AttemptPending   AttemptStatus = "pending"
	AttemptClaimed   AttemptStatus = "claimed"
	AttemptCompleted AttemptStatus = "completed"
	AttemptFailed    AttemptStatus = "failed"
)

type ConsentAction string

const (
	ConsentAccepted     ConsentAction = "accepted"
	ConsentAcknowledged ConsentAction = "acknowledged"
	ConsentGranted      ConsentAction = "granted"
	ConsentRejected     ConsentAction = "rejected"
	ConsentWithdrawn    ConsentAction = "withdrawn"
)

type ConsentReceipt struct {
	DocumentKey   string        `json:"document_key"`
	Version       string        `json:"version"`
	DigestSHA256  string        `json:"digest_sha256"`
	Action        ConsentAction `json:"action"`
	Purpose       string        `json:"purpose"`
	Locale        string        `json:"locale"`
	Surface       string        `json:"surface"`
	ControlTextID string        `json:"control_text_id"`
}

type OAuthAttempt struct {
	ID                     string
	StateHash              string
	PKCEVerifierCiphertext []byte
	NonceCiphertext        []byte
	Provider               Provider
	Intent                 FlowIntent
	TargetAccountID        string
	BoundSessionTokenHash  string
	ReturnTo               string
	ContractCountry        string
	Consents               []ConsentReceipt
	CorrelationID          string
	Status                 AttemptStatus
	CreatedAt              time.Time
	ExpiresAt              time.Time
	ClaimedAt              *time.Time
	CompletedAt            *time.Time
}

type ExternalIdentity struct {
	Subject         string
	Email           string
	EmailVerified   bool
	DisplayName     string
	RevocationToken string
}

type Account struct {
	ID              string
	Email           string
	NormalizedEmail string
	DisplayName     string
	ContractCountry string
	CreatedAt       time.Time
}

type ProviderIdentity struct {
	Provider                  Provider
	Subject                   string
	AccountID                 string
	Email                     string
	RevocationTokenCiphertext []byte
	LinkedAt                  time.Time
}

type Session struct {
	ID              string
	AccountID       string
	TokenHash       string
	CreatedAt       time.Time
	AuthenticatedAt time.Time
	ExpiresAt       time.Time
	RevokedAt       *time.Time
}

type ConsentEvent struct {
	ID            string
	AccountID     string
	DocumentKey   string
	Version       string
	DigestSHA256  string
	Action        ConsentAction
	Purpose       string
	Locale        string
	Country       string
	Surface       string
	ControlTextID string
	CorrelationID string
	OccurredAt    time.Time
}

type OutboxEvent struct {
	ID            string
	Type          string
	Version       int
	AggregateID   string
	CorrelationID string
	Payload       []byte
	OccurredAt    time.Time
}

type ProviderConfig struct {
	ClientID         string
	AuthorizationURL string
	RedirectURL      string
	Scopes           []string
	ExtraParameters  map[string]string
}

type ExchangeRequest struct {
	Code          string
	RedirectURL   string
	PKCEVerifier  string
	ExpectedNonce string
}

type ProviderAdapter interface {
	Config() ProviderConfig
	Exchange(context.Context, ExchangeRequest) (ExternalIdentity, error)
	Revoke(context.Context, string) error
}

type ProviderError struct {
	Code      string
	Retryable bool
	Denied    bool
	Cause     error
}

func (e *ProviderError) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Cause.Error()
	}
	return e.Code
}

func (e *ProviderError) Unwrap() error {
	return e.Cause
}

type Sealer interface {
	Seal([]byte) ([]byte, error)
	Open([]byte) ([]byte, error)
}

type TransactionStore interface {
	SaveAttempt(context.Context, OAuthAttempt) error
	ClaimAttempt(context.Context, string, time.Time) (OAuthAttempt, error)
	ReleaseAttempt(context.Context, string) error
	FailAttempt(context.Context, string, time.Time) error
	Transaction(context.Context, func(Transaction) error) error
}

type Transaction interface {
	Attempt(string) (OAuthAttempt, bool, error)
	UpdateAttempt(OAuthAttempt) error
	ProviderIdentity(Provider, string) (ProviderIdentity, bool, error)
	ProviderIdentities(string) ([]ProviderIdentity, error)
	PutProviderIdentity(ProviderIdentity) error
	DeleteProviderIdentity(Provider, string) error
	Account(string) (Account, bool, error)
	AccountByVerifiedEmail(string) (Account, bool, error)
	PutAccount(Account) error
	SessionByTokenHash(string) (Session, bool, error)
	Sessions(string) ([]Session, error)
	PutSession(Session) error
	ConsentExists(string, ConsentReceipt, string) (bool, error)
	AppendConsent(ConsentEvent) error
	AppendOutbox(OutboxEvent) error
}

type BeginRequest struct {
	Provider        Provider
	ReturnTo        string
	ContractCountry string
	Consents        []ConsentReceipt
}

type BeginLinkRequest struct {
	Provider     Provider
	ReturnTo     string
	SessionToken string
}

type Authorization struct {
	URL       string
	ExpiresAt time.Time
}

type CallbackRequest struct {
	State         string
	Code          string
	ProviderError string
}

type CallbackResult struct {
	AccountID     string
	SessionToken  string
	SessionExpiry time.Time
	Linked        bool
	Onboarding    bool
	ReturnTo      string
}

type SessionPrincipal struct {
	AccountID string
	SessionID string
	ExpiresAt time.Time
}
