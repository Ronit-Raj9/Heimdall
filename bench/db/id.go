package db

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
	"sync"
	"time"
)

// crockford is the lowercase Crockford base32 alphabet: sortable, no i l o u.
const crockford = "0123456789abcdefghjkmnpqrstvwxyz"

// idState is the last id this process made, so two ids of the same
// millisecond still sort in the order they were made.
var idState struct {
	sync.Mutex
	ms   uint64
	rand [10]byte
}

// NewID returns prefix + "_" + a 26-character id: 48 bits of unix
// milliseconds then 80 random bits, Crockford base32 (ULID layout, lowercase).
// Ids sort by creation time to the millisecond, and within one process also
// inside a millisecond: a second id of the same millisecond is the previous
// random part plus one (the monotonic rule of the ULID spec). "Newest by id"
// queries depend on that (jobs.Store.Latest after a cancel and a resume in
// the same millisecond). NewID("org") and NewID("org_") both give "org_01j...".
func NewID(prefix string) string {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	idState.Lock()
	if ms <= idState.ms {
		// Same millisecond, or the clock stepped back: keep the last
		// timestamp and count the random part up.
		ms = idState.ms
		for i := len(idState.rand) - 1; i >= 0; i-- {
			idState.rand[i]++
			if idState.rand[i] != 0 {
				break
			}
		}
	} else {
		if _, err := rand.Read(idState.rand[:]); err != nil {
			idState.Unlock()
			panic("db: crypto/rand: " + err.Error())
		}
		idState.ms = ms
	}
	binary.BigEndian.PutUint64(b[:8], ms<<16)
	copy(b[6:], idState.rand[:])
	idState.Unlock()
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
