package accountprivacyruntime

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	accountprivacy "github.com/apdsoftware/postqron/features/f12-account-privacy"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type recordingAccessBoundary struct {
	freezeCalls   int
	restoreCalls  int
	finalizeCalls int
}

func (boundary *recordingAccessBoundary) Freeze(context.Context, string) error {
	boundary.freezeCalls++
	return nil
}

func (boundary *recordingAccessBoundary) Restore(context.Context, string) error {
	boundary.restoreCalls++
	return nil
}

func (boundary *recordingAccessBoundary) Finalize(context.Context, string) error {
	boundary.finalizeCalls++
	return nil
}

type failingProviderRevoker struct {
	calls int
}

func (revoker *failingProviderRevoker) RevokeForDeletion(
	context.Context,
	accountprivacy.DeletionRequest,
	[]string,
) error {
	revoker.calls++
	return errors.New("provider revocation unavailable")
}

func TestDeactivateRestoresAccountWhenPostFreezePhaseFails(t *testing.T) {
	t.Parallel()
	database, err := sql.Open("pgx", "postgres://127.0.0.1:1/unavailable?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	access := &recordingAccessBoundary{}
	safety := deletionSafety{
		database:  database,
		access:    access,
		providers: &failingProviderRevoker{},
		now:       time.Now,
	}
	_, err = safety.Deactivate(context.Background(), accountprivacy.DeletionRequest{
		AccountID: "account-1",
		Scope:     accountprivacy.DeleteAccount,
	})
	if err == nil {
		t.Fatal("expected post-freeze failure")
	}
	if access.freezeCalls != 1 || access.restoreCalls != 1 {
		t.Fatalf("freeze calls = %d, restore calls = %d", access.freezeCalls, access.restoreCalls)
	}
}

func TestDeactivateFailsClosedWhenProviderRevocationFails(t *testing.T) {
	t.Parallel()
	access := &recordingAccessBoundary{}
	providers := &failingProviderRevoker{}
	safety := deletionSafety{
		access:    access,
		providers: providers,
		now:       time.Now,
	}
	receipt, err := safety.Deactivate(context.Background(), accountprivacy.DeletionRequest{
		AccountID:   "account-1",
		Scope:       accountprivacy.DeleteWorkspace,
		WorkspaceID: "workspace-1",
	})
	if err == nil {
		t.Fatal("expected provider revocation failure")
	}
	if receipt.ProviderRevocationAttempted {
		t.Fatal("provider revocation must not be declared when the boundary failed")
	}
	if providers.calls != 1 {
		t.Fatalf("provider revocation calls = %d", providers.calls)
	}
}
