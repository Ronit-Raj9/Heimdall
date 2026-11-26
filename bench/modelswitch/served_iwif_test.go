package modelswitch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestModelSwitchReportServedAsAsked: the per-run served model counts of
// readBench reach each Model of the report (I26's fillModel snippet).
func TestModelSwitchReportServedAsAsked(t *testing.T) {
	src, ev, opt := fixture()
	for _, lr := range src.runs["exp_1"] {
		if lr.Slot.Arm != "A" {
			continue
		}
		rows := []schemas.GatewayRequest{
			{ID: "1", Kind: "message", Status: "success", ModelRequested: "claude-sonnet-4-5", ModelServed: "claude-sonnet-4-5-20250929"},
			{ID: "2", Kind: "message", Status: "success", ModelRequested: "claude-sonnet-4-5", ModelServed: "claude-sonnet-4-5"},
			{ID: "3", Kind: "message", Status: "success", ModelRequested: "claude-sonnet-4-5", ModelServed: "claude-3-7-sonnet"},
		}
		b, _ := json.Marshal(map[string]any{"requests": schemas.RequestLog{Complete: true, Requests: rows}})
		src.files[lr.Run.RunID]["evidence.json"] = b
	}
	rep, err := Build(context.Background(), src, ev, opt)
	if err != nil {
		t.Fatal(err)
	}
	var a, b *Model
	for i := range rep.Models {
		switch rep.Models[i].Label {
		case "A":
			a = &rep.Models[i]
		case "B":
			b = &rep.Models[i]
		}
	}
	if a == nil || b == nil {
		t.Fatalf("models %+v", rep.Models)
	}
	if v, ok := a.ServedAsAsked.Float(); !ok || v != 0.5 {
		t.Fatalf("A served as asked %+v", a.ServedAsAsked)
	}
	if a.ServedUnknown != 32 || a.ServedModels["claude-sonnet-4-5-20250929"] != 32 || a.ServedModels["claude-3-7-sonnet"] != 32 || len(a.ServedModels) != 2 {
		t.Fatalf("A unknown %d served %v", a.ServedUnknown, a.ServedModels)
	}
	if _, ok := b.ServedAsAsked.Float(); ok || b.ServedAsAsked.State != schemas.ValueUnknown || b.ServedAsAsked.Reason == "" {
		t.Fatalf("B without served models must be unknown with a reason: %+v", b.ServedAsAsked)
	}
}
