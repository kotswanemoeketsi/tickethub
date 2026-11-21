package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/cache"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/clock"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
)

// Sandbox is a self-contained test gateway for development. It serves its own hosted
// checkout page and, like a real provider, reports the outcome through a signed webhook,
// so local testing exercises exactly the same verification and fulfilment code as
// production. Transactions are kept in the shared cache so API and worker agree on them.
type Sandbox struct {
	store      cache.Cache
	secret     string
	apiURL     string
	webhookURL string
	client     *http.Client
	now        clock.Func
	logger     *slog.Logger
}

// NewSandbox returns the sandbox gateway. apiURL is the public base URL of the API (used for
// the checkout page link); webhookURL is where signed webhooks are delivered.
func NewSandbox(store cache.Cache, secret, apiURL, webhookURL string, now clock.Func, logger *slog.Logger) *Sandbox {
	return &Sandbox{store: store, secret: secret, apiURL: strings.TrimSuffix(apiURL, "/"), webhookURL: webhookURL,
		client: &http.Client{Timeout: 10 * time.Second}, now: now, logger: logger}
}

func (s *Sandbox) Name() string { return "sandbox" }

type sandboxTxn struct {
	Reference     string     `json:"reference"`
	TransactionID string     `json:"transaction_id"`
	AmountCents   int64      `json:"amount_cents"`
	Currency      string     `json:"currency"`
	Email         string     `json:"email"`
	Description   string     `json:"description"`
	CallbackURL   string     `json:"callback_url"`
	Status        Status     `json:"status"`
	PaidAt        *time.Time `json:"paid_at"`
}

const sandboxTTL = 72 * time.Hour

func sandboxKey(ref string) string { return "sandbox:txn:" + ref }

func (s *Sandbox) load(ctx context.Context, ref string) (sandboxTxn, error) {
	var t sandboxTxn
	ok, err := cache.GetJSON(ctx, s.store, sandboxKey(ref), &t)
	if err != nil {
		return t, err
	}
	if !ok {
		return t, ErrNotFound
	}
	return t, nil
}

func (s *Sandbox) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutSession, error) {
	t := sandboxTxn{
		Reference: req.Reference, TransactionID: "sbx_" + strings.ToLower(random.Code(12)), AmountCents: req.AmountCents,
		Currency: req.Currency, Email: req.Email, Description: req.Description, CallbackURL: req.CallbackURL, Status: StatusPending,
	}
	if err := cache.SetJSON(ctx, s.store, sandboxKey(req.Reference), t, sandboxTTL); err != nil {
		return CheckoutSession{}, err
	}
	return CheckoutSession{RedirectURL: s.apiURL + "/sandbox/checkout/" + req.Reference}, nil
}

func (s *Sandbox) VerifyPayment(ctx context.Context, reference string) (Verification, error) {
	t, err := s.load(ctx, reference)
	if err != nil {
		return Verification{}, err
	}
	raw, _ := json.Marshal(t)
	v := Verification{Reference: t.Reference, TransactionID: t.TransactionID, Status: t.Status, AmountCents: t.AmountCents,
		Currency: t.Currency, PaidAt: t.PaidAt, Raw: raw}
	if t.Status == StatusFailed {
		v.FailureReason = "Declined in sandbox"
	}
	return v, nil
}

func (s *Sandbox) sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Sandbox) ParseWebhook(header http.Header, body []byte) (WebhookEvent, error) {
	given, err := hex.DecodeString(header.Get("X-Sandbox-Signature"))
	if err != nil || len(given) == 0 {
		return WebhookEvent{}, ErrInvalidSignature
	}
	expected, _ := hex.DecodeString(s.sign(body))
	if !hmac.Equal(given, expected) {
		return WebhookEvent{}, ErrInvalidSignature
	}
	var envelope struct {
		Event string `json:"event"`
		Data  struct {
			Reference string `json:"reference"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return WebhookEvent{}, err
	}
	ev := WebhookEvent{Type: envelope.Event, Reference: envelope.Data.Reference, DedupeKey: dedupeKey(body), Payload: body}
	switch envelope.Event {
	case "charge.success", "charge.failed":
		ev.Kind = WebhookPayment
	default:
		ev.Kind = WebhookIgnored
	}
	return ev, nil
}

func (s *Sandbox) Refund(ctx context.Context, req RefundRequest) (RefundResult, error) {
	t, err := s.load(ctx, req.PaymentReference)
	if err != nil {
		return RefundResult{}, err
	}
	if t.Status != StatusSucceeded {
		return RefundResult{}, fmt.Errorf("sandbox: cannot refund a transaction that is %s", t.Status)
	}
	return RefundResult{ProviderRefundID: "sbx_rf_" + strings.ToLower(random.Code(10)), Status: RefundCompleted}, nil
}

func (s *Sandbox) RefundStatus(_ context.Context, providerRefundID string) (RefundResult, error) {
	return RefundResult{ProviderRefundID: providerRefundID, Status: RefundCompleted}, nil
}

// Complete simulates the customer finishing payment, then delivers the signed webhook.
// It is exported for the sandbox page and for automated tests.
func (s *Sandbox) Complete(ctx context.Context, reference string, success bool) (sandboxTxn, error) {
	t, err := s.load(ctx, reference)
	if err != nil {
		return t, err
	}
	if t.Status == StatusPending {
		if success {
			now := s.now()
			t.Status, t.PaidAt = StatusSucceeded, &now
		} else {
			t.Status = StatusFailed
		}
		if err := cache.SetJSON(ctx, s.store, sandboxKey(reference), t, sandboxTTL); err != nil {
			return t, err
		}
	}
	return t, nil
}

// WebhookBody builds and signs the webhook the sandbox sends for a transaction.
func (s *Sandbox) WebhookBody(t sandboxTxn) ([]byte, string) {
	event := "charge.success"
	if t.Status != StatusSucceeded {
		event = "charge.failed"
	}
	body, _ := json.Marshal(map[string]any{
		"event": event,
		"data":  map[string]any{"reference": t.Reference, "amount": t.AmountCents, "currency": t.Currency, "id": t.TransactionID},
	})
	return body, s.sign(body)
}

func (s *Sandbox) deliverWebhook(t sandboxTxn) {
	body, signature := s.WebhookBody(t)
	for attempt := 1; attempt <= 3; attempt++ {
		time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		req, err := http.NewRequest(http.MethodPost, s.webhookURL, bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Sandbox-Signature", signature)
		resp, err := s.client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		s.logger.Warn("sandbox webhook delivery failed", "attempt", attempt, "error", err)
	}
}

var checkoutPage = template.Must(template.New("checkout").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sandbox payment</title>
<style>
body{font-family:system-ui,-apple-system,Segoe UI,Roboto,sans-serif;background:#f4f4f5;margin:0;display:grid;place-items:center;min-height:100vh}
.card{background:#fff;border-radius:16px;box-shadow:0 10px 30px rgba(0,0,0,.08);padding:32px;max-width:420px;width:90%}
.badge{display:inline-block;background:#fef3c7;color:#92400e;font-size:12px;font-weight:600;padding:4px 10px;border-radius:999px}
h1{font-size:20px;margin:16px 0 4px}.muted{color:#71717a;font-size:14px}
.amount{font-size:36px;font-weight:700;margin:20px 0}
button{width:100%;padding:14px;border:0;border-radius:10px;font-size:16px;font-weight:600;cursor:pointer;margin-top:10px}
.pay{background:#4f46e5;color:#fff}.decline{background:#f4f4f5;color:#3f3f46}
</style></head><body><div class="card">
<span class="badge">SANDBOX — no real money moves</span>
<h1>{{.Description}}</h1><div class="muted">Paying as {{.Email}} · Ref {{.Reference}}</div>
<div class="amount">{{.Currency}} {{.Amount}}</div>
{{if eq .Status "pending"}}
<form method="post"><input type="hidden" name="outcome" value="success"><button class="pay" type="submit">Pay now</button></form>
<form method="post"><input type="hidden" name="outcome" value="decline"><button class="decline" type="submit">Decline payment</button></form>
{{else}}<p class="muted">This payment is already {{.Status}}.</p><a href="{{.CallbackURL}}">Return to the shop</a>{{end}}
</div></body></html>`))

// CheckoutPage serves the sandbox hosted payment page (GET) and records the outcome (POST).
func (s *Sandbox) CheckoutPage(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("reference")
	t, err := s.load(r.Context(), ref)
	if err != nil {
		http.Error(w, "Unknown or expired sandbox payment.", http.StatusNotFound)
		return
	}
	if r.Method == http.MethodPost {
		updated, err := s.Complete(r.Context(), ref, r.FormValue("outcome") == "success")
		if err != nil {
			http.Error(w, "Could not record the sandbox payment.", http.StatusInternalServerError)
			return
		}
		go s.deliverWebhook(updated)
		http.Redirect(w, r, updated.CallbackURL, http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = checkoutPage.Execute(w, map[string]any{
		"Description": t.Description, "Email": t.Email, "Reference": t.Reference, "Currency": t.Currency,
		"Amount": fmt.Sprintf("%d.%02d", t.AmountCents/100, t.AmountCents%100), "Status": string(t.Status), "CallbackURL": t.CallbackURL,
	})
}
