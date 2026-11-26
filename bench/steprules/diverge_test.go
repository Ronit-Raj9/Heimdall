package steprules

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func turnReq(seq int, req string) schemas.StepRecord {
	return schemas.StepRecord{Seq: seq, Kind: schemas.StepModelTurn, RequestID: req, Summary: "thinking"}
}

func TestFirstDivergenceCommand(t *testing.T) {
	a := []schemas.StepRecord{turnReq(1, "a1"), cmd(2, "pip install -e .", "", 0), cmd(3, "pytest", "", 0)}
	b := []schemas.StepRecord{turnReq(1, "b1"), cmd(2, "pip install -e .", "", 0), cmd(3, "python setup.py build", "", 0)}
	d := FirstDivergence(a, b, DefaultDivergeOptions())
	if !d.Found || d.Category != DivCommand || d.SeqA != 3 || d.SeqB != 3 || d.TextA != "pytest" {
		t.Fatalf("got %+v", d)
	}
	if d.Note != DivergenceNote {
		t.Fatalf("note %q", d.Note)
	}
}

func TestFirstDivergenceToolResult(t *testing.T) {
	a := []schemas.StepRecord{{Seq: 1, Kind: schemas.StepToolExecution, ToolName: "fetch"}}
	b := []schemas.StepRecord{{Seq: 1, Kind: schemas.StepToolExecution, ToolName: "fetch", IsError: true}}
	d := FirstDivergence(a, b, DefaultDivergeOptions())
	if !d.Found || d.Category != DivToolResult {
		t.Fatalf("got %+v", d)
	}
}

func TestFirstDivergenceEnded(t *testing.T) {
	a := []schemas.StepRecord{cmd(1, "make", "", 0), cmd(2, "make install", "", 0)}
	b := []schemas.StepRecord{cmd(1, "make", "", 0)}
	d := FirstDivergence(a, b, DefaultDivergeOptions())
	if !d.Found || d.Category != DivEnded || d.SeqA != 2 || d.SeqB != 0 {
		t.Fatalf("got %+v", d)
	}
	if d.After[0].Steps != 1 || d.After[1].Steps != 0 {
		t.Fatalf("after %+v", d.After)
	}
	if same := FirstDivergence(b, b, DefaultDivergeOptions()); same.Found {
		t.Fatalf("equal lists: %+v", same)
	}
}

func TestFirstDivergenceIgnoresReads(t *testing.T) {
	a := []schemas.StepRecord{cmd(1, "cat README.md", "", 0), cmd(2, "pytest", "", 0)}
	b := []schemas.StepRecord{cmd(1, "pytest", "", 0)}
	if d := FirstDivergence(a, b, DefaultDivergeOptions()); d.Found {
		t.Fatalf("reads not skipped: %+v", d)
	}
	opt := DefaultDivergeOptions()
	opt.IgnoreReads = false
	if d := FirstDivergence(a, b, opt); !d.Found {
		t.Fatal("reads kept but no divergence")
	}
}

func TestFirstDivergenceCostAfter(t *testing.T) {
	a := []schemas.StepRecord{turnReq(1, "a1"), cmd(2, "pytest", "", 1), turnReq(3, "a2"), turnReq(4, "a3")}
	b := []schemas.StepRecord{turnReq(1, "b1"), cmd(2, "go build", "", 0), turnReq(3, "b2")}
	opt := DefaultDivergeOptions()
	prices := map[string]float64{"a2": 0.5, "a3": 0.25, "b2": 1}
	opt.CostOf = func(side int, req string) (float64, bool) { v, ok := prices[req]; return v, ok }
	d := FirstDivergence(a, b, opt)
	if !d.Found || d.After[0].CostUSD == nil || *d.After[0].CostUSD != 0.75 || d.After[0].Turns != 2 || d.After[0].TestRuns != 1 || d.After[0].Errors != 1 {
		t.Fatalf("after a: %+v", d.After[0])
	}
	if d.After[1].CostUSD == nil || *d.After[1].CostUSD != 1 {
		t.Fatalf("after b: %+v", d.After[1])
	}
	delete(prices, "a3")
	d = FirstDivergence(a, b, opt)
	if d.After[0].CostUSD != nil {
		t.Fatalf("one unknown cost should give nil, got %v", *d.After[0].CostUSD)
	}
}
