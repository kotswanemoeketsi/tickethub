package ai

import (
	"testing"
	"time"
)

func TestResolveWhen(t *testing.T) {
	loc, _ := time.LoadLocation("Africa/Johannesburg")
	wed := time.Date(2026, 11, 18, 15, 0, 0, 0, loc) // a Wednesday
	sat := time.Date(2026, 11, 21, 9, 0, 0, 0, loc)  // a Saturday
	cases := []struct {
		name             string
		now              time.Time
		when, from, to   string
		wantPreset       string
		wantFrom, wantTo string
	}{
		{"weekend from midweek", wed, "this_weekend", "", "", "weekend", "2026-11-20", "2026-11-22"},
		{"weekend on Saturday", sat, "this_weekend", "", "", "weekend", "2026-11-21", "2026-11-22"},
		{"next weekend", wed, "next_weekend", "", "", "custom", "2026-11-27", "2026-11-29"},
		{"tomorrow", wed, "tomorrow", "", "", "custom", "2026-11-19", "2026-11-19"},
		{"next 7 days", wed, "next_7_days", "", "", "week", "2026-11-18", "2026-11-24"},
		{"next month", wed, "next_month", "", "", "custom", "2026-12-01", "2026-12-31"},
		{"explicit dates", wed, "dates", "2026-12-12", "2026-12-12", "custom", "2026-12-12", "2026-12-12"},
		{"past dates are dropped", wed, "dates", "2025-01-01", "2025-01-02", "", "", ""},
		{"a range starting earlier starts today", wed, "dates", "2026-11-01", "2026-11-30", "custom", "2026-11-18", "2026-11-30"},
		{"nonsense", wed, "someday", "", "", "", "", ""},
	}
	for _, c := range cases {
		preset, from, to := resolveWhen(c.when, c.from, c.to, c.now)
		if preset != c.wantPreset || from != c.wantFrom || to != c.wantTo {
			t.Errorf("%s: got %q %s..%s, want %q %s..%s", c.name, preset, from, to, c.wantPreset, c.wantFrom, c.wantTo)
		}
	}
}

func TestReviewClean(t *testing.T) {
	r := EventReview{Verdict: "ready", Issues: []ReviewIssue{
		{Severity: "low", Area: "details", Message: "Add an image."},
		{Severity: "high", Area: "tickets", Message: "VIP costs less than General."},
		{Severity: "urgent", Area: "details", Message: "Unknown severity."},
	}}.clean()
	if r.Verdict != VerdictNeedsChanges || len(r.Issues) != 2 || r.Issues[0].Severity != "high" {
		t.Fatalf("clean: %+v", r)
	}
	if r := (EventReview{Verdict: "approve!"}).clean(); r.Verdict != VerdictNeedsChanges {
		t.Fatalf("unknown verdicts become needs_changes, got %q", r.Verdict)
	}
}
