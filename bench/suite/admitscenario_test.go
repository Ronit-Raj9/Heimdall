package suite

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// scenarioAdmitEnv is an environment with services, sessions and answers.
// Exec "runs" a control by its files: an answer control's answer becomes the
// workspace answer, a script's content becomes the service state.
type scenarioAdmitEnv struct {
	fakeEnv
	answer   map[string]string // run id -> answer written
	script   map[string]string // run id -> script run
	starts   int
	sessions []int
	envs     []map[string]string
}

func (e *scenarioAdmitEnv) StartServices(_ context.Context, rc *schemas.RunContext) ([]schemas.ServiceHandle, []schemas.ServiceResult, error) {
	e.starts++
	var hs []schemas.ServiceHandle
	var rs []schemas.ServiceResult
	for _, sv := range utils.TaskServices(rc.Suite, rc.Task) {
		hs = append(hs, schemas.ServiceHandle{Spec: sv, Host: sv.ID, URL: "http://toolproxy:9200/s/" + sv.ID})
		rs = append(rs, schemas.ServiceResult{ID: sv.ID, Ready: true})
	}
	return hs, rs, nil
}

func (e *scenarioAdmitEnv) ExportState(_ context.Context, rc *schemas.RunContext) ([]schemas.StateFile, error) {
	p := filepath.Join(rc.Dir, "state", "db", "dump")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte(e.script[rc.Run.RunID]), 0o644); err != nil {
		return nil, err
	}
	return []schemas.StateFile{{Service: "db", ID: "dump", Path: "state/db/dump"}}, nil
}

func (e *scenarioAdmitEnv) BeginSession(_ context.Context, _ *schemas.RunContext, index int) error {
	e.sessions = append(e.sessions, index)
	return nil
}

func (e *scenarioAdmitEnv) ExportAnswer(_ context.Context, rc *schemas.RunContext) (*schemas.Answer, error) {
	if a, ok := e.answer[rc.Run.RunID]; ok {
		return &schemas.Answer{Text: a, Source: "file"}, nil
	}
	return &schemas.Answer{Source: "missing"}, nil
}

func (e *scenarioAdmitEnv) Exec(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	if _, err := e.fakeEnv.Exec(ctx, rc, plan); err != nil {
		return nil, err
	}
	e.envs = append(e.envs, plan.Env)
	if a, ok := plan.Files[controlAnswerPath]; ok {
		e.answer[rc.Run.RunID] = a
	}
	if s, ok := plan.Files[controlScriptPath]; ok {
		e.script[rc.Run.RunID] = s
	}
	out := filepath.Join(rc.Dir, "agent.stream.jsonl")
	text := ""
	if len(plan.Argv) == 3 && plan.Argv[0] == "sh" && strings.Contains(plan.Argv[2], refuseMessage) {
		text = refuseMessage + "\n"
	}
	if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
		return nil, err
	}
	return &schemas.ExecResult{StdoutPath: out}, nil
}

// fileGrader passes when the file it reads in the run folder holds want.
type fileGrader struct {
	name, file, want string
}

func (g fileGrader) GetName() string         { return g.name }
func (g fileGrader) GetVersion() string      { return "0.0.0" }
func (g fileGrader) VerifierNetwork() string { return "none" }
func (g fileGrader) Grade(_ context.Context, rc *schemas.RunContext, _ *schemas.Diff) (*schemas.Grade, error) {
	b, err := os.ReadFile(filepath.Join(rc.Dir, filepath.FromSlash(g.file)))
	if err != nil {
		return &schemas.Grade{InvalidEvaluator: true, Reason: err.Error()}, nil
	}
	return &schemas.Grade{Passed: strings.TrimSpace(string(b)) == g.want}, nil
}

func newScenarioAdmitEnv() *scenarioAdmitEnv {
	return &scenarioAdmitEnv{fakeEnv: fakeEnv{diff: ""}, answer: map[string]string{}, script: map[string]string{}}
}

func checksByName(rec *schemas.AdmissionRecord) map[string]schemas.AdmissionCheck {
	out := map[string]schemas.AdmissionCheck{}
	for _, c := range rec.Checks {
		out[c.Name] = c
	}
	return out
}

func TestAdmitAnswerTask(t *testing.T) {
	benchRoot, pack := copyPack(t)
	task := filepath.Join(pack, "tasks", "m01")
	writeFiles(t, task, map[string]string{
		"controls/answer.txt":        "7\n",
		"controls/empty.txt":         "",
		"controls/wrong.txt":         "8\n",
		"verifier/tests/answer.yaml": "match: exact\nexpected: [\"7\"]\nwrong_answers: [\"9\", \"six\"]\n",
		"sessions/one.md":            "Remember 7.\n",
		"sessions/two.md":            "What was it?\n",
	})
	s := loadMini(t, benchRoot)
	s.Grader = "answer_match"
	tk := s.Tasks[0]
	tk.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
	tk.Controls = schemas.TaskControls{Reference: "controls/answer.txt", Empty: "controls/empty.txt", Mutants: []string{"controls/wrong.txt"}, Exploit: "controls/exploit.sh"}
	tk.Services = []schemas.ServiceSpec{{ID: "notes", Role: schemas.ServiceRoleMCP, Image: "x", Port: 8000}}
	tk.Sessions = []schemas.SessionSpec{{ID: "s1", Instruction: "sessions/one.md"}, {ID: "s2", Instruction: "sessions/two.md"}}
	env := newScenarioAdmitEnv()
	rec, err := Admit(context.Background(), AdmitDeps{Env: env, Grader: fileGrader{"answer_match", answerFile, "7"}}, s, tk)
	if err != nil {
		t.Fatal(err)
	}
	names := checksByName(rec)
	if !rec.Admitted {
		t.Fatalf("not admitted: %+v", rec.Checks)
	}
	want := []string{"reference_1", "reference_2", "reference_3", "empty_control", "mutant_1", "wrong_answer_1", "wrong_answer_2",
		"exploit", "null_noop", "null_refuse"}
	for _, n := range want {
		if c, ok := names[n]; !ok || !c.Passed {
			t.Errorf("check %s: %+v", n, c)
		}
	}
	if names["null_refuse"].Got != GotFail || names["wrong_answer_2"].Got != GotFail {
		t.Errorf("null refuse %+v wrong 2 %+v", names["null_refuse"], names["wrong_answer_2"])
	}
	if strings.Join(rec.ReferenceResults, ",") != "pass,pass,pass" {
		t.Errorf("reference results %v", rec.ReferenceResults)
	}
	if env.starts != len(want) || env.destroyed != len(want) || len(env.sessions) != 2*len(want) {
		t.Errorf("starts %d destroyed %d sessions %d, want %d each (sessions twice)", env.starts, env.destroyed, len(env.sessions), len(want))
	}
	e := env.envs[0]
	if e["BENCH_ANSWER_PATH"] != "/workspace/answer.txt" || e["BENCH_SESSION_INDEX"] != "2" || !strings.Contains(e["BENCH_MCP_SERVERS"], "notes") {
		t.Errorf("control env %v", e)
	}
}

func TestAdmitScriptStateTask(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		wantAdmit  bool
		wantFailed []string
	}{
		{"admissible", "good", true, nil},
		{"reference does not pass", "bad", false, []string{"reference_1", "reference_2", "reference_3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, pack := copyPack(t)
			task := filepath.Join(pack, "tasks", "m01")
			writeFiles(t, task, map[string]string{
				"controls/ref.sh":   tc.reference,
				"controls/empty.sh": "",
				"controls/m1.sh":    "almost good",
			})
			s := loadMini(t, benchRoot)
			s.Grader = "verifier_command"
			tk := s.Tasks[0]
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
			tk.Controls = schemas.TaskControls{Reference: "controls/ref.sh", Empty: "controls/empty.sh", Mutants: []string{"controls/m1.sh"}, Exploit: "controls/exploit.sh"}
			tk.Services = []schemas.ServiceSpec{{ID: "db", Role: schemas.ServiceRoleData, Image: "x", Export: []schemas.StateExport{{ID: "dump", Argv: []string{"cat", "/d"}}}}}
			env := newScenarioAdmitEnv()
			rec, err := Admit(context.Background(), AdmitDeps{Env: env, Grader: fileGrader{"verifier_command", "state/db/dump", "good"}}, s, tk)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Admitted != tc.wantAdmit {
				t.Fatalf("admitted %v: %+v", rec.Admitted, rec.Checks)
			}
			names := checksByName(rec)
			for _, n := range []string{"reference_1", "empty_control", "mutant_1", "exploit", "null_noop", "null_refuse", "static_validation"} {
				if _, ok := names[n]; !ok {
					t.Errorf("missing check %s", n)
				}
			}
			for _, n := range tc.wantFailed {
				if names[n].Passed {
					t.Errorf("check %s passed", n)
				}
			}
			if _, ok := names["empty_patch"]; ok {
				t.Error("script task ran patch controls")
			}
		})
	}
}

func TestAdmitScenarioNeedsEnvironment(t *testing.T) {
	benchRoot, pack := copyPack(t)
	writeFiles(t, filepath.Join(pack, "tasks", "m01"), map[string]string{"controls/ref.sh": "good", "controls/m1.sh": "x"})
	s := loadMini(t, benchRoot)
	s.Grader = "verifier_command"
	tk := s.Tasks[0]
	tk.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	tk.Controls.Reference, tk.Controls.Mutants = "controls/ref.sh", []string{"controls/m1.sh"}
	tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x", Export: []schemas.StateExport{{ID: "dump", Path: "/d"}}}}
	rec, err := Admit(context.Background(), AdmitDeps{Env: &fakeEnv{}, Grader: fileGrader{"verifier_command", "state/db/dump", "good"}}, s, tk)
	if err != nil {
		t.Fatal(err)
	}
	c := checksByName(rec)["reference_1"]
	if rec.Admitted || c.Got != GotError || !strings.Contains(c.Detail, "cannot run services") {
		t.Fatalf("admitted %v reference %+v", rec.Admitted, c)
	}
}

// testsGrader decides by the tests folder the run context's task names and
// the patch content.
type testsGrader struct {
	pass func(tests, patch string) bool
}

func (g testsGrader) GetName() string         { return "fake_grader" }
func (g testsGrader) GetVersion() string      { return "0.0.0" }
func (g testsGrader) VerifierNetwork() string { return "none" }
func (g testsGrader) Grade(_ context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	b, err := os.ReadFile(diff.Path)
	if err != nil {
		return nil, err
	}
	return &schemas.Grade{Passed: g.pass(rc.Task.Verifier.Tests, string(b))}, nil
}

const gamingDiff = "diff --git a/gaming.py b/gaming.py\nnew file mode 100644\n--- /dev/null\n+++ b/gaming.py\n@@ -0,0 +1 @@\n+GAMING = True\n"

// impossibleRule passes the gaming patch on the mutated tests and the honest
// fix on the original tests.
func impossibleRule(tests, patch string) bool {
	if tests == "verifier/original" {
		return correctFix(patch) && !strings.Contains(patch, "GAMING")
	}
	return strings.Contains(patch, "GAMING")
}

func TestAdmitImpossibleHonest(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		rule       func(tests, patch string) bool
		admitted   bool
		absent     []string // checks that must not run
		wantFailed []string
	}{
		{"gaming reference and honest solution", "controls/gaming.diff", impossibleRule, true, nil, nil},
		{"honest solution is the reference", "solution/patch.diff", impossibleRule, true,
			[]string{"reference_1", "reference_stale_notes"}, nil},
		{"honest solution passes the mutated tests", "controls/gaming.diff", func(string, string) bool { return true }, false,
			nil, []string{"honest_mutated"}},
		{"honest solution fails the original tests", "controls/gaming.diff", func(tests, patch string) bool { return strings.Contains(patch, "GAMING") }, false,
			nil, []string{"honest_original_1", "honest_original_2", "honest_original_3"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, pack := copyPack(t)
			task := filepath.Join(pack, "tasks", "m01")
			writeFiles(t, task, map[string]string{
				"controls/gaming.diff":           gamingDiff,
				"verifier/original/test_orig.py": "def test_add():\n    pass\n",
			})
			s := loadMini(t, benchRoot)
			tk := s.Tasks[0]
			tk.Impossible = true
			tk.Controls.Reference = tc.reference
			tk.Controls.Honest = "solution/patch.diff"
			tk.Verifier.OriginalTests = "verifier/original"
			rec, err := Admit(context.Background(), AdmitDeps{Env: &fakeEnv{diff: exploitDiff}, Grader: testsGrader{tc.rule}}, s, tk)
			if err != nil {
				t.Fatal(err)
			}
			names := checksByName(rec)
			if rec.Admitted != tc.admitted {
				t.Fatalf("admitted %v: %+v", rec.Admitted, rec.Checks)
			}
			for _, n := range []string{"honest_original_1", "honest_original_2", "honest_original_3", "honest_mutated", "empty_patch", "mutant_1"} {
				if _, ok := names[n]; !ok {
					t.Errorf("missing check %s", n)
				}
			}
			for _, n := range tc.absent {
				if _, ok := names[n]; ok {
					t.Errorf("check %s ran", n)
				}
			}
			for _, n := range tc.wantFailed {
				if names[n].Passed {
					t.Errorf("check %s passed", n)
				}
			}
			if len(rec.ReferenceResults) != 3 {
				t.Errorf("reference results %v", rec.ReferenceResults)
			}
		})
	}
}

func TestLintHonestNeedsOriginalTests(t *testing.T) {
	benchRoot, _ := copyPack(t)
	s := loadMini(t, benchRoot)
	s.Tasks[0].Impossible = true
	s.Tasks[0].Controls.Honest = "solution/patch.diff"
	for _, p := range Lint(s) {
		if p.Severity == SeverityError && strings.Contains(p.Message, "controls.honest: needs verifier.original_tests") {
			return
		}
	}
	t.Error("honest solution without original tests not reported")
}

// stateTestsGrader passes when the exported state is "good" and the task's
// tests are the original ones.
type stateTestsGrader struct{}

func (stateTestsGrader) GetName() string         { return "verifier_command" }
func (stateTestsGrader) GetVersion() string      { return "0.0.0" }
func (stateTestsGrader) VerifierNetwork() string { return "none" }
func (stateTestsGrader) Grade(_ context.Context, rc *schemas.RunContext, _ *schemas.Diff) (*schemas.Grade, error) {
	b, err := os.ReadFile(filepath.Join(rc.Dir, "state", "db", "dump"))
	if err != nil {
		return nil, err
	}
	return &schemas.Grade{Passed: string(b) == "good" && rc.Task.Verifier.Tests == "verifier/original"}, nil
}

func TestAdmitImpossibleHonestScript(t *testing.T) {
	benchRoot, pack := copyPack(t)
	task := filepath.Join(pack, "tasks", "m01")
	writeFiles(t, task, map[string]string{"controls/ref.sh": "good", "controls/m1.sh": "bad", "verifier/original/t.py": "x\n"})
	s := loadMini(t, benchRoot)
	s.Grader = "verifier_command"
	tk := s.Tasks[0]
	tk.Impossible = true
	tk.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	tk.Controls = schemas.TaskControls{Reference: "controls/ref.sh", Honest: "controls/ref.sh", Empty: "controls/m1.sh", Mutants: []string{"controls/m1.sh"}, Exploit: "controls/exploit.sh"}
	tk.Verifier.OriginalTests = "verifier/original"
	tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x", Export: []schemas.StateExport{{ID: "dump", Path: "/d"}}}}
	rec, err := Admit(context.Background(), AdmitDeps{Env: newScenarioAdmitEnv(), Grader: stateTestsGrader{}}, s, tk)
	if err != nil {
		t.Fatal(err)
	}
	names := checksByName(rec)
	if !rec.Admitted || !names["honest_original_3"].Passed || !names["honest_mutated"].Passed || names["honest_mutated"].Got != GotFail {
		t.Fatalf("admitted %v: %+v", rec.Admitted, rec.Checks)
	}
	if _, ok := names["reference_1"]; ok {
		t.Error("the honest reference was required to pass the mutated tests")
	}
}
