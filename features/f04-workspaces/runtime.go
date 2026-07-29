package workspaces

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AppLocale string

const (
	LocaleEN AppLocale = "en"
	LocaleIT AppLocale = "it"
	LocaleES AppLocale = "es"
	LocaleFR AppLocale = "fr"
	LocaleDE AppLocale = "de"
)

type AppSessionAccount struct {
	ID              string
	DisplayName     string
	Email           string
	Locale          AppLocale
	ContractCountry string
}

type AppWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role Role   `json:"role"`
}

type AppSession struct {
	Account struct {
		ID          string    `json:"id"`
		DisplayName string    `json:"display_name"`
		Email       string    `json:"email,omitempty"`
		Locale      AppLocale `json:"locale"`
	} `json:"account"`
	OnboardingRequired bool           `json:"onboarding_required"`
	CurrentWorkspace   *AppWorkspace  `json:"current_workspace,omitempty"`
	Workspaces         []AppWorkspace `json:"workspaces"`
}

type OnboardingConsentReceipt struct {
	DocumentKey   string    `json:"document_key"`
	Version       string    `json:"version"`
	DigestSHA256  string    `json:"digest_sha256"`
	Action        string    `json:"action"`
	Locale        AppLocale `json:"locale"`
	Purpose       string    `json:"purpose"`
	Surface       string    `json:"surface"`
	ControlTextID string    `json:"control_text_id"`
}

type OnboardingWorkspaceInput struct {
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

type CompleteOnboardingCommand struct {
	Account   AppSessionAccount
	Consents  []OnboardingConsentReceipt
	Workspace OnboardingWorkspaceInput
}

type OnboardingRequiredEvent struct {
	AccountID            string
	Email                string
	DisplayName          string
	ContractCountry      string
	PersonalWorkspaceKey string
	RequestedRole        string
	IdempotencyKey       string
	OccurredAt           time.Time
}

type RuntimeStore interface {
	AppSession(context.Context, AppSessionAccount) (AppSession, error)
	CompleteOnboarding(context.Context, CompleteOnboardingCommand, time.Time) (AppSession, bool, error)
	SelectWorkspace(context.Context, AppSessionAccount, string, time.Time) error
	CurrentWorkspace(context.Context, string) (Workspace, Role, error)
	CurrentMemberships(context.Context, string) ([]Membership, error)
	ConsumeOnboardingRequired(context.Context, OnboardingRequiredEvent, time.Time) (Workspace, bool, error)
}

type RuntimeService struct {
	store RuntimeStore
	now   func() time.Time
}

func NewRuntimeService(store RuntimeStore) (*RuntimeService, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: runtime store is required", ErrInvalidArgument)
	}
	return &RuntimeService{
		store: store,
		now:   time.Now,
	}, nil
}

func NewRuntimeServiceWithClock(
	store RuntimeStore,
	clock func() time.Time,
) (*RuntimeService, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: runtime store is required", ErrInvalidArgument)
	}
	if clock == nil {
		clock = time.Now
	}
	return &RuntimeService{store: store, now: clock}, nil
}

var (
	ErrInvalidConsentReceipt = errors.New("invalid onboarding consent receipt")
	ErrConsentOutdated       = errors.New("onboarding consent no longer matches the current legal document")
	ErrRuntimeUnavailable    = errors.New("runtime onboarding dependencies unavailable")
)

func (service *RuntimeService) AppSession(
	ctx context.Context,
	account AppSessionAccount,
) (AppSession, error) {
	if err := validateAppSessionAccount(account); err != nil {
		return AppSession{}, err
	}
	return service.store.AppSession(ctx, normalizeSessionAccount(account))
}

func (service *RuntimeService) CompleteOnboarding(
	ctx context.Context,
	command CompleteOnboardingCommand,
) (AppSession, bool, error) {
	command.Account = normalizeSessionAccount(command.Account)
	if err := validateAppSessionAccount(command.Account); err != nil {
		return AppSession{}, false, err
	}
	if err := validateOnboardingWorkspace(command.Workspace); err != nil {
		return AppSession{}, false, err
	}
	if err := validateOnboardingConsents(command.Consents); err != nil {
		return AppSession{}, false, err
	}
	return service.store.CompleteOnboarding(ctx, command, service.now().UTC())
}

func (service *RuntimeService) SelectWorkspace(
	ctx context.Context,
	account AppSessionAccount,
	workspaceID string,
) error {
	account = normalizeSessionAccount(account)
	if err := validateAppSessionAccount(account); err != nil {
		return err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return fmt.Errorf("%w: workspace id is required", ErrInvalidArgument)
	}
	return service.store.SelectWorkspace(ctx, account, workspaceID, service.now().UTC())
}

func (service *RuntimeService) CurrentWorkspace(
	ctx context.Context,
	accountID string,
) (Workspace, Role, error) {
	if strings.TrimSpace(accountID) == "" {
		return Workspace{}, "", ErrUnauthenticated
	}
	return service.store.CurrentWorkspace(ctx, accountID)
}

func (service *RuntimeService) CurrentMemberships(
	ctx context.Context,
	accountID string,
) ([]Membership, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, ErrUnauthenticated
	}
	return service.store.CurrentMemberships(ctx, accountID)
}

func (service *RuntimeService) ConsumeOnboardingRequired(
	ctx context.Context,
	event OnboardingRequiredEvent,
) (Workspace, bool, error) {
	event.AccountID = strings.TrimSpace(event.AccountID)
	event.Email = strings.TrimSpace(event.Email)
	event.DisplayName = strings.TrimSpace(event.DisplayName)
	event.ContractCountry = strings.ToUpper(strings.TrimSpace(event.ContractCountry))
	event.PersonalWorkspaceKey = strings.TrimSpace(event.PersonalWorkspaceKey)
	event.RequestedRole = strings.ToLower(strings.TrimSpace(event.RequestedRole))
	event.IdempotencyKey = strings.TrimSpace(event.IdempotencyKey)
	if event.AccountID == "" ||
		event.ContractCountry != "IT" ||
		!strings.HasPrefix(event.PersonalWorkspaceKey, "personal:") ||
		event.RequestedRole != "owner" ||
		!strings.HasPrefix(event.IdempotencyKey, "auth-account:") {
		return Workspace{}, false, fmt.Errorf("%w: onboarding event is malformed", ErrInvalidArgument)
	}
	return service.store.ConsumeOnboardingRequired(ctx, event, service.now().UTC())
}

func normalizeSessionAccount(account AppSessionAccount) AppSessionAccount {
	account.ID = strings.TrimSpace(account.ID)
	account.DisplayName = strings.TrimSpace(account.DisplayName)
	account.Email = strings.TrimSpace(account.Email)
	account.ContractCountry = strings.ToUpper(strings.TrimSpace(account.ContractCountry))
	switch account.Locale {
	case LocaleIT, LocaleES, LocaleFR, LocaleDE:
	default:
		account.Locale = LocaleEN
	}
	return account
}

func validateAppSessionAccount(account AppSessionAccount) error {
	if account.ID == "" {
		return ErrUnauthenticated
	}
	if account.ContractCountry != "" && account.ContractCountry != "IT" {
		return fmt.Errorf("%w: unsupported contract country", ErrInvalidArgument)
	}
	return nil
}

func validateOnboardingWorkspace(workspace OnboardingWorkspaceInput) error {
	switch workspace.Mode {
	case "create":
		name := strings.TrimSpace(workspace.Name)
		if name == "" || len(name) > 80 {
			return fmt.Errorf("%w: workspace name is required", ErrInvalidArgument)
		}
	case "select":
		if strings.TrimSpace(workspace.ID) == "" {
			return fmt.Errorf("%w: workspace id is required", ErrInvalidArgument)
		}
	default:
		return fmt.Errorf("%w: unsupported onboarding workspace mode", ErrInvalidArgument)
	}
	return nil
}

func validateOnboardingConsents(receipts []OnboardingConsentReceipt) error {
	if len(receipts) != 2 {
		return fmt.Errorf("%w: exactly two onboarding consents are required", ErrInvalidConsentReceipt)
	}
	seen := make(map[string]struct{}, len(receipts))
	for _, receipt := range receipts {
		receipt.DocumentKey = strings.TrimSpace(receipt.DocumentKey)
		receipt.Version = strings.TrimSpace(receipt.Version)
		receipt.DigestSHA256 = strings.TrimSpace(receipt.DigestSHA256)
		receipt.Action = strings.TrimSpace(receipt.Action)
		receipt.Purpose = strings.TrimSpace(receipt.Purpose)
		receipt.Surface = strings.TrimSpace(receipt.Surface)
		receipt.ControlTextID = strings.TrimSpace(receipt.ControlTextID)
		if receipt.DocumentKey == "" ||
			receipt.Version == "" ||
			!validSHA256(receipt.DigestSHA256) ||
			receipt.Action != "accepted" ||
			receipt.Purpose == "" ||
			receipt.Surface != "app_onboarding" ||
			receipt.ControlTextID == "" {
			return ErrInvalidConsentReceipt
		}
		switch receipt.Locale {
		case LocaleEN, LocaleIT, LocaleES, LocaleFR, LocaleDE:
		default:
			return ErrInvalidConsentReceipt
		}
		switch receipt.DocumentKey {
		case "terms":
			if receipt.Purpose != "contract" {
				return ErrInvalidConsentReceipt
			}
		case "privacy":
			if receipt.Purpose != "privacy_acknowledgement" {
				return ErrInvalidConsentReceipt
			}
		default:
			return ErrInvalidConsentReceipt
		}
		if _, duplicate := seen[receipt.DocumentKey]; duplicate {
			return ErrInvalidConsentReceipt
		}
		seen[receipt.DocumentKey] = struct{}{}
	}
	return nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
