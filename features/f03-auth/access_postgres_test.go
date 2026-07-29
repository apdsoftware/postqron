package auth

import (
	"context"
	"database/sql/driver"
	"testing"
)

func TestPostgresFreezeIsOneAtomicAccessBoundary(t *testing.T) {
	database, state := openPasswordTestDatabase(
		t,
		passwordSQLStep{
			kind:     "query",
			contains: "SELECT access_state",
			columns:  []string{"access_state"},
			rows:     [][]driver.Value{{"active"}},
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "UPDATE auth_accounts",
			affected: 1,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "UPDATE auth_sessions",
			affected: 2,
		},
		passwordSQLStep{
			kind:     "exec",
			contains: "UPDATE auth_oauth_attempts",
			affected: 3,
		},
	)
	store, err := NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FreezeAccountAccess(
		context.Background(),
		"account-1",
		testNow,
	); err != nil {
		t.Fatalf("FreezeAccountAccess() error = %v", err)
	}
	assertPasswordSQLComplete(t, state)
}
