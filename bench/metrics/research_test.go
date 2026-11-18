package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// researchRun builds a research run: a task folder with a research spec
// whose evidence page is https://e.example/ev, and a run folder holding the
// given sources.json (none when empty).
func researchRun(t *testing.T, sources string, evidence bool) *schemas.RunContext {
	t.Helper()
	task, run := t.TempDir(), t.TempDir()
	spec := `{"question": "q", "match": "numeric", "expected": ["1"], "require_grounding": true, "evidence_urls": ["https://e.example/ev"]}`
	if !evidence {
		spec = `{"question": "q", "match": "exact", "abstain": true}`
	}
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(task, "verifier", "tests", "research.json"), spec)
	write(filepath.Join(run, "artifacts", "answer", "answer.txt"), "1\n")
	if sources != "" {
		write(filepath.Join(run, "artifacts", "sources", "sources.json"), sources)
	}
	return &schemas.RunContext{
		Suite: &schemas.Suite{Track: schemas.TrackResearch, External: []schemas.ExternalAPI{{ID: "tavily", BaseURL: "https://api.tavily.com"}}},
		Task:  &schemas.Task{Dir: task, Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}},
		Dir:   run,
	}
}

func searchRow(seq int, urls ...string) schemas.ToolCall {
	res := `{"results":[`
	for i, u := range urls {
		if i > 0 {
			res += ","
		}
		res += `{"url":"` + u + `"}`
	}
	return schemas.ToolCall{Seq: seq, Server: "tavily", Tool: "POST /search", Protocol: "external", Status: 200, Replayed: true, Result: res + `]}`}
}

func TestResearchMetrics(t *testing.T) {
	ledger := []schemas.ToolCall{
		searchRow(1, "https://e.example/ev", "https://d.example/distractor"),
		{Seq: 2, Server: "websearch", Tool: "web_search", Protocol: "mcp", Result: `{"results":[{"url":"https://mcp-only.example/x"}]}`},
		{Seq: 3, Server: "tavily", Tool: "POST /search", Protocol: "external", Status: 599, IsError: true, Unmatched: true},
		{Seq: 4, Server: "tavily", Tool: "POST /extract", Protocol: "external", Status: 200, Result: `{"results":[{"url":"https://f.example/fetched","raw_content":"x"}]}`},
	}
	tests := []struct {
		name   string
		rc     func(t *testing.T) *schemas.RunContext
		calls  []schemas.ToolCall
		metric schemas.Metric
		want   float64
		status string // observed | na | unknown
	}{
		{name: "searches count every search call", rc: func(t *testing.T) *schemas.RunContext { return researchRun(t, `[]`, true) }, calls: ledger, metric: newSearchesMetric(), want: 2, status: "observed"},
		{name: "searches unknown without ledger", rc: func(t *testing.T) *schemas.RunContext { return researchRun(t, `[]`, true) }, metric: newSearchesMetric(), status: "unknown"},
		{name: "searches not applicable outside research", rc: func(t *testing.T) *schemas.RunContext {
			return &schemas.RunContext{Suite: &schemas.Suite{}, Task: &schemas.Task{}}
		}, calls: ledger, metric: newSearchesMetric(), status: "na"},
		{name: "sources cited, distinct after normalizing", rc: func(t *testing.T) *schemas.RunContext {
			return researchRun(t, `["https://e.example/ev", "https://E.example/ev/", "https://f.example/fetched"]`, true)
		}, metric: newSourcesCitedMetric(), want: 2, status: "observed"},
		{name: "sources cited zero when sources.json is broken", rc: func(t *testing.T) *schemas.RunContext { return researchRun(t, `[oops`, true) }, metric: newSourcesCitedMetric(), want: 0, status: "observed"},
		{name: "grounded: searched and fetched pages count", rc: func(t *testing.T) *schemas.RunContext {
			return researchRun(t, `["https://e.example/ev", "https://f.example/fetched"]`, true)
		}, calls: ledger, metric: newCitationsGroundedMetric(), want: 1, status: "observed"},
		{name: "grounded catches an invented citation", rc: func(t *testing.T) *schemas.RunContext {
			return researchRun(t, `["https://e.example/ev", "https://invented.example/page"]`, true)
		}, calls: ledger, metric: newCitationsGroundedMetric(), want: 0.5, status: "observed"},
		{name: "grounded catches a real page never retrieved in this run", rc: func(t *testing.T) *schemas.RunContext { return researchRun(t, `["https://e.example/ev"]`, true) }, calls: []schemas.ToolCall{searchRow(1, "https://d.example/distractor")}, metric: newCitationsGroundedMetric(), want: 0, status: "observed"},
		{name: "grounded ignores the MCP row text", rc: func(t *testing.T) *schemas.RunContext { return researchRun(t, `["https://mcp-only.example/x"]`, true) }, calls: ledger, metric: newCitationsGroundedMetric(), want: 0, status: "observed"},
		{name: "grounded not applicable without citations", rc: func(t *testing.T) *schemas.RunContext { return researchRun(t, `[]`, true) }, calls: ledger, metric: newCitationsGroundedMetric(), status: "na"},
		{name: "grounded unknown without ledger", rc: func(t *testing.T) *schemas.RunContext { return researchRun(t, `["https://e.example/ev"]`, true) }, metric: newCitationsGroundedMetric(), status: "unknown"},
		{name: "precision", rc: func(t *testing.T) *schemas.RunContext {
			return researchRun(t, `["https://e.example/ev", "https://d.example/distractor", "https://f.example/fetched", "https://x.example/y"]`, true)
		}, metric: newCitationPrecisionMetric(), want: 0.25, status: "observed"},
		{name: "precision not applicable on an abstention task", rc: func(t *testing.T) *schemas.RunContext { return researchRun(t, `["https://e.example/ev"]`, false) }, metric: newCitationPrecisionMetric(), status: "na"},
		{name: "precision unknown without a spec", rc: func(t *testing.T) *schemas.RunContext {
			rc := researchRun(t, `["https://e.example/ev"]`, true)
			rc.Task.Dir = t.TempDir()
			return rc
		}, metric: newCitationPrecisionMetric(), status: "unknown"},
		{name: "cited unknown without a run folder", rc: func(t *testing.T) *schemas.RunContext {
			rc := researchRun(t, `[]`, true)
			rc.Dir = ""
			return rc
		}, metric: newSourcesCitedMetric(), status: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.metric.Compute(context.Background(), tt.rc(t), &schemas.Evidence{ToolCalls: tt.calls})
			if err != nil {
				t.Fatal(err)
			}
			switch tt.status {
			case "observed":
				if got, ok := v.Float(); !ok || got != tt.want {
					t.Errorf("value = %+v, want %v", v, tt.want)
				}
			case "na":
				if v.State != schemas.ValueNotApplicable {
					t.Errorf("value = %+v, want not applicable", v)
				}
			case "unknown":
				if v.State != schemas.ValueUnknown {
					t.Errorf("value = %+v, want unknown", v)
				}
			}
		})
	}
}
