package agentevents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Collector endpoints. The collector is served beside the tool proxy, so an
// agent reaches it at http://toolproxy:9200 plus these paths.
const (
	PathEvents = "/events"
	PathOTLP   = "/otlp/v1/traces"
	// LedgerName is the collector's ledger next to the tool ledger; the
	// environment kernel copies it into the run folder.
	LedgerName = "agent-events.jsonl"
	// CounterName holds the collector's counters (CollectorCounts).
	CounterName = "agent-events.counts.json"
)

// CollectorCounts are the collector's counters, rewritten after every
// request. Received counts every event or span seen; Dropped those refused
// (bad lines, over a cap, a body too large counts one).
type CollectorCounts struct {
	Received int `json:"received"`
	Kept     int `json:"kept"`
	Dropped  int `json:"dropped"`
}

// Collector receives reported events for one run and appends them, capped
// and redacted, to its ledger. It never routes anything out, never answers
// an agent with more than a count, and a full ledger only drops events.
type Collector struct {
	mu      sync.Mutex
	ledger  string
	counts  string
	lim     Limits
	f       *os.File
	bytes   int64
	c       CollectorCounts
	now     func() time.Time
	failure error
}

// NewCollector opens (creates or truncates) the ledger at ledgerPath and the
// counters file beside it.
func NewCollector(ledgerPath string, lim Limits) (*Collector, error) {
	f, err := os.OpenFile(ledgerPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open agent event ledger: %w", err)
	}
	c := &Collector{ledger: ledgerPath, counts: filepath.Join(filepath.Dir(ledgerPath), CounterName), lim: lim.WithDefaults(), f: f, now: time.Now}
	c.writeCounts()
	return c, nil
}

// Close closes the ledger.
func (c *Collector) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		return nil
	}
	err := c.f.Close()
	c.f = nil
	return err
}

// Counts returns the current counters.
func (c *Collector) Counts() CollectorCounts {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.c
}

// Handles reports whether path is a collector endpoint.
func Handles(path string) bool {
	return path == PathEvents || path == PathOTLP
}

// ServeHTTP implements http.Handler for POST /events (native events: JSON
// lines, a JSON array or one object) and POST /otlp/v1/traces (OTLP/HTTP,
// protobuf or JSON). It answers 202 with the counts of this request, 413
// when the body is over the cap, 415 for a body it cannot read.
func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, c.lim.MaxBody+1))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > c.lim.MaxBody {
		c.drop(1)
		http.Error(w, fmt.Sprintf("body over %d bytes; dropped", c.lim.MaxBody), http.StatusRequestEntityTooLarge)
		return
	}
	now := c.now()
	var recs []Record
	var bad int
	switch r.URL.Path {
	case PathOTLP:
		recs, bad, err = ParseOTLP(body, r.Header.Get("Content-Type"), c.lim, now)
		if err != nil {
			c.drop(1)
			http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
			return
		}
	default:
		recs, bad = ParseNative(body, schemas.ReportChannelNative, c.lim, now)
	}
	kept, dropped := c.append(recs)
	c.drop(bad)
	if r.URL.Path == PathOTLP {
		// An OTLP exporter expects an ExportTraceServiceResponse; an empty
		// one in the request's encoding means full success.
		ct := r.Header.Get("Content-Type")
		if strings.Contains(ct, "protobuf") {
			w.Header().Set("Content-Type", ContentTypeOTLPProto)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", ContentTypeOTLPJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]int{"accepted": kept, "dropped": dropped + bad})
}

// append writes records to the ledger within the caps and returns how many
// were kept and dropped.
func (c *Collector) append(recs []Record) (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept, dropped := 0, 0
	for _, r := range recs {
		c.c.Received++
		line, err := json.Marshal(r)
		if err != nil || c.f == nil || c.c.Kept >= c.lim.MaxEvents || c.bytes+int64(len(line))+1 > c.lim.MaxBytes {
			c.c.Dropped++
			dropped++
			continue
		}
		if _, err := c.f.Write(append(line, '\n')); err != nil {
			c.failure = err
			c.c.Dropped++
			dropped++
			continue
		}
		c.bytes += int64(len(line)) + 1
		c.c.Kept++
		kept++
	}
	c.writeCountsLocked()
	return kept, dropped
}

func (c *Collector) drop(n int) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.c.Received += n
	c.c.Dropped += n
	c.writeCountsLocked()
}

func (c *Collector) writeCounts() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeCountsLocked()
}

// writeCountsLocked replaces the counters file atomically. A failure is
// remembered, never returned to the agent.
func (c *Collector) writeCountsLocked() {
	raw, _ := json.Marshal(c.c)
	tmp := c.counts + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		c.failure = err
		return
	}
	if err := os.Rename(tmp, c.counts); err != nil {
		c.failure = err
	}
}

// Healthy reports the last write failure, if any.
func (c *Collector) Healthy() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		return errors.New("agent event ledger closed")
	}
	return c.failure
}

// ReadCounts parses a counters file; empty input is zero counts.
func ReadCounts(raw []byte) (CollectorCounts, error) {
	var c CollectorCounts
	if len(raw) == 0 {
		return c, nil
	}
	err := json.Unmarshal(raw, &c)
	return c, err
}
