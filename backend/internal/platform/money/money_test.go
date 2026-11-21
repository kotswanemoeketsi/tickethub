package money

import "testing"

func TestFormat(t *testing.T) {
	cases := []struct {
		cents    int64
		currency string
		want     string
	}{
		{0, "ZAR", "R 0.00"},
		{5, "ZAR", "R 0.05"},
		{125000, "ZAR", "R 1,250.00"},
		{123456789, "zar", "R 1,234,567.89"},
		{-9950, "ZAR", "-R 99.50"},
		{100, "KES", "KES 1.00"},
	}
	for _, c := range cases {
		if got := Format(c.cents, c.currency); got != c.want {
			t.Errorf("Format(%d, %s) = %q, want %q", c.cents, c.currency, got, c.want)
		}
	}
}
