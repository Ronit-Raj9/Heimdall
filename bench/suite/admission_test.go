package suite

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeGrader passes a diff when the content-based rule says so. It never runs
// anything.
type fakeGrader struct {
	pass  func(patch string) bool
	err   error
	seen  []string // patch contents graded, in order
	ctxOK bool     // every call had a run context with suite, task, slot, run and dir
}

func (g *fakeGrader) GetName() string    { return "fake_grader" }
func (g *fakeGrader) GetVersion() string { return "0.0.0" }

// VerifierNetwork implements VerifierNetworkReporter.
func (g *fakeGrader) VerifierNetwork() string { return "none" }

// silentGrader is a fakeGrader that does not report its verifier network.
type silentGrader struct{ inner *fakeGrader }

func (g silentGrader) GetName() string    { return "silent_grader" }
func (g silentGrader) GetVersion() string { return "0.0.0" }
func (g silentGrader) Grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	return g.inner.Grade(ctx, rc, diff)
}

func (g *fakeGrader) Grade(_ context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	g.ctxOK = rc != nil && rc.Suite != nil && rc.Task != nil && rc.Slot != nil && rc.Run != nil && rc.Dir != "" &&
		strings.HasPrefix(rc.Run.RunID, "r_") && rc.Slot.Task == rc.Task.ID && !rc.Slot.Counted
	if !filepath.IsAbs(diff.Path) {
		return nil, errors.New("diff path not absolute")
	}
	b, err := os.ReadFile(diff.Path)
	if err != nil {
		return nil, err
	}
	if utils.SHA256Bytes(b) != diff.SHA256 {
		return nil, errors.New("diff sha256 mismatch")
	}
	g.seen = append(g.seen, string(b))
	if g.err != nil {
		return nil, g.err
	}
	if g.pass(string(b)) {
		return &schemas.Grade{Passed: true, TestsRan: 2}, nil
	}
	return &schemas.Grade{Passed: false, TestsRan: 2, Failures: 1, ExitCode: 1}, nil
}

// fakeEnv records the scripted runs (exploit, noop, refuse) and exports a
// fixed diff.
type fakeEnv struct {
	diff      string // content of the exported diff
	execErr   error
	plans     []*schemas.RunPlan
	created   bool
	destroyed int
}

func (e *fakeEnv) GetName() string    { return "fake_env" }
func (e *fakeEnv) GetVersion() string { return "0.0.0" }

func (e *fakeEnv) Create(_ context.Context, rc *schemas.RunContext) (*schemas.EnvironmentHandle, error) {
	if rc.Agent == nil || rc.Task == nil {
		return nil, errors.New("run context incomplete")
	}
	e.created = true
	return &schemas.EnvironmentHandle{Volume: "v", Workdir: "/workspace"}, nil
}

func (e *fakeEnv) Exec(_ context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	e.plans = append(e.plans, plan)
	if e.execErr != nil {
		return nil, e.execErr
	}
	return &schemas.ExecResult{ExitCode: 0}, nil
}

func (e *fakeEnv) ExportDiff(_ context.Context, rc *schemas.RunContext) (*schemas.Diff, error) {
	path := filepath.Join(rc.Dir, "workspace.diff")
	if err := os.WriteFile(path, []byte(e.diff), 0o644); err != nil {
		return nil, err
	}
	return &schemas.Diff{Path: path, SHA256: utils.SHA256Bytes([]byte(e.diff))}, nil
}

func (e *fakeEnv) Destroy(context.Context, *schemas.RunContext) error {
	e.destroyed++
	return nil
}

const exploitDiff = "diff --git a/tests/test_ok.py b/tests/test_ok.py\nnew file mode 100644\n--- /dev/null\n+++ b/tests/test_ok.py\n@@ -0,0 +1,2 @@\n+def test_ok():\n+    assert True\n"

// correctFix passes exactly the patches that contain the reference change.
func correctFix(patch string) bool { return strings.Contains(patch, "+    return a + b") }

func TestAdmit(t *testing.T) {
	tests := []struct {
		name         string
		grader       *fakeGrader
		env          *fakeEnv
		wantAdmitted bool
		wantFailed   []string // names of checks that must fail
	}{
		{"admissible", &fakeGrader{pass: correctFix}, &fakeEnv{diff: exploitDiff}, true, nil},
		{"grader passes everything", &fakeGrader{pass: func(string) bool { return true }}, &fakeEnv{diff: exploitDiff},
			false, []string{"empty_patch", "mutant_1", "exploit"}},
		{"grader fails everything", &fakeGrader{pass: func(string) bool { return false }}, &fakeEnv{diff: exploitDiff},
			false, []string{"reference_1", "reference_2", "reference_3", "reference_stale_notes"}},
		{"grader errors", &fakeGrader{pass: correctFix, err: errors.New("boom")}, &fakeEnv{diff: exploitDiff},
			false, []string{"reference_1", "empty_patch", "mutant_1", "exploit", "reference_stale_notes"}},
		{"exploit exec fails", &fakeGrader{pass: correctFix}, &fakeEnv{diff: exploitDiff, execErr: errors.New("no docker")},
			false, []string{"exploit"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, _ := copyPack(t)
			s := loadMini(t, benchRoot)
			rec, err := Admit(context.Background(), AdmitDeps{Env: tc.env, Grader: tc.grader}, s, s.Tasks[0])
			if err != nil {
				t.Fatal(err)
			}
			if rec.Admitted != tc.wantAdmitted {
				t.Errorf("admitted = %v, want %v: %+v", rec.Admitted, tc.wantAdmitted, rec.Checks)
			}
			names := map[string]schemas.AdmissionCheck{}
			for _, c := range rec.Checks {
				names[c.Name] = c
			}
			for _, want := range []string{"reference_1", "reference_2", "reference_3", "empty_patch", "mutant_1", "exploit",
				"reference_stale_notes", "null_noop", "null_refuse", "instruction_edited", "secret_scan", "verifier_no_network"} {
				if _, ok := names[want]; !ok {
					t.Errorf("missing check %s", want)
				}
			}
			for _, f := range tc.wantFailed {
				if names[f].Passed {
					t.Errorf("check %s passed, want failed: %+v", f, names[f])
				}
			}
			if tc.wantAdmitted && names["exploit"].Got != GotFail {
				t.Errorf("exploit got %q", names["exploit"].Got)
			}
			if tc.env.destroyed != 3 {
				t.Errorf("Destroy called %d times, want 3 (exploit, noop, refuse)", tc.env.destroyed)
			}
			if rec.Grader != "fake_grader" || rec.ReferenceRepeats != 3 || len(rec.ReferenceResults) != 3 {
				t.Errorf("record grader %q, repeats %d, results %v", rec.Grader, rec.ReferenceRepeats, rec.ReferenceResults)
			}
			if !tc.grader.ctxOK {
				t.Error("grader got an incomplete run context")
			}
		})
	}
}

func TestAdmitExploitPlan(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	s := loadMini(t, benchRoot)
	env := &fakeEnv{diff: exploitDiff}
	if _, err := Admit(context.Background(), AdmitDeps{Env: env, Grader: &fakeGrader{pass: correctFix}}, s, s.Tasks[0]); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(packRoot, "tasks/m01/controls/exploit.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.plans) != 3 {
		t.Fatalf("scripted runs = %d, want 3", len(env.plans))
	}
	p := env.plans[0]
	if strings.Join(p.Argv, " ") != "sh /tmp/bench/exploit.sh" || p.Files["/tmp/bench/exploit.sh"] != string(script) ||
		p.Image != "bench/mini-base:1" || p.Network != "none" || p.User != "1000" || p.WallS != 120 || p.Workdir != "/workspace" {
		t.Fatalf("exploit plan = %+v", p)
	}
	if got := strings.Join(env.plans[1].Argv, " "); got != "true" {
		t.Errorf("noop argv = %q", got)
	}
	if got := strings.Join(env.plans[2].Argv, " "); !strings.Contains(got, refuseMessage) {
		t.Errorf("refuse argv = %q", got)
	}
	for _, p := range env.plans[1:] {
		if p.Network != "none" || p.User != "1000" || len(p.Files) != 0 {
			t.Errorf("null plan = %+v", p)
		}
	}
}

func TestAdmitStaticChecks(t *testing.T) {
	tests := []struct {
		name       string
		edit       func(t *testing.T, pack string, s *schemas.Suite)
		grader     func(*fakeGrader) schemas.Grader
		wantFailed string
		wantGot    string
		absent     string // a check that must not run
	}{
		{name: "draft instruction", edit: func(t *testing.T, pack string, _ *schemas.Suite) {
			editFile(t, filepath.Join(pack, "tasks/m01/instruction.md"), "too.", "too.\n"+EditMeMarker+"\n")
		}, wantFailed: "instruction_edited", wantGot: "draft"},
		{name: "secret in repo", edit: func(t *testing.T, pack string, _ *schemas.Suite) {
			if err := os.WriteFile(filepath.Join(pack, "tasks/m01/repo/settings.py"), []byte("AWS = 'AKIAABCDEFGHIJKLMNOP'\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, wantFailed: "secret_scan", wantGot: "1 findings"},
		{name: "grader without network report", grader: func(g *fakeGrader) schemas.Grader { return silentGrader{g} },
			wantFailed: "verifier_no_network", wantGot: "unknown"},
		{name: "no stale notes skips the stale check", edit: func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Notes = schemas.TaskNotes{}
		}, absent: "reference_stale_notes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, packRoot := copyPack(t)
			s := loadMini(t, benchRoot)
			if tc.edit != nil {
				tc.edit(t, packRoot, s)
			}
			fg := &fakeGrader{pass: correctFix}
			var g schemas.Grader = fg
			if tc.grader != nil {
				g = tc.grader(fg)
			}
			rec, err := Admit(context.Background(), AdmitDeps{Env: &fakeEnv{diff: exploitDiff}, Grader: g}, s, s.Tasks[0])
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range rec.Checks {
				if c.Name == tc.absent {
					t.Errorf("check %s ran", c.Name)
				}
				if c.Name == tc.wantFailed && (c.Passed || c.Got != tc.wantGot) {
					t.Errorf("check %s = %+v, want failed with got %q", c.Name, c, tc.wantGot)
				}
				if c.Name == "secret_scan" && strings.Contains(c.Detail, "AKIAABCDEFGHIJKLMNOP") {
					t.Errorf("secret value printed: %s", c.Detail)
				}
			}
			if wantAdmitted := tc.wantFailed == ""; rec.Admitted != wantAdmitted {
				t.Errorf("admitted = %v, want %v: %+v", rec.Admitted, wantAdmitted, rec.Checks)
			}
		})
	}
}

func TestAdmitRequiresDeps(t *testing.T) {
	if _, err := Admit(context.Background(), AdmitDeps{}, &schemas.Suite{}, &schemas.Task{}); err == nil {
		t.Fatal("want error without env and grader")
	}
}

func TestRecordAdmission(t *testing.T) {
	l := &schemas.SuiteLock{Tasks: map[string]schemas.TaskLock{"t1": {Status: schemas.TaskDraft}}}
	if err := RecordAdmission(l, "t1", &schemas.AdmissionRecord{Admitted: true}); err != nil {
		t.Fatal(err)
	}
	if l.Tasks["t1"].Status != schemas.TaskAdmitted || l.Tasks["t1"].Admission == nil {
		t.Fatalf("lock = %+v", l.Tasks["t1"])
	}
	if err := RecordAdmission(l, "t1", &schemas.AdmissionRecord{Admitted: false}); err != nil {
		t.Fatal(err)
	}
	if l.Tasks["t1"].Status != schemas.TaskDraft {
		t.Fatalf("failed admission left status %s", l.Tasks["t1"].Status)
	}
	if err := RecordAdmission(l, "t9", nil); err == nil {
		t.Fatal("want error for unknown task")
	}
}

func TestDiffFiles(t *testing.T) {
	patch := []byte("diff --git a/calc/core.py b/calc/core.py\n--- a/calc/core.py\n+++ b/calc/core.py\n" +
		"diff --git a/NEW.md b/NEW.md\nnew file mode 100644\n" +
		"diff --git a/old.txt b/old.txt\ndeleted file mode 100644\n")
	got := diffFiles(patch)
	want := []schemas.DiffFile{{Path: "calc/core.py", Status: "modified"}, {Path: "NEW.md", Status: "added"}, {Path: "old.txt", Status: "deleted"}}
	if len(got) != len(want) {
		t.Fatalf("diffFiles = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("file %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestWithNewFilesApplies checks the stale-notes patch with git apply when git
// is installed. git apply works outside a repository and touches no network.
func TestWithNewFilesApplies(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	_, packRoot := copyPack(t)
	task := filepath.Join(packRoot, "tasks", "m01")
	ref, err := os.ReadFile(filepath.Join(task, "solution", "patch.diff"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		notes string
	}{
		{"multi line", "# notes\n\nline two\n"},
		{"no final newline", "# notes\nlast"},
		{"one line", "only\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			work := filepath.Join(t.TempDir(), "w")
			if err := os.CopyFS(work, os.DirFS(filepath.Join(task, "repo"))); err != nil {
				t.Fatal(err)
			}
			patch := filepath.Join(t.TempDir(), "p.diff")
			if err := os.WriteFile(patch, withNewFiles(ref, []byte(tc.notes), staleNotesFiles), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(git, "apply", "-p1", patch)
			cmd.Dir = work
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git apply: %v\n%s", err, out)
			}
			for _, f := range staleNotesFiles {
				b, err := os.ReadFile(filepath.Join(work, f))
				if err != nil || string(b) != tc.notes {
					t.Fatalf("%s = %q (%v), want %q", f, b, err, tc.notes)
				}
			}
		})
	}
}
