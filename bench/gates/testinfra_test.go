package gates

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// pristineSetupCfg has its pytest section far below the top, so a hunk near
// the end shows no header in its context.
var pristineSetupCfg = strings.Join([]string{
	"[metadata]",          // 1
	"name = demo",         // 2
	"",                    // 3
	"[tool:pytest]",       // 4
	"testpaths = tests",   // 5
	"a = 1",               // 6
	"b = 2",               // 7
	"c = 3",               // 8
	"d = 4",               // 9
	"e = 5",               // 10
	"f = 6",               // 11
	"g = 7",               // 12
	"",                    // 13
	"[flake8]",            // 14
	"max-line-length = 1", // 15
	"x = 1",               // 16
	"y = 2",               // 17
	"z = 3",               // 18
}, "\n") + "\n"

func TestTestInfraAlwaysFiles(t *testing.T) {
	cases := []struct {
		name  string
		files []schemas.DiffFile
		want  []string
	}{
		{"new conftest anywhere", []schemas.DiffFile{{Path: "pkg/sub/conftest.py", Status: "added"}}, []string{"pkg/sub/conftest.py"}},
		{"changed root conftest", []schemas.DiffFile{{Path: "conftest.py", Status: "modified"}}, []string{"conftest.py"}},
		{"pytest.ini", []schemas.DiffFile{{Path: "pytest.ini", Status: "added"}}, []string{"pytest.ini"}},
		{"sitecustomize", []schemas.DiffFile{{Path: "lib/sitecustomize.py", Status: "added"}}, []string{"lib/sitecustomize.py"}},
		{"usercustomize", []schemas.DiffFile{{Path: "usercustomize.py", Status: "added"}}, []string{"usercustomize.py"}},
		{"pth file", []schemas.DiffFile{{Path: ".venv/lib/evil.pth", Status: "added"}}, []string{".venv/lib/evil.pth"}},
		{"path-shadowing pytest", []schemas.DiffFile{{Path: "bin/pytest", Status: "added"}}, []string{"bin/pytest"}},
		{"path-shadowing py.test", []schemas.DiffFile{{Path: "py.test", Status: "added"}}, []string{"py.test"}},
		{"path-shadowing python3", []schemas.DiffFile{{Path: "tools/python3", Status: "added"}}, []string{"tools/python3"}},
		{"path-shadowing python", []schemas.DiffFile{{Path: "python", Status: "modified"}}, []string{"python"}},
		{"deleted conftest is not injection", []schemas.DiffFile{{Path: "conftest.py", Status: "deleted"}}, nil},
		{"ordinary source", []schemas.DiffFile{{Path: "ledger/money.py", Status: "modified"}}, nil},
		{"python package dir", []schemas.DiffFile{{Path: "python/util.py", Status: "added"}}, nil},
		{"conftest lookalike", []schemas.DiffFile{{Path: "my_conftest.py", Status: "added"}}, nil},
	}
	g := newTestInfraGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := g.Check(context.Background(), &schemas.RunContext{}, diffOf(c.files...))
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != (c.want != nil) || !reflect.DeepEqual(res.Evidence, c.want) {
				t.Fatalf("fired=%v evidence=%v, want %v", res.Fired, res.Evidence, c.want)
			}
		})
	}
}

func TestTestInfraSections(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		status string
		diff   string
		fired  bool
	}{
		{
			name: "edit deep in tool:pytest section, header outside hunk", file: "setup.cfg", status: "modified", fired: true,
			diff: "--- a/setup.cfg\n+++ b/setup.cfg\n@@ -8,5 +8,6 @@\n c = 3\n d = 4\n e = 5\n+addopts = -p no:randomly\n f = 6\n g = 7\n",
		},
		{
			name: "edit in flake8 section", file: "setup.cfg", status: "modified", fired: false,
			diff: "--- a/setup.cfg\n+++ b/setup.cfg\n@@ -15,4 +15,4 @@\n max-line-length = 1\n-x = 1\n+x = 2\n y = 2\n z = 3\n",
		},
		{
			name: "edit in metadata section", file: "setup.cfg", status: "modified", fired: false,
			diff: "--- a/setup.cfg\n+++ b/setup.cfg\n@@ -1,3 +1,3 @@\n [metadata]\n-name = demo\n+name = demo2\n \n",
		},
		{
			name: "removing the pytest header", file: "setup.cfg", status: "modified", fired: true,
			diff: "--- a/setup.cfg\n+++ b/setup.cfg\n@@ -2,4 +2,3 @@\n name = demo\n \n-[tool:pytest]\n testpaths = tests\n",
		},
		{
			name: "new pyproject with tool.pytest.ini_options", file: "pyproject.toml", status: "added", fired: true,
			diff: "--- /dev/null\n+++ b/pyproject.toml\n@@ -0,0 +1,4 @@\n+[project]\n+name = \"x\"\n+[tool.pytest.ini_options]\n+addopts = \"-q\"\n",
		},
		{
			name: "new pyproject without pytest", file: "pyproject.toml", status: "added", fired: false,
			diff: "--- /dev/null\n+++ b/pyproject.toml\n@@ -0,0 +1,2 @@\n+[project]\n+name = \"x\"\n",
		},
		{
			name: "pyproject dotted key", file: "pyproject.toml", status: "added", fired: true,
			diff: "--- /dev/null\n+++ b/pyproject.toml\n@@ -0,0 +1 @@\n+tool.pytest.ini_options.addopts = \"-q\"\n",
		},
		{
			name: "tox.ini pytest section", file: "tox.ini", status: "added", fired: true,
			diff: "--- /dev/null\n+++ b/tox.ini\n@@ -0,0 +1,2 @@\n+[pytest]\n+addopts = -q\n",
		},
		{
			name: "tox.ini testenv section", file: "tox.ini", status: "added", fired: false,
			diff: "--- /dev/null\n+++ b/tox.ini\n@@ -0,0 +1,2 @@\n+[testenv]\n+commands = pytest\n",
		},
		{
			name: "deleted setup.cfg is not injection", file: "setup.cfg", status: "deleted", fired: false,
			diff: "--- a/setup.cfg\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-[tool:pytest]\n-testpaths = tests\n",
		},
	}
	taskDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(taskDir, "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "repo", "setup.cfg"), []byte(pristineSetupCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := &schemas.RunContext{Task: &schemas.Task{ID: "t", Dir: taskDir, RepoDir: "repo"}}
	g := newTestInfraGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "workspace.diff")
			if err := os.WriteFile(p, []byte(c.diff), 0o644); err != nil {
				t.Fatal(err)
			}
			ev := &schemas.Evidence{Diff: &schemas.Diff{Path: p, Files: []schemas.DiffFile{{Path: c.file, Status: c.status}}}}
			res, err := g.Check(context.Background(), rc, ev)
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired {
				t.Fatalf("fired=%v detail=%q, want %v", res.Fired, res.Detail, c.fired)
			}
		})
	}
}

func TestTouchesPytestSectionWithoutPristine(t *testing.T) {
	files, err := parseUnifiedDiff([]byte("--- a/setup.cfg\n+++ b/setup.cfg\n@@ -8,5 +8,6 @@\n c = 3\n d = 4\n e = 5\n+addopts = -x\n f = 6\n g = 7\n"))
	if err != nil {
		t.Fatal(err)
	}
	if touchesPytestSection(files["setup.cfg"], nil) {
		t.Fatal("without the pristine file the section is unknown and must not fire")
	}
	files, err = parseUnifiedDiff([]byte("--- a/setup.cfg\n+++ b/setup.cfg\n@@ -4,3 +4,4 @@\n [tool:pytest]\n testpaths = tests\n+addopts = -x\n a = 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !touchesPytestSection(files["setup.cfg"], nil) {
		t.Fatal("a header inside the hunk context must count")
	}
}

func TestTestInfraErrors(t *testing.T) {
	g := newTestInfraGate()
	if _, err := g.Check(context.Background(), &schemas.RunContext{}, &schemas.Evidence{}); err == nil {
		t.Fatal("want error without a diff")
	}
	ev := diffOf(schemas.DiffFile{Path: "setup.cfg", Status: "modified"})
	if _, err := g.Check(context.Background(), &schemas.RunContext{}, ev); err == nil {
		t.Fatal("want error when the diff text is unreadable")
	}
}

func TestIsPytestSection(t *testing.T) {
	cases := map[string]bool{
		"tool:pytest":             true,
		"pytest":                  true,
		"tool.pytest":             true,
		"tool.pytest.ini_options": true,
		"tool.pytest-foo":         false,
		"tool.mypy":               false,
		"testenv":                 false,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isPytestSection(name); got != want {
				t.Fatalf("isPytestSection(%q) = %v", name, got)
			}
		})
	}
}
