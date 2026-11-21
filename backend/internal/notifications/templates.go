package notifications

import (
	"bytes"
	"fmt"
	"html/template"
	"regexp"
	"strings"
	texttemplate "text/template"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/money"
)

// Each email has an HTML body (wrapped in the shared layout) and a plain-text alternative.
// HTML uses inline styles and tables because many email clients ignore stylesheets.

var layout = template.Must(template.New("layout").Parse(`<!doctype html>
<html><body style="margin:0;padding:0;background:#f4f4f5;font-family:Arial,Helvetica,sans-serif;color:#18181b;">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;padding:24px 0;">
<tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:12px;overflow:hidden;">
<tr><td style="background:#4f46e5;color:#ffffff;padding:20px 28px;font-size:20px;font-weight:bold;">{{.AppName}}</td></tr>
<tr><td style="padding:28px;font-size:15px;line-height:1.6;">{{.Body}}</td></tr>
<tr><td style="padding:16px 28px;color:#71717a;font-size:12px;border-top:1px solid #e4e4e7;">
Sent by {{.AppName}} · <a href="{{.PublicURL}}" style="color:#4f46e5;">{{.PublicURL}}</a></td></tr>
</table></td></tr></table></body></html>`))

const button = `<p style="margin:24px 0;"><a href="{{.Link}}" style="background:#4f46e5;color:#ffffff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:bold;display:inline-block;">`

var htmlTemplates = template.Must(template.New("emails").Funcs(template.FuncMap{"money": formatCents}).Parse(`
{{define "event_box"}}<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;border-radius:8px;margin:16px 0;">
<tr><td style="padding:16px;"><div style="font-size:17px;font-weight:bold;">{{.Order.Event.Title}}</div>
<div style="color:#52525b;">{{.When}}</div><div style="color:#52525b;">{{.Order.Event.VenueName}}{{if .Order.Event.VenueAddress}}, {{.Order.Event.VenueAddress}}{{end}}</div></td></tr></table>{{end}}

{{define "order_tickets"}}<p>Hi {{.Name}},</p>
<p>{{if .Complimentary}}You have been sent complimentary tickets.{{else}}Thank you for your order! Your payment was received and your tickets are attached as a PDF.{{end}}</p>
{{template "event_box" .}}
<table role="presentation" width="100%" cellpadding="6" cellspacing="0" style="border-collapse:collapse;">
{{range .Order.Items}}<tr><td style="border-bottom:1px solid #e4e4e7;">{{.Quantity}} × {{.TicketTypeName}}</td><td align="right" style="border-bottom:1px solid #e4e4e7;">{{money .TotalCents $.Order.Currency}}</td></tr>{{end}}
{{if gt .Order.DiscountCents 0}}<tr><td>Discount{{if .Order.PromotionCode}} ({{.Order.PromotionCode}}){{end}}</td><td align="right">−{{money .Order.DiscountCents .Order.Currency}}</td></tr>{{end}}
<tr><td><strong>Total</strong></td><td align="right"><strong>{{.Total}}</strong></td></tr></table>
<p>Order reference: <strong>{{.Order.Reference}}</strong></p>
` + button + `View your tickets</a></p>
<p style="color:#52525b;font-size:13px;">Each ticket has its own QR code and can be scanned once. Keep your tickets private.</p>{{end}}

{{define "single_ticket"}}<p>Hi {{.Name}},</p>
<p>{{.Purchaser}} has a ticket for you. It is attached as a PDF — show the QR code at the entrance.</p>
{{template "event_box" .}}
<p style="color:#52525b;font-size:13px;">Order reference {{.Order.Reference}}. Each ticket can be scanned once.</p>{{end}}

{{define "eft_instructions"}}<p>Hi {{.Name}},</p>
<p>Your tickets for <strong>{{.Order.Event.Title}}</strong> are reserved until <strong>{{.PayBy}}</strong>. To complete your order, make an EFT payment with these details:</p>
<table role="presentation" cellpadding="6" cellspacing="0" style="background:#f4f4f5;border-radius:8px;margin:16px 0;width:100%;">
<tr><td>Bank</td><td><strong>{{.EFT.BankName}}</strong></td></tr>
<tr><td>Account name</td><td><strong>{{.EFT.AccountName}}</strong></td></tr>
<tr><td>Account number</td><td><strong>{{.EFT.AccountNumber}}</strong></td></tr>
<tr><td>Branch code</td><td><strong>{{.EFT.BranchCode}}</strong></td></tr>
<tr><td>Payment reference</td><td><strong style="font-size:17px;">{{.EFT.Reference}}</strong></td></tr>
<tr><td>Amount</td><td><strong>{{.Amount}}</strong></td></tr></table>
<p>Please use the reference exactly as shown so we can match your payment. Your tickets will be emailed as soon as the payment reflects. If we have not received it by the deadline, the reservation is released.</p>
` + button + `View your order</a></p>{{end}}

{{define "event_cancelled"}}<p>Hi {{.Name}},</p>
<p>We are sorry to let you know that <strong>{{.Order.Event.Title}}</strong> has been cancelled by the organiser.</p>
<p>Your tickets on order <strong>{{.Order.Reference}}</strong> are no longer valid, and a full refund has been queued. You will receive another email once the refund has been processed — you do not need to do anything.</p>{{end}}

{{define "refund_update"}}<p>Hi {{.Name}},</p>
{{if eq .Refund.Status "completed"}}<p>We have refunded <strong>{{.Amount}}</strong> for order <strong>{{.Refund.OrderReference}}</strong> ({{.Refund.EventTitle}}).</p>
<p>Card refunds usually reflect within 5–10 business days, depending on your bank. The refunded tickets are no longer valid.</p>
{{else}}<p>Your refund request for order <strong>{{.Refund.OrderReference}}</strong> ({{.Refund.EventTitle}}) was not approved.</p>
{{if .Refund.ReviewNote}}<p style="background:#f4f4f5;border-radius:8px;padding:12px;">{{.Refund.ReviewNote}}</p>{{end}}
<p>Your tickets remain valid.</p>{{end}}{{end}}

{{define "password_reset"}}<p>Hi {{.Name}},</p>
<p>We received a request to reset your password. Click the button below to choose a new one. The link expires soon and can be used once.</p>
` + button + `Reset password</a></p>
<p style="color:#52525b;font-size:13px;">If you did not ask for this, you can ignore this email — your password will not change.</p>{{end}}

{{define "account_invite"}}<p>Hi {{.Name}},</p>
<p>You have been given access to <strong>{{.InvitedTo}}</strong>. Set your password to get started:</p>
` + button + `Set your password</a></p>
<p style="color:#52525b;font-size:13px;">This link expires in 72 hours. If it has expired, use "Forgot password" on the sign-in page.</p>{{end}}
`))

var textTemplates = texttemplate.Must(texttemplate.New("emails").Funcs(texttemplate.FuncMap{"money": formatCents}).Parse(`
{{define "order_tickets"}}Hi {{.Name}},

{{if .Complimentary}}You have been sent complimentary tickets.{{else}}Thank you for your order! Your tickets are attached as a PDF.{{end}}

{{.Order.Event.Title}}
{{.When}}
{{.Order.Event.VenueName}}

{{range .Order.Items}}{{.Quantity}} x {{.TicketTypeName}}: {{money .TotalCents $.Order.Currency}}
{{end}}Total: {{.Total}}
Order reference: {{.Order.Reference}}

View your tickets: {{.Link}}{{end}}

{{define "single_ticket"}}Hi {{.Name}},

{{.Purchaser}} has a ticket for you for {{.Order.Event.Title}} ({{.When}}). It is attached as a PDF.{{end}}

{{define "eft_instructions"}}Hi {{.Name}},

Your tickets for {{.Order.Event.Title}} are reserved until {{.PayBy}}. Please pay by EFT:

Bank: {{.EFT.BankName}}
Account name: {{.EFT.AccountName}}
Account number: {{.EFT.AccountNumber}}
Branch code: {{.EFT.BranchCode}}
Reference: {{.EFT.Reference}}
Amount: {{.Amount}}

Use the reference exactly as shown. View your order: {{.Link}}{{end}}

{{define "event_cancelled"}}Hi {{.Name}},

{{.Order.Event.Title}} has been cancelled. Your tickets on order {{.Order.Reference}} are no longer valid and a full refund has been queued.{{end}}

{{define "refund_update"}}Hi {{.Name}},

{{if eq .Refund.Status "completed"}}We have refunded {{.Amount}} for order {{.Refund.OrderReference}} ({{.Refund.EventTitle}}).{{else}}Your refund request for order {{.Refund.OrderReference}} was not approved. {{.Refund.ReviewNote}}{{end}}{{end}}

{{define "password_reset"}}Hi {{.Name}},

Reset your password: {{.Link}}

If you did not ask for this, ignore this email.{{end}}

{{define "account_invite"}}Hi {{.Name}},

You have been given access to {{.InvitedTo}}. Set your password: {{.Link}}{{end}}
`))

type rendered struct {
	html string
	text string
}

func render(name string, data any) (rendered, error) {
	if htmlTemplates.Lookup(name) == nil {
		return rendered{}, fmt.Errorf("%w: %s", errUnknownTemplate, name)
	}
	var h, t bytes.Buffer
	if err := htmlTemplates.ExecuteTemplate(&h, name, data); err != nil {
		return rendered{}, err
	}
	if err := textTemplates.ExecuteTemplate(&t, name, data); err != nil {
		return rendered{}, err
	}
	return rendered{html: h.String(), text: collapseBlankLines(t.String())}, nil
}

var blankLines = regexp.MustCompile(`\n{3,}`)

func collapseBlankLines(s string) string {
	return strings.TrimSpace(blankLines.ReplaceAllString(s, "\n\n"))
}

func formatCents(cents int64, currency string) string {
	return money.Format(cents, currency)
}

func locationOf(tz string) *time.Location {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}
