package handlers

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func ownImageErr(arm, task string) error {
	return fmt.Errorf("arm %s, task %s: the task brings its own image and suite hd-harbor-bfcl grades a snapshot of the agent's container, but agent claude-code has no build.base_arg, so it would run in its own image img:1 and the verifier would not find the task's environment (every run would grade 0); build the agent on the task image with build.base_arg (docs/scenarios-design.md) or leave the task out", arm, task)
}

func TestPlainPlanErrorsStripsStutter(t *testing.T) {
	err := errors.New("plan: plan: plan e_x: arm A: condition clean, task t1: skills: skill zz-good in agents/zz-test-loop/skills has no passing scan: open the agent's Tools tab and press Scan")
	got := PlainPlanErrors(err)
	if len(got) != 1 || !strings.HasPrefix(got[0], "Skill zz-good of agent zz-test-loop") {
		t.Fatalf("got %q", got)
	}
	if got[0] != "Skill zz-good of agent zz-test-loop has no passing safety scan. Open the agent's Tools tab and press Scan." {
		t.Fatalf("sentence %q", got[0])
	}
	changed := PlainPlanErrors(errors.New("plan e_x: skill s1 in agents/a1/skills changed after its scan: open the agent's Tools tab and press Scan"))
	if len(changed) != 1 || changed[0] != "Skill s1 of agent a1 changed after its safety scan. Open the agent's Tools tab and press Scan." {
		t.Fatalf("changed %q", changed)
	}
}

func TestPlainPlanErrorsGroupsOwnImageTasks(t *testing.T) {
	err := fmt.Errorf("plan e_x: %w", errors.Join(ownImageErr("A", "bfcl-1"), ownImageErr("A", "bfcl-2"), ownImageErr("B", "bfcl-3")))
	got := PlainPlanErrors(err)
	want := "Agent claude-code runs in its own container, but 3 tasks of hd-harbor-bfcl bring their own environment (bfcl-1, bfcl-2, bfcl-3), so every run of them would score 0. Leave those tasks out or pick another agent."
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for _, bad := range []string{"build.base_arg", "docs/"} {
		if strings.Contains(got[0], bad) {
			t.Fatalf("%q in %q", bad, got[0])
		}
	}
	one := PlainPlanErrors(ownImageErr("A", "bfcl-9"))
	if len(one) != 1 || !strings.Contains(one[0], "but 1 task of hd-harbor-bfcl bring their own environment (bfcl-9)") {
		t.Fatalf("one %q", one)
	}
}

func TestPlainPlanErrorsSettingsField(t *testing.T) {
	got := PlainPlanErrors(errors.New("plan e_x: arm B: agent config agents/bare-model.yaml: effective agent config: schema: notes.project_file: required field missing"))
	if len(got) != 1 || got[0] != "The agent settings file agents/bare-model.yaml is missing the field notes.project_file." {
		t.Fatalf("got %q", got)
	}
}

func TestPlainPlanErrorsRegistry(t *testing.T) {
	want := "The container image ghcr.io/x/base:1 is not ready on the shared worker yet. Try again later, or connect your own worker in Settings, Workers."
	for _, line := range []string{
		"plan e_x: arm A: policy refused: base image ghcr.io/x/base:1 is not in the shared worker's image registry; ask the operator to copy it there (deploy/hosted/registry-sync.sh ghcr.io/x/base:1)",
		"plan e_x: policy refused: base image ghcr.io/x/base:1 (sha256:abc) is not in the shared worker's image registry any more; ask the operator to copy it there again (deploy/hosted/registry-sync.sh ghcr.io/x/base:1)",
	} {
		got := PlainPlanErrors(errors.New(line))
		if len(got) != 1 || got[0] != want {
			t.Fatalf("got %q", got)
		}
		for _, bad := range []string{"registry-sync.sh", "operator"} {
			if strings.Contains(got[0], bad) {
				t.Fatalf("%q in %q", bad, got[0])
			}
		}
	}
}

func TestPlainPlanErrorsDedupes(t *testing.T) {
	skill := "skill s1 in agents/a1/skills has no passing scan: open the agent's Tools tab and press Scan"
	err := errors.Join(errors.New("plan e_x: arm A: "+skill), errors.New("plan e_x: arm B: "+skill), errors.New("plan e_x: unknown agent agents/x.yaml"))
	got := PlainPlanErrors(err)
	if len(got) != 2 || !strings.HasPrefix(got[0], "Skill s1 of agent a1") || got[1] != "unknown agent agents/x.yaml" {
		t.Fatalf("got %q", got)
	}
	if PlainPlanErrors(nil) != nil {
		t.Fatal("nil error")
	}
}

func TestPlainPlanErrorsKeepsLockSentences(t *testing.T) {
	err := errors.New("plan e_x: suite lock: task file-inbox is draft, not admitted; suite acme@1.0.0 has no lock")
	got := PlainPlanErrors(err)
	want := []string{
		"This benchmark has tasks that have not passed the readiness check yet: file-inbox. Pick a ready benchmark, choose only ready tasks, or run the readiness check on the benchmark page.",
		"Benchmark acme is not frozen yet. Freeze it and run its readiness check on the benchmark page.",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q", got)
	}
	if PlainPlanError(err) != strings.Join(want, " ") {
		t.Fatalf("joined %q", PlainPlanError(err))
	}
}
