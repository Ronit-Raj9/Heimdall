package handlers

import (
	"errors"
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
	// Two tasks: MDE exists and is large. Nothing has run: no estimates.
	if p.MDE <= 0 || len(p.Warnings) != 0 || len(p.Errors) != 0 || p.EstUSD.IsObserved() || p.EstWallS.Reason != "no pilot yet" {
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
