package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// corpusRun is a BrowseComp-Plus-like run: a research suite with a corpus
// tool service and no Tavily API, and a task with two evidence pages.
func corpusRun(t *testing.T, spec string) *schemas.RunContext {
	t.Helper()
	task := t.TempDir()
	p := filepath.Join(task, "verifier", "tests", "research.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(spec), 0o644); err != nil {
		t.Fatal(err)
	}
	return &schemas.RunContext{
		Suite: &schemas.Suite{Track: schemas.TrackResearch, Services: []schemas.ServiceSpec{{ID: CorpusService, Role: schemas.ServiceRoleTool}}},
		Task:  &schemas.Task{Dir: task, Verifier: schemas.TaskVerifier{Tests: "verifier/tests"}},
		Dir:   t.TempDir(),
	}
}

const twoEvidence = `{"question": "q", "match": "exact", "evidence_urls": ["https://a.example/one", "https://b.example/two/"]}`

func corpusSearch(seq int, urls ...string) schemas.ToolCall {
	c := searchRow(seq, urls...)
	c.Server, c.Protocol, c.Replayed = CorpusService, "http", false
	return c
}

func TestEvidenceRecall(t *testing.T) {
	m := newEvidenceRecallMetric()
	cases := []struct {
		name   string
		rc     func(t *testing.T) *schemas.RunContext
		calls  []schemas.ToolCall
		want   float64
		status string
	}{
		{"one of two evidence pages searched", func(t *testing.T) *schemas.RunContext { return corpusRun(t, twoEvidence) },
			[]schemas.ToolCall{corpusSearch(1, "https://A.example/one/", "https://c.example/neg")}, 0.5, "observed"},
		{"both, across searches", func(t *testing.T) *schemas.RunContext { return corpusRun(t, twoEvidence) },
			[]schemas.ToolCall{corpusSearch(1, "https://a.example/one"), corpusSearch(2, "https://b.example/two")}, 1, "observed"},
		{"fetches and failed searches do not count", func(t *testing.T) *schemas.RunContext { return corpusRun(t, twoEvidence) },
			[]schemas.ToolCall{
				{Seq: 1, Server: CorpusService, Tool: "POST /extract", Protocol: "http", Status: 200, Result: `{"results":[{"url":"https://a.example/one"}]}`},
				{Seq: 2, Server: CorpusService, Tool: "POST /search", Protocol: "http", Status: 500, IsError: true, Result: `{"results":[{"url":"https://b.example/two"}]}`},
				{Seq: 3, Server: CorpusService, Tool: "POST /search", Protocol: "http", Status: 200, Fault: "error", Result: `{"results":[{"url":"https://b.example/two"}]}`},
				{Seq: 4, Server: "websearch", Tool: "web_search", Protocol: "mcp", Result: `{"results":[{"url":"https://a.example/one"}]}`},
			}, 0, "observed"},
		{"Tavily rows count too", func(t *testing.T) *schemas.RunContext { return researchRun(t, `[]`, true) },
			[]schemas.ToolCall{searchRow(1, "https://e.example/ev")}, 1, "observed"},
		{"a clipped result is scanned", func(t *testing.T) *schemas.RunContext { return corpusRun(t, twoEvidence) },
			[]schemas.ToolCall{{Seq: 1, Server: CorpusService, Tool: "POST /search", Protocol: "http", Status: 200, Result: `{"results":[{"url":"https://a.example/one","content":"cut`}}, 0.5, "observed"},
		{"no ledger is unknown", func(t *testing.T) *schemas.RunContext { return corpusRun(t, twoEvidence) }, nil, 0, "unknown"},
		{"no evidence pages is not applicable", func(t *testing.T) *schemas.RunContext { return corpusRun(t, `{"question": "q", "match": "exact"}`) },
			[]schemas.ToolCall{corpusSearch(1, "https://a.example/one")}, 0, "na"},
		{"outside research is not applicable", func(t *testing.T) *schemas.RunContext {
			return &schemas.RunContext{Suite: &schemas.Suite{}, Task: &schemas.Task{}}
		}, []schemas.ToolCall{corpusSearch(1, "https://a.example/one")}, 0, "na"},
		{"unreadable spec is unknown", func(t *testing.T) *schemas.RunContext { return corpusRun(t, `{"question": "q", "surprise": 1}`) },
			[]schemas.ToolCall{corpusSearch(1, "https://a.example/one")}, 0, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := &schemas.Evidence{ToolCalls: c.calls}
			if c.calls == nil {
				ev = &schemas.Evidence{}
			}
			v, err := m.Compute(context.Background(), c.rc(t), ev)
			if err != nil {
				t.Fatal(err)
			}
			switch c.status {
			case "observed":
				if f, ok := v.Float(); !ok || !v.IsObserved() || f != c.want {
					t.Errorf("got %+v, want %v", v, c.want)
				}
			case "na":
				if v.State != schemas.ValueNotApplicable {
					t.Errorf("got %+v, want not applicable", v)
				}
			case "unknown":
				if v.State != schemas.ValueUnknown || v.Reason == "" {
					t.Errorf("got %+v, want unknown with a reason", v)
				}
			}
		})
	}
}
