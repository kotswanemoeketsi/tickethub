package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type ozowTestTransport struct{ handler http.HandlerFunc }

func (tr ozowTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	tr.handler(w, req)
	return w.Result(), nil
}

func testOzow(t *testing.T, handler http.HandlerFunc) *Ozow {
	t.Helper()
	o := NewOzow(OzowConfig{ClientID: "client", ClientSecret: "secret", SiteCode: "SITE", BaseURL: "https://one.ozow.com",
		WebhookSecret: "whsec_" + base64.StdEncoding.EncodeToString([]byte("test-signing-key"))},
		func(context.Context, string) (string, time.Time, error) {
			return "request-id", time.Now().Add(-time.Hour), nil
		})
	o.client.Transport = ozowTestTransport{handler}
	return o
}

func TestOzowCheckoutAndRefund(t *testing.T) {
	tokenCalls := 0
	o := testOzow(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls++
			r.ParseForm()
			if r.Form.Get("client_secret") != "secret" || r.Form.Get("scope") != "payments refunds" {
				t.Error("incorrect token request")
			}
			fmt.Fprint(w, `{"access_token":"token","expires_in":"14400"}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing auth")
		}
		switch r.URL.Path {
		case "/payments":
			if r.Header.Get("Idempotency-Key") != "PAY-123" {
				t.Error("missing checkout idempotency key")
			}
			var body struct {
				Amount    ozowAmount `json:"amount"`
				ExpireAt  string     `json:"expireAt"`
				Reference string     `json:"merchantReference"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Amount.Value.String() != "123.45" || body.Amount.Currency != "ZAR" || body.Reference != "PAY-123" || body.ExpireAt == "" {
				t.Errorf("wrong checkout: %+v", body)
			}
			fmt.Fprint(w, `{"id":"request-id","status":"Created","redirectUrl":"https://pay.ozow.com/opaque-checkout"}`)
		case "/transactions/tx-id/refunds":
			if r.Header.Get("Idempotency-Key") != "REF-123" {
				t.Error("missing refund idempotency key")
			}
			fmt.Fprint(w, `{"id":"refund-id","status":"Pending"}`)
		case "/refunds/refund-id":
			fmt.Fprint(w, `{"id":"refund-id","status":"Complete"}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	s, err := o.CreateCheckout(context.Background(), CheckoutRequest{Reference: "PAY-123", AmountCents: 12345, Currency: "ZAR", CallbackURL: "https://gotaticket.co.za/orders/1/payment-return", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil || s.ProviderRequestID != "request-id" || s.RedirectURL != "https://pay.ozow.com/opaque-checkout" {
		t.Fatalf("checkout: %+v %v", s, err)
	}
	r, err := o.Refund(context.Background(), RefundRequest{TransactionID: "tx-id", RefundReference: "REF-123", AmountCents: 12345, Currency: "ZAR", Reason: "Cancellation"})
	if err != nil || r.Status != RefundPending {
		t.Fatalf("refund: %+v %v", r, err)
	}
	r, err = o.RefundStatus(context.Background(), r.ProviderRefundID)
	if err != nil || r.Status != RefundCompleted {
		t.Fatalf("refund status: %+v %v", r, err)
	}
	if tokenCalls != 1 {
		t.Errorf("token not reused: %d", tokenCalls)
	}
}

func TestOzowVerification(t *testing.T) {
	for _, tc := range []struct {
		name, status, reference, site, amount, currency string
		want                                            Status
		wantError                                       bool
	}{
		{"paid", "Successful", "PAY-123", "SITE", "123.45", "ZAR", StatusSucceeded, false},
		{"case insensitive", "successful", "PAY-123", "SITE", "123.45", "ZAR", StatusSucceeded, false},
		{"pending", "Pending", "PAY-123", "SITE", "123.45", "ZAR", StatusPending, false},
		{"incomplete", "Incomplete", "PAY-123", "SITE", "123.45", "ZAR", StatusPending, false},
		{"retryable attempt", "Error", "PAY-123", "SITE", "123.45", "ZAR", StatusPending, false},
		{"refunded", "Refunded", "PAY-123", "SITE", "123.45", "ZAR", StatusPending, false},
		{"unknown", "new-status", "PAY-123", "SITE", "123.45", "ZAR", StatusPending, false},
		{"wrong reference", "Successful", "OTHER", "SITE", "123.45", "ZAR", "", true},
		{"wrong site", "Successful", "PAY-123", "OTHER", "123.45", "ZAR", "", true},
		{"fractional cent", "Successful", "PAY-123", "SITE", "123.451", "ZAR", "", true},
		{"wrong currency", "Successful", "PAY-123", "SITE", "123.45", "USD", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := testOzow(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					fmt.Fprint(w, `{"access_token":"token","expires_in":14400}`)
					return
				}
				if r.URL.Path != "/payments/request-id/transactions" || r.URL.Query().Get("fromDate") == "" {
					t.Error("incorrect verification URL")
				}
				fmt.Fprintf(w, `{"results":[{"id":"tx-id","merchantReference":%q,"siteCode":%q,"status":%q,"amount":{"currency":%q,"value":%s}}]}`, tc.reference, tc.site, tc.status, tc.currency, tc.amount)
			})
			v, err := o.VerifyPayment(context.Background(), "PAY-123")
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantError && v.Status != tc.want {
				t.Errorf("status = %s", v.Status)
			}
			if v.Status == StatusSucceeded && v.AmountCents != 12345 {
				t.Errorf("amount = %d", v.AmountCents)
			}
		})
	}
}

func signedOzow(o *Ozow, body []byte, stamp string) http.Header {
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(o.cfg.WebhookSecret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("msg-123." + stamp + "."))
	mac.Write(body)
	h := http.Header{}
	h.Set("svix-id", "msg-123")
	h.Set("svix-timestamp", stamp)
	h.Set("svix-signature", "v1,bad v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return h
}

func TestOzowWebhookAuthentication(t *testing.T) {
	o := testOzow(t, func(http.ResponseWriter, *http.Request) { t.Fatal("webhook parsing must not call API") })
	body := []byte(`{"type":"transaction.complete","data":{"SiteCode":"SITE","TransactionReference":"PAY-123","IsTest":"False"}}`)
	h := signedOzow(o, body, strconv.FormatInt(time.Now().Unix(), 10))
	ev, err := o.ParseWebhook(h, body)
	if err != nil || ev.Kind != WebhookPayment || ev.Reference != "PAY-123" {
		t.Fatalf("valid webhook: %+v %v", ev, err)
	}
	duplicate, err := o.ParseWebhook(h, body)
	if err != nil || duplicate.DedupeKey != ev.DedupeKey {
		t.Fatal("unstable dedupe key")
	}
	refundBody := []byte(`{"type":"refund.complete","data":{"SiteCode":"SITE","TransactionReference":"PAY-123","RefundId":"refund-id","IsTest":"False"}}`)
	refundH := signedOzow(o, refundBody, strconv.FormatInt(time.Now().Unix(), 10))
	refundEv, err := o.ParseWebhook(refundH, refundBody)
	if err != nil || refundEv.Kind != WebhookRefund || refundEv.Reference != "refund-id" {
		t.Fatalf("refund webhook: %+v %v", refundEv, err)
	}
	for _, tc := range []struct {
		name   string
		header http.Header
		body   []byte
	}{
		{"tampered", h, append(append([]byte{}, body...), ' ')},
		{"missing headers", http.Header{}, body},
		{"old", signedOzow(o, body, strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)), body},
		{"future", signedOzow(o, body, strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)), body},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := o.ParseWebhook(tc.header, tc.body); !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("accepted invalid signature: %v", err)
			}
		})
	}
	for _, bad := range []string{
		`{"type":"transaction.complete","data":{"id":"tx-id","status":"Successful"}}`,
		`{"type":"transaction.complete","data":{"SiteCode":"OTHER","TransactionReference":"PAY-123","IsTest":"False"}}`,
		`{"type":"transaction.complete","data":{"SiteCode":"SITE","TransactionReference":"PAY-123","IsTest":"True"}}`,
		`{"type":"refund.complete","data":{"SiteCode":"SITE","TransactionReference":"","RefundId":"refund-id","IsTest":"False"}}`,
		`{"type":"refund.complete","data":{"SiteCode":"SITE","TransactionReference":"PAY-123","RefundId":"","IsTest":"False"}}`,
	} {
		b := []byte(bad)
		if _, err := o.ParseWebhook(signedOzow(o, b, strconv.FormatInt(time.Now().Unix(), 10)), b); err == nil {
			t.Error("accepted invalid full payload")
		}
	}
}

func TestOzowPublishedSignatureVector(t *testing.T) {
	o := NewOzow(OzowConfig{WebhookSecret: "whsec_plJ3nmyCDGBKInavdOK15jsl"}, nil)
	o.now = func() time.Time { return time.Unix(1731705121, 0) }
	h := http.Header{}
	h.Set("svix-id", "msg_loFOjxBNrRLzqYUf")
	h.Set("svix-timestamp", "1731705121")
	h.Set("svix-signature", "v1,rAvfW3dJ/X/qxhsaXPOyyCGmRKsaKWcsNccKXlIktD0=")
	if _, err := o.ParseWebhook(h, []byte(`{"event_type":"ping","data":{"success":true}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestOzowRejectsUnsafePagination(t *testing.T) {
	o := testOzow(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"access_token":"token","expires_in":14400}`)
			return
		}
		fmt.Fprint(w, `{"results":[],"links":{"next":"https://evil.example/steal-token"}}`)
	})
	if _, err := o.VerifyPayment(context.Background(), "PAY-123"); err == nil {
		t.Fatal("accepted external pagination URL")
	}
}

func TestOzowAmountPrecisionAndRefundStates(t *testing.T) {
	for _, value := range []string{"0", "-1", "1.001", "9223372036854775808", "nonsense"} {
		if _, err := ozowCents(json.Number(value)); err == nil {
			t.Errorf("accepted invalid amount %s", value)
		}
	}
	for _, tc := range []struct {
		state string
		want  RefundStatus
	}{
		{"Pending", RefundPending}, {"Submitted", RefundPending}, {"Complete", RefundCompleted},
		{"Failed", RefundFailed}, {"Cancelled", RefundFailed}, {"Returned", RefundFailed}, {"unknown", RefundPending},
	} {
		result, err := ozowRefundResult(json.RawMessage(fmt.Sprintf(`{"id":"refund-id","status":%q}`, tc.state)))
		if err != nil || result.Status != tc.want {
			t.Errorf("%s: %+v %v", tc.state, result, err)
		}
	}
}

func TestOzowPaginationFindsSuccessAfterFailedAttempt(t *testing.T) {
	o := testOzow(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"access_token":"token","expires_in":14400}`)
			return
		}
		if r.URL.Query().Get("offset") == "50" {
			fmt.Fprint(w, `{"results":[{"id":"paid","merchantReference":"PAY-123","siteCode":"SITE","status":"Successful","amount":{"currency":"ZAR","value":1.23}}]}`)
		} else {
			fmt.Fprint(w, `{"results":[{"id":"failed","merchantReference":"PAY-123","siteCode":"SITE","status":"Error"}],"links":{"next":"https://one.ozow.com/payments/request-id/transactions?offset=50"}}`)
		}
	})
	v, err := o.VerifyPayment(context.Background(), "PAY-123")
	if err != nil || v.Status != StatusSucceeded || v.TransactionID != "paid" || v.AmountCents != 123 {
		t.Fatalf("%+v %v", v, err)
	}
}
