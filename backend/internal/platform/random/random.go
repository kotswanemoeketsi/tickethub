// Package random generates cryptographically secure identifiers and tokens.
package random

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"
)

// Alphabet is Crockford's base32 alphabet used for codes: no I, L, O or U, so codes are easy
// to read aloud and type.
const Alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Code returns a random human-friendly code of length n.
func Code(n int) string {
	out := make([]byte, n)
	max := big.NewInt(int64(len(Alphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("random: crypto/rand unavailable: " + err.Error())
		}
		out[i] = Alphabet[idx.Int64()]
	}
	return string(out)
}

// Token returns a URL-safe random token carrying the given number of random bytes.
func Token(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic("random: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// Hex returns a random hex string carrying the given number of random bytes.
func Hex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic("random: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// HashToken returns the hex SHA-256 of a secret token, for storage in place of the token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
