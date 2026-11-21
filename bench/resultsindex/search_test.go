package resultsindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

type fakeSearchSource struct {
	runs  []SearchRun
	files map[string]map[string][]byte
	reads int
}

func (f *fakeSearchSource) SearchRuns(context.Context) ([]SearchRun, error) { return f.runs, nil }

func (f *fakeSearchSource) RunFile(runID, name string) ([]byte, error) {
	f.reads++
	if b, ok := f.files[runID][name]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("%s: %w", name, schemas.ErrNotFound)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func searchFixture(t *testing.T) *fakeSearchSource {
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	ev1 := map[string]any{
		"steps": []schemas.StepRecord{{Seq: 1, Kind: schemas.StepToolRequest, ToolName: "refund_order", Summary: "refund_order order A1"},
			{Seq: 2, Kind: schemas.StepToolExecution, ToolName: "refund_order", Summary: "dup", WitnessOf: 1}},
		"answer": schemas.Answer{Text: "Done. All tests pass and the refund is issued."},
	}
	tools1 := `{"seq":1,"server":"crm","tool":"refund_order","protocol":"mcp","arguments":"{\"order\":\"A1\"}","result":"ok"}
{"seq":2,"server":"crm","tool":"tools/list","protocol":"mcp"}
`
	ev2 := map[string]any{"answer": schemas.Answer{Text: "I could not finish; tests fail."}}
	gates2 := []schemas.GateResult{{Gate: "protected_files", Fired: true, Detail: "tests/test_refund.py changed"}, {Gate: "quiet", Fired: false, Detail: "never shown"}}
	grade2 := schemas.Grade{Reason: "2 required tests failed", FailedTests: []string{"test_refund_amount"}}
	return &fakeSearchSource{
		runs: []SearchRun{
			{RunID: "r1", Experiment: "e1", Benchmark: "support", Task: "t1", Agent: "bot", Version: "3", Outcome: "pass", Started: t0, SealHash: "s1"},
			{RunID: "r2", Experiment: "e1", Benchmark: "support", Task: "t2", Agent: "bot", Version: "4", Outcome: "fail", Started: t0.Add(time.Hour), SealHash: "s2"},
		},
		files: map[string]map[string][]byte{
			"r1": {"evidence.json": mustJSON(t, ev1), "tools.jsonl": []byte(tools1)},
			"r2": {"evidence.json": mustJSON(t, ev2), "gates.json": mustJSON(t, gates2), "grade.json": mustJSON(t, grade2)},
		},
	}
}

func TestParseSearchQuery(t *testing.T) {
	q, err := ParseSearchQuery(`tool:Refund_Order "All Tests Pass" agent:bot extra`, map[string]string{"outcome": "fail", "agent": "other"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Tools) != 1 || q.Tools[0] != "refund_order" || len(q.Terms) != 2 || q.Terms[0] != "all tests pass" || q.Terms[1] != "extra" ||
		q.Agent != "other" || q.Outcome != "fail" || q.Limit != DefaultSearchLimit {
		t.Fatalf("query %+v", q)
	}
	if q, _ := ParseSearchQuery("x", nil, 10_000); q.Limit != MaxSearchLimit {
		t.Fatalf("limit %d", q.Limit)
	}
	for _, bad := range []string{`"open`, string(make([]rune, 600))} {
		if _, err := ParseSearchQuery(bad, nil, 0); !errors.Is(err, ErrBadSearch) {
			t.Fatalf("%q: err %v", bad[:min(len(bad), 10)], err)
		}
	}
}

func TestSearch(t *testing.T) {
	src := searchFixture(t)
	ix := NewSearchIndex()
	run := func(q string, filters map[string]string) *SearchResult {
		t.Helper()
		pq, err := ParseSearchQuery(q, filters, 0)
		if err != nil {
			t.Fatal(err)
		}
		res, err := ix.Search(context.Background(), src, pq)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	cases := []struct {
		q       string
		filters map[string]string
		runs    []string
	}{
		{`"all tests pass"`, nil, []string{"r1"}},
		{`tests`, nil, []string{"r2", "r1"}}, // newest first
		{`tool:refund_order`, nil, []string{"r1"}},
		{`tool:refund_order tests`, map[string]string{"outcome": "fail"}, nil},
		{`protected_files`, nil, []string{"r2"}},
		{`never shown`, nil, nil}, // a gate that did not fire is not indexed
		{`test_refund_amount`, nil, []string{"r2"}},
		{`order`, map[string]string{"version": "3", "benchmark": "support@1.0.0"}, []string{"r1"}},
		{`tests fail refund`, nil, []string{"r2"}}, // all words, across passages of one run
		{`dup`, nil, nil}, // a second witness is not indexed
		{``, nil, nil},
	}
	for _, tc := range cases {
		res := run(tc.q, tc.filters)
		var got []string
		for _, h := range res.Hits {
			got = append(got, h.RunID)
		}
		if fmt.Sprint(got) != fmt.Sprint(tc.runs) || res.Total != len(tc.runs) {
			t.Fatalf("%q %v: hits %v total %d", tc.q, tc.filters, got, res.Total)
		}
	}
	res := run(`"all tests pass"`, nil)
	m := res.Hits[0].Matches
	if len(m) != 1 || m[0].Field != SearchFieldAnswer || len(m[0].Highlights) != 1 {
		t.Fatalf("matches %+v", m)
	}
	h := m[0].Highlights[0]
	if got := string([]rune(m[0].Text)[h[0]:h[1]]); got != "All tests pass" {
		t.Fatalf("highlight %q", got)
	}
	if res.Hits[0].Agent != "bot" || res.Hits[0].Version != "3" || res.Hits[0].Started == nil {
		t.Fatalf("hit %+v", res.Hits[0])
	}
	// Cached by seal hash: a second search reads no file.
	before := src.reads
	run(`tests`, nil)
	if src.reads != before {
		t.Fatalf("cache missed: %d reads", src.reads-before)
	}
	src.runs[0].SealHash = "s1b"
	run(`tests`, nil)
	if src.reads == before {
		t.Fatal("changed seal not re-read")
	}
	// Limit truncates but counts every match.
	pq, _ := ParseSearchQuery("tests", nil, 1)
	r, err := ix.Search(context.Background(), src, pq)
	if err != nil || len(r.Hits) != 1 || r.Total != 2 || !r.Truncated {
		t.Fatalf("limit: %+v %v", r, err)
	}
	// Runs that are gone leave the cache.
	src.runs = src.runs[:1]
	run(`tests`, nil)
	if _, ok := ix.docs["r2"]; ok {
		t.Fatal("gone run kept")
	}
}

func TestSnippetWindow(t *testing.T) {
	long := ""
	for range 40 {
		long += "padding "
	}
	text, hl := snippet(long+"Needle here and needle again", []string{"needle"})
	if len(hl) != 2 || text[:3] != "..." {
		t.Fatalf("snippet %q %v", text, hl)
	}
	r := []rune(text)
	for _, h := range hl {
		if got := string(r[h[0]:h[1]]); got != "Needle" && got != "needle" {
			t.Fatalf("highlight %q", got)
		}
	}
}
