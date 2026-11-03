package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schemaMigrationsDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    name TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

// RunMigrations applies each SQL file at most once, tracked in schema_migrations.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, paths []string) error {
	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := Migrate(ctx, pool, path); err != nil {
			return err
		}
	}
	return nil
}

// Migrate applies a single SQL migration file if it has not been recorded yet.
// Existing databases created before schema_migrations are bootstrapped by
// inspecting the live schema so heavy migrations are not re-run on every start.
func Migrate(ctx context.Context, pool *pgxpool.Pool, path string) error {
	if _, err := pool.Exec(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	migPath := path
	if abs, err := filepath.Abs(path); err == nil {
		migPath = abs
	}
	name := filepath.Base(migPath)

	var recorded bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name = $1)`, name).Scan(&recorded); err != nil {
		return fmt.Errorf("check migration %s: %w", name, err)
	}
	if recorded {
		slog.Debug("migration already applied", "name", name)
		return nil
	}

	if applied, err := migrationAppliedInSchema(ctx, pool, name); err != nil {
		return err
	} else if applied {
		slog.Info("migration bootstrapped from existing schema", "name", name)
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT DO NOTHING`, name); err != nil {
			return fmt.Errorf("record bootstrapped migration %s: %w", name, err)
		}
		return nil
	}

	slog.Info("applying migration", "name", name, "path", migPath)
	b, err := os.ReadFile(migPath)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	if _, err := pool.Exec(ctx, string(b)); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	slog.Info("migration applied", "name", name)
	return nil
}

func migrationAppliedInSchema(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	switch name {
	case "001_flights_core.sql":
		return tableExists(ctx, pool, "jobs")
	case "002_multi_collector.sql":
		return columnExists(ctx, pool, "jobs", "source")
	case "004_maintenance_indexes.sql":
		return indexExists(ctx, pool, "idx_collection_runs_created_at")
	default:
		return false, nil
	}
}

// AppendSiblingMigration appends a migration file from the same directory as the
// first configured migration path (e.g. 004_maintenance_indexes.sql).
func AppendSiblingMigration(paths []string, filename string) []string {
	if filename == "" {
		return paths
	}
	var migDir string
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		migDir = filepath.Dir(path)
		break
	}
	if migDir == "" {
		return paths
	}
	candidate := filepath.Join(migDir, filename)
	for _, path := range paths {
		if path == candidate {
			return paths
		}
	}
	return append(paths, candidate)
}

func indexExists(ctx context.Context, pool *pgxpool.Pool, index string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM pg_indexes
			WHERE schemaname = 'public' AND indexname = $1
		)`, index).Scan(&exists)
	return exists, err
}

func tableExists(ctx context.Context, pool *pgxpool.Pool, table string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, table).Scan(&exists)
	return exists, err
}

func columnExists(ctx context.Context, pool *pgxpool.Pool, table, column string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
	return exists, err
}
