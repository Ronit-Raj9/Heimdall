package gwfeature

import (
	"math"
	"testing"
)

func run(task, arm string, success bool) Run {
	return Run{RunID: task + arm, Task: task, Arm: arm, Success: &success}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPairedTasksMeanOfTaskMeans(t *testing.T) {
	runs := []Run{
		run("t1", "A", false), run("t1", "A", true), run("t1", "B", true), run("t1", "B", true), // +0.5
		run("t2", "A", true), run("t2", "B", true), // 0
		run("t3", "A", true), run("t3", "B", false), run("t3", "B", true), // -0.5
		{RunID: "x", Task: "t3", Arm: "B"}, // unknown success: ignored
	}
	p := PairedTasks(runs, "A", "B", SolveValue)
	if p.Tasks != 3 || !near(p.Delta.Point, 0) {
		t.Fatalf("paired %+v", p)
	}
	if p.Delta.Low == nil || p.Delta.High == nil || p.P == nil {
		t.Fatalf("interval and p expected: %+v", p)
	}
	runs[0] = run("t1", "A", true)
	runs[1] = run("t1", "A", true)
	runs[7] = run("t3", "B", true)
	p = PairedTasks(runs, "A", "B", SolveValue)
	if !near(p.Delta.Point, 0) {
		t.Fatalf("all equal: %+v", p)
	}
	runs[4] = run("t2", "A", false)
	p = PairedTasks(runs, "A", "B", SolveValue)
	if !near(p.Delta.Point, 1.0/3) {
		t.Fatalf("one task up: %+v", p)
	}
}

func TestPairedTasksSkipsOneSidedTasks(t *testing.T) {
	runs := []Run{run("t1", "A", true), run("t1", "B", false), run("t2", "A", true), run("t3", "B", true)}
	p := PairedTasks(runs, "A", "B", SolveValue)
	if p.Tasks != 1 || !near(p.Delta.Point, -1) {
		t.Fatalf("paired %+v", p)
	}
	if p := PairedTasks(nil, "A", "B", SolveValue); p.Tasks != 0 || p.P != nil {
		t.Fatalf("no task: %+v", p)
	}
}

func TestPairedTasksIntervalNeedsTwoTasks(t *testing.T) {
	runs := []Run{run("t1", "A", true), run("t1", "B", false)}
	p := PairedTasks(runs, "A", "B", SolveValue)
	if p.Delta.Low != nil || p.Delta.High != nil {
		t.Fatalf("one task must have no interval: %+v", p)
	}
	runs = append(runs, run("t2", "A", true), run("t2", "B", true))
	p = PairedTasks(runs, "A", "B", SolveValue)
	if p.Delta.Low == nil || p.Delta.High == nil || *p.Delta.Low > p.Delta.Point || *p.Delta.High < p.Delta.Point {
		t.Fatalf("two tasks: %+v", p)
	}
}

func TestLatencyValueMedianOfSuccessfulMessages(t *testing.T) {
	r := Run{Rows: []Row{{Kind: "message", Status: "success", LatencyMS: 10}, {Kind: "message", Status: "success", LatencyMS: 30}, {Kind: "message", Status: "error", LatencyMS: 999}, {Kind: "models", Status: "success", LatencyMS: 999}, {Kind: "message", Status: "success", LatencyMS: 20}}}
	if v, ok := LatencyValue(r); !ok || v != 20 {
		t.Fatalf("latency %v %v", v, ok)
	}
	if _, ok := LatencyValue(Run{}); ok {
		t.Fatal("no rows must be unknown")
	}
}
