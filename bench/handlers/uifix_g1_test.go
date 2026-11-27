package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRunListRowTaskTitle(t *testing.T) {
	s := &schemas.Suite{ID: "s", Tasks: []*schemas.Task{{ID: "t1", Title: " Fix the parser "}, {ID: "t2"}}}
	suites := map[string]suiteFacts{"s": suiteFactsOf(s)}
	row := runListRow(RunView{Slot: schemas.Slot{Suite: "s", Task: "t1"}}, suites)
	if row.TaskTitle != "Fix the parser" {
		t.Fatalf("title %q", row.TaskTitle)
	}
	if row := runListRow(RunView{Slot: schemas.Slot{Suite: "s", Task: "t2"}}, suites); row.TaskTitle != "" {
		t.Fatalf("untitled task %q", row.TaskTitle)
	}
	if row := runListRow(RunView{Slot: schemas.Slot{Suite: "gone", Task: "t1"}}, suites); row.TaskTitle != "" {
		t.Fatalf("unknown suite %q", row.TaskTitle)
	}
}
