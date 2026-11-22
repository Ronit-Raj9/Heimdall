package engine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Y03 (G1 P1-2): a deployed agent's endpoint that is down (refused, reset,
// DNS or TLS failure, 502, 503 or 504 after the launcher's retries) is the
// customer's infrastructure, never the agent's answer. The launcher
// (adapters/remotehttp.py, adapters/openaiagent.py) prints one
// endpoint_unreachable line and exits endpointUnreachableExit; the run is
// then sealed as aborted_infra when the endpoint never answered (the slot
// gets a new run, up to maxAttempts) or infra_error when it answered some
// turns first. Neither is ever scored.

// endpointUnreachableExit is the launchers' EXIT_UNREACHABLE (EX_TEMPFAIL).
const endpointUnreachableExit = 75

// endpointPart is the part a run's endpoint failure is recorded under.
const endpointPart = "agent endpoint"

// endpointLine is the launcher's endpoint_unreachable line.
type endpointLine struct {
	Type     string `json:"type"`
	Reason   string `json:"reason"`
	Attempts int    `json:"attempts"`
	Answered int    `json:"answered"`
}

// endpointUnreachable reads the launcher's endpoint_unreachable line when
// the run is a remote endpoint's and its launcher exited with
// endpointUnreachableExit. Both must hold: the exit code is the launcher's
// own, and the line is printed by the launcher, never the agent.
func endpointUnreachable(rc *schemas.RunContext) (*endpointLine, bool) {
	if rc == nil || rc.Agent == nil || rc.Exec == nil || rc.Exec.ExitCode != endpointUnreachableExit || rc.Exec.StdoutPath == "" {
		return nil, false
	}
	if rc.Agent.Remote == nil && !utils.IsOpenAIAgent(rc.Agent) {
		return nil, false
	}
	f, err := os.Open(rc.Exec.StdoutPath)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var found *endpointLine
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"endpoint_unreachable"`) {
			continue
		}
		var l endpointLine
		if json.Unmarshal(line, &l) == nil && l.Type == "endpoint_unreachable" {
			found = &l
		}
	}
	return found, found != nil
}

// endpointOutcome turns a run whose endpoint was unreachable into
// infrastructure. Only an outcome that would otherwise count against the
// agent changes: a pass, a fired veto or an evaluator problem stays.
func endpointOutcome(rc *schemas.RunContext, o schemas.Outcome) (schemas.Outcome, *schemas.PartFailure) {
	switch o {
	case schemas.OutcomeFail, schemas.OutcomeTimeout, schemas.OutcomeCrash, schemas.OutcomeInfraError:
	default:
		return o, nil
	}
	l, ok := endpointUnreachable(rc)
	if !ok {
		return o, nil
	}
	out := schemas.OutcomeAbortedInfra
	how := "never answered, so the slot gets a new run"
	if l.Answered > 0 {
		out = schemas.OutcomeInfraError
		how = fmt.Sprintf("answered %d calls first", l.Answered)
	}
	reason := strings.TrimSpace(l.Reason)
	if len(reason) > 200 {
		reason = reason[:200]
	}
	pf := &schemas.PartFailure{Stage: schemas.StageRun, Part: endpointPart,
		Error: fmt.Sprintf("the agent's endpoint did not answer after %d attempts (%s) and %s; excluded from scores as infrastructure", l.Attempts, reason, how)}
	return out, pf
}
