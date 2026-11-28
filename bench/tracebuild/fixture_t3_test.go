package tracebuild

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/maximhq/bifrost/bench/investigator"
)

// TestWriteT3Fixture writes the refund Result, as Build produces it, wrapped
// the way the tracetask.build job stores it, for lane T3's handler and UI
// tests: HEIMDALL_T3_FIXTURE=<path> go test ./tracebuild -run TestWriteT3Fixture.
// The admission part uses admission's own control names (notes T1.md).
func TestWriteT3Fixture(t *testing.T) {
	out := os.Getenv("HEIMDALL_T3_FIXTURE")
	if out == "" {
		t.Skip("HEIMDALL_T3_FIXTURE not set")
	}
	m := &investigator.FakeModel{Replies: []string{refundDiag(t), refundWrite(t)}, CostUSD: 0.01, Cap: 0.5}
	res, err := Build(context.Background(), input(t, m))
	if err != nil {
		t.Fatal(err)
	}
	type check struct {
		Name   string `json:"name"`
		Want   string `json:"want"`
		Got    string `json:"got"`
		Detail string `json:"detail,omitempty"`
	}
	controls := []check{{"reference_1", "pass", "pass", ""}, {"empty_control", "fail", "fail", ""}, {"exploit", "fail", "fail", ""}, {"null_noop", "fail", "fail", ""}, {"null_refuse", "fail", "fail", ""}}
	for _, mu := range res.Mutants {
		if mu.Control != "" {
			controls = append(controls, check{mu.Control, "fail", "fail", ""})
		}
	}
	job := map[string]any{
		"Result": nil,
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatal(err)
	}
	delete(job, "Result")
	job["admission"] = map[string]any{"status": "ran", "admitted": false, "controls": controls,
		"guards": []check{{"guard_no_answer_leak", "pass", "pass", ""}, {"guard_instruction_clear", "pass", "fail", "A person must approve the instruction: open the draft and press Approve"}}}
	job["attempt"] = 1
	data, err := json.MarshalIndent(job, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
