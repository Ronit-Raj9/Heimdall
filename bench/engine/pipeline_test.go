package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
)

// pipelineFixture plans a two-condition experiment and returns the bench, the
// manifest and the first slot of arm B under the given condition.
func pipelineFixture(t *testing.T, cond string) (*bench, *schemas.Manifest, *schemas.Slot) {
	t.Helper()
	b := newBench(t)
	m := b.plan(t, testExperiment(
		schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
		schemas.ConditionSpec{ID: "stale-notes", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
		schemas.ConditionSpec{ID: "model-outage", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1},
	))
	for i := range m.Slots {
		if s := &m.Slots[i]; s.Arm == "B" && s.Condition == cond {
			return b, m, s
		}
	}
	t.Fatalf("no slot for B/%s", cond)
	return nil, nil, nil
}

type traceOutputEnvironment struct{ schemas.Environment }

func (e traceOutputEnvironment) Exec(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	res, err := e.Environment.Exec(ctx, rc, plan)
	if err != nil {
		return res, err
	}
	raw := `{"schema":"bifrost.agent.event","version":"1","type":"model_response","summary":"answer"}` + "\n{bad-json\n"
	if err := os.WriteFile(res.StdoutPath, []byte(raw), 0o644); err != nil {
		return res, err
	}
	return res, nil
}

func TestRunSlotPersistsTraceDiagnostics(t *testing.T) {
	b, m, slot := pipelineFixture(t, "clean")
	b.reg.Environments[0] = traceOutputEnvironment{b.reg.Environments[0]}
	for _, source := range evidence.Builtin(evidence.Deps{Parsers: adapters.BuiltinParsers()}) {
		if source.GetName() == evidence.AgentStream {
			b.reg.Evidence = append(b.reg.Evidence, source)
		}
	}
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	want := res.Run.Agent.TraceDiagnostics
	if want == nil || want.Source != schemas.TraceSourceProtocol || want.Quality != schemas.TraceQualityPartial || want.Malformed != 1 || res.Run.Outcome != schemas.OutcomePass {
		t.Fatalf("run=%+v diagnostics=%+v", res.Run, want)
	}
	for _, name := range []string{"run.json", "evidence.json", "canonical.json"} {
		data, err := os.ReadFile(filepath.Join(res.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var trace *schemas.ParseDiagnostics
		if name == "run.json" {
			var run schemas.Run
			if err := json.Unmarshal(data, &run); err != nil {
				t.Fatal(err)
			}
			trace = run.Agent.TraceDiagnostics
		} else {
			var envelope struct {
				Trace *schemas.ParseDiagnostics `json:"agent_trace"`
			}
			if err := json.Unmarshal(data, &envelope); err != nil {
				t.Fatal(err)
			}
			trace = envelope.Trace
		}
		if trace == nil || trace.Source != want.Source || trace.Quality != want.Quality || trace.Malformed != want.Malformed {
			t.Fatalf("%s: trace=%+v want=%+v", name, trace, want)
		}
	}
}

func runOne(ctx context.Context, t *testing.T, b *bench, m *schemas.Manifest, slot *schemas.Slot) (*SlotResult, error) {
	t.Helper()
	return NewPipeline(b.reg, b.root, nil).RunSlot(ctx, SlotInput{Manifest: m, Suite: b.suite, Slot: slot, Attempt: 1})
}

func TestRunSlotHappyPath(t *testing.T) {
	b, m, slot := pipelineFixture(t, "stale-notes")
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	run := res.Run
	if run.Outcome != schemas.OutcomePass || run.Stage != schemas.StageSeal || res.Seal == nil {
		t.Fatalf("outcome %s stage %s seal %v", run.Outcome, run.Stage, res.Seal)
	}
	if y, ok := run.Y.Float(); !ok || y != 1 {
		t.Fatalf("y = %+v", run.Y)
	}
	if run.Timing.AgentStarted == nil || run.Timing.AgentExited == nil || run.Timing.Sealed == nil {
		t.Fatalf("timing %+v", run.Timing)
	}
	if run.Gateway.VirtualKeyID != "vk-"+run.RunID || run.Gateway.SessionID != run.RunID || run.Claim == nil {
		t.Fatalf("gateway %+v claim %v", run.Gateway, run.Claim)
	}
	rec := b.store.record(run.RunID)
	var want []string
	for _, s := range schemas.Stages {
		want = append(want, string(s)+":started", string(s)+":completed")
	}
	if got := transitionsOf(rec); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("transitions\n got %v\nwant %v", got, want)
	}
	for _, f := range []string{"run.json", "evidence.json", "gates.json", "grade.json", "plan.json", "metrics.json", "receipts.json", "canonical.json"} {
		if _, err := os.Stat(filepath.Join(res.Dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	canonical, err := os.ReadFile(filepath.Join(res.Dir, "canonical.json"))
	if err != nil || !strings.Contains(string(canonical), `"canonical_version"`) || run.CanonicalHash == "" {
		t.Fatalf("canonical artifact missing or untracked: err=%v hash=%q", err, run.CanonicalHash)
	}
	plan, _ := os.ReadFile(filepath.Join(res.Dir, "plan.json"))
	if strings.Contains(string(plan), "sk-") || !strings.Contains(string(plan), "visible") {
		t.Fatalf("plan.json not redacted: %s", plan)
	}
	evidence, _ := os.ReadFile(filepath.Join(res.Dir, "evidence.json"))
	if strings.Contains(string(evidence), `"big"`) {
		t.Fatal("evidence.json kept raw gateway rows")
	}
	var receipts []schemas.Receipt
	raw, _ := os.ReadFile(filepath.Join(res.Dir, "receipts.json"))
	if err := json.Unmarshal(raw, &receipts); err != nil || len(receipts) != 1 || len(receipts[0].Steps) != len(schemas.AllReceiptSteps) {
		t.Fatalf("receipts %s (%v)", raw, err)
	}
	steps := receipts[0].Steps
	if steps[schemas.StepArmed].State != schemas.StepYes || steps[schemas.StepEffect].State != schemas.StepYes ||
		steps[schemas.StepAssigned].State != schemas.StepYes || steps[schemas.StepVisible].State != schemas.StepUnknown ||
		steps[schemas.StepFired].State != schemas.StepNotApplicable {
		t.Fatalf("receipt steps %+v", steps)
	}
	if b.w.count("destroy") != 1 || b.w.count("revoke") != 1 {
		t.Fatal("cleanup did not run exactly once")
	}
	if uf := b.w.agents[0].Notes.UserFile; !filepath.IsAbs(uf) {
		t.Fatalf("parts got a relative user file %q", uf)
	}
	if m.Arms["B"].Config.Notes.UserFile != "experiments/fixtures/codewins.md" {
		t.Fatal("pipeline mutated the manifest's arm config")
	}
}

func TestRunSlotOutcomes(t *testing.T) {
	tests := []struct {
		name  string
		setup func(w *world)
		want  schemas.Outcome
		y     float64
		known bool
	}{
		{"pass", func(*world) {}, schemas.OutcomePass, 1, true},
		{"fail", func(w *world) { w.gradePass = false }, schemas.OutcomeFail, 0, true},
		{"timeout", func(w *world) { w.gradePass, w.timedOut = false, true }, schemas.OutcomeTimeout, 0, true},
		{"timed out but passed", func(w *world) { w.timedOut = true }, schemas.OutcomePass, 1, true},
		{"invalid evaluator", func(w *world) { w.invalid = true }, schemas.OutcomeInvalidEvaluator, 0, false},
		{"veto gate", func(w *world) { w.veto = true }, schemas.OutcomeIntegrityViolation, 0, true},
		{"veto wins over a later failure", func(w *world) { w.veto, w.fail["receipts"] = true, true }, schemas.OutcomeIntegrityViolation, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, m, slot := pipelineFixture(t, "stale-notes")
			tt.setup(b.w)
			res, err := runOne(context.Background(), t, b, m, slot)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Outcome != tt.want {
				t.Fatalf("outcome %s, want %s (failures %+v)", res.Run.Outcome, tt.want, res.Run.Failures)
			}
			y, ok := res.Run.Y.Float()
			if ok != tt.known || (ok && y != tt.y) {
				t.Fatalf("y = %+v", res.Run.Y)
			}
		})
	}
}

// TestRunSlotCrashAtEveryStage injects a failure into the part behind each
// stage. Cleanup must always run, the transition log must show the failed
// stage, and the outcome is aborted_infra before the agent starts and
// infra_error after.
func TestRunSlotCrashAtEveryStage(t *testing.T) {
	tests := []struct {
		op      string
		stage   schemas.Stage
		want    schemas.Outcome
		sealErr bool
	}{
		{"create", schemas.StagePrepare, schemas.OutcomeAbortedInfra, false},
		{"mint", schemas.StagePrepare, schemas.OutcomeAbortedInfra, false},
		{"arm", schemas.StageArm, schemas.OutcomeAbortedInfra, false},
		{"plan", schemas.StageRun, schemas.OutcomeAbortedInfra, false},
		{"exec", schemas.StageRun, schemas.OutcomeInfraError, false},
		{"disarm", schemas.StageDisarm, schemas.OutcomeInfraError, false},
		{"export", schemas.StageExport, schemas.OutcomeInfraError, false},
		{"collect", schemas.StageCollect, schemas.OutcomeInfraError, false},
		{"grade", schemas.StageGrade, schemas.OutcomeInfraError, false},
		{"gate", schemas.StageGate, schemas.OutcomeInfraError, false},
		{"measure", schemas.StageMeasure, schemas.OutcomeInfraError, false},
		{"receipts", schemas.StageReceipt, schemas.OutcomeInfraError, false},
		{"fault_set", schemas.StagePrepare, schemas.OutcomeAbortedInfra, false},
		{"destroy", schemas.StageCleanup, schemas.OutcomeInfraError, false},
		{"fault_clear", schemas.StageCleanup, schemas.OutcomeInfraError, false},
		{"revoke", schemas.StageCleanup, schemas.OutcomeInfraError, false},
		{"seal", schemas.StageSeal, schemas.OutcomeInfraError, true},
	}
	for _, tt := range tests {
		t.Run(tt.op, func(t *testing.T) {
			b, m, slot := pipelineFixture(t, "stale-notes")
			b.w.fail[tt.op] = true
			res, err := runOne(context.Background(), t, b, m, slot)
			if (err != nil) != tt.sealErr {
				t.Fatalf("err = %v, want seal error %v", err, tt.sealErr)
			}
			if res == nil {
				t.Fatal("no result")
			}
			if res.Run.Outcome != tt.want {
				t.Fatalf("outcome %s, want %s", res.Run.Outcome, tt.want)
			}
			if b.w.count("destroy") != 1 {
				t.Fatalf("destroy ran %d times", b.w.count("destroy"))
			}
			minted := tt.op != "create" && tt.op != "mint"
			if minted != (b.w.count("fault_clear") == 1) {
				t.Fatalf("fault plan cleared %d times, key minted %v", b.w.count("fault_clear"), minted)
			}
			if minted != (b.w.count("revoke") == 1) {
				t.Fatalf("revoke ran %d times, key minted %v", b.w.count("revoke"), minted)
			}
			rec := b.store.record(res.Run.RunID)
			if !hasTransition(rec, tt.stage, schemas.TransitionFailed) || !hasTransition(rec, schemas.StageCleanup, schemas.TransitionStarted) {
				t.Fatalf("transitions %v", transitionsOf(rec))
			}
			if rec.sealed == tt.sealErr {
				t.Fatalf("sealed = %v", rec.sealed)
			}
			if len(res.Run.Failures) == 0 || res.Run.Failures[0].Stage != tt.stage {
				t.Fatalf("failures %+v", res.Run.Failures)
			}
			if _, ok := res.Run.Y.Float(); ok {
				t.Fatalf("y observed for %s", res.Run.Outcome)
			}
			if tt.want == schemas.OutcomeAbortedInfra && b.w.count("exec") != 0 {
				t.Fatal("agent ran after a pre-agent failure")
			}
			if tt.want == schemas.OutcomeInfraError && !tt.sealErr && tt.stage != schemas.StageCleanup &&
				!hasTransition(rec, schemas.StageReceipt, schemas.TransitionStarted) {
				t.Fatal("later stages did not run after a post-agent failure")
			}
		})
	}
}

func TestRunSlotFailurePartNames(t *testing.T) {
	b, m, slot := pipelineFixture(t, "clean")
	b.w.fail["grade"] = true
	res, err := runOne(context.Background(), t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	f := res.Run.Failures[0]
	if f.Part != "grader/hidden_tests" || !strings.Contains(f.Error, "injected grade failure") {
		t.Fatalf("failure %+v", f)
	}
	if b.w.count("arm") != 0 || b.w.count("disarm") != 0 || b.w.count("receipts") != 0 {
		t.Fatal("a clean run called the probe layer")
	}
}

func TestRunSlotCancelledContext(t *testing.T) {
	b, m, slot := pipelineFixture(t, "clean")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := runOne(ctx, t, b, m, slot)
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Outcome != schemas.OutcomeAbortedInfra || res.Seal == nil {
		t.Fatalf("outcome %s seal %v", res.Run.Outcome, res.Seal)
	}
	if b.w.count("create") != 0 {
		t.Fatal("prepare ran on a cancelled context")
	}
	rec := b.store.record(res.Run.RunID)
	if !hasTransition(rec, schemas.StageCleanup, schemas.TransitionCompleted) || !hasTransition(rec, schemas.StageSeal, schemas.TransitionCompleted) {
		t.Fatalf("cleanup and seal need a fresh context: %v", transitionsOf(rec))
	}
}

// TestRunSlotFaultPlan checks that every run's key slot is bound to its run
// id at the fault proxy and cleared before the key is revoked. A gateway
// probe binds its own plan when it arms, so the engine only clears it.
func TestRunSlotFaultPlan(t *testing.T) {
	tests := []struct {
		cond string
		want []string
	}{
		{"clean", []string{"mint", "fault_set", "fault_clear", "revoke"}},
		{"stale-notes", []string{"mint", "fault_set", "fault_clear", "revoke"}},
		{"model-outage", []string{"mint", "fault_clear", "revoke"}},
	}
	for _, tt := range tests {
		t.Run(tt.cond, func(t *testing.T) {
			b, m, slot := pipelineFixture(t, tt.cond)
			res, err := runOne(context.Background(), t, b, m, slot)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, op := range b.w.seq {
				if op == "mint" || op == "revoke" || strings.HasPrefix(op, "fault_") {
					got = append(got, op)
				}
			}
			if strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Fatalf("calls %v, want %v", got, tt.want)
			}
			if tt.cond != "model-outage" {
				if p := b.w.plans[0]; p.Mode != "none" || p.RunID != res.Run.RunID {
					t.Fatalf("bound plan %+v", p)
				}
			}
		})
	}
}

func TestRedactPlan(t *testing.T) {
	tests := []struct {
		key    string
		redact bool
	}{
		{"ANTHROPIC_AUTH_TOKEN", true},
		{"OPENAI_API_KEY", true},
		{"my_secret", true},
		{"X_AUTHORIZATION", true},
		{"ANTHROPIC_BASE_URL", false},
		{"CLAUDE_CONFIG_DIR", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			p := &schemas.RunPlan{Env: map[string]string{tt.key: "v"}}
			got := redactPlan(p).Env[tt.key]
			if (got == "[redacted]") != tt.redact || p.Env[tt.key] != "v" {
				t.Fatalf("redacted %q, original %q", got, p.Env[tt.key])
			}
		})
	}
	if redactPlan(nil) != nil {
		t.Fatal("nil plan")
	}
}

func TestOpenStage(t *testing.T) {
	tr := func(s schemas.Stage, st schemas.TransitionStatus) schemas.Transition {
		return schemas.Transition{Stage: s, Status: st}
	}
	tests := []struct {
		name string
		ts   []schemas.Transition
		want schemas.Stage
	}{
		{"none", nil, ""},
		{"open run", []schemas.Transition{tr("prepare", "started"), tr("prepare", "completed"), tr("run", "started")}, "run"},
		{"all closed", []schemas.Transition{tr("prepare", "started"), tr("prepare", "failed")}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := openStage(tt.ts); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// upstreamDNSErr is the transport error the fault proxy logged when Docker
// DNS broke on a live experiment.
const upstreamDNSErr = "Post: dial tcp: lookup api.openai.com on 127.0.0.11:53: i/o timeout"

// ledgerLine is one model call in a fault proxy ledger: answered with 200,
// or unreachable with the proxy's 502 and upstream_error set.
func ledgerLine(call int, reached bool) schemas.LedgerEntry {
	e := schemas.LedgerEntry{RunID: "r", Call: call, Kind: "message", Action: "pass", Upstream: "real", Status: 200, ResponseID: "resp"}
	if !reached {
		e.Status, e.ResponseID, e.UpstreamError = 502, "", upstreamDNSErr
	}
	return e
}

// loadDNSIncident reads testdata/upstreamdns: the ledger and gateway rows of a
// real run whose fault proxy could not resolve api.openai.com. The proxy
// predates upstream_error, so the rows carry only its 502 and message.
func loadDNSIncident(t *testing.T) ([]schemas.LedgerEntry, *schemas.RequestLog) {
	t.Helper()
	var ledger []schemas.LedgerEntry
	var rl schemas.RequestLog
	for name, add := range map[string]func([]byte) error{
		"proxy.ledger.jsonl": func(b []byte) error {
			var e schemas.LedgerEntry
			err := json.Unmarshal(b, &e)
			ledger = append(ledger, e)
			return err
		},
		"gateway.requests.jsonl": func(b []byte) error {
			var r schemas.GatewayRequest
			err := json.Unmarshal(b, &r)
			rl.Requests = append(rl.Requests, r)
			return err
		},
	} {
		raw, err := os.ReadFile(filepath.Join("testdata", "upstreamdns", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if err := add([]byte(line)); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
	return ledger, &rl
}

// TestRunSlotUpstreamUnreachable checks the upstream rule end to end: model
// calls the fault proxy could not deliver, with no fault planned, never count
// against the agent.
func TestRunSlotUpstreamUnreachable(t *testing.T) {
	unreachable := []schemas.LedgerEntry{ledgerLine(1, false), ledgerLine(2, false), ledgerLine(3, false)}
	lost := []schemas.LedgerEntry{ledgerLine(1, true), ledgerLine(2, true), ledgerLine(3, false)}
	recovered := []schemas.LedgerEntry{ledgerLine(1, false), ledgerLine(2, true)}
	tests := []struct {
		name     string
		ledger   []schemas.LedgerEntry
		setup    func(w *world)
		want     schemas.Outcome
		recorded bool // an upstream part failure is on the run
	}{
		{"never answered", unreachable, func(w *world) { w.gradePass = false }, schemas.OutcomeAbortedInfra, true},
		{"never answered even if graded pass", unreachable, func(*world) {}, schemas.OutcomeAbortedInfra, true},
		{"lost after answers", lost, func(w *world) { w.gradePass = false }, schemas.OutcomeInfraError, true},
		{"lost after answers and timed out", lost, func(w *world) { w.gradePass, w.timedOut = false, true }, schemas.OutcomeInfraError, true},
		{"lost after answers but passed", lost, func(*world) {}, schemas.OutcomePass, false},
		{"recovered and failed", recovered, func(w *world) { w.gradePass = false }, schemas.OutcomeFail, false},
		{"veto wins", unreachable, func(w *world) { w.veto = true }, schemas.OutcomeIntegrityViolation, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, m, slot := pipelineFixture(t, "stale-notes")
			tt.setup(b.w)
			b.w.ledgers = [][]schemas.LedgerEntry{tt.ledger}
			res, err := runOne(context.Background(), t, b, m, slot)
			if err != nil {
				t.Fatal(err)
			}
			if res.Run.Outcome != tt.want {
				t.Fatalf("outcome %s, want %s (failures %+v)", res.Run.Outcome, tt.want, res.Run.Failures)
			}
			var pf *schemas.PartFailure
			for i, f := range res.Run.Failures {
				if f.Part == upstreamPart {
					pf = &res.Run.Failures[i]
				}
			}
			if (pf != nil) != tt.recorded {
				t.Fatalf("upstream failure recorded %v, want %v: %+v", pf != nil, tt.recorded, res.Run.Failures)
			}
			if pf != nil && (pf.Stage != schemas.StageRun || !strings.Contains(pf.Error, upstreamDNSErr)) {
				t.Fatalf("upstream failure %+v", pf)
			}
		})
	}
}

// TestSmokeRunIsScriptedEndToEnd is 3-C-01 through RunSlot on the default
// pipeline (what the scheduler, canaries, compare and remote workers use): a
// smoke run's slot binds the scripted plan, so no call of the run can reach
// a keyed upstream. (A gateway probe's plan carries Scripted instead:
// TestGatewayArmScriptedOnNoPaidRun in probes.)
func TestSmokeRunIsScriptedEndToEnd(t *testing.T) {
	b, m, slot := pipelineFixture(t, "clean")
	m.Experiment.Smoke = true
	res, err := NewPipeline(b.reg, b.root, nil).RunSlot(context.Background(), SlotInput{Manifest: m, Suite: b.suite, Slot: slot, Attempt: 1})
	if err != nil || res == nil {
		t.Fatalf("RunSlot: %v", err)
	}
	if len(b.w.plans) != 1 || b.w.plans[0].Mode != faultModeScripted || b.w.plans[0].RunID != res.Run.RunID {
		t.Fatalf("plans %+v, want one scripted plan for %s", b.w.plans, res.Run.RunID)
	}
}
