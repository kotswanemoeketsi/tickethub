package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Passwords are hashed with PBKDF2-HMAC-SHA256 from the Go standard library, using the
// OWASP-recommended 600,000 iterations by default. The encoded form records its parameters
// ("pbkdf2-sha256$<iterations>$<salt>$<hash>") so the work factor can be raised later and
// existing hashes upgraded transparently at the next successful login.
const (
	hashScheme = "pbkdf2-sha256"
	saltBytes  = 16
	keyBytes   = 32
)

var errMalformedHash = errors.New("malformed password hash")

// PasswordHasher hashes and verifies passwords.
type PasswordHasher struct {
	Iterations int
}

// Hash returns the encoded hash of password.
func (h PasswordHasher) Hash(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, h.Iterations, keyBytes)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, h.Iterations, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// Verify reports whether password matches encoded, and whether the hash should be
// regenerated because it was made with weaker parameters than the current ones.
func (h PasswordHasher) Verify(password, encoded string) (match, needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false, false, errMalformedHash
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false, false, errMalformedHash
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[2])
	if err != nil {
		return false, false, errMalformedHash
	}
	want, err := enc.DecodeString(parts[3])
	if err != nil {
		return false, false, errMalformedHash
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(want))
	if err != nil {
		return false, false, err
	}
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	return true, iterations < h.Iterations, nil
}
