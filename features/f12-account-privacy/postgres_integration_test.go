package accountprivacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRecoversPartialAccountAndServesAccountArea(t *testing.T) {
	database := integrationDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	accountID := "account-270-" + integrationSuffix()
	workspaceID := fmt.Sprintf(
		"personal-%s-%d",
		accountID,
		time.Now().UnixNano(),
	)
	if _, err := database.ExecContext(ctx, `
		INSERT INTO auth_accounts (
			id, email, normalized_email, display_name, contract_country,
			created_at
		) VALUES ($1, $2, $2, '', 'IT', $3)
	`, accountID, accountID+"@example.test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO f04_workspaces (
			id, personal_account_id, name, status, created_at, updated_at
		) VALUES ($1, $2, 'Recovered workspace', 'active', $3, $3)
	`, workspaceID, accountID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO f04_memberships (
			workspace_id, account_id, role, status, created_at, updated_at
		) VALUES ($1, $2, 'owner', 'active', $3, $3)
	`, workspaceID, accountID, now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.Exec(`DELETE FROM f10_workspace_billing WHERE workspace_id = $1`, workspaceID)
		_, _ = database.Exec(`DELETE FROM f04_workspaces WHERE id = $1`, workspaceID)
		cleanupAccount(t, database, accountID)
		_, _ = database.Exec(`DELETE FROM auth_accounts WHERE id = $1`, accountID)
	})

	applyMigrationFile(
		t,
		database,
		"migrations/000003_recover_partial_account_provisioning.sql",
	)

	repository, err := NewPostgresRepository(
		database,
		nil,
		accountAreaPostgresProjection{database: database},
	)
	if err != nil {
		t.Fatal(err)
	}
	adapters := defaultAdapters(now)
	service, err := NewService(Dependencies{
		Repository:       repository,
		Plans:            accountAreaPostgresProjection{database: database},
		Providers:        adapters,
		ExportAuthorizer: adapters,
		ExportQueue:      adapters,
		DownloadSigner:   adapters,
		ExportArtifacts:  adapters,
		Ownership:        adapters,
		DeletionSafety:   adapters,
		Eraser:           adapters,
	}, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(
		service,
		fixedAuthenticator{principal: Principal{
			AccountID:       accountID,
			AuthenticatedAt: now,
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/api/v1/account", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/account = %d: %s", response.Code, response.Body)
	}
	var area AccountArea
	if err := json.Unmarshal(response.Body.Bytes(), &area); err != nil {
		t.Fatal(err)
	}
	if area.Profile.DisplayName != accountID ||
		len(area.Workspaces) != 1 ||
		area.Workspaces[0].Workspace.ID != workspaceID ||
		area.Workspaces[0].Plan.Code != "team" {
		t.Fatalf("recovered account area = %#v", area)
	}

	unchangedAt := now.Add(time.Hour)
	if _, err := database.ExecContext(ctx, `
		UPDATE f10_workspace_billing
		   SET plan_code = 'pro',
		       billing_state = 'active',
		       channel_quantity = 6,
		       updated_at = $2
		 WHERE workspace_id = $1
	`, workspaceID, unchangedAt); err != nil {
		t.Fatal(err)
	}
	applyMigrationFile(
		t,
		database,
		"migrations/000003_recover_partial_account_provisioning.sql",
	)
	var (
		billingCount int
		planCode     string
		billingState string
		updatedAt    time.Time
	)
	if err := database.QueryRowContext(ctx, `
		SELECT count(*), min(plan_code), min(billing_state), min(updated_at)
		  FROM f10_workspace_billing
		 WHERE workspace_id = $1
	`, workspaceID).Scan(
		&billingCount,
		&planCode,
		&billingState,
		&updatedAt,
	); err != nil {
		t.Fatal(err)
	}
	if billingCount != 1 || planCode != "pro" || billingState != "active" ||
		!updatedAt.Equal(unchangedAt) {
		t.Fatalf(
			"idempotent recovery changed existing billing: count=%d plan=%s state=%s updated=%s",
			billingCount,
			planCode,
			billingState,
			updatedAt,
		)
	}
}

func TestPostgresRepositoryUpdatesProfile(t *testing.T) {
	database := integrationDatabase(t)
	repository, err := NewPostgresRepository(database, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	accountID := "f12-profile-" + integrationSuffix()
	now := time.Now().UTC().Truncate(time.Second)
	seedProfile(t, database, Profile{
		AccountID:   accountID,
		DisplayName: "Carlo",
		Locale:      "it-IT",
		Timezone:    "Europe/Rome",
		UpdatedAt:   now,
	})
	t.Cleanup(func() { cleanupAccount(t, database, accountID) })

	profile, err := repository.UpdateProfile(context.Background(), ProfileUpdateCommand{
		AccountID:   accountID,
		DisplayName: "Carlo Zuffetti",
		Locale:      "en-US",
		Timezone:    "America/Santo_Domingo",
		Now:         now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.DisplayName != "Carlo Zuffetti" || profile.Locale != "en-US" {
		t.Fatalf("unexpected updated profile: %#v", profile)
	}
	stored, err := repository.Profile(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Timezone != "America/Santo_Domingo" {
		t.Fatalf("stored timezone = %q", stored.Timezone)
	}
}

func TestPostgresServiceReusesActiveExportRequest(t *testing.T) {
	database := integrationDatabase(t)
	repository, err := NewPostgresRepository(database, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	accountID := "f12-export-" + integrationSuffix()
	t.Cleanup(func() { cleanupAccount(t, database, accountID) })

	now := time.Now().UTC().Truncate(time.Second)
	adapters := defaultAdapters(now)
	clock := now
	randomCall := byte(0)
	service, err := NewService(Dependencies{
		Repository:       repository,
		Plans:            adapters,
		Providers:        adapters,
		ExportAuthorizer: adapters,
		ExportQueue:      adapters,
		DownloadSigner:   adapters,
		ExportArtifacts:  adapters,
		Ownership:        adapters,
		DeletionSafety:   adapters,
		Eraser:           adapters,
	}, WithClock(func() time.Time { return clock }), WithRandom(func(destination []byte) error {
		randomCall++
		for index := range destination {
			destination[index] = randomCall + byte(index)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	principal := Principal{AccountID: accountID, AuthenticatedAt: now.Add(-time.Minute)}

	first, err := service.RequestExport(context.Background(), principal, ExportAccount, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.RequestExport(context.Background(), principal, ExportAccount, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("active export was not reused: %#v %#v", first, second)
	}
	if len(adapters.exportJobs) != 1 {
		t.Fatalf("unexpected queued export jobs: %#v", adapters.exportJobs)
	}

	clock = now.Add(ExportRetention + time.Second)
	third, err := service.RequestExport(context.Background(), Principal{
		AccountID:       accountID,
		AuthenticatedAt: clock.Add(-time.Minute),
	}, ExportAccount, "")
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == first.ID {
		t.Fatalf("expired export request was reused: %#v %#v", first, third)
	}
	stored, err := repository.Export(context.Background(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != ExportExpired {
		t.Fatalf("expired request status = %q", stored.Status)
	}
}

func TestPostgresRepositoryPersistsGracePeriodCancellationAndCompletion(t *testing.T) {
	database := integrationDatabase(t)
	repository, err := NewPostgresRepository(database, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	accountID := "f12-delete-" + integrationSuffix()
	t.Cleanup(func() { cleanupAccount(t, database, accountID) })
	now := time.Now().UTC().Truncate(time.Second)

	cancelled := DeletionRequest{
		ID:          "delete-cancel-" + integrationSuffix(),
		AccountID:   accountID,
		Scope:       DeleteAccount,
		Status:      DeletionDeactivating,
		RequestedAt: now,
		GraceEndsAt: now.Add(GracePeriod),
		Ownership:   OwnershipPlan{},
	}
	if err := repository.CreateDeletion(context.Background(), cancelled); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkGracePeriod(context.Background(), cancelled.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := repository.CancelDeletion(context.Background(), cancelled.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	storedCancelled, err := repository.Deletion(context.Background(), cancelled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedCancelled.Status != DeletionCancelled {
		t.Fatalf("cancelled status = %q", storedCancelled.Status)
	}

	completing := DeletionRequest{
		ID:          "delete-complete-" + integrationSuffix(),
		AccountID:   accountID,
		Scope:       DeleteWorkspace,
		WorkspaceID: "workspace-" + integrationSuffix(),
		Status:      DeletionGracePeriod,
		RequestedAt: now,
		GraceEndsAt: now,
		Ownership: OwnershipPlan{Actions: []OwnershipAction{{
			WorkspaceID: "workspace-" + integrationSuffix(),
			Action:      DeleteOwnedSpace,
		}}},
	}
	if err := repository.CreateDeletion(context.Background(), DeletionRequest{
		ID:          completing.ID,
		AccountID:   completing.AccountID,
		Scope:       completing.Scope,
		WorkspaceID: completing.WorkspaceID,
		Status:      DeletionDeactivating,
		RequestedAt: completing.RequestedAt,
		GraceEndsAt: completing.GraceEndsAt,
		Ownership:   completing.Ownership,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.MarkGracePeriod(context.Background(), completing.ID, now); err != nil {
		t.Fatal(err)
	}
	due, err := repository.ClaimDueDeletions(context.Background(), now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != completing.ID {
		t.Fatalf("unexpected due deletions: %#v", due)
	}
	complete := DeletionCompleteCommand{
		RequestID:          completing.ID,
		CompletedAt:        now.Add(2 * time.Second),
		TombstoneID:        "tombstone-" + integrationSuffix(),
		TombstoneExpiresAt: now.Add(2*time.Second + TombstoneRetention),
	}
	if err := repository.CompleteDeletion(context.Background(), complete); err != nil {
		t.Fatal(err)
	}
	storedCompleted, err := repository.Deletion(context.Background(), completing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedCompleted.Status != DeletionCompleted || storedCompleted.TombstoneID != complete.TombstoneID {
		t.Fatalf("unexpected completed deletion: %#v", storedCompleted)
	}
	var tombstoneCount int
	if err := database.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM account_privacy_tombstones WHERE deletion_request_id = $1`,
		completing.ID,
	).Scan(&tombstoneCount); err != nil {
		t.Fatal(err)
	}
	if tombstoneCount != 1 {
		t.Fatalf("tombstone count = %d", tombstoneCount)
	}
}

func integrationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	databaseURL := integrationDatabaseURL()
	if databaseURL == "" {
		t.Skip("neither F12_DATABASE_URL nor CI-compatible database URL is configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	applyMigrationFile(t, database, "migrations/000001_create_account_privacy.sql")
	applyMigrationFile(t, database, "migrations/000002_enforce_idempotent_exports.sql")
	applyMigrationFile(t, database, "migrations/000003_recover_partial_account_provisioning.sql")
	return database
}

func integrationDatabaseURL() string {
	for _, name := range []string{"F12_DATABASE_URL", "DATABASE_URL", "TEST_DATABASE_URL"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func applyMigrationFile(t *testing.T, database *sql.DB, relativePath string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Clean(relativePath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(string(contents)); err != nil && !isDuplicateDDL(err) {
		t.Fatalf("apply %s: %v", relativePath, err)
	}
}

func isDuplicateDDL(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "already exists") ||
		strings.Contains(err.Error(), "does not exist"))
}

func seedProfile(t *testing.T, database *sql.DB, profile Profile) {
	t.Helper()
	if _, err := database.Exec(
		`INSERT INTO account_privacy_profiles (
			account_id, display_name, locale, timezone, updated_at
		) VALUES ($1, $2, $3, $4, $5)`,
		profile.AccountID,
		profile.DisplayName,
		profile.Locale,
		profile.Timezone,
		profile.UpdatedAt,
	); err != nil {
		t.Fatal(err)
	}
}

func cleanupAccount(t *testing.T, database *sql.DB, accountID string) {
	t.Helper()
	_, _ = database.Exec(`DELETE FROM account_privacy_audit_events WHERE account_id = $1`, accountID)
	_, _ = database.Exec(`
		DELETE FROM account_privacy_tombstones
		WHERE deletion_request_id IN (
			SELECT id FROM account_privacy_deletion_requests WHERE account_id = $1
		)
	`, accountID)
	_, _ = database.Exec(`DELETE FROM account_privacy_deletion_requests WHERE account_id = $1`, accountID)
	_, _ = database.Exec(`DELETE FROM account_privacy_export_requests WHERE account_id = $1`, accountID)
	_, _ = database.Exec(`DELETE FROM account_privacy_profiles WHERE account_id = $1`, accountID)
}

func integrationSuffix() string {
	return time.Now().UTC().Format("20060102150405.000000000")
}

type accountAreaPostgresProjection struct {
	database *sql.DB
}

func (projection accountAreaPostgresProjection) Workspaces(
	ctx context.Context,
	accountID string,
) ([]WorkspaceRef, error) {
	rows, err := projection.database.QueryContext(ctx, `
		SELECT workspace.id, workspace.name, membership.role
		  FROM f04_memberships AS membership
		  JOIN f04_workspaces AS workspace
		    ON workspace.id = membership.workspace_id
		 WHERE membership.account_id = $1
		   AND membership.status = 'active'
		   AND workspace.status = 'active'
		 ORDER BY workspace.created_at, workspace.id
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var workspaces []WorkspaceRef
	for rows.Next() {
		var workspace WorkspaceRef
		if err := rows.Scan(
			&workspace.ID,
			&workspace.Name,
			&workspace.Role,
		); err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	return workspaces, rows.Err()
}

func (projection accountAreaPostgresProjection) Plan(
	ctx context.Context,
	workspaceID string,
	_ string,
) (Plan, error) {
	rows, err := projection.database.QueryContext(ctx, `
		SELECT plan_code, plan_name, billing_state, period_end,
		       resource, used, quota_limit
		  FROM f10_public_entitlement_usage
		 WHERE workspace_id = $1
		 ORDER BY resource
	`, workspaceID)
	if err != nil {
		return Plan{}, err
	}
	defer rows.Close()
	plan := Plan{
		Usage:  map[string]int64{},
		Limits: map[string]int64{},
	}
	found := false
	for rows.Next() {
		found = true
		var (
			renewsAt time.Time
			resource string
			used     int64
			limit    sql.NullInt64
		)
		if err := rows.Scan(
			&plan.Code,
			&plan.Name,
			&plan.State,
			&renewsAt,
			&resource,
			&used,
			&limit,
		); err != nil {
			return Plan{}, err
		}
		plan.Usage[resource] = used
		if limit.Valid {
			plan.Limits[resource] = limit.Int64
		}
		plan.RenewsAt = &renewsAt
	}
	if err := rows.Err(); err != nil {
		return Plan{}, err
	}
	if !found {
		return Plan{}, ErrNotFound
	}
	plan.Manageable = plan.Code != "start"
	return plan, nil
}

func TestIsUniqueViolationIgnoresOtherErrors(t *testing.T) {
	if isUniqueViolation(errors.New("not a pg error")) {
		t.Fatal("unexpected unique violation match")
	}
}
