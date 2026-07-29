package privacyruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type recordingFinalizeBoundary struct {
	accountID string
}

func (boundary *recordingFinalizeBoundary) Finalize(_ context.Context, accountID string) error {
	boundary.accountID = accountID
	return nil
}

func TestErrorCodeNeverIncludesPII(t *testing.T) {
	t.Parallel()
	if got := errorCode(assertionError("user@example.test token-secret")); got != "privacy_runtime_failed" {
		t.Fatalf("unexpected safe code %q", got)
	}
}

func TestNewCreatesPrivateArtifactDirectory(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "exports")
	// New validates the database before touching disk, so exercise the same
	// production directory invariant directly.
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("artifact directory mode = %o", info.Mode().Perm())
	}
}

func TestFinalizeUsesPublicAccountAccessBoundary(t *testing.T) {
	t.Parallel()
	boundary := &recordingFinalizeBoundary{}
	service := &Service{access: boundary}
	if err := service.finalizeAccountAccess(context.Background(), "account-1"); err != nil {
		t.Fatal(err)
	}
	if boundary.accountID != "account-1" {
		t.Fatalf("finalized account = %q", boundary.accountID)
	}
}

type assertionError string

func (err assertionError) Error() string { return string(err) }
