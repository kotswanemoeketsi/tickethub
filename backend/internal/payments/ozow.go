package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Ozow uses One API hosted checkout and full, Svix-signed transaction webhooks.
// Lookup reads the durable payment-request ID saved before redirecting the buyer.
type Ozow struct {
	cfg         OzowConfig
	lookup      func(context.Context, string) (string, time.Time, error)
	client      *http.Client
	now         func() time.Time
	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

type OzowConfig struct {
	ClientID, ClientSecret, SiteCode, WebhookSecret, BaseURL string
}

func NewOzow(cfg OzowConfig, lookup func(context.Context, string) (string, time.Time, error)) *Ozow {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Ozow{cfg: cfg, lookup: lookup, now: time.Now, client: &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (o *Ozow) Name() string { return "ozow" }

func (o *Ozow) accessToken(ctx context.Context) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.token != "" && o.now().Before(o.tokenExpiry) {
		return o.token, nil
	}
	form := url.Values{"client_id": {o.cfg.ClientID}, "client_secret": {o.cfg.ClientSecret},
		"grant_type": {"client_credentials"}, "scope": {"payments refunds"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.BaseURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ozow authentication: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ozow authentication: HTTP %d", resp.StatusCode)
	}
	var result struct {
		AccessToken string      `json:"access_token"`
		ExpiresIn   json.Number `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", fmt.Errorf("ozow authentication: malformed response")
	}
	seconds, err := result.ExpiresIn.Int64()
	if err != nil || seconds < 1 || seconds > 86400 || result.AccessToken == "" {
		return "", fmt.Errorf("ozow authentication: invalid token lifetime or token")
	}
	o.token, o.tokenExpiry = result.AccessToken, o.now().Add(time.Duration(seconds)*time.Second-time.Duration(seconds/10)*time.Second)
	return o.token, nil
}

// Requests are never automatically retried. Mutating calls carry stable idempotency keys.
func (o *Ozow) call(ctx context.Context, method, path, key string, body any) (json.RawMessage, error) {
	token, err := o.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.cfg.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ozow request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		o.mu.Lock()
		if o.token == token {
			o.token = ""
		}
		o.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ozow %s: HTTP %d", method, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("ozow: invalid JSON response")
	}
	return raw, nil
}

type ozowAmount struct {
	Currency string      `json:"currency"`
	Value    json.Number `json:"value"`
}

func ozowMoney(cents int64, currency string) (ozowAmount, error) {
	if cents <= 0 || currency != "ZAR" {
		return ozowAmount{}, fmt.Errorf("ozow requires a positive ZAR amount")
	}
	return ozowAmount{Currency: currency, Value: json.Number(fmt.Sprintf("%d.%02d", cents/100, cents%100))}, nil
}

// Parse exactly: do not round a fractional cent into an apparently matching payment.
func ozowCents(value json.Number) (int64, error) {
	r, ok := new(big.Rat).SetString(value.String())
	if !ok {
		return 0, fmt.Errorf("ozow: invalid amount")
	}
	r.Mul(r, big.NewRat(100, 1))
	if !r.IsInt() || !r.Num().IsInt64() || r.Sign() <= 0 {
		return 0, fmt.Errorf("ozow: invalid amount precision")
	}
	return r.Num().Int64(), nil
}

func (o *Ozow) CreateCheckout(ctx context.Context, req CheckoutRequest) (CheckoutSession, error) {
	amount, err := ozowMoney(req.AmountCents, req.Currency)
	if err != nil {
		return CheckoutSession{}, err
	}
	if req.Reference == "" || len(req.Reference) > 50 || !req.ExpiresAt.After(o.now()) {
		return CheckoutSession{}, fmt.Errorf("ozow: invalid reference or checkout expiry")
	}
	// A short deterministic bank reference leaves room for Ozow's configured site prefix.
	sum := sha256.Sum256([]byte(req.Reference))
	data, err := o.call(ctx, http.MethodPost, "/payments", req.Reference, map[string]any{
		"siteCode": o.cfg.SiteCode, "region": "ZA", "amount": amount,
		"merchantReference": req.Reference, "beneficiaryReference": fmt.Sprintf("GAT%x", sum[:5]),
		"expireAt": req.ExpiresAt.UTC().Format(time.RFC3339), "returnUrl": req.CallbackURL,
	})
	if err != nil {
		return CheckoutSession{}, err
	}
	var result struct {
		ID          string `json:"id"`
		RedirectURL string `json:"redirectUrl"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return CheckoutSession{}, err
	}
	u, err := url.Parse(result.RedirectURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || result.ID == "" || !strings.EqualFold(result.Status, "Created") {
		return CheckoutSession{}, fmt.Errorf("ozow: missing active checkout URL or ID")
	}
	return CheckoutSession{RedirectURL: result.RedirectURL, ProviderRequestID: result.ID}, nil
}

type ozowTransaction struct {
	ID            string     `json:"id"`
	Reference     string     `json:"merchantReference"`
	SiteCode      string     `json:"siteCode"`
	Amount        ozowAmount `json:"amount"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason"`
	CompletedDate string     `json:"completedDate"`
}

func (o *Ozow) VerifyPayment(ctx context.Context, reference string) (Verification, error) {
	id, created, err := o.lookup(ctx, reference)
	if err != nil {
		return Verification{}, err
	}
	if id == "" {
		return Verification{}, fmt.Errorf("ozow: checkout ID not yet saved")
	}
	query := url.Values{"fromDate": {created.UTC().AddDate(0, 0, -1).Format("2006-01-02")}, "toDate": {o.now().UTC().AddDate(0, 0, 1).Format("2006-01-02")}, "limit": {"50"}}
	path := "/payments/" + url.PathEscape(id) + "/transactions?" + query.Encode()
	var successful *Verification
	seen := map[string]bool{}
	for page := 0; path != ""; page++ {
		if page >= 100 || seen[path] {
			return Verification{}, fmt.Errorf("ozow: invalid transaction pagination")
		}
		seen[path] = true
		raw, err := o.call(ctx, http.MethodGet, path, "", nil)
		if err != nil {
			return Verification{}, err
		}
		var list struct {
			Results []ozowTransaction `json:"results"`
			Links   struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return Verification{}, err
		}
		for _, tx := range list.Results {
			if tx.Reference != reference || tx.SiteCode != o.cfg.SiteCode || tx.ID == "" {
				return Verification{}, fmt.Errorf("ozow: transaction identity mismatch")
			}
			// One API Transaction uses Successful; unrecognised states never issue tickets.
			if !strings.EqualFold(tx.Status, "Successful") {
				continue
			}
			cents, err := ozowCents(tx.Amount.Value)
			if err != nil {
				return Verification{}, err
			}
			if tx.Amount.Currency != "ZAR" {
				return Verification{}, fmt.Errorf("ozow: unexpected transaction currency")
			}
			if successful != nil && successful.TransactionID != tx.ID {
				return Verification{}, fmt.Errorf("ozow: multiple successful transactions; manual reconciliation required")
			}
			txRaw, _ := json.Marshal(tx)
			v := Verification{Reference: reference, TransactionID: tx.ID, Status: StatusSucceeded, AmountCents: cents, Currency: tx.Amount.Currency, Raw: txRaw}
			if paid, err := time.Parse(time.RFC3339, tx.CompletedDate); err == nil {
				v.PaidAt = &paid
			}
			successful = &v
		}
		path = ""
		if list.Links.Next != "" {
			base, _ := url.Parse(o.cfg.BaseURL)
			next, err := url.Parse(list.Links.Next)
			if err != nil {
				return Verification{}, fmt.Errorf("ozow: invalid pagination link")
			}
			next = base.ResolveReference(next)
			if next.Scheme != base.Scheme || next.Host != base.Host || next.User != nil || next.Path != base.Path+"/payments/"+id+"/transactions" {
				return Verification{}, fmt.Errorf("ozow: unsafe pagination link")
			}
			path = strings.TrimPrefix(next.Path, base.Path) + "?" + next.RawQuery
		}
	}
	if successful != nil {
		return *successful, nil
	}
	// A failed transaction attempt does not close the payment request: the buyer may
	// retry within the same hosted checkout. Keep polling until the order expires.
	return Verification{Reference: reference, Status: StatusPending}, nil
}

func (o *Ozow) ParseWebhook(header http.Header, body []byte) (WebhookEvent, error) {
	id, stamp := header.Get("svix-id"), header.Get("svix-timestamp")
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	now := o.now().Unix()
	if err != nil || id == "" || seconds < now-300 || seconds > now+300 {
		return WebhookEvent{}, ErrInvalidSignature
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(o.cfg.WebhookSecret, "whsec_"))
	if err != nil || len(key) == 0 {
		return WebhookEvent{}, ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + stamp + "."))
	mac.Write(body)
	valid := false
	for _, signature := range strings.Fields(header.Get("svix-signature")) {
		version, value, _ := strings.Cut(signature, ",")
		given, err := base64.StdEncoding.DecodeString(value)
		if version == "v1" && err == nil && hmac.Equal(given, mac.Sum(nil)) {
			valid = true
		}
	}
	if !valid {
		return WebhookEvent{}, ErrInvalidSignature
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			SiteCode  string `json:"SiteCode"`
			Reference string `json:"TransactionReference"`
			RefundID  string `json:"RefundId"`
			IsTest    string `json:"IsTest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		return WebhookEvent{}, err
	}
	ev := WebhookEvent{Type: event.Type, DedupeKey: dedupeKey([]byte(id)), Payload: body}
	switch event.Type {
	case "transaction.complete":
		if event.Data.Reference == "" || event.Data.SiteCode != o.cfg.SiteCode {
			return WebhookEvent{}, fmt.Errorf("ozow: expected full webhook for the configured site")
		}
		// Simulation notifications never authorise real ticket delivery.
		if !strings.EqualFold(event.Data.IsTest, "False") {
			return WebhookEvent{}, fmt.Errorf("ozow: test or missing IsTest flag")
		}
		ev.Kind, ev.Reference = WebhookPayment, event.Data.Reference
	case "refund.complete":
		if event.Data.Reference == "" || event.Data.RefundID == "" || event.Data.SiteCode != o.cfg.SiteCode {
			return WebhookEvent{}, fmt.Errorf("ozow: invalid refund webhook")
		}
		// Simulation notifications never authorise real refund processing.
		if !strings.EqualFold(event.Data.IsTest, "False") {
			return WebhookEvent{}, fmt.Errorf("ozow: test or missing IsTest flag in refund")
		}
		ev.Kind, ev.Reference = WebhookRefund, event.Data.RefundID
	}
	return ev, nil
}

func ozowRefundResult(raw json.RawMessage) (RefundResult, error) {
	var response struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return RefundResult{}, err
	}
	if response.ID == "" {
		return RefundResult{}, fmt.Errorf("ozow: missing refund ID")
	}
	result := RefundResult{ProviderRefundID: response.ID, Status: RefundPending, Raw: raw}
	switch strings.ToLower(response.Status) {
	case "complete":
		result.Status = RefundCompleted
	case "failed", "cancelled", "returned":
		result.Status, result.FailureReason = RefundFailed, firstNonEmpty(response.Reason, response.Status)
	}
	return result, nil
}

func (o *Ozow) Refund(ctx context.Context, req RefundRequest) (RefundResult, error) {
	amount, err := ozowMoney(req.AmountCents, req.Currency)
	if err != nil {
		return RefundResult{}, err
	}
	if req.TransactionID == "" || req.RefundReference == "" {
		return RefundResult{}, fmt.Errorf("ozow: refund requires transaction ID and idempotency reference")
	}
	raw, err := o.call(ctx, http.MethodPost, "/transactions/"+url.PathEscape(req.TransactionID)+"/refunds", req.RefundReference,
		map[string]any{"amount": amount, "reason": req.Reason, "realTimePayment": false})
	if err != nil {
		return RefundResult{}, err
	}
	return ozowRefundResult(raw)
}

func (o *Ozow) RefundStatus(ctx context.Context, id string) (RefundResult, error) {
	raw, err := o.call(ctx, http.MethodGet, "/refunds/"+url.PathEscape(id), "", nil)
	if err != nil {
		return RefundResult{}, err
	}
	result, err := ozowRefundResult(raw)
	if err == nil && result.ProviderRefundID != id {
		return RefundResult{}, fmt.Errorf("ozow: refund identity mismatch")
	}
	return result, err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
