package sales

import (
	"context"
	"time"
)

// ExpireReservations releases pending orders whose hold has lapsed. Run periodically by the
// worker; each order is handled in its own short transaction.
func (s *Service) ExpireReservations(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM orders WHERE status = 'pending' AND expires_at <= ? ORDER BY expires_at LIMIT 200`, s.now())
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	expired := 0
	for _, id := range ids {
		if err := s.expireOrder(ctx, id); err != nil {
			s.logger.Error("expire reservation", "order_id", id, "error", err)
			continue
		}
		expired++
	}
	return expired, nil
}

// ReconcilePayments re-verifies online payments still pending after a few minutes, so a lost
// or delayed webhook can never leave a paying customer without tickets.
func (s *Service) ReconcilePayments(ctx context.Context) (int, error) {
	now := s.now()
	rows, err := s.db.QueryContext(ctx, `SELECT provider_reference FROM payments
		WHERE status = 'pending' AND method = 'online' AND provider = ? AND created_at BETWEEN ? AND ?
		ORDER BY created_at LIMIT 50`, s.gateway.Name(), now.Add(-48*time.Hour), now.Add(-2*time.Minute))
	if err != nil {
		return 0, err
	}
	var refs []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return 0, err
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, ref := range refs {
		if err := s.SyncPayment(ctx, ref); err != nil {
			s.logger.Warn("reconcile payment", "reference", ref, "error", err)
		}
	}
	return len(refs), nil
}

// ReconcileRefunds polls the gateway for refunds that are still processing.
func (s *Service) ReconcileRefunds(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.provider_refund_id FROM refunds r JOIN payments p ON p.id = r.payment_id WHERE r.status = 'processing' AND r.provider_refund_id <> '' AND p.provider = ? LIMIT 50`, s.gateway.Name())
	if err != nil {
		return 0, err
	}
	type pending struct {
		id         int64
		providerID string
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.providerID); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range list {
		result, err := s.gateway.RefundStatus(ctx, p.providerID)
		if err != nil {
			s.logger.Warn("reconcile refund", "refund_id", p.id, "error", err)
			continue
		}
		if err := s.applyGatewayRefund(ctx, p.id, result); err != nil {
			s.logger.Error("apply refund status", "refund_id", p.id, "error", err)
		}
	}
	return len(list), nil
}
