package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	featureruntime "github.com/apdsoftware/postqron/packages/runtime"
	"github.com/jackc/pgx/v5"
)

var migrationNamePattern = regexp.MustCompile(`^\d{6}_[a-z0-9_]+\.sql$`)

const advisoryLockID int64 = 6_831_774_864_658_031

type Migration struct {
	FeatureID string
	Name      string
	Path      string
	Checksum  string
	SQL       string
}

func Collect(features []featureruntime.Feature) ([]Migration, error) {
	var migrations []Migration
	ids := make(map[string]struct{})
	orderedFeatures, err := featureruntime.ResolveOrder(features)
	if err != nil {
		return nil, err
	}

	for _, feature := range orderedFeatures {
		configuredMigrations := slices.Clone(feature.Manifest.Migrations)
		slices.Sort(configuredMigrations)
		for _, configuredPath := range configuredMigrations {
			name := filepath.Base(configuredPath)
			if !migrationNamePattern.MatchString(name) {
				return nil, fmt.Errorf(
					"%s migration %q must match NNNNNN_name.sql",
					feature.Manifest.ID,
					name,
				)
			}
			id := feature.Manifest.ID + "/" + name
			if _, duplicate := ids[id]; duplicate {
				return nil, fmt.Errorf("duplicate migration %s", id)
			}
			ids[id] = struct{}{}

			path := filepath.Join(feature.Directory, configuredPath)
			source, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read migration %s: %w", id, err)
			}
			sql := strings.TrimSpace(string(source))
			if sql == "" {
				return nil, fmt.Errorf("migration %s is empty", id)
			}
			normalized := strings.ToUpper(sql)
			if strings.Contains(normalized, "-- MIGRATE:DOWN") {
				return nil, fmt.Errorf("migration %s contains a down migration", id)
			}
			if strings.Contains(normalized, "BEGIN;") || strings.Contains(normalized, "COMMIT;") {
				return nil, fmt.Errorf("migration %s manages transactions explicitly", id)
			}

			hash := sha256.Sum256(source)
			migrations = append(migrations, Migration{
				FeatureID: feature.Manifest.ID,
				Name:      name,
				Path:      path,
				Checksum:  hex.EncodeToString(hash[:]),
				SQL:       sql,
			})
		}
	}

	return migrations, nil
}

func Apply(ctx context.Context, databaseURL string, migrations []Migration) error {
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required to apply migrations")
	}
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer connection.Close(ctx)

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = connection.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockID)
	}()

	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS postqron_schema_migrations (
			feature_id text NOT NULL,
			name text NOT NULL,
			checksum_sha256 text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (feature_id, name)
		)
	`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}

	for _, migration := range migrations {
		var appliedChecksum string
		err := connection.QueryRow(
			ctx,
			`SELECT checksum_sha256
			 FROM postqron_schema_migrations
			 WHERE feature_id = $1 AND name = $2`,
			migration.FeatureID,
			migration.Name,
		).Scan(&appliedChecksum)
		switch {
		case err == nil && appliedChecksum != migration.Checksum:
			return fmt.Errorf(
				"applied migration %s/%s has changed",
				migration.FeatureID,
				migration.Name,
			)
		case err == nil:
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("read migration ledger: %w", err)
		}

		transaction, err := connection.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s/%s: %w", migration.FeatureID, migration.Name, err)
		}
		if _, err = transaction.Exec(ctx, migration.SQL); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("apply migration %s/%s: %w", migration.FeatureID, migration.Name, err)
		}
		if _, err = transaction.Exec(
			ctx,
			`INSERT INTO postqron_schema_migrations (feature_id, name, checksum_sha256)
			 VALUES ($1, $2, $3)`,
			migration.FeatureID,
			migration.Name,
			migration.Checksum,
		); err != nil {
			_ = transaction.Rollback(ctx)
			return fmt.Errorf("record migration %s/%s: %w", migration.FeatureID, migration.Name, err)
		}
		if err = transaction.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s/%s: %w", migration.FeatureID, migration.Name, err)
		}
	}
	return nil
}
