package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Paystack integrates https://paystack.com (cards, instant EFT and more; supports ZAR).
//
//   - Checkout:     POST /transaction/initialize → hosted payment page
//   - Verification: GET  /transaction/verify/{reference}
//   - Webhooks:     x-paystack-signature = hex(HMAC-SHA512(body, secret key))
//   - Refunds:      POST /refund, GET /refund/{id}
type Paystack struct {
	secretKey string
	baseURL   string
	client    *http.Client
}

// NewPaystack returns a Paystack gateway.
func NewPaystack(secretKey, baseURL string) *Paystack {
	return &Paystack{secretKey: secretKey, baseURL: strings.TrimSuffix(baseURL, "/"), client: &http.Client{Timeout: 20 * time.Second}}
}

func (p *Paystack) Name() string { return "paystack" }

type paystackEnvelope struct {
	Status  bool            `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// call performs an API request and returns the "data" member of the response.
func (p *Paystack) call(ctx context.Context, method, path string, body any) (json.RawMessage, int, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+p.secretKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("paystack %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	var env paystackEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("paystack %s %s: unexpected response (HTTP %d)", method, path, resp.StatusCode)
	}
	if !env.Status || resp.StatusCode >= 300 {
		return nil, resp.StatusCode, fmt.Errorf("paystack %s %s: %s (HTTP %d)", method, path, env.Message, resp.StatusCode)
	}
	return env.Data, resp.StatusCode, nil
}

func (p *Paystack) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutSession, error) {
	data, _, err := p.call(ctx, http.MethodPost, "/transaction/initialize", map[string]any{
		"email":        req.Email,
		"amount":       req.AmountCents,
		"currency":     req.Currency,
		"reference":    req.Reference,
		"callback_url": req.CallbackURL,
		"metadata":     req.Metadata,
	})
	if err != nil {
		return CheckoutSession{}, err
	}
	var out struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.AuthorizationURL == "" {
		return CheckoutSession{}, fmt.Errorf("paystack initialize: missing authorization_url")
	}
	return CheckoutSession{RedirectURL: out.AuthorizationURL}, nil
}

type paystackTransaction struct {
	ID              int64  `json:"id"`
	Status          string `json:"status"`
	Reference       string `json:"reference"`
	Amount          int64  `json:"amount"`
	Currency        string `json:"currency"`
	PaidAt          string `json:"paid_at"`
	GatewayResponse string `json:"gateway_response"`
}

func (p *Paystack) VerifyPayment(ctx context.Context, reference string) (Verification, error) {
	data, status, err := p.call(ctx, http.MethodGet, "/transaction/verify/"+url.PathEscape(reference), nil)
	if err != nil {
		if status == http.StatusNotFound || status == http.StatusBadRequest {
			return Verification{}, ErrNotFound
		}
		return Verification{}, err
	}
	var tx paystackTransaction
	if err := json.Unmarshal(data, &tx); err != nil {
		return Verification{}, fmt.Errorf("paystack verify: %w", err)
	}
	v := Verification{
		Reference: tx.Reference, TransactionID: strconv.FormatInt(tx.ID, 10), AmountCents: tx.Amount,
		Currency: strings.ToUpper(tx.Currency), Raw: data,
	}
	switch tx.Status {
	case "success":
		v.Status = StatusSucceeded
		if t, err := time.Parse(time.RFC3339, tx.PaidAt); err == nil {
			t = t.UTC()
			v.PaidAt = &t
		}
	case "failed", "abandoned", "reversed":
		v.Status = StatusFailed
		v.FailureReason = firstNonEmpty(tx.GatewayResponse, tx.Status)
	default:
		v.Status = StatusPending
	}
	return v, nil
}

func (p *Paystack) ParseWebhook(header http.Header, body []byte) (WebhookEvent, error) {
	given, err := hex.DecodeString(header.Get("x-paystack-signature"))
	if err != nil || len(given) == 0 {
		return WebhookEvent{}, ErrInvalidSignature
	}
	mac := hmac.New(sha512.New, []byte(p.secretKey))
	mac.Write(body)
	if !hmac.Equal(given, mac.Sum(nil)) {
		return WebhookEvent{}, ErrInvalidSignature
	}
	var envelope struct {
		Event string         `json:"event"`
		Data  map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return WebhookEvent{}, fmt.Errorf("paystack webhook: %w", err)
	}
	ev := WebhookEvent{Type: envelope.Event, DedupeKey: dedupeKey(body), Payload: body}
	switch {
	case envelope.Event == "charge.success":
		ev.Kind = WebhookPayment
		ev.Reference = stringField(envelope.Data, "reference")
	case strings.HasPrefix(envelope.Event, "refund."):
		ev.Kind = WebhookRefund
		ev.Reference = firstNonEmpty(stringField(envelope.Data, "transaction_reference"), stringField(envelope.Data, "reference"))
	default:
		ev.Kind = WebhookIgnored
	}
	return ev, nil
}

type paystackRefund struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

func mapPaystackRefund(data json.RawMessage) (RefundResult, error) {
	var r paystackRefund
	if err := json.Unmarshal(data, &r); err != nil {
		return RefundResult{}, fmt.Errorf("paystack refund: %w", err)
	}
	res := RefundResult{ProviderRefundID: strconv.FormatInt(r.ID, 10), Raw: data}
	switch r.Status {
	case "processed":
		res.Status = RefundCompleted
	case "failed", "reversed":
		res.Status = RefundFailed
		res.FailureReason = "refund " + r.Status + " at Paystack"
	default: // pending, processing, needs-attention
		res.Status = RefundPending
	}
	return res, nil
}

func (p *Paystack) Refund(ctx context.Context, req RefundRequest) (RefundResult, error) {
	transaction := req.TransactionID
	if transaction == "" {
		transaction = req.PaymentReference
	}
	data, _, err := p.call(ctx, http.MethodPost, "/refund", map[string]any{
		"transaction":   transaction,
		"amount":        req.AmountCents,
		"currency":      req.Currency,
		"merchant_note": req.Reason,
		"reference":     req.RefundReference,
	})
	if err != nil {
		return RefundResult{}, err
	}
	return mapPaystackRefund(data)
}

func (p *Paystack) RefundStatus(ctx context.Context, providerRefundID string) (RefundResult, error) {
	data, _, err := p.call(ctx, http.MethodGet, "/refund/"+url.PathEscape(providerRefundID), nil)
	if err != nil {
		return RefundResult{}, err
	}
	return mapPaystackRefund(data)
}

func stringField(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
