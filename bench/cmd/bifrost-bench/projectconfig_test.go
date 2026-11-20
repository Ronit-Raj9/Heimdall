package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/ci"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Every command that loads the default config finds the project init wrote
// under .heimdall, from the repository root or below (audit 5-B-04).
func TestDefaultConfigFindsInitProject(t *testing.T) {
	dir, _, _ := initQuickRepo(t)
	want := filepath.Join(dir, ".heimdall", "deploy", "bench.yaml")
	if got := resolveConfigPath(defaultConfig); got != want {
		t.Fatalf("resolveConfigPath = %s, want %s", got, want)
	}
	cfg, err := loadConfig(defaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	if root, _, err := benchmarksRoot(nil, "", defaultConfig); err != nil || root != cfg.Path(cfg.BenchmarksRoot) ||
		root != filepath.Join(dir, ".heimdall", "benchmarks") {
		t.Fatalf("benchmarks root %s, %v", root, err)
	}
	sub := filepath.Join(dir, "lib")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got := resolveConfigPath(defaultConfig); got != want {
		t.Fatalf("from a subfolder: %s", got)
	}
	if got := resolveConfigPath("other.yaml"); got != "other.yaml" {
		t.Fatalf("explicit path rewritten: %s", got)
	}
}

// A rejected admission after review fails the command, and a retry proves
// again instead of stopping at "already reviewed" (audit 5-B-05).
func TestTaskReviewRejectedFailsAndRetries(t *testing.T) {
	dir, base, ref := initQuickRepo(t)
	suiteDir := filepath.Join(dir, ".heimdall", "benchmarks", "lib")
	// A draft: its instruction still has the EDIT ME line, so static
	// validation rejects it before any Docker step.
	if _, err := scaffold.InitTask(context.Background(), scaffold.TaskInitOptions{SuiteDir: suiteDir, ID: "m-2", Repo: dir, Base: base, Reference: ref,
		ExcludeDir: filepath.Join(dir, ".heimdall")}); err != nil {
		t.Fatal(err)
	}
	if code := runTask([]string{"review", "m-2"}); code != 1 {
		t.Fatalf("review of a rejected task: exit %d, want 1", code)
	}
	if code := runTask([]string{"review", "m-2"}); code != 1 {
		t.Fatalf("retry skipped the proof: exit %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(suiteDir, "suite.lock")); err == nil {
		if a, err := newApp(context.Background(), defaultConfig, appOptions{}); err == nil {
			if ok, _ := admissionCurrent(context.Background(), a, "lib", "m-2"); ok {
				t.Fatal("rejected task recorded as admitted")
			}
		}
	}
}

// A mining dry-run writes nothing, not even the run store (audit 5-B-12).
func TestMineDryRunCreatesNoRunStore(t *testing.T) {
	dir, _, _ := initQuickRepo(t)
	runs := filepath.Join(dir, ".heimdall", "runs")
	if err := os.RemoveAll(runs); err != nil {
		t.Fatal(err)
	}
	if code := runTasks([]string{"mine", "-dry-run", "-limit", "10"}); code != exitOK {
		t.Fatalf("dry-run exit %d", code)
	}
	if _, err := os.Stat(runs); err == nil {
		t.Fatal("dry-run created the run store")
	}
}

// An existing draft folder is not counted as a proved task (audit 5-B-06).
func TestAdmittedInNeedsAnAdmission(t *testing.T) {
	if admittedIn(nil, "m-1") {
		t.Fatal("no lock")
	}
	l := &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{
		"draft": {Status: schemas.TaskDraft},
		"ok":    {Status: schemas.TaskAdmitted, Admission: &schemas.AdmissionRecord{Admitted: true}},
	}}
	if admittedIn(l, "draft") || admittedIn(l, "missing") || !admittedIn(l, "ok") {
		t.Fatal("admittedIn")
	}
}

func TestMineSkipsExistingDraft(t *testing.T) {
	dir, base, ref := initQuickRepo(t)
	suiteDir := filepath.Join(dir, ".heimdall", "benchmarks", "lib")
	cands, _, err := scaffold.MineCandidates(context.Background(), scaffold.MineOptions{Repo: dir,
		TestGlobs: scaffold.DefaultTestGlobs(scaffold.TemplatePythonPytest), Exclude: ".heimdall"})
	if err != nil || len(cands) != 1 {
		t.Fatalf("candidates %v %v", cands, err)
	}
	if _, err := scaffold.InitTask(context.Background(), scaffold.TaskInitOptions{SuiteDir: suiteDir, ID: cands[0].TaskID(), Repo: dir, Base: base, Reference: ref,
		ExcludeDir: filepath.Join(dir, ".heimdall")}); err != nil {
		t.Fatal(err)
	}
	if code := runTasks([]string{"mine", "-limit", "1"}); code != 1 {
		t.Fatalf("mining with only a draft: exit %d, want 1", code)
	}
}

// A refused refs comparison leaves no fixture tree behind, so the same
// --name works once the arguments are corrected (audit 5-B-11).
func TestRefusedRefsCompareCanRetry(t *testing.T) {
	dir, base, ref := initQuickRepo(t)
	cfg, err := loadConfig(defaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	f := compareFlags{baseline: "HEAD", candidate: "HEAD", base: "agents/claude-code.yaml", trials: 1, repo: dir,
		paths: strings.Join(scaffold.DefaultAgentPaths, ","), name: "identical"}
	if _, err := buildCompare(context.Background(), f, cfg); err == nil || !strings.Contains(err.Error(), "nothing to compare") {
		t.Fatalf("identical refs: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Root, "experiments", "fixtures", "identical")); err == nil {
		t.Fatal("fixture tree left after a refused comparison")
	}
	f.baseline, f.candidate = base, ref
	if _, err := buildCompare(context.Background(), f, cfg); err != nil && strings.Contains(err.Error(), "file exists") {
		t.Fatalf("retry under the same name: %v", err)
	}
}

// Refs with the same agent files are a verified "nothing to gate" with its
// own exit code, which the live workflow skips on (audit 5-B-14).
func TestCompareNoAgentChangeExitCode(t *testing.T) {
	dir, _, _ := initQuickRepo(t)
	f := compareFlags{config: defaultConfig, baseline: "HEAD", candidate: "HEAD", base: "agents/claude-code.yaml", trials: 1, repo: dir,
		paths: strings.Join(scaffold.DefaultAgentPaths, ","), name: "pr-gate", writeOnly: true, failOn: "regressed,invalid"}
	var out strings.Builder
	if code := compare(context.Background(), f, strings.NewReader(""), &out); code != ci.ExitNoAgentChange {
		t.Fatalf("no agent change: exit %d", code)
	}
	wf, err := ci.GitHubWorkflow(ci.WorkflowOptions{Mode: ci.ModeLive, Source: "o/r", SourceRef: "main", Base: "agents/claude-code.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wf, "if: steps.write.outputs.changed == 'true'") || !strings.Contains(wf, `-eq 6 ]`) {
		t.Fatalf("live workflow does not skip the gate on no agent change:\n%s", wf)
	}
}
