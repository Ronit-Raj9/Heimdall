package db

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
	"time"
)

// crockford is the lowercase Crockford base32 alphabet: sortable, no i l o u.
const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// NewID returns prefix + "_" + a 26-character id: 48 bits of unix
// milliseconds then 80 random bits, Crockford base32 (ULID layout, lowercase).
// Ids sort by creation time to the millisecond. NewID("org") and
// NewID("org_") both give "org_01j...".
func NewID(prefix string) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixMilli())<<16)
	if _, err := rand.Read(b[6:]); err != nil {
		panic("db: crypto/rand: " + err.Error())
	}
	prefix = strings.TrimSuffix(prefix, "_")
	var out strings.Builder
	out.Grow(len(prefix) + 27)
	if prefix != "" {
		out.WriteString(prefix)
		out.WriteByte('_')
	}
	out.WriteString(encode128(b))
	return out.String()
}

// encode128 writes 128 bits as 26 base32 digits, most significant first
// (the top digit carries 3 bits).
func encode128(b [16]byte) string {
	hi := binary.BigEndian.Uint64(b[:8])
	lo := binary.BigEndian.Uint64(b[8:])
	var d [26]byte
	for i := 25; i >= 0; i-- {
		d[i] = crockford[lo&31]
		lo = lo>>5 | hi<<59
		hi >>= 5
	}
	return string(d[:])
}
