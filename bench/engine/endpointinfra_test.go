package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestEndpointOutcome(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	never := write("never.jsonl", `{"type": "request"}`+"\n"+`{"attempts": 4, "reason": "HTTP 502", "type": "endpoint_unreachable"}`+"\n")
	some := write("some.jsonl", `{"answered": 3, "attempts": 4, "reason": "no response", "type": "endpoint_unreachable"}`+"\n")
	plain := write("plain.jsonl", `{"type": "error", "message": "the agent answered HTTP 500"}`+"\n")
	remote := &schemas.AgentConfig{Adapter: "remote_http", Remote: &schemas.RemoteAgent{URL: "https://a.example.com"}}
	rc := func(a *schemas.AgentConfig, code int, out string) *schemas.RunContext {
		return &schemas.RunContext{Agent: a, Exec: &schemas.ExecResult{ExitCode: code, StdoutPath: out}}
	}
	for name, c := range map[string]struct {
		rc   *schemas.RunContext
		in   schemas.Outcome
		want schemas.Outcome
		pf   bool
	}{
		"never answered":         {rc(remote, 75, never), schemas.OutcomeFail, schemas.OutcomeAbortedInfra, true},
		"answered some first":    {rc(remote, 75, some), schemas.OutcomeFail, schemas.OutcomeInfraError, true},
		"pass stays":             {rc(remote, 75, never), schemas.OutcomePass, schemas.OutcomePass, false},
		"veto stays":             {rc(remote, 75, never), schemas.OutcomeIntegrityViolation, schemas.OutcomeIntegrityViolation, false},
		"other exit code":        {rc(remote, 1, never), schemas.OutcomeFail, schemas.OutcomeFail, false},
		"no line":                {rc(remote, 75, plain), schemas.OutcomeFail, schemas.OutcomeFail, false},
		"in-container agent":     {rc(&schemas.AgentConfig{Adapter: "command"}, 75, never), schemas.OutcomeFail, schemas.OutcomeFail, false},
		"openai-compatible, too": {rc(&schemas.AgentConfig{Adapter: "openai_agent", OpenAI: &schemas.OpenAIAgent{}}, 75, never), schemas.OutcomeTimeout, schemas.OutcomeAbortedInfra, true},
	} {
		got, pf := endpointOutcome(c.rc, c.in)
		if got != c.want || (pf != nil) != c.pf {
			t.Errorf("%s: %s %v, want %s %t", name, got, pf, c.want, c.pf)
		}
		if pf != nil && (pf.Part != endpointPart || !strings.Contains(pf.Error, "excluded from scores")) {
			t.Errorf("%s: failure %+v", name, pf)
		}
	}
}
