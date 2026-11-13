package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func tr(stage schemas.Stage, status schemas.TransitionStatus) schemas.Transition {
	return schemas.Transition{Stage: stage, Status: status, At: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
}

func appendRaw(t *testing.T, p, data string) {
	t.Helper()
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSlotRunsOrderAndSealed(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	// Attempt 2 is created first; SlotRuns must still order by attempt.
	r2 := mustNewRun(t, s, m, "s_a1", "r_second", 2, schemas.OutcomePass)
	r1 := mustNewRun(t, s, m, "s_a1", "r_first", 1, schemas.OutcomeAbortedInfra)
	for _, st := range []schemas.Stage{schemas.StagePrepare, schemas.StageArm} {
		if err := s.AppendTransition(ctx, r1, tr(st, schemas.TransitionCompleted)); err != nil {
			t.Fatalf("AppendTransition: %v", err)
		}
	}
	mustSeal(t, s, r1)
	if err := s.AppendTransition(ctx, r2, tr(schemas.StagePrepare, schemas.TransitionStarted)); err != nil {
		t.Fatalf("AppendTransition: %v", err)
	}
	recs, err := s.SlotRuns(ctx, "e_test", "s_a1")
	if err != nil {
		t.Fatalf("SlotRuns: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d runs, want 2", len(recs))
	}
	tests := []struct {
		idx         int
		runID       string
		sealed      bool
		transitions int
	}{
		{0, "r_first", true, 2},
		{1, "r_second", false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.runID, func(t *testing.T) {
			got := recs[tc.idx]
			if got.Run.RunID != tc.runID || got.Sealed != tc.sealed || len(got.Transitions) != tc.transitions {
				t.Fatalf("rec %d = {%s sealed=%v transitions=%d}, want {%s %v %d}",
					tc.idx, got.Run.RunID, got.Sealed, len(got.Transitions), tc.runID, tc.sealed, tc.transitions)
			}
			if got.Dir != s.runDir(&got.Run) {
				t.Fatalf("dir = %s", got.Dir)
			}
		})
	}
	if err := s.AppendTransition(ctx, r1, tr(schemas.StageSeal, schemas.TransitionCompleted)); !errors.Is(err, schemas.ErrSealed) {
		t.Fatalf("append after seal: err = %v, want ErrSealed", err)
	}
	if _, err := s.SlotRuns(ctx, "e_test", "s_nope"); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("unknown slot err = %v", err)
	}
}

func TestTransitionsTornLine(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		tail        string // raw bytes appended after two good lines
		appendAfter bool   // append one more transition through the store
		want        int
		tornWarns   int
		corruptWarn int
	}{
		{name: "clean", want: 2},
		{name: "torn last line ignored", tail: `{"stage":"gra`, want: 2, tornWarns: 1},
		{name: "append after torn line", tail: `{"stage":"gra`, appendAfter: true, want: 3, corruptWarn: 1},
		{name: "complete last line without newline", tail: `{"stage":"grade","status":"started","at":"2026-11-14T10:00:00Z"}`, want: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			log := &recLogger{}
			s := newTestStore(t, log)
			m := mustCreateExp(t, s, "e_test")
			r := mustNewRun(t, s, m, "s_b1", "r_1", 1, schemas.OutcomePass)
			for _, st := range []schemas.Stage{schemas.StagePrepare, schemas.StageArm} {
				if err := s.AppendTransition(ctx, r, tr(st, schemas.TransitionCompleted)); err != nil {
					t.Fatalf("AppendTransition: %v", err)
				}
			}
			if tc.tail != "" {
				appendRaw(t, filepath.Join(s.runDir(r), "transitions.jsonl"), tc.tail)
			}
			if tc.appendAfter {
				if err := s.AppendTransition(ctx, r, tr(schemas.StageRun, schemas.TransitionStarted)); err != nil {
					t.Fatalf("AppendTransition: %v", err)
				}
			}
			recs, err := s.SlotRuns(ctx, "e_test", "s_b1")
			if err != nil {
				t.Fatalf("SlotRuns must not fail on a torn line: %v", err)
			}
			if got := len(recs[0].Transitions); got != tc.want {
				t.Fatalf("transitions = %d, want %d", got, tc.want)
			}
			if got := log.count("torn last transition line"); got != tc.tornWarns {
				t.Fatalf("torn warnings = %d, want %d", got, tc.tornWarns)
			}
			if got := log.count("corrupt transition line"); got != tc.corruptWarn {
				t.Fatalf("corrupt warnings = %d, want %d", got, tc.corruptWarn)
			}
			if tc.appendAfter && recs[0].Transitions[2].Stage != schemas.StageRun {
				t.Fatalf("last transition = %+v", recs[0].Transitions[2])
			}
		})
	}
}

func TestSlotRunsMissingRunJSON(t *testing.T) {
	ctx := context.Background()
	log := &recLogger{}
	s := newTestStore(t, log)
	m := mustCreateExp(t, s, "e_test")
	r := mustNewRun(t, s, m, "s_a1", "r_1", 1, schemas.OutcomePass)
	if err := os.Remove(filepath.Join(s.runDir(r), "run.json")); err != nil {
		t.Fatal(err)
	}
	recs, err := s.SlotRuns(ctx, "e_test", "s_a1")
	if err != nil {
		t.Fatalf("SlotRuns: %v", err)
	}
	if len(recs) != 1 || recs[0].Run.RunID != "r_1" || recs[0].Run.SlotID != "s_a1" {
		t.Fatalf("recs = %+v", recs)
	}
	if log.count("run.json unreadable") != 1 {
		t.Fatalf("warns = %v", log.warns)
	}
}
