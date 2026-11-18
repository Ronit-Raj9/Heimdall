package traceinbox

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// MaxPushedSessions caps the sessions one push may carry.
const MaxPushedSessions = 20

// IngestRequest is one pushed trace (POST /api/bench/inbox/ingest, `inbox
// ingest`). Trace is the trace JSON in any format traceimport reads.
type IngestRequest struct {
	Trace   json.RawMessage `json:"trace"`
	Format  string          `json:"format,omitempty"`   // otlp, langfuse or chat; empty is detected
	Reason  string          `json:"reason,omitempty"`   // why the pushing tool flagged it
	Link    string          `json:"link,omitempty"`     // back to the trace in the pushing tool
	Source  string          `json:"source,omitempty"`   // the pushing tool's own name, shown as a reason
	TraceID string          `json:"trace_id,omitempty"` // the session to take when the trace holds several
}

// IngestResult is what a push did.
type IngestResult struct {
	ItemID      string   `json:"item_id"`
	Created     bool     `json:"created"`
	Occurrences int      `json:"occurrences"`
	Items       []string `json:"-"` // every item touched, when the push held several sessions
}

// Ingest takes one pushed trace through the same path as a pull: parse,
// redact, signature, dedupe. Every session of the trace is taken (at most
// MaxPushedSessions) unless TraceID picks one; a push is flagged by being
// pushed, with its reason.
func (ib *Inbox) Ingest(ctx context.Context, src *Source, req IngestRequest) (*IngestResult, error) {
	if src == nil {
		return nil, errors.New("no webhook source")
	}
	if len(req.Trace) == 0 || string(req.Trace) == "null" {
		return nil, errors.New("trace is empty")
	}
	if req.Format != "" && !contains(traceimport.Formats(), req.Format) {
		return nil, fmt.Errorf("format %q: want one of %s", req.Format, strings.Join(traceimport.Formats(), ", "))
	}
	tr, err := traceimport.Parse(req.Trace, req.Format)
	if err != nil {
		return nil, err
	}
	sessions := tr.Sessions
	if req.TraceID != "" {
		s, err := tr.Pick(req.TraceID)
		if err != nil {
			return nil, err
		}
		sessions = []*traceimport.Session{s}
	}
	if len(sessions) > MaxPushedSessions {
		return nil, fmt.Errorf("the trace holds %d sessions; push at most %d at a time, or name one with trace_id", len(sessions), MaxPushedSessions)
	}
	reasons := []string{}
	if r := oneLine(req.Reason, 240); r != "" {
		reasons = append(reasons, r)
	}
	if req.Source != "" {
		reasons = append(reasons, "pushed by "+oneLine(req.Source, 60))
	}
	extra := map[string]string{}
	if u, err := url.Parse(req.Link); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		extra[linkOverride] = req.Link
	}
	res := &IngestResult{}
	now := ib.now()
	for _, s := range sessions {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		rs := append([]string(nil), reasons...)
		if len(rs) == 0 {
			rs = append(rs, "pushed to the inbox; outcome "+s.Summary())
		}
		out, err := ib.take(src, Flagged{Session: s, TraceID: s.ID, At: now, Reasons: rs, Kinds: []string{FlagPushed}, Extra: extra})
		if err != nil {
			return nil, err
		}
		if res.ItemID == "" {
			res.ItemID, res.Created = out.ItemID, out.Created
		}
		res.Items = append(res.Items, out.ItemID)
	}
	items, err := ib.Store.Items()
	if err == nil {
		for _, it := range items {
			if it.ID == res.ItemID {
				res.Occurrences = it.Occurrences
			}
		}
	}
	_ = ib.Store.Log(LogEvent{At: now, Event: "ingest", Item: res.ItemID, Source: src.Name})
	return res, nil
}

// linkOverride is the Extra key of a link the pusher sent.
const linkOverride = "_link"

// Gate guards the webhook: a token compared by hash in constant time, a
// body size cap and a token bucket over pushes per minute.
type Gate struct {
	hash     [32]byte
	enabled  bool
	MaxBytes int64
	rate     float64 // pushes per second
	burst    float64
	mu       sync.Mutex
	tokens   float64
	last     time.Time
}

// NewGate returns a gate for token. An empty token gives a gate that
// refuses everything.
func NewGate(token string, perMinute int, maxBytes int64) *Gate {
	if perMinute <= 0 {
		perMinute = DefaultRate
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBody
	}
	g := &Gate{MaxBytes: maxBytes, rate: float64(perMinute) / 60, burst: float64(perMinute)}
	g.tokens = g.burst
	if token != "" {
		g.hash = sha256.Sum256([]byte(token))
		g.enabled = true
	}
	return g
}

// Enabled reports whether a token is set.
func (g *Gate) Enabled() bool { return g != nil && g.enabled }

// Check compares a presented token with the gate's by their sha256, in
// constant time. It accepts "Bearer <token>" or the bare token.
func (g *Gate) Check(presented string) bool {
	if !g.Enabled() {
		return false
	}
	presented = strings.TrimSpace(presented)
	if p, ok := strings.CutPrefix(presented, "Bearer "); ok {
		presented = strings.TrimSpace(p)
	}
	if presented == "" {
		return false
	}
	h := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(h[:], g.hash[:]) == 1
}

// Allow takes one push from the bucket at now.
func (g *Gate) Allow(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.last.IsZero() {
		g.tokens = min(g.burst, g.tokens+now.Sub(g.last).Seconds()*g.rate)
	}
	g.last = now
	if g.tokens < 1 {
		return false
	}
	g.tokens--
	return true
}
