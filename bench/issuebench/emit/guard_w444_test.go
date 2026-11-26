package emit

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/taskcat"
	"github.com/maximhq/bifrost/bench/taskguard"
)

// TestEmitWritesGuardAndCategory: each emitted issue task has guard.json
// (builder issue, the commit pair, the flaky tests, the model's clarity
// check) and a rule label in the suite's category sidecar.
func TestEmitWritesGuardAndCategory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	env := issuebench.EnvSpec{Repo: "Acme/Calc", Version: "1.0", Language: "python", BaseImage: "python:3.11-slim", Commands: []string{"pip install pytest"}, TestCmd: "pytest -rA"}
	in := []Input{{Env: env, RepoDir: repoWithHistory(t), Validation: issuebench.Validation{
		Candidate: issuebench.Candidate{Repo: "Acme/Calc", PR: 1, Issue: 101, IssueTitle: "fix: add is wrong", IssueBody: "add(1, 2) returns -1",
			IssueCreated: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), TestPatch: testPatch, FixPatch: fixPatch, Language: "python",
			BaseSHA: "1111111111111111111111111111111111111111", MergeSHA: "2222222222222222222222222222222222222222"},
		FailToPass: []string{"tests/test_calc.py::test_add"},
		Flaky:      []string{"tests/test_calc.py::test_time"},
		Checks:     issuebench.LLMChecks{Clear: false},
	}}}
	rep, err := Emit(context.Background(), in, Options{BenchRoot: root, WorkDir: filepath.Join(root, "work"), Readiness: &fakeQueue{}})
	must(t, err)
	s := rep.Suites[0]
	if len(s.Tasks) != 1 {
		t.Fatalf("tasks %v", s.Tasks)
	}
	suiteDir := filepath.Join(root, "benchmarks", s.Suite)
	r, err := taskguard.Read(filepath.Join(suiteDir, "tasks", s.Tasks[0]))
	if err != nil || r == nil {
		t.Fatalf("guard.json = %v %v", r, err)
	}
	if r.Builder != taskguard.BuilderIssue || r.Source != "1111111111111111111111111111111111111111..2222222222222222222222222222222222222222" ||
		len(r.FlakyTests) != 1 || r.Clarity == nil || r.Clarity.Verdict != taskguard.ClarityUnclear || r.Clarity.Origin != taskguard.OriginModel {
		t.Fatalf("guard record = %+v", r)
	}
	cats, err := taskcat.Read(suiteDir)
	if err != nil || cats[s.Tasks[0]].Category != taskcat.BugFix || cats[s.Tasks[0]].Source != taskcat.SourceRule {
		t.Fatalf("categories = %+v %v", cats, err)
	}
}
