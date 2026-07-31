package emailruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	email "github.com/apdsoftware/postqron/features/f14-email"
)

const (
	postqronEnvEnv               = "POSTQRON_ENV"
	mailronixEndpointEnv         = "POSTQRON_MAILRONIX_ENDPOINT"
	mailronixAPIKeySecretEnv     = "POSTQRON_MAILRONIX_API_KEY_SECRET_NAME"
	mailronixSenderEmailEnv      = "POSTQRON_MAILRONIX_SENDER_EMAIL"
	mailronixDomainVerifiedEnv   = "POSTQRON_MAILRONIX_DOMAIN_VERIFIED"
	mailronixFailureThresholdEnv = "POSTQRON_MAILRONIX_FAILURE_THRESHOLD"
	mailronixCircuitOpenForEnv   = "POSTQRON_MAILRONIX_CIRCUIT_OPEN_FOR"
)

type Service struct {
	emailService *email.Service
	database     *sql.DB
	appDomain    string
	clock        func() time.Time
}

func NewService(
	database *sql.DB,
	appDomain string,
	clock func() time.Time,
) (*Service, error) {
	if database == nil {
		return nil, errors.New("email runtime database is required")
	}
	if clock == nil {
		clock = time.Now
	}
	validatedDomain, err := validateAppDomain(appDomain)
	if err != nil {
		return nil, err
	}
	brand, err := loadBrand(validatedDomain)
	if err != nil {
		return nil, err
	}
	renderer, err := email.NewRenderer(brand)
	if err != nil {
		return nil, err
	}
	sender, err := runtimeSender()
	if err != nil {
		return nil, err
	}
	service, err := email.NewService(
		&sqlStore{database: database},
		renderer,
		sender,
		email.RetryPolicy{
			BaseDelay: 2 * time.Second,
			MaxDelay:  5 * time.Minute,
		},
	)
	if err != nil {
		return nil, err
	}
	return &Service{
		emailService: service,
		database:     database,
		appDomain:    validatedDomain,
		clock:        clock,
	}, nil
}

type SocialNotificationCommand struct {
	WorkspaceID    string
	PostID         string
	ChannelID      string
	Provider       string
	RecipientID    string
	Locale         string
	TemplateID     string
	IdempotencyKey string
}

type SocialNotificationState string

const (
	SocialNotificationPending          SocialNotificationState = "pending"
	SocialNotificationDelivered        SocialNotificationState = "delivered"
	SocialNotificationPermanentFailure SocialNotificationState = "permanent_failure"
)

// EnqueueSocialNotification is the worker-facing F14 boundary. It accepts only
// identifiers already resolved by the server-side F9 bridge; callers cannot
// supply an address, rendered copy, social content, or provider credentials.
func (service *Service) EnqueueSocialNotification(
	ctx context.Context,
	command SocialNotificationCommand,
) (string, SocialNotificationState, error) {
	if service == nil || service.emailService == nil || service.database == nil {
		return "", "", errors.New("email runtime service is not configured")
	}
	command.WorkspaceID = strings.TrimSpace(command.WorkspaceID)
	command.PostID = strings.TrimSpace(command.PostID)
	command.ChannelID = strings.TrimSpace(command.ChannelID)
	command.Provider = strings.TrimSpace(command.Provider)
	command.RecipientID = strings.TrimSpace(command.RecipientID)
	command.Locale = strings.TrimSpace(command.Locale)
	command.TemplateID = strings.TrimSpace(command.TemplateID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	if command.WorkspaceID == "" || command.PostID == "" ||
		command.ChannelID == "" || command.RecipientID == "" ||
		command.IdempotencyKey == "" {
		return "", "", errors.New("invalid social notification command")
	}
	template, err := socialTemplate(command.Provider, command.TemplateID)
	if err != nil {
		return "", "", err
	}
	recipient, err := service.resolveSocialRecipient(
		ctx,
		command.WorkspaceID,
		command.RecipientID,
	)
	if err != nil {
		return "", "", err
	}
	now := service.clock().UTC()
	result, err := service.emailService.Enqueue(ctx, email.Message{
		IdempotencyKey:    "social-notification:" + command.IdempotencyKey,
		Channel:           email.ChannelTransactional,
		Template:          template,
		TemplateVersion:   "1.0.0",
		SourceWorkspaceID: command.WorkspaceID,
		Recipient: email.Recipient{
			ID: recipient.id, Email: recipient.email, Name: recipient.name,
			Locale: recipient.locale,
		},
		Data: email.TemplateData{
			ActionURL: "https://" + service.appDomain + "/app/posts/" +
				url.PathEscape(command.PostID) + "?channel=" +
				url.QueryEscape(command.ChannelID),
			OccurredAt: now,
		},
		CreatedAt:   now,
		MaxAttempts: 5,
	})
	if err != nil {
		return "", "", err
	}
	return service.socialDeliveryState(ctx, result.ID, now)
}

type resolvedSocialRecipient struct {
	id, email, name, locale string
}

func (service *Service) resolveSocialRecipient(
	ctx context.Context,
	workspaceID, expectedRecipientID string,
) (resolvedSocialRecipient, error) {
	var recipient resolvedSocialRecipient
	err := service.database.QueryRowContext(ctx, `
		SELECT account.id, account.email, account.display_name,
		       CASE
		           WHEN lower(split_part(COALESCE(profile.locale, ''), '-', 1))
		               IN ('en', 'it', 'es', 'fr', 'de')
		           THEN lower(split_part(profile.locale, '-', 1))
		           ELSE 'en'
		       END
		  FROM f04_memberships membership
		  JOIN auth_accounts account ON account.id = membership.account_id
		  LEFT JOIN account_privacy_profiles profile
		    ON profile.account_id = membership.account_id
		 WHERE membership.workspace_id = $1
		   AND membership.status = 'active'
		   AND membership.role::text = 'owner'
		   AND account.email_verified_at IS NOT NULL
		 ORDER BY membership.account_id
		 LIMIT 1`,
		workspaceID,
	).Scan(&recipient.id, &recipient.email, &recipient.name, &recipient.locale)
	if err != nil {
		return resolvedSocialRecipient{}, fmt.Errorf(
			"resolve social notification recipient: %w",
			err,
		)
	}
	if recipient.id != expectedRecipientID {
		return resolvedSocialRecipient{}, errors.New(
			"social notification recipient changed",
		)
	}
	return recipient, nil
}

func socialTemplate(provider, templateID string) (email.TemplateID, error) {
	var expected email.TemplateID
	switch provider {
	case "facebook_groups":
		expected = email.TemplateFacebookGroupManual
	case "instagram_personal":
		expected = email.TemplateInstagramPersonalManual
	default:
		return "", errors.New("unsupported social notification provider")
	}
	if string(expected) != templateID {
		return "", errors.New("social notification template mismatch")
	}
	return expected, nil
}

func (service *Service) socialDeliveryState(
	ctx context.Context,
	deliveryID string,
	now time.Time,
) (string, SocialNotificationState, error) {
	var state string
	err := service.database.QueryRowContext(ctx, `
		SELECT state
		  FROM f14_email_deliveries
		 WHERE id = $1`,
		deliveryID,
	).Scan(&state)
	if err != nil {
		return "", "", fmt.Errorf("read social email delivery: %w", err)
	}
	if state == "sending" {
		result, updateErr := service.database.ExecContext(ctx, `
			UPDATE f14_email_deliveries
			   SET state = 'failed',
			       last_diagnostic_code = CASE
			           WHEN provider_call_started_at IS NOT NULL
			           THEN 'ambiguous_delivery'
			           ELSE 'lease_attempts_exhausted'
			       END,
			       last_diagnostic_detail = '',
			       updated_at = $2,
			       retention_until = $3,
			       lease_token = NULL,
			       locked_until = NULL,
			       provider_call_started_at = NULL
			 WHERE id = $1
			   AND state = 'sending'
			   AND locked_until <= $2
			   AND (
			       provider_call_started_at IS NOT NULL
			       OR attempt_count >= max_attempts
			   )`,
			deliveryID,
			now,
			now.AddDate(1, 0, 0),
		)
		if updateErr != nil {
			return "", "", fmt.Errorf("reconcile ambiguous social email: %w", updateErr)
		}
		if rows, rowsErr := result.RowsAffected(); rowsErr != nil {
			return "", "", rowsErr
		} else if rows == 1 {
			state = "failed"
		}
	}
	switch state {
	case "delivered":
		return deliveryID, SocialNotificationDelivered, nil
	case "accepted":
		// Mailronix contract 1.0.0 only confirms that the request was queued.
		// The same 202 response is returned for recipients later discarded by
		// suppression handling, so accepted must never advance F8 to notified.
		return deliveryID, SocialNotificationPermanentFailure, nil
	case "failed", "bounced", "complained", "suppressed":
		return deliveryID, SocialNotificationPermanentFailure, nil
	default:
		return deliveryID, SocialNotificationPending, nil
	}
}

func (service *Service) DispatchOne(ctx context.Context) (bool, error) {
	if service == nil || service.emailService == nil {
		return false, errors.New("email runtime service is not configured")
	}
	now := service.clock().UTC()
	if _, err := service.purgeExpiredDeliveries(ctx, now); err != nil {
		return false, err
	}
	return service.emailService.DispatchOne(ctx)
}

func (service *Service) purgeExpiredDeliveries(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	transaction, err := service.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin expired email purge: %w", err)
	}
	defer transaction.Rollback()
	if _, err = transaction.ExecContext(ctx, `
		UPDATE f08_meta_notification_outbox AS notification
		   SET email_delivery_id = NULL
		  FROM f14_email_deliveries AS delivery
		 WHERE notification.email_delivery_id = delivery.id
		   AND delivery.retention_until <= $1`,
		now,
	); err != nil {
		return 0, fmt.Errorf("unlink expired social email: %w", err)
	}
	if _, err = transaction.ExecContext(ctx, `
		DELETE FROM f14_email_provider_events
		 WHERE provider_message_id IN (
		     SELECT provider_message_id
		       FROM f14_email_deliveries
		      WHERE retention_until <= $1
		        AND provider_message_id IS NOT NULL
		 )`,
		now,
	); err != nil {
		return 0, fmt.Errorf("purge expired email events: %w", err)
	}
	result, err := transaction.ExecContext(ctx, `
		DELETE FROM f14_email_deliveries
		 WHERE retention_until <= $1`,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf("purge expired email deliveries: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err = transaction.Commit(); err != nil {
		return 0, fmt.Errorf("commit expired email purge: %w", err)
	}
	return count, nil
}

func loadBrand(appDomain string) (email.Brand, error) {
	tokensPath := filepath.Join("features", "f01-brand", "tokens", "tokens.json")
	file, err := os.Open(tokensPath)
	if err != nil {
		return email.Brand{}, fmt.Errorf("open F1 tokens: %w", err)
	}
	defer file.Close()
	logoURL := "https://" + appDomain + "/brand/logo-primary.svg"
	return email.LoadBrandFromF1(file, "Postqron", logoURL)
}

func validateAppDomain(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "://") || strings.ContainsAny(value, "/?#@") {
		return "", errors.New("APP_DOMAIN must be a bare host name")
	}
	parsed, err := url.Parse("https://" + value)
	if err != nil || parsed.Host == "" || parsed.Host != value || parsed.User != nil {
		return "", errors.New("APP_DOMAIN must be a bare host name")
	}
	return value, nil
}

func runtimeSender() (email.Sender, error) {
	boundary, err := runtimeSenderBoundary()
	if err != nil {
		return nil, err
	}
	return boundary.Sender, nil
}

func runtimeSenderBoundary() (email.SenderBoundary, error) {
	return email.NewSenderBoundaryFromEnv(runtimeSenderOptions(), envSecretProvider{})
}

func runtimeSenderOptions() email.SenderBoundaryOptions {
	environment := strings.ToLower(strings.TrimSpace(os.Getenv(postqronEnvEnv)))
	production := environment == "production"
	options := email.SenderBoundaryOptions{
		Environment: environment,
		Production:  production,
	}
	if production || runtimeSenderConfigured() {
		options.Mode = email.SenderModeLive
	}
	return options
}

func runtimeSenderConfigured() bool {
	return strings.TrimSpace(os.Getenv(mailronixEndpointEnv)) != "" &&
		strings.TrimSpace(os.Getenv(mailronixAPIKeySecretEnv)) != "" &&
		strings.TrimSpace(os.Getenv(mailronixSenderEmailEnv)) != "" &&
		strings.TrimSpace(os.Getenv(mailronixDomainVerifiedEnv)) != ""
}

type envSecretProvider struct{}

func (envSecretProvider) Secret(_ context.Context, name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", errors.New("secret is unavailable")
	}
	return value, nil
}
