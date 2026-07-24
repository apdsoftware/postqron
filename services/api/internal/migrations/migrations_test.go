package migrations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
)

func TestCollect(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "000001_create_records.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE records (id bigint PRIMARY KEY);\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	features := []featureruntime.Feature{{
		Directory: directory,
		Manifest: featureruntime.Manifest{
			ID:         "records",
			Migrations: []string{"./000001_create_records.sql"},
		},
	}}

	collected, err := Collect(features)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(collected) != 1 || collected[0].Checksum == "" {
		t.Fatalf("Collect() = %#v, want one checksummed migration", collected)
	}
}

func TestCollectRejectsDownMigration(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "000001_create_records.sql")
	if err := os.WriteFile(path, []byte("-- migrate:down\nDROP TABLE records;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	features := []featureruntime.Feature{{
		Directory: directory,
		Manifest: featureruntime.Manifest{
			ID:         "records",
			Migrations: []string{"./000001_create_records.sql"},
		},
	}}

	_, err := Collect(features)
	if err == nil || !strings.Contains(err.Error(), "down migration") {
		t.Fatalf("Collect() error = %v, want down migration rejection", err)
	}
}

func TestCollectFollowsFeatureDependencyOrder(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"000001_foundation.sql", "000001_scheduler.sql"} {
		if err := os.WriteFile(
			filepath.Join(directory, name),
			[]byte("SELECT 1;\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	features := []featureruntime.Feature{
		{
			Directory: directory,
			Manifest: featureruntime.Manifest{
				ID:           "scheduler",
				Dependencies: []string{"foundation"},
				Migrations:   []string{"./000001_scheduler.sql"},
			},
		},
		{
			Directory: directory,
			Manifest: featureruntime.Manifest{
				ID:         "foundation",
				Migrations: []string{"./000001_foundation.sql"},
			},
		},
	}

	collected, err := Collect(features)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if collected[0].FeatureID != "foundation" || collected[1].FeatureID != "scheduler" {
		t.Fatalf("Collect() order = %s, %s", collected[0].FeatureID, collected[1].FeatureID)
	}
}
