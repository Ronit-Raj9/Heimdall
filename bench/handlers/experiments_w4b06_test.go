package handlers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// slowPlanLauncher is fakeLauncher whose planner always runs out of time.
type slowPlanLauncher struct{ fakeLauncher }

func (*slowPlanLauncher) Plan(context.Context, *schemas.Experiment) (*schemas.Manifest, error) {
	return nil, fmt.Errorf("plan demo: load suite: %w", context.DeadlineExceeded)
}

// TestPlanDeadlineAnswers503 checks a plan that runs out of time answers 503
// with Retry-After and a plain sentence, as the estimate does.
func TestPlanDeadlineAnswers503(t *testing.T) {
	d := fullDeps()
	d.launcher = &slowPlanLauncher{}
	c := newTestClient(t, d)
	resp := c.do(t, "POST", "/api/bench/experiments/plan", []byte(`{"name":"demo"}`), authed("application/json"))
	if resp.StatusCode() != 503 {
		t.Fatalf("status %d, want 503; body %s", resp.StatusCode(), resp.Body())
	}
	if got := string(resp.Header.Peek("Retry-After")); got != "5" {
		t.Fatalf("Retry-After %q, want 5", got)
	}
	if !strings.Contains(string(resp.Body()), "Still reading this benchmark's tasks. Trying again in a few seconds.") {
		t.Fatalf("body %s", resp.Body())
	}
}

// TestVerdictDefaultsToNewestRegrade checks the verdict route without
// grade_source reads the newest re-score, and grade_source=original still
// reads the original grading.
func TestVerdictDefaultsToNewestRegrade(t *testing.T) {
	d := fullDeps()
	d.index = regradeIndex{testIndex()}
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil))
	if body["grade_source"] != "rg_1" {
		t.Fatalf("default grade_source = %v, want rg_1", body["grade_source"])
	}
	body = decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test/verdict?grade_source=original", nil, nil))
	if body["grade_source"] != "original" {
		t.Fatalf("explicit original grade_source = %v", body["grade_source"])
	}
	plain := decodeBody(t, newTestClient(t, fullDeps()).do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil))
	if plain["grade_source"] != "original" {
		t.Fatalf("without re-scores grade_source = %v, want original", plain["grade_source"])
	}
}
