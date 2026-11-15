package traceexport

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestGroupSteps(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	log := &schemas.RequestLog{Requests: []schemas.GatewayRequest{
		{ID: "c", ParentID: "b", Kind: "message", Timestamp: t0.Add(3 * time.Second)},
		{ID: "a", Kind: "message", Timestamp: t0},
		{ID: "b", ParentID: "a", Kind: "message", Timestamp: t0.Add(time.Second)},
		{ID: "d", ParentID: "gone", Kind: "message", Timestamp: t0.Add(4 * time.Second)},
		{Kind: "message", Timestamp: t0.Add(5 * time.Second)},
		{ID: "e", Kind: "models", Timestamp: t0.Add(6 * time.Second)},
		{ID: "x", ParentID: "y", Kind: "message", Timestamp: t0.Add(7 * time.Second)},
		{ID: "y", ParentID: "x", Kind: "message", Timestamp: t0.Add(8 * time.Second)},
	}}
	steps := groupSteps(log)
	want := []struct {
		id   string
		rows int
	}{{"a", 3}, {"d", 1}, {"request:4", 1}, {"x", 2}}
	if len(steps) != len(want) {
		t.Fatalf("got %d steps, want %d", len(steps), len(want))
	}
	for i, w := range want {
		if steps[i].id != w.id || len(steps[i].rows) != w.rows || steps[i].number != i+1 {
			t.Fatalf("step %d: got %s with %d rows (number %d), want %s with %d", i, steps[i].id, len(steps[i].rows), steps[i].number, w.id, w.rows)
		}
	}
	if groupSteps(nil) != nil {
		t.Fatal("nil log gave steps")
	}
}

func TestSumTokensUnknown(t *testing.T) {
	if _, ok := sumTokens([]schemas.GatewayRequest{{}, {}}); ok {
		t.Fatal("rows without usage summed to a known value")
	}
	tok, ok := sumTokens([]schemas.GatewayRequest{{Tokens: &schemas.Tokens{Fresh: 1, Out: 2}}, {}, {Tokens: &schemas.Tokens{CacheWrite: 3}}})
	if !ok || tok.Fresh != 1 || tok.Out != 2 || tok.CacheWrite != 3 {
		t.Fatalf("got %+v %v", tok, ok)
	}
}

func TestGenAIProvider(t *testing.T) {
	cases := map[string]string{"openai": "openai", "anthropic": "anthropic", "bedrock": "aws.bedrock", "vertex": "gcp.vertex_ai",
		"gemini": "gcp.gemini", "azure": "azure.ai.openai", "mistral": "mistral_ai", "xai": "x_ai", "": ""}
	for in, want := range cases {
		if got := genAIProvider(in); got != want {
			t.Fatalf("genAIProvider(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStepCountsFromRows(t *testing.T) {
	b := &builder{}
	st := step{id: "a", number: 4, rows: []schemas.GatewayRequest{
		{Attempts: []schemas.Attempt{{N: 1}, {N: 2}, {N: 3}}},
		{FallbackIndex: 1},
	}}
	n, attempts, retries, fallbacks := b.stepCounts(st)
	if n != 4 || attempts != 4 || retries != 2 || fallbacks != 1 {
		t.Fatalf("got %d %d %d %d", n, attempts, retries, fallbacks)
	}
}
