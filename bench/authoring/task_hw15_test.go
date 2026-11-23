package authoring

import (
	"context"
	"math"
	"testing"
)

// One assist call that writes several fields is counted once: its cost sits
// on the first field it wrote (fix wave 3, N278).
func TestAssistCallCostCountedOnce(t *testing.T) {
	f := &fakeModel{replies: map[string][]string{
		"grading criteria for one task":   {rubricReply},
		"For each grading criterion":      {criterionRepl},
		"plausible wrong answers":         {nearMissReply},
		"pass this task's checks without": {exploitReply},
	}}
	a, _ := newAssistant(t, f, 1)
	s := qaSuite(t, true)
	d, err := DraftTask(context.Background(), a, s, s.Tasks[0], TaskOptions{Ops: AllOps, NearMiss: 1})
	if err != nil {
		t.Fatal(err)
	}
	a.Close(context.Background())
	sum, perCall := 0.0, map[string]int{}
	for _, g := range d.Generated {
		sum += g.CostUSD
		perCall[g.Op+"|"+g.At]++
	}
	multi := false
	for _, n := range perCall {
		multi = multi || n > 1
	}
	if !multi {
		t.Fatalf("fixture has no call that wrote two fields: %+v", d.Generated)
	}
	if math.Abs(sum-d.CostUSD) > 1e-9 {
		t.Fatalf("fields sum to $%v, the calls cost $%v: %+v", sum, d.CostUSD, d.Generated)
	}
}
