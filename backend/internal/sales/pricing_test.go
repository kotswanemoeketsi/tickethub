package sales

import (
	"reflect"
	"testing"
)

func TestPriceWithoutDiscount(t *testing.T) {
	q := Price([]PriceLine{
		{TicketTypeID: 1, UnitPriceCents: 25000, Quantity: 2},
		{TicketTypeID: 2, UnitPriceCents: 9950, Quantity: 1},
	}, nil)
	if q.SubtotalCents != 59950 || q.DiscountCents != 0 || q.TotalCents != 59950 {
		t.Fatalf("unexpected totals: %+v", q)
	}
}

func TestPercentageDiscountIsAllocatedExactly(t *testing.T) {
	q := Price([]PriceLine{
		{TicketTypeID: 1, UnitPriceCents: 333, Quantity: 3, Eligible: true},
		{TicketTypeID: 2, UnitPriceCents: 1001, Quantity: 1, Eligible: true},
		{TicketTypeID: 3, UnitPriceCents: 5000, Quantity: 1, Eligible: false},
	}, &Discount{Type: DiscountPercentage, Value: 1500}) // 15%

	eligible := int64(333*3 + 1001)
	wantDiscount := (eligible*1500 + 5000) / 10000
	if q.DiscountCents != wantDiscount {
		t.Fatalf("discount = %d, want %d", q.DiscountCents, wantDiscount)
	}
	var sumLineDiscounts, sumLineTotals int64
	for _, l := range q.Lines {
		sumLineDiscounts += l.DiscountCents
		sumLineTotals += l.TotalCents
		if !l.Eligible && l.DiscountCents != 0 {
			t.Fatalf("ineligible line received a discount: %+v", l)
		}
	}
	if sumLineDiscounts != q.DiscountCents {
		t.Fatalf("line discounts %d do not add up to order discount %d", sumLineDiscounts, q.DiscountCents)
	}
	if sumLineTotals != q.TotalCents || q.TotalCents != q.SubtotalCents-q.DiscountCents {
		t.Fatalf("totals inconsistent: %+v", q)
	}
}

func TestFixedDiscountIsCappedAtEligibleValue(t *testing.T) {
	q := Price([]PriceLine{
		{TicketTypeID: 1, UnitPriceCents: 1000, Quantity: 1, Eligible: true},
		{TicketTypeID: 2, UnitPriceCents: 4000, Quantity: 1, Eligible: false},
	}, &Discount{Type: DiscountFixed, Value: 50000})
	if q.DiscountCents != 1000 || q.TotalCents != 4000 {
		t.Fatalf("expected discount capped at 1000, got %+v", q)
	}
}

func TestHundredPercentDiscountMakesOrderFree(t *testing.T) {
	q := Price([]PriceLine{{TicketTypeID: 1, UnitPriceCents: 777, Quantity: 3, Eligible: true}},
		&Discount{Type: DiscountPercentage, Value: 10000})
	if q.TotalCents != 0 || q.DiscountCents != 2331 {
		t.Fatalf("expected free order, got %+v", q)
	}
}

func TestDiscountWithNoEligibleLines(t *testing.T) {
	q := Price([]PriceLine{{TicketTypeID: 1, UnitPriceCents: 1000, Quantity: 2}}, &Discount{Type: DiscountFixed, Value: 500})
	if q.DiscountCents != 0 || q.TotalCents != 2000 {
		t.Fatalf("expected no discount, got %+v", q)
	}
}

func TestLargeAmountsDoNotOverflow(t *testing.T) {
	q := Price([]PriceLine{{TicketTypeID: 1, UnitPriceCents: 100_000_000, Quantity: 100, Eligible: true}},
		&Discount{Type: DiscountPercentage, Value: 3333})
	if q.TotalCents <= 0 || q.TotalCents+q.DiscountCents != q.SubtotalCents {
		t.Fatalf("overflow or inconsistency: %+v", q)
	}
}

func TestUnitPrices(t *testing.T) {
	cases := []struct {
		total int64
		qty   int
		want  []int64
	}{
		{1000, 3, []int64{334, 333, 333}},
		{900, 3, []int64{300, 300, 300}},
		{0, 2, []int64{0, 0}},
		{5, 4, []int64{2, 1, 1, 1}},
	}
	for _, c := range cases {
		got := UnitPrices(c.total, c.qty)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("UnitPrices(%d, %d) = %v, want %v", c.total, c.qty, got, c.want)
		}
		var sum int64
		for _, v := range got {
			sum += v
		}
		if sum != c.total {
			t.Errorf("UnitPrices(%d, %d) sums to %d", c.total, c.qty, sum)
		}
	}
}
