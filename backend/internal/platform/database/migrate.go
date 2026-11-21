package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"
)

const migrationLockName = "gotaticket_schema_migrations"

// Migrate applies every *.sql file in fsys that has not been applied yet, in lexical order.
//
// A MySQL advisory lock serialises concurrent instances so that only one of them migrates.
// Note that MySQL DDL statements commit implicitly, so each migration file should be
// written to succeed or fail as a whole.
func Migrate(ctx context.Context, dsn string, fsys fs.FS, logger *slog.Logger) error {
	normalized, err := NormalizeDSN(dsn, true)
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer db.Close()

	// When started alongside the database (for example in Docker), wait for it to accept
	// connections rather than failing on the first attempt.
	var conn *sql.Conn
	for deadline := time.Now().Add(90 * time.Second); ; {
		if conn, err = db.Conn(ctx); err == nil {
			if err = conn.PingContext(ctx); err == nil {
				break
			}
			conn.Close()
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("acquire migration connection: %w", err)
		}
		logger.Warn("database not reachable yet, retrying", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	defer conn.Close()

	var locked sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 60)", migrationLockName).Scan(&locked); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if !locked.Valid || locked.Int64 != 1 {
		return fmt.Errorf("timed out waiting for migration lock")
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT RELEASE_LOCK(?)", migrationLockName) //nolint:errcheck

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(255) NOT NULL PRIMARY KEY,
		applied_at DATETIME(3) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		logger.Info("applying migration", "version", version)
		if _, err := conn.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)", version, time.Now().UTC()); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}
