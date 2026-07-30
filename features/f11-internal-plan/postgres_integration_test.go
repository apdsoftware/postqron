package internalplan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresAssignmentRevocationAuditAndPublicIsolation(t *testing.T) {
	pool := internalPlanIntegrationPool(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	repository := NewSQLRepository(pool)
	repository.auditTable = "sensitive_audit_events"
	service, err := NewService(repository, &adminAuthorizerStub{
		admins: map[string]bool{adminID: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	eventNumber := 0
	service.newID = func() (string, error) {
		eventNumber++
		return fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-%012d", eventNumber), nil
	}
	principal := Principal{AccountID: adminID, StronglyAuthenticated: true}

	provisionTrial(t, pool, workspaceID, now)
	if _, err := pool.Exec(ctx, `
		INSERT INTO f11_internal_plan_allowlist (
			account_id,
			workspace_id,
			active,
			allowed_at,
			allowed_by_account_id
		) VALUES ($1, $2, true, $3, $4)
	`, targetID, workspaceID, now, adminID); err != nil {
		t.Fatal(err)
	}

	assigned, err := service.Assign(ctx, principal, AssignmentRequest{
		WorkspaceID:     workspaceID,
		TargetAccountID: targetID,
		CorrelationID:   "case-17-postgres-assign",
	})
	if err != nil || !assigned.Changed || !assigned.Active {
		t.Fatalf("assignment = %#v, %v", assigned, err)
	}

	var (
		overrideActive bool
		publicPlan     string
	)
	if err := pool.QueryRow(ctx, `
		SELECT active
		  FROM f10_internal_entitlement_overrides
		 WHERE workspace_id = $1
	`, workspaceID).Scan(&overrideActive); err != nil {
		t.Fatal(err)
	}
	if !overrideActive {
		t.Fatal("F10 enforcement override was not activated")
	}
	var (
		largeReservationAccepted bool
		quotaLimitIsUnlimited    bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT accepted, quota_limit IS NULL
		  FROM f10_apply_usage($1, 'channels', 1000, $2, $3)
	`, workspaceID, "case-17-unlimited-capacity", now).Scan(
		&largeReservationAccepted,
		&quotaLimitIsUnlimited,
	); err != nil {
		t.Fatal(err)
	}
	if !largeReservationAccepted || !quotaLimitIsUnlimited {
		t.Fatalf(
			"unlimited enforcement = accepted %v, null limit %v",
			largeReservationAccepted,
			quotaLimitIsUnlimited,
		)
	}
	if err := pool.QueryRow(ctx, `
		SELECT plan_code
		  FROM f10_public_entitlement_usage
		 WHERE workspace_id = $1
		 LIMIT 1
	`, workspaceID).Scan(&publicPlan); err != nil {
		t.Fatal(err)
	}
	if publicPlan != "team" {
		t.Fatalf("public plan = %q, want unchanged public Team trial", publicPlan)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE f11_internal_plan_allowlist
		   SET active = false,
		       revoked_at = $3,
		       revoked_by_account_id = $4
		 WHERE account_id = $1
		   AND workspace_id = $2
	`, targetID, workspaceID, now.Add(time.Minute), adminID); err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(2 * time.Minute) }
	revoked, err := service.Revoke(ctx, principal, RevocationRequest{
		WorkspaceID:   workspaceID,
		CorrelationID: "case-17-postgres-revoke",
	})
	if err != nil || !revoked.Changed || revoked.Active {
		t.Fatalf("revocation = %#v, %v", revoked, err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT active
		  FROM f10_internal_entitlement_overrides
		 WHERE workspace_id = $1
	`, workspaceID).Scan(&overrideActive); err != nil {
		t.Fatal(err)
	}
	if overrideActive {
		t.Fatal("override remains active after revocation")
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM sensitive_audit_events
		 WHERE workspace_id = $1
		   AND action IN ('plan.internal_assigned', 'plan.internal_revoked')
		   AND outcome = 'succeeded'
	`, workspaceID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("successful audit count = %d, want 2", auditCount)
	}
}

func TestPostgresDenialsAreAuditedAndAuditFailureRollsBack(t *testing.T) {
	pool := internalPlanIntegrationPool(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	repository := NewSQLRepository(pool)
	repository.auditTable = "sensitive_audit_events"
	service, err := NewService(repository, &adminAuthorizerStub{
		admins: map[string]bool{adminID: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.newID = func() (string, error) {
		return "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", nil
	}
	principal := Principal{AccountID: adminID, StronglyAuthenticated: true}

	provisionTrial(t, pool, workspaceID, now)
	_, err = service.Assign(ctx, principal, AssignmentRequest{
		WorkspaceID:     workspaceID,
		TargetAccountID: targetID,
		CorrelationID:   "case-17-postgres-denied",
	})
	if !errors.Is(err, ErrTargetNotAllowlisted) {
		t.Fatalf("non-allowlisted assignment error = %v", err)
	}
	var deniedCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM sensitive_audit_events
		 WHERE workspace_id = $1
		   AND outcome = 'denied'
	`, workspaceID).Scan(&deniedCount); err != nil {
		t.Fatal(err)
	}
	if deniedCount != 1 {
		t.Fatalf("denied audit count = %d, want 1", deniedCount)
	}

	secondWorkspace := "66666666-6666-4666-8666-666666666666"
	provisionTrial(t, pool, secondWorkspace, now)
	if _, err := pool.Exec(ctx, `
		INSERT INTO f11_internal_plan_allowlist (
			account_id,
			workspace_id,
			active,
			allowed_at,
			allowed_by_account_id
		) VALUES ($1, $2, true, $3, $4)
	`, targetID, secondWorkspace, now, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE sensitive_audit_events`); err != nil {
		t.Fatal(err)
	}

	_, err = service.Assign(ctx, principal, AssignmentRequest{
		WorkspaceID:     secondWorkspace,
		TargetAccountID: targetID,
		CorrelationID:   "case-17-postgres-audit-failure",
	})
	if !errors.Is(err, ErrInternalPlanUnavailable) {
		t.Fatalf("audit failure error = %v", err)
	}
	var activeExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM f10_internal_entitlement_overrides
			 WHERE workspace_id = $1
			   AND active
		)
	`, secondWorkspace).Scan(&activeExists); err != nil {
		t.Fatal(err)
	}
	if activeExists {
		t.Fatal("override committed despite audit failure")
	}
}

func internalPlanIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}

	schema := fmt.Sprintf("f11_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join("..", "f10-entitlements", "migrations", "000001_create_entitlements.sql"),
		filepath.Join("..", "f10-entitlements", "migrations", "000002_paddle_d07.sql"),
		filepath.Join("..", "f10-entitlements", "migrations", "000003_paddle_dunning_30_days.sql"),
		filepath.Join("..", "f10-entitlements", "migrations", "000004_d09_public_unlimited.sql"),
		filepath.Join("..", "f10-entitlements", "migrations", "000005_po_20260727_plan_limits.sql"),
		filepath.Join("..", "f10-entitlements", "migrations", "000006_align_workspace_ids_with_f04.sql"),
		filepath.Join("migrations", "000001_create_internal_plan_administration.sql"),
	} {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE sensitive_audit_events (
			event_id text PRIMARY KEY,
			occurred_at timestamptz NOT NULL,
			actor_type text NOT NULL,
			actor_id text NOT NULL,
			workspace_id text,
			action text NOT NULL,
			target_type text NOT NULL,
			target_id text NOT NULL,
			outcome text NOT NULL,
			correlation_id text NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return pool
}

func provisionTrial(
	t *testing.T,
	pool *pgxpool.Pool,
	id string,
	now time.Time,
) {
	t.Helper()
	var created bool
	if err := pool.QueryRow(
		context.Background(),
		`SELECT f10_provision_trial($1, $2)`,
		id,
		now,
	).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatalf("trial for %s was not created", id)
	}
}
