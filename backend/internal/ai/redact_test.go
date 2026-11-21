package ai

import "testing"

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"Call Thabo on 082 555 1234 for VIP tables":           "Call Thabo on [number removed] for VIP tables",
		"WhatsApp +27 (82) 555-1234 or email info@jozi.co.za": "WhatsApp [number removed] or email [email removed]",
		"Doors 18:00, tickets R250, 2026 edition, 5000 fans":  "Doors 18:00, tickets R250, 2026 edition, 5000 fans",
		"Bank account 62000000000, branch 250655":             "Bank account [number removed], branch 250655",
		"ID 8001015009087": "ID [number removed]",
	}
	for in, want := range cases {
		if got := redact(in); got != want {
			t.Errorf("redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("  Ndiyabulela kakhulu  ", 10); got != "Ndiyabulel" {
		t.Fatalf("clip = %q", got)
	}
}
