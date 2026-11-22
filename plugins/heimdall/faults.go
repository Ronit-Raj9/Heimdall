package heimdall

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"sync"
	"time"
)

// Fault modes, ported from Heimdall's fault plan semantics (bench/faultproxy).
const (
	ModeNone      = "none"
	ModeError     = "error"      // answer Status (default 500) for every call in the window
	ModeRateLimit = "rate_limit" // answer 429 with Retry-After
	ModeTimeout   = "timeout"    // hold DelayMS (default 30s) then answer 504
	ModeLatency   = "latency"    // hold DelayMS, then let the call through
	ModeFlaky     = "flaky"      // fail with probability FailProb, seeded, so a rerun fails the same calls
	ModeOutage    = "outage"     // answer Status (default 503) for the window: a provider outage
)

// maxDelayMS caps every hold at ten minutes, as the fault proxy does.
const maxDelayMS = 600_000

// Plan is one fault plan. Window fields follow the proxy: FromCall is the
// first faulted call (1-based, default 1); Calls ends the window after that
// many calls; UntilS ends it that many seconds after its first faulted call.
// Model narrows the plan to one model ("" or "*" = all).
type Plan struct {
	Mode        string  `json:"mode"`
	Status      int     `json:"status,omitempty"`
	RetryAfterS int     `json:"retry_after_s,omitempty"`
	DelayMS     int     `json:"delay_ms,omitempty"`
	FailProb    float64 `json:"fail_prob,omitempty"`
	Seed        string  `json:"seed,omitempty"`
	FromCall    int     `json:"from_call,omitempty"`
	Calls       int     `json:"calls,omitempty"`
	UntilS      int     `json:"until_s,omitempty"`
	Model       string  `json:"model,omitempty"`
	Message     string  `json:"message,omitempty"`
}

// Validate rejects plans the plugin would apply wrongly.
func (p Plan) Validate() error {
	if p.FromCall < 0 || p.Calls < 0 || p.UntilS < 0 || p.DelayMS < 0 || p.RetryAfterS < 0 {
		return errors.New("from_call, calls, until_s, delay_ms and retry_after_s must be >= 0")
	}
	if p.DelayMS > maxDelayMS {
		return fmt.Errorf("delay_ms must be <= %d", maxDelayMS)
	}
	if p.Status != 0 && (p.Status < 400 || p.Status > 599) {
		return fmt.Errorf("status must be 4xx or 5xx, got %d", p.Status)
	}
	if p.FailProb != 0 && p.Mode != ModeFlaky {
		return fmt.Errorf("fail_prob needs mode flaky, got %q", p.Mode)
	}
	switch p.Mode {
	case "", ModeNone, ModeError, ModeRateLimit, ModeTimeout, ModeOutage:
	case ModeLatency:
		if p.DelayMS == 0 {
			return errors.New("latency plan needs delay_ms")
		}
	case ModeFlaky:
		if !(p.FailProb > 0 && p.FailProb <= 1) {
			return fmt.Errorf("flaky plan needs fail_prob in (0, 1], got %v", p.FailProb)
		}
	default:
		return fmt.Errorf("mode must be none, error, rate_limit, timeout, latency, flaky or outage, got %q", p.Mode)
	}
	return nil
}

// Decision is what the plugin does to one call.
type Decision struct {
	Mode        string        // "" when the call passes untouched
	Status      int           // error status to answer, 0 for none
	RetryAfterS int           // Retry-After seconds for a 429
	Delay       time.Duration // hold before answering or passing
	Pass        bool          // after Delay, let the call go upstream
	Call        int           // this call's 1-based number within the slot
	Message     string
}

// Faulted reports whether the decision changes the call at all.
func (d Decision) Faulted() bool { return d.Mode != "" }

type slot struct {
	calls     int
	firstFire time.Time
}

// tracker counts calls per slot (run id, else fault plan id) so call-count
// windows work across a run's many requests.
type tracker struct {
	mu    sync.Mutex
	slots map[string]*slot
	now   func() time.Time
}

func newTracker() *tracker {
	return &tracker{slots: map[string]*slot{}, now: time.Now}
}

// decide counts one call on the slot and returns what the plan does to it.
func (t *tracker) decide(key string, p Plan, model string) Decision {
	if p.Mode == "" || p.Mode == ModeNone {
		return Decision{}
	}
	if p.Model != "" && p.Model != "*" && p.Model != model {
		return Decision{}
	}
	t.mu.Lock()
	s := t.slots[key]
	if s == nil {
		s = &slot{}
		t.slots[key] = s
	}
	s.calls++
	n := s.calls
	from := p.FromCall
	if from < 1 {
		from = 1
	}
	if n < from || (p.Calls > 0 && n >= from+p.Calls) {
		t.mu.Unlock()
		return Decision{Call: n}
	}
	now := t.now()
	if s.firstFire.IsZero() {
		s.firstFire = now
	}
	if p.UntilS > 0 && now.Sub(s.firstFire) >= time.Duration(p.UntilS)*time.Second {
		t.mu.Unlock()
		return Decision{Call: n}
	}
	t.mu.Unlock()

	d := Decision{Mode: p.Mode, Call: n, Message: p.Message}
	switch p.Mode {
	case ModeError:
		d.Status = orDefault(p.Status, 500)
	case ModeOutage:
		d.Status = orDefault(p.Status, 503)
	case ModeRateLimit:
		d.Status = orDefault(p.Status, 429)
		d.RetryAfterS = orDefault(p.RetryAfterS, 1)
	case ModeTimeout:
		d.Status = orDefault(p.Status, 504)
		d.Delay = time.Duration(orDefault(p.DelayMS, 30_000)) * time.Millisecond
	case ModeLatency:
		d.Delay = time.Duration(p.DelayMS) * time.Millisecond
		d.Pass = true
	case ModeFlaky:
		seed := p.Seed
		if seed == "" {
			seed = key
		}
		if !flakyFails(seed, n, p.FailProb) {
			return Decision{Call: n}
		}
		d.Status = orDefault(p.Status, 503)
	}
	if d.Message == "" {
		d.Message = fmt.Sprintf("heimdall fault %s on call %d", d.Mode, n)
	}
	return d
}

// flakyFails is a deterministic coin: the same seed and call always agree.
func flakyFails(seed string, call int, prob float64) bool {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed + ":" + strconv.Itoa(call)))
	u := float64(h.Sum64()>>11) / float64(1<<53)
	return u < prob
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
