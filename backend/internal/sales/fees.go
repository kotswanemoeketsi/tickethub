package sales

// PaidTicketFeeCents is the fixed organiser fee for each paid ticket (ZAR).
const PaidTicketFeeCents int64 = 500

// ticketFees uses the discounted price actually paid and rounds commission to
// the nearest cent per ticket. Free and complimentary tickets incur no fees.
func ticketFees(price int64, bps int, complimentary bool) (commission, fixed int64) {
	if complimentary || price <= 0 {
		return 0, 0
	}
	return (price*int64(bps) + 5000) / 10000, PaidTicketFeeCents
}
