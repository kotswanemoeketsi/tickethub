// Package tickets generates ticket codes, signed QR payloads and printable PDF tickets.
package tickets

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/random"
)

// QR payloads have the form "GT1.<ticket code>.<signature>". The signature is an HMAC over
// the ticket code and its QR version, so codes cannot be forged or guessed, nothing secret
// is stored in the database, and a ticket's QR can be revoked (by bumping its version)
// when the ticket is transferred to a different attendee.
const qrPrefix = "GT1"

// ErrInvalidQR is returned for payloads that are malformed or carry a bad signature.
var ErrInvalidQR = errors.New("invalid ticket QR code")

// Signer creates and checks QR payloads.
type Signer struct {
	key []byte
}

// NewSigner returns a signer using key.
func NewSigner(key string) *Signer { return &Signer{key: []byte(key)} }

func (s *Signer) signature(code string, version int) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(qrPrefix + "|" + code + "|" + strconv.Itoa(version)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

// Payload returns the QR content for a ticket.
func (s *Signer) Payload(code string, version int) string {
	return qrPrefix + "." + code + "." + s.signature(code, version)
}

// Parse extracts the ticket code and signature from a scanned payload. A bare ticket code
// (typed in by hand when a QR code is damaged) is also accepted and returns an empty signature.
func Parse(scanned string) (code, signature string, err error) {
	scanned = strings.TrimSpace(scanned)
	if !strings.HasPrefix(scanned, qrPrefix+".") {
		code = NormalizeCode(scanned)
		if !validCode(code) {
			return "", "", ErrInvalidQR
		}
		return code, "", nil
	}
	parts := strings.Split(scanned, ".")
	if len(parts) != 3 || !validCode(parts[1]) || parts[2] == "" {
		return "", "", ErrInvalidQR
	}
	return parts[1], parts[2], nil
}

// validCode reports whether s has the length and alphabet of a ticket code.
func validCode(s string) bool {
	if len(s) != codeLength {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(random.Alphabet, r) {
			return false
		}
	}
	return true
}

// Verify reports whether signature is valid for code at version.
func (s *Signer) Verify(code string, version int, signature string) bool {
	return hmac.Equal([]byte(signature), []byte(s.signature(code, version)))
}

const codeLength = 10

// NewCode returns a random ticket code such as "7K3M9QX2AB" (about 50 bits of entropy).
func NewCode() string { return random.Code(codeLength) }

// NewOrderReference returns a short order reference such as "GT7K3M9QX2", suitable as an
// EFT payment reference.
func NewOrderReference() string { return "GT" + random.Code(8) }

// NewRefundReference returns a refund reference.
func NewRefundReference() string { return "RF" + random.Code(10) }

// NewPaymentReference returns a unique gateway transaction reference.
func NewPaymentReference() string { return "PAY" + random.Code(14) }

// NormalizeCode upper-cases a typed ticket code and strips separators.
func NormalizeCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	return strings.NewReplacer("-", "", " ", "").Replace(code)
}

// FormatCode groups a ticket code for display: "7K3M9-QX2AB".
func FormatCode(code string) string {
	if len(code) != codeLength {
		return code
	}
	return code[:5] + "-" + code[5:]
}
