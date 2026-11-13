// Package utils holds small helpers any bench package may import: ids,
// hashing, templating and docker commands. It imports nothing from the bench
// except schemas.
package utils

import (
	"crypto/rand"
	"strings"
)

const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// NewID returns prefix + "_" + n random lowercase alphanumerics, e.g. "r_p20fj3s8wa".
// Ids are opaque: nothing parses them.
func NewID(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteByte('_')
	for _, c := range b {
		sb.WriteByte(idAlphabet[int(c)%len(idAlphabet)])
	}
	return sb.String()
}

// NewNonce returns a 24-character random token for probe receipts.
func NewNonce() string {
	return NewID("nonce", 24)
}
