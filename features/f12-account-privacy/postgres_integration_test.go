package accountprivacy

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

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
		for index := range destination {
			destination[index] = byte(index + 1)
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

func TestIsUniqueViolationIgnoresOtherErrors(t *testing.T) {
	if isUniqueViolation(errors.New("not a pg error")) {
		t.Fatal("unexpected unique violation match")
	}
}
