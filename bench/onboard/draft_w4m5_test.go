package onboard

import (
	"context"
	"testing"
)

// Rows that are all blank give a problem line that says why nothing was drafted.
func TestDraftRowsBlankRowsProblem(t *testing.T) {
	env := testEnv(t)
	in := Input{Data: "question,answer\n,\n , \n", Name: "blank.csv"}
	set, err := Draft(context.Background(), env, in, Plan{Suite: "blank"})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tasks) != 0 {
		t.Fatalf("tasks %d", len(set.Tasks))
	}
	found := false
	for _, p := range set.Problems {
		if p.Message == "No row has both a question and an answer. Blank rows are skipped." {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems %+v", set.Problems)
	}
}

// Dropping the last task clears the old readiness result of the set.
func TestDropLastTaskClearsReadiness(t *testing.T) {
	env := testEnv(t)
	ctx := context.Background()
	in := Input{Data: "question,answer\nWhat is the hotel cap per night?,250\n", Name: "one.csv"}
	set, err := Draft(ctx, env, in, Plan{Suite: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Tasks) != 1 {
		t.Fatalf("tasks %d problems %+v", len(set.Tasks), set.Problems)
	}
	set.Readiness = &Readiness{Status: "failed", Error: "old"}
	if err := set.EditTask(ctx, env, set.Tasks[0].ID, TaskEdit{Drop: true}); err != nil {
		t.Fatal(err)
	}
	if set.Readiness != nil {
		t.Fatalf("readiness kept: %+v", set.Readiness)
	}
}
