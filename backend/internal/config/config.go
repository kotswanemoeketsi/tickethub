// Package config loads application settings from environment variables (and an optional
// .env file for local development) and validates them once at start-up.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every runtime setting. Field comments name the environment variable.
type Config struct {
	AppName   string // APP_NAME
	Env       string // APP_ENV: development | production
	HTTPAddr  string // HTTP_ADDR
	PublicURL string // PUBLIC_URL: where the web app is served (links in emails, payment returns)
	APIURL    string // API_URL: public base URL of this API (sandbox checkout pages)

	DatabaseDSN string // DATABASE_DSN, e.g. user:pass@tcp(127.0.0.1:3306)/gotaticket
	AutoMigrate bool   // AUTO_MIGRATE
	RedisURL    string // REDIS_URL (optional; in-memory cache is used when empty)

	JWTSecret          string        // JWT_SECRET
	AccessTokenTTL     time.Duration // ACCESS_TOKEN_TTL
	RefreshTokenTTL    time.Duration // REFRESH_TOKEN_TTL
	CookieSecure       bool          // COOKIE_SECURE
	TrustProxyHeaders  bool          // TRUST_PROXY_HEADERS
	CORSAllowedOrigins []string      // CORS_ALLOWED_ORIGINS (comma separated)
	PasswordIterations int           // PASSWORD_HASH_ITERATIONS
	AuthRateLimit      int           // AUTH_RATE_LIMIT_PER_MINUTE: sign-in/registration requests per IP

	QRSigningKey string // QR_SIGNING_KEY

	// WebDir is the built React app to serve at "/" (empty: API only, e.g. behind Vite).
	WebDir string // WEB_DIR

	// Sign-in with Google (OpenID Connect). Disabled unless both are set.
	GoogleClientID     string // GOOGLE_CLIENT_ID
	GoogleClientSecret string // GOOGLE_CLIENT_SECRET

	// AI features (DeepSeek, or any OpenAI-compatible API). Disabled unless the key is set.
	AIAPIKey          string        // DEEPSEEK_API_KEY
	AIBaseURL         string        // AI_BASE_URL
	AIModel           string        // AI_MODEL: everyday features (search, writing)
	AIReviewModel     string        // AI_REVIEW_MODEL: the event pre-check
	AITimeout         time.Duration // AI_TIMEOUT
	AIDailyTokenLimit int           // AI_DAILY_TOKEN_LIMIT: platform-wide cap, 0 for none

	DefaultCurrency       string        // DEFAULT_CURRENCY
	DefaultTimezone       string        // DEFAULT_TIMEZONE
	DefaultCommissionBPS  int           // DEFAULT_COMMISSION_BPS (350 = 3.5%)
	ReservationTTL        time.Duration // RESERVATION_TTL
	EFTPaymentWindow      time.Duration // EFT_PAYMENT_WINDOW
	CheckinOpensBefore    time.Duration // CHECKIN_OPENS_BEFORE
	CheckinClosesAfterEnd time.Duration // CHECKIN_CLOSES_AFTER_END

	PaymentProvider   string // PAYMENT_PROVIDER: sandbox | ozow
	OzowClientID      string // OZOW_CLIENT_ID
	OzowClientSecret  string // OZOW_CLIENT_SECRET
	OzowSiteCode      string // OZOW_SITE_CODE
	OzowWebhookSecret string // OZOW_WEBHOOK_SECRET
	OzowBaseURL       string // OZOW_BASE_URL
	SandboxSecret     string // SANDBOX_WEBHOOK_SECRET

	EFTEnabled       bool   // EFT_ENABLED
	EFTBankName      string // EFT_BANK_NAME
	EFTAccountName   string // EFT_ACCOUNT_NAME
	EFTAccountNumber string // EFT_ACCOUNT_NUMBER
	EFTBranchCode    string // EFT_BRANCH_CODE

	MailFrom     string // MAIL_FROM
	SMTPHost     string // SMTP_HOST (optional; emails are written to MAIL_DIR when empty)
	SMTPPort     int    // SMTP_PORT
	SMTPUsername string // SMTP_USERNAME
	SMTPPassword string // SMTP_PASSWORD
	SMTPTLS      string // SMTP_TLS: none | starttls | tls
	MailDir      string // MAIL_DIR

	StorageDir string // STORAGE_DIR

	EmbeddedWorker bool // EMBEDDED_WORKER: run the background worker inside the API process

	BootstrapAdminEmail    string // BOOTSTRAP_ADMIN_EMAIL
	BootstrapAdminPassword string // BOOTSTRAP_ADMIN_PASSWORD

	LogLevel  string // LOG_LEVEL
	LogFormat string // LOG_FORMAT: text | json
}

// IsProduction reports whether the app runs with production safeguards.
func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads the configuration. Values from a .env file (when present) never override
// variables that are already set in the environment.
func Load() (Config, error) {
	if err := LoadDotEnv(".env"); err != nil {
		return Config{}, err
	}
	var errs []error
	env := newReader(&errs)

	cfg := Config{
		AppName:   env.str("APP_NAME", "GotATicket"),
		Env:       env.str("APP_ENV", "development"),
		HTTPAddr:  env.str("HTTP_ADDR", ":8080"),
		PublicURL: strings.TrimSuffix(env.str("PUBLIC_URL", "http://localhost:5173"), "/"),
		APIURL:    strings.TrimSuffix(env.str("API_URL", "http://localhost:8080"), "/"),

		DatabaseDSN: env.str("DATABASE_DSN", "gotaticket:gotaticket@tcp(127.0.0.1:3306)/gotaticket"),
		AutoMigrate: env.bool("AUTO_MIGRATE", true),
		RedisURL:    env.str("REDIS_URL", ""),

		JWTSecret:          env.str("JWT_SECRET", ""),
		AccessTokenTTL:     env.duration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:    env.duration("REFRESH_TOKEN_TTL", 30*24*time.Hour),
		CookieSecure:       env.bool("COOKIE_SECURE", false),
		TrustProxyHeaders:  env.bool("TRUST_PROXY_HEADERS", false),
		CORSAllowedOrigins: env.list("CORS_ALLOWED_ORIGINS"),
		PasswordIterations: env.int("PASSWORD_HASH_ITERATIONS", 600_000),
		AuthRateLimit:      env.int("AUTH_RATE_LIMIT_PER_MINUTE", 30),

		QRSigningKey: env.str("QR_SIGNING_KEY", ""),

		GoogleClientID:     env.str("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: env.str("GOOGLE_CLIENT_SECRET", ""),

		AIAPIKey:          env.str("DEEPSEEK_API_KEY", ""),
		AIBaseURL:         strings.TrimSuffix(env.str("AI_BASE_URL", "https://api.deepseek.com"), "/"),
		AIModel:           env.str("AI_MODEL", "deepseek-flash"),
		AIReviewModel:     env.str("AI_REVIEW_MODEL", "deepseek-v4-pro"),
		AITimeout:         env.duration("AI_TIMEOUT", 30*time.Second),
		AIDailyTokenLimit: env.int("AI_DAILY_TOKEN_LIMIT", 2_000_000),

		DefaultCurrency:       strings.ToUpper(env.str("DEFAULT_CURRENCY", "ZAR")),
		DefaultTimezone:       env.str("DEFAULT_TIMEZONE", "Africa/Johannesburg"),
		DefaultCommissionBPS:  env.int("DEFAULT_COMMISSION_BPS", 350),
		ReservationTTL:        env.duration("RESERVATION_TTL", 15*time.Minute),
		EFTPaymentWindow:      env.duration("EFT_PAYMENT_WINDOW", 48*time.Hour),
		CheckinOpensBefore:    env.duration("CHECKIN_OPENS_BEFORE", 4*time.Hour),
		CheckinClosesAfterEnd: env.duration("CHECKIN_CLOSES_AFTER_END", 2*time.Hour),

		PaymentProvider:   strings.ToLower(env.str("PAYMENT_PROVIDER", "sandbox")),
		OzowClientID:      env.str("OZOW_CLIENT_ID", ""),
		OzowClientSecret:  env.str("OZOW_CLIENT_SECRET", ""),
		OzowSiteCode:      env.str("OZOW_SITE_CODE", ""),
		OzowWebhookSecret: env.str("OZOW_WEBHOOK_SECRET", ""),
		OzowBaseURL:       strings.TrimRight(env.str("OZOW_BASE_URL", "https://one.ozow.com/v1"), "/"),
		SandboxSecret:     env.str("SANDBOX_WEBHOOK_SECRET", "sandbox-webhook-secret"),

		EFTEnabled:       env.bool("EFT_ENABLED", true),
		EFTBankName:      env.str("EFT_BANK_NAME", "First National Bank"),
		EFTAccountName:   env.str("EFT_ACCOUNT_NAME", "GotATicket (Pty) Ltd"),
		EFTAccountNumber: env.str("EFT_ACCOUNT_NUMBER", "62000000000"),
		EFTBranchCode:    env.str("EFT_BRANCH_CODE", "250655"),

		MailFrom:     env.str("MAIL_FROM", "GotATicket <no-reply@gotaticket.local>"),
		SMTPHost:     env.str("SMTP_HOST", ""),
		SMTPPort:     env.int("SMTP_PORT", 587),
		SMTPUsername: env.str("SMTP_USERNAME", ""),
		SMTPPassword: env.str("SMTP_PASSWORD", ""),
		SMTPTLS:      strings.ToLower(env.str("SMTP_TLS", "starttls")),
		MailDir:      env.str("MAIL_DIR", "./storage/mail"),

		StorageDir: env.str("STORAGE_DIR", "./storage/uploads"),
		WebDir:     env.str("WEB_DIR", ""),

		EmbeddedWorker: env.bool("EMBEDDED_WORKER", true),

		BootstrapAdminEmail:    env.str("BOOTSTRAP_ADMIN_EMAIL", ""),
		BootstrapAdminPassword: env.str("BOOTSTRAP_ADMIN_PASSWORD", ""),

		LogLevel:  strings.ToLower(env.str("LOG_LEVEL", "info")),
		LogFormat: strings.ToLower(env.str("LOG_FORMAT", "text")),
	}
	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

const devSecretWarning = "dev-only-insecure-secret-change-me-0123456789"

func (c *Config) validate() error {
	var errs []error
	if c.JWTSecret == "" || c.QRSigningKey == "" {
		if c.IsProduction() {
			errs = append(errs, errors.New("JWT_SECRET and QR_SIGNING_KEY must be set in production"))
		}
		if c.JWTSecret == "" {
			c.JWTSecret = devSecretWarning
		}
		if c.QRSigningKey == "" {
			c.QRSigningKey = devSecretWarning
		}
	}
	if len(c.JWTSecret) < 32 || len(c.QRSigningKey) < 32 {
		errs = append(errs, errors.New("JWT_SECRET and QR_SIGNING_KEY must be at least 32 characters"))
	}
	if _, err := time.LoadLocation(c.DefaultTimezone); err != nil {
		errs = append(errs, fmt.Errorf("DEFAULT_TIMEZONE: %w", err))
	}
	if len(c.DefaultCurrency) != 3 {
		errs = append(errs, errors.New("DEFAULT_CURRENCY must be a 3-letter ISO code"))
	}
	if c.DefaultCommissionBPS < 0 || c.DefaultCommissionBPS > 10_000 {
		errs = append(errs, errors.New("DEFAULT_COMMISSION_BPS must be between 0 and 10000"))
	}
	switch c.PaymentProvider {
	case "sandbox":
		if c.IsProduction() {
			errs = append(errs, errors.New("PAYMENT_PROVIDER=sandbox is not allowed in production"))
		}
	case "ozow":
		if c.OzowClientID == "" || c.OzowClientSecret == "" || c.OzowSiteCode == "" || c.OzowWebhookSecret == "" {
			errs = append(errs, errors.New("OZOW_CLIENT_ID, OZOW_CLIENT_SECRET, OZOW_SITE_CODE and OZOW_WEBHOOK_SECRET are required"))
		}
		if key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(c.OzowWebhookSecret, "whsec_")); err != nil || len(key) == 0 {
			errs = append(errs, errors.New("OZOW_WEBHOOK_SECRET must be a valid Svix signing secret"))
		}
		if c.OzowBaseURL != "https://one.ozow.com/v1" && c.OzowBaseURL != "https://stagingone.ozow.com/v1" {
			errs = append(errs, errors.New("OZOW_BASE_URL must be an official Ozow One API endpoint"))
		}
		if c.IsProduction() && c.OzowBaseURL != "https://one.ozow.com/v1" {
			errs = append(errs, errors.New("production requires the production Ozow endpoint"))
		}
		if c.DefaultCurrency != "ZAR" {
			errs = append(errs, errors.New("Ozow requires DEFAULT_CURRENCY=ZAR"))
		}
		for _, address := range []string{c.PublicURL, c.APIURL} {
			u, err := url.Parse(address)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Hostname() == "localhost" || u.User != nil {
				errs = append(errs, errors.New("Ozow requires public HTTPS PUBLIC_URL and API_URL"))
			}
		}
	default:
		errs = append(errs, fmt.Errorf("PAYMENT_PROVIDER must be sandbox or ozow, got %q", c.PaymentProvider))
	}
	if c.AIDailyTokenLimit < 0 {
		errs = append(errs, errors.New("AI_DAILY_TOKEN_LIMIT must be 0 (no limit) or more"))
	}
	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		errs = append(errs, errors.New("set both GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET, or neither"))
	}
	switch c.SMTPTLS {
	case "none", "starttls", "tls":
	default:
		errs = append(errs, errors.New("SMTP_TLS must be none, starttls or tls"))
	}
	if c.PasswordIterations < 10_000 {
		errs = append(errs, errors.New("PASSWORD_HASH_ITERATIONS must be at least 10000"))
	}
	if c.IsProduction() && c.PasswordIterations < 600_000 {
		errs = append(errs, errors.New("PASSWORD_HASH_ITERATIONS must be at least 600000 in production"))
	}
	if c.IsProduction() && !c.CookieSecure {
		errs = append(errs, errors.New("COOKIE_SECURE must be true in production"))
	}
	return errors.Join(errs...)
}

// UsingDevSecrets reports whether insecure development secrets are in use.
func (c Config) UsingDevSecrets() bool {
	return c.JWTSecret == devSecretWarning || c.QRSigningKey == devSecretWarning
}

// reader reads typed environment variables and collects parse errors.
type reader struct{ errs *[]error }

func newReader(errs *[]error) reader { return reader{errs: errs} }

func (r reader) str(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(v)
	}
	return def
}

func (r reader) bool(key string, def bool) bool {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*r.errs = append(*r.errs, fmt.Errorf("%s: expected true or false, got %q", key, v))
		return def
	}
	return b
}

func (r reader) int(key string, def int) int {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.ReplaceAll(v, "_", ""))
	if err != nil {
		*r.errs = append(*r.errs, fmt.Errorf("%s: expected an integer, got %q", key, v))
		return def
	}
	return n
}

func (r reader) duration(key string, def time.Duration) time.Duration {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*r.errs = append(*r.errs, fmt.Errorf("%s: expected a duration such as 15m or 48h, got %q", key, v))
		return def
	}
	return d
}

func (r reader) list(key string) []string {
	var out []string
	for _, part := range strings.Split(r.str(key, ""), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
