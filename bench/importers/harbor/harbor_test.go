package harbor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

var fixedNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// runImport imports from into a fresh bench root and returns the root and
// the result.
func runImport(t *testing.T, from, suiteID string, opts importers.Options) (string, *importers.Result) {
	t.Helper()
	root := t.TempDir()
	abs, err := filepath.Abs(from)
	if err != nil {
		t.Fatal(err)
	}
	opts.BenchRoot, opts.From, opts.SuiteID = root, abs, suiteID
	res, err := (&importer{now: func() time.Time { return fixedNow }}).Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return root, res
}

// loadSuite loads the written suite with the bench's own loader, which
// validates every file against the JSON Schemas, then lints and locks it.
func loadSuite(t *testing.T, root, id string) *schemas.Suite {
	t.Helper()
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load imported suite: %v", err)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint error: %s: %s", p.Path, p.Message)
		}
	}
	if _, err := suite.Lock(s, fixedNow); err != nil {
		t.Fatalf("lock imported suite: %v", err)
	}
	return s
}

func TestRegistered(t *testing.T) {
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("harbor importer not registered")
	}
	if info := im.Info(); info.Track != schemas.TrackTerminal || len(info.Benchmarks) == 0 {
		t.Errorf("info = %+v", info)
	}
}

func TestImportTerminalBench(t *testing.T) {
	root, res := runImport(t, filepath.Join("testdata", "tb"), "tb-sample", importers.Options{})
	if got := strings.Join(res.Tasks, ","); got != "hello-file,needs-compose,pip-check,solo-compose-yml" { // X2: compose tasks import (compose.go)
		t.Fatalf("tasks = %s", got)
	}
	wantSkips := map[string]string{
		"downloads-tests":   "fetches from the network at verify time",
		"fetching-solution": "the reference solution downloads at run time (apt-get install -y jq)",
		"honeypot":          "hack-verifiable task",
		"compose-refused":   "multi-container compose environment the bench cannot run as written", // X2
		"needs-gpu":         "needs 1 GPU(s)",
		"no-workdir":        "sets no literal WORKDIR",
	}
	if len(res.Skipped) != len(wantSkips) {
		t.Errorf("skipped = %+v", res.Skipped)
	}
	for _, s := range res.Skipped {
		if want := wantSkips[s.ID]; want == "" || !strings.Contains(s.Reason, want) {
			t.Errorf("skip %s: reason %q, want it to contain %q", s.ID, s.Reason, want)
		}
	}
	s := loadSuite(t, root, "tb-sample")
	if s.Track != schemas.TrackTerminal || s.Grader != "verifier_command" || s.Verifier.Mode != schemas.VerifierSnapshot ||
		s.Verifier.Result != schemas.VerifierResultReward || s.Verifier.ResultPath != "/grader/out/reward.json" {
		t.Errorf("suite = %+v", s)
	}
	if s.Verifier.TimeoutS != 660 || s.Workdir != "/app" && s.Workdir != "/srv/project" {
		t.Errorf("verifier timeout %d, workdir %s", s.Verifier.TimeoutS, s.Workdir)
	}
	imp := s.Imported
	if imp == nil || imp.Importer != "harbor" || imp.Benchmark != "terminal-bench@2.0" || imp.License != "Apache-2.0" || !imp.At.Equal(fixedNow) {
		t.Errorf("imported = %+v", imp)
	}
	hello := s.TaskByID("hello-file")
	if hello == nil {
		t.Fatal("hello-file not loaded")
	}
	env := hello.Environment
	if env == nil || env.Dockerfile != "environment/Dockerfile" || env.Context != "environment" || env.WorkspacePath != "/app" || !env.BuildNetwork || env.User != "root" {
		t.Errorf("environment = %+v", env)
	}
	if hello.Difficulty != "easy" || hello.Timeouts.AgentS != 300 || hello.Title != "Write a greeting file." ||
		strings.Join(hello.Tags, ",") != "category:file-operations,shell,files,basics" {
		t.Errorf("task = %+v", hello)
	}
	if p := hello.Provenance; p == nil || p.ImportedFrom != "terminal-bench@2.0" || p.License != "Apache-2.0" || p.CreatedBy == "" {
		t.Errorf("provenance = %+v", p)
	}
	if hello.Controls.Reference != "controls/reference.sh" || hello.Controls.Empty != "controls/empty.sh" || hello.Verifier.Tests != "verifier/tests" {
		t.Errorf("controls %+v, verifier %+v", hello.Controls, hello.Verifier)
	}
	dir := filepath.Join(root, "benchmarks", "tb-sample")
	for _, f := range []string{".gitignore", "base/Dockerfile", "tasks/hello-file/verifier/tests/upstream/test_outputs.py",
		"tasks/hello-file/verifier/tests/upstream/test.sh", "tasks/hello-file/environment/seed.txt", "tasks/hello-file/solution/greeting.txt", "tasks/hello-file/IMPORT.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, experimentFile)); err == nil {
		t.Error("a terminal suite got a skills experiment")
	}
	dockerfile := readFile(t, filepath.Join(dir, "tasks/hello-file/environment/Dockerfile"))
	for _, want := range []string{"COPY seed.txt /app/seed.txt\n", "RUN apt-get install -y curl\n", "venv --python 3.13 /opt/harbor-tests/venv1",
		"RUN mkdir -p /logs/verifier /tests", `ENV GREETING_LANG="en"`} {
		if !strings.Contains(dockerfile, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, dockerfile)
		}
	}
	pip := readFile(t, filepath.Join(dir, "tasks/pip-check/environment/Dockerfile"))
	if !strings.HasSuffix(pip, "USER worker\n") || !strings.Contains(pip, "USER root\nRUN apt-get update -qq && apt-get install -y gcc > /dev/null 2>&1\n") {
		t.Errorf("pip-check Dockerfile:\n%s", pip)
	}
	if wrapper := readFile(t, filepath.Join(dir, "tasks/pip-check/verifier/tests/harbor/verify.sh")); !strings.Contains(wrapper, "cd '/srv/project' ||") {
		t.Errorf("wrapper does not enter the workdir:\n%s", wrapper)
	}
}

func TestImportSkillsBench(t *testing.T) {
	root, res := runImport(t, filepath.Join("testdata", "skills"), "skills-sample", importers.Options{Extra: map[string]string{"agent": "agents/claude-code.yaml"}})
	if got := strings.Join(res.Tasks, ","); got != "csv-sum,stl-mass" || len(res.Skipped) != 0 {
		t.Fatalf("tasks = %s, skipped = %+v", got, res.Skipped)
	}
	s := loadSuite(t, root, "skills-sample")
	if s.Track != schemas.TrackSkills || s.Imported.Benchmark != "skillsfixture@unknown" || s.Imported.License != "MIT" {
		t.Errorf("suite track %s, imported %+v", s.Track, s.Imported)
	}
	dir := filepath.Join(root, "benchmarks", "skills-sample")
	stl := s.TaskByID("stl-mass")
	if stl.Skills != "skills" || stl.Environment.WorkspacePath != "/root" {
		t.Errorf("stl-mass = %+v %+v", stl, stl.Environment)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks/stl-mass/skills/mesh-analysis/SKILL.md")); err != nil {
		t.Errorf("skill not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks/stl-mass/environment/skills")); !os.IsNotExist(err) {
		t.Errorf("skills left in the build context: %v", err)
	}
	if df := readFile(t, filepath.Join(dir, "tasks/stl-mass/environment/Dockerfile")); strings.Contains(df, "\nCOPY skills") {
		t.Errorf("skills still baked in:\n%s", df)
	}
	csv := s.TaskByID("csv-sum")
	if csv.Environment.WorkspacePath != "/app" || csv.Timeouts.AgentS != 600 || csv.Difficulty != "medium" || csv.Skills != "skills" {
		t.Errorf("csv-sum = %+v %+v", csv, csv.Environment)
	}
	wrapper := readFile(t, filepath.Join(dir, "tasks/csv-sum/verifier/tests/harbor/verify.sh"))
	if !strings.Contains(wrapper, "MOUNT=/verifier\n") || !strings.Contains(wrapper, "export SUM_PORT='8090'\n") {
		t.Errorf("csv-sum wrapper:\n%s", wrapper)
	}
	if ref := readFile(t, filepath.Join(dir, "tasks/stl-mass/controls/reference.sh")); !strings.Contains(ref, "<<'HARBOR_SKILLS'") || !strings.Contains(ref, "for d in '/skills'; do") {
		t.Errorf("stl-mass reference does not install the skills:\n%s", ref)
	}
	if df := readFile(t, filepath.Join(dir, "tasks/stl-mass/environment/Dockerfile")); !strings.Contains(df, "RUN mkdir -p /logs/verifier /tests /solution /skills") {
		t.Errorf("stl-mass Dockerfile lacks the baked folders:\n%s", df)
	}
	if ref := readFile(t, filepath.Join(dir, "tasks/csv-sum/controls/reference.sh")); !strings.Contains(ref, "MOUNT=/oracle\n") || !strings.Contains(ref, "export WORKSPACE='/app'\n") {
		t.Errorf("csv-sum reference:\n%s", ref)
	}
	var exp schemas.Experiment
	if err := yaml.Unmarshal([]byte(readFile(t, filepath.Join(dir, experimentFile))), &exp); err != nil {
		t.Fatalf("experiment: %v", err)
	}
	if exp.Suite != "skills-sample@1.0.0" || exp.Base != "agents/claude-code.yaml" || exp.Arms["A"].Set["skills.enabled"] != false ||
		exp.Arms["B"].Set["skills.enabled"] != true || !exp.Conditions[0].Tasks.All {
		t.Errorf("experiment = %+v", exp)
	}
}

func TestImportOptions(t *testing.T) {
	from := filepath.Join("testdata", "tb")
	t.Run("ids and missing ids", func(t *testing.T) {
		_, res := runImport(t, from, "s", importers.Options{IDs: []string{"pip-check", "nope"}})
		if strings.Join(res.Tasks, ",") != "pip-check" || len(res.Skipped) != 1 || res.Skipped[0].ID != "nope" {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("limit", func(t *testing.T) {
		_, res := runImport(t, from, "s", importers.Options{Limit: 1})
		if strings.Join(res.Tasks, ",") != "hello-file" {
			t.Errorf("tasks = %v", res.Tasks)
		}
	})
	t.Run("keep networked", func(t *testing.T) {
		_, res := runImport(t, from, "s", importers.Options{IDs: []string{"fetching-solution"}, Extra: map[string]string{"keep_networked": "true"}})
		if strings.Join(res.Tasks, ",") != "fetching-solution" {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("keep hack-verifiable", func(t *testing.T) {
		root, res := runImport(t, from, "s", importers.Options{IDs: []string{"honeypot"}, Extra: map[string]string{"hack_verifiable": "keep"}})
		if strings.Join(res.Tasks, ",") != "honeypot" {
			t.Fatalf("result = %+v", res)
		}
		s := loadSuite(t, root, "s")
		hp := s.TaskByID("honeypot")
		if strings.Join(hp.Protected, ",") != "admin/**" || !contains(hp.Tags, "hack-verifiable") {
			t.Errorf("task = %+v", hp)
		}
		if notes := readFile(t, filepath.Join(root, "benchmarks/s/tasks/honeypot/IMPORT.md")); !strings.Contains(notes, "reading them is not detected") {
			t.Errorf("IMPORT.md:\n%s", notes)
		}
	})
	t.Run("prebuilt image", func(t *testing.T) {
		root, res := runImport(t, from, "s", importers.Options{IDs: []string{"hello-file", "pip-check"}, Extra: map[string]string{"image": "prebuilt"}})
		if strings.Join(res.Tasks, ",") != "hello-file" || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "names no docker_image") {
			t.Fatalf("result = %+v", res)
		}
		df := readFile(t, filepath.Join(root, "benchmarks/s/tasks/hello-file/environment/Dockerfile"))
		if !strings.Contains(df, "FROM example/hello-file:20251031\nWORKDIR /app\n") {
			t.Errorf("Dockerfile:\n%s", df)
		}
	})
	t.Run("overrides", func(t *testing.T) {
		root, _ := runImport(t, from, "s", importers.Options{IDs: []string{"hello-file"}, Extra: map[string]string{
			"benchmark": "terminal-bench@2.1", "source": "https://example.com/tb", "revision": "abc", "license": "MIT"}})
		s := loadSuite(t, root, "s")
		if i := s.Imported; i.Benchmark != "terminal-bench@2.1" || i.Source != "https://example.com/tb" || i.Revision != "abc" || i.License != "MIT" {
			t.Errorf("imported = %+v", i)
		}
	})
}

func TestImportExtendAndForce(t *testing.T) {
	root := t.TempDir()
	from, err := filepath.Abs(filepath.Join("testdata", "tb"))
	if err != nil {
		t.Fatal(err)
	}
	im := &importer{now: func() time.Time { return fixedNow }}
	run := func(ids []string, force bool) *importers.Result {
		res, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "grow", IDs: ids, Force: force})
		if err != nil {
			t.Fatalf("Import %v: %v", ids, err)
		}
		return res
	}
	run([]string{"hello-file"}, false)
	run([]string{"pip-check"}, false)
	s := loadSuite(t, root, "grow")
	if strings.Join(s.TaskIDs, ",") != "hello-file,pip-check" || s.Workdir != "/app" {
		t.Errorf("tasks = %v, workdir %s", s.TaskIDs, s.Workdir)
	}
	if _, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "grow", IDs: []string{"hello-file"}}); err == nil {
		t.Error("re-import without -force wrote a task")
	}
	if res := run([]string{"hello-file"}, true); len(res.Tasks) != 1 {
		t.Errorf("forced re-import = %+v", res)
	}
}

func TestImportErrors(t *testing.T) {
	im := &importer{now: time.Now}
	cases := []struct {
		name string
		opts importers.Options
		want string
	}{
		{"bad suite id", importers.Options{From: "testdata/tb", SuiteID: "Bad Id"}, "suite id"},
		{"missing from", importers.Options{From: "testdata/none", SuiteID: "s"}, "is not a folder"},
		{"no tasks", importers.Options{From: "testdata", SuiteID: "s", Extra: map[string]string{"tasks_dir": "tb/hello-file/tests"}}, "no task folder"},
		{"unknown setting", importers.Options{From: "testdata/tb", SuiteID: "s", Extra: map[string]string{"colour": "red"}}, "unknown setting"},
		{"bad track", importers.Options{From: "testdata/tb", SuiteID: "s", Extra: map[string]string{"track": "coding"}}, "want terminal or skills"},
		{"bad image", importers.Options{From: "testdata/tb", SuiteID: "s", Extra: map[string]string{"image": "pull"}}, "want build or prebuilt"},
		{"bad hack_verifiable", importers.Options{From: "testdata/tb", SuiteID: "s", Extra: map[string]string{"hack_verifiable": "yes"}}, "want skip or keep"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.opts.BenchRoot = t.TempDir()
			if _, err := im.Import(context.Background(), c.opts); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("got %v, want error containing %q", err, c.want)
			}
		})
	}
}

// TestReferenceScriptUnpacks runs the generated reference control's unpack
// step on the host with sh, base64 and tar, against a temporary folder: the
// solution files must come back byte for byte. solve.sh itself is not run.
func TestReferenceScriptUnpacks(t *testing.T) {
	for _, tool := range []string{"sh", "base64", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	root, _ := runImport(t, filepath.Join("testdata", "tb"), "ref", importers.Options{IDs: []string{"hello-file"}})
	script := readFile(t, filepath.Join(root, "benchmarks/ref/tasks/hello-file/controls/reference.sh"))
	out := t.TempDir()
	start := strings.Index(script, "base64 -d")
	end := strings.Index(script, "rm -f /tmp/harbor-solution.tgz")
	if start < 0 || end < 0 {
		t.Fatalf("unexpected script:\n%s", script)
	}
	unpack := strings.NewReplacer("/tmp/harbor-solution.tgz", filepath.Join(out, "s.tgz"), `"$SOL"`, out).Replace(script[start:end])
	cmd := exec.Command("sh", "-c", unpack)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("unpack: %v\n%s", err, b)
	}
	for _, f := range []string{"solve.sh", "greeting.txt"} {
		want := readFile(t, filepath.Join("testdata", "tb", "hello-file", "solution", f))
		if got := readFile(t, filepath.Join(out, f)); got != want {
			t.Errorf("%s: got %q, want %q", f, got, want)
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestImportRealClone imports a real upstream clone when BENCH_HARBOR_FROM
// names one (for example datasets/terminal-bench-2). BENCH_HARBOR_ROOT keeps
// the written suite in that bench root instead of a temporary folder,
// BENCH_HARBOR_SUITE names it, BENCH_HARBOR_IDS limits the tasks and
// BENCH_HARBOR_SET holds settings as k=v,k=v. It
// needs no network and no Docker; it is skipped otherwise.
func TestImportRealClone(t *testing.T) {
	from := os.Getenv("BENCH_HARBOR_FROM")
	if from == "" {
		t.Skip("BENCH_HARBOR_FROM not set")
	}
	root := os.Getenv("BENCH_HARBOR_ROOT")
	if root == "" {
		root = t.TempDir()
	}
	id := os.Getenv("BENCH_HARBOR_SUITE")
	if id == "" {
		id = "harbor-real"
	}
	opts := importers.Options{BenchRoot: root, From: from, SuiteID: id, Force: true}
	if ids := os.Getenv("BENCH_HARBOR_IDS"); ids != "" {
		opts.IDs = strings.Split(ids, ",")
	}
	if set := os.Getenv("BENCH_HARBOR_SET"); set != "" {
		opts.Extra = map[string]string{}
		for _, kv := range strings.Split(set, ",") {
			k, v, _ := strings.Cut(kv, "=")
			opts.Extra[k] = v
		}
	}
	res, err := (&importer{now: time.Now}).Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	t.Logf("imported %d task(s): %s", len(res.Tasks), strings.Join(res.Tasks, ", "))
	for _, s := range res.Skipped {
		t.Logf("skipped %s: %s", s.ID, s.Reason)
	}
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, p := range suite.Lint(s) {
		t.Logf("lint %s %s: %s", p.Severity, p.Path, p.Message)
	}
}

func TestSkillRoots(t *testing.T) {
	dir := t.TempDir()
	body := "for r in ('/app/skills', '/root/.claude/skills'):\n    p = r + '/x'\n# relative skills/ and ../skills are ignored\n"
	if err := os.WriteFile(filepath.Join(dir, "solve.py"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(skillRoots(dir), ","); got != "/skills,/app/skills,/root/.claude/skills" {
		t.Errorf("roots = %s", got)
	}
}

// A suite another importer wrote is refused before any task is staged, so
// its grader, provenance and tasks stay as they were.
func TestImportRefusesForeignSuite(t *testing.T) {
	root := t.TempDir()
	from, err := filepath.Abs(filepath.Join("testdata", "tb"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "benchmarks", "native-like")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "id: native-like\nversion: \"1\"\ngrader: answer_match\ntasks: [fx-count-1]\nimported:\n  importer: longmemeval\n  benchmark: longmemeval-cleaned@oracle\n"
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	im := &importer{now: func() time.Time { return fixedNow }}
	_, err = im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "native-like", IDs: []string{"hello-file"}})
	if err == nil || !strings.Contains(err.Error(), "not written by the harbor importer") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "suite.yaml")); got != orig {
		t.Fatalf("suite.yaml changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", "hello-file")); !os.IsNotExist(err) {
		t.Fatalf("task staged into a refused suite: %v", err)
	}
	// A Harbor suite of another benchmark is refused as well.
	if _, err := im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "tb", IDs: []string{"hello-file"}}); err != nil {
		t.Fatal(err)
	}
	_, err = im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "tb", IDs: []string{"pip-check"},
		Extra: map[string]string{"benchmark": "other@1"}})
	if err == nil || !strings.Contains(err.Error(), "not other@1") {
		t.Fatalf("other benchmark: err = %v", err)
	}
}

func TestImportSeparateVerifier(t *testing.T) {
	from := filepath.Join("testdata", "separate")
	t.Run("skipped by default", func(t *testing.T) {
		abs, _ := filepath.Abs(from)
		res, err := (&importer{now: func() time.Time { return fixedNow }}).Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: abs, SuiteID: "s"})
		if err == nil || res == nil || len(res.Skipped) != 2 || !strings.Contains(res.Skipped[0].Reason, "-set verifier=separate") {
			t.Errorf("result = %+v, err = %v", res, err)
		}
	})
	t.Run("mapped", func(t *testing.T) {
		root, res := runImport(t, from, "s", importers.Options{Extra: map[string]string{"verifier": "separate", "image": "prebuilt"}})
		if strings.Join(res.Tasks, ",") != "sep-ok" || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "service container") {
			t.Fatalf("result = %+v", res)
		}
		s := loadSuite(t, root, "s")
		env := s.TaskByID("sep-ok").Environment
		if !strings.HasPrefix(env.VerifierImage, "example/sep-ok-verifier@sha256:") || strings.Join(env.VerifierArtifacts, ",") != "/app/greeting.txt,/app/out/" {
			t.Errorf("environment = %+v", env)
		}
		if v := readFile(t, filepath.Join(root, "benchmarks/s/tasks/sep-ok/verifier/tests/harbor/verify.sh")); !strings.Contains(v, "cp -a /grader/collect/. /") {
			t.Errorf("verify.sh:\n%s", v)
		}
	})
	t.Run("bad value", func(t *testing.T) {
		if _, err := readSettings(map[string]string{"verifier": "x"}); err == nil {
			t.Error("want an error")
		}
	})
}
