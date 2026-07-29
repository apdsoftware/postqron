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

	workspaces "github.com/apdsoftware/postqron/features/f04-workspaces"
	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/apdsoftware/postqron/services/worker/internal/emailruntime"
	"github.com/apdsoftware/postqron/services/worker/internal/privacyruntime"
)

type Runner struct {
	features []featureruntime.Feature
	database *sql.DB
	interval time.Duration
	clock    func() time.Time
	logger   *slog.Logger
	email    *emailruntime.Service
	privacy  *privacyruntime.Service
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
	appDomain string,
	interval time.Duration,
	clock func() time.Time,
	logger *slog.Logger,
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
	return &Runner{
		features: features,
		database: database,
		interval: interval,
		clock:    clock,
		logger:   logger,
		email:    emailService,
		privacy:  privacyService,
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
	repository, err := workspaces.NewPostgresRepository(r.database)
	if err != nil {
		return true, err
	}
	service, err := workspaces.NewRuntimeServiceWithClock(repository, r.clock)
	if err != nil {
		return true, err
	}
	_, _, err = service.ConsumeOnboardingRequired(ctx, workspaces.OnboardingRequiredEvent{
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
			strings.TrimSpace(event.DisplayName),
			r.clock().UTC(),
		)
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
