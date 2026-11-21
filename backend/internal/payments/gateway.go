// Package payments integrates online payment gateways behind a common interface.
//
// The rest of the system never trusts a browser's claim that a payment succeeded. A paid
// ticket is only issued after the gateway confirms the transaction server-to-server,
// either through a signature-verified webhook or by calling VerifyPayment.
package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

// Status is the normalised state of a gateway transaction.
type Status string

const (
	StatusPending   Status = "pending"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// RefundStatus is the normalised state of a gateway refund.
type RefundStatus string

const (
	RefundPending   RefundStatus = "pending"
	RefundCompleted RefundStatus = "completed"
	RefundFailed    RefundStatus = "failed"
)

var (
	// ErrInvalidSignature is returned for webhooks that fail signature verification.
	ErrInvalidSignature = errors.New("invalid webhook signature")
	// ErrNotFound is returned when the gateway does not know the transaction.
	ErrNotFound = errors.New("transaction not found at gateway")
)

// CheckoutRequest starts a hosted payment page.
type CheckoutRequest struct {
	Reference   string // our unique payment reference
	AmountCents int64
	Currency    string
	Email       string
	CallbackURL string // where the gateway sends the customer afterwards
	Description string
	Metadata    map[string]string
}

// CheckoutSession is where to send the customer to pay.
type CheckoutSession struct {
	RedirectURL string
}

// Verification is the gateway's authoritative view of a transaction.
type Verification struct {
	Reference     string
	TransactionID string
	Status        Status
	AmountCents   int64
	Currency      string
	PaidAt        *time.Time
	FailureReason string
	Raw           json.RawMessage
}

// WebhookKind classifies webhook deliveries.
type WebhookKind int

const (
	WebhookIgnored WebhookKind = iota
	WebhookPayment
	WebhookRefund
)

// WebhookEvent is a verified webhook delivery.
type WebhookEvent struct {
	Kind      WebhookKind
	Type      string
	Reference string // payment reference for payment events, transaction reference for refunds
	DedupeKey string
	Payload   json.RawMessage
}

// RefundRequest asks the gateway to return money.
type RefundRequest struct {
	PaymentReference string
	TransactionID    string
	AmountCents      int64
	Currency         string
	RefundReference  string // our idempotency reference
	Reason           string
}

// RefundResult is the gateway's view of a refund.
type RefundResult struct {
	ProviderRefundID string
	Status           RefundStatus
	FailureReason    string
	Raw              json.RawMessage
}

// Gateway is implemented by each payment provider.
type Gateway interface {
	Name() string
	CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutSession, error)
	VerifyPayment(ctx context.Context, reference string) (Verification, error)
	ParseWebhook(header http.Header, body []byte) (WebhookEvent, error)
	Refund(ctx context.Context, req RefundRequest) (RefundResult, error)
	RefundStatus(ctx context.Context, providerRefundID string) (RefundResult, error)
}

// dedupeKey identifies a webhook delivery by the hash of its body.
func dedupeKey(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
