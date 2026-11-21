package sales

import (
	"math/big"
)

// Discount types.
const (
	DiscountPercentage = "percentage" // Value in basis points: 1000 = 10%
	DiscountFixed      = "fixed"      // Value in cents
)

// Discount is a promotion applied to an order.
type Discount struct {
	Type  string
	Value int64
}

// PriceLine is one ticket type in a basket.
type PriceLine struct {
	TicketTypeID   int64
	UnitPriceCents int64
	Quantity       int
	Eligible       bool // whether the discount applies to this line
}

// PricedLine is a line with its share of the discount.
type PricedLine struct {
	PriceLine
	GrossCents    int64
	DiscountCents int64
	TotalCents    int64
}

// Quote is a fully priced basket.
type Quote struct {
	Lines         []PricedLine
	SubtotalCents int64
	DiscountCents int64
	TotalCents    int64
}

// Price computes order totals. The discount is spread across eligible lines in proportion
// to their value so that partial refunds return exactly what was paid for each ticket, and
// the line discounts always add up to the order discount to the cent.
func Price(lines []PriceLine, d *Discount) Quote {
	q := Quote{Lines: make([]PricedLine, len(lines))}
	var eligibleGross int64
	for i, l := range lines {
		gross := l.UnitPriceCents * int64(l.Quantity)
		q.Lines[i] = PricedLine{PriceLine: l, GrossCents: gross}
		q.SubtotalCents += gross
		if l.Eligible {
			eligibleGross += gross
		}
	}

	if d != nil && eligibleGross > 0 {
		switch d.Type {
		case DiscountPercentage:
			// Round to the nearest cent.
			q.DiscountCents = mulDiv(eligibleGross, d.Value, 10_000, true)
		case DiscountFixed:
			q.DiscountCents = min(d.Value, eligibleGross)
		}
		q.DiscountCents = min(max(q.DiscountCents, 0), eligibleGross)
	}

	// Allocate proportionally (rounding down), then hand out the remaining cents one at a
	// time to eligible lines that still have room.
	allocated := int64(0)
	for i := range q.Lines {
		l := &q.Lines[i]
		if !l.Eligible || q.DiscountCents == 0 {
			continue
		}
		l.DiscountCents = mulDiv(q.DiscountCents, l.GrossCents, eligibleGross, false)
		allocated += l.DiscountCents
	}
	for remainder := q.DiscountCents - allocated; remainder > 0; {
		progressed := false
		for i := range q.Lines {
			l := &q.Lines[i]
			if remainder == 0 {
				break
			}
			if l.Eligible && l.DiscountCents < l.GrossCents {
				l.DiscountCents++
				remainder--
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}

	for i := range q.Lines {
		l := &q.Lines[i]
		l.TotalCents = l.GrossCents - l.DiscountCents
		q.TotalCents += l.TotalCents
	}
	return q
}

// UnitPrices splits a line total into per-ticket prices that add up exactly. The first
// tickets absorb any leftover cents: 1000 over 3 tickets gives 334, 333, 333.
func UnitPrices(lineTotalCents int64, quantity int) []int64 {
	if quantity <= 0 {
		return nil
	}
	out := make([]int64, quantity)
	base := lineTotalCents / int64(quantity)
	extra := lineTotalCents % int64(quantity)
	for i := range out {
		out[i] = base
		if int64(i) < extra {
			out[i]++
		}
	}
	return out
}

// mulDiv computes a*b/c without overflowing int64 intermediates.
func mulDiv(a, b, c int64, roundHalfUp bool) int64 {
	if c == 0 {
		return 0
	}
	num := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if roundHalfUp {
		num.Add(num, big.NewInt(c/2))
	}
	return num.Quo(num, big.NewInt(c)).Int64()
}
