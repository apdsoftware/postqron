package scheduling

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresSchedulingAuthorizerRequiresSelectedActiveWorkspace(
	t *testing.T,
) {
	databaseURL := os.Getenv("F07_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("F07_DATABASE_URL is not configured")
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	suffix := time.Now().UTC().Format("20060102150405.000000000")
	workspaceID := "workspace-f07-auth-" + suffix
	ownerID := "account-f07-owner-" + suffix
	memberID := "account-f07-member-" + suffix
	unselectedID := "account-f07-unselected-" + suffix
	now := time.Now().UTC()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{
			`INSERT INTO f04_workspaces (
				id, personal_account_id, name, status, created_at, updated_at
			) VALUES ($1, $2, 'F7 auth integration', 'active', $3, $3)`,
			[]any{workspaceID, ownerID, now},
		},
		{
			`INSERT INTO f04_memberships (
				workspace_id, account_id, role, status, created_at, updated_at
			) VALUES
				($1, $2, 'owner', 'active', $5, $5),
				($1, $3, 'member', 'active', $5, $5),
				($1, $4, 'member', 'active', $5, $5)`,
			[]any{workspaceID, ownerID, memberID, unselectedID, now},
		},
		{
			`INSERT INTO f04_workspace_selections (
				account_id, workspace_id, selected_at, updated_at
			) VALUES ($1, $2, $3, $3)`,
			[]any{memberID, workspaceID, now},
		},
	} {
		if _, err := database.ExecContext(
			context.Background(),
			statement.query,
			statement.args...,
		); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = database.ExecContext(
			context.Background(),
			"DELETE FROM f04_workspaces WHERE id = $1",
			workspaceID,
		)
	})

	authorizer := postgresSchedulingAuthorizer{database: database}
	allowed, err := authorizer.CanManageScheduling(
		context.Background(),
		workspaceID,
		memberID,
	)
	if err != nil || !allowed {
		t.Fatalf("selected active member allowed=%v err=%v", allowed, err)
	}
	allowed, err = authorizer.CanManageScheduling(
		context.Background(),
		workspaceID,
		unselectedID,
	)
	if err != nil || allowed {
		t.Fatalf("unselected member allowed=%v err=%v", allowed, err)
	}
}
