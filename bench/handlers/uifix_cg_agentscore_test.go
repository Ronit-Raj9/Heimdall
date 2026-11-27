package handlers

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

type cgScoreIndex struct {
	ReadIndex
	runs map[string][]RunView
}

func (x cgScoreIndex) Experiments() []string {
	out := []string{}
	for k := range x.runs {
		out = append(out, k)
	}
	return out
}

func (x cgScoreIndex) Runs(exp string) []RunView { return x.runs[exp] }

func cgRun(agent, hash string, outcome schemas.Outcome, at time.Time, sealed bool) RunView {
	var v RunView
	v.Sealed = sealed
	v.Run.Agent.ConfigID, v.Run.Agent.ConfigHash = agent, hash
	v.Run.Outcome = outcome
	v.Run.Timing.Started = at
	return v
}

// TestAgentVersionsAndLastScore: versions counts config hashes; last_score
// is the pass share of the agent's scored runs in the evaluation of its
// latest scored run; an agent with no scored run gets none.
func TestAgentVersionsAndLastScore(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	h := &AgentsHandler{index: cgScoreIndex{runs: map[string][]RunView{
		"e_old": {cgRun("a", "h1", schemas.OutcomePass, t0, true)},
		"e_new": {
			cgRun("a", "h2", schemas.OutcomePass, t0.Add(48*time.Hour), true),
			cgRun("a", "h2", schemas.OutcomeFail, t0.Add(49*time.Hour), true),
			cgRun("a", "h2", schemas.OutcomeFail, t0.Add(50*time.Hour), false), // not sealed
			cgRun("b", "h9", schemas.OutcomePass, t0, false),
		},
	}}}
	counts := h.sealedRuns()
	a := summarizeAgent(AgentConfigFile{Path: "agents/a.yaml", Config: &schemas.AgentConfig{ID: "a"}}, counts)
	if a.Versions == nil || *a.Versions != 2 {
		t.Fatalf("versions %v", a.Versions)
	}
	if a.LastScore == nil || a.LastScore.ExpID != "e_new" || a.LastScore.Runs != 2 || a.LastScore.Score != 0.5 {
		t.Fatalf("last score %+v", a.LastScore)
	}
	b := summarizeAgent(AgentConfigFile{Path: "agents/b.yaml", Config: &schemas.AgentConfig{ID: "b"}}, counts)
	if b.LastScore != nil || b.Versions == nil || *b.Versions != 1 {
		t.Fatalf("b %+v %v", b.LastScore, b.Versions)
	}
}
