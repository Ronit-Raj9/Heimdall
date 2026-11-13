package handlers

import (
	"testing"
)

func TestPairResponse(t *testing.T) {
	idx := testIndex()
	idx.files["r_t01_B"][FileEvents] = []byte("{\"event_id\":\"r_t01_B:1\",\"type\":\"tool_call\",\"target\":\"cat CLAUDE.md\"}\n" +
		"{\"event_id\":\"r_t01_B:2\",\"type\":\"tool_call\",\"target\":\"pytest\"}\n")
	d := fullDeps()
	d.index = idx
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/pairs/p_t01", nil, nil))
	runs, _ := body["runs"].([]any)
	steps, _ := body["alignment"].([]any)
	first, _ := body["first_divergence"].(map[string]any)
	if len(runs) != 2 || len(steps) != 2 || first == nil || first["index"] != float64(0) || first["note"] != DivergenceNote {
		t.Fatalf("pair = %v", body)
	}
	step0 := steps[0].(map[string]any)
	events := step0["events"].(map[string]any)
	if events["A"] != nil || events["B"] != "r_t01_B:1" || step0["diverged"] != true {
		t.Errorf("step 0 = %v", step0)
	}
	if run0 := runs[0].(map[string]any); run0["arm"] != "A" || run0["metrics"] == nil {
		t.Errorf("run 0 = %v", run0)
	}
}

func TestPairWithOneRunHasNoAlignment(t *testing.T) {
	idx := testIndex()
	idx.runs = idx.runs[:1]
	d := fullDeps()
	d.index = idx
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/pairs/p_t01", nil, nil))
	if steps, _ := body["alignment"].([]any); len(steps) != 0 || body["first_divergence"] != nil {
		t.Fatalf("pair = %v", body)
	}
}
