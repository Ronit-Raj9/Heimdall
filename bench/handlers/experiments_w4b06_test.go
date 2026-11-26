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
