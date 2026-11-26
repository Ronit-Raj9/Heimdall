package tracedraft

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// fakeSession is a made-up session (never a real file). It plants a key and
// a home folder path, which must never reach the model.
func fakeSession() *traceimport.Session {
	return &traceimport.Session{
		ID:     "s1",
		Format: traceimport.FormatClaudeCode,
		Turns: []traceimport.Turn{
			{Role: "user", Text: "Refund order A-1001 to the customer. Key sk-live0123456789abcdefgh in /home/planted-user/x"},
			{Role: "assistant", Text: "I could not refund the order."},
		},
		ToolCalls: []traceimport.ToolCall{
			{Seq: 1, Name: "refund_order", Arguments: json.RawMessage(`{"order_id":"A-1001"}`), Result: "order not found", IsError: true},
		},
		FinalAnswer: "I could not refund the order.",
		Outcome:     traceimport.Outcome{Status: "unknown", Hints: []string{"1 of 1 tool calls failed"}},
		Workspace:   &traceimport.Workspace{Cwd: "/home/planted-user/shop"},
	}
}

func reply(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNormalizeFolds(t *testing.T) {
	for in, want := range map[string]string{
		"“hello”   world":       `"hello" world`,
		"**bold** and `code`":   "bold and code",
		`line one\nline two`:    "line one line two",
		"...the middle part…":   "the middle part",
		"  It’s _fine_ \t now ": "It's fine now",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckQuotes(t *testing.T) {
	src := SessionSource("item-1", fakeSession())
	res := CheckQuotes([]Quote{
		{RunID: "item-1", Seq: 0, Text: "I could not refund the order."},
		{RunID: "item-1", Seq: 1, Text: "order not found"},
		{RunID: "item-1", Seq: 0, Text: "I could not refund the money."},
		{RunID: "item-1", Seq: 7, Text: "order not found"},
		{RunID: "other", Seq: 1, Text: "order not found"},
		{RunID: "item-1", Seq: 0, Text: "short"},
		{RunID: "item-1", Seq: 0, Text: strings.Repeat("x", 241)},
	}, src)
	want := []string{"", "", "not found in the step", "no such step", "no such step", "too short", "too long"}
	for i, r := range res {
		if (want[i] == "") != r.OK || r.Reason != want[i] {
			t.Errorf("quote %d: %+v, want reason %q", i, r, want[i])
		}
	}
	if res[2].Closest == "" {
		t.Errorf("a paraphrase gets the closest passage: %+v", res[2])
	}
}

// TestTraceDraftJobWritesProposal (package part): the model's draft is
// parsed, a quote not in the session is dropped and counted, a tool not in
// the session is dropped, and what the model saw is redacted.
func TestDraftDropsUnverified(t *testing.T) {
	first := modelDraft{
		Guide: GuideDraft{Calls: []CallDraft{{Tool: "refund_order", Args: map[string]any{"order_id": "A-1001"}}, {Tool: "delete_everything"}},
			Forbidden: []CallDraft{{Tool: "refund_order", Args: map[string]any{"order_id": "A-9"}}}, ReplyRubric: "Says the refund went through."},
		Reasons: []ProposalReason{
			{Field: "calls", Text: "The refund call failed.", Quotes: []Quote{{RunID: "item-1", Seq: 1, Text: "order not found"}}},
			{Field: "reply_rubric", Text: "Made up.", Quotes: []Quote{{RunID: "item-1", Seq: 0, Text: "the refund was issued in full"}}},
		},
	}
	repaired := first
	fm := &investigator.FakeModel{Replies: []string{reply(t, first), reply(t, repaired)}, CostUSD: 0.01, Cap: 1}
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	p, err := Draft(context.Background(), fm, Input{Item: "item-1", Kind: "tool-use", Session: fakeSession(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(fm.Seen) != 2 {
		t.Fatalf("calls = %d, want the draft and one repair round", len(fm.Seen))
	}
	for _, req := range fm.Seen {
		for _, m := range req.Messages {
			for _, planted := range []string{"sk-live0123456789abcdefgh", "planted-user"} {
				if strings.Contains(m.Content, planted) {
					t.Errorf("the model saw %q", planted)
				}
			}
		}
	}
	if len(p.Guide.Calls) != 1 || p.Guide.Calls[0].Tool != "refund_order" {
		t.Errorf("calls = %+v", p.Guide.Calls)
	}
	if len(p.Reasons) != 1 || p.Reasons[0].Field != "calls" {
		t.Errorf("reasons = %+v", p.Reasons)
	}
	if p.Dropped != 2 { // one unverified reason, one unknown tool
		t.Errorf("dropped = %d", p.Dropped)
	}
	if p.Origin != "model" || p.Item != "item-1" || p.Kind != "tool-use" || p.CostUSD != 0.02 || !p.CreatedAt.Equal(now) {
		t.Errorf("proposal = %+v", p)
	}
	if p.Guide.ReplyRubric == "" {
		t.Errorf("a field whose reason failed stays: %+v", p.Guide)
	}
}

type servedCaller struct{ investigator.FakeModel }

func (c *servedCaller) Call(ctx context.Context, req investigator.Request) (investigator.Reply, error) {
	r, err := c.FakeModel.Call(ctx, req)
	r.Model, r.Served = "openai/cheap", "openai/other"
	return r, err
}

func TestTraceDraftRefusesOtherServedModel(t *testing.T) {
	c := &servedCaller{investigator.FakeModel{Replies: []string{`{"guide":{},"reasons":[]}`}, CostUSD: 0.01, Cap: 1}}
	_, err := Draft(context.Background(), c, Input{Item: "item-1", Kind: "qa", Session: fakeSession()})
	if err == nil || err.Error() != "The gateway answered with openai/other, not openai/cheap. Nothing was drafted." {
		t.Fatalf("err = %v", err)
	}
}

func TestDraftRefusesBadReply(t *testing.T) {
	fm := &investigator.FakeModel{Replies: []string{"not json"}, CostUSD: 0.01, Cap: 1}
	if _, err := Draft(context.Background(), fm, Input{Item: "item-1", Kind: "qa", Session: fakeSession()}); err == nil {
		t.Fatal("a reply that is not JSON must fail")
	}
}
