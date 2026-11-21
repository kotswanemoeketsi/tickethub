package tickets

import (
	"strings"
	"testing"
)

func TestQRPayloadRoundTrip(t *testing.T) {
	s := NewSigner("test-key-that-is-long-enough-for-hmac")
	code := NewCode()
	payload := s.Payload(code, 1)

	gotCode, sig, err := Parse(payload)
	if err != nil {
		t.Fatal(err)
	}
	if gotCode != code || !s.Verify(code, 1, sig) {
		t.Fatalf("payload %q did not verify", payload)
	}
}

func TestQRPayloadRejectsTamperingAndOtherKeys(t *testing.T) {
	s := NewSigner("key-one-key-one-key-one-key-one")
	other := NewSigner("key-two-key-two-key-two-key-two")
	code := NewCode()
	_, sig, _ := Parse(s.Payload(code, 1))

	if s.Verify(code, 2, sig) {
		t.Error("signature must be bound to the QR version")
	}
	if other.Verify(code, 1, sig) {
		t.Error("signature must be bound to the signing key")
	}
	if s.Verify(NewCode(), 1, sig) {
		t.Error("signature must be bound to the ticket code")
	}
}

func TestParseAcceptsTypedCodes(t *testing.T) {
	code, sig, err := Parse(" ab12c-de34f ")
	if err != nil || code != "AB12CDE34F" || sig != "" {
		t.Fatalf("got %q %q %v", code, sig, err)
	}
	for _, bad := range []string{"", "GT1.", "GT1.SHORT.sig", "not a ticket", strings.Repeat("A", 11), "GT1.AB12CDE34F."} {
		if _, _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestFormatCode(t *testing.T) {
	if got := FormatCode("AB12CDE34F"); got != "AB12C-DE34F" {
		t.Fatalf("got %q", got)
	}
}
