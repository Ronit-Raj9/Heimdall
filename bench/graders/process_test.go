package graders

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func call(name string, args any) ProcessCall {
	return ProcessCall{Name: name, Args: normalizeJSON(args), ArgsKnown: true}
}

func expected(name string, args map[string]any, match string) schemas.ExpectedTool {
	return schemas.ExpectedTool{Name: name, Args: args, ArgsMatch: match}
}

func TestCompareProcessModes(t *testing.T) {
	ref := []schemas.ExpectedTool{expected("lookup_order", map[string]any{"id": "A1"}, ""), expected("refund_order", nil, "")}
	cases := []struct {
		name    string
		match   string
		agent   []ProcessCall
		matched bool
		missing []string
		extra   []string
		dev     int
	}{
		{"strict same", "strict", []ProcessCall{call("lookup_order", map[string]any{"id": "A1", "x": 1}), call("refund_order", map[string]any{"amt": 3})}, true, nil, nil, 0},
		{"strict order", "strict", []ProcessCall{call("refund_order", nil), call("lookup_order", map[string]any{"id": "A1"})}, false, nil, nil, 1},
		{"strict wrong args", "", []ProcessCall{call("lookup_order", map[string]any{"id": "B2"}), call("refund_order", nil)}, false, []string{"lookup_order"}, []string{"lookup_order"}, 1},
		{"strict short", "strict", []ProcessCall{call("lookup_order", map[string]any{"id": "A1"})}, false, []string{"refund_order"}, nil, 2},
		{"unordered", "unordered", []ProcessCall{call("refund_order", nil), call("lookup_order", map[string]any{"id": "A1"})}, true, nil, nil, 0},
		{"unordered extra", "unordered", []ProcessCall{call("refund_order", nil), call("lookup_order", map[string]any{"id": "A1"}), call("ping", nil)}, false, nil, []string{"ping"}, 0},
		{"subset fewer", "subset", []ProcessCall{call("refund_order", nil)}, true, []string{"lookup_order"}, nil, 0},
		{"subset extra", "subset", []ProcessCall{call("ping", nil)}, false, []string{"lookup_order", "refund_order"}, []string{"ping"}, 0},
		{"superset extra", "superset", []ProcessCall{call("ping", nil), call("lookup_order", map[string]any{"id": "A1"}), call("refund_order", nil)}, true, nil, []string{"ping"}, 0},
		{"superset missing", "superset", []ProcessCall{call("lookup_order", map[string]any{"id": "A1"})}, false, []string{"refund_order"}, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := &schemas.ProcessSpec{ExpectedTools: ref, Match: tc.match}
			r, err := referencePath(spec, "")
			if err != nil {
				t.Fatal(err)
			}
			s, err := CompareProcess(spec, r, tc.agent)
			if err != nil {
				t.Fatal(err)
			}
			if s.Matched != tc.matched || !reflect.DeepEqual(s.Missing, tc.missing) || !reflect.DeepEqual(s.Extra, tc.extra) || s.FirstDeviation != tc.dev {
				t.Fatalf("score %+v", s)
			}
		})
	}
}

func TestCompareProcessEfficiencyAndRedundancy(t *testing.T) {
	spec := &schemas.ProcessSpec{ExpectedTools: []schemas.ExpectedTool{{Name: "a"}, {Name: "b"}}, Match: "superset", MaxStepsRatio: 1.5}
	ref, err := referencePath(spec, "")
	if err != nil {
		t.Fatal(err)
	}
	agent := []ProcessCall{call("a", map[string]any{"q": 1}), call("a", map[string]any{"q": 1.0}), call("a", map[string]any{"q": 2}), call("b", nil)}
	s, err := CompareProcess(spec, ref, agent)
	if err != nil {
		t.Fatal(err)
	}
	if s.StepRatio == nil || *s.StepRatio != 2 || !s.OverBudget || s.Redundant == nil || *s.Redundant != 1 || !s.Matched {
		t.Fatalf("score %+v", s)
	}
	spec.ReferenceSteps = 4
	if s, _ = CompareProcess(spec, ref, agent); *s.StepRatio != 1 || s.OverBudget {
		t.Fatalf("reference_steps: %+v", s)
	}
	// Names only (gateway): no redundancy, arguments not checked.
	s, _ = CompareProcess(spec, ref, []ProcessCall{{Name: "a"}, {Name: "a"}, {Name: "b"}})
	if s.Redundant != nil || s.ArgsChecked || !s.Matched {
		t.Fatalf("names only: %+v", s)
	}
}

func TestCompareProcessRejectsBadSpec(t *testing.T) {
	for name, spec := range map[string]*schemas.ProcessSpec{
		"match":    {Match: "fuzzy"},
		"ratio":    {MaxStepsRatio: -1},
		"refsteps": {ReferenceSteps: -2},
	} {
		if _, err := CompareProcess(spec, nil, nil); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if _, err := referencePath(&schemas.ProcessSpec{ExpectedTools: []schemas.ExpectedTool{{Name: "a", ArgsMatch: "loose"}}}, ""); err == nil {
		t.Fatal("args_match loose accepted")
	}
	if _, err := referencePath(&schemas.ProcessSpec{Reference: "../x.jsonl"}, t.TempDir()); err == nil {
		t.Fatal("reference outside the task folder accepted")
	}
}

func TestParseReference(t *testing.T) {
	data := []byte(`{"seq":1,"server":"crm","tool":"tools/list","protocol":"mcp"}
{"seq":2,"server":"crm","tool":"lookup_order","protocol":"mcp","arguments":"{\"id\":\"A1\"}"}
{"seq":3,"server":"crm","tool":"initialize","protocol":"mcp","kind":"mcp_method"}

{"name":"refund_order","args":{"amount":5}}
`)
	got, err := ParseReference(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "lookup_order" || !jsonEqual(got[0].Args, map[string]any{"id": "A1"}) || got[1].Name != "refund_order" {
		t.Fatalf("reference %+v", got)
	}
	if _, err := ParseReference([]byte(`{"seq":1}`)); err == nil {
		t.Fatal("row without a tool accepted")
	}
	if _, err := ParseReference([]byte(`not json`)); err == nil {
		t.Fatal("bad json accepted")
	}
}

func TestProcessMetrics(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ref.jsonl"), []byte(`{"name":"lookup_order"}
{"name":"refund_order"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	task := &schemas.Task{ID: "t", Dir: dir, Process: &schemas.ProcessSpec{Reference: "ref.jsonl", Match: "unordered"}}
	ledger := &schemas.Evidence{ToolCalls: []schemas.ToolCall{
		{Seq: 1, Tool: "tools/list", Protocol: "mcp"},
		{Seq: 2, Tool: "lookup_order", Protocol: "mcp", Arguments: `{"id":"A1"}`},
		{Seq: 3, Tool: "lookup_order", Protocol: "mcp", Arguments: `{"id":"A1"}`},
		{Seq: 4, Tool: "refund_order", Protocol: "mcp"},
	}}
	gateway := &schemas.Evidence{Requests: &schemas.RequestLog{}, RequestedTools: []schemas.ToolCallRecord{
		{ToolName: "refund_order", Requested: true}, {ToolName: "lookup_order", Requested: true}}}
	byName := map[string]schemas.Metric{}
	for _, m := range ProcessMetrics() {
		byName[m.GetName()] = m
		if d := m.Descriptor(); !d.ReportOnly || d.Name != m.GetName() || d.Label == "" {
			t.Fatalf("descriptor %+v", d)
		}
	}
	compute := func(name string, rc *schemas.RunContext, ev *schemas.Evidence) schemas.Value {
		v, err := byName[name].Compute(context.Background(), rc, ev)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	rc := &schemas.RunContext{Task: task}
	cases := []struct {
		name   string
		rc     *schemas.RunContext
		ev     *schemas.Evidence
		metric string
		want   float64
		state  schemas.ValueState
	}{
		{"ledger extra call", rc, ledger, ProcessToolMatch, 0, schemas.ValueObserved},
		{"ledger ratio", rc, ledger, ProcessStepRatio, 1.5, schemas.ValueObserved},
		{"ledger repeats", rc, ledger, ProcessRedundantCalls, 1, schemas.ValueObserved},
		{"gateway match", rc, gateway, ProcessToolMatch, 1, schemas.ValueObserved},
		{"gateway repeats unknown", rc, gateway, ProcessRedundantCalls, 0, schemas.ValueUnknown},
		{"no evidence", rc, &schemas.Evidence{}, ProcessToolMatch, 0, schemas.ValueUnknown},
		{"no process block", &schemas.RunContext{Task: &schemas.Task{ID: "t"}}, ledger, ProcessToolMatch, 0, schemas.ValueNotApplicable},
		{"missing reference", &schemas.RunContext{Task: &schemas.Task{Dir: dir, Process: &schemas.ProcessSpec{Reference: "nope.jsonl"}}}, ledger, ProcessStepRatio, 0, schemas.ValueUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := compute(tc.metric, tc.rc, tc.ev)
			if v.State != tc.state {
				t.Fatalf("value %+v, want state %s", v, tc.state)
			}
			if tc.state == schemas.ValueObserved {
				if f, _ := v.Float(); f != tc.want {
					t.Fatalf("value %+v, want %v", v, tc.want)
				}
			}
		})
	}
}
