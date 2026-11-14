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
		{name: "grader without network report", grader: func(g *fakeGrader) schemas.Grader { return silentGrader{g} },
			wantFailed: "verifier_no_network", wantGot: "unknown"},
		{name: "no stale notes skips the stale check", edit: func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Notes = schemas.TaskNotes{}
			s.ProbeIDs = nil
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
			}
			if rec.Checks[0].Name != StaticValidationCheck || !rec.Checks[0].Passed || rec.Checks[0].Got != "clean" {
				t.Errorf("first check = %+v, want static_validation passed", rec.Checks[0])
			}
			if wantAdmitted := tc.wantFailed == ""; rec.Admitted != wantAdmitted {
				t.Errorf("admitted = %v, want %v: %+v", rec.Admitted, wantAdmitted, rec.Checks)
			}
		})
	}
}

// TestAdmitRefusesInvalidTask checks that a task with any static validation
// error is refused before a workspace is created or a patch is graded, with
// the messages task validate prints.
func TestAdmitRefusesInvalidTask(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	write := func(t *testing.T, path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name       string
		edit       func(t *testing.T, task string, s *schemas.Suite)
		wantDetail string
	}{
		{"draft instruction", func(t *testing.T, task string, _ *schemas.Suite) {
			editFile(t, filepath.Join(task, "instruction.md"), "too.", "too.\n"+EditMeMarker+"\n")
		}, "still holds the " + EditMeMarker + " marker"},
		{"leaked hidden test", func(t *testing.T, task string, _ *schemas.Suite) {
			b, err := os.ReadFile(filepath.Join(task, "verifier/tests/test_hidden.py"))
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(task, "repo/tests/test_hidden_copy.py"), string(b))
		}, "a hidden test must not be readable by the agent"},
		{"escaping symlink in the repo", func(t *testing.T, task string, _ *schemas.Suite) {
			outside := filepath.Join(t.TempDir(), "id_rsa")
			write(t, outside, "not a key\n")
			if err := os.Symlink(outside, filepath.Join(task, "repo", "calc", "key.txt")); err != nil {
				t.Fatal(err)
			}
		}, "resolves outside the task folder"},
		{"secret in repo", func(t *testing.T, task string, _ *schemas.Suite) {
			write(t, filepath.Join(task, "repo/settings.py"), "AWS = 'AKIAABCDEFGHIJKLMNOP'\n")
		}, "possible AWS access key id"},
		{"stale notes missing while the suite runs stale-notes", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Notes.Stale = ""
		}, "notes.stale: missing"},
		{"required id that does not exist", func(t *testing.T, _ string, s *schemas.Suite) {
			s.Tasks[0].Verifier.Required = []string{"test_hidden.py::test_nope"}
		}, "no function test_nope"},
		{"reference does not apply", func(t *testing.T, task string, _ *schemas.Suite) {
			editFile(t, filepath.Join(task, "repo/calc/core.py"), "def add", "def add_numbers")
		}, "does not apply cleanly"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, packRoot := copyPack(t)
			s := loadMini(t, benchRoot)
			tc.edit(t, filepath.Join(packRoot, "tasks", "m01"), s)
			env := &fakeEnv{diff: exploitDiff}
			fg := &fakeGrader{pass: correctFix}
			rec, err := Admit(context.Background(), AdmitDeps{Env: env, Grader: fg}, s, s.Tasks[0])
			if err != nil {
				t.Fatal(err)
			}
			if rec.Admitted || len(rec.Checks) != 1 {
				t.Fatalf("record = %+v, want one failed check", rec)
			}
			c := rec.Checks[0]
			if c.Name != StaticValidationCheck || c.Passed || !strings.HasSuffix(c.Got, "errors") || !strings.Contains(c.Detail, tc.wantDetail) {
				t.Errorf("check = %+v, want failed static_validation naming %q", c, tc.wantDetail)
			}
			if strings.Contains(c.Detail, "AKIAABCDEFGHIJKLMNOP") {
				t.Errorf("secret value printed: %s", c.Detail)
			}
			if env.created || len(env.plans) > 0 || len(fg.seen) > 0 {
				t.Errorf("controls ran before static validation refused: created %v, plans %d, graded %d", env.created, len(env.plans), len(fg.seen))
			}
		})
	}
}

// TestAdmitMissingRequiredDetail checks that a required test id the
// verifier never reports fails the reference runs and that the check names
// the id.
func TestAdmitMissingRequiredDetail(t *testing.T) {
	benchRoot, _ := copyPack(t)
	s := loadMini(t, benchRoot)
	g := &missingRequiredGrader{fakeGrader{pass: correctFix}}
	rec, err := Admit(context.Background(), AdmitDeps{Env: &fakeEnv{diff: exploitDiff}, Grader: g}, s, s.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	if rec.Admitted {
		t.Fatal("admitted with a required test that never ran")
	}
	for _, c := range rec.Checks {
		if c.Name == "reference_1" && (c.Passed || !strings.Contains(c.Detail, "missing_required=test_hidden.py::test_add_negative")) {
			t.Errorf("reference_1 = %+v, want failed naming the missing id", c)
		}
	}
}

// missingRequiredGrader grades like a junit grader whose report lacks the
// task's required test.
type missingRequiredGrader struct{ fakeGrader }

func (g *missingRequiredGrader) Grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	gr, err := g.fakeGrader.Grade(ctx, rc, diff)
	if err != nil || !gr.Passed {
		return gr, err
	}
	return &schemas.Grade{TestsRan: 2, MissingRequired: rc.Task.Verifier.Required,
		Reason: "required tests did not run: " + strings.Join(rc.Task.Verifier.Required, ", ")}, nil
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
