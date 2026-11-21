package app

import (
	"context"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/jobs"
)

// periodicTask runs fn every interval until the context ends.
type periodicTask struct {
	name     string
	interval time.Duration
	fn       func(ctx context.Context) (int, error)
}

// RunWorker processes background jobs (emails) and the periodic maintenance tasks until ctx
// is cancelled. Every task is idempotent, so running several workers side by side is safe.
func (a *App) RunWorker(ctx context.Context) {
	runner := jobs.NewRunner(a.DB, a.Now, a.Logger)
	a.Notifier.Register(runner)

	tasks := []periodicTask{
		{"expire reservations", 30 * time.Second, a.Sales.ExpireReservations},
		{"reconcile payments", 2 * time.Minute, a.Sales.ReconcilePayments},
		{"reconcile refunds", 5 * time.Minute, a.Sales.ReconcileRefunds},
		{"recover stale jobs", 5 * time.Minute, func(ctx context.Context) (int, error) {
			n, err := runner.RecoverStale(ctx, 10*time.Minute)
			return int(n), err
		}},
		{"purge completed jobs", 6 * time.Hour, func(ctx context.Context) (int, error) {
			n, err := runner.PurgeCompleted(ctx, 14*24*time.Hour)
			return int(n), err
		}},
	}
	for _, t := range tasks {
		go a.runPeriodic(ctx, t)
	}
	runner.Run(ctx)
}

func (a *App) runPeriodic(ctx context.Context, t periodicTask) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		n, err := t.fn(ctx)
		switch {
		case err != nil && ctx.Err() == nil:
			a.Logger.Error("periodic task failed", "task", t.name, "error", err)
		case n > 0:
			a.Logger.Info("periodic task", "task", t.name, "processed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
