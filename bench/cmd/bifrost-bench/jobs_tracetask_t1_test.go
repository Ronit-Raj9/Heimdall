package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

func TestTraceTaskBuildJob(t *testing.T) {
	root := t.TempDir()
	item := w445Item(t, root)
	diag, _ := json.Marshal(map[string]any{"kind": "false_claim", "summary": "The agent's reply hid that the refund failed.",
		"quotes": []any{map[string]any{"run_id": item, "seq": 1, "text": "Error: order not found"}}})
	write, _ := json.Marshal(map[string]any{
		"instruction": "Refund order A-1001 and tell the customer what happened.",
		"answers":     []string{"order not found"},
		"variants": []any{
			map[string]any{"name": "claims-success", "what_it_does_wrong": "says the refund went through", "answer": "The refund went through."},
			map[string]any{"name": "silent", "what_it_does_wrong": "says nothing useful", "answer": "Done."},
		},
	})
	fm := &investigator.FakeModel{Replies: []string{string(diag), string(write)}, CostUSD: 0.01, Cap: 1}
	dones := withFakeCaller(t, fm)
	env := jobEnv{ProjectRoot: root}
	params := func(m map[string]any) json.RawMessage {
		base := map[string]any{"inbox_id": item, "suite": "support", "provider": "openai", "model": "cheap", "budget_usd": 0.5}
		for k, v := range m {
			base[k] = v
		}
		b, _ := json.Marshal(base)
		return b
	}
	for _, bad := range []map[string]any{{"model": ""}, {"budget_usd": 0}} {
		if _, _, err := validateTraceTask(context.Background(), env, params(bad)); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	out, err := runTraceTask(context.Background(), env, &jobs.Job{ID: "j1", Params: params(nil)}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := out.(traceTaskResult)
	if r.Failure.Kind != "false_claim" || r.Shape != "qa" || len(r.Mutants) != 2 || r.Admission.Admitted || *dones != 1 {
		t.Fatalf("result %+v dones %d", r, *dones)
	}
	data, err := os.ReadFile(filepath.Join(root, traceinbox.Dir, traceinbox.ItemsDir, item, TraceTaskBuildDir, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"failure"`, `"admission"`, `"admitted": false`, `"files"`} {
		if !strings.Contains(string(data), k) {
			t.Errorf("stored result lacks %s", k)
		}
	}
}
