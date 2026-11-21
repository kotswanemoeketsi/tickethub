// Package database wraps database/sql for MySQL with transaction helpers and migrations.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

// Queryer is satisfied by both *sql.DB and *sql.Tx, so store functions can run either
// standalone or inside a caller's transaction.
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB is the application's database handle.
type DB struct {
	*sql.DB
}

// Options tunes the connection pool.
type Options struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	// ConnectTimeout bounds how long Open waits for the server to become reachable,
	// which smooths over container start-up ordering.
	ConnectTimeout time.Duration
}

// NormalizeDSN enforces the connection settings the application relies on:
// UTC timestamps, parsed DATETIME columns and utf8mb4.
func NormalizeDSN(dsn string, multiStatements bool) (string, error) {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "", fmt.Errorf("parse database DSN: %w", err)
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	cfg.Collation = "utf8mb4_0900_ai_ci"
	cfg.MultiStatements = multiStatements
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	return cfg.FormatDSN(), nil
}

// Open connects to MySQL, retrying until the server answers or the timeout elapses.
func Open(ctx context.Context, dsn string, opts Options, logger *slog.Logger) (*DB, error) {
	normalized, err := NormalizeDSN(dsn, false)
	if err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("mysql", normalized)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if opts.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
	}
	if opts.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(opts.ConnMaxLifetime)
	}

	deadline := time.Now().Add(opts.ConnectTimeout)
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = sqlDB.PingContext(pingCtx)
		cancel()
		if err == nil {
			return &DB{DB: sqlDB}, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("connect to database: %w", err)
		}
		logger.Warn("database not reachable yet, retrying", "error", err)
		time.Sleep(2 * time.Second)
	}
}

const maxTxAttempts = 4

// WithTx runs fn in a transaction, committing when fn returns nil and rolling back otherwise.
// Transactions that fail with a deadlock or lock-wait timeout are retried, which is the
// documented way to handle InnoDB deadlocks. fn must therefore be safe to re-run.
func (db *DB) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	var err error
	for attempt := 1; attempt <= maxTxAttempts; attempt++ {
		err = db.runTx(ctx, fn)
		if err == nil || !IsRetryable(err) || attempt == maxTxAttempts {
			return err
		}
		backoff := time.Duration(attempt*attempt)*10*time.Millisecond + time.Duration(rand.IntN(20))*time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return err
}

func (db *DB) runTx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func mysqlErrorNumber(err error) uint16 {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		return me.Number
	}
	return 0
}

// IsDuplicateKey reports a unique-constraint violation.
func IsDuplicateKey(err error) bool { return mysqlErrorNumber(err) == 1062 }

// IsRetryable reports a deadlock (1213) or lock wait timeout (1205).
func IsRetryable(err error) bool {
	n := mysqlErrorNumber(err)
	return n == 1213 || n == 1205
}

// Placeholders returns "?, ?, ?" for n parameters.
func Placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// Args converts a typed slice into the []any expected by database/sql.
func Args[T any](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// NullIfZero maps zero IDs to SQL NULL for optional foreign keys.
func NullIfZero(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// EscapeLike escapes LIKE wildcards in user input and wraps it for a contains-search.
func EscapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.TrimSpace(s)) + "%"
}
