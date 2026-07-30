package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	workspaces "github.com/apdsoftware/postqron/features/f04-workspaces"
	socialconnections "github.com/apdsoftware/postqron/features/f05-social-connections"
	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/worker/internal/emailruntime"
	"github.com/apdsoftware/postqron/services/worker/internal/privacyruntime"
	"github.com/apdsoftware/postqron/services/worker/internal/publishingruntime"
)

var newWorkspaceRuntimeService = func(
	database *sql.DB,
	clock func() time.Time,
) (workspaceOnboardingRuntime, error) {
	repository, err := workspaces.NewPostgresRepository(database)
	if err != nil {
		return nil, err
	}
	return workspaces.NewRuntimeServiceWithClock(repository, clock)
}

type workspaceOnboardingRuntime interface {
	ConsumeOnboardingRequired(
		context.Context,
		workspaces.OnboardingRequiredEvent,
	) (workspaces.Workspace, bool, error)
}

type Runner struct {
	features        []featureruntime.Feature
	database        *sql.DB
	interval        time.Duration
	clock           func() time.Time
	logger          *slog.Logger
	email           *emailruntime.Service
	privacy         *privacyruntime.Service
	publishing      publishingDispatcher
	closePublishing func()
}

type publishingDispatcher interface {
	DispatchOne(context.Context) (bool, error)
}

func New(features []featureruntime.Feature, interval time.Duration, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		features: features,
		interval: interval,
		clock:    time.Now,
		logger:   logger,
	}
}

func NewRuntime(
	features []featureruntime.Feature,
	database *sql.DB,
	databaseURL string,
	appDomain string,
	interval time.Duration,
	clock func() time.Time,
	logger *slog.Logger,
) (*Runner, error) {
	return NewRuntimeWithExecutor(
		features, database, databaseURL, appDomain, interval, clock,
		logger, nil,
	)
}

// NewRuntimeWithExecutor is the real worker composition root for F5→F8.
// Passing nil keeps publishing fail-closed; enabling any F8 static provider
// without a bootstrapped public F5 AuthenticatedExecutor fails startup.
func NewRuntimeWithExecutor(
	features []featureruntime.Feature,
	database *sql.DB,
	databaseURL string,
	appDomain string,
	interval time.Duration,
	clock func() time.Time,
	logger *slog.Logger,
	executor *socialconnections.AuthenticatedExecutor,
) (*Runner, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = time.Now
	}
	emailService, err := emailruntime.NewService(database, appDomain, clock)
	if err != nil {
		return nil, err
	}
	privacyService, err := privacyruntime.New(
		database,
		os.Getenv("POSTQRON_PRIVACY_ARTIFACT_DIR"),
		clock,
		logger,
	)
	if err != nil {
		return nil, err
	}
	publishingService, err := publishingruntime.NewWithExecutor(
		context.Background(),
		database,
		databaseURL,
		clock,
		executor,
	)
	if err != nil {
		return nil, err
	}
	return &Runner{
		features:        features,
		database:        database,
		interval:        interval,
		clock:           clock,
		logger:          logger,
		email:           emailService,
		privacy:         privacyService,
		publishing:      publishingService,
		closePublishing: publishingService.Close,
	}, nil
}

func (r *Runner) Run(ctx context.Context) {
	r.Tick(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.Tick(ctx)
		}
	}
}

func (r *Runner) Tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if r.publishing != nil {
		if dispatched, err := r.publishing.DispatchOne(ctx); err != nil {
			r.logger.Error("worker F8 publishing dispatch failed", "error", err)
		} else if dispatched {
			r.logger.Info("worker F8 publishing dispatch processed")
		}
	}
	if r.database != nil && r.email != nil {
		if processed, err := r.processOnboardingEvent(ctx); err != nil {
			r.logger.Error("worker onboarding bridge failed", "error", err)
		} else if processed {
			r.logger.Info("worker onboarding bridge processed")
		}
		if dispatched, err := r.email.DispatchOne(ctx); err != nil {
			r.logger.Error("worker email dispatch failed", "error", err)
		} else if dispatched {
			r.logger.Info("worker email dispatch processed")
		}
		r.privacy.Tick(ctx)
	}
	for _, feature := range r.features {
		r.logger.Info(
			"worker feature tick",
			"feature", feature.Manifest.ID,
			"version", feature.Manifest.Version,
		)
	}
}

func (r *Runner) Close() {
	if r != nil && r.closePublishing != nil {
		r.closePublishing()
	}
}

type onboardingEvent struct {
	AccountID            string    `json:"account_id"`
	Email                string    `json:"email"`
	DisplayName          string    `json:"display_name"`
	ContractCountry      string    `json:"contract_country"`
	PersonalWorkspaceKey string    `json:"personal_workspace_key"`
	RequestedRole        string    `json:"requested_role"`
	IdempotencyKey       string    `json:"idempotency_key"`
	OccurredAt           time.Time `json:"occurred_at"`
}

func (r *Runner) processOnboardingEvent(ctx context.Context) (bool, error) {
	if r.database == nil {
		return false, errors.New("worker database is unavailable")
	}
	row := r.database.QueryRowContext(ctx, `
		WITH claimed AS (
			SELECT id, payload, attempts
			  FROM auth_outbox_events
			 WHERE event_type = 'auth.account.onboarding-required'
			   AND event_version = 1
			   AND published_at IS NULL
			 ORDER BY occurred_at, id
			 FOR UPDATE SKIP LOCKED
			 LIMIT 1
		)
		UPDATE auth_outbox_events event
		   SET attempts = claimed.attempts + 1
		  FROM claimed
		 WHERE event.id = claimed.id
		RETURNING event.id, claimed.payload`,
	)
	var (
		eventID string
		payload []byte
	)
	if err := row.Scan(&eventID, &payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("claim onboarding event: %w", err)
	}
	var event onboardingEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		_, _ = r.database.ExecContext(
			ctx,
			`UPDATE auth_outbox_events SET last_error_code = $2 WHERE id = $1`,
			eventID,
			"invalid_payload",
		)
		return true, fmt.Errorf("decode onboarding event: %w", err)
	}
	service, err := newWorkspaceRuntimeService(r.database, r.clock)
	if err != nil {
		return true, err
	}
	workspace, _, err := service.ConsumeOnboardingRequired(ctx, workspaces.OnboardingRequiredEvent{
		AccountID:            event.AccountID,
		Email:                event.Email,
		DisplayName:          event.DisplayName,
		ContractCountry:      event.ContractCountry,
		PersonalWorkspaceKey: event.PersonalWorkspaceKey,
		RequestedRole:        event.RequestedRole,
		IdempotencyKey:       event.IdempotencyKey,
		OccurredAt:           event.OccurredAt,
	})
	if err == nil {
		_, err = r.database.ExecContext(
			ctx,
			`INSERT INTO account_privacy_profiles (
				account_id, display_name, locale, timezone, updated_at
			) VALUES ($1, $2, 'it-IT', 'Europe/Rome', $3)
			ON CONFLICT (account_id) DO NOTHING`,
			event.AccountID,
			accountProfileDisplayName(event.DisplayName, event.Email),
			r.clock().UTC(),
		)
	}
	if err == nil {
		err = r.database.QueryRowContext(
			ctx,
			`SELECT f10_provision_trial($1::text, $2)`,
			workspace.ID,
			event.OccurredAt.UTC(),
		).Scan(new(bool))
	}
	if err != nil {
		_, _ = r.database.ExecContext(
			ctx,
			`UPDATE auth_outbox_events SET last_error_code = $2 WHERE id = $1`,
			eventID,
			"bridge_failed",
		)
		return true, err
	}
	_, err = r.database.ExecContext(
		ctx,
		`UPDATE auth_outbox_events
		    SET published_at = $2,
		        last_error_code = NULL
		  WHERE id = $1`,
		eventID,
		r.clock().UTC(),
	)
	if err != nil {
		return true, fmt.Errorf("mark onboarding event published: %w", err)
	}
	return true, nil
}

func accountProfileDisplayName(displayName, email string) string {
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = strings.TrimSpace(email)
		if localPart, _, found := strings.Cut(name, "@"); found {
			name = strings.TrimSpace(localPart)
		}
	}
	if name == "" {
		name = "Postqron user"
	}
	if utf8.RuneCountInString(name) <= 100 {
		return name
	}
	return string([]rune(name)[:100])
}
