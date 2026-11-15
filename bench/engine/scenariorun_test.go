package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeScenarioEnv is fakeEnv with every optional scenario interface. Its
// switches are read under the world's lock.
type fakeScenarioEnv struct {
	fakeEnv
	s *scenarioWorld
}

// scenarioWorld records what the scenario env saw and decides what it does.
type scenarioWorld struct {
	notReady     string          // service id that fails its ready check
	stateErr     string          // StateFile.Error of every export
	noState      bool            // ExportState returns no files
	answer       *schemas.Answer // what ExportAnswer returns
	sessionExits []int           // exit code per session (default 0)
	sessionTOs   []bool          // timed out per session
	begun        []int           // BeginSession indexes
	plans        []schemas.RunPlan
	instructions []string // rc.Task.Instruction seen by each Exec
	snapshots    int
	artifacts    []schemas.ArtifactFile // what ExportArtifacts returns; files without Error are written
	artifactErr  error                  // ExportArtifacts fails with it
}

func (e fakeScenarioEnv) StartServices(_ context.Context, rc *schemas.RunContext) ([]schemas.ServiceHandle, []schemas.ServiceResult, error) {
	if err := e.w.hit("start_services"); err != nil {
		return nil, nil, err
	}
	var hs []schemas.ServiceHandle
	var rs []schemas.ServiceResult
	for _, sv := range append(append([]schemas.ServiceSpec(nil), rc.Suite.Services...), rc.Task.Services...) {
		ok := sv.ID != e.s.notReady
		r := schemas.ServiceResult{ID: sv.ID, Role: sv.Role, Image: sv.Image, Ready: ok, Proxied: true}
		if !ok {
			r.Error = "ready check timed out after 60s"
		}
		rs = append(rs, r)
		hs = append(hs, schemas.ServiceHandle{Spec: sv, Container: "c-" + sv.ID, Host: sv.ID, URL: "http://toolproxy:9200/s/" + sv.ID})
	}
	if e.s.notReady != "" {
		return hs, rs, errors.New("a service did not come up")
	}
	return hs, rs, nil
}

func (e fakeScenarioEnv) ExportState(_ context.Context, rc *schemas.RunContext) ([]schemas.StateFile, error) {
	if err := e.w.hit("export_state"); err != nil {
		return nil, err
	}
	if e.s.noState {
		return nil, nil
	}
	p := filepath.Join(rc.Dir, "state", "db", "dump")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, []byte("rows\n"), 0o644); err != nil {
		return nil, err
	}
	return []schemas.StateFile{{Service: "db", ID: "dump", Path: "state/db/dump", SHA256: "x", Bytes: 5, Error: e.s.stateErr}}, nil
}

func (e fakeScenarioEnv) BeginSession(_ context.Context, _ *schemas.RunContext, index int) error {
	if err := e.w.hit("begin_session"); err != nil {
		return err
	}
	e.w.mu.Lock()
	e.s.begun = append(e.s.begun, index)
	e.w.mu.Unlock()
	return nil
}

func (e fakeScenarioEnv) Snapshot(context.Context, *schemas.RunContext) (string, error) {
	if err := e.w.hit("snapshot"); err != nil {
		return "", err
	}
	e.s.snapshots++
	return "sha256:snap", nil
}

func (e fakeScenarioEnv) ExportAnswer(context.Context, *schemas.RunContext) (*schemas.Answer, error) {
	if err := e.w.hit("export_answer"); err != nil {
		return nil, err
	}
	return e.s.answer, nil
}

func (e fakeScenarioEnv) ExportArtifacts(_ context.Context, rc *schemas.RunContext) ([]schemas.ArtifactFile, error) {
	if err := e.w.hit("export_artifacts"); err != nil {
		return nil, err
	}
	if e.s.artifactErr != nil {
		return nil, e.s.artifactErr
	}
	for _, f := range e.s.artifacts {
		if f.Error != "" {
			continue
		}
		p := filepath.Join(rc.Dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
			return nil, err
		}
	}
	return e.s.artifacts, nil
}

func (e fakeScenarioEnv) Exec(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	res, err := e.fakeEnv.Exec(ctx, rc, plan)
	if err != nil {
		return nil, err
	}
	e.w.mu.Lock()
	defer e.w.mu.Unlock()
	e.s.plans = append(e.s.plans, *plan)
	e.s.instructions = append(e.s.instructions, rc.Task.Instruction)
	if rc.Session != nil {
		i := rc.SessionIndex
		if i < len(e.s.sessionExits) {
			res.ExitCode = e.s.sessionExits[i]
		}
		if i < len(e.s.sessionTOs) {
			res.TimedOut = e.s.sessionTOs[i]
		}
		res.StdoutPath = filepath.Join(rc.Dir, "agent.stream.jsonl")
		if err := os.WriteFile(res.StdoutPath, []byte(`{"session":"`+rc.Session.ID+`"}`+"\n"), 0o644); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// scenarioBench is a bench whose environment is a fakeScenarioEnv and whose task
// t01 is edited by edit before planning.
func scenarioBench(t *testing.T, edit func(s *schemas.Suite, task *schemas.Task)) (*bench, *scenarioWorld, *schemas.Manifest, *schemas.Slot) {
	t.Helper()
	b := newBench(t)
	sw := &scenarioWorld{}
	b.reg.Environments = []schemas.Environment{fakeScenarioEnv{fakeEnv{part{"docker_workspace"}, b.w}, sw}}
	edit(b.suite, b.suite.TaskByID("t01"))
	m := b.plan(t, testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1}))
	for i := range m.Slots {
		if m.Slots[i].Arm == "A" {
			return b, sw, m, &m.Slots[i]
		}
	}
	t.Fatal("no slot of arm A")
	return nil, nil, nil, nil
}

func withStateService(s *schemas.Suite, task *schemas.Task) {
	s.Grader = "hidden_tests"
	task.Services = []schemas.ServiceSpec{{ID: "db", Role: schemas.ServiceRoleMCP, Image: "db:1", Port: 8000,
		Export: []schemas.StateExport{{ID: "dump", Argv: []string{"cat", "/data"}}}}}
	task.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
}

func withSessions(_ *schemas.Suite, task *schemas.Task) {
	task.Instruction = "instruction.md"
	task.Sessions = []schemas.SessionSpec{{ID: "s1", Instruction: "one.md"}, {ID: "s2", Instruction: "two.md", WallS: 42}}
	task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
}

func readRunFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

func TestRunSlotServicesAndState(t *testing.T) {
	b, sw, m, slot := scenarioBench(t, withStateService)
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Outcome != schemas.OutcomePass {
		t.Fatalf("outcome %s, failures %+v", res.Run.Outcome, res.Run.Failures)
	}
	seq := strings.Join(b.w.seq, " ")
	if !strings.Contains(seq, "create start_services mint") {
		t.Errorf("services did not start between create and mint: %s", seq)
	}
	if !strings.Contains(seq, "export export_state") {
		t.Errorf("state not exported after the diff: %s", seq)
	}
	if len(sw.plans) != 1 || sw.plans[0].Env["BENCH_MCP_SERVERS"] != `{"db":{"url":"http://toolproxy:9200/s/db/mcp"}}` {
		t.Fatalf("plan env %+v", sw.plans)
	}
	if _, ok := sw.plans[0].Env["BENCH_SESSION_ID"]; ok {
		t.Error("single-session run got session variables")
	}
	var services []schemas.ServiceResult
	if err := json.Unmarshal([]byte(readRunFile(t, res.Dir, FileServices)), &services); err != nil || len(services) != 1 || !services[0].Ready {
		t.Fatalf("services.json %v %+v", err, services)
	}
	var ev schemas.Evidence
	if err := json.Unmarshal([]byte(readRunFile(t, res.Dir, "evidence.json")), &ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.State) != 1 || len(ev.Services) != 1 {
		t.Fatalf("evidence state %+v services %+v", ev.State, ev.Services)
	}
	if ev.Grade == nil || ev.Grade.Mode != schemas.OutputState {
		t.Fatalf("grade mode %+v", ev.Grade)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, FileSessions)); !os.IsNotExist(err) {
		t.Error("sessions.json written for a single-session run")
	}
}

func TestRunSlotServiceNotReady(t *testing.T) {
	b, sw, m, slot := scenarioBench(t, withStateService)
	sw.notReady = "db"
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Outcome != schemas.OutcomeAbortedInfra {
		t.Fatalf("outcome %s", res.Run.Outcome)
	}
	if b.w.count("exec") != 0 || b.w.count("destroy") != 1 {
		t.Fatalf("exec %d destroy %d", b.w.count("exec"), b.w.count("destroy"))
	}
	found := false
	for _, f := range res.Run.Failures {
		if f.Stage == schemas.StagePrepare && strings.Contains(f.Error, "db (ready check timed out") {
			found = true
		}
	}
	if !found {
		t.Fatalf("failure does not name the service: %+v", res.Run.Failures)
	}
	if _, err := os.Stat(filepath.Join(res.Dir, FileServices)); err != nil {
		t.Errorf("services.json missing on an aborted run: %v", err)
	}
}

func TestRunSlotStateMissingIsInvalidEvaluator(t *testing.T) {
	for name, set := range map[string]func(*scenarioWorld){
		"export error": func(sw *scenarioWorld) { sw.stateErr = "export larger than 16 MiB" },
		"no export":    func(sw *scenarioWorld) { sw.noState = true },
	} {
		t.Run(name, func(t *testing.T) {
			b, sw, m, slot := scenarioBench(t, withStateService)
			set(sw)
			res, err := runOne(context.Background(), t, b, m, slot)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Outcome != schemas.OutcomeInvalidEvaluator {
				t.Fatalf("outcome %s", res.Run.Outcome)
			}
			if b.w.count("grade") != 0 {
				t.Error("grader ran without the state")
			}
			var g schemas.Grade
			if err := json.Unmarshal([]byte(readRunFile(t, res.Dir, "grade.json")), &g); err != nil || !strings.Contains(g.Reason, "state output") {
				t.Fatalf("grade %+v %v", g, err)
			}
		})
	}
}

func TestRunSlotSessions(t *testing.T) {
	b, sw, m, slot := scenarioBench(t, withSessions)
	sw.answer = &schemas.Answer{Text: "seven", Source: AnswerFromFile}
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Outcome != schemas.OutcomePass {
		t.Fatalf("outcome %s %+v", res.Run.Outcome, res.Run.Failures)
	}
	if got := sw.begun; len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("begun %v", got)
	}
	if strings.Join(sw.instructions, ",") != "one.md,two.md" {
		t.Fatalf("instructions %v", sw.instructions)
	}
	for i, p := range sw.plans {
		n := []string{"1", "2"}[i]
		if p.Env["BENCH_SESSION_INDEX"] != n || p.Env["BENCH_SESSION_COUNT"] != "2" || p.Env["BENCH_MEMORY_DIR"] != "/memory" ||
			p.Env["BENCH_SESSION_ID"] != "s"+n || p.Env["BENCH_ANSWER_PATH"] != "/workspace/answer.txt" {
			t.Errorf("session %s env %+v", n, p.Env)
		}
	}
	if sw.plans[1].WallS != 42 {
		t.Errorf("session wall budget not applied: %d", sw.plans[1].WallS)
	}
	var sessions []schemas.SessionResult
	if err := json.Unmarshal([]byte(readRunFile(t, res.Dir, FileSessions)), &sessions); err != nil || len(sessions) != 2 || sessions[1].Index != 2 ||
		sessions[1].Stdout != "agent.stream.2.jsonl" {
		t.Fatalf("sessions.json %v %+v", err, sessions)
	}
	if got := readRunFile(t, res.Dir, "agent.stream.2.jsonl"); !strings.Contains(got, `"s2"`) {
		t.Errorf("session 2 stream %q", got)
	}
	if got := readRunFile(t, res.Dir, FileStream); !strings.Contains(got, `"s1"`) || !strings.Contains(got, `"s2"`) {
		t.Errorf("concatenated stream %q", got)
	}
	if got := readRunFile(t, res.Dir, FileAnswer); got != "seven" {
		t.Errorf("answer.txt %q", got)
	}
	if b.suite.TaskByID("t01").Instruction != "instruction.md" {
		t.Error("the suite's task was changed")
	}
}

func TestRunSlotSessionEndsRun(t *testing.T) {
	tests := []struct {
		name  string
		exits []int
		tos   []bool
		want  schemas.Outcome
		execs int
	}{
		{"first session times out", nil, []bool{true}, schemas.OutcomeTimeout, 1},
		{"first session crashes", []int{2}, nil, schemas.OutcomeCrash, 1},
		{"last session exits non-zero is graded", []int{0, 1}, nil, schemas.OutcomePass, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, sw, m, slot := scenarioBench(t, withSessions)
			sw.sessionExits, sw.sessionTOs = tc.exits, tc.tos
			sw.answer = &schemas.Answer{Text: "x", Source: AnswerFromFile}
			res, err := runOne(context.Background(), t, b, m, slot)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Outcome != tc.want || b.w.count("exec") != tc.execs {
				t.Fatalf("outcome %s execs %d", res.Run.Outcome, b.w.count("exec"))
			}
		})
	}
}

func TestRunSlotAnswerFallback(t *testing.T) {
	tests := []struct {
		name       string
		answer     *schemas.Answer
		wantText   string
		wantSource string
	}{
		{"file", &schemas.Answer{Text: "42", Source: AnswerFromFile}, "42", AnswerFromFile},
		{"missing file uses final message", &schemas.Answer{Source: AnswerMissing}, "done", AnswerFromFinalMessage},
		{"nil answer uses final message", nil, "done", AnswerFromFinalMessage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, sw, m, slot := scenarioBench(t, func(s *schemas.Suite, task *schemas.Task) {
				task.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
			})
			sw.answer = tc.answer
			res, err := runOne(context.Background(), t, b, m, slot)
			if err != nil {
				t.Fatal(err)
			}
			if got := readRunFile(t, res.Dir, FileAnswer); got != tc.wantText {
				t.Errorf("answer.txt %q", got)
			}
			var ev schemas.Evidence
			if err := json.Unmarshal([]byte(readRunFile(t, res.Dir, "evidence.json")), &ev); err != nil || ev.Answer == nil ||
				ev.Answer.Source != tc.wantSource || ev.Answer.SHA256 == "" {
				t.Fatalf("evidence answer %+v %v", ev.Answer, err)
			}
			if !strings.Contains(strings.Join(b.w.seq, " "), "collect export_answer") && !strings.Contains(strings.Join(b.w.seq, " "), "export_answer") {
				t.Error("answer not exported")
			}
		})
	}
}

func TestRunSlotSnapshot(t *testing.T) {
	b, sw, m, slot := scenarioBench(t, func(s *schemas.Suite, _ *schemas.Task) { s.Verifier.Mode = schemas.VerifierSnapshot })
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	var ev schemas.Evidence
	if err := json.Unmarshal([]byte(readRunFile(t, res.Dir, "evidence.json")), &ev); err != nil || ev.Snapshot != "sha256:snap" || sw.snapshots != 1 {
		t.Fatalf("snapshot %q (%d) %v", ev.Snapshot, sw.snapshots, err)
	}
	if ev.Grade == nil || ev.Grade.Mode != schemas.VerifierSnapshot {
		t.Fatalf("grade mode %+v", ev.Grade)
	}
}

func TestRunSlotPlainTaskUnchanged(t *testing.T) {
	b, sw, m, slot := scenarioBench(t, func(*schemas.Suite, *schemas.Task) {})
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sw.plans) != 1 || len(sw.plans[0].Env) != 3 {
		t.Fatalf("plain plan env changed: %+v", sw.plans)
	}
	for _, op := range []string{"start_services", "export_state", "export_answer", "begin_session", "snapshot"} {
		if b.w.count(op) != 0 {
			t.Errorf("%s called for a plain task", op)
		}
	}
	for _, f := range []string{FileSessions, FileServices, FileAnswer} {
		if _, err := os.Stat(filepath.Join(res.Dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s written for a plain task", f)
		}
	}
	if m.TaskImages != nil || m.Scenario != nil {
		t.Errorf("plain manifest has scenario inputs: %v %v", m.TaskImages, m.Scenario)
	}
}

func TestSessionsEnded(t *testing.T) {
	at := time.Now()
	tests := []struct {
		name     string
		sessions []schemas.SessionResult
		count    int
		want     schemas.Outcome
	}{
		{"single session", []schemas.SessionResult{{Index: 1, TimedOut: true}}, 1, ""},
		{"all ran", []schemas.SessionResult{{Index: 1}, {Index: 2, ExitCode: 1}}, 2, ""},
		{"timeout", []schemas.SessionResult{{Index: 1, TimedOut: true, Started: at}}, 3, schemas.OutcomeTimeout},
		{"crash", []schemas.SessionResult{{Index: 1}, {Index: 2, ExitCode: 137}}, 3, schemas.OutcomeCrash},
		{"none", nil, 2, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionsEnded(tc.sessions, tc.count); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// withArtifacts makes t01 an artifact task with a required report and an
// optional plot.
func withArtifacts(_ *schemas.Suite, task *schemas.Task) {
	task.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{
		{ID: "report", Path: "out/*.csv", Required: true}, {ID: "plot", Path: "plot.svg"}}}
}

func TestRunSlotArtifacts(t *testing.T) {
	report := schemas.ArtifactFile{ID: "report", Name: "r.csv", Path: "artifacts/report/r.csv", Source: "out/r.csv",
		SHA256: "3a6eb0790f39ac87c94f3856b2dd2c5d110e6811602261a9a923d3bb23adc8b7", Bytes: 4}
	noReport := schemas.ArtifactFile{ID: "report", Source: "out/*.csv", Error: "missing: no file matches out/*.csv"}
	noPlot := schemas.ArtifactFile{ID: "plot", Source: "plot.svg", Error: "missing: no file matches plot.svg"}
	tests := []struct {
		name      string
		files     []schemas.ArtifactFile
		exportErr error
		outcome   schemas.Outcome
		graded    bool   // the grader ran
		reason    string // wanted in the grade reason when the grader did not run
	}{
		{name: "required artifact present, optional missing: the verifier grades", files: []schemas.ArtifactFile{report, noPlot},
			outcome: schemas.OutcomePass, graded: true},
		{name: "required artifact missing fails without the verifier", files: []schemas.ArtifactFile{noReport, noPlot},
			outcome: schemas.OutcomeFail, reason: "artifact output: required artifact missing: report (out/*.csv: missing: no file matches out/*.csv)"},
		// A failed export stage would make the run infra_error instead.
		{name: "export machinery error is invalid_evaluator", exportErr: errors.New("helper exit 1: no python3"),
			outcome: schemas.OutcomeInvalidEvaluator, reason: "artifact output: export failed: helper exit 1: no python3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, sw, m, slot := scenarioBench(t, withArtifacts)
			sw.artifacts, sw.artifactErr = tc.files, tc.exportErr
			res, err := runOne(context.Background(), t, b, m, slot)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Outcome != tc.outcome || b.w.count("export_artifacts") != 1 {
				t.Fatalf("outcome %s, exports %d", res.Run.Outcome, b.w.count("export_artifacts"))
			}
			if got := b.w.count("grade") == 1; got != tc.graded {
				t.Fatalf("grader ran: %v, want %v", got, tc.graded)
			}
			var ev schemas.Evidence
			if err := json.Unmarshal([]byte(readRunFile(t, res.Dir, "evidence.json")), &ev); err != nil || ev.Grade == nil {
				t.Fatalf("evidence %+v %v", ev, err)
			}
			if ev.Grade.Mode != schemas.OutputArtifact || (!tc.graded && ev.Grade.Reason != tc.reason) {
				t.Fatalf("grade %+v", ev.Grade)
			}
			if tc.exportErr == nil && len(ev.Artifacts) != len(tc.files) {
				t.Fatalf("evidence artifacts %+v", ev.Artifacts)
			}
		})
	}
}

func TestRunSlotPlainTaskExportsNoArtifacts(t *testing.T) {
	b, _, m, slot := scenarioBench(t, func(*schemas.Suite, *schemas.Task) {})
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	if b.w.count("export_artifacts") != 0 {
		t.Error("artifacts exported for a plain task")
	}
	var ev schemas.Evidence
	if err := json.Unmarshal([]byte(readRunFile(t, res.Dir, "evidence.json")), &ev); err != nil || ev.Artifacts != nil || ev.Grade == nil || ev.Grade.Mode != "" {
		t.Fatalf("plain evidence %+v %v", ev, err)
	}
}

func TestScenarioGradeProblemArtifacts(t *testing.T) {
	rc := &schemas.RunContext{Suite: &schemas.Suite{}, Task: &schemas.Task{Output: &schemas.OutputSpec{Kind: schemas.OutputArtifact}}}
	if got := scenarioGradeProblem(rc, &schemas.Evidence{}, ""); got != "" {
		t.Fatalf("clean export reported %q", got)
	}
	if got := scenarioGradeProblem(rc, &schemas.Evidence{}, "environment docker_workspace cannot export artifacts"); got != "artifact output: environment docker_workspace cannot export artifacts" {
		t.Fatalf("got %q", got)
	}
	plain := &schemas.RunContext{Suite: &schemas.Suite{}, Task: &schemas.Task{}}
	if got := scenarioGradeProblem(plain, &schemas.Evidence{}, "ignored"); got != "" {
		t.Fatalf("plain task reported %q", got)
	}
}

// plainArtifactEnv is a scenario env without ExportArtifacts.
type plainArtifactEnv struct{ fakeEnv }

func TestExportArtifactsWithoutSupport(t *testing.T) {
	b := newBench(t)
	st := &runState{rc: &schemas.RunContext{Run: &schemas.Run{RunID: "r_1"}, Suite: b.suite, Task: &schemas.Task{ID: "t"}},
		env: plainArtifactEnv{fakeEnv{part{"plain"}, b.w}}, ev: &schemas.Evidence{}}
	withArtifacts(nil, st.rc.Task)
	p := NewPipeline(b.reg, b.root, nil)
	if err := p.exportScenario(context.Background(), st); err != nil {
		t.Fatalf("export stage failed: %v", err)
	}
	if st.artifactErr != "environment plain cannot export artifacts" || st.ev.Artifacts != nil {
		t.Fatalf("artifactErr %q", st.artifactErr)
	}
	if got := scenarioGradeProblem(st.rc, st.ev, st.artifactErr); got != "artifact output: environment plain cannot export artifacts" {
		t.Fatalf("reason %q", got)
	}
}
