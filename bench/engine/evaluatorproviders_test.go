package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestEvaluatorProvidersInUpstreamCheck guards 3-C-06: providers used only
// by judges, the auditor, vision judges and model-backed services are
// checked, also when no arm calls a provider.
func TestEvaluatorProvidersInUpstreamCheck(t *testing.T) {
	suite := &schemas.Suite{
		Grading: schemas.GradingJudged,
		Judge:   &schemas.JudgeSpec{Provider: "openai", Model: "j", Auditor: &schemas.JudgeAuditorSpec{Provider: "gemini", Model: "a"}},
		Tasks: []*schemas.Task{
			{ID: "t01", Services: []schemas.ServiceSpec{{ID: "crm", Gateway: &schemas.ServiceGateway{Provider: "mistral", Model: "m"}}},
				Output: &schemas.OutputSpec{Visual: &schemas.VisualCheck{Rubric: "r.md", Judge: &schemas.VisualJudgeSpec{Provider: "vertex", Model: "v"}}}},
			{ID: "t02", Services: []schemas.ServiceSpec{{ID: "unused", Gateway: &schemas.ServiceGateway{Provider: "cohere", Model: "c"}}}},
		},
	}
	all := []condPlan{{id: "clean", tasks: []string{"t01"}}}
	got := evaluatorProviders(&schemas.Experiment{}, suite, all)
	want := map[string][]string{"openai": {"judge"}, "gemini": {"judge auditor"}, "vertex": {"vision judge (task t01)"}, "mistral": {"service crm (task t01)"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := evaluatorProviders(&schemas.Experiment{Smoke: true}, suite, all); len(got) != 0 {
		t.Fatalf("no-paid experiment: got %v, want none", got)
	}

	// An all-scripted arm set (no arm provider) with a live judge still checks.
	checker := &fakeChecker{statuses: []UpstreamStatus{{Provider: "openai", Host: "api.openai.com:443", Error: "down"}}}
	p := &Planner{upstreams: checker, logger: schemas.NoopLogger{}}
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{}}
	err := p.checkUpstreams(context.Background(), m, got)
	if checker.calls != 1 || err == nil || !strings.Contains(err.Error(), "used by judge") {
		t.Fatalf("calls %d err %v", checker.calls, err)
	}
}
