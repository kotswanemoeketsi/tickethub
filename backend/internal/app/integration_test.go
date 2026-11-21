package app_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/rbac"
)

func ticketType(name string, priceCents, quantity int) map[string]any {
	return map[string]any{"name": name, "price_cents": priceCents, "quantity_total": quantity, "max_per_order": 10}
}

// Many customers racing for the last tickets: exactly the available quantity succeeds.
func TestReservationsNeverOversell(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 10, ticketType("General", 10000, 10))

	const buyers = 30
	tokens := make([]string, buyers)
	for i := range tokens {
		_, tokens[i] = e.user(fmt.Sprintf("racer%d", i))
	}
	var ok, soldOut atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < buyers; i++ {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			_, r := e.reserve(token, fx.EventID, fx.Types[0], 1, "")
			switch {
			case r.Status == 201:
				ok.Add(1)
			case r.Status == 409 && r.Error.Code == "sold_out":
				soldOut.Add(1)
			default:
				t.Errorf("unexpected response %d: %s", r.Status, r.Body)
			}
		}(tokens[i])
	}
	wg.Wait()

	if ok.Load() != 10 || soldOut.Load() != buyers-10 {
		t.Fatalf("expected 10 reservations and %d sold out, got %d and %d", buyers-10, ok.Load(), soldOut.Load())
	}
	if sold, reserved := e.inventory(fx.Types[0]); sold != 0 || reserved != 10 {
		t.Fatalf("inventory sold=%d reserved=%d, want 0/10", sold, reserved)
	}
}

// The complete purchase journey over HTTP, including webhook authenticity and idempotency.
func TestPurchaseFlowWithWebhook(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 100, ticketType("General", 25000, 50), ticketType("VIP", 90000, 10))
	email, customer := e.user("buyer")

	// Promotional code: 20% off VIP only.
	e.must(201, e.request("POST", fmt.Sprintf("/api/v1/admin/events/%d/promotions", fx.EventID), fx.OwnerToken, map[string]any{
		"code": "vip20", "discount_type": "percentage", "discount_value": 2000, "is_active": true, "ticket_type_ids": []int64{fx.Types[1]},
	}))
	r := e.request("POST", "/api/v1/orders", customer, map[string]any{
		"event_id": fx.EventID, "promo_code": "VIP20",
		"items": []map[string]any{{"ticket_type_id": fx.Types[0], "quantity": 2}, {"ticket_type_id": fx.Types[1], "quantity": 1}},
	})
	e.must(201, r)
	var o orderView
	r.decode(t, &o)
	if o.DiscountCents != 18000 || o.TotalCents != 50000+90000-18000 {
		t.Fatalf("unexpected pricing: discount %d total %d", o.DiscountCents, o.TotalCents)
	}

	ref, r := e.checkout(customer, o, "online")
	e.must(200, r)

	// A forged webhook is rejected and changes nothing.
	forged := []byte(`{"event":"charge.success","data":{"reference":"` + ref + `"}}`)
	e.must(401, e.request("POST", "/api/v1/payments/webhooks/sandbox", "", forged, "X-Sandbox-Signature", "deadbeef"))
	// A correctly signed webhook for a payment the gateway has not completed changes nothing.
	if got := e.order(customer, o.ID); got.Status != "pending" {
		t.Fatalf("order should still be pending, got %s", got.Status)
	}

	e.must(200, e.payViaWebhook(ref))
	paid := e.order(customer, o.ID)
	if paid.Status != "paid" || len(paid.Tickets) != 3 {
		t.Fatalf("expected paid order with 3 tickets, got %s with %d", paid.Status, len(paid.Tickets))
	}
	var total int64
	for _, tk := range paid.Tickets {
		if tk.QRPayload == "" {
			t.Fatal("owner should receive QR payloads")
		}
		total += tk.PriceCents
	}
	if total != paid.TotalCents {
		t.Fatalf("ticket prices %d do not add up to the order total %d", total, paid.TotalCents)
	}

	// Delivering the same webhook again is harmless.
	e.must(200, e.payViaWebhook(ref))
	if again := e.order(customer, o.ID); len(again.Tickets) != 3 {
		t.Fatalf("duplicate webhook issued extra tickets: %d", len(again.Tickets))
	}
	if sold, reserved := e.inventory(fx.Types[0]); sold != 2 || reserved != 0 {
		t.Fatalf("General inventory sold=%d reserved=%d", sold, reserved)
	}

	// Tickets are emailed by the worker with a PDF attached.
	runJobs(t, e)
	msgs := e.mail.to(email)
	if len(msgs) == 0 || len(msgs[0].Attachments) != 1 || msgs[0].Attachments[0].ContentType != "application/pdf" {
		t.Fatalf("expected a ticket email with a PDF, got %d emails", len(msgs))
	}

	pdf := e.must(200, e.request("GET", fmt.Sprintf("/api/v1/orders/%d/tickets.pdf", o.ID), customer, nil))
	if string(pdf.Body[:5]) != "%PDF-" {
		t.Fatal("ticket download is not a PDF")
	}
	// Another customer cannot see the order.
	_, stranger := e.user("stranger")
	e.must(404, e.request("GET", fmt.Sprintf("/api/v1/orders/%d", o.ID), stranger, nil))
}

func runJobs(t *testing.T, e *env) {
	t.Helper()
	runner := newRunner(e)
	for i := 0; i < 10; i++ {
		n, err := runner.RunOnce(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
}

// Lapsed reservations release stock; a payment that arrives afterwards is honoured when the
// tickets are still available and refunded when they are not.
func TestExpiryAndLatePayments(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 1, ticketType("Solo", 5000, 1))
	_, alice := e.user("alice")
	_, bob := e.user("bob")

	o, r := e.reserve(alice, fx.EventID, fx.Types[0], 1, "")
	e.must(201, r)
	ref, r := e.checkout(alice, o, "online")
	e.must(200, r)

	e.clock.Advance(46 * time.Minute) // past every hold extension
	if _, err := e.app.Sales.ExpireReservations(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.order(alice, o.ID); got.Status != "expired" {
		t.Fatalf("expected the lapsed reservation to expire, got %s", got.Status)
	}
	if _, reserved := e.inventory(fx.Types[0]); reserved != 0 {
		t.Fatalf("expired reservation still holds stock")
	}

	// Alice pays late while the ticket is still free: she gets it.
	e.must(200, e.payViaWebhook(ref))
	if got := e.order(alice, o.ID); got.Status != "paid" || len(got.Tickets) != 1 {
		t.Fatalf("late payment with stock available should be honoured, got %s", got.Status)
	}

	// Bob reserves... but the only ticket is sold.
	if _, r := e.reserve(bob, fx.EventID, fx.Types[0], 1, ""); r.Status != 409 {
		t.Fatalf("expected sold out, got %d", r.Status)
	}

	// Second scenario: Carol's hold lapses, Dave buys the ticket, then Carol's payment lands.
	fx2 := e.publishedEvent(admin, 72*time.Hour, 1, ticketType("Solo", 5000, 1))
	_, carol := e.user("carol")
	_, dave := e.user("dave")
	oc, r := e.reserve(carol, fx2.EventID, fx2.Types[0], 1, "")
	e.must(201, r)
	refCarol, r := e.checkout(carol, oc, "online")
	e.must(200, r)
	e.clock.Advance(46 * time.Minute)
	if _, err := e.app.Sales.ExpireReservations(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.buy(dave, fx2.EventID, fx2.Types[0], 1)
	e.must(200, e.payViaWebhook(refCarol))
	late := e.order(carol, oc.ID)
	if late.Status != "expired" || len(late.Tickets) != 0 {
		t.Fatalf("late payment without stock must not issue tickets, got %s / %d tickets", late.Status, len(late.Tickets))
	}
	if len(late.Refunds) != 1 || late.Refunds[0].Status != "approved" {
		t.Fatalf("late payment should be queued for refund, got %+v", late.Refunds)
	}
	if sold, _ := e.inventory(fx2.Types[0]); sold != 1 {
		t.Fatalf("oversold after late payment: sold=%d", sold)
	}
}

// Door rules: one entry per ticket, concurrent scans, wrong event, re-entry policy.
func TestCheckinRules(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 2*time.Hour, 100, ticketType("General", 10000, 50))
	other := e.publishedEvent(admin, 2*time.Hour, 100, ticketType("General", 10000, 50))
	tomorrow := e.publishedEvent(admin, 30*time.Hour, 100, ticketType("General", 10000, 50))
	_, customer := e.user("guest")
	o := e.buy(customer, fx.EventID, fx.Types[0], 2)
	_, door := e.user("door", rbac.RoleCheckinOfficer)

	// Doors open four hours before the start, so tomorrow's tickets are refused today.
	early := e.buy(customer, tomorrow.EventID, tomorrow.Types[0], 1)
	if res := e.scan(door, tomorrow.EventID, early.Tickets[0].QRPayload, "in"); res.Reason != "too_early" {
		t.Fatalf("expected too_early, got %+v", res)
	}

	// Two devices scan the same ticket at once: exactly one wins.
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e.scan(door, fx.EventID, o.Tickets[0].QRPayload, "in").Result == "accepted" {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("expected exactly one accepted scan, got %d", accepted.Load())
	}
	if res := e.scan(door, fx.EventID, o.Tickets[0].QRPayload, "in"); res.Reason != "already_checked_in" {
		t.Fatalf("expected already_checked_in, got %+v", res)
	}
	// Re-entry is not allowed by default.
	if res := e.scan(door, fx.EventID, o.Tickets[0].QRPayload, "out"); res.Result != "accepted" {
		t.Fatalf("check-out should be accepted, got %+v", res)
	}
	if res := e.scan(door, fx.EventID, o.Tickets[0].QRPayload, "in"); res.Reason != "already_used" {
		t.Fatalf("expected already_used without re-entry, got %+v", res)
	}
	if res := e.scan(door, other.EventID, o.Tickets[1].QRPayload, "in"); res.Reason != "wrong_event" {
		t.Fatalf("expected wrong_event, got %+v", res)
	}
	tampered := o.Tickets[1].QRPayload[:len(o.Tickets[1].QRPayload)-2] + "xx"
	if res := e.scan(door, fx.EventID, tampered, "in"); res.Reason != "invalid_code" {
		t.Fatalf("expected invalid_code for a tampered QR, got %+v", res)
	}

	// Transferring a ticket revokes its old QR code.
	e.must(200, e.request("PUT", fmt.Sprintf("/api/v1/tickets/%d/attendee", o.Tickets[1].ID), customer,
		map[string]string{"first_name": "New", "last_name": "Holder", "email": "new@example.test"}))
	if res := e.scan(door, fx.EventID, o.Tickets[1].QRPayload, "in"); res.Reason != "qr_revoked" {
		t.Fatalf("expected qr_revoked after transfer, got %+v", res)
	}
	fresh := e.order(customer, o.ID)
	if res := e.scan(door, fx.EventID, fresh.Tickets[1].QRPayload, "in"); res.Result != "accepted" {
		t.Fatalf("new QR should be accepted, got %+v", res)
	}
}

// Organisers only ever see and change their own data.
func TestOrganiserIsolation(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	a := e.publishedEvent(admin, 72*time.Hour, 50, ticketType("General", 10000, 50))
	b := e.publishedEvent(admin, 72*time.Hour, 50, ticketType("General", 10000, 50))
	_, customer := e.user("cust")
	orderB := e.buy(customer, b.EventID, b.Types[0], 1)

	ownerA := a.OwnerToken
	for _, path := range []string{
		fmt.Sprintf("/api/v1/admin/events/%d", b.EventID),
		fmt.Sprintf("/api/v1/admin/events/%d/ticket-types", b.EventID),
		fmt.Sprintf("/api/v1/admin/events/%d/attendees", b.EventID),
		fmt.Sprintf("/api/v1/admin/orders/%d", orderB.ID),
	} {
		if r := e.request("GET", path, ownerA, nil); r.Status != 404 {
			t.Errorf("GET %s as another organiser: expected 404, got %d", path, r.Status)
		}
	}
	e.must(404, e.request("PUT", fmt.Sprintf("/api/v1/admin/ticket-types/%d", b.Types[0]), ownerA, ticketType("Hacked", 1, 50)))
	e.must(404, e.request("POST", fmt.Sprintf("/api/v1/admin/events/%d/cancel", b.EventID), ownerA, map[string]string{"reason": "x"}))

	var orders []struct {
		ID int64 `json:"id"`
	}
	e.must(200, e.request("GET", "/api/v1/admin/orders", ownerA, nil)).decode(t, &orders)
	for _, o := range orders {
		if o.ID == orderB.ID {
			t.Fatal("organiser A can list organiser B's orders")
		}
	}
	// An organiser cannot approve their own event.
	e.must(403, e.request("POST", fmt.Sprintf("/api/v1/admin/events/%d/approve", a.EventID), ownerA, nil))
}

// Roles are configurable, and nobody can grant more than they hold.
func TestCustomRolesAndEscalationGuard(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)

	var role struct {
		ID int64 `json:"id"`
	}
	e.must(201, e.request("POST", "/api/v1/admin/roles", admin, map[string]any{
		"name": "Marketing Manager " + fmt.Sprint(time.Now().UnixNano()), "scope": "platform",
		"permissions": []string{rbac.DashboardView, rbac.ReportsView, rbac.EventsView, rbac.AllOrganisers},
	})).decode(t, &role)

	email, _ := e.user("marketer")
	var target struct {
		ID int64 `json:"id"`
	}
	e.must(200, e.request("GET", "/api/v1/admin/users?q="+email, admin, nil))
	if err := e.app.DB.QueryRowContext(e.ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&target.ID); err != nil {
		t.Fatal(err)
	}
	e.must(200, e.request("PUT", fmt.Sprintf("/api/v1/admin/users/%d/roles", target.ID), admin, map[string]any{"role_ids": []int64{role.ID}}))
	marketer := e.login(email)
	e.must(200, e.request("GET", "/api/v1/admin/reports/ticket-sales", marketer, nil))
	e.must(403, e.request("GET", "/api/v1/admin/reports/revenue", marketer, nil))
	e.must(403, e.request("POST", "/api/v1/admin/events", marketer, map[string]any{}))

	// A user manager who cannot see audit logs cannot create a role that grants them.
	managerEmail, _ := e.user("usermgr")
	var mgrRole struct {
		ID int64 `json:"id"`
	}
	e.must(201, e.request("POST", "/api/v1/admin/roles", admin, map[string]any{
		"name": "User Manager " + fmt.Sprint(time.Now().UnixNano()), "scope": "platform",
		"permissions": []string{rbac.UsersView, rbac.UsersManage, rbac.RolesManage},
	})).decode(t, &mgrRole)
	if err := e.app.DB.QueryRowContext(e.ctx, `SELECT id FROM users WHERE email = ?`, managerEmail).Scan(&target.ID); err != nil {
		t.Fatal(err)
	}
	e.must(200, e.request("PUT", fmt.Sprintf("/api/v1/admin/users/%d/roles", target.ID), admin, map[string]any{"role_ids": []int64{mgrRole.ID}}))
	manager := e.login(managerEmail)
	e.must(403, e.request("POST", "/api/v1/admin/roles", manager, map[string]any{
		"name": "Sneaky", "scope": "platform", "permissions": []string{rbac.AuditView},
	}))
	// ...nor assign the Administrator role to themselves.
	var adminRoleID int64
	_ = e.app.DB.QueryRowContext(e.ctx, `SELECT id FROM roles WHERE slug = ?`, rbac.RoleAdmin).Scan(&adminRoleID)
	e.must(403, e.request("PUT", fmt.Sprintf("/api/v1/admin/users/%d/roles", target.ID), manager, map[string]any{"role_ids": []int64{adminRoleID}}))
	// Organiser-only permissions cannot be smuggled into organiser roles.
	e.must(422, e.request("POST", "/api/v1/admin/roles", admin, map[string]any{
		"name": "Bad organiser role", "scope": "organiser", "permissions": []string{rbac.UsersManage},
	}))
}

// A customer refund from request to money returned, including the effect at the door.
func TestRefundLifecycle(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 10, ticketType("General", 20000, 10))
	_, customer := e.user("refundee")
	o := e.buy(customer, fx.EventID, fx.Types[0], 2)

	var refund struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	e.must(201, e.request("POST", fmt.Sprintf("/api/v1/orders/%d/refunds", o.ID), customer, map[string]any{
		"ticket_ids": []int64{o.Tickets[0].ID}, "reason": "Cannot attend",
	})).decode(t, &refund)
	// The same ticket cannot be refunded twice.
	e.must(422, e.request("POST", fmt.Sprintf("/api/v1/orders/%d/refunds", o.ID), customer, map[string]any{
		"ticket_ids": []int64{o.Tickets[0].ID}, "reason": "Again",
	}))

	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/refunds/%d/approve", refund.ID), fx.OwnerToken, nil))
	e.clock.Advance(70 * time.Hour) // doors are open
	_, door := e.user("door", rbac.RoleCheckinOfficer)
	if res := e.scan(door, fx.EventID, o.Tickets[0].QRPayload, "in"); res.Reason != "ticket_void" {
		t.Fatalf("refunded ticket must not get in, got %+v", res)
	}
	e.clock.Advance(-70 * time.Hour)

	// Organisers may approve but not move money; finance processes the refund.
	e.must(403, e.request("POST", fmt.Sprintf("/api/v1/admin/refunds/%d/process", refund.ID), fx.OwnerToken, nil))
	_, finance := e.user("finance", rbac.RoleFinanceManager)
	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/refunds/%d/process", refund.ID), finance, nil))

	after := e.order(customer, o.ID)
	if after.Status != "partially_refunded" || after.RefundedCents != 20000 || after.Payments[0].RefundedCents != 20000 {
		t.Fatalf("unexpected order after refund: %+v", after)
	}
	if sold, _ := e.inventory(fx.Types[0]); sold != 1 {
		t.Fatalf("refunded seat not returned to inventory: sold=%d", sold)
	}
}

// Manual EFT: bank details, staff confirmation with amount check, then tickets.
func TestEFTPayment(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 7*24*time.Hour, 10, ticketType("General", 15000, 10))
	email, customer := e.user("eft")
	o, r := e.reserve(customer, fx.EventID, fx.Types[0], 2, "")
	e.must(201, r)
	_, r = e.checkout(customer, o, "eft")
	e.must(200, r)
	runJobs(t, e)
	if len(e.mail.to(email)) != 1 {
		t.Fatal("expected EFT instructions email")
	}

	pending := e.order(customer, o.ID)
	paymentID := pending.Payments[0].ID
	_, finance := e.user("finance", rbac.RoleFinanceManager)
	e.must(422, e.request("POST", fmt.Sprintf("/api/v1/admin/payments/%d/confirm", paymentID), finance, map[string]any{"amount_cents": 100, "bank_reference": "X"}))
	// Organisers cannot mark their own orders as paid.
	e.must(403, e.request("POST", fmt.Sprintf("/api/v1/admin/payments/%d/confirm", paymentID), fx.OwnerToken, map[string]any{"amount_cents": 30000, "bank_reference": "X"}))
	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/payments/%d/confirm", paymentID), finance, map[string]any{"amount_cents": 30000, "bank_reference": "FNB-1"}))
	if paid := e.order(customer, o.ID); paid.Status != "paid" || len(paid.Tickets) != 2 {
		t.Fatalf("expected paid order with tickets, got %s", paid.Status)
	}
	e.must(409, e.request("POST", fmt.Sprintf("/api/v1/admin/payments/%d/confirm", paymentID), finance, map[string]any{"amount_cents": 30000, "bank_reference": "FNB-1"}))
}

// Cancelling an event releases holds and queues refunds for every paid order.
func TestEventCancellation(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 20, ticketType("General", 10000, 20))
	_, buyer := e.user("paid")
	_, holder := e.user("holder")
	paid := e.buy(buyer, fx.EventID, fx.Types[0], 2)
	held, r := e.reserve(holder, fx.EventID, fx.Types[0], 3, "")
	e.must(201, r)

	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/events/%d/cancel", fx.EventID), fx.OwnerToken, map[string]string{"reason": "Venue flooded"}))
	if o := e.order(holder, held.ID); o.Status != "cancelled" {
		t.Fatalf("pending order should be cancelled, got %s", o.Status)
	}
	after := e.order(buyer, paid.ID)
	if len(after.Refunds) != 1 || after.Refunds[0].Status != "approved" {
		t.Fatalf("paid order should have an approved refund, got %+v", after.Refunds)
	}
	for _, tk := range after.Tickets {
		if tk.Status != "void" {
			t.Fatalf("tickets of a cancelled event must be void, got %s", tk.Status)
		}
	}
	if _, reserved := e.inventory(fx.Types[0]); reserved != 0 {
		t.Fatal("cancelled event still holds reservations")
	}
	if _, r := e.reserve(buyer, fx.EventID, fx.Types[0], 1, ""); r.Status != 409 {
		t.Fatalf("cancelled event must not sell, got %d", r.Status)
	}
}

// Promotion usage limits hold under reservation and are released with the hold.
func TestPromotionUsageLimit(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 20, ticketType("General", 10000, 20))
	e.must(201, e.request("POST", fmt.Sprintf("/api/v1/admin/events/%d/promotions", fx.EventID), fx.OwnerToken, map[string]any{
		"code": "ONCE", "discount_type": "fixed", "discount_value": 2500, "max_uses": 1, "is_active": true,
	}))
	_, first := e.user("first")
	_, second := e.user("second")
	o, r := e.reserve(first, fx.EventID, fx.Types[0], 1, "ONCE")
	e.must(201, r)
	if o.TotalCents != 7500 {
		t.Fatalf("expected R75 after discount, got %d", o.TotalCents)
	}
	if _, r := e.reserve(second, fx.EventID, fx.Types[0], 1, "ONCE"); r.Status != 422 {
		t.Fatalf("expected exhausted promo code, got %d", r.Status)
	}
	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/orders/%d/cancel", o.ID), first, nil))
	if _, r := e.reserve(second, fx.EventID, fx.Types[0], 1, "ONCE"); r.Status != 201 {
		t.Fatalf("released promo code should be usable again, got %d", r.Status)
	}
}

// Sessions: refresh tokens rotate, reuse is detected, deactivation is immediate.
func TestSessionSecurity(t *testing.T) {
	e := newEnv(t)
	email, _ := e.user("session")
	login := e.must(200, e.request("POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}, "X-Client", "mobile"))
	var s struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	login.decode(t, &s)
	first := s.RefreshToken

	rotated := e.must(200, e.request("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": first}, "X-Client", "mobile"))
	rotated.decode(t, &s)
	if s.RefreshToken == first {
		t.Fatal("refresh token was not rotated")
	}
	// Re-using the old token revokes the whole session, including the new token.
	e.must(401, e.request("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": first}, "X-Client", "mobile"))
	e.must(401, e.request("POST", "/api/v1/auth/refresh", "", map[string]string{"refresh_token": s.RefreshToken}, "X-Client", "mobile"))

	// Deactivated accounts lose access immediately, even with a valid access token.
	admin := e.login(superAdminMail)
	var id int64
	_ = e.app.DB.QueryRowContext(e.ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&id)
	token := e.login(email)
	e.must(200, e.request("GET", "/api/v1/auth/me", token, nil))
	e.must(200, e.request("POST", fmt.Sprintf("/api/v1/admin/users/%d/deactivate", id), admin, nil))
	e.must(401, e.request("GET", "/api/v1/auth/me", token, nil))
	e.must(403, e.request("POST", "/api/v1/auth/login", "", map[string]string{"email": email, "password": testPassword}))
}

func TestLoginLockoutCountsOnlyFailures(t *testing.T) {
	e := newEnv(t)
	email, _ := e.user("lockout")
	creds := func(password string) map[string]string {
		return map[string]string{"email": email, "password": password}
	}

	// Signing in repeatedly with the right password never locks the account.
	for i := 0; i < 15; i++ {
		e.must(200, e.request("POST", "/api/v1/auth/login", "", creds(testPassword)))
	}
	// Ten wrong passwords do, and then even the right password is refused for a while.
	for i := 0; i < 10; i++ {
		e.must(401, e.request("POST", "/api/v1/auth/login", "", creds("wrong-password-1")))
	}
	e.must(429, e.request("POST", "/api/v1/auth/login", "", creds(testPassword)))
}
