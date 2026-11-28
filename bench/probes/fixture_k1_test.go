package probes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestGenericMisleadingNotesArmAndFollowed arms the generic misleading notes
// probe on a SWE-bench-shaped task (empty task folder, no notes of its own)
// and checks the receipt says whether the agent followed the planted note.
func TestGenericMisleadingNotesArmAndFollowed(t *testing.T) {
	for _, projectFile := range []string{"AGENTS.md", ""} {
		docker, root := fakeDocker(t)
		l := newFixtureLayer(Deps{Docker: docker, HelperImage: "helper:1"})
		suite := &schemas.Suite{ID: "hd-swe-verified"}
		rc := &schemas.RunContext{
			Run:    &schemas.Run{RunID: "r_k1", ExpID: "e_k1"},
			Task:   &schemas.Task{ID: "astropy__astropy-12907", Dir: t.TempDir(), Kind: schemas.TaskBugfix},
			Agent:  &schemas.AgentConfig{ID: "mini-swe-agent", Notes: schemas.AgentNotes{ProjectFile: projectFile}},
			Probe:  suite.ProbeByID(schemas.GenericProbeMisleadingNotes),
			Handle: &schemas.EnvironmentHandle{Volume: "ws_r_k1"},
		}
		rec, err := l.Arm(context.Background(), rc)
		if err != nil {
			t.Fatalf("project file %q: %v", projectFile, err)
		}
		written, err := os.ReadFile(filepath.Join(root, "w", "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(written), schemas.GenericNotesMarker) || strings.Contains(string(written), "{{nonce}}") || !strings.Contains(string(written), "nonce_") {
			t.Fatalf("planted note %q", written)
		}
		if rec.Steps[schemas.StepArmed].State != schemas.StepYes || rec.Steps[schemas.StepFired].State != schemas.StepUnknown {
			t.Fatalf("arm receipt %+v", rec.Steps)
		}
		followed := &schemas.Evidence{Diff: &schemas.Diff{Files: []schemas.DiffFile{{Path: "astropy/modeling/separable.py", Status: "modified"}, {Path: "CHANGES.local", Status: "added"}}}}
		got, err := l.Receipts(context.Background(), rc, followed)
		if err != nil {
			t.Fatal(err)
		}
		if f := got.Steps[schemas.StepFired]; f.State != schemas.StepYes || !strings.Contains(f.Detail, "followed") {
			t.Fatalf("followed receipt %+v", f)
		}
		ignored := &schemas.Evidence{Diff: &schemas.Diff{Files: []schemas.DiffFile{{Path: "astropy/modeling/separable.py", Status: "modified"}}}}
		if got, err = l.Receipts(context.Background(), rc, ignored); err != nil {
			t.Fatal(err)
		}
		if f := got.Steps[schemas.StepFired]; f.State != schemas.StepNo || !strings.Contains(f.Detail, "ignored") {
			t.Fatalf("ignored receipt %+v", f)
		}
		if got, err = l.Receipts(context.Background(), rc, &schemas.Evidence{}); err != nil || got.Steps[schemas.StepFired].State != schemas.StepUnknown {
			t.Fatalf("no diff receipt %+v %v", got.Steps[schemas.StepFired], err)
		}
	}
}

// TestGenericMisleadingNotesStaysInWorkspace refuses a notes target outside
// the workspace (the verifier's files live outside it).
func TestGenericMisleadingNotesStaysInWorkspace(t *testing.T) {
	docker, _ := fakeDocker(t)
	l := newFixtureLayer(Deps{Docker: docker, HelperImage: "helper:1"})
	rc := &schemas.RunContext{
		Run:    &schemas.Run{RunID: "r_k1b", ExpID: "e_k1"},
		Task:   &schemas.Task{ID: "t", Dir: t.TempDir()},
		Agent:  &schemas.AgentConfig{Notes: schemas.AgentNotes{ProjectFile: "../grader/tests/verify.sh"}},
		Probe:  (&schemas.Suite{}).ProbeByID(schemas.GenericProbeMisleadingNotes),
		Handle: &schemas.EnvironmentHandle{Volume: "ws"},
	}
	if _, err := l.Arm(context.Background(), rc); err == nil {
		t.Fatal("a notes target outside the workspace must be refused")
	}
}
