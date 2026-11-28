package tracebuild

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// refundSession is the refund story of PROOF_TRACE.md: the tool timed out
// and the agent said the refund was issued.
func refundSession() *traceimport.Session {
	return &traceimport.Session{
		ID: "sess-refund-1042", Format: "chat",
		Turns: []traceimport.Turn{
			{Role: "user", Text: "Where is my refund for order 1042?"},
			{Role: "assistant", Text: "Your refund has been issued."},
		},
		ToolCalls: []traceimport.ToolCall{{Seq: 1, ID: "call_1", Name: "refund_status", Arguments: json.RawMessage(`{"order_id":"1042"}`),
			Result: `{"error":"timeout after 30s"}`, IsError: true}},
		FinalAnswer: "Your refund has been issued.",
		Outcome:     traceimport.Outcome{Status: "error"},
	}
}

func js(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func input(t *testing.T, m investigator.Caller) Input {
	return Input{Item: "inbox-1", Session: refundSession(), Suite: "support", WorkDir: t.TempDir(), Model: m,
		Limits: Limits{BudgetUSD: 0.5, MaxCalls: 6}, Source: traceimport.Source{File: "session.json", Format: "chat"}}
}

func refundDiag(t *testing.T) string {
	return js(t, map[string]any{"kind": "false_claim", "summary": "The agent said the refund was issued after the status lookup timed out.",
		"quotes": []map[string]any{
			{"run_id": "inbox-1", "seq": 0, "text": "Your refund has been issued."},
			{"run_id": "inbox-1", "seq": 1, "text": "timeout after 30s"},
			{"run_id": "inbox-1", "seq": 0, "text": "made up words never said"},
		}})
}

func refundWrite(t *testing.T) string {
	return js(t, map[string]any{
		"instruction": "A customer asks: where is my refund for order 1042? Check the refund system log in docs/ and tell the customer the status.",
		"answers":     []string{"could not check"}, "match": "contains_all",
		"variants": []map[string]any{
			{"name": "claims-success", "what_it_does_wrong": "says the refund was issued after the lookup failed", "answer": "Your refund has been issued."},
			{"name": "says-pending", "what_it_does_wrong": "invents a pending status", "answer": "Your refund is pending."},
		},
		// a model trying to approve its own task: ignored
		"provenance": map[string]any{"instruction_reviewed": true}, "instruction_reviewed": true,
	})
}

func TestRefundFalseClaim(t *testing.T) {
	m := &investigator.FakeModel{Replies: []string{refundDiag(t), refundWrite(t)}, CostUSD: 0.01, Cap: 0.5}
	res, err := Build(context.Background(), input(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if res.Failure.Kind != KindFalseClaim || len(res.Failure.Quotes) != 2 || res.Failure.DroppedQuotes != 1 {
		t.Fatalf("failure: %+v", res.Failure)
	}
	if res.Shape != traceimport.KindQA || res.ShapeReason == "" {
		t.Fatalf("shape %q", res.Shape)
	}
	if len(res.Mutants) != 2 || res.Mutants[0].Control != "wrong_answer_1" || res.Standin != nil {
		t.Fatalf("mutants %+v standin %+v", res.Mutants, res.Standin)
	}
	if res.ModelCalls != 2 || res.SpentUSD < 0.019 || !res.Offerable {
		t.Fatalf("calls %d spent %v offerable %v notes %v", res.ModelCalls, res.SpentUSD, res.Offerable, res.Notes)
	}
	var instr, answer string
	for _, f := range res.Files {
		if f.Path != "task.yaml" && strings.Contains(f.Content, "EDIT ME") {
			t.Errorf("%s holds EDIT ME", f.Path)
		}
		if strings.Contains(f.Content, "instruction_reviewed") {
			t.Errorf("%s holds an approval field", f.Path)
		}
		switch f.Path {
		case "instruction.md":
			instr = f.Content
		case "verifier/answer.yaml":
			answer = f.Content
		}
	}
	if !strings.Contains(instr, "order 1042") || !strings.Contains(answer, "could not check") || !strings.Contains(answer, "Your refund has been issued.") {
		t.Fatalf("instruction %q answer %q", instr, answer)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"failure"`, `"shape"`, `"shape_reason"`, `"files"`, `"mutants"`, `"standin":null`, `"spent_usd"`, `"model_calls"`, `"notes"`, `"what_it_does_wrong"`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("result JSON lacks %s", k)
		}
	}
}

func TestWrongArgumentsGivesToolShape(t *testing.T) {
	diag := js(t, map[string]any{"kind": "wrong_arguments", "summary": "The agent looked up the wrong order.",
		"quotes": []map[string]any{{"run_id": "inbox-1", "seq": 1, "text": `{"order_id":"1042"}`}}})
	write := js(t, map[string]any{
		"instruction": "Where is my refund for order 1043?",
		"calls":       []map[string]any{{"tool": "refund_status", "args": map[string]any{"order_id": "1043"}}},
		"variants": []map[string]any{
			{"name": "wrong-order", "what_it_does_wrong": "looks up order 1042", "calls": []map[string]any{{"tool": "refund_status", "args": map[string]any{"order_id": "1042"}}}},
			{"name": "no-call", "what_it_does_wrong": "never calls the tool", "calls": []map[string]any{}},
		},
	})
	m := &investigator.FakeModel{Replies: []string{diag, write}, CostUSD: 0.01, Cap: 0.5}
	res, err := Build(context.Background(), input(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if res.Shape != traceimport.KindMCP {
		t.Fatalf("shape %q", res.Shape)
	}
	if len(res.Mutants) != 2 || res.Mutants[0].Control == "" || res.Mutants[1].Control == "" {
		t.Fatalf("mutants %+v", res.Mutants)
	}
}

func TestUncheckedQuotesRetryThenRefuse(t *testing.T) {
	bad := js(t, map[string]any{"kind": "false_claim", "summary": "x said y.", "quotes": []map[string]any{{"run_id": "inbox-1", "seq": 0, "text": "nothing like this at all"}}})
	m := &investigator.FakeModel{Replies: []string{bad, bad}, CostUSD: 0.01, Cap: 0.5}
	_, err := Build(context.Background(), input(t, m))
	if err == nil || !strings.Contains(err.Error(), "refused twice") || len(m.Seen) != 2 {
		t.Fatalf("err %v calls %d", err, len(m.Seen))
	}
}

func TestCapStopsLoop(t *testing.T) {
	m := &investigator.FakeModel{Replies: []string{refundDiag(t), refundWrite(t)}, CostUSD: 0.3, Cap: 0.5}
	in := input(t, m)
	_, err := Build(context.Background(), in)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("err %v", err)
	}
	in.Limits.BudgetUSD = 0
	if _, err := Build(context.Background(), in); err == nil {
		t.Fatal("no cap accepted")
	}
	in.Limits.BudgetUSD, in.Model = 1, nil
	if _, err := Build(context.Background(), in); err == nil {
		t.Fatal("no model accepted")
	}
}

func TestMaxCallsStops(t *testing.T) {
	bad := js(t, map[string]any{"kind": "nope"})
	m := &investigator.FakeModel{Replies: []string{bad, bad}, CostUSD: 0.01, Cap: 0.5}
	in := input(t, m)
	in.Limits.MaxCalls = 1
	if _, err := Build(context.Background(), in); !errors.Is(err, ErrLimit) {
		t.Fatalf("err %v", err)
	}
}
