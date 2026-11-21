// Package validate provides a small, explicit field validator.
//
// Usage:
//
//	v := validate.New()
//	v.Required("email", in.Email)
//	v.Email("email", in.Email)
//	if err := v.Err(); err != nil { return err }
package validate

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kotswanemoeketsi/gotaticket/internal/platform/apperr"
)

// Errors collects the first error message reported for each field.
type Errors map[string]string

// New returns an empty error collection.
func New() Errors { return Errors{} }

// Add records msg for field unless the field already has an error.
func (e Errors) Add(field, msg string) {
	if _, exists := e[field]; !exists {
		e[field] = msg
	}
}

// Check records msg for field when ok is false.
func (e Errors) Check(ok bool, field, msg string) {
	if !ok {
		e.Add(field, msg)
	}
}

// Has reports whether field already has an error.
func (e Errors) Has(field string) bool {
	_, ok := e[field]
	return ok
}

func (e Errors) Required(field, value string) {
	e.Check(strings.TrimSpace(value) != "", field, "is required")
}

func (e Errors) MaxLen(field, value string, max int) {
	e.Check(utf8.RuneCountInString(value) <= max, field, fmt.Sprintf("must be at most %d characters", max))
}

// Email validates a bare address such as "jane@example.com" (display names are rejected).
func (e Errors) Email(field, value string) {
	if value == "" {
		return
	}
	addr, err := mail.ParseAddress(value)
	e.Check(err == nil && addr.Address == value && strings.Contains(value, "."), field, "must be a valid email address")
}

var phonePattern = regexp.MustCompile(`^\+?[0-9 ()-]{7,20}$`)

// Phone validates an optional phone number.
func (e Errors) Phone(field, value string) {
	if value == "" {
		return
	}
	e.Check(phonePattern.MatchString(value), field, "must be a valid phone number")
}

// Password enforces the platform password policy.
func (e Errors) Password(field, value string) {
	if utf8.RuneCountInString(value) < 8 {
		e.Add(field, "must be at least 8 characters")
		return
	}
	if len(value) > 128 {
		e.Add(field, "must be at most 128 characters")
		return
	}
	var hasLetter, hasDigit bool
	for _, r := range value {
		switch {
		case unicode.IsLetter(r):
			hasLetter = true
		case unicode.IsDigit(r):
			hasDigit = true
		}
	}
	e.Check(hasLetter && hasDigit, field, "must contain at least one letter and one number")
}

// OneOf validates that value is one of the allowed options.
func (e Errors) OneOf(field, value string, allowed ...string) {
	for _, a := range allowed {
		if value == a {
			return
		}
	}
	e.Add(field, "must be one of: "+strings.Join(allowed, ", "))
}

// Range validates that an integer lies within [min, max].
func (e Errors) Range(field string, value, min, max int64) {
	e.Check(value >= min && value <= max, field, fmt.Sprintf("must be between %d and %d", min, max))
}

// Err returns a validation error when any field failed, otherwise nil.
func (e Errors) Err() error {
	if len(e) == 0 {
		return nil
	}
	return apperr.Validation(e)
}

// NormalizeEmail lower-cases and trims an email address.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ParseLocalDateTime parses a wall-clock time such as "2026-12-05T18:30" in the given IANA
// time zone and returns the instant in UTC.
func ParseLocalDateTime(value, timezone string) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("unknown time zone %q", timezone)
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date/time %q", value)
}
