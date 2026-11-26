package tracedraft

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/investigator"
)

// Group E security review: the proposal never holds text the redactor would
// remove, even when the model writes a key, an email or a home folder name
// of its own into the guide or a reason. A verified quote stays.
func TestProposalIsRedacted(t *testing.T) {
	planted := []string{"sk-live9876543210zyxwvuts", "pat.person@example.org", "/home/someone-else"}
	draft := modelDraft{
		Guide: GuideDraft{
			Instruction: "Refund order A-1001; mail pat.person@example.org when done.",
			Calls:       []CallDraft{{Tool: "refund_order", Args: map[string]any{"order_id": "A-1001", "api_key": "sk-live9876543210zyxwvuts"}}},
			ReplyRubric: "Says the refund went through and names /home/someone-else/notes.",
		},
		Reasons: []ProposalReason{
			{Field: "calls", Text: "The call with sk-live9876543210zyxwvuts failed.", Quotes: []Quote{{RunID: "item-1", Seq: 1, Text: "order not found"}}},
		},
	}
	fm := &investigator.FakeModel{Replies: []string{reply(t, draft)}, CostUSD: 0.01, Cap: 1}
	p, err := Draft(context.Background(), fm, Input{Item: "item-1", Kind: "tool-use", Session: fakeSession(), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range append(planted, "planted-user", "sk-live0123456789abcdefgh") {
		if strings.Contains(string(b), s) {
			t.Errorf("the proposal holds %q: %s", s, b)
		}
	}
	if len(p.Reasons) != 1 || len(p.Reasons[0].Quotes) != 1 || p.Reasons[0].Quotes[0].Text != "order not found" {
		t.Errorf("the verified quote must stay: %+v", p.Reasons)
	}
	if len(p.Guide.Calls) != 1 || p.Guide.Calls[0].Args["order_id"] != "A-1001" {
		t.Errorf("call args lost: %+v", p.Guide.Calls)
	}
}
