package app_test

import (
	"io"
	"log/slog"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/jobs"
)

// newRunner returns a job runner wired to the app's email handlers, for running queued jobs
// synchronously inside tests.
func newRunner(e *env) *jobs.Runner {
	r := jobs.NewRunner(e.app.DB, e.app.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.app.Notifier.Register(r)
	return r
}
