// Command gotaticket runs the GotATicket platform.
//
//	gotaticket serve    run the HTTP API (and, with EMBEDDED_WORKER=true, the worker)
//	gotaticket worker   run the background worker only
//	gotaticket migrate  apply database migrations and exit
//	gotaticket seed     load demo data into an empty database
//	gotaticket healthcheck  exit 0 if the local API answers its health check (for Docker)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // event time zones work even on hosts without a zoneinfo database

	"github.com/kotswanemoeketsi/gotaticket/internal/app"
	"github.com/kotswanemoeketsi/gotaticket/internal/catalog"
	"github.com/kotswanemoeketsi/gotaticket/internal/config"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/seed"
	"github.com/kotswanemoeketsi/gotaticket/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "help" || command == "-h" || command == "--help" {
		fmt.Println("usage: gotaticket [serve|worker|migrate|seed|healthcheck]")
		return nil
	}
	if command == "healthcheck" {
		return healthcheck()
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	logger := newLogger(cfg)
	slog.SetDefault(logger)
	if cfg.UsingDevSecrets() {
		logger.Warn("using built-in development secrets; set JWT_SECRET and QR_SIGNING_KEY before going live")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch command {
	case "serve":
		return serve(ctx, cfg, logger)
	case "worker":
		a, err := app.New(ctx, cfg, logger)
		if err != nil {
			return err
		}
		defer a.Close()
		logger.Info("worker started")
		a.RunWorker(ctx)
		return nil
	case "migrate":
		return database.Migrate(ctx, cfg.DatabaseDSN, migrations.FS, logger)
	case "seed":
		// The seeder replays six weeks of activity, so the app runs on a simulated clock.
		simulated := clock.NewFake(time.Now())
		a, err := app.New(ctx, cfg, logger, app.WithClock(simulated.Now))
		if err != nil {
			return err
		}
		defer a.Close()
		return seed.Run(ctx, a, simulated)
	default:
		return fmt.Errorf("unknown command %q (expected serve, worker, migrate, seed or healthcheck)", command)
	}
}

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	a, err := app.New(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer a.Close()
	// Venues saved before locations were tracked get their province and coordinates here.
	if n, err := catalog.BackfillVenueLocations(ctx, a.DB, time.Now()); err != nil {
		logger.Warn("backfill venue locations", "error", err)
	} else if n > 0 {
		logger.Info("filled in venue locations", "venues", n)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("API listening", "addr", cfg.HTTPAddr, "payment_provider", a.Gateway.Name(), "env", cfg.Env)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	if cfg.EmbeddedWorker {
		go a.RunWorker(workerCtx)
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	stopWorker()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}

// healthcheck asks the API running in this container whether it is healthy. Container
// images have no curl or wget, so the binary checks itself.
func healthcheck() error {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR: %w", err)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://127.0.0.1:" + port + "/api/v1/health")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", res.StatusCode)
	}
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
