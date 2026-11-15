package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// regateGate is a veto gate with a settable version and result.
type regateGate struct {
	name, version string
	fire          bool
	err           error
}

func (g regateGate) GetName() string            { return g.name }
func (g regateGate) GetVersion() string         { return g.version }
func (g regateGate) Effect() schemas.GateEffect { return schemas.GateVeto }
func (g regateGate) Baseline() bool             { return true }
func (g regateGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if g.err != nil {
		return nil, g.err
	}
	if rc.Task == nil || ev.Diff == nil || ev.Gates != nil {
		return nil, errors.New("gate saw no task, no diff, or earlier gate results")
	}
	return &schemas.GateResult{Fired: g.fire, Detail: "recomputed"}, nil
}

// successMetric is 0 under a fired veto, else 1 when the grade passed.
type successMetric struct{}

func (successMetric) GetName() string    { return "verified_success" }
func (successMetric) GetVersion() string { return "1.0.0" }
func (successMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{Name: "verified_success", Kind: schemas.KindRate, MinEvidence: schemas.EvidenceOutcome}
}
func (successMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if vetoFired(ev.Gates) || ev.Grade == nil || !ev.Grade.Passed {
		return schemas.Observed(0, schemas.EvidenceOutcome), nil
	}
	return schemas.Observed(1, schemas.EvidenceOutcome), nil
}

// tokensMetric does not read gates.
type tokensMetric struct{}

func (tokensMetric) GetName() string    { return "tokens" }
func (tokensMetric) GetVersion() string { return "1.0.0" }
func (tokensMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{Name: "tokens", Kind: schemas.KindCount, MinEvidence: schemas.EvidenceGateway}
}
func (tokensMetric) Compute(context.Context, *schemas.RunContext, *schemas.Evidence) (schemas.Value, error) {
	return schemas.Observed(99, schemas.EvidenceGateway), nil
}

// regateRun writes a sealed-looking run folder: workspace.diff and
// evidence.json recording the diff's sha256 (or sum when not empty).
func regateRun(t *testing.T, outcome schemas.Outcome, passed bool, sealed []schemas.GateResult, success float64, sum string, protect map[string]string) *schemas.SealedRun {
	t.Helper()
	dir := t.TempDir()
	diff := filepath.Join(dir, "workspace.diff")
	if err := os.WriteFile(diff, []byte("diff --git a/tests/t.py b/tests/t.py\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if sum == "" {
		var err error
		if sum, err = utils.SHA256File(diff); err != nil {
			t.Fatal(err)
		}
	}
	ev := schemas.Evidence{
		Diff:    &schemas.Diff{Path: "/old/host/path/workspace.diff", SHA256: sum, Files: []schemas.DiffFile{{Path: "tests/t.py", Status: "modified"}}},
		Exec:    &schemas.ExecResult{ExitCode: 0},
		Protect: protect,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evidence.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return &schemas.SealedRun{
		Slot:  schemas.Slot{SlotID: "s_1", Arm: "A", Task: "t08", Condition: schemas.ConditionClean, Counted: true},
		Run:   schemas.Run{RunID: "r_1", Outcome: outcome, Y: scoreOf(outcome)},
		Dir:   dir,
		Grade: &schemas.Grade{Passed: passed},
		Gates: sealed,
		Metrics: map[string]schemas.Value{
			"verified_success": schemas.Observed(success, schemas.EvidenceOutcome),
			"tokens":           schemas.Observed(99, schemas.EvidenceGateway),
		},
	}
}

func TestRegate(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_1", Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "codex"}}}}
	suite := &schemas.Suite{ID: "coding-core", Tasks: []*schemas.Task{{ID: "t08"}}}
	vetoV1 := []schemas.GateResult{{Gate: "g1", Version: "1.0.0", Effect: schemas.GateVeto, Fired: true}, {Gate: "g2", Version: "1.0.0", Effect: schemas.GateVeto}}
	quietV1 := []schemas.GateResult{{Gate: "g1", Version: "1.0.0", Effect: schemas.GateVeto}, {Gate: "g2", Version: "1.0.0", Effect: schemas.GateVeto}}
	g2 := regateGate{name: "g2", version: "1.0.0"}
	cases := []struct {
		name       string
		g1         regateGate
		run        func(t *testing.T) *schemas.SealedRun
		failures   bool
		outcome    schemas.Outcome
		recomputed []string
		metrics    map[string]float64
		notes      string
		err        string
	}{
		{name: "new rule clears the veto", g1: regateGate{name: "g1", version: "1.1.0"},
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomeIntegrityViolation, true, vetoV1, 0, "", nil)
			},
			outcome: schemas.OutcomePass, recomputed: []string{"g1"}, metrics: map[string]float64{"verified_success": 1}},
		{name: "cleared veto on a failed grade", g1: regateGate{name: "g1", version: "1.1.0"},
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomeIntegrityViolation, false, vetoV1, 0, "", nil)
			},
			outcome: schemas.OutcomeFail, recomputed: []string{"g1"}},
		{name: "cleared veto on a run with a part failure", g1: regateGate{name: "g1", version: "1.1.0"}, failures: true,
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomeIntegrityViolation, true, vetoV1, 0, "", nil)
			},
			outcome: schemas.OutcomeInfraError, recomputed: []string{"g1"}, metrics: map[string]float64{"verified_success": 1}},
		{name: "same version is carried", g1: regateGate{name: "g1", version: "1.0.0"},
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomeIntegrityViolation, true, vetoV1, 0, "", nil)
			},
			outcome: schemas.OutcomeIntegrityViolation},
		{name: "recompute failure keeps the sealed veto", g1: regateGate{name: "g1", version: "1.1.0", err: errors.New("boom")},
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomeIntegrityViolation, true, vetoV1, 0, "", nil)
			},
			outcome: schemas.OutcomeIntegrityViolation, notes: "g1: kept sealed result 1.0.0: recompute failed: boom"},
		{name: "new rule adds a veto", g1: regateGate{name: "g1", version: "1.1.0", fire: true},
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomePass, true, quietV1, 1, "", nil)
			},
			outcome: schemas.OutcomeIntegrityViolation, recomputed: []string{"g1"}, metrics: map[string]float64{"verified_success": 0}},
		{name: "diff changed since export", g1: regateGate{name: "g1", version: "1.1.0"},
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomeIntegrityViolation, true, vetoV1, 0, strings.Repeat("0", 64), nil)
			},
			err: "workspace.diff sha256"},
		{name: "protected hashes need the unsealed handle", g1: regateGate{name: "g1", version: "1.1.0"},
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomeIntegrityViolation, true, vetoV1, 0, "", map[string]string{"tests/t.py": "aa"})
			},
			err: "environment handle"},
		{name: "gate-dependent metric that does not reproduce", g1: regateGate{name: "g1", version: "1.1.0"},
			run: func(t *testing.T) *schemas.SealedRun {
				return regateRun(t, schemas.OutcomeIntegrityViolation, true, vetoV1, 1, "", nil)
			},
			err: "does not reproduce"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := &schemas.Registry{Gates: []schemas.Gate{c.g1, g2}, Metrics: []schemas.Metric{successMetric{}, tokensMetric{}}}
			sr := c.run(t)
			if c.failures {
				sr.Run.Failures = []schemas.PartFailure{{Stage: schemas.StageGrade, Part: "grader/hidden_tests", Error: "x"}}
			}
			got, err := Regate(context.Background(), reg, m, suite, sr)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Regate: %v", err)
			}
			if got.Outcome != c.outcome || !reflect.DeepEqual(got.Y, scoreOf(c.outcome)) || !reflect.DeepEqual(got.Recomputed, c.recomputed) {
				t.Fatalf("outcome %s y %+v recomputed %v, want %s %v", got.Outcome, got.Y, got.Recomputed, c.outcome, c.recomputed)
			}
			if len(got.Metrics) != len(c.metrics) {
				t.Fatalf("metrics %+v, want %v", got.Metrics, c.metrics)
			}
			for name, want := range c.metrics {
				if v, ok := got.Metrics[name].Float(); !ok || v != want {
					t.Fatalf("metric %s = %+v, want %v", name, got.Metrics[name], want)
				}
			}
			if c.notes != "" && (len(got.Notes) != 1 || got.Notes[0] != c.notes) {
				t.Fatalf("notes %q, want %q", got.Notes, c.notes)
			}
			if len(got.Gates) != 2 || got.Gates[0].Gate != "g1" || got.Gates[1].Gate != "g2" {
				t.Fatalf("gates %+v", got.Gates)
			}
			if len(c.recomputed) > 0 && c.recomputed[0] == "g1" && (got.Gates[0].Version != c.g1.version || got.Gates[0].Effect != schemas.GateVeto) {
				t.Fatalf("recomputed g1 = %+v", got.Gates[0])
			}
		})
	}
}

func TestRegateVersions(t *testing.T) {
	reg := &schemas.Registry{Gates: []schemas.Gate{regateGate{name: "g1", version: "1.1.0"}, regateGate{name: "g2", version: "1.0.0"}}}
	got := RegateVersions(reg, &schemas.Manifest{})
	if !reflect.DeepEqual(got, map[string]string{"g1": "1.1.0", "g2": "1.0.0"}) {
		t.Fatalf("versions %v", got)
	}
}

func TestOutcomeFrom(t *testing.T) {
	veto := []schemas.GateResult{{Effect: schemas.GateVeto, Fired: true}}
	pass, fail := &schemas.Grade{Passed: true}, &schemas.Grade{}
	timedOut := &schemas.ExecResult{TimedOut: true}
	none := utils.UpstreamTally{}
	never := utils.UpstreamTally{Errors: 3, LastFailed: true}
	lost := utils.UpstreamTally{Errors: 2, Answered: 4, LastFailed: true}
	recovered := utils.UpstreamTally{Errors: 1, Answered: 4}
	incident := utils.TallyUpstream(loadDNSIncident(t))
	cases := []struct {
		name            string
		aborted, broken bool
		gates           []schemas.GateResult
		grade           *schemas.Grade
		exec            *schemas.ExecResult
		up              utils.UpstreamTally
		want            schemas.Outcome
	}{
		{"aborted wins", true, true, veto, pass, nil, none, schemas.OutcomeAbortedInfra},
		{"veto beats broken", false, true, veto, pass, nil, none, schemas.OutcomeIntegrityViolation},
		{"broken", false, true, nil, pass, nil, none, schemas.OutcomeInfraError},
		{"no grade", false, false, nil, nil, nil, none, schemas.OutcomeInfraError},
		{"invalid evaluator", false, false, nil, &schemas.Grade{InvalidEvaluator: true}, nil, none, schemas.OutcomeInvalidEvaluator},
		{"pass", false, false, nil, pass, nil, none, schemas.OutcomePass},
		{"timeout", false, false, nil, fail, timedOut, none, schemas.OutcomeTimeout},
		{"fail", false, false, nil, fail, nil, none, schemas.OutcomeFail},
		{"never answered is aborted", false, false, nil, fail, nil, never, schemas.OutcomeAbortedInfra},
		{"never answered beats broken", false, true, nil, nil, nil, never, schemas.OutcomeAbortedInfra},
		{"veto beats never answered", false, false, veto, fail, nil, never, schemas.OutcomeIntegrityViolation},
		{"lost after answers is infra", false, false, nil, fail, nil, lost, schemas.OutcomeInfraError},
		{"lost after answers beats timeout", false, false, nil, fail, timedOut, lost, schemas.OutcomeInfraError},
		{"lost after answers but passed", false, false, nil, pass, nil, lost, schemas.OutcomePass},
		{"recovered errors change nothing", false, false, nil, fail, nil, recovered, schemas.OutcomeFail},
		{"DNS incident sealed as fail is aborted", false, false, nil, fail, nil, incident, schemas.OutcomeAbortedInfra},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := outcomeFrom(c.aborted, c.broken, "", c.gates, c.grade, c.exec, c.up); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
	if incident.Errors != 30 || incident.Answered != 0 || !incident.Inferred {
		t.Fatalf("incident tally %+v", incident)
	}
}

func TestRegateOutcomeUpstream(t *testing.T) {
	veto := []schemas.GateResult{{Effect: schemas.GateVeto, Fired: true}}
	fail := &schemas.Grade{}
	never := utils.UpstreamTally{Errors: 3, LastFailed: true}
	lost := utils.UpstreamTally{Errors: 2, Answered: 4, LastFailed: true}
	recovered := utils.UpstreamTally{Errors: 1, Answered: 4}
	cases := []struct {
		name   string
		sealed schemas.Outcome
		gates  []schemas.GateResult
		up     utils.UpstreamTally
		want   schemas.Outcome
	}{
		{"fail that never reached the provider", schemas.OutcomeFail, nil, never, schemas.OutcomeInfraError},
		{"timeout that ended on the provider", schemas.OutcomeTimeout, nil, lost, schemas.OutcomeInfraError},
		{"recovered fail stays", schemas.OutcomeFail, nil, recovered, schemas.OutcomeFail},
		{"pass stays", schemas.OutcomePass, nil, lost, schemas.OutcomePass},
		{"crash stays", schemas.OutcomeCrash, nil, never, schemas.OutcomeCrash},
		{"aborted stays", schemas.OutcomeAbortedInfra, veto, never, schemas.OutcomeAbortedInfra},
		{"new veto wins", schemas.OutcomeFail, veto, never, schemas.OutcomeIntegrityViolation},
		{"cleared veto, never answered", schemas.OutcomeIntegrityViolation, nil, never, schemas.OutcomeInfraError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run := &schemas.Run{Outcome: c.sealed}
			if got := regateOutcome(run, "", c.gates, fail, nil, c.up); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

// TestRegateDNSIncident regates a run sealed as fail by an engine older than
// the upstream rule: its fault proxy could not resolve the provider, and no
// model call was ever answered. The regate makes it infra_error.
func TestRegateDNSIncident(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_1", Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "codex"}}}}
	suite := &schemas.Suite{ID: "coding-core", Tasks: []*schemas.Task{{ID: "t08"}}}
	sealed := []schemas.GateResult{{Gate: "g1", Version: "1.0.0", Effect: schemas.GateVeto}}
	sr := regateRun(t, schemas.OutcomeFail, false, sealed, 0, "", nil)
	var ev schemas.Evidence
	raw, err := os.ReadFile(filepath.Join(sr.Dir, "evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	ev.Ledger, ev.Requests = loadDNSIncident(t)
	if raw, err = json.Marshal(ev); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sr.Dir, "evidence.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	reg := &schemas.Registry{Gates: []schemas.Gate{regateGate{name: "g1", version: "1.0.0"}}}
	got, err := Regate(context.Background(), reg, m, suite, sr)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != schemas.OutcomeInfraError || got.Y.State != schemas.ValueUnknown {
		t.Fatalf("outcome %s y %+v", got.Outcome, got.Y)
	}
}

// TestRegateRunWithoutGateStage regates runs that were sealed before their
// gate stage ran: no gate is checked on their partial evidence, nothing fires
// and the outcome stays.
func TestRegateRunWithoutGateStage(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_1", Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "codex"}}}}
	suite := &schemas.Suite{ID: "coding-core", Tasks: []*schemas.Task{{ID: "t08"}}}
	reg := &schemas.Registry{Gates: []schemas.Gate{regateGate{name: "egress", version: "1.0.0", fire: true}}}
	for _, o := range []schemas.Outcome{schemas.OutcomeInfraError, schemas.OutcomeAbortedInfra} {
		t.Run(string(o), func(t *testing.T) {
			sr := regateRun(t, o, false, nil, 0, "", nil)
			sr.Run.Failures = []schemas.PartFailure{{Stage: schemas.StageRun, Part: "engine", Error: "agent run interrupted: context canceled"}}
			got, err := Regate(context.Background(), reg, m, suite, sr)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Gates) != 0 || len(got.Recomputed) != 0 || got.Outcome != o ||
				len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "gate stage never ran") {
				t.Fatalf("regated %+v", got)
			}
		})
	}
}

func TestSealedScenario(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(FileAnswer, "42")
	write("state/db/dump", "rows\n")
	write(fileToolLedger, `{"seq":1,"server":"db","tool":"query","protocol":"mcp","args_sha256":"a","result_sha256":"b","is_error":false,"started":"2026-11-15T00:00:00Z","latency_ms":3}`+"\n")
	write(FileSessions, `[{"id":"s1","index":1,"exit_code":0,"timed_out":false,"started":"2026-11-15T00:00:00Z","ended":"2026-11-15T00:00:01Z","stdout_path":"agent.stream.1.jsonl"}]`)
	ev := &schemas.Evidence{
		Answer: &schemas.Answer{Source: AnswerFromFile, SHA256: utils.SHA256Bytes([]byte("42"))},
		State:  []schemas.StateFile{{Service: "db", ID: "dump", Path: "state/db/dump", SHA256: utils.SHA256Bytes([]byte("rows\n"))}},
	}
	if err := sealedScenario(dir, ev); err != nil {
		t.Fatal(err)
	}
	if ev.Answer.Text != "42" || len(ev.ToolCalls) != 1 || ev.ToolCalls[0].Tool != "query" || len(ev.Sessions) != 1 {
		t.Fatalf("evidence %+v", ev)
	}

	bad := &schemas.Evidence{Answer: &schemas.Answer{SHA256: utils.SHA256Bytes([]byte("41"))}}
	if err := sealedScenario(dir, bad); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("changed answer: got %v", err)
	}
	badState := &schemas.Evidence{State: []schemas.StateFile{{Service: "db", ID: "dump", Path: "state/db/dump", SHA256: "0"}}}
	if err := sealedScenario(dir, badState); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("changed state: got %v", err)
	}
	plain := &schemas.Evidence{}
	if err := sealedScenario(t.TempDir(), plain); err != nil || plain.ToolCalls != nil || plain.Sessions != nil {
		t.Fatalf("plain run changed: %+v %v", plain, err)
	}

	// An empty ledger round-trips through evidence.json (omitempty) as no
	// field; the sealed tools.jsonl says the run made zero calls.
	noCalls := t.TempDir()
	if err := os.WriteFile(filepath.Join(noCalls, fileToolLedger), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(schemas.Evidence{ToolCalls: []schemas.ToolCall{}})
	if err != nil {
		t.Fatal(err)
	}
	var reloaded schemas.Evidence
	if err := json.Unmarshal(b, &reloaded); err != nil {
		t.Fatal(err)
	}
	if err := sealedScenario(noCalls, &reloaded); err != nil || reloaded.ToolCalls == nil || len(reloaded.ToolCalls) != 0 {
		t.Fatalf("empty ledger reloaded as %#v (%v), want an empty list", reloaded.ToolCalls, err)
	}
}
