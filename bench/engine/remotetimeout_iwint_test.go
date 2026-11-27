package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func remoteTimeoutRC(t *testing.T, remote bool, exit int, stream string) *schemas.RunContext {
	t.Helper()
	p := filepath.Join(t.TempDir(), "agent.stream.jsonl")
	if err := os.WriteFile(p, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &schemas.AgentConfig{ID: "deployed"}
	if remote {
		a.Remote = &schemas.RemoteAgent{}
	}
	return &schemas.RunContext{Agent: a, Exec: &schemas.ExecResult{ExitCode: exit, StdoutPath: p}}
}

// A remote agent whose launcher ran out of polls (or of time between turns)
// seals as a timeout whose first failure says the agent did not finish in
// time; it is still the agent's fail (score 0), never a graded half answer.
func TestRemoteTimeoutSaysTheAgentDidNotFinish(t *testing.T) {
	poll := `{"type": "turn", "n": 1}` + "\n" + `{"message": "incomplete answer: the agent did not finish within the time limit (30 polls)", "type": "error"}` + "\n"
	o, pf := remoteTimeoutOutcome(remoteTimeoutRC(t, true, 1, poll), schemas.OutcomeFail)
	if o != schemas.OutcomeTimeout || pf == nil {
		t.Fatalf("outcome %s, failure %+v", o, pf)
	}
	if !strings.HasPrefix(pf.Error, "The agent did not finish in time (incomplete answer: the agent did not finish within the time limit (30 polls))") ||
		pf.Part != remoteTimeoutPart || pf.Stage != schemas.StageRun {
		t.Fatalf("failure %+v", pf)
	}
	if y, ok := o.Score(); !ok || y != 0 {
		t.Fatalf("a timeout must count as the agent's fail: %v %v", y, ok)
	}
	turns := `{"message": "timed out after 2 of 5 turns", "type": "error"}` + "\n"
	if o, pf := remoteTimeoutOutcome(remoteTimeoutRC(t, true, 1, turns), schemas.OutcomeCrash); o != schemas.OutcomeTimeout || pf == nil {
		t.Fatalf("multi-turn timeout: %s %+v", o, pf)
	}
}

func TestRemoteTimeoutLeavesOtherRunsAlone(t *testing.T) {
	line := `{"message": "incomplete answer: the agent did not finish within the time limit (30 polls)", "type": "error"}` + "\n"
	for name, c := range map[string]struct {
		rc *schemas.RunContext
		o  schemas.Outcome
	}{
		"a pass":                   {remoteTimeoutRC(t, true, 1, line), schemas.OutcomePass},
		"infrastructure":           {remoteTimeoutRC(t, true, 1, line), schemas.OutcomeAbortedInfra},
		"not a remote agent":       {remoteTimeoutRC(t, false, 1, line), schemas.OutcomeFail},
		"another exit code":        {remoteTimeoutRC(t, true, 75, line), schemas.OutcomeFail},
		"another launcher error":   {remoteTimeoutRC(t, true, 1, `{"message": "the answer is not JSON", "type": "error"}`+"\n"), schemas.OutcomeFail},
		"a timeout the agent said": {remoteTimeoutRC(t, true, 1, `{"type": "message", "text": "incomplete answer: soon"}`+"\n"), schemas.OutcomeFail},
	} {
		if o, pf := remoteTimeoutOutcome(c.rc, c.o); o != c.o || pf != nil {
			t.Errorf("%s: %s %+v, want %s unchanged", name, o, pf, c.o)
		}
	}
}
