// Package jobs is a MySQL-backed job queue used as a transactional outbox.
//
// Jobs are inserted in the same transaction as the business change that causes them (for
// example "email the tickets" is enqueued in the payment-confirmation transaction), so a
// committed change can never lose its side effects and a rolled-back change never emits
// them. Workers claim jobs with SELECT ... FOR UPDATE SKIP LOCKED, so several worker
// processes can run safely side by side.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
)

// Enqueue inserts a job to run as soon as possible. Pass a transaction as q to make the job
// part of a larger atomic change.
func Enqueue(ctx context.Context, q database.Queryer, now time.Time, jobType string, payload any) error {
	return EnqueueAt(ctx, q, now, now, jobType, payload)
}

// EnqueueAt inserts a job that becomes runnable at runAt.
func EnqueueAt(ctx context.Context, q database.Queryer, now, runAt time.Time, jobType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode job payload: %w", err)
	}
	_, err = q.ExecContext(ctx,
		`INSERT INTO jobs (type, payload, status, run_at, created_at, updated_at) VALUES (?, ?, 'pending', ?, ?, ?)`,
		jobType, raw, runAt, now, now)
	if err != nil {
		return fmt.Errorf("enqueue job %s: %w", jobType, err)
	}
	return nil
}

// Handler processes one job payload. Returning an error schedules a retry with backoff;
// returning a Permanent error marks the job failed immediately.
type Handler func(ctx context.Context, payload json.RawMessage) error

// PermanentError marks a failure that retrying cannot fix (for example a deleted order).
type PermanentError struct{ Err error }

func (e PermanentError) Error() string { return e.Err.Error() }
func (e PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err so the job is not retried.
func Permanent(err error) error { return PermanentError{Err: err} }

// Runner polls the jobs table and dispatches jobs to registered handlers.
type Runner struct {
	db           *database.DB
	handlers     map[string]Handler
	now          clock.Func
	logger       *slog.Logger
	workerID     string
	batchSize    int
	pollInterval time.Duration
	jobTimeout   time.Duration
}

// NewRunner returns a runner with sensible defaults.
func NewRunner(db *database.DB, now clock.Func, logger *slog.Logger) *Runner {
	host, _ := os.Hostname()
	return &Runner{
		db:           db,
		handlers:     map[string]Handler{},
		now:          now,
		logger:       logger,
		workerID:     fmt.Sprintf("%s-%s", host, random.Hex(3)),
		batchSize:    10,
		pollInterval: 2 * time.Second,
		jobTimeout:   2 * time.Minute,
	}
}

// Register binds a handler to a job type.
func (r *Runner) Register(jobType string, h Handler) { r.handlers[jobType] = h }

// Run processes jobs until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	r.logger.Info("job runner started", "worker_id", r.workerID)
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		processed, err := r.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			r.logger.Error("job batch failed", "error", err)
		}
		if processed > 0 {
			continue // there may be more work queued; poll again immediately
		}
		select {
		case <-ctx.Done():
			r.logger.Info("job runner stopped")
			return
		case <-ticker.C:
		}
	}
}

type claimedJob struct {
	id          int64
	jobType     string
	payload     json.RawMessage
	attempts    int
	maxAttempts int
}

// RunOnce claims and processes one batch of due jobs. It returns how many were processed.
func (r *Runner) RunOnce(ctx context.Context) (int, error) {
	batch, err := r.claim(ctx)
	if err != nil {
		return 0, err
	}
	for _, job := range batch {
		r.process(ctx, job)
	}
	return len(batch), nil
}

func (r *Runner) claim(ctx context.Context) ([]claimedJob, error) {
	var batch []claimedJob
	now := r.now()
	err := r.db.WithTx(ctx, func(tx *sql.Tx) error {
		batch = batch[:0]
		rows, err := tx.QueryContext(ctx, `
			SELECT id, type, payload, attempts, max_attempts FROM jobs
			WHERE status = 'pending' AND run_at <= ?
			ORDER BY run_at, id
			LIMIT ?
			FOR UPDATE SKIP LOCKED`, now, r.batchSize)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var j claimedJob
			if err := rows.Scan(&j.id, &j.jobType, &j.payload, &j.attempts, &j.maxAttempts); err != nil {
				return err
			}
			batch = append(batch, j)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		rows.Close()
		if len(batch) == 0 {
			return nil
		}
		ids := make([]any, len(batch))
		for i, j := range batch {
			ids[i] = j.id
		}
		args := append([]any{now, r.workerID, now}, ids...)
		_, err = tx.ExecContext(ctx, `UPDATE jobs SET status = 'running', attempts = attempts + 1, locked_at = ?, locked_by = ?, updated_at = ?
			WHERE id IN (`+database.Placeholders(len(ids))+`)`, args...)
		return err
	})
	return batch, err
}

func (r *Runner) process(ctx context.Context, job claimedJob) {
	attempt := job.attempts + 1
	handler, ok := r.handlers[job.jobType]
	var err error
	if !ok {
		err = Permanent(fmt.Errorf("no handler registered for job type %q", job.jobType))
	} else {
		jobCtx, cancel := context.WithTimeout(ctx, r.jobTimeout)
		err = safeRun(jobCtx, handler, job.payload)
		cancel()
	}

	now := r.now()
	if err == nil {
		_, dbErr := r.db.ExecContext(ctx, `UPDATE jobs SET status = 'completed', locked_at = NULL, last_error = NULL, updated_at = ? WHERE id = ?`, now, job.id)
		if dbErr != nil {
			r.logger.Error("mark job completed", "job_id", job.id, "error", dbErr)
		}
		return
	}

	var permanent PermanentError
	if errors.As(err, &permanent) || attempt >= job.maxAttempts {
		r.logger.Error("job failed permanently", "job_id", job.id, "type", job.jobType, "attempt", attempt, "error", err)
		_, _ = r.db.ExecContext(ctx, `UPDATE jobs SET status = 'failed', locked_at = NULL, last_error = ?, updated_at = ? WHERE id = ?`, truncate(err.Error()), now, job.id)
		return
	}
	retryAt := now.Add(backoff(attempt))
	r.logger.Warn("job failed, will retry", "job_id", job.id, "type", job.jobType, "attempt", attempt, "retry_at", retryAt, "error", err)
	_, _ = r.db.ExecContext(ctx, `UPDATE jobs SET status = 'pending', locked_at = NULL, run_at = ?, last_error = ?, updated_at = ? WHERE id = ?`, retryAt, truncate(err.Error()), now, job.id)
}

func safeRun(ctx context.Context, h Handler, payload json.RawMessage) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("job handler panicked: %v", p)
		}
	}()
	return h(ctx, payload)
}

// RecoverStale returns jobs stuck in "running" (for example after a worker crash) to the
// queue so they are retried.
func (r *Runner) RecoverStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	now := r.now()
	res, err := r.db.ExecContext(ctx, `UPDATE jobs SET status = 'pending', locked_at = NULL, run_at = ?, updated_at = ?
		WHERE status = 'running' AND locked_at < ?`, now, now, now.Add(-olderThan))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PurgeCompleted deletes completed jobs older than the retention period.
func (r *Runner) PurgeCompleted(ctx context.Context, retention time.Duration) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM jobs WHERE status = 'completed' AND updated_at < ? LIMIT 5000`, r.now().Add(-retention))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// backoff grows exponentially from 30 seconds and is capped at one hour.
func backoff(attempt int) time.Duration {
	d := 30 * time.Second << (attempt - 1)
	if d > time.Hour || d <= 0 {
		return time.Hour
	}
	return d
}

func truncate(s string) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) > 2000 {
		return s[:2000]
	}
	return s
}
