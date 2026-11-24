// Package app wires the modules of the modular monolith together: it owns configuration,
// infrastructure (database, cache, mailer, storage) and the construction order of services.
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/ai"
	"github.com/kotswanemoeketsi/gotaticket/internal/auth"
	"github.com/kotswanemoeketsi/gotaticket/internal/checkin"
	"github.com/kotswanemoeketsi/gotaticket/internal/config"
	"github.com/kotswanemoeketsi/gotaticket/internal/dashboard"
	"github.com/kotswanemoeketsi/gotaticket/internal/events"
	"github.com/kotswanemoeketsi/gotaticket/internal/identity"
	"github.com/kotswanemoeketsi/gotaticket/internal/notifications"
	"github.com/kotswanemoeketsi/gotaticket/internal/organisers"
	"github.com/kotswanemoeketsi/gotaticket/internal/payments"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/mailer"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/ratelimit"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/storage"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/web"
	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
	"github.com/kotswanemoeketsi/gotaticket/internal/reports"
	"github.com/kotswanemoeketsi/gotaticket/internal/sales"
	"github.com/kotswanemoeketsi/gotaticket/internal/tickets"
	"github.com/kotswanemoeketsi/gotaticket/migrations"
)

// App holds every service of the platform.
type App struct {
	Config config.Config
	Logger *slog.Logger
	Now    clock.Func

	DB      *database.DB
	Cache   cache.Cache
	Mailer  mailer.Mailer
	Storage *storage.Local
	Limiter *ratelimit.Limiter
	Tokens  *auth.TokenIssuer
	Loader  *auth.PrincipalLoader
	Signer  *tickets.Signer
	Gateway payments.Gateway
	Sandbox *payments.Sandbox // nil unless PAYMENT_PROVIDER=sandbox

	Identity   *identity.Service
	web        http.Handler
	Organisers *organisers.Service
	Events     *events.Service
	Sales      *sales.Service
	Checkin    *checkin.Service
	Reports    *reports.Service
	Dashboard  *dashboard.Service
	Notifier   *notifications.Notifier
	AI         *ai.Service
}

// Option customises construction, mainly for tests.
type Option func(*options)

type options struct {
	now            clock.Func
	mailer         mailer.Mailer
	cache          cache.Cache
	googleAuthURL  string
	googleTokenURL string
}

// WithClock replaces the system clock.
func WithClock(now clock.Func) Option { return func(o *options) { o.now = now } }

// WithMailer replaces the configured mailer.
func WithMailer(m mailer.Mailer) Option { return func(o *options) { o.mailer = m } }

// WithGoogleEndpoints points sign-in with Google at other URLs (used by tests).
func WithGoogleEndpoints(authURL, tokenURL string) Option {
	return func(o *options) { o.googleAuthURL, o.googleTokenURL = authURL, tokenURL }
}

// WithCache replaces the configured cache.
func WithCache(c cache.Cache) Option { return func(o *options) { o.cache = c } }

// New connects to infrastructure, applies migrations and builds every service.
func New(ctx context.Context, cfg config.Config, logger *slog.Logger, opts ...Option) (*App, error) {
	o := options{now: clock.System}
	for _, opt := range opts {
		opt(&o)
	}
	a := &App{Config: cfg, Logger: logger, Now: o.now}

	if cfg.AutoMigrate {
		if err := database.Migrate(ctx, cfg.DatabaseDSN, migrations.FS, logger); err != nil {
			return nil, err
		}
	}
	db, err := database.Open(ctx, cfg.DatabaseDSN, database.Options{
		MaxOpenConns: 25, MaxIdleConns: 10, ConnMaxLifetime: 30 * time.Minute, ConnectTimeout: 60 * time.Second,
	}, logger)
	if err != nil {
		return nil, err
	}
	a.DB = db
	if err := rbac.Sync(ctx, db, a.Now()); err != nil {
		return nil, fmt.Errorf("sync permissions: %w", err)
	}

	switch {
	case o.cache != nil:
		a.Cache = o.cache
	case cfg.RedisURL != "":
		if a.Cache, err = cache.NewRedis(ctx, cfg.RedisURL, "gotaticket:"); err != nil {
			return nil, err
		}
	default:
		logger.Warn("REDIS_URL not set: using an in-memory cache (fine for a single instance, not for production)")
		a.Cache = cache.NewMemory()
	}

	switch {
	case o.mailer != nil:
		a.Mailer = o.mailer
	case cfg.SMTPHost != "":
		a.Mailer = mailer.NewSMTP(mailer.SMTPConfig{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword, TLS: mailer.TLSMode(cfg.SMTPTLS), From: cfg.MailFrom})
	default:
		if a.Mailer, err = mailer.NewFile(cfg.MailDir, cfg.MailFrom, logger); err != nil {
			return nil, err
		}
	}

	if a.Storage, err = storage.NewLocal(cfg.StorageDir, "/uploads"); err != nil {
		return nil, err
	}
	a.Limiter = ratelimit.New(a.Cache)
	a.Tokens = auth.NewTokenIssuer(cfg.JWTSecret, cfg.AccessTokenTTL, a.Now)
	a.Loader = auth.NewPrincipalLoader(db, a.Cache, 60*time.Second)
	a.Signer = tickets.NewSigner(cfg.QRSigningKey)

	switch cfg.PaymentProvider {
	case "ozow":
		a.Gateway = payments.NewOzow(payments.OzowConfig{
			ClientID: cfg.OzowClientID, ClientSecret: cfg.OzowClientSecret, SiteCode: cfg.OzowSiteCode,
			WebhookSecret: cfg.OzowWebhookSecret, BaseURL: cfg.OzowBaseURL,
		}, func(ctx context.Context, reference string) (string, time.Time, error) {
			var id string
			var created time.Time
			err := db.QueryRowContext(ctx, `SELECT provider_request_id, created_at FROM payments WHERE provider = 'ozow' AND provider_reference = ?`, reference).Scan(&id, &created)
			if errors.Is(err, sql.ErrNoRows) {
				err = payments.ErrNotFound
			}
			return id, created, err
		})
	default:
		// The sandbox plays the part of an external gateway, so its records must outlive one
		// process (`gotaticket seed` pays for orders that `gotaticket serve` later refunds).
		// Redis provides that; without Redis the in-memory cache would not, so use files.
		sandboxStore := a.Cache
		if _, inMemory := a.Cache.(*cache.Memory); inMemory {
			if sandboxStore, err = cache.NewDir(filepath.Join(filepath.Dir(cfg.StorageDir), "sandbox")); err != nil {
				return nil, err
			}
		}
		a.Sandbox = payments.NewSandbox(sandboxStore, cfg.SandboxSecret, cfg.APIURL, internalURL(cfg.HTTPAddr)+"/api/v1/payments/webhooks/sandbox", a.Now, logger)
		a.Gateway = a.Sandbox
	}

	if cfg.WebDir != "" {
		if a.web, err = web.Handler(cfg.WebDir); err != nil {
			return nil, err
		}
	}

	a.Notifier = notifications.New(a.Mailer, nil, cfg.AppName, cfg.PublicURL, logger)
	a.Identity, err = identity.NewService(db, auth.PasswordHasher{Iterations: cfg.PasswordIterations}, a.Tokens, a.Loader, a.Limiter, a.Notifier,
		identity.Config{AppName: cfg.AppName, PublicURL: cfg.PublicURL, RefreshTokenTTL: cfg.RefreshTokenTTL}, a.Now, logger)
	if err != nil {
		return nil, err
	}
	a.Identity.EnableGoogle(identity.GoogleConfig{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		RedirectURL:  cfg.PublicURL + "/api/v1/auth/google/callback",
		AuthURL:      o.googleAuthURL,
		TokenURL:     o.googleTokenURL,
	}, a.Cache)
	a.Organisers = organisers.NewService(db, a.Identity, a.Loader, cfg.DefaultCommissionBPS, a.Now, logger)
	a.Events = events.NewService(db, a.Cache, events.Config{DefaultCurrency: cfg.DefaultCurrency, DefaultTimezone: cfg.DefaultTimezone}, a.Now, logger)
	a.Sales = sales.NewService(db, a.Gateway, a.Signer, a.Limiter, sales.Config{
		AppName: cfg.AppName, PublicURL: cfg.PublicURL, ReservationTTL: cfg.ReservationTTL, EFTEnabled: cfg.EFTEnabled, EFTWindow: cfg.EFTPaymentWindow,
		Bank: sales.BankDetails{BankName: cfg.EFTBankName, AccountName: cfg.EFTAccountName, AccountNumber: cfg.EFTAccountNumber, BranchCode: cfg.EFTBranchCode},
	}, a.Now, logger)
	a.Events.SetCancellationHook(a.Sales)
	a.Notifier.SetSales(a.Sales)
	a.Checkin = checkin.NewService(db, a.Signer, checkin.Config{OpensBefore: cfg.CheckinOpensBefore, ClosesAfterEnd: cfg.CheckinClosesAfterEnd}, a.Now)
	a.Reports = reports.NewService(db, cfg.DefaultTimezone, cfg.DefaultCurrency, cfg.AppName, a.Now)
	a.Dashboard = dashboard.NewService(db, cfg.DefaultTimezone, cfg.DefaultCurrency, a.Now)

	// AI features are off until DEEPSEEK_API_KEY is set.
	var llm ai.Completer
	if cfg.AIAPIKey != "" {
		llm = ai.NewClient(cfg.AIBaseURL, cfg.AIAPIKey, cfg.AITimeout)
		logger.Info("AI features on", "base_url", cfg.AIBaseURL, "model", cfg.AIModel, "review_model", cfg.AIReviewModel)
	}
	loc, err := time.LoadLocation(cfg.DefaultTimezone)
	if err != nil {
		return nil, err
	}
	a.AI = ai.NewService(ai.Deps{LLM: llm, DB: db, Cache: a.Cache, Limiter: a.Limiter, Now: a.Now, Events: a.Events, Logger: logger},
		ai.Config{Model: cfg.AIModel, ReviewModel: cfg.AIReviewModel, DailyTokenLimit: cfg.AIDailyTokenLimit, Timezone: loc})

	if err := a.bootstrapSuperAdmin(ctx); err != nil {
		return nil, fmt.Errorf("bootstrap super administrator: %w", err)
	}
	return a, nil
}

// Close releases infrastructure connections.
func (a *App) Close() {
	if a.Cache != nil {
		_ = a.Cache.Close()
	}
	if a.DB != nil {
		_ = a.DB.Close()
	}
}

// internalURL is how the process reaches its own HTTP server (used by the sandbox gateway
// to deliver webhooks).
func internalURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// bootstrapSuperAdmin creates the first super administrator from BOOTSTRAP_ADMIN_* settings
// when no super administrator exists yet. It never changes an existing account's password.
func (a *App) bootstrapSuperAdmin(ctx context.Context) error {
	email, password := a.Config.BootstrapAdminEmail, a.Config.BootstrapAdminPassword
	var existing int
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_roles ur JOIN roles r ON r.id = ur.role_id WHERE r.slug = ?`, rbac.RoleSuperAdmin).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	if email == "" || password == "" {
		a.Logger.Warn("no super administrator exists; set BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD and restart to create one")
		return nil
	}
	hash, err := auth.PasswordHasher{Iterations: a.Config.PasswordIterations}.Hash(password)
	if err != nil {
		return err
	}
	return a.DB.WithTx(ctx, func(tx *sql.Tx) error {
		now := a.Now()
		var userID int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&userID)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			res, err := tx.ExecContext(ctx, `INSERT INTO users (email, password_hash, first_name, last_name, status, email_verified_at, password_changed_at, created_at, updated_at)
				VALUES (?, ?, 'Super', 'Administrator', 'active', ?, ?, ?, ?)`, email, hash, now, now, now, now)
			if err != nil {
				return err
			}
			userID, _ = res.LastInsertId()
		case err != nil:
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT IGNORE INTO user_roles (user_id, role_id, created_at)
			SELECT ?, id, ? FROM roles WHERE slug = ?`, userID, now, rbac.RoleSuperAdmin); err != nil {
			return err
		}
		a.Logger.Info("super administrator created; change the bootstrap password after signing in", "email", email)
		return nil
	})
}
