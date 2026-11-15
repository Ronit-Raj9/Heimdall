package agentdojo

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// fakeDocker answers the importer's docker calls: no image present, a build
// that succeeds, and the hand-written catalog for `run`.
type fakeDocker struct {
	t        *testing.T
	present  bool
	calls    [][]string
	catalog  []byte
	buildErr bool
}

func (f *fakeDocker) Run(_ context.Context, args ...string) (*utils.DockerResult, error) {
	f.calls = append(f.calls, args)
	if args[0] == "image" {
		if f.present {
			return &utils.DockerResult{Stdout: []byte("sha256:abc\n")}, nil
		}
		return &utils.DockerResult{ExitCode: 1, Stderr: []byte("No such image")}, nil
	}
	return &utils.DockerResult{}, nil
}

func (f *fakeDocker) MustRun(ctx context.Context, args ...string) (*utils.DockerResult, error) {
	f.calls = append(f.calls, args)
	switch args[0] {
	case "build":
		if f.buildErr {
			return nil, os.ErrPermission
		}
		return &utils.DockerResult{}, nil
	case "run":
		return &utils.DockerResult{Stdout: f.catalog}, nil
	}
	f.t.Fatalf("unexpected docker call %v", args)
	return nil, nil
}

func newFake(t *testing.T) *fakeDocker {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "catalog-banking.json"))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeDocker{t: t, catalog: b}
}

func testImporter(d dockerRunner) *importer {
	return &importer{docker: d, now: func() time.Time { return time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC) }}
}

func importOpts(t *testing.T, extra map[string]string) importers.Options {
	t.Helper()
	from, err := filepath.Abs(filepath.Join("testdata", "upstream"))
	if err != nil {
		t.Fatal(err)
	}
	return importers.Options{BenchRoot: t.TempDir(), From: from, SuiteID: "agentdojo-banking", Extra: extra}
}

func TestRegistered(t *testing.T) {
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("agentdojo importer not registered")
	}
	info := im.Info()
	if info.Track != schemas.TrackSafety || info.Grading != schemas.GradingVerified || info.License != "MIT" {
		t.Fatalf("info %+v", info)
	}
}

func TestImport(t *testing.T) {
	fake := newFake(t)
	opts := importOpts(t, map[string]string{SetSuite: "banking", SetAttacks: "important_instructions,direct"})
	res, err := testImporter(fake).Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"banking-user-task-0", "banking-user-task-1"}; !reflect.DeepEqual(res.Tasks, want) {
		t.Fatalf("tasks %v, want %v", res.Tasks, want)
	}
	if len(res.Skipped) != 2 || res.Skipped[0].ID != "user_task_3" || !strings.Contains(res.Skipped[0].Reason, "empty run would pass") ||
		res.Skipped[1].ID != "user_task_2" || !strings.Contains(res.Skipped[1].Reason, "no injection vector") {
		t.Fatalf("skipped %+v", res.Skipped)
	}
	// docker: inspect (absent), build, then the catalog with no network.
	if len(fake.calls) != 3 || fake.calls[0][0] != "image" || fake.calls[1][0] != "build" || fake.calls[2][0] != "run" {
		t.Fatalf("docker calls %v", fake.calls)
	}
	if run := strings.Join(fake.calls[2], " "); !strings.Contains(run, "--network none") || !strings.HasSuffix(run, "catalog.py v1.2.2 banking") {
		t.Fatalf("catalog run %q", run)
	}
	tag := fake.calls[1][len(fake.calls[1])-2]
	if !strings.HasPrefix(tag, "bench/agentdojo:0.1.35-") || len(tag) != len("bench/agentdojo:0.1.35-")+12 {
		t.Fatalf("image tag %q", tag)
	}

	var suite schemas.Suite
	readYAML(t, filepath.Join(res.SuiteDir, "suite.yaml"), &suite)
	wantProbes := []string{
		"agentdojo-important-instructions-inj0", "agentdojo-important-instructions-inj1",
		"agentdojo-direct-inj0", "agentdojo-direct-inj1",
	}
	if suite.ID != "agentdojo-banking" || suite.Track != schemas.TrackSafety || suite.Grader != "verifier_command" ||
		suite.Verifier.Result != schemas.VerifierResultReward || suite.Output.Kind != schemas.OutputState ||
		!reflect.DeepEqual(suite.TaskIDs, res.Tasks) || !reflect.DeepEqual(suite.ProbeIDs, wantProbes) {
		t.Fatalf("suite %+v", suite)
	}
	if suite.Imported == nil || suite.Imported.License != "MIT" || suite.Imported.Importer != Name ||
		suite.Imported.Benchmark != "agentdojo@v1.2.2 banking (package 0.1.35)" || suite.Imported.Revision != "" {
		t.Fatalf("import record %+v", suite.Imported)
	}

	var task schemas.Task
	readYAML(t, filepath.Join(res.SuiteDir, "tasks", "banking-user-task-0", "task.yaml"), &task)
	if task.Output == nil || task.Output.Kind != schemas.OutputState || task.Output.AnswerPath != "answer.txt" {
		t.Fatalf("output %+v", task.Output)
	}
	if task.Environment == nil || task.Environment.VerifierImage != tag || task.Environment.Image != suite.BaseImage.Tag ||
		!strings.HasPrefix(suite.BaseImage.Tag, "bench/agentdojo-base:") || len(task.Services) != 1 {
		t.Fatalf("task environment %+v services %+v", task.Environment, task.Services)
	}
	svc := task.Services[0]
	wantEnv := map[string]string{"AGENTDOJO_SUITE": "banking", "AGENTDOJO_BENCHMARK_VERSION": "v1.2.2", "AGENTDOJO_USER_TASK": "user_task_0"}
	if svc.ID != serviceID || svc.Role != schemas.ServiceRoleMCP || svc.Image != tag || !reflect.DeepEqual(svc.Env, wantEnv) ||
		svc.Port != servicePort || svc.Ready == nil || svc.Ready.HTTP != "/healthz" || svc.MCP == nil || svc.MCP.Path != "/mcp" ||
		len(svc.Export) != 1 || svc.Export[0].ID != "run" || svc.Export[0].Path != "/state/run.json" {
		t.Fatalf("service %+v", svc)
	}
	if task.Controls.Reference != "controls/reference.sh" || task.Controls.Empty != "controls/empty.sh" || task.Difficulty != "easy" ||
		!reflect.DeepEqual(task.Controls.Mutants, []string{"controls/mutant1.sh"}) || task.Controls.Exploit != "controls/exploit.sh" {
		t.Fatalf("task %+v", task)
	}
	mutant := readFile(t, filepath.Join(res.SuiteDir, "tasks", "banking-user-task-0", "controls", "mutant1.sh"))
	if !strings.Contains(mutant, `"calls":[{"function":"read_file"`) || strings.Contains(mutant, "send_money") || !strings.Contains(mutant, `"answer":""`) {
		t.Fatalf("mutant drops the last call and the answer:\n%s", mutant)
	}
	var other schemas.Task
	readYAML(t, filepath.Join(res.SuiteDir, "tasks", "banking-user-task-1", "task.yaml"), &other)
	if len(other.Controls.Mutants) != 0 {
		t.Fatalf("a task whose near miss passes gets no mutant: %v", other.Controls.Mutants)
	}
	if _, err := os.Stat(filepath.Join(res.SuiteDir, "tasks", "banking-user-task-1", "controls", "mutant1.sh")); !os.IsNotExist(err) {
		t.Fatalf("mutant1.sh written for a task without a mutant: %v", err)
	}

	var pr schemas.ProbeSpec
	readYAML(t, filepath.Join(res.SuiteDir, "probes", "agentdojo-direct-inj1.yaml"), &pr)
	wantApply := map[string]map[string]string{serviceID: {"AGENTDOJO_ATTACK": "direct", "AGENTDOJO_INJECTION_TASK": "injection_task_1"}}
	if pr.Layer != schemas.LayerService || pr.PS2Family != schemas.FamilyHostileTool || !pr.GoalPreserving ||
		!reflect.DeepEqual(pr.Apply.ServiceEnv, wantApply) || !strings.Contains(pr.SafeBehavior, "new_password") {
		t.Fatalf("probe %+v", pr)
	}

	for _, f := range []string{
		".gitignore", "base/Dockerfile", "image/Dockerfile", "image/heimdall/server.py", "image/heimdall/verify.py",
		"image/heimdall/catalog.py", "image/heimdall/common.py", "image/upstream/pyproject.toml", "image/upstream/LICENSE",
		"image/upstream/README.md", "image/upstream/src/agentdojo/__init__.py",
		"image/upstream/src/agentdojo/data/suites/banking/environment.yaml",
		"tasks/banking-user-task-0/instruction.md", "tasks/banking-user-task-0/repo/README.md",
		"tasks/banking-user-task-0/verifier/tests/task.json", "tasks/banking-user-task-0/verifier/requirements.md",
	} {
		if _, err := os.Stat(filepath.Join(res.SuiteDir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	instr := readFile(t, filepath.Join(res.SuiteDir, "tasks", "banking-user-task-1", "instruction.md"))
	for _, want := range []string{"What is my balance?", "assists the user", "answer.txt", "`agentdojo`"} {
		if !strings.Contains(instr, want) {
			t.Errorf("instruction lacks %q:\n%s", want, instr)
		}
	}
	if task := readFile(t, filepath.Join(res.SuiteDir, "tasks", "banking-user-task-1", "verifier", "tests", "task.json")); !strings.Contains(task, `"user_task": "user_task_1"`) {
		t.Errorf("hidden task file %s", task)
	}
}

func TestImportExtendsAndSkipsExisting(t *testing.T) {
	opts := importOpts(t, map[string]string{SetSuite: "banking", SetInjectionTasks: "injection_task_1"})
	opts.IDs = []string{"user_task_1"}
	if _, err := testImporter(newFake(t)).Import(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	opts.IDs = nil
	opts.Extra = map[string]string{SetSuite: "banking", SetImage: "bench/agentdojo:prebuilt"}
	fake := newFake(t)
	res, err := testImporter(fake).Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 || fake.calls[0][0] != "run" || !strings.Contains(strings.Join(fake.calls[0], " "), "bench/agentdojo:prebuilt") {
		t.Fatalf("a prebuilt image must skip the build: %v", fake.calls)
	}
	if want := []string{"banking-user-task-0"}; !reflect.DeepEqual(res.Tasks, want) {
		t.Fatalf("tasks %v, want %v", res.Tasks, want)
	}
	var suite schemas.Suite
	readYAML(t, filepath.Join(res.SuiteDir, "suite.yaml"), &suite)
	if want := []string{"banking-user-task-1", "banking-user-task-0"}; !reflect.DeepEqual(suite.TaskIDs, want) {
		t.Fatalf("suite tasks %v, want %v", suite.TaskIDs, want)
	}
	if want := []string{"agentdojo-important-instructions-inj1", "agentdojo-important-instructions-inj0"}; !reflect.DeepEqual(suite.ProbeIDs, want) {
		t.Fatalf("suite probes %v, want %v", suite.ProbeIDs, want)
	}
	skipped := false
	for _, s := range res.Skipped {
		skipped = skipped || (s.ID == "user_task_1" && strings.Contains(s.Reason, "-force"))
	}
	if !skipped {
		t.Fatalf("existing task not skipped: %+v", res.Skipped)
	}
}

func TestImportErrors(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]string
		fake  func(*fakeDocker)
		want  string
	}{
		{"no suite", map[string]string{}, nil, "-set suite=<name> is required"},
		{"unknown setting", map[string]string{SetSuite: "banking", "model": "x"}, nil, `unknown setting "model"`},
		{"bad version", map[string]string{SetSuite: "banking", SetVersion: "latest"}, nil, "is not like"},
		{"suite not in clone", map[string]string{SetSuite: "slack"}, nil, `no suite "slack"`},
		{"unknown attack", map[string]string{SetSuite: "banking", SetAttacks: "manual"}, nil, `attack "manual"`},
		{"unknown injection task", map[string]string{SetSuite: "banking", SetInjectionTasks: "injection_task_9"}, nil, `"injection_task_9"`},
		{"build fails", map[string]string{SetSuite: "banking"}, func(f *fakeDocker) { f.buildErr = true }, "build image"},
		{"catalog for another suite", map[string]string{SetSuite: "banking", SetVersion: "v1.2"}, nil, "asked for banking v1.2"},
		{"bad catalog", map[string]string{SetSuite: "banking"}, func(f *fakeDocker) { f.catalog = []byte("{") }, "decode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newFake(t)
			if c.fake != nil {
				c.fake(fake)
			}
			_, err := testImporter(fake).Import(context.Background(), importOpts(t, c.extra))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
	if _, err := testImporter(newFake(t)).Import(context.Background(), importers.Options{}); err == nil {
		t.Fatal("empty options must fail")
	}
}

func TestBuildImageSkipsPresent(t *testing.T) {
	fake := newFake(t)
	fake.present = true
	if err := testImporter(fake).buildImage(context.Background(), "bench/agentdojo:x", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls %v", fake.calls)
	}
}

func TestSplitList(t *testing.T) {
	if got := splitList(" a, ,b ,"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if got := splitList(""); got != nil {
		t.Fatalf("got %v", got)
	}
}

func readYAML(t *testing.T, p string, out any) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		t.Fatalf("%s: %v\n%s", p, err, b)
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
