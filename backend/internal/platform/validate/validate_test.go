package validate

import (
	"testing"
	"time"
)

func TestEmailAndPassword(t *testing.T) {
	for _, ok := range []string{"jane@example.com", "j.doe+tickets@mail.co.za"} {
		v := New()
		v.Email("email", ok)
		if v.Err() != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"jane", "Jane <jane@example.com>", "jane@localhost", "@example.com"} {
		v := New()
		v.Email("email", bad)
		if v.Err() == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	for pw, valid := range map[string]bool{"short1": false, "allletters": false, "12345678": false, "Secur3pass": true} {
		v := New()
		v.Password("password", pw)
		if (v.Err() == nil) != valid {
			t.Errorf("password %q: valid=%v", pw, v.Err() == nil)
		}
	}
}

func TestParseLocalDateTime(t *testing.T) {
	got, err := ParseLocalDateTime("2026-12-05T18:30", "Africa/Johannesburg")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 12, 5, 16, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Daylight saving is handled by the zone database: 09:00 in London is 08:00 UTC in July.
	summer, _ := ParseLocalDateTime("2026-07-01T09:00", "Europe/London")
	if summer.Hour() != 8 {
		t.Fatalf("expected 08:00 UTC, got %v", summer)
	}
	if _, err := ParseLocalDateTime("tomorrow", "Africa/Johannesburg"); err == nil {
		t.Fatal("invalid dates must be rejected")
	}
	if _, err := ParseLocalDateTime("2026-12-05T18:30", "Mars/Olympus"); err == nil {
		t.Fatal("unknown zones must be rejected")
	}
}
