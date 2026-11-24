package app_test

// Integration tests run the whole application against a real MySQL database. They are
// skipped when no test database is reachable. Configure it with TEST_DATABASE_DSN
// (default: gotaticket:gotaticket@tcp(127.0.0.1:3306)/gotaticket_test). The schema is
// dropped and re-created at the start of the run, so never point this at real data.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/app"
	"github.com/kotswanemoeketsi/gotaticket/internal/config"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/database"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/mailer"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
	"github.com/kotswanemoeketsi/gotaticket/migrations"
)

const (
	testPassword   = "Passw0rd!x"
	superAdminMail = "root@gotaticket.test"
)

var (
	testDSN     string
	dbAvailable bool
)

func TestMain(m *testing.M) {
	testDSN = os.Getenv("TEST_DATABASE_DSN")
	if testDSN == "" {
		testDSN = "gotaticket:gotaticket@tcp(127.0.0.1:3306)/gotaticket_test"
	}
	if err := resetSchema(testDSN); err != nil {
		fmt.Fprintf(os.Stderr, "integration tests skipped: %v\n", err)
	} else {
		dbAvailable = true
	}
	os.Exit(m.Run())
}

// resetSchema drops every table and applies the migrations from scratch.
func resetSchema(dsn string) error {
	normalized, err := database.NormalizeDSN(dsn, true)
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", normalized)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return err
		}
		tables = append(tables, "`"+t+"`")
	}
	rows.Close()
	if len(tables) > 0 {
		if _, err := db.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0; DROP TABLE "+strings.Join(tables, ", ")+"; SET FOREIGN_KEY_CHECKS = 1"); err != nil {
			return err
		}
	}
	return database.Migrate(ctx, dsn, migrations.FS, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// captureMailer records sent messages instead of delivering them.
type captureMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (c *captureMailer) Send(_ context.Context, m mailer.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m)
	return nil
}

func (c *captureMailer) to(addr string) []mailer.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []mailer.Message
	for _, m := range c.sent {
		for _, to := range m.To {
			if strings.EqualFold(to, addr) {
				out = append(out, m)
			}
		}
	}
	return out
}

type env struct {
	t     *testing.T
	app   *app.App
	srv   *httptest.Server
	clock *clock.Fake
	mail  *captureMailer
	ctx   context.Context
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith builds an environment after letting configure adjust the settings and add
// application options.
func newEnvWith(t *testing.T, configure func(*config.Config) []app.Option) *env {
	t.Helper()
	if !dbAvailable {
		t.Skip("MySQL test database not available")
	}
	cfg := config.Config{
		AppName: "GotATicket", Env: "test", HTTPAddr: "127.0.0.1:0", PublicURL: "http://web.test", APIURL: "http://api.test",
		DatabaseDSN: testDSN, JWTSecret: strings.Repeat("j", 40), QRSigningKey: strings.Repeat("q", 40),
		// Long-lived access tokens let tests move the clock forward without re-authenticating.
		AccessTokenTTL: 30 * 24 * time.Hour, RefreshTokenTTL: 60 * 24 * time.Hour, PasswordIterations: 10_000, AuthRateLimit: 100_000,
		DefaultCurrency: "ZAR", DefaultTimezone: "Africa/Johannesburg", DefaultCommissionBPS: 350,
		ReservationTTL: 15 * time.Minute, EFTPaymentWindow: 48 * time.Hour, CheckinOpensBefore: 4 * time.Hour, CheckinClosesAfterEnd: 2 * time.Hour,
		PaymentProvider: "sandbox", SandboxSecret: "sandbox-secret", EFTEnabled: true, EFTBankName: "Test Bank", EFTAccountName: "GotATicket",
		EFTAccountNumber: "123", EFTBranchCode: "456", MailFrom: "GotATicket <no-reply@gotaticket.test>", StorageDir: t.TempDir(),
		BootstrapAdminEmail: superAdminMail, BootstrapAdminPassword: testPassword,
	}
	clk := clock.NewFake(time.Now().UTC().Truncate(time.Second))
	mail := &captureMailer{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	opts := []app.Option{app.WithClock(clk.Now), app.WithMailer(mail), app.WithCache(cache.NewMemory())}
	if configure != nil {
		opts = append(opts, configure(&cfg)...)
	}
	a, err := app.New(ctx, cfg, logger, opts...)
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(func() {
		srv.Close()
		a.Close()
	})
	return &env{t: t, app: a, srv: srv, clock: clk, mail: mail, ctx: ctx}
}

// response is a decoded API response.
type response struct {
	Status int
	Data   json.RawMessage
	Error  struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields"`
	}
	Body   []byte
	Header http.Header
}

func (r response) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Data, v); err != nil {
		t.Fatalf("decode %s: %v", string(r.Data), err)
	}
}

func (e *env) request(method, path, token string, body any, headers ...string) response {
	e.t.Helper()
	var reader io.Reader
	if raw, ok := body.([]byte); ok {
		reader = bytes.NewReader(raw)
	} else if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := response{Status: res.StatusCode, Body: raw, Header: res.Header}
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		out.Data = envelope.Data
		if len(envelope.Error) > 0 {
			_ = json.Unmarshal(envelope.Error, &out.Error)
		}
	}
	return out
}

// must asserts the response status.
func (e *env) must(status int, r response) response {
	e.t.Helper()
	if r.Status != status {
		e.t.Fatalf("expected HTTP %d, got %d: %s", status, r.Status, string(r.Body))
	}
	return r
}

func (e *env) login(email string) string {
	e.t.Helper()
	r := e.must(200, e.request("POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}))
	var s struct {
		AccessToken string `json:"access_token"`
	}
	r.decode(e.t, &s)
	return s.AccessToken
}

// user creates an account with the given platform role slugs and returns a token for it.
func (e *env) user(prefix string, roles ...string) (email, token string) {
	e.t.Helper()
	email = fmt.Sprintf("%s-%s@example.test", prefix, strings.ToLower(random.Code(6)))
	e.must(201, e.request("POST", "/api/v1/auth/register", "", map[string]string{
		"first_name": "Test", "last_name": prefix, "email": email, "password": testPassword,
	}))
	for _, role := range roles {
		if _, err := e.app.DB.ExecContext(e.ctx, `INSERT INTO user_roles (user_id, role_id, created_at)
			SELECT u.id, r.id, NOW() FROM users u, roles r WHERE u.email = ? AND r.slug = ?`, email, role); err != nil {
			e.t.Fatal(err)
		}
	}
	return email, e.login(email)
}

type eventFixture struct {
	OrganiserID int64
	EventID     int64
	VenueID     int64
	Types       []int64
	OwnerToken  string
	OwnerEmail  string
}

// publishedEvent creates an organiser with an owner, an event starting in startsIn with the
// given ticket types (name → price cents/quantity), and publishes it.
func (e *env) publishedEvent(adminToken string, startsIn time.Duration, capacity int, types ...map[string]any) eventFixture {
	e.t.Helper()
	ownerEmail, _ := e.user("owner")
	var org struct {
		ID int64 `json:"id"`
	}
	e.must(201, e.request("POST", "/api/v1/admin/organisers", adminToken, map[string]any{
		"name": "Org " + random.Code(5), "email": "org@example.test", "owner_email": ownerEmail,
	})).decode(e.t, &org)
	ownerToken := e.login(ownerEmail)

	var venue struct {
		ID int64 `json:"id"`
	}
	e.must(201, e.request("POST", "/api/v1/admin/venues", adminToken, map[string]any{"name": "Hall", "city": "Johannesburg"})).decode(e.t, &venue)

	loc, _ := time.LoadLocation("Africa/Johannesburg")
	start := e.clock.Now().Add(startsIn).In(loc)
	var ev struct {
		ID int64 `json:"id"`
	}
	e.must(201, e.request("POST", "/api/v1/admin/events", ownerToken, map[string]any{
		"organiser_id": org.ID, "venue_id": venue.ID, "title": "Event " + random.Code(4), "description": "Test event",
		"starts_at": start.Format("2006-01-02T15:04"), "ends_at": start.Add(4 * time.Hour).Format("2006-01-02T15:04"),
		"timezone": "Africa/Johannesburg", "capacity": capacity, "refunds_allowed": true, "refund_cutoff_hours": 1,
	})).decode(e.t, &ev)

	fx := eventFixture{OrganiserID: org.ID, EventID: ev.ID, VenueID: venue.ID, OwnerToken: ownerToken, OwnerEmail: ownerEmail}
	for _, tt := range types {
		var created struct {
			ID int64 `json:"id"`
		}
		e.must(201, e.request("POST", fmt.Sprintf("/api/v1/admin/events/%d/ticket-types", ev.ID), ownerToken, tt)).decode(e.t, &created)
		fx.Types = append(fx.Types, created.ID)
	}
	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/events/%d/submit", ev.ID), ownerToken, nil))
	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/events/%d/approve", ev.ID), adminToken, nil))
	return fx
}

type orderView struct {
	ID            int64  `json:"id"`
	Reference     string `json:"reference"`
	Status        string `json:"status"`
	TotalCents    int64  `json:"total_cents"`
	DiscountCents int64  `json:"discount_cents"`
	RefundedCents int64  `json:"refunded_cents"`
	Items         []struct {
		ID       int64 `json:"id"`
		Quantity int   `json:"quantity"`
	} `json:"items"`
	Tickets []struct {
		ID           int64  `json:"id"`
		Status       string `json:"status"`
		CheckinState string `json:"checkin_state"`
		QRPayload    string `json:"qr_payload"`
		PriceCents   int64  `json:"price_cents"`
	} `json:"tickets"`
	Payments []struct {
		ID                int64  `json:"id"`
		Method            string `json:"method"`
		Status            string `json:"status"`
		ProviderReference string `json:"provider_reference"`
		RefundedCents     int64  `json:"refunded_cents"`
	} `json:"payments"`
	Refunds []struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	} `json:"refunds"`
}

// reserve starts an order for a single ticket type.
func (e *env) reserve(token string, eventID, ticketTypeID int64, qty int, promo string) (orderView, response) {
	e.t.Helper()
	r := e.request("POST", "/api/v1/orders", token, map[string]any{
		"event_id": eventID, "items": []map[string]any{{"ticket_type_id": ticketTypeID, "quantity": qty}}, "promo_code": promo,
	})
	var o orderView
	if r.Status == 201 {
		r.decode(e.t, &o)
	}
	return o, r
}

func attendeesFor(o orderView) []map[string]any {
	var out []map[string]any
	for _, it := range o.Items {
		for i := 0; i < it.Quantity; i++ {
			out = append(out, map[string]any{"order_item_id": it.ID, "first_name": "Guest", "last_name": fmt.Sprint(i), "email": "guest@example.test"})
		}
	}
	return out
}

// checkout submits attendees and the payment method, returning the payment reference for
// online payments.
func (e *env) checkout(token string, o orderView, method string) (string, response) {
	e.t.Helper()
	r := e.request("POST", fmt.Sprintf("/api/v1/orders/%d/checkout", o.ID), token, map[string]any{"attendees": attendeesFor(o), "payment_method": method})
	if r.Status != 200 {
		return "", r
	}
	var res struct {
		Order orderView `json:"order"`
	}
	r.decode(e.t, &res)
	for _, p := range res.Order.Payments {
		if p.Method == "online" && p.Status == "pending" {
			return p.ProviderReference, r
		}
	}
	return "", r
}

// payViaWebhook completes a sandbox payment and delivers its signed webhook to the API.
func (e *env) payViaWebhook(reference string) response {
	e.t.Helper()
	txn, err := e.app.Sandbox.Complete(e.ctx, reference, true)
	if err != nil {
		e.t.Fatal(err)
	}
	body, signature := e.app.Sandbox.WebhookBody(txn)
	return e.request("POST", "/api/v1/payments/webhooks/sandbox", "", body, "X-Sandbox-Signature", signature)
}

// buy reserves, checks out and pays online, returning the paid order.
func (e *env) buy(token string, eventID, ticketTypeID int64, qty int) orderView {
	e.t.Helper()
	o, r := e.reserve(token, eventID, ticketTypeID, qty, "")
	e.must(201, r)
	ref, r := e.checkout(token, o, "online")
	e.must(200, r)
	e.must(200, e.payViaWebhook(ref))
	return e.order(token, o.ID)
}

func (e *env) order(token string, id int64) orderView {
	e.t.Helper()
	var o orderView
	e.must(200, e.request("GET", fmt.Sprintf("/api/v1/orders/%d", id), token, nil)).decode(e.t, &o)
	return o
}

func (e *env) adminOrder(token string, id int64) orderView {
	e.t.Helper()
	var o orderView
	e.must(200, e.request("GET", fmt.Sprintf("/api/v1/admin/orders/%d", id), token, nil)).decode(e.t, &o)
	return o
}

// inventory returns quantity_sold and quantity_reserved for a ticket type.
func (e *env) inventory(ticketTypeID int64) (sold, reserved int) {
	e.t.Helper()
	if err := e.app.DB.QueryRowContext(e.ctx, `SELECT quantity_sold, quantity_reserved FROM ticket_types WHERE id = ?`, ticketTypeID).Scan(&sold, &reserved); err != nil {
		e.t.Fatal(err)
	}
	return sold, reserved
}

type scanResult struct {
	Result  string `json:"result"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func (e *env) scan(token string, eventID int64, code, direction string) scanResult {
	e.t.Helper()
	var res scanResult
	e.must(200, e.request("POST", "/api/v1/admin/checkin/scan", token, map[string]any{"event_id": eventID, "code": code, "direction": direction})).decode(e.t, &res)
	return res
}
