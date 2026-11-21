// Package notifications renders and sends the platform's emails. Order-related emails run
// as background jobs from the transactional outbox, so they are retried until delivered.
package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/jobs"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/mailer"
	"github.com/kotswanemoeketsi/gotaticket/internal/platform/money"
	"github.com/kotswanemoeketsi/gotaticket/internal/sales"
)

// Notifier sends emails.
type Notifier struct {
	mailer    mailer.Mailer
	sales     *sales.Service
	appName   string
	publicURL string
	logger    *slog.Logger
}

// New returns a notifier.
func New(m mailer.Mailer, salesSvc *sales.Service, appName, publicURL string, logger *slog.Logger) *Notifier {
	return &Notifier{mailer: m, sales: salesSvc, appName: appName, publicURL: strings.TrimSuffix(publicURL, "/"), logger: logger}
}

// SetSales attaches the sales service once it has been constructed (the identity service,
// which also uses this notifier, is built first).
func (n *Notifier) SetSales(s *sales.Service) { n.sales = s }

// Register binds the email jobs to the worker.
func (n *Notifier) Register(r *jobs.Runner) {
	r.Register(sales.JobSendOrderTickets, n.orderJob(n.sendOrderTickets))
	r.Register(sales.JobSendEFTInstructions, n.orderJob(n.sendEFTInstructions))
	r.Register(sales.JobSendEventCancelled, n.orderJob(n.sendEventCancelled))
	r.Register(sales.JobSendRefundUpdate, n.sendRefundUpdate)
	r.Register(sales.JobSendTicket, n.sendTicket)
}

type orderPayload struct {
	OrderID  int64 `json:"order_id"`
	RefundID int64 `json:"refund_id"`
	TicketID int64 `json:"ticket_id"`
}

func decode(raw json.RawMessage) (orderPayload, error) {
	var p orderPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, jobs.Permanent(fmt.Errorf("decode job payload: %w", err))
	}
	return p, nil
}

func (n *Notifier) orderJob(send func(context.Context, sales.OrderView) error) jobs.Handler {
	return func(ctx context.Context, raw json.RawMessage) error {
		p, err := decode(raw)
		if err != nil {
			return err
		}
		order, err := n.sales.OrderForEmail(ctx, p.OrderID)
		if apperr.IsCode(err, "not_found") {
			return jobs.Permanent(err)
		}
		if err != nil {
			return err
		}
		return send(ctx, order)
	}
}

func (n *Notifier) sendOrderTickets(ctx context.Context, o sales.OrderView) error {
	if o.Status != sales.OrderPaid && o.Status != sales.OrderPartiallyRefunded {
		return nil
	}
	pdf, err := n.sales.RenderTicketsPDF(ctx, o.ID, 0)
	if apperr.IsCode(err, "no_valid_tickets") {
		return nil
	}
	if err != nil {
		return err
	}
	body, err := render("order_tickets", map[string]any{
		"Name": firstName(o.CustomerName), "Order": o, "When": sales.EventDateText(o.Event.StartsAt, o.Event.EndsAt, o.Event.Timezone),
		"Link": n.publicURL + fmt.Sprintf("/account/orders/%d", o.ID), "Total": money.Format(o.TotalCents, o.Currency),
		"Complimentary": o.Source == "complimentary",
	})
	if err != nil {
		return err
	}
	attachment := mailer.Attachment{Filename: "tickets-" + o.Reference + ".pdf", ContentType: "application/pdf", Data: pdf}
	if err := n.send(ctx, o.CustomerEmail, "Your tickets for "+o.Event.Title, body, attachment); err != nil {
		return err
	}

	// Attendees with their own email address also receive their individual ticket.
	sent := map[string]bool{strings.ToLower(o.CustomerEmail): true}
	for _, t := range o.Tickets {
		email := strings.ToLower(t.HolderEmail)
		if t.Status != "valid" || sent[email] {
			continue
		}
		sent[email] = true
		if err := n.sendSingleTicket(ctx, o, t.ID, t.HolderFirstName, t.HolderEmail); err != nil {
			return err
		}
	}
	return nil
}

func (n *Notifier) sendTicket(ctx context.Context, raw json.RawMessage) error {
	p, err := decode(raw)
	if err != nil {
		return err
	}
	orderID, name, email, err := n.sales.TicketForEmail(ctx, p.TicketID)
	if apperr.IsCode(err, "not_found") {
		return jobs.Permanent(err)
	}
	if err != nil {
		return err
	}
	o, err := n.sales.OrderForEmail(ctx, orderID)
	if err != nil {
		return err
	}
	return n.sendSingleTicket(ctx, o, p.TicketID, firstName(name), email)
}

func (n *Notifier) sendSingleTicket(ctx context.Context, o sales.OrderView, ticketID int64, name, email string) error {
	pdf, err := n.sales.RenderTicketsPDF(ctx, o.ID, ticketID)
	if apperr.IsCode(err, "no_valid_tickets") {
		return nil
	}
	if err != nil {
		return err
	}
	body, err := render("single_ticket", map[string]any{
		"Name": name, "Order": o, "When": sales.EventDateText(o.Event.StartsAt, o.Event.EndsAt, o.Event.Timezone), "Purchaser": o.CustomerName,
	})
	if err != nil {
		return err
	}
	return n.send(ctx, email, "Your ticket for "+o.Event.Title, body,
		mailer.Attachment{Filename: fmt.Sprintf("ticket-%s.pdf", o.Reference), ContentType: "application/pdf", Data: pdf})
}

func (n *Notifier) sendEFTInstructions(ctx context.Context, o sales.OrderView) error {
	if o.EFT == nil {
		return nil // already paid, cancelled or expired
	}
	body, err := render("eft_instructions", map[string]any{
		"Name": firstName(o.CustomerName), "Order": o, "EFT": o.EFT, "Amount": money.Format(o.EFT.AmountCents, o.EFT.Currency),
		"PayBy": o.EFT.PayBy.In(locationOf(o.Event.Timezone)).Format("Mon 2 Jan 2006, 15:04 MST"),
		"Link":  n.publicURL + fmt.Sprintf("/account/orders/%d", o.ID),
	})
	if err != nil {
		return err
	}
	return n.send(ctx, o.CustomerEmail, "Complete your EFT payment for "+o.Event.Title, body)
}

func (n *Notifier) sendEventCancelled(ctx context.Context, o sales.OrderView) error {
	body, err := render("event_cancelled", map[string]any{"Name": firstName(o.CustomerName), "Order": o})
	if err != nil {
		return err
	}
	return n.send(ctx, o.CustomerEmail, o.Event.Title+" has been cancelled", body)
}

func (n *Notifier) sendRefundUpdate(ctx context.Context, raw json.RawMessage) error {
	p, err := decode(raw)
	if err != nil {
		return err
	}
	r, err := n.sales.RefundForEmail(ctx, p.RefundID)
	if apperr.IsCode(err, "not_found") {
		return jobs.Permanent(err)
	}
	if err != nil {
		return err
	}
	if r.Status != "completed" && r.Status != "rejected" {
		return nil
	}
	body, err := render("refund_update", map[string]any{"Name": firstName(r.CustomerName), "Refund": r, "Amount": money.Format(r.AmountCents, r.Currency)})
	if err != nil {
		return err
	}
	subject := fmt.Sprintf("Your refund for order %s has been processed", r.OrderReference)
	if r.Status == "rejected" {
		subject = fmt.Sprintf("Update on your refund request for order %s", r.OrderReference)
	}
	return n.send(ctx, r.CustomerEmail, subject, body)
}

// PasswordReset implements identity.Notifier.
func (n *Notifier) PasswordReset(ctx context.Context, to, name, link string) error {
	body, err := render("password_reset", map[string]any{"Name": name, "Link": link})
	if err != nil {
		return err
	}
	return n.send(ctx, to, "Reset your "+n.appName+" password", body)
}

// AccountInvite implements identity.Notifier.
func (n *Notifier) AccountInvite(ctx context.Context, to, name, link, invitedTo string) error {
	body, err := render("account_invite", map[string]any{"Name": name, "Link": link, "InvitedTo": invitedTo})
	if err != nil {
		return err
	}
	return n.send(ctx, to, "You've been invited to "+invitedTo, body)
}

func (n *Notifier) send(ctx context.Context, to, subject string, body rendered, attachments ...mailer.Attachment) error {
	var html bytes.Buffer
	if err := layout.Execute(&html, map[string]any{"AppName": n.appName, "Body": template.HTML(body.html), "PublicURL": n.publicURL}); err != nil {
		return err
	}
	err := n.mailer.Send(ctx, mailer.Message{
		To: []string{to}, Subject: subject, HTML: html.String(),
		Text: body.text + "\n\n— " + n.appName + "\n" + n.publicURL, Attachments: attachments,
	})
	if err != nil {
		return fmt.Errorf("send %q to %s: %w", subject, to, err)
	}
	return nil
}

func firstName(full string) string {
	if f := strings.Fields(full); len(f) > 0 {
		return f[0]
	}
	return "there"
}

var errUnknownTemplate = errors.New("unknown email template")
