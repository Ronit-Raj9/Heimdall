// Package drain is the server's drain mode (INFRA_I4 item 3a): while it is
// on, no coordinator hands out a new lease, so the runs already out on
// workers finish and nothing new starts. An upgrade turns it on, waits until
// no lease is in flight (or a timeout passes), and then restarts the server.
// The state lives in the process only: a restarted server always starts
// with drain mode off.
//
// The drain routes (handlers/drain.go) accept a server admin or the drain
// token, a random value the server writes at start to a file only the
// server's own user can read (TokenPath). An operator command running in the
// same container (bifrost-bench drain) reads that file, so upgrade.sh needs
// no admin session.
package drain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TokenHeader carries the drain token.
const TokenHeader = "X-Heimdall-Drain-Token"

// TokenFile is the name of the drain token file under the runs root.
const TokenFile = ".drain.token"

// Status is the drain state as the routes and the CLI see it.
type Status struct {
	// Draining is true while no new lease is handed out.
	Draining bool `json:"draining"`
	// Since is when drain mode was turned on; empty while it is off.
	Since *time.Time `json:"since,omitempty"`
	// By names who turned it on (an admin's email, or "drain token").
	By string `json:"by,omitempty"`
	// InFlight counts the leases still out on workers.
	InFlight int `json:"in_flight"`
	// Drained is true when drain mode is on and no lease is in flight: the
	// server can be restarted without stopping a run.
	Drained bool `json:"drained"`
}

var (
	on    atomic.Bool
	mu    sync.Mutex
	since time.Time
	by    string

	srcMu   sync.Mutex
	sources = map[int]func() int{}
	nextSrc int

	tokenSum atomic.Pointer[[sha256.Size]byte]
)

// Active reports whether drain mode is on. Coordinators check it before
// every new lease.
func Active() bool { return on.Load() }

// Start turns drain mode on (again: the first start time is kept) and
// returns the status.
func Start(who string) Status {
	mu.Lock()
	if !on.Load() {
		since, by = time.Now().UTC(), who
		on.Store(true)
	}
	mu.Unlock()
	return Get()
}

// Stop turns drain mode off: leases are handed out again.
func Stop() Status {
	mu.Lock()
	on.Store(false)
	since, by = time.Time{}, ""
	mu.Unlock()
	return Get()
}

// Get returns the current status.
func Get() Status {
	mu.Lock()
	st := Status{Draining: on.Load(), By: by}
	if st.Draining {
		t := since
		st.Since = &t
	}
	mu.Unlock()
	st.InFlight = InFlight()
	st.Drained = st.Draining && st.InFlight == 0
	return st
}

// AddSource registers a count of leases in flight (one per coordinator
// family); the returned function removes it.
func AddSource(f func() int) (remove func()) {
	srcMu.Lock()
	defer srcMu.Unlock()
	id := nextSrc
	nextSrc++
	sources[id] = f
	return func() {
		srcMu.Lock()
		delete(sources, id)
		srcMu.Unlock()
	}
}

// InFlight sums every registered source.
func InFlight() int {
	srcMu.Lock()
	fs := make([]func() int, 0, len(sources))
	for _, f := range sources {
		fs = append(fs, f)
	}
	srcMu.Unlock()
	n := 0
	for _, f := range fs {
		n += f()
	}
	return n
}

// TokenPath is the drain token file of a runs root.
func TokenPath(runsRoot string) string { return filepath.Join(runsRoot, TokenFile) }

// WriteToken makes a new random drain token, writes it to path (mode 0600,
// replaced atomically) and keeps its hash for CheckToken.
func WriteToken(path string) error {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return fmt.Errorf("drain token: %w", err)
	}
	tok := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("drain token: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), TokenFile+".*")
	if err != nil {
		return fmt.Errorf("drain token: %w", err)
	}
	_, werr := tmp.WriteString(tok + "\n")
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("drain token: %w", werr)
	}
	SetToken(tok)
	return nil
}

// SetToken keeps tok's hash as the drain token (tests, WriteToken).
func SetToken(tok string) {
	if tok == "" {
		tokenSum.Store(nil)
		return
	}
	sum := sha256.Sum256([]byte(tok))
	tokenSum.Store(&sum)
}

// CheckToken reports whether tok is the drain token, in constant time.
// With no token written, nothing matches.
func CheckToken(tok string) bool {
	want := tokenSum.Load()
	if want == nil || tok == "" {
		return false
	}
	sum := sha256.Sum256([]byte(tok))
	return subtle.ConstantTimeCompare(sum[:], want[:]) == 1
}

// ReadToken reads a drain token file.
func ReadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read drain token: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", errors.New("read drain token: the file is empty")
	}
	return tok, nil
}
