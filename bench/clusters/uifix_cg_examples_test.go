package clusters

import (
	"context"
	"testing"
	"time"
)

// TestExampleRuns: every example run id comes with its benchmark and task,
// in the same order.
func TestExampleRuns(t *testing.T) {
	rep, err := Build(context.Background(), fixture(), Query{Now: day.Add(96 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	top := rep.Clusters[0]
	if len(top.ExampleRuns) != len(top.Examples) {
		t.Fatalf("%d example runs for %d examples", len(top.ExampleRuns), len(top.Examples))
	}
	for i, e := range top.ExampleRuns {
		if e.RunID != top.Examples[i] || e.Suite != "coding-core" || e.Task != "t01" || e.ExpID == "" {
			t.Fatalf("example %d: %+v", i, e)
		}
	}
}
