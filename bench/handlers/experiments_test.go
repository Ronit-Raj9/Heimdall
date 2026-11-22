package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestDecodeExperiment(t *testing.T) {
	tests := []struct {
		name, mediaType, body string
		status                int
		all                   bool
		ids                   int
	}{
		{"yaml", "application/yaml", "name: x\nconditions:\n  - {id: clean, tasks: all, trials: 3}\n", 200, true, 0},
		{"yaml no type", "", "name: x\nconditions:\n  - {id: clean, tasks: [t01, t02], trials: 1}\n", 200, false, 2},
		{"json", "application/json", `{"name":"x","conditions":[{"id":"clean","tasks":{"all":true},"trials":3}]}`, 200, true, 0},
		{"json ids", "application/json", `{"name":"x","conditions":[{"id":"clean","tasks":{"ids":["t01"]},"trials":3}]}`, 200, false, 1},
		{"yaml unknown field", "text/yaml", "name: x\nnope: 1\n", 400, false, 0},
		{"json unknown field", "application/json", `{"name":"x","nope":1}`, 400, false, 0},
		{"empty", "application/json", "  ", 400, false, 0},
		{"unsupported", "application/xml", "<x/>", 415, false, 0},
		{"yaml second document", "application/yaml", "name: x\n---\nname: y\n", 400, false, 0},
		{"json second object", "application/json", `{"name":"x"} {"name":"y"}`, 400, false, 0},
		{"json trailing space", "application/json", "{\"name\":\"x\",\"conditions\":[{\"id\":\"clean\",\"tasks\":{\"all\":true},\"trials\":1}]}\n\n", 200, true, 0},
		{"yaml matrix provider typo", "application/yaml", "name: x\nmatrix:\n  models: [{model: first, provdier: fake}, second]\n", 400, false, 0},
		{"json matrix provider typo", "application/json", `{"name":"x","matrix":{"models":[{"model":"first","provdier":"fake"},"second"]}}`, 400, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, status, err := DecodeExperiment(tt.mediaType, []byte(tt.body))
			if status != tt.status {
				t.Fatalf("status = %d, want %d (err %v)", status, tt.status, err)
			}
			if tt.status != 200 {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil || len(exp.Conditions) != 1 {
				t.Fatalf("exp = %+v, err %v", exp, err)
			}
			if sel := exp.Conditions[0].Tasks; sel.All != tt.all || len(sel.IDs) != tt.ids {
				t.Errorf("tasks = %+v", sel)
			}
		})
	}
}

func TestBuildPlan(t *testing.T) {
	m := testManifest()
	m.Experiment.Verdict = schemas.VerdictSpec{Alpha: 0.05, AssumedSD: 0.30}
	p := BuildPlan(m)
	if p.Slots != 4 || p.Pairs != 2 || len(p.Conditions) != 1 || !p.MDEAssumedSD || p.SD != 0.30 {
		t.Fatalf("plan = %+v", p)
	}
	// Two tasks: MDE exists and is large, so the plan warns (N40). Nothing has run: no estimates.
	if p.MDE <= 0 || len(p.Warnings) != 1 || len(p.Errors) != 0 || p.EstUSD.IsObserved() || p.EstWallS.Reason != "no pilot yet" {
		t.Errorf("plan = %+v", p)
	}
	m.Slots = m.Slots[:2] // one task left
	p = BuildPlan(m)
	if p.MDE != 0 || len(p.Warnings) != 1 {
		t.Errorf("one-task plan = %+v", p)
	}
}

func TestFailedPlan(t *testing.T) {
	p := FailedPlan(&schemas.Experiment{Verdict: schemas.VerdictSpec{AssumedSD: 0.4}}, errors.New("no arms"))
	if len(p.Errors) != 1 || p.Slots != 0 || p.SD != 0.4 || p.Conditions == nil || p.Warnings == nil || p.EstUSD.State != schemas.ValueUnknown {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPlanAndCreateRoutes(t *testing.T) {
	d := fullDeps()
	l := &fakeLauncher{}
	d.launcher = l
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "POST", "/api/bench/experiments/plan",
		[]byte(`{"name":"demo","conditions":[{"id":"clean","tasks":{"all":true},"trials":1}]}`), authed("application/json")))
	if body["slots"] != float64(4) || body["pairs"] != float64(2) || l.planned == nil || !l.planned.Conditions[0].Tasks.All {
		t.Fatalf("plan = %v", body)
	}
	resp := c.do(t, "POST", "/api/bench/experiments/plan", []byte("name: bad\n"), authed("application/yaml"))
	body = decodeBody(t, resp)
	if errs, _ := body["errors"].([]any); resp.StatusCode() != 200 || len(errs) != 1 || body["slots"] != float64(0) {
		t.Errorf("refused plan = %d %v, want 200 with one error", resp.StatusCode(), body)
	}
	if _, leaked := body["manifest"]; leaked {
		t.Errorf("plan leaks the manifest: %v", body)
	}
	if resp := c.do(t, "POST", "/api/bench/experiments/plan", []byte("nope: 1\n"), authed("application/yaml")); resp.StatusCode() != 400 {
		t.Errorf("unreadable plan body status = %d, want 400", resp.StatusCode())
	}
	if resp := c.do(t, "POST", "/api/bench/experiments", []byte("name: bad\n"), authed("application/yaml")); resp.StatusCode() != 400 {
		t.Errorf("refused create status = %d, want 400", resp.StatusCode())
	}
	resp = c.do(t, "POST", "/api/bench/experiments", []byte("name: demo\n"), authed("application/yaml"))
	body = decodeBody(t, resp)
	if resp.StatusCode() != 201 || body["exp_id"] != testExp || body["manifest_hash"] != "sha256:abc" ||
		body["created_at"] != "2026-11-14T23:41:00Z" || l.created == nil {
		t.Fatalf("create = %d %v", resp.StatusCode(), body)
	}
	resp = c.do(t, "POST", "/api/bench/experiments/e_test/start", nil, authed(""))
	body = decodeBody(t, resp)
	slots, _ := body["slots"].(map[string]any)
	if resp.StatusCode() != 202 || body["state"] != "analyzing" || slots["total"] != float64(4) || l.started != testExp {
		t.Fatalf("start = %d %v", resp.StatusCode(), body)
	}
}

// policyAnalyzer is fakeAnalyzer with a blocking release policy result.
type policyAnalyzer struct{ fakeAnalyzer }

func (p *policyAnalyzer) Analyze(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, error) {
	a, err := p.fakeAnalyzer.Analyze(ctx, expID, gradeSource)
	if err != nil {
		return nil, err
	}
	a.Policy = &schemas.PolicyResult{
		PolicyName: "enterprise-default", PolicyHash: "abc", Decision: schemas.PolicyBlock,
		Checks: []schemas.PolicyCheckResult{
			{ID: "fc", Metric: "false_completion", Condition: "all", Arm: "B", Status: schemas.PolicyFail, Blocking: true,
				Observed: schemas.Observed(0.2, schemas.EvidenceNative), Threshold: "<= 0.05", N: 10, Reason: "mean 0.2", Evidence: []string{"r_t01_B"}},
			{ID: "ra", Metric: "retry_amplification", Condition: "clean", Arm: "B", Status: schemas.PolicyUnknown, Blocking: true,
				Observed: schemas.Unknown("not recorded", ""), Threshold: "<= 3"},
		},
	}
	return a, nil
}

func TestVerdictPayloadPolicy(t *testing.T) {
	d := fullDeps()
	d.analyzer = &policyAnalyzer{}
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil))
	checkKeys(t, "VerdictPayload", body, loadKeys(t, "VerdictPayload"), true)
	pol, ok := body["policy"].(map[string]any)
	if !ok || pol["decision"] != "block" || pol["policy_name"] != "enterprise-default" {
		t.Fatalf("policy = %v", body["policy"])
	}
	checkKeys(t, "PolicyResult", pol, loadKeys(t, "PolicyResult"), false)
	for i, c := range objectsAt(t, body, "policy.checks[]") {
		checkKeys(t, fmt.Sprintf("PolicyCheckResult #%d", i), c, loadKeys(t, "PolicyCheckResult"), false)
	}
	checks, _ := pol["checks"].([]any)
	second, _ := checks[1].(map[string]any)
	observed, _ := second["observed"].(map[string]any)
	if second["status"] != "unknown" || observed["state"] != "unknown" || observed["v"] != nil {
		t.Fatalf("unknown check = %v", second)
	}

	// No policy: the key is absent, not null.
	plain := decodeBody(t, newTestClient(t, fullDeps()).do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil))
	if _, ok := plain["policy"]; ok {
		t.Fatalf("policy present without a policy: %v", plain["policy"])
	}
}

func TestVerdictPayload(t *testing.T) {
	body := decodeBody(t, newTestClient(t, fullDeps()).do(t, "GET", "/api/bench/experiments/e_test/verdict?grade_source=rg_1", nil, nil))
	verdict, _ := body["verdict"].(map[string]any)
	gates, _ := body["gates"].([]any)
	diff, _ := body["config_diff"].([]any)
	if body["grade_source"] != "rg_1" || body["suite"] != "coding-core@1.0.0" || verdict["decision"] != "improved" ||
		len(gates) != 1 || len(diff) != 1 {
		t.Fatalf("verdict = %v", body)
	}
}

// manifestWithTasks is testManifest with n tasks on two arms.
func manifestWithTasks(n int) *schemas.Manifest {
	m := testManifest()
	m.Slots = nil
	for i := 0; i < n; i++ {
		task := fmt.Sprintf("t%03d", i)
		for _, arm := range []string{"A", "B"} {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: "s_" + task + "_" + arm, PairID: "p_" + task, ExpID: testExp,
				Task: task, Arm: arm, Condition: "clean", Trial: 1, Counted: true})
		}
	}
	return m
}

// A plan that can only detect a large change says so, with the task count
// that would detect 10 points, instead of "No errors and no warnings" (N40).
func TestBuildPlanWarnsUnderpowered(t *testing.T) {
	m := manifestWithTasks(5)
	m.Experiment.Verdict = schemas.VerdictSpec{Alpha: 0.05, AssumedSD: 0.30}
	p := BuildPlan(m)
	if len(p.Warnings) != 1 || len(p.Errors) != 0 {
		t.Fatalf("5 tasks: warnings %q errors %q, want one warning", p.Warnings, p.Errors)
	}
	w := p.Warnings[0]
	if !strings.Contains(w, "With 5 tasks this comparison can only detect a change of about ") || !strings.Contains(w, " tasks are needed to detect 10 points.") {
		t.Fatalf("warning %q", w)
	}
	if !strings.Contains(w, fmt.Sprintf("about %.1f points", p.MDE)) || p.MDE < 40 || p.MDE > 60 {
		t.Fatalf("warning %q does not name the MDE %.1f", w, p.MDE)
	}
	m = manifestWithTasks(200)
	m.Experiment.Verdict = schemas.VerdictSpec{Alpha: 0.05, AssumedSD: 0.30}
	if p := BuildPlan(m); len(p.Warnings) != 0 {
		t.Fatalf("200 tasks: warnings %q", p.Warnings)
	}
}

// FailedPlan shows the plain sentence, not "plan experiment: plan <id>: ..."
// (N42, N44).
func TestFailedPlanUsesPlainText(t *testing.T) {
	p := FailedPlan(&schemas.Experiment{}, errors.New("plan e_zz_test_ab_fl4n: suite lock: task file-inbox is draft, not admitted"))
	if len(p.Errors) != 1 {
		t.Fatalf("errors %q", p.Errors)
	}
	if e := p.Errors[0]; strings.Contains(e, "plan experiment") || strings.Contains(e, "e_zz_test_ab_fl4n") || !strings.Contains(e, "file-inbox") ||
		!strings.HasPrefix(e, "This benchmark has tasks that have not passed the readiness check yet") {
		t.Fatalf("error %q", e)
	}
}
