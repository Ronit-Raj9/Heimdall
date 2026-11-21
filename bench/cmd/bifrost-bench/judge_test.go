package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reporters"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/verdict"
)

// W8 judge calibration: the whole loop at $0 through the real store,
// analysis and record chain, with a fake gateway and a fake judge.

// calGateway mints and revokes keys in memory.
type calGateway struct {
	mu      sync.Mutex
	minted  int
	revoked int
}

func (g *calGateway) GetName() string    { return "fake" }
func (g *calGateway) GetVersion() string { return "0" }
func (g *calGateway) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	return &schemas.GatewayManifest{}, nil
}
func (g *calGateway) MintKey(_ context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.minted++
	return &schemas.RunKey{ID: "vk-" + rc.Run.RunID, Value: "sk-judge"}, nil
}
func (g *calGateway) RevokeKey(context.Context, *schemas.RunKey) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.revoked++
	return nil
}
func (g *calGateway) RevokeStale(context.Context, map[string]bool) (int, error) { return 0, nil }
func (g *calGateway) FetchRequests(context.Context, *schemas.RunKey, schemas.FetchExpectation) (*schemas.RequestLog, error) {
	return nil, fmt.Errorf("not used")
}

// fakeChat says correct when the graded response mentions Max.
func fakeChat() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.Unmarshal(raw, &body)
		p := body.Messages[len(body.Messages)-1].Content
		resp := p[strings.LastIndex(p, "<<<"):]
		verdict := "incorrect"
		if strings.Contains(resp, "Max") {
			verdict = "correct"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "judge-model",
			"choices": []any{map[string]any{"message": map[string]any{"content": verdict}}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 1}})
	}))
}

// judgedFixture is a judged experiment of suite qa: four sealed runs of task
// q1, each with an answer and a judge grade, and the suite folder.
func judgedFixture(t *testing.T) (*app, *schemas.Manifest, *schemas.Suite) {
	t.Helper()
	root := t.TempDir()
	runsRoot := filepath.Join(root, "runs")
	t.Cleanup(func() { writable(t, runsRoot) })
	st, err := store.NewFSStore(runsRoot, schemas.NoopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	suiteRoot := filepath.Join(root, "benchmarks", "qa")
	taskDir := filepath.Join(suiteRoot, "tasks", "q1")
	if err := os.MkdirAll(filepath.Join(taskDir, "verifier"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(suiteRoot, "suite.yaml"), []byte("id: qa\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "verifier", "answer.yaml"), []byte("question: Dog's name?\nexpected: [Max]\nwrong_answers: [Rex]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &schemas.Suite{ID: "qa", Version: "1.0.0", Root: suiteRoot, Grading: schemas.GradingJudged, Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer},
		Judge: &schemas.JudgeSpec{Provider: "openai", Model: "judge-model", PromptVersion: graders.PromptQAv1, Votes: 1},
		Tasks: []*schemas.Task{{ID: "q1", Dir: taskDir, Verifier: schemas.TaskVerifier{Tests: "verifier"}}}, TaskIDs: []string{"q1"}}
	tmpls, _ := graders.PromptTemplates(graders.PromptQAv1)
	m := &schemas.Manifest{ExpID: "e_jc", SuiteID: "qa", Hash: "sha256:m", Arms: map[string]schemas.ArmManifest{"A": {Label: "a"}, "B": {Label: "b"}},
		Scenario:   map[string]string{"suite.judge": "h", "suite.grading": "g"},
		Experiment: schemas.Experiment{Primary: schemas.Endpoint{Condition: schemas.ConditionClean, Metric: verdict.MetricVerifiedSuccess}}}
	answers := map[string]string{"s_t1_a": "It is Rex.", "s_t1_b": "Your dog is Max.", "s_t2_a": "Rex", "s_t2_b": "Max!"}
	for trial, task := range []string{"t1", "t2"} {
		for _, arm := range []string{"A", "B"} {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: "s_" + task + "_" + strings.ToLower(arm), PairID: "p_" + task, Task: "q1",
				TaskVersion: "1.0.0", Arm: arm, Condition: schemas.ConditionClean, Trial: trial + 1, Counted: true})
		}
	}
	ctx := context.Background()
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	for i := range m.Slots {
		sl := m.Slots[i]
		sl.ExpID = m.ExpID
		ans := answers[sl.SlotID]
		pass := strings.Contains(ans, "Max")
		vote, outcome, v := "incorrect", schemas.OutcomeFail, 0
		if pass {
			vote, outcome, v = "correct", schemas.OutcomePass, 1
		}
		r := &schemas.Run{RunID: "r_" + strings.ReplaceAll(strings.TrimPrefix(sl.SlotID, "s_"), "_", ""), ExpID: m.ExpID, SlotID: sl.SlotID, PairID: sl.PairID,
			Attempt: 1, Stage: schemas.StageSeal, ManifestHash: m.Hash, Outcome: outcome}
		if _, err := st.NewRun(ctx, &sl, r); err != nil {
			t.Fatal(err)
		}
		g := schemas.Grade{Passed: pass, Judged: true, Mode: "answer", Judge: &schemas.JudgeRecord{Model: "judge-model", PromptVersion: graders.PromptQAv1,
			PromptSHA256: tmpls["qa-v1-answer"], Votes: []string{vote}, USD: schemas.Observed(0.001, schemas.EvidenceGateway), ServedModel: "judge-model"}}
		gb, _ := json.Marshal(g)
		for name, data := range map[string][]byte{
			"metrics.json": []byte(fmt.Sprintf(`{"verified_success":{"state":"observed","v":%d,"evidence":"outcome"}}`, v)),
			"grade.json":   gb, graders.AnswerFile: []byte(ans),
		} {
			if err := st.WriteRunFile(ctx, r, name, data); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := st.Seal(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	a := &app{cfg: &lib.Config{Root: root, BenchmarksRoot: "benchmarks"}, logger: schemas.NoopLogger{}, store: st,
		reg: &schemas.Registry{Policies: verdict.Builtin(), Reporters: reporters.Builtin()}}
	return a, m, s
}

func TestJudgeCalibrationLoop(t *testing.T) {
	ctx := context.Background()
	a, m, s := judgedFixture(t)

	// No record: the instrument says so and the report has the section.
	an, report, err := a.analyze(ctx, m.ExpID, "")
	if err != nil {
		t.Fatal(err)
	}
	if an.JudgeCalibration == nil || an.JudgeCalibration.State != schemas.CalibrationStateUncalibrated || an.JudgeCalibration.JudgedRuns != 4 {
		t.Fatalf("uncalibrated: %+v", an.JudgeCalibration)
	}
	if an.Verdict.Decision == schemas.DecisionImproved || an.Verdict.Decision == schemas.DecisionRegressed {
		t.Fatalf("an uncalibrated judge cannot decide: %s", an.Verdict.Decision)
	}
	if !strings.Contains(string(report), "## Judge calibration") || !strings.Contains(string(report), "judge not calibrated") {
		t.Fatalf("report lacks the section:\n%s", report)
	}

	// Human reviews become labels: one pass agreeing with the judge, one
	// pass disagreeing (contested), one flag (ignored).
	for _, rq := range []schemas.ReviewRequest{
		{RunID: "r_t1b", Decision: schemas.ReviewPass, Reason: "names the dog", Reviewer: "ana"},
		{RunID: "r_t1a", Decision: schemas.ReviewPass, Reason: "Rex is the dog's nickname", Reviewer: "bo"},
		{RunID: "r_t2a", Decision: schemas.ReviewFlagOnly, Reason: "look", Reviewer: "bo"},
	} {
		if _, err := a.store.AddReview(ctx, m.ExpID, &rq); err != nil {
			t.Fatal(err)
		}
	}
	items, skipped, err := reviewLabels(ctx, a.store, m.ExpID, s)
	if err != nil || len(items) != 2 || len(skipped) != 0 {
		t.Fatalf("review labels %+v skipped %v err %v", items, skipped, err)
	}
	if items[0].Source != schemas.CalibrationSourceReview || !strings.HasPrefix(items[0].Ref, "rv_") || items[0].Labeller == "" {
		t.Fatalf("review item %+v", items[0])
	}
	if _, err := calibration.Append(s.Root, append(items, seedLabels(s)...)); err != nil {
		t.Fatal(err)
	}
	q, err := a.store.ReviewQueue(ctx, m.ExpID, schemas.ReviewReasonContested)
	if err != nil || len(q.Items) != 1 || q.Items[0].RunID != "r_t1a" {
		t.Fatalf("contested queue %+v %v", q, err)
	}

	// Calibrate: without -yes and without a terminal nothing is spent.
	var out strings.Builder
	gw := &calGateway{}
	srv := fakeChat()
	defer srv.Close()
	cfg := func() (graders.JudgeConfig, error) {
		return graders.JudgeConfig{Gateway: gw, URL: srv.URL, PriceRef: "snap"}, nil
	}
	th := calibration.Thresholds(nil, "")
	rec, err := calibrateSuite(ctx, nil, s, calibrateOptions{thresh: th, judgeCfg: cfg}, strings.NewReader(""), &out)
	if err != nil || rec != nil || gw.minted != 0 || !strings.Contains(out.String(), "not run") || !strings.Contains(out.String(), "forecast: 24 judge calls") {
		t.Fatalf("unconsented calibration: rec %v err %v minted %d\n%s", rec, err, gw.minted, out.String())
	}
	out.Reset()
	rec, err = calibrateSuite(ctx, nil, s, calibrateOptions{yes: true, thresh: th, judgeCfg: cfg}, nil, &out)
	if err != nil || rec == nil || gw.minted != 1 || gw.revoked != 1 {
		t.Fatalf("calibration: %v minted %d\n%s", err, gw.minted, out.String())
	}
	// Two human items: "Your dog is Max." correct (judge agrees), "It is Rex."
	// labelled correct by bo (judge says incorrect); seeds Max and Rex agree,
	// and so do the two adversarial controls (injection, refusal: incorrect).
	if rec.HumanLabels != 2 || rec.SeedItems != 4 || rec.Human.TP != 1 || rec.Human.FN != 1 || rec.Seed.TP != 1 || rec.Seed.TN != 3 {
		t.Fatalf("record %+v human %+v", rec, rec.Human)
	}
	recs, err := calibration.ReadRecords(s.Root)
	if err != nil || !recs.Verified || len(recs.Valid) != 1 {
		t.Fatalf("records %+v %v", recs, err)
	}

	// With a record of 2 human labels: few labels, bounds fail, still capped.
	an, report, err = a.analyze(ctx, m.ExpID, "")
	if err != nil {
		t.Fatal(err)
	}
	jc := an.JudgeCalibration
	if jc.State != schemas.CalibrationStateFewLabels || jc.RecordID != rec.RecordID || jc.ContestedRuns != 1 || len(jc.BiasAdjusted) != 2 {
		t.Fatalf("after calibration: %+v", jc)
	}
	if !strings.Contains(string(report), rec.RecordID) {
		t.Fatalf("report lacks the record id:\n%s", report)
	}

	// judge status reads the same files.
	st, err := (judgeSvc{a: a}).status(s, th)
	if err != nil || st.State != schemas.CalibrationStateFewLabels || st.Latest == nil || st.Records != 1 {
		t.Fatalf("status %+v %v", st, err)
	}
	var sb strings.Builder
	writeStatus(&sb, st)
	if !strings.Contains(sb.String(), "few_labels") || !strings.Contains(sb.String(), rec.RecordID) {
		t.Fatalf("status text:\n%s", sb.String())
	}
}

func TestJudgeCalibrationLeavesVerifiedAnalysesAlone(t *testing.T) {
	a, m := analysisFixture(t)
	an, report, err := a.analyze(context.Background(), m.ExpID, "")
	if err != nil {
		t.Fatal(err)
	}
	if an.JudgeCalibration != nil || strings.Contains(string(report), "Judge calibration") {
		t.Fatal("a verified experiment must not get a judge calibration")
	}
	for _, c := range an.Verdict.Instruments {
		if c.Name == calibration.InstrumentName {
			t.Fatal("no judge_calibration instrument on a verified experiment")
		}
	}
}

func TestRunJudgeUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}, {"calibrate"}, {"calibrate", "qa", "-from-reviews"}, {"label"}} {
		if code := runJudge(args); code != exitUsage {
			t.Fatalf("judge %v = %d, want usage", args, code)
		}
	}
}

func TestThresholdsFromPolicyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "p.yaml")
	body := "policy_version: 1\nname: jc\nchecks:\n  - {id: c, metric: verified_success, rule: no_regression}\njudge_calibration:\n  min_agreement: 0.8\n  min_labels: 50\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := thresholdsFrom(p)
	if err != nil || th.MinAgreement != 0.8 || th.MinTNR != calibration.DefaultMinTNR || th.MinLabels != 50 || th.Source != p {
		t.Fatalf("thresholds %+v %v", th, err)
	}
	if err := os.WriteFile(p, []byte(body+"  min_kappa: 0.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := thresholdsFrom(p); err == nil {
		t.Fatal("an unknown judge_calibration key must be refused")
	}
	if d, err := thresholdsFrom(""); err != nil || d.Source != "defaults" {
		t.Fatalf("defaults %+v %v", d, err)
	}
}

func TestExperimentAllowsUncalibratedJudgeFlag(t *testing.T) {
	src, err := os.ReadFile("../../examples/acme-memory/experiments/memory-smoke.yaml")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(src), "verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}",
		"verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30, allow_uncalibrated_judge: true}", 1)
	p := filepath.Join(t.TempDir(), "exp.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	exp, err := loadExperiment(p)
	if err != nil || !exp.Verdict.AllowUncalibratedJudge {
		t.Fatalf("allow_uncalibrated_judge not read: %+v %v", exp, err)
	}
}
