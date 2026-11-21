package runctl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newExp(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "e_x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, "e_x"
}

func TestReadEmptyIsRunning(t *testing.T) {
	root, id := newExp(t)
	st, err := Read(root, id)
	if err != nil || st.Paused() || st.Mode != ModeRunning {
		t.Fatalf("state %+v err %v", st, err)
	}
	if err := Verify(root, id); err != nil {
		t.Fatal(err)
	}
}

func TestPauseResumeLogChain(t *testing.T) {
	root, id := newExp(t)
	st, err := Pause(root, id, "ana@example.com", "provider outage")
	if err != nil || !st.Paused() || st.PausedBy != "ana@example.com" || st.PauseReason != "provider outage" {
		t.Fatalf("pause: %+v %v", st, err)
	}
	// A second pause changes nothing and writes no entry.
	if _, err := Pause(root, id, "bo", "again"); err != nil {
		t.Fatal(err)
	}
	c := 3
	u := 12.5
	models := []string{"m2", "m1", "m1"}
	st, err = Apply(root, id, "ana", ActionSet, "", "raise", func(s *State) error {
		(&Patch{Concurrency: &c, TotalUSD: &u, PausedModels: &models}).ApplyTo(s)
		return nil
	})
	if err != nil || st.Concurrency != 3 || st.TotalUSD != 12.5 || strings.Join(st.PausedModels, ",") != "m1,m2" {
		t.Fatalf("set: %+v %v", st, err)
	}
	st, err = Resume(root, id, "ana", "")
	if err != nil || st.Paused() || st.PausedBy != "" {
		t.Fatalf("resume: %+v %v", st, err)
	}
	log, err := Log(root, id)
	if err != nil || len(log) != 3 {
		t.Fatalf("log %d entries, err %v", len(log), err)
	}
	if log[0].Action != ActionPause || log[1].Action != ActionSet || log[2].Action != ActionResume || log[1].Prev != log[0].Hash {
		t.Fatalf("log %+v", log)
	}
	if err := Verify(root, id); err != nil {
		t.Fatal(err)
	}
	if !st.ModelPaused("m1") || st.ModelPaused("m3") {
		t.Fatal("paused models")
	}
}

func TestVerifyDetectsEdits(t *testing.T) {
	root, id := newExp(t)
	if _, err := Pause(root, id, "a", "r1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Resume(root, id, "a", "r2"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, id, LogFile)
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "r1", "rX", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(root, id); !errors.Is(err, ErrBrokenLog) {
		t.Fatalf("edited log verified: %v", err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	// Dropping the last line leaves the state ahead of the log.
	lines := strings.SplitAfter(string(b), "\n")
	if err := os.WriteFile(p, []byte(lines[0]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(root, id); !errors.Is(err, ErrBrokenLog) {
		t.Fatalf("truncated log verified: %v", err)
	}
}

func TestStateRebuiltFromLog(t *testing.T) {
	root, id := newExp(t)
	if _, err := Apply(root, id, "a", ActionSupersede, "r_1", "requeue", func(s *State) error {
		s.Superseded = map[string]Supersede{"r_1": {SlotID: "s_1", Outcome: "infra_error", By: "a"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, id, StateFile)); err != nil {
		t.Fatal(err)
	}
	st, err := Read(root, id)
	if err != nil || !st.IsSuperseded("r_1") || st.Seq != 1 {
		t.Fatalf("rebuilt %+v %v", st, err)
	}
	set, err := SupersededRuns(filepath.Join(root, id))
	if err != nil || !set["r_1"] {
		t.Fatalf("superseded set %v %v", set, err)
	}
}

func TestPatchValidate(t *testing.T) {
	bad := -1
	if err := (&Patch{Concurrency: &bad}).Validate(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("negative concurrency accepted")
	}
	if err := (&Patch{}).Validate(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("empty patch accepted")
	}
	ms := []string{"other"}
	if err := (&Patch{PausedModels: &ms}).Validate([]string{"m1"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown model accepted")
	}
	ok := []string{"m1"}
	if err := (&Patch{PausedModels: &ok}).Validate([]string{"m1"}); err != nil {
		t.Fatal(err)
	}
}

func TestApplyRefusals(t *testing.T) {
	root, id := newExp(t)
	if _, err := Apply(root, "../x", "a", ActionSet, "", "", func(*State) error { return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatal("bad id accepted")
	}
	if _, err := Apply(root, "e_missing", "a", ActionSet, "", "", func(*State) error { return nil }); err == nil {
		t.Fatal("missing experiment accepted")
	}
	if _, err := Pause(root, id, "a", strings.Repeat("x", MaxReasonLen+1)); !errors.Is(err, ErrInvalid) {
		t.Fatal("long reason accepted")
	}
}
