package heimdall

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
)

type usageSink struct {
	mu      sync.Mutex
	batches []UsageBatch
	auth    []string
	status  int
}

func (s *usageSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path != UsageIngestPath || r.Method != http.MethodPost {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	var b UsageBatch
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.batches = append(s.batches, b)
	if s.status != 0 {
		w.WriteHeader(s.status)
	}
}

func (s *usageSink) rows() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		n += len(b.Records)
	}
	return n
}

func TestUsageExporter(t *testing.T) {
	t.Run("disabled without url", func(t *testing.T) {
		e, err := NewUsageExporter(UsageConfig{}, nil)
		if err != nil || e != nil {
			t.Fatalf("want nil exporter, got %v %v", e, err)
		}
		e.Add(UsageRecord{RunID: "r"}) // safe on nil
		e.Close()
		if e.Stats() != (UsageStats{}) {
			t.Fatal("nil stats")
		}
	})
	t.Run("bad config", func(t *testing.T) {
		for _, c := range []UsageConfig{{URL: "ftp://x", Secret: "s"}, {URL: "http://x"}} {
			if _, err := NewUsageExporter(c, nil); err == nil {
				t.Fatalf("want error for %+v", c)
			}
		}
	})
	t.Run("batches and flushes on close", func(t *testing.T) {
		sink := &usageSink{}
		srv := httptest.NewServer(sink)
		defer srv.Close()
		e, err := NewUsageExporter(UsageConfig{URL: srv.URL + "/", Secret: "sek", BatchSize: 2, FlushInterval: time.Hour}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			e.Add(UsageRecord{RunID: "run-1", Provider: "openai", Model: "gpt-4o-mini", TotalTokens: 10, CacheHit: i == 0, FaultApplied: "rate_limit"})
		}
		e.Add(UsageRecord{Provider: "openai"}) // no run id: skipped
		e.Close()
		e.Add(UsageRecord{RunID: "late"}) // after close: dropped
		if got := sink.rows(); got != 5 {
			t.Fatalf("rows %d, want 5", got)
		}
		st := e.Stats()
		if st.Sent != 5 || st.Dropped != 1 || st.Failed != 0 {
			t.Fatalf("stats %+v", st)
		}
		for _, a := range sink.auth {
			if a != "Bearer sek" {
				t.Fatalf("auth %q", a)
			}
		}
		if !sink.batches[0].Records[0].CacheHit || sink.batches[0].Records[0].FaultApplied != "rate_limit" {
			t.Fatalf("fields lost: %+v", sink.batches[0].Records[0])
		}
	})
	t.Run("interval flush", func(t *testing.T) {
		sink := &usageSink{}
		srv := httptest.NewServer(sink)
		defer srv.Close()
		e, _ := NewUsageExporter(UsageConfig{URL: srv.URL, Secret: "s", FlushInterval: 20 * time.Millisecond}, nil)
		defer e.Close()
		e.Add(UsageRecord{RunID: "r"})
		deadline := time.Now().Add(2 * time.Second)
		for sink.rows() == 0 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if sink.rows() != 1 {
			t.Fatal("interval flush did not send")
		}
	})
	t.Run("server error counted", func(t *testing.T) {
		sink := &usageSink{status: http.StatusUnauthorized}
		srv := httptest.NewServer(sink)
		defer srv.Close()
		e, _ := NewUsageExporter(UsageConfig{URL: srv.URL, Secret: "s", FlushInterval: time.Hour}, nil)
		e.Add(UsageRecord{RunID: "r"})
		e.Close()
		if st := e.Stats(); st.Failed != 1 || st.LastErr == "" {
			t.Fatalf("stats %+v", st)
		}
	})
	t.Run("queue limit drops", func(t *testing.T) {
		e, _ := NewUsageExporter(UsageConfig{URL: "http://127.0.0.1:1", Secret: "s", QueueLimit: 2, BatchSize: 100, FlushInterval: time.Hour, Timeout: 50 * time.Millisecond}, nil)
		for i := 0; i < 4; i++ {
			e.Add(UsageRecord{RunID: "r"})
		}
		e.Close()
		if st := e.Stats(); st.Dropped != 2 || st.Failed != 2 {
			t.Fatalf("stats %+v", st)
		}
	})
}

func TestUsageRecord(t *testing.T) {
	now := time.Unix(100, 0)
	info := &RequestInfo{RunID: "r1", EvalID: "e1", Org: "o1", Arm: "b", FaultPlanID: "p1"}
	primary, pmodel := schemas.ModelProvider("openai"), "gpt-4o"
	status := 429
	chat := &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{
		Usage: &schemas.BifrostLLMUsage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10, Cost: &schemas.BifrostCost{TotalCost: 0.5}},
		ExtraFields: schemas.BifrostResponseExtraFields{Latency: 42,
			RoutingInfo: schemas.RoutingInfo{Provider: "anthropic", Model: "claude", IsFallback: true, PrimaryProvider: &primary, PrimaryModel: &pmodel},
			CacheDebug:  &schemas.BifrostCacheMetadata{CacheHit: true}},
	}}
	cases := []struct {
		name string
		info *RequestInfo
		d    *Decision
		resp *schemas.BifrostResponse
		be   *schemas.BifrostError
		ok   bool
		want UsageRecord
	}{
		{name: "no run id", info: &RequestInfo{Org: "o"}, resp: chat},
		{name: "nothing", info: info},
		{name: "chunk without usage", info: info, resp: &schemas.BifrostResponse{ChatResponse: &schemas.BifrostChatResponse{}}},
		{name: "chat with fallback and cache", info: info, resp: chat, ok: true, want: UsageRecord{
			RunID: "r1", EvaluationID: "e1", OrgID: "o1", Arm: "b", FaultPlanID: "p1", Provider: "anthropic", Model: "claude",
			InputTokens: 7, OutputTokens: 3, TotalTokens: 10, CostUSD: 0.5, LatencyMS: 42, CacheHit: true,
			FallbackUsed: true, FallbackFrom: "openai/gpt-4o", StatusCode: 200, StartedAt: now}},
		{name: "fault error", info: info, d: &Decision{Mode: "rate_limit"}, be: &schemas.BifrostError{StatusCode: &status,
			Error:       &schemas.ErrorField{Message: "slow down"},
			ExtraFields: schemas.BifrostErrorExtraFields{Provider: "openai", OriginalModelRequested: "gpt-4o"}}, ok: true,
			want: UsageRecord{RunID: "r1", EvaluationID: "e1", OrgID: "o1", Arm: "b", FaultPlanID: "p1", Provider: "openai", Model: "gpt-4o",
				FaultApplied: "rate_limit", StatusCode: 429, Error: "slow down", StartedAt: now}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := usageRecord(c.info, c.d, c.resp, c.be, now)
			if ok != c.ok || got != c.want {
				t.Fatalf("got %+v %v, want %+v %v", got, ok, c.want, c.ok)
			}
		})
	}
}
