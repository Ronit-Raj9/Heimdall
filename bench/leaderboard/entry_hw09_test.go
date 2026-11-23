package leaderboard

import (
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestEntryTasksSolved is the leaderboard row of N295 (fix wave 3): 8 tasks,
// one never solved, 11 graded runs with 10 passes. The headline is the mean
// over tasks (87.5%), so its caption counts tasks on the same basis: 7 of 8
// tasks solved, not "10 of 11 solved".
func TestEntryTasksSolved(t *testing.T) {
	exp := &expInfo{id: "e_1", m: &schemas.Manifest{ExpID: "e_1"}, trials: map[string]int{trialKey("A", "clean"): 1}}
	var runs []gradedRun
	add := func(task string, success float64) {
		n := len(runs)
		runs = append(runs, gradedRun{success: success, rec: &record{exp: exp, sealed: true,
			slot: schemas.Slot{SlotID: fmt.Sprintf("s%d", n), Task: task, Arm: "A", Condition: "clean", Trial: 1, Counted: true}}})
	}
	for i := 1; i <= 7; i++ {
		add(fmt.Sprintf("t%d", i), 1)
		if i <= 3 {
			add(fmt.Sprintf("t%d", i), 1)
		}
	}
	add("t8", 0)
	e := newEntry("h", runs, runs, 0).e
	if e.Runs != 11 || e.Passes.Num != 10 || e.Tasks != 8 {
		t.Fatalf("runs %d passes %d/%d tasks %d", e.Runs, e.Passes.Num, e.Passes.Den, e.Tasks)
	}
	if x, ok := e.Success.Float(); !ok || x != 0.875 {
		t.Fatalf("success %v", e.Success)
	}
	if e.TasksSolved != 7 {
		t.Fatalf("tasks_solved %d, want 7", e.TasksSolved)
	}
}

// TestMarkdownRankingOneDefinitionPerCell: the copied table shows tasks
// solved next to the task mean and says reliability was not measured when no
// task was repeated (N295).
func TestMarkdownRankingOneDefinitionPerCell(t *testing.T) {
	e := schemas.LeaderboardEntry{Rank: 1, Tier: 1, Agent: "codex", ConfigHash: "h1234567890", Tasks: 8, TasksSolved: 7, Runs: 11, Trials: 1,
		Passes:      schemas.Ratio{Num: 10, Den: 11},
		Success:     schemas.Observed(0.875, schemas.EvidenceOutcome),
		Reliability: schemas.Unknown("pass^k needs 2 or more trials per task; its experiments ran 1", schemas.EvidenceOutcome),
		Metrics:     map[string]schemas.Value{}}
	var b strings.Builder
	writeRanking(&b, []schemas.LeaderboardEntry{e})
	out := b.String()
	for _, want := range []string{"Tasks solved", "| 7/8 |", "not measured (needs 2 or more tries per task; this ran 1)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ranking lacks %q:\n%s", want, out)
		}
	}
}
