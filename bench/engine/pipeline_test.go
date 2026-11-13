package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, f := range []string{"run.json", "evidence.json", "gates.json", "grade.json", "plan.json", "metrics.json", "receipts.json"} {
		if _, err := os.Stat(filepath.Join(res.Dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
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
