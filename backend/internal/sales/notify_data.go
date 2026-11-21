package sales

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
)

// These read-only helpers give the notification worker the data it needs. They perform no
// authorisation because they run in the background on behalf of the system.

// OrderForEmail returns an order without QR payloads.
func (s *Service) OrderForEmail(ctx context.Context, orderID int64) (OrderView, error) {
	return s.orderView(ctx, s.db, orderID, false)
}

// RefundForEmail returns a refund.
func (s *Service) RefundForEmail(ctx context.Context, refundID int64) (RefundView, error) {
	return s.refundView(ctx, refundID)
}

// TicketForEmail returns a ticket's order ID and current holder.
func (s *Service) TicketForEmail(ctx context.Context, ticketID int64) (orderID int64, holderName, holderEmail string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT t.order_id, CONCAT(a.first_name, ' ', a.last_name), a.email
		FROM tickets t JOIN attendees a ON a.id = t.attendee_id WHERE t.id = ?`, ticketID).Scan(&orderID, &holderName, &holderEmail)
	if errors.Is(err, sql.ErrNoRows) {
		err = apperr.NotFound("Ticket")
	}
	return orderID, holderName, holderEmail, err
}
