package modelswitch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestModelSwitchServedAsAsked: each run counts its model calls whose served
// model is known and the model asked for, the calls whose served model is
// unknown (a summary row that only repeats the requested id), and the
// distinct served ids.
func TestModelSwitchServedAsAsked(t *testing.T) {
	src, ev, _ := fixture()
	for _, lr := range src.runs["exp_1"] {
		if lr.Slot.Arm != "A" {
			continue
		}
		rows := []schemas.GatewayRequest{
			{ID: "1", Kind: "message", Status: "success", ModelRequested: "claude-sonnet-4-5", ModelServed: "claude-sonnet-4-5-20250929"},
			{ID: "2", Kind: "message", Status: "success", ModelRequested: "claude-sonnet-4-5", ModelServed: "claude-sonnet-4-5"},
			{ID: "3", Kind: "message", Status: "success", ModelRequested: "claude-sonnet-4-5", ModelServed: "claude-3-7-sonnet"},
			{ID: "4", Kind: "models", Status: "success", ModelRequested: "x", ModelServed: "y"},
		}
		b, _ := json.Marshal(map[string]any{"requests": schemas.RequestLog{Complete: true, Requests: rows}})
		src.files[lr.Run.RunID]["evidence.json"] = b
	}
	br, err := readBench(context.Background(), src, ev.Spec.Arms, ev.Children[0])
	if err != nil {
		t.Fatal(err)
	}
	a := br.arms["A"]
	if len(a) == 0 {
		t.Fatal("no runs for A")
	}
	for _, r := range a {
		if r.servedKnown != 2 || r.servedAsAsked != 1 || r.servedUnknown != 1 {
			t.Fatalf("run counts known %d as asked %d unknown %d", r.servedKnown, r.servedAsAsked, r.servedUnknown)
		}
		if r.servedModels["claude-3-7-sonnet"] != 1 || r.servedModels["claude-sonnet-4-5-20250929"] != 1 || len(r.servedModels) != 2 {
			t.Fatalf("served models %v", r.servedModels)
		}
	}
	for _, r := range br.arms["B"] {
		if r.servedKnown != 0 || r.servedUnknown != 0 {
			t.Fatalf("B without evidence counted %d known %d unknown", r.servedKnown, r.servedUnknown)
		}
	}
	m := Model{ServedAsAsked: schemas.Unknown("x", schemas.EvidenceGateway), ServedUnknown: 1, ServedModels: map[string]int{"a": 1}}
	if _, err := json.Marshal(m); err != nil {
		t.Fatal(err)
	}
}
