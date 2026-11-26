package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/tracedraft"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// w445Item puts one made-up chat session (never a real file) into a fresh
// inbox under root and returns its item id.
func w445Item(t *testing.T, root string) string {
	t.Helper()
	ib := traceinbox.New(filepath.Join(root, traceinbox.Dir), &traceinbox.Config{}, filepath.Join(root, "benchmarks"))
	trace := `{"id":"c1","status":"failed","messages":[{"role":"user","content":"Refund order A-1001 please, key sk-live0123456789abcdefgh"},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"t1","type":"function","function":{"name":"refund_order","arguments":"{\"order_id\":\"A-1001\"}"}}]},` +
		`{"role":"tool","tool_call_id":"t1","content":"Error: order not found"},{"role":"assistant","content":"I could not refund the order."}]}`
	res, err := ib.Ingest(context.Background(), &traceinbox.Source{Name: "test", Kind: traceinbox.KindWebhook}, traceinbox.IngestRequest{Trace: json.RawMessage(trace)})
	if err != nil {
		t.Fatal(err)
	}
	return res.ItemID
}

// withFakeCaller points openCaller at c for one test.
func withFakeCaller(t *testing.T, c investigator.Caller) *int {
	t.Helper()
	old := openCaller
	dones := new(int)
	openCaller = func(ctx context.Context, env jobEnv, j *jobs.Job, p modelParams) (investigator.Caller, func(), error) {
		if p.BudgetUSD > traceDraftMaxUSD {
			t.Errorf("budget %v passed the cap", p.BudgetUSD)
		}
		return c, func() { *dones++ }, nil
	}
	t.Cleanup(func() { openCaller = old })
	return dones
}

func w445Params(item string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"item": item, "suite": "support", "kind": "tool-use", "provider": "openai", "model": "cheap", "budget_usd": 5.0, "yes": true})
	return b
}

func TestTraceDraftJobWritesProposal(t *testing.T) {
	root := t.TempDir()
	item := w445Item(t, root)
	draft := map[string]any{
		"guide": map[string]any{"calls": []any{map[string]any{"tool": "refund_order", "args": map[string]any{"order_id": "A-1001"}}}, "reply_rubric": "Says the refund went through."},
		"reasons": []any{
			map[string]any{"field": "calls", "text": "The refund failed.", "quotes": []any{map[string]any{"run_id": item, "seq": 1, "text": "Error: order not found"}}},
			map[string]any{"field": "reply_rubric", "text": "Invented.", "quotes": []any{map[string]any{"run_id": item, "seq": 0, "text": "the refund was issued in full"}}},
		},
	}
	b, _ := json.Marshal(draft)
	fm := &investigator.FakeModel{Replies: []string{string(b), string(b)}, CostUSD: 0.01, Cap: 1}
	dones := withFakeCaller(t, fm)
	env := jobEnv{ProjectRoot: root}
	if _, _, err := validateTraceDraft(context.Background(), env, w445Params(item)); err != nil {
		t.Fatal(err)
	}
	out, err := runTraceDraft(context.Background(), env, &jobs.Job{ID: "j1", Params: w445Params(item)}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := out.(*tracedraft.Proposal)
	if p.Dropped != 1 || len(p.Reasons) != 1 || len(p.Guide.Calls) != 1 || *dones != 1 {
		t.Errorf("proposal = %+v, done called %d times", p, *dones)
	}
	for _, req := range fm.Seen {
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "sk-live0123456789abcdefgh") {
				t.Errorf("the model saw the key")
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(root, traceinbox.Dir, traceinbox.ItemsDir, item, handlers.ProposalFile))
	if err != nil {
		t.Fatal(err)
	}
	var stored tracedraft.Proposal
	if json.Unmarshal(data, &stored) != nil || stored.Item != item || stored.Origin != "model" || stored.Dropped != 1 {
		t.Errorf("proposal.json = %s", data)
	}
}

func TestTraceDraftValidate(t *testing.T) {
	root := t.TempDir()
	item := w445Item(t, root)
	env := jobEnv{ProjectRoot: root}
	for name, tc := range map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"no confirmation": {func(m map[string]any) { m["yes"] = false }, "Confirm the cost first."},
		"zero budget":     {func(m map[string]any) { m["budget_usd"] = 0 }, "Set a spending cap above zero."},
		"no model":        {func(m map[string]any) { m["model"] = "" }, "Pick a model first."},
		"unknown item":    {func(m map[string]any) { m["item"] = "x" }, "is not an inbox item id"},
	} {
		var m map[string]any
		_ = json.Unmarshal(w445Params(item), &m)
		tc.edit(m)
		raw, _ := json.Marshal(m)
		_, _, err := validateTraceDraft(context.Background(), env, raw)
		if err == nil || !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	norm, _, err := validateTraceDraft(context.Background(), env, w445Params(item))
	if err != nil || norm.(traceDraftParams).BudgetUSD != traceDraftMaxUSD {
		t.Errorf("budget not capped: %+v %v", norm, err)
	}
}

func TestTraceDraftNoModelCalls(t *testing.T) {
	root := t.TempDir()
	item := w445Item(t, root)
	old := openCaller
	openCaller = nil
	t.Cleanup(func() { openCaller = old })
	_, err := runTraceDraft(context.Background(), jobEnv{ProjectRoot: root}, &jobs.Job{ID: "j1", Params: w445Params(item)}, nil, nil)
	if err == nil || err.Error() != "this server makes no model calls" {
		t.Errorf("err = %v", err)
	}
}
