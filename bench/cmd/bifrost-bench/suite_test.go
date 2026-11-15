package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestRunSuiteInit(t *testing.T) {
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"init", "web", "-template", "node-test", "-config", config}); code != exitOK {
		t.Fatalf("suite init exit %d", code)
	}
	for _, rel := range []string{"suite.yaml", "base/Dockerfile", "README.md", "probes/model-outage.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "benchmarks", "web", filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	if code := runSuite([]string{"init", "web", "-template", "node-test", "-config", config}); code != exitInfra {
		t.Fatalf("suite init over an existing suite: exit %d", code)
	}
	if code := runSuite([]string{"init", "py", "-config", config, "-template", "python-pytest", "-image", "bench/coding-core-base:1"}); code != exitOK {
		t.Fatalf("suite init -image exit %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "benchmarks", "py", "suite.yaml"))
	if err != nil || !strings.Contains(string(b), "tag: bench/coding-core-base:1") || strings.Contains(string(b), "dockerfile:") {
		t.Fatalf("suite.yaml with -image: %v\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "benchmarks", "py", "base")); !os.IsNotExist(err) {
		t.Error("-image still wrote base/")
	}
	for _, args := range [][]string{
		{"init", "x", "-config", config},
		{"init", "x", "-template", "cobol", "-config", config},
		{"init", "Bad Id", "-template", "go-test", "-config", config},
	} {
		if code := runSuite(args); code == exitOK {
			t.Errorf("suite %v: exit 0", args)
		}
	}
}

// TestSuiteLintLockWithConfig runs lint and lock on a copy of coding-core
// inside a fresh project, resolved through -config.
func TestSuiteLintLockWithConfig(t *testing.T) {
	src := filepath.Join("..", "..", "benchmarks", "coding-core")
	if _, err := os.Stat(filepath.Join(src, "suite.yaml")); err != nil {
		t.Skipf("coding-core not readable: %v", err)
	}
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	dst := filepath.Join(dir, "benchmarks", "coding-core")
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dst, "suite.lock"))
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"lint", "-config", config, "coding-core"}); code != exitOK {
		t.Fatalf("lint exit %d", code)
	}
	if code := runSuite([]string{"lock", "coding-core", "-config", config}); code != exitOK {
		t.Fatalf("lock exit %d", code)
	}
	after, err := os.ReadFile(filepath.Join(dst, "suite.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("re-locking an unchanged coding-core copy changed suite.lock")
	}
}

func TestRunSuiteInitScenario(t *testing.T) {
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	want := map[string]string{
		"tool-use": "tasks/restock/verifier/tests/verify.py", "mcp": "services/inventory/server.py",
		"memory": "tasks/launch-date/sessions/recall.md", "qa": "tasks/hotel-cap/verifier/answer.yaml",
	}
	for tmpl, file := range want {
		id := "s-" + tmpl
		if code := runSuite([]string{"init", id, "-template", tmpl, "-config", config}); code != exitOK {
			t.Fatalf("suite init -template %s: exit %d", tmpl, code)
		}
		for _, rel := range []string{"suite.yaml", "README.md", "base/Dockerfile", file} {
			if _, err := os.Stat(filepath.Join(dir, "benchmarks", id, filepath.FromSlash(rel))); err != nil {
				t.Errorf("%s: %v", rel, err)
			}
		}
	}
}

func TestServiceBuilds(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "desk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "services", "desk", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &schemas.Suite{Root: root,
		Services: []schemas.ServiceSpec{{ID: "cache", Build: &schemas.ImageSpec{Dockerfile: "svc/cache/Dockerfile", Tag: "acme/cache:1"}}},
		Tasks: []*schemas.Task{{Dir: filepath.Join(root, "tasks", "t1"), Services: []schemas.ServiceSpec{
			{ID: "desk", Image: "acme/desk:1"},
			{ID: "other", Image: "acme/other:1"},
		}}}}
	got := serviceBuilds(s)
	want := []serviceBuild{
		{service: "cache", tag: "acme/cache:1", root: root, dockerfile: "svc/cache/Dockerfile"},
		{service: "desk", tag: "acme/desk:1", root: root, dockerfile: filepath.Join("services", "desk", "Dockerfile")},
		{service: "other", tag: "acme/other:1", root: root},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serviceBuilds = %+v, want %+v", got, want)
	}
}

// TestScenarioAnswerControls grades every answer-kind control of the
// scenario template samples and the example projects with the real
// answer_match rules: the reference must match; the empty control, the
// exploit and every mutant must not.
func TestScenarioAnswerControls(t *testing.T) {
	root := t.TempDir()
	var suites []string
	for _, tmpl := range []string{scaffold.TemplateMemory, scaffold.TemplateQA} {
		files, err := scaffold.Suite(scaffold.SuiteOptions{ID: "s-" + tmpl, Template: tmpl})
		if err != nil {
			t.Fatal(err)
		}
		if err := scaffold.WriteFiles(filepath.Join(root, "s-"+tmpl), files, false); err != nil {
			t.Fatal(err)
		}
		suites = append(suites, filepath.Join(root, "s-"+tmpl))
	}
	examples, err := filepath.Glob(filepath.Join("..", "..", "examples", "*", "benchmarks", "*", "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range examples {
		suites = append(suites, filepath.Dir(p))
	}
	graded := 0
	for _, dir := range suites {
		ids, err := filepath.Glob(filepath.Join(dir, "tasks", "*", "task.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, ty := range ids {
			taskID := filepath.Base(filepath.Dir(ty))
			s, task, err := suite.LoadTask(filepath.Dir(dir), filepath.Base(dir), taskID)
			if err != nil {
				t.Fatalf("load %s/%s: %v", dir, taskID, err)
			}
			if utils.OutputKind(s, task) != schemas.OutputAnswer {
				continue
			}
			spec, err := graders.LoadAnswerSpec(task)
			if err != nil {
				t.Fatalf("%s/%s: %v", s.ID, taskID, err)
			}
			want := map[string]bool{task.Controls.Reference: true, task.Controls.Empty: false, task.Controls.Exploit: false}
			for _, m := range task.Controls.Mutants {
				want[m] = false
			}
			for rel, pass := range want {
				answer := controlAnswer(t, filepath.Join(task.Dir, filepath.FromSlash(rel)))
				if ok, why := graders.MatchAnswer(spec, answer); ok != pass {
					t.Errorf("%s/%s %s: match %v, want %v (%s)", s.ID, taskID, rel, ok, pass, why)
				}
				graded++
			}
		}
	}
	if graded == 0 {
		t.Fatal("no answer controls graded")
	}
}

// controlAnswer is the answer an answer task's control gives: the file's
// text, or, for a script (the exploit), what it writes to BENCH_ANSWER_PATH
// when run in a scratch workspace, as admission runs it in the agent's place.
func controlAnswer(t *testing.T, path string) string {
	t.Helper()
	if !strings.HasSuffix(path, ".sh") {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(b))
	}
	ws := t.TempDir()
	cmd := exec.Command("sh", path)
	cmd.Dir = ws
	cmd.Env = append(os.Environ(), "BENCH_ANSWER_PATH="+filepath.Join(ws, "answer.txt"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, out)
	}
	b, err := os.ReadFile(filepath.Join(ws, "answer.txt"))
	if err != nil {
		t.Fatalf("%s wrote no answer: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}
