package scaffold

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// TestScenarioSuitesValidate writes every scenario template and checks that
// its sample task loads through the suite loader (and so the JSON Schemas)
// and that task validate finds no error in it.
func TestScenarioSuitesValidate(t *testing.T) {
	for _, tmpl := range ScenarioTemplates() {
		t.Run(tmpl, func(t *testing.T) {
			root, _ := newSuiteDir(t, "demo", tmpl)
			s, task, err := suite.LoadTask(root, "demo", scenarioSamples[tmpl])
			if err != nil {
				t.Fatalf("load sample: %v", err)
			}
			checkScenarioShape(t, tmpl, s, task)
			for _, p := range suite.ValidateTask(context.Background(), s, task) {
				if p.Severity == suite.SeverityError {
					t.Errorf("sample: %s: %s", p.Path, p.Message)
				}
			}
		})
	}
}

// checkScenarioShape checks what a scenario task must declare for its kind.
func checkScenarioShape(t *testing.T, tmpl string, s *schemas.Suite, task *schemas.Task) {
	t.Helper()
	switch {
	case stateTemplate(tmpl):
		if s.Output == nil || s.Output.Kind != schemas.OutputState || s.Grader != "verifier_command" || s.Verifier.Result != schemas.VerifierResultReward {
			t.Errorf("suite output %+v grader %s result %s", s.Output, s.Grader, s.Verifier.Result)
		}
		if len(task.Services) != 1 || len(task.Services[0].Export) != 1 || task.Services[0].Image != "bench/demo-inventory:1" {
			t.Fatalf("services = %+v", task.Services)
		}
		wantRole := schemas.ServiceRoleTool
		if tmpl == TemplateMCP {
			wantRole = schemas.ServiceRoleMCP
		}
		if task.Services[0].Role != wantRole || task.Services[0].Files["/seed/db.json"] != "seed/db.json" {
			t.Errorf("service = %+v", task.Services[0])
		}
		if !strings.HasSuffix(task.Controls.Reference, ".sh") || !strings.HasSuffix(task.Controls.Empty, ".sh") {
			t.Errorf("controls = %+v, want scripts", task.Controls)
		}
	default:
		if s.Output == nil || s.Output.Kind != schemas.OutputAnswer || s.Grader != "answer_match" {
			t.Errorf("suite output %+v grader %s", s.Output, s.Grader)
		}
		if task.Verifier.Answer == "" || !strings.HasSuffix(task.Controls.Reference, ".txt") {
			t.Errorf("verifier %+v controls %+v", task.Verifier, task.Controls)
		}
		if tmpl == TemplateMemory && len(task.Sessions) != 2 {
			t.Errorf("sessions = %+v", task.Sessions)
		}
	}
}

// TestScenarioStateControls runs the state templates' sample controls on the
// host against the real tool server: the reference must reach reward 1, the
// empty control, the mutant and the exploit reward 0.
func TestScenarioStateControls(t *testing.T) {
	if _, err := lookPython(); err != nil {
		t.Skip("python3 not installed")
	}
	for _, tmpl := range []string{TemplateToolUse, TemplateMCP} {
		t.Run(tmpl, func(t *testing.T) {
			_, dir := newSuiteDir(t, "demo", tmpl)
			task := filepath.Join(dir, "tasks", "restock")
			want := map[string]float64{
				"solution/reference.sh": 1, "controls/empty.sh": 0,
				"controls/mutant-wrong-item.sh": 0, "controls/exploit.sh": 0,
			}
			for script, reward := range want {
				got := RunStateControl(t, StateControl{
					Server: filepath.Join(dir, "services", "inventory", "server.py"), Service: "inventory",
					Seed: filepath.Join(task, "seed", "db.json"), Tests: filepath.Join(task, "verifier", "tests"),
					Script: filepath.Join(task, script), MCP: tmpl == TemplateMCP,
				})
				if got != reward {
					t.Errorf("%s: reward %v, want %v", script, got, reward)
				}
			}
		})
	}
}

// StateControl is one host run of a state task's control script against its
// tool server, graded by the task's verify.py.
type StateControl struct {
	Server, Service, Seed, Tests, Script string
	MCP                                  bool
}

// RunStateControl starts the tool server on a free loopback port, runs the
// script in a scratch workspace as admission would run it in the agent's
// place, runs verify.py on the server's state and returns the reward.
func RunStateControl(t *testing.T, c StateControl) float64 {
	t.Helper()
	work := t.TempDir()
	state := filepath.Join(work, "state", c.Service, "db.json")
	port := freePort(t)
	srv := exec.Command("python3", "-I", c.Server)
	srv.Env = append(os.Environ(), "PORT="+port, "SEED_PATH="+c.Seed, "STATE_PATH="+state)
	var srvLog strings.Builder
	srv.Stderr = &srvLog
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = srv.Process.Kill()
		_ = srv.Wait()
	}()
	base := "http://127.0.0.1:" + port
	waitHealthy(t, base+"/healthz", &srvLog)
	ws := filepath.Join(work, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	run := exec.Command("sh", c.Script)
	run.Dir = ws
	run.Env = append(os.Environ(), "TOOL_URL="+base,
		fmt.Sprintf(`BENCH_MCP_SERVERS={"%s":{"url":"%s/mcp"}}`, c.Service, base))
	if out, err := run.CombinedOutput(); err != nil {
		t.Logf("%s: %v\n%s", filepath.Base(c.Script), err, out)
	}
	out := filepath.Join(work, "out")
	verify := exec.Command("python3", "-I", filepath.Join(c.Tests, "verify.py"))
	verify.Env = append(os.Environ(), "GRADER_STATE="+filepath.Join(work, "state"), "GRADER_OUT="+out)
	if b, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("verify.py: %v\n%s", err, b)
	}
	var r struct {
		Reward float64 `json:"reward"`
	}
	b, err := os.ReadFile(filepath.Join(out, "reward.json"))
	if err != nil || json.Unmarshal(b, &r) != nil {
		t.Fatalf("reward.json: %v %s", err, b)
	}
	return r.Reward
}

func lookPython() (string, error) { return exec.LookPath("python3") }

func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
}

func waitHealthy(t *testing.T, url string, log *strings.Builder) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("server at %s never became healthy:\n%s", url, log.String())
}

func TestInitScenarioTask(t *testing.T) {
	for _, tmpl := range ScenarioTemplates() {
		t.Run(tmpl, func(t *testing.T) {
			root, dir := newSuiteDir(t, "demo", tmpl)
			res, err := InitScenarioTask(ScenarioTaskOptions{SuiteDir: dir, ID: "t02"})
			if err != nil {
				t.Fatal(err)
			}
			if res.Template != tmpl || res.From != scenarioSamples[tmpl] || !res.SuiteEdited || len(res.EditMe) == 0 {
				t.Fatalf("result = %+v", res)
			}
			s, task, err := suite.LoadTask(root, "demo", "t02")
			if err != nil {
				t.Fatalf("load t02: %v", err)
			}
			if task.ID != "t02" || task.Title != "Task t02" || len(task.Controls.Mutants) != 0 ||
				task.Provenance == nil || task.Provenance.CreatedBy != "task init" || task.Provenance.License != "proprietary" {
				t.Errorf("task = %+v provenance %+v", task, task.Provenance)
			}
			checkScenarioShape(t, tmpl, s, task)
			for _, m := range []string{"controls/mutant-wrong-item.sh", "controls/wrong-city.txt", "controls/old-date.txt"} {
				if _, err := os.Stat(filepath.Join(res.Dir, m)); err == nil {
					t.Errorf("mutant %s was copied", m)
				}
			}
			errs := editMeErrors(s, task)
			if len(errs) == 0 {
				t.Fatal("task validate passes a task that still holds EDIT ME markers")
			}
			for _, rel := range res.EditMe {
				stripEditMe(t, filepath.Join(res.Dir, filepath.FromSlash(rel)))
			}
			if errs := editMeErrors(s, task); len(errs) > 0 {
				t.Errorf("after removing the markers: %v", errs)
			}
		})
	}
}

// editMeErrors lists the error problems task validate reports for a task.
func editMeErrors(s *schemas.Suite, task *schemas.Task) []string {
	var out []string
	for _, p := range suite.ValidateTask(context.Background(), s, task) {
		if p.Severity == suite.SeverityError {
			out = append(out, p.Path+": "+p.Message)
		}
	}
	return out
}

// stripEditMe removes what task init added: the EDIT ME block of an
// instruction or the EDIT ME comment line.
func stripEditMe(t *testing.T, path string) {
	t.Helper()
	b := readFile(t, path)
	if strings.HasPrefix(b, editMeInstruction) {
		b = strings.TrimPrefix(b, editMeInstruction)
	} else {
		var kept []string
		for _, l := range strings.Split(b, "\n") {
			if !strings.HasPrefix(l, "# EDIT ME") && !strings.HasPrefix(l, EditMeMarker) {
				kept = append(kept, l)
			}
		}
		b = strings.Join(kept, "\n")
	}
	if err := os.WriteFile(path, []byte(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInitScenarioTaskFromAndEmbedded(t *testing.T) {
	_, dir := newSuiteDir(t, "demo", TemplateQA)
	if _, err := InitScenarioTask(ScenarioTaskOptions{SuiteDir: dir, ID: "t02", License: "MIT"}); err != nil {
		t.Fatal(err)
	}
	res, err := InitScenarioTask(ScenarioTaskOptions{SuiteDir: dir, ID: "t03", From: "t02", Kind: TemplateQA})
	if err != nil || res.From != "t02" {
		t.Fatalf("from t02: %+v %v", res, err)
	}
	if meta := readFile(t, filepath.Join(res.Dir, "task.yaml")); !strings.Contains(meta, "license: MIT") || !strings.Contains(meta, "id: t03") {
		t.Errorf("t03 task.yaml:\n%s", meta)
	}
	if instr := readFile(t, filepath.Join(res.Dir, "instruction.md")); strings.Count(instr, EditMeMarker) != 1 {
		t.Errorf("instruction marked %d times", strings.Count(instr, EditMeMarker))
	}
	for _, id := range []string{"hotel-cap", "t02", "t03"} {
		if err := os.RemoveAll(filepath.Join(dir, "tasks", id)); err != nil {
			t.Fatal(err)
		}
	}
	res, err = InitScenarioTask(ScenarioTaskOptions{SuiteDir: dir, ID: "t04"})
	if err != nil || !strings.Contains(res.From, "template qa") {
		t.Fatalf("embedded sample: %+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, "repo", "docs", "travel-policy.md")); err != nil {
		t.Error(err)
	}
}

func TestInitScenarioTaskRefusals(t *testing.T) {
	_, mcp := newSuiteDir(t, "demo", TemplateMCP)
	_, py := newSuiteDir(t, "pyapp", TemplatePythonPytest)
	tests := []struct {
		name string
		opts ScenarioTaskOptions
		want string
	}{
		{"bad id", ScenarioTaskOptions{SuiteDir: mcp, ID: "Bad Id"}, "task id"},
		{"exists", ScenarioTaskOptions{SuiteDir: mcp, ID: "restock"}, "exists"},
		{"kind mismatch", ScenarioTaskOptions{SuiteDir: mcp, ID: "t02", Kind: TemplateQA}, "written from template mcp"},
		{"unknown kind", ScenarioTaskOptions{SuiteDir: mcp, ID: "t02", Kind: "chat"}, "want one of"},
		{"repo suite", ScenarioTaskOptions{SuiteDir: py, ID: "t02"}, "pass -repo"},
		{"from itself", ScenarioTaskOptions{SuiteDir: mcp, ID: "t02", From: "t02"}, "another task"},
		{"from missing", ScenarioTaskOptions{SuiteDir: mcp, ID: "t02", From: "nope"}, "read task"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := InitScenarioTask(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(mcp, "tasks", "t02")); err == nil {
		t.Error("a refused task init left a folder behind")
	}
	if _, err := InitScenarioTask(ScenarioTaskOptions{SuiteDir: mcp, ID: "restock", From: "restock", Force: true}); err == nil {
		t.Error("want error copying a task onto itself")
	}
}

func TestRewriteTaskYAMLKeepsComments(t *testing.T) {
	src := []byte("# old head\nid: a\nversion: 2.0.0\ntitle: Old\nkind: feature # why\ncontrols:\n  reference: r.sh\n  mutants: [m.sh]\n")
	out, err := rewriteTaskYAML(src, ScenarioTaskOptions{ID: "b"}, TemplateMCP, "a")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"id: b\n", "version: 1.0.0\n", "title: Task b\n", "kind: feature # why\n", "mutants: []\n",
		"created_by: task init", "license: proprietary", "from the shape of task a"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "old head") {
		t.Errorf("old head comment kept:\n%s", s)
	}
}

func TestMarkScript(t *testing.T) {
	got := string(markScript([]byte("#!/bin/sh\necho hi\n")))
	if !strings.HasPrefix(got, "#!/bin/sh\n# EDIT ME") || !strings.HasSuffix(got, "echo hi\n") {
		t.Errorf("markScript = %q", got)
	}
	if got := string(markScript([]byte("echo hi\n"))); !strings.HasPrefix(got, "# EDIT ME") {
		t.Errorf("no shebang: %q", got)
	}
}

func TestScenarioSample(t *testing.T) {
	for _, tmpl := range ScenarioTemplates() {
		if !IsScenarioTemplate(tmpl) || ScenarioSample(tmpl) == "" {
			t.Errorf("%s: no sample", tmpl)
		}
	}
	if IsScenarioTemplate(TemplateGoTest) || ScenarioSample(TemplateGoTest) != "" {
		t.Error("go-test is not a scenario template")
	}
}
