package heimdall

// Usage export (lane C03). The plugin records one usage row per model call
// that passed through Bifrost (run id, provider, model, tokens, cost,
// latency, cache hit, fallback used, fault applied) and posts them in
// batches to Heimdall's ingest route (POST /api/bench/gateway/usage), with a
// shared secret as a bearer token. Export is best effort: a slow or down
// Heimdall never blocks or fails a model call. Rows past the queue limit are
// dropped and counted.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

// UsageIngestPath is the Heimdall route the exporter posts to.
const UsageIngestPath = "/api/bench/gateway/usage"

// Exporter defaults.
const (
	DefaultUsageBatchSize     = 100
	DefaultUsageFlushInterval = 2 * time.Second
	DefaultUsageTimeout       = 5 * time.Second
	DefaultUsageQueueLimit    = 10000
)

// UsageRecord is one model call as Heimdall stores it.
type UsageRecord struct {
	RunID        string    `json:"run_id"`
	EvaluationID string    `json:"evaluation_id,omitempty"`
	OrgID        string    `json:"org_id,omitempty"`
	Arm          string    `json:"arm,omitempty"`
	RequestID    string    `json:"request_id,omitempty"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	TotalTokens  int       `json:"total_tokens"`
	CostUSD      float64   `json:"cost_usd"`
	LatencyMS    int64     `json:"latency_ms"`
	CacheHit     bool      `json:"cache_hit"`
	FallbackUsed bool      `json:"fallback_used"`
	FallbackFrom string    `json:"fallback_from,omitempty"`
	FaultApplied string    `json:"fault_applied,omitempty"`
	FaultPlanID  string    `json:"fault_plan_id,omitempty"`
	StatusCode   int       `json:"status_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	StartedAt    time.Time `json:"started_at"`
}

// UsageBatch is the body of one POST to Heimdall.
type UsageBatch struct {
	Records []UsageRecord `json:"records"`
}

// UsageConfig configures the exporter. URL empty disables export.
type UsageConfig struct {
	URL           string        `json:"url"`         // Heimdall base URL, for example https://heimdall.example
	Secret        string        `json:"secret"`      // shared secret, sent as Authorization: Bearer
	BatchSize     int           `json:"batch_size"`  // rows per POST
	FlushInterval time.Duration `json:"-"`           // max wait before a partial batch is sent
	Timeout       time.Duration `json:"-"`           // per POST
	FlushMS       int           `json:"flush_ms"`    // FlushInterval in the JSON config
	TimeoutMS     int           `json:"timeout_ms"`  // Timeout in the JSON config
	QueueLimit    int           `json:"queue_limit"` // rows held before new ones are dropped
}

// UsageStats counts what the exporter did, for the plugin's status.
type UsageStats struct {
	Sent    int    `json:"sent"`
	Dropped int    `json:"dropped"`
	Failed  int    `json:"failed"`
	LastErr string `json:"last_error,omitempty"`
}

// UsageExporter batches usage rows and posts them to Heimdall.
type UsageExporter struct {
	cfg    UsageConfig
	client *http.Client

	mu      sync.Mutex
	queue   []UsageRecord
	stats   UsageStats
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	stopped bool
}

// NewUsageExporter starts an exporter. It returns nil when cfg.URL is empty
// (export off); every method is safe on a nil exporter.
func NewUsageExporter(cfg UsageConfig, client *http.Client) (*UsageExporter, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, nil
	}
	if !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://") {
		return nil, fmt.Errorf("heimdall usage url must start with http:// or https://: %q", cfg.URL)
	}
	if cfg.Secret == "" {
		return nil, fmt.Errorf("heimdall usage export needs a shared secret")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultUsageBatchSize
	}
	if cfg.FlushInterval <= 0 && cfg.FlushMS > 0 {
		cfg.FlushInterval = time.Duration(cfg.FlushMS) * time.Millisecond
	}
	if cfg.Timeout <= 0 && cfg.TimeoutMS > 0 {
		cfg.Timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultUsageFlushInterval
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultUsageTimeout
	}
	if cfg.QueueLimit <= 0 {
		cfg.QueueLimit = DefaultUsageQueueLimit
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	e := &UsageExporter{cfg: cfg, client: client, wake: make(chan struct{}, 1),
		stop: make(chan struct{}), done: make(chan struct{})}
	go e.loop()
	return e, nil
}

// Add queues one row. It never blocks on the network. Rows without a run id
// are not Heimdall traffic and are skipped.
func (e *UsageExporter) Add(r UsageRecord) {
	if e == nil || r.RunID == "" {
		return
	}
	e.mu.Lock()
	if e.stopped || len(e.queue) >= e.cfg.QueueLimit {
		e.stats.Dropped++
		e.mu.Unlock()
		return
	}
	e.queue = append(e.queue, r)
	full := len(e.queue) >= e.cfg.BatchSize
	e.mu.Unlock()
	if full {
		select {
		case e.wake <- struct{}{}:
		default:
		}
	}
}

// Stats returns a copy of the counters.
func (e *UsageExporter) Stats() UsageStats {
	if e == nil {
		return UsageStats{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}

// Close sends what is queued and stops the exporter.
func (e *UsageExporter) Close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.stopped = true
	e.mu.Unlock()
	close(e.stop)
	<-e.done
}

func (e *UsageExporter) loop() {
	defer close(e.done)
	t := time.NewTicker(e.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			for e.flush() {
			}
			return
		case <-e.wake:
			for e.flush() {
			}
		case <-t.C:
			e.flush()
		}
	}
}

// flush sends one batch. It reports whether a full batch was sent, so the
// caller keeps draining.
func (e *UsageExporter) flush() bool {
	e.mu.Lock()
	n := min(len(e.queue), e.cfg.BatchSize)
	if n == 0 {
		e.mu.Unlock()
		return false
	}
	batch := make([]UsageRecord, n)
	copy(batch, e.queue[:n])
	e.queue = append(e.queue[:0:0], e.queue[n:]...)
	e.mu.Unlock()

	err := e.post(batch)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err != nil {
		e.stats.Failed += n
		e.stats.LastErr = err.Error()
		return false
	}
	e.stats.Sent += n
	return n == e.cfg.BatchSize
}

func (e *UsageExporter) post(batch []UsageRecord) error {
	body, err := json.Marshal(UsageBatch{Records: batch})
	if err != nil {
		return fmt.Errorf("encode usage batch: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), e.cfg.Timeout)
	defer cancel()
	url := strings.TrimRight(e.cfg.URL, "/") + UsageIngestPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build usage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.cfg.Secret)
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("post usage batch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("post usage batch: heimdall answered %d", resp.StatusCode)
	}
	return nil
}

// usageRecord builds the row for one finished call from the Heimdall
// identity, the fault decision and Bifrost's response or error. ok is false
// for a streamed chunk that carries no usage yet (only the final chunk is
// recorded) and for requests with no run id.
func usageRecord(info *RequestInfo, d *Decision, resp *schemas.BifrostResponse, be *schemas.BifrostError, now time.Time) (UsageRecord, bool) {
	if info == nil || info.RunID == "" {
		return UsageRecord{}, false
	}
	r := UsageRecord{RunID: info.RunID, EvaluationID: info.EvalID, OrgID: info.Org, Arm: info.Arm,
		FaultPlanID: info.FaultPlanID, StartedAt: now}
	if d != nil {
		r.FaultApplied = d.Mode
	}
	switch {
	case resp != nil:
		u := resp.NormalizedUsage()
		if u == nil && be == nil {
			return UsageRecord{}, false
		}
		if ex := resp.GetExtraFields(); ex != nil {
			fillRouting(&r, ex.RoutingInfo, ex.Provider, ex.OriginalModelRequested)
			r.LatencyMS = ex.Latency
			r.CacheHit = ex.CacheDebug != nil && ex.CacheDebug.CacheHit
		}
		if u != nil {
			r.InputTokens, r.OutputTokens, r.TotalTokens = u.PromptTokens, u.CompletionTokens, u.TotalTokens
			if u.Cost != nil {
				r.CostUSD = u.Cost.TotalCost
			}
		}
		r.StatusCode = 200
	case be != nil:
		r.Provider = string(be.ExtraFields.Provider)
		r.Model = be.ExtraFields.OriginalModelRequested
		if be.StatusCode != nil {
			r.StatusCode = *be.StatusCode
		}
		if be.Error != nil {
			r.Error = be.Error.Message
		}
	default:
		return UsageRecord{}, false
	}
	return r, true
}

func fillRouting(r *UsageRecord, ri schemas.RoutingInfo, provider schemas.ModelProvider, model string) {
	r.Provider, r.Model = string(ri.Provider), ri.Model
	if r.Provider == "" {
		r.Provider = string(provider)
	}
	if r.Model == "" {
		r.Model = model
	}
	if ri.IsFallback {
		r.FallbackUsed = true
		if ri.PrimaryProvider != nil {
			r.FallbackFrom = string(*ri.PrimaryProvider)
		}
		if ri.PrimaryModel != nil {
			r.FallbackFrom += "/" + *ri.PrimaryModel
		}
	}
}
