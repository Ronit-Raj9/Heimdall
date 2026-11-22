package suite

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// copyPack copies testdata/valid/mini into a temp benchmarks folder and
// returns that folder and the pack root.
func copyPack(t *testing.T) (benchRoot, packRoot string) {
	t.Helper()
	benchRoot = t.TempDir()
	packRoot = filepath.Join(benchRoot, "mini")
	src := filepath.Join("testdata", "valid", "mini")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(packRoot, r)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy pack: %v", err)
	}
	return benchRoot, packRoot
}

// editFile replaces old with new in the file at path, failing if old is absent.
func editFile(t *testing.T, path, old, new string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", path, old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	l := newLocalLoader(benchRoot)
	if l.GetName() != "local" || l.GetVersion() != "1.0.0" {
		t.Fatalf("name/version = %s/%s", l.GetName(), l.GetVersion())
	}
	for _, ref := range []string{"mini", "mini@1.0.0", packRoot} {
		t.Run(ref, func(t *testing.T) {
			s, err := l.Load(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if s.ID != "mini" || s.Root != packRoot || len(s.Tasks) != 1 || len(s.Probes) != 1 {
				t.Fatalf("loaded %+v", s)
			}
			task := s.TaskByID("m01")
			if task == nil || task.Dir != filepath.Join(packRoot, "tasks", "m01") || !filepath.IsAbs(task.Dir) {
				t.Fatalf("task: %+v", task)
			}
			p := s.ProbeByID("stale-notes")
			if p == nil || p.Path != filepath.Join(packRoot, "probes", "stale-notes.yaml") {
				t.Fatalf("probe: %+v", p)
			}
			if s.Lock != nil || s.LockHash != "" {
				t.Fatalf("no lock expected, got %s", s.LockHash)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		edit func(t *testing.T, pack string)
		want []string
	}{
		{"version mismatch", "mini@2.0.0", nil, []string{"mini/suite.yaml", "version"}},
		{"missing pack", "nope", nil, []string{"not found", "suites here: mini"}},
		{"unknown task field", "mini", func(t *testing.T, pack string) {
			editFile(t, filepath.Join(pack, "tasks/m01/task.yaml"), "kind: bugfix", "kind: bugfix\nkinds: bugfix")
		}, []string{"mini/tasks/m01/task.yaml", "kinds: unknown field"}},
		{"missing mutant file", "mini", func(t *testing.T, pack string) {
			editFile(t, filepath.Join(pack, "tasks/m01/task.yaml"), "controls/mutant1.diff", "controls/mutant9.diff")
		}, []string{"mini/tasks/m01/task.yaml", "controls.mutants[0]", "controls/mutant9.diff not found"}},
		{"path escapes task", "mini", func(t *testing.T, pack string) {
			editFile(t, filepath.Join(pack, "tasks/m01/task.yaml"), "instruction: instruction.md", "instruction: ../../suite.yaml")
		}, []string{"instruction", "leaves the folder"}},
		{"task id mismatch", "mini", func(t *testing.T, pack string) {
			editFile(t, filepath.Join(pack, "tasks/m01/task.yaml"), "id: m01", "id: m02")
		}, []string{"mini/tasks/m01/task.yaml", "id: got \"m02\""}},
		{"bad probe", "mini", func(t *testing.T, pack string) {
			editFile(t, filepath.Join(pack, "probes/stale-notes.yaml"), "goal_preserving: true", "goal_preserving: yes please")
		}, []string{"mini/probes/stale-notes.yaml", "goal_preserving"}},
		{"suite unknown field", "mini", func(t *testing.T, pack string) {
			editFile(t, filepath.Join(pack, "suite.yaml"), "workdir:", "work_dir: /x\nworkdir:")
		}, []string{"mini/suite.yaml", "work_dir: unknown field"}},
		{"repo_dir is a symlink to /etc", "mini", func(t *testing.T, pack string) {
			editFile(t, filepath.Join(pack, "tasks/m01/task.yaml"), "repo_dir: repo", "repo_dir: hostetc")
			symlink(t, "/etc", filepath.Join(pack, "tasks/m01/hostetc"))
		}, []string{"mini/tasks/m01/task.yaml", "repo_dir: hostetc: resolves outside the folder through a symlink"}},
		{"notes.clean is a symlink to a file outside", "mini", func(t *testing.T, pack string) {
			outside := filepath.Join(t.TempDir(), "id_rsa")
			if err := os.WriteFile(outside, []byte("key\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			clean := filepath.Join(pack, "tasks/m01/fixtures/notes.clean.md")
			if err := os.Remove(clean); err != nil {
				t.Fatal(err)
			}
			symlink(t, outside, clean)
		}, []string{"notes.clean: fixtures/notes.clean.md: resolves outside the folder through a symlink"}},
		{"symlink in the repo climbs out of the task", "mini", func(t *testing.T, pack string) {
			symlink(t, "../../../../outside.txt", filepath.Join(pack, "tasks/m01/repo/calc/peek.txt"))
		}, []string{"mini/tasks/m01/repo/calc/peek.txt: symlink -> ../../../../outside.txt resolves outside the task folder"}},
		{"symlink chain that is inside lexically", "mini", func(t *testing.T, pack string) {
			symlink(t, "../..", filepath.Join(pack, "tasks/m01/repo/calc/top"))
			symlink(t, "top/..", filepath.Join(pack, "tasks/m01/repo/calc/climb"))
		}, []string{"repo/calc/climb", "resolves outside the task folder"}},
		{"task folder is a symlink out of the suite", "mini", func(t *testing.T, pack string) {
			elsewhere := filepath.Join(t.TempDir(), "m01")
			if err := os.Rename(filepath.Join(pack, "tasks/m01"), elsewhere); err != nil {
				t.Fatal(err)
			}
			symlink(t, elsewhere, filepath.Join(pack, "tasks/m01"))
		}, []string{"task folder: tasks/m01: resolves outside the folder through a symlink"}},
		{"base Dockerfile folder is a symlink out of the suite", "mini", func(t *testing.T, pack string) {
			elsewhere := filepath.Join(t.TempDir(), "base")
			if err := os.Rename(filepath.Join(pack, "base"), elsewhere); err != nil {
				t.Fatal(err)
			}
			symlink(t, elsewhere, filepath.Join(pack, "base"))
		}, []string{"base_image.dockerfile", "resolves outside the folder through a symlink"}},
		{"probe fixture symlink out of the suite", "mini", func(t *testing.T, pack string) {
			symlink(t, "/etc/hostname", filepath.Join(pack, "probes", "planted.md"))
		}, []string{"mini/probes/planted.md: symlink -> /etc/hostname resolves outside the suite folder"}},
		{"lock for another suite", "mini", func(t *testing.T, pack string) {
			if err := os.WriteFile(filepath.Join(pack, LockFile), []byte(`{"suite":"other"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}, []string{"mini/suite.lock", "suite: got \"other\""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, packRoot := copyPack(t)
			if tc.edit != nil {
				tc.edit(t, packRoot)
			}
			_, err := newLocalLoader(benchRoot).Load(context.Background(), tc.ref)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

// symlink creates a link at path holding target.
func symlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

// TestLoadInsideSymlinks checks that symlinks that stay inside the task
// folder load, are listed in one lint warning, and that a named path that is
// such a link resolves to its real target.
func TestLoadInsideSymlinks(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	task := filepath.Join(packRoot, "tasks", "m01")
	symlink(t, "core.py", filepath.Join(task, "repo", "calc", "alias.py"))
	if err := os.Rename(filepath.Join(task, "fixtures", "notes.clean.md"), filepath.Join(task, "fixtures", "real.md")); err != nil {
		t.Fatal(err)
	}
	symlink(t, "real.md", filepath.Join(task, "fixtures", "notes.clean.md"))
	s, err := newLocalLoader(benchRoot).Load(context.Background(), "mini")
	if err != nil {
		t.Fatalf("inside links must load: %v", err)
	}
	var warned bool
	for _, p := range Lint(s) {
		if p.Severity == SeverityError {
			t.Errorf("lint error: %s: %s", p.Path, p.Message)
		}
		if p.Path == "tasks/m01" && strings.Contains(p.Message, "2 symlinks stay inside the task folder") &&
			strings.Contains(p.Message, "repo/calc/alias.py -> core.py") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning listing the inside symlinks: %+v", Lint(s))
	}
	got, err := resolveIn(task, "fixtures/notes.clean.md")
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(filepath.Join(task, "fixtures", "real.md")); got != want {
		t.Errorf("resolveIn = %q, want %q", got, want)
	}
}

func TestLoadReadsLock(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	l := newLocalLoader(benchRoot)
	s, err := l.Load(context.Background(), "mini")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := Lock(s, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	data, err := WriteLock(filepath.Join(packRoot, LockFile), lock)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := l.Load(context.Background(), "mini@1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if s2.Lock == nil || s2.LockHash != utils.SHA256Bytes(data) {
		t.Fatalf("lock hash = %q, want %q", s2.LockHash, utils.SHA256Bytes(data))
	}
	if s2.Lock.Tasks["m01"].RepoTreeSHA256 != lock.Tasks["m01"].RepoTreeSHA256 {
		t.Fatal("lock round trip lost task hashes")
	}
}

// TestLoadCodingCore loads and lints the real pack once all eight tasks exist.
// The pack must load; lint findings are logged for the task authors, not
// failed on, because tasks are authored in parallel with this package.
func TestLoadCodingCore(t *testing.T) {
	benchRoot := filepath.Join("..", "benchmarks")
	entries, err := os.ReadDir(filepath.Join(benchRoot, "coding-core", "tasks"))
	if err != nil {
		t.Skipf("coding-core tasks not readable: %v", err)
	}
	n := 0
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(benchRoot, "coding-core", "tasks", e.Name(), TaskFile)); e.IsDir() && err == nil {
			n++
		}
	}
	if n < 8 {
		t.Skipf("coding-core has %d task folders with task.yaml, want 8", n)
	}
	s, err := newLocalLoader(benchRoot).Load(context.Background(), "coding-core@1.0.0")
	if err != nil {
		t.Fatalf("load coding-core: %v", err)
	}
	problems := Lint(s)
	for _, p := range problems {
		t.Logf("lint %s %s: %s", p.Severity, p.Path, p.Message)
	}
	t.Logf("coding-core: %d tasks, %d probes, %d lint problems", len(s.Tasks), len(s.Probes), len(problems))
	l, err := Lock(s, fixedNow)
	if err != nil {
		t.Fatalf("lock coding-core: %v", err)
	}
	// Re-locking the unchanged pack must reproduce suite.lock byte for byte,
	// so no change to the loader or the lock moves the coding-core lock hash.
	got, err := encodeLock(l)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(benchRoot, "coding-core", LockFile))
	if err != nil {
		t.Skipf("coding-core lock not readable: %v", err)
	}
	if !bytes.Equal(got, legacyLockBytes(t, want)) { // Lock fingerprints: legacy fields only (scenario_test.go)
		t.Errorf("re-locking coding-core changed suite.lock")
	}
	for _, p := range problems {
		if p.Severity == SeverityError {
			t.Errorf("coding-core lint error: %s: %s", p.Path, p.Message)
		}
	}
}

func TestLoadTask(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	if err := os.MkdirAll(filepath.Join(packRoot, "tasks", "m02"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "tasks", "m02", TaskFile), []byte("id: m02\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	editFile(t, filepath.Join(packRoot, SuiteFile), "tasks: [m01]", "tasks: [m01, m02]")
	if _, err := newLocalLoader(benchRoot).Load(context.Background(), "mini"); err == nil {
		t.Fatal("whole-suite load should fail on the half-written m02")
	}
	s, task, err := LoadTask(benchRoot, "mini", "m01")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if task.ID != "m01" || len(s.Tasks) != 1 || s.Tasks[0] != task || len(s.TaskIDs) != 2 || s.Root != packRoot {
		t.Fatalf("suite = %+v, task = %+v", s, task)
	}
	if _, _, err := LoadTask(benchRoot, "mini", "m02"); err == nil || !strings.Contains(err.Error(), "required field missing") {
		t.Fatalf("LoadTask m02: %v", err)
	}
}

// A task file reached through a symlinked folder that leaves the pack is
// refused after the task folder is resolved once (E2): the walk below the
// folder still checks every link.
func TestLoadRefusesTaskSymlinkOutsidePack(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	task := filepath.Join(packRoot, "tasks", "m01")
	elsewhere := filepath.Join(t.TempDir(), "fixtures")
	if err := os.Rename(filepath.Join(task, "fixtures"), elsewhere); err != nil {
		t.Fatal(err)
	}
	symlink(t, elsewhere, filepath.Join(task, "fixtures"))
	_, err := newLocalLoader(benchRoot).Load(context.Background(), "mini")
	if err == nil || !strings.Contains(err.Error(), "notes.clean: fixtures/notes.clean.md: resolves outside the folder through a symlink") {
		t.Fatalf("want the escaping folder refused, got %v", err)
	}
}
