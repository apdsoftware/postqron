package accountprivacyruntime

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPrivateArtifactStoreRejectsTraversal(t *testing.T) {
	t.Parallel()
	store, err := newPrivateArtifactStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", ".", "..", "../secret", "/tmp/secret", `..\secret`, "nested/../secret"} {
		if _, err := store.path(key); err == nil {
			t.Fatalf("expected key %q to be rejected", key)
		}
	}
}

func TestPrivateArtifactStoreDeleteIsIdempotentUnderRace(t *testing.T) {
	t.Parallel()
	store, err := newPrivateArtifactStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.root, "export.zip")
	if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errors := make(chan error, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errors <- store.DeleteExport(context.Background(), "export.zip")
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}
