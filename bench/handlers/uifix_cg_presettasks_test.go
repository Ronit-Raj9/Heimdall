package handlers

import "testing"

func TestPresetTasks(t *testing.T) {
	per := []EvalBenchmarkEstimate{{Tasks: 10}, {Tasks: 4}, {Tasks: 0, Skipped: "no task"}}
	if n, all := presetTasks(per, 10); n != 14 || !all {
		t.Fatalf("%d %v", n, all)
	}
	if n, all := presetTasks(per[:1], 10); n != 10 || all {
		t.Fatalf("%d %v", n, all)
	}
}
