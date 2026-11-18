package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunA2A(t *testing.T) {
	d := fullDeps()
	idx := stepsFixture(t)
	dir := filepath.Join(idx.root, "r_t01_A")
	ev := `{"a2a":{"entry":"hb-r-1-lead","complete":true,"strength":"witnessed",` +
		`"operations":[{"id":"op1","timestamp":"2026-11-18T08:00:00Z","operation":"SendMessage","status":"success","agent":"hb-r-1-lead","member":"lead","caller":"bench","task_id":"t1"}],` +
		`"hops":[{"from":"lead","to":"writer","operation_id":"op2","source":"gateway_key"}],` +
		`"agreement":{"task_id":"t1","launcher_state":"TASK_STATE_COMPLETED","gateway_state":"TASK_STATE_COMPLETED","answer_matches":true}}}`
	if err := os.WriteFile(filepath.Join(dir, FileEvidence), []byte(ev), 0o644); err != nil {
		t.Fatal(err)
	}
	d.index = idx
	c := newTestClient(t, d)
	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/a2a", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	var out RunA2A
	if err := json.Unmarshal(resp.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Available || out.A2A.Entry != "hb-r-1-lead" || len(out.A2A.Hops) != 1 || out.A2A.Agreement == nil || !*out.A2A.Agreement.AnswerMatches {
		t.Fatalf("a2a = %+v", out)
	}

	// A run of another agent: available false, a2a null.
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t02_A/a2a", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	out = RunA2A{}
	if err := json.Unmarshal(resp.Body(), &out); err != nil || out.Available || out.A2A != nil {
		t.Fatalf("other run = %+v %v", out, err)
	}
}
