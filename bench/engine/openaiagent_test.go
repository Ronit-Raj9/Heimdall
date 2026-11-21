package engine

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestOpenAITaskErrs(t *testing.T) {
	arm := func(o *schemas.OpenAIAgent) map[string]schemas.ArmManifest {
		return map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "x", Adapter: "openai_agent", OpenAI: o}}}
	}
	ok := &schemas.OpenAIAgent{URL: "https://agent.example.com/v1"}
	suite := &schemas.Suite{ID: "s"}
	answer := &schemas.Task{ID: "t1", Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}}
	diff := &schemas.Task{ID: "t2"}
	state := &schemas.Task{ID: "t3", Output: &schemas.OutputSpec{Kind: schemas.OutputState}}
	stateMCP := &schemas.Task{ID: "t4", Output: &schemas.OutputSpec{Kind: schemas.OutputState},
		Services: []schemas.ServiceSpec{{ID: "crm", Role: schemas.ServiceRoleMCP}}}
	if errs := openaiTaskErrs(suite, answer, arm(ok), nil); len(errs) != 0 {
		t.Fatalf("answer task: %v", errs)
	}
	if errs := openaiTaskErrs(suite, stateMCP, arm(ok), nil); len(errs) != 0 {
		t.Fatalf("state task with MCP: %v", errs)
	}
	for _, tc := range []struct {
		task *schemas.Task
		o    *schemas.OpenAIAgent
		want string
	}{
		{diff, ok, "cannot produce a diff output"},
		{state, ok, "no MCP tools"},
		{stateMCP, &schemas.OpenAIAgent{URL: ok.URL, Tools: "off"}, "needs openai.tools auto"},
		{answer, nil, "needs an openai block"},
		{answer, &schemas.OpenAIAgent{URL: "nope"}, "want http(s)"},
	} {
		errs := openaiTaskErrs(suite, tc.task, arm(tc.o), nil)
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), tc.want) {
			t.Errorf("task %s: %v, want %q", tc.task.ID, errs, tc.want)
		}
	}
	// Other adapters are not this check's business.
	other := map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "c", Adapter: "command"}}}
	if errs := openaiTaskErrs(suite, diff, other, nil); len(errs) != 0 {
		t.Fatalf("command arm: %v", errs)
	}
}
