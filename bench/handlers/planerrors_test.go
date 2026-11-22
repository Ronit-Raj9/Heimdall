package handlers

import (
	"errors"
	"strings"
	"testing"
)

func TestPlainPlanErrorDraftTasks(t *testing.T) {
	got := PlainPlanError(errors.New("plan e_x_ab12: suite lock: task file-inbox is draft, not admitted; task mail is draft, not admitted"))
	want := "This benchmark has tasks that have not passed the readiness check yet: file-inbox, mail. Pick a ready benchmark, choose only ready tasks, or run the readiness check on the benchmark page."
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	var parts []string
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		parts = append(parts, "task "+id+" is draft, not admitted")
	}
	got = PlainPlanError(errors.New("plan e_x: suite lock: " + strings.Join(parts, "; ")))
	if !strings.Contains(got, "a, b, c, d, e and 2 more.") {
		t.Fatalf("long list %q", got)
	}
	got = PlainPlanError(errors.New("plan e_x: suite lock: suite acme@1.0.0 has no lock"))
	if got != "Benchmark acme is not frozen yet. Freeze it and run its readiness check on the benchmark page." {
		t.Fatalf("no lock %q", got)
	}
	got = PlainPlanError(errors.New("plan e_x: suite lock: task t1 admission was recorded for other lock fingerprints, so it is no longer admitted; run suite admit"))
	if got != "Task t1 changed after its readiness check. Run the readiness check again." {
		t.Fatalf("stale %q", got)
	}
}

// The planner names a fresh experiment id on every plan; two plans of the
// same experiment read the same (N44).
func TestPlainPlanErrorStripsID(t *testing.T) {
	a := PlainPlanError(errors.New("plan e_demo_ab12: unknown agent agents/x.yaml"))
	b := PlainPlanError(errors.New("plan e_demo_cd34: unknown agent agents/x.yaml"))
	if a != b || a != "unknown agent agents/x.yaml" {
		t.Fatalf("got %q and %q", a, b)
	}
	if PlainPlanError(nil) != "" {
		t.Fatal("nil error")
	}
}
