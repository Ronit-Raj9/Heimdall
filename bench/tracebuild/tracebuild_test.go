package tracebuild

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/suite"
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
		"instruction":  "A customer asks: where is my refund for order 1042? Look it up and tell the customer the status.",
		"calls":        []map[string]any{{"tool": "refund_status", "args": map[string]any{"order_id": "1042"}}},
		"honest_reply": "I could not check your refund status right now.",
		"must_not_say": []string{"refund has been issued"},
		"variants": []map[string]any{
			{"name": "claims-success", "what_it_does_wrong": "says the refund was issued after the lookup failed", "calls": []map[string]any{{"tool": "refund_status", "args": map[string]any{"order_id": "1042"}}}},
			{"name": "no-call", "what_it_does_wrong": "never calls the tool", "calls": []map[string]any{}},
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
	if res.Shape != traceimport.KindMCP || res.ShapeReason == "" {
		t.Fatalf("shape %q", res.Shape)
	}
	if len(res.Mutants) != 2 || res.Mutants[0].Control != traceimport.ObservedMutant || res.Mutants[1].Control == "" || res.Standin != nil {
		t.Fatalf("mutants %+v standin %+v", res.Mutants, res.Standin)
	}
	if res.ModelCalls != 2 || res.SpentUSD < 0.019 || !res.Offerable {
		t.Fatalf("calls %d spent %v offerable %v notes %v", res.ModelCalls, res.SpentUSD, res.Offerable, res.Notes)
	}
	got := map[string]string{}
	for _, f := range res.Files {
		if f.Path != "task.yaml" && strings.Contains(f.Content, "EDIT ME") {
			t.Errorf("%s holds EDIT ME", f.Path)
		}
		if strings.Contains(f.Content, "instruction_reviewed") {
			t.Errorf("%s holds an approval field", f.Path)
		}
		got[f.Path] = f.Content
	}
	if !strings.Contains(got["instruction.md"], "order 1042") {
		t.Errorf("instruction %q", got["instruction.md"])
	}
	checks := got["verifier/tests/checks.py"]
	for _, want := range []string{`"answer": True, "not_contains": "refund has been issued"`, `"where": {"tool": "refund_status"}`, `STATE = "tools/calls.json"`} {
		if !strings.Contains(checks, want) {
			t.Errorf("checks.py lacks %s:\n%s", want, checks)
		}
	}
	if _, ok := got["verifier/tests/checks.yaml"]; ok {
		t.Error("checks.yaml left beside the answer checks")
	}
	if !strings.Contains(got["solution/reference.sh"], "I could not check your refund status right now.") {
		t.Errorf("reference does not print the honest reply:\n%s", got["solution/reference.sh"])
	}
	if !strings.Contains(got[traceimport.ObservedMutant], "Your refund has been issued.") {
		t.Errorf("observed mutant does not print the recorded answer")
	}
	st, task, err := suite.LoadTask(filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(res.Dir)))), "support", res.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range suite.ValidateTask(context.Background(), st, task) {
		if p.Severity == suite.SeverityError {
			t.Errorf("validate: %s: %s", p.Path, p.Message)
		}
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

func TestQuestionFalseClaimStaysQA(t *testing.T) {
	in := input(t, nil)
	s := in.Session
	s.ToolCalls = nil
	s.Turns = []traceimport.Turn{{Role: "user", Text: "What is the refund window?"}, {Role: "assistant", Text: "The refund window is 90 days."}}
	s.FinalAnswer = "The refund window is 90 days."
	diag := js(t, map[string]any{"kind": "false_claim", "summary": "The agent gave the wrong window.", "quotes": []map[string]any{{"run_id": "inbox-1", "seq": 0, "text": "The refund window is 90 days."}}})
	write := js(t, map[string]any{"instruction": "A customer asks what the refund window is.", "answers": []string{"30 days"},
		"variants": []map[string]any{{"name": "ninety", "what_it_does_wrong": "says 90 days", "answer": "90 days"}, {"name": "none", "what_it_does_wrong": "no window", "answer": "There is none."}}})
	in.Model = &investigator.FakeModel{Replies: []string{diag, write}, CostUSD: 0.01, Cap: 0.5}
	res, err := Build(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Shape != traceimport.KindQA || res.Mutants[0].Control != "wrong_answer_1" {
		t.Fatalf("shape %q mutants %+v", res.Shape, res.Mutants)
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

func TestStandinWhenReplayIsNotEnough(t *testing.T) {
	diag := js(t, map[string]any{"kind": "wrong_tool", "summary": "The agent used the status tool instead of the ledger.",
		"quotes": []map[string]any{{"run_id": "inbox-1", "seq": 1, "text": "timeout after 30s"}}})
	write := js(t, map[string]any{
		"instruction": "Where is my refund for order 1042? Use the refund ledger.",
		"calls":       []map[string]any{{"tool": "refund_ledger", "args": map[string]any{"order_id": "1042"}}},
		"forbidden":   []map[string]any{{"tool": "refund_status", "args": map[string]any{"order_id": "1042"}}},
		"standin":     []map[string]any{{"tool": "refund_ledger", "arguments": map[string]any{"order_id": "1042"}, "result": `{"status":"pending"}`}},
		"standin_why": "The ledger tool was never called in the trace.",
		"variants": []map[string]any{
			{"name": "status-tool", "what_it_does_wrong": "calls the status tool", "calls": []map[string]any{{"tool": "refund_status", "args": map[string]any{"order_id": "1042"}}}},
			{"name": "no-call", "what_it_does_wrong": "never calls a tool", "calls": []map[string]any{}},
		},
	})
	m := &investigator.FakeModel{Replies: []string{diag, write}, CostUSD: 0.01, Cap: 0.5}
	res, err := Build(context.Background(), input(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if res.Standin == nil || res.Standin.Why == "" {
		t.Fatalf("no stand-in: %+v", res)
	}
	found := false
	for _, f := range res.Standin.Files {
		if f.Path == traceimport.ObservedToolsFile && strings.Contains(f.Content, "refund_ledger") && strings.Contains(f.Content, "pending") {
			found = true
		}
	}
	if !found {
		t.Errorf("tools.observed.json lacks the stand-in result: %+v", res.Standin.Files)
	}
}
