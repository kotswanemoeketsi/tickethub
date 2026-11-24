package sales

import "testing"

func TestTicketFees(t *testing.T) {
	for _, tt := range []struct {
		name              string
		price             int64
		bps               int
		comp              bool
		commission, fixed int64
	}{
		{"R200 ticket", 20000, 350, false, 700, 500},
		{"round half cent up", 100, 350, false, 4, 500},
		{"discounted price", 15000, 350, false, 525, 500},
		{"free", 0, 350, false, 0, 0},
		{"complimentary", 20000, 350, true, 0, 0},
		{"negotiated rate", 20000, 200, false, 400, 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			commission, fixed := ticketFees(tt.price, tt.bps, tt.comp)
			if commission != tt.commission || fixed != tt.fixed {
				t.Fatalf("got %d + %d, want %d + %d", commission, fixed, tt.commission, tt.fixed)
			}
		})
	}
}
