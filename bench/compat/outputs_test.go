package compat

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func outputSuite(id string, kinds ...string) *schemas.Suite {
	s := &schemas.Suite{ID: id, Track: "support", Services: []schemas.ServiceSpec{{ID: "crm", Role: schemas.ServiceRoleMCP}}}
	for i, k := range kinds {
		t := &schemas.Task{ID: id + "-" + string(rune('a'+i)), Output: &schemas.OutputSpec{Kind: k}}
		s.Tasks = append(s.Tasks, t)
		s.TaskIDs = append(s.TaskIDs, t.ID)
	}
	return s
}

func TestFullRemoteToolsOff(t *testing.T) {
	remote := &schemas.AgentConfig{ID: "bot", Adapter: "remote_http", Remote: &schemas.RemoteAgent{URL: "https://x"}}
	state := outputSuite("tau", schemas.OutputState, schemas.OutputState)
	ok, _, reason, _ := Full(remote, state)
	if ok || !strings.Contains(reason, "turn on tool access (remote.tools: expose)") || !strings.HasPrefix(reason, "Agent bot cannot run benchmark tau:") {
		t.Fatalf("tools off on state: %v %q", ok, reason)
	}
	// Check alone (the planner's capability rule) still says ok.
	if ok, _, _ := Check(remote, state); !ok {
		t.Fatal("Check must stay capabilities only")
	}
	remote.Remote.Tools = schemas.RemoteToolsExpose
	if ok, _, reason, note := Full(remote, state); !ok || reason != "" || note != "" {
		t.Fatalf("tools exposed: %v %q %q", ok, reason, note)
	}
}

func TestFullPartialNote(t *testing.T) {
	remote := &schemas.AgentConfig{ID: "bot", Adapter: "remote_http"}
	mixed := outputSuite("mix", schemas.OutputAnswer, schemas.OutputState, schemas.OutputAnswer)
	ok, _, reason, note := Full(remote, mixed)
	if !ok || reason != "" || !strings.HasPrefix(note, "1 of 3 tasks are skipped:") {
		t.Fatalf("mixed: %v %q %q", ok, reason, note)
	}
	run, why := RunnableTasks(remote, mixed, mixed.TaskIDs)
	if len(run) != 2 || why == "" {
		t.Fatalf("runnable %v %q", run, why)
	}
}

func TestTaskOKKinds(t *testing.T) {
	a2a := &schemas.AgentConfig{ID: "a", Adapter: "a2a", A2A: &schemas.A2AAgent{CardURL: "https://x"}}
	oa := &schemas.AgentConfig{ID: "o", Adapter: "openai_agent", OpenAI: &schemas.OpenAIAgent{URL: "https://x/v1"}}
	cmd := &schemas.AgentConfig{ID: "c", Adapter: "command"}
	diff := outputSuite("d", schemas.OutputDiff)
	art := outputSuite("r", schemas.OutputArtifact)
	state := outputSuite("s", schemas.OutputState)
	noTools := &schemas.Suite{ID: "n", Tasks: []*schemas.Task{{ID: "n1", Output: &schemas.OutputSpec{Kind: schemas.OutputState}}}}

	cases := []struct {
		name  string
		agent *schemas.AgentConfig
		s     *schemas.Suite
		ok    bool
		want  string
	}{
		{"command diff", cmd, diff, true, ""},
		{"a2a diff", a2a, diff, false, "code workspace"},
		{"a2a artifact", a2a, art, false, "a2a.artifacts: inline"},
		{"a2a state", a2a, state, false, "a2a.tools: expose"},
		{"openai state mcp", oa, state, true, ""},
		{"openai state no mcp", oa, noTools, false, "no MCP tools"},
		{"openai artifact", oa, art, false, "OpenAI-compatible endpoint runs outside"},
	}
	for _, c := range cases {
		ok, why := TaskOK(c.agent, c.s, c.s.Tasks[0])
		if ok != c.ok || !strings.Contains(why, c.want) {
			t.Errorf("%s: %v %q", c.name, ok, why)
		}
	}
	oa.OpenAI.Tools = schemas.OpenAIToolsOff
	if ok, why := TaskOK(oa, state, state.Tasks[0]); ok || !strings.Contains(why, "openai.tools: off") {
		t.Fatalf("openai tools off: %v %q", ok, why)
	}
	exposed := &schemas.AgentConfig{ID: "e", Adapter: "remote_http", Remote: &schemas.RemoteAgent{Tools: schemas.RemoteToolsExpose}}
	if ok, why := TaskOK(exposed, noTools, noTools.Tasks[0]); ok || !strings.Contains(why, "no tool your endpoint could reach") {
		t.Fatalf("nothing to publish: %v %q", ok, why)
	}
}
